package session_test

// Wave 3 UI copy/scope items:
//   - memory rows bound to a workspace show a human-readable scope label, never
//     the raw "workspace:<id>" storage value;
//   - the model chip names the assembly's default model when no model row is
//     configured (FR-26), with the explanation in the chip menu.

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestModelChipNamesAssemblyDefault covers FR-26: with no model row configured
// but an assembly-provided default model, the chip says the model is a default
// without a configured connection and the menu explains where it comes from.
func TestModelChipNamesAssemblyDefault(t *testing.T) {
	chat := modelThinkingChat("gpt-4o-mini") // fake config model, empty catalog
	h := newModelThinkingHarness(t, chat, newPermissionSettings(nil), &thinkingCatalog{})
	tester := h.tester()

	const chip = "gpt-4o-mini（默认模型，尚未配置连接）"
	h.assertTexts(tester, chip)
	h.assertNoExactTexts(tester, "未配置")
	h.clickExact(tester, chip)
	h.assertTexts(tester, "默认模型来自装配，添加模型行后可覆盖")
	tester.Key(0, ui.KeyEscape)
}
