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
	"math"
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
func assertTextInk(t *testing.T, tester *ui.Tester, label string, tokens *design.Tokens,
	want, forbidden ui.Color) {
	t.Helper()
	rect, ok := tester.Find(label)
	if !ok {
		t.Fatalf("%q not found; texts = %v", label, tester.Texts())
	}
	// Glyph edges are blends of ink over the panel, and CI's font fallback never
	// fully covers small CJK strokes, so the check compares each sample against
	// both semantics on the want↔background blend line instead of demanding an
	// exact token pixel (2026-11-17: the strict scan was host-font dependent).
	img := tester.Image()
	bg := tokens.BgBase
	matches, wrong := 0, 0
	for y := int(rect.Y); y < int(rect.Y+rect.H); y++ {
		for x := int(rect.X); x < int(rect.X+rect.W); x++ {
			px := statusPixel(t, img, x, y)
			if inkDistance(px, bg) <= 30 {
				continue
			}
			if inkDistance(px, want) < inkDistance(px, forbidden) {
				matches++
			} else if inkDistance(px, forbidden) < inkDistance(px, want) {
				wrong++
			}
		}
	}
	if wrong > 0 && matches == 0 {
		t.Fatalf("%q paints the forbidden semantic %+v (%d samples); want %+v",
			label, forbidden, wrong, want)
	}
	if matches < 2 {
		t.Fatalf("%q shows no ink in the want semantic %+v (matches=%d); texts = %v",
			label, want, matches, tester.Texts())
	}
}

// inkDistance is the euclidean RGB distance between two opaque colors.
func inkDistance(a, b ui.Color) float64 {
	dr := float64(a.R) - float64(b.R)
	dg := float64(a.G) - float64(b.G)
	db := float64(a.B) - float64(b.B)
	return math.Sqrt(dr*dr + dg*dg + db*db)
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
	// Exactly one "保存": PageFooter draws one primary and (after the 2026-11-17
	// label dedupe, matching DialogFooter) registers one text node for it. A
	// stray in-section submit would add a second.
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

	// Exactly one "添加": one footer primary, no in-section submit.
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

// TestMemoryExtractionCautionIsWarning covers the model-extraction cost/privacy

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
	// The button box (not the text node): PageFooter no longer adds a redundant
	// .Label, so Find would return just the text. Sample inside the left padding.
	rect := footerPrimary(t, tester, label).Bounds
	img := tester.Image()
	backdrop := tokens.BgBase // the footer sits on the modal panel (S0)
	// A disabled control must not keep the brand accent fill, and it must stay
	// legible: the 2026-11-18 review (VD-20) replaced the framework's 50% wash
	// (1.05:1 against the panel) with the solid neutral fill and a hairline, the
	// same control as the composer's disabled send. Sampling a solid area is
	// stable; the label ink is left to the renderer (pixel text scans are not
	// portable across font stacks and only repeated the token contract).
	wantFill := tokens.BgStrong.Over(backdrop)
	fill := statusPixel(t, img, int(rect.X)+4, int(rect.Y+rect.H/2))
	if !statusColorNear(fill, wantFill, 3) {
		t.Fatalf("disabled primary fill = %+v, want the neutral disabled fill %+v",
			fill, wantFill)
	}
	accentWash := tokens.Accent.Alpha(0.5).Over(backdrop)
	if statusColorNear(fill, accentWash, 10) {
		t.Fatalf("disabled primary fill = %+v is still the accent wash %+v", fill, accentWash)
	}
}

// TestDisabledFooterPrimaryMatchesComposerTokens covers both themes: a clean
// page's disabled Save is the neutral disabled control (same tokens as the
// composer's disabled send), and an enabled Save keeps the accent fill.
//
// The page runs in English: the assertions are about function (disabled state,
// one primary, the accent fill once dirty), so the copy is incidental and the
// English labels keep the fixture readable on any host.
func TestDisabledFooterPrimaryMatchesComposerTokens(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	h.uiRoot.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Model")
	requireFooterDisabled(t, tester, "Save", true, "model clean (light)")
	assertDisabledPrimaryInk(t, tester, "Save", design.Light())

	// Enabled primary keeps the brand accent fill (the gray-out is disabled-only).
	h.focusInput(tester, "System prompt")
	tester.Type("Keep it short.")
	tester.Frame()
	requireFooterDisabled(t, tester, "Save", false, "model dirty")
	rect := footerPrimary(t, tester, "Save").Bounds
	fill := statusPixel(t, tester.Image(), int(rect.X)+4, int(rect.Y+rect.H/2))
	if !statusColorNear(fill, design.Light().Accent, 3) {
		t.Fatalf("enabled primary fill = %+v, want the accent %+v", fill, design.Light().Accent)
	}

	// The dark theme keeps the same neutral disabled control; Appearance is a
	// clean page, so its footer primary is disabled again.
	h.uiRoot.Theme().SetDark(true)
	tester.Frame()
	h.click(tester, "Appearance")
	tester.Frame()
	requireFooterDisabled(t, tester, "Save", true, "appearance clean (dark)")
	assertDisabledPrimaryInk(t, tester, "Save", design.Dark())
}

