package dingtalk_test

// TDD for the DingTalk chat-bot adapter. The two things that go wrong in
// production are pinned here: an unsigned or replayed callback must never reach
// the inbox (it is a public HTTP endpoint), and the outbound body must carry the
// exact field set the robot API expects — a wrong shape is a silent no-op on the
// platform side.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
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
	"github.com/zeokeel/zeokeel/internal/modules/dingtalk"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
)

const testSecret = "app-secret"

// sign reproduces DingTalk's documented rule:
// stringToSign = timestamp + "\n" + secret, sign = base64(HMAC-SHA256(secret, stringToSign)).
func sign(secret, timestamp string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "\n" + secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

// fakeRobot is the outbound half of the platform.
type fakeRobot struct {
	mu       sync.Mutex
	requests []capturedRequest
	status   int
}

type capturedRequest struct {
	method string
	path   string
	header http.Header
	body   map[string]any
}

func (f *fakeRobot) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		captured := capturedRequest{method: r.Method, path: r.URL.Path, header: r.Header.Clone()}
		_ = json.NewDecoder(r.Body).Decode(&captured.body)
		f.mu.Lock()
		f.requests = append(f.requests, captured)
		status := f.status
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"processQueryKey": "pq-1"})
	})
	return mux
}

func (f *fakeRobot) last() *capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return nil
	}
	return &f.requests[len(f.requests)-1]
}

