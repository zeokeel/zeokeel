//go:build linux

package globalhotkey

// Linux Wayland global-shortcut backend: the xdg-desktop-portal
// org.freedesktop.portal.GlobalShortcuts interface, spoken through the minimal
// pure-Go D-Bus client in dbus_linux.go. No cgo, no new module dependency.
//
// Protocol subset (version 1 of the interface, version 2 is backwards
// compatible for what is used here):
//
//	CreateSession(a{sv} options, out o handle)
//	  -> Request::Response(u response, a{sv} results{ session_handle })
//	BindShortcuts(o session_handle, a(sa{sv}) shortcuts, s parent_window,
//	              a{sv} options{ handle_token }, out o request_handle)
//	  -> Request::Response(u response, a{sv} results)
//	GlobalShortcuts::Activated(o session_handle, s shortcut_id, t timestamp,
//	                           a{sv} options{ activation_token })
//	org.freedesktop.portal.Session.Close()
//
// Each registered hotkey becomes a portal shortcut whose id is derived from the
// canonical hotkey spec (portalShortcutID), with a user-readable description and
// a preferred_trigger in the XDG shortcuts specification vocabulary (e.g.
// "CTRL+ALT+z"). Activated signals are demultiplexed by session and shortcut id
// and the Manager callback runs on its own goroutine, matching the X11/Windows
// backends' contract.
//
// # Async consent step
//
// CreateSession is synchronous (the portal answers the Request::Response without
// any user interaction). BindShortcuts is deliberately NOT awaited: a portal
// answers it only after showing the user a configuration dialog, and blocking
// app startup on a dialog would be wrong. Registration therefore succeeds once
// the portal accepts the BindShortcuts method call; if the user later cancels,
// the Request::Response is logged and the session is marked dead (the shortcuts
// stay inactive). This is documented as a known limitation.
//
// # Degradation
//
// Detection order: Wayland session ($XDG_SESSION_TYPE=wayland or
// $WAYLAND_DISPLAY set) -> the GlobalShortcuts portal; when the portal (or its
// D-Bus session bus) is unavailable the backend reports ErrUnsupported and
// platformBackend falls back to the X11/XWayland path. Non-Wayland sessions never
// touch D-Bus. Headless Wayland sessions with neither portal nor X display
// degrade to ErrUnsupported so the shell can keep in-app shortcuts.
//
// # Unverified against real desktops
//
// The protocol subset above is implemented from the published portal and D-Bus
// specifications and exercised against a fake portal over a unix socket (plus a
// real dbus-daemon for the transport). It has NOT been validated against a real
// GNOME/KDE portal: the compositor-specific behaviour of BindShortcuts consent,
// shortcut id persistence across sessions and Activated delivery are unverified
// here and should be exercised on a real Wayland desktop before advertising the
// feature.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// D-Bus portal constants.
const (
	portalBusName              = "org.freedesktop.portal.Desktop"
	portalObjectPath           = "/org/freedesktop/portal/desktop"
	globalShortcutsInterface   = "org.freedesktop.portal.GlobalShortcuts"
	portalRequestInterface     = "org.freedesktop.portal.Request"
	portalSessionInterface     = "org.freedesktop.portal.Session"
	propertiesInterface        = "org.freedesktop.DBus.Properties"
	globalShortcutsMinVersion  = 1
	portalShortcutIDPrefix     = "zeokeel-"
	portalSessionTokenPrefix   = "zeokeel_"
	portalBindUndeliveredLimit = 32
)

// Timeouts are variables so tests can shorten them.
var (
	// waylandRequestTimeout bounds one portal round trip (connect, probe,
	// CreateSession, method-call replies).
	waylandRequestTimeout = 5 * time.Second
	// waylandBindResponseTimeout bounds the background wait for the
	// user-consent Response of BindShortcuts.
	waylandBindResponseTimeout = 60 * time.Second
	// waylandBackendFactory is the seam platformBackend uses; tests replace it.
	waylandBackendFactory = func() (backend, error) { return newWaylandBackend() }
)

// isWaylandSession reports whether the process runs in a Wayland session.
// XDG_SESSION_TYPE is authoritative; WAYLAND_DISPLAY is the documented fallback
// (it is also set by nested compositors and xwayland-run wrappers).
func isWaylandSession() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE")), "wayland") {
		return true
	}
	return strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != ""
}

