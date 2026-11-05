// Package globalhotkey registers system-wide keyboard shortcuts (show/hide the
// window, new session, …) without requiring focus.
//
// # TDD state
//
// Round 1 is the seam contract only: ParseHotkey, Manager and the platform
// backend split are defined, but every function returns ErrNotImplemented. The
// tests in globalhotkey_test.go are RED by design; the platform implementation
// lands in the same round (Windows RegisterHotKey; Linux/macOS documented as
// unsupported for now).
//
// # Design
//
//   - A Hotkey is a modifier set plus one key: "Ctrl+Alt+Z", "Meta+Space",
//     "Shift+F12". At least one modifier is required: a bare key would steal a
//     normal typing key from every application.
//   - Manager owns registration bookkeeping (duplicate detection, handles,
//     close semantics) and delegates the actual OS registration to a backend.
//   - The backend seam (`backend`) is unexported and injected in tests, so the
//     logic is testable on any platform; New() selects the real backend by
//     GOOS: Windows uses RegisterHotKey, Linux/macOS return ErrUnsupported
//     until a dependency-free implementation exists (see docs/BUGS.md).
//   - Registration failures from the OS are surfaced as ErrOccupied (another
//     application already owns the shortcut) or wrapped errors; callers keep
//     the app usable instead of failing startup.
//
// # Platform support
//
//	Windows: implemented (RegisterHotKey on a dedicated locked OS thread with a
//	         message loop; WM_HOTKEY dispatches callbacks on a goroutine).
//	Linux:   ErrUnsupported — a pure-Go implementation needs an X11 client
//	         (xgb-style) or a portal; tracked as a BUGS item, no new dependency
//	         is added for it.
//	macOS:   ErrUnsupported — RegisterEventHotKey needs Carbon/Cocoa (cgo),
//	         which violates the CGO_ENABLED=0 hard constraint; a future
//	         CGEventTap-based helper process could be evaluated.
package globalhotkey
