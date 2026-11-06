package runtime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// manualCompactionSeed is the conversation the manual compaction tests run on:
// a system prompt and three user/assistant exchanges. WindowCompactor with
// KeepLast=2 drops four of them and keeps the third exchange.
func manualCompactionSeed() []Message {
	return []Message{
		{Role: RoleSystem, Content: "system rules"},
		{Role: RoleUser, Content: "old question one"},
		{Role: RoleAssistant, Content: "old answer one"},
		{Role: RoleUser, Content: "old question two"},
		{Role: RoleAssistant, Content: "old answer two"},
		{Role: RoleUser, Content: "old question three"},
		{Role: RoleAssistant, Content: "old answer three"},
	}
}

// keepLastFn adapts WindowCompactor to the recordingCompactor script seam.
func keepLastFn(keepLast int) func(context.Context, string, []Message, int) ([]Message, error) {
	return func(ctx context.Context, sessionID string, messages []Message, budget int) ([]Message, error) {
		return WindowCompactor{KeepLast: keepLast}.Compact(ctx, sessionID, messages, budget)
	}
}

// replaceHistory replaces the fake history's stored conversation, which is how
// this package simulates the session controller's durable store write.
func (h *fakeHistory) replace(sessionID string, messages []Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.msgs[sessionID] = append([]Message(nil), messages...)
}

// recordingCompactionPersister is a scripted CompactionPersister. It records
// every call and, when history is set, replaces the fake history's conversation
// with the compacted surface exactly like the production store does.
type recordingCompactionPersister struct {
	mu         sync.Mutex
	calls      int
	sessionIDs []string
	priors     [][]Message
	compacted  [][]Message
	replaced   int
	err        error
	history    *fakeHistory
}

var _ CompactionPersister = (*recordingCompactionPersister)(nil)

func (p *recordingCompactionPersister) PersistCompaction(ctx context.Context, sessionID string, prior, compacted []Message) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	p.mu.Lock()
	p.calls++
	p.sessionIDs = append(p.sessionIDs, sessionID)
	p.priors = append(p.priors, cloneMessages(prior))
	p.compacted = append(p.compacted, cloneMessages(compacted))
	replaced, persistErr, history := p.replaced, p.err, p.history
	p.mu.Unlock()
	if persistErr != nil {
		return 0, persistErr
	}
	if history != nil {
		history.replace(sessionID, compacted)
	}
	return replaced, nil
}

func (p *recordingCompactionPersister) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *recordingCompactionPersister) priorAt(i int) []Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.priors) {
		return nil
	}
	return cloneMessages(p.priors[i])
}

func (p *recordingCompactionPersister) compactedAt(i int) []Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.compacted) {
		return nil
	}
	return cloneMessages(p.compacted[i])
}