// sessionBusAddress returns the session D-Bus address to connect to.
func sessionBusAddress() string {
	if address := strings.TrimSpace(os.Getenv("DBUS_SESSION_BUS_ADDRESS")); address != "" {
		return address
	}
	if uid := os.Getuid(); uid >= 0 {
		return fmt.Sprintf("unix:path=/run/user/%d/bus", uid)
	}
	return ""
}

// waylandShortcut is one registration tracked by the backend.
type waylandShortcut struct {
	id       int
	portalID string
	hotkey   Hotkey
	fn       func()
}

// waylandBackend implements backend on top of one portal session.
type waylandBackend struct {
	address string

	mu      sync.Mutex // guards client/session/regs/closed
	opMu    sync.Mutex // serializes session creation/binding operations
	client  *dbusClient
	session string
	regs    map[int]*waylandShortcut
	closed  bool

	reqMu   sync.Mutex // guards waiters/early
	waiters map[string]chan *dbusMessage
	early   map[string]*dbusMessage

	tokenSeq atomic.Uint64
	done     chan struct{}
}

// newWaylandBackend returns the Wayland portal backend for the current session.
// It returns ErrUnsupported when this is not a Wayland session or when the
// portal cannot be reached, so platformBackend can keep the X11 path.
func newWaylandBackend() (*waylandBackend, error) {
	if !isWaylandSession() {
		return nil, fmt.Errorf("%w: not a Wayland session", ErrUnsupported)
	}
	return newWaylandBackendAt(sessionBusAddress())
}

// newWaylandBackendAt connects to the given bus address, says Hello and probes
// the GlobalShortcuts portal. Tests pass a fake portal's unix socket here.
func newWaylandBackendAt(address string) (*waylandBackend, error) {
	ctx, cancel := context.WithTimeout(context.Background(), waylandRequestTimeout)
	defer cancel()
	client, err := connectDBus(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("%w: session bus unavailable: %v", ErrUnsupported, err)
	}
	b := &waylandBackend{
		address: address,
		client:  client,
		regs:    make(map[int]*waylandShortcut),
		waiters: make(map[string]chan *dbusMessage),
		early:   make(map[string]*dbusMessage),
		done:    make(chan struct{}),
	}
	go b.forwardSignals(client)
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), waylandRequestTimeout)
	defer cancelProbe()
	if err := b.probePortal(probeCtx, client); err != nil {
		_ = client.close()
		return nil, err
	}
	b.addMatch(client)
	return b, nil
}

// probePortal verifies the portal owns org.freedesktop.portal.Desktop and
// exposes a usable org.freedesktop.portal.GlobalShortcuts version. Any failure
// degrades to ErrUnsupported (documented fallback contract).
func (b *waylandBackend) probePortal(ctx context.Context, client *dbusClient) error {
	reply, err := client.call(ctx, portalBusName, portalObjectPath, propertiesInterface, "Get",
		"ss", globalShortcutsInterface, "version")
	if err != nil {
		return fmt.Errorf("%w: GlobalShortcuts portal unavailable: %v", ErrUnsupported, err)
	}
	values, err := reply.bodyValues()
	if err != nil || len(values) != 1 {
		return fmt.Errorf("%w: malformed GlobalShortcuts version reply", ErrUnsupported)
	}
	variant, ok := values[0].(dbusVariant)
	if !ok || variant.Signature != "u" {
		return fmt.Errorf("%w: malformed GlobalShortcuts version property", ErrUnsupported)
	}
	version, ok := variant.Value.(uint32)
	if !ok || version < globalShortcutsMinVersion {
		return fmt.Errorf("%w: GlobalShortcuts portal version %v is too old", ErrUnsupported, variant.Value)
	}
	return nil
}

// addMatch asks the bus for portal signals. Portal Request::Response and
// GlobalShortcuts::Activated signals are emitted unicast to the requesting
// connection, so this is defensive for portals that broadcast instead.
func (b *waylandBackend) addMatch(client *dbusClient) {
	ctx, cancel := context.WithTimeout(context.Background(), waylandRequestTimeout)
	defer cancel()
	rules := []string{
		"type='signal',interface='" + portalRequestInterface + "',member='Response'",
		"type='signal',interface='" + globalShortcutsInterface + "'",
	}
	for _, rule := range rules {
		_ = client.notify(ctx, dbusBusName, dbusObjectPath, dbusBusName, "AddMatch", "s", rule)
	}
}

