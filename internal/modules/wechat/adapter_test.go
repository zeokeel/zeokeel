package wechat_test

// TDD for the WeChat ClawBot adapter against a fake iLink server. The platform
// rules that cost real money to learn the hard way are the tests: every
// sendmessage carries the full field set plus the *current* context token, and a
// reply outside the 24h window is refused locally instead of being dropped
// silently by the platform.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
	"github.com/zeokeel/zeokeel/internal/modules/wechat"
)

type fakeILink struct {
	mu        sync.Mutex
	updates   []map[string]any
	sends     []map[string]any
	sendError int
	status    int
}

func (f *fakeILink) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ilink/bot/getupdates", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		updates := f.updates
		f.updates = nil
		status := f.status
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"msgs": updates})
	})
	mux.HandleFunc("/ilink/bot/sendmessage", func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.sends = append(f.sends, payload)
		sendError := f.sendError
		f.mu.Unlock()
		if sendError != 0 {
			w.WriteHeader(sendError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	return mux
}

func (f *fakeILink) lastSend() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sends) == 0 {
		return nil
	}
	return f.sends[len(f.sends)-1]
}

func (f *fakeILink) pushMessage(id, from, text string, token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, map[string]any{
		"msg_id":        id,
		"from_user_id":  from,
		"to_user_id":    "bot@im.wechat",
		"context_token": token,
		"item_list": []map[string]any{
			{"type": 1, "text_item": map[string]any{"text": text}},
		},
	})
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func newFixture(t *testing.T) (*wechat.Adapter, *fakeILink, contracts.InboxService, *clock) {
	t.Helper()
	server := &fakeILink{}
	httpServer := httptest.NewServer(server.handler())
	t.Cleanup(httpServer.Close)
	clk := &clock{t: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
	inboxSvc := inbox.NewService(inbox.Config{Now: clk.now, MaxPerMinute: 1000})
	adapter := wechat.NewAdapter(inboxSvc, wechat.Config{
		BaseURL: httpServer.URL,
		Token:   "bot-token",
		Now:     clk.now,
	})
	return adapter, server, inboxSvc, clk
}

func TestPollOnceIngestsTextAndStatus(t *testing.T) {
	adapter, server, inboxSvc, clk := newFixture(t)
	server.pushMessage("m1", "user@im.wechat", "帮我看下昨天的退款率", "token-1")

	ingested, err := adapter.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if ingested != 1 {
		t.Fatalf("ingested = %d, want 1", ingested)
	}
	items, err := inboxSvc.List(context.Background(), contracts.InboxFilter{})
	if err != nil || len(items) != 1 {
		t.Fatalf("inbox = (%v, %v), want one item", items, err)
	}
	item := items[0]
	if item.Source != "wechat" || item.Sender != "user@im.wechat" {
		t.Fatalf("item = %+v", item)
	}
	if !strings.Contains(item.Title, "退款率") {
		t.Fatalf("title = %q", item.Title)
	}

	status, err := adapter.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Connected || status.LastInbound.IsZero() {
		t.Fatalf("status = %+v, want connected with a last inbound", status)
	}
	if !status.WindowClosesAt.Equal(clk.now().Add(contracts.WeChatWindow)) {
		t.Fatalf("window closes = %v, want %v", status.WindowClosesAt, clk.now().Add(contracts.WeChatWindow))
	}
}

func TestPollOnceDedupesPlatformRetries(t *testing.T) {
	adapter, server, inboxSvc, _ := newFixture(t)
	server.pushMessage("m1", "user@im.wechat", "hi", "token-1")
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	server.pushMessage("m1", "user@im.wechat", "hi", "token-1")
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	items, _ := inboxSvc.List(context.Background(), contracts.InboxFilter{})
	if len(items) != 1 || items[0].Count != 2 {
		t.Fatalf("items = %+v, want one with count 2", items)
	}
}

func TestSendCarriesTheFullPayload(t *testing.T) {
	adapter, server, _, _ := newFixture(t)
	server.pushMessage("m1", "user@im.wechat", "hi", "token-1")
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if err := adapter.Send(context.Background(), "user@im.wechat", "收到，我在查"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	payload := server.lastSend()
	msg, ok := payload["msg"].(map[string]any)
	if !ok {
		t.Fatalf("payload = %v, want a msg object", payload)
	}
	for _, field := range []string{"from_user_id", "to_user_id", "client_id", "message_type", "message_state", "context_token"} {
		if _, ok := msg[field]; !ok {
			t.Fatalf("msg missing required field %q: %v", field, msg)
		}
	}
	if msg["context_token"] != "token-1" {
		t.Fatalf("context_token = %v, want the inbound token", msg["context_token"])
	}
	base, _ := payload["base_info"].(map[string]any)
	if base["channel_version"] != contracts.WeChatChannelVersion {
		t.Fatalf("channel_version = %v", base["channel_version"])
	}
}

func TestSendRefusesWithoutContext(t *testing.T) {
	adapter, _, _, _ := newFixture(t)
	if err := adapter.Send(context.Background(), "user@im.wechat", "hi"); !errors.Is(err, contracts.ErrWeChatNoContext) {
		t.Fatalf("err = %v, want ErrWeChatNoContext", err)
	}
}

func TestSendRefusesOutsideTheWindow(t *testing.T) {
	adapter, server, _, clk := newFixture(t)
	server.pushMessage("m1", "user@im.wechat", "hi", "token-1")
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	clk.advance(contracts.WeChatWindow + time.Minute)
	if err := adapter.Send(context.Background(), "user@im.wechat", "late"); !errors.Is(err, contracts.ErrWeChatWindowClosed) {
		t.Fatalf("err = %v, want ErrWeChatWindowClosed", err)
	}
	if server.lastSend() != nil {
		t.Fatal("a refused message was still posted to the platform")
	}
}

func TestSendRejectsEmptyText(t *testing.T) {
	adapter, server, _, _ := newFixture(t)
	server.pushMessage("m1", "user@im.wechat", "hi", "token-1")
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if err := adapter.Send(context.Background(), "user@im.wechat", "   "); !errors.Is(err, contracts.ErrWeChatEmptyMessage) {
		t.Fatalf("err = %v, want ErrWeChatEmptyMessage", err)
	}
}

func TestSendSurfacesPlatformFailure(t *testing.T) {
	adapter, server, _, _ := newFixture(t)
	server.pushMessage("m1", "user@im.wechat", "hi", "token-1")
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	server.sendError = http.StatusInternalServerError
	if err := adapter.Send(context.Background(), "user@im.wechat", "hi"); !errors.Is(err, contracts.ErrWeChatUnavailable) {
		t.Fatalf("err = %v, want ErrWeChatUnavailable", err)
	}
}

func TestPollSurfacesUnauthorized(t *testing.T) {
	adapter, server, _, _ := newFixture(t)
	server.status = http.StatusUnauthorized
	if _, err := adapter.PollOnce(context.Background()); !errors.Is(err, contracts.ErrWeChatUnauthorized) {
		t.Fatalf("err = %v, want ErrWeChatUnauthorized", err)
	}
}
