package agent

// Tests for the product-side pre-run compaction: archive + exact compact event
// before the run, no duplicated user message/tool replay, non-blocking failures,
// and no compaction inside the budget.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// precompactProvider answers compaction-summary requests with a fixed summary
// and normal requests from a script, recording the last normal request so the
// test can assert the model saw the compacted history.
type precompactProvider struct {
	mu                sync.Mutex
	steps             []scriptStep
	normalCalls       int
	summaryCalls      int
	lastNormalRequest rig.Request
}

func (p *precompactProvider) Name() string { return "precompact-script" }

func (p *precompactProvider) Stream(_ context.Context, request rig.Request) (rig.Stream, error) {
	if isCompactionSummaryRequest(request) {
		p.mu.Lock()
		p.summaryCalls++
		p.mu.Unlock()
		return &scriptedStream{chunks: []rig.Chunk{{Delta: "PRE-RUN SUMMARY"}}}, nil
	}
	p.mu.Lock()
	index := p.normalCalls
	p.normalCalls++
	p.lastNormalRequest = request
	p.mu.Unlock()
	if index < len(p.steps) {
		step := p.steps[index]
		if step.err != nil {
			return nil, step.err
		}
		return &scriptedStream{chunks: step.chunks}, nil
	}
	return &scriptedStream{chunks: []rig.Chunk{{Delta: "ok"}, {FinishReason: "stop"}}}, nil
}

func (p *precompactProvider) calls() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.normalCalls, p.summaryCalls
}

// failingCompactor always fails, for the non-blocking failure path.
type failingCompactor struct{ err error }

func (c failingCompactor) Compact(context.Context, string, []rig.Message, int) ([]rig.Message, error) {
	return nil, c.err
}

// newPrecompactService builds a service with an explicit context budget and the
// scripted provider, over the given durable store.
func newPrecompactService(t *testing.T, store *fakeStore, budget int, provider rig.Provider, tools ...rig.Tool) *service {
	t.Helper()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	svc := newService(Config{ContextBudget: budget}, rigtools.NewRegistry(tools...),
		contracts.SettingsService(settings), &fakeLogger{}, store, fakeApprovals{})
	if provider != nil {
		svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	}
	return svc
}

// seedBigHistory writes count large prior messages into the durable history.
func seedBigHistory(t *testing.T, store *fakeStore, sessionID string, count int) []rig.Message {
	t.Helper()
	messages := make([]rig.Message, 0, count)
	for index := 0; index < count; index++ {
		role := rig.RoleUser
		if index%2 == 1 {
			role = rig.RoleAssistant
		}
		messages = append(messages, rig.NewMessage(role, strings.Repeat("x", 300)+"-"+string(rune('a'+index%26))))
	}
	if err := store.ReplaceHistory(context.Background(), sessionID, messages); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	return messages
}

// compactEventsOfType decodes the durable compact events of one session.
func compactEventsOfType(t *testing.T, store *fakeStore, sessionID string) []compactPayload {
	t.Helper()
	events := eventsOfType(t, store, sessionID, compactEvent)
	out := make([]compactPayload, 0, len(events))
	for _, event := range events {
		var payload compactPayload
		decodeEvent(t, event, &payload)
		out = append(out, payload)
	}
	return out
}

// foldedCompactEvents returns the compact events of the ContextInspector timeline.
func foldedCompactEvents(t *testing.T, svc *service, sessionID string) []contracts.ContextEvent {
	t.Helper()
	folded, ok := svc.SessionContext(sessionID)
	if !ok {
		t.Fatal("SessionContext reported no timeline")
	}
	out := make([]contracts.ContextEvent, 0, 1)
	for _, event := range folded.Events {
		if event.Kind == contracts.ContextEventCompact {
			out = append(out, event)
		}
	}
	return out
}

