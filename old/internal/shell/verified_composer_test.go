package shell

import (
	"context"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// TestComposerShiftEnterInsertsNewline pins DSH's composer keys on both live
// screens: Shift+Enter inserts a line break without submitting, and a plain
// Enter sends the multiline text.
func TestComposerShiftEnterInsertsNewline(t *testing.T) {
	t.Run("session", func(t *testing.T) {
		provider := &shellProvider{}
		h := newLiveApp(t, provider)
		h.app.Screen = Session
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()

		tt.Type("line one")
		tt.Key(ui.Shift, ui.KeyEnter)
		tt.Frame()

		if provider.callCount() != 0 {
			t.Fatalf("Shift+Enter submitted %d request(s)", provider.callCount())
		}
		if !strings.Contains(h.app.draft, "line one\n") {
			t.Fatalf("Shift+Enter draft = %q, want a trailing newline", h.app.draft)
		}
		tt.Type("line two")

		tt.Key(0, ui.KeyEnter)
		waitIdle(t, h.ctrl)
		h.ctrl.Wait()
		h.app.SetSnapshot(h.ctrl.Snapshot())
		if h.app.draft != "" {
			t.Fatalf("Enter left draft %q", h.app.draft)
		}
		found := false
		for _, m := range h.app.Snapshot().Messages {
			if m.Role == state.RoleUser && strings.Contains(m.Text, "line one\nline two") {
				found = true
			}
		}
		if !found {
			t.Fatalf("Enter did not submit the multiline draft: %+v", h.app.Snapshot().Messages)
		}
	})

	t.Run("home", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Screen = Home
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()

		before, err := h.ctrl.ListSessions(context.Background())
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		if err := tt.Click("Composer input"); err != nil {
			t.Fatalf("focus Home composer: %v", err)
		}
		tt.Type("home line one")
		tt.Key(ui.Shift, ui.KeyEnter)
		tt.Frame()
		if !strings.Contains(h.app.draft, "home line one\n") {
			t.Fatalf("Home Shift+Enter draft = %q, want a newline", h.app.draft)
		}
		after, err := h.ctrl.ListSessions(context.Background())
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		if len(after) != len(before) {
			t.Fatalf("Home Shift+Enter created a session: %d -> %d", len(before), len(after))
		}

		tt.Type("home line two")
		tt.Key(0, ui.KeyEnter)
		waitIdle(t, h.ctrl)
		h.ctrl.Wait()
		if h.app.Screen != Session {
			t.Fatalf("Home Enter did not open the created session: screen=%v", h.app.Screen)
		}
		h.app.SetSnapshot(h.ctrl.Snapshot())
		found := false
		for _, m := range h.app.Snapshot().Messages {
			if m.Role == state.RoleUser && strings.Contains(m.Text, "home line one\nhome line two") {
				found = true
			}
		}
		if !found {
			t.Fatalf("Home Enter did not send the multiline draft: %+v", h.app.Snapshot().Messages)
		}
	})
}

// TestComposerDraftScopedPerSession covers the global draft bug: leaving a
// session keeps its draft, opening another session starts blank, and only the
// explicit Home workspace transfer moves a draft into a new session.
func TestComposerDraftScopedPerSession(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Session
	h.app.Language = LangEN
	ctx := context.Background()
	first := h.ctrl.CurrentSessionID()
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	tt.Type("private draft for first")
	tt.Frame()

	second, err := h.ctrl.CreateSessionInWorkspace(ctx, h.ctrl.CurrentWorkspaceID(), "second")
	if err != nil {
		t.Fatalf("CreateSessionInWorkspace: %v", err)
	}
	h.app.openSession(second)
	tt.Frame()
	if h.app.draft != "" {
		t.Fatalf("draft followed the session switch: %q", h.app.draft)
	}

	tt.Type("second draft")
	tt.Frame()
	h.app.openSession(first)
	tt.Frame()
	if h.app.draft != "private draft for first" {
		t.Fatalf("draft restored = %q, want the first session's draft", h.app.draft)
	}

	// Home owns a separate draft.
	h.app.Screen = Home
	tt.Frame()
	if h.app.draft != "" {
		t.Fatalf("Home inherited a session draft: %q", h.app.draft)
	}
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus Home composer: %v", err)
	}
	tt.Type("home draft")
	tt.Frame()
	h.app.openSession(second)
	tt.Frame()
	if h.app.draft != "second draft" {
		t.Fatalf("Home draft leaked into the second session: %q", h.app.draft)
	}
	h.app.Screen = Home
	tt.Frame()
	if h.app.draft != "home draft" {
		t.Fatalf("Home draft not restored: %q", h.app.draft)
	}
}