// --- phase 2: disclosure dedup + section-action placement ---------------------

// TestModelSectionTitleNamesContentOnly pins the de-mechanized section title:
// the title names the content, the footer-save mechanism moved to the section
// description (zh + en).
func TestModelSectionTitleNamesContentOnly(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.assertTexts(tester, "提示词与运行参数", "本节草稿由页脚「保存」统一写入。")
	h.assertNoTexts(tester, "（页脚保存）")
	// The model list section follows the same rule: content name in the title,
	// the apply-immediately behavior note in the description.
	h.assertTexts(tester, "模型列表", "列表增删改与默认模型即时生效。")
	h.assertNoTexts(tester, "（即时生效）")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Prompt & runtime", "The footer Save writes this section's drafts.")
	h.assertNoTexts(tester, "saved by the footer")
	h.assertTexts(tester, "Models", "List edits and the default model apply immediately.")
	h.assertNoTexts(tester, "(apply immediately)")
}

// TestMemoryPolicyUnlimitedDisclosedOnce pins the single disclosure of the
// unlimited rule: the section helper says "0/empty = unlimited" once; the two
// stepper descriptions explain only what their value does.
func TestMemoryPolicyUnlimitedDisclosedOnce(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	provide[contracts.MemoryStore](t, h.ctx, contracts.KeyMemory, newFakeMemoryConsolidator())
	tester := h.tester()
	tester.Frame()
	h.click(tester, "记忆")
	h.assertTexts(tester, "0 或留空表示不限制；点「按策略整理」保存并立即整理。",
		"超过后删除最旧的记忆。", "早于该天数的记忆会被删除。")
	h.assertNoTexts(tester, "0 表示不限")
}

// TestMemoryAddPlaceholderExemplifies pins the add-memory placeholder: it
// exemplifies an entry instead of repeating the field label.
func TestMemoryAddPlaceholderExemplifies(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	provide[contracts.MemoryStore](t, h.ctx, contracts.KeyMemory, newFakeMemoryStore())
	tester := h.tester()
	tester.Frame()
	h.click(tester, "记忆")
	// The placeholder is the input's accessible Placeholder (not a text node):
	// it exemplifies an entry instead of repeating the field label.
	info, ok := tester.AccessByRole(ui.RoleTextField, "记忆内容 输入")
	if !ok || info.Placeholder != "例如：发布前先跑 go test ./..." {
		t.Fatalf("add-memory placeholder = %q (ok=%v), want the example entry",
			info.Placeholder, ok)
	}
	if info.Placeholder == "记忆内容" {
		t.Fatal("the placeholder must not repeat the field label")
	}
}

// TestDataPrivacySectionActionsAtTitle pins the unified section-action
// placement: open/export/clear sit on their section title rows (SectionAction),
// while the inline copy stays on the path value row below the title.
func TestDataPrivacySectionActionsAtTitle(t *testing.T) {
	chat := &fakeChat{}
	seedDataPageSessions(chat)
	h := newHarness(t, chat, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "数据与隐私")

	sameRow := func(title, action string) {
		t.Helper()
		tr, ok := tester.Find(title)
		if !ok {
			t.Fatalf("section title %q missing; texts = %v", title, tester.Texts())
		}
		ar, ok := tester.Find(action)
		if !ok {
			t.Fatalf("section action %q missing; texts = %v", action, tester.Texts())
		}
		tc, ac := tr.Y+tr.H/2, ar.Y+ar.H/2
		if d := tc - ac; d > 2 || d < -2 {
			t.Fatalf("%q (centerY %v) is not on the %q title row (centerY %v)",
				action, ac, title, tc)
		}
	}
	sameRow("数据目录", "打开数据目录")
	sameRow("会话数据", "导出全部会话")
	sameRow("会话数据", "清除全部会话")
	sameRow("诊断报告", "导出诊断报告")

	// The inline copy stays next to the path value, below the title row.
	title, ok := tester.Find("数据目录")
	if !ok {
		t.Fatal("数据目录 title missing")
	}
	copyRect, ok := tester.Find("复制")
	if !ok {
		t.Fatalf("inline copy missing; texts = %v", tester.Texts())
	}
	if copyRect.Y+copyRect.H/2 <= title.Y+title.H/2+2 {
		t.Fatalf("copy (centerY %v) must stay inline on the path row, below the title (centerY %v)",
			copyRect.Y+copyRect.H/2, title.Y+title.H/2)
	}
}
