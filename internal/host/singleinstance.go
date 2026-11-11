package host

// Single-instance policy: on desktop launches the process always keeps one
// instance and hands later launches to it (mygo's
// RequestSingleInstanceLock/OnSecondInstance). The lock handshake needs the GUI
// event loop, so only the fixed setting-to-bool decision is unit-tested;
// runDesktop carries the lock itself.

// singleInstanceEnabled reports whether the process keeps one instance. It is
// always true (A3 fixed behavior): the old ui.singleInstance control was removed
// from Settings, so a stored opt-out from an older version no longer disables
// the lock. The raw parameter is kept for the existing call sites.
func singleInstanceEnabled(_ string) bool {
	return true
}

// singleInstanceSetting reports the single-instance decision. The settings store
// is accepted for the existing caller but never read: later launches are always
// handed to the first instance.
func singleInstanceSetting(_ *Settings) bool {
	return true
}
