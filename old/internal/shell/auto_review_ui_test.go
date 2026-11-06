package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/session"
)

// TestAutoReviewUISelectorOption proves the experimental permission is offered
// with its description in all three selectors: Settings > General, the Home
// composer access menu, and the Session composer access menu. Choosing it in
// Settings persists the General default, choosing it on Home stages it for the
// next session, and choosing it on Session writes only that session.
func TestAutoReviewUISelectorOption(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	label := h.app.autoReviewLabel()
	desc := h.app.autoReviewDescription()
	if label != "Auto review EXP" {
		t.Fatalf("auto review label = %q, want Auto review EXP", label)
	}
	if desc == "" {
		t.Fatal("auto review description is empty")
	}

	// Settings > General: the option is listed with its description, and
	// choosing it persists the future-session default.
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if err := tt.Click("Permission, current: Workspace Write"); err != nil {
		t.Fatalf("open Settings permission selector: %v", err)
	}
	for _, want := range []string{label, desc} {
		if !tt.HasText(want) {
			t.Fatalf("Settings permission options missing %q: %q", want, tt.Texts())
		}
	}
	if err := tt.Click(label); err != nil {
		t.Fatalf("choose Auto review in Settings: %v", err)
	}
	cfg, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if cfg.Permission != session.PermissionAutoReview {
		t.Fatalf("General permission = %q, want %q", cfg.Permission, session.PermissionAutoReview)
	}

	// Reset the General default to Workspace Write so the composer chip and
	// the new menu row have distinct labels for the click assertions below.
	tt.Frame()
	if err := tt.Click("Permission, current: " + label); err != nil {
		t.Fatalf("reopen Settings permission selector: %v", err)
	}
	if err := tt.Click("Workspace Write"); err != nil {
		t.Fatalf("reset General permission: %v", err)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if h.app.Screen != Home {
		t.Fatalf("Esc left Settings for screen %v, want Home", h.app.Screen)
	}

	// Home composer access menu: stages the choice for the next session and
	// does not touch the General default.
	if err := tt.Click("Access mode, current: Workspace Write"); err != nil {
		t.Fatalf("open Home access menu: %v", err)
	}
	for _, want := range []string{label, desc} {
		if !tt.HasText(want) {
			t.Fatalf("Home access menu missing %q: %q", want, tt.Texts())
		}
	}
	if err := tt.Click(label); err != nil {
		t.Fatalf("choose Auto review on Home: %v", err)
	}
	if h.app.homePermission != session.PermissionAutoReview {
		t.Fatalf("staged Home permission = %q, want %q", h.app.homePermission, session.PermissionAutoReview)
	}
	cfg, err = h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("load settings after Home choice: %v", err)
	}
	if cfg.Permission != session.PermissionWorkspace {
		t.Fatalf("Home choice changed the General default to %q", cfg.Permission)
	}
	tt.Frame()
	if !tt.HasText("Access mode, current: " + label) {
		t.Fatalf("Home chip did not show the staged Auto review mode: %q", tt.Texts())
	}

	// Session composer access menu: writes only the current session and is
	// rebuilt with the auto-review permission.
	h.app.Screen = Session
	id := h.ctrl.CurrentSessionID()
	h.ctrl.SetSessionPermission(id, session.PermissionWorkspace)
	tt.Frame()
	if err := tt.Click("Access mode, current: Workspace Write"); err != nil {
		t.Fatalf("open Session access menu: %v", err)
	}
	for _, want := range []string{label, desc} {
		if !tt.HasText(want) {
			t.Fatalf("Session access menu missing %q: %q", want, tt.Texts())
		}
	}
	if err := tt.Click(label); err != nil {
		t.Fatalf("choose Auto review on Session: %v", err)
	}
	if got := h.ctrl.SessionPermissionOrDefault(id); got != session.PermissionAutoReview {
		t.Fatalf("session permission = %q, want %q", got, session.PermissionAutoReview)
	}
	cfg, err = h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("load settings after Session choice: %v", err)
	}
	if cfg.Permission != session.PermissionWorkspace {
		t.Fatalf("Session choice changed the General default to %q", cfg.Permission)
	}

	// Chinese label and description. The live resource is shared, so switch
	// the language explicitly and open the Home menu again.
	h.app.Language = LangZH
	h.app.Screen = Home
	h.app.homePermission = session.PermissionWorkspace
	tt.Frame()
	if err := tt.Click("访问模式，当前：" + h.app.composerPermissionLabel(session.PermissionWorkspace)); err != nil {
		t.Fatalf("open Home access menu (zh): %v", err)
	}
	for _, want := range []string{"自动审查（实验）", "由模型审查会修改数据的工具调用，并自动允许或拒绝。"} {
		if !tt.HasText(want) {
			t.Fatalf("Chinese access menu missing %q: %q", want, tt.Texts())
		}
	}
}
