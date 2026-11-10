package host

// Tests for the user-visible install half: InstallPendingUpdateNow applies an
// already staged update immediately instead of waiting for the next start.
// Windows runs the NSIS setup installer after the app quits (through this
// executable's hidden helper mode); macOS mounts the signed dmg and swaps the
// .app bundle, keeping the old bundle until the new version starts; the other
// layouts replace/relaunch the running executable. Every process and the
// executable path are injected seams: no real hdiutil/ditto/open/setup runs and
// the test binary is never replaced.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateCall is one recorded install process invocation.
type updateCall struct {
	kind string // "exec" (waits for the command) or "launch" (detached)
	name string
	args []string
}

// stubUpdateExec replaces the waiting-command seam for one test.
func stubUpdateExec(t *testing.T, calls *[]updateCall, fn func(name string, args ...string) error) {
	t.Helper()
	previous := updateExecCommand
	updateExecCommand = func(name string, args ...string) error {
		*calls = append(*calls, updateCall{kind: "exec", name: name, args: append([]string(nil), args...)})
		if fn == nil {
			return nil
		}
		return fn(name, args...)
	}
	t.Cleanup(func() { updateExecCommand = previous })
}

// stubUpdateLaunch replaces the detached-launch seam for one test.
func stubUpdateLaunch(t *testing.T, calls *[]updateCall, fn func(name string, args ...string) error) {
	t.Helper()
	previous := updateLaunch
	updateLaunch = func(name string, args ...string) error {
		*calls = append(*calls, updateCall{kind: "launch", name: name, args: append([]string(nil), args...)})
		if fn == nil {
			return nil
		}
		return fn(name, args...)
	}
	t.Cleanup(func() { updateLaunch = previous })
}

// stubUpdateExecutable points the "running executable" seam at path.
func stubUpdateExecutable(t *testing.T, path string) {
	t.Helper()
	previous := updateExecutable
	updateExecutable = func() (string, error) { return path, nil }
	t.Cleanup(func() { updateExecutable = previous })
}

// stubStagedHash replaces the staged-asset hashing seam and returns a counter of
// the calls (F10: startup must not hash plans it only skips, and must hash an
// applied plan exactly once).
func stubStagedHash(t *testing.T) *int {
	t.Helper()
	previous := stagedFileSHA256
	count := 0
	stagedFileSHA256 = func(path string) (string, error) {
		count++
		return previous(path)
	}
	t.Cleanup(func() { stagedFileSHA256 = previous })
	return &count
}

// requireHelperLaunch asserts one detached launch of exe in a hidden helper mode
// with target as the only argument (the launcher contract that replaced the
// cmd.exe/sh scripts).
func requireHelperLaunch(t *testing.T, calls []updateCall, exe, mode, target string) updateCall {
	t.Helper()
	for _, call := range calls {
		if call.kind != "launch" || call.name != exe {
			continue
		}
		if len(call.args) == 2 && call.args[0] == mode && call.args[1] == target {
			return call
		}
	}
	t.Fatalf("calls = %+v, want a helper launch %q %q %q", calls, exe, mode, target)
	return updateCall{}
}

// requireAppImageShellLaunch asserts the AppImage relaunch goes through the
// stable shell helper (path passed as an argv element, not interpolated).
func requireAppImageShellLaunch(t *testing.T, calls []updateCall, appImage string) {
	t.Helper()
	for _, call := range calls {
		if call.kind != "launch" || call.name != "sh" || len(call.args) != 4 {
			continue
		}
		if call.args[0] == "-c" && call.args[2] == "sh" && call.args[3] == appImage {
			return
		}
	}
	t.Fatalf("calls = %+v, want a stable shell relaunch for %q", calls, appImage)
}

// requireNoExecProcesses asserts the install path waited for no helper command
// (the updater no longer shells out to cmd.exe/sh).
func requireNoExecProcesses(t *testing.T, calls []updateCall) {
	t.Helper()
	for _, call := range calls {
		if call.kind == "exec" {
			t.Fatalf("calls = %+v, want no waiting helper commands", calls)
		}
	}
}

// findUpdateCall returns the first recorded call of kind/name.
func findUpdateCall(calls []updateCall, kind, name string) (updateCall, bool) {
	for _, call := range calls {
		if call.kind == kind && call.name == name {
			return call, true
		}
	}
	return updateCall{}, false
}