func (f *fakeRobot) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func newFixture(t *testing.T) (*dingtalk.Adapter, *fakeRobot, contracts.InboxService, *clock) {
	t.Helper()
	robot := &fakeRobot{}
	httpServer := httptest.NewServer(robot.handler())
	t.Cleanup(httpServer.Close)
	clk := &clock{t: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
	inboxSvc := inbox.NewService(inbox.Config{Now: clk.now, MaxPerMinute: 1000})
	adapter := dingtalk.NewAdapter(inboxSvc, dingtalk.Config{
		BaseURL:    httpServer.URL,
		Token:      "access-token",
		SignSecret: testSecret,
		RobotCode:  "dingrobot1",
		Now:        clk.now,
	})
	return adapter, robot, inboxSvc, clk
}

// callback builds a chat-bot text callback plus its signed headers.
func callback(t *testing.T, clk *clock, msgID, conversationID, staffID, msgType, text string) (map[string]string, []byte) {
	t.Helper()
	payload := map[string]any{
		"msgtype":          msgType,
		"msgId":            msgID,
		"createAt":         clk.now().UnixMilli(),
		"conversationId":   conversationID,
		"conversationType": "1",
		"senderNick":       "张三",
		"senderStaffId":    staffID,
		"text":             map[string]any{"content": text},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal callback: %v", err)
	}
	timestamp := strconv.FormatInt(clk.now().Unix(), 10)
	headers := map[string]string{
		dingtalk.HeaderSignature: sign(testSecret, timestamp),
		dingtalk.HeaderTimestamp: timestamp,
	}
	return headers, body
}

func TestReceiveIngestsSignedTextCallback(t *testing.T) {
	adapter, _, inboxSvc, clk := newFixture(t)
	headers, body := callback(t, clk, "msg-1", "cid-1", "staff-1", "text", "  帮我看下昨天的退款率  ")

	item, merged, err := adapter.Receive(context.Background(), headers, body)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if merged {
		t.Fatal("first arrival reported as merged")
	}
	if item.Source != "dingtalk" {
		t.Fatalf("source = %q, want dingtalk", item.Source)
	}
	if item.Channel != "cid-1" || item.Sender != "staff-1" {
		t.Fatalf("item = %+v, want channel cid-1 sender staff-1", item)
	}
	if item.DedupKey != "dingtalk:msg-1" {
		t.Fatalf("dedup key = %q", item.DedupKey)
	}
	if item.Body != "帮我看下昨天的退款率" {
		t.Fatalf("body = %q, want the trimmed text", item.Body)
	}
	if !strings.Contains(item.Title, "退款率") {
		t.Fatalf("title = %q", item.Title)
	}

	items, err := inboxSvc.List(context.Background(), contracts.InboxFilter{})
	if err != nil || len(items) != 1 {
		t.Fatalf("inbox = (%v, %v), want one item", items, err)
	}

	status, err := adapter.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Connected {
		t.Fatal("status disconnected with a configured token")
	}
	if !status.LastInbound.Equal(clk.now()) {
		t.Fatalf("last inbound = %v, want %v", status.LastInbound, clk.now())
	}
}

func TestReceiveRejectsBadSignature(t *testing.T) {
	adapter, _, inboxSvc, clk := newFixture(t)
	headers, body := callback(t, clk, "msg-1", "cid-1", "staff-1", "text", "hi")
	headers[dingtalk.HeaderSignature] = sign("wrong-secret", headers[dingtalk.HeaderTimestamp])

	if _, _, err := adapter.Receive(context.Background(), headers, body); !errors.Is(err, contracts.ErrDingTalkBadSignature) {
		t.Fatalf("err = %v, want ErrDingTalkBadSignature", err)
	}
	assertInboxEmpty(t, inboxSvc)
}

func TestReceiveRejectsMissingSignatureHeaders(t *testing.T) {
	adapter, _, inboxSvc, clk := newFixture(t)
	_, body := callback(t, clk, "msg-1", "cid-1", "staff-1", "text", "hi")

	for name, headers := range map[string]map[string]string{
		"no signature": {dingtalk.HeaderTimestamp: strconv.FormatInt(clk.now().Unix(), 10)},
		"no timestamp": {dingtalk.HeaderSignature: "AAAA"},
		"empty map":    {},
	} {
		if _, _, err := adapter.Receive(context.Background(), headers, body); !errors.Is(err, contracts.ErrDingTalkBadSignature) {
			t.Fatalf("%s: err = %v, want ErrDingTalkBadSignature", name, err)
		}
	}
	assertInboxEmpty(t, inboxSvc)
}

func TestReceiveRejectsMissingSecret(t *testing.T) {
	robot := &fakeRobot{}
	httpServer := httptest.NewServer(robot.handler())
	t.Cleanup(httpServer.Close)
	adapter := dingtalk.NewAdapter(inbox.NewService(inbox.Config{MaxPerMinute: 1000}), dingtalk.Config{
		BaseURL: httpServer.URL, Token: "access-token", // no SignSecret
	})
	clk := &clock{t: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
	headers, body := callback(t, clk, "msg-1", "cid-1", "staff-1", "text", "hi")

	if _, _, err := adapter.Receive(context.Background(), headers, body); !errors.Is(err, contracts.ErrDingTalkBadSignature) {
		t.Fatalf("err = %v, want ErrDingTalkBadSignature without a secret", err)
	}
}

func TestReceiveRejectsStaleAndFutureTimestamps(t *testing.T) {
	for name, offset := range map[string]time.Duration{
		"stale":          -2 * time.Hour,
		"future":         2 * time.Hour,
		"just past skew": -(contracts.DingTalkMaxSkew + time.Minute),
	} {
		t.Run(name, func(t *testing.T) {
			adapter, _, inboxSvc, clk := newFixture(t)
			stamp := strconv.FormatInt(clk.now().Add(offset).Unix(), 10)
			body := []byte(`{"msgtype":"text","msgId":"m1","conversationId":"cid-1","senderStaffId":"staff-1","text":{"content":"hi"}}`)
			headers := map[string]string{
				dingtalk.HeaderSignature: sign(testSecret, stamp),
				dingtalk.HeaderTimestamp: stamp,
			}
			if _, _, err := adapter.Receive(context.Background(), headers, body); !errors.Is(err, contracts.ErrDingTalkStaleTimestamp) {
				t.Fatalf("err = %v, want ErrDingTalkStaleTimestamp", err)
			}
			assertInboxEmpty(t, inboxSvc)
			status, _ := adapter.Status(context.Background())
			if !status.LastInbound.IsZero() {
				t.Fatalf("a rejected callback moved last inbound to %v", status.LastInbound)
			}
		})
	}
}

func TestReceiveAcceptsTimestampInsideTheSkew(t *testing.T) {
	adapter, _, _, clk := newFixture(t)
	stamp := strconv.FormatInt(clk.now().Add(-30*time.Minute).Unix(), 10)
	body := []byte(`{"msgtype":"text","msgId":"m1","conversationId":"cid-1","senderStaffId":"staff-1","text":{"content":"hi"}}`)
	headers := map[string]string{
		dingtalk.HeaderSignature: sign(testSecret, stamp),
		dingtalk.HeaderTimestamp: stamp,
	}
	if _, _, err := adapter.Receive(context.Background(), headers, body); err != nil {
		t.Fatalf("Receive inside skew: %v", err)
	}
}

func TestReceiveDuplicateMsgIDMerges(t *testing.T) {
	adapter, _, inboxSvc, clk := newFixture(t)
	headers, body := callback(t, clk, "msg-1", "cid-1", "staff-1", "text", "again")

	first, merged, err := adapter.Receive(context.Background(), headers, body)
	if err != nil || merged {
		t.Fatalf("first: (%+v, %v, %v)", first, merged, err)
	}
	clk.advance(time.Minute)
	second, merged, err := adapter.Receive(context.Background(), headers, body)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !merged {
		t.Fatal("platform retry reported as a new item")
	}
	if second.ID != first.ID {
		t.Fatalf("retry id = %q, want %q", second.ID, first.ID)
	}

	items, _ := inboxSvc.List(context.Background(), contracts.InboxFilter{})
	if len(items) != 1 || items[0].Count != 2 {
		t.Fatalf("items = %+v, want one with count 2", items)
	}
}

func TestReceiveRejectsNonTextMessage(t *testing.T) {
	adapter, _, inboxSvc, clk := newFixture(t)
	headers, body := callback(t, clk, "msg-2", "cid-1", "staff-1", "picture", "")

	if _, _, err := adapter.Receive(context.Background(), headers, body); !errors.Is(err, contracts.ErrDingTalkUnsupportedMessage) {
		t.Fatalf("err = %v, want ErrDingTalkUnsupportedMessage", err)
	}
	assertInboxEmpty(t, inboxSvc)
}

func TestReceiveRejectsMalformedBody(t *testing.T) {
	adapter, _, inboxSvc, clk := newFixture(t)
	stamp := strconv.FormatInt(clk.now().Unix(), 10)
	headers := map[string]string{
		dingtalk.HeaderSignature: sign(testSecret, stamp),
		dingtalk.HeaderTimestamp: stamp,
	}

	for name, body := range map[string][]byte{
		"not json":   []byte("{"),
		"no msg id":  []byte(`{"msgtype":"text","conversationId":"cid-1","senderStaffId":"s","text":{"content":"hi"}}`),
		"no content": []byte(`{"msgtype":"text","msgId":"m1","conversationId":"cid-1","senderStaffId":"s"}`),
		"blank text": []byte(`{"msgtype":"text","msgId":"m1","conversationId":"cid-1","senderStaffId":"s","text":{"content":"  "}}`),
	} {
		if _, _, err := adapter.Receive(context.Background(), headers, body); !errors.Is(err, contracts.ErrDingTalkInvalid) {
			t.Fatalf("%s: err = %v, want ErrDingTalkInvalid", name, err)
		}
	}
	assertInboxEmpty(t, inboxSvc)
}

func TestSendPostsTheDocumentedBody(t *testing.T) {
	adapter, robot, _, _ := newFixture(t)

	if err := adapter.Send(context.Background(), "cid-1", "收到，我在查"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	captured := robot.last()
	if captured == nil {
		t.Fatal("no request reached the robot API")
	}
	if captured.method != http.MethodPost {
		t.Fatalf("method = %s", captured.method)
	}
	if captured.path != "/v1.0/robot/groupMessages/send" {
		t.Fatalf("path = %s", captured.path)
	}
	if got := captured.header.Get("Authorization"); got != "Bearer access-token" {
		t.Fatalf("authorization = %q", got)
	}
	if got := captured.header.Get("x-acs-dingtalk-access-token"); got != "access-token" {
		t.Fatalf("access token header = %q", got)
	}
	if captured.body["robotCode"] != "dingrobot1" {
		t.Fatalf("robotCode = %v", captured.body["robotCode"])
	}
	if captured.body["openConversationId"] != "cid-1" {
		t.Fatalf("openConversationId = %v", captured.body["openConversationId"])
	}
	if captured.body["msgKey"] != "sampleText" {
		t.Fatalf("msgKey = %v", captured.body["msgKey"])
	}
	param, ok := captured.body["msgParam"].(string)
	if !ok {
		t.Fatalf("msgParam = %v, want a JSON string", captured.body["msgParam"])
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(param), &decoded); err != nil {
		t.Fatalf("msgParam is not JSON: %v (%s)", err, param)
	}
	if decoded["content"] != "收到，我在查" {
		t.Fatalf("msgParam content = %v", decoded["content"])
	}
}

func TestSendUnauthorized(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		adapter, robot, _, _ := newFixture(t)
		robot.status = status
		if err := adapter.Send(context.Background(), "cid-1", "hi"); !errors.Is(err, contracts.ErrDingTalkUnauthorized) {
			t.Fatalf("status %d: err = %v, want ErrDingTalkUnauthorized", status, err)
		}
	}
}

func TestSendUnavailable(t *testing.T) {
	adapter, robot, _, _ := newFixture(t)
	robot.status = http.StatusInternalServerError
	if err := adapter.Send(context.Background(), "cid-1", "hi"); !errors.Is(err, contracts.ErrDingTalkUnavailable) {
		t.Fatalf("err = %v, want ErrDingTalkUnavailable", err)
	}

	// A network failure is "unavailable", not a crash or a silent success.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	offline := dingtalk.NewAdapter(inbox.NewService(inbox.Config{MaxPerMinute: 1000}), dingtalk.Config{
		BaseURL: deadURL, Token: "access-token", SignSecret: testSecret, RobotCode: "dingrobot1",
	})
	if err := offline.Send(context.Background(), "cid-1", "hi"); !errors.Is(err, contracts.ErrDingTalkUnavailable) {
		t.Fatalf("offline err = %v, want ErrDingTalkUnavailable", err)
	}
}