// forwardSignals routes the signals of one connection until it closes.
func (b *waylandBackend) forwardSignals(client *dbusClient) {
	for {
		select {
		case msg, ok := <-client.signals:
			if !ok {
				return
			}
			b.routeSignal(msg)
		case <-b.done:
			return
		}
	}
}

// routeSignal dispatches the portal signals this backend consumes.
func (b *waylandBackend) routeSignal(msg *dbusMessage) {
	switch {
	case msg.Interface == portalRequestInterface && msg.Member == "Response":
		b.deliverResponse(msg)
	case msg.Interface == globalShortcutsInterface && msg.Member == "Activated":
		b.dispatchActivated(msg)
	case msg.Interface == globalShortcutsInterface && msg.Member == "Deactivated":
		// No deactivation state is tracked: a shortcut is either bound and
		// waiting for Activated or absent from the session.
	}
}

// deliverResponse hands a Request::Response signal to the goroutine waiting for
// that request path (or buffers it briefly, closing the method-return/signal
// race).
func (b *waylandBackend) deliverResponse(msg *dbusMessage) {
	b.reqMu.Lock()
	if waitCh, ok := b.waiters[msg.Path]; ok {
		delete(b.waiters, msg.Path)
		b.reqMu.Unlock()
		select {
		case waitCh <- msg:
		default:
		}
		return
	}
	if len(b.early) >= portalBindUndeliveredLimit {
		b.early = make(map[string]*dbusMessage)
	}
	b.early[msg.Path] = msg
	b.reqMu.Unlock()
}

// awaitResponse waits for the Response signal of one request object.
func (b *waylandBackend) awaitResponse(requestPath string, timeout time.Duration) (*dbusMessage, error) {
	b.reqMu.Lock()
	if msg, ok := b.early[requestPath]; ok {
		delete(b.early, requestPath)
		b.reqMu.Unlock()
		return msg, nil
	}
	waitCh := make(chan *dbusMessage, 1)
	b.waiters[requestPath] = waitCh
	b.reqMu.Unlock()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case msg := <-waitCh:
		return msg, nil
	case <-timer.C:
		b.reqMu.Lock()
		delete(b.waiters, requestPath)
		b.reqMu.Unlock()
		return nil, context.DeadlineExceeded
	case <-b.done:
		b.reqMu.Lock()
		delete(b.waiters, requestPath)
		b.reqMu.Unlock()
		return nil, errDBusClosed
	}
}

// dispatchActivated decodes Activated and runs the matching manager callback.
func (b *waylandBackend) dispatchActivated(msg *dbusMessage) {
	values, err := msg.bodyValues()
	if err != nil || len(values) != 4 {
		log.Printf("globalhotkey: malformed GlobalShortcuts.Activated signal: %v", err)
		return
	}
	session, _ := values[0].(string)
	shortcutID, _ := values[1].(string)
	// The activation token is part of the protocol and is parsed for forward
	// compatibility, but the package callback seam carries no token yet.
	if options, ok := values[3].(map[string]any); ok {
		_, _ = variantString(options["activation_token"])
	}
	b.mu.Lock()
	if b.closed || session != b.session {
		b.mu.Unlock()
		return
	}
	var fn func()
	for _, reg := range b.regs {
		if reg.portalID == shortcutID {
			fn = reg.fn
			break
		}
	}
	b.mu.Unlock()
	if fn != nil {
		go fn()
	}
}

// register adds the hotkey and (re)binds the session's shortcut set.
func (b *waylandBackend) register(id int, h Hotkey, fn func()) error {
	if fn == nil {
		return fmt.Errorf("%w: nil callback", ErrInvalidHotkey)
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrClosed
	}
	if _, exists := b.regs[id]; exists {
		b.mu.Unlock()
		return fmt.Errorf("%w: handle %d", ErrAlreadyRegistered, id)
	}
	b.regs[id] = &waylandShortcut{id: id, portalID: portalShortcutID(h), hotkey: h, fn: fn}
	b.mu.Unlock()

	b.opMu.Lock()
	defer b.opMu.Unlock()
	if err := b.rebindLocked(); err != nil {
		b.mu.Lock()
		delete(b.regs, id)
		b.mu.Unlock()
		return err
	}
	return nil
}

