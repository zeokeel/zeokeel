package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// storeBackedHistory is the runtime.History adapter over the real store, the
// way the production app wires it: the messages table is the source of truth,
// tool calls are interleaved by start time, and Append is a no-op because the
// session controller is the single writer. A manual /compact replaces the
// stored rows, so the next turn reads the compacted conversation back.
type storeBackedHistory struct {
	st *store.Store
}

var _ runtime.History = (*storeBackedHistory)(nil)

func (h *storeBackedHistory) Messages(ctx context.Context, sessionID string) ([]runtime.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	msgs, err := h.st.ListMessages(sessionID)
	if err != nil {
		return nil, err
	}
	calls, err := h.st.ToolCallsForSession(sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]runtime.Message, 0, len(msgs)+2*len(calls))
	cursor := 0
	emitCall := func(call store.ToolCall) {
		out = append(out, runtime.Message{
			Role: runtime.RoleAssistant,
			ToolCalls: []runtime.ToolCall{{
				ID:        call.ID,
				Name:      call.Name,
				Arguments: json.RawMessage(call.Args),
			}},
		})
		out = append(out, runtime.Message{
			Role:       runtime.RoleTool,
			Content:    call.Result,
			ToolCallID: call.ID,
			ToolName:   call.Name,
		})
	}
	for _, m := range msgs {
		for cursor < len(calls) && calls[cursor].StartedAt.Before(m.CreatedAt) {
			emitCall(calls[cursor])
			cursor++
		}
		switch runtime.Role(m.Role) {
		case runtime.RoleUser:
			out = append(out, runtime.Message{Role: runtime.RoleUser, Content: m.Content})
		case runtime.RoleAssistant:
			out = append(out, runtime.Message{
				Role: runtime.RoleAssistant, Content: m.Content, Reasoning: m.Reasoning,
			})
		}
	}
	for ; cursor < len(calls); cursor++ {
		emitCall(calls[cursor])
	}
	return out, nil
}

func (h *storeBackedHistory) Append(context.Context, string, ...runtime.Message) error {
	return nil
}

// newCompactionHarness builds the shared-agent harness with a production-like
// store-backed History and a deterministic WindowCompactor.
func newCompactionHarness(t *testing.T, provider runtime.Provider) *harness {
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
	agent, err := runtime.New(runtime.Config{
		Provider:      provider,
		Tools:         registry,
		Instruction:   "test agent",
		History:       &storeBackedHistory{st: st},
		Compactor:     runtime.WindowCompactor{KeepLast: 2},
		ContextBudget: 10000,
	})
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}
	h := &harness{t: t, dir: dir, store: st, agent: agent, workspace: ws.ID}
	h.newController()
	t.Cleanup(h.close)
	return h
}

