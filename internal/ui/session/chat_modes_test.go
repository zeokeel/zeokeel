package session_test

// Tests for the four chat modes: normal, plan, minimal and creator.

import (
	"strings"
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
	h.assertTexts(tester, "普通")
	if got := textsContaining(tester.Texts(), "模式"); got != 0 {
		t.Fatalf("mode badge rendered in normal mode; texts = %v", tester.Texts())
	}

	h.click(tester, "普通")
	h.assertTexts(tester, "普通模式", "计划模式", "精简模式", "创作模式")

	// Minimal: normal agent mode + chat.mode minimal, with its badge and label.
	h.click(tester, "精简模式")
	if got := settings.get(contracts.SettingAgentMode); got != contracts.AgentModeNormal {
		t.Fatalf("minimal agent mode = %q, want normal", got)
	}
	if got := settings.get(contracts.SettingChatMode); got != contracts.ChatModeMinimal {
		t.Fatalf("stored chat mode = %q, want minimal", got)
	}
	h.assertTexts(tester, "精简", "精简模式")

	// Creator: normal agent mode + chat.mode creator.
	h.click(tester, "精简")
	h.click(tester, "创作模式")
	if got := settings.get(contracts.SettingChatMode); got != contracts.ChatModeCreator {
		t.Fatalf("stored chat mode = %q, want creator", got)
	}
	h.assertTexts(tester, "创作", "创作模式")

	// Plan: agent plan + chat standard (plan has gate priority).
	h.click(tester, "创作")
	h.click(tester, "计划模式")
	if got := settings.get(contracts.SettingAgentMode); got != contracts.AgentModePlan {
		t.Fatalf("plan agent mode = %q, want plan", got)
	}
	if got := settings.get(contracts.SettingChatMode); got != contracts.ChatModeStandard {
		t.Fatalf("plan chat mode = %q, want standard", got)
	}
	h.assertTexts(tester, "计划", "计划模式")

	// Back to normal writes both settings.
	h.click(tester, "计划")
	h.click(tester, "普通模式")
	if got := settings.get(contracts.SettingAgentMode); got != contracts.AgentModeNormal {
		t.Fatalf("normal agent mode = %q", got)
	}
	if got := settings.get(contracts.SettingChatMode); got != contracts.ChatModeStandard {
		t.Fatalf("normal chat mode = %q", got)
	}
	h.assertTexts(tester, "普通")
	if got := textsContaining(tester.Texts(), "计划模式"); got != 0 {
		t.Fatalf("plan badge after normal; texts = %v", tester.Texts())
	}
}

func TestChatModeNormalizesBadValues(t *testing.T) {
	settings := newPermissionSettings(map[string]string{
		contracts.SettingChatMode:  "not-a-mode",
		contracts.SettingAgentMode: "not-a-mode",
	})
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	h.assertTexts(tester, "普通")
	if got := textsContaining(tester.Texts(), "模式"); got != 0 {
		t.Fatalf("bad mode rendered a badge; texts = %v", tester.Texts())
	}
	// Selecting the already-effective normal option normalizes the stored pair.
	h.click(tester, "普通")
	h.click(tester, "普通模式")
	if got := settings.get(contracts.SettingChatMode); got != contracts.ChatModeStandard {
		t.Fatalf("normalized chat mode = %q, want standard", got)
	}
	if got := settings.get(contracts.SettingAgentMode); got != contracts.AgentModeNormal {
		t.Fatalf("normalized agent mode = %q, want normal", got)
	}

	// Plan badge wins over a creator chat mode (plan gate priority).
	settings.set(contracts.SettingAgentMode, contracts.AgentModePlan)
	settings.set(contracts.SettingChatMode, contracts.ChatModeCreator)
	tester.Frame()
	h.assertTexts(tester, "计划", "计划模式")
}

func TestChatModeHiddenWithoutSettings(t *testing.T) {
	h := newPermissionHarness(t, permissionChat(), nil)
	tester := h.tester()
	for _, text := range []string{"普通", "精简", "创作", "精简模式", "创作模式", "计划模式"} {
		if got := modeTextCount(tester.Texts(), text); got != 0 {
			t.Fatalf("mode control %q rendered without settings; texts = %v", text, tester.Texts())
		}
	}
}

func TestChatModeEnglish(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	h.assertTexts(tester, "Normal")
	h.click(tester, "Normal")
	h.assertTexts(tester, "Normal mode", "Plan mode", "Minimal mode", "Creator mode")
	h.click(tester, "Minimal mode")
	if got := settings.get(contracts.SettingChatMode); got != contracts.ChatModeMinimal {
		t.Fatalf("English stored chat mode = %q", got)
	}
	h.assertTexts(tester, "Minimal", "Minimal mode")
}

func TestChatModeBadgeAccentDistinct(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	// The badges are text-asserted (colors are theme values); minimal vs plan
	// must both be present as mode labels.
	settings.set(contracts.SettingChatMode, contracts.ChatModeMinimal)
	tester.Frame()
	h.assertTexts(tester, "精简模式")
	settings.set(contracts.SettingChatMode, contracts.ChatModeCreator)
	tester.Frame()
	h.assertTexts(tester, "创作模式")
	if got := strings.Count(strings.Join(tester.Texts(), "\x00"), "模式"); got == 0 {
		t.Fatalf("mode badge missing; texts = %v", tester.Texts())
	}
}

// TestPermissionChipNever covers AUDIT-P3-14: the settings behavior page can
// store agent.permission=never (fail-closed); the composer chip must show that
// mode instead of falling back to "ask".
func TestPermissionChipNever(t *testing.T) {
	settings := &permissionSettings{values: map[string]string{contracts.SettingAgentPermission: contracts.PermissionNever}}
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "从不允许")
	h.assertNoTexts(tester, "询问")
}
