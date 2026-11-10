package host

// Tests for the host's immediate-install command: the palette/settings action
// ("install update and restart") that applies an already downloaded update
// without waiting for a restart. Registration goes through the UI root's command
// surface; the process and quit seams are injected so no real installer starts
// and the mygo app is never quit from a test.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// registeredCommand finds one command in the UI root's command surface.
func registeredCommand(t *testing.T, uiRoot *uiroot.Root, id string) contracts.CommandSpec {
	t.Helper()
	for _, command := range uiRoot.Runtime().Commands() {
		if command.ID == id {
			return command
		}
	}
	ids := make([]string, 0, len(uiRoot.Runtime().Commands()))
	for _, command := range uiRoot.Runtime().Commands() {
		ids = append(ids, command.ID)
	}
	t.Fatalf("command %q not registered; commands = %v", id, ids)
	return contracts.CommandSpec{}
}

// TestUpdateInstallCommandEnabledOnlyWithPendingPlan covers F7 for the palette:
// the command is always registered (the settings page looks it up before it
// schedules the download and ignores Enabled), but the palette only lists it
// while a plan is actually pending.
func TestUpdateInstallCommandEnabledOnlyWithPendingPlan(t *testing.T) {
	dataDir := t.TempDir()
	uiRoot := uiroot.New(uiroot.Config{AppName: appName})
	logger := &trayRecordingLogger{}
	unregister, err := registerUpdateInstallCommand(uiRoot, dataDir, logger)
	if err != nil {
		t.Fatalf("registerUpdateInstallCommand: %v", err)
	}
	t.Cleanup(unregister)

	command := registeredCommand(t, uiRoot, updateInstallCommandID)
	if command.Title != "安装更新并重启" {
		t.Fatalf("command title = %q, want 安装更新并重启", command.Title)
	}
	if command.Run == nil {
		t.Fatal("command has no Run")
	}
	if command.Enabled == nil {
		t.Fatal("command has no Enabled hook: the palette would list it without a plan")
	}
	if command.Enabled() {
		t.Fatal("command enabled without a pending plan")
	}

	// The settings page's style of lookup (presence, not Enabled) must still find
	// the command before any download happened: it decides whether the primary
	// action can say "install and restart".
	if !updateInstallCommandPresence(uiRoot) {
		t.Fatal("settings-style lookup did not find the command before a plan existed")
	}

	// With a pending plan the palette may list and run it.
	staged := filepath.Join(dataDir, "updates", "zeokeel-setup-1.3.0-amd64.exe")
	writePendingUpdate(t, dataDir, pendingInstallFile{
		Version: "1.3.0", Path: staged, SHA256: "abc", Kind: installKindInstaller,
	}, "setup bytes")
	if !command.Enabled() {
		t.Fatal("command disabled with a pending installer plan")
	}

	exe := filepath.Join(t.TempDir(), "zeokeel.exe")
	stubUpdateExecutable(t, exe)
	calls := make([]updateCall, 0)
	stubUpdateLaunch(t, &calls, nil)
	quits := 0
	previousQuit := quitApplication
	quitApplication = func() { quits++ }
	t.Cleanup(func() { quitApplication = previousQuit })
	if err := command.Run(); err != nil {
		t.Fatalf("Run with an installer plan: %v", err)
	}
	if quits != 1 {
		t.Fatalf("quit requests = %d, want exactly 1 after a successful install launch", quits)
	}
	requireHelperLaunch(t, calls, exe, updateHelperInstallAfterExit, staged)

	// Unregistering removes the command again (assembly/window lifetime).
	unregister()
	removed := true
	for _, registered := range uiRoot.Runtime().Commands() {
		if registered.ID == updateInstallCommandID {
			removed = false
		}
	}
	if !removed {
		t.Fatal("command still registered after unregister")
	}
}

// TestUpdateInstallCommandWithoutPlanFailsClearly keeps the palette invocation
// safe: even if the command is run without a plan, Run returns the no-update
// error, starts nothing and quits nothing.
func TestUpdateInstallCommandWithoutPlanFailsClearly(t *testing.T) {
	dataDir := t.TempDir()
	uiRoot := uiroot.New(uiroot.Config{AppName: appName})
	logger := &trayRecordingLogger{}
	unregister, err := registerUpdateInstallCommand(uiRoot, dataDir, logger)
	if err != nil {
		t.Fatalf("registerUpdateInstallCommand: %v", err)
	}
	t.Cleanup(unregister)
	command := registeredCommand(t, uiRoot, updateInstallCommandID)

	calls := make([]updateCall, 0)
	stubUpdateLaunch(t, &calls, nil)
	quits := 0
	previousQuit := quitApplication
	quitApplication = func() { quits++ }
	t.Cleanup(func() { quitApplication = previousQuit })

	if err := command.Run(); err == nil || !strings.Contains(err.Error(), "no downloaded update") {
		t.Fatalf("Run without a plan = %v, want the clear no-update error", err)
	}
	if len(calls) != 0 || quits != 0 {
		t.Fatalf("no-plan run started processes %+v and quit %d times, want none", calls, quits)
	}
}

// updateInstallCommandPresence mirrors the settings page's lookup: it needs the
// command to exist, not to be Enabled (which only the palette filters on).
func updateInstallCommandPresence(uiRoot *uiroot.Root) bool {
	for _, command := range uiRoot.Runtime().Commands() {
		if command.ID == updateInstallCommandID && command.Run != nil {
			return true
		}
	}
	return false
}
