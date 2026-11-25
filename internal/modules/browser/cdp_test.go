package browser

// Protocol-layer tests: the CDP client is exercised against a fake Transport,
// so no browser (and no display) is needed. They pin the exact documented
// method and params of every command, the session the command rides on, and the
// "bad URL never reaches the wire" rule.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// sendCall is one recorded command. The session is read back from the context
// the client attached (WithSession), which is how a multiplexing transport
// would route the command to a page.
type sendCall struct {
	method     string
	params     any
	session    string
	hasSession bool
}

type fakeTransport struct {
	mu    sync.Mutex
	calls []sendCall
	reply func(ctx context.Context, method string, params any) (json.RawMessage, error)
}

func (f *fakeTransport) Send(ctx context.Context, method string, params any) (json.RawMessage, error) {
	session, hasSession := SessionFrom(ctx)
	f.mu.Lock()
	f.calls = append(f.calls, sendCall{method: method, params: params, session: session, hasSession: hasSession})
	f.mu.Unlock()
	if f.reply != nil {
		return f.reply(ctx, method, params)
	}
	return json.RawMessage(`{}`), nil
}

func (f *fakeTransport) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeTransport) last(t *testing.T) sendCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("no sends recorded")
	}
	return f.calls[len(f.calls)-1]
}

// scriptedTransport answers by method: the CDP-shaped replies the protocol
// client must parse.
func scriptedTransport(t *testing.T, replies map[string]json.RawMessage) *fakeTransport {
	t.Helper()
	return &fakeTransport{reply: func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		raw, ok := replies[method]
		if !ok {
			t.Errorf("unexpected method %q", method)
			return nil, errors.New("unexpected method")
		}
		return raw, nil
	}}
}

func TestOpenSendsCreateTarget(t *testing.T) {
	now := time.Date(2026, 11, 25, 9, 30, 0, 0, time.UTC)
	tr := scriptedTransport(t, map[string]json.RawMessage{
		"Target.createTarget": json.RawMessage(`{"targetId":"T-1"}`),
	})
	cdp := NewCDP(tr, func() time.Time { return now })

	page, err := cdp.Open(context.Background(), "https://example.com/a?b=1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if page.ID != "T-1" {
		t.Errorf("page.ID = %q, want T-1", page.ID)
	}
	if page.URL != "https://example.com/a?b=1" {
		t.Errorf("page.URL = %q, want the requested url", page.URL)
	}
	if page.Title != "" {
		t.Errorf("page.Title = %q, want empty until Title is asked", page.Title)
	}
	if !page.OpenedAt.Equal(now) {
		t.Errorf("page.OpenedAt = %v, want %v", page.OpenedAt, now)
	}
	if got := tr.count(); got != 1 {
		t.Fatalf("sends = %d, want 1", got)
	}
	call := tr.last(t)
	if call.method != "Target.createTarget" {
		t.Errorf("method = %q, want Target.createTarget", call.method)
	}
	want := createTargetParams{URL: "https://example.com/a?b=1", NewWindow: false}
	if call.params != want {
		t.Errorf("params = %+v, want %+v (newWindow must stay false: pages open as tabs)", call.params, want)
	}
	if call.hasSession {
		t.Errorf("createTarget carries session %q; it is a browser-level command", call.session)
	}
}

func TestTitleReadsDocumentTitleFromEvaluate(t *testing.T) {
	tr := scriptedTransport(t, map[string]json.RawMessage{
		"Target.createTarget": json.RawMessage(`{"targetId":"T-7"}`),
		"Runtime.evaluate":    json.RawMessage(`{"result":{"type":"string","value":"Example Domain"}}`),
	})
	cdp := NewCDP(tr, nil)
	page, err := cdp.Open(context.Background(), "https://example.com/")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	title, err := cdp.Title(context.Background(), page.ID)
	if err != nil {
		t.Fatalf("Title: %v", err)
	}
	if title != "Example Domain" {
		t.Errorf("Title = %q, want Example Domain", title)
	}
	call := tr.last(t)
	if call.method != "Runtime.evaluate" {
		t.Errorf("method = %q, want Runtime.evaluate", call.method)
	}
	want := evaluateParams{Expression: "document.title", ReturnByValue: true}
	if call.params != want {
		t.Errorf("params = %+v, want %+v", call.params, want)
	}
	if !call.hasSession || call.session != "T-7" {
		t.Errorf("session = (%q, %v), want the page session T-7", call.session, call.hasSession)
	}
}

