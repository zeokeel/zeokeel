//go:build darwin

package globalhotkey

// macOS global hotkeys need RegisterEventHotKey (Carbon) or a CGEventTap plus
// an auxiliary process; both require cgo or a helper binary. ZeoKeel is
// CGO_ENABLED=0 and single-binary (hard constraints C1/C3), so this package
// reports ErrUnsupported on macOS instead of shipping a half-working backend.
//
// A future option is a small signed helper app that owns the hotkey and talks to
// the main process over a socket; that is a separate release line (see
// docs/BUGS.md PD-12).
func platformBackend() (backend, error) {
	return nil, ErrUnsupported
}