// unregister drops the hotkey and rebinds the remaining set (a portal session
// can only be bound once, so a changed set needs a fresh session).
func (b *waylandBackend) unregister(id int) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrClosed
	}
	if _, exists := b.regs[id]; !exists {
		b.mu.Unlock()
		return fmt.Errorf("%w: %d", ErrUnknownHandle, id)
	}
	delete(b.regs, id)
	b.mu.Unlock()

	b.opMu.Lock()
	defer b.opMu.Unlock()
	return b.rebindLocked()
}

// close releases the portal session and the bus connection. Idempotent.
func (b *waylandBackend) close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()

	b.opMu.Lock()
	client := b.currentClient()
	b.closeSessionLocked(client)
	b.mu.Lock()
	client = b.client
	b.client = nil
	b.regs = make(map[int]*waylandShortcut)
	b.mu.Unlock()
	b.opMu.Unlock()

	close(b.done)
	if client != nil {
		_ = client.close()
	}
	return nil
}

// rebindLocked recreates the portal session with the current registration set.
// Caller holds opMu.
func (b *waylandBackend) rebindLocked() error {
	// Defensive re-check: a concurrent close must not resurrect a connection or
	// open a new portal session.
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return ErrClosed
	}
	client, err := b.ensureClientLocked()
	if err != nil {
		return err
	}
	// A session may only BindShortcuts once; release the old one first.
	b.closeSessionLocked(client)

	b.mu.Lock()
	shortcuts := make([]*waylandShortcut, 0, len(b.regs))
	for _, reg := range b.regs {
		shortcuts = append(shortcuts, reg)
	}
	b.mu.Unlock()
	if len(shortcuts) == 0 {
		return nil
	}
	sort.Slice(shortcuts, func(i, j int) bool { return shortcuts[i].portalID < shortcuts[j].portalID })

	session, err := b.createSessionLocked(client)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.session = session
	b.mu.Unlock()
	if err := b.bindShortcutsLocked(client, session, shortcuts); err != nil {
		b.closeSessionLocked(client)
		return err
	}
	return nil
}

// ensureClientLocked returns a healthy bus connection, reconnecting once if the
// previous one broke. Caller holds opMu.
func (b *waylandBackend) ensureClientLocked() (*dbusClient, error) {
	client := b.currentClient()
	if client != nil && !client.broken() {
		return client, nil
	}
	if client != nil {
		_ = client.close()
	}
	b.mu.Lock()
	b.session = ""
	b.client = nil
	b.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), waylandRequestTimeout)
	defer cancel()
	fresh, err := connectDBus(ctx, b.address)
	if err != nil {
		return nil, fmt.Errorf("%w: session bus reconnection failed: %v", ErrUnsupported, err)
	}
	b.mu.Lock()
	b.client = fresh
	b.mu.Unlock()
	go b.forwardSignals(fresh)
	b.addMatch(fresh)
	return fresh, nil
}

// currentClient returns the current bus client under the state lock.
func (b *waylandBackend) currentClient() *dbusClient {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.client
}

// closeSessionLocked closes the current portal session (best effort). Caller
// holds opMu.
func (b *waylandBackend) closeSessionLocked(client *dbusClient) {
	b.mu.Lock()
	session := b.session
	b.session = ""
	b.mu.Unlock()
	closePortalSession(client, session)
}

// closePortalSession releases one portal session object (best effort). A
// session whose grant was refused or whose binding failed must be closed
// explicitly: the portal keeps the session (and any partial shortcut grant)
// alive until the connection drops otherwise.
func closePortalSession(client *dbusClient, session string) {
	if session == "" || client == nil || client.broken() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), waylandRequestTimeout)
	defer cancel()
	_ = client.notify(ctx, portalBusName, session, portalSessionInterface, "Close", "")
}

