package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseNamespacesTrimsAndDropsEmptyEntries(t *testing.T) {
	assert.Equal(t, []string{"default", "kube-system", "ops"}, parseNamespaces(" default, kube-system,,ops "))
	assert.Empty(t, parseNamespaces(", ,"))
}

func TestValidateRejectsEmptyNamespaceList(t *testing.T) {
	cfg := &Config{
		Label:      "config.sync",
		LabelType:  "label",
		Folder:     "/out",
		Namespaces: []string{},
		LogFormat:  "json",
	}

	assert.ErrorContains(t, cfg.Validate(), EnvNamespace)
}

func TestValidateRejectsEmptyNamespaceEntry(t *testing.T) {
	cfg := &Config{
		Label:      "config.sync",
		LabelType:  "label",
		Folder:     "/out",
		Namespaces: []string{""},
		LogFormat:  "json",
	}

	assert.ErrorContains(t, cfg.Validate(), EnvNamespace)
}
