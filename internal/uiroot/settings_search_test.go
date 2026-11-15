package uiroot

// Frame tests for the settings search index (P2-2): the navigation search is
// no longer a page-title filter with a host-owned alias table. Pages declare
// their own keywords and their searchable items (a field, a row, a section);
// a search hit on an item selects its page and asks the page to reveal the
// item's region, so "压缩", "价格" and "字号" reach the control that holds the
// setting instead of only the page around it.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// searchProbe is a settings page that records the regions the host asked it to
// reveal. Its Build is a plain marker so the test reads the host's index and
// reveal contract, not a page module's rendering.
type searchProbe struct {
	reveals []string
}

func (p *searchProbe) page(id, section, title string, order int,
	keywords []string, items []contracts.SettingsSearchItem, body string,
) contracts.SettingsPageSpec {
	return contracts.SettingsPageSpec{
		ID: id, Section: section, Title: title, Order: order,
		Keywords:    keywords,
		SearchItems: items,
		Build:       func(c *ui.Context) { ui.Text(c, body) },
		Reveal:      func(_ *ui.Context, anchorKey string) { p.reveals = append(p.reveals, anchorKey) },
	}
}

func (p *searchProbe) revealCount(anchorKey string) int {
	count := 0
	for _, revealed := range p.reveals {
		if revealed == anchorKey {
			count++
		}
	}
	return count
}

// settingsSearchPages registers the small settings surface the host tests
// search: a model page with two searchable items, a behavior page with the
// message text size, a notifications page that only carries page keywords, and
// an advanced page that must stay reachable through a search hit.
func settingsSearchPages(t *testing.T, r *Root) (model, behavior, notifications, advanced *searchProbe) {
	t.Helper()
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	model = &searchProbe{}
	behavior = &searchProbe{}
	notifications = &searchProbe{}
	advanced = &searchProbe{}
	mustPage(t, r, model.page("model", "模型", "模型设置", 10, nil,
		[]contracts.SettingsSearchItem{
			{ID: "context-budget", Label: "上下文预算", AnchorKey: "model.context-budget",
				Keywords: []string{"压缩", "compact", "compaction", "上下文", "context", "预算", "budget"}},
			{ID: "pricing", Label: "模型价格", AnchorKey: "model.catalog",
				Keywords: []string{"价格", "price", "定价", "成本", "cost"}},
		}, "settings-model-body"))
	mustPage(t, r, behavior.page("behavior", "常用", "行为", 10, nil,
		[]contracts.SettingsSearchItem{
			{ID: "message-size", Label: "消息字号", AnchorKey: "behavior.message-size",
				Keywords: []string{"字号", "字体大小", "text size", "font size", "字体"}},
		}, "settings-behavior-body"))
	mustPage(t, r, notifications.page("notifications", "集成", "通知", 10,
		[]string{"通知", "notification", "气泡", "toast"},
		nil, "settings-notifications-body"))
	mustPage(t, r, advanced.page("diagnostics", "高级", "诊断", 10, nil,
		[]contracts.SettingsSearchItem{
			{ID: "log-path", Label: "日志位置", AnchorKey: "diagnostics.log",
				Keywords: []string{"日志", "log", "日志文件"}},
		}, "settings-diagnostics-body"))
	return model, behavior, notifications, advanced
}

// openSettingsSearch opens the settings surface and focuses its navigation
// search field.
func openSettingsSearch(t *testing.T, r *Root, tt *ui.Tester) {
	t.Helper()
	r.Navigator().ToggleSettings()
	tt.Frame()
	if err := tt.Click("搜索设置"); err != nil {
		t.Fatalf("focus the settings search: %v; texts = %v", err, tt.Texts())
	}
	tt.Frame()
}

// settingsSearch runs one search query (clearing the previous one first) and
// settles the frames the selection and the reveal need.
func settingsSearch(tt *ui.Tester, query string) {
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if query != "" {
		tt.Type(query)
	}
	tt.Frame()
	tt.Frame()
}

// TestSettingsSearchHitsItemAndRevealsAnchor covers the index core: an item
// keyword ("压缩") selects the page that declares the item and hands the host's
// reveal request to that page with the item's anchor key — once per search, not
// on every frame.
func TestSettingsSearchHitsItemAndRevealsAnchor(t *testing.T) {
	r := newTestRoot(t)
	model, behavior, _, _ := settingsSearchPages(t, r)
	tt := newTestTester(t, r, 1400, 900)
	tt.Frame()
	openSettingsSearch(t, r, tt)

	settingsSearch(tt, "压缩")
	requireHasText(t, tt, "settings-model-body")
	if got := model.revealCount("model.context-budget"); got != 1 {
		t.Fatalf("reveal calls for model.context-budget = %d, want 1; reveals = %v",
			got, model.reveals)
	}
	if len(behavior.reveals) != 0 {
		t.Fatalf("the behavior page was asked to reveal for a model hit: %v", behavior.reveals)
	}
	// The reveal is a one-shot request: later frames of the same query must not
	// keep scrolling the page or fight the user's scrolling.
	tt.Frame()
	tt.Frame()
	if got := model.revealCount("model.context-budget"); got != 1 {
		t.Fatalf("reveal ran again on later frames: %d calls, reveals = %v", got, model.reveals)
	}

	// A different item of the same page reveals its own anchor.
	settingsSearch(tt, "价格")
	requireHasText(t, tt, "settings-model-body")
	if got := model.revealCount("model.catalog"); got != 1 {
		t.Fatalf("reveal calls for model.catalog = %d, want 1; reveals = %v",
			got, model.reveals)
	}
}