// createSessionLocked creates one portal session and waits for its handle.
// Caller holds opMu.
func (b *waylandBackend) createSessionLocked(client *dbusClient) (string, error) {
	token := b.nextToken()
	options := map[string]any{
		"handle_token":         token,
		"session_handle_token": token,
	}
	ctx, cancel := context.WithTimeout(context.Background(), waylandRequestTimeout)
	defer cancel()
	reply, err := client.call(ctx, portalBusName, portalObjectPath, globalShortcutsInterface,
		"CreateSession", "a{sv}", options)
	if err != nil {
		return "", portalOperationError("CreateSession", err)
	}
	values, err := reply.bodyValues()
	if err != nil || len(values) != 1 {
		return "", fmt.Errorf("globalhotkey: CreateSession: malformed reply (%v)", err)
	}
	requestPath, ok := values[0].(string)
	if !ok || requestPath == "" {
		return "", errors.New("globalhotkey: CreateSession returned no request handle")
	}
	response, err := b.awaitResponse(requestPath, waylandRequestTimeout)
	if err != nil {
		return "", fmt.Errorf("globalhotkey: CreateSession response: %w", err)
	}
	responseValues, err := response.bodyValues()
	if err != nil || len(responseValues) != 2 {
		return "", fmt.Errorf("globalhotkey: CreateSession: malformed response (%v)", err)
	}
	responseCode, _ := responseValues[0].(uint32)
	if responseCode != 0 {
		return "", fmt.Errorf("globalhotkey: CreateSession was refused by the portal (response code %d)", responseCode)
	}
	results, _ := responseValues[1].(map[string]any)
	session, ok := variantString(results["session_handle"])
	if !ok || session == "" {
		return "", errors.New("globalhotkey: CreateSession response has no session_handle")
	}
	return session, nil
}

