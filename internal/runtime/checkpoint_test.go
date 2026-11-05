package runtime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/state"
)

// memoryCheckpointStore is an in-memory CheckpointStore for tests. It counts
// saves and deletes and can inject failures on any operation.
type memoryCheckpointStore struct {
	mu        sync.Mutex
	data      map[string]*Checkpoint
	saves     int
	deletes   int
	loadErr   error
	saveErr   error
	deleteErr error
}

var _ CheckpointStore = (*memoryCheckpointStore)(nil)

func newMemoryCheckpointStore() *memoryCheckpointStore {
	return &memoryCheckpointStore{data: make(map[string]*Checkpoint)}
}

func (s *memoryCheckpointStore) Load(ctx context.Context, sessionID string) (*Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	cp, ok := s.data[sessionID]
	if !ok || cp == nil {
		return nil, nil
	}
	return cloneCheckpoint(cp), nil
}

func (s *memoryCheckpointStore) Save(ctx context.Context, sessionID string, cp *Checkpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saves++
	s.data[sessionID] = cloneCheckpoint(cp)
	return nil
}

func (s *memoryCheckpointStore) Delete(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deletes++
	delete(s.data, sessionID)
	return nil
}

func (s *memoryCheckpointStore) get(sessionID string) *Checkpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneCheckpoint(s.data[sessionID])
}

func (s *memoryCheckpointStore) saveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saves
}

func cloneCheckpoint(cp *Checkpoint) *Checkpoint {
	if cp == nil {
		return nil
	}
	out := *cp
	out.History = cloneMessages(cp.History)
	return &out
}

func runTurn(t *testing.T, agent *Agent, sessionID, text string) []state.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, sessionID, text)
	if err != nil {
		t.Fatalf("Run(%q): %v", text, err)
	}
	return collectEvents(t, ch)
}

func providerMessages(t *testing.T, p *fakeProvider, index int) []string {
	t.Helper()
	req, ok := p.requestAt(index)
	if !ok {
		t.Fatalf("provider got no request at index %d", index)
	}
	return messageContents(req.Messages)
}

func TestCheckpointSavesCompletedTurn(t *testing.T) {
	store := newMemoryCheckpointStore()
	agent := newTestAgent(t, Config{Provider: echoProvider(t), CheckpointStore: store})

	before := time.Now().Add(-time.Second)
	events := runTurn(t, agent, "session-cp", "hello")
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("turn produced error event: %q", errText)
	}

	if store.saveCount() != 1 {
		t.Fatalf("Save calls = %d, want 1", store.saveCount())
	}
	cp := store.get("session-cp")
	if cp == nil {
		t.Fatal("no checkpoint saved")
	}
	if cp.TurnID == "" {
		t.Fatal("checkpoint TurnID is empty")
	}
	if cp.UpdatedAt.Before(before) || cp.UpdatedAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("checkpoint UpdatedAt = %v, want now", cp.UpdatedAt)
	}
	if cp.Phase != CheckpointPhaseCompleted {
		t.Fatalf("checkpoint Phase = %q, want %q", cp.Phase, CheckpointPhaseCompleted)
	}
	got := messageContents(cp.History)
	want := []string{"user:hello", "assistant:reply:hello"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("checkpoint history = %v, want %v", got, want)
	}
}

func TestCheckpointResumesInNewAgent(t *testing.T) {
	store := newMemoryCheckpointStore()

	first := newTestAgent(t, Config{Provider: echoProvider(t), CheckpointStore: store})
	runTurn(t, first, "session-resume", "first")

	p := echoProvider(t)
	second := newTestAgent(t, Config{Provider: p, CheckpointStore: store})
	events := runTurn(t, second, "session-resume", "second")
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("resumed turn produced error event: %q", errText)
	}

	got := providerMessages(t, p, 0)
	want := []string{"user:first", "assistant:reply:first", "user:second"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("resumed provider messages = %v, want %v", got, want)
	}

	cp := store.get("session-resume")
	wantStored := []string{"user:first", "assistant:reply:first", "user:second", "assistant:reply:second"}
	if cp == nil || strings.Join(messageContents(cp.History), "|") != strings.Join(wantStored, "|") {
		t.Fatalf("checkpoint history = %+v, want %v", cp, wantStored)
	}
	if cp.TurnID == "" {
		t.Fatal("resumed checkpoint has no TurnID")
	}
}

