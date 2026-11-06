//go:build linux

package globalhotkey

// TDD contract tests for the minimal pure-Go D-Bus client used by the Wayland
// GlobalShortcuts backend (dbus_linux.go).
//
// The wire format is pinned byte-for-byte here: the fake portal used by
// wayland_linux_test.go shares the same codec, so only these exact-bytes tests
// (plus the optional real dbus-daemon test) protect against a self-consistent
// but non-conformant implementation.
//
// Run: go test -count=1 ./internal/globalhotkey

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// decodeHex turns an annotated, whitespace-separated hex dump into bytes.
func decodeHex(t *testing.T, dump string) []byte {
	t.Helper()
	var compact strings.Builder
	for _, line := range strings.Split(dump, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		for _, r := range line {
			if r != ' ' && r != '\t' {
				compact.WriteRune(r)
			}
		}
	}
	b, err := hex.DecodeString(compact.String())
	if err != nil {
		t.Fatalf("decodeHex(%q): %v", dump, err)
	}
	return b
}

// TestMarshalHelloCallExactBytes pins the complete wire image of the very first
// message this client sends: org.freedesktop.DBus.Hello.
func TestMarshalHelloCallExactBytes(t *testing.T) {
	msg := &dbusMessage{
		Type:        dbusMethodCall,
		Serial:      1,
		Path:        "/org/freedesktop/DBus",
		Interface:   "org.freedesktop.DBus",
		Member:      "Hello",
		Destination: "org.freedesktop.DBus",
	}
	got, err := marshalDBusMessage(msg)
	if err != nil {
		t.Fatalf("marshalDBusMessage: %v", err)
	}
	want := decodeHex(t, `
		6c 01 00 01 00 00 00 00 01 00 00 00 6d 00 00 00
		01 01 6f 00 15 00 00 00 2f 6f 72 67 2f 66 72 65 65
		64 65 73 6b 74 6f 70 2f 44 42 75 73 00 00 00
		02 01 73 00 14 00 00 00 6f 72 67 2e 66 72 65 65 64
		65 73 6b 74 6f 70 2e 44 42 75 73 00 00 00 00
		03 01 73 00 05 00 00 00 48 65 6c 6c 6f 00 00 00
		06 01 73 00 14 00 00 00 6f 72 67 2e 66 72 65 65 64
		65 73 6b 74 6f 70 2e 44 42 75 73 00 00 00 00`)
	if !bytes.Equal(got, want) {
		t.Fatalf("Hello wire bytes mismatch\n got %x\nwant %x", got, want)
	}
}

// TestMarshalBodyDictAlignment pins array length/padding/variant alignment for
// an a{sv} body: length excludes padding, dict entries are 8-aligned, and a
// variant's payload is aligned to its own type.
func TestMarshalBodyDictAlignment(t *testing.T) {
	got, err := marshalDBusBody("a{sv}", map[string]any{"a": uint32(1)})
	if err != nil {
		t.Fatalf("marshalDBusBody: %v", err)
	}
	want := decodeHex(t, `
		10 00 00 00                    # array data length: 16 bytes
		00 00 00 00                    # padding to dict-entry alignment (8)
		01 00 00 00 61 00              # key string "a"
		01 75 00                       # variant signature "u"
		00 00 00                       # padding to uint32 alignment
		01 00 00 00                    # value 1
	`)
	if !bytes.Equal(got, want) {
		t.Fatalf("a{sv} body mismatch\n got %x\nwant %x", got, want)
	}
}

// TestMarshalBodyEmptyDict: an empty array still carries the alignment padding
// for its element type (spec: padding is required even when n is zero).
func TestMarshalBodyEmptyDict(t *testing.T) {
	got, err := marshalDBusBody("a{sv}", map[string]any{})
	if err != nil {
		t.Fatalf("marshalDBusBody: %v", err)
	}
	want := decodeHex(t, `00 00 00 00  00 00 00 00`)
	if !bytes.Equal(got, want) {
		t.Fatalf("empty a{sv} mismatch: got %x want %x", got, want)
	}
}

