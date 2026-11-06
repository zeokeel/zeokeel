package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// echoTool is the single test tool. It is registered for every turn so the
// Provider request can be checked for its ToolSpec.
type echoTool struct{}

func (echoTool) Name() string        { return "echo" }
func (echoTool) Description() string { return "Echoes its JSON arguments." }
func (echoTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	return string(args), nil
}

// fakeProvider is a scripted runtime.Provider. respond produces the chunks for
// one model call; block keeps the stream open until the turn context is
// cancelled, which is how the Stop tests hold a turn in flight.
type fakeProvider struct {
	respond func(ctx context.Context, req runtime.Request, call int) []runtime.Chunk
	block   atomic.Bool
	// streamErr makes the provider fail before a stream exists, which the
	// runtime turns into a failed turn.
	streamErr error

	mu       sync.Mutex
	reqs     []runtime.Request
	canceled atomic.Bool
}

var _ runtime.Provider = (*fakeProvider)(nil)

func (p *fakeProvider) Stream(ctx context.Context, req runtime.Request) (<-chan runtime.Chunk, error) {
	p.mu.Lock()
	call := len(p.reqs)
	p.reqs = append(p.reqs, req)
	p.mu.Unlock()

	if p.streamErr != nil {
		return nil, p.streamErr
	}

	var chunks []runtime.Chunk
	if p.respond != nil {
		chunks = p.respond(ctx, req, call)
	}

	out := make(chan runtime.Chunk)
	go func() {
		defer close(out)
		for _, chunk := range chunks {
			select {
			case out <- chunk:
			case <-ctx.Done():
				p.canceled.Store(true)
				return
			}
		}
		if p.block.Load() {
			<-ctx.Done()
			p.canceled.Store(true)
		}
	}()
	return out, nil
}

func (p *fakeProvider) requestAt(i int) (runtime.Request, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.reqs) {
		return runtime.Request{}, false
	}
	return p.reqs[i], true
}

func (p *fakeProvider) requestCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.reqs)
}

// echoRespond streams an "echo:<last user message>" reply in three chunks so
// intermediate snapshots can be observed.
func echoRespond(_ context.Context, req runtime.Request, _ int) []runtime.Chunk {
	last := ""
	for _, msg := range req.Messages {
		if msg.Role == runtime.RoleUser {
			last = msg.Content
		}
	}
	text := "echo:" + last
	parts := split3(text)
	chunks := make([]runtime.Chunk, 0, len(parts))
	for i, part := range parts {
		chunk := runtime.Chunk{Text: part}
		if i == len(parts)-1 {
			chunk.Done = true
			chunk.Usage = &runtime.Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10}
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}

func split3(s string) []string {
	if len(s) < 3 {
		return []string{s}
	}
	third := len(s) / 3
	return []string{s[:third], s[third : 2*third], s[2*third:]}
}

// snapshotCollector stores every snapshot passed to OnUpdate. Snapshots are
// immutable, so retaining the pointer is safe.
type snapshotCollector struct {
	mu    sync.Mutex
	snaps []*state.Snapshot
}

func (c *snapshotCollector) add(snap *state.Snapshot) {
	c.mu.Lock()
	c.snaps = append(c.snaps, snap)
	c.mu.Unlock()
}

func (c *snapshotCollector) all() []*state.Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*state.Snapshot, len(c.snaps))
	copy(out, c.snaps)
	return out
}

type harness struct {
	t         *testing.T
	dir       string
	store     *store.Store
	broker    *state.Broker
	projector *state.Projector
	agent     *runtime.Agent
	gate      *runtime.ApprovalQueue
	workspace string
	ctrl      *session.Controller
}

func newHarness(t *testing.T, provider runtime.Provider) *harness {
	return newHarnessWithGate(t, provider, nil)
}

func newHarnessWithGate(t *testing.T, provider runtime.Provider, gate *runtime.ApprovalQueue) *harness {
	return newHarnessConfigured(t, provider, func(cfg *runtime.Config) { cfg.Gate = gate })
}

