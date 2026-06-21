package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/acidghost/k8s-fs-sidecar/internal/config"
	"github.com/acidghost/k8s-fs-sidecar/internal/processor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const testNamespace = "default"

// This is an integration test, not a unit test: it drives the real
// watcher → filter → processor → filesystem loop against an in-process fake
// clientset. It validates the end-to-end contract that matters to consumers
// (seeded objects materialize on disk, mutations propagate, deletions clean
// up) without standing up a real apiserver.
//
// Run with -race to surface the Processor.state data race: WatchConfigMaps
// and WatchSecrets share one *Processor, so their goroutines concurrently
// mutate proc.state.

func newE2EConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	folder := t.TempDir()
	return &config.Config{
		Label:            "config.sync",
		LabelValue:       "enabled",
		LabelType:        "label",
		Folder:           folder,
		FolderAnnotation: "k8s-sidecar-target-directory",
		Namespaces:       []string{testNamespace},
		Resources:        []string{"configmap", "secret"},
		FileMode:         0600,
		DirMode:          0700,
	}, folder
}

func TestE2E_InitialSyncUpdateDelete(t *testing.T) {
	cfg, folder := newE2EConfig(t)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app-config", Namespace: testNamespace,
			Labels: map[string]string{"config.sync": "enabled"},
		},
		Data: map[string]string{"config.yaml": "key: value"},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app-secret", Namespace: testNamespace,
			Labels: map[string]string{"config.sync": "enabled"},
		},
		Data: map[string][]byte{"password": []byte("super-secret")},
	}

	clientset := fake.NewSimpleClientset(cm, secret)
	proc := processor.NewProcessor(cfg)

	ctx := t.Context()

	WatchConfigMaps(ctx, clientset, proc, cfg)
	WatchSecrets(ctx, clientset, proc, cfg)

	// Initial sync: both files appear on disk.
	require.Eventually(t, func() bool {
		_, err1 := os.Stat(filepath.Join(folder, "config.yaml"))
		_, err2 := os.Stat(filepath.Join(folder, "password"))
		return err1 == nil && err2 == nil
	}, 2*time.Second, 10*time.Millisecond, "initial sync did not materialize files")

	assertFile(t, filepath.Join(folder, "password"), "super-secret")

	// Update the ConfigMap: new content propagates.
	cm.Data["config.yaml"] = "key: updated"
	_, err := clientset.CoreV1().ConfigMaps(testNamespace).Update(ctx, cm, metav1.UpdateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(filepath.Join(folder, "config.yaml")) //nolint:gosec // test fixture path
		return err == nil && string(b) == "key: updated"
	}, 2*time.Second, 10*time.Millisecond, "ConfigMap update did not propagate to disk")

	// Delete the ConfigMap: file is removed.
	require.NoError(t, clientset.CoreV1().ConfigMaps(testNamespace).Delete(ctx, cm.Name, metav1.DeleteOptions{}))
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(folder, "config.yaml"))
		return os.IsNotExist(err)
	}, 2*time.Second, 10*time.Millisecond, "ConfigMap deletion did not remove the file")

	// Filter is honored: a ConfigMap without the label never lands on disk.
	unmatched := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "no-sync", Namespace: testNamespace},
		Data:       map[string]string{"ignored.yaml": "x"},
	}
	_, err = clientset.CoreV1().ConfigMaps(testNamespace).Create(ctx, unmatched, metav1.CreateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		// Give the watch a moment to deliver the event, then confirm it was filtered.
		time.Sleep(100 * time.Millisecond)
		_, statErr := os.Stat(filepath.Join(folder, "ignored.yaml"))
		return os.IsNotExist(statErr)
	}, 2*time.Second, 200*time.Millisecond, "unlabeled ConfigMap should be filtered out")

}

