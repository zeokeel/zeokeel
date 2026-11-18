package uiroot

// Frame tests for the command palette's design 03-palette pass: the rows are
// grouped under the action, navigation and settings headings, the host derives
// the "前往 …" and "设置：…" commands from the registries, the search reads the
// keywords modules attach to a command, and a long list always paints the
// chosen row inside the list's viewport (the row Enter will run).

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// paletteListViewport returns the palette List's visible box. The list carries
// the accessible name "命令", which the conformance test pins.
func paletteListViewport(t *testing.T, tt *ui.Tester) ui.Rect {
	t.Helper()
	info, ok := tt.AccessByRole(ui.RoleList, "命令")
	if !ok {
		t.Fatalf("palette list is missing; texts = %v", tt.Texts())
	}
	return info.Bounds
}

// requireTextInsideList asserts text is painted inside the palette list's
// viewport, i.e. the row it belongs to is actually on screen.
func requireTextInsideList(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	viewport := paletteListViewport(t, tt)
	rect, ok := tt.Find(text)
	if !ok {
		t.Fatalf("frame does not show %q; texts = %v", text, tt.Texts())
	}
	if rect.Y < viewport.Y || rect.Y+rect.H > viewport.Y+viewport.H {
		t.Fatalf("%q at y=%v..%v is outside the palette list %v",
			text, rect.Y, rect.Y+rect.H, viewport)
	}
}

// TestPaletteGroupsRowsByCategory covers the grouped palette: an actions group
// heading, then the navigation group generated from the visible screens, then
// the settings group generated from the visible settings pages. Existing
// commands keep their default actions group without changing their spec.
func TestPaletteGroupsRowsByCategory(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustScreen(t, r, textScreen("automation", "自动化", "body-automation", 20))
	mustCommand(t, r, contracts.CommandSpec{ID: "demo.action", Title: "演示动作",
		Order: 10, Run: func() error { return nil }})
	mustPage(t, r, contracts.SettingsPageSpec{
		ID: "appearance", Section: "外观", Title: "外观", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "page-body") },
	})

	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	r.togglePalette()
	tt.Frame()

	// The headings and their rows appear in the design's order.
	requireTextBefore(t, tt, "操作", "演示动作", "前往", "前往 自动化", "设置", "设置：外观")

	// The generated commands carry stable IDs and the category that groups
	// them, so other surfaces can address them like registered commands.
	generated := map[string]contracts.CommandSpec{}
	for _, cmd := range r.paletteCommands("") {
		generated[cmd.ID] = cmd
	}
	goTo, ok := generated["uiroot.goto.automation"]
	if !ok {
		t.Fatalf("no goto command for the automation screen; commands = %v", generated)
	}
	if goTo.Category != contracts.CommandCategoryGoTo || goTo.Title != "前往 自动化" {
		t.Fatalf("goto command = %+v, want the navigation category and a 前往 title", goTo)
	}
	settings, ok := generated["uiroot.settings.appearance"]
	if !ok {
		t.Fatalf("no settings command for the appearance page; commands = %v", generated)
	}
	if settings.Category != contracts.CommandCategorySettings || settings.Title != "设置：外观" {
		t.Fatalf("settings command = %+v, want the settings category and a 设置： title", settings)
	}
}

// TestPaletteGoToCommandNavigates runs the generated navigation command: it
// pushes the screen the command names, like the sidebar row would.
func TestPaletteGoToCommandNavigates(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustScreen(t, r, textScreen("automation", "自动化", "body-automation", 20))

	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	r.togglePalette()
	tt.Frame()
	tt.Type("前往 自动化")
	tt.Frame()
	requireHasText(t, tt, "前往 自动化")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	requireNoText(t, tt, "命令面板")
	if got := r.Navigator().Current(); got != "automation" {
		t.Fatalf("navigator current = %q after the goto command, want automation", got)
	}
	requireHasText(t, tt, "body-automation")
}

// TestPaletteSettingsCommandOpensPage runs the generated settings command: it
// deep-links the settings surface to the page the command names, through the
// same OpenSettingsPage path modules use.
func TestPaletteSettingsCommandOpensPage(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustPage(t, r, contracts.SettingsPageSpec{
		ID: "appearance", Section: "外观", Title: "外观", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "page-body") },
	})
	mustPage(t, r, contracts.SettingsPageSpec{
		ID: "model", Section: "模型", Title: "模型", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "model-body") },
	})

	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	r.togglePalette()
	tt.Frame()
	tt.Type("设置：外观")
	tt.Frame()
	requireHasText(t, tt, "设置：外观")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	requireNoText(t, tt, "命令面板")
	if !r.Navigator().SettingsOpen() {
		t.Fatal("the settings surface did not open from the palette command")
	}
	requireHasText(t, tt, "page-body")
	if r.settingsSelectedID != "appearance" {
		t.Fatalf("settings page selected = %q, want appearance", r.settingsSelectedID)
	}
}

