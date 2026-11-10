package session_test

// Tests for the four chat modes: normal, plan, minimal and creator.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func modeTextCount(testerTexts []string, text string) int {
	count := 0
	for _, value := range testerTexts {
		if value == text {
			count++
		}
	}
	return count
}

func TestChatModeSelectorFourOptions(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	// The chip keeps its stable destination label (会话设置); the current mode is
	// a trailing value inside the menu, never the chip text (R17).
	h.assertTexts(tester, "会话设置")
	if got := textsContaining(tester.Texts(), "模式"); got != 0 {
		t.Fatalf("mode control rendered outside the session menu; texts = %v", tester.Texts())
	}

	h.openSessionMenu(tester)
	h.assertTexts(tester, "对话模式", "普通模式", "计划模式", "精简模式", "创作模式")

	// Minimal: normal agent mode + chat.mode minimal; the mode group's trailing
	// value names it (the option row repeats the same label).
	h.clickExact(tester, "精简模式")
	if got := settings.get(contracts.SettingAgentMode); got != contracts.AgentModeNormal {
		t.Fatalf("minimal agent mode = %q, want normal", got)
	}
	if got := settings.get(contracts.SettingChatMode); got != contracts.ChatModeMinimal {
		t.Fatalf("stored chat mode = %q, want minimal", got)
	}
	h.assertTexts(tester, "会话设置")
	h.openSessionMenu(tester)
	assertMenuTrailingValue(t, tester, "精简模式")

	// Creator: normal agent mode + chat.mode creator.
	h.clickExact(tester, "创作模式")
	if got := settings.get(contracts.SettingChatMode); got != contracts.ChatModeCreator {
		t.Fatalf("stored chat mode = %q, want creator", got)
	}
	h.assertTexts(tester, "会话设置")

	// Plan: agent plan + chat standard (plan has gate priority).
	h.openSessionMenu(tester)
	h.clickExact(tester, "计划模式")
	if got := settings.get(contracts.SettingAgentMode); got != contracts.AgentModePlan {
		t.Fatalf("plan agent mode = %q, want plan", got)
	}
	if got := settings.get(contracts.SettingChatMode); got != contracts.ChatModeStandard {
		t.Fatalf("plan chat mode = %q, want standard", got)
	}
	h.assertTexts(tester, "会话设置")
	h.openSessionMenu(tester)
	assertMenuTrailingValue(t, tester, "计划模式")

	// Back to normal writes both settings.
	h.clickExact(tester, "普通模式")
	if got := settings.get(contracts.SettingAgentMode); got != contracts.AgentModeNormal {
		t.Fatalf("normal agent mode = %q", got)
	}
	if got := settings.get(contracts.SettingChatMode); got != contracts.ChatModeStandard {
		t.Fatalf("normal chat mode = %q", got)
	}
	h.assertTexts(tester, "会话设置")
	if got := textsContaining(tester.Texts(), "计划模式"); got != 0 {
		t.Fatalf("plan state still on the composer row; texts = %v", tester.Texts())
	}
}

// assertMenuTrailingValue asserts one value appears in an open session menu as
// the group header's trailing value: the trailing node once plus the selectable
// option's own label and text nodes (three occurrences; a missing trailing value
// leaves two).
func assertMenuTrailingValue(t *testing.T, tester *ui.Tester, value string) {
	t.Helper()
	if got := exactTextCount(tester.Texts(), value); got != 3 {
		t.Fatalf("menu value %q appears %d times, want the trailing value plus the option (3); texts = %v", value, got, tester.Texts())
	}
}

