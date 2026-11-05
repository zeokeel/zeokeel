package app

import (
	"context"
	"errors"
	"testing"

	"github.com/zeokeel/zeokeel/internal/shell"
	"github.com/zeokeel/zeokeel/internal/updater"
)

// TestInstallShellUpdateHookWiring covers the Round-4 GUI seam: startServices
// installs an adapter that mirrors app.DownloadAndInstallUpdate into the
// shell's outcome type (so the Updates page can show progress/provenance).
func TestInstallShellUpdateHookWiring(t *testing.T) {
	feed := newUpdateFeed(t, "9.9.9", []byte("shell-wired-update"), false)
	a, _ := updateTestApp(t, feed.server.URL+"/feed.json")
	fake := &fakeUpdateReplacer{}
	a.updateReplacer = fake

	var installed shell.UpdateInstallHook
	prev := setShellInstallUpdateHook
	setShellInstallUpdateHook = func(fn shell.UpdateInstallHook) { installed = fn }
	t.Cleanup(func() { setShellInstallUpdateHook = prev })
	// Before wiring, the shell has no app hook (its built-in fallback applies).
	if installed != nil {
		t.Fatal("shell install hook was injected before startServices/installShellUpdateHook")
	}
	a.installShellUpdateHook()
	if installed == nil {
		t.Fatal("installShellUpdateHook did not install the shell seam")
	}

	var progressCalls int
	outcome, err := installed(context.Background(), func(int64, int64) { progressCalls++ })
	if err != nil {
		t.Fatalf("shell install hook: %v", err)
	}
	if outcome == nil || !outcome.Staged || !outcome.Applied || outcome.ReleaseVersion != "9.9.9" {
		t.Fatalf("outcome = %+v, want staged+applied 9.9.9", outcome)
	}
	if progressCalls == 0 {
		t.Fatal("progress callback never ran through the shell seam")
	}
	if fake.calls() != 1 {
		t.Fatalf("replace calls = %d, want 1", fake.calls())
	}
}

// TestInstallShellUpdateHookNoFeed covers the fallback/no-configuration case:
// the adapter reports updater.ErrDisabled, which the shell turns into the
// "no update feed" message instead of trying the network.
func TestInstallShellUpdateHookNoFeed(t *testing.T) {
	a, _ := updateTestApp(t, "")
	if _, err := a.shellInstallUpdate(context.Background(), nil); !errors.Is(err, updater.ErrDisabled) {
		t.Fatalf("shellInstallUpdate without feed = %v, want updater.ErrDisabled", err)
	}
}

// TestInstallShellUpdateHookNilSafe keeps the seam testable after injection
// removal: clearing the package setter must not panic (tests and embedders
// replace it).
func TestInstallShellUpdateHookNilSafe(t *testing.T) {
	a, _ := updateTestApp(t, "")
	prev := setShellInstallUpdateHook
	setShellInstallUpdateHook = nil
	t.Cleanup(func() { setShellInstallUpdateHook = prev })
	a.installShellUpdateHook() // must be a no-op, not a panic
}