// compactSeedMessages persists a six-message conversation (three exchanges)
// and returns the stored rows in order.
func compactSeedMessages(t *testing.T, st *store.Store, sessionID string) []store.Message {
	t.Helper()
	base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	contents := [][2]string{
		{"user", "old question one"},
		{"assistant", "old answer one"},
		{"user", "old question two"},
		{"assistant", "old answer two"},
		{"user", "recent question"},
		{"assistant", "recent answer"},
	}
	out := make([]store.Message, 0, len(contents))
	for i, pair := range contents {
		msg, err := st.AppendMessage(store.Message{
			SessionID: sessionID,
			Role:      pair[0],
			Content:   pair[1],
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatalf("AppendMessage(%d): %v", i, err)
		}
		out = append(out, *msg)
	}
	return out
}

func requestContents(req runtime.Request) []string {
	out := make([]string, 0, len(req.Messages))
	for _, msg := range req.Messages {
		out = append(out, string(msg.Role)+":"+msg.Content)
	}
	return out
}

// TestControllerCompactCurrentPersists pins the persistent /compact contract:
// the session's stored messages are replaced by a summary plus the kept tail,
// the live snapshot folds the same change, the event is persisted for replay,
// and a reopened session plus later turns all use the compacted conversation.
func TestControllerCompactCurrentPersists(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	h := newCompactionHarness(t, provider)
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "compact session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Run three real turns so the store, the live snapshot and the agent's
	// History all describe the same conversation (six messages).
	for _, text := range []string{"old question one", "old question two", "recent question"} {
		if err := h.ctrl.Submit(ctx, id, text); err != nil {
			t.Fatalf("Submit(%q): %v", text, err)
		}
		waitIdleAndDrain(t, h.ctrl)
	}
	seeded, err := h.store.ListMessages(id)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(seeded) != 6 {
		t.Fatalf("seeded messages = %d, want 6", len(seeded))
	}
	removed := seeded[:4]
	kept := seeded[4:]

	replaced, saved, err := h.ctrl.CompactCurrent(ctx)
	if err != nil {
		t.Fatalf("CompactCurrent: %v", err)
	}
	if replaced != 4 {
		t.Fatalf("replaced items = %d, want the four dropped rows", replaced)
	}
	if saved <= 0 {
		t.Fatalf("estimated tokens saved = %d, want a positive value", saved)
	}

	// The store now holds exactly the compacted surface: summary + kept tail,
	// with dense ordinals and the kept rows' ids preserved.
	stored, err := h.store.ListMessages(id)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(stored) != 3 {
		t.Fatalf("stored messages after compaction = %d, want 3: %+v", len(stored), stored)
	}
	if stored[0].Content == "" || !strings.Contains(stored[0].Content, "Earlier conversation compacted") {
		t.Fatalf("stored summary = %q, want a framed compaction summary", stored[0].Content)
	}
	if got, want := stored[1].ID, kept[0].ID; got != want {
		t.Fatalf("kept tail id = %q, want the original %q", got, want)
	}
	if got, want := stored[2].ID, kept[1].ID; got != want {
		t.Fatalf("kept tail id = %q, want the original %q", got, want)
	}
	for i := range stored {
		if stored[i].Ordinal != int64(i+1) {
			t.Fatalf("stored message %d ordinal = %d, want dense %d", i, stored[i].Ordinal, i+1)
		}
	}
	for _, msg := range stored {
		for _, gone := range removed {
			if msg.ID == gone.ID {
				t.Fatalf("replaced message %q still stored", gone.ID)
			}
		}
	}

	// The live snapshot folded the same replacement.
	snap := h.ctrl.Snapshot()
	if snap == nil {
		t.Fatal("Snapshot = nil after compaction")
	}
	liveIDs := make([]string, 0, len(snap.Messages))
	for _, msg := range snap.Messages {
		liveIDs = append(liveIDs, msg.MsgID)
	}
	if len(liveIDs) != 3 || liveIDs[0] != stored[0].ID || liveIDs[1] != kept[0].ID || liveIDs[2] != kept[1].ID {
		t.Fatalf("live snapshot message ids = %v, want [summary %s %s]", liveIDs, kept[0].ID, kept[1].ID)
	}

	// The compaction is logged for replay.
	events, err := h.store.ListEvents(id)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	compactionEvents := 0
	for _, rec := range events {
		if rec.Type == session.EventCompaction {
			compactionEvents++
		}
	}
	if compactionEvents != 1 {
		t.Fatalf("persisted compaction events = %d, want 1", compactionEvents)
	}

	// Reopening the session shows the compacted conversation (messages table +
	// idempotent event replay), not the old history.
	reopened, err := h.ctrl.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession after compaction: %v", err)
	}
	if len(reopened.Messages) != 3 {
		t.Fatalf("reopened messages = %d, want 3: %+v", len(reopened.Messages), reopened.Messages)
	}
	if reopened.Messages[0].MsgID != stored[0].ID {
		t.Fatalf("reopened first message = %q, want the summary %q", reopened.Messages[0].MsgID, stored[0].ID)
	}
	joined := ""
	for _, msg := range reopened.Messages {
		joined += msg.Text + "\n"
	}
	for _, dropped := range []string{"old question one", "old answer one", "old question two", "old answer two", "echo:old question"} {
		if strings.Contains(joined, dropped) {
			t.Fatalf("reopened session still shows replaced history %q: %q", dropped, joined)
		}
	}

	// The next model request is seeded from the compacted store history and
	// never falls back to the dropped messages, even on a later turn.
	if err := h.ctrl.Submit(ctx, id, "after compact"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	req, ok := provider.requestAt(3)
	if !ok {
		t.Fatal("provider got no request after compaction")
	}
	contents := requestContents(req)
	requestJoined := strings.Join(contents, "|")
	if !strings.Contains(requestJoined, "Earlier conversation compacted") {
		t.Fatalf("next turn request has no compaction summary: %v", contents)
	}
	for _, dropped := range []string{"old question one", "old answer one", "old question two", "old answer two"} {
		if strings.Contains(requestJoined, dropped) {
			t.Fatalf("next turn request replays dropped history %q: %v", dropped, contents)
		}
	}
	if last := contents[len(contents)-1]; last != "user:after compact" {
		t.Fatalf("last request message = %q, want the new prompt", last)
	}

	// Second turn: no fallback to the pre-compaction history either.
	if err := h.ctrl.Submit(ctx, id, "and again"); err != nil {
		t.Fatalf("Submit again: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	req, ok = provider.requestAt(4)
	if !ok {
		t.Fatal("provider got no second request")
	}
	if joined := strings.Join(requestContents(req), "|"); strings.Contains(joined, "old question one") ||
		strings.Contains(joined, "old answer two") {
		t.Fatalf("second turn replays dropped history: %v", requestContents(req))
	}
}

// toolTurnRespond scripts one tool-using turn followed by plain turns: the
// first model call of "tool turn" asks for the echo tool, everything else
// answers with text.
func toolTurnRespond(_ context.Context, req runtime.Request, _ int) []runtime.Chunk {
	if len(req.Messages) > 0 {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == runtime.RoleUser && last.Content == "tool turn" {
			return []runtime.Chunk{{
				ToolCalls: []runtime.ToolCall{{
					ID:        "call-tool-1",
					Name:      "echo",
					Arguments: json.RawMessage(`{"path":"note.txt"}`),
				}},
				Done: true,
			}}
		}
	}
	return []runtime.Chunk{{Text: "plain answer", Done: true}}
}

// TestControllerCompactCurrentToolCalls proves a compaction over a tool-using
// conversation removes the replaced turns' tool-call rows as well: the model
// history must not replay an assistant tool call or a tool result whose turn was
// summarized away.
func TestControllerCompactCurrentToolCalls(t *testing.T) {
	provider := &fakeProvider{respond: toolTurnRespond}
	h := newCompactionHarness(t, provider)
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "tool compact")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for _, text := range []string{"tool turn", "recent question"} {
		if err := h.ctrl.Submit(ctx, id, text); err != nil {
			t.Fatalf("Submit(%q): %v", text, err)
		}
		waitIdleAndDrain(t, h.ctrl)
	}
	calls, err := h.store.ToolCallsForSession(id)
	if err != nil {
		t.Fatalf("ToolCallsForSession: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("seeded tool calls = %d, want 1", len(calls))
	}
	seeded, err := h.store.ListMessages(id)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(seeded) != 4 {
		t.Fatalf("seeded messages = %d, want 4", len(seeded))
	}

	replaced, _, err := h.ctrl.CompactCurrent(ctx)
	if err != nil {
		t.Fatalf("CompactCurrent: %v", err)
	}
	if replaced != 2 {
		t.Fatalf("replaced items = %d, want the two stored rows of the tool turn", replaced)
	}
	// The replaced turn's tool call is gone with its messages.
	calls, err = h.store.ToolCallsForSession(id)
	if err != nil {
		t.Fatalf("ToolCallsForSession after compaction: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("tool calls after compaction = %+v, want none", calls)
	}
	stored, err := h.store.ListMessages(id)
	if err != nil {
		t.Fatalf("ListMessages after compaction: %v", err)
	}
	if len(stored) != 3 {
		t.Fatalf("stored messages after compaction = %d, want 3: %+v", len(stored), stored)
	}

	// The next model request carries the summary and the kept tail only: no
	// tool call/result pair from the summarized turn can leak back in.
	providerCallsBefore := provider.requestCount()
	if err := h.ctrl.Submit(ctx, id, "after compact"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	req, ok := provider.requestAt(providerCallsBefore)
	if !ok {
		t.Fatal("provider got no request after compaction")
	}
	for _, message := range req.Messages {
		if message.Role == runtime.RoleTool || len(message.ToolCalls) > 0 {
			t.Fatalf("next request replays a tool exchange: %+v", message)
		}
		if message.Role == runtime.RoleUser && message.Content == "tool turn" {
			t.Fatalf("next request replays the summarized user turn: %v", requestContents(req))
		}
	}
	if !strings.Contains(strings.Join(requestContents(req), "|"), "Earlier conversation compacted") {
		t.Fatalf("next request has no compaction summary: %v", requestContents(req))
	}
}

// TestCompactSummaryPersistedRowStaysRecognizable is the reopen + auto-compact
// regression for P2-3: the persisted /compact summary is an assistant row, so a
// later automatic compaction must still recognize it as a summary (drop it and
// emit exactly one fresh system summary) instead of replaying it as ordinary
// assistant text.
func TestCompactSummaryPersistedRowStaysRecognizable(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	h := newCompactionHarness(t, provider)
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "summary marker")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for _, text := range []string{"old question one", "old question two", "recent question"} {
		if err := h.ctrl.Submit(ctx, id, text); err != nil {
			t.Fatalf("Submit(%q): %v", text, err)
		}
		waitIdleAndDrain(t, h.ctrl)
	}
	if _, _, err := h.ctrl.CompactCurrent(ctx); err != nil {
		t.Fatalf("CompactCurrent: %v", err)
	}
	stored, err := h.store.ListMessages(id)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(stored) != 3 || stored[0].Role != "assistant" ||
		!strings.HasPrefix(stored[0].Content, "Earlier conversation compacted (4 messages)") {
		t.Fatalf("persisted summary row = %+v, want an assistant row with the stable marker", stored)
	}

	// Force automatic compaction on the next two turns: the first turn's prior
	// has nothing to drop beyond the summary, the second turn's does.
	h.agent.SetContextBudget(1)
	for _, text := range []string{"turn one", "turn two"} {
		if err := h.ctrl.Submit(ctx, id, text); err != nil {
			t.Fatalf("Submit(%q): %v", text, err)
		}
		waitIdleAndDrain(t, h.ctrl)
	}
	req, ok := provider.requestAt(4)
	if !ok {
		t.Fatalf("provider requests = %d, want the second post-compaction turn", provider.requestCount())
	}
	summaries := 0
	for _, message := range req.Messages {
		if !strings.HasPrefix(message.Content, "Earlier conversation compacted") {
			continue
		}
		summaries++
		if message.Role != runtime.RoleSystem {
			t.Fatalf("auto-compacted summary role = %s, want the fresh system summary: %q", message.Role, message.Content)
		}
		if strings.Contains(message.Content, "(4 messages)") {
			t.Fatalf("auto-compaction kept the stale persisted summary: %q", message.Content)
		}
		if !strings.Contains(message.Content, "(2 messages)") {
			t.Fatalf("auto-compaction summary = %q, want the fresh dropped count", message.Content)
		}
	}
	if summaries != 1 {
		t.Fatalf("provider request carries %d compaction summaries, want exactly 1: %v",
			summaries, requestContents(req))
	}
}

// TestControllerCompactCurrentErrors pins the classified failures the shell
// turns into stable messages.
func TestControllerCompactCurrentErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("no current session", func(t *testing.T) {
		h := newHarness(t, &fakeProvider{respond: echoRespond})
		if _, _, err := h.ctrl.CompactCurrent(ctx); !errors.Is(err, session.ErrInvalidArgument) {
			t.Fatalf("CompactCurrent without a current session = %v, want ErrInvalidArgument", err)
		}
	})

	t.Run("no agent", func(t *testing.T) {
		h := newHarness(t, &fakeProvider{respond: echoRespond})
		broker := state.NewBroker()
		ctrl, err := session.New(session.Config{
			Store:       h.store,
			Broker:      broker,
			WorkspaceID: h.workspace,
		})
		if err != nil {
			t.Fatalf("session.New: %v", err)
		}
		t.Cleanup(func() {
			_ = ctrl.Close()
			broker.Close()
		})
		if _, err := ctrl.CreateSession(ctx, "static session"); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		if _, _, err := ctrl.CompactCurrent(ctx); !errors.Is(err, session.ErrNoAgent) {
			t.Fatalf("CompactCurrent without an agent = %v, want ErrNoAgent", err)
		}
	})

	t.Run("no compactor", func(t *testing.T) {
		// The shared harness agent has a compactor; build a controller whose
		// agent has a History but no compactor to reach the sentinel.
		dir := t.TempDir()
		st, err := store.Open(dir)
		if err != nil {
			t.Fatalf("store.Open: %v", err)
		}
		t.Cleanup(func() { _ = st.Close() })
		ws, err := st.CreateWorkspace("test", dir)
		if err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}
		agent, err := runtime.New(runtime.Config{
			Provider:    &fakeProvider{respond: echoRespond},
			Instruction: "no compactor",
			History:     &storeBackedHistory{st: st},
		})
		if err != nil {
			t.Fatalf("runtime.New: %v", err)
		}
		broker := state.NewBroker()
		ctrl, err := session.New(session.Config{
			Store: st, Broker: broker, Agent: agent, WorkspaceID: ws.ID,
		})
		if err != nil {
			t.Fatalf("session.New: %v", err)
		}
		t.Cleanup(func() {
			_ = ctrl.Close()
			broker.Close()
		})
		id, err := ctrl.CreateSession(ctx, "no compactor")
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		compactSeedMessages(t, st, id)
		if _, _, err := ctrl.CompactCurrent(ctx); !errors.Is(err, runtime.ErrNoCompactor) {
			t.Fatalf("CompactCurrent without a compactor = %v, want ErrNoCompactor", err)
		}
	})
}

// TestControllerCompactCurrentBusyTurn keeps the busy classification: a running
// turn blocks the command before anything is persisted.
func TestControllerCompactCurrentBusyTurn(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	provider.block.Store(true)
	h := newCompactionHarness(t, provider)
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "busy")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	compactSeedMessages(t, h.store, id)
	if err := h.ctrl.Submit(ctx, id, "hold the turn"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitPhase(t, h.ctrl, state.PhaseRunning)
	before, err := h.store.ListMessages(id)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if _, _, err := h.ctrl.CompactCurrent(ctx); !errors.Is(err, session.ErrTurnRunning) {
		t.Fatalf("CompactCurrent while a turn runs = %v, want ErrTurnRunning", err)
	}
	after, err := h.store.ListMessages(id)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("busy CompactCurrent changed the store: %d -> %d rows", len(before), len(after))
	}
	h.ctrl.Stop()
	waitIdleAndDrain(t, h.ctrl)
}

// TestControllerCompactCurrentAgentFactory pins the per-session agent path.
func TestControllerCompactCurrentAgentFactory(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	ws, err := st.CreateWorkspace("test", dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	broker := state.NewBroker()
	provider := &fakeProvider{respond: echoRespond}
	hist := &storeBackedHistory{st: st}
	ctrl, err := session.New(session.Config{
		Store:       st,
		Broker:      broker,
		WorkspaceID: ws.ID,
		AgentFactory: func(string) (*runtime.Agent, error) {
			return runtime.New(runtime.Config{
				Provider:      provider,
				Instruction:   "factory agent",
				History:       hist,
				Compactor:     runtime.WindowCompactor{KeepLast: 2},
				ContextBudget: 10000,
			})
		},
	})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	t.Cleanup(func() {
		_ = ctrl.Close()
		broker.Close()
		_ = st.Close()
	})
	ctx := context.Background()
	id, err := ctrl.CreateSession(ctx, "factory compact")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	compactSeedMessages(t, st, id)

	replaced, saved, err := ctrl.CompactCurrent(ctx)
	if err != nil {
		t.Fatalf("CompactCurrent (agent factory): %v", err)
	}
	if replaced != 4 || saved <= 0 {
		t.Fatalf("factory CompactCurrent = %d/%d, want 4/positive", replaced, saved)
	}
	stored, err := st.ListMessages(id)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(stored) != 3 || !strings.Contains(stored[0].Content, "Earlier conversation compacted") {
		t.Fatalf("factory compaction stored %d messages: %+v", len(stored), stored)
	}
}
