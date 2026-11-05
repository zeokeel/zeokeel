//go:build linux

package globalhotkey

// Linux backend: X11 only (see x11_linux.go), implemented with purego dlopen of
// the system libX11.so.6 — no cgo and no new module dependency.
//
// Headless sessions (no $DISPLAY) and Wayland sessions that expose no usable X
// display degrade to ErrUnsupported; the shell then stays on in-app shortcuts.
// Wayland's org.freedesktop.portal.GlobalShortcuts remains the future path for
// a native Wayland implementation (it needs a D-Bus client).
func platformBackend() (backend, error) {
	return newX11Backend()
}
