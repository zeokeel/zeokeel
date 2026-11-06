package shell

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
	"github.com/zeokeel/zeokeel/old/internal/tools"
)

// shellGoalStore records the /goal commands' persisted lifecycle, the same
// store seam the app wires for the goal tool and the round driver.
type shellGoalStore struct {
	mu    sync.Mutex
	goals map[string]*tools.Goal
	saves int
}

var _ tools.GoalStateStore = (*shellGoalStore)(nil)

func (s *shellGoalStore) LoadGoal(sessionID string) (*tools.Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.goals[sessionID], nil
}

func (s *shellGoalStore) SaveGoal(sessionID string, goal *tools.Goal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.goals == nil {
		s.goals = make(map[string]*tools.Goal)
	}
	s.saves++
	if goal == nil {
		delete(s.goals, sessionID)
		return nil
	}
	s.goals[sessionID] = goal
	return nil
}

func (s *shellGoalStore) savedGoal(sessionID string) *tools.Goal {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.goals[sessionID]
}

// slashHelpDraft checks that /help lists a command name.
func slashHelpDraft(t *testing.T, h *liveApp, tt *ui.Tester) string {
	t.Helper()
	slashComposer(t, h.app, tt, "/help")
	draft := h.app.draft
	h.app.draft = ""
	return draft
}

// TestSlashGoalCompactMenuRows pins the two new palette rows: /goal and
// /compact are discoverable, localized rows in the commands section, and the
// typed query filters to them.
func TestSlashGoalCompactMenuRows(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openSlashPalette(t, h, tt)

	for _, want := range []string{"/goal", "/compact", "Create, pause, resume or clear the session goal", "Replace older history with a summary now"} {
		if !tt.HasText(want) {
			t.Fatalf("palette missing %q: %v", want, tt.Texts())
		}
	}

	tt.Type("goal")
	tt.Frame()
	if !tt.HasText("/goal") {
		t.Fatalf("filtered palette missing /goal: %v", tt.Texts())
	}
	if tt.HasText("/compact") {
		t.Fatalf("filtering on goal still shows /compact: %v", tt.Texts())
	}
}

// TestSlashGoalCommandLifecycle drives the /goal command end to end: bare
// reports usage or state, the objective form sets and persists a goal, and the
// control words run the lifecycle transitions.
func TestSlashGoalCommandLifecycle(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	goalStore := &shellGoalStore{}
	h.ctrl.SetGoalStore(goalStore)
	id := h.ctrl.CurrentSessionID()
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	slashComposer(t, h.app, tt, "/goal")
	if !tt.HasText("No goal set") {
		t.Fatalf("bare /goal without a goal missing usage: %v", tt.Texts())
	}

	slashComposer(t, h.app, tt, "/goal Ship the release")
	goal := h.ctrl.Goal()
	if goal == nil || goal.Objective != "Ship the release" || goal.Phase != tools.GoalPhaseActive {
		t.Fatalf("after /goal <objective> goal = %+v", goal)
	}
	if saved := goalStore.savedGoal(id); saved == nil || saved.Objective != "Ship the release" {
		t.Fatalf("goal was not persisted through the goal store: %+v", saved)
	}
	if !tt.HasText("Goal set") {
		t.Fatalf("missing goal-set toast: %v", tt.Texts())
	}

	slashComposer(t, h.app, tt, "/goal")
	for _, want := range []string{"Goal: Ship the release", "phase: active", "automatic continuation round", "Next: /goal pause"} {
		if !tt.HasText(want) {
			t.Fatalf("bare /goal status missing %q: %v", want, tt.Texts())
		}
	}

	slashComposer(t, h.app, tt, "/goal pause")
	if goal := h.ctrl.Goal(); goal == nil || goal.Phase != tools.GoalPhasePaused {
		t.Fatalf("after /goal pause goal = %+v", goal)
	}
	if !tt.HasText("Goal paused") {
		t.Fatalf("missing paused toast: %v", tt.Texts())
	}

	slashComposer(t, h.app, tt, "/goal resume")
	if goal := h.ctrl.Goal(); goal == nil || goal.Phase != tools.GoalPhaseActive {
		t.Fatalf("after /goal resume goal = %+v", goal)
	}
	if !tt.HasText("Goal resumed") {
		t.Fatalf("missing resumed toast: %v", tt.Texts())
	}

	slashComposer(t, h.app, tt, "/goal complete")
	if goal := h.ctrl.Goal(); goal == nil || goal.Status != tools.GoalCompleted {
		t.Fatalf("after /goal complete goal = %+v", goal)
	}
	if !tt.HasText("Goal completed") {
		t.Fatalf("missing completed toast: %v", tt.Texts())
	}

	// A fresh objective reopens the goal and /goal abandon marks it terminal.
	slashComposer(t, h.app, tt, "/goal Another objective")
	slashComposer(t, h.app, tt, "/goal abandon")
	if goal := h.ctrl.Goal(); goal == nil || goal.Status != tools.GoalAbandoned {
		t.Fatalf("after /goal abandon goal = %+v", goal)
	}
	if !tt.HasText("Goal abandoned") {
		t.Fatalf("missing abandoned toast: %v", tt.Texts())
	}

	slashComposer(t, h.app, tt, "/goal clear")
	if h.ctrl.Goal() != nil {
		t.Fatalf("/goal clear left goal %+v", h.ctrl.Goal())
	}
	if !tt.HasText("Goal cleared") {
		t.Fatalf("missing cleared toast: %v", tt.Texts())
	}

	// /goal pause without a goal is a stable, displayable error, not a panic
	// and not an internal stack.
	slashComposer(t, h.app, tt, "/goal pause")
	if !tt.HasText("Could not update the goal") {
		t.Fatalf("missing no-goal error toast: %v", tt.Texts())
	}
}

