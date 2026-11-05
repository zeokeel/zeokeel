//go:build !windows && !linux && !darwin

package globalhotkey

// No global-hotkey backend exists for this platform.
func platformBackend() (backend, error) {
	return nil, ErrUnsupported
}