// TestMarshalBodyStructAndArray covers the BindShortcuts argument shape
// a(sa{sv}) and plain structs; the exact bytes also guard dict ordering.
func TestMarshalBodyShortcutList(t *testing.T) {
	shortcuts := []dbusShortcut{{
		ID: "zeokeel-Ctrl+Alt+Z",
		Options: map[string]any{
			"description":       "Show ZeoKeel",
			"preferred_trigger": "CTRL+ALT+z",
		},
	}}
	got, err := marshalDBusBody("a(sa{sv})", shortcuts)
	if err != nil {
		t.Fatalf("marshalDBusBody: %v", err)
	}
	// Decoding must round-trip the same values (the fake portal relies on it).
	values, err := unmarshalDBusBody("a(sa{sv})", got, binary.LittleEndian)
	if err != nil {
		t.Fatalf("unmarshalDBusBody: %v", err)
	}
	if len(values) != 1 {
		t.Fatalf("decoded %d values, want 1", len(values))
	}
	structs, ok := values[0].([]any)
	if !ok || len(structs) != 1 {
		t.Fatalf("decoded array = %#v", values[0])
	}
	fields, ok := structs[0].([]any)
	if !ok || len(fields) != 2 {
		t.Fatalf("decoded struct = %#v", structs[0])
	}
	if fields[0] != "zeokeel-Ctrl+Alt+Z" {
		t.Fatalf("decoded shortcut id = %#v", fields[0])
	}
	opts, ok := fields[1].(map[string]any)
	if !ok {
		t.Fatalf("decoded options = %#v", fields[1])
	}
	if v, ok := variantString(opts["preferred_trigger"]); !ok || v != "CTRL+ALT+z" {
		t.Fatalf("decoded preferred_trigger = %#v", opts["preferred_trigger"])
	}
	if v, ok := variantString(opts["description"]); !ok || v != "Show ZeoKeel" {
		t.Fatalf("decoded description = %#v", opts["description"])
	}
}

// TestMarshalMessageWithBody checks body length/padding wiring on a full
// message: header ends on an 8-byte boundary, body length is exact.
func TestMarshalMessageWithBody(t *testing.T) {
	msg := &dbusMessage{
		Type:        dbusMethodReturn,
		Serial:      9,
		ReplySerial: 4,
		Signature:   "ua{sv}",
	}
	var err error
	msg.Body, err = marshalDBusBody("ua{sv}", uint32(0), map[string]any{})
	if err != nil {
		t.Fatalf("marshalDBusBody: %v", err)
	}
	wire, err := marshalDBusMessage(msg)
	if err != nil {
		t.Fatalf("marshalDBusMessage: %v", err)
	}
	if len(wire)%8 != len(msg.Body)%8 || len(wire) < len(msg.Body) {
		t.Fatalf("wire length %d body length %d", len(wire), len(msg.Body))
	}
	if got := binary.LittleEndian.Uint32(wire[4:8]); int(got) != len(msg.Body) {
		t.Fatalf("wire body length = %d, want %d", got, len(msg.Body))
	}
	if !bytes.HasSuffix(wire, msg.Body) {
		t.Fatal("wire does not end with the encoded body")
	}
	decoded, err := readDBusMessage(bytes.NewReader(wire))
	if err != nil {
		t.Fatalf("readDBusMessage: %v", err)
	}
	if decoded.Type != dbusMethodReturn || decoded.Serial != 9 || decoded.ReplySerial != 4 || decoded.Signature != "ua{sv}" {
		t.Fatalf("decoded header = %#v", decoded)
	}
	values, err := decoded.bodyValues()
	if err != nil {
		t.Fatalf("bodyValues: %v", err)
	}
	if len(values) != 2 || values[0] != uint32(0) {
		t.Fatalf("decoded values = %#v", values)
	}
}