// TestSlashGoalControlWordsAndEdit pins the DSH syntax rule: only a bare
// control word is a command, anything after it is an objective; edit takes an
// inline replacement objective.
func TestSlashGoalControlWordsAndEdit(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	// "pause after verification" is not a pause: the whole line is the
	// objective.
	slashComposer(t, h.app, tt, "/goal pause after verification")
	goal := h.ctrl.Goal()
	if goal == nil || goal.Objective != "pause after verification" || goal.Phase != tools.GoalPhaseActive {
		t.Fatalf("/goal pause after verification = %+v, want it treated as an objective", goal)
	}
	goalID := goal.ID
	revision := goal.Revision

	slashComposer(t, h.app, tt, "/goal edit Tighten the scope")
	goal = h.ctrl.Goal()
	if goal == nil || goal.Objective != "Tighten the scope" {
		t.Fatalf("/goal edit objective = %+v", goal)
	}
	// /goal edit replaces the objective in place: the goal identity and its
	// lifecycle phase survive, only the revision advances.
	if goal.ID != goalID {
		t.Fatalf("/goal edit changed the goal id %q -> %q", goalID, goal.ID)
	}
	if goal.Phase != tools.GoalPhaseActive {
		t.Fatalf("/goal edit changed the phase to %q", goal.Phase)
	}
	if goal.Revision != revision+1 {
		t.Fatalf("/goal edit revision = %d, want %d", goal.Revision, revision+1)
	}
	if !tt.HasText("Goal updated") {
		t.Fatalf("missing edit toast: %v", tt.Texts())
	}

	// Editing a paused goal keeps it paused (edit never re-arms continuation).
	slashComposer(t, h.app, tt, "/goal pause")
	paused := h.ctrl.Goal()
	if paused == nil || paused.Phase != tools.GoalPhasePaused {
		t.Fatalf("/goal pause = %+v", paused)
	}
	slashComposer(t, h.app, tt, "/goal edit Keep it paused")
	goal = h.ctrl.Goal()
	if goal == nil || goal.ID != goalID || goal.Phase != tools.GoalPhasePaused ||
		goal.Objective != "Keep it paused" || goal.Revision != paused.Revision+1 {
		t.Fatalf("/goal edit while paused = %+v, want the same paused goal", goal)
	}

	// A bare "edit" has no replacement objective: it reports usage instead of
	// creating a goal named "edit".
	slashComposer(t, h.app, tt, "/goal edit")
	if goal := h.ctrl.Goal(); goal == nil || goal.Objective != "Keep it paused" {
		t.Fatalf("bare /goal edit changed the goal: %+v", goal)
	}
	if !tt.HasText("Usage: /goal edit") {
		t.Fatalf("missing /goal edit usage: %v", tt.Texts())
	}
}

// TestSlashGoalCommandNeverReachesModel keeps the slash-line contract: the
// command runs locally, no provider turn starts and no slash line is stored as
// a user message.
func TestSlashGoalCommandNeverReachesModel(t *testing.T) {
	provider := &shellProvider{}
	h := newLiveApp(t, provider)
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	for _, line := range []string{"/goal Ship it", "/goal", "/goal pause", "/goal clear"} {
		slashComposer(t, h.app, tt, line)
	}
	if got := provider.callCount(); got != 0 {
		t.Fatalf("/goal reached the model (%d provider calls)", got)
	}
	messages, err := h.store.ListMessages(h.ctrl.CurrentSessionID())
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	for _, message := range messages {
		if message.Role == "user" && strings.HasPrefix(strings.TrimSpace(message.Content), "/goal") {
			t.Fatalf("/goal slash line became a user message: %q", message.Content)
		}
	}
}