// TestNewSessionDoesNotCarryDraft covers New Session: it opens a blank
// conversation, never the previous session's text.
func TestNewSessionDoesNotCarryDraft(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Session
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Type("do not carry me")
	tt.Frame()

	h.app.newSession()
	tt.Frame()
	if h.app.draft != "" {
		t.Fatalf("New Session carried the old draft: %q", h.app.draft)
	}
}

// TestHomeModeStagesNewTaskPreset covers the Home mode chip: it shows and
// stages the preset for the next session, never rewriting the last session.
func TestHomeModeStagesNewTaskPreset(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if !tt.HasText("Standard mode") {
		t.Fatalf("Home mode chip does not show Standard: %v", tt.Texts())
	}
	if err := tt.Click("Standard mode"); err != nil {
		t.Fatalf("click mode chip: %v", err)
	}
	if err := tt.Click("Creator mode"); err != nil {
		t.Fatalf("choose Creator mode: %v", err)
	}
	tt.Frame()
	if h.app.homeMode != "creator" {
		t.Fatalf("staged Home mode = %q, want creator", h.app.homeMode)
	}
	if got := h.ctrl.Mode(); got != "standard" {
		t.Fatalf("Home mode chip changed the last session to %q", got)
	}
	if got := h.app.agentPresetDefault(); got != "creator" {
		t.Fatalf("agent.preset = %q, want creator", got)
	}
	if !tt.HasText("Creator mode") {
		t.Fatalf("Home chip did not show the staged mode: %v", tt.Texts())
	}

	// A new Home task applies the staged preset to the created session.
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus Home composer: %v", err)
	}
	h.app.draft = "new task"
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	waitIdle(t, h.ctrl)
	h.ctrl.Wait()
	h.app.SetSnapshot(h.ctrl.Snapshot())
	if got := h.ctrl.Mode(); got != "creator" {
		t.Fatalf("Home-created session mode = %q, want creator", got)
	}

	// Home renders mode errors instead of swallowing them.
	h.app.Screen = Home
	h.app.modeUI().err = "mode test error"
	tt.Frame()
	if !tt.HasText("mode test error") {
		t.Fatalf("Home did not render modeUI().err: %v", tt.Texts())
	}
}

// TestHomeWorkspacePickerCreatesBlankSession covers the hero workspace
// picker: it chooses the workspace for a new task, creates a blank session
// there and moves the draft into it instead of opening an existing history.
func TestHomeWorkspacePickerCreatesBlankSession(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	firstWS := h.ctrl.CurrentWorkspaceID()
	second, err := h.ctrl.CreateWorkspace(ctx, "second-ws", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	existing, err := h.ctrl.CreateSessionInWorkspace(ctx, second.ID, "existing conversation")
	if err != nil {
		t.Fatalf("CreateSessionInWorkspace: %v", err)
	}
	if err := h.ctrl.SelectWorkspace(ctx, firstWS); err != nil {
		t.Fatalf("SelectWorkspace: %v", err)
	}

	h.app.Screen = Home
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus Home composer: %v", err)
	}
	tt.Type("carried draft")
	tt.Frame()

	if err := tt.Click("Workspace, current: test"); err != nil {
		t.Fatalf("open workspace picker: %v", err)
	}
	if err := tt.Click("Switch to second-ws"); err != nil {
		t.Fatalf("choose second-ws: %v", err)
	}
	tt.Frame()

	if h.app.Screen != Session {
		t.Fatalf("workspace picker left Home for %v", h.app.Screen)
	}
	if got := h.ctrl.CurrentWorkspaceID(); got != second.ID {
		t.Fatalf("workspace = %q, want %q", got, second.ID)
	}
	if got := h.ctrl.CurrentSessionID(); got == existing {
		t.Fatalf("workspace picker reopened the existing session %q", existing)
	}
	if got := h.ctrl.CurrentSessionID(); got == "" {
		t.Fatal("workspace picker created no session")
	}
	if h.app.draft != "carried draft" {
		t.Fatalf("draft after workspace transfer = %q", h.app.draft)
	}
	snap := h.app.Snapshot()
	if snap != nil && len(snap.Messages) != 0 {
		t.Fatalf("new workspace session is not blank: %+v", snap.Messages)
	}
}