func TestChatModeNormalizesBadValues(t *testing.T) {
	settings := newPermissionSettings(map[string]string{
		contracts.SettingChatMode:  "not-a-mode",
		contracts.SettingAgentMode: "not-a-mode",
	})
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	h.assertTexts(tester, "会话设置")
	if got := textsContaining(tester.Texts(), "模式"); got != 0 {
		t.Fatalf("bad mode rendered a chip; texts = %v", tester.Texts())
	}
	// Selecting the already-effective normal option normalizes the stored pair.
	h.openSessionMenu(tester)
	h.clickExact(tester, "普通模式")
	if got := settings.get(contracts.SettingChatMode); got != contracts.ChatModeStandard {
		t.Fatalf("normalized chat mode = %q, want standard", got)
	}
	if got := settings.get(contracts.SettingAgentMode); got != contracts.AgentModeNormal {
		t.Fatalf("normalized agent mode = %q, want normal", got)
	}

	// Plan owns the menu's mode value over a creator chat mode (plan priority)
	// while the chip label stays fixed.
	settings.set(contracts.SettingAgentMode, contracts.AgentModePlan)
	settings.set(contracts.SettingChatMode, contracts.ChatModeCreator)
	tester.Frame()
	h.assertTexts(tester, "会话设置")
	h.openSessionMenu(tester)
	assertMenuTrailingValue(t, tester, "计划模式")
}

func TestChatModeHiddenWithoutSettings(t *testing.T) {
	h := newPermissionHarness(t, permissionChat(), nil)
	tester := h.tester()
	for _, text := range []string{"普通", "精简", "创作"} {
		if got := modeTextCount(tester.Texts(), text); got != 0 {
			t.Fatalf("mode control %q rendered without settings; texts = %v", text, tester.Texts())
		}
	}
	// The quiet session menu still exists for the built-in prompt presets, but
	// it offers no mode header or options.
	h.openSessionMenu(tester)
	for _, text := range []string{"对话模式", "普通模式", "精简模式", "创作模式", "计划模式"} {
		if got := exactTextCount(tester.Texts(), text); got != 0 {
			t.Fatalf("mode option %q rendered without settings; texts = %v", text, tester.Texts())
		}
	}
}

func TestChatModeEnglish(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	h.assertTexts(tester, "Session settings")
	h.openSessionMenu(tester)
	h.assertTexts(tester, "Conversation mode", "Normal mode", "Plan mode", "Minimal mode", "Creator mode")
	h.clickExact(tester, "Minimal mode")
	if got := settings.get(contracts.SettingChatMode); got != contracts.ChatModeMinimal {
		t.Fatalf("English stored chat mode = %q", got)
	}
	h.assertTexts(tester, "Session settings")
	h.openSessionMenu(tester)
	assertMenuTrailingValue(t, tester, "Minimal mode")
}

func TestChatModeChipLabelStaysStable(t *testing.T) {
	settings := newPermissionSettings(map[string]string{
		contracts.SettingAgentPermission: contracts.PermissionAsk,
	})
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	// The chip never renames itself after the active mode: the mode is readable
	// inside the menu as the mode group's trailing value (R17).
	settings.set(contracts.SettingChatMode, contracts.ChatModeMinimal)
	tester.Frame()
	h.assertTexts(tester, "会话设置")
	h.openSessionMenu(tester)
	assertMenuTrailingValue(t, tester, "精简模式")
	h.clickExact(tester, "普通模式")

	settings.set(contracts.SettingChatMode, contracts.ChatModeCreator)
	tester.Frame()
	h.assertTexts(tester, "会话设置")
	h.openSessionMenu(tester)
	assertMenuTrailingValue(t, tester, "创作模式")
}

// TestLegacyNeverNormalizesReadOnly covers the retired fail-closed "never"
// row: it must normalize to the read-only preset (never is not a selectable
// mode any more) and the composer must never show the old 禁止工具 label.
func TestLegacyNeverNormalizesReadOnly(t *testing.T) {
	settings := &permissionSettings{values: map[string]string{contracts.SettingAgentPermission: contracts.PermissionNever}}
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "会话设置")
	h.assertNoTexts(tester, "禁止工具", "从不允许", "询问", "只读")
	h.openSessionMenu(tester)
	h.assertTexts(tester, "权限模式", "只读", "工作区可写", "所有权限")
	assertMenuTrailingValue(t, tester, "只读")
	h.assertNoTexts(tester, "从不允许")
	readOnly, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "只读")
	if !ok || !readOnly.Checked {
		t.Fatalf("legacy never row does not normalize to the checked read-only radio; texts = %v", tester.Texts())
	}
}
