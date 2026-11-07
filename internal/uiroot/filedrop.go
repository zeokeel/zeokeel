package uiroot

import "sync"

// fileDropService is the host's file-drop bridge: the window's OnFileDrop
// callback calls Dispatch on the main thread, and a UI module owns the handler
// that stages the paths.
type fileDropService struct {
	mu      sync.Mutex
	handler func(paths []string)
}

// SetHandler installs the drop handler (nil clears it).
func (s *fileDropService) SetHandler(fn func(paths []string)) {
	s.mu.Lock()
	s.handler = fn
	s.mu.Unlock()
}

// Dispatch delivers one drop to the installed handler. The slice is copied so
// a handler may keep it; a nil handler is a no-op.
func (s *fileDropService) Dispatch(paths []string) {
	if len(paths) == 0 {
		return
	}
	copied := append([]string(nil), paths...)
	s.mu.Lock()
	handler := s.handler
	s.mu.Unlock()
	if handler != nil {
		handler(copied)
	}
}

// FileDrop returns the host file-drop service (contracts.KeyFileDrop).
func (r *Root) FileDrop() *fileDropService { return r.fileDrop }
