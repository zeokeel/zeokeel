package gateway

// The demotion guard for the channel ingress: DingTalk and Feishu are outbound
// only, so /api/inbound has exactly one shape — the generic webhook source. These
// tests fail the day a channel adapter regains a private path here (which is how
// the earlier dingtalk/feishu ingress and the Feishu card callback were wired),
// not just when the handler compiles differently.

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
	"github.com/zeokeel/zeokeel/internal/modules/webhook"
)

// fakeChannelSender stands in for each demoted adapter: it can send and report
// status, and nothing else — which is the whole point.
type fakeChannelSender struct {
	sends int
}

func (f *fakeChannelSender) Send(context.Context, string, string) error {
	f.sends++
	return nil
}

func (f *fakeChannelSender) Status(context.Context) (contracts.DingTalkStatus, error) {
	return contracts.DingTalkStatus{Connected: true}, nil
}

// fakeFeishuSender is the Feishu half: same narrow surface, a different Status
// return type, so the two fakes cannot be swapped by accident.
type fakeFeishuSender struct {
	sends int
}

func (f *fakeFeishuSender) Send(context.Context, string, string) error {
	f.sends++
	return nil
}

func (f *fakeFeishuSender) Status(context.Context) (contracts.FeishuStatus, error) {
	return contracts.FeishuStatus{Connected: true}, nil
}

