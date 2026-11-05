package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/globalhotkey"
	"github.com/zeokeel/zeokeel/internal/updater"
)

// The Updates page's real check and the Desktop page's global-shortcut field.
// The app layer owns the platform wiring; the shell owns the state machine and
// the validation shown to the user, with an injectable seam so tests never
// touch the network or the OS.

const (
	settingUpdateFeed    = "update.feed_url"
	settingGlobalHotkey  = "ui.global_hotkey"
	updateCheckTimeout   = 20 * time.Second
	updateInstallTimeout = 120 * time.Second
)

// updateCheckState is the Updates page's persisted-in-memory state.
type updateCheckState struct {
	checking bool
	// message is the last result, already localized.
	message string
}

// checkForUpdate is the injectable update seam. nil uses the updater engine.
var checkForUpdate func(app *App, ctx context.Context) (*updater.Release, bool, error)

// UpdateInstallOutcome reports what an injected install seam did. It mirrors
// app.UpdateInstallResult so internal/app can adapt it in one line without the
// shell importing the app package.
type UpdateInstallOutcome struct {
	// CurrentVersion is the running build version used for the check.
	CurrentVersion string
	// ReleaseVersion is the newer release's version (empty when up to date).
	ReleaseVersion string
	// StagedPath is the verified download in <data dir>/updates.
	StagedPath string
	// Staged reports a verified download scheduled for install-on-restart.
	Staged bool
	// Applied reports the running executable was replaced right away.
	Applied bool
	// UpToDate reports the feed had no newer release.
	UpToDate bool
}

// UpdateInstallHook is the app seam shape: check the feed, download and verify
// the asset, schedule/apply the install and report progress. Nil falls back to
// the updater engine directly (download + schedule, install on restart).
type UpdateInstallHook func(ctx context.Context, progress func(downloaded, total int64)) (*UpdateInstallOutcome, error)

// installUpdateHook is the injected seam (internal/app installs it in app.Run
// through SetInstallUpdateHook).
var installUpdateHook UpdateInstallHook

// SetInstallUpdateHook installs the app's DownloadAndInstallUpdate as the
// shell's install action. app.Run wires it once:
//
//	shell.SetInstallUpdateHook(func(ctx context.Context, progress func(int64, int64)) (*shell.UpdateInstallOutcome, error) {
//		r, err := a.DownloadAndInstallUpdate(ctx, progress)
//		if r == nil { return nil, err }
//		return &shell.UpdateInstallOutcome{
//			CurrentVersion: r.CurrentVersion, ReleaseVersion: r.ReleaseVersion,
//			StagedPath: r.StagedPath, Staged: r.Staged, Applied: r.Applied, UpToDate: r.UpToDate,
//		}, err
//	})
//
// Passing nil restores the built-in updater-engine fallback.
func SetInstallUpdateHook(fn UpdateInstallHook) { installUpdateHook = fn }

// updateFeedURL is the configured manifest URL setting/env.
func (a *App) updateFeedURL() string {
	feed := strings.TrimSpace(a.shellSetting(settingUpdateFeed, ""))
	if feed == "" {
		feed = strings.TrimSpace(os.Getenv("ZEOKEEL_UPDATE_FEED"))
	}
	return feed
}

// updateConfig builds the updater configuration shared by check and download.
func (a *App) updateConfig() updater.Config {
	return updater.Config{
		CurrentVersion: BuildVersion,
		FeedURL:        a.updateFeedURL(),
		DataDir:        a.pluginDataDir(),
	}
}

// defaultUpdateCheck runs the updater engine against the configured feed. A
// build without a feed (no update.feed_url setting and no
// ZEOKEEL_UPDATE_FEED) reports ErrDisabled so the page can explain it.
func (a *App) defaultUpdateCheck(ctx context.Context) (*updater.Release, bool, error) {
	cfg := a.updateConfig()
	if cfg.FeedURL == "" {
		return nil, false, updater.ErrDisabled
	}
	return updater.Check(ctx, cfg)
}

