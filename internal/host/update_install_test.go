package host

// The "install and restart" handoff (TRANSPORT §5.1.12): the gateway cannot
// replace its own executable, so it writes a marker and stops; the host applies
// the staged update once the gateway is gone. These tests pin both ends without
// running an installer (the installer seams are swapped).

import (
	"os"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestInstallRequestIsConsumedEvenWhenNothingIsStaged: a marker with no plan must
// be dropped, not retried on every start.
func TestInstallRequestIsConsumedEvenWhenNothingIsStaged(t *testing.T) {
	dataDir := t.TempDir()
	logger := NewLogger(dataDir)
	t.Cleanup(func() { _ = logger.Close() })

	if err := contracts.RequestUpdateInstallNow(dataDir); err != nil {
		t.Fatalf("RequestUpdateInstallNow: %v", err)
	}
	maybeInstallRequestedUpdate(dataDir, logger)
	if contracts.UpdateInstallNowRequested(dataDir) {
		t.Fatal("the request marker survived a failed install; it would repeat on every start")
	}
}

// TestNoMarkerMeansNoInstall: a staged update stays queued for the next start
// unless the user asked for it now. The seam counts installer launches.
func TestNoMarkerMeansNoInstall(t *testing.T) {
	dataDir := t.TempDir()
	logger := NewLogger(dataDir)
	t.Cleanup(func() { _ = logger.Close() })

	launches := 0
	originalExecutable := updateExecutable
	originalLaunch := updateLaunch
	updateExecutable = func() (string, error) { return os.Args[0], nil }
	updateLaunch = func(string, ...string) error { launches++; return nil }
	t.Cleanup(func() { updateExecutable = originalExecutable; updateLaunch = originalLaunch })

	maybeInstallRequestedUpdate(dataDir, logger)
	if launches != 0 {
		t.Fatalf("an installer ran (%d) without a request", launches)
	}
}

// TestStaleMarkerIsClearedAtStartup: a marker left by a crash between the request
// and the install would otherwise install without anyone asking in this run.
func TestStaleMarkerIsClearedAtStartup(t *testing.T) {
	dataDir := t.TempDir()
	logger := NewLogger(dataDir)
	t.Cleanup(func() { _ = logger.Close() })

	if err := contracts.RequestUpdateInstallNow(dataDir); err != nil {
		t.Fatalf("RequestUpdateInstallNow: %v", err)
	}
	clearStaleInstallMarker(dataDir, logger)
	if _, err := os.Stat(contracts.UpdateInstallNowPath(dataDir)); !os.IsNotExist(err) {
		t.Fatalf("stale marker not cleared: %v", err)
	}
	// Clearing an absent marker is fine.
	clearStaleInstallMarker(dataDir, logger)
}
