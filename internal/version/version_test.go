package version

import (
	"strings"
	"testing"
)

func TestFull(t *testing.T) {
	v := Full()
	if v == "" {
		t.Error("Full() returned empty string")
	}
}

func TestFull_Default(t *testing.T) {
	// When not set via ldflags, Version defaults to "dev"
	v := Full()
	// Should contain version info
	if !strings.Contains(v, "dev") && !strings.Contains(v, "commit:") {
		t.Errorf("Full() should contain version info, got %s", v)
	}
}

func TestVersion_Variables(t *testing.T) {
	// Variables should be accessible (even if empty)
	_ = Version
	_ = GitCommit
	_ = BuildDate
}
