// Package cli provides Cobra command definitions for Rotaze.
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/rotaze/rotaze/internal/cache"
	"github.com/rotaze/rotaze/internal/config"
	"github.com/rotaze/rotaze/internal/logger"
	"github.com/rotaze/rotaze/internal/version"
)

var (
	// Global flags
	configPath string
	verbose    bool
	logFile    string

	// Global instances (initialized in PersistentPreRun)
	globalConfig *config.Config
	globalCache  *cache.Cache
	globalLogger *zap.Logger
)

// verbosef prints an extra diagnostic line only when --verbose/-v is set.
func verbosef(format string, args ...interface{}) {
	if verbose {
		fmt.Fprintf(os.Stdout, "[debug] "+format+"\n", args...)
	}
}

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:   "rotaze",
	Short: "Self-healing SOCKS5 proxy with automatic IP rotation",
	Long: `Rotaze is a lightweight, self-healing SOCKS5/HTTP proxy server that
automatically rotates through free VPN tunnels when the target URL becomes
unreachable.

It exposes a local SOCKS5/HTTP proxy (127.0.0.1:1080; the "mixed" inbound
accepts both socks5:// and http:// clients) that applications can connect to,
while a background monitoring process checks a user-specified target URL. When
the URL fails to respond, Rotaze intelligently selects the next best-performing
tunnel and switches traffic through it—all without disrupting existing
connections.

Examples:
  rotaze run --url https://example.com
  rotaze watch --url https://example.com --interval 30
  rotaze daemon start
  rotaze pool list --country VN
  rotaze use 5
  rotaze interactive`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Skip initialization for help/version/install-singbox commands
		switch cmd.Name() {
		case "help", "version", "install-singbox":
			return
		}

		// Load configuration
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(2)
		}
		globalConfig = cfg

		// Initialize logger
		logCfg := logger.Config{
			Level:  cfg.Logging.Level,
			File:   cfg.Logging.File,
			Stdout: cfg.Logging.Stdout,
		}
		if verbose {
			logCfg.Level = "debug"
		}
		if logFile != "" {
			logCfg.File = logFile
		}

		l, err := logger.New(logCfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing logger: %v\n", err)
			os.Exit(2)
		}
		globalLogger = l

		// Initialize cache
		c, err := cache.NewCache(cfg.Cache.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing cache: %v\n", err)
			os.Exit(2)
		}
		globalCache = c
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		if globalCache != nil {
			globalCache.Close()
		}
		if globalLogger != nil {
			globalLogger.Sync()
		}
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "Path to config file (TOML)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging (debug level)")
	rootCmd.PersistentFlags().StringVar(&logFile, "log-file", "", "Write logs to file")

	// Add subcommands
	rootCmd.AddCommand(newRunCmd())
	rootCmd.AddCommand(newWatchCmd())
	rootCmd.AddCommand(newDaemonCmd())
	rootCmd.AddCommand(newPoolCmd())
	rootCmd.AddCommand(newUseCmd())
	rootCmd.AddCommand(newLockCmd())
	rootCmd.AddCommand(newStatusCmd())
	rootCmd.AddCommand(newInteractiveCmd())
	rootCmd.AddCommand(newVersionCmd())
	rootCmd.AddCommand(newInstallSingboxCmd())
}

// getDataDir returns the data directory from the config's DB path.
func getDataDir(cfg *config.Config) string {
	dbPath := cfg.Cache.DBPath
	// If DBPath ends with /state.db, strip it to get the data directory
	if len(dbPath) > len("state.db") && dbPath[len(dbPath)-len("state.db"):] == "state.db" {
		return dbPath[:len(dbPath)-len("state.db")]
	}
	// Fall back to parent directory
	return filepath.Dir(dbPath)
}

// newVersionCmd creates the version command.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version number",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("rotaze %s\n", version.Full())
		},
	}
}
