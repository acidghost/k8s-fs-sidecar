package filter

import (
	"testing"

	"github.com/acidghost/k8s-fs-sidecar/internal/config"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMatchesLabels_MatchingKeyAndValue(t *testing.T) {
	labels := map[string]string{"app": "myapp", "env": "prod"}
	result := matchesLabels(labels, "app", "myapp")
	assert.True(t, result)
}

func TestMatchesLabels_MatchingKeyNoValue(t *testing.T) {
	labels := map[string]string{"app": "myapp", "env": "prod"}
	result := matchesLabels(labels, "app", "")
	assert.True(t, result)
}

func TestMatchesLabels_NonMatchingValue(t *testing.T) {
	labels := map[string]string{"app": "myapp", "env": "prod"}
	result := matchesLabels(labels, "app", "otherapp")
	assert.False(t, result)
}

func TestMatchesLabels_KeyNotExists(t *testing.T) {
	labels := map[string]string{"app": "myapp"}
	result := matchesLabels(labels, "missing", "value")
	assert.False(t, result)
}

func TestMatchesLabels_NilMap(t *testing.T) {
	result := matchesLabels(nil, "app", "value")
	assert.False(t, result)
}

func TestMatchesLabels_EmptyMap(t *testing.T) {
	labels := map[string]string{}
	result := matchesLabels(labels, "app", "value")
	assert.False(t, result)
}

func TestMatchesAnnotations_MatchingKeyAndValue(t *testing.T) {
	annotations := map[string]string{"config.sync": "enabled"}
	result := matchesAnnotations(annotations, "config.sync", "enabled")
	assert.True(t, result)
}

func TestMatchesAnnotations_MatchingKeyNoValue(t *testing.T) {
	annotations := map[string]string{"config.sync": "enabled"}
	result := matchesAnnotations(annotations, "config.sync", "")
	assert.True(t, result)
}

func TestMatchesAnnotations_NonMatchingValue(t *testing.T) {
	annotations := map[string]string{"config.sync": "enabled"}
	result := matchesAnnotations(annotations, "config.sync", "disabled")
	assert.False(t, result)
}

func TestMatchesAnnotations_KeyNotExists(t *testing.T) {
	annotations := map[string]string{"config.sync": "enabled"}
	result := matchesAnnotations(annotations, "missing", "value")
	assert.False(t, result)
}

func TestMatchesAnnotations_NilMap(t *testing.T) {
	result := matchesAnnotations(nil, "config.sync", "value")
	assert.False(t, result)
}

func TestMatchesAnnotations_EmptyMap(t *testing.T) {
	annotations := map[string]string{}
	result := matchesAnnotations(annotations, "config.sync", "value")
	assert.False(t, result)
}

func TestMatches_RoutesToLabelMatching(t *testing.T) {
	obj := &metav1.ObjectMeta{
		Name:      "test-config",
		Namespace: "default",
		Labels: map[string]string{
			"app": "myapp",
		},
		Annotations: map[string]string{
			"config.sync": "enabled",
		},
	}

	cfg := &config.Config{
		Label:      "app",
		LabelValue: "myapp",
		LabelType:  "label",
	}

	result := Matches(obj, cfg)
	assert.True(t, result)
}

func TestMatches_RoutesToAnnotationMatching(t *testing.T) {
	obj := &metav1.ObjectMeta{
		Name:      "test-config",
		Namespace: "default",
		Labels: map[string]string{
			"app": "myapp",
		},
		Annotations: map[string]string{
			"config.sync": "enabled",
		},
	}

	cfg := &config.Config{
		Label:      "config.sync",
		LabelValue: "enabled",
		LabelType:  "annotation",
	}

	result := Matches(obj, cfg)
	assert.True(t, result)
}

func TestMatches_LabelTypeLabelIgnoresAnnotations(t *testing.T) {
	obj := &metav1.ObjectMeta{
		Name:      "test-config",
		Namespace: "default",
		Annotations: map[string]string{
			"app": "myapp",
		},
	}

	cfg := &config.Config{
		Label:      "app",
		LabelValue: "myapp",
		LabelType:  "label",
	}

	result := Matches(obj, cfg)
	assert.False(t, result)
}

func TestMatches_LabelTypeAnnotationIgnoresLabels(t *testing.T) {
	obj := &metav1.ObjectMeta{
		Name:      "test-config",
		Namespace: "default",
		Labels: map[string]string{
			"app": "myapp",
		},
	}

	cfg := &config.Config{
		Label:      "app",
		LabelValue: "myapp",
		LabelType:  "annotation",
	}

	result := Matches(obj, cfg)
	assert.False(t, result)
}
