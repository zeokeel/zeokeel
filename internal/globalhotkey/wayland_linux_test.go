//go:build linux

package globalhotkey

// TDD contract tests for the Wayland GlobalShortcuts (xdg-desktop-portal)
// backend. A fake portal is replayed over a real unix socket; no real desktop
// portal is required (and none can run in CI — see the documented unverified
// items in wayland_linux.go).
//
// Run: go test -count=1 ./internal/globalhotkey

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCreateCall is one observed CreateSession call.
type fakeCreateCall struct {
	Options     map[string]any
	RequestPath string
	SessionPath string
}

// fakeBindCall is one observed BindShortcuts call.
type fakeBindCall struct {
	Session      string
	Shortcuts    []dbusShortcut
	ParentWindow string
	Options      map[string]any
	RequestPath  string
}

// fakePortalConfig scripts the fake portal's behaviour.
type fakePortalConfig struct {
	version            uint32 // interface version reported via Properties.Get
	probeUnsupported   bool   // Properties.Get fails (portal too old/missing)
	createSilent       bool   // never answer CreateSession's Response signal
	createResponseCode uint32 // non-zero: report a failed CreateSession
	bindResponseSilent bool   // never answer BindShortcuts' Response signal
	bindResponseCode   uint32 // non-zero: report a failed bind
	bindRejectName     string // non-empty: D-Bus error reply to BindShortcuts
	bindRejectMessage  string
}

// fakePortal is a scripted org.freedesktop.portal.Desktop service.
type fakePortal struct {
	t   *testing.T
	cfg fakePortalConfig
	bus *fakeBusServer

	mu              sync.Mutex
	seq             int
	lastSessionPath string
	lastRequestPath string
	lastClosedPath  string

	createCalls chan fakeCreateCall
	bindCalls   chan fakeBindCall
	closeCalls  chan string
}

// newFakePortal starts the fake portal on a fresh unix socket.
func newFakePortal(t *testing.T, cfg fakePortalConfig) *fakePortal {
	t.Helper()
	if cfg.version == 0 {
		cfg.version = 2
	}
	p := &fakePortal{
		t:           t,
		cfg:         cfg,
		createCalls: make(chan fakeCreateCall, 16),
		bindCalls:   make(chan fakeBindCall, 16),
		closeCalls:  make(chan string, 16),
	}
	p.bus = newFakeBusServer(t, p.handle)
	return p
}

// addr returns the bus address for DBUS_SESSION_BUS_ADDRESS / connectDBus.
func (p *fakePortal) addr() string { return p.bus.addr }

// handle routes one incoming message.
func (p *fakePortal) handle(c *fakeBusConn, msg *dbusMessage) {
	switch {
	case msg.Interface == dbusBusName:
		switch msg.Member {
		case "Hello":
			c.reply(msg, "s", c.unique)
		case "AddMatch", "RemoveMatch":
			if msg.Flags&dbusFlagNoReplyExpected == 0 {
				c.reply(msg, "")
			}
		default:
			if msg.Flags&dbusFlagNoReplyExpected == 0 {
				c.errorReply(msg, "org.freedesktop.DBus.Error.UnknownMethod", msg.Member)
			}
		}
	case msg.Interface == propertiesInterface && msg.Member == "Get":
		p.handlePropertiesGet(c, msg)
	case msg.Interface == globalShortcutsInterface:
		p.handleGlobalShortcuts(c, msg)
	case msg.Interface == portalSessionInterface && msg.Member == "Close":
		p.mu.Lock()
		p.lastClosedPath = msg.Path
		p.mu.Unlock()
		select {
		case p.closeCalls <- msg.Path:
		default:
		}
		if msg.Flags&dbusFlagNoReplyExpected == 0 {
			c.reply(msg, "")
		}
	default:
		if msg.Flags&dbusFlagNoReplyExpected == 0 {
			c.errorReply(msg, "org.freedesktop.DBus.Error.UnknownMethod", msg.Interface+"."+msg.Member)
		}
	}
}

