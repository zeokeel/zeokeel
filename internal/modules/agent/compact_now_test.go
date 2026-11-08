package agent

// Tests for manual history compaction (SessionCompactor).

import (
	"context"
	"errors"
	"fmt"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeCompactor is a scripted rig.Compactor seam.
type fakeCompactor struct {
	messages []rig.Message
	err      error
	calls    int
	budget   int
}

func (c *fakeCompactor) Compact(_ context.Context, _ string, _ []rig.Message, budget int) ([]rig.Message, error) {
	c.calls++
	c.budget = budget
	return c.messages, c.err
}

func compactTestMessages(count int) []rig.Message {
	messages := make([]rig.Message, 0, count)
	for index := 0; index < count; index++ {
		role := rig.RoleUser
		if index%2 == 1 {
			role = rig.RoleAssistant
		}
		messages = append(messages, rig.NewMessage(role, fmt.Sprintf("message-%d", index)))
	}
	return messages
}

func newCompactService(t *testing.T, cfg Config, compactor rig.Compactor, compactorErr error) (*service, *fakeStore, string) {
	t.Helper()
	store := newFakeStore()
	svc := newService(cfg, rigtools.NewRegistry(), newFakeSettings(nil), &fakeLogger{}, store, fakeApprovals{})
	svc.compactorFactory = func(contracts.ChatConfig) (rig.Compactor, error) {
		if compactorErr != nil {
			return nil, compactorErr
		}
		return compactor, nil
	}
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return svc, store, sessionID
}

func TestCompactNowReplacesHistory(t *testing.T) {
	compactor := &fakeCompactor{messages: compactTestMessages(2)}
	svc, store, sessionID := newCompactService(t, Config{ContextBudget: 100}, compactor, nil)
	before := compactTestMessages(4)
	if err := store.ReplaceHistory(context.Background(), sessionID, before); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	result, err := svc.CompactNow(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	if result.Replaced != 4 || result.Saved != 2 || compactor.calls != 1 || compactor.budget != 100 {
		t.Fatalf("result/calls = %+v / %d / %d", result, compactor.calls, compactor.budget)
	}
	if result.BeforeTokens <= 0 || result.AfterTokens <= 0 {
		t.Fatalf("token estimates = %d/%d", result.BeforeTokens, result.AfterTokens)
	}
	loaded, err := store.History().Load(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("stored history length = %d, want 2", len(loaded))
	}
	// Active session is unchanged by compaction.
	if got := svc.ActiveSession(); got != sessionID {
		t.Fatalf("active session = %q, want %q", got, sessionID)
	}
}

func TestCompactNowRejectsRunning(t *testing.T) {
	compactor := &fakeCompactor{messages: compactTestMessages(1)}
	svc, store, sessionID := newCompactService(t, Config{ContextBudget: 100}, compactor, nil)
	_ = store.ReplaceHistory(context.Background(), sessionID, compactTestMessages(4))
	svc.mu.Lock()
	svc.sessions[sessionID].running = true
	svc.mu.Unlock()
	if _, err := svc.CompactNow(context.Background(), sessionID); !errors.Is(err, contracts.ErrCompactRunning) {
		t.Fatalf("CompactNow(running) = %v, want ErrCompactRunning", err)
	}
	if compactor.calls != 0 {
		t.Fatalf("compactor called for a running session")
	}
}

func TestCompactNowUnavailableWithoutBudgetOrCompactor(t *testing.T) {
	compactor := &fakeCompactor{messages: compactTestMessages(1)}
	svc, store, sessionID := newCompactService(t, Config{}, compactor, nil)
	_ = store.ReplaceHistory(context.Background(), sessionID, compactTestMessages(4))
	if _, err := svc.CompactNow(context.Background(), sessionID); !errors.Is(err, contracts.ErrCompactUnavailable) {
		t.Fatalf("CompactNow(budget=0) = %v, want ErrCompactUnavailable", err)
	}
	svc2, store2, session2 := newCompactService(t, Config{ContextBudget: 100}, nil, errors.New("provider boom"))
	_ = store2.ReplaceHistory(context.Background(), session2, compactTestMessages(4))
	if _, err := svc2.CompactNow(context.Background(), session2); !errors.Is(err, contracts.ErrCompactUnavailable) {
		t.Fatalf("CompactNow(provider failure) = %v, want ErrCompactUnavailable", err)
	}
}

func TestCompactNowNothingToCompact(t *testing.T) {
	messages := compactTestMessages(4)
	compactor := &fakeCompactor{messages: messages}
	svc, store, sessionID := newCompactService(t, Config{ContextBudget: 100}, compactor, nil)
	_ = store.ReplaceHistory(context.Background(), sessionID, messages)
	if _, err := svc.CompactNow(context.Background(), sessionID); !errors.Is(err, contracts.ErrCompactNothing) {
		t.Fatalf("CompactNow(no reduction) = %v, want ErrCompactNothing", err)
	}
	emptySvc, _, emptySession := newCompactService(t, Config{ContextBudget: 100}, compactor, nil)
	if _, err := emptySvc.CompactNow(context.Background(), emptySession); !errors.Is(err, contracts.ErrCompactNothing) {
		t.Fatalf("CompactNow(empty history) = %v, want ErrCompactNothing", err)
	}
}

func TestCompactNowSaveFailure(t *testing.T) {
	compactor := &fakeCompactor{messages: compactTestMessages(2)}
	svc, store, sessionID := newCompactService(t, Config{ContextBudget: 100}, compactor, nil)
	_ = store.ReplaceHistory(context.Background(), sessionID, compactTestMessages(4))
	store.replaceErr = errors.New("disk full")
	if _, err := svc.CompactNow(context.Background(), sessionID); !errors.Is(err, contracts.ErrCompactSave) {
		t.Fatalf("CompactNow(save failure) = %v, want ErrCompactSave", err)
	}
}
