package host

// Immediate install: the in-app "install update and restart" action applies an
// already staged update without waiting for the next start. The Windows NSIS
// setup installer runs detached after the app quits; macOS mounts the signed dmg
// and swaps the .app bundle in place; AppImage/binary/archive replace the running
// executable and relaunch it. Every OS process and the executable path go through
// the update* seams below, so tests never start a real installer and never
// replace the test binary.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

var (
	// updateExecutable reports the running executable. Tests point it at a temp
	// fixture so installing over it never touches the test binary.
	updateExecutable = os.Executable
	// updateExecCommand runs one install helper command and waits for it to
	// finish (hdiutil/ditto). Tests swap it so nothing real runs.
	updateExecCommand = func(name string, args ...string) error {
		command := exec.Command(name, args...)
		output, err := command.CombinedOutput()
		if err != nil {
			if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
				return fmt.Errorf("%s: %w (%s)", name, err, trimmed)
			}
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	}
	// updateLaunch starts one detached process and does not wait for it (the
	// setup installer, the reopened app). Tests swap it.
	updateLaunch = func(name string, args ...string) error {
		command := exec.Command(name, args...)
		return command.Start()
	}
)

// InstallPendingUpdateNow applies the downloaded update immediately: it
// re-validates pending.json + the staged checksum, installs the staged asset for
// its kind and relaunches the updated app. The caller (the host command) quits
// the running process on success; a failure keeps the plan for a retry and
// returns a user-readable error.
func InstallPendingUpdateNow(dataDir string, logger contracts.Logger) error {
	pending, ok, err := readPendingUpdate(dataDir)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("no downloaded update to install; check for updates first")
	}
	switch pending.Kind {
	case installKindInstaller:
		return installPendingInstaller(dataDir, pending, logger)
	case installKindDMG:
		return installPendingDMG(dataDir, pending, logger)
	case installKindAppImage:
		return installPendingAppImage(dataDir, pending, logger)
	case installKindArchive:
		return installPendingArchive(dataDir, pending, logger)
	default:
		return installPendingExecutable(dataDir, pending, logger)
	}
}

// installPendingInstaller runs the Windows NSIS setup after this process quits.
// A hidden helper mode of this executable waits for the app to release its
// single-instance lock and then starts the setup through exec.Command (plain
// argv, no cmd.exe quoting); the staged setup stays until the new version
// starts, so cancelling the setup leaves a retryable plan.
func installPendingInstaller(dataDir string, pending pendingUpdate, logger contracts.Logger) error {
	// Spawn this executable in its hidden helper mode: plain argv means a path
	// containing spaces, '%' or '&' reaches the helper untouched (cmd.exe has a
	// different quoting algorithm from os/exec's CommandLineToArgvW escaping).
	// The helper waits for this process to release the single-instance lock and
	// the exe handle, then runs the NSIS setup with exec.Command (no shell).
	exe, err := updateExecutable()
	if err != nil {
		return fmt.Errorf("locate the running executable for the setup helper: %w", err)
	}
	if err := updateLaunch(exe, updateHelperInstallAfterExit, pending.Staged); err != nil {
		return fmt.Errorf("launch the setup installer: %w", err)
	}
	// Keep the plan (and the staged setup) until the new version starts: the
	// startup clears it once the running version matches, and until then the
	// command can retry when the user cancels the setup.
	if logger != nil {
		logger.Printf("%s: running the downloaded setup installer after quit", appName)
	}
	return nil
}

