package processor

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/acidghost/k8s-fs-sidecar/internal/config"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type FileState struct {
	Folder string
	Files  map[string][]byte
}

// Processor owns the per-resource file state for a sidecar instance. Binding
// config and state to a struct (rather than package-level globals) keeps each
// instance self-contained, lets tests construct a fresh one without resetting
// shared state, and gives concurrent access a natural home for a mutex.
type Processor struct {
	cfg   *config.Config
	mu    sync.Mutex
	state map[string]*FileState
}

func NewProcessor(cfg *config.Config) *Processor {
	return &Processor{cfg: cfg, state: make(map[string]*FileState)}
}

// safePath joins folder and filename and verifies the result does not escape
// the destination folder. This is defense-in-depth: Kubernetes validates that
// ConfigMap/Secret keys cannot contain path separators, so traversal is not
// reachable in practice, but we do not rely on upstream validation for security.
func safePath(folder, filename string) (string, error) {
	folder = filepath.Clean(folder)
	fullPath := filepath.Join(folder, filename)
	rel, err := filepath.Rel(folder, fullPath)
	if err != nil {
		return "", fmt.Errorf("invalid path for %q: %w", filename, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes destination folder %q", filename, folder)
	}
	return fullPath, nil
}

func GetResourceKey(obj *metav1.ObjectMeta, resourceType string) string {
	return fmt.Sprintf("%s/%s/%s", resourceType, obj.Namespace, obj.Name)
}

func GetDestinationFolder(obj *metav1.ObjectMeta, defaultFolder, annotation string) string {
	if obj.Annotations != nil {
		if val, ok := obj.Annotations[annotation]; ok {
			if filepath.IsAbs(val) {
				return val
			}
			return filepath.Join(defaultFolder, val)
		}
	}
	return defaultFolder
}

func (p *Processor) WriteFile(folder, filename string, data []byte) error {
	fullPath, err := safePath(folder, filename)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), p.cfg.DirMode); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", filepath.Dir(fullPath), err)
	}

	if _, err := os.Stat(fullPath); err == nil {
		existingData, err := os.ReadFile(fullPath) //nolint:gosec // fullPath is validated by safePath above
		if err == nil {
			existingHash := sha256.Sum256(existingData)
			newHash := sha256.Sum256(data)
			if existingHash == newHash {
				return nil
			}
		}
	}

	if err := p.writeFileAtomic(fullPath, data); err != nil {
		return err
	}

	log.Info().Str("file", fullPath).Msg("File written")
	return nil
}

// writeFileAtomic writes data to a temp file in the same directory as fullPath,
// fsyncs it, then renames it over the target. The rename is atomic on POSIX
// filesystems, so a consuming container never observes a partially-written
// file during an update. The temp file lives in the target's directory (not
// /tmp) so the rename stays within one filesystem and is genuinely atomic.
func (p *Processor) writeFileAtomic(fullPath string, data []byte) error {
	dir := filepath.Dir(fullPath)

	tmp, err := os.CreateTemp(dir, ".k8s-fs-sidecar-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()

	// cleanup closes the fd and removes the temp file. It's best-effort: a
	// no-op once the rename below has succeeded, and tolerant of close/remove
	// failures since there is nothing useful to do with them on an error path.
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("failed to write temp file %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(p.cfg.FileMode); err != nil {
		cleanup()
		return fmt.Errorf("failed to chmod temp file %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("failed to sync temp file %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("failed to close temp file %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, fullPath); err != nil {
		cleanup()
		return fmt.Errorf("failed to rename %s to %s: %w", tmpName, fullPath, err)
	}
	return nil
}

func DeleteFile(folder, filename string) error {
	fullPath, err := safePath(folder, filename)
	if err != nil {
		return err
	}
	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete file %s: %w", fullPath, err)
	}
	log.Info().Str("file", fullPath).Msg("File deleted")
	return nil
}

func (p *Processor) ProcessConfigMap(cm *corev1.ConfigMap, eventType string) error {
	key := GetResourceKey(&cm.ObjectMeta, "configmap")

	if eventType == "DELETED" {
		return p.handleDeletedResource(key)
	}

	folder := GetDestinationFolder(&cm.ObjectMeta, p.cfg.Folder, p.cfg.FolderAnnotation)
	state := &FileState{Folder: folder, Files: make(map[string][]byte)}

	for k, v := range cm.Data {
		state.Files[k] = []byte(v)
	}

	maps.Copy(state.Files, cm.BinaryData)

	return p.updateResource(key, state)
}

func (p *Processor) ProcessSecret(secret *corev1.Secret, eventType string) error {
	key := GetResourceKey(&secret.ObjectMeta, "secret")

	if eventType == "DELETED" {
		return p.handleDeletedResource(key)
	}

	folder := GetDestinationFolder(&secret.ObjectMeta, p.cfg.Folder, p.cfg.FolderAnnotation)
	state := &FileState{Folder: folder, Files: make(map[string][]byte)}

	maps.Copy(state.Files, secret.Data)

	return p.updateResource(key, state)
}

func (p *Processor) updateResource(key string, newState *FileState) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	oldState, exists := p.state[key]
	var errs []error

	for filename, data := range newState.Files {
		if err := p.WriteFile(newState.Folder, filename, data); err != nil {
			log.Error().Err(err).Str("file", filename).Msg("Failed to write file")
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}

	if exists && oldState.Folder != newState.Folder {
		for filename := range oldState.Files {
			if err := DeleteFile(oldState.Folder, filename); err != nil {
				log.Error().Err(err).Str("file", filename).Msg("Failed to delete old file")
				errs = append(errs, err)
			}
		}
	} else if exists {
		for filename := range oldState.Files {
			if _, ok := newState.Files[filename]; !ok {
				if err := DeleteFile(oldState.Folder, filename); err != nil {
					log.Error().Err(err).Str("file", filename).Msg("Failed to delete removed file")
					errs = append(errs, err)
				}
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}

	p.state[key] = newState
	return nil
}

func (p *Processor) handleDeletedResource(key string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	oldState, exists := p.state[key]
	if !exists {
		return nil
	}
	var errs []error

	for filename := range oldState.Files {
		if err := DeleteFile(oldState.Folder, filename); err != nil {
			log.Error().Err(err).Str("file", filename).Msg("Failed to delete file")
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}

	delete(p.state, key)
	return nil
}

func (p *Processor) PruneStale(resourceType, namespace string, keep map[string]struct{}) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	prefix := fmt.Sprintf("%s/%s/", resourceType, namespace)
	var errs []error
	pruned := make([]string, 0)

	for key, state := range p.state {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if _, ok := keep[key]; ok {
			continue
		}
		failed := false
		for filename := range state.Files {
			if err := DeleteFile(state.Folder, filename); err != nil {
				log.Error().Err(err).Str("file", filename).Str("resource", key).Msg("Failed to delete stale file")
				errs = append(errs, err)
				failed = true
			}
		}
		if !failed {
			pruned = append(pruned, key)
		}
	}

	for _, key := range pruned {
		delete(p.state, key)
	}
	return errors.Join(errs...)
}
