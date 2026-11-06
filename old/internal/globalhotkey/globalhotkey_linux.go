//go:build linux

package globalhotkey

import "fmt"

// Linux backend selection.
//
// Wayland sessions cannot use global key grabs: the compositor-owned
// org.freedesktop.portal.GlobalShortcuts interface is the only supported path
// (see wayland_linux.go). Detection order is therefore:
//
//  1. Wayland session ($XDG_SESSION_TYPE=wayland or $WAYLAND_DISPLAY set) ->
//     the GlobalShortcuts portal;
//  2. X11 (see x11_linux.go), also as the fallback for Wayland sessions that
//     still expose a usable XWayland display;
//  3. otherwise ErrUnsupported, and the shell keeps in-app shortcuts.
//
// Non-Wayland sessions never touch D-Bus: they go straight to the X11 backend,
// preserving its existing behaviour.
func platformBackend() (backend, error) {
	if isWaylandSession() {
		portalB, portalErr := waylandBackendFactory()
		if portalErr == nil {
			return portalB, nil
		}
		// A Wayland session may still have a usable XWayland display; keep the
		// long-standing X11 fallback before giving up.
		x11B, x11Err := newX11Backend()
		if x11Err == nil {
			return x11B, nil
		}
		return nil, fmt.Errorf("%w: Wayland global shortcuts unavailable (portal: %v; X11: %v)",
			ErrUnsupported, portalErr, x11Err)
	}
	return newX11Backend()
}
