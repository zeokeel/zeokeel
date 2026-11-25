package wechat_test

// Platform-shaped replay fixtures for the WeChat (iLink) adapter.
//
// testdata/ holds recorded response bodies, not bodies this file invents: the
// fake below writes the file bytes straight to the wire, so whatever shape the
// platform uses is what the adapter decodes. testdata/README.md is the
// provenance table — each fixture there says whether the adapter must ingest,
// skip, refuse or (for the two knowingly-unhandled shapes) silently drop, and
// why.
//
// The point of these tests is that the adapter's *silences* are the expensive
// failures on this channel: a media-only message must not become an empty-text
// inbox row, an empty poll must not reset the 24h reply window, and — the bug
// this file was written to catch — a `sendmessage` that answers HTTP 200 with a
// body-level error code must not be reported to the caller as delivered.
//
// The wire contract being pinned is the one in docs/research/channel-capabilities.md
// §1 plus docs/plans/grokbot-open-source-plan.md §6: `getupdates` long-poll in,
// `sendmessage` out with the current message's `context_token`, and "missing
// fields → HTTP 200 but not delivered" on the send side.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
	"github.com/zeokeel/zeokeel/internal/modules/wechat"
)

// readFixture loads one recorded response body. It is replayed verbatim: a test
// that re-encoded it would still pass while the adapter silently stopped
// understanding the real shape.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

// fixtureILink is the replay server: every endpoint answers with a recorded
// body. Unlike fakeILink it does not build payloads, because the fixture files
// exist precisely to stop the test from deciding what the platform says.
type fixtureILink struct {
	mu sync.Mutex
	// updates is the getupdates answer; updatesStatus makes it an HTTP failure.
	updates       []byte
	updatesStatus int
	// sendBody/sendStatus are the sendmessage answer.
	sendBody   []byte
	sendStatus int
	// loginBody/loginStatus are the getloginqrcode answer.
	loginBody   []byte
	loginStatus int

	polls int
	sends []map[string]any
}

func (f *fixtureILink) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ilink/bot/getupdates", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		f.mu.Lock()
		body, status := f.updates, f.updatesStatus
		f.polls++
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/ilink/bot/sendmessage", func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.sends = append(f.sends, payload)
		body, status := f.sendBody, f.sendStatus
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/ilink/bot/getloginqrcode", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		f.mu.Lock()
		body, status := f.loginBody, f.loginStatus
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write(body)
	})
	return mux
}

// answerUpdates installs a recorded getupdates body.
func (f *fixtureILink) answerUpdates(t *testing.T, name string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = readFixture(t, name)
}

// answerSend installs a recorded sendmessage body.
func (f *fixtureILink) answerSend(t *testing.T, name string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendBody = readFixture(t, name)
}

func (f *fixtureILink) sentMessages() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.sends...)
}

func (f *fixtureILink) pollCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.polls
}

