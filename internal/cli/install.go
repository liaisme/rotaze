package cli

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// defaultSingboxVersion is the fallback sing-box release used when the
// latest-version lookup fails (e.g. offline).
const defaultSingboxVersion = "v1.13.19"

// singboxReleasesAPI returns the GitHub API endpoint used to find the latest
// stable sing-box release. Overridable for tests.
var singboxReleasesAPI = "https://api.github.com/repos/SagerNet/sing-box/releases/latest"

// latestSingboxVersion queries the GitHub API for the latest stable sing-box
// release tag (e.g. "v1.14.0"). Falls back to defaultSingboxVersion on any
// error so the installer still works offline.
func latestSingboxVersion() (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(singboxReleasesAPI)
	if err != nil {
		return "", fmt.Errorf("querying latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("querying latest release: HTTP %d", resp.StatusCode)
	}

	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", fmt.Errorf("decoding latest release: %w", err)
	}
	if !strings.HasPrefix(rel.TagName, "v") {
		return "", fmt.Errorf("unexpected tag %q", rel.TagName)
	}
	return rel.TagName, nil
}

// singboxArch maps runtime.GOARCH to the sing-box release architecture name.
func singboxArch(goarch string) (string, bool) {
	switch goarch {
	case "amd64":
		return "amd64", true
	case "arm64":
		return "arm64", true
	case "arm":
		return "armv7", true
	default:
		return "", false
	}
}

// downloadAndInstallSingbox downloads a sing-box tarball and extracts the
// sing-box binary into destDir (created if missing), without needing root.
func downloadAndInstallSingbox(url, destDir string, out io.Writer) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d from %s", resp.StatusCode, url)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("invalid tarball: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid tarball: %w", err)
		}
		if filepath.Base(hdr.Name) != "sing-box" {
			continue
		}

		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return err
		}
		outPath := filepath.Join(destDir, "sing-box")
		f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		if err := os.Chmod(outPath, 0o755); err != nil {
			return err
		}
		fmt.Fprintf(out, "Installed sing-box to %s\n", outPath)
		return nil
	}

	return fmt.Errorf("sing-box binary not found in tarball")
}

// newInstallSingboxCmd creates the install-singbox command. It is a standalone
// utility: it works even without a config/cache (see root.go's skip list).
func newInstallSingboxCmd() *cobra.Command {
	var (
		version string
		dir     string
	)

	cmd := &cobra.Command{
		Use:   "install-singbox",
		Short: "Download and install the sing-box binary (no sudo needed)",
		Long: `Downloads the sing-box binary for your architecture and installs it to
~/.local/bin (or --dir), which rotaze checks automatically when starting a proxy.

No root privileges required. Useful on ARM devices where a prebuilt
rotaze binary is deployed without sing-box.`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			if version == "" {
				latest, err := latestSingboxVersion()
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not determine latest version (%v); using %s\n", err, defaultSingboxVersion)
					version = defaultSingboxVersion
				} else {
					version = latest
				}
			}
			if dir == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					fmt.Fprintf(os.Stderr, "cannot determine home directory: %v\n", err)
					os.Exit(1)
				}
				dir = filepath.Join(home, ".local", "bin")
			}

			arch, ok := singboxArch(runtime.GOARCH)
			if !ok {
				fmt.Fprintf(os.Stderr, "unsupported architecture: %s\n", runtime.GOARCH)
				os.Exit(1)
			}

			// Release tag keeps the v prefix (v1.13.19) but the asset filename drops it.
			asset := strings.TrimPrefix(version, "v")
			url := fmt.Sprintf("https://github.com/SagerNet/sing-box/releases/download/%s/sing-box-%s-linux-%s.tar.gz",
				version, asset, arch)
			fmt.Printf("Downloading sing-box %s (linux/%s)...\n", version, arch)
			if err := downloadAndInstallSingbox(url, dir, os.Stdout); err != nil {
				fmt.Fprintf(os.Stderr, "Failed: %v\n", err)
				os.Exit(1)
			}
		},
	}

	cmd.Flags().StringVar(&version, "version", "", "sing-box release to download (default: latest stable)")
	cmd.Flags().StringVar(&dir, "dir", "", "Install directory (default ~/.local/bin)")
	return cmd
}
