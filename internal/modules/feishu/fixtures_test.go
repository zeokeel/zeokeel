package feishu_test

// Platform-shaped replay fixtures for the Feishu (Lark) open-platform callbacks.
//
// The bodies in testdata/ are transcribed from the official docs (testdata/README.md
// lists the URL behind every file), not invented here, because the shape is the
// contract: the event body nests the message under header/event and carries its
// `content` as a JSON *string*, the v1 envelope is flat, and an encrypted push
// wraps everything in {"encrypt": ...}.
//
// Every case asserts the typed outcome: the exact inbox item, the exact pair a
// card button yields, or one specific typed error plus "the inbox stayed empty".
//
// The signing helpers (signedHeaders, digest, clock) come from adapter_test.go and
// encode the documented rule: hex(sha256(timestamp + nonce + encryptKey + rawBody)).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/feishu"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
)

// replayEnvironment is the adapter under test with an empty inbox and a fixed
// clock. No platform server is started: these fixtures are all inbound.
type replayEnvironment struct {
	adapter *feishu.Adapter
	inbox   contracts.InboxService
	clk     *clock
}

func newReplayEnvironment(t *testing.T) *replayEnvironment {
	t.Helper()
	clk := &clock{t: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
	inboxService := inbox.NewService(inbox.Config{Now: clk.now, MaxPerMinute: 1000})
	adapter := feishu.NewAdapter(inboxService, feishu.Config{
		Token:      testToken,
		EncryptKey: testEncryptKey,
		Now:        clk.now,
	})
	return &replayEnvironment{adapter: adapter, inbox: inboxService, clk: clk}
}

// headers signs a recorded body exactly as the platform would: over the raw
// bytes, with the fixture clock (so the replay window accepts it).
func (r *replayEnvironment) headers(nonce string, body []byte) map[string]string {
	return signedHeaders(r.clk.now().Unix(), nonce, testEncryptKey, body)
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

func (r *replayEnvironment) items(t *testing.T) []contracts.InboxItem {
	t.Helper()
	items, err := r.inbox.List(context.Background(), contracts.InboxFilter{})
	if err != nil {
		t.Fatalf("inbox list: %v", err)
	}
	return items
}

// The documented v2 event: schema 2.0, header + event.message, and content as a
// JSON string that has to be decoded a second time to get the text.
//
// The text keeps the platform's mention placeholder ("@_user_1") verbatim: the
// adapter does not rewrite what the user sent, and resolving the placeholder
// would need the mentions table, which the inbox shape does not carry. Pinned
// here so that behaviour is a decision rather than an accident.
func TestReplayV2MessageEvent(t *testing.T) {
	f := newReplayEnvironment(t)
	body := readFixture(t, "event_v2_message.json")

	item, merged, err := f.adapter.Receive(context.Background(), f.headers("n-replay-1", body), body)
	if err != nil {
		t.Fatalf("Receive(v2 im.message.receive_v1): %v", err)
	}
	if merged {
		t.Fatal("a first arrival was reported as a platform retry")
	}
	if item.Source != contracts.FeishuSource {
		t.Fatalf("source = %q, want %q", item.Source, contracts.FeishuSource)
	}
	if want := "oc_5ce6d572455d361153b7xx51da133945"; item.Channel != want {
		t.Fatalf("channel = %q, want the event's chat_id %q", item.Channel, want)
	}
	if want := "ou_84aad35d084aa403a838cf73ee18467"; item.Sender != want {
		t.Fatalf("sender = %q, want the sender open_id %q", item.Sender, want)
	}
	if want := "feishu:om_5ce6d572455d361153b7cb51da133945"; item.DedupKey != want {
		t.Fatalf("dedup key = %q, want %q", item.DedupKey, want)
	}
	if want := "om_5ce6d572455d361153b7cb51da133945"; item.Ref != want {
		t.Fatalf("ref = %q, want the message id %q", item.Ref, want)
	}
	if want := "@_user_1 帮我看下昨天的退款率"; item.Body != want {
		t.Fatalf("body = %q, want the decoded content.text %q", item.Body, want)
	}
	if item.Title != "@_user_1 帮我看下昨天的退款率" {
		t.Fatalf("title = %q, want the first line of the body", item.Title)
	}
	if item.Count != 1 {
		t.Fatalf("count = %d, want 1", item.Count)
	}

	items := f.items(t)
	if len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("inbox = %+v, want the one returned item", items)
	}
	status, err := f.adapter.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.LastInbound.Equal(f.clk.now()) {
		t.Fatalf("last inbound = %v, want %v", status.LastInbound, f.clk.now())
	}
}

// The documented v1.0 envelope: type=event_callback, uuid/ts/token, and the event
// body *flat* under "event" (msg_type/message_id/open_chat_id/open_id/text as a
// plain string).
//
// KNOWN GAP (reported, not fixed here): parseEvent maps a v1 envelope onto the v2
// header only when the event body already looks like a v2 event (sender.sender_id
// + message.message_id). A real v1 message event therefore fails the identity
// check and is refused as ErrFeishuBadPayload. That is at least the right kind of
// failure — loud and typed, not a silently acknowledged event — but v1 bots are
// not actually supported. If v1 support is wanted, normalise the flat fields into
// the message shape and assert ingestion here instead.
func TestReplayV1FlatEventCallbackIsRefusedAsBadPayload(t *testing.T) {
	f := newReplayEnvironment(t)
	body := readFixture(t, "event_v1_callback_flat.json")

	item, merged, err := f.adapter.Receive(context.Background(), f.headers("n-replay-v1", body), body)
	if !errors.Is(err, contracts.ErrFeishuBadPayload) {
		t.Fatalf("err = %v, want ErrFeishuBadPayload", err)
	}
	if merged || item.ID != "" {
		t.Fatalf("item = %+v (merged %v), want nothing filed", item, merged)
	}
	if items := f.items(t); len(items) != 0 {
		t.Fatalf("inbox = %+v, want no writes", items)
	}
	status, statusErr := f.adapter.Status(context.Background())
	if statusErr != nil {
		t.Fatalf("Status: %v", statusErr)
	}
	if !status.LastInbound.IsZero() {
		t.Fatalf("a refused callback moved last inbound to %v", status.LastInbound)
	}
}

// The documented v2 card callback: the button's action.value is an object holding
// our approval payload. The card callback is authenticated like an event, so the
// request signature is checked before any field is read.
func TestReplayCardActionCallbackWithObjectValue(t *testing.T) {
	f := newReplayEnvironment(t)
	body := readFixture(t, "card_action_v2.json")
	headers := f.headers("n-card-1", body)

	if err := f.adapter.VerifyRequest(headers, body); err != nil {
		t.Fatalf("VerifyRequest(signed card action): %v", err)
	}
	approvalID, decision, ok := f.adapter.CardAction(body)
	if !ok || approvalID != "ap_100" || decision != contracts.FeishuDecisionAllow {
		t.Fatalf("CardAction = (%q, %q, %v), want (ap_100, allow, true)", approvalID, decision, ok)
	}
	approvalID, decision, token, ok := f.adapter.CardActionWithToken(body)
	if !ok || approvalID != "ap_100" || decision != contracts.FeishuDecisionAllow || token != "tok_100" {
		t.Fatalf("CardActionWithToken = (%q, %q, %q, %v)", approvalID, decision, token, ok)
	}
	// Reading a card action must never touch the inbox or the platform.
	if items := f.items(t); len(items) != 0 {
		t.Fatalf("inbox = %+v, want no rows from a card click", items)
	}
}

// The documented legacy card callback: same button semantics, but the value is a
// JSON *string* and there is no per-approval token anywhere in the body — so the
// decision is readable but not actionable (approving needs the one-time token).
func TestReplayLegacyCardActionWithStringValue(t *testing.T) {
	f := newReplayEnvironment(t)
	body := readFixture(t, "card_action_legacy_string.json")
	headers := f.headers("n-card-2", body)

	if err := f.adapter.VerifyRequest(headers, body); err != nil {
		t.Fatalf("VerifyRequest(signed legacy card action): %v", err)
	}
	approvalID, decision, ok := f.adapter.CardAction(body)
	if !ok || approvalID != "ap_legacy_7" || decision != contracts.FeishuDecisionDeny {
		t.Fatalf("CardAction = (%q, %q, %v), want (ap_legacy_7, deny, true)", approvalID, decision, ok)
	}
	if _, _, _, ok := f.adapter.CardActionWithToken(body); ok {
		t.Fatal("a legacy card value without a token was reported as an actionable approval")
	}
}

// The documented url_verification handshake. Receive must report "not a message"
// (zero item, nil error) so the HTTP layer can echo the challenge; writing an
// inbox row for it would put a verification token in the Lead's queue.
func TestReplayURLVerificationHandshake(t *testing.T) {
	f := newReplayEnvironment(t)
	body := readFixture(t, "url_verification.json")

	challenge, ok := f.adapter.VerifyURL(body)
	if !ok || challenge != "ajls384kdjx98XX" {
		t.Fatalf("VerifyURL = (%q, %v), want (ajls384kdjx98XX, true)", challenge, ok)
	}

	item, merged, err := f.adapter.Receive(context.Background(), f.headers("n-challenge", body), body)
	if err != nil {
		t.Fatalf("Receive(challenge): %v", err)
	}
	if merged || item.ID != "" {
		t.Fatalf("item = %+v (merged %v), want nothing filed", item, merged)
	}
	if items := f.items(t); len(items) != 0 {
		t.Fatalf("inbox = %+v, want no writes for a handshake", items)
	}
}

// The documented encrypted push: when an Encrypt Key is configured the platform
// sends {"encrypt": "<base64>"} — for events *and* for the handshake. The
// signature over that wrapper verifies fine (it is over the raw body), so an
// adapter that only checks the signature would treat the body as an unrelated
// event and answer 200 without ever reading the message: a silent black hole.
// It must be a typed refusal instead.
func TestReplayEncryptedPayloadIsRefused(t *testing.T) {
	f := newReplayEnvironment(t)
	body := readFixture(t, "event_encrypted.json")

	// The challenge (if this were a handshake) is inside the ciphertext, so no
	// plaintext challenge can be echoed.
	if _, ok := f.adapter.VerifyURL(body); ok {
		t.Fatal("VerifyURL found a challenge inside an encrypted body")
	}

	t.Run("signed", func(t *testing.T) {
		_, _, err := f.adapter.Receive(context.Background(), f.headers("n-enc-1", body), body)
		if err == nil {
			t.Fatal("an encrypted push was acknowledged silently")
		}
		if !errors.Is(err, contracts.ErrFeishuBadPayload) {
			t.Fatalf("err = %v, want ErrFeishuBadPayload", err)
		}
	})

	t.Run("unsigned", func(t *testing.T) {
		_, _, err := f.adapter.Receive(context.Background(), map[string]string{}, body)
		if !errors.Is(err, contracts.ErrFeishuBadSignature) {
			t.Fatalf("err = %v, want authenticity checked before the envelope", err)
		}
	})

	if items := f.items(t); len(items) != 0 {
		t.Fatalf("inbox = %+v, want no writes for an encrypted body", items)
	}
	status, err := f.adapter.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.LastInbound.IsZero() {
		t.Fatalf("a refused encrypted push moved last inbound to %v", status.LastInbound)
	}
}