// bindShortcutsLocked sends BindShortcuts and arranges for its user-consent
// response to be inspected asynchronously. Caller holds opMu.
func (b *waylandBackend) bindShortcutsLocked(client *dbusClient, session string, shortcuts []*waylandShortcut) error {
	token := b.nextToken()
	list := make([]dbusShortcut, 0, len(shortcuts))
	for _, shortcut := range shortcuts {
		list = append(list, dbusShortcut{
			ID: shortcut.portalID,
			Options: map[string]any{
				"description":       portalShortcutDescription(shortcut.hotkey),
				"preferred_trigger": portalTrigger(shortcut.hotkey),
			},
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), waylandRequestTimeout)
	defer cancel()
	reply, err := client.call(ctx, portalBusName, portalObjectPath, globalShortcutsInterface,
		"BindShortcuts", "oa(sa{sv})sa{sv}", session, list, "", map[string]any{"handle_token": token})
	if err != nil {
		return portalOperationError("BindShortcuts", err)
	}
	values, err := reply.bodyValues()
	if err != nil || len(values) != 1 {
		return fmt.Errorf("globalhotkey: BindShortcuts: malformed reply (%v)", err)
	}
	requestPath, ok := values[0].(string)
	if !ok || requestPath == "" {
		return errors.New("globalhotkey: BindShortcuts returned no request handle")
	}
	b.mu.Lock()
	b.session = session
	b.mu.Unlock()
	go b.watchBindResponse(client, requestPath, session)
	return nil
}

// watchBindResponse inspects the asynchronous BindShortcuts consent response.
// The shortcuts are already considered registered; a refusal is only logged and
// invalidates the session so a later registration can retry.
func (b *waylandBackend) watchBindResponse(client *dbusClient, requestPath, session string) {
	response, err := b.awaitResponse(requestPath, waylandBindResponseTimeout)
	if err != nil {
		if errors.Is(err, errDBusClosed) || errors.Is(err, context.DeadlineExceeded) {
			select {
			case <-b.done:
				return
			default:
			}
		}
		log.Printf("globalhotkey: Wayland global shortcuts were not acknowledged by the desktop portal: %v", err)
		return
	}
	values, err := response.bodyValues()
	if err != nil || len(values) != 2 {
		log.Printf("globalhotkey: malformed BindShortcuts response: %v", err)
		b.releaseRejectedSession(client, session)
		return
	}
	if code, _ := values[0].(uint32); code != 0 {
		log.Printf("globalhotkey: the desktop portal refused to bind the Wayland global shortcuts (response code %d); they will not activate", code)
		b.releaseRejectedSession(client, session)
	}
}

// releaseRejectedSession closes a portal session whose shortcut binding was
// refused, so a rejected BindShortcuts attempt does not leak a live session.
// The session is only released while it is still the backend's current one: a
// later registration may already have replaced it.
func (b *waylandBackend) releaseRejectedSession(client *dbusClient, session string) {
	b.mu.Lock()
	owned := b.session == session
	if owned {
		b.session = ""
	}
	b.mu.Unlock()
	if owned {
		closePortalSession(client, session)
	}
}

// nextToken returns a fresh valid object-path token.
func (b *waylandBackend) nextToken() string {
	return fmt.Sprintf("%s%d_%d", portalSessionTokenPrefix, os.Getpid(), b.tokenSeq.Add(1))
}

// portalShortcutID derives the stable portal shortcut id from the hotkey spec.
func portalShortcutID(h Hotkey) string { return portalShortcutIDPrefix + h.String() }

// portalShortcutDescription is the user-readable description shown by the portal.
func portalShortcutDescription(h Hotkey) string {
	return "ZeoKeel shortcut " + h.String()
}

// portalTrigger renders the hotkey in the XDG shortcuts-specification trigger
// vocabulary: xkbcommon modifier names (CTRL, ALT, SHIFT, LOGO) and xkbcommon
// keysym names without the XKB_KEY_ prefix, joined by '+'.
func portalTrigger(h Hotkey) string {
	var parts []string
	if h.Mods&ModCtrl != 0 {
		parts = append(parts, "CTRL")
	}
	if h.Mods&ModAlt != 0 {
		parts = append(parts, "ALT")
	}
	if h.Mods&ModShift != 0 {
		parts = append(parts, "SHIFT")
	}
	if h.Mods&ModMeta != 0 {
		parts = append(parts, "LOGO")
	}
	parts = append(parts, portalKeyName(h.Key))
	return strings.Join(parts, "+")
}

// portalKeyName maps a canonical Hotkey.Key to its xkbcommon keysym name.
func portalKeyName(key string) string {
	if len(key) == 1 {
		// Base-layer letters/digits are the lowercase xkbcommon keysym names.
		return key
	}
	named := map[string]string{
		"space": "space", "tab": "Tab", "enter": "Return", "escape": "Escape",
		"backspace": "BackSpace", "up": "Up", "down": "Down", "left": "Left",
		"right": "Right", "home": "Home", "end": "End", "pageup": "Prior",
		"pagedown": "Next", "insert": "Insert", "delete": "Delete",
	}
	if name, ok := named[key]; ok {
		return name
	}
	return strings.ToUpper(key) // f1..f24 -> F1..F24
}

// portalOperationError classifies a failed portal operation into the package's
// sentinel errors where possible (ErrUnsupported for a missing/short interface,
// ErrOccupied for conflict-style errors).
func portalOperationError(operation string, err error) error {
	var reply *dbusErrorReply
	if !errors.As(err, &reply) {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("globalhotkey: %s timed out: %w", operation, err)
		}
		return fmt.Errorf("globalhotkey: %s: %w", operation, err)
	}
	lower := strings.ToLower(reply.Name + " " + reply.Message)
	switch {
	case strings.Contains(reply.Name, "ServiceUnknown"),
		strings.Contains(reply.Name, "NameHasNoOwner"),
		strings.Contains(reply.Name, "UnknownMethod"),
		strings.Contains(reply.Name, "UnknownInterface"),
		strings.Contains(reply.Name, "UnknownObject"),
		strings.Contains(reply.Name, "UnknownProperty"),
		strings.Contains(reply.Name, "NotSupported"):
		return fmt.Errorf("%w: %s: %v", ErrUnsupported, operation, err)
	case strings.Contains(lower, "occupied"),
		strings.Contains(lower, "already"),
		strings.Contains(lower, "exists"),
		strings.Contains(lower, "in use"),
		strings.Contains(lower, "inuse"),
		strings.Contains(lower, "taken"):
		return fmt.Errorf("%w: %s: %v", ErrOccupied, operation, err)
	}
	return fmt.Errorf("globalhotkey: %s: %v", operation, err)
}