// TestWorkspaceCollapsePersists covers the in-memory-only collapse map: a
// fresh App on the same store restores the collapsed workspace.
func TestWorkspaceCollapsePersists(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	h.app.Language = LangEN
	wsID := h.ctrl.CurrentWorkspaceID()
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if err := tt.Click("test"); err != nil {
		t.Fatalf("collapse workspace row: %v", err)
	}
	tt.Frame()
	if !h.app.wsCollapsed[wsID] {
		t.Fatal("clicking the current workspace did not collapse it")
	}
	if got, err := h.store.GetSetting(settingWorkspaceCollapsed); err != nil || !strings.Contains(got, wsID) {
		t.Fatalf("collapsed setting = %q, %v", got, err)
	}

	// A new App (a restart) restores the flag and keeps the session row
	// hidden.
	fresh := &App{Screen: Home, Ctrl: h.ctrl, Language: LangEN}
	freshTT := ui.NewTester(fresh.View, 1600, 1000)
	freshTT.Frame()
	if !fresh.wsCollapsed[wsID] {
		t.Fatalf("fresh App did not restore wsCollapsed: %v", fresh.wsCollapsed)
	}
	if freshTT.HasText("live") {
		t.Fatalf("collapsed workspace still shows its session row: %v", freshTT.Texts())
	}
}

// TestBrowseWorkspaceRootGuardsNativePicker covers Browse…: without a desktop
// backend it reports a visible error instead of blocking on the native dialog,
// and an injected picker still fills the typed field.
func TestBrowseWorkspaceRootGuardsNativePicker(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.beginAddWorkspace()
	h.app.browseWorkspaceRoot()
	if h.app.wsErr == "" {
		t.Fatal("Browse without a desktop backend did not report an error")
	}
	if !strings.Contains(h.app.wsErr, "unavailable") {
		t.Fatalf("wsErr = %q, want an unavailable message", h.app.wsErr)
	}

	dir := t.TempDir()
	h.app.PickDirectory = func() (string, error) { return dir, nil }
	h.app.browseWorkspaceRoot()
	if h.app.wsRoot != dir {
		t.Fatalf("injected picker root = %q, want %q", h.app.wsRoot, dir)
	}
	if h.app.wsErr != "" {
		t.Fatalf("injected picker left wsErr=%q", h.app.wsErr)
	}
}