// handlePropertiesGet answers the version probe.
func (p *fakePortal) handlePropertiesGet(c *fakeBusConn, msg *dbusMessage) {
	values, err := msg.bodyValues()
	if err != nil || len(values) != 2 {
		c.errorReply(msg, "org.freedesktop.DBus.Error.InvalidArgs", "bad Properties.Get")
		return
	}
	iface, _ := values[0].(string)
	property, _ := values[1].(string)
	if p.cfg.probeUnsupported {
		c.errorReply(msg, "org.freedesktop.DBus.Error.UnknownInterface", "GlobalShortcuts is not available")
		return
	}
	if iface != globalShortcutsInterface || property != "version" {
		c.errorReply(msg, "org.freedesktop.DBus.Error.UnknownProperty", iface+"."+property)
		return
	}
	c.reply(msg, "v", dbusVariant{Signature: "u", Value: p.cfg.version})
}

// handleGlobalShortcuts implements CreateSession and BindShortcuts.
func (p *fakePortal) handleGlobalShortcuts(c *fakeBusConn, msg *dbusMessage) {
	switch msg.Member {
	case "CreateSession":
		values, err := msg.bodyValues()
		if err != nil || len(values) != 1 {
			c.errorReply(msg, "org.freedesktop.DBus.Error.InvalidArgs", "CreateSession")
			return
		}
		options, _ := values[0].(map[string]any)
		p.mu.Lock()
		p.seq++
		seq := p.seq
		sessionPath := fmt.Sprintf("%s/session/1_0/zeokeel_%d", portalObjectPath, seq)
		requestPath := fmt.Sprintf("%s/request/1_0/zeokeel_%d", portalObjectPath, seq)
		p.lastSessionPath = sessionPath
		p.lastRequestPath = requestPath
		p.mu.Unlock()
		select {
		case p.createCalls <- fakeCreateCall{Options: options, RequestPath: requestPath, SessionPath: sessionPath}:
		default:
			p.t.Errorf("createCalls channel full")
		}
		c.reply(msg, "o", requestPath)
		if p.cfg.createSilent {
			return
		}
		code := p.cfg.createResponseCode
		results := map[string]any{}
		if code == 0 {
			results["session_handle"] = dbusVariant{Signature: "s", Value: sessionPath}
		}
		c.signal(requestPath, portalRequestInterface, "Response", "ua{sv}", code, results)
	case "BindShortcuts":
		values, err := msg.bodyValues()
		if err != nil || len(values) != 4 {
			c.errorReply(msg, "org.freedesktop.DBus.Error.InvalidArgs", "BindShortcuts")
			return
		}
		session, _ := values[0].(string)
		rawShortcuts, _ := values[1].([]any)
		parentWindow, _ := values[2].(string)
		options, _ := values[3].(map[string]any)
		var shortcuts []dbusShortcut
		for _, raw := range rawShortcuts {
			fields, ok := raw.([]any)
			if !ok || len(fields) != 2 {
				p.t.Errorf("BindShortcuts: bad shortcut tuple %#v", raw)
				continue
			}
			id, _ := fields[0].(string)
			opts, _ := fields[1].(map[string]any)
			shortcuts = append(shortcuts, dbusShortcut{ID: id, Options: opts})
		}
		p.mu.Lock()
		p.seq++
		seq := p.seq
		requestPath := fmt.Sprintf("%s/request/1_0/zeokeel_bind_%d", portalObjectPath, seq)
		p.lastRequestPath = requestPath
		p.mu.Unlock()
		select {
		case p.bindCalls <- fakeBindCall{
			Session: session, Shortcuts: shortcuts, ParentWindow: parentWindow,
			Options: options, RequestPath: requestPath,
		}:
		default:
			p.t.Errorf("bindCalls channel full")
		}
		if p.cfg.bindRejectName != "" {
			c.errorReply(msg, p.cfg.bindRejectName, p.cfg.bindRejectMessage)
			return
		}
		c.reply(msg, "o", requestPath)
		if p.cfg.bindResponseSilent {
			return
		}
		c.signal(requestPath, portalRequestInterface, "Response", "ua{sv}", p.cfg.bindResponseCode, map[string]any{})
	case "ListShortcuts":
		if msg.Flags&dbusFlagNoReplyExpected == 0 {
			c.errorReply(msg, "org.freedesktop.DBus.Error.NotSupported", "ListShortcuts is not used")
		}
	default:
		if msg.Flags&dbusFlagNoReplyExpected == 0 {
			c.errorReply(msg, "org.freedesktop.DBus.Error.UnknownMethod", "GlobalShortcuts."+msg.Member)
		}
	}
}

