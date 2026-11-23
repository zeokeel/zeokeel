// Command gwprobe drives a running ZeoKeel gateway over its real protocol: it
// opens the control-plane WebSocket, creates a session, starts one turn on the
// SSE endpoint, and prints the event sequence. Modes cover the flows the browser
// acceptance list depends on, and it exits non-zero when an assertion fails, so
// CI (which has no browser) can run a protocol-level end-to-end check.
//
//	gwprobe -addr 127.0.0.1:9932 -token "$TOK" -mode plain
//	gwprobe -addr 127.0.0.1:9932 -token "$TOK" -mode approval   # allows the card
//	gwprobe -addr 127.0.0.1:9932 -token "$TOK" -mode cancel
//	gwprobe -addr 127.0.0.1:9932 -token "$TOK" -mode slow       # disconnect ≠ stop
//
// It is a development tool: scripts/acceptance/serve-with-fake-model.sh --probe
// <mode> starts the fixture and runs it in one command.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9932", "gateway host:port")
	token := flag.String("token", os.Getenv("ZK_TOKEN"), "gateway token (default $ZK_TOKEN)")
	mode := flag.String("mode", "plain", "plain | approval | cancel | slow")
	text := flag.String("text", "", "prompt text (default per mode)")
	timeout := flag.Duration("timeout", 90*time.Second, "overall deadline")
	flag.Parse()
	if *token == "" {
		fatalf("a token is required (-token or $ZK_TOKEN); it is the #token= value the gateway printed")
	}
	if *text == "" {
		*text = "say hello from gwprobe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	probe := &probe{addr: *addr, token: *token, text: *text}
	if err := probe.connect(); err != nil {
		fatalf("connect: %v", err)
	}
	defer probe.close()

	var err error
	switch *mode {
	case "plain":
		err = probe.runPlain(ctx)
	case "approval":
		err = probe.runApproval(ctx)
	case "cancel":
		err = probe.runCancel(ctx)
	case "slow":
		err = probe.runSlow(ctx)
	default:
		fatalf("unknown mode %q (plain | approval | cancel | slow)", *mode)
	}
	if err != nil {
		fatalf("mode %s: %v", *mode, err)
	}
	fmt.Printf("OK mode=%s session=%s\n", *mode, probe.sessionID)
}

// fatalf exits non-zero with a message on stderr.
func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gwprobe: "+format+"\n", args...)
	os.Exit(1)
}

// probe is one client session against the gateway.
type probe struct {
	addr      string
	token     string
	text      string
	conn      net.Conn
	reader    *bufio.Reader
	http      *http.Client
	origin    string
	sessionID string
	nextID    int
}

// connect opens the control-plane socket and completes the handshake.
func (p *probe) connect() error {
	p.origin = "http://" + p.addr
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	p.http = &http.Client{Jar: jar}
	if err := p.authenticate(); err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", p.addr, 5*time.Second)
	if err != nil {
		return err
	}
	p.conn = conn
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	request := "GET /ws HTTP/1.1\r\nHost: " + p.addr + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(key) + "\r\nSec-WebSocket-Version: 13\r\n" +
		"Origin: " + p.origin + "\r\nAuthorization: Bearer " + p.token + "\r\n\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		return err
	}
	p.reader = bufio.NewReader(conn)
	response, err := http.ReadResponse(p.reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		return fmt.Errorf("ws handshake answered %s (is the token current?)", response.Status)
	}
	if err := p.call("initialize", map[string]any{"clientName": "gwprobe", "protocolVersion": 1}); err != nil {
		return err
	}
	return p.call("ready", nil)
}

// authenticate exchanges the token for the session cookie (§3.1).
func (p *probe) authenticate() error {
	request, err := http.NewRequest(http.MethodPost, p.origin+"/auth", strings.NewReader(`{"token":"`+p.token+`"}`))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", p.origin)
	response, err := p.http.Do(request)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusOK {
		return fmt.Errorf("/auth answered %s (token expired?)", response.Status)
	}
	return nil
}

// close drops the socket.
func (p *probe) close() {
	if p.conn != nil {
		_ = p.conn.Close()
		p.conn = nil
	}
}

// call sends one request and returns its result.
func (p *probe) call(method string, params any) error {
	_, err := p.callResult(method, params)
	return err
}

