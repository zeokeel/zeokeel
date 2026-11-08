package settings_test

// ZK-15: the model page shows a restrained, non-blocking hint when the
// configured/draft model is not a model-catalog preset and — once a successful
// connection check has discovered a model list — not one of those discovered
// ids either. Unknown ids are still accepted and saved (the endpoint silently
// falls back to its own default model, which is what the hint warns about).

import (
	"context"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// modelHintHarness opens the model page with a chat config whose model is the
// given id and a usable OpenAI-compatible endpoint. The store starts empty so
// the settings catalog falls back to the built-in presets.
func modelHintHarness(t *testing.T, model string) (*harness, *fakeSettings) {
	t.Helper()
	store := newFakeSettings(nil)
	chat := &fakeChat{config: contracts.ChatConfig{
		Model:   model,
		BaseURL: "https://api.example.com/v1",
		APIKey:  "sk-test",
	}}
	h := modelCheckHarness(t, store, chat)
	return h, store
}

// TestModelUnknownModelShowsHintAndStillSaves covers the custom-model case: the
// hint renders, and saving is not blocked — the entered name is what persists.
func TestModelUnknownModelShowsHintAndStillSaves(t *testing.T) {
	h, store := modelHintHarness(t, "my-custom-model")
	tester := h.tester()
	h.assertTexts(tester, "该模型不在内置目录中")

	h.click(tester, "保存")
	if got := store.get(contracts.SettingsChatModel); got != "my-custom-model" {
		t.Fatalf("saved model = %q, want the entered custom name", got)
	}
	h.assertTexts(tester, "已保存")
}

// TestModelPresetModelShowsNoHint covers the catalog-preset case: a model that
// is one of the built-in catalog presets shows no warning.
func TestModelPresetModelShowsNoHint(t *testing.T) {
	h, _ := modelHintHarness(t, "deepseek-chat")
	tester := h.tester()
	h.assertNoTexts(tester, "该模型不在内置")
}

// TestModelDiscoveredListControlsHint covers the post-check cases: an unlisted
// model warns once the check discovered a list, while a discovered model does
// not.
func TestModelDiscoveredListControlsHint(t *testing.T) {
	withModelListGetter(t, func(context.Context, string, string) (int, []byte, error) {
		return 200, []byte(`{"data":[{"id":"model-a"},{"id":"model-b"}]}`), nil
	})

	// Unlisted custom model: the discovered list does not contain it.
	h, _ := modelHintHarness(t, "my-custom-model")
	tester := h.tester()
	h.click(tester, "测试当前输入")
	waitModelCheckText(t, h, tester, "连接正常")
	h.assertTexts(tester, "该模型不在内置/已发现目录中")

	// Discovered model: the hint disappears.
	h2, _ := modelHintHarness(t, "model-a")
	tester2 := h2.tester()
	h2.click(tester2, "测试当前输入")
	waitModelCheckText(t, h2, tester2, "连接正常")
	h2.assertNoTexts(tester2, "该模型不在内置")
}

// TestModelHintEnglishCopy covers the localized English copy.
func TestModelHintEnglishCopy(t *testing.T) {
	h, _ := modelHintHarness(t, "my-custom-model")
	tester := h.tester()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "not in the built-in catalog")
}