// lastSession returns the most recently created session path.
func (p *fakePortal) lastSession() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastSessionPath
}

// lastClosed returns the most recently closed session path.
func (p *fakePortal) lastClosed() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastClosedPath
}

// activate emits the Activated signal for a shortcut id.
func (p *fakePortal) activate(shortcutID string) {
	conn := p.bus.lastConn()
	session := p.lastSession()
	if conn == nil || session == "" {
		p.t.Errorf("activate(%q): no connection or session", shortcutID)
		return
	}
	conn.signal(session, globalShortcutsInterface, "Activated", "osta{sv}",
		session, shortcutID, uint64(1700000000000),
		map[string]any{"activation_token": dbusVariant{Signature: "s", Value: "token-1"}})
}

// nextCreate returns the next observed CreateSession call.
func (p *fakePortal) nextCreate(t *testing.T) fakeCreateCall {
	t.Helper()
	select {
	case call := <-p.createCalls:
		return call
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for CreateSession")
		return fakeCreateCall{}
	}
}

// nextBind returns the next observed BindShortcuts call.
func (p *fakePortal) nextBind(t *testing.T) fakeBindCall {
	t.Helper()
	select {
	case call := <-p.bindCalls:
		return call
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for BindShortcuts")
		return fakeBindCall{}
	}
}

// nextClose returns the next observed Session.Close.
func (p *fakePortal) nextClose(t *testing.T) string {
	t.Helper()
	select {
	case path := <-p.closeCalls:
		return path
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Session.Close")
		return ""
	}
}

// waitFor polls cond until it turns true or the deadline expires.
func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// backendClose disconnects a backend the way Manager.Close would.
func backendClose(t *testing.T, b *waylandBackend) {
	t.Helper()
	if err := b.close(); err != nil {
		t.Fatalf("backend close: %v", err)
	}
}

// --- platform detection -----------------------------------------------------

// TestIsWaylandSession pins the session-type detection order (XDG_SESSION_TYPE
// then WAYLAND_DISPLAY).
func TestIsWaylandSession(t *testing.T) {
	cases := []struct {
		sessionType, waylandDisplay string
		want                        bool
	}{
		{"wayland", "", true},
		{"Wayland", "", true},
		{"x11", "wayland-0", true},
		{"x11", "", false},
		{"", "wayland-1", true},
		{"", "", false},
		{"tty", "", false},
	}
	for _, tc := range cases {
		t.Setenv("XDG_SESSION_TYPE", tc.sessionType)
		t.Setenv("WAYLAND_DISPLAY", tc.waylandDisplay)
		if got := isWaylandSession(); got != tc.want {
			t.Fatalf("isWaylandSession(XDG_SESSION_TYPE=%q, WAYLAND_DISPLAY=%q) = %v, want %v",
				tc.sessionType, tc.waylandDisplay, got, tc.want)
		}
	}
}

