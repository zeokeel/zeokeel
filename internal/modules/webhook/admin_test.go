package webhook_test

// TDD for source management: the settings UI must be able to add, rotate and
// remove endpoints, the secrets must survive a restart, and no read path may
// ever expose a secret.

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
	"github.com/zeokeel/zeokeel/internal/modules/webhook"
)

func openAdmin(t *testing.T, dir string) (*webhook.Service, contracts.InboxService) {
	t.Helper()
	inboxSvc := inbox.NewService(inbox.Config{Now: time.Now, MaxPerMinute: 1000})
	svc, err := webhook.Open(dir, inboxSvc, webhook.Config{Now: time.Now})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return svc, inboxSvc
}

func TestSourcesAreSortedAndSecretFree(t *testing.T) {
	dir := t.TempDir()
	svc, _ := openAdmin(t, dir)
	ctx := context.Background()
	if err := svc.UpsertSource(ctx, contracts.WebhookSource{ID: "zulu", Secret: []byte("0123456789abcdef"), DefaultChannel: "c1"}); err != nil {
		t.Fatalf("Upsert zulu: %v", err)
	}
	if err := svc.UpsertSource(ctx, contracts.WebhookSource{ID: "alpha", Secret: []byte("0123456789abcdef"), DefaultChannel: "c2"}); err != nil {
		t.Fatalf("Upsert alpha: %v", err)
	}
	sources, err := svc.Sources(ctx)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(sources) != 2 || sources[0].ID != "alpha" || sources[1].ID != "zulu" {
		t.Fatalf("sources = %+v, want alpha then zulu", sources)
	}
	if sources[0].Channel != "c2" {
		t.Fatalf("channel = %q", sources[0].Channel)
	}
}

func TestUpsertValidatesIDAndSecret(t *testing.T) {
	dir := t.TempDir()
	svc, _ := openAdmin(t, dir)
	ctx := context.Background()

	for _, source := range []contracts.WebhookSource{
		{ID: "", Secret: []byte("0123456789abcdef")},
		{ID: "Bad Id", Secret: []byte("0123456789abcdef")},
		{ID: "ok", Secret: []byte("short")},
		{ID: "ok"},
	} {
		if err := svc.UpsertSource(ctx, source); !errors.Is(err, contracts.ErrWebhookInvalidSource) {
			t.Fatalf("UpsertSource(%+v) err = %v, want ErrWebhookInvalidSource", source, err)
		}
	}
}

func TestRemoveSource(t *testing.T) {
	dir := t.TempDir()
	svc, _ := openAdmin(t, dir)
	ctx := context.Background()
	if err := svc.UpsertSource(ctx, contracts.WebhookSource{ID: "temp", Secret: []byte("0123456789abcdef")}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := svc.RemoveSource(ctx, "temp"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := svc.RemoveSource(ctx, "temp"); !errors.Is(err, contracts.ErrWebhookUnknownSource) {
		t.Fatalf("second remove err = %v, want ErrWebhookUnknownSource", err)
	}
	if sources, _ := svc.Sources(ctx); len(sources) != 0 {
		t.Fatalf("sources = %+v, want none", sources)
	}
}

func TestSourcesSurviveReopenAndStillVerify(t *testing.T) {
	dir := t.TempDir()
	svc, _ := openAdmin(t, dir)
	secret := []byte("rotating-secret-0001")
	if err := svc.UpsertSource(context.Background(), contracts.WebhookSource{ID: "github", Secret: secret, DefaultChannel: "repo"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	reopened, reopenedInbox := openAdmin(t, dir)
	sources, err := reopened.Sources(context.Background())
	if err != nil || len(sources) != 1 || sources[0].ID != "github" {
		t.Fatalf("sources after reopen = (%+v, %v)", sources, err)
	}
	body := []byte(`{"title":"persisted"}`)
	header := webhook.Sign(secret, time.Now(), body)
	if _, err := reopened.Receive(context.Background(), "github", header, body); err != nil {
		t.Fatalf("Receive after reopen: %v", err)
	}
	if items, _ := reopenedInbox.List(context.Background(), contracts.InboxFilter{}); len(items) != 1 {
		t.Fatalf("inbox items = %d, want 1", len(items))
	}
}

func TestRotatingSecretInvalidatesTheOldOne(t *testing.T) {
	dir := t.TempDir()
	svc, _ := openAdmin(t, dir)
	ctx := context.Background()
	if err := svc.UpsertSource(ctx, contracts.WebhookSource{ID: "github", Secret: []byte("old-secret-000000")}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := svc.UpsertSource(ctx, contracts.WebhookSource{ID: "github", Secret: []byte("new-secret-000000")}); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	body := []byte(`{"title":"x"}`)
	if _, err := svc.Receive(ctx, "github", webhook.Sign([]byte("old-secret-000000"), time.Now(), body), body); !errors.Is(err, contracts.ErrWebhookBadSignature) {
		t.Fatalf("old secret err = %v, want ErrWebhookBadSignature", err)
	}
	if _, err := svc.Receive(ctx, "github", webhook.Sign([]byte("new-secret-000000"), time.Now(), body), body); err != nil {
		t.Fatalf("new secret: %v", err)
	}
}

func TestSourcesFileIsPrivate(t *testing.T) {
	dir := t.TempDir()
	svc, _ := openAdmin(t, dir)
	if err := svc.UpsertSource(context.Background(), contracts.WebhookSource{ID: "github", Secret: []byte("0123456789abcdef")}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "sources.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %o, want 600", mode)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "sources.json"))
	// The secret is stored base64-encoded (still plaintext, but the file is
	// 0600); the check proves it really persists, not that it is encrypted.
	if !strings.Contains(string(raw), base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))) {
		t.Fatal("secret missing from the persisted file (it must persist, just privately)")
	}
}