// runUpdateCheck starts one asynchronous check and renders "checking" until it
// finishes. The goroutine only touches the shell state store plus Invalidate,
// so a slow network never blocks the frame.
func (a *App) runUpdateCheck(c *ui.Context) {
	st := a.shellUI()
	if st.updateChecking {
		return
	}
	st.updateChecking = true
	st.updateMessage = a.menuText("Checking for updates…", "正在检查更新…")
	if c != nil {
		c.Invalidate()
	}
	hook := checkForUpdate
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
		defer cancel()
		var release *updater.Release
		var available bool
		var err error
		if hook != nil {
			release, available, err = hook(a, ctx)
		} else {
			release, available, err = a.defaultUpdateCheck(ctx)
		}
		st := a.shellUI()
		st.updateChecking = false
		switch {
		case errors.Is(err, updater.ErrDisabled):
			st.updateMessage = a.menuText(
				"No update feed is configured for this build, so updates are manual.",
				"此版本未配置更新源，因此仅支持手动更新。")
		case err != nil:
			st.updateMessage = fmt.Sprintf(a.menuText("Could not check for updates: %v",
				"检查更新失败：%v"), err)
		case available && release != nil:
			st.updateMessage = fmt.Sprintf(a.menuText("Update available: %s",
				"发现新版本：%s"), release.Version)
			if notes := strings.TrimSpace(release.Notes); notes != "" {
				st.updateMessage += " · " + notes
			}
			st.updateRelease = release
			st.updateAvailable = true
			st.updateInstalled = false
		default:
			st.updateMessage = a.menuText("You are up to date.",
				"当前已是最新版本。")
		}
		if c != nil {
			c.Invalidate()
		}
	}()
}

// runUpdateInstall downloads and schedules the update found by the last check.
// The download runs off the frame thread; the page shows progress and then the
// restart prompt. A build without a feed or without a checked release keeps
// the action disabled.
func (a *App) runUpdateInstall(c *ui.Context) {
	st := a.shellUI()
	if st.updateInstalling || st.updateChecking {
		return
	}
	if !st.updateAvailable || st.updateRelease == nil {
		a.toastDeferred(c, a.menuText("Check for updates first.", "请先检查更新。"))
		return
	}
	if a.updateFeedURL() == "" {
		a.toastDeferred(c, a.menuText(
			"No update feed is configured for this build; configure update.feed_url.",
			"此版本未配置更新源；请配置 update.feed_url。"))
		return
	}
	rel := st.updateRelease
	st.updateInstalling = true
	st.updateProgressDownloaded = 0
	st.updateProgressTotal = 0
	st.updateMessage = fmt.Sprintf(a.menuText("Downloading and installing %s…",
		"正在下载并安装 %s…"), rel.Version)
	if c != nil {
		c.Invalidate()
	}
	hook := installUpdateHook
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), updateInstallTimeout)
		defer cancel()
		var outcome *UpdateInstallOutcome
		var err error
		progress := func(downloaded, total int64) {
			st := a.shellUI()
			st.updateProgressMu.Lock()
			st.updateProgressDownloaded, st.updateProgressTotal = downloaded, total
			st.updateProgressMu.Unlock()
			if c != nil {
				c.Invalidate()
			}
		}
		if hook != nil {
			outcome, err = hook(ctx, progress)
		} else {
			var staged *updater.Staged
			staged, err = a.defaultUpdateInstall(ctx, rel)
			if err == nil && staged != nil {
				outcome = &UpdateInstallOutcome{
					CurrentVersion: BuildVersion,
					ReleaseVersion: rel.Version,
					StagedPath:     staged.Path,
					Staged:         true,
				}
			}
		}
		st := a.shellUI()
		st.updateInstalling = false
		switch {
		case err != nil:
			// A staged-but-not-applied result is not a lost update: say it
			// installs on the next start, then show the failure reason.
			prefix := a.menuText("Could not install the update: ",
				"安装更新失败：")
			if outcome != nil && outcome.Staged {
				prefix = a.menuText("Update downloaded; it installs on the next start, but applying it now failed: ",
					"更新已下载，将在下次启动时安装，但立即应用失败：")
			}
			st.updateMessage = fmt.Sprintf("%s%v", prefix, err)
		case outcome == nil:
			st.updateMessage = a.menuText("Could not install the update.",
				"安装更新失败。")
		case outcome.UpToDate:
			st.updateMessage = a.menuText("You are up to date.",
				"当前已是最新版本。")
		default:
			st.updateStaged = &updater.Staged{
				Version: outcome.ReleaseVersion, Path: outcome.StagedPath,
			}
			st.updateInstalled = true
			version := outcome.ReleaseVersion
			if version == "" {
				version = rel.Version
			}
			st.updateMessage = fmt.Sprintf(a.menuText(
				"Update %s downloaded. Restart ZeoKeel to finish installing.",
				"更新 %s 已下载。重启 ZeoKeel 完成安装。"), version)
		}
		if c != nil {
			c.Invalidate()
		}
	}()
}