func TestTitleReportsProtocolViolations(t *testing.T) {
	cases := map[string]string{
		"non-string value": `{"result":{"type":"number","value":42}}`,
		"missing value":    `{"result":{"type":"string"}}`,
		"exception":        `{"result":{"type":"undefined"},"exceptionDetails":{"text":"boom"}}`,
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			tr := &fakeTransport{reply: func(_ context.Context, method string, _ any) (json.RawMessage, error) {
				if method == "Target.createTarget" {
					return json.RawMessage(`{"targetId":"T-1"}`), nil
				}
				return json.RawMessage(reply), nil
			}}
			cdp := NewCDP(tr, nil)
			if _, err := cdp.Open(context.Background(), "https://example.com/"); err != nil {
				t.Fatalf("Open: %v", err)
			}
			if _, err := cdp.Title(context.Background(), "T-1"); !errors.Is(err, ErrProtocol) {
				t.Fatalf("Title = %v, want ErrProtocol", err)
			}
		})
	}
}

func TestScreenshotDecodesBase64PNG(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nfake-png-payload")
	encoded := base64.StdEncoding.EncodeToString(png)
	tr := scriptedTransport(t, map[string]json.RawMessage{
		"Target.createTarget":    json.RawMessage(`{"targetId":"T-2"}`),
		"Page.captureScreenshot": json.RawMessage(`{"data":"` + encoded + `"}`),
	})
	cdp := NewCDP(tr, nil)
	if _, err := cdp.Open(context.Background(), "https://example.com/"); err != nil {
		t.Fatalf("Open: %v", err)
	}

	shot, err := cdp.Screenshot(context.Background(), "T-2", true)
	if err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if !bytes.Equal(shot, png) {
		t.Errorf("Screenshot bytes = %q, want %q", shot, png)
	}
	call := tr.last(t)
	if call.method != "Page.captureScreenshot" {
		t.Errorf("method = %q, want Page.captureScreenshot", call.method)
	}
	want := captureScreenshotParams{Format: "png", CaptureBeyondViewport: true}
	if call.params != want {
		t.Errorf("params = %+v, want %+v (full page is a browser-side capture)", call.params, want)
	}
	if !call.hasSession || call.session != "T-2" {
		t.Errorf("session = (%q, %v), want the page session T-2", call.session, call.hasSession)
	}

	// The viewport-only capture must not ask for captureBeyondViewport.
	if _, err := cdp.Screenshot(context.Background(), "T-2", false); err != nil {
		t.Fatalf("Screenshot(viewport): %v", err)
	}
	if got := tr.last(t).params; got != (captureScreenshotParams{Format: "png", CaptureBeyondViewport: false}) {
		t.Errorf("viewport params = %+v, want captureBeyondViewport false", got)
	}
}

func TestScreenshotAcceptsDataURLAndRejectsGarbage(t *testing.T) {
	png := []byte("png-bytes")
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	tr := &fakeTransport{reply: func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		if method == "Page.captureScreenshot" {
			return json.RawMessage(`{"data":"` + dataURL + `"}`), nil
		}
		return json.RawMessage(`{}`), nil
	}}
	cdp := NewCDP(tr, nil)
	shot, err := cdp.Screenshot(context.Background(), "T-1", false)
	if err != nil {
		t.Fatalf("Screenshot(data url): %v", err)
	}
	if !bytes.Equal(shot, png) {
		t.Errorf("Screenshot(bytes) = %q, want %q", shot, png)
	}

	garbage := &fakeTransport{reply: func(_ context.Context, _ string, _ any) (json.RawMessage, error) {
		return json.RawMessage(`{"data":"not base64!!"}`), nil
	}}
	if _, err := NewCDP(garbage, nil).Screenshot(context.Background(), "T-1", false); !errors.Is(err, ErrProtocol) {
		t.Fatalf("Screenshot(garbage) = %v, want ErrProtocol", err)
	}
}

