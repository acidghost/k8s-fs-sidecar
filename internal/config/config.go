package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"
)

const (
	EnvPrefix      = "FS_SIDECAR_"
	EnvLabel       = EnvPrefix + "LABEL"
	EnvLabelValue  = EnvPrefix + "LABEL_VALUE"
	EnvLabelType   = EnvPrefix + "LABEL_ANNOTATION"
	EnvFolder      = EnvPrefix + "FOLDER"
	EnvFolderAnnot = EnvPrefix + "FOLDER_ANNOTATION"
	EnvNamespace   = EnvPrefix + "NAMESPACE"
	EnvResource    = EnvPrefix + "RESOURCE"
	EnvLogLevel    = EnvPrefix + "LOG_LEVEL"
	EnvLogFormat   = EnvPrefix + "LOG_FORMAT"
	EnvFileMode    = EnvPrefix + "FILE_MODE"
	EnvDirMode     = EnvPrefix + "DIR_MODE"
)

const (
	// defaultFileMode / defaultDirMode are the secure-by-default permission
	// modes: owner-only. Override via FS_SIDECAR_FILE_MODE / FS_SIDECAR_DIR_MODE
	// when the consuming container runs as a different uid.
	defaultFileMode os.FileMode = 0600
	defaultDirMode  os.FileMode = 0700
)

var namespaceFilePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

type Config struct {
	Label            string
	LabelValue       string
	LabelType        string
	Folder           string
	FolderAnnotation string
	Namespaces       []string
	Resources        []string
	LogLevel         string
	LogFormat        string
	FileMode         os.FileMode
	DirMode          os.FileMode
}

func LoadFromEnv() (*Config, error) {
	cfg := &Config{}

	cfg.Label = os.Getenv(EnvLabel)
	cfg.LabelValue = os.Getenv(EnvLabelValue)
	cfg.LabelType = os.Getenv(EnvLabelType)
	if cfg.LabelType == "" {
		cfg.LabelType = "label"
	}

	cfg.Folder = os.Getenv(EnvFolder)
	cfg.FolderAnnotation = os.Getenv(EnvFolderAnnot)
	if cfg.FolderAnnotation == "" {
		cfg.FolderAnnotation = "k8s-sidecar-target-directory"
	}

	namespace := os.Getenv(EnvNamespace)
	if namespace == "" {
		currentNs, err := getCurrentNamespace()
		if err != nil {
			return nil, fmt.Errorf("failed to get current namespace: %w", err)
		}
		cfg.Namespaces = []string{currentNs}
	} else {
		cfg.Namespaces = parseNamespaces(namespace)
	}

	resource := os.Getenv(EnvResource)
	if resource == "" {
		resource = "both"
	}
	switch resource {
	case "configmap":
		cfg.Resources = []string{"configmap"}
	case "secret":
		cfg.Resources = []string{"secret"}
	case "both":
		cfg.Resources = []string{"configmap", "secret"}
	default:
		return nil, fmt.Errorf("invalid resource type: %s, must be 'configmap', 'secret', or 'both'", resource)
	}

	cfg.LogLevel = os.Getenv(EnvLogLevel)
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}

	cfg.LogFormat = os.Getenv(EnvLogFormat)
	if cfg.LogFormat == "" {
		cfg.LogFormat = "json"
	}

	fileMode, err := parseMode(EnvFileMode, os.Getenv(EnvFileMode), defaultFileMode)
	if err != nil {
		return nil, err
	}
	cfg.FileMode = fileMode

	dirMode, err := parseMode(EnvDirMode, os.Getenv(EnvDirMode), defaultDirMode)
	if err != nil {
		return nil, err
	}
	cfg.DirMode = dirMode

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	log.Info().
		Str("label", cfg.Label).
		Str("label_value", cfg.LabelValue).
		Str("label_type", cfg.LabelType).
		Str("folder", cfg.Folder).
		Strs("namespaces", cfg.Namespaces).
		Strs("resources", cfg.Resources).
		Str("log_level", cfg.LogLevel).
		Str("log_format", cfg.LogFormat).
		Str("file_mode", fmt.Sprintf("%04o", cfg.FileMode.Perm())).
		Str("dir_mode", fmt.Sprintf("%04o", cfg.DirMode.Perm())).
		Msg("Configuration loaded")

	return cfg, nil
}

func (c *Config) Validate() error {
	if c.Label == "" {
		return fmt.Errorf("%s is required", EnvLabel)
	}
	if c.Folder == "" {
		return fmt.Errorf("%s is required", EnvFolder)
	}
	if len(c.Namespaces) == 0 {
		return fmt.Errorf("%s must include at least one namespace", EnvNamespace)
	}
	for _, namespace := range c.Namespaces {
		if strings.TrimSpace(namespace) == "" {
			return fmt.Errorf("%s must not include empty namespaces", EnvNamespace)
		}
	}
	if c.LabelType != "label" && c.LabelType != "annotation" {
		return fmt.Errorf("%s must be 'label' or 'annotation'", EnvLabelType)
	}
	if c.LogFormat != "json" && c.LogFormat != "logfmt" {
		return fmt.Errorf("%s must be 'json' or 'logfmt'", EnvLogFormat)
	}
	return nil
}

func parseNamespaces(value string) []string {
	parts := strings.Split(value, ",")
	namespaces := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			namespaces = append(namespaces, part)
		}
	}
	return namespaces
}

func getCurrentNamespace() (string, error) {
	data, err := os.ReadFile(namespaceFilePath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// parseMode parses an octal permission string (e.g. "0644") into an os.FileMode.
// An empty value yields defaultMode. The mode is validated to reject setuid,
// setgid, and sticky bits (>0777) which make no sense for synced config files.
func parseMode(envName, value string, defaultMode os.FileMode) (os.FileMode, error) {
	if value == "" {
		return defaultMode, nil
	}
	n, err := strconv.ParseUint(value, 8, 16)
	if err != nil {
		return 0, fmt.Errorf("%s must be an octal permission mode (e.g. 0644): %w", envName, err)
	}
	if n > 0o777 {
		return 0, fmt.Errorf("%s value %s includes setuid/setgid/sticky bits; use a mode of 0777 or less", envName, value)
	}
	return os.FileMode(n), nil
}
