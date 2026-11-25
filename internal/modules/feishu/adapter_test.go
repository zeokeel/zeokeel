package feishu_test

// TDD for the Feishu (Lark) open-platform adapter. The platform rules that cost
// real money to learn the hard way are the tests: the signature is
// sha256(timestamp + nonce + encryptKey + rawBody) over the *raw* bytes (a
// re-encoded body never matches), an old timestamp is a replay, and a url
// verification handshake must echo the challenge without writing an inbox row.
//
// The signing helpers below are written from the platform contract, not from
// the adapter's code: if the two drift apart, every test here goes red.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/feishu"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
)

const (
	testEncryptKey = "enc-key-1"
	testToken      = "t-tenant-token"
)

// digest is hex(sha256(parts...)) — the shape Feishu puts in X-Lark-Signature.
func digest(parts ...[]byte) string {
	sum := sha256.New()
	for _, part := range parts {
		sum.Write(part)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// signatureV2 is the documented scheme: timestamp + nonce + encryptKey + body.
func signatureV2(timestamp int64, nonce, encryptKey string, body []byte) string {
	return digest([]byte(strconv.FormatInt(timestamp, 10)), []byte(nonce), []byte(encryptKey), body)
}

// signatureLegacy is the older-bot scheme: the same secret and body, but no
// nonce (see the adapter's doc comment for why this is still accepted).
func signatureLegacy(timestamp int64, encryptKey string, body []byte) string {
	return digest([]byte(strconv.FormatInt(timestamp, 10)), []byte(encryptKey), body)
}

func signedHeaders(timestamp int64, nonce, encryptKey string, body []byte) map[string]string {
	return map[string]string{
		"X-Lark-Request-Timestamp": strconv.FormatInt(timestamp, 10),
		"X-Lark-Request-Nonce":     nonce,
		"X-Lark-Signature":         signatureV2(timestamp, nonce, encryptKey, body),
	}
}

// textEvent builds an im.message.receive_v1 push with a JSON-encoded text body,
// exactly as the platform sends it (content is a *string* holding JSON).
func textEvent(messageID, chatID, openID, text string) []byte {
	content, _ := json.Marshal(map[string]string{"text": text})
	payload := map[string]any{
		"schema": "2.0",
		"header": map[string]any{
			"event_id":    "ev-" + messageID,
			"event_type":  "im.message.receive_v1",
			"create_time": "1750000000000",
			"token":       "verification-token",
			"app_id":      "cli_test",
			"tenant_key":  "tenant",
		},
		"event": map[string]any{
			"sender": map[string]any{
				"sender_id":   map[string]any{"open_id": openID, "union_id": "on_x", "user_id": "u_x"},
				"sender_type": "user",
			},
			"message": map[string]any{
				"message_id":   messageID,
				"chat_id":      chatID,
				"chat_type":    "p2p",
				"message_type": "text",
				"content":      string(content),
				"create_time":  "1750000000000",
			},
		},
	}
	encoded, _ := json.Marshal(payload)
	return encoded
}

func challengeBody(challenge string) []byte {
	encoded, _ := json.Marshal(map[string]string{"type": "url_verification", "challenge": challenge})
	return encoded
}

// clock is the adapter's injected clock, so "stale" is deterministic.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

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
	inbox    contracts.InboxService
	clk      *clock
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

	clk := &clock{t: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
	inboxService := inbox.NewService(inbox.Config{Now: clk.now, MaxPerMinute: 1000})
	cfg := feishu.Config{
		BaseURL:    server.URL,
		Token:      testToken,
		EncryptKey: testEncryptKey,
		Now:        clk.now,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	adapter := feishu.NewAdapter(inboxService, cfg)
	return &fixture{adapter: adapter, platform: platform, inbox: inboxService, clk: clk, baseURL: server.URL}
}

// headersNow signs body with the fixture clock (the platform's timestamp).
func (f *fixture) headersNow(nonce string, body []byte) map[string]string {
	return signedHeaders(f.clk.now().Unix(), nonce, testEncryptKey, body)
}

func (f *fixture) inboxItems(t *testing.T) []contracts.InboxItem {
	t.Helper()
	items, err := f.inbox.List(context.Background(), contracts.InboxFilter{})
	if err != nil {
		t.Fatalf("inbox list: %v", err)
	}
	return items
}

func TestVerifyURLEchoesTheChallengeWithoutIngesting(t *testing.T) {
	f := newFixture(t)
	body := challengeBody("ch-12345")

	challenge, ok := f.adapter.VerifyURL(body)
	if !ok || challenge != "ch-12345" {
		t.Fatalf("VerifyURL = (%q, %v), want (ch-12345, true)", challenge, ok)
	}

	item, merged, err := f.adapter.Receive(context.Background(), f.headersNow("n1", body), body)
	if err != nil {
		t.Fatalf("Receive(challenge): %v", err)
	}
	if merged || item.ID != "" {
		t.Fatalf("challenge produced an inbox item: %+v", item)
	}
	if items := f.inboxItems(t); len(items) != 0 {
		t.Fatalf("inbox = %+v, want no writes for a challenge", items)
	}
}

func TestVerifyURLRejectsNonChallengePayloads(t *testing.T) {
	f := newFixture(t)
	if _, ok := f.adapter.VerifyURL(textEvent("om_1", "oc_1", "ou_1", "hi")); ok {
		t.Fatal("VerifyURL accepted an event body")
	}
	if _, ok := f.adapter.VerifyURL([]byte("not json")); ok {
		t.Fatal("VerifyURL accepted garbage")
	}
	if _, ok := f.adapter.VerifyURL([]byte(`{"type":"url_verification"}`)); ok {
		t.Fatal("VerifyURL accepted a challenge without a challenge value")
	}
}

func TestChallengeWithABadSignatureIsStillRejected(t *testing.T) {
	f := newFixture(t)
	body := challengeBody("ch-1")
	headers := f.headersNow("n1", body)
	headers["X-Lark-Signature"] = signatureV2(f.clk.now().Unix(), "n1", "wrong-key", body)
	if _, _, err := f.adapter.Receive(context.Background(), headers, body); !errors.Is(err, contracts.ErrFeishuBadSignature) {
		t.Fatalf("err = %v, want ErrFeishuBadSignature", err)
	}
}

func TestReceiveRejectsBadSignature(t *testing.T) {
	f := newFixture(t)
	body := textEvent("om_1", "oc_1", "ou_1", "帮我查退款率")
	headers := f.headersNow("n1", body)
	headers["X-Lark-Signature"] = strings.Repeat("0", 64)

	if _, _, err := f.adapter.Receive(context.Background(), headers, body); !errors.Is(err, contracts.ErrFeishuBadSignature) {
		t.Fatalf("err = %v, want ErrFeishuBadSignature", err)
	}
	if items := f.inboxItems(t); len(items) != 0 {
		t.Fatalf("inbox = %+v, want no writes for an unsigned request", items)
	}
}

func TestReceiveRejectsMissingSignatureHeaders(t *testing.T) {
	f := newFixture(t)
	body := textEvent("om_1", "oc_1", "ou_1", "hi")
	if _, _, err := f.adapter.Receive(context.Background(), map[string]string{}, body); !errors.Is(err, contracts.ErrFeishuBadSignature) {
		t.Fatalf("err = %v, want ErrFeishuBadSignature", err)
	}
	headers := f.headersNow("n1", body)
	delete(headers, "X-Lark-Request-Timestamp")
	if _, _, err := f.adapter.Receive(context.Background(), headers, body); !errors.Is(err, contracts.ErrFeishuBadSignature) {
		t.Fatalf("err = %v, want ErrFeishuBadSignature without a timestamp", err)
	}
}

func TestReceiveRejectsStaleRequests(t *testing.T) {
	f := newFixture(t)
	body := textEvent("om_1", "oc_1", "ou_1", "hi")
	stale := f.clk.now().Add(-1 * (contracts.FeishuMaxClockSkew + time.Minute)).Unix()

	if _, _, err := f.adapter.Receive(context.Background(), signedHeaders(stale, "n1", testEncryptKey, body), body); !errors.Is(err, contracts.ErrFeishuStale) {
		t.Fatalf("err = %v, want ErrFeishuStale", err)
	}
	future := f.clk.now().Add(contracts.FeishuMaxClockSkew + time.Minute).Unix()
	if _, _, err := f.adapter.Receive(context.Background(), signedHeaders(future, "n1", testEncryptKey, body), body); !errors.Is(err, contracts.ErrFeishuStale) {
		t.Fatalf("future-dated err = %v, want ErrFeishuStale", err)
	}
}

func TestReceiveAcceptsTheLegacySignatureScheme(t *testing.T) {
	f := newFixture(t)
	body := textEvent("om_legacy", "oc_1", "ou_1", "老机器人")
	timestamp := f.clk.now().Unix()
	headers := map[string]string{
		"X-Lark-Request-Timestamp": strconv.FormatInt(timestamp, 10),
		"X-Lark-Signature":         signatureLegacy(timestamp, testEncryptKey, body),
	}

	item, merged, err := f.adapter.Receive(context.Background(), headers, body)
	if err != nil {
		t.Fatalf("Receive(legacy): %v", err)
	}
	if merged || item.ID == "" || item.Source != contracts.FeishuSource {
		t.Fatalf("item = %+v, want one feishu item", item)
	}
}

func TestReceiveIngestsATextMessage(t *testing.T) {
	f := newFixture(t)
	body := textEvent("om_100", "oc_chat", "ou_sender", "帮我看下昨天的退款率")

	item, merged, err := f.adapter.Receive(context.Background(), f.headersNow("n1", body), body)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if merged {
		t.Fatal("first arrival reported as a merge")
	}
	if item.Source != contracts.FeishuSource {
		t.Fatalf("source = %q, want %q", item.Source, contracts.FeishuSource)
	}
	if item.Channel != "oc_chat" || item.Sender != "ou_sender" {
		t.Fatalf("channel/sender = (%q, %q)", item.Channel, item.Sender)
	}
	if item.DedupKey != "feishu:om_100" {
		t.Fatalf("dedupe key = %q, want feishu:om_100", item.DedupKey)
	}
	if item.Body != "帮我看下昨天的退款率" || item.Title != "帮我看下昨天的退款率" {
		t.Fatalf("body/title = (%q, %q)", item.Body, item.Title)
	}
	if item.Ref != "om_100" {
		t.Fatalf("ref = %q, want the message id", item.Ref)
	}
	if item.Count != 1 {
		t.Fatalf("count = %d, want 1", item.Count)
	}

	status, err := f.adapter.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Connected || !status.LastInbound.Equal(f.clk.now()) {
		t.Fatalf("status = %+v, want connected with last inbound at the event time", status)
	}
}

func TestReceiveMergesPlatformRetries(t *testing.T) {
	f := newFixture(t)
	body := textEvent("om_dupe", "oc_chat", "ou_sender", "hi")

	if _, merged, err := f.adapter.Receive(context.Background(), f.headersNow("n1", body), body); err != nil || merged {
		t.Fatalf("first receive = (%v, %v), want a new item", merged, err)
	}
	if _, merged, err := f.adapter.Receive(context.Background(), f.headersNow("n2", body), body); err != nil || !merged {
		t.Fatalf("second receive = (%v, %v), want a merge", merged, err)
	}
	items := f.inboxItems(t)
	if len(items) != 1 || items[0].Count != 2 {
		t.Fatalf("items = %+v, want one item with count 2", items)
	}
}

func TestReceiveAcceptsTheV1EventCallbackShape(t *testing.T) {
	f := newFixture(t)
	// v1: type=event_callback at the top, the kind in event.type, no header.
	body := []byte(`{"type":"event_callback","event":{"type":"message","sender":{"sender_id":{"open_id":"ou_v1"}},"message":{"message_id":"om_v1","chat_id":"oc_v1","message_type":"text","content":"{\"text\":\"老版本回调\"}"}}}`)
	item, merged, err := f.adapter.Receive(context.Background(), f.headersNow("n1", body), body)
	if err != nil {
		t.Fatalf("Receive(v1): %v", err)
	}
	if merged || item.Source != contracts.FeishuSource || item.Channel != "oc_v1" || item.Sender != "ou_v1" {
		t.Fatalf("item = %+v, want one v1 feishu item", item)
	}
	if item.DedupKey != "feishu:om_v1" || item.Body != "老版本回调" {
		t.Fatalf("item = %+v", item)
	}

	// A v1 callback for another event kind must still be ignored, not misread
	// as a message with missing ids.
	other := []byte(`{"type":"event_callback","event":{"type":"event","event_type":"im.chat.member.bot.added_v1"}}`)
	if item, _, err := f.adapter.Receive(context.Background(), f.headersNow("n2", other), other); err != nil || item.ID != "" {
		t.Fatalf("v1 non-message = (%+v, %v), want it ignored", item, err)
	}
}

func TestReceiveIgnoresUnrelatedEvents(t *testing.T) {
	f := newFixture(t)
	// A non-message event we do not ingest.
	body := []byte(`{"schema":"2.0","header":{"event_type":"im.chat.member.bot.added_v1"},"event":{}}`)
	if item, merged, err := f.adapter.Receive(context.Background(), f.headersNow("n1", body), body); err != nil || merged || item.ID != "" {
		t.Fatalf("member-added = (%+v, %v, %v), want it ignored", item, merged, err)
	}

	// An image message: the channel is text-only for now, so it must not
	// become an empty inbox row.
	imageBody := []byte(`{"schema":"2.0","header":{"event_type":"im.message.receive_v1"},"event":{"sender":{"sender_id":{"open_id":"ou_1"}},"message":{"message_id":"om_img","chat_id":"oc_1","message_type":"image","content":"{\"image_key\":\"img_1\"}"}}}`)
	if item, _, err := f.adapter.Receive(context.Background(), f.headersNow("n2", imageBody), imageBody); err != nil || item.ID != "" {
		t.Fatalf("image = (%+v, %v), want it ignored", item, err)
	}
	if items := f.inboxItems(t); len(items) != 0 {
		t.Fatalf("inbox = %+v, want nothing ingested", items)
	}
}

func TestReceiveRejectsBadPayloads(t *testing.T) {
	f := newFixture(t)
	body := []byte("{not json")
	if _, _, err := f.adapter.Receive(context.Background(), f.headersNow("n1", body), body); !errors.Is(err, contracts.ErrFeishuBadPayload) {
		t.Fatalf("garbage err = %v, want ErrFeishuBadPayload", err)
	}

	// A text message without an identity cannot be filed.
	missing := []byte(`{"schema":"2.0","header":{"event_type":"im.message.receive_v1"},"event":{"sender":{"sender_id":{"open_id":"ou_1"}},"message":{"message_type":"text","chat_id":"oc_1","content":"{\"text\":\"hi\"}"}}}`)
	if _, _, err := f.adapter.Receive(context.Background(), f.headersNow("n2", missing), missing); !errors.Is(err, contracts.ErrFeishuBadPayload) {
		t.Fatalf("missing id err = %v, want ErrFeishuBadPayload", err)
	}

	// A text message whose content is not JSON.
	brokenContent := []byte(`{"schema":"2.0","header":{"event_type":"im.message.receive_v1"},"event":{"sender":{"sender_id":{"open_id":"ou_1"}},"message":{"message_id":"om_x","chat_id":"oc_1","message_type":"text","content":"plain"}}}`)
	if _, _, err := f.adapter.Receive(context.Background(), f.headersNow("n3", brokenContent), brokenContent); !errors.Is(err, contracts.ErrFeishuBadPayload) {
		t.Fatalf("bad content err = %v, want ErrFeishuBadPayload", err)
	}
	if items := f.inboxItems(t); len(items) != 0 {
		t.Fatalf("inbox = %+v, want no writes for bad payloads", items)
	}
}

func TestCardActionExtractsTheDecision(t *testing.T) {
	f := newFixture(t)

	v2 := []byte(`{"schema":"2.0","header":{"event_type":"card.action.trigger"},"event":{"operator":{"open_id":"ou_1"},"action":{"tag":"button","value":{"approvalId":"ap_42","decision":"allow"}}}}`)
	approvalID, decision, ok := f.adapter.CardAction(v2)
	if !ok || approvalID != "ap_42" || decision != contracts.FeishuDecisionAllow {
		t.Fatalf("v2 = (%q, %q, %v)", approvalID, decision, ok)
	}

	// Legacy top-level action, with the value carried as a JSON string.
	legacy := []byte(`{"open_id":"ou_1","action":{"tag":"button","value":"{\"approvalId\":\"ap_7\",\"decision\":\"deny\"}"}}`)
	approvalID, decision, ok = f.adapter.CardAction(legacy)
	if !ok || approvalID != "ap_7" || decision != contracts.FeishuDecisionDeny {
		t.Fatalf("legacy = (%q, %q, %v)", approvalID, decision, ok)
	}
}

func TestCardActionAbsentReturnsFalse(t *testing.T) {
	f := newFixture(t)
	cases := map[string]string{
		"no action":        `{"schema":"2.0","header":{"event_type":"card.action.trigger"},"event":{}}`,
		"no value":         `{"schema":"2.0","header":{"event_type":"card.action.trigger"},"event":{"action":{"tag":"button"}}}`,
		"no approval id":   `{"event":{"action":{"value":{"decision":"allow"}}}}`,
		"unknown decision": `{"event":{"action":{"value":{"approvalId":"ap_1","decision":"maybe"}}}}`,
		"garbage":          `not json`,
	}
	for name, body := range cases {
		if approvalID, decision, ok := f.adapter.CardAction([]byte(body)); ok {
			t.Fatalf("%s: extracted (%q, %q), want ok=false", name, approvalID, decision)
		}
	}
	if platform, ok := f.platform.lastSend(); ok {
		t.Fatalf("card action touched the platform: %+v", platform)
	}
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
	if status.Connected || !status.LastInbound.IsZero() {
		t.Fatalf("status = %+v, want disconnected and never seen", status)
	}
}