// TestAgentCompactNowPersistsReplacement pins the manual /compact contract: the
// agent computes the compacted surface, hands it to the persistence seam (the
// session store), mirrors it to the checkpoint, and the next Run reads the
// compacted conversation back from history instead of the raw one.
func TestAgentCompactNowPersistsReplacement(t *testing.T) {
	hist := newFakeHistory()
	seed := manualCompactionSeed()
	hist.msgs["session-manual"] = append([]Message(nil), seed...)
	compactor := &recordingCompactor{fn: keepLastFn(2)}
	persister := &recordingCompactionPersister{replaced: 4, history: hist}
	checkpoints := newMemoryCheckpointStore()
	provider := echoProvider(t)
	agent := newTestAgent(t, Config{
		Provider:        provider,
		History:         hist,
		Compactor:       compactor,
		CheckpointStore: checkpoints,
		ContextBudget:   10000,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	replaced, saved, err := agent.CompactNow(ctx, "session-manual", persister)
	if err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	if replaced != 4 {
		t.Fatalf("replaced items = %d, want the persister's 4", replaced)
	}
	if saved <= 0 {
		t.Fatalf("estimated tokens saved = %d, want a positive value", saved)
	}
	if compactor.callCount() != 1 {
		t.Fatalf("compactor calls = %d, want 1", compactor.callCount())
	}
	if got := len(compactor.inputAt(0)); got != len(seed) {
		t.Fatalf("compactor saw %d messages, want the stored %d", got, len(seed))
	}
	if budget := compactor.budgetAt(0); budget != agent.ContextBudget() {
		t.Fatalf("compactor budget = %d, want ContextBudget() %d", budget, agent.ContextBudget())
	}
	if persister.callCount() != 1 {
		t.Fatalf("persister calls = %d, want 1", persister.callCount())
	}
	if got := persister.priorAt(0); len(got) != len(seed) {
		t.Fatalf("persister prior = %d messages, want %d", len(got), len(seed))
	}
	wantCompacted := []string{
		"system:system rules",
		"system:Earlier conversation compacted (4 messages)",
		"user:old question three",
		"assistant:old answer three",
	}
	if got := messageContents(persister.compactedAt(0)); strings.Join(got, "|") != strings.Join(wantCompacted, "|") {
		t.Fatalf("persisted compaction = %v, want %v", got, wantCompacted)
	}

	// The checkpoint mirrors the compacted surface immediately.
	cp, err := checkpoints.Load(ctx, "session-manual")
	if err != nil {
		t.Fatalf("Load checkpoint: %v", err)
	}
	if cp == nil {
		t.Fatal("manual compaction saved no checkpoint")
	}
	if got := messageContents(cp.History); strings.Join(got, "|") != strings.Join(wantCompacted, "|") {
		t.Fatalf("checkpoint history = %v, want %v", got, wantCompacted)
	}

	// The next turn is seeded from the replaced (compacted) history.
	ch, err := agent.Run(ctx, "session-manual", "next question")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	collectEvents(t, ch)

	req, ok := provider.requestAt(0)
	if !ok {
		t.Fatal("provider got no request")
	}
	wantRequest := append(append([]string(nil), wantCompacted...), "user:next question")
	if got := messageContents(req.Messages); strings.Join(got, "|") != strings.Join(wantRequest, "|") {
		t.Fatalf("provider messages = %v, want %v", got, wantRequest)
	}
	// The completed turn's checkpoint is the compacted prior plus the turn.
	cp, err = checkpoints.Load(ctx, "session-manual")
	if err != nil {
		t.Fatalf("Load checkpoint after turn: %v", err)
	}
	if got, want := len(cp.History), len(wantRequest)+1; got != want {
		t.Fatalf("checkpoint after turn has %d messages, want %d", got, want)
	}
	// History now holds the compacted surface plus the turn's messages.
	if got := len(hist.messages("session-manual")); got != len(wantRequest)+1 {
		t.Fatalf("history rows after turn = %d, want %d", got, len(wantRequest)+1)
	}
}

// TestAgentCompactNowUnavailable pins the deterministic failure sentinels the
// shell maps to stable messages.
func TestAgentCompactNowUnavailable(t *testing.T) {
	ctx := context.Background()
	newPersister := func(hist *fakeHistory) *recordingCompactionPersister {
		return &recordingCompactionPersister{replaced: 1, history: hist}
	}

	t.Run("no compactor", func(t *testing.T) {
		hist := newFakeHistory()
		hist.msgs["s"] = manualCompactionSeed()
		agent := newTestAgent(t, Config{Provider: echoProvider(t), History: hist})
		if _, _, err := agent.CompactNow(ctx, "s", newPersister(hist)); !errors.Is(err, ErrNoCompactor) {
			t.Fatalf("CompactNow without a compactor = %v, want ErrNoCompactor", err)
		}
	})

	t.Run("no persistence seam", func(t *testing.T) {
		hist := newFakeHistory()
		hist.msgs["s"] = manualCompactionSeed()
		agent := newTestAgent(t, Config{
			Provider:  echoProvider(t),
			History:   hist,
			Compactor: &recordingCompactor{fn: keepLastFn(2)},
		})
		if _, _, err := agent.CompactNow(ctx, "s", nil); !errors.Is(err, ErrNoCompactionPersistence) {
			t.Fatalf("CompactNow without persistence = %v, want ErrNoCompactionPersistence", err)
		}
	})

	t.Run("empty history", func(t *testing.T) {
		agent := newTestAgent(t, Config{
			Provider:  echoProvider(t),
			History:   newFakeHistory(),
			Compactor: &recordingCompactor{fn: keepLastFn(2)},
		})
		if _, _, err := agent.CompactNow(ctx, "s", &recordingCompactionPersister{}); !errors.Is(err, ErrNoCompactableHistory) {
			t.Fatalf("CompactNow on empty history = %v, want ErrNoCompactableHistory", err)
		}
	})

	t.Run("compactor leaves history unchanged", func(t *testing.T) {
		hist := newFakeHistory()
		hist.msgs["s"] = []Message{
			{Role: RoleSystem, Content: "rules"},
			{Role: RoleUser, Content: "hi"},
		}
		agent := newTestAgent(t, Config{
			Provider:  echoProvider(t),
			History:   hist,
			Compactor: &recordingCompactor{fn: keepLastFn(8)},
		})
		persister := newPersister(hist)
		replaced, saved, err := agent.CompactNow(ctx, "s", persister)
		if !errors.Is(err, ErrNoCompactableHistory) {
			t.Fatalf("CompactNow with nothing to compact = %d/%d/%v, want ErrNoCompactableHistory", replaced, saved, err)
		}
		if persister.callCount() != 0 {
			t.Fatalf("no-op compaction persisted %d times", persister.callCount())
		}
	})

	t.Run("compactor failure propagates", func(t *testing.T) {
		hist := newFakeHistory()
		hist.msgs["s"] = manualCompactionSeed()
		boom := errors.New("summarizer exploded")
		agent := newTestAgent(t, Config{
			Provider:  echoProvider(t),
			History:   hist,
			Compactor: &recordingCompactor{err: boom},
		})
		if _, _, err := agent.CompactNow(ctx, "s", newPersister(hist)); !errors.Is(err, boom) {
			t.Fatalf("CompactNow compactor failure = %v, want the compactor error", err)
		}
	})

	t.Run("persistence failure is reported", func(t *testing.T) {
		hist := newFakeHistory()
		hist.msgs["s"] = manualCompactionSeed()
		agent := newTestAgent(t, Config{
			Provider:  echoProvider(t),
			History:   hist,
			Compactor: &recordingCompactor{fn: keepLastFn(2)},
		})
		persister := &recordingCompactionPersister{err: errors.New("store write failed")}
		if _, _, err := agent.CompactNow(ctx, "s", persister); !errors.Is(err, ErrCompactionNotSaved) {
			t.Fatalf("CompactNow persistence failure = %v, want ErrCompactionNotSaved", err)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		hist := newFakeHistory()
		hist.msgs["s"] = manualCompactionSeed()
		agent := newTestAgent(t, Config{
			Provider:  echoProvider(t),
			History:   hist,
			Compactor: &recordingCompactor{fn: keepLastFn(2)},
		})
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := agent.CompactNow(canceled, "s", newPersister(hist)); !errors.Is(err, context.Canceled) {
			t.Fatalf("CompactNow with a cancelled context = %v, want context.Canceled", err)
		}
	})

	t.Run("missing session id", func(t *testing.T) {
		hist := newFakeHistory()
		hist.msgs["s"] = manualCompactionSeed()
		agent := newTestAgent(t, Config{
			Provider:  echoProvider(t),
			History:   hist,
			Compactor: &recordingCompactor{fn: keepLastFn(2)},
		})
		if _, _, err := agent.CompactNow(ctx, "  ", newPersister(hist)); err == nil {
			t.Fatal("CompactNow accepted an empty session id")
		}
	})
}

// TestAgentCompactNowBusyWhileTurnRuns pins the runMu exclusion: a manual
// compaction must not interleave with a turn that is already driving the
// shared topology, and nothing is persisted in that state.
func TestAgentCompactNowBusyWhileTurnRuns(t *testing.T) {
	hist := newFakeHistory()
	hist.msgs["session-busy"] = manualCompactionSeed()
	started := make(chan struct{})
	release := make(chan struct{})
	provider := &fakeProvider{fn: func(_ context.Context, _ Request) ([]Chunk, error) {
		close(started)
		<-release
		return []Chunk{{Text: "finished", Done: true}}, nil
	}}
	compactor := &recordingCompactor{fn: keepLastFn(2)}
	persister := &recordingCompactionPersister{replaced: 2, history: hist}
	agent := newTestAgent(t, Config{
		Provider:      provider,
		History:       hist,
		Compactor:     compactor,
		ContextBudget: 10000,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch, err := agent.Run(ctx, "session-busy", "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("turn never reached the provider")
	}
	if _, _, err := agent.CompactNow(ctx, "session-busy", persister); !errors.Is(err, ErrCompactionBusy) {
		t.Fatalf("CompactNow while a turn runs = %v, want ErrCompactionBusy", err)
	}
	if compactor.callCount() != 0 || persister.callCount() != 0 {
		t.Fatalf("busy CompactNow compacted %d/persisted %d times", compactor.callCount(), persister.callCount())
	}

	close(release)
	collectEvents(t, ch)

	// Once the turn finished the agent is idle and the manual compaction runs.
	replaced, saved, err := agent.CompactNow(ctx, "session-busy", persister)
	if err != nil {
		t.Fatalf("CompactNow after the turn: %v", err)
	}
	if replaced <= 0 || saved <= 0 {
		t.Fatalf("idle CompactNow = %d/%d, want a positive replacement and savings", replaced, saved)
	}
	if persister.callCount() != 1 {
		t.Fatalf("idle CompactNow persisted %d times, want 1", persister.callCount())
	}
}

// TestCompactionSummaryRecognitionCoversPersistedRole pins the persisted
// summary's recognizer: the /compact row is stored with the assistant role (the
// app's storeHistory maps only user/assistant rows), so a reopened session's
// summary must be recognized by content for both the runtime's system summary
// and the persisted assistant row, and never for ordinary text.
func TestCompactionSummaryRecognitionCoversPersistedRole(t *testing.T) {
	framed := compactionSummaryPrefix + "7 messages)\n\nold summary text"
	for _, role := range []Role{RoleSystem, RoleAssistant} {
		if !isCompactionSummary(Message{Role: role, Content: framed}) {
			t.Fatalf("isCompactionSummary(%s framed summary) = false", role)
		}
	}
	if isCompactionSummary(Message{Role: RoleUser, Content: framed}) {
		t.Fatal("a user row must not be treated as a compaction summary")
	}
	if isCompactionSummary(Message{Role: RoleAssistant, Content: "Earlier conversation compacted but not really"}) {
		t.Fatal("ordinary assistant text was treated as a compaction summary")
	}
}

// TestCompactorsReplacePersistedAssistantSummary is the auto-compact regression
// for a reopened session: the previous /compact summary is an assistant row, so
// a later compaction must drop it and emit exactly one fresh summary instead of
// keeping the old one as a normal answer.
func TestCompactorsReplacePersistedAssistantSummary(t *testing.T) {
	persisted := []Message{
		{Role: RoleAssistant, Content: compactionSummaryPrefix + "7 messages)\n\nold summary text"},
		{Role: RoleUser, Content: "recent question"},
		{Role: RoleAssistant, Content: "recent answer"},
		{Role: RoleUser, Content: "newer question"},
	}
	assertSingleFreshSummary := func(t *testing.T, out []Message, wantNote string) {
		t.Helper()
		got := messageContents(out)
		summaries := 0
		for i, msg := range out {
			if isCompactionSummary(msg) {
				summaries++
				if !strings.Contains(msg.Content, wantNote) {
					t.Fatalf("summary %d = %q, want the fresh marker %q", i, msg.Content, wantNote)
				}
			}
		}
		if summaries != 1 {
			t.Fatalf("compacted conversation has %d summaries, want exactly 1: %v", summaries, got)
		}
		if len(out) == 0 || out[0].Role != RoleSystem || !strings.Contains(out[0].Content, "2 messages") {
			t.Fatalf("compacted[0] = %v, want the fresh system summary: %v", out[0], got)
		}
		if last := got[len(got)-1]; last != "user:newer question" {
			t.Fatalf("compacted tail = %q, want the kept newest message", last)
		}
	}

	window, err := WindowCompactor{KeepLast: 1}.Compact(context.Background(), "s", persisted, 100)
	if err != nil {
		t.Fatalf("WindowCompactor.Compact: %v", err)
	}
	assertSingleFreshSummary(t, window, compactionSummaryPrefix+"2 messages)")

	p := &fakeProvider{fn: func(_ context.Context, req Request) ([]Chunk, error) {
		for _, msg := range req.Messages {
			if msg.Content == LLMCompactorSystemPrompt {
				return []Chunk{{Text: "RESUMMARIZED", Done: true}}, nil
			}
		}
		return []Chunk{{Text: "reply", Done: true}}, nil
	}}
	llm, err := LLMCompactor{Provider: p, KeepLast: 1}.Compact(context.Background(), "s", persisted, 100)
	if err != nil {
		t.Fatalf("LLMCompactor.Compact: %v", err)
	}
	assertSingleFreshSummary(t, llm, "RESUMMARIZED")
}

// TestCompactionSummaryContent frames a persisted summary so a reopen always
// stores a recognizable row, even when a custom compactor's summary text does
// not carry the stable marker.
func TestCompactionSummaryContent(t *testing.T) {
	framed := compactionSummaryPrefix + "9 messages)\n\nkept"
	if got := CompactionSummaryContent(3, framed); got != framed {
		t.Fatalf("CompactionSummaryContent(already framed) = %q, want unchanged %q", got, framed)
	}
	want := compactionSummaryPrefix + "4 messages)\n\ncustom digest"
	if got := CompactionSummaryContent(4, "custom digest"); got != want {
		t.Fatalf("CompactionSummaryContent(custom) = %q, want %q", got, want)
	}
	wantCountOnly := compactionSummaryPrefix + "2 messages)"
	if got := CompactionSummaryContent(2, ""); got != wantCountOnly {
		t.Fatalf("CompactionSummaryContent(empty) = %q, want %q", got, wantCountOnly)
	}
}
