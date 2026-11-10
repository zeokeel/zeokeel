package host

// Detached update helper modes. The running app cannot reliably quote a
// cmd.exe command line for a path that may contain spaces, '%' or '&' (Go's
// os/exec uses CommandLineToArgvW escaping, cmd.exe has its own unquoting
// rules), and on Windows it cannot replace its own executable. So the updater
// spawns *this same executable* in a hidden helper mode with plain argv (which
// Go passes losslessly), and the helper — after the app has had time to quit —
// starts the installer or reopens the updated app with exec.Command, no shell.
//
// The helper runs before the mygo application is initialized, so it never
// acquires the single-instance lock and never opens a window.
const (
	// updateHelperInstallAfterExit waits, then runs the NSIS setup path in argv[1].
	updateHelperInstallAfterExit = "--zeokeel-install-after-exit"
	// updateHelperOpenAfterExit waits, then reopens the app/executable in argv[1].
	updateHelperOpenAfterExit = "--zeokeel-open-after-exit"
)

// detachedProcess is the platform process seam: it builds the launch for one
// helper target (bash/dir on unix, plain argv on Windows). Tests replace it so the
// helper modes are exercised without spawning anything.
var detachedProcess = platformDetachedProcess

// runUpdateHelper handles one hidden helper mode and reports whether the process
// was a helper (the caller then exits with the returned code). It is called at
// the very start of Main, before flag parsing and before any mygo state exists.
func runUpdateHelper(args []string) (handled bool, code int) {
	if len(args) < 2 {
		return false, 0
	}
	mode, target := args[0], args[1]
	switch mode {
	case updateHelperInstallAfterExit, updateHelperOpenAfterExit:
	default:
		return false, 0
	}
	// The helper is detached from the parent; start it only after the running
	// process has released the single-instance lock (and, on Windows, its
	// executable file handle). A fixed short delay is enough for the quit path.
	waitForUpdateHelperDelay()
	if mode == updateHelperInstallAfterExit {
		return true, runDetachedTarget(target)
	}
	return true, reopenAfterExitTarget(target)
}

// runDetachedTarget starts one executable without a shell and releases its
// handle (the app is quitting; a zombie must not outlive it). The installer is
// interactive: its window must stay visible, so the Windows helper does not
// pass HideWindow here.
func runDetachedTarget(target string) int {
	launch, err := detachedProcess(target, false)
	if err != nil {
		return 1
	}
	if err := launch(); err != nil {
		return 1
	}
	return 0
}

// reopenAfterExitTarget reopens an app bundle or executable after the old
// process quit: a macOS bundle is opened through LaunchServices, everything
// else is executed directly.
func reopenAfterExitTarget(target string) int {
	launch, err := detachedProcess(target, true)
	if err != nil {
		return 1
	}
	if err := launch(); err != nil {
		return 1
	}
	return 0
}