// installPendingDMG silently installs the macOS disk image: mount it, copy the
// single .app bundle next to the current one with ditto, swap the current bundle
// for the new one, detach and reopen the app. When the bundle location is not
// writable or a mount/copy/swap step fails, it falls back to opening the dmg so
// the user can finish the install manually, keeps the plan and returns a clear
// error.
func installPendingDMG(dataDir string, pending pendingUpdate, logger contracts.Logger) error {
	fallback := func(cause error) error {
		if logger != nil {
			logger.Printf("%s: automatic dmg install failed (%v); opening the disk image for a manual install", appName, cause)
		}
		if launchErr := updateLaunch("open", pending.Staged); launchErr != nil {
			return fmt.Errorf("install the dmg automatically: %v (open the downloaded dmg: %v)", cause, launchErr)
		}
		return fmt.Errorf("install the dmg automatically: %v (opened the downloaded dmg for a manual install)", cause)
	}

	current, err := updateExecutable()
	if err != nil {
		return fallback(fmt.Errorf("locate the running executable: %w", err))
	}
	bundle, err := darwinAppBundleRoot(current)
	if err != nil {
		return fallback(err)
	}
	// The work dir sits next to the bundle: creating it verifies the install
	// location is writable before anything is mounted or swapped, and it keeps
	// every temp file on the bundle's filesystem so the renames are atomic.
	work, err := os.MkdirTemp(filepath.Dir(bundle), ".zeokeel-dmg-*")
	if err != nil {
		return fallback(fmt.Errorf("the app bundle location is not writable: %w", err))
	}
	defer os.RemoveAll(work)
	mount := filepath.Join(work, "mnt")
	if err := os.MkdirAll(mount, 0o755); err != nil {
		return fallback(fmt.Errorf("create the dmg mount point: %w", err))
	}
	if err := updateExecCommand("hdiutil", "attach", "-nobrowse", "-noautoopen", "-mountpoint", mount, pending.Staged); err != nil {
		return fallback(fmt.Errorf("mount the downloaded dmg: %w", err))
	}
	attached := true
	defer func() {
		if attached {
			if detachErr := updateExecCommand("hdiutil", "detach", mount); detachErr != nil && logger != nil {
				logger.Printf("%s: detach the downloaded dmg: %v", appName, detachErr)
			}
		}
	}()
	app, err := singleAppInDMG(mount)
	if err != nil {
		return fallback(fmt.Errorf("read the mounted dmg: %w", err))
	}
	next := bundle + ".new"
	if err := os.RemoveAll(next); err != nil {
		return fallback(fmt.Errorf("clear the previous new app bundle: %w", err))
	}
	if err := updateExecCommand("ditto", app, next); err != nil {
		return fallback(fmt.Errorf("copy the new app bundle: %w", err))
	}
	if err := updateExecCommand("hdiutil", "detach", mount); err != nil && logger != nil {
		logger.Printf("%s: detach the downloaded dmg: %v", appName, err)
	}
	attached = false

	backup := bundle + installBackupSuffix
	if err := os.RemoveAll(backup); err != nil {
		return fallback(fmt.Errorf("remove the previous app bundle backup: %w", err))
	}
	if err := os.Rename(bundle, backup); err != nil {
		return fallback(fmt.Errorf("move the current app bundle aside: %w", err))
	}
	if err := os.Rename(next, bundle); err != nil {
		restoreErr := os.Rename(backup, bundle)
		if restoreErr != nil {
			return fmt.Errorf("move the new app bundle into place: %w (restore the old bundle: %v)", err, restoreErr)
		}
		return fallback(fmt.Errorf("move the new app bundle into place: %w", err))
	}
	// Keep <Name>.app.old and the pending plan until the new process proves it
	// starts: the running process may lazily load resources from the old bundle
	// and a failed reopen must be recoverable. Startup removes the backup once
	// it runs the new version, so a leftover is self-healing.
	if err := reopenAppBundleAfterQuit(bundle); err != nil {
		_ = os.RemoveAll(bundle)
		if restoreErr := os.Rename(backup, bundle); restoreErr != nil {
			return fmt.Errorf("reopen the updated app bundle: %w (restore the old bundle: %v)", err, restoreErr)
		}
		return fmt.Errorf("reopen the updated app bundle: %w (restored the previous app bundle)", err)
	}
	if logger != nil {
		logger.Printf("%s: installed the downloaded dmg into %q; the previous bundle stays until the new version starts", appName, bundle)
	}
	return nil
}