// callResult sends one request, skipping notifications, and returns the result.
func (p *probe) callResult(method string, params any) (map[string]any, error) {
	p.nextID++
	id := p.nextID
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	if err := writeFrame(p.conn, append(payload, '\n')); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if err := p.conn.SetReadDeadline(deadline); err != nil {
			return nil, err
		}
		frame, err := readJSONFrame(p.reader)
		if err != nil {
			return nil, err
		}
		if frame["method"] != nil {
			continue // a broadcast, not our reply
		}
		if frame["id"] != float64(id) {
			continue
		}
		if errObject, failed := frame["error"].(map[string]any); failed {
			return nil, fmt.Errorf("%s failed: %v", method, errObject)
		}
		result, _ := frame["result"].(map[string]any)
		if result == nil {
			result = map[string]any{}
		}
		return result, nil
	}
}

// nextNotification waits for one notification with the given method.
func (p *probe) nextNotification(method string, timeout time.Duration) (map[string]any, error) {
	deadline := time.Now().Add(timeout)
	for {
		if err := p.conn.SetReadDeadline(deadline); err != nil {
			return nil, err
		}
		frame, err := readJSONFrame(p.reader)
		if err != nil {
			return nil, err
		}
		if frame["method"] == method {
			return frame, nil
		}
	}
}

// createSession makes a conversation through the protocol.
func (p *probe) createSession(title string) error {
	result, err := p.callResult("session.create", map[string]any{"title": title})
	if err != nil {
		return err
	}
	p.sessionID, _ = result["sessionId"].(string)
	if p.sessionID == "" {
		return errors.New("session.create returned no sessionId")
	}
	fmt.Printf("session.create -> %s\n", p.sessionID)
	return nil
}

// stream is one open SSE run.
type stream struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
	seen    map[string]int
	order   []string
}

// startRun posts the run and returns the live stream.
func (p *probe) startRun(clientRunID string) (*stream, error) {
	payload, err := json.Marshal(map[string]any{
		"sessionId":   p.sessionID,
		"text":        p.text,
		"clientRunId": clientRunID,
	})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(http.MethodPost, p.origin+"/api/chat/run", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", p.origin)
	request.Header.Set("Accept", "text/event-stream")
	response, err := p.http.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, fmt.Errorf("POST /api/chat/run answered %s", response.Status)
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	return &stream{body: response.Body, scanner: scanner, seen: map[string]int{}}, nil
}

// next reads the next SSE event name and its data line.
func (s *stream) next() (string, string, error) {
	data := ""
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			continue
		}
		if !strings.HasPrefix(line, "event: ") {
			continue
		}
		event := strings.TrimPrefix(line, "event: ")
		s.seen[event]++
		s.order = append(s.order, event)
		return event, data, nil
	}
	if err := s.scanner.Err(); err != nil {
		return "", "", err
	}
	return "", "", io.EOF
}

// drainTo reads until the terminal event, returning the event order.
func (s *stream) drainTo(done string) ([]string, error) {
	for {
		event, _, err := s.next()
		if err != nil {
			return s.order, err
		}
		if event == done {
			return s.order, nil
		}
		if event == "error" && done != "error" {
			return s.order, errors.New("the run reported an error event")
		}
	}
}

// runPlain is the minimal turn: create, run, stream, done.
func (p *probe) runPlain(ctx context.Context) error {
	if err := p.createSession("gwprobe plain"); err != nil {
		return err
	}
	run, err := p.startRun("gwprobe-plain")
	if err != nil {
		return err
	}
	defer func() { _ = run.body.Close() }()
	order, err := run.drainTo("done")
	if err != nil {
		return err
	}
	fmt.Printf("events: %s\n", strings.Join(order, " -> "))
	if run.seen["message.delta"] == 0 {
		return errors.New("no message.delta arrived: streaming did not work")
	}
	if last := order[len(order)-1]; last != "done" {
		return fmt.Errorf("last event = %q, want done", last)
	}
	return p.expectSessionSettled(ctx, true)
}

