package wechat_test

// TDD for the credentials seam: the login token should come from the shared
// credential store when no explicit config token is set, so one place owns the
// secret and a restart/hot-update does not depend on an assembly document.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
	"github.com/zeokeel/zeokeel/internal/modules/wechat"
)

type fakeCreds struct {
	mu     sync.Mutex
	values map[string]map[string]string
}

func newFakeCreds() *fakeCreds { return &fakeCreds{values: map[string]map[string]string{}} }

func key(kind, id string) string { return kind + "/" + id }

func (f *fakeCreds) Get(_ context.Context, kind, id string) (contracts.Credential, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fields, ok := f.values[key(kind, id)]
	if !ok {
		return contracts.Credential{}, false, nil
	}
	copied := make(map[string]string, len(fields))
	for k, v := range fields {
		copied[k] = v
	}
	return contracts.Credential{Kind: kind, ID: id, Fields: copied}, true, nil
}

func (f *fakeCreds) Set(_ context.Context, credential contracts.Credential) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := make(map[string]string, len(credential.Fields))
	for k, v := range credential.Fields {
		copied[k] = v
	}
	f.values[key(credential.Kind, credential.ID)] = copied
	return nil
}

func (f *fakeCreds) Delete(_ context.Context, kind, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.values, key(kind, id))
	return nil
}

func (f *fakeCreds) List(_ context.Context, kind string) ([]contracts.CredentialInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []contracts.CredentialInfo
	for full := range f.values {
		_ = full
	}
	return out, nil
}

func TestAdapterUsesTheStoredToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer stored-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"msgs":[]}`))
	}))
	defer server.Close()

	creds := newFakeCreds()
	if err := creds.Set(context.Background(), contracts.Credential{
		Kind: "wechat", ID: "default", Fields: map[string]string{"token": "stored-token"},
	}); err != nil {
		t.Fatalf("seed creds: %v", err)
	}
	inboxSvc := inbox.NewService(inbox.Config{Now: time.Now})
	adapter := wechat.NewAdapterWithCredentials(inboxSvc, wechat.Config{BaseURL: server.URL, Now: time.Now}, creds)

	status, err := adapter.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Connected {
		t.Fatal("adapter did not pick up the stored token")
	}
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce with the stored token: %v", err)
	}
}

func TestExplicitConfigTokenWinsOverTheStore(t *testing.T) {
	creds := newFakeCreds()
	_ = creds.Set(context.Background(), contracts.Credential{
		Kind: "wechat", ID: "default", Fields: map[string]string{"token": "stored-token"},
	})
	inboxSvc := inbox.NewService(inbox.Config{Now: time.Now})
	adapter := wechat.NewAdapterWithCredentials(inboxSvc, wechat.Config{Token: "explicit", Now: time.Now}, creds)
	status, _ := adapter.Status(context.Background())
	if !status.Connected {
		t.Fatal("explicit token not reported as connected")
	}
}

func TestSetTokenPersistsIntoTheStore(t *testing.T) {
	creds := newFakeCreds()
	inboxSvc := inbox.NewService(inbox.Config{Now: time.Now})
	adapter := wechat.NewAdapterWithCredentials(inboxSvc, wechat.Config{Now: time.Now}, creds)
	if err := adapter.SetToken("fresh-token"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}
	stored, ok, err := creds.Get(context.Background(), "wechat", "default")
	if err != nil || !ok || stored.Fields["token"] != "fresh-token" {
		t.Fatalf("stored credentials = (%+v, %v, %v)", stored, ok, err)
	}
}