// updateArgAfter returns the argument following flag.
func updateArgAfter(args []string, flag string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == flag {
			return args[index+1]
		}
	}
	return ""
}

// writeExecutableFixture writes an executable file with mode 0755.
func writeExecutableFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fixture %q: %v", path, err)
	}
}

// writeDMGBundleFixture builds a fake macOS app bundle and returns its root and
// running executable path.
func writeDMGBundleFixture(t *testing.T) (bundle, executable string) {
	t.Helper()
	root := t.TempDir()
	bundle = filepath.Join(root, "ZeoKeel.app")
	executable = filepath.Join(bundle, "Contents", "MacOS", "zeokeel")
	writeExecutableFixture(t, executable, "old app")
	return bundle, executable
}

// writeRealisticDMGMount lays out what mygo's real dmg contains (F2): the .app
// bundle next to .DS_Store, .VolumeIcon.icns and the /Applications symlink.
func writeRealisticDMGMount(t *testing.T, mount, appName, appContent string) {
	t.Helper()
	if err := os.MkdirAll(mount, 0o755); err != nil {
		t.Fatalf("mkdir mount: %v", err)
	}
	for _, name := range []string{".DS_Store", ".VolumeIcon.icns"} {
		if err := os.WriteFile(filepath.Join(mount, name), []byte("finder metadata"), 0o644); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
	}
	if err := os.Symlink("/Applications", filepath.Join(mount, "Applications")); err != nil {
		t.Fatalf("symlink Applications: %v", err)
	}
	writeExecutableFixture(t, filepath.Join(mount, appName, "Contents", "MacOS", "zeokeel"), appContent)
}

// stubDittoCopy simulates ditto copying the mounted app to the target bundle.
func stubDittoCopy(t *testing.T) func(name string, args ...string) error {
	return func(name string, args ...string) error {
		if name != "ditto" {
			return nil
		}
		if len(args) != 2 {
			t.Fatalf("ditto args = %+v, want source + target", args)
		}
		if !strings.HasSuffix(strings.ToLower(args[0]), ".app") {
			t.Fatalf("ditto source = %q, want the mounted .app bundle", args[0])
		}
		writeExecutableFixture(t, filepath.Join(args[1], "Contents", "MacOS", "zeokeel"), "new app")
		return nil
	}
}

// requireNoFile asserts one path does not exist.
func requireNoFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("path %q still exists (err = %v)", path, err)
	}
}

// TestInstallPendingUpdateNowWithoutPlan covers the command's no-pending case: a
// clear error and no process launched.
func TestInstallPendingUpdateNowWithoutPlan(t *testing.T) {
	calls := make([]updateCall, 0)
	stubUpdateExec(t, &calls, nil)
	stubUpdateLaunch(t, &calls, nil)

	err := InstallPendingUpdateNow(t.TempDir(), nil)
	if err == nil {
		t.Fatal("InstallPendingUpdateNow without a plan = nil error")
	}
	if !strings.Contains(err.Error(), "no downloaded update") {
		t.Fatalf("error = %q, want the clear no-update message", err)
	}
	if len(calls) != 0 {
		t.Fatalf("process calls = %+v, want none without a plan", calls)
	}
}

// TestInstallPendingUpdateNowRejectsBadChecksum keeps the plan when the staged
// bytes no longer match pending.json (the checksum is re-verified right before
// the installer runs).
func TestInstallPendingUpdateNowRejectsBadChecksum(t *testing.T) {
	dataDir := t.TempDir()
	staged := filepath.Join(dataDir, "updates", "zeokeel-setup-1.3.0-amd64.exe")
	writePendingUpdate(t, dataDir, pendingInstallFile{
		Version: "1.3.0", Path: staged, SHA256: strings.Repeat("0", 64), Kind: installKindInstaller,
	}, "setup bytes")
	calls := make([]updateCall, 0)
	stubUpdateLaunch(t, &calls, nil)
	hashes := stubStagedHash(t)

	err := InstallPendingUpdateNow(dataDir, nil)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("InstallPendingUpdateNow = %v, want a checksum mismatch", err)
	}
	if *hashes != 1 {
		t.Fatalf("staged hashes = %d, want exactly 1 (the installation verifies once)", *hashes)
	}
	if len(calls) != 0 {
		t.Fatalf("process calls = %+v, want none for an invalid plan", calls)
	}
	if _, statErr := os.Stat(pendingJSONPath(dataDir)); statErr != nil {
		t.Fatalf("pending plan removed on a rejected install: %v", statErr)
	}
	if _, statErr := os.Stat(staged); statErr != nil {
		t.Fatalf("staged setup removed on a rejected install: %v", statErr)
	}
}

