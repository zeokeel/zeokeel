package webhook_test

// TDD for the first channel adapter. The rules here are security rules: a
// forged request must not reach the inbox, a replayed one must not create a
// second item, an unknown source must not be guessed at, and a stale delivery
// must be refused rather than trusted.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
	"github.com/zeokeel/zeokeel/internal/modules/webhook"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func sign(secret []byte, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(strconv.FormatInt(timestamp, 10) + "."))
	mac.Write(body)
	return fmt.Sprintf("t=%d,v1=%s", timestamp, hex.EncodeToString(mac.Sum(nil)))
}

type fixture struct {
	svc    *webhook.Service
	inbox  contracts.InboxService
	clock  *clock
	secret []byte
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	clk := &clock{t: time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)}
	secret := []byte("s3cret")
	inboxSvc := inbox.NewService(inbox.Config{Now: clk.now, MaxPerMinute: 100})
	svc := webhook.NewService(inboxSvc, webhook.Config{
		Now: clk.now,
		Sources: []contracts.WebhookSource{
			{ID: "github", Secret: secret, DefaultChannel: "repo-events"},
		},
	})
	return &fixture{svc: svc, inbox: inboxSvc, clock: clk, secret: secret}
}

func (f *fixture) deliver(t *testing.T, body string) (contracts.WebhookResult, error) {
	t.Helper()
	return f.svc.Receive(context.Background(), "github",
		sign(f.secret, f.clock.now().Unix(), []byte(body)), []byte(body))
}

func TestValidDeliveryLandsInTheInbox(t *testing.T) {
	f := newFixture(t)
	result, err := f.deliver(t, `{"title":"新 issue","body":"#12 崩溃","ref":"gh://12"}`)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if result.Item.Title != "新 issue" || result.Item.Source != "webhook" {
		t.Fatalf("item = %+v", result.Item)
	}
	if result.Item.Channel != "repo-events" {
		t.Fatalf("channel = %q, want the configured default", result.Item.Channel)
	}
	items, _ := f.inbox.List(context.Background(), contracts.InboxFilter{})
	if len(items) != 1 {
		t.Fatalf("inbox items = %d, want 1", len(items))
	}
}

func TestForgedSignatureIsRejectedAndNothingIsStored(t *testing.T) {
	f := newFixture(t)
	body := []byte(`{"title":"evil"}`)
	bad := sign([]byte("wrong-secret"), f.clock.now().Unix(), body)
	if _, err := f.svc.Receive(context.Background(), "github", bad, body); !errors.Is(err, contracts.ErrWebhookBadSignature) {
		t.Fatalf("err = %v, want ErrWebhookBadSignature", err)
	}
	items, _ := f.inbox.List(context.Background(), contracts.InboxFilter{})
	if len(items) != 0 {
		t.Fatalf("forged delivery reached the inbox: %d items", len(items))
	}
}

func TestMissingSignatureIsRejected(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Receive(context.Background(), "github", "", []byte(`{}`)); !errors.Is(err, contracts.ErrWebhookBadSignature) {
		t.Fatalf("err = %v, want ErrWebhookBadSignature", err)
	}
}

func TestStaleTimestampIsRejected(t *testing.T) {
	f := newFixture(t)
	body := []byte(`{"title":"old"}`)
	old := f.clock.now().Add(-contracts.WebhookMaxSkew - time.Minute).Unix()
	if _, err := f.svc.Receive(context.Background(), "github", sign(f.secret, old, body), body); !errors.Is(err, contracts.ErrWebhookStale) {
		t.Fatalf("err = %v, want ErrWebhookStale", err)
	}
}

func TestFutureTimestampIsRejected(t *testing.T) {
	f := newFixture(t)
	body := []byte(`{"title":"future"}`)
	future := f.clock.now().Add(contracts.WebhookMaxSkew + time.Minute).Unix()
	if _, err := f.svc.Receive(context.Background(), "github", sign(f.secret, future, body), body); !errors.Is(err, contracts.ErrWebhookStale) {
		t.Fatalf("err = %v, want ErrWebhookStale", err)
	}
}

func TestUnknownSourceIsRejected(t *testing.T) {
	f := newFixture(t)
	body := []byte(`{}`)
	if _, err := f.svc.Receive(context.Background(), "nope", sign(f.secret, f.clock.now().Unix(), body), body); !errors.Is(err, contracts.ErrWebhookUnknownSource) {
		t.Fatalf("err = %v, want ErrWebhookUnknownSource", err)
	}
}

func TestReplayOfTheSameBodyMergesInsteadOfDuplicating(t *testing.T) {
	f := newFixture(t)
	body := `{"title":"同一个事件"}`
	first, err := f.deliver(t, body)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := f.deliver(t, body)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !second.Merged {
		t.Fatal("replay created a second item")
	}
	if second.Item.ID != first.Item.ID || second.Item.Count != 2 {
		t.Fatalf("second = %+v, want the same item with count 2", second.Item)
	}
}

func TestMalformedPayloadIsRejected(t *testing.T) {
	f := newFixture(t)
	if _, err := f.deliver(t, `{not json`); !errors.Is(err, contracts.ErrWebhookBadPayload) {
		t.Fatalf("err = %v, want ErrWebhookBadPayload", err)
	}
}

func TestMissingTitleIsRejected(t *testing.T) {
	f := newFixture(t)
	if _, err := f.deliver(t, `{"body":"no title"}`); !errors.Is(err, contracts.ErrWebhookBadPayload) {
		t.Fatalf("err = %v, want ErrWebhookBadPayload", err)
	}
}

func TestInboxRateLimitPropagates(t *testing.T) {
	clk := &clock{t: time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)}
	secret := []byte("s")
	inboxSvc := inbox.NewService(inbox.Config{Now: clk.now, MaxPerMinute: 1})
	svc := webhook.NewService(inboxSvc, webhook.Config{
		Now:     clk.now,
		Sources: []contracts.WebhookSource{{ID: "s1", Secret: secret}},
	})
	send := func(body string) error {
		_, err := svc.Receive(context.Background(), "s1", sign(secret, clk.now().Unix(), []byte(body)), []byte(body))
		return err
	}
	if err := send(`{"title":"a"}`); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := send(`{"title":"b"}`); !errors.Is(err, contracts.ErrInboxRateLimited) {
		t.Fatalf("second err = %v, want the inbox rate limit to propagate", err)
	}
}
