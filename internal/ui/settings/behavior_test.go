package settings_test

// TDD tests (RED first) for the Behavior page: the default agent permission.

import (
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

func TestBehaviorPageDefaultsWorkspace(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	// The default is workspace-write: normal in-workspace file edits need no
	// prompt. Exactly the three presets are listed, in preset order.
	h.assertTexts(tester, "行为", "默认代理权限", "当前：工作区可写",
		"只读", "工作区可写", "所有权限",
		"只自动允许只读与信息类工具，其他操作会询问",
		"工作区内的文件读写自动允许，其他操作询问", "修改立即生效")
	h.assertNoTexts(tester, "完全访问", "从不")
	if raw := store.get(contracts.SettingAgentPermission); raw != "" {
		t.Fatalf("defaults wrote the permission row: %q", raw)
	}
}

func TestBehaviorPageSelectsPermission(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")

	h.click(tester, "工作区可写") // open the current-value menu
	h.assertTexts(tester, "只读", "所有权限")
	h.click(tester, "所有权限")
	if raw := store.get(contracts.SettingAgentPermission); raw != contracts.PermissionFull {
		t.Fatalf("selecting full wrote %q", raw)
	}
	h.assertTexts(tester, "已保存", "当前：所有权限")

	h.click(tester, "所有权限") // reopen with the new current label
	h.click(tester, "只读")
	if raw := store.get(contracts.SettingAgentPermission); raw != contracts.PermissionReadOnly {
		t.Fatalf("selecting read-only wrote %q", raw)
	}
	h.assertTexts(tester, "当前：只读")
}

// TestBehaviorPageNormalizesLegacyPermissionRows covers the data migration: the
// retired stored values "ask" and "never" display (and are treated) as the
// read-only preset instead of silently falling back to workspace or vanishing.
func TestBehaviorPageNormalizesLegacyPermissionRows(t *testing.T) {
	for _, legacy := range []string{"ask", "ask ", "NEVER"} {
		store := newFakeSettings(map[string]string{
			contracts.SettingAgentPermission: legacy,
		})
		h := newHarness(t, &fakeChat{}, store, settings.Config{})
		tester := h.tester()
		tester.Frame()
		h.click(tester, "行为")
		h.assertTexts(tester, "当前：只读")
		// Rendering must not rewrite the stored legacy row by itself.
		if raw := store.get(contracts.SettingAgentPermission); raw != legacy {
			t.Fatalf("a frame rewrote legacy permission %q to %q", legacy, raw)
		}
	}
}

func TestBehaviorPageBadValueNormalizedWorkspace(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingAgentPermission: "bogus-mode",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	h.assertTexts(tester, "当前：工作区可写")
	// Read-only is still selectable; choosing it writes the explicit value.
	h.click(tester, "工作区可写")
	h.click(tester, "只读")
	if raw := store.get(contracts.SettingAgentPermission); raw != contracts.PermissionReadOnly {
		t.Fatalf("selecting read-only wrote %q", raw)
	}
	tester.Frame()
	h.assertTexts(tester, "当前：只读")
}

func TestBehaviorPageWriteFailure(t *testing.T) {
	store := newFakeSettings(nil)
	store.setErr = errBehaviorStoreWrite
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	h.click(tester, "工作区可写")
	h.click(tester, "只读")
	h.assertTexts(tester, "保存失败：")
}

func TestBehaviorPageEnglishCopy(t *testing.T) {
	store := newFakeSettings(nil)
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Behavior")
	h.assertTexts(tester, "Behavior", "Default agent permission", "Current: Workspace-write",
		"Read-only", "Workspace-write", "All permissions",
		"Auto-allow read-only and informational tools; ask for the rest",
		"Auto-allow workspace file reads and writes; ask for the rest",
		"Changes apply immediately")
	h.assertNoTexts(tester, "Ask", "Full access", "Never")
	h.click(tester, "Workspace-write")
	h.click(tester, "All permissions")
	if raw := store.get(contracts.SettingAgentPermission); raw != contracts.PermissionFull {
		t.Fatalf("English selection wrote %q", raw)
	}
	h.assertTexts(tester, "Saved", "Current: All permissions")
}

// errBehaviorStoreWrite scripts one settings write failure.
var errBehaviorStoreWrite = errors.New("behavior store boom")

// expandMoreConversationSettings opens the collapsed 更多对话设置 / More conversation
// settings disclosure and settles its opening motion (the native Collapsible
// animates for 200ms, so the inner controls are not laid out until the frame is
// advanced; same shape as rightpanel_test.go's Diagnostics disclosure helper).
func expandMoreConversationSettings(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	tester.Frame()
	tester.Advance(350 * time.Millisecond)
	tester.Frame()
	// The OS sandbox section sits above the disclosure, so on the 1200x800
	// test window the trigger starts below the scroll viewport: scroll it into
	// view first, as a user would.
	ensureVisibleInFrame(t, tester, label)
	if err := tester.Click(label); err != nil {
		t.Fatalf("expand %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
	tester.Advance(350 * time.Millisecond)
	tester.Frame()
}

func TestBehaviorPageGoalAutoContinue(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	expandMoreConversationSettings(t, tester, "更多对话设置")
	h.assertTexts(tester, "目标自动续跑",
		"回合结束后若目标仍激活，自动发送继续推进提示（每个目标最多 5 次）")
	if raw := store.get(contracts.SettingGoalAutoContinue); raw != "" {
		t.Fatalf("defaults wrote the goal-auto-continue row: %q", raw)
	}
	h.click(tester, "目标自动续跑")
	if raw := store.get(contracts.SettingGoalAutoContinue); raw != "true" {
		t.Fatalf("enabling goal auto-continue wrote %q", raw)
	}
	h.click(tester, "目标自动续跑")
	if raw := store.get(contracts.SettingGoalAutoContinue); raw != "false" {
		t.Fatalf("disabling goal auto-continue wrote %q", raw)
	}
}

func TestBehaviorPageGoalAutoContinueBadValueAndEnglish(t *testing.T) {
	store := newFakeSettings(map[string]string{contracts.SettingGoalAutoContinue: "garbage"})
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Behavior")
	expandMoreConversationSettings(t, tester, "More conversation settings")
	h.assertTexts(tester, "Auto-continue goal",
		"After a turn ends while the goal is still active, automatically send a continuation prompt (at most 5 times per goal)")
	h.click(tester, "Auto-continue goal")
	if raw := store.get(contracts.SettingGoalAutoContinue); raw != "true" {
		t.Fatalf("English bad-value toggle wrote %q, want true", raw)
	}
}

// TestBehaviorPageCollapsesLowFrequencySections covers the page's progressive
// disclosure: the daily permission setting stays visible, the four
// lower-frequency conversation controls are collapsed behind one trigger, and
// only the muted summary hints at them.
func TestBehaviorPageCollapsesLowFrequencySections(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	h.assertTexts(tester, "默认代理权限", "更多对话设置", "排队发送：开")
	h.assertNoTexts(tester, "运行中允许排队发送", "目标自动续跑",
		"工作区指令文件", "选择对话文字大小")

	// Expanding reveals every moved control and the summary gives way to them.
	expandMoreConversationSettings(t, tester, "更多对话设置")
	h.assertTexts(tester, "运行中发送", "运行中允许排队发送", "目标自动续跑",
		"消息字号", "工作区指令文件")
	h.assertNoTexts(tester, "排队发送：")

	// A representative setting still persists from inside the disclosure.
	// Queueing defaults on, so the first click turns it off.
	h.click(tester, "运行中允许排队发送")
	if raw := store.get(contracts.SettingSendWhileRunning); raw != "false" {
		t.Fatalf("collapsed-section queue toggle wrote %q, want false", raw)
	}
}

// TestBehaviorPageCollapsedSummaryReflectsSettings covers the muted digest shown
// while the disclosure is closed.
func TestBehaviorPageCollapsedSummaryReflectsSettings(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingSendWhileRunning: "true",
		contracts.SettingGoalAutoContinue: "true",
		contracts.SettingChatFontSize:     "large",
		contracts.SettingAgentsFile:       "RULES.md",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	h.assertTexts(tester, "排队发送：开", "目标续跑：开", "消息字号：大",
		"指令文件：RULES.md")
}

// TestBehaviorPageMoreSettingsEnglish covers the English copy of the disclosure
// trigger, the digest and the expanded controls.
func TestBehaviorPageMoreSettingsEnglish(t *testing.T) {
	store := newFakeSettings(nil)
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Behavior")
	h.assertTexts(tester, "More conversation settings", "Queue: On")
	expandMoreConversationSettings(t, tester, "More conversation settings")
	h.assertTexts(tester, "Sending while running", "Queue prompts while running",
		"Auto-continue goal", "Message text size", "Workspace instructions file")
}
