// Package logger provides structured logging using Zap.
package logger

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Config holds logger configuration.
type Config struct {
	Level  string // debug, info, warn, error
	File   string // log file path (empty = stdout only)
	Stdout bool   // also log to stdout (default: true)
}

// DefaultConfig returns a logger config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Level:  "info",
		File:   "",
		Stdout: true,
	}
}

// New creates a new Zap logger from config.
func New(cfg Config) (*zap.Logger, error) {
	level, err := zapcore.ParseLevel(cfg.Level)
	if err != nil {
		level = zapcore.InfoLevel
	}

	// Build encoder config
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "ts"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderCfg.EncodeLevel = zapcore.CapitalLevelEncoder
	encoderCfg.EncodeDuration = zapcore.MillisDurationEncoder

	var cores []zapcore.Core

	// Stdout core
	if cfg.Stdout || cfg.File == "" {
		stdoutSyncer := zapcore.Lock(os.Stdout)
		cores = append(cores, zapcore.NewCore(
			zapcore.NewConsoleEncoder(encoderCfg),
			stdoutSyncer,
			level,
		))
	}

	// File core
	if cfg.File != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.File), 0755); err != nil {
			return nil, err
		}

		f, err := os.OpenFile(cfg.File, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, err
		}

		fileSyncer := zapcore.AddSync(f)
		// Use JSON encoder for files
		cores = append(cores, zapcore.NewCore(
			zapcore.NewJSONEncoder(encoderCfg),
			fileSyncer,
			level,
		))
	}

	core := zapcore.NewTee(cores...)
	return zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel)), nil
}

// InitLogger initializes the global logger and returns it.
func InitLogger(cfg Config) (*zap.Logger, error) {
	return New(cfg)
}
