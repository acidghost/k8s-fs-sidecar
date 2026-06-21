package processor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/acidghost/k8s-fs-sidecar/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// newTestProcessor builds a Processor against a fresh temp dir. Each test gets
// its own instance — no global state to reset between cases.

func newTestProcessor(t *testing.T) (*Processor, string) {
	t.Helper()
	folder := t.TempDir()
	return NewProcessor(&config.Config{
		Folder:           folder,
		FolderAnnotation: "k8s-sidecar-target-directory",
		FileMode:         0600,
		DirMode:          0700,
	}), folder
}

// newProcessorWithModes builds a Processor for tests that exercise file I/O
// directly via WriteFile/DeleteFile, where cfg.Folder is irrelevant (the test
// passes its own folder) but the permission modes matter.
func newProcessorWithModes(t *testing.T, fileMode, dirMode os.FileMode) *Processor {
	t.Helper()
	return NewProcessor(&config.Config{
		Folder:           t.TempDir(),
		FolderAnnotation: "k8s-sidecar-target-directory",
		FileMode:         fileMode,
		DirMode:          dirMode,
	})
}

func TestGetResourceKey(t *testing.T) {
	obj := &metav1.ObjectMeta{Name: "test-config", Namespace: "default"}

	assert.Equal(t, "configmap/default/test-config", GetResourceKey(obj, "configmap"))
	assert.Equal(t, "secret/default/test-config", GetResourceKey(obj, "secret"))
}

func TestGetDestinationFolder(t *testing.T) {
	tests := []struct {
		name string
		obj  *metav1.ObjectMeta
		want string
	}{
		{"absolute", &metav1.ObjectMeta{Annotations: map[string]string{"k8s-sidecar-target-directory": "/absolute/path"}}, "/absolute/path"},
		{"relative", &metav1.ObjectMeta{Annotations: map[string]string{"k8s-sidecar-target-directory": "subfolder"}}, "/default/folder/subfolder"},
		{"other annotation present", &metav1.ObjectMeta{Annotations: map[string]string{"other-annotation": "value"}}, "/default/folder"},
		{"nil annotations", &metav1.ObjectMeta{Annotations: nil}, "/default/folder"},
		{"no annotations at all", &metav1.ObjectMeta{}, "/default/folder"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, GetDestinationFolder(tc.obj, "/default/folder", "k8s-sidecar-target-directory"))
		})
	}
}

func TestSafePath(t *testing.T) {
	t.Run("valid filename", func(t *testing.T) {
		p, err := safePath("/etc/config", "config.yaml")
		require.NoError(t, err)
		assert.Equal(t, "/etc/config/config.yaml", p)
	})
	t.Run("nested path", func(t *testing.T) {
		p, err := safePath("/etc/config", "subdir/config.yaml")
		require.NoError(t, err)
		assert.Equal(t, "/etc/config/subdir/config.yaml", p)
	})
	t.Run("parent traversal rejected", func(t *testing.T) {
		_, err := safePath("/etc/config", "../etc/passwd")
		assert.Error(t, err)
	})
	t.Run("parent only rejected", func(t *testing.T) {
		_, err := safePath("/etc/config", "..")
		assert.Error(t, err)
	})
	t.Run("absolute filename contained", func(t *testing.T) {
		// filepath.Join collapses a leading slash, so an absolute key is kept
		// inside the destination folder rather than escaping it.
		p, err := safePath("/etc/config", "/etc/passwd")
		require.NoError(t, err)
		assert.Equal(t, "/etc/config/etc/passwd", p)
	})
}

func TestProcessConfigMap_StatePopulated(t *testing.T) {
	proc, _ := newTestProcessor(t)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app-config", Namespace: "default"},
		Data:       map[string]string{"config.yaml": "key: value"},
		BinaryData: map[string][]byte{"blob.bin": {0x01, 0x02}},
	}

	require.NoError(t, proc.ProcessConfigMap(cm, "ADDED"))

	state := proc.state["configmap/default/app-config"]
	require.NotNil(t, state)
	assert.Len(t, state.Files, 2)
	assert.Equal(t, []byte{0x01, 0x02}, state.Files["blob.bin"])
}

func TestProcessConfigMap_FolderAnnotationRecordedInState(t *testing.T) {
	proc, _ := newTestProcessor(t)
	customFolder := filepath.Join(t.TempDir(), "custom")
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app-config", Namespace: "default",
			Annotations: map[string]string{"k8s-sidecar-target-directory": customFolder},
		},
		Data: map[string]string{"config.yaml": "key: value"},
	}

	require.NoError(t, proc.ProcessConfigMap(cm, "ADDED"))

	assert.Equal(t, customFolder, proc.state["configmap/default/app-config"].Folder)
}

func TestProcessSecret_FolderAnnotationRecordedInState(t *testing.T) {
	proc, _ := newTestProcessor(t)
	customFolder := filepath.Join(t.TempDir(), "custom")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app-secret", Namespace: "default",
			Annotations: map[string]string{"k8s-sidecar-target-directory": customFolder},
		},
		Data: map[string][]byte{"password": []byte("super-secret")},
	}

	require.NoError(t, proc.ProcessSecret(secret, "ADDED"))

	assert.Equal(t, customFolder, proc.state["secret/default/app-secret"].Folder)
}

func TestProcessConfigMap_WriteFailureDoesNotCommitState(t *testing.T) {
	proc, _ := newTestProcessor(t)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app-config", Namespace: "default"},
		Data:       map[string]string{"../escape": "nope"},
	}

	require.Error(t, proc.ProcessConfigMap(cm, "ADDED"))
	assert.Nil(t, proc.state["configmap/default/app-config"])
}

func TestProcessConfigMap_UpdateWriteFailureKeepsPriorState(t *testing.T) {
	proc, folder := newTestProcessor(t)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app-config", Namespace: "default"},
		Data:       map[string]string{"config.yaml": "v1"},
	}
	require.NoError(t, proc.ProcessConfigMap(cm, "ADDED"))

	cm.Data = map[string]string{"../escape": "nope"}
	require.Error(t, proc.ProcessConfigMap(cm, "MODIFIED"))

	state := proc.state["configmap/default/app-config"]
	require.NotNil(t, state)
	assert.Equal(t, map[string][]byte{"config.yaml": []byte("v1")}, state.Files)
	require.FileExists(t, filepath.Join(folder, "config.yaml"))
}

func TestHandleDeletedResource_Existing(t *testing.T) {
	proc, _ := newTestProcessor(t)
	proc.state["configmap/default/test-config"] = &FileState{
		Folder: filepath.Join(t.TempDir(), "x"),
		Files:  map[string][]byte{"config.yaml": []byte("key: value")},
	}

	require.NoError(t, proc.handleDeletedResource("configmap/default/test-config"))

	assert.Nil(t, proc.state["configmap/default/test-config"])
}

func TestHandleDeletedResource_NotExisting(t *testing.T) {
	proc, _ := newTestProcessor(t)

	assert.NoError(t, proc.handleDeletedResource("configmap/default/never-was"))
}