// TestPaletteSearchMatchesKeywords covers the search surface: a command is also
// found by the aliases a module attaches (Keywords), and a generated settings
// command keeps the settings page's aliases, so "主题" reaches the appearance
// page the way the settings navigation's own search would.
func TestPaletteSearchMatchesKeywords(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustCommand(t, r, contracts.CommandSpec{
		ID: "demo.theme", Title: "配色方案", Hint: "切换外观",
		Keywords: []string{"theme", "深色"},
		Order:    10,
		Run:      func() error { return nil },
	})
	mustPage(t, r, contracts.SettingsPageSpec{
		ID: "appearance", Section: "外观", Title: "外观", Order: 10,
		Keywords: []string{"主题", "theme"},
		Build:    func(c *ui.Context) { ui.Text(c, "page-body") },
	})

	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	r.togglePalette()
	tt.Frame()

	// The English alias finds the action command.
	tt.Type("theme")
	tt.Frame()
	requireHasText(t, tt, "配色方案")

	// The page's own keyword (declared by its module) finds the generated
	// settings command even though neither its title nor its ID contains the
	// word.
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	tt.Type("主题")
	tt.Frame()
	requireHasText(t, tt, "设置：外观")
	requireNoText(t, tt, "配色方案")
}

// TestPaletteLongListKeepsChosenRowVisible covers the palette's scroll model
// with more commands than the list's 320 DIP viewport holds: the first frame of
// an opening paints the chosen first row, moving the choice deep into the list
// scrolls the chosen row into view, and a reopened palette starts from the top
// again instead of keeping the previous opening's scroll place.
func TestPaletteLongListKeepsChosenRowVisible(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	const commandCount = 24
	for index := 0; index < commandCount; index++ {
		index := index
		mustCommand(t, r, contracts.CommandSpec{
			ID:    fmt.Sprintf("demo.command.%02d", index),
			Title: fmt.Sprintf("演示命令 %02d", index),
			Order: (index + 1) * 10,
			Run:   func() error { return nil },
		})
	}

	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	r.togglePalette()
	tt.Frame()
	requireHasText(t, tt, "命令面板")
	if r.paletteCount < commandCount {
		t.Fatalf("paletteCount = %d, want at least the %d commands", r.paletteCount, commandCount)
	}
	// Opening paints the chosen first row inside the viewport.
	requireTextInsideList(t, tt, "演示命令 00")
	if got := selectionPixels(tt); got < 5000 {
		t.Fatalf("opened palette paints no chosen row: selection pixels = %d", got)
	}

	// Move the choice to the last command: every frame keeps the chosen row
	// visible, so the row Enter would run is the row on screen.
	for index := 0; index < commandCount-1; index++ {
		tt.Key(0, ui.KeyDown)
		tt.Frame()
	}
	if r.paletteSelected != commandCount-1 {
		t.Fatalf("paletteSelected = %d after %d Down, want %d",
			r.paletteSelected, commandCount-1, commandCount-1)
	}
	requireTextInsideList(t, tt, fmt.Sprintf("演示命令 %02d", commandCount-1))
	if got := selectionPixels(tt); got < 5000 {
		t.Fatalf("scrolled palette paints no chosen row: accent pixels = %d", got)
	}

	// Reopening starts from the top: the chosen first row is visible again even
	// though the list was scrolled to its end before the palette closed.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	requireNoText(t, tt, "命令面板")
	r.togglePalette()
	tt.Frame()
	if r.paletteSelected != 0 {
		t.Fatalf("reopened palette selected = %d, want the first row (0)", r.paletteSelected)
	}
	requireTextInsideList(t, tt, "演示命令 00")
	if got := selectionPixels(tt); got < 5000 {
		t.Fatalf("reopened palette paints no chosen row: selection pixels = %d", got)
	}
}

// TestPaletteHintUsesMetadataType covers DESIGN.md §5.3's 11 DIP rule for the
// palette's no-match hint: "Esc 关闭 / Esc to close" is instruction copy, not a
// kbd-only micro-label, so it takes the metadata step (TypeXXS, 18 DIP line)
// rather than TypeXXXS's 14 DIP line.
func TestPaletteHintUsesMetadataType(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	r.togglePalette()
	tt.Frame()
	tt.Type("zzz-no-such-command")
	tt.Frame()
	requireHasText(t, tt, "没有匹配的命令")
	hint, ok := tt.Find("Esc 关闭")
	if !ok {
		t.Fatalf("the no-match hint is missing; texts = %v", tt.Texts())
	}
	if hint.H < 18 {
		t.Fatalf("the \"Esc 关闭\" hint is %.1f DIP tall, want the 18 DIP metadata line (TypeXXS); an 11 DIP sentence is DESIGN.md §5.3's TypeXXXS misuse",
			hint.H)
	}
}
