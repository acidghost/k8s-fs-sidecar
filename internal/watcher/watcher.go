package watcher

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/acidghost/k8s-fs-sidecar/internal/config"
	"github.com/acidghost/k8s-fs-sidecar/internal/filter"
	"github.com/acidghost/k8s-fs-sidecar/internal/processor"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

// reconnectDelay is the pause between a failed/lost watch and the next attempt.
const reconnectDelay = 5 * time.Second

func WatchConfigMaps(ctx context.Context, clientset kubernetes.Interface, proc *processor.Processor, cfg *config.Config) {
	for _, namespace := range cfg.Namespaces {
		go watchConfigMapsInNamespace(ctx, clientset, proc, cfg, namespace)
	}
}

func WatchSecrets(ctx context.Context, clientset kubernetes.Interface, proc *processor.Processor, cfg *config.Config) {
	for _, namespace := range cfg.Namespaces {
		go watchSecretsInNamespace(ctx, clientset, proc, cfg, namespace)
	}
}

// watchConfigMapsInNamespace runs the standard List→Watch→resume loop. It
// threads the resourceVersion through so a watch resumes from the last event
// the client saw (no gap, no re-processing) and only re-lists when the
// resourceVersion has expired (410 Gone) or on the very first sync.
func watchConfigMapsInNamespace(ctx context.Context, clientset kubernetes.Interface, proc *processor.Processor, cfg *config.Config, namespace string) {
	rv := ""
	for {
		if ctx.Err() != nil {
			return
		}
		if rv == "" {
			newRV, err := initialConfigMapSync(ctx, clientset, proc, cfg, namespace)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Error().Err(err).Str("namespace", namespace).Msg("Initial ConfigMap sync failed, retrying in 5 seconds")
				if !backoff(ctx) {
					return
				}
				continue
			}
			rv = newRV
		}

		nextRV, err := watchConfigMaps(ctx, clientset, proc, cfg, namespace, rv)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if apierrors.IsResourceExpired(err) || apierrors.IsGone(err) {
				log.Warn().Err(err).Str("namespace", namespace).Msg("ConfigMap watch resource version expired, re-listing")
				rv = ""
			} else {
				log.Error().Err(err).Str("namespace", namespace).Msg("ConfigMap watch failed, retrying in 5 seconds")
				rv = nextRV // resume from the latest RV we observed
			}
		} else {
			rv = nextRV // watch closed cleanly; resume from latest RV
		}
		if !backoff(ctx) {
			return
		}
	}
}

func watchSecretsInNamespace(ctx context.Context, clientset kubernetes.Interface, proc *processor.Processor, cfg *config.Config, namespace string) {
	rv := ""
	for {
		if ctx.Err() != nil {
			return
		}
		if rv == "" {
			newRV, err := initialSecretSync(ctx, clientset, proc, cfg, namespace)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Error().Err(err).Str("namespace", namespace).Msg("Initial Secret sync failed, retrying in 5 seconds")
				if !backoff(ctx) {
					return
				}
				continue
			}
			rv = newRV
		}

		nextRV, err := watchSecrets(ctx, clientset, proc, cfg, namespace, rv)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if apierrors.IsResourceExpired(err) || apierrors.IsGone(err) {
				log.Warn().Err(err).Str("namespace", namespace).Msg("Secret watch resource version expired, re-listing")
				rv = ""
			} else {
				log.Error().Err(err).Str("namespace", namespace).Msg("Secret watch failed, retrying in 5 seconds")
				rv = nextRV
			}
		} else {
			rv = nextRV
		}
		if !backoff(ctx) {
			return
		}
	}
}

func initialConfigMapSync(ctx context.Context, clientset kubernetes.Interface, proc *processor.Processor, cfg *config.Config, namespace string) (string, error) {
	log.Info().Str("namespace", namespace).Msg("Performing initial ConfigMap sync")

	configMaps, err := clientset.CoreV1().ConfigMaps(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", err
	}

	seen := make(map[string]struct{})
	var errs []error
	for i := range configMaps.Items {
		cm := &configMaps.Items[i]
		if !filter.Matches(&cm.ObjectMeta, cfg) {
			continue
		}
		seen[processor.GetResourceKey(&cm.ObjectMeta, "configmap")] = struct{}{}
		if err := proc.ProcessConfigMap(cm, "ADDED"); err != nil {
			log.Error().Err(err).Str("name", cm.Name).Str("namespace", namespace).Msg("Failed to process ConfigMap")
			errs = append(errs, err)
		}
	}
	if err := proc.PruneStale("configmap", namespace, seen); err != nil {
		log.Error().Err(err).Str("namespace", namespace).Msg("Failed to prune stale ConfigMaps")
		errs = append(errs, err)
	}

	log.Info().Str("namespace", namespace).Int("count", len(configMaps.Items)).Msg("Initial ConfigMap sync completed")
	return configMaps.ResourceVersion, errors.Join(errs...)
}

