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
	settingUpdateFeed   = "update.feed_url"
	settingGlobalHotkey = "ui.global_hotkey"
	updateCheckTimeout  = 20 * time.Second
)

// updateCheckState is the Updates page's persisted-in-memory state.
type updateCheckState struct {
	checking bool
	// message is the last result, already localized.
	message string
}

// checkForUpdate is the injectable update seam. nil uses the updater engine.
var checkForUpdate func(app *App, ctx context.Context) (*updater.Release, bool, error)

// defaultUpdateCheck runs the updater engine against the configured feed. A
// build without a feed (no update.feed_url setting and no
// ZEOKEEL_UPDATE_FEED) reports ErrDisabled so the page can explain it.
func (a *App) defaultUpdateCheck(ctx context.Context) (*updater.Release, bool, error) {
	feed := strings.TrimSpace(a.shellSetting(settingUpdateFeed, ""))
	if feed == "" {
		feed = strings.TrimSpace(os.Getenv("ZEOKEEL_UPDATE_FEED"))
	}
	if feed == "" {
		return nil, false, updater.ErrDisabled
	}
	return updater.Check(ctx, updater.Config{
		CurrentVersion: BuildVersion,
		FeedURL:        feed,
		DataDir:        a.pluginDataDir(),
		Replacer:       nil,
	})
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
		default:
			st.updateMessage = a.menuText("You are up to date.",
				"当前已是最新版本。")
		}
		if c != nil {
			c.Invalidate()
		}
	}()
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
			errText = fmt.Sprintf(a.menuText("Invalid shortcut: %v", "快捷键无效：%v"), err)
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