func TestSendRefusesWithoutTokenOrText(t *testing.T) {
	robot := &fakeRobot{}
	httpServer := httptest.NewServer(robot.handler())
	t.Cleanup(httpServer.Close)
	noToken := dingtalk.NewAdapter(inbox.NewService(inbox.Config{MaxPerMinute: 1000}), dingtalk.Config{
		BaseURL: httpServer.URL, SignSecret: testSecret,
	})
	if err := noToken.Send(context.Background(), "cid-1", "hi"); !errors.Is(err, contracts.ErrDingTalkUnauthorized) {
		t.Fatalf("err = %v, want ErrDingTalkUnauthorized", err)
	}
	if robot.count() != 0 {
		t.Fatal("a send without a token still hit the network")
	}

	adapter, robot2, _, _ := newFixture(t)
	if err := adapter.Send(context.Background(), "cid-1", "   "); !errors.Is(err, contracts.ErrDingTalkEmptyMessage) {
		t.Fatalf("err = %v, want ErrDingTalkEmptyMessage", err)
	}
	if err := adapter.Send(context.Background(), "", "hi"); !errors.Is(err, contracts.ErrDingTalkInvalid) {
		t.Fatalf("err = %v, want ErrDingTalkInvalid for a missing chat id", err)
	}
	if robot2.count() != 0 {
		t.Fatal("a refused send still hit the network")
	}
}

func TestStatusBeforeAnyInbound(t *testing.T) {
	adapter, _, _, _ := newFixture(t)
	status, err := adapter.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Connected || !status.LastInbound.IsZero() {
		t.Fatalf("status = %+v, want connected with no inbound yet", status)
	}

	offline := dingtalk.NewAdapter(inbox.NewService(inbox.Config{}), dingtalk.Config{SignSecret: testSecret})
	disconnected, err := offline.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if disconnected.Connected {
		t.Fatal("reported connected without an access token")
	}
}

func assertInboxEmpty(t *testing.T, inboxSvc contracts.InboxService) {
	t.Helper()
	items, err := inboxSvc.List(context.Background(), contracts.InboxFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("inbox = %+v, want nothing to reach it", items)
	}
}
