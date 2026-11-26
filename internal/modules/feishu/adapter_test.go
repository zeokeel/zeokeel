package feishu_test

// TDD for the Feishu (Lark) outbound adapter. Everything here is the outbound
// half of the platform contract: the message body shape, the tenant-token mint
// and cache, and the error mapping a caller's fallback logic depends on. The
// ingress (signature scheme, url_verification handshake, event parsing, card
// actions) is gone by decision — see docs/research/channel-capabilities.md §6 —
// and internal/contracts/channel_surface_test.go pins that it does not come back.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/feishu"
)

const testToken = "t-tenant-token"

type sentMessage struct {
	path   string
	query  string
	auth   string
	ctype  string
	fields map[string]any
}

// fakePlatform is the open-api side: message send plus tenant token mint.
type fakePlatform struct {
	mu            sync.Mutex
	sends         []sentMessage
	sendStatus    int
	sendCode      int
	sendMessage   string
	tokenStatus   int
	tokenCode     int
	tokenMints    int
	tokenValue    string
	tokenLifetime int
}

func (f *fakePlatform) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		var fields map[string]any
		_ = json.NewDecoder(r.Body).Decode(&fields)
		f.mu.Lock()
		f.sends = append(f.sends, sentMessage{
			path: r.URL.Path, query: r.URL.RawQuery,
			auth: r.Header.Get("Authorization"), ctype: r.Header.Get("Content-Type"),
			fields: fields,
		})
		status, code, message := f.sendStatus, f.sendCode, f.sendMessage
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
		}
		if code != 0 {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "msg": message})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"message_id": "om_out"}})
	})
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.tokenMints++
		status, code, token, lifetime := f.tokenStatus, f.tokenCode, f.tokenValue, f.tokenLifetime
		if token == "" {
			token = "t-minted"
		}
		if lifetime == 0 {
			lifetime = 7200
		}
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		if code != 0 {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "msg": "bad credentials"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok", "tenant_access_token": token, "expire": lifetime})
	})
	return mux
}

func (f *fakePlatform) lastSend() (sentMessage, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sends) == 0 {
		return sentMessage{}, false
	}
	return f.sends[len(f.sends)-1], true
}

type fixture struct {
	adapter  *feishu.Adapter
	platform *fakePlatform
	baseURL  string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWith(t, nil)
}

func newFixtureWith(t *testing.T, mutate func(*feishu.Config)) *fixture {
	t.Helper()
	platform := &fakePlatform{}
	server := httptest.NewServer(platform.handler())
	t.Cleanup(server.Close)

	cfg := feishu.Config{
		BaseURL: server.URL,
		Token:   testToken,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	adapter := feishu.NewAdapter(cfg)
	return &fixture{adapter: adapter, platform: platform, baseURL: server.URL}
}

func TestSendPostsTheMessage(t *testing.T) {
	f := newFixture(t)
	if err := f.adapter.Send(context.Background(), "oc_chat", "收到，我在查"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	sent, ok := f.platform.lastSend()
	if !ok {
		t.Fatal("no request reached the platform")
	}
	if sent.path != "/open-apis/im/v1/messages" {
		t.Fatalf("path = %q", sent.path)
	}
	if sent.query != "receive_id_type=chat_id" {
		t.Fatalf("query = %q, want receive_id_type=chat_id", sent.query)
	}
	if sent.auth != "Bearer "+testToken {
		t.Fatalf("authorization = %q", sent.auth)
	}
	if !strings.HasPrefix(sent.ctype, "application/json") {
		t.Fatalf("content type = %q", sent.ctype)
	}
	if sent.fields["receive_id"] != "oc_chat" || sent.fields["msg_type"] != "text" {
		t.Fatalf("payload = %+v", sent.fields)
	}
	content, ok := sent.fields["content"].(string)
	if !ok {
		t.Fatalf("content = %v, want a JSON string", sent.fields["content"])
	}
	var decoded struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &decoded); err != nil || decoded.Text != "收到，我在查" {
		t.Fatalf("content = %q (%v), want {\"text\":\"收到，我在查\"}", content, err)
	}
}

func TestSendRejectsEmptyText(t *testing.T) {
	f := newFixture(t)
	if err := f.adapter.Send(context.Background(), "oc_chat", "   "); !errors.Is(err, contracts.ErrFeishuEmptyMessage) {
		t.Fatalf("err = %v, want ErrFeishuEmptyMessage", err)
	}
	if _, ok := f.platform.lastSend(); ok {
		t.Fatal("an empty message was posted to the platform")
	}
}

func TestSendRejectsAnEmptyReceiveID(t *testing.T) {
	f := newFixture(t)
	if err := f.adapter.Send(context.Background(), "  ", "hi"); !errors.Is(err, contracts.ErrFeishuBadPayload) {
		t.Fatalf("err = %v, want ErrFeishuBadPayload for a missing chat", err)
	}
	if _, ok := f.platform.lastSend(); ok {
		t.Fatal("a send without a chat id was posted to the platform")
	}
}

func TestSendRequiresCredentials(t *testing.T) {
	f := newFixtureWith(t, func(cfg *feishu.Config) { cfg.Token = "" })
	if err := f.adapter.Send(context.Background(), "oc_chat", "hi"); !errors.Is(err, contracts.ErrFeishuUnauthorized) {
		t.Fatalf("err = %v, want ErrFeishuUnauthorized", err)
	}
	if _, ok := f.platform.lastSend(); ok {
		t.Fatal("posted without credentials")
	}
	status, err := f.adapter.Status(context.Background())
	if err != nil || status.Connected {
		t.Fatalf("status = (%+v, %v), want disconnected", status, err)
	}
}

func TestSendMapsUnauthorized(t *testing.T) {
	f := newFixture(t)
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		f.platform.mu.Lock()
		f.platform.sendStatus = status
		f.platform.mu.Unlock()
		if err := f.adapter.Send(context.Background(), "oc_chat", "hi"); !errors.Is(err, contracts.ErrFeishuUnauthorized) {
			t.Fatalf("status %d err = %v, want ErrFeishuUnauthorized", status, err)
		}
	}
}

