package shell

import "testing"

// TestBuildInfoDefaultsAndString pins the build metadata contract the app and
// cmd/main wire: defaults are printable, SetBuildInfo replaces them, and the
// one-line string carries the version plus the injected commit/build time.
func TestBuildInfoDefaultsAndString(t *testing.T) {
	oldVersion, oldCommit, oldTime := BuildVersion, BuildCommit, BuildTime
	t.Cleanup(func() { BuildVersion, BuildCommit, BuildTime = oldVersion, oldCommit, oldTime })

	if got := BuildVersionString(); got != "dev" {
		t.Fatalf("default BuildVersionString = %q, want dev", got)
	}

	SetBuildInfo("0.1.0", "abc123def456789", "2026-11-05T23:00:00Z")
	if got := BuildVersionString(); got != "0.1.0 (commit abc123def456, built 2026-11-05T23:00:00Z)" {
		t.Fatalf("BuildVersionString = %q", got)
	}

	// Only the version: no dangling parentheses.
	SetBuildInfo("0.2.0", "", "")
	BuildCommit, BuildTime = "unknown", "unknown"
	if got := BuildVersionString(); got != "0.2.0" {
		t.Fatalf("version-only BuildVersionString = %q", got)
	}

	// Empty input never erases an earlier value.
	SetBuildInfo("", "", "")
	if BuildVersion != "0.2.0" {
		t.Fatalf("empty SetBuildInfo erased the version: %q", BuildVersion)
	}
}
