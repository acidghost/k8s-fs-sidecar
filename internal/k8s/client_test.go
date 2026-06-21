package k8s

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// NewClient reaches out to the environment (in-cluster token, kubeconfig,
// rest config) via package-level indirection vars (fileExists, inClusterConfig,
// buildConfigFlags, newForConfig, userHomeDir). These tests swap those seams
// for fakes and assert on the branching/error-propagation logic without
// touching a real cluster or filesystem.

var errSeam = errors.New("seam failure")

// seamsSnapshot mirrors the package-level seam vars. A nil field means "leave
// the current value in place", which is convenient for error-path tests that
// only exercise the seam up to the failure point.
type seamsSnapshot struct {
	fileExists       func(string) (os.FileInfo, error)
	inClusterConfig  func() (*rest.Config, error)
	buildConfigFlags func(string, string) (*rest.Config, error)
	newForConfig     func(*rest.Config) (*kubernetes.Clientset, error)
	userHomeDir      func() (string, error)
}

func overrideSeams(t *testing.T, snap seamsSnapshot) {
	t.Helper()
	orig := seamsSnapshot{fileExists, inClusterConfig, buildConfigFlags, newForConfig, userHomeDir}
	if snap.fileExists != nil {
		fileExists = snap.fileExists
	}
	if snap.inClusterConfig != nil {
		inClusterConfig = snap.inClusterConfig
	}
	if snap.buildConfigFlags != nil {
		buildConfigFlags = snap.buildConfigFlags
	}
	if snap.newForConfig != nil {
		newForConfig = snap.newForConfig
	}
	if snap.userHomeDir != nil {
		userHomeDir = snap.userHomeDir
	}
	t.Cleanup(func() {
		fileExists = orig.fileExists
		inClusterConfig = orig.inClusterConfig
		buildConfigFlags = orig.buildConfigFlags
		newForConfig = orig.newForConfig
		userHomeDir = orig.userHomeDir
	})
}

func TestNewClient_InClusterWhenTokenPresent(t *testing.T) {
	var seenRestCfg *rest.Config
	kubeconfigCalled := false

	overrideSeams(t, seamsSnapshot{
		fileExists: func(string) (os.FileInfo, error) { return nil, nil }, // token file exists
		inClusterConfig: func() (*rest.Config, error) {
			return &rest.Config{Host: "https://kubernetes.default.svc"}, nil
		},
		buildConfigFlags: func(string, string) (*rest.Config, error) {
			kubeconfigCalled = true
			return nil, nil
		},
		newForConfig: func(cfg *rest.Config) (*kubernetes.Clientset, error) {
			seenRestCfg = cfg
			return &kubernetes.Clientset{}, nil
		},
	})

	cs, err := NewClient()
	require.NoError(t, err)
	require.NotNil(t, cs)
	assert.False(t, kubeconfigCalled, "kubeconfig path must not run when in-cluster token is present")
	assert.Equal(t, "https://kubernetes.default.svc", seenRestCfg.Host)
}

func TestNewClient_KubeconfigWhenNoToken(t *testing.T) {
	var seenKubeconfig string
	inClusterCalled := false

	overrideSeams(t, seamsSnapshot{
		fileExists: func(string) (os.FileInfo, error) { return nil, errSeam }, // token file absent
		inClusterConfig: func() (*rest.Config, error) {
			inClusterCalled = true
			return nil, nil
		},
		userHomeDir: func() (string, error) { return "/fake/home", nil },
		buildConfigFlags: func(_, kubeconfig string) (*rest.Config, error) {
			seenKubeconfig = kubeconfig
			return &rest.Config{}, nil
		},
		newForConfig: func(*rest.Config) (*kubernetes.Clientset, error) {
			return &kubernetes.Clientset{}, nil
		},
	})

	_, err := NewClient()
	require.NoError(t, err)
	assert.False(t, inClusterCalled, "in-cluster path must not run when token is absent")
	assert.Equal(t, "/fake/home/.kube/config", seenKubeconfig)
}

func TestNewClient_PropagatesInClusterConfigError(t *testing.T) {
	overrideSeams(t, seamsSnapshot{
		fileExists:      func(string) (os.FileInfo, error) { return nil, nil },
		inClusterConfig: func() (*rest.Config, error) { return nil, errSeam },
	})

	_, err := NewClient()
	assert.ErrorIs(t, err, errSeam)
}

func TestNewClient_PropagatesUserHomeDirError(t *testing.T) {
	overrideSeams(t, seamsSnapshot{
		fileExists:  func(string) (os.FileInfo, error) { return nil, errSeam }, // no token
		userHomeDir: func() (string, error) { return "", errSeam },
	})

	_, err := NewClient()
	assert.ErrorIs(t, err, errSeam)
}

func TestNewClient_PropagatesBuildConfigError(t *testing.T) {
	overrideSeams(t, seamsSnapshot{
		fileExists:       func(string) (os.FileInfo, error) { return nil, errSeam }, // no token
		userHomeDir:      func() (string, error) { return "/fake/home", nil },
		buildConfigFlags: func(string, string) (*rest.Config, error) { return nil, errSeam },
	})

	_, err := NewClient()
	assert.ErrorIs(t, err, errSeam)
}

func TestNewClient_PropagatesNewForConfigError(t *testing.T) {
	overrideSeams(t, seamsSnapshot{
		fileExists:      func(string) (os.FileInfo, error) { return nil, nil },
		inClusterConfig: func() (*rest.Config, error) { return &rest.Config{}, nil },
		newForConfig:    func(*rest.Config) (*kubernetes.Clientset, error) { return nil, errSeam },
	})

	_, err := NewClient()
	assert.ErrorIs(t, err, errSeam)
}
