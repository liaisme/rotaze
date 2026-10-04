package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"tilde slash", "~/.rotaze/state.db", filepath.Join(home, ".rotaze", "state.db")},
		{"bare tilde", "~", home},
		{"tilde with trailing slash", "~/.rotaze/", filepath.Join(home, ".rotaze")},
		{"absolute stays", "/var/lib/rotaze/state.db", "/var/lib/rotaze/state.db"},
		{"relative stays", "data/state.db", "data/state.db"},
		{"tilde in middle stays", "/opt/~/state.db", "/opt/~/state.db"},
		{"user form stays", "~root/.rotaze/state.db", "~root/.rotaze/state.db"},
		{"empty stays", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expandPath(tt.in); got != tt.want {
				t.Errorf("expandPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestLoadConfig_ExpandsTildePaths is the regression guard for the bug this
// fixes: the shipped config.example.toml writes db_path = "~/.rotaze/state.db",
// and every consumer (the SQLite cache, the log file, and the data dir the
// rotator writes sing-box configs into) took that string literally, creating a
// directory named "~" under the working directory.
func TestLoadConfig_ExpandsTildePaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	body := "db_path = \"~/.rotaze/state.db\"\n" +
		"log_file_unused = \"\"\n" +
		"[logging]\nfile = \"~/.rotaze/rotaze.log\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	wantDB := filepath.Join(home, ".rotaze", "state.db")
	if cfg.Cache.DBPath != wantDB {
		t.Errorf("db_path = %q, want %q", cfg.Cache.DBPath, wantDB)
	}
	if !filepath.IsAbs(cfg.Cache.DBPath) {
		t.Errorf("db_path %q is not absolute", cfg.Cache.DBPath)
	}
	wantLog := filepath.Join(home, ".rotaze", "rotaze.log")
	if cfg.Logging.File != wantLog {
		t.Errorf("logging.file = %q, want %q", cfg.Logging.File, wantLog)
	}
}