// TestReadMessageBigEndian: the peer may answer in big-endian order.
func TestReadMessageBigEndian(t *testing.T) {
	wire := decodeHex(t, `
		42 02 00 01                    # 'B', METHOD_RETURN, flags 0, version 1
		00 00 00 04                    # body length = 4
		00 00 00 02                    # serial = 2
		00 00 00 0f                    # header fields length = 15 (padding excluded)
		05 01 75 00                    # REPLY_SERIAL, variant signature "u"
		00 00 00 01                    # reply serial = 1
		08 01 67 00                    # SIGNATURE, variant signature "g"
		01 75 00                       # signature "u"
		00                             # padding to the 8-byte body boundary
		01 02 03 04                    # body: uint32 0x01020304, big-endian
	`)
	msg, err := readDBusMessage(bytes.NewReader(wire))
	if err != nil {
		t.Fatalf("readDBusMessage: %v", err)
	}
	if msg.ReplySerial != 1 || msg.Serial != 2 || msg.Type != dbusMethodReturn {
		t.Fatalf("decoded header = %#v", msg)
	}
	values, err := msg.bodyValues()
	if err != nil {
		t.Fatalf("bodyValues: %v", err)
	}
	if len(values) != 1 || values[0] != uint32(0x01020304) {
		t.Fatalf("decoded values = %#v", values)
	}
}

// TestReadMessageRejectsInvalid covers framing validation: bad endianness flag,
// unsupported protocol version and absurd lengths must not be trusted.
func TestReadMessageRejectsInvalid(t *testing.T) {
	cases := map[string][]byte{
		"bad endianness": decodeHex(t, `58 02 00 01 00 00 00 00 00 00 00 02 00 00 00 00`),
		"bad version":    decodeHex(t, `6c 02 00 07 00 00 00 00 00 00 00 02 00 00 00 00`),
		"huge body":      decodeHex(t, `6c 02 00 01 ff ff ff ff 00 00 00 02 00 00 00 00`),
		"huge fields":    decodeHex(t, `6c 02 00 01 00 00 00 00 00 00 00 02 ff ff ff ff`),
	}
	for name, wire := range cases {
		if _, err := readDBusMessage(bytes.NewReader(wire)); err == nil {
			t.Fatalf("%s: readDBusMessage accepted corrupt input", name)
		}
	}
}

// TestMarshalBodyRejectsBadValues: type/signature mismatches surface as errors
// instead of panics.
func TestMarshalBodyRejectsBadValues(t *testing.T) {
	cases := []struct {
		sig    string
		values []any
	}{
		{"s", []any{uint32(1)}},
		{"a{sv}", []any{"not a dict"}},
		{"a(sa{sv})", []any{"not a list"}},
		{"u", nil},
		{"a{sv}", []any{map[string]any{"k": struct{}{}}}},
	}
	for _, tc := range cases {
		if _, err := marshalDBusBody(tc.sig, tc.values...); err == nil {
			t.Fatalf("marshalDBusBody(%q, %#v) accepted a bad value", tc.sig, tc.values)
		}
	}
}

// --- fake bus transport helpers (shared with wayland_linux_test.go) ---------

// fakeBusConn is one authenticated fake-bus connection. Writes are serialized
// and messages carry a monotonically increasing serial.
type fakeBusConn struct {
	conn   net.Conn
	unique string

	mu     sync.Mutex
	serial uint32
}

// send writes one message to the peer.
func (c *fakeBusConn) send(msg *dbusMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if msg.Serial == 0 {
		c.serial++
		msg.Serial = c.serial
	}
	if msg.Destination == "" {
		msg.Destination = c.unique
	}
	wire, err := marshalDBusMessage(msg)
	if err != nil {
		return
	}
	_, _ = c.conn.Write(wire)
}