func startDemotedChannelGateway(t *testing.T, chanDingTalk contracts.DingTalkService, chanFeishu contracts.FeishuService) *runningGateway {
	t.Helper()
	root := testRoot(t, newFakeChat())
	if chanDingTalk != nil {
		if _, err := root.Provide(contracts.KeyDingTalk, chanDingTalk); err != nil {
			t.Fatalf("provide dingtalk: %v", err)
		}
	}
	if chanFeishu != nil {
		if _, err := root.Provide(contracts.KeyFeishu, chanFeishu); err != nil {
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

// TestDemotedChannelsHaveNoInboundPath pins that both demoted adapters are
// registered (so the assembly still composes them) and unreachable over the
// ingress: the generic webhook source answers, and when no webhook service is
// wired the path is a 404 — never the adapter.
func TestDemotedChannelsHaveNoInboundPath(t *testing.T) {
	dingtalk, feishu := &fakeChannelSender{}, &fakeFeishuSender{}
	gw := startDemotedChannelGateway(t, dingtalk, feishu)
	defer gw.stop(t)

	for _, path := range []string{"/api/inbound/dingtalk", "/api/inbound/feishu"} {
		response := postInbound(t, gw.address, path, `{"msgtype":"text"}`, map[string]string{
			"X-DingTalk-Signature": "abc",
			"X-Lark-Signature":     "abc",
		})
		// No KeyWebhook on this root: the source is unknown, so 404. What matters
		// is that it is the webhook adapter's answer, not a channel one.
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404 (only the generic webhook may answer here)", path, response.StatusCode)
		}
	}
	if dingtalk.sends != 0 || feishu.sends != 0 {
		t.Fatal("an ingress request reached a demoted channel adapter")
	}
}

// TestFeishuCardCallbackRouteIsGone pins that the card-button route was removed,
// not merely left unregistered: nobody may resolve an approval from a webhook
// body any more.
func TestFeishuCardCallbackRouteIsGone(t *testing.T) {
	dingtalk, feishu := &fakeChannelSender{}, &fakeFeishuSender{}
	gw := startDemotedChannelGateway(t, dingtalk, feishu)
	defer gw.stop(t)

	for _, path := range []string{"/api/inbound/feishu/card", "/api/inbound/feishu/card/"} {
		response := postInbound(t, gw.address, path, `{"event":{"action":{"value":{}}}}`, nil)
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404 (the card callback route is retired)", path, response.StatusCode)
		}
	}
	if feishu.sends != 0 {
		t.Fatal("a card callback reached the feishu adapter")
	}
}

// TestAChannelNameCannotBeAWebhookSource closes the seam the demotion left open:
// /api/inbound/<source> accepts any id matching the source pattern, so `dingtalk`
// and `feishu` were still legal *webhook source names*. The HMAC still decided
// whether such a delivery landed, but a configured endpoint named after a
// demoted channel puts the old path back on the map and files rows whose name
// says "a channel" — so the name is now refused, and a sources.json carrying one
// is not loaded either. The path therefore stays the generic adapter's 404 and no
// demoted adapter is consulted, even with the webhook service present.
func TestAChannelNameCannotBeAWebhookSource(t *testing.T) {
	dingtalk, feishu := &fakeChannelSender{}, &fakeFeishuSender{}
	dir := t.TempDir()
	// sources.json is plain product data: write one the way a hand edit would,
	// with a reserved row next to a legitimate one.
	document := `{"version":1,"sources":[` +
		`{"id":"dingtalk","secret":"MDEyMzQ1Njc4OWFiY2RlZg=="},` +
		`{"id":"github","secret":"MDEyMzQ1Njc4OWFiY2RlZg=="}]}`
	if err := os.WriteFile(filepath.Join(dir, "sources.json"), []byte(document), 0o600); err != nil {
		t.Fatalf("write sources.json: %v", err)
	}
	inboxService := inbox.NewService(inbox.Config{Now: time.Now, MaxPerMinute: 1000})
	webhookService, err := webhook.Open(dir, inboxService, webhook.Config{Now: time.Now})
	if err != nil {
		t.Fatalf("webhook.Open: %v", err)
	}

	root := testRoot(t, newFakeChat())
	if _, err := root.Provide(contracts.KeyDingTalk, contracts.DingTalkService(dingtalk)); err != nil {
		t.Fatalf("provide dingtalk: %v", err)
	}
	if _, err := root.Provide(contracts.KeyFeishu, contracts.FeishuService(feishu)); err != nil {
		t.Fatalf("provide feishu: %v", err)
	}
	if _, err := root.Provide(contracts.KeyWebhook, contracts.WebhookService(webhookService)); err != nil {
		t.Fatalf("provide webhook: %v", err)
	}
	if _, err := root.Provide(contracts.KeyInbox, contracts.InboxService(inboxService)); err != nil {
		t.Fatalf("provide inbox: %v", err)
	}
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Provide(root); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	gw := serveGateway(t, gateway)
	defer gw.stop(t)

	// The admin write path refuses the name outright (the UI shows this error).
	if err := webhookService.UpsertSource(context.Background(), contracts.WebhookSource{
		ID: "dingtalk", Secret: []byte("0123456789abcdef"),
	}); !errors.Is(err, contracts.ErrWebhookInvalidSource) {
		t.Fatalf("UpsertSource(dingtalk) err = %v, want ErrWebhookInvalidSource", err)
	}

	for _, path := range []string{"/api/inbound/dingtalk", "/api/inbound/feishu", "/api/inbound/wechat", "/api/inbound/email"} {
		response := postInbound(t, gw.address, path, `{"msgtype":"text"}`, map[string]string{
			"X-DingTalk-Signature":           "abc",
			"X-Lark-Signature":               "abc",
			contracts.WebhookSignatureHeader: "t=1,v1=aa",
		})
		// 404 is the webhook adapter's "unknown source": the reserved row was not
		// loaded, so nothing answers under a channel's name.
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404 (a channel name is not a webhook source)", path, response.StatusCode)
		}
	}
	if dingtalk.sends != 0 || feishu.sends != 0 {
		t.Fatal("an ingress request reached a demoted channel adapter")
	}

	// The generic source in the same file still answers, so the 404s above mean
	// "unknown source", not "no webhook service": a bad signature is a 401.
	response := postInbound(t, gw.address, "/api/inbound/github", `{"title":"x"}`, map[string]string{
		contracts.WebhookSignatureHeader: "t=1,v1=aa",
	})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("github status = %d, want 401 (the source exists; only the signature is wrong)", response.StatusCode)
	}
}