// TestInstallPendingUpdateNowLaunchesInstaller covers the Windows path: the app
// spawns itself in the hidden install-helper mode with the setup path (plain
// argv, no cmd.exe quoting); the helper starts the interactive NSIS setup after
// the app quits. The plan and the staged setup stay until the new version
// starts, so a cancelled setup stays retryable.
func TestInstallPendingUpdateNowLaunchesInstaller(t *testing.T) {
	dataDir := t.TempDir()
	staged := filepath.Join(dataDir, "updates", "zeokeel-setup-1.3.0-amd64.exe")
	writePendingUpdate(t, dataDir, pendingInstallFile{
		Version: "1.3.0", Path: staged, SHA256: "abc", Kind: installKindInstaller,
	}, "setup bytes")
	exe := filepath.Join(t.TempDir(), "ZeoKeel", "zeokeel.exe")
	stubUpdateExecutable(t, exe)
	calls := make([]updateCall, 0)
	stubUpdateExec(t, &calls, nil)
	stubUpdateLaunch(t, &calls, nil)
	hashes := stubStagedHash(t)

	if err := InstallPendingUpdateNow(dataDir, nil); err != nil {
		t.Fatalf("InstallPendingUpdateNow: %v", err)
	}
	requireHelperLaunch(t, calls, exe, updateHelperInstallAfterExit, staged)
	requireNoExecProcesses(t, calls)
	if *hashes != 1 {
		t.Fatalf("staged hashes = %d, want exactly 1", *hashes)
	}
	// The setup exe and the plan must stay on disk until the installer ran and
	// the new version starts (startup clears the plan then); while the plan is
	// pending the command can retry after a cancelled setup.
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("staged setup removed before the installer ran: %v", err)
	}
	if _, err := os.Stat(pendingJSONPath(dataDir)); err != nil {
		t.Fatalf("pending plan removed before the new version started: %v", err)
	}
}

// TestInstallPendingUpdateNowDMGInstallsBundle covers the macOS silent path
// against mygo's real dmg layout (F2): .DS_Store, .VolumeIcon.icns and the
// /Applications symlink sit next to the .app. installPendingDMG must pick the
// single .app, copy it with ditto, swap the current bundle and reopen it. The old
// bundle, the plan and the staged dmg stay until the new version starts (F5).
func TestInstallPendingUpdateNowDMGInstallsBundle(t *testing.T) {
	bundle, executable := writeDMGBundleFixture(t)
	stubUpdateExecutable(t, executable)
	dataDir := t.TempDir()
	staged := filepath.Join(dataDir, "updates", "zeokeel-1.3.0-arm64.dmg")
	writePendingUpdate(t, dataDir, pendingInstallFile{
		Version: "1.3.0", Path: staged, SHA256: "abc", Kind: installKindDMG,
	}, "dmg bytes")
	calls := make([]updateCall, 0)
	ditto := stubDittoCopy(t)
	stubUpdateExec(t, &calls, func(name string, args ...string) error {
		if name == "hdiutil" && len(args) > 0 && args[0] == "attach" {
			mount := updateArgAfter(args, "-mountpoint")
			if mount == "" {
				t.Fatalf("hdiutil attach without -mountpoint: %+v", args)
			}
			writeRealisticDMGMount(t, mount, "ZeoKeel.app", "new app")
			return nil
		}
		return ditto(name, args...)
	})
	stubUpdateLaunch(t, &calls, nil)
	hashes := stubStagedHash(t)

	logger := &trayRecordingLogger{}
	if err := InstallPendingUpdateNow(dataDir, logger); err != nil {
		t.Fatalf("InstallPendingUpdateNow: %v (log %s)", err, logger.text())
	}
	requireFileContent(t, executable, "new app")
	// F5: the previous bundle stays until the new version starts (startup cleans
	// it), so a broken new app can be restored.
	requireFileContent(t, filepath.Join(bundle+installBackupSuffix, "Contents", "MacOS", "zeokeel"), "old app")
	requireNoFile(t, bundle+".new")
	if _, ok := findUpdateCall(calls, "exec", "ditto"); !ok {
		t.Fatalf("calls = %+v, want a ditto copy", calls)
	}
	attach, ok := findUpdateCall(calls, "exec", "hdiutil")
	if !ok || len(attach.args) == 0 || attach.args[0] != "attach" {
		t.Fatalf("calls = %+v, want an hdiutil attach", calls)
	}
	detached := false
	for _, call := range calls {
		if call.kind == "exec" && call.name == "hdiutil" && len(call.args) > 0 && call.args[0] == "detach" {
			detached = true
		}
	}
	if !detached {
		t.Fatalf("calls = %+v, want the image detached after the swap", calls)
	}
	requireHelperLaunch(t, calls, executable, updateHelperOpenAfterExit, bundle)
	// F5: plan and staged dmg stay until the new version proves it starts.
	if _, err := os.Stat(pendingJSONPath(dataDir)); err != nil {
		t.Fatalf("pending plan removed before the new version started: %v", err)
	}
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("staged dmg removed before the new version started: %v", err)
	}
	if *hashes != 1 {
		t.Fatalf("staged hashes = %d, want exactly 1", *hashes)
	}
}