// reply sends a method return with the given body signature/values.
func (c *fakeBusConn) reply(req *dbusMessage, signature string, values ...any) {
	body, err := marshalDBusBody(signature, values...)
	if err != nil {
		return
	}
	c.send(&dbusMessage{
		Type:        dbusMethodReturn,
		ReplySerial: req.Serial,
		Signature:   signature,
		Body:        body,
	})
}

// errorReply sends a D-Bus error reply.
func (c *fakeBusConn) errorReply(req *dbusMessage, name, text string) {
	var body []byte
	if text != "" {
		body, _ = marshalDBusBody("s", text)
	}
	c.send(&dbusMessage{
		Type:        dbusError,
		ReplySerial: req.Serial,
		ErrorName:   name,
		Signature:   "s",
		Body:        body,
	})
}

// signal emits a unicast signal from the fake service to the client.
func (c *fakeBusConn) signal(path, iface, member, signature string, values ...any) {
	body, err := marshalDBusBody(signature, values...)
	if err != nil {
		return
	}
	c.send(&dbusMessage{
		Type:      dbusSignal,
		Path:      path,
		Interface: iface,
		Member:    member,
		Sender:    portalBusName,
		Signature: signature,
		Body:      body,
	})
}

// fakeBusServer is a unix-socket server that performs the SASL EXTERNAL
// handshake and then delegates every binary message to a handler.
type fakeBusServer struct {
	t      *testing.T
	addr   string
	ln     net.Listener
	handle func(*fakeBusConn, *dbusMessage)

	mu     sync.Mutex
	conns  []*fakeBusConn
	guid   string
	closed bool
}

// newFakeBusServer starts a fake bus at a fresh unix socket.
func newFakeBusServer(t *testing.T, handle func(*fakeBusConn, *dbusMessage)) *fakeBusServer {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "bus")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen unix %s: %v", path, err)
	}
	s := &fakeBusServer{
		t:      t,
		addr:   "unix:path=" + path,
		ln:     ln,
		handle: handle,
		guid:   "0123456789abcdef0123456789abcdef",
	}
	go s.acceptLoop()
	t.Cleanup(s.close)
	return s
}

// acceptLoop accepts connections until the listener closes.
func (s *fakeBusServer) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		c := &fakeBusConn{conn: conn, unique: s.nextUnique()}
		s.mu.Lock()
		s.conns = append(s.conns, c)
		s.mu.Unlock()
		go s.serveConn(c)
	}
}

// nextUnique allocates a unique connection name.
func (s *fakeBusServer) nextUnique() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ":1." + itoa(len(s.conns)+1)
}

// serveConn performs the SASL handshake and pumps messages.
func (s *fakeBusServer) serveConn(c *fakeBusConn) {
	defer c.conn.Close()
	br := bufio.NewReader(c.conn)
	// Client sends a single NUL byte before the SASL line.
	if _, err := br.ReadByte(); err != nil {
		return
	}
	line, err := readSASLLine(br)
	if err != nil || !strings.HasPrefix(line, "AUTH EXTERNAL ") {
		return
	}
	if _, err := c.conn.Write([]byte("OK " + s.guid + "\r\n")); err != nil {
		return
	}
	line, err = readSASLLine(br)
	if err != nil || line != "BEGIN" {
		return
	}
	for {
		msg, err := readDBusMessage(br)
		if err != nil {
			return
		}
		s.handle(c, msg)
	}
}

// close stops the server and drops all connections.
func (s *fakeBusServer) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	conns := append([]*fakeBusConn(nil), s.conns...)
	s.mu.Unlock()
	_ = s.ln.Close()
	for _, c := range conns {
		_ = c.conn.Close()
	}
}

// connectionCount reports how many connections were accepted.
func (s *fakeBusServer) connectionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// lastConn returns the most recently accepted connection.
func (s *fakeBusServer) lastConn() *fakeBusConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.conns) == 0 {
		return nil
	}
	return s.conns[len(s.conns)-1]
}