func TestSendMapsPlatformErrorCodes(t *testing.T) {
	f := newFixture(t)
	f.platform.mu.Lock()
	f.platform.sendCode = 99991663 // invalid access token reported as code 200
	f.platform.sendMessage = "Invalid access token"
	f.platform.mu.Unlock()
	if err := f.adapter.Send(context.Background(), "oc_chat", "hi"); !errors.Is(err, contracts.ErrFeishuUnauthorized) {
		t.Fatalf("auth code err = %v, want ErrFeishuUnauthorized", err)
	}

	f.platform.mu.Lock()
	f.platform.sendCode = 230001
	f.platform.sendMessage = "internal"
	f.platform.mu.Unlock()
	if err := f.adapter.Send(context.Background(), "oc_chat", "hi"); !errors.Is(err, contracts.ErrFeishuUnavailable) {
		t.Fatalf("platform code err = %v, want ErrFeishuUnavailable", err)
	}
}

func TestSendMapsUnavailable(t *testing.T) {
	f := newFixture(t)
	f.platform.mu.Lock()
	f.platform.sendStatus = http.StatusInternalServerError
	f.platform.mu.Unlock()
	if err := f.adapter.Send(context.Background(), "oc_chat", "hi"); !errors.Is(err, contracts.ErrFeishuUnavailable) {
		t.Fatalf("5xx err = %v, want ErrFeishuUnavailable", err)
	}

	// A closed server is a network failure, not an auth failure.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	offline := newFixtureWith(t, func(cfg *feishu.Config) { cfg.BaseURL = deadURL })
	if err := offline.adapter.Send(context.Background(), "oc_chat", "hi"); !errors.Is(err, contracts.ErrFeishuUnavailable) {
		t.Fatalf("network err = %v, want ErrFeishuUnavailable", err)
	}
}

func TestSendMintsAndCachesTheTenantToken(t *testing.T) {
	f := newFixtureWith(t, func(cfg *feishu.Config) {
		cfg.Token = ""
		cfg.AppID = "cli_app"
		cfg.AppSecret = "app-secret"
	})
	for i := 0; i < 2; i++ {
		if err := f.adapter.Send(context.Background(), "oc_chat", "hi"); err != nil {
			t.Fatalf("Send #%d: %v", i+1, err)
		}
	}
	f.platform.mu.Lock()
	mints := f.platform.tokenMints
	f.platform.mu.Unlock()
	if mints != 1 {
		t.Fatalf("token mints = %d, want the token to be cached", mints)
	}
	sent, _ := f.platform.lastSend()
	if sent.auth != "Bearer t-minted" {
		t.Fatalf("authorization = %q, want the minted token", sent.auth)
	}
}

func TestSendMapsBadAppCredentials(t *testing.T) {
	f := newFixtureWith(t, func(cfg *feishu.Config) {
		cfg.Token = ""
		cfg.AppID = "cli_app"
		cfg.AppSecret = "wrong"
	})
	f.platform.mu.Lock()
	f.platform.tokenCode = 10014 // app secret invalid
	f.platform.mu.Unlock()
	if err := f.adapter.Send(context.Background(), "oc_chat", "hi"); !errors.Is(err, contracts.ErrFeishuUnauthorized) {
		t.Fatalf("err = %v, want ErrFeishuUnauthorized", err)
	}
}

func TestStatusWithoutCredentialsIsDisconnected(t *testing.T) {
	f := newFixtureWith(t, func(cfg *feishu.Config) { cfg.Token = "" })
	status, err := f.adapter.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Connected {
		t.Fatalf("status = %+v, want disconnected", status)
	}
}
