package k8s

import (
	"os"
	"path/filepath"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	fileExists       = os.Stat
	inClusterConfig  = rest.InClusterConfig
	buildConfigFlags = clientcmd.BuildConfigFromFlags
	newForConfig     = kubernetes.NewForConfig
	userHomeDir      = os.UserHomeDir
)

func NewClient() (*kubernetes.Clientset, error) {
	var config *rest.Config
	var err error

	if _, err := fileExists("/var/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
		config, err = inClusterConfig()
		if err != nil {
			return nil, err
		}
	} else {
		home, err := userHomeDir()
		if err != nil {
			return nil, err
		}
		kubeconfig := filepath.Join(home, ".kube", "config")
		config, err = buildConfigFlags("", kubeconfig)
		if err != nil {
			return nil, err
		}
	}

	clientset, err := newForConfig(config)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}
