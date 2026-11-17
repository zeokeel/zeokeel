package settings_test

// Frame tests for the 2026-11-17 UI review proposal (settings scope):
//
//   - rule 14: the Memory and LSP pages keep their one primary in the host's
//     fixed footer, visible without scrolling at a 720 DIP window; the instant
//     verbs (add a memory, consolidate) are section actions, not mid-page
//     primaries;
//   - static risk disclosures use the warning semantic, red stays for real
//     errors and destructive actions;
//   - switch rows: label at the section's left edge, switch at the row's
//     trailing edge (one x across pages), description at the section edge;
//   - the About page's build-facts table shows version/commit/Go once and the
//     placeholder is the one "Unknown" word;
//   - a disabled footer primary takes the composer's neutral disabled tokens
//     (t.DisabledPrimary(), the preset uikit/composer.go applies to the send
//     button), not mygo's 50% accent wash.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// shortWindowTester opens the settings surface at a 720 DIP tall window: the
// height the review's footer-visibility evidence used.
func shortWindowTester(h *harness) *ui.Tester {
	return ui.NewTester(h.uiRoot.View, 1200, 720)
}

// shortWindowTypeNumberInput is typeNumberInput for the 720 DIP window: the
// shared helper's visibility bounds are tuned for the 800 DIP test size, so
// this one keeps the field inside the shorter panel (top 40, bottom 680)
// before clicking — a click below the panel would hit the modal scrim.
func shortWindowTypeNumberInput(t *testing.T, tester *ui.Tester, label, text string) {
	t.Helper()
	target := label + " 输入"
	for range 24 {
		rect, ok := tester.Find(target)
		if ok && rect.W > 0 && rect.H > 0 {
			centerY := rect.Y + rect.H/2
			if centerY >= 140 && centerY <= 620 {
				tester.ClickAt(rect.X+16, rect.Y+rect.H/2)
				tester.Frame()
				tester.Key(ui.Cmd, ui.KeyA)
				tester.Type(text)
				tester.Frame()
				return
			}
		}
		tester.Scroll(700, 400, 0, 160)
		tester.Frame()
	}
	t.Fatalf("number input %q never became visible in the short window; texts = %v",
		target, tester.Texts())
}

// shortWindowClearAndType is clearAndType for the 720 DIP window (the shared
// helper's visibility bounds are tuned for 800): it keeps the field inside the
// shorter panel before clicking, so the click never lands on the modal scrim.
func shortWindowClearAndType(t *testing.T, tester *ui.Tester, label, value string) {
	t.Helper()
	for range 24 {
		rect, ok := tester.Find(label)
		if ok && rect.W > 0 && rect.H > 0 {
			centerY := rect.Y + rect.H/2
			if centerY >= 140 && centerY <= 620 {
				tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
				tester.Frame()
				tester.Key(ui.Cmd, ui.KeyA)
				tester.Key(0, ui.KeyBackspace)
				tester.Type(value)
				tester.Frame()
				return
			}
		}
		tester.Scroll(700, 400, 0, 160)
		tester.Frame()
	}
	t.Fatalf("input %q never became visible in the short window; texts = %v",
		label, tester.Texts())
}

// assertInsideWindow asserts label's box is painted inside the window without
// any scrolling: a control clipped by the page scroll would fail this (its
// label rect sits outside the visible panel or has no area).
func assertInsideWindow(t *testing.T, tester *ui.Tester, label string, height int) {
	t.Helper()
	rect, ok := tester.Find(label)
	if !ok {
		t.Fatalf("%q not found; texts = %v", label, tester.Texts())
	}
	if rect.W <= 0 || rect.H <= 0 || rect.Y < 0 || rect.Y+rect.H > float32(height) {
		t.Fatalf("%q box %+v is not visible inside the %d DIP window without scrolling",
			label, rect, height)
	}
}