// TestInstallPendingUpdateNowDMGRestoresOnReopenFailure covers F5: when the new
// bundle cannot be reopened, the previous bundle is restored instead of leaving
// the app broken, and the plan stays so the user can retry.
func TestInstallPendingUpdateNowDMGRestoresOnReopenFailure(t *testing.T) {
	bundle, executable := writeDMGBundleFixture(t)
	stubUpdateExecutable(t, executable)
	dataDir := t.TempDir()
	staged := filepath.Join(dataDir, "updates", "zeokeel-1.3.0-arm64.dmg")
	writePendingUpdate(t, dataDir, pendingInstallFile{
		Version: "1.3.0", Path: staged, SHA256: "abc", Kind: installKindDMG,
	}, "dmg bytes")
	calls := make([]updateCall, 0)
	ditto := stubDittoCopy(t)
	stubUpdateExec(t, &calls, func(name string, args ...string) error {
		if name == "hdiutil" && len(args) > 0 && args[0] == "attach" {
			writeRealisticDMGMount(t, updateArgAfter(args, "-mountpoint"), "ZeoKeel.app", "new app")
			return nil
		}
		return ditto(name, args...)
	})
	stubUpdateLaunch(t, &calls, func(name string, args ...string) error {
		if len(args) > 0 && args[0] == updateHelperOpenAfterExit {
			return errors.New("open failed")
		}
		return nil
	})

	logger := &trayRecordingLogger{}
	err := InstallPendingUpdateNow(dataDir, logger)
	if err == nil || !strings.Contains(err.Error(), "restored") {
		t.Fatalf("InstallPendingUpdateNow = %v, want the restored-bundle error", err)
	}
	requireFileContent(t, executable, "old app")
	requireNoFile(t, bundle+installBackupSuffix)
	requireNoFile(t, bundle+".new")
	if _, statErr := os.Stat(pendingJSONPath(dataDir)); statErr != nil {
		t.Fatalf("pending plan removed after a failed reopen: %v", statErr)
	}
	if _, statErr := os.Stat(staged); statErr != nil {
		t.Fatalf("staged dmg removed after a failed reopen: %v", statErr)
	}
}