// TestSessionBusAddress covers the env override and the /run/user/$UID default.
func TestSessionBusAddress(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/tmp/custom-bus")
	if got := sessionBusAddress(); got != "unix:path=/tmp/custom-bus" {
		t.Fatalf("sessionBusAddress() = %q", got)
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	want := fmt.Sprintf("unix:path=/run/user/%d/bus", os.Getuid())
	if got := sessionBusAddress(); got != want {
		t.Fatalf("sessionBusAddress() = %q, want %q", got, want)
	}
}

// TestPortalTriggerMapping pins the XDG shortcuts-spec trigger vocabulary.
func TestPortalTriggerMapping(t *testing.T) {
	cases := []struct{ spec, want string }{
		{"Ctrl+Alt+Z", "CTRL+ALT+z"},
		{"shift+meta+f12", "SHIFT+LOGO+F12"},
		{"Meta+Space", "LOGO+space"},
		{"Ctrl+Shift+PageDown", "CTRL+SHIFT+Next"},
		{"Alt+1", "ALT+1"},
		{"Ctrl+Enter", "CTRL+Return"},
		{"Ctrl+Alt+Shift+Meta+Z", "CTRL+ALT+SHIFT+LOGO+z"},
		{"Ctrl+Tab", "CTRL+Tab"},
		{"Ctrl+Escape", "CTRL+Escape"},
		{"Ctrl+Backspace", "CTRL+BackSpace"},
		{"Ctrl+PageUp", "CTRL+Prior"},
		{"Ctrl+Delete", "CTRL+Delete"},
		{"Ctrl+Up", "CTRL+Up"},
		{"Ctrl+Home", "CTRL+Home"},
		{"Ctrl+Insert", "CTRL+Insert"},
	}
	for _, tc := range cases {
		h, err := ParseHotkey(tc.spec)
		if err != nil {
			t.Fatalf("ParseHotkey(%q): %v", tc.spec, err)
		}
		if got := portalTrigger(h); got != tc.want {
			t.Fatalf("portalTrigger(%q) = %q, want %q", tc.spec, got, tc.want)
		}
	}
}

// TestPortalShortcutID pins the stable application shortcut id.
func TestPortalShortcutID(t *testing.T) {
	h, err := ParseHotkey("Ctrl+Alt+Z")
	if err != nil {
		t.Fatalf("ParseHotkey: %v", err)
	}
	if got := portalShortcutID(h); got != "zeokeel-Ctrl+Alt+Z" {
		t.Fatalf("portalShortcutID = %q", got)
	}
}

// --- backend happy paths ----------------------------------------------------

// TestWaylandBackendRegisterBindAndActivate is the end-to-end fake-portal
// replay: CreateSession -> BindShortcuts -> Response -> Activated -> callback.
func TestWaylandBackendRegisterBindAndActivate(t *testing.T) {
	p := newFakePortal(t, fakePortalConfig{})
	b, err := newWaylandBackendAt(p.addr())
	if err != nil {
		t.Fatalf("newWaylandBackendAt: %v", err)
	}
	fired := make(chan struct{}, 1)
	h, err := ParseHotkey("Ctrl+Alt+Z")
	if err != nil {
		t.Fatalf("ParseHotkey: %v", err)
	}
	if err := b.register(7, h, func() { fired <- struct{}{} }); err != nil {
		t.Fatalf("register: %v", err)
	}

	create := p.nextCreate(t)
	for _, key := range []string{"handle_token", "session_handle_token"} {
		if v, ok := variantString(create.Options[key]); !ok || v == "" {
			t.Fatalf("CreateSession options[%q] = %#v", key, create.Options[key])
		}
	}
	bind := p.nextBind(t)
	if bind.Session != create.SessionPath {
		t.Fatalf("BindShortcuts session = %q, want %q", bind.Session, create.SessionPath)
	}
	if bind.ParentWindow != "" {
		t.Fatalf("BindShortcuts parent_window = %q, want empty", bind.ParentWindow)
	}
	if len(bind.Shortcuts) != 1 {
		t.Fatalf("BindShortcuts shortcuts = %#v", bind.Shortcuts)
	}
	sc := bind.Shortcuts[0]
	if sc.ID != "zeokeel-Ctrl+Alt+Z" {
		t.Fatalf("shortcut id = %q", sc.ID)
	}
	if v, _ := variantString(sc.Options["description"]); v == "" {
		t.Fatalf("shortcut description empty: %#v", sc.Options)
	}
	if v, _ := variantString(sc.Options["preferred_trigger"]); v != "CTRL+ALT+z" {
		t.Fatalf("preferred_trigger = %#v, want CTRL+ALT+z", sc.Options["preferred_trigger"])
	}
	if v, _ := variantString(bind.Options["handle_token"]); v == "" {
		t.Fatalf("BindShortcuts handle_token = %#v", bind.Options["handle_token"])
	}

	p.activate(sc.ID)
	select {
	case <-fired:
	case <-time.After(3 * time.Second):
		t.Fatal("Activated signal did not dispatch the callback")
	}

	backendClose(t, b)
	if closed := p.nextClose(t); closed != create.SessionPath {
		t.Fatalf("closed session = %q, want %q", closed, create.SessionPath)
	}
}

// TestWaylandBackendIgnoresStaleSessionActivation: only the current session's
// Activated signals may fire callbacks.
func TestWaylandBackendIgnoresStaleSessionActivation(t *testing.T) {
	p := newFakePortal(t, fakePortalConfig{})
	b, err := newWaylandBackendAt(p.addr())
	if err != nil {
		t.Fatalf("newWaylandBackendAt: %v", err)
	}
	defer backendClose(t, b)
	fired := make(chan struct{}, 4)
	h, _ := ParseHotkey("Ctrl+Alt+Z")
	if err := b.register(1, h, func() { fired <- struct{}{} }); err != nil {
		t.Fatalf("register: %v", err)
	}
	q := p.nextCreate(t)
	sc := p.nextBind(t).Shortcuts[0]
	conn := p.bus.lastConn()
	conn.signal("/org/freedesktop/portal/desktop/session/1_0/stale", globalShortcutsInterface,
		"Activated", "osta{sv}", "/org/freedesktop/portal/desktop/session/1_0/stale",
		sc.ID, uint64(1), map[string]any{})
	select {
	case <-fired:
		t.Fatal("stale-session Activated dispatched the callback")
	case <-time.After(120 * time.Millisecond):
	}
	conn.signal(q.SessionPath, globalShortcutsInterface, "Activated", "osta{sv}",
		q.SessionPath, sc.ID, uint64(2), map[string]any{})
	select {
	case <-fired:
	case <-time.After(3 * time.Second):
		t.Fatal("current-session Activated did not dispatch")
	}
}

// TestWaylandBackendSecondRegisterRebindsFreshSession: the portal allows one
// BindShortcuts attempt per session, so a changed set re-binds in a new session.
func TestWaylandBackendSecondRegisterRebindsFreshSession(t *testing.T) {
	p := newFakePortal(t, fakePortalConfig{})
	b, err := newWaylandBackendAt(p.addr())
	if err != nil {
		t.Fatalf("newWaylandBackendAt: %v", err)
	}
	defer backendClose(t, b)
	var mu sync.Mutex
	fired := map[string]int{}
	cb := func(name string) func() {
		return func() {
			mu.Lock()
			fired[name]++
			mu.Unlock()
		}
	}
	h1, _ := ParseHotkey("Ctrl+Alt+Z")
	h2, _ := ParseHotkey("Shift+F12")
	if err := b.register(1, h1, cb("one")); err != nil {
		t.Fatalf("register 1: %v", err)
	}
	c1 := p.nextCreate(t)
	b1 := p.nextBind(t)
	if len(b1.Shortcuts) != 1 {
		t.Fatalf("first bind shortcuts = %#v", b1.Shortcuts)
	}
	if err := b.register(2, h2, cb("two")); err != nil {
		t.Fatalf("register 2: %v", err)
	}
	if closed := p.nextClose(t); closed != c1.SessionPath {
		t.Fatalf("replaced session = %q, want %q", closed, c1.SessionPath)
	}
	c2 := p.nextCreate(t)
	b2 := p.nextBind(t)
	if c2.SessionPath == c1.SessionPath {
		t.Fatal("second registration reused the session that already bound shortcuts")
	}
	if len(b2.Shortcuts) != 2 {
		t.Fatalf("second bind shortcuts = %#v, want both", b2.Shortcuts)
	}
	p.activate("zeokeel-Ctrl+Alt+Z")
	p.activate("zeokeel-Shift+F12")
	waitFor(t, "both callbacks", 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return fired["one"] == 1 && fired["two"] == 1
	})
}