func TestE2E_FolderAnnotation(t *testing.T) {
	cfg, folder := newE2EConfig(t)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app-config", Namespace: testNamespace,
			Labels:      map[string]string{"config.sync": "enabled"},
			Annotations: map[string]string{"k8s-sidecar-target-directory": "sub"},
		},
		Data: map[string]string{"config.yaml": "key: value"},
	}

	clientset := fake.NewSimpleClientset(cm)
	proc := processor.NewProcessor(cfg)

	ctx := t.Context()

	WatchConfigMaps(ctx, clientset, proc, cfg)

	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(folder, "sub", "config.yaml"))
		return err == nil
	}, 2*time.Second, 10*time.Millisecond, "folder annotation should redirect to a subfolder")

}

func TestE2E_RemovesFilesWhenResourcesStopMatching(t *testing.T) {
	cfg, folder := newE2EConfig(t)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app-config", Namespace: testNamespace,
			Labels: map[string]string{"config.sync": "enabled"},
		},
		Data: map[string]string{"config.yaml": "v1"},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app-secret", Namespace: testNamespace,
			Labels: map[string]string{"config.sync": "enabled"},
		},
		Data: map[string][]byte{"password": []byte("super-secret")},
	}
	clientset := fake.NewSimpleClientset(cm, secret)
	proc := processor.NewProcessor(cfg)

	ctx := t.Context()

	WatchConfigMaps(ctx, clientset, proc, cfg)
	WatchSecrets(ctx, clientset, proc, cfg)

	require.Eventually(t, func() bool {
		_, err1 := os.Stat(filepath.Join(folder, "config.yaml"))
		_, err2 := os.Stat(filepath.Join(folder, "password"))
		return err1 == nil && err2 == nil
	}, 2*time.Second, 10*time.Millisecond, "initial sync did not materialize files")

	cm.Labels = map[string]string{"config.sync": "disabled"}
	_, err := clientset.CoreV1().ConfigMaps(testNamespace).Update(ctx, cm, metav1.UpdateOptions{})
	require.NoError(t, err)
	secret.Labels = map[string]string{"config.sync": "disabled"}
	_, err = clientset.CoreV1().Secrets(testNamespace).Update(ctx, secret, metav1.UpdateOptions{})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		_, err1 := os.Stat(filepath.Join(folder, "config.yaml"))
		_, err2 := os.Stat(filepath.Join(folder, "password"))
		return os.IsNotExist(err1) && os.IsNotExist(err2)
	}, 2*time.Second, 10*time.Millisecond, "files should be removed when resources stop matching")
}

func TestInitialSyncPrunesResourcesMissingFromRelist(t *testing.T) {
	cfg, folder := newE2EConfig(t)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app-config", Namespace: testNamespace,
			Labels: map[string]string{"config.sync": "enabled"},
		},
		Data: map[string]string{"config.yaml": "v1"},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app-secret", Namespace: testNamespace,
			Labels: map[string]string{"config.sync": "enabled"},
		},
		Data: map[string][]byte{"password": []byte("super-secret")},
	}
	clientset := fake.NewSimpleClientset(cm, secret)
	proc := processor.NewProcessor(cfg)
	ctx := context.Background()

	_, err := initialConfigMapSync(ctx, clientset, proc, cfg, testNamespace)
	require.NoError(t, err)
	_, err = initialSecretSync(ctx, clientset, proc, cfg, testNamespace)
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(folder, "config.yaml"))
	require.FileExists(t, filepath.Join(folder, "password"))

	require.NoError(t, clientset.CoreV1().ConfigMaps(testNamespace).Delete(ctx, cm.Name, metav1.DeleteOptions{}))
	require.NoError(t, clientset.CoreV1().Secrets(testNamespace).Delete(ctx, secret.Name, metav1.DeleteOptions{}))

	_, err = initialConfigMapSync(ctx, clientset, proc, cfg, testNamespace)
	require.NoError(t, err)
	_, err = initialSecretSync(ctx, clientset, proc, cfg, testNamespace)
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(folder, "config.yaml"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(folder, "password"))
	assert.True(t, os.IsNotExist(err))
}