// assertFoldedCompactOutcome pins that the read model carries the same outcome as
// the durable payload, live and after a restart replay.
func assertFoldedCompactOutcome(t *testing.T, store *fakeStore, svc *service, sessionID, wantOutcome string) {
	t.Helper()
	durable := compactEventsOfType(t, store, sessionID)
	if len(durable) != 1 {
		t.Fatalf("durable compact events = %+v, want 1", durable)
	}
	folded := foldedCompactEvents(t, svc, sessionID)
	if len(folded) != 1 {
		t.Fatalf("folded compact events = %+v, want 1", folded)
	}
	if folded[0].Outcome != wantOutcome || folded[0].Outcome != durable[0].Outcome {
		t.Fatalf("folded Outcome = %q, durable = %q, want %q", folded[0].Outcome, durable[0].Outcome, wantOutcome)
	}
	// Restart replay keeps the outcome (the fold is shared by live and restore).
	restarted := newService(Config{}, rigtools.NewRegistry(),
		contracts.SettingsService(newFakeSettings(nil)), &fakeLogger{}, store, fakeApprovals{})
	restarted.restore(context.Background())
	replayed := foldedCompactEvents(t, restarted, sessionID)
	if len(replayed) != 1 || replayed[0].Outcome != wantOutcome {
		t.Fatalf("replayed compact events = %+v, want Outcome %q", replayed, wantOutcome)
	}
}

// identityCompactor reports no reduction (it returns the messages unchanged),
// exercising the no-reduction outcome path.
type identityCompactor struct{}

func (identityCompactor) Compact(_ context.Context, _ string, messages []rig.Message, _ int) ([]rig.Message, error) {
	return append([]rig.Message(nil), messages...), nil
}

