// Package config handles configuration loading and management.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds the full Rotaze configuration.
type Config struct {
	Checker  CheckerConfig  `mapstructure:"checker"`
	Rotation RotationConfig `mapstructure:"rotation"`
	Proxy    ProxyConfig    `mapstructure:"proxy"`
	Cache    CacheConfig    `mapstructure:"cache"`
	Logging  LoggingConfig  `mapstructure:"logging"`
}

// CheckerConfig holds URL monitoring settings.
type CheckerConfig struct {
	URLs          []string `mapstructure:"urls"`
	Interval      int      `mapstructure:"interval"`       // seconds
	MaxRetries    int      `mapstructure:"max_retries"`
	Timeout       int      `mapstructure:"timeout"`        // seconds
	CheckIP       bool     `mapstructure:"check_ip"`
	Country       string   `mapstructure:"country"`
	MaxAttempts   int      `mapstructure:"max_attempts"`   // consecutive failures before rotate
}

// RotationConfig holds rotation settings.
type RotationConfig struct {
	Strategy       string  `mapstructure:"strategy"` // smart, random
	MinSuccessRate float64 `mapstructure:"min_success_rate"`
	CooldownPeriod int     `mapstructure:"cooldown_period"` // seconds
}

// ProxyConfig holds proxy listener settings.
type ProxyConfig struct {
	ListenPort int    `mapstructure:"listen_port"`
	ListenAddr string `mapstructure:"listen_addr"`
	ProxyType  string `mapstructure:"proxy_type"` // socks5
}

// CacheConfig holds database settings.
type CacheConfig struct {
	DBPath   string `mapstructure:"db_path"`
	NodeTTL  int    `mapstructure:"node_ttl"` // seconds
	MaxNodes int    `mapstructure:"max_nodes"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	File   string `mapstructure:"file"`
	Stdout bool   `mapstructure:"stdout"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".rotaze", "state.db")
	logFile := filepath.Join(home, ".rotaze", "rotaze.log")

	return &Config{
		Checker: CheckerConfig{
			URLs:        []string{"https://toplinks.io/kSDVNrX"},
			Interval:    30,
			MaxRetries:  3,
			Timeout:     15,
			CheckIP:     false,
			Country:     "VN",
			MaxAttempts: 3,
		},
		Rotation: RotationConfig{
			Strategy:       "smart",
			MinSuccessRate: 0.75,
			CooldownPeriod: 300,
		},
		Proxy: ProxyConfig{
			ListenPort: 1080,
			ListenAddr: "127.0.0.1",
			ProxyType:  "socks5",
		},
		Cache: CacheConfig{
			DBPath:   dbPath,
			NodeTTL:  3600,
			MaxNodes: 200,
		},
		Logging: LoggingConfig{
			Level:  "info",
			File:   logFile,
			Stdout: true,
		},
	}
}

// LoadConfig loads configuration from file, with env var overrides.
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()

	v := viper.New()

	// Set defaults from default config
	v.SetDefault("checker.urls", cfg.Checker.URLs)
	v.SetDefault("checker.interval", cfg.Checker.Interval)
	v.SetDefault("checker.max_retries", cfg.Checker.MaxRetries)
	v.SetDefault("checker.timeout", cfg.Checker.Timeout)
	v.SetDefault("checker.check_ip", cfg.Checker.CheckIP)
	v.SetDefault("checker.country", cfg.Checker.Country)
	v.SetDefault("checker.max_attempts", cfg.Checker.MaxAttempts)

	v.SetDefault("rotation.strategy", cfg.Rotation.Strategy)
	v.SetDefault("rotation.min_success_rate", cfg.Rotation.MinSuccessRate)
	v.SetDefault("rotation.cooldown_period", cfg.Rotation.CooldownPeriod)

	v.SetDefault("proxy.listen_port", cfg.Proxy.ListenPort)
	v.SetDefault("proxy.listen_addr", cfg.Proxy.ListenAddr)
	v.SetDefault("proxy.proxy_type", cfg.Proxy.ProxyType)

	v.SetDefault("cache.db_path", cfg.Cache.DBPath)
	v.SetDefault("cache.node_ttl", cfg.Cache.NodeTTL)
	v.SetDefault("cache.max_nodes", cfg.Cache.MaxNodes)

	v.SetDefault("logging.level", cfg.Logging.Level)
	v.SetDefault("logging.file", cfg.Logging.File)
	v.SetDefault("logging.stdout", cfg.Logging.Stdout)

	// Env var overrides (ROTAZE_CHECKER_COUNTRY=US)
	v.SetEnvPrefix("ROTAZE")
	v.AutomaticEnv()

	// Config file
	if path != "" {
		v.SetConfigFile(path)
	} else {
		home, _ := os.UserHomeDir()
		v.SetConfigName("config")
		v.SetConfigType("toml")
		v.AddConfigPath(filepath.Join(home, ".rotaze"))
		v.AddConfigPath(".")
	}

	// Read config file (optional)
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config: %w", err)
		}
		// Config file not found is OK, use defaults
	}

	// Unmarshal into config struct
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// ReloadConfig reloads configuration from file, useful for SIGHUP handler.
// Returns nil if config file hasn't changed.
func ReloadConfig(path string) (*Config, error) {
	return LoadConfig(path)
}

// ValidateConfig checks the config for valid values.
func ValidateConfig(cfg *Config) error {
	// Checker validation
	if cfg.Checker.Interval < 5 || cfg.Checker.Interval > 3600 {
		return fmt.Errorf("checker.interval must be between 5 and 3600 seconds")
	}
	if cfg.Checker.Timeout < 1 || cfg.Checker.Timeout > 60 {
		return fmt.Errorf("checker.timeout must be between 1 and 60 seconds")
	}
	if cfg.Checker.MaxRetries < 0 || cfg.Checker.MaxRetries > 10 {
		return fmt.Errorf("checker.max_retries must be between 0 and 10")
	}
	if cfg.Checker.Country == "" {
		cfg.Checker.Country = "all"
	}

	// Rotation validation
	if cfg.Rotation.Strategy != "smart" && cfg.Rotation.Strategy != "random" {
		return fmt.Errorf("rotation.strategy must be 'smart' or 'random'")
	}
	if cfg.Rotation.MinSuccessRate < 0 || cfg.Rotation.MinSuccessRate > 1 {
		return fmt.Errorf("rotation.min_success_rate must be between 0 and 1")
	}

	// Proxy validation
	if cfg.Proxy.ListenPort < 1 || cfg.Proxy.ListenPort > 65535 {
		return fmt.Errorf("proxy.listen_port must be between 1 and 65535")
	}
	if cfg.Proxy.ProxyType != "socks5" {
		return fmt.Errorf("proxy.proxy_type must be 'socks5'")
	}

	// Cache validation
	if cfg.Cache.NodeTTL < 60 {
		return fmt.Errorf("cache.node_ttl must be at least 60 seconds")
	}
	if cfg.Cache.MaxNodes < 10 {
		return fmt.Errorf("cache.max_nodes must be at least 10")
	}

	// Logging validation
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[cfg.Logging.Level] {
		return fmt.Errorf("logging.level must be one of: debug, info, warn, error")
	}

	return nil
}