func initialSecretSync(ctx context.Context, clientset kubernetes.Interface, proc *processor.Processor, cfg *config.Config, namespace string) (string, error) {
	log.Info().Str("namespace", namespace).Msg("Performing initial Secret sync")

	secrets, err := clientset.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", err
	}

	seen := make(map[string]struct{})
	var errs []error
	for i := range secrets.Items {
		secret := &secrets.Items[i]
		if !filter.Matches(&secret.ObjectMeta, cfg) {
			continue
		}
		seen[processor.GetResourceKey(&secret.ObjectMeta, "secret")] = struct{}{}
		if err := proc.ProcessSecret(secret, "ADDED"); err != nil {
			log.Error().Err(err).Str("name", secret.Name).Str("namespace", namespace).Msg("Failed to process Secret")
			errs = append(errs, err)
		}
	}
	if err := proc.PruneStale("secret", namespace, seen); err != nil {
		log.Error().Err(err).Str("namespace", namespace).Msg("Failed to prune stale Secrets")
		errs = append(errs, err)
	}

	log.Info().Str("namespace", namespace).Int("count", len(secrets.Items)).Msg("Initial Secret sync completed")
	return secrets.ResourceVersion, errors.Join(errs...)
}

// watchConfigMaps consumes events from a Watch started at rv and returns the
// latest resourceVersion observed (from real events or bookmarks) plus any
// terminal error. Bookmarks update the RV without touching disk; a closed
// result channel returns (rv, nil) so the caller resumes without re-listing.
func watchConfigMaps(ctx context.Context, clientset kubernetes.Interface, proc *processor.Processor, cfg *config.Config, namespace, rv string) (string, error) {
	log.Info().Str("namespace", namespace).Str("resource_version", rv).Msg("Starting ConfigMap watch")

	watcher, err := clientset.CoreV1().ConfigMaps(namespace).Watch(ctx, metav1.ListOptions{
		ResourceVersion:     rv,
		AllowWatchBookmarks: true,
	})
	if err != nil {
		return rv, err
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return rv, ctx.Err()
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return rv, nil // watch closed; caller resumes from rv
			}
			switch obj := event.Object.(type) {
			case *corev1.ConfigMap:
				if obj.ResourceVersion != "" {
					rv = obj.ResourceVersion
				}
				if event.Type == watch.Bookmark {
					continue
				}
				if !filter.Matches(&obj.ObjectMeta, cfg) {
					if err := proc.ProcessConfigMap(obj, string(watch.Deleted)); err != nil {
						log.Error().Err(err).Str("name", obj.Name).Str("namespace", namespace).Msg("Failed to remove unmatched ConfigMap")
					}
					continue
				}
				eventType := string(event.Type)
				log.Info().Str("type", eventType).Str("name", obj.Name).Str("namespace", namespace).Msg("ConfigMap event")
				if err := proc.ProcessConfigMap(obj, eventType); err != nil {
					log.Error().Err(err).Str("name", obj.Name).Str("namespace", namespace).Msg("Failed to process ConfigMap")
				}
			default:
				if event.Type == watch.Error {
					return rv, fmt.Errorf("configmap watch error frame: %v", event.Object)
				}
				// Unknown frame type (shouldn't happen); ignore and keep watching.
			}
		}
	}
}

func watchSecrets(ctx context.Context, clientset kubernetes.Interface, proc *processor.Processor, cfg *config.Config, namespace, rv string) (string, error) {
	log.Info().Str("namespace", namespace).Str("resource_version", rv).Msg("Starting Secret watch")

	watcher, err := clientset.CoreV1().Secrets(namespace).Watch(ctx, metav1.ListOptions{
		ResourceVersion:     rv,
		AllowWatchBookmarks: true,
	})
	if err != nil {
		return rv, err
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return rv, ctx.Err()
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return rv, nil
			}
			switch obj := event.Object.(type) {
			case *corev1.Secret:
				if obj.ResourceVersion != "" {
					rv = obj.ResourceVersion
				}
				if event.Type == watch.Bookmark {
					continue
				}
				if !filter.Matches(&obj.ObjectMeta, cfg) {
					if err := proc.ProcessSecret(obj, string(watch.Deleted)); err != nil {
						log.Error().Err(err).Str("name", obj.Name).Str("namespace", namespace).Msg("Failed to remove unmatched Secret")
					}
					continue
				}
				eventType := string(event.Type)
				log.Info().Str("type", eventType).Str("name", obj.Name).Str("namespace", namespace).Msg("Secret event")
				if err := proc.ProcessSecret(obj, eventType); err != nil {
					log.Error().Err(err).Str("name", obj.Name).Str("namespace", namespace).Msg("Failed to process Secret")
				}
			default:
				if event.Type == watch.Error {
					return rv, fmt.Errorf("secret watch error frame: %v", event.Object)
				}
			}
		}
	}
}

// backoff pauses for reconnectDelay but returns early (false) when ctx is
// canceled, so a shutdown during the backoff window isn't delayed by the
// full interval.
func backoff(ctx context.Context) bool {
	t := time.NewTimer(reconnectDelay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