// TestWaylandBackendUnregisterLastClosesSession: removing the last shortcut
// closes the portal session, an unknown handle reports ErrUnknownHandle.
func TestWaylandBackendUnregisterLastClosesSession(t *testing.T) {
	p := newFakePortal(t, fakePortalConfig{})
	b, err := newWaylandBackendAt(p.addr())
	if err != nil {
		t.Fatalf("newWaylandBackendAt: %v", err)
	}
	defer backendClose(t, b)
	h, _ := ParseHotkey("Ctrl+Alt+Z")
	if err := b.register(1, h, func() {}); err != nil {
		t.Fatalf("register: %v", err)
	}
	session := p.nextCreate(t).SessionPath
	p.nextBind(t)
	if err := b.unregister(1); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if closed := p.nextClose(t); closed != session {
		t.Fatalf("closed session = %q, want %q", closed, session)
	}
	if err := b.unregister(1); !errors.Is(err, ErrUnknownHandle) {
		t.Fatalf("unregister(unknown) err = %v, want ErrUnknownHandle", err)
	}
}

// TestWaylandBackendCloseIdempotent covers close semantics and ErrClosed.
func TestWaylandBackendCloseIdempotent(t *testing.T) {
	p := newFakePortal(t, fakePortalConfig{})
	b, err := newWaylandBackendAt(p.addr())
	if err != nil {
		t.Fatalf("newWaylandBackendAt: %v", err)
	}
	if err := b.close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := b.close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	h, _ := ParseHotkey("Ctrl+Alt+Z")
	if err := b.register(1, h, func() {}); !errors.Is(err, ErrClosed) {
		t.Fatalf("register after close = %v, want ErrClosed", err)
	}
	if err := b.unregister(1); !errors.Is(err, ErrClosed) {
		t.Fatalf("unregister after close = %v, want ErrClosed", err)
	}
	// No session was ever created, so no Session.Close may be sent.
	select {
	case path := <-p.closeCalls:
		t.Fatalf("unexpected Session.Close for %q", path)
	case <-time.After(100 * time.Millisecond):
	}
}

