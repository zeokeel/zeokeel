// Package globalhotkey registers system-wide keyboard shortcuts (show/hide the
// window, new session, …) without requiring focus.
//
// # Status
//
// Implemented:
//   - Windows (RegisterHotKey message loop);
//   - Linux/X11 (pure-Go dlopen of libX11 via purego);
//   - Linux/Wayland via the xdg-desktop-portal org.freedesktop.portal.GlobalShortcuts
//     interface, spoken through a minimal pure-Go D-Bus client
//     (wayland_linux.go + dbus_linux.go);
//   - macOS (pure-Go dlopen of Carbon/HIToolbox).
//
// Headless sessions on every platform degrade to ErrUnsupported and the shell
// falls back to in-app shortcuts.
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
//     GOOS: Windows uses RegisterHotKey, Linux picks X11 or the Wayland portal,
//     macOS uses Carbon.
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
//	Linux/Wayland: implemented (wayland_linux.go): detection order is Wayland
//	         session ($XDG_SESSION_TYPE=wayland or $WAYLAND_DISPLAY set) ->
//	         org.freedesktop.portal.GlobalShortcuts; X11/XWayland remains the
//	         fallback when the portal is missing, and non-Wayland sessions never
//	         touch D-Bus. The portal flow is CreateSession -> BindShortcuts ->
//	         Activated, with a minimal pure-Go D-Bus client (AF_UNIX session bus,
//	         SASL EXTERNAL, Hello, method_call/reply, signals, little-endian
//	         encoding and both byte orders on decode). BindShortcuts consent is
//	         asynchronous: registration succeeds when the portal accepts the
//	         method call instead of blocking app startup on the user dialog. A
//	         changed shortcut set is rebound in a fresh session because the
//	         portal allows only one BindShortcuts request per session.
//	         Caveat: implemented from the published portal/D-Bus specifications
//	         and tested against a fake portal over a unix socket (plus a real
//	         dbus-daemon for the transport), but NOT validated against a real
//	         GNOME/KDE portal. Compositor-specific consent dialogs, shortcut-id
//	         persistence and Activated delivery should be verified on a real
//	         Wayland desktop before advertising the feature.
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
package globalhotkey