// assertTextInk asserts the text showing label paints in the want semantic
// color and never in the forbidden one: it scans the label's box for ink
// pixels (anti-aliased edges blend toward the background, core glyph pixels
// carry the full token color).
func assertTextInk(t *testing.T, tester *ui.Tester, label string, want, forbidden ui.Color) {
	t.Helper()
	rect, ok := tester.Find(label)
	if !ok {
		t.Fatalf("%q not found; texts = %v", label, tester.Texts())
	}
	img := tester.Image()
	found := false
	for y := int(rect.Y); y < int(rect.Y+rect.H); y++ {
		for x := int(rect.X); x < int(rect.X+rect.W); x++ {
			px := statusPixel(t, img, x, y)
			if statusColorNear(px, forbidden, 3) {
				t.Fatalf("%q paints the forbidden semantic %+v at (%d,%d); want %+v",
					label, forbidden, x, y, want)
			}
			if statusColorNear(px, want, 3) {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("%q shows no ink in the want semantic %+v; texts = %v", label, want, tester.Texts())
	}
}

// --- rule 14: fixed footers on Memory and LSP --------------------------------

// TestMemoryFooterSaveVisibleAtShortWindow pins the memory page's submit
// 落点: the consolidation steppers' Save is the host footer's one primary,
// visible without scrolling at 720 DIP; Save persists the policy without
// running a consolidation, while the section's 按策略整理 action saves and
// consolidates at once.
func TestMemoryFooterSaveVisibleAtShortWindow(t *testing.T) {
	consolidator := newFakeMemoryConsolidator()
	store := newFakeSettings(map[string]string{
		contracts.SettingMemoryMaxEntries: "20",
		contracts.SettingMemoryMaxAgeDays: "30",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	provide[contracts.MemoryStore](t, h.ctx, contracts.KeyMemory, consolidator)
	tester := shortWindowTester(h)
	tester.Frame()
	h.click(tester, "记忆")

	// The footer primary exists, is disabled while the policy is clean, and
	// sits inside the short window without any scrolling.
	requireFooterDisabled(t, tester, "保存", true, "memory policy clean")
	assertInsideWindow(t, tester, "保存", 720)
	if got := exactTextCount(tester.Texts(), "保存"); got != 1 {
		t.Fatalf("保存 labels = %d, want the footer's one primary; texts = %v", got, tester.Texts())
	}
	// The instant verbs are section actions, not mid-page primaries.
	h.assertTexts(tester, "按策略整理", "添加记忆")

	// Edit a stepper (the body scrolls like a user would), then submit from
	// the footer without scrolling back: the footer never moved.
	shortWindowTypeNumberInput(t, tester, "最大条目数", "12")
	tester.Frame()
	if !tester.HasText("整理策略") {
		t.Fatal("settings dismissed while scrolling to the stepper")
	}
	requireFooterDisabled(t, tester, "保存", false, "memory policy dirty")
	if err := tester.Click("保存"); err != nil {
		t.Fatalf("click footer Save without scrolling: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if got := store.get(contracts.SettingMemoryMaxEntries); got != "12" {
		t.Fatalf("%s = %q, want 12", contracts.SettingMemoryMaxEntries, got)
	}
	if policies := consolidator.policies(); len(policies) != 0 {
		t.Fatalf("footer Save ran a consolidation: %+v, want save-only", policies)
	}
	h.assertTexts(tester, "已保存")
	requireFooterDisabled(t, tester, "保存", true, "memory policy saved")

	// The section action still saves and consolidates at once.
	h.click(tester, "按策略整理")
	policies := consolidator.policies()
	if len(policies) != 1 || policies[0].MaxEntries != 12 {
		t.Fatalf("consolidate policies = %+v, want one MaxEntries 12", policies)
	}
}

// TestLSPFooterAddVisibleAtShortWindow pins the LSP page's submit落点: the add
// form's one primary lives in the host footer (no in-section submit), visible
// without scrolling at 720 DIP even after the body scrolled to the fields.
func TestLSPFooterAddVisibleAtShortWindow(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := shortWindowTester(h)
	tester.Frame()
	h.click(tester, "LSP")

	if got := exactTextCount(tester.Texts(), "添加"); got != 1 {
		t.Fatalf("添加 labels = %d, want the footer's one primary; texts = %v", got, tester.Texts())
	}
	assertInsideWindow(t, tester, "添加", 720)

	shortWindowClearAndType(t, tester, "名称 输入", "go")
	shortWindowClearAndType(t, tester, "命令 输入", "gopls")
	// The body scrolled to the fields; the footer submit needs no scroll back.
	if err := tester.Click("添加"); err != nil {
		t.Fatalf("click footer Add without scrolling: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	servers := storedLSPServers(t, store)
	if len(servers) != 1 || servers[0].Name != "go" || servers[0].Command != "gopls" {
		t.Fatalf("stored servers = %+v, want one go/gopls", servers)
	}
	h.assertTexts(tester, "已添加（重启后生效）")
}

// --- warning semantic for static risk disclosures ----------------------------

// TestLSPWorkspaceRiskIsWarning covers the workspace-config trust disclosure:
// a static caution paints in the warning semantic in both themes, never the
// danger red reserved for real errors.
func TestLSPWorkspaceRiskIsWarning(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "LSP")
	risk := "风险：只应在你信任的仓库中开启；恶意仓库可通过该文件让工具启动任意命令。"
	h.assertTexts(tester, risk)
	assertTextInk(t, tester, risk, design.Light().WarningText, design.Light().DangerText)

	h.uiRoot.Theme().SetDark(true)
	tester.Frame()
	assertTextInk(t, tester, risk, design.Dark().WarningText, design.Dark().DangerText)
}

// TestMemoryExtractionCautionIsWarning covers the model-extraction cost/privacy
// note: the same static-caution class as the LSP risk line, warning not danger.
func TestMemoryExtractionCautionIsWarning(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	provide[contracts.MemoryStore](t, h.ctx, contracts.KeyMemory, newFakeMemoryStore())
	tester := h.tester()
	tester.Frame()
	h.click(tester, "记忆")
	caution := "会消耗模型额度并向服务商发送对话内容。"
	h.assertTexts(tester, caution)
	assertTextInk(t, tester, caution, design.Light().WarningText, design.Light().DangerText)
}

// --- switch row alignment -----------------------------------------------------

// TestSwitchRowsTrailingAndAligned pins the switch-row anatomy: the label sits
// at the section's left edge, the switch at the row's trailing edge, the
// description below at the section edge (not orphaned under a control column),
// and every settings page's switch shares one trailing x.
func TestSwitchRowsTrailingAndAligned(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "外观")

	autostart, ok := tester.AccessByRole(ui.RoleSwitch, "开机自启动")
	if !ok {
		t.Fatalf("autostart switch missing; texts = %v", tester.Texts())
	}
	tray, ok := tester.AccessByRole(ui.RoleSwitch, "关闭时最小化到托盘")
	if !ok {
		t.Fatalf("tray switch missing; texts = %v", tester.Texts())
	}
	if d := autostart.Bounds.X - tray.Bounds.X; d > 1 || d < -1 {
		t.Fatalf("switch x differs within the section: autostart %v vs tray %v",
			autostart.Bounds, tray.Bounds)
	}
	// Label and description align with the section title's left edge.
	title, ok := tester.Find("桌面集成")
	if !ok {
		t.Fatal("section title 桌面集成 missing")
	}
	caption, ok := tester.Find("开机自启动")
	if !ok {
		t.Fatal("switch caption 开机自启动 missing")
	}
	if d := caption.X - title.X; d > 1 || d < -1 {
		t.Fatalf("caption left edge %v is not the section edge %v", caption.X, title.X)
	}
	description, ok := tester.Find("关闭窗口后应用继续在托盘运行，任务不中断。修改后重启应用生效。")
	if !ok {
		t.Fatalf("tray description missing; texts = %v", tester.Texts())
	}
	if d := description.X - title.X; d > 1 || d < -1 {
		t.Fatalf("description left edge %v is orphaned from the section edge %v",
			description.X, title.X)
	}

	// Cross-page: the notifications and LSP switches share the trailing x.
	h.click(tester, "通知")
	toast, ok := tester.AccessByRole(ui.RoleSwitch, "显示通知气泡")
	if !ok {
		t.Fatalf("toast switch missing; texts = %v", tester.Texts())
	}
	if d := toast.Bounds.X - autostart.Bounds.X; d > 1 || d < -1 {
		t.Fatalf("notifications switch x %v differs from appearance %v",
			toast.Bounds.X, autostart.Bounds.X)
	}
	h.click(tester, "LSP")
	workspace, ok := tester.AccessByRole(ui.RoleSwitch, "读取工作区 LSP 配置（.zeokeel/lsp.json）")
	if !ok {
		t.Fatalf("workspace switch missing; texts = %v", tester.Texts())
	}
	if d := workspace.Bounds.X - autostart.Bounds.X; d > 1 || d < -1 {
		t.Fatalf("LSP switch x %v differs from appearance %v",
			workspace.Bounds.X, autostart.Bounds.X)
	}
}

// --- About build-facts dedup ---------------------------------------------------

// TestAboutBuildFactsDeduplicated pins the merged build-facts table: version,
// commit and Go appear exactly once (the old 构建信息/当前版本/运行时信息
// sections repeated them), the retired 当前版本 section is gone, and the
// runtime section keeps the storage facts.
func TestAboutBuildFactsDeduplicated(t *testing.T) {
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		nil, contracts.AppInfo{
			Name: "ZeoKeel", Version: "1.2.3", Commit: "abc124",
			BuildTime: "2026-01-02T03:04:05Z", GoVersion: "go1.27.3",
		})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "关于")
	h.assertTexts(tester, "构建信息", "运行时信息", "平台", "数据目录")
	h.assertNoTexts(tester, "当前版本")
	for _, once := range []string{"1.2.3", "abc124", "go1.27.3"} {
		if got := exactTextCount(tester.Texts(), once); got != 1 {
			t.Fatalf("%q appears %d times, want once in the build-facts table; texts = %v",
				once, got, tester.Texts())
		}
	}
}

// TestAboutPlaceholderUnified pins the one placeholder word: empty build
// metadata reads 未知 in Chinese and Unknown (capital U) in English — the old
// diagnostics sheet's lowercase "unknown" is gone.
func TestAboutPlaceholderUnified(t *testing.T) {
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		nil, contracts.AppInfo{Name: "ZeoKeel"})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "关于")
	h.assertTexts(tester, "未知")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Unknown")
	h.assertNoTexts(tester, "unknown")
}

// --- disabled footer primary = composer's disabled tokens --------------------

// assertDisabledPrimaryInk asserts the disabled footer primary paints the
// composer's neutral disabled look: t.DisabledPrimary()'s BgStrong fill and
// LabelTertiary text at mygo's 50% disabled opacity over the panel background
// (the exact mechanism uikit/composer.go applies to the disabled send button),
// and not the old 50% accent wash with white text.
func assertDisabledPrimaryInk(t *testing.T, tester *ui.Tester, label string, tokens *design.Tokens) {
	t.Helper()
	rect, ok := tester.Find(label)
	if !ok {
		t.Fatalf("footer primary %q missing; texts = %v", label, tester.Texts())
	}
	img := tester.Image()
	backdrop := tokens.BgBase // the footer sits on the modal panel (S0)
	wantFill := tokens.BgStrong.Alpha(0.5).Over(backdrop)
	// The label box is the text; the button's horizontal padding carries the
	// fill a few DIP left of it.
	fill := statusPixel(t, img, int(rect.X)-8, int(rect.Y+rect.H/2))
	if !statusColorNear(fill, wantFill, 3) {
		t.Fatalf("disabled primary fill = %+v, want the composer's neutral disabled fill %+v",
			fill, wantFill)
	}
	accentWash := tokens.Accent.Alpha(0.5).Over(backdrop)
	if statusColorNear(fill, accentWash, 10) {
		t.Fatalf("disabled primary fill = %+v is still the accent wash %+v", fill, accentWash)
	}
	wantInk := tokens.LabelTertiary.Alpha(0.5).Over(wantFill)
	found := false
	for y := int(rect.Y); y < int(rect.Y+rect.H); y++ {
		for x := int(rect.X); x < int(rect.X+rect.W); x++ {
			if statusColorNear(statusPixel(t, img, x, y), wantInk, 3) {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("disabled primary text shows no tertiary ink %+v (the composer's disabled label color)",
			wantInk)
	}
}

// TestDisabledFooterPrimaryMatchesComposerTokens covers both themes: a clean
// page's disabled Save is the neutral disabled control (same tokens as the
// composer's disabled send), and an enabled Save keeps the accent fill.
func TestDisabledFooterPrimaryMatchesComposerTokens(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	requireFooterDisabled(t, tester, "保存", true, "model clean (light)")
	assertDisabledPrimaryInk(t, tester, "保存", design.Light())

	// Enabled primary keeps the brand accent fill (the gray-out is disabled-only).
	h.focusInput(tester, "系统提示词 输入")
	tester.Type("请简短回答。")
	tester.Frame()
	requireFooterDisabled(t, tester, "保存", false, "model dirty")
	rect, ok := tester.Find("保存")
	if !ok {
		t.Fatal("enabled Save missing")
	}
	fill := statusPixel(t, tester.Image(), int(rect.X)-8, int(rect.Y+rect.H/2))
	if !statusColorNear(fill, design.Light().Accent, 3) {
		t.Fatalf("enabled primary fill = %+v, want the accent %+v", fill, design.Light().Accent)
	}

	// Dark theme: the same neutral mechanism on the dark palette.
	h.uiRoot.Theme().SetDark(true)
	tester.Frame()
	h.click(tester, "外观")
	requireFooterDisabled(t, tester, "保存", true, "appearance clean (dark)")
	assertDisabledPrimaryInk(t, tester, "保存", design.Dark())
}
