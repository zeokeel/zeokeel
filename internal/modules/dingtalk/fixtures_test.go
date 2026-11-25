package dingtalk_test

// Platform-shaped replay fixtures for the DingTalk chat-bot callback.
//
// The bodies in testdata/ are transcribed from the official 接收消息的消息协议
// page (https://open.dingtalk.com/document/robots/receive-message), not invented
// here, because the shape *is* the contract: a field the adapter drops is an
// inbox row with the wrong sender, and a header the adapter cannot read is every
// callback in production refused. testdata/README.md lists each fixture with the
// outcome it pins.
//
// Each case below asserts the typed outcome — the exact ingested item fields, or
// one specific typed error together with "nothing reached the inbox". "It did not
// panic" is not an assertion.
//
// The signing rule is the documented one (adapter_test.go owns the helper):
//
//	stringToSign = timestamp + "\n" + appSecret       (timestamp in milliseconds)
//	sign         = base64(HMAC-SHA256(appSecret, stringToSign))

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/dingtalk"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
)

// readFixture loads a recorded callback body. The bytes are replayed verbatim:
// the signature covers the body only through the timestamp here, but an adapter
// that decodes and re-encodes would still lose the exact shape these files exist
// to preserve.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

// documentedHeaders builds the two headers the platform's HTTP-mode callback
// documents: `timestamp` in unix milliseconds and `sign`.
func documentedHeaders(secret string, at time.Time) map[string]string {
	timestamp := strconv.FormatInt(at.UnixMilli(), 10)
	return map[string]string{
		"timestamp": timestamp,
		"sign":      sign(secret, timestamp),
	}
}

