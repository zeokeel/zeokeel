package app

import "log"

// Runtime autostart reconciliation (PD-12).
//
// The shell persists the app.autostart boolean; the app makes the OS agree with
// it. Each platform has its own registration mechanism (XDG autostart entry,
// Windows Run key, macOS LaunchAgent). Failures are logged and never block
// startup: autostart is a convenience, not a reason to refuse to run.

// syncAutostartFromSetting reconciles the platform autostart registration with
// the persisted app.autostart setting.
func (a *app) syncAutostartFromSetting() {
	enabled := a.settingBool("app.autostart", false)
	if err := setAutostart(enabled); err != nil {
		log.Printf("zeokeel: autostart %v: %v", enabled, err)
	}
}