// newHarnessConfigured is newHarnessWithGate with a callback that may set any
// additional runtime.Config field, used by tests that wire automatic review.
func newHarnessConfigured(t *testing.T, provider runtime.Provider, configure func(*runtime.Config)) *harness {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	ws, err := st.CreateWorkspace("test", dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	registry := runtime.NewRegistry()
	if err := registry.Register(echoTool{}); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	cfg := runtime.Config{
		Provider:    provider,
		Tools:       registry,
		Instruction: "test agent",
	}
	if configure != nil {
		configure(&cfg)
	}
	agent, err := runtime.New(cfg)
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}

	// The harness only exposes the concrete approval queue; tests that wire a
	// different Gate inspect it through their own closure.
	approvalGate, _ := cfg.Gate.(*runtime.ApprovalQueue)
	h := &harness{t: t, dir: dir, store: st, agent: agent, gate: approvalGate, workspace: ws.ID}
	h.newController()
	t.Cleanup(h.close)
	return h
}

func (h *harness) newController() *session.Controller {
	h.t.Helper()
	h.broker = state.NewBroker()
	h.projector = state.NewProjector(context.Background(), h.broker)
	ctrl, err := session.New(session.Config{
		Store:       h.store,
		Broker:      h.broker,
		Projector:   h.projector,
		Agent:       h.agent,
		Gate:        h.gate,
		WorkspaceID: h.workspace,
	})
	if err != nil {
		h.t.Fatalf("session.New: %v", err)
	}
	h.ctrl = ctrl
	return ctrl
}

// restart replaces the controller, broker and projector but keeps the store
// and agent, simulating an app restart over the same data directory.
func (h *harness) restart() *session.Controller {
	h.t.Helper()
	if h.ctrl != nil {
		_ = h.ctrl.Close()
	}
	if h.projector != nil {
		h.projector.Close()
	}
	if h.broker != nil {
		h.broker.Close()
	}
	return h.newController()
}

func (h *harness) close() {
	if h.ctrl != nil {
		_ = h.ctrl.Close()
	}
	if h.projector != nil {
		h.projector.Close()
	}
	if h.broker != nil {
		h.broker.Close()
	}
	if h.store != nil {
		_ = h.store.Close()
	}
}

func waitPhase(t *testing.T, ctrl *session.Controller, phase state.Phase) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ctrl.Snapshot().Phase == phase {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for phase %q, last snapshot: %+v", phase, ctrl.Snapshot())
}

func waitIdleAndDrain(t *testing.T, ctrl *session.Controller) {
	t.Helper()
	// "Idle and drained" is only stable once no turn is registered and the
	// snapshot is idle again. A terminal Idle can be observed while the goal
	// driver's automatic continuation is being accepted (the driver hands the
	// slot over atomically, but the continuation's snapshot may not be running
	// yet), so Wait can wake on the finished turn and still find the
	// continuation registered: re-check instead of returning on the stale Idle.
	deadline := time.Now().Add(10 * time.Second)
	for {
		waitPhase(t, ctrl, state.PhaseIdle)
		ctrl.Wait()
		if ctrl.Snapshot().Phase == state.PhaseIdle {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session did not settle after waiting for its continuations: %+v", ctrl.Snapshot())
		}
	}
}