func TestCheckpointMissingStartsFresh(t *testing.T) {
	store := newMemoryCheckpointStore()
	p := echoProvider(t)
	agent := newTestAgent(t, Config{Provider: p, CheckpointStore: store})

	events := runTurn(t, agent, "session-missing", "hello")
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("missing checkpoint produced error event: %q", errText)
	}
	if got := providerMessages(t, p, 0); len(got) != 1 || got[0] != "user:hello" {
		t.Fatalf("provider messages = %v, want only the prompt", got)
	}
}

func TestCheckpointCorruptIsIgnored(t *testing.T) {
	store := newMemoryCheckpointStore()
	store.loadErr = errors.New("corrupt checkpoint")
	p := echoProvider(t)
	agent := newTestAgent(t, Config{Provider: p, CheckpointStore: store})

	events := runTurn(t, agent, "session-corrupt", "hello")
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("corrupt checkpoint produced error event: %q", errText)
	}
	if got := providerMessages(t, p, 0); len(got) != 1 || got[0] != "user:hello" {
		t.Fatalf("provider messages = %v, want a fresh conversation", got)
	}
	// New completed state is still saved, replacing the unreadable checkpoint.
	if store.saveCount() != 1 {
		t.Fatalf("Save calls = %d, want 1", store.saveCount())
	}
}

func TestCheckpointSaveErrorBecomesErrorEvent(t *testing.T) {
	store := newMemoryCheckpointStore()
	store.saveErr = errors.New("checkpoint disk full")
	agent := newTestAgent(t, Config{Provider: echoProvider(t), CheckpointStore: store})

	events := runTurn(t, agent, "session-save-err", "hello")
	errText := findErrorText(events)
	if !strings.Contains(errText, "checkpoint disk full") {
		t.Fatalf("error event = %q, events: %#v", errText, events)
	}
	if !strings.Contains(errText, "save checkpoint") {
		t.Fatalf("error event = %q, want save checkpoint context", errText)
	}
	gotPhases := phases(events)
	if len(gotPhases) == 0 || gotPhases[len(gotPhases)-1] != state.PhaseFailed {
		t.Fatalf("phases = %v, want failed", gotPhases)
	}
	if store.get("session-save-err") != nil {
		t.Fatal("failed Save left a checkpoint")
	}
}

func TestCheckpointHistoryTakesPrecedence(t *testing.T) {
	hist := newFakeHistory()
	hist.msgs["session-both"] = []Message{
		{Role: RoleUser, Content: "from history"},
		{Role: RoleAssistant, Content: "history answer"},
	}
	store := newMemoryCheckpointStore()
	store.data["session-both"] = &Checkpoint{
		History:   []Message{{Role: RoleUser, Content: "from checkpoint"}},
		TurnID:    "old",
		UpdatedAt: time.Now(),
		Phase:     CheckpointPhaseCompleted,
	}
	p := echoProvider(t)
	agent := newTestAgent(t, Config{Provider: p, History: hist, CheckpointStore: store})

	runTurn(t, agent, "session-both", "new")
	got := providerMessages(t, p, 0)
	want := []string{"user:from history", "assistant:history answer", "user:new"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("provider messages = %v, want history to win", got)
	}

	// A completed turn is still mirrored to the checkpoint store.
	cp := store.get("session-both")
	if cp == nil || len(cp.History) != 4 {
		t.Fatalf("checkpoint = %+v, want mirrored completed conversation", cp)
	}
}

func TestCheckpointNotSavedForFailedTurn(t *testing.T) {
	store := newMemoryCheckpointStore()
	provider := &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
		return nil, errors.New("provider exploded")
	}}
	agent := newTestAgent(t, Config{Provider: provider, CheckpointStore: store})

	events := runTurn(t, agent, "session-failed", "hello")
	if errText := findErrorText(events); !strings.Contains(errText, "provider exploded") {
		t.Fatalf("error event = %q", errText)
	}
	if store.saveCount() != 0 {
		t.Fatalf("Save calls = %d, want 0 for a failed turn", store.saveCount())
	}
}

