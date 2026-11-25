package gateway

// The inbound endpoint is the one HTTP surface that authenticates by signature
// instead of the browser token, so its refusals matter as much as its success:
// no browser origin, no token, no guessing an unknown source, and a hard body
// cap before any parsing.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakeWebhook struct {
	// reply maps a source id to the result the fake returns; err is returned
	// for that source instead when set.
	errs map[string]error
	// seen records the last delivery.
	seen struct {
		source string
		header string
		body   string
	}
}

func (f *fakeWebhook) Receive(_ context.Context, sourceID, header string, body []byte) (contracts.WebhookResult, error) {
	f.seen.source = sourceID
	f.seen.header = header
	f.seen.body = string(body)
	if err, ok := f.errs[sourceID]; ok {
		return contracts.WebhookResult{}, err
	}
	return contracts.WebhookResult{
		Item: contracts.InboxItem{
			ID: "inbox-1", Source: "webhook", Title: "delivered", Channel: "c", Count: 1,
			ReceivedAt: time.Now(), Status: contracts.InboxStatusNew,
		},
	}, nil
}

func startInboundGateway(t *testing.T, webhook contracts.WebhookService) *runningGateway {
	t.Helper()
	root := testRoot(t, newFakeChat())
	if _, err := root.Provide(contracts.KeyWebhook, webhook); err != nil {
		t.Fatalf("provide webhook: %v", err)
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

func postInbound(t *testing.T, address, path, body string, headers map[string]string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, "http://"+address+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func TestInboundAcceptedWithoutBrowserToken(t *testing.T) {
	webhook := &fakeWebhook{errs: map[string]error{}}
	gw := startInboundGateway(t, webhook)
	defer gw.stop(t)

	response := postInbound(t, gw.address, "/api/inbound/github", `{"title":"x"}`, map[string]string{
		contracts.WebhookSignatureHeader: "t=1,v1=aa",
	})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", response.StatusCode)
	}
	var payload struct {
		ID     string `json:"id"`
		Merged bool   `json:"merged"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.ID != "inbox-1" {
		t.Fatalf("payload = %+v, want the inbox id", payload)
	}
	if webhook.seen.source != "github" || webhook.seen.body != `{"title":"x"}` {
		t.Fatalf("adapter saw source %q body %q", webhook.seen.source, webhook.seen.body)
	}
}

func TestInboundMapsAdapterErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"bad signature", contracts.ErrWebhookBadSignature, http.StatusUnauthorized},
		{"stale", contracts.ErrWebhookStale, http.StatusUnauthorized},
		{"unknown source", contracts.ErrWebhookUnknownSource, http.StatusNotFound},
		{"bad payload", contracts.ErrWebhookBadPayload, http.StatusBadRequest},
		{"rate limited", contracts.ErrInboxRateLimited, http.StatusTooManyRequests},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			webhook := &fakeWebhook{errs: map[string]error{"github": tc.err}}
			gw := startInboundGateway(t, webhook)
			defer gw.stop(t)
			response := postInbound(t, gw.address, "/api/inbound/github", `{}`, nil)
			if response.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, tc.status)
			}
		})
	}
}

func TestInboundRejectsBrowserOrigin(t *testing.T) {
	webhook := &fakeWebhook{errs: map[string]error{}}
	gw := startInboundGateway(t, webhook)
	defer gw.stop(t)

	response := postInbound(t, gw.address, "/api/inbound/github", `{}`, map[string]string{
		"Origin": "https://evil.example",
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (a browser origin must never reach a signature endpoint)", response.StatusCode)
	}
	if webhook.seen.source != "" {
		t.Fatal("the adapter was called despite a browser origin")
	}
}

func TestInboundRejectsNonPost(t *testing.T) {
	webhook := &fakeWebhook{errs: map[string]error{}}
	gw := startInboundGateway(t, webhook)
	defer gw.stop(t)

	request, _ := http.NewRequest(http.MethodGet, "http://"+gw.address+"/api/inbound/github", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", response.StatusCode)
	}
}

func TestInboundRejectsOversizedBody(t *testing.T) {
	webhook := &fakeWebhook{errs: map[string]error{}}
	gw := startInboundGateway(t, webhook)
	defer gw.stop(t)

	body := strings.Repeat("a", inboundMaxBody+1024)
	response := postInbound(t, gw.address, "/api/inbound/github", body, nil)
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", response.StatusCode)
	}
	if webhook.seen.source != "" {
		t.Fatal("an oversized body reached the adapter")
	}
}

func TestInboundMissingWebhookServiceIs404(t *testing.T) {
	// No KeyWebhook on the root: the endpoint must say "unknown" instead of
	// panicking or pretending to accept.
	gw := startTestGateway(t, newFakeChat())
	defer gw.stop(t)
	response := postInbound(t, gw.address, "/api/inbound/github", `{}`, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.StatusCode)
	}
}

func TestInboundRequiresSourceSegment(t *testing.T) {
	webhook := &fakeWebhook{errs: map[string]error{}}
	gw := startInboundGateway(t, webhook)
	defer gw.stop(t)
	response := postInbound(t, gw.address, "/api/inbound/", `{}`, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a missing source", response.StatusCode)
	}
}
