package uiroot

// Frame tests for the anchored settings deep link (navigator.OpenSettingsPageAt):
// one call selects a settings page and asks its Reveal hook for one of the page's
// searchable items, the same locate path a navigation-search hit uses, so a
// surface outside settings (the Context page's 「上下文设置」) can hand the user to
// the exact control it talks about.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// anchorTestPages registers the small settings surface these tests address: a
// model page with the context-budget anchor and an advanced diagnostics page.
func anchorTestPages(t *testing.T, r *Root) (model, advanced *searchProbe) {
	t.Helper()
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	model = &searchProbe{}
	advanced = &searchProbe{}
	mustPage(t, r, model.page("model", "模型", "模型设置", 10, nil,
		[]contracts.SettingsSearchItem{
			{ID: "context-budget", Label: "上下文预算", AnchorKey: "model.context-budget"},
		}, "settings-model-body"))
	mustPage(t, r, advanced.page("diagnostics", "高级", "诊断", 10, nil,
		[]contracts.SettingsSearchItem{
			{ID: "log-path", Label: "日志位置", AnchorKey: "diagnostics.log"},
		}, "settings-diagnostics-body"))
	return model, advanced
}

// TestSettingsOpenPageAtRevealsAnchor covers the deep link: the page is selected,
// its body builds, and its Reveal hook receives the anchor key once.
func TestSettingsOpenPageAtRevealsAnchor(t *testing.T) {
	r := newTestRoot(t)
	model, advanced := anchorTestPages(t, r)
	tt := newTestTester(t, r, 1400, 900)
	tt.Frame()

	if err := r.nav.OpenSettingsPageAt("model", "model.context-budget"); err != nil {
		t.Fatalf("OpenSettingsPageAt(model): %v", err)
	}
	tt.Frame()
	tt.Frame()
	if !r.Navigator().SettingsOpen() {
		t.Fatal("the anchored deep link did not open the settings surface")
	}
	requireHasText(t, tt, "settings-model-body")
	if got := model.revealCount("model.context-budget"); got != 1 {
		t.Fatalf("reveal calls for model.context-budget = %d, want 1; reveals = %v",
			got, model.reveals)
	}
	if len(advanced.reveals) != 0 {
		t.Fatalf("a page the deep link did not target was asked to reveal: %v", advanced.reveals)
	}
	// The reveal is a one-shot request: later frames must not re-scroll.
	tt.Frame()
	tt.Frame()
	if got := model.revealCount("model.context-budget"); got != 1 {
		t.Fatalf("reveal ran again on later frames: %d calls, reveals = %v", got, model.reveals)
	}
}

// TestSettingsOpenPageAtAfterStaleSearch covers the edge the deep link must
// survive: a search from an earlier settings visit must neither consume nor clear
// the pending anchor request.
func TestSettingsOpenPageAtAfterStaleSearch(t *testing.T) {
	r := newTestRoot(t)
	model, _ := anchorTestPages(t, r)
	tt := newTestTester(t, r, 1400, 900)
	tt.Frame()
	openSettingsSearch(t, r, tt)
	settingsSearch(tt, "日志")
	requireHasText(t, tt, "settings-diagnostics-body")
	r.nav.CloseSettings()
	tt.Frame()

	if err := r.nav.OpenSettingsPageAt("model", "model.context-budget"); err != nil {
		t.Fatalf("OpenSettingsPageAt(model) after a search: %v", err)
	}
	tt.Frame()
	tt.Frame()
	requireHasText(t, tt, "settings-model-body")
	if got := model.revealCount("model.context-budget"); got != 1 {
		t.Fatalf("reveal calls after a stale search = %d, want 1; reveals = %v",
			got, model.reveals)
	}
}

// TestSettingsOpenPageAtAdvancedPage covers a page inside the collapsed advanced
// group: the deep link opens the group and reveals the item.
func TestSettingsOpenPageAtAdvancedPage(t *testing.T) {
	r := newTestRoot(t)
	_, advanced := anchorTestPages(t, r)
	tt := newTestTester(t, r, 1400, 900)
	tt.Frame()

	if err := r.nav.OpenSettingsPageAt("diagnostics", "diagnostics.log"); err != nil {
		t.Fatalf("OpenSettingsPageAt(diagnostics): %v", err)
	}
	tt.Frame()
	tt.Frame()
	requireHasText(t, tt, "settings-diagnostics-body")
	if got := advanced.revealCount("diagnostics.log"); got != 1 {
		t.Fatalf("reveal calls for diagnostics.log = %d, want 1; reveals = %v",
			got, advanced.reveals)
	}
}

// TestSettingsOpenPageAtUnknownPage covers the error half: an unregistered page
// reports the same error as OpenSettingsPage.
func TestSettingsOpenPageAtUnknownPage(t *testing.T) {
	r := newTestRoot(t)
	anchorTestPages(t, r)
	if err := r.nav.OpenSettingsPageAt("missing", "some.anchor"); err == nil {
		t.Fatal("OpenSettingsPageAt reported success for an unregistered page")
	}
	if r.nav.SettingsOpen() {
		t.Fatal("a failed anchored deep link opened the settings surface")
	}
}
