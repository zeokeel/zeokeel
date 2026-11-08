package session_test

// Tests for the four chat modes: normal, plan, minimal and creator.

import (
	"testing"

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
	// The standard mode is the quiet default: the mode lives inside the one
	// session menu instead of a resident chip.
	h.assertTexts(tester, "会话设置")
	if got := textsContaining(tester.Texts(), "模式"); got != 0 {
		t.Fatalf("mode control rendered outside the session menu; texts = %v", tester.Texts())
	}

	h.openSessionMenu(tester)
	h.assertTexts(tester, "对话模式", "普通模式", "计划模式", "精简模式", "创作模式")

	// Minimal: normal agent mode + chat.mode minimal, with its contextual label.
	h.clickExact(tester, "精简模式")
	if got := settings.get(contracts.SettingAgentMode); got != contracts.AgentModeNormal {
		t.Fatalf("minimal agent mode = %q, want normal", got)
	}
	if got := settings.get(contracts.SettingChatMode); got != contracts.ChatModeMinimal {
		t.Fatalf("stored chat mode = %q, want minimal", got)
	}
	h.assertTexts(tester, "精简")

	// Creator: normal agent mode + chat.mode creator.
	h.openSessionMenu(tester)
	h.clickExact(tester, "创作模式")
	if got := settings.get(contracts.SettingChatMode); got != contracts.ChatModeCreator {
		t.Fatalf("stored chat mode = %q, want creator", got)
	}
	h.assertTexts(tester, "创作")

	// Plan: agent plan + chat standard (plan has gate priority).
	h.openSessionMenu(tester)
	h.clickExact(tester, "计划模式")
	if got := settings.get(contracts.SettingAgentMode); got != contracts.AgentModePlan {
		t.Fatalf("plan agent mode = %q, want plan", got)
	}
	if got := settings.get(contracts.SettingChatMode); got != contracts.ChatModeStandard {
		t.Fatalf("plan chat mode = %q, want standard", got)
	}
	h.assertTexts(tester, "计划模式")

	// Back to normal writes both settings.
	h.openSessionMenu(tester)
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

	// Plan owns the contextual label over a creator chat mode (plan priority).
	settings.set(contracts.SettingAgentMode, contracts.AgentModePlan)
	settings.set(contracts.SettingChatMode, contracts.ChatModeCreator)
	tester.Frame()
	h.assertTexts(tester, "计划模式")
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
	h.assertTexts(tester, "Settings")
	h.openSessionMenu(tester)
	h.assertTexts(tester, "Conversation mode", "Normal mode", "Plan mode", "Minimal mode", "Creator mode")
	h.clickExact(tester, "Minimal mode")
	if got := settings.get(contracts.SettingChatMode); got != contracts.ChatModeMinimal {
		t.Fatalf("English stored chat mode = %q", got)
	}
	h.assertTexts(tester, "Minimal")
}

func TestChatModeBadgeAccentDistinct(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	// The chip label is the active mode; colors are theme values.
	settings.set(contracts.SettingChatMode, contracts.ChatModeMinimal)
	tester.Frame()
	h.assertTexts(tester, "精简")
	settings.set(contracts.SettingChatMode, contracts.ChatModeCreator)
	tester.Frame()
	h.assertTexts(tester, "创作")
	h.assertNoTexts(tester, "精简")
}

// TestPermissionChipNever covers AUDIT-P3-14: the settings behavior page can
// store agent.permission=never (fail-closed); the composer chip must show that
// mode instead of falling back to "ask".
func TestPermissionChipNever(t *testing.T) {
	settings := &permissionSettings{values: map[string]string{contracts.SettingAgentPermission: contracts.PermissionNever}}
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	tester.Frame()
	// Fail-closed (never) owns the contextual label; the menu row keeps the
	// longer "从不允许" wording.
	h.assertTexts(tester, "禁止工具")
	h.assertNoTexts(tester, "询问")
	h.openSessionMenu(tester)
	h.assertTexts(tester, "权限模式", "从不允许")
}