// TestInstallPendingUpdateNowDMGFallsBackToOpeningTheImage covers the fallback:
// when the swap cannot even start (the executable is not inside a bundle, or a
// mount/copy step fails) the dmg is opened for the user and the plan is kept.
func TestInstallPendingUpdateNowDMGFallsBackToOpeningTheImage(t *testing.T) {
	cases := []struct {
		name string
		// setup prepares the fixture and returns the executable path.
		setup func(t *testing.T) string
		fail  func(t *testing.T) func(name string, args ...string) error
	}{
		{
			name: "not an app bundle",
			setup: func(t *testing.T) string {
				t.Helper()
				executable := filepath.Join(t.TempDir(), "zeokeel")
				writeExecutableFixture(t, executable, "old app")
				return executable
			},
		},
		{
			name: "mount fails",
			setup: func(t *testing.T) string {
				t.Helper()
				_, executable := writeDMGBundleFixture(t)
				return executable
			},
			fail: func(t *testing.T) func(name string, args ...string) error {
				return func(name string, args ...string) error {
					if name == "hdiutil" && len(args) > 0 && args[0] == "attach" {
						return errors.New("hdiutil: attach failed")
					}
					return nil
				}
			},
		},
		{
			name: "copy fails",
			setup: func(t *testing.T) string {
				t.Helper()
				_, executable := writeDMGBundleFixture(t)
				return executable
			},
			fail: func(t *testing.T) func(name string, args ...string) error {
				return func(name string, args ...string) error {
					if name == "ditto" {
						return errors.New("ditto: copy failed")
					}
					if name == "hdiutil" && len(args) > 0 && args[0] == "attach" {
						writeRealisticDMGMount(t, updateArgAfter(args, "-mountpoint"), "ZeoKeel.app", "new app")
						return nil
					}
					return nil
				}
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			base := testCase.setup(t)
			stubUpdateExecutable(t, base)
			dataDir := t.TempDir()
			staged := filepath.Join(dataDir, "updates", "zeokeel-1.3.0-arm64.dmg")
			writePendingUpdate(t, dataDir, pendingInstallFile{
				Version: "1.3.0", Path: staged, SHA256: "abc", Kind: installKindDMG,
			}, "dmg bytes")
			calls := make([]updateCall, 0)
			var fail func(name string, args ...string) error
			if testCase.fail != nil {
				fail = testCase.fail(t)
			}
			stubUpdateExec(t, &calls, fail)
			stubUpdateLaunch(t, &calls, nil)

			logger := &trayRecordingLogger{}
			err := InstallPendingUpdateNow(dataDir, logger)
			if err == nil {
				t.Fatalf("InstallPendingUpdateNow = nil error, want the fallback failure")
			}
			openCall, ok := findUpdateCall(calls, "launch", "open")
			if !ok || len(openCall.args) != 1 || openCall.args[0] != staged {
				t.Fatalf("calls = %+v, want open %q as the fallback", calls, staged)
			}
			if !strings.Contains(logger.text(), "dmg") && !strings.Contains(logger.text(), "disk image") {
				t.Fatalf("log = %q, want the fallback explained", logger.text())
			}
			if _, statErr := os.Stat(pendingJSONPath(dataDir)); statErr != nil {
				t.Fatalf("pending plan removed on a failed install: %v", statErr)
			}
			if _, statErr := os.Stat(staged); statErr != nil {
				t.Fatalf("staged dmg removed on a failed install: %v", statErr)
			}
		})
	}
}