// --- error paths ------------------------------------------------------------

// TestWaylandBackendPortalRejectsBind: a method-level rejection surfaces as an
// error and rolls the registration back.
func TestWaylandBackendPortalRejectsBind(t *testing.T) {
	p := newFakePortal(t, fakePortalConfig{
		bindRejectName:    "org.freedesktop.portal.Error.NotAllowed",
		bindRejectMessage: "the user does not allow shortcuts",
	})
	b, err := newWaylandBackendAt(p.addr())
	if err != nil {
		t.Fatalf("newWaylandBackendAt: %v", err)
	}
	defer backendClose(t, b)
	h, _ := ParseHotkey("Ctrl+Alt+Z")
	err = b.register(1, h, func() {})
	if err == nil {
		t.Fatal("register succeeded although the portal rejected BindShortcuts")
	}
	if errors.Is(err, ErrOccupied) || errors.Is(err, ErrUnsupported) {
		t.Fatalf("rejection classified as %v", err)
	}
	if !strings.Contains(err.Error(), "NotAllowed") {
		t.Fatalf("rejection error = %v, want portal error name", err)
	}
	if err := b.unregister(1); !errors.Is(err, ErrUnknownHandle) {
		t.Fatalf("failed registration left state behind: unregister = %v", err)
	}
}

// TestWaylandBackendOccupiedMapsToErrOccupied: conflict-style portal errors keep
// the package's ErrOccupied classification.
func TestWaylandBackendOccupiedMapsToErrOccupied(t *testing.T) {
	p := newFakePortal(t, fakePortalConfig{
		bindRejectName:    "org.freedesktop.portal.Error.Occupied",
		bindRejectMessage: "shortcut already taken",
	})
	b, err := newWaylandBackendAt(p.addr())
	if err != nil {
		t.Fatalf("newWaylandBackendAt: %v", err)
	}
	defer backendClose(t, b)
	h, _ := ParseHotkey("Ctrl+Alt+Z")
	err = b.register(1, h, func() {})
	if !errors.Is(err, ErrOccupied) {
		t.Fatalf("register err = %v, want ErrOccupied", err)
	}
}

