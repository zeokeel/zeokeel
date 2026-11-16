package settings_test

// Wave 3 settings items: the empty model list names the assembly-provided
// default model so the page and the composer chip tell the same story.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// TestModelPageEmptyStateNamesDefaultModel covers the model page's empty list:
// when the assembly still provides a default model, the empty state says which
// model is in effect and that adding a row overrides it.
func TestModelPageEmptyStateNamesDefaultModel(t *testing.T) {
	chat := &fakeChat{}
	chat.config = contracts.ChatConfig{Model: "gpt-4o-mini"}
	store := newFakeSettings(nil)
	h := newHarness(t, chat, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")

	h.assertTexts(tester, "还没有模型", "默认模型", "gpt-4o-mini")
}