// TestPreRunCompactArchivesAndRecordsExactEvent covers the happy path.
func TestPreRunCompactArchivesAndRecordsExactEvent(t *testing.T) {
	store := newFakeStore()
	provider := &precompactProvider{}
	svc := newPrecompactService(t, store, 800, provider)
	sessionID, err := svc.NewSession("pre compact")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	seeded := seedBigHistory(t, store, sessionID, 14)
	if err := svc.Send(sessionID, "new question"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	if len(session.Messages) == 0 {
		t.Fatal("run produced no messages")
	}
	assistant := session.Messages[len(session.Messages)-1]
	if assistant.Error != "" || assistant.Text != "ok" {
		t.Fatalf("assistant = %+v, want a successful run after the pre-compaction", assistant)
	}
	if users := userMessages(session); len(users) != 1 {
		t.Fatalf("user messages = %d, want exactly one (pre-compaction must not duplicate it)", len(users))
	}

	// Exact durable compact event (kind auto, inferred=false, real counts).
	compacts := compactEventsOfType(t, store, sessionID)
	if len(compacts) != 1 {
		t.Fatalf("compact events = %+v, want exactly the pre-run one", compacts)
	}
	compactEvent := compacts[0]
	if compactEvent.Kind != "auto" || compactEvent.Inferred || compactEvent.Outcome != "" {
		t.Fatalf("compact event = %+v, want an exact successful auto event", compactEvent)
	}
	if compactEvent.Before <= compactEvent.After || compactEvent.Replaced != len(seeded) ||
		compactEvent.Saved >= compactEvent.Replaced || compactEvent.Saved == 0 {
		t.Fatalf("compact event = %+v, want real before/after/replaced/saved counts", compactEvent)
	}
	archives := decodeArchiveEvents(t, store, sessionID)
	if len(archives) != 1 || archives[0].Kind != "auto" || archives[0].MessageCount != len(seeded) ||
		archives[0].RetainedMessages != len(seeded) {
		t.Fatalf("archives = %+v, want the full raw auto archive", archives)
	}
	// Durable history is compacted (compacted messages + the new turn).
	history, err := store.History().Load(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history) >= len(seeded)+2 {
		t.Fatalf("history = %d messages, want the compacted history + new turn", len(history))
	}
	// The model's first normal request saw the compacted history: the summary is
	// present, the old bulk text is gone and the new user message appears once.
	normalCalls, summaryCalls := provider.calls()
	if normalCalls != 1 || summaryCalls != 1 {
		t.Fatalf("provider calls = %d normal/%d summary, want 1/1", normalCalls, summaryCalls)
	}
	request := provider.lastNormalRequest
	if len(request.Messages) >= len(seeded)+1 {
		t.Fatalf("first model request messages = %d, want the compacted prefix", len(request.Messages))
	}
	joined := ""
	for _, message := range request.Messages {
		joined += message.Text() + "\n"
	}
	if !strings.Contains(joined, "PRE-RUN SUMMARY") {
		t.Fatalf("first model request lacks the compaction summary: %q", joined)
	}
	if strings.Count(joined, strings.Repeat("x", 300)+"-a") > 0 && strings.Count(joined, strings.Repeat("x", 300)) > 0 {
		t.Fatalf("first model request still carries the pre-compaction bulk history: %q", joined)
	}
	if strings.Count(joined, "new question") != 1 {
		t.Fatalf("first model request has %d copies of the new user message, want 1", strings.Count(joined, "new question"))
	}
	// ContextInspector: exact compact + auto archive metadata; a successful
	// compaction has an empty Outcome in both the durable payload and the fold.
	assertFoldedCompactOutcome(t, store, svc, sessionID, "")
	folded, ok := svc.SessionContext(sessionID)
	if !ok {
		t.Fatal("SessionContext reported no timeline")
	}
	var sawArchive bool
	for _, event := range folded.Events {
		if event.Kind == contracts.ContextEventCompact && (event.Detail != "auto" || event.Inferred) {
			t.Fatalf("folded compact event = %+v, want exact auto", event)
		}
		if event.Kind == contracts.ContextEventArchive && event.ArchivedMessages == len(seeded) {
			sawArchive = true
		}
	}
	if !sawArchive {
		t.Fatalf("inspector events = %+v, want the auto archive metadata", folded.Events)
	}

	// A later turn inside the (now small) budget does not compact again.
	if err := svc.Send(sessionID, "second question"); err != nil {
		t.Fatalf("second Send: %v", err)
	}
	waitIdle(t, svc, sessionID)
	if compacts := compactEventsOfType(t, store, sessionID); len(compacts) != 1 {
		t.Fatalf("compact events after a within-budget turn = %d, want 1", len(compacts))
	}
}

// TestPreRunCompactSkipsWithinBudget covers the no-op path.
func TestPreRunCompactSkipsWithinBudget(t *testing.T) {
	store := newFakeStore()
	provider := &precompactProvider{}
	svc := newPrecompactService(t, store, 1_000_000, provider)
	sessionID, _ := svc.NewSession("within budget")
	seedBigHistory(t, store, sessionID, 4)
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)
	normalCalls, summaryCalls := provider.calls()
	if normalCalls != 1 || summaryCalls != 0 {
		t.Fatalf("provider calls = %d/%d, want no compaction", normalCalls, summaryCalls)
	}
	if compacts := compactEventsOfType(t, store, sessionID); len(compacts) != 0 {
		t.Fatalf("compact events = %+v, want none within budget", compacts)
	}
	if archives := decodeArchiveEvents(t, store, sessionID); len(archives) != 0 {
		t.Fatalf("archives = %+v, want none within budget", archives)
	}
}

// TestPreRunCompactFailureDoesNotBlockRun covers a failing compactor: the run
// still proceeds with the original history and records the outcome.
func TestPreRunCompactFailureDoesNotBlockRun(t *testing.T) {
	store := newFakeStore()
	provider := &precompactProvider{}
	svc := newPrecompactService(t, store, 300, provider)
	svc.compactorFactory = func(contracts.ChatConfig) (rig.Compactor, error) {
		return failingCompactor{err: errors.New("summary backend down")}, nil
	}
	sessionID, _ := svc.NewSession("compact fails")
	seeded := seedBigHistory(t, store, sessionID, 12)
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	if assistant := session.Messages[len(session.Messages)-1]; assistant.Error != "" || assistant.Text != "ok" {
		t.Fatalf("assistant = %+v, want the run to proceed after a failed compaction", assistant)
	}
	compacts := compactEventsOfType(t, store, sessionID)
	if len(compacts) != 1 || compacts[0].Outcome != compactOutcomeFailed || compacts[0].After != compacts[0].Before {
		t.Fatalf("compact events = %+v, want one failed outcome", compacts)
	}
	assertFoldedCompactOutcome(t, store, svc, sessionID, compactOutcomeFailed)
	if archives := decodeArchiveEvents(t, store, sessionID); len(archives) != 0 {
		t.Fatalf("archives after a failed compaction = %+v, want none", archives)
	}
	history, _ := store.History().Load(context.Background(), sessionID)
	if len(history) != len(seeded)+2 {
		t.Fatalf("history after a failed compaction = %d, want the untouched history + turn", len(history))
	}
}

