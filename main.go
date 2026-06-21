package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/acidghost/k8s-fs-sidecar/internal/config"
	"github.com/acidghost/k8s-fs-sidecar/internal/k8s"
	"github.com/acidghost/k8s-fs-sidecar/internal/logger"
	"github.com/acidghost/k8s-fs-sidecar/internal/processor"
	"github.com/acidghost/k8s-fs-sidecar/internal/watcher"
	"github.com/rs/zerolog/log"
)

var (
	buildVersion string
	buildCommit  string
	buildDate    string
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatal().Err(err).Msg("Invalid configuration")
	}

	logger.Init(cfg.LogLevel, cfg.LogFormat)

	log.Info().
		Str("version", buildVersion).
		Str("commit", buildCommit).
		Str("date", buildDate).
		Msg("Starting k8s-fs-sidecar")

	if err := os.MkdirAll(cfg.Folder, cfg.DirMode); err != nil {
		log.Fatal().Err(err).Str("folder", cfg.Folder).Msg("Failed to create output folder")
	}

	clientset, err := k8s.NewClient()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create Kubernetes client")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	proc := processor.NewProcessor(cfg)

	log.Info().Msg("Starting watchers")

	for _, resource := range cfg.Resources {
		switch resource {
		case "configmap":
			watcher.WatchConfigMaps(ctx, clientset, proc, cfg)
		case "secret":
			watcher.WatchSecrets(ctx, clientset, proc, cfg)
		}
	}

	log.Info().Msg("k8s-fs-sidecar is running")

	<-ctx.Done()
	log.Info().Msg("Shutting down")
}
