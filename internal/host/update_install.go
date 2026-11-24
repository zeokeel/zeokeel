package host

// "Install and restart now": the bridge between the gateway's update.install and
// the installer this process runs.
//
// The gateway cannot install: it holds the data directory and the HTTP listener,
// and on Windows it cannot replace its own running executable. So the browser
// asks the gateway, the gateway writes <dataDir>/updates/install-now and stops
// itself, and the host applies the staged update once the gateway is gone — the
// point where the executable handle is released.
//
// Without the marker a staged update stays queued for the next start
// (internal/host/install.go), which is the "later" option in the same dialog.

import (
	"os"
	"path/filepath"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// maybeInstallRequestedUpdate applies a staged update the user asked to install
// at shutdown, then relaunches the updated app. Failures are logged: the plan is
// kept, so the next start retries and 关于 shows it.
func maybeInstallRequestedUpdate(dataDir string, logger contracts.Logger) {
	if !contracts.UpdateInstallNowRequested(dataDir) {
		return
	}
	// Consume the request before installing: a failed install must not repeat on
	// every start without somebody asking again.
	contracts.ClearUpdateInstallNow(dataDir)
	if err := InstallPendingUpdateNow(dataDir, logger); err != nil {
		if logger != nil {
			logger.Printf("%s: install the requested update: %v", appName, err)
		}
		return
	}
	if logger != nil {
		logger.Printf("%s: installed the requested update", appName)
	}
}

// clearStaleInstallMarker runs at startup, before anything can read the marker:
// a marker left by a crash between the request and the install would otherwise
// install an update on a start where nobody asked.
func clearStaleInstallMarker(dataDir string, logger contracts.Logger) {
	path := contracts.UpdateInstallNowPath(dataDir)
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) && logger != nil {
		logger.Printf("%s: clear the stale update marker %q: %v", appName, filepath.Base(path), err)
	}
}