// newReplayFixture wires an adapter to the replay server with a permissive
// inbox, so a failure is always the adapter's decision rather than a rate limit.
// The default sendmessage answer is the recorded success body: a test that sends
// has to say what the platform answered.
func newReplayFixture(t *testing.T) (*wechat.Adapter, *fixtureILink, contracts.InboxService, *clock) {
	t.Helper()
	server := &fixtureILink{sendBody: readFixture(t, "sendmessage_ok.json")}
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

// inboxItems returns everything filed so far.
func inboxItems(t *testing.T, svc contracts.InboxService) []contracts.InboxItem {
	t.Helper()
	items, err := svc.List(context.Background(), contracts.InboxFilter{})
	if err != nil {
		t.Fatalf("inbox list: %v", err)
	}
	return items
}

// assertNothingIngested pins the silence: no inbox row at all (not an empty-text
// one), no error path taken, and the 24h reply window untouched.
func assertNothingIngested(t *testing.T, adapter *wechat.Adapter, svc contracts.InboxService) {
	t.Helper()
	items := inboxItems(t, svc)
	if len(items) != 0 {
		t.Fatalf("inbox = %+v, want no item: a skipped message must leave no row", items)
	}
	status, err := adapter.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.LastInbound.IsZero() {
		t.Fatalf("last inbound = %v, want zero: nothing was ingested, so no reply window may open", status.LastInbound)
	}
	if !status.WindowClosesAt.IsZero() {
		t.Fatalf("window closes at %v, want zero", status.WindowClosesAt)
	}
}

// The one shape that must work end to end: a text message is filed with the
// platform's identity fields and its context token becomes the reply token.
func TestReplayGetUpdatesTextMessageIngestsAndRepliesWithItsToken(t *testing.T) {
	adapter, server, inboxSvc, clk := newReplayFixture(t)
	server.answerUpdates(t, "getupdates_text_message.json")

	ingested, err := adapter.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("PollOnce(text message): %v", err)
	}
	if ingested != 1 {
		t.Fatalf("ingested = %d, want 1", ingested)
	}

	items := inboxItems(t, inboxSvc)
	if len(items) != 1 {
		t.Fatalf("inbox = %+v, want exactly one item", items)
	}
	item := items[0]
	want := map[string]string{
		"source":  "wechat",
		"channel": "wxid_alice01", // from_user_id names the single-chat address
		"sender":  "wxid_alice01",
		"dedup":   "wechat:7502345678901234567",
		"title":   "帮我看下昨天的退款率",
		"body":    "帮我看下昨天的退款率",
	}
	if item.Source != want["source"] {
		t.Fatalf("source = %q, want %q", item.Source, want["source"])
	}
	if item.Channel != want["channel"] {
		t.Fatalf("channel = %q, want the fixture's from_user_id %q", item.Channel, want["channel"])
	}
	if item.Sender != want["sender"] {
		t.Fatalf("sender = %q, want %q", item.Sender, want["sender"])
	}
	if item.DedupKey != want["dedup"] {
		t.Fatalf("dedup key = %q, want %q", item.DedupKey, want["dedup"])
	}
	if item.Body != want["body"] {
		t.Fatalf("body = %q, want exactly the trimmed text_item.text %q", item.Body, want["body"])
	}
	if item.Title != want["title"] {
		t.Fatalf("title = %q, want %q", item.Title, want["title"])
	}
	// Ignored metadata must not leak into the body: to_user_id is the bot.
	for _, forbidden := range []string{"wxid_zeokeel_bot", "ctx-3f9a1c7d2b"} {
		if strings.Contains(item.Body, forbidden) || strings.Contains(item.Title, forbidden) {
			t.Fatalf("body/title contain ignored field %q: body=%q title=%q", forbidden, item.Body, item.Title)
		}
	}

	status, err := adapter.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Connected {
		t.Fatal("status = disconnected, want connected (a poll with the configured token ran)")
	}
	if !status.LastInbound.Equal(clk.now()) {
		t.Fatalf("last inbound = %v, want %v", status.LastInbound, clk.now())
	}
	if want := clk.now().Add(contracts.WeChatWindow); !status.WindowClosesAt.Equal(want) {
		t.Fatalf("window closes at %v, want %v", status.WindowClosesAt, want)
	}

	// The reply has to ride *this* message's context token: that is the one
	// platform rule the research note is explicit about.
	if err := adapter.Send(context.Background(), "wxid_alice01", "收到，我在查"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	sent := server.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("sendmessage calls = %d, want 1", len(sent))
	}
	msg, ok := sent[0]["msg"].(map[string]any)
	if !ok {
		t.Fatalf("payload = %v, want a msg object", sent[0])
	}
	if msg["context_token"] != "ctx-3f9a1c7d2b" {
		t.Fatalf("context_token = %v, want the ingested message's token", msg["context_token"])
	}
	if msg["to_user_id"] != "wxid_alice01" {
		t.Fatalf("to_user_id = %v, want the fixture's from_user_id", msg["to_user_id"])
	}
}

// The documented "nothing new" answer. The long-poll returns this most of the
// time, so it must not move the 24h window: an empty poll is not an interaction.
func TestReplayGetUpdatesEmptyIsANoOp(t *testing.T) {
	adapter, server, inboxSvc, clk := newReplayFixture(t)
	server.answerUpdates(t, "getupdates_empty.json")

	ingested, err := adapter.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("PollOnce(empty): %v", err)
	}
	if ingested != 0 {
		t.Fatalf("ingested = %d, want 0", ingested)
	}
	assertNothingIngested(t, adapter, inboxSvc)
	if adapter.NeedsKeepalive(clk.now().Add(25 * time.Hour)) {
		t.Fatal("NeedsKeepalive = true without any inbound message: there is no window to keep alive")
	}
}

// A media-only message is the silent-failure case the channel is vulnerable to:
// it must be skipped, and skipping must not open a reply window for a message
// nobody was ever shown.
func TestReplayGetUpdatesMediaOnlyIsSkipped(t *testing.T) {
	adapter, server, inboxSvc, _ := newReplayFixture(t)
	server.answerUpdates(t, "getupdates_media_only.json")

	ingested, err := adapter.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("PollOnce(media only): %v", err)
	}
	if ingested != 0 {
		t.Fatalf("ingested = %d, want 0: the channel is text-only", ingested)
	}
	assertNothingIngested(t, adapter, inboxSvc)

	// No context token was recorded either, so a reply attempt fails loudly
	// instead of being posted to the platform where it would be dropped.
	err = adapter.Send(context.Background(), "wxid_alice01", "收到")
	if !errors.Is(err, contracts.ErrWeChatNoContext) {
		t.Fatalf("Send after a media-only message = %v, want ErrWeChatNoContext", err)
	}
	if sent := server.sentMessages(); len(sent) != 0 {
		t.Fatalf("sendmessage calls = %d, want 0: a refused send must not reach the platform", len(sent))
	}
}