func TestBadURLIsRejectedBeforeAnySend(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"no scheme":        "example.com",
		"ftp":              "ftp://example.com/file",
		"file":             "file:///etc/passwd",
		"javascript":       "javascript:alert(1)",
		"data":             "data:text/html,<h1>x</h1>",
		"chrome internals": "chrome://settings",
		"no host":          "https://",
		"control char":     "https://example.com/\x00",
		"too long":         "https://example.com/" + string(bytes.Repeat([]byte("a"), MaxURLBytes)),
	}
	for name, rawURL := range cases {
		t.Run(name, func(t *testing.T) {
			tr := &fakeTransport{}
			cdp := NewCDP(tr, nil)
			page, err := cdp.Open(context.Background(), rawURL)
			if !errors.Is(err, contracts.ErrBrowserBadURL) {
				t.Fatalf("Open(%q) = (%+v, %v), want ErrBrowserBadURL", rawURL, page, err)
			}
			if got := tr.count(); got != 0 {
				t.Fatalf("sends = %d, want 0: an invalid URL must never reach the browser", got)
			}
			if page.ID != "" || page.URL != "" || !page.OpenedAt.IsZero() {
				t.Errorf("page = %+v, want the zero PageInfo", page)
			}
		})
	}
}

func TestValidateURLAcceptsHTTPAndHTTPS(t *testing.T) {
	for _, rawURL := range []string{
		"http://example.com",
		"https://example.com/path?q=1#frag",
		"http://127.0.0.1:8080/",
		"https://[::1]:8443/x",
	} {
		if err := ValidateURL(rawURL); err != nil {
			t.Errorf("ValidateURL(%q) = %v, want nil", rawURL, err)
		}
	}
}

func TestOpenWithoutTransportIsUnavailable(t *testing.T) {
	page, err := NewCDP(nil, nil).Open(context.Background(), "https://example.com/")
	if !errors.Is(err, contracts.ErrBrowserUnavailable) {
		t.Fatalf("Open(no transport) = (%+v, %v), want ErrBrowserUnavailable", page, err)
	}
	if !errors.Is(err, contracts.ErrBrowserUnavailable) || page.ID != "" {
		t.Errorf("page = %+v, want zero", page)
	}
}

func TestOpenRejectsEmptyTargetID(t *testing.T) {
	tr := &fakeTransport{reply: func(context.Context, string, any) (json.RawMessage, error) {
		return json.RawMessage(`{"targetId":""}`), nil
	}}
	_, err := NewCDP(tr, nil).Open(context.Background(), "https://example.com/")
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("Open(empty targetId) = %v, want ErrProtocol", err)
	}
}

func TestCloseSendsCloseTarget(t *testing.T) {
	tr := &fakeTransport{reply: func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		if method == "Target.closeTarget" {
			return json.RawMessage(`{"success":true}`), nil
		}
		return json.RawMessage(`{}`), nil
	}}
	if err := NewCDP(tr, nil).Close(context.Background(), "T-9"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	call := tr.last(t)
	if call.method != "Target.closeTarget" {
		t.Errorf("method = %q, want Target.closeTarget", call.method)
	}
	if got := call.params; got != (closeTargetParams{TargetID: "T-9"}) {
		t.Errorf("params = %+v, want targetId T-9", got)
	}
}

func TestCloseRefusedIsNotFound(t *testing.T) {
	tr := &fakeTransport{reply: func(context.Context, string, any) (json.RawMessage, error) {
		return json.RawMessage(`{"success":false}`), nil
	}}
	if err := NewCDP(tr, nil).Close(context.Background(), "T-9"); !errors.Is(err, contracts.ErrBrowserNotFound) {
		t.Fatalf("Close(refused) = %v, want ErrBrowserNotFound", err)
	}
}