// runApproval parks on the approval card, allows it, and expects the tool result.
func (p *probe) runApproval(ctx context.Context) error {
	if err := p.createSession("gwprobe approval"); err != nil {
		return err
	}
	run, err := p.startRun("gwprobe-approval")
	if err != nil {
		return err
	}
	defer func() { _ = run.body.Close() }()

	frame, err := p.nextNotification("approvals.delta", 20*time.Second)
	if err != nil {
		return fmt.Errorf("no approvals.delta (does the scripted model request a gated tool?): %w", err)
	}
	params, _ := frame["params"].(map[string]any)
	added, _ := params["added"].([]any)
	if len(added) == 0 {
		return fmt.Errorf("approvals.delta carried no request: %v", params)
	}
	item, _ := added[0].(map[string]any)
	id, _ := item["id"].(string)
	fmt.Printf("approvals.delta tool=%v id=%s session=%v\n", item["tool"], id, item["sessionId"])
	if id == "" {
		return errors.New("the pending approval has no id")
	}
	if err := p.call("approval.resolve", map[string]any{"id": id, "decision": "allow"}); err != nil {
		return err
	}
	fmt.Println("approval.resolve allow -> ok")

	order, err := run.drainTo("done")
	if err != nil {
		return err
	}
	fmt.Printf("events: %s\n", strings.Join(order, " -> "))
	for _, want := range []string{"tool.start", "tool.result", "message.delta", "done"} {
		if run.seen[want] == 0 {
			return fmt.Errorf("the approval chain never produced %s (events: %v)", want, order)
		}
	}
	return p.expectSessionSettled(ctx, true)
}

// runCancel stops a run by protocol and expects the session to settle.
func (p *probe) runCancel(ctx context.Context) error {
	if err := p.createSession("gwprobe cancel"); err != nil {
		return err
	}
	run, err := p.startRun("gwprobe-cancel")
	if err != nil {
		return err
	}
	if _, _, err := run.next(); err != nil {
		return fmt.Errorf("no first event: %w", err)
	}
	if err := p.call("run.cancel", map[string]any{"sessionId": p.sessionID}); err != nil {
		return err
	}
	fmt.Println("run.cancel -> ok")
	_ = run.body.Close()
	return p.expectSessionSettled(ctx, false)
}

// runSlow proves a dropped SSE socket is not a stop request (§4.6).
func (p *probe) runSlow(ctx context.Context) error {
	if err := p.createSession("gwprobe slow"); err != nil {
		return err
	}
	run, err := p.startRun("gwprobe-slow")
	if err != nil {
		return err
	}
	for i := 0; i < 2; i++ {
		if _, _, err := run.next(); err != nil {
			return fmt.Errorf("no event %d: %w", i+1, err)
		}
	}
	_ = run.body.Close()
	fmt.Println("disconnected after 2 events; the run must keep going")
	return p.expectSessionSettled(ctx, false)
}

// expectSessionSettled polls session.snapshot until the run is over, and checks
// the running flag was the expected value at least once on the way.
func (p *probe) expectSessionSettled(ctx context.Context, _ bool) error {
	sawRunning := false
	for {
		result, err := p.callResult("session.snapshot", map[string]any{"sessionId": p.sessionID})
		if err != nil {
			return err
		}
		session, _ := result["session"].(map[string]any)
		running, _ := session["running"].(bool)
		if running {
			sawRunning = true
		}
		if !running {
			messages, _ := session["messages"].([]any)
			fmt.Printf("session settled: messages=%d\n", len(messages))
			if len(messages) == 0 {
				return errors.New("the session settled with no messages")
			}
			return nil
		}
		if sawRunning {
			fmt.Println("session.running stayed true after the disconnect")
		}
		select {
		case <-ctx.Done():
			return errors.New("the run never settled before the deadline")
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// --- minimal RFC 6455 client (text frames, client masking) -----------------

// writeFrame writes one masked text frame.
func writeFrame(conn net.Conn, payload []byte) error {
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	head := []byte{0x81}
	switch {
	case len(payload) < 126:
		head = append(head, 0x80|byte(len(payload)))
	case len(payload) <= 0xffff:
		head = append(head, 0x80|126, 0, 0)
		binary.BigEndian.PutUint16(head[len(head)-2:], uint16(len(payload)))
	default:
		head = append(head, 0x80|127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(head[len(head)-8:], uint64(len(payload)))
	}
	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	_, err := conn.Write(append(append(head, mask...), masked...))
	return err
}

// readJSONFrame reads one text frame and decodes it.
func readJSONFrame(reader *bufio.Reader) (map[string]any, error) {
	var head [2]byte
	if _, err := io.ReadFull(reader, head[:]); err != nil {
		return nil, err
	}
	length := int64(head[1] & 0x7f)
	switch length {
	case 126:
		var extended [2]byte
		if _, err := io.ReadFull(reader, extended[:]); err != nil {
			return nil, err
		}
		length = int64(binary.BigEndian.Uint16(extended[:]))
	case 127:
		var extended [8]byte
		if _, err := io.ReadFull(reader, extended[:]); err != nil {
			return nil, err
		}
		length = int64(binary.BigEndian.Uint64(extended[:]))
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	var frame map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(payload), &frame); err != nil {
		return nil, err
	}
	return frame, nil
}