// defaultUpdateInstall downloads the asset, verifies it and schedules the
// install for the next start (the platform replaces the executable then).
func (a *App) defaultUpdateInstall(ctx context.Context, rel *updater.Release) (*updater.Staged, error) {
	cfg := a.updateConfig()
	if cfg.FeedURL == "" {
		return nil, updater.ErrDisabled
	}
	staged, err := updater.Download(ctx, cfg, *rel)
	if err != nil {
		return nil, err
	}
	if err := updater.ScheduleInstall(cfg, *staged); err != nil {
		return nil, err
	}
	return staged, nil
}

// updateInstallState describes the install action's label and enabled state.
func (a *App) updateInstallState() (label string, enabled bool, hint string) {
	st := a.shellUI()
	feed := a.updateFeedURL()
	switch {
	case feed == "":
		return a.menuText("Download and install", "下载并安装"), false,
			a.menuText("Configure update.feed_url (or ZEOKEEL_UPDATE_FEED) to enable updates.",
				"请配置 update.feed_url（或 ZEOKEEL_UPDATE_FEED）以启用更新。")
	case st.updateInstalling:
		return a.menuText("Downloading…", "正在下载…"), false,
			a.updateProgressHint()
	case st.updateInstalled:
		return a.menuText("Restart to finish", "重启完成安装"), false,
			a.menuText("The downloaded update installs when ZeoKeel restarts.",
				"重启 ZeoKeel 后完成安装。")
	case !st.updateAvailable:
		return a.menuText("Download and install", "下载并安装"), false,
			a.menuText("Check for updates first.", "请先检查更新。")
	default:
		return a.menuText("Download and install", "下载并安装"), true,
			a.menuText("Downloads the verified asset and installs it on restart.",
				"下载校验后的安装包，并在重启时安装。")
	}
}

// updateProgressHint renders the download progress line while installing.
func (a *App) updateProgressHint() string {
	st := a.shellUI()
	st.updateProgressMu.Lock()
	downloaded, total := st.updateProgressDownloaded, st.updateProgressTotal
	st.updateProgressMu.Unlock()
	base := a.menuText("The update downloads in the background; keep ZeoKeel open.",
		"更新在后台下载，请保持 ZeoKeel 运行。")
	if total <= 0 {
		return base
	}
	pct := downloaded * 100 / total
	if pct > 100 {
		pct = 100
	}
	return fmt.Sprintf(a.menuText("Downloading… %d%% (%d/%d bytes)", "正在下载… %d%%（%d/%d 字节）"),
		pct, downloaded, total)
}

// updateStatusText is the current status line of the Updates page.
func (a *App) updateStatusText() string {
	st := a.shellUI()
	if st.updateMessage != "" {
		return st.updateMessage
	}
	return a.menuText("Manual updates only.",
		"仅支持手动更新。")
}

// globalHotkeySupportNote explains platform support for the shortcut field.
func (a *App) globalHotkeySupportNote() (string, bool) {
	if runtime.GOOS == "windows" {
		return a.menuText("Register this shortcut system-wide while ZeoKeel runs.",
			"ZeoKeel 运行期间，在系统范围内注册该快捷键。"), true
	}
	return a.menuText("Global shortcuts are not supported on this platform yet (Windows only); the value is saved for a future build.",
		"此平台尚不支持全局快捷键（仅 Windows）；该值会保存，供后续版本使用。"), false
}