func waitAssistantText(t *testing.T, ctrl *session.Controller, substr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		snap := ctrl.Snapshot()
		for _, msg := range snap.Messages {
			if msg.Role == state.RoleAssistant && strings.Contains(msg.Text, substr) {
				return
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for assistant text %q; snapshot: %+v", substr, ctrl.Snapshot())
}

func waitApproval(t *testing.T, ctrl *session.Controller, callID string) state.ApprovalRequest {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, req := range ctrl.PendingApprovals() {
			if req.CallID == callID {
				return req
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for approval %q, pending: %+v", callID, ctrl.PendingApprovals())
	return state.ApprovalRequest{}
}

// approvalRespond requests the echo tool on the first model call and answers
// with text once the tool result comes back.
func approvalRespond(_ context.Context, _ runtime.Request, call int) []runtime.Chunk {
	if call == 0 {
		return []runtime.Chunk{{
			ToolCalls: []runtime.ToolCall{{
				ID:        "call-1",
				Name:      "echo",
				Arguments: json.RawMessage(`{"path":"README.md"}`),
			}},
			Done: true,
		}}
	}
	return []runtime.Chunk{{Text: "done", Done: true}}
}

func TestNewValidation(t *testing.T) {
	if _, err := session.New(session.Config{}); !errors.Is(err, session.ErrInvalidConfig) {
		t.Fatalf("New(empty) error = %v, want ErrInvalidConfig", err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := session.New(session.Config{Store: st}); !errors.Is(err, session.ErrInvalidConfig) {
		t.Fatalf("New(nil broker) error = %v, want ErrInvalidConfig", err)
	}
}

func TestTwoTurnConversationPersistsMessagesAndEvents(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	h := newHarness(t, provider)
	ctx := context.Background()

	id, err := h.ctrl.CreateSession(ctx, "two turns")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if id == "" {
		t.Fatal("CreateSession returned an empty id")
	}
	seed := h.ctrl.Snapshot()
	if seed.SessionID != id {
		t.Fatalf("seed SessionID = %q, want %q", seed.SessionID, id)
	}
	if seed.Phase != state.PhaseIdle {
		t.Fatalf("seed Phase = %q, want idle", seed.Phase)
	}

	collector := &snapshotCollector{}
	h.ctrl.OnUpdate(collector.add)

	if err := h.ctrl.Submit(ctx, id, "hello"); err != nil {
		t.Fatalf("Submit(hello): %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	if err := h.ctrl.Submit(ctx, id, "again"); err != nil {
		t.Fatalf("Submit(again): %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	want := []struct {
		role state.Role
		text string
	}{
		{state.RoleUser, "hello"},
		{state.RoleAssistant, "echo:hello"},
		{state.RoleUser, "again"},
		{state.RoleAssistant, "echo:again"},
	}
	snap := h.ctrl.Snapshot()
	if snap.SessionID != id || snap.Phase != state.PhaseIdle {
		t.Fatalf("final snapshot header = %q/%q", snap.SessionID, snap.Phase)
	}
	if len(snap.Messages) != len(want) {
		t.Fatalf("snapshot messages = %d, want %d: %+v", len(snap.Messages), len(want), snap.Messages)
	}
	for i, w := range want {
		if snap.Messages[i].Role != w.role || snap.Messages[i].Text != w.text {
			t.Fatalf("snapshot message %d = %+v, want %+v", i, snap.Messages[i], w)
		}
	}

	// The messages table must contain the same two-turn transcript.
	stored, err := h.store.ListMessages(id)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(stored) != len(want) {
		t.Fatalf("stored messages = %d, want %d: %+v", len(stored), len(want), stored)
	}
	turnIDs := map[string]bool{}
	for i, w := range want {
		if stored[i].Role != string(w.role) || stored[i].Content != w.text {
			t.Fatalf("stored message %d = %+v, want %+v", i, stored[i], w)
		}
		if stored[i].TurnID == "" {
			t.Fatalf("stored message %d has no turn id", i)
		}
		turnIDs[stored[i].TurnID] = true
	}
	if len(turnIDs) != 2 {
		t.Fatalf("turn ids = %v, want two distinct turns", turnIDs)
	}

	// Events must include the prompt marker, streamed tokens, status and usage.
	events, err := h.store.ListEvents(id)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	counts := map[string]int{}
	for _, ev := range events {
		counts[ev.Type]++
		if ev.Seq <= 0 {
			t.Fatalf("event %+v has non-positive seq", ev)
		}
	}
	for _, typ := range []string{session.EventMessage, session.EventToken, session.EventStatus, session.EventUsage} {
		if counts[typ] == 0 {
			t.Fatalf("no %q events; counts: %v", typ, counts)
		}
	}
	if counts[session.EventMessage] != 2 {
		t.Fatalf("message events = %d, want 2 (one per prompt)", counts[session.EventMessage])
	}
	if counts[session.EventToken] < 6 {
		t.Fatalf("token events = %d, want at least 6", counts[session.EventToken])
	}
	if counts[session.EventStatus] < 4 {
		t.Fatalf("status events = %d, want at least 4", counts[session.EventStatus])
	}

	sess, err := h.store.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Status != store.SessionIdle {
		t.Fatalf("persisted status = %q, want idle", sess.Status)
	}

	// The provider saw both turns, routed to this session, with the echo tool.
	if got := provider.requestCount(); got != 2 {
		t.Fatalf("provider calls = %d, want 2", got)
	}
	for i := 0; i < 2; i++ {
		req, ok := provider.requestAt(i)
		if !ok {
			t.Fatalf("missing provider request %d", i)
		}
		if req.SessionID != id {
			t.Fatalf("request %d SessionID = %q, want %q", i, req.SessionID, id)
		}
		if len(req.Tools) != 1 || req.Tools[0].Name != "echo" {
			t.Fatalf("request %d tools = %+v, want echo", i, req.Tools)
		}
	}

	if len(collector.all()) == 0 {
		t.Fatal("OnUpdate never fired")
	}
}

func TestStreamingTokensAccumulateInSnapshots(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	h := newHarness(t, provider)
	ctx := context.Background()

	id, err := h.ctrl.CreateSession(ctx, "stream")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	collector := &snapshotCollector{}
	h.ctrl.OnUpdate(collector.add)

	if err := h.ctrl.Submit(ctx, id, "hello"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	full := "echo:hello"
	sawPartial, sawFull := false, false
	for _, snap := range collector.all() {
		for _, msg := range snap.Messages {
			if msg.Role != state.RoleAssistant {
				continue
			}
			if msg.Text == full {
				sawFull = true
			}
			if msg.Text != "" && msg.Text != full && strings.HasPrefix(full, msg.Text) {
				sawPartial = true
				if snap.Phase != state.PhaseRunning {
					t.Fatalf("partial snapshot phase = %q, want running", snap.Phase)
				}
			}
		}
	}
	if !sawPartial {
		t.Fatalf("no intermediate assistant snapshot found (%d snapshots)", len(collector.all()))
	}
	if !sawFull {
		t.Fatal("no snapshot contained the completed assistant text")
	}

	final := h.ctrl.Snapshot()
	last := final.Messages[len(final.Messages)-1]
	if last.Role != state.RoleAssistant || last.Text != full {
		t.Fatalf("final assistant message = %+v, want %q", last, full)
	}
}

func TestOnUpdateFiresForSeedAndTurn(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	h := newHarness(t, provider)
	ctx := context.Background()

	var mu sync.Mutex
	phases := map[state.Phase]int{}
	h.ctrl.OnUpdate(func(snap *state.Snapshot) {
		mu.Lock()
		phases[snap.Phase]++
		mu.Unlock()
	})

	id, err := h.ctrl.CreateSession(ctx, "callbacks")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	mu.Lock()
	seeded := phases[state.PhaseIdle]
	mu.Unlock()
	if seeded == 0 {
		t.Fatal("OnUpdate did not fire for the session seed")
	}

	if err := h.ctrl.Submit(ctx, id, "hello"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	mu.Lock()
	defer mu.Unlock()
	if phases[state.PhaseRunning] == 0 {
		t.Fatal("OnUpdate never reported a running phase")
	}
	if phases[state.PhaseIdle] < 2 {
		t.Fatalf("idle updates = %d, want seed + terminal", phases[state.PhaseIdle])
	}
}

func TestStopCancelsTurnAndIsIdempotent(t *testing.T) {
	provider := &fakeProvider{
		respond: func(context.Context, runtime.Request, int) []runtime.Chunk {
			return []runtime.Chunk{{Text: "partial"}}
		},
	}
	provider.block.Store(true)
	h := newHarness(t, provider)
	ctx := context.Background()

	id, err := h.ctrl.CreateSession(ctx, "stop")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, "keep going"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitAssistantText(t, h.ctrl, "partial")
	waitPhase(t, h.ctrl, state.PhaseRunning)

	h.ctrl.Stop()
	if got := h.ctrl.Snapshot().Phase; got != state.PhaseIdle {
		t.Fatalf("phase right after Stop = %q, want idle", got)
	}
	h.ctrl.Stop() // idempotent
	h.ctrl.Wait()
	h.ctrl.Stop() // no active turn

	if !provider.canceled.Load() {
		t.Fatal("provider never observed context cancellation")
	}
	if got := h.ctrl.Snapshot().Phase; got != state.PhaseIdle {
		t.Fatalf("phase after drain = %q, want idle", got)
	}
	sess, err := h.store.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Status != store.SessionIdle {
		t.Fatalf("persisted status = %q, want idle", sess.Status)
	}

	messages, err := h.store.ListMessages(id)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages after stop = %d, want user + partial assistant: %+v", len(messages), messages)
	}
	if messages[1].Role != "assistant" || messages[1].Content != "partial" {
		t.Fatalf("partial assistant message = %+v", messages[1])
	}
	events, err := h.store.ListEvents(id)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for _, ev := range events {
		if ev.Type == session.EventError {
			t.Fatalf("cancellation persisted an error event: %+v", ev)
		}
	}

	// The cancelled turn must release the session for a new Submit.
	provider.block.Store(false)
	if err := h.ctrl.Submit(ctx, id, "after stop"); err != nil {
		t.Fatalf("Submit after Stop: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
}

func TestConcurrentSubmitSameSessionRejected(t *testing.T) {
	provider := &fakeProvider{
		respond: func(context.Context, runtime.Request, int) []runtime.Chunk {
			return []runtime.Chunk{{Text: "partial"}}
		},
	}
	provider.block.Store(true)
	h := newHarness(t, provider)
	ctx := context.Background()

	id, err := h.ctrl.CreateSession(ctx, "busy")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, "first"); err != nil {
		t.Fatalf("first Submit: %v", err)
	}
	waitAssistantText(t, h.ctrl, "partial")

	err = h.ctrl.Submit(ctx, id, "second")
	if !errors.Is(err, session.ErrTurnRunning) {
		t.Fatalf("second Submit error = %v, want ErrTurnRunning", err)
	}
	if !strings.Contains(err.Error(), id) {
		t.Fatalf("error %q does not name the session", err)
	}

	h.ctrl.Stop()
	h.ctrl.Wait()
}

func TestReopenSessionReadsHistoryFromStore(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	h := newHarness(t, provider)
	ctx := context.Background()

	id, err := h.ctrl.CreateSession(ctx, "persisted")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for _, prompt := range []string{"hello", "again"} {
		if err := h.ctrl.Submit(ctx, id, prompt); err != nil {
			t.Fatalf("Submit(%q): %v", prompt, err)
		}
		waitIdleAndDrain(t, h.ctrl)
	}

	// Replace controller/broker/projector while keeping the store: this is a
	// fresh process as far as in-memory state is concerned.
	h.restart()

	snap, err := h.ctrl.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if snap.SessionID != id {
		t.Fatalf("SessionID = %q, want %q", snap.SessionID, id)
	}
	if snap.Phase != state.PhaseIdle {
		t.Fatalf("Phase = %q, want idle", snap.Phase)
	}
	want := []string{"hello", "echo:hello", "again", "echo:again"}
	if len(snap.Messages) != len(want) {
		t.Fatalf("history messages = %d, want %d: %+v", len(snap.Messages), len(want), snap.Messages)
	}
	for i, text := range want {
		if snap.Messages[i].Text != text {
			t.Fatalf("history message %d = %+v, want %q", i, snap.Messages[i], text)
		}
		wantRole := state.RoleAssistant
		if i%2 == 0 {
			wantRole = state.RoleUser
		}
		if snap.Messages[i].Role != wantRole {
			t.Fatalf("history message %d role = %q, want %q", i, snap.Messages[i].Role, wantRole)
		}
	}
	if snap.Usage.Turns == 0 || snap.Usage.Out == 0 {
		t.Fatalf("usage not restored: %+v", snap.Usage)
	}
	if snap.Tools == nil {
		t.Fatal("Tools map is nil after reopen")
	}
	if got := h.ctrl.Snapshot(); got.SessionID != id || len(got.Messages) != len(want) {
		t.Fatalf("Snapshot after OpenSession = %+v", got)
	}

	if _, err := h.ctrl.OpenSession(ctx, "missing-session"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("OpenSession(missing) error = %v, want ErrNotFound", err)
	}
}

func TestSubmitValidation(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	h := newHarness(t, provider)
	ctx := context.Background()

	id, err := h.ctrl.CreateSession(ctx, "validation")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, "   "); !errors.Is(err, session.ErrEmptyPrompt) {
		t.Fatalf("Submit(blank) error = %v, want ErrEmptyPrompt", err)
	}
	if err := h.ctrl.Submit(ctx, "", "hello"); !errors.Is(err, session.ErrInvalidArgument) {
		t.Fatalf("Submit(empty session) error = %v, want ErrInvalidArgument", err)
	}
	if err := h.ctrl.Submit(ctx, "does-not-exist", "hello"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Submit(unknown session) error = %v, want ErrNotFound", err)
	}
}

func TestFailedTurnPersistsTerminalState(t *testing.T) {
	provider := &fakeProvider{streamErr: errors.New("provider exploded")}
	h := newHarness(t, provider)
	ctx := context.Background()

	id, err := h.ctrl.CreateSession(ctx, "failure")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, "hello"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitPhase(t, h.ctrl, state.PhaseFailed)
	h.ctrl.Wait()

	if got := h.ctrl.Snapshot().Phase; got != state.PhaseFailed {
		t.Fatalf("phase = %q, want failed", got)
	}
	lastError := h.ctrl.Snapshot().LastError
	if lastError == nil || !strings.Contains(lastError.Text, "provider exploded") {
		t.Fatalf("LastError = %+v, want provider failure", lastError)
	}

	sess, err := h.store.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Status != store.SessionFailed {
		t.Fatalf("persisted status = %q, want failed", sess.Status)
	}

	events, err := h.store.ListEvents(id)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var sawFailedStatus, sawError bool
	for _, ev := range events {
		switch ev.Type {
		case session.EventStatus:
			var payload struct {
				Phase string `json:"phase"`
			}
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				t.Fatalf("decode status payload: %v", err)
			}
			if payload.Phase == string(state.PhaseFailed) {
				sawFailedStatus = true
			}
		case session.EventError:
			sawError = true
		}
	}
	if !sawFailedStatus || !sawError {
		t.Fatalf("events missing failed status/error: failed=%v error=%v", sawFailedStatus, sawError)
	}

	// The user message is kept even though the turn failed; there is no
	// assistant row.
	messages, err := h.store.ListMessages(id)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 1 || messages[0].Role != "user" || messages[0].Content != "hello" {
		t.Fatalf("messages = %+v, want only the user prompt", messages)
	}
}

func TestApprovalApproveRunsToolAndClearsPending(t *testing.T) {
	provider := &fakeProvider{respond: approvalRespond}
	gate := runtime.NewApprovalQueue(func(toolName string, _ json.RawMessage) bool {
		return toolName == "echo"
	})
	h := newHarnessWithGate(t, provider, gate)
	ctx := context.Background()

	id, err := h.ctrl.CreateSession(ctx, "approve")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, "please echo"); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	req := waitApproval(t, h.ctrl, "call-1")
	if req.Name != "echo" || string(req.Args) != `{"path":"README.md"}` {
		t.Fatalf("approval request = %+v", req)
	}
	if !h.ctrl.Approve("call-1") {
		t.Fatal("Approve returned false for a pending request")
	}
	if got := h.ctrl.PendingApprovals(); len(got) != 0 {
		t.Fatalf("pending after Approve = %+v, want none", got)
	}

	waitIdleAndDrain(t, h.ctrl)
	if got := h.ctrl.PendingApprovals(); len(got) != 0 {
		t.Fatalf("pending after terminal status = %+v, want none", got)
	}
	waitAssistantText(t, h.ctrl, "done")

	// The second provider request carries the tool result, proving the gated
	// tool actually ran after the approval.
	second, ok := provider.requestAt(1)
	if !ok {
		t.Fatal("provider got no request after the tool ran")
	}
	var toolResult string
	for _, msg := range second.Messages {
		if msg.Role == runtime.RoleTool && msg.ToolCallID == "call-1" {
			toolResult = msg.Content
		}
	}
	if toolResult != `{"path":"README.md"}` {
		t.Fatalf("tool result fed back = %q, want the echo of the args", toolResult)
	}
}

// TestApprovalDenyFailsToolAndClearsPending covers Esc/Deny: the tool never
// runs, its call is recorded as an error, and the denial is fed back to the
// model so the conversation continues instead of failing the turn.
func TestApprovalDenyFailsToolAndClearsPending(t *testing.T) {
	provider := &fakeProvider{respond: approvalRespond}
	gate := runtime.NewApprovalQueue(func(toolName string, _ json.RawMessage) bool {
		return toolName == "echo"
	})
	h := newHarnessWithGate(t, provider, gate)
	ctx := context.Background()

	id, err := h.ctrl.CreateSession(ctx, "deny")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, "please echo"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitApproval(t, h.ctrl, "call-1")

	if !h.ctrl.Deny("call-1") {
		t.Fatal("Deny returned false for a pending request")
	}
	if got := h.ctrl.PendingApprovals(); len(got) != 0 {
		t.Fatalf("pending after Deny = %+v, want none", got)
	}
	waitPhase(t, h.ctrl, state.PhaseIdle)
	h.ctrl.Wait()

	if last := h.ctrl.Snapshot().LastError; last != nil {
		t.Fatalf("LastError = %+v, want none: a denied tool is a tool result", last)
	}
	if got := provider.requestCount(); got != 2 {
		t.Fatalf("provider calls = %d, want 2 (a second call after the denial)", got)
	}
	second, ok := provider.requestAt(1)
	if !ok {
		t.Fatal("provider got no second request")
	}
	var toolResult string
	for _, msg := range second.Messages {
		if msg.Role == runtime.RoleTool && msg.ToolCallID == "call-1" {
			toolResult = msg.Content
		}
	}
	if !strings.Contains(toolResult, "denied by user") {
		t.Fatalf("second request tool result = %q, want the denial", toolResult)
	}
	if got := h.ctrl.PendingApprovals(); len(got) != 0 {
		t.Fatalf("pending after denied status = %+v, want none", got)
	}
}

func TestApprovalCancelClearsPending(t *testing.T) {
	provider := &fakeProvider{respond: approvalRespond}
	gate := runtime.NewApprovalQueue(func(toolName string, _ json.RawMessage) bool {
		return toolName == "echo"
	})
	h := newHarnessWithGate(t, provider, gate)
	ctx := context.Background()

	id, err := h.ctrl.CreateSession(ctx, "cancel approval")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, "please echo"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitApproval(t, h.ctrl, "call-1")

	h.ctrl.Stop()
	if got := h.ctrl.PendingApprovals(); len(got) != 0 {
		t.Fatalf("pending right after Stop = %+v, want none", got)
	}
	h.ctrl.Wait()
	if got := h.ctrl.PendingApprovals(); len(got) != 0 {
		t.Fatalf("pending after cancelled turn = %+v, want none", got)
	}
	if h.ctrl.Approve("call-1") {
		t.Fatal("Approve returned true for a cancelled request")
	}
	if got := h.ctrl.Snapshot().Phase; got != state.PhaseIdle {
		t.Fatalf("phase after cancel = %q, want idle", got)
	}
}

func TestPendingApprovalsAndWorkspaceRoot(t *testing.T) {
	provider := &fakeProvider{respond: approvalRespond}
	gate := runtime.NewApprovalQueue(func(toolName string, _ json.RawMessage) bool {
		return toolName == "echo"
	})
	h := newHarnessWithGate(t, provider, gate)
	ctx := context.Background()

	if got := h.ctrl.WorkspaceRoot(); got != h.dir {
		t.Fatalf("WorkspaceRoot = %q, want %q", got, h.dir)
	}
	if got := h.ctrl.PendingApprovals(); got != nil {
		t.Fatalf("PendingApprovals before any turn = %+v, want nil", got)
	}
	if h.ctrl.Approve("unknown") || h.ctrl.Deny("unknown") {
		t.Fatal("Approve/Deny accepted an unknown call id")
	}

	id, err := h.ctrl.CreateSession(ctx, "workspace")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, "please echo"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitApproval(t, h.ctrl, "call-1")

	// A pending request of another session is not surfaced as current.
	if _, err := h.ctrl.CreateSession(ctx, "other"); err != nil {
		t.Fatalf("CreateSession(other): %v", err)
	}
	if got := h.ctrl.PendingApprovals(); len(got) != 0 {
		t.Fatalf("PendingApprovals now = %+v, want the other session's view", got)
	}

	if err := h.ctrl.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := h.ctrl.PendingApprovals(); len(got) != 0 {
		t.Fatalf("pending after Close = %+v, want none", got)
	}
	if h.ctrl.Approve("call-1") || h.ctrl.Deny("call-1") {
		t.Fatal("Approve/Deny accepted a decision after Close")
	}
}

func TestSettingsLoadSaveAndRestart(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})

	defaults, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Load defaults: %v", err)
	}
	if want := session.DefaultSettings(); defaults != want {
		t.Fatalf("defaults = %+v, want %+v", defaults, want)
	}

	custom := session.Settings{
		Permission:         session.PermissionAsk,
		Language:           session.LanguageChinese,
		Theme:              session.ThemeDark,
		FontSize:           session.FontSizeLarge,
		WorkDetails:        session.WorkDetailsStandard,
		ShowCodingView:     false,
		CollapseWorkDetail: session.CollapseImmediately,
		SendBehavior:       session.SendInterrupt,
		PerformanceUsage:   session.PerformanceDetailed,
	}
	if err := h.ctrl.Settings().Save(custom); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != custom {
		t.Fatalf("Load after Save = %+v, want %+v", got, custom)
	}

	wantKeys := map[string]string{
		"ui.permission":            "ask",
		"ui.language":              "zh",
		"ui.theme":                 "dark",
		"ui.font_size":             "16",
		"ui.work_details":          "standard",
		"ui.show_coding_view":      "false",
		"ui.collapse_work_details": "immediately",
		"ui.send_behavior":         "interrupt",
	}
	for key, want := range wantKeys {
		value, err := h.store.GetSetting(key)
		if err != nil {
			t.Errorf("GetSetting(%q): %v", key, err)
			continue
		}
		if value != want {
			t.Errorf("setting %q = %q, want %q", key, value, want)
		}
	}

	// A fresh controller over the same store sees the persisted values.
	h.restart()
	got, err = h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Load after restart: %v", err)
	}
	if got != custom {
		t.Fatalf("Load after restart = %+v, want %+v", got, custom)
	}

	// Unknown values normalize back to the defaults instead of sticking.
	if err := h.ctrl.Settings().Save(session.Settings{Permission: "bogus", FontSize: 99, ShowCodingView: true}); err != nil {
		t.Fatalf("Save invalid: %v", err)
	}
	got, err = h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Load after invalid save: %v", err)
	}
	if want := session.DefaultSettings(); got != want {
		t.Fatalf("normalized settings = %+v, want %+v", got, want)
	}
}

func TestNewSessionInheritsPermissionDefault(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	ctx := context.Background()

	first, err := h.ctrl.CreateSession(ctx, "first")
	if err != nil {
		t.Fatalf("CreateSession(first): %v", err)
	}
	if got := h.ctrl.SessionPermission(first); got != session.PermissionWorkspace {
		t.Fatalf("first permission = %q, want %q", got, session.PermissionWorkspace)
	}

	if err := h.ctrl.Settings().Save(session.Settings{Permission: session.PermissionAsk}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	second, err := h.ctrl.CreateSession(ctx, "second")
	if err != nil {
		t.Fatalf("CreateSession(second): %v", err)
	}
	if got := h.ctrl.SessionPermission(second); got != session.PermissionAsk {
		t.Fatalf("second permission = %q, want %q", got, session.PermissionAsk)
	}
	// Changing the default does not rewrite sessions that already exist.
	if got := h.ctrl.SessionPermission(first); got != session.PermissionWorkspace {
		t.Fatalf("first permission after change = %q, want %q", got, session.PermissionWorkspace)
	}
	if got := h.ctrl.SessionPermission("unknown"); got != "" {
		t.Fatalf("unknown session permission = %q, want empty", got)
	}
}

func TestRenameAndDeleteSession(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	ctx := context.Background()

	id, err := h.ctrl.CreateSession(ctx, "original")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess, err := h.ctrl.RenameSession(ctx, id, "  renamed  ")
	if err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	if sess.Title != "renamed" {
		t.Fatalf("renamed title = %q, want %q", sess.Title, "renamed")
	}
	stored, err := h.store.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if stored.Title != "renamed" {
		t.Fatalf("stored title = %q, want %q", stored.Title, "renamed")
	}
	if _, err := h.ctrl.RenameSession(ctx, id, "   "); !errors.Is(err, session.ErrInvalidArgument) {
		t.Fatalf("RenameSession(blank) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := h.ctrl.RenameSession(ctx, "missing", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RenameSession(missing) error = %v, want ErrNotFound", err)
	}

	// A turn's messages and events go away with the session.
	if err := h.ctrl.Submit(ctx, id, "hello"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	if msgs, err := h.store.ListMessages(id); err != nil || len(msgs) == 0 {
		t.Fatalf("ListMessages before delete = %d, %v; want messages", len(msgs), err)
	}

	if err := h.ctrl.DeleteSession(ctx, id); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := h.store.GetSession(id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetSession after delete error = %v, want ErrNotFound", err)
	}
	if got := h.ctrl.CurrentSessionID(); got != "" {
		t.Fatalf("CurrentSessionID after delete = %q, want empty", got)
	}
	if sessions, err := h.ctrl.ListSessions(ctx); err != nil || len(sessions) != 0 {
		t.Fatalf("ListSessions after delete = %+v, %v; want none", sessions, err)
	}
	if msgs, err := h.store.ListMessages(id); err != nil || len(msgs) != 0 {
		t.Fatalf("ListMessages after delete = %d, %v; want none", len(msgs), err)
	}
	if events, err := h.store.ListEvents(id); err != nil || len(events) != 0 {
		t.Fatalf("ListEvents after delete = %d, %v; want none", len(events), err)
	}
	if err := h.ctrl.DeleteSession(ctx, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("DeleteSession(again) error = %v, want ErrNotFound", err)
	}
	if err := h.ctrl.DeleteSession(ctx, "  "); !errors.Is(err, session.ErrInvalidArgument) {
		t.Fatalf("DeleteSession(blank) error = %v, want ErrInvalidArgument", err)
	}
}