// FIXED (was testdata/README.md §1): the wrapped {"data":{"msgs":[…]}} shape some
// iLink deployments send is now unwrapped instead of being read as "no messages".
// The old behaviour was the worst kind of bug: a poll loop reporting success while
// the queue quietly stopped draining.
func TestReplayGetUpdatesDataEnvelopeIsUnwrapped(t *testing.T) {
	adapter, server, inboxSvc, _ := newReplayFixture(t)
	server.answerUpdates(t, "getupdates_data_envelope.json")

	ingested, err := adapter.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("PollOnce(data envelope) = %v", err)
	}
	if ingested != 1 {
		t.Fatalf("ingested = %d, want the wrapped message unwrapped", ingested)
	}
	items, listErr := inboxSvc.List(context.Background(), contracts.InboxFilter{})
	if listErr != nil || len(items) != 1 {
		t.Fatalf("inbox = (%+v, %v), want one row", items, listErr)
	}
}

// FIXED (was testdata/README.md §2): a 200 whose body carries ret_code != 0 is a
// refusal, not an empty poll. Reporting it as an error makes the loop back off and
// keeps Status honest instead of saying "connected, nothing new" forever.
func TestReplayGetUpdatesErrorRetCodeIsAnError(t *testing.T) {
	adapter, server, inboxSvc, _ := newReplayFixture(t)
	server.answerUpdates(t, "getupdates_error_retcode.json")

	if _, err := adapter.PollOnce(context.Background()); !errors.Is(err, contracts.ErrWeChatUnavailable) {
		t.Fatalf("PollOnce(ret_code -1) = %v, want ErrWeChatUnavailable", err)
	}
	assertNothingIngested(t, adapter, inboxSvc)
}

// The delivered answer: HTTP 200 {"ok": true} is the only body that may be
// reported as a successful send.
func TestReplaySendMessageOKIsDelivered(t *testing.T) {
	adapter, server, _, _ := newReplayFixture(t)
	server.answerUpdates(t, "getupdates_text_message.json")
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	server.answerSend(t, "sendmessage_ok.json")

	if err := adapter.Send(context.Background(), "wxid_alice01", "收到，我在查"); err != nil {
		t.Fatalf("Send(ok body) = %v, want nil", err)
	}
	if sent := server.sentMessages(); len(sent) != 1 {
		t.Fatalf("sendmessage calls = %d, want 1", len(sent))
	}
}

// REGRESSION (was a real bug, fixed in adapter.go): an HTTP 200 whose body says
// the platform refused the message.
//
// Before the fix, Send returned nil for *any* 200. On this platform a refused
// send arrives as a 200 (the research note says missing fields get "HTTP 200 but
// not delivered"), so the caller was told "delivered" while the user received
// nothing — the worst failure mode a notification channel has, because nothing
// retries or falls back. The check now reads the body and maps a body-level
// error onto ErrWeChatUnavailable carrying the platform's errmsg.
func TestReplaySendMessageErrorRetCodeIsRefused(t *testing.T) {
	adapter, server, _, _ := newReplayFixture(t)
	server.answerUpdates(t, "getupdates_text_message.json")
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	server.answerSend(t, "sendmessage_error_retcode.json")

	err := adapter.Send(context.Background(), "wxid_alice01", "收到，我在查")
	if err == nil {
		t.Fatal("Send(200 with ret_code -1) = nil: a refused message must never be reported as delivered")
	}
	if !errors.Is(err, contracts.ErrWeChatUnavailable) {
		t.Fatalf("err = %v, want it to wrap ErrWeChatUnavailable so the caller can retry/fall back", err)
	}
	if !strings.Contains(err.Error(), "invalid context token") {
		t.Fatalf("err = %v, want the platform's errmsg in the message", err)
	}
	// The request did reach the platform (it answered), so exactly one attempt
	// is expected — the point is the *verdict*, not a client-side refusal.
	if sent := server.sentMessages(); len(sent) != 1 {
		t.Fatalf("sendmessage calls = %d, want 1", len(sent))
	}
}