// dropConnections closes every open connection (a portal going away).
func (s *fakeBusServer) dropConnections() {
	s.mu.Lock()
	conns := append([]*fakeBusConn(nil), s.conns...)
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.conn.Close()
	}
}

// readSASLLine reads one CRLF-terminated SASL command line.
func readSASLLine(br *bufio.Reader) (string, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

// itoa is a tiny decimal helper so the test file needs no strconv import churn.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// --- D-Bus client tests -----------------------------------------------------

// helloHandler is the minimal bus handler every fake bus needs: answer Hello
// with the connection's unique name and ignore no-reply bookkeeping calls.
func helloHandler(c *fakeBusConn, msg *dbusMessage) bool {
	if msg.Interface == "org.freedesktop.DBus" && msg.Member == "Hello" {
		c.reply(msg, "s", c.unique)
		return true
	}
	return false
}

// TestDBusClientHelloCallAndError exercises authentication, Hello, a normal
// method call, a D-Bus error reply and clean shutdown against the fake bus.
func TestDBusClientHelloCallAndError(t *testing.T) {
	var echoed *dbusMessage
	srv := newFakeBusServer(t, func(c *fakeBusConn, msg *dbusMessage) {
		if helloHandler(c, msg) {
			return
		}
		switch msg.Member {
		case "Echo":
			echoed = msg
			c.reply(msg, "s", "hello world")
		case "Boom":
			c.errorReply(msg, "org.freedesktop.portal.Error.NotAllowed", "not allowed")
		case "FireAndForget":
			// NO_REPLY_EXPECTED: the client must not wait for a reply.
		}
	})
	client, err := connectDBus(context.Background(), srv.addr)
	if err != nil {
		t.Fatalf("connectDBus: %v", err)
	}
	if client.uniqueName() == "" || !strings.HasPrefix(client.uniqueName(), ":1.") {
		t.Fatalf("uniqueName() = %q", client.uniqueName())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reply, err := client.call(ctx, "org.example.Echo", "/org/example", "org.example.Test", "Echo", "s", "ping")
	if err != nil {
		t.Fatalf("call Echo: %v", err)
	}
	values, err := reply.bodyValues()
	if err != nil {
		t.Fatalf("Echo body: %v", err)
	}
	if len(values) != 1 || values[0] != "hello world" {
		t.Fatalf("Echo values = %#v", values)
	}
	if echoed == nil || echoed.Destination != "org.example.Echo" {
		t.Fatalf("Echo request destination = %#v", echoed)
	}

	_, err = client.call(ctx, "org.example.Boom", "/org/example", "org.example.Test", "Boom", "")
	var dbusErr *dbusErrorReply
	if !errors.As(err, &dbusErr) {
		t.Fatalf("Boom call err = %v, want *dbusErrorReply", err)
	}
	if dbusErr.Name != "org.freedesktop.portal.Error.NotAllowed" || dbusErr.Message != "not allowed" {
		t.Fatalf("Boom error = %#v", dbusErr)
	}

	if err := client.notify(ctx, "org.example.Echo", "/org/example", "org.example.Test", "FireAndForget", ""); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if err := client.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !client.broken() {
		t.Fatal("client still reports a healthy connection after close")
	}
	if _, err := client.call(ctx, "org.example.Echo", "/org/example", "org.example.Test", "Echo", "s", "x"); err == nil {
		t.Fatal("call after close succeeded")
	}
}

// TestDBusClientAuthRejected: a server that rejects SASL must surface an error
// instead of hanging in the handshake.
func TestDBusClientAuthRejected(t *testing.T) {
	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "bus"))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		_, _ = br.ReadByte() // NUL
		_, _ = readSASLLine(br)
		_, _ = conn.Write([]byte("REJECTED EXTERNAL\r\n"))
	}()
	prev := dbusDialTimeout
	dbusDialTimeout = 2 * time.Second
	t.Cleanup(func() { dbusDialTimeout = prev })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := connectDBus(ctx, "unix:path="+ln.Addr().String())
	if err == nil {
		_ = client.close()
		t.Fatal("connectDBus accepted a rejected SASL handshake")
	}
	if !strings.Contains(err.Error(), "rejected") && !strings.Contains(err.Error(), "REJECTED") {
		t.Fatalf("auth error = %v", err)
	}
}

