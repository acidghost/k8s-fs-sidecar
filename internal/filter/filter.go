package filter

import (
	"github.com/acidghost/k8s-fs-sidecar/internal/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Matches(obj *metav1.ObjectMeta, cfg *config.Config) bool {
	if cfg.LabelType == "label" {
		return matchesLabels(obj.Labels, cfg.Label, cfg.LabelValue)
	}
	return matchesAnnotations(obj.Annotations, cfg.Label, cfg.LabelValue)
}

func matchesLabels(labels map[string]string, key, value string) bool {
	if labels == nil {
		return false
	}
	if value == "" {
		_, exists := labels[key]
		return exists
	}
	return labels[key] == value
}

func matchesAnnotations(annotations map[string]string, key, value string) bool {
	if annotations == nil {
		return false
	}
	if value == "" {
		_, exists := annotations[key]
		return exists
	}
	return annotations[key] == value
}