func TestCheckpointWithCompaction(t *testing.T) {
	store := newMemoryCheckpointStore()
	store.data["session-cp-compact"] = &Checkpoint{
		History: []Message{
			{Role: RoleUser, Content: repeatedContent("old1:", 80)},
			{Role: RoleAssistant, Content: repeatedContent("old2:", 80)},
			{Role: RoleUser, Content: repeatedContent("old3:", 80)},
		},
		TurnID: "prev",
		Phase:  CheckpointPhaseCompleted,
	}
	p := echoProvider(t)
	agent := newTestAgent(t, Config{
		Provider:        p,
		CheckpointStore: store,
		Compactor:       WindowCompactor{KeepLast: 1},
		ContextBudget:   1,
	})

	runTurn(t, agent, "session-cp-compact", "new")
	got := providerMessages(t, p, 0)
	wantPrefix := []string{
		"system:Earlier conversation compacted (2 messages)",
		"user:" + repeatedContent("old3:", 80),
		"user:new",
	}
	if len(got) != len(wantPrefix) {
		t.Fatalf("provider messages = %v, want %v", got, wantPrefix)
	}
	for i := range wantPrefix {
		if got[i] != wantPrefix[i] {
			t.Fatalf("provider messages = %v, want %v", got, wantPrefix)
		}
	}

	cp := store.get("session-cp-compact")
	if cp == nil {
		t.Fatal("no checkpoint after compacted turn")
	}
	// The checkpoint stores the compacted prior plus the new turn, not the
	// dropped messages.
	wantStored := []string{
		"system:Earlier conversation compacted (2 messages)",
		"user:" + repeatedContent("old3:", 80),
		"user:new",
		"assistant:reply:new",
	}
	if strings.Join(messageContents(cp.History), "|") != strings.Join(wantStored, "|") {
		t.Fatalf("checkpoint history = %v, want %v", messageContents(cp.History), wantStored)
	}
}

// TestCheckpointResumeKeepsCompactionSummary covers the restart-resume path:
// the saved checkpoint begins with the synthetic summary system message, and a
// new agent with an Instruction must still send that summary to the provider
// (eino's leading-system strip must not treat it as a stale instruction).
func TestCheckpointResumeKeepsCompactionSummary(t *testing.T) {
	store := newMemoryCheckpointStore()
	store.data["session-cp-summary"] = &Checkpoint{
		History: []Message{
			{Role: RoleUser, Content: repeatedContent("old-q ", 80)},
			{Role: RoleAssistant, Content: repeatedContent("old-a ", 80)},
			{Role: RoleUser, Content: "tail-u"},
		},
		TurnID:    "prev",
		UpdatedAt: time.Now(),
		Phase:     CheckpointPhaseCompleted,
	}

	firstProvider := echoProvider(t)
	first := newTestAgent(t, Config{
		Provider:        firstProvider,
		Instruction:     "AGENT RULES",
		CheckpointStore: store,
		Compactor:       WindowCompactor{KeepLast: 1},
		ContextBudget:   1,
	})
	if events := runTurn(t, first, "session-cp-summary", "first-new"); findErrorText(events) != "" {
		t.Fatalf("first turn produced error event: %q", findErrorText(events))
	}
	wantFirst := []string{
		"system:AGENT RULES",
		"system:Earlier conversation compacted (2 messages)",
		"user:tail-u",
		"user:first-new",
	}
	if got := providerMessages(t, firstProvider, 0); strings.Join(got, "|") != strings.Join(wantFirst, "|") {
		t.Fatalf("first provider messages = %v, want %v", got, wantFirst)
	}

	cp := store.get("session-cp-summary")
	if cp == nil || len(cp.History) == 0 || !isCompactionSummary(cp.History[0]) {
		t.Fatalf("checkpoint does not begin with the compaction summary: %+v", cp)
	}

	secondProvider := echoProvider(t)
	second := newTestAgent(t, Config{
		Provider:        secondProvider,
		Instruction:     "AGENT RULES",
		CheckpointStore: store,
	})
	if events := runTurn(t, second, "session-cp-summary", "next"); findErrorText(events) != "" {
		t.Fatalf("resumed turn produced error event: %q", findErrorText(events))
	}
	wantResumed := []string{
		"system:AGENT RULES",
		"system:Earlier conversation compacted (2 messages)",
		"user:tail-u",
		"user:first-new",
		"assistant:reply:first-new",
		"user:next",
	}
	if got := providerMessages(t, secondProvider, 0); strings.Join(got, "|") != strings.Join(wantResumed, "|") {
		t.Fatalf("resumed provider messages = %v, want %v", got, wantResumed)
	}
}

func TestCheckpointDelete(t *testing.T) {
	store := newMemoryCheckpointStore()
	agent := newTestAgent(t, Config{Provider: echoProvider(t), CheckpointStore: store})
	runTurn(t, agent, "session-delete", "hello")

	if err := store.Delete(context.Background(), "session-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if cp := store.get("session-delete"); cp != nil {
		t.Fatalf("checkpoint still present after Delete: %+v", cp)
	}
	if store.deletes != 1 {
		t.Fatalf("Delete calls = %d, want 1", store.deletes)
	}
}
