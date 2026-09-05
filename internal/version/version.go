// Package version holds build-time version information.
package version

// These variables are set at build time via -ldflags.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
	GoVersion = "unknown"
)

// Full returns a detailed version string.
func Full() string {
	return Version + " (commit: " + GitCommit + ", built: " + BuildDate + ", go: " + GoVersion + ")"
}
