package logger

import (
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func Init(level, logFormat string) {
	switch strings.ToLower(level) {
	case "trace":
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	switch strings.ToLower(logFormat) {
	case "json":
		log.Logger = log.Output(os.Stdout)
	case "logfmt":
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})
	default:
		log.Logger = log.Output(os.Stdout)
	}

	log.Logger = log.With().Caller().Logger()
}

func GetLogger() zerolog.Logger {
	return log.Logger
}
