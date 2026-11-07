package agent

// Tests for per-session model overrides: the read model's effective model, the
// persisted settings key, the asynchronous run's provider request and restore.

import (
	"context"
	"errors"
	"strings"
	"testing"

	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// failingSetSettings is a settings store whose writes fail.
type failingSetSettings struct {
	*fakeSettings
	err error
}

func (s *failingSetSettings) Set(string, string) error { return s.err }

// TestSessionModelSwitching covers the read model, persistence, notifications
// and the validation errors of SetSessionModel.
func TestSessionModelSwitching(t *testing.T) {
	settings := newFakeSettings(nil)
	store := newFakeStore()
	svc := newService(Config{Model: "base-model"}, rigtools.NewRegistry(),
		settings, &fakeLogger{}, store, fakeApprovals{})
	id, err := svc.NewSession("chat")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session, _ := svc.Session(id)
	if session.Model != "base-model" {
		t.Fatalf("default Model = %q, want base-model", session.Model)
	}
	var notifications int
	unsub, err := svc.Subscribe(func(sessionID string) {
		if sessionID == id {
			notifications++
		}
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsub()

	if err := svc.SetSessionModel(id, "   "); err == nil {
		t.Fatal("SetSessionModel(empty) returned nil error")
	}
	if err := svc.SetSessionModel("missing", "m"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("SetSessionModel(unknown) = %v, want unknown-session error", err)
	}
	if err := svc.SetSessionModel(id, "  deepseek-reasoner  "); err != nil {
		t.Fatalf("SetSessionModel: %v", err)
	}
	if got := settings.Get(contracts.SessionModelSettingKey(id), ""); got != "deepseek-reasoner" {
		t.Fatalf("persisted model = %q, want deepseek-reasoner", got)
	}
	session, _ = svc.Session(id)
	if session.Model != "deepseek-reasoner" {
		t.Fatalf("Model after switch = %q", session.Model)
	}
	if svc.Config().Model != "base-model" {
		t.Fatalf("global Config.Model = %q, want base-model", svc.Config().Model)
	}
	if notifications == 0 {
		t.Fatal("SetSessionModel did not notify subscribers")
	}
	if infos := svc.Sessions(); len(infos) != 1 || infos[0].ID != id {
		t.Fatalf("Sessions after switch = %+v", infos)
	}
}

// TestSessionModelWithoutSettings covers the nil-settings fallbacks.
func TestSessionModelWithoutSettings(t *testing.T) {
	svc := newService(Config{Model: "cfg-model"}, rigtools.NewRegistry(),
		nil, &fakeLogger{}, nil, fakeApprovals{})
	id, err := svc.NewSession("chat")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session, _ := svc.Session(id)
	if session.Model != "cfg-model" {
		t.Fatalf("Model without settings = %q, want cfg-model", session.Model)
	}
	if err := svc.SetSessionModel(id, "m"); !errors.Is(err, ErrSettingsUnavailable) {
		t.Fatalf("SetSessionModel without settings = %v, want ErrSettingsUnavailable", err)
	}
}

// TestSessionModelWriteFailure covers a failed persistence write: the error is
// wrapped and the effective model does not change.
func TestSessionModelWriteFailure(t *testing.T) {
	settings := &failingSetSettings{fakeSettings: newFakeSettings(nil), err: errors.New("read-only settings")}
	svc := newService(Config{Model: "base"}, rigtools.NewRegistry(),
		settings, &fakeLogger{}, newFakeStore(), fakeApprovals{})
	id, err := svc.NewSession("chat")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	err = svc.SetSessionModel(id, "other")
	if err == nil || !strings.Contains(err.Error(), "read-only settings") {
		t.Fatalf("SetSessionModel write failure = %v, want wrapped error", err)
	}
	session, _ := svc.Session(id)
	if session.Model != "base" {
		t.Fatalf("Model after failed write = %q, want base", session.Model)
	}
}

// TestSessionModelRestoresFromSettings covers a rebuilt service: the override
// lives in the settings store, so restore + Session exposes it again.
func TestSessionModelRestoresFromSettings(t *testing.T) {
	settings := newFakeSettings(nil)
	store := newFakeStore()
	first := newService(Config{Model: "base"}, rigtools.NewRegistry(),
		settings, &fakeLogger{}, store, fakeApprovals{})
	id, err := first.NewSession("chat")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := first.SetSessionModel(id, "restored-model"); err != nil {
		t.Fatalf("SetSessionModel: %v", err)
	}
	second := newService(Config{Model: "base"}, rigtools.NewRegistry(),
		settings, &fakeLogger{}, store, fakeApprovals{})
	second.restore(context.Background())
	session, ok := second.Session(id)
	if !ok {
		t.Fatalf("restored session %q missing", id)
	}
	if session.Model != "restored-model" {
		t.Fatalf("restored Model = %q, want restored-model", session.Model)
	}
}

// TestSessionModelUsedByRuns covers the provider request for Send and
// Regenerate using the session override instead of the global config.
func TestSessionModelUsedByRuns(t *testing.T) {
	provider := &captureProvider{}
	svc, _ := newAttachmentService(t, provider)
	id, err := svc.NewSession("chat")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionModel(id, "session-model"); err != nil {
		t.Fatalf("SetSessionModel: %v", err)
	}
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	request := provider.lastRequest()
	if request.Model != "session-model" {
		t.Fatalf("Send provider model = %q, want session-model", request.Model)
	}
	if err := svc.Regenerate(id); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	waitIdle(t, svc, id)
	request = provider.lastRequest()
	if request.Model != "session-model" {
		t.Fatalf("Regenerate provider model = %q, want session-model", request.Model)
	}
}