// TestSlashGoalWithoutSession gives the Home/no-session path a stable prompt
// instead of silently doing nothing.
func TestSlashGoalWithoutSession(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := h.ctrl.DeleteSession(context.Background(), h.ctrl.CurrentSessionID()); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	h.app.runSlashCommand("/goal")
	tt.Frame()
	tt.Advance(300 * 1e6)
	tt.Frame()
	if !tt.HasText("Open a session before using /goal.") {
		t.Fatalf("missing no-session prompt: %v", tt.Texts())
	}

	// A static shell (no controller) reports the same limitation instead of
	// panicking on a nil controller.
	static := &App{Screen: Home, Language: LangEN}
	static.runSlashCommand("/goal")
	if got := static.slashMenu().deferredToast; !strings.Contains(got, "live session controller") {
		t.Fatalf("static /goal toast = %q, want the live-controller prompt", got)
	}
}

// compactShellHistory is the runtime.History adapter of the /compact shell
// fixture. Like the production app's storeHistory it reads the messages table
// (the single source of truth the controller writes) and Append is a no-op, so
// a persisted /compact replacement is what the next turn reads back.
type compactShellHistory struct {
	st *store.Store
}

var _ runtime.History = (*compactShellHistory)(nil)

func (h *compactShellHistory) Messages(ctx context.Context, sessionID string) ([]runtime.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stored, err := h.st.ListMessages(sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]runtime.Message, 0, len(stored))
	for _, msg := range stored {
		switch runtime.Role(msg.Role) {
		case runtime.RoleUser:
			out = append(out, runtime.Message{Role: runtime.RoleUser, Content: msg.Content})
		case runtime.RoleAssistant:
			out = append(out, runtime.Message{
				Role: runtime.RoleAssistant, Content: msg.Content, Reasoning: msg.Reasoning,
			})
		}
	}
	return out, nil
}

func (h *compactShellHistory) Append(context.Context, string, ...runtime.Message) error {
	return nil
}

// newLiveCompactionApp is newLiveApp with a compaction-capable runtime agent:
// a store-backed live controller whose agent has History, a WindowCompactor and
// a budget. A manual /compact replaces the stored conversation through the
// controller and the next turn reads it back through History.
func newLiveCompactionApp(t *testing.T, provider *shellProvider) *liveApp {
	t.Helper()
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
	tool := &shellTool{}
	registry := runtime.NewRegistry()
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	hist := &compactShellHistory{st: st}
	agent, err := runtime.New(runtime.Config{
		Provider:      provider,
		Tools:         registry,
		Instruction:   "test agent",
		History:       hist,
		Compactor:     runtime.WindowCompactor{KeepLast: 2},
		ContextBudget: 10000,
	})
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}
	broker := state.NewBroker()
	ctrl, err := session.New(session.Config{
		Store:       st,
		Broker:      broker,
		Agent:       agent,
		WorkspaceID: ws.ID,
	})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	t.Cleanup(func() {
		_ = ctrl.Close()
		broker.Close()
	})
	app := &App{Screen: Session, Ctrl: ctrl}
	ctrl.OnUpdate(app.SetSnapshot)
	if _, err := ctrl.CreateSession(context.Background(), "compact"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	app.SetSnapshot(ctrl.Snapshot())
	return &liveApp{app: app, ctrl: ctrl, store: st, tool: tool, dir: dir}
}

// seedCompactConversation runs three real turns so the store, the live snapshot
// and the agent's History share one six-message conversation (the last two rows
// are the tail WindowCompactor keeps).
func seedCompactConversation(t *testing.T, h *liveApp) {
	t.Helper()
	for _, text := range []string{"old question one", "old question two", "recent question"} {
		if err := h.ctrl.Submit(context.Background(), h.ctrl.CurrentSessionID(), text); err != nil {
			t.Fatalf("Submit(%q): %v", text, err)
		}
		waitIdle(t, h.ctrl)
	}
}

// runSlashLineNow runs a slash line directly (no composer keystrokes) and
// renders the deferred toast. Tests use it while a turn holds the composer.
func runSlashLineNow(t *testing.T, app *App, tt *ui.Tester, line string) {
	t.Helper()
	app.runSlashCommand(line)
	tt.Frame()
	tt.Advance(300 * 1e6)
	tt.Frame()
}