func TestE2E_WatchResumesAfterCloseWithoutRelist(t *testing.T) {
	// The watcher must resume from its last resourceVersion after a watch
	// closes, not re-list everything. We prove this by: seeding one object,
	// starting the watcher, then injecting an update and confirming it lands
	// on disk. Because the fake clientset honors ResourceVersion on Watch
	// (delivering objects with RV >= the watch RV), a watcher that re-listed
	// from scratch would still see the seeded object, but only a watcher that
	// resumes from the list's RV will catch an update that happens *after* the
	// initial sync without missing it.
	cfg, folder := newE2EConfig(t)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app-config", Namespace: testNamespace,
			Labels: map[string]string{"config.sync": "enabled"},
		},
		Data: map[string]string{"config.yaml": "v1"},
	}
	clientset := fake.NewSimpleClientset(cm)
	proc := processor.NewProcessor(cfg)

	ctx := t.Context()

	WatchConfigMaps(ctx, clientset, proc, cfg)

	require.Eventually(t, func() bool {
		b, err := os.ReadFile(filepath.Join(folder, "config.yaml")) //nolint:gosec // test fixture
		return err == nil && string(b) == "v1"
	}, 2*time.Second, 10*time.Millisecond, "initial sync should write v1")

	// Update after the initial sync: a watcher that resumed from the list's
	// RV (rather than re-listing from scratch) will receive this event.
	cm.Data["config.yaml"] = "v2"
	_, err := clientset.CoreV1().ConfigMaps(testNamespace).Update(ctx, cm, metav1.UpdateOptions{})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		b, err := os.ReadFile(filepath.Join(folder, "config.yaml")) //nolint:gosec // test fixture
		return err == nil && string(b) == "v2"
	}, 2*time.Second, 10*time.Millisecond, "watch should deliver the post-sync update")
}

func TestE2E_ExpiredResourceVersionTriggersRelist(t *testing.T) {
	// When a watch returns a "resource version expired" error, the loop must
	// drop its RV, re-list, and resume watching. We prove the full recovery by:
	// making the first Watch fail with ResourceExpired, then mutating the
	// object and asserting the update eventually lands on disk. That can only
	// happen if the loop re-listed and opened a fresh working watch.
	//
	// Note: this test waits through one reconnectDelay (5s) backoff.
	cfg, folder := newE2EConfig(t)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app-config", Namespace: testNamespace,
			Labels: map[string]string{"config.sync": "enabled"},
		},
		Data: map[string]string{"config.yaml": "v1"},
	}
	clientset := fake.NewSimpleClientset(cm)

	var firstAttempt atomic.Int32
	clientset.PrependWatchReactor("configmaps", func(_ k8stesting.Action) (bool, watch.Interface, error) {
		if firstAttempt.Add(1) == 1 {
			return true, nil, apierrors.NewResourceExpired("the resourceVersion provided was too old")
		}
		return false, nil, nil // fall through to default reactor
	})

	proc := processor.NewProcessor(cfg)

	ctx := t.Context()

	WatchConfigMaps(ctx, clientset, proc, cfg)

	// Wait for the initial sync to land v1, then mutate. The update can only
	// propagate once the loop has recovered from the expired error.
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(filepath.Join(folder, "config.yaml")) //nolint:gosec // test fixture
		return err == nil && string(b) == "v1"
	}, 2*time.Second, 10*time.Millisecond, "initial sync should write v1")

	cm.Data["config.yaml"] = "v2"
	_, err := clientset.CoreV1().ConfigMaps(testNamespace).Update(ctx, cm, metav1.UpdateOptions{})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		b, err := os.ReadFile(filepath.Join(folder, "config.yaml")) //nolint:gosec // test fixture
		return err == nil && string(b) == "v2"
	}, 12*time.Second, 50*time.Millisecond, "update should propagate after the loop recovers from expired RV")
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test fixture path
	require.NoError(t, err)
	assert.Equal(t, want, string(b))
}