// TestDBusClientCallTimesOut: a server that reads but never answers must not
// wedge the caller past the context deadline.
func TestDBusClientCallTimesOut(t *testing.T) {
	srv := newFakeBusServer(t, func(c *fakeBusConn, msg *dbusMessage) {
		_ = helloHandler(c, msg) // never reply to anything else
	})
	client, err := connectDBus(context.Background(), srv.addr)
	if err != nil {
		t.Fatalf("connectDBus: %v", err)
	}
	defer client.close()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = client.call(ctx, "org.example.Slow", "/org/example", "org.example.Test", "Slow", "")
	if err == nil {
		t.Fatal("call with no reply succeeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("call err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("call blocked for %v past its deadline", elapsed)
	}
}

// TestParseBusAddress covers the unix: address forms a session bus may use.
func TestParseBusAddress(t *testing.T) {
	cases := []struct {
		address string
		want    []string
	}{
		{"unix:path=/run/user/1000/bus", []string{"/run/user/1000/bus"}},
		{"unix:abstract=/tmp/dbus-abc", []string{"@/tmp/dbus-abc"}},
		{"unix:path=/a;unix:path=/b", []string{"/a", "/b"}},
		{"unix:path=/run/user/1000/bus,guid=deadbeef", []string{"/run/user/1000/bus"}},
	}
	for _, tc := range cases {
		got, err := parseBusAddress(tc.address)
		if err != nil {
			t.Fatalf("parseBusAddress(%q): %v", tc.address, err)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("parseBusAddress(%q) = %#v, want %#v", tc.address, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("parseBusAddress(%q) = %#v, want %#v", tc.address, got, tc.want)
			}
		}
	}
	if _, err := parseBusAddress("tcp:host=localhost"); err == nil {
		t.Fatal("parseBusAddress accepted a non-unix transport")
	}
}

// TestDBusClientAgainstRealBus is the only interop check that does not share the
// codec with the peer: it spawns a private real dbus-daemon, authenticates,
// calls it and decodes its reply. Skipped when dbus-daemon is unavailable.
func TestDBusClientAgainstRealBus(t *testing.T) {
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon not installed; skipping real-bus interop test")
	}
	dir := t.TempDir()
	addr := "unix:path=" + filepath.Join(dir, "bus")
	cmd := exec.Command(daemon, "--session", "--nofork", "--print-address", "--address="+addr)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("dbus-daemon stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start dbus-daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	// Wait for the private bus socket to appear.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, statErr := net.Dial("unix", filepath.Join(dir, "bus")); statErr == nil {
			break
		}
		if time.Now().After(deadline) {
			_, _ = io.Copy(io.Discard, stdout)
			t.Fatalf("private dbus-daemon did not create %s", addr)
		}
		time.Sleep(20 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := connectDBus(ctx, addr)
	if err != nil {
		t.Fatalf("connectDBus(real bus): %v", err)
	}
	defer client.close()
	if !strings.HasPrefix(client.uniqueName(), ":") {
		t.Fatalf("real bus assigned unique name %q", client.uniqueName())
	}
	// GetNameOwner exercises string arguments; the reply is a real object path.
	reply, err := client.call(ctx, dbusBusName, dbusObjectPath, dbusBusName, "GetNameOwner", "s", "org.freedesktop.DBus")
	if err != nil {
		t.Fatalf("GetNameOwner: %v", err)
	}
	values, err := reply.bodyValues()
	if err != nil {
		t.Fatalf("GetNameOwner body: %v", err)
	}
	if len(values) != 1 || values[0] != "org.freedesktop.DBus" {
		t.Fatalf("GetNameOwner values = %#v", values)
	}
	// ListNames decodes a real `as` array emitted by dbus-daemon itself.
	listReply, err := client.call(ctx, dbusBusName, dbusObjectPath, dbusBusName, "ListNames", "")
	if err != nil {
		t.Fatalf("ListNames: %v", err)
	}
	names, err := listReply.bodyValues()
	if err != nil {
		t.Fatalf("ListNames body: %v", err)
	}
	if len(names) != 1 {
		t.Fatalf("ListNames values = %#v", names)
	}
	arr, ok := names[0].([]any)
	if !ok {
		t.Fatalf("ListNames array = %#v", names[0])
	}
	var sawBus bool
	for _, v := range arr {
		if v == "org.freedesktop.DBus" {
			sawBus = true
		}
	}
	if !sawBus {
		t.Fatalf("ListNames = %#v, missing org.freedesktop.DBus", arr)
	}
	// GetConnectionCredentials returns a real a{sv} built by dbus-daemon,
	// validating dictionary/variant decoding against an independent peer.
	credReply, err := client.call(ctx, dbusBusName, dbusObjectPath, dbusBusName,
		"GetConnectionCredentials", "s", client.uniqueName())
	if err != nil {
		t.Fatalf("GetConnectionCredentials: %v", err)
	}
	credValues, err := credReply.bodyValues()
	if err != nil {
		t.Fatalf("GetConnectionCredentials body: %v", err)
	}
	if len(credValues) != 1 {
		t.Fatalf("GetConnectionCredentials values = %#v", credValues)
	}
	credentials, ok := credValues[0].(map[string]any)
	if !ok {
		t.Fatalf("GetConnectionCredentials dict = %#v", credValues[0])
	}
	userID, ok := credentials["UnixUserID"].(dbusVariant)
	if !ok || userID.Signature != "u" || userID.Value != uint32(os.Getuid()) {
		t.Fatalf("UnixUserID credential = %#v, want uint32(%d)", credentials["UnixUserID"], os.Getuid())
	}

	// UpdateActivationEnvironment exercises a{ss} array *encoding* against the
	// real daemon (a malformed array would make it disconnect instead of just
	// returning an error).
	_, err = client.call(ctx, dbusBusName, dbusObjectPath, dbusBusName,
		"UpdateActivationEnvironment", "a{ss}", map[string]any{"ZEOKEEL_TEST": "1"})
	var updateErr *dbusErrorReply
	if err != nil && !errors.As(err, &updateErr) {
		t.Fatalf("UpdateActivationEnvironment: protocol-level failure: %v", err)
	}
	if updateErr != nil && (strings.Contains(updateErr.Name, "InvalidArgs") || strings.Contains(updateErr.Name, "Protocol")) {
		t.Fatalf("UpdateActivationEnvironment rejected the a{ss} body: %v", updateErr)
	}
	// The connection must still be healthy after the array-encoding calls.
	if _, err := client.call(ctx, dbusBusName, dbusObjectPath, dbusBusName, "GetId", ""); err != nil {
		t.Fatalf("bus connection unhealthy after array encoding: %v", err)
	}

	// A missing service is a real D-Bus error reply (name has no owner).
	_, err = client.call(ctx, dbusBusName, dbusObjectPath, dbusBusName, "GetNameOwner", "s", "org.example.NoSuchService")
	var dbusErr *dbusErrorReply
	if !errors.As(err, &dbusErr) {
		t.Fatalf("GetNameOwner(missing) err = %v", err)
	}
	if dbusErr.Name != "org.freedesktop.DBus.Error.NameHasNoOwner" {
		t.Fatalf("GetNameOwner(missing) error name = %q", dbusErr.Name)
	}
}

// TestEOFOnEmptyReader makes the reader's error contract explicit.
func TestEOFOnEmptyReader(t *testing.T) {
	if _, err := readDBusMessage(bytes.NewReader(nil)); err == nil {
		t.Fatal("readDBusMessage accepted EOF")
	}
}