// TestWaylandBackendCreateSessionTimeout: a portal that never answers the
// CreateSession request fails the registration instead of hanging forever.
func TestWaylandBackendCreateSessionTimeout(t *testing.T) {
	prev := waylandRequestTimeout
	waylandRequestTimeout = 150 * time.Millisecond
	t.Cleanup(func() { waylandRequestTimeout = prev })

	p := newFakePortal(t, fakePortalConfig{createSilent: true})
	b, err := newWaylandBackendAt(p.addr())
	if err != nil {
		t.Fatalf("newWaylandBackendAt: %v", err)
	}
	defer backendClose(t, b)
	h, _ := ParseHotkey("Ctrl+Alt+Z")
	start := time.Now()
	err = b.register(1, h, func() {})
	if err == nil {
		t.Fatal("register succeeded although CreateSession never answered")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("register err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("registration blocked for %v", elapsed)
	}
}

// TestWaylandBackendCreateSessionDenied: a non-zero portal response code is an
// error, not a silent success.
func TestWaylandBackendCreateSessionDenied(t *testing.T) {
	p := newFakePortal(t, fakePortalConfig{createResponseCode: 1})
	b, err := newWaylandBackendAt(p.addr())
	if err != nil {
		t.Fatalf("newWaylandBackendAt: %v", err)
	}
	defer backendClose(t, b)
	h, _ := ParseHotkey("Ctrl+Alt+Z")
	err = b.register(1, h, func() {})
	if err == nil {
		t.Fatal("register succeeded although the portal denied the session")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("denial looked like a timeout: %v", err)
	}
}

// TestWaylandProbeUnsupportedWhenPortalMissing: a portal without
// GlobalShortcuts degrades to ErrUnsupported (so X11 fallback can kick in).
func TestWaylandProbeUnsupportedWhenPortalMissing(t *testing.T) {
	p := newFakePortal(t, fakePortalConfig{probeUnsupported: true})
	if _, err := newWaylandBackendAt(p.addr()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("newWaylandBackendAt err = %v, want ErrUnsupported", err)
	}
}

// TestWaylandNewBackendHonorsWaylandEnv: the production constructor only takes
// the portal path when the session is Wayland and the bus address is honoured.
func TestWaylandNewBackendHonorsWaylandEnv(t *testing.T) {
	p := newFakePortal(t, fakePortalConfig{})
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", p.addr())
	b, err := newWaylandBackend()
	if err != nil {
		t.Fatalf("newWaylandBackend: %v", err)
	}
	backendClose(t, b)

	t.Setenv("XDG_SESSION_TYPE", "x11")
	t.Setenv("WAYLAND_DISPLAY", "")
	if _, err := newWaylandBackend(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("newWaylandBackend on x11 err = %v, want ErrUnsupported", err)
	}
}

// TestWaylandBackendReconnectsAfterDisconnect: a dropped bus connection is
// re-established on the next registration.
func TestWaylandBackendReconnectsAfterDisconnect(t *testing.T) {
	p := newFakePortal(t, fakePortalConfig{})
	b, err := newWaylandBackendAt(p.addr())
	if err != nil {
		t.Fatalf("newWaylandBackendAt: %v", err)
	}
	defer backendClose(t, b)
	if got := p.bus.connectionCount(); got != 1 {
		t.Fatalf("initial connections = %d, want 1", got)
	}
	p.bus.dropConnections()
	waitFor(t, "client to notice the disconnect", 3*time.Second, func() bool {
		b.mu.Lock()
		client := b.client
		b.mu.Unlock()
		return client == nil || client.broken()
	})
	h, _ := ParseHotkey("Ctrl+Alt+Z")
	if err := b.register(1, h, func() {}); err != nil {
		t.Fatalf("register after disconnect: %v", err)
	}
	if got := p.bus.connectionCount(); got < 2 {
		t.Fatalf("connections after reconnect = %d, want >= 2", got)
	}
	p.nextCreate(t)
	p.nextBind(t)
}

// --- platform wiring --------------------------------------------------------

// markerBackend is a stand-in backend for wiring tests.
type markerBackend struct{ closed bool }

func (m *markerBackend) register(int, Hotkey, func()) error { return nil }
func (m *markerBackend) unregister(int) error               { return nil }
func (m *markerBackend) close() error                       { m.closed = true; return nil }

// TestPlatformBackendPrefersWaylandPortal: on a Wayland session the portal
// backend wins when it is available.
func TestPlatformBackendPrefersWaylandPortal(t *testing.T) {
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	want := &markerBackend{}
	prev := waylandBackendFactory
	waylandBackendFactory = func() (backend, error) { return want, nil }
	t.Cleanup(func() { waylandBackendFactory = prev })

	got, err := platformBackend()
	if err != nil {
		t.Fatalf("platformBackend: %v", err)
	}
	if got != want {
		t.Fatalf("platformBackend = %T, want the portal backend", got)
	}
}

// TestPlatformBackendWaylandPortalFailureFallsBackToX11: XWayland keeps working
// when the portal is missing.
func TestPlatformBackendWaylandPortalFailureFallsBackToX11(t *testing.T) {
	withFakeX11(t) // sets DISPLAY and a fake libX11 table
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	prev := waylandBackendFactory
	waylandBackendFactory = func() (backend, error) {
		return nil, fmt.Errorf("%w: no portal", ErrUnsupported)
	}
	t.Cleanup(func() { waylandBackendFactory = prev })

	got, err := platformBackend()
	if err != nil {
		t.Fatalf("platformBackend fallback: %v", err)
	}
	if _, ok := got.(*x11Backend); !ok {
		t.Fatalf("platformBackend = %T, want the X11 fallback", got)
	}
}

// TestPlatformBackendWaylandWithoutPortalOrX11Unsupported: the documented
// degradation is ErrUnsupported, not a hard failure.
func TestPlatformBackendWaylandWithoutPortalOrX11Unsupported(t *testing.T) {
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DISPLAY", "")
	prev := waylandBackendFactory
	waylandBackendFactory = func() (backend, error) {
		return nil, fmt.Errorf("%w: no portal", ErrUnsupported)
	}
	t.Cleanup(func() { waylandBackendFactory = prev })

	if _, err := platformBackend(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("platformBackend err = %v, want ErrUnsupported", err)
	}
}

// TestPlatformBackendNonWaylandUsesX11: an X11/tty session never pays for the
// D-Bus portal probe (X11 path unchanged).
func TestPlatformBackendNonWaylandUsesX11(t *testing.T) {
	withFakeX11(t)
	t.Setenv("XDG_SESSION_TYPE", "x11")
	t.Setenv("WAYLAND_DISPLAY", "")
	prev := waylandBackendFactory
	waylandBackendFactory = func() (backend, error) {
		t.Error("portal backend must not be probed outside Wayland sessions")
		return nil, ErrUnsupported
	}
	t.Cleanup(func() { waylandBackendFactory = prev })

	got, err := platformBackend()
	if err != nil {
		t.Fatalf("platformBackend: %v", err)
	}
	if _, ok := got.(*x11Backend); !ok {
		t.Fatalf("platformBackend = %T, want the X11 backend", got)
	}
}

// TestWaylandBindRejectionClosesPortalSession: when the portal answers
// BindShortcuts with a non-zero response code (the user refused, or the portal
// failed), the backend must release the portal session it created instead of
// leaking a bound-but-useless session.
func TestWaylandBindRejectionClosesPortalSession(t *testing.T) {
	p := newFakePortal(t, fakePortalConfig{bindResponseCode: 2})
	b, err := newWaylandBackendAt(p.addr())
	if err != nil {
		t.Fatalf("newWaylandBackendAt: %v", err)
	}
	defer backendClose(t, b)
	h, err := ParseHotkey("Ctrl+Alt+Z")
	if err != nil {
		t.Fatalf("ParseHotkey: %v", err)
	}
	if err := b.register(1, h, func() {}); err != nil {
		t.Fatalf("register: %v", err)
	}
	created := p.nextCreate(t)
	p.nextBind(t)
	if closed := p.nextClose(t); closed != created.SessionPath {
		t.Fatalf("rejected BindShortcuts leaked its portal session: closed = %q, want %q", closed, created.SessionPath)
	}
}
