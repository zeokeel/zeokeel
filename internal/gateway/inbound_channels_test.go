package gateway

// The other two channel adapters get their own ingress paths. Feishu is special:
// its URL handshake must answer 200 with the challenge (a 202 would fail the
// platform's verification), so the handler asks the adapter before treating the
// body as a message.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakeDingTalk struct {
	seen struct {
		headers map[string]string
		body    string
	}
	err error
}

func (f *fakeDingTalk) Receive(_ context.Context, headers map[string]string, body []byte) (contracts.InboxItem, bool, error) {
	f.seen.headers = headers
	f.seen.body = string(body)
	if f.err != nil {
		return contracts.InboxItem{}, false, f.err
	}
	return contracts.InboxItem{ID: "inbox-dt", Source: "dingtalk", Title: "dt", Status: contracts.InboxStatusNew}, false, nil
}

func (f *fakeDingTalk) Send(context.Context, string, string) error { return nil }

func (f *fakeDingTalk) Status(context.Context) (contracts.DingTalkStatus, error) {
	return contracts.DingTalkStatus{Connected: true}, nil
}

type fakeFeishu struct {
	received int
	err      error
}

func (f *fakeFeishu) VerifyURL(body []byte) (string, bool) {
	var payload struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Type != "url_verification" {
		return "", false
	}
	return payload.Challenge, true
}

func (f *fakeFeishu) Receive(_ context.Context, _ map[string]string, body []byte) (contracts.InboxItem, bool, error) {
	f.received++
	if f.err != nil {
		return contracts.InboxItem{}, false, f.err
	}
	return contracts.InboxItem{ID: "inbox-fs", Source: "feishu", Title: "fs", Status: contracts.InboxStatusNew}, false, nil
}

func (f *fakeFeishu) CardAction([]byte) (string, string, bool) { return "", "", false }

func (f *fakeFeishu) Send(context.Context, string, string) error { return nil }

func (f *fakeFeishu) Status(context.Context) (contracts.FeishuStatus, error) {
	return contracts.FeishuStatus{Connected: true}, nil
}

func startChannelGateway(t *testing.T, dingtalk contracts.DingTalkService, feishu contracts.FeishuService) *runningGateway {
	t.Helper()
	root := testRoot(t, newFakeChat())
	if dingtalk != nil {
		if _, err := root.Provide(contracts.KeyDingTalk, dingtalk); err != nil {
			t.Fatalf("provide dingtalk: %v", err)
		}
	}
	if feishu != nil {
		if _, err := root.Provide(contracts.KeyFeishu, feishu); err != nil {
			t.Fatalf("provide feishu: %v", err)
		}
	}
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Provide(root); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	return serveGateway(t, gateway)
}

func TestDingTalkIngressAcceptsAndForwardsHeaders(t *testing.T) {
	dingtalk := &fakeDingTalk{}
	gw := startChannelGateway(t, dingtalk, nil)
	defer gw.stop(t)

	response := postInbound(t, gw.address, "/api/inbound/dingtalk", `{"msgtype":"text"}`, map[string]string{
		"X-DingTalk-Signature": "abc",
		"X-DingTalk-Timestamp": "1700000000",
	})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", response.StatusCode)
	}
	if dingtalk.seen.body != `{"msgtype":"text"}` {
		t.Fatalf("adapter saw body %q", dingtalk.seen.body)
	}
	if !headerPresent(dingtalk.seen.headers, "X-DingTalk-Signature") {
		t.Fatalf("adapter did not receive the signature header: %v", dingtalk.seen.headers)
	}
}

func TestDingTalkIngressMapsBadSignature(t *testing.T) {
	dingtalk := &fakeDingTalk{err: contracts.ErrDingTalkBadSignature}
	gw := startChannelGateway(t, dingtalk, nil)
	defer gw.stop(t)
	if response := postInbound(t, gw.address, "/api/inbound/dingtalk", `{}`, nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.StatusCode)
	}
}

func TestFeishuChallengeAnswers200WithChallenge(t *testing.T) {
	feishu := &fakeFeishu{}
	gw := startChannelGateway(t, nil, feishu)
	defer gw.stop(t)

	response := postInbound(t, gw.address, "/api/inbound/feishu", `{"type":"url_verification","challenge":"ch-42"}`, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the platform rejects a challenge answered with 202)", response.StatusCode)
	}
	var payload struct {
		Challenge string `json:"challenge"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil || payload.Challenge != "ch-42" {
		t.Fatalf("challenge payload = (%+v, %v)", payload, err)
	}
	if feishu.received != 0 {
		t.Fatal("a handshake reached the message path")
	}
}

func TestFeishuMessageIngressAccepts(t *testing.T) {
	feishu := &fakeFeishu{}
	gw := startChannelGateway(t, nil, feishu)
	defer gw.stop(t)

	response := postInbound(t, gw.address, "/api/inbound/feishu", `{"schema":"2.0"}`, map[string]string{
		"X-Lark-Signature": "sig",
	})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", response.StatusCode)
	}
	if feishu.received != 1 {
		t.Fatalf("adapter received %d messages, want 1", feishu.received)
	}
}

func TestFeishuStaleMapsTo401(t *testing.T) {
	feishu := &fakeFeishu{err: contracts.ErrFeishuStale}
	gw := startChannelGateway(t, nil, feishu)
	defer gw.stop(t)
	if response := postInbound(t, gw.address, "/api/inbound/feishu", `{}`, nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.StatusCode)
	}
}

func TestChannelIngressMissingServiceIs404(t *testing.T) {
	gw := startChannelGateway(t, nil, nil)
	defer gw.stop(t)
	for _, path := range []string{"/api/inbound/dingtalk", "/api/inbound/feishu"} {
		if response := postInbound(t, gw.address, path, `{}`, nil); response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, response.StatusCode)
		}
	}
}

func headerPresent(headers map[string]string, want string) bool {
	for key := range headers {
		if strings.EqualFold(key, want) {
			return true
		}
	}
	return false
}

var _ = errors.Is
var _ = time.Now
