package host

// The host command that applies a downloaded update immediately: the update
// settings page and the command palette both reach "install update and restart"
// through it. The settings page schedules the download (pending.json); this
// command runs the real installer/dmg swap without waiting for the next start.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// updateInstallCommandID is the stable command ID the update page and the
// command palette use for the immediate install action.
const updateInstallCommandID = "uiroot.install-update"

// quitApplication requests the product quit from the UI thread. Tests swap it so
// the command can be exercised without a running mygo application.
var quitApplication = func() { mygo.App.Quit() }

// registerUpdateInstallCommand adds the immediate-install command to the UI
// root's command surface. It is always registered (the update page looks it up
// BEFORE scheduling the download, by presence, so the primary action can say
// "install and restart"), but Enabled only while a plan is pending: the command
// palette filters on Enabled and must not list a no-op entry. Run re-validates
// the plan and returns a clear error when none exists (headless/embedder use).
// InstallPendingUpdateNow runs on the UI thread and the host quits on success so
// the new version starts. The returned function unregisters the command on
// shutdown.
func registerUpdateInstallCommand(uiRoot *uiroot.Root, dataDir string,
	logger contracts.Logger) (func(), error) {
	if uiRoot == nil {
		return nil, errors.New("no UI root for the install-update command")
	}
	title, hint := "安装更新并重启", "运行已下载的更新安装包，然后重启 ZeoKeel"
	if uiRoot.Locale().Language() == "en" {
		title, hint = "Install update and restart", "Run the downloaded update installer and restart ZeoKeel"
	}
	return uiRoot.Runtime().RegisterCommand(contracts.CommandSpec{
		ID:    updateInstallCommandID,
		Title: title,
		Hint:  hint,
		Order: 915,
		Enabled: func() bool {
			return pendingInstallExists(dataDir)
		},
		Run: func() error {
			if err := InstallPendingUpdateNow(dataDir, logger); err != nil {
				return err
			}
			quitApplication()
			return nil
		},
	})
}

// pendingInstallExists reports whether a downloaded update is scheduled. It is
// the cheap check behind the command's Enabled hook (the palette must not list
// the action without a plan); the full plan/checksum validation happens in
// InstallPendingUpdateNow.
func pendingInstallExists(dataDir string) bool {
	if strings.TrimSpace(dataDir) == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dataDir, updatesDirName, pendingInstallName))
	return err == nil
}