// TestInstallPendingUpdateNowAppImage replaces $APPIMAGE and relaunches it
// through the stable system shell (the detached helper would run from the
// AppImage runtime's temporary mount, which disappears when the parent quits),
// then consumes the plan.
func TestInstallPendingUpdateNowAppImage(t *testing.T) {
	dataDir := t.TempDir()
	appImage := filepath.Join(t.TempDir(), "ZeoKeel.AppImage")
	writeExecutableFixture(t, appImage, "old appimage")
	t.Setenv("APPIMAGE", appImage)
	exe := filepath.Join(t.TempDir(), "zeokeel")
	stubUpdateExecutable(t, exe)
	_ = exe
	staged := filepath.Join(dataDir, "updates", "zeokeel-1.3.0-linux-amd64.AppImage")
	writePendingUpdate(t, dataDir, pendingInstallFile{
		Version: "1.3.0", Path: staged, SHA256: "abc", Kind: installKindAppImage,
	}, "new appimage")
	calls := make([]updateCall, 0)
	stubUpdateLaunch(t, &calls, nil)
	hashes := stubStagedHash(t)

	if err := InstallPendingUpdateNow(dataDir, nil); err != nil {
		t.Fatalf("InstallPendingUpdateNow: %v", err)
	}
	requireFileContent(t, appImage, "new appimage")
	info, err := os.Stat(appImage)
	if err != nil {
		t.Fatalf("stat replaced AppImage: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("replaced AppImage mode = %v, want executable", info.Mode())
	}
	requireAppImageShellLaunch(t, calls, appImage)
	requireNoFile(t, pendingJSONPath(dataDir))
	requireNoFile(t, staged)
	if *hashes != 1 {
		t.Fatalf("staged hashes = %d, want exactly 1", *hashes)
	}
}

// TestInstallPendingUpdateNowBinary replaces the running executable, relaunches
// the new one through the helper and consumes the plan. The executable path is
// injected, so the test binary is never replaced.
func TestInstallPendingUpdateNowBinary(t *testing.T) {
	dataDir := t.TempDir()
	current := filepath.Join(t.TempDir(), "zeokeel")
	writeExecutableFixture(t, current, "old binary")
	stubUpdateExecutable(t, current)
	staged := filepath.Join(dataDir, "updates", "1.3.0-zeokeel")
	writePendingUpdate(t, dataDir, pendingInstallFile{
		Version: "1.3.0", Path: staged, SHA256: "abc", Kind: installKindBinary,
	}, "new binary")
	calls := make([]updateCall, 0)
	stubUpdateLaunch(t, &calls, nil)

	if err := InstallPendingUpdateNow(dataDir, nil); err != nil {
		t.Fatalf("InstallPendingUpdateNow: %v", err)
	}
	requireFileContent(t, current, "new binary")
	requireHelperLaunch(t, calls, current, updateHelperOpenAfterExit, current)
	requireNoFile(t, pendingJSONPath(dataDir))
	requireNoFile(t, staged)
}

// TestInstallPendingUpdateNowArchive covers a legacy archive plan: the archive
// is extracted over the app tree, the updated executable relaunched through the
// helper and the plan consumed.
func TestInstallPendingUpdateNowArchive(t *testing.T) {
	dataDir := t.TempDir()
	archiveBody := buildTarGz(t, []testArchiveEntry{
		{name: "zeokeel-app/", dir: true},
		{name: "zeokeel-app/zeokeel", body: "new binary", mode: 0o755},
	})
	staged := filepath.Join(dataDir, updatesDirName, "1.3.0-zeokeel-linux-amd64.tar.gz")
	writePendingUpdateBytes(t, dataDir, pendingInstallFile{
		Version: "1.3.0", Path: staged, Kind: installKindArchive,
	}, archiveBody)
	installDir := filepath.Join(t.TempDir(), "Zeokeel")
	current := writeFakeExecutable(t, installDir, "zeokeel", "old binary")
	stubUpdateExecutable(t, current)
	calls := make([]updateCall, 0)
	stubUpdateLaunch(t, &calls, nil)

	if err := InstallPendingUpdateNow(dataDir, nil); err != nil {
		t.Fatalf("InstallPendingUpdateNow: %v", err)
	}
	requireFileContent(t, current, "new binary")
	requireHelperLaunch(t, calls, current, updateHelperOpenAfterExit, current)
	requireNoFile(t, pendingJSONPath(dataDir))
	requireNoFile(t, staged)
}

// TestApplyPendingUpdateOnStartupSkipsInstallerAndDMG pins that startup never
// silently runs an installer or dmg swap: those need the user-visible command,
// so the plan is kept and the reason logged. It must decide that without hashing
// the (possibly hundreds of MB) installer/dmg (F10).
func TestApplyPendingUpdateOnStartupSkipsInstallerAndDMG(t *testing.T) {
	for _, kind := range []string{installKindInstaller, installKindDMG} {
		t.Run(kind, func(t *testing.T) {
			dataDir := t.TempDir()
			staged := filepath.Join(dataDir, "updates", "1.3.0-staged-"+kind)
			writePendingUpdate(t, dataDir, pendingInstallFile{
				Version: "1.3.0", Path: staged, SHA256: "abc", Kind: kind,
			}, "staged bytes")
			logger := &trayRecordingLogger{}
			hashes := stubStagedHash(t)

			applyPendingUpdateOnStartup(dataDir, "0.2.7", logger)

			if *hashes != 0 {
				t.Fatalf("startup hashed %d staged asset(s), want 0 when the plan is only skipped", *hashes)
			}
			if _, err := os.Stat(pendingJSONPath(dataDir)); err != nil {
				t.Fatalf("pending plan removed by startup: %v", err)
			}
			if _, err := os.Stat(staged); err != nil {
				t.Fatalf("staged asset removed by startup: %v", err)
			}
			text := strings.ToLower(logger.text())
			if !strings.Contains(text, "install") || !strings.Contains(text, "in-app") {
				t.Fatalf("startup log = %q, want it to explain the install command wait", logger.text())
			}
		})
	}
}

// TestApplyPendingUpdateOnStartupVersionCleanup covers F17: the already-installed
// cleanup compares the app version passed by the host options (not the package
// global), and only clears the plan when that version satisfies it.
func TestApplyPendingUpdateOnStartupVersionCleanup(t *testing.T) {
	cases := []struct {
		name       string
		appVersion string
		wantClear  bool
	}{
		{name: "same version clears", appVersion: "v1.3.0", wantClear: true},
		{name: "newer version clears", appVersion: "1.4.0", wantClear: true},
		{name: "older version keeps", appVersion: "0.2.7", wantClear: false},
		{name: "dev version keeps", appVersion: "dev", wantClear: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			dataDir := t.TempDir()
			staged := filepath.Join(dataDir, "updates", "1.3.0-zeokeel-setup-1.3.0-amd64.exe")
			writePendingUpdate(t, dataDir, pendingInstallFile{
				Version: "1.3.0", Path: staged, SHA256: "abc", Kind: installKindInstaller,
			}, "setup bytes")
			logger := &trayRecordingLogger{}
			hashes := stubStagedHash(t)

			// The package global must not decide: it stays at the test binary's
			// default ("dev") and an explicit app version is passed instead.
			applyPendingUpdateOnStartup(dataDir, testCase.appVersion, logger)

			if *hashes != 0 {
				t.Fatalf("startup hashed %d staged asset(s), want 0", *hashes)
			}
			_, planErr := os.Stat(pendingJSONPath(dataDir))
			_, stagedErr := os.Stat(staged)
			if testCase.wantClear {
				if planErr == nil || stagedErr == nil {
					t.Fatalf("already-installed plan kept (plan err=%v, staged err=%v)", planErr, stagedErr)
				}
				if !strings.Contains(logger.text(), "already running") {
					t.Fatalf("startup log = %q, want the already-installed cleanup", logger.text())
				}
				return
			}
			if planErr != nil || stagedErr != nil {
				t.Fatalf("plan cleared for running version %q (plan err=%v, staged err=%v)",
					testCase.appVersion, planErr, stagedErr)
			}
		})
	}
}