// hotkeyErrorText turns the globalhotkey parser's English errors into the UI
// language. The parser owns validation; the shell owns the explanation, and a
// Chinese UI must not leak "unknown key"/"needs at least one modifier".
func (a *App) hotkeyErrorText(err error) string {
	if err == nil {
		return ""
	}
	raw := err.Error()
	token := func(prefix string) string {
		if idx := strings.Index(raw, prefix); idx >= 0 {
			return strings.Trim(strings.TrimSpace(raw[idx+len(prefix):]), ".")
		}
		return ""
	}
	switch {
	case strings.Contains(raw, "empty hotkey"):
		return a.menuText("The shortcut cannot be empty.", "快捷键不能为空。")
	case strings.Contains(raw, "needs at least one modifier and a key"):
		return a.menuText("A shortcut needs at least one modifier and a key.",
			"快捷键至少需要一个修饰键和一个按键。")
	case strings.Contains(raw, "unknown modifier"):
		if name := token("unknown modifier"); name != "" {
			return fmt.Sprintf(a.menuText("Unknown modifier %s.", "未知修饰键 %s。"), name)
		}
		return a.menuText("Unknown modifier.", "未知修饰键。")
	case strings.Contains(raw, "duplicate modifier"):
		if name := token("duplicate modifier"); name != "" {
			return fmt.Sprintf(a.menuText("Duplicate modifier %s.", "重复的修饰键 %s。"), name)
		}
		return a.menuText("Duplicate modifier.", "重复的修饰键。")
	case strings.Contains(raw, "unknown key"):
		if name := token("unknown key"); name != "" {
			return fmt.Sprintf(a.menuText("Unknown key %s.", "未知按键 %s。"), name)
		}
		return a.menuText("Unknown key.", "未知按键。")
	default:
		return a.menuText("Invalid shortcut.", "快捷键无效。")
	}
}

// globalHotkeyRow is the Desktop page's shortcut field. The draft is persisted
// only when the spec parses, and an invalid value is explained inline instead
// of silently dropped.
func (a *App) globalHotkeyRow(c *ui.Context, t *design.Tokens) {
	st := a.shellUI()
	if st.textDrafts == nil {
		st.textDrafts = make(map[string]string)
	}
	draft, ok := st.textDrafts[settingGlobalHotkey]
	if !ok {
		draft = a.shellSetting(settingGlobalHotkey, "")
	}
	note, _ := a.globalHotkeySupportNote()
	saved := a.shellSetting(settingGlobalHotkey, "")
	errText := ""
	spec := strings.TrimSpace(draft)
	if spec != "" {
		if _, err := globalhotkey.ParseHotkey(spec); err != nil {
			errText = a.menuText("Invalid shortcut: ", "快捷键无效：") + a.hotkeyErrorText(err)
		} else if spec != saved {
			if err := a.setShellSetting(settingGlobalHotkey, spec); err != nil {
				errText = a.menuText("Could not save the setting", "设置保存失败")
			}
		}
	}
	a.settingsRow(c, t, a.menuText("Global shortcut", "全局快捷键"), note,
		settingsRowStyle{last: true}, func() {
			ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &draft).Label(a.menuText("Global shortcut", "全局快捷键")).
					Placeholder("Ctrl+Alt+Z").MinWidth(t.Theme().Space(40))
				if errText != "" {
					ui.Text(c, errText).FontSize(11).TextColor(t.Danger).MaxLines(2)
				}
			})
		})
	st.textDrafts[settingGlobalHotkey] = draft
}

// hotkeyTesterLabel exposes the parsed spec to the page without re-parsing the
// element tree: used by tests to keep the validation behavior observable.
func (a *App) hotkeyTesterLabel() string {
	note, supported := a.globalHotkeySupportNote()
	if supported {
		return note
	}
	return note
}
