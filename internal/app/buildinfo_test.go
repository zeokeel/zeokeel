package app

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/shell"
)

// TestBuildInfoForwarding covers the wave-2 build metadata contract: main
// injects version/commit/time through app.SetBuildInfo, app.Run forwards them
// to internal/shell, and the About/Diagnostics version string shows them.
func TestBuildInfoForwarding(t *testing.T) {
	prevVersion, prevCommit, prevTime := BuildVersion, BuildCommit, BuildTime
	prevShell := [3]string{shell.BuildVersion, shell.BuildCommit, shell.BuildTime}
	t.Cleanup(func() {
		BuildVersion, BuildCommit, BuildTime = prevVersion, prevCommit, prevTime
		shell.BuildVersion, shell.BuildCommit, shell.BuildTime = prevShell[0], prevShell[1], prevShell[2]
	})

	if got := buildInfoString(); got != "dev" {
		t.Errorf("default build info = %q, want dev", got)
	}
	SetBuildInfo("0.1.0", "abcdef1234567890", "2026-11-05T00:00:00Z")
	publishBuildInfo()
	got := shell.BuildVersionString()
	for _, want := range []string{"0.1.0", "commit abcdef123456", "built 2026-11-05T00:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("shell.BuildVersionString() = %q, missing %q", got, want)
		}
	}
	if got := buildInfoString(); !strings.Contains(got, "0.1.0") || !strings.Contains(got, "commit abcdef123456") {
		t.Errorf("app buildInfoString() = %q", got)
	}

	// Empty values never erase what an earlier call (or the build) provided.
	SetBuildInfo("", "", "")
	if BuildVersion != "0.1.0" || BuildCommit != "abcdef1234567890" {
		t.Errorf("empty SetBuildInfo overwrote values: %q %q", BuildVersion, BuildCommit)
	}
}