// newReplayAdapter wires an adapter with the fixture clock and a permissive
// inbox, so a test fails on the adapter's decision rather than on rate limits.
func newReplayAdapter(t *testing.T) (*dingtalk.Adapter, contracts.InboxService, *clock) {
	t.Helper()
	clk := &clock{t: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
	inboxService := inbox.NewService(inbox.Config{Now: clk.now, MaxPerMinute: 1000})
	adapter := dingtalk.NewAdapter(inboxService, dingtalk.Config{
		Token:      "access-token",
		SignSecret: testSecret,
		RobotCode:  "dingohropvkloy0xdm3k",
		Now:        clk.now,
	})
	return adapter, inboxService, clk
}

func assertInboxHasOne(t *testing.T, inboxService contracts.InboxService) contracts.InboxItem {
	t.Helper()
	items, err := inboxService.List(context.Background(), contracts.InboxFilter{})
	if err != nil {
		t.Fatalf("inbox list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("inbox = %+v, want exactly one item", items)
	}
	return items[0]
}

// The documented group text callback. This is the shape the platform actually
// posts: the message id, the conversation id and the sender's staff id are the
// only identity fields, and `text.content` arrives with a leading space.
func TestReplayDocumentedGroupTextCallback(t *testing.T) {
	adapter, inboxService, clk := newReplayAdapter(t)
	body := readFixture(t, "text_callback_group.json")

	item, merged, err := adapter.Receive(context.Background(), documentedHeaders(testSecret, clk.now()), body)
	if err != nil {
		t.Fatalf("Receive(documented group text): %v", err)
	}
	if merged {
		t.Fatal("a first arrival was reported as a platform retry")
	}
	if item.Source != "dingtalk" {
		t.Fatalf("source = %q, want dingtalk", item.Source)
	}
	if want := "cid6EUvB2O8qVF2RYQtHTKEsg=="; item.Channel != want {
		t.Fatalf("channel = %q, want the callback's conversationId %q", item.Channel, want)
	}
	if want := "014728255240768602"; item.Sender != want {
		t.Fatalf("sender = %q, want the callback's senderStaffId %q", item.Sender, want)
	}
	if want := "dingtalk:msgrK2ckHkMbaiUWt5xQU+riw=="; item.DedupKey != want {
		t.Fatalf("dedup key = %q, want %q", item.DedupKey, want)
	}
	// text.content is " 帮我看下昨天的退款率" (leading space, as the doc shows).
	if want := "帮我看下昨天的退款率"; item.Body != want {
		t.Fatalf("body = %q, want %q", item.Body, want)
	}
	if item.Title != "帮我看下昨天的退款率" {
		t.Fatalf("title = %q, want the trimmed single-line text", item.Title)
	}
	if item.Ref != "" {
		t.Fatalf("ref = %q, want empty: a text callback carries no message permalink to jump back to", item.Ref)
	}

	stored := assertInboxHasOne(t, inboxService)
	if stored.ID != item.ID || stored.Body != item.Body || stored.Count != 1 {
		t.Fatalf("stored = %+v, want the returned item once", stored)
	}

	status, err := adapter.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.LastInbound.Equal(clk.now()) {
		t.Fatalf("last inbound = %v, want %v (a verified callback was ingested)", status.LastInbound, clk.now())
	}
}

// The documented 1:1 callback. The platform marks the conversation with
// conversationType "1" (group: "2") and omits conversationTitle/atUsers.
//
// What differs in Channel: nothing. The adapter addresses the inbox by
// conversationId in both cases, because that id already identifies the chat —
// the group/1:1 distinction is platform metadata the inbox never needs. This
// test pins that (so a future change that starts branching on conversationType
// has to change the fixture's expectation too).
func TestReplaySingleChatCallbackKeepsConversationIDAsChannel(t *testing.T) {
	adapter, inboxService, clk := newReplayAdapter(t)
	body := readFixture(t, "text_callback_single.json")

	item, merged, err := adapter.Receive(context.Background(), documentedHeaders(testSecret, clk.now()), body)
	if err != nil {
		t.Fatalf("Receive(documented 1:1 text): %v", err)
	}
	if merged {
		t.Fatal("a first arrival was reported as a platform retry")
	}
	if want := "cidjMOsz******AhS4Jg=="; item.Channel != want {
		t.Fatalf("channel = %q, want the 1:1 conversationId %q", item.Channel, want)
	}
	if want := "manager5678"; item.Sender != want {
		t.Fatalf("sender = %q, want %q", item.Sender, want)
	}
	if want := "dingtalk:msgm3Xk1BcpAoFnR7yT6hV9Q=="; item.DedupKey != want {
		t.Fatalf("dedup key = %q, want %q", item.DedupKey, want)
	}
	if want := "在吗？帮我看下审批"; item.Body != want {
		t.Fatalf("body = %q, want %q", item.Body, want)
	}
	// conversationTitle ("测试群" in the group fixture) never becomes the channel
	// or leaks into the text.
	if strings.Contains(item.Channel, "测试群") || strings.Contains(item.Body, "测试群") {
		t.Fatalf("item = %+v, want conversationTitle to stay platform metadata", item)
	}
	assertInboxHasOne(t, inboxService)
}

// sessionWebhook / sessionWebhookExpiredTime / robotCode / timeout are reply
// plumbing. They must be dropped, not folded into the body: a webhook URL pasted
// into the inbox text is a credential leak with a chat message around it.
func TestReplaySessionFieldsStayOutOfTheBody(t *testing.T) {
	adapter, inboxService, clk := newReplayAdapter(t)
	body := readFixture(t, "text_with_session_fields.json")

	item, merged, err := adapter.Receive(context.Background(), documentedHeaders(testSecret, clk.now()), body)
	if err != nil {
		t.Fatalf("Receive(text + session fields): %v", err)
	}
	if merged {
		t.Fatal("a first arrival was reported as a platform retry")
	}
	if want := "好的，谢谢"; item.Body != want {
		t.Fatalf("body = %q, want exactly the text content %q", item.Body, want)
	}
	if item.Title != "好的，谢谢" {
		t.Fatalf("title = %q", item.Title)
	}
	for name, forbidden := range map[string]string{
		"session webhook":    "sendBySession",
		"webhook host":       "oapi.dingtalk.com",
		"robot code":         "dingohropvkloy0xdm3k",
		"timeout value":      "3000",
		"conversation title": "值班群",
	} {
		if strings.Contains(item.Body, forbidden) || strings.Contains(item.Title, forbidden) {
			t.Fatalf("body/title contain the %s (%q): body=%q title=%q", name, forbidden, item.Body, item.Title)
		}
	}
	if item.Channel != "cid7a6b5c4d3e2f1a==" || item.Sender != "staff0001" {
		t.Fatalf("channel/sender = (%q, %q)", item.Channel, item.Sender)
	}
	assertInboxHasOne(t, inboxService)
}

// A markdown body must be refused, not ingested as if it were text. (The
// documented inbound types are text/richText/picture/audio/video/file, so
// markdown is defensive; richText right below is the real documented neighbour —
// both take the same "unsupported msgtype" branch.)
func TestReplayMarkdownIsRefusedAsUnsupported(t *testing.T) {
	adapter, inboxService, clk := newReplayAdapter(t)
	body := readFixture(t, "markdown_callback.json")

	_, _, err := adapter.Receive(context.Background(), documentedHeaders(testSecret, clk.now()), body)
	if !errors.Is(err, contracts.ErrDingTalkUnsupportedMessage) {
		t.Fatalf("err = %v, want ErrDingTalkUnsupportedMessage", err)
	}
	if !strings.Contains(err.Error(), "markdown") {
		t.Fatalf("err = %v, want it to name the refused msgtype", err)
	}
	assertInboxEmpty(t, inboxService)
}

// richText is a documented inbound type whose payload nests the text under
// content.richText — the adapter has no text field to read, so the important
// part is that it says "unsupported" rather than "invalid/empty".
func TestReplayRichTextIsRefusedAsUnsupported(t *testing.T) {
	adapter, inboxService, clk := newReplayAdapter(t)
	body := readFixture(t, "richtext_callback.json")

	_, _, err := adapter.Receive(context.Background(), documentedHeaders(testSecret, clk.now()), body)
	if !errors.Is(err, contracts.ErrDingTalkUnsupportedMessage) {
		t.Fatalf("err = %v, want ErrDingTalkUnsupportedMessage", err)
	}
	if !strings.Contains(err.Error(), "richText") {
		t.Fatalf("err = %v, want it to name the refused msgtype", err)
	}
	assertInboxEmpty(t, inboxService)
}

// Missing-field variants. Both are well-formed JSON that the adapter must refuse
// with the *invalid* error (not unsupported, not a guess):
//
//   - no msgId: there is no dedupe identity, so a platform retry would duplicate.
//   - no senderStaffId: the doc says this field is only returned once the robot is
//     published, so an unpublished bot (or an external sender) produces exactly
//     this body. The adapter refuses instead of filing the encrypted senderId as
//     the owner. That is a deliberate limitation, pinned here so it stays visible.
func TestReplayMissingIdentityFieldsAreInvalid(t *testing.T) {
	for _, testCase := range []struct {
		fixture string
		why     string
	}{
		{"text_missing_msg_id.json", "no dedupe identity"},
		{"text_missing_staff_id.json", "no staff id to bind the sender to"},
	} {
		t.Run(testCase.fixture, func(t *testing.T) {
			adapter, inboxService, clk := newReplayAdapter(t)
			body := readFixture(t, testCase.fixture)

			_, _, err := adapter.Receive(context.Background(), documentedHeaders(testSecret, clk.now()), body)
			if !errors.Is(err, contracts.ErrDingTalkInvalid) {
				t.Fatalf("err = %v, want ErrDingTalkInvalid (%s)", err, testCase.why)
			}
			assertInboxEmpty(t, inboxService)
			status, statusErr := adapter.Status(context.Background())
			if statusErr != nil {
				t.Fatalf("Status: %v", statusErr)
			}
			if !status.LastInbound.IsZero() {
				t.Fatalf("a refused callback moved last inbound to %v", status.LastInbound)
			}
		})
	}
}

// The documented header spelling (`timestamp`/`sign`, milliseconds) and the
// local X-DingTalk-* spelling (seconds, used by scripts/channel-verify.sh and the
// gateway passthrough test) both have to verify. The HMAC is computed over the
// timestamp string exactly as it arrived, so the unit is never attacker-chosen:
// signing a seconds value while sending a milliseconds header must fail.
func TestReplaySignatureHeaderSpellingsAndUnits(t *testing.T) {
	body := readFixture(t, "text_callback_group.json")

	t.Run("documented milliseconds", func(t *testing.T) {
		adapter, _, clk := newReplayAdapter(t)
		if _, _, err := adapter.Receive(context.Background(), documentedHeaders(testSecret, clk.now()), body); err != nil {
			t.Fatalf("documented headers: %v", err)
		}
	})

	t.Run("local seconds", func(t *testing.T) {
		adapter, _, clk := newReplayAdapter(t)
		seconds := strconv.FormatInt(clk.now().Unix(), 10)
		headers := map[string]string{
			"X-DingTalk-Timestamp": seconds,
			"X-DingTalk-Signature": sign(testSecret, seconds),
		}
		if _, _, err := adapter.Receive(context.Background(), headers, body); err != nil {
			t.Fatalf("local headers: %v", err)
		}
	})

	t.Run("milliseconds header must not be signed as seconds", func(t *testing.T) {
		adapter, inboxService, clk := newReplayAdapter(t)
		headers := documentedHeaders(testSecret, clk.now())
		headers["sign"] = sign(testSecret, strconv.FormatInt(clk.now().Unix(), 10))
		if _, _, err := adapter.Receive(context.Background(), headers, body); !errors.Is(err, contracts.ErrDingTalkBadSignature) {
			t.Fatalf("err = %v, want ErrDingTalkBadSignature", err)
		}
		assertInboxEmpty(t, inboxService)
	})

	t.Run("milliseconds outside the skew are a replay", func(t *testing.T) {
		adapter, inboxService, clk := newReplayAdapter(t)
		stale := documentedHeaders(testSecret, clk.now().Add(-2*time.Hour))
		if _, _, err := adapter.Receive(context.Background(), stale, body); !errors.Is(err, contracts.ErrDingTalkStaleTimestamp) {
			t.Fatalf("err = %v, want ErrDingTalkStaleTimestamp", err)
		}
		assertInboxEmpty(t, inboxService)
	})
}