// TestSettingsSearchHitsItemOnOtherPage covers switching the selected page by
// an item hit: "字号" reaches the behavior page even though the page title says
// nothing about text size.
func TestSettingsSearchHitsItemOnOtherPage(t *testing.T) {
	r := newTestRoot(t)
	_, behavior, _, _ := settingsSearchPages(t, r)
	tt := newTestTester(t, r, 1400, 900)
	tt.Frame()
	openSettingsSearch(t, r, tt)

	settingsSearch(tt, "字号")
	requireHasText(t, tt, "settings-behavior-body")
	requireNoText(t, tt, "settings-model-body")
	if got := behavior.revealCount("behavior.message-size"); got != 1 {
		t.Fatalf("reveal calls for behavior.message-size = %d, want 1; reveals = %v",
			got, behavior.reveals)
	}
}

// TestSettingsSearchPageKeywordsMatchWithoutItem covers the page-level half:
// keywords declared by the page module still filter the navigation, without an
// item reveal when the match is the page's own alias.
func TestSettingsSearchPageKeywordsMatchWithoutItem(t *testing.T) {
	r := newTestRoot(t)
	_, _, notifications, _ := settingsSearchPages(t, r)
	tt := newTestTester(t, r, 1400, 900)
	tt.Frame()
	openSettingsSearch(t, r, tt)

	settingsSearch(tt, "toast")
	requireHasText(t, tt, "settings-notifications-body")
	if len(notifications.reveals) != 0 {
		t.Fatalf("a page-keyword hit asked the page to reveal: %v", notifications.reveals)
	}
	// The page's own title still matches, as before.
	settingsSearch(tt, "通知")
	requireHasText(t, tt, "settings-notifications-body")
}

// TestSettingsSearchOpensAdvancedPage covers the preserved advanced-group
// behavior: a search hit on a page inside 高级 shows the page (the group opens
// for the duration of the search), and clearing the query folds the group back.
func TestSettingsSearchOpensAdvancedPage(t *testing.T) {
	r := newTestRoot(t)
	_, _, _, advanced := settingsSearchPages(t, r)
	tt := newTestTester(t, r, 1400, 900)
	tt.Frame()
	openSettingsSearch(t, r, tt)

	settingsSearch(tt, "日志")
	requireHasText(t, tt, "settings-diagnostics-body")
	if got := advanced.revealCount("diagnostics.log"); got != 1 {
		t.Fatalf("reveal calls for diagnostics.log = %d, want 1; reveals = %v",
			got, advanced.reveals)
	}

	// Clearing the query restores the grouped navigation: the full page list is
	// back. The advanced group stays open while its page is selected (the group
	// follows the page that shows), so the selected diagnostics page stays
	// reachable instead of being folded away under its own selection.
	settingsSearch(tt, "")
	requireHasText(t, tt, "模型设置")
	requireHasText(t, tt, "行为")
	requireHasText(t, tt, "通知")
	if got := advanced.revealCount("diagnostics.log"); got != 1 {
		t.Fatalf("clearing the query revealed the item again: %d calls", got)
	}
}

// TestSettingsSearchClearedDoesNotReveal covers the query reset: clearing the
// search restores the whole navigation and drops a pending reveal instead of
// scrolling an item the user is no longer searching for.
func TestSettingsSearchClearedDoesNotReveal(t *testing.T) {
	r := newTestRoot(t)
	model, behavior, _, _ := settingsSearchPages(t, r)
	tt := newTestTester(t, r, 1400, 900)
	tt.Frame()
	openSettingsSearch(t, r, tt)

	settingsSearch(tt, "压缩")
	requireHasText(t, tt, "settings-model-body")
	reveals := len(model.reveals)

	settingsSearch(tt, "")
	requireHasText(t, tt, "模型设置")
	requireHasText(t, tt, "行为")
	tt.Frame()
	tt.Frame()
	if len(model.reveals) != reveals || len(behavior.reveals) != 0 {
		t.Fatalf("clearing the query revealed an item: model=%v behavior=%v",
			model.reveals, behavior.reveals)
	}

	// The next distinct query reveals again (the one-shot bookkeeping is per
	// query, not once per surface).
	settingsSearch(tt, "字号")
	requireHasText(t, tt, "settings-behavior-body")
	if got := behavior.revealCount("behavior.message-size"); got != 1 {
		t.Fatalf("reveal calls after a new query = %d, want 1; reveals = %v",
			got, behavior.reveals)
	}
}
