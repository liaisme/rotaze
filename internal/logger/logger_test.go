package logger

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Level != "info" {
		t.Errorf("Level = %s, want info", cfg.Level)
	}
	if cfg.File != "" {
		t.Errorf("File = %s, want empty", cfg.File)
	}
	if !cfg.Stdout {
		t.Error("Stdout should be true")
	}
}

func TestNew_Stdout(t *testing.T) {
	cfg := Config{
		Level:  "info",
		Stdout: true,
	}

	log, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer log.Sync()

	// Should not panic
	log.Info("test message")
	log.Warn("test warning")
	log.Error("test error")
}

func TestNew_DebugLevel(t *testing.T) {
	cfg := Config{
		Level:  "debug",
		Stdout: true,
	}

	log, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer log.Sync()

	log.Debug("debug message")
	log.Info("info message")
}

func TestNew_FileOutput(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	cfg := Config{
		Level:  "info",
		File:   logFile,
		Stdout: false,
	}

	log, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer log.Sync()

	log.Info("file log message")

	// File should exist
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		t.Error("log file should exist")
	}

	// File should have content
	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if len(content) == 0 {
		t.Error("log file should not be empty")
	}
}

func TestNew_InvalidLevel(t *testing.T) {
	cfg := Config{
		Level:  "invalid",
		Stdout: true,
	}

	// Should fallback to info level
	log, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer log.Sync()

	// Should not panic
	log.Info("test")
}

func TestNew_BothStdoutAndFile(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "both.log")

	cfg := Config{
		Level:  "info",
		File:   logFile,
		Stdout: true,
	}

	log, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer log.Sync()

	log.Info("both output message")

	// File should exist
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		t.Error("log file should exist")
	}
}

func TestInitLogger(t *testing.T) {
	cfg := DefaultConfig()
	log, err := InitLogger(cfg)
	if err != nil {
		t.Fatalf("InitLogger() error = %v", err)
	}
	defer log.Sync()

	log.Info("init logger test")
}