// TestSessionModelSelectionIsPerSession covers the per-session model picker:
// choosing a model never changes the Settings default, and reopening a session
// keeps its own choice.
func TestSessionModelSelectionIsPerSession(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	alpha, err := h.ctrl.Models().Create(store.Model{Name: "Alpha", Model: "alpha-1"})
	if err != nil {
		t.Fatalf("create Alpha: %v", err)
	}
	beta, err := h.ctrl.Models().Create(store.Model{Name: "Beta", Model: "beta-1"})
	if err != nil {
		t.Fatalf("create Beta: %v", err)
	}
	first := h.ctrl.CurrentSessionID()
	second, err := h.ctrl.CreateSessionInWorkspace(ctx, h.ctrl.CurrentWorkspaceID(), "second")
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}

	h.app.Screen = Session
	h.app.Language = LangEN
	h.app.openSession(first)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click(modelChipLabel(h, alpha)); err != nil {
		t.Fatalf("open model menu: %v", err)
	}
	if err := tt.Click(beta.Name); err != nil {
		t.Fatalf("select Beta: %v", err)
	}
	if got := h.ctrl.SessionModelID(first); got != beta.ID {
		t.Fatalf("session 1 model = %q, want Beta", got)
	}
	if got, err := h.ctrl.Models().DefaultID(); err != nil || got != alpha.ID {
		t.Fatalf("global default = %q, %v; want Alpha", got, err)
	}

	h.app.openSession(second)
	tt.Frame()
	if !tt.HasText(alpha.Name) || tt.HasText(beta.Name) {
		t.Fatalf("session 2 shows the wrong model: %v", tt.Texts())
	}
	if err := tt.Click(modelChipLabel(h, alpha)); err != nil {
		t.Fatalf("open session 2 model menu: %v", err)
	}
	if err := tt.Click(alpha.Name); err != nil {
		t.Fatalf("select Alpha in session 2: %v", err)
	}

	h.app.openSession(first)
	tt.Frame()
	if !tt.HasText(beta.Name) {
		t.Fatalf("session 1 lost its Beta choice: %v", tt.Texts())
	}
	if got := h.ctrl.SessionModelID(first); got != beta.ID {
		t.Fatalf("session 1 model after reopen = %q, want Beta", got)
	}
}

// TestHomeComposerChoicesApplyToCreatedSession covers the staged Home access
// mode and model: the new session gets both, while the General defaults and
// the Settings model default stay unchanged.
func TestHomeComposerChoicesApplyToCreatedSession(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	alpha, err := h.ctrl.Models().Create(store.Model{Name: "Alpha", Model: "alpha-1"})
	if err != nil {
		t.Fatalf("create Alpha: %v", err)
	}
	beta, err := h.ctrl.Models().Create(store.Model{Name: "Beta", Model: "beta-1"})
	if err != nil {
		t.Fatalf("create Beta: %v", err)
	}
	if err := h.ctrl.Models().SetDefault(alpha.ID); err != nil {
		t.Fatalf("default Alpha: %v", err)
	}
	h.app.Screen = Home
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if err := tt.Click("Access mode, current: Workspace Write"); err != nil {
		t.Fatalf("open access menu: %v", err)
	}
	if err := tt.Click("Full access"); err != nil {
		t.Fatalf("stage Full access: %v", err)
	}
	tt.Frame()
	if err := tt.Click(modelChipLabel(h, alpha)); err != nil {
		t.Fatalf("open model menu: %v", err)
	}
	if err := tt.Click(beta.Name); err != nil {
		t.Fatalf("stage Beta: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus Home composer: %v", err)
	}
	h.app.draft = "new task with choices"
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	waitIdle(t, h.ctrl)
	h.ctrl.Wait()
	id := h.ctrl.CurrentSessionID()
	if got := h.ctrl.SessionPermissionOrDefault(id); got != session.PermissionFull {
		t.Fatalf("new session permission = %q, want full", got)
	}
	if got := h.ctrl.SessionModelID(id); got != beta.ID {
		t.Fatalf("new session model = %q, want Beta", got)
	}
	cfg, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if cfg.Permission != session.PermissionWorkspace {
		t.Fatalf("global permission = %q, want unchanged workspace", cfg.Permission)
	}
	if got, err := h.ctrl.Models().DefaultID(); err != nil || got != alpha.ID {
		t.Fatalf("global default model = %q, %v; want Alpha", got, err)
	}
}

// TestDraftDoesNotFollowImmediateSessionSwitch covers a switch made before the
// first frame: the controller already points at the new session, so the old
// buffer must be dropped rather than attached to it.
func TestDraftDoesNotFollowImmediateSessionSwitch(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Session
	h.app.draft = "private draft for first"
	second, err := h.ctrl.CreateSessionInWorkspace(context.Background(), h.ctrl.CurrentWorkspaceID(), "second")
	if err != nil {
		t.Fatalf("CreateSessionInWorkspace: %v", err)
	}
	h.app.openSession(second)
	if h.app.draft != "" {
		t.Fatalf("draft followed the immediate switch: %q", h.app.draft)
	}
}