// installPendingAppImage replaces the running AppImage (named by $APPIMAGE) and
// relaunches it.
func installPendingAppImage(dataDir string, pending pendingUpdate, logger contracts.Logger) error {
	appImage := strings.TrimSpace(os.Getenv("APPIMAGE"))
	if appImage == "" {
		return errors.New("not running from an AppImage; restart the downloaded update manually")
	}
	if err := replaceAppImage(appImage, pending.Staged); err != nil {
		return fmt.Errorf("replace the running AppImage: %w", err)
	}
	if err := relaunchAfterQuit(appImage); err != nil {
		return fmt.Errorf("relaunch the updated AppImage: %w", err)
	}
	clearPendingInstall(dataDir, pending.Staged, logger)
	if logger != nil {
		logger.Printf("%s: installed the downloaded AppImage over %q", appName, appImage)
	}
	return nil
}

// installPendingArchive extracts a release archive over the app and relaunches
// the updated executable (legacy plans: new releases ship installers/dmgs).
func installPendingArchive(dataDir string, pending pendingUpdate, logger contracts.Logger) error {
	current, err := updateExecutable()
	if err != nil {
		return fmt.Errorf("locate the running executable: %w", err)
	}
	if err := installArchive(pending.Staged, current); err != nil {
		return fmt.Errorf("install the download archive: %w", err)
	}
	if err := relaunchAfterQuit(current); err != nil {
		return fmt.Errorf("relaunch the updated executable: %w", err)
	}
	clearPendingInstall(dataDir, pending.Staged, logger)
	if logger != nil {
		logger.Printf("%s: installed the downloaded archive", appName)
	}
	return nil
}

// installPendingExecutable replaces the running executable with the staged
// binary and relaunches the new one.
func installPendingExecutable(dataDir string, pending pendingUpdate, logger contracts.Logger) error {
	current, err := updateExecutable()
	if err != nil {
		return fmt.Errorf("locate the running executable: %w", err)
	}
	replacer := defaultExecutableReplacer{}
	if err := replacer.Replace(pending.Staged); err != nil {
		return fmt.Errorf("replace the running executable: %w", err)
	}
	if err := relaunchAfterQuit(current); err != nil {
		return fmt.Errorf("relaunch the updated executable: %w", err)
	}
	clearPendingInstall(dataDir, pending.Staged, logger)
	if logger != nil {
		logger.Printf("%s: installed the downloaded executable", appName)
	}
	return nil
}

// relaunchAfterQuit starts the updated application after this process quits. A
// hidden helper mode of this same executable waits briefly (the running
// instance still holds the single-instance lock and, on Windows, the exe
// handle), then executes the target through exec.Command — no shell, so paths
// with spaces/%/& are passed losslessly.
func relaunchAfterQuit(target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return errors.New("empty relaunch target")
	}
	// A Linux AppImage helper runs from the runtime's temporary mount; the mount
	// disappears when the parent quits, so the helper itself may not survive to
	// relaunch. Use the stable system shell for that one target instead of
	// copying this executable out of the mount.
	if runtime.GOOS == "linux" && strings.HasSuffix(strings.ToLower(target), ".appimage") {
		return updateLaunch("sh", "-c", `sleep 3; exec "$1"`, "sh", target)
	}
	exe, err := updateExecutable()
	if err != nil {
		return fmt.Errorf("locate the running executable for the relaunch helper: %w", err)
	}
	return updateLaunch(exe, updateHelperOpenAfterExit, target)
}

// reopenAppBundleAfterQuit reopens a macOS app bundle after quit: the helper
// executes LaunchServices' `open` for a .app directory instead of trying to
// exec the bundle.
func reopenAppBundleAfterQuit(bundle string) error {
	bundle = strings.TrimSpace(bundle)
	if bundle == "" {
		return errors.New("empty app bundle path")
	}
	return relaunchAfterQuit(bundle)
}
