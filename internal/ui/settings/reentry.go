package settings

// Page re-entry detection. Settings pages live in the module state across the
// whole session, so a transient success badge ("已复制", "已保存") would
// otherwise stay on the page forever, and values applied elsewhere would not be
// re-read when the user comes back. The host builds only the current page once
// per frame, so a page built with a frame number more than one ahead of its
// previous build was off screen in between (another page, or the closed
// settings surface). Same-frame rebuilds and the next frame keep it false, which
// is what makes it a page-re-entry test rather than a per-frame reset.

import (
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// transientStatus remembers the frame of the last build of one settings page.
type transientStatus struct {
	built bool
	frame uint64
}

// reentered reports whether the page is being built again after being off
// screen, and records the current frame. A nil runtime reports false.
func (ts *transientStatus) reentered(runtime contracts.UIRuntime) bool {
	if runtime == nil {
		return false
	}
	frame := runtime.Frame()
	reentered := ts.built && frame > ts.frame+1
	ts.built = true
	ts.frame = frame
	return reentered
}

// reenter refreshes the appearance page on re-entry: the immediately applied
// values (theme, language, desktop switches) are re-read from the store and the
// host locale, and the transient status line is cleared. A pending global-hotkey
// draft is the page's only explicit-save field and stays so the host can keep
// guarding it; its saved snapshot is refreshed instead.
func (st *appearanceState) reenter(store contracts.SettingsService, locale contracts.LocaleService) {
	pendingHotkey := st.dirty()
	if store != nil {
		st.themeMode = appearanceThemeMode(
			store.Get(contracts.SettingThemeMode, ""),
			store.Get(contracts.SettingsUIThemeDark, ""),
		)
		st.autostart = store.Get(contracts.SettingAutostart, "false") == "true"
		st.closeToTray = closeToTraySettingEnabled(store.Get(contracts.SettingCloseToTray, "false"))
		saved := strings.TrimSpace(store.Get(contracts.SettingGlobalHotkey, ""))
		st.savedHotkey = saved
		if !pendingHotkey {
			st.globalHotkey = saved
			st.hotkeyText = formatHotkeyDisplay(saved)
		}
	}
	if locale != nil {
		st.language = locale.Language()
	}
	st.capturing = false
	st.captureText = ""
	st.captureFocus = false
	st.hotkeyError = ""
	st.status = ""
	st.statusErr = false
}
