package wechat_test

// TDD for the login-token seam: SetToken persists through the store, and
// reopening the adapter on the same directory reports Connected and actually
// uses the stored token on the wire.

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
	"github.com/zeokeel/zeokeel/internal/modules/wechat"
)

// newStoreFixture is newFixture plus a data dir for the token store.
func newStoreFixture(t *testing.T) (*fakeILink, string, contracts.InboxService, string, *clock) {
	t.Helper()
	server := &fakeILink{}
	httpServer := httptest.NewServer(server.handler())
	t.Cleanup(httpServer.Close)
	clk := &clock{t: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
	inboxSvc := inbox.NewService(inbox.Config{Now: clk.now, MaxPerMinute: 1000})
	return server, httpServer.URL, inboxSvc, t.TempDir(), clk
}

func TestSetTokenPersistsAndReconnectsOnReopen(t *testing.T) {
	server, url, inboxSvc, dir, clk := newStoreFixture(t)
	cfg := wechat.Config{BaseURL: url, Now: clk.now}

	store, err := wechat.OpenTokenStore(dir)
	if err != nil {
		t.Fatalf("OpenTokenStore: %v", err)
	}
	adapter, err := wechat.OpenAdapter(inboxSvc, cfg, store)
	if err != nil {
		t.Fatalf("OpenAdapter: %v", err)
	}
	status, err := adapter.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Connected {
		t.Fatalf("status = %+v, want disconnected before login", status)
	}

	if err := adapter.SetToken("bot-token-1"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "token.json"))
	if err != nil {
		t.Fatalf("token.json was not written: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("token.json mode = %04o, want 0600", mode)
	}

	// A restart is exactly this: a new store + adapter over the same dir.
	restartedStore, err := wechat.OpenTokenStore(dir)
	if err != nil {
		t.Fatalf("OpenTokenStore (restart): %v", err)
	}
	restarted, err := wechat.OpenAdapter(inboxSvc, cfg, restartedStore)
	if err != nil {
		t.Fatalf("OpenAdapter (restart): %v", err)
	}
	status, err = restarted.Status(context.Background())
	if err != nil {
		t.Fatalf("Status (restart): %v", err)
	}
	if !status.Connected {
		t.Fatalf("status after restart = %+v, want Connected", status)
	}

	// Connected must mean the persisted token is really used, not just flagged.
	server.pushMessage("m1", "user@im.wechat", "hi", "token-1")
	if _, err := restarted.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce after restart: %v", err)
	}
	if got, want := server.lastAuthHeader(), "Bearer bot-token-1"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
}

func TestSetTokenWithoutStoreStaysInMemory(t *testing.T) {
	_, url, inboxSvc, _, clk := newStoreFixture(t)
	adapter := wechat.NewAdapter(inboxSvc, wechat.Config{BaseURL: url, Now: clk.now})
	if err := adapter.SetToken("bot-token-1"); err != nil {
		t.Fatalf("SetToken without a store: %v", err)
	}
	status, err := adapter.Status(context.Background())
	if err != nil || !status.Connected {
		t.Fatalf("status = (%+v, %v), want connected", status, err)
	}
}

func TestSetTokenForgetsThePreviousLoginsContextTokens(t *testing.T) {
	_, url, inboxSvc, dir, clk := newStoreFixture(t)
	store, err := wechat.OpenTokenStore(dir)
	if err != nil {
		t.Fatalf("OpenTokenStore: %v", err)
	}
	adapter, err := wechat.OpenAdapter(inboxSvc, wechat.Config{BaseURL: url, Now: clk.now}, store)
	if err != nil {
		t.Fatalf("OpenAdapter: %v", err)
	}
	if err := adapter.SetToken("bot-token-1"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}
	// Context tokens belong to the logged-in bot identity; a new login must not
	// keep answering with the old identity's tokens.
	if err := adapter.SetToken("bot-token-2"); err != nil {
		t.Fatalf("SetToken (relogin): %v", err)
	}
	if err := adapter.Send(context.Background(), "user@im.wechat", "hi"); !errors.Is(err, contracts.ErrWeChatNoContext) {
		t.Fatalf("Send after relogin = %v, want ErrWeChatNoContext", err)
	}
}

func TestOpenAdapterReportsCorruptTokenFile(t *testing.T) {
	_, url, inboxSvc, dir, clk := newStoreFixture(t)
	path := filepath.Join(dir, "token.json")
	if err := os.WriteFile(path, []byte(`{"token":`), 0o600); err != nil {
		t.Fatalf("seed corrupt token: %v", err)
	}
	store, err := wechat.OpenTokenStore(dir)
	if err != nil {
		t.Fatalf("OpenTokenStore: %v", err)
	}
	if _, err := wechat.OpenAdapter(inboxSvc, wechat.Config{BaseURL: url, Now: clk.now}, store); !errors.Is(err, wechat.ErrTokenStoreCorrupt) {
		t.Fatalf("OpenAdapter err = %v, want ErrTokenStoreCorrupt", err)
	}
	// Quarantine is the caller's decision: the file is left for it to move.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("corrupt token file was removed by OpenAdapter: %v", err)
	}
}

func TestConfigTokenWinsOverTheStore(t *testing.T) {
	server, url, inboxSvc, dir, clk := newStoreFixture(t)
	store, err := wechat.OpenTokenStore(dir)
	if err != nil {
		t.Fatalf("OpenTokenStore: %v", err)
	}
	if err := store.Save("stored-token"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	adapter, err := wechat.OpenAdapter(inboxSvc, wechat.Config{BaseURL: url, Token: "explicit-token", Now: clk.now}, store)
	if err != nil {
		t.Fatalf("OpenAdapter: %v", err)
	}
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	// An explicitly configured token (assembly config) must not be overridden by
	// whatever an older login left on disk.
	if got, want := server.lastAuthHeader(), "Bearer explicit-token"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
}

func TestSetTokenSurfacesStoreFailure(t *testing.T) {
	_, url, inboxSvc, dir, clk := newStoreFixture(t)
	store, err := wechat.OpenTokenStore(dir)
	if err != nil {
		t.Fatalf("OpenTokenStore: %v", err)
	}
	adapter, err := wechat.OpenAdapter(inboxSvc, wechat.Config{BaseURL: url, Now: clk.now}, store)
	if err != nil {
		t.Fatalf("OpenAdapter: %v", err)
	}
	// Make the store path unwritable by turning it into a directory.
	if err := os.MkdirAll(filepath.Join(dir, "token.json"), 0o700); err != nil {
		t.Fatalf("seed unwritable store: %v", err)
	}
	if err := adapter.SetToken("bot-token-1"); err == nil {
		t.Fatal("SetToken succeeded with an unwritable token file")
	}
	// A login that could not be persisted must not be reported as connected: it
	// would vanish on the next restart.
	status, err := adapter.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Connected {
		t.Fatalf("status = %+v after a failed save, want disconnected", status)
	}
}