// The two body shapes the delivery check must also get right, in both
// directions. Pin both so the check cannot become "reject anything I did not
// expect" (which would fail every send on a platform that answers tersely):
//
//   - {"ok": false} is an explicit refusal, same class as a non-zero ret_code.
//   - {} carries no verdict at all; the request already got a 200 and nothing in
//     the body contradicts it, so it stays a delivery.
func TestReplaySendMessageBodyVerdicts(t *testing.T) {
	for _, testCase := range []struct {
		fixture   string
		wantErr   bool
		wantInMsg string
		why       string
	}{
		{"sendmessage_ok_false.json", true, "send too frequently", "ok:false is a refusal"},
		{"sendmessage_no_verdict.json", false, "", "an empty verdict contradicts nothing"},
	} {
		t.Run(testCase.fixture, func(t *testing.T) {
			adapter, server, _, _ := newReplayFixture(t)
			server.answerUpdates(t, "getupdates_text_message.json")
			if _, err := adapter.PollOnce(context.Background()); err != nil {
				t.Fatalf("poll: %v", err)
			}
			server.answerSend(t, testCase.fixture)

			err := adapter.Send(context.Background(), "wxid_alice01", "收到，我在查")
			switch {
			case testCase.wantErr && err == nil:
				t.Fatalf("Send(%s) = nil, want a refusal (%s)", testCase.fixture, testCase.why)
			case testCase.wantErr && !errors.Is(err, contracts.ErrWeChatUnavailable):
				t.Fatalf("err = %v, want it to wrap ErrWeChatUnavailable", err)
			case testCase.wantErr && !strings.Contains(err.Error(), testCase.wantInMsg):
				t.Fatalf("err = %v, want the platform's errmsg %q", err, testCase.wantInMsg)
			case !testCase.wantErr && err != nil:
				t.Fatalf("Send(%s) = %v, want nil (%s)", testCase.fixture, err, testCase.why)
			}
		})
	}
}

// Regression for the QR answer: the platform TTL (60s here) must win over the
// local 5-minute default, otherwise the UI keeps offering a code that the
// platform has already retired.
func TestReplayLoginQRCodeHonoursThePlatformTTL(t *testing.T) {
	adapter, server, _, clk := newReplayFixture(t)
	server.mu.Lock()
	server.loginBody = readFixture(t, "login_qrcode.json")
	server.mu.Unlock()

	login, err := adapter.StartLogin(context.Background())
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if want := "qr_9f3c1d7a5b2e"; login.QRCodeID != want {
		t.Fatalf("QRCodeID = %q, want %q", login.QRCodeID, want)
	}
	if want := "https://ilinkai.weixin.qq.com/qr/qr_9f3c1d7a5b2e"; login.QRCodeURL != want {
		t.Fatalf("QRCodeURL = %q, want %q", login.QRCodeURL, want)
	}
	if want := clk.now().Add(60 * time.Second); !login.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v (expires_in: 60 from the fixture)", login.ExpiresAt, want)
	}
}

// The two silent-drop shapes are fixed: a wrapped payload is unwrapped, and a
// 200 that carries an error ret_code is an error rather than "no messages".

func TestDataEnvelopeIsUnwrappedInsteadOfDropped(t *testing.T) {
	inboxSvc := inbox.NewService(inbox.Config{Now: time.Now, MaxPerMinute: 100})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"msgs":[{"msg_id":"m-envelope","from_user_id":"wxid_bob","to_user_id":"bot","context_token":"tok","item_list":[{"type":1,"text_item":{"text":"在吗"}}]}]}}`))
	}))
	t.Cleanup(server.Close)
	adapter := wechat.NewAdapter(inboxSvc, wechat.Config{BaseURL: server.URL, Token: "bot-token", Now: time.Now})

	ingested, err := adapter.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if ingested != 1 {
		t.Fatalf("ingested = %d, want the wrapped message to be unwrapped", ingested)
	}
	items, _ := inboxSvc.List(context.Background(), contracts.InboxFilter{})
	if len(items) != 1 || items[0].Sender != "wxid_bob" {
		t.Fatalf("items = %+v", items)
	}
}

func TestGetUpdatesRetCodeIsAnErrorNotAnEmptyPoll(t *testing.T) {
	inboxSvc := inbox.NewService(inbox.Config{Now: time.Now, MaxPerMinute: 100})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ret_code":-2,"errmsg":"token expired"}`))
	}))
	t.Cleanup(server.Close)
	adapter := wechat.NewAdapter(inboxSvc, wechat.Config{BaseURL: server.URL, Token: "bot-token", Now: time.Now})

	if _, err := adapter.PollOnce(context.Background()); !errors.Is(err, contracts.ErrWeChatUnavailable) {
		t.Fatalf("err = %v, want ErrWeChatUnavailable so the loop backs off instead of pretending it is empty", err)
	}
}
