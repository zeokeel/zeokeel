// Package globalhotkey registers system-wide keyboard shortcuts (show/hide the
// window, new session, …) without requiring focus.
//
// # Status
//
// Implemented: Windows (RegisterHotKey message loop) and Linux/X11 (pure-Go
// dlopen of libX11 via purego). macOS stays ErrUnsupported (Carbon/Cocoa needs
// cgo, which the CGO_ENABLED=0 hard constraint forbids). Headless sessions on
// every platform degrade to ErrUnsupported and the shell falls back to in-app
// shortcuts.
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
//	Linux:   implemented for X11 sessions (x11_linux.go): pure-Go dlopen of the
//	         system libX11.so.6 via purego, XGrabKey with NumLock/CapsLock
//	         variants (none/Mod2/Lock/both), a dedicated XPending/XNextEvent
//	         pump, incremental XUngrabKey and XCloseDisplay on close. No cgo and
//	         no new module dependency (purego is already in the module graph).
//	         A grab conflict (BadAccess, surfaced through the Xlib error handler
//	         after XSync) maps to ErrOccupied. Headless (no $DISPLAY) and
//	         Wayland-only sessions without a usable X display return
//	         ErrUnsupported.
//	macOS:   implemented for GUI sessions (globalhotkey_darwin.go): pure-Go
//	         dlopen of Carbon.framework/HIToolbox via purego, using the
//	         deprecated but still-shipped Carbon hot-key API
//	         (GetApplicationEventTarget + InstallEventHandler +
//	         RegisterEventHotKey, dispatch on the kEventHotKeyPressed event).
//	         No cgo and no new module dependency. eventHotKeyExistsErr maps to
//	         ErrOccupied; a headless/non-GUI process without an application
//	         event target returns ErrUnsupported.
//
//	         Runtime caveat: this backend is cross-compiled for darwin
//	         amd64/arm64 but was not executed on a macOS host during
//	         development. It assumes 64-bit UPPs are plain function pointers
//	         (the documented 64-bit ABI), the 8-byte EventHotKeyID struct is
//	         passed as one 64-bit register (SysV/AAPCS64), and that the process
//	         has a running AppKit/Carbon event target; failures degrade to
//	         ErrUnsupported/ErrOccupied and never crash startup. A future round
//	         should validate on a real Mac and otherwise keep the CGEventTap
//	         helper-process option as a fallback.
//
// # Linux / Wayland deferred path
//
// Native Wayland forbids global key grabs by design. The supported API is the
// D-Bus desktop portal org.freedesktop.portal.GlobalShortcuts
// (GlobalShortcuts1), which needs a D-Bus client plus a portal implementation
// of a recent enough version on the desktop. ZeoKeel has no pure-Go D-Bus
// dependency today, and the portal API is still not implemented by several
// compositors, so a native Wayland backend is deferred: under Wayland sessions
// that expose no usable X display, New() returns ErrUnsupported and the shell
// keeps in-app shortcuts. When XWayland is present with working grabs, the X11
// path may work, depending on the compositor's security policy.
package globalhotkey