// TestApplyPendingUpdateOnStartupAppliesLegacyBinaryPlan keeps the existing
// install-on-restart behavior for non-installer layouts and pins F10: the applied
// plan is hashed exactly once (not once for the kind decision and again inside
// applyPendingUpdate).
func TestApplyPendingUpdateOnStartupAppliesLegacyBinaryPlan(t *testing.T) {
	dataDir := t.TempDir()
	current := filepath.Join(t.TempDir(), "zeokeel")
	writeExecutableFixture(t, current, "old binary")
	stubUpdateExecutable(t, current)
	staged := filepath.Join(dataDir, "updates", "1.3.0-zeokeel")
	writePendingUpdate(t, dataDir, pendingInstallFile{
		Version: "1.3.0", Path: staged, SHA256: "abc", Kind: installKindBinary,
	}, "new binary")
	logger := &trayRecordingLogger{}
	hashes := stubStagedHash(t)

	applyPendingUpdateOnStartup(dataDir, "0.2.7", logger)

	if *hashes != 1 {
		t.Fatalf("staged hashes = %d, want exactly 1 for an applied plan", *hashes)
	}
	requireFileContent(t, current, "new binary")
	requireNoFile(t, pendingJSONPath(dataDir))
	requireNoFile(t, staged)
}

// TestApplyPendingUpdateOnStartupSkipsAlreadyRunningPlanWithoutHashing covers the
// ordering of the startup decisions: an already-installed plan is cleared before
// any hashing (only the version comparison runs).
func TestApplyPendingUpdateOnStartupSkipsAlreadyRunningPlanWithoutHashing(t *testing.T) {
	dataDir := t.TempDir()
	staged := filepath.Join(dataDir, "updates", "1.3.0-zeokeel-1.3.0-arm64.dmg")
	writePendingUpdate(t, dataDir, pendingInstallFile{
		Version: "1.3.0", Path: staged, SHA256: "abc", Kind: installKindDMG,
	}, "dmg bytes")
	logger := &trayRecordingLogger{}
	hashes := stubStagedHash(t)

	applyPendingUpdateOnStartup(dataDir, "v1.3.0", logger)

	if *hashes != 0 {
		t.Fatalf("staged hashes = %d, want 0 when clearing an already-installed plan", *hashes)
	}
	requireNoFile(t, pendingJSONPath(dataDir))
	requireNoFile(t, staged)
}