// archiveFailAutoStore fails only the auto compaction archive.
type archiveFailAutoStore struct{ *fakeStore }

func (s *archiveFailAutoStore) AppendEvent(ctx context.Context, event rig.SessionEvent) error {
	if event.Type == compactArchiveEventType {
		return errors.New("audit disk full")
	}
	return s.fakeStore.AppendEvent(ctx, event)
}

// TestPreRunCompactArchiveFailureDoesNotBlockRun covers the non-blocking archive
// rule: no archive means no replacement, but the run continues.
func TestPreRunCompactArchiveFailureDoesNotBlockRun(t *testing.T) {
	store := &archiveFailAutoStore{fakeStore: newFakeStore()}
	provider := &precompactProvider{}
	svc := newPrecompactService(t, store.fakeStore, 300, provider)
	svc.store = store
	sessionID, _ := svc.NewSession("archive fails")
	seeded := seedBigHistory(t, store.fakeStore, sessionID, 12)
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	if assistant := session.Messages[len(session.Messages)-1]; assistant.Error != "" || assistant.Text != "ok" {
		t.Fatalf("assistant = %+v, want the run to proceed without the archive", assistant)
	}
	compacts := compactEventsOfType(t, store.fakeStore, sessionID)
	if len(compacts) != 1 || compacts[0].Outcome != compactOutcomeArchiveFailed {
		t.Fatalf("compact events = %+v, want the archive-failed outcome", compacts)
	}
	assertFoldedCompactOutcome(t, store.fakeStore, svc, sessionID, compactOutcomeArchiveFailed)
	history, _ := store.fakeStore.History().Load(context.Background(), sessionID)
	if len(history) != len(seeded)+2 {
		t.Fatalf("history after a failed archive = %d, want the untouched history + turn", len(history))
	}
}

// TestPreRunCompactNoReductionRecordsOutcome covers the no-reduction path: the
// read model must explain the skipped compaction (not pretend it compacted).
func TestPreRunCompactNoReductionRecordsOutcome(t *testing.T) {
	store := newFakeStore()
	provider := &precompactProvider{}
	svc := newPrecompactService(t, store, 300, provider)
	svc.compactorFactory = func(contracts.ChatConfig) (rig.Compactor, error) {
		return identityCompactor{}, nil
	}
	sessionID, _ := svc.NewSession("no reduction")
	seeded := seedBigHistory(t, store, sessionID, 12)
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	if assistant := session.Messages[len(session.Messages)-1]; assistant.Error != "" || assistant.Text != "ok" {
		t.Fatalf("assistant = %+v, want the run to proceed after a no-reduction compaction", assistant)
	}
	compacts := compactEventsOfType(t, store, sessionID)
	if len(compacts) != 1 || compacts[0].Outcome != compactOutcomeNoReduction {
		t.Fatalf("compact events = %+v, want the no-reduction outcome", compacts)
	}
	assertFoldedCompactOutcome(t, store, svc, sessionID, compactOutcomeNoReduction)
	history, _ := store.History().Load(context.Background(), sessionID)
	if len(history) != len(seeded)+2 {
		t.Fatalf("history after a no-reduction attempt = %d, want the untouched history + turn", len(history))
	}
}
