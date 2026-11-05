//go:build linux

package globalhotkey

// Linux has no dependency-free way to register system-wide hotkeys:
//   - X11 needs an X11 protocol client (the common Go option is a third-party
//     xgb/xgb-go dependency) plus a key-grab/event-loop implementation;
//   - Wayland forbids global key grabs by design and requires a desktop portal
//     (org.freedesktop.portal.GlobalShortcuts), which is a D-Bus client;
//   - reading /dev/input bypasses the display server and needs elevated
//     permissions, so it is not a viable product path.
//
// Adding another module dependency for this feature is a product/architecture
// decision outside this package, so New() reports ErrUnsupported and the shell
// stays on in-app shortcuts. Tracked as a BUGS item (PD-12 global shortcuts).
func platformBackend() (backend, error) {
	return nil, ErrUnsupported
}