// TestSlashCompactCommand covers the /compact success path: it replaces the
// stored conversation with a summary plus the kept tail, reports the
// replacement count and tokens saved, runs no model turn, and the next model
// request reads the compacted conversation back from the store.
func TestSlashCompactCommand(t *testing.T) {
	provider := &shellProvider{}
	h := newLiveCompactionApp(t, provider)
	h.app.Language = LangEN
	id := h.ctrl.CurrentSessionID()
	seedCompactConversation(t, h)
	providerCallsBefore := provider.callCount()
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	slashComposer(t, h.app, tt, "/compact")
	if got := provider.callCount(); got != providerCallsBefore {
		t.Fatalf("/compact ran a model turn (%d calls, was %d)", got, providerCallsBefore)
	}
	if !tt.HasText("Compacted 4 history items") {
		t.Fatalf("missing compacted-count toast: %v", tt.Texts())
	}
	if !tt.HasText("tokens saved") {
		t.Fatalf("missing token-savings toast: %v", tt.Texts())
	}

	// The store now holds the compacted surface: summary + the two kept rows.
	stored, err := h.store.ListMessages(id)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(stored) != 3 || !strings.Contains(stored[0].Content, "Earlier conversation compacted") {
		t.Fatalf("stored conversation after /compact = %+v, want summary + kept tail", stored)
	}
	for _, msg := range stored {
		if strings.Contains(msg.Content, "old question one") || strings.Contains(msg.Content, "old question two") {
			t.Fatalf("replaced message still stored: %+v", msg)
		}
	}
	// The live transcript folded the same replacement.
	live := h.app.Snapshot()
	if live == nil || len(live.Messages) != 3 || live.Messages[0].MsgID != stored[0].ID {
		t.Fatalf("live snapshot after /compact = %+v, want the compacted messages", live)
	}

	if err := h.ctrl.Submit(context.Background(), id, "after compact"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdle(t, h.ctrl)
	req, ok := provider.requestAt(providerCallsBefore)
	if !ok {
		t.Fatal("provider got no request")
	}
	var joined strings.Builder
	for _, message := range req.Messages {
		joined.WriteString(string(message.Role))
		joined.WriteString(":")
		joined.WriteString(message.Content)
		joined.WriteString("|")
	}
	if !strings.Contains(joined.String(), "Earlier conversation compacted (4 messages)") {
		t.Fatalf("next request has no compaction summary: %s", joined.String())
	}
	if strings.Contains(joined.String(), "old question one") || strings.Contains(joined.String(), "old question two") {
		t.Fatalf("next request still replays dropped history: %s", joined.String())
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != runtime.RoleUser || last.Content != "after compact" {
		t.Fatalf("last request message = %s:%q, want the new prompt", last.Role, last.Content)
	}
}

// TestSlashCompactCommandUsageAndFailures pins the stable messages for the
// argument, unavailable and nothing-to-compact paths.
func TestSlashCompactCommandUsageAndFailures(t *testing.T) {
	t.Run("arguments are refused", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		slashComposer(t, h.app, tt, "/compact now")
		if !tt.HasText("Usage: /compact (no arguments)") {
			t.Fatalf("missing /compact usage: %v", tt.Texts())
		}
	})

	t.Run("no compactor", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		slashComposer(t, h.app, tt, "/compact")
		if !tt.HasText("Compaction is not available") {
			t.Fatalf("missing no-compactor toast: %v", tt.Texts())
		}
	})

	t.Run("nothing to compact", func(t *testing.T) {
		h := newLiveCompactionApp(t, &shellProvider{})
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		slashComposer(t, h.app, tt, "/compact")
		if !tt.HasText("Nothing to compact yet.") {
			t.Fatalf("missing empty-history toast: %v", tt.Texts())
		}
	})

	t.Run("busy turn", func(t *testing.T) {
		h := newLiveCompactionApp(t, &shellProvider{hang: true})
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		if err := h.ctrl.Submit(context.Background(), h.ctrl.CurrentSessionID(), "hold the turn"); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		waitRunning(t, h.ctrl)
		runSlashLineNow(t, h.app, tt, "/compact")
		if !tt.HasText("Compaction is unavailable because a turn is running") {
			t.Fatalf("missing busy toast: %v", tt.Texts())
		}
		h.ctrl.Stop()
		waitIdle(t, h.ctrl)
	})

	t.Run("no controller", func(t *testing.T) {
		static := &App{Screen: Session, Language: LangEN}
		static.runSlashCommand("/compact")
		if got := static.slashMenu().deferredToast; !strings.Contains(got, "live session controller") {
			t.Fatalf("static /compact toast = %q, want the live-controller prompt", got)
		}
	})
}

// TestSlashHelpListsGoalAndCompact keeps /help in sync with the new commands.
func TestSlashHelpListsGoalAndCompact(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	draft := slashHelpDraft(t, h, tt)
	for _, want := range []string{"/goal", "/compact", "Compaction is automatic", "atomic @[label](dsh-session:<id>)", "Unknown commands are sent to the model"} {
		if !strings.Contains(draft, want) {
			t.Fatalf("/help text missing %q: %q", want, draft)
		}
	}
}
