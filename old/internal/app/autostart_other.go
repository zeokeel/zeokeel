//go:build !linux && !windows && !darwin

package app

// setAutostart is a no-op on platforms without a per-user login autostart
// mechanism supported by this build. The setting is still persisted by the
// shell; the app logs no error, so toggling it never breaks a session.
func setAutostart(bool) error { return nil }
