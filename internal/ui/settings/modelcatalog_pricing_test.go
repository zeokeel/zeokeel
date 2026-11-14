package settings_test

// Frame tests for model pricing: the editor's three optional per-million USD
// fields (input/cached input/output), their inline validation, saving into the
// catalog row with list read-back, and clearing the prices back to "not
// configured" without inventing $0.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// pricedFixture is deepseekFixture with prices configured.
func pricedFixture() contracts.ModelInfo {
	model := deepseekFixture()
	model.InputPricePerMillion = 2
	model.CachedInputPricePerMillion = 0.5
	model.OutputPricePerMillion = 8
	return model
}

func TestModelPricingEditSavesAndRowEchoes(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: modelCatalogRow(t, deepseekFixture()),
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	openEditModal(t, h, tester, "deepseek-chat")
	expandCatalogAdvanced(t, h, tester)
	scrollDialogBody(t, tester, 300)
	h.assertTexts(tester, "价格（可选）", "输入价格 / 1M", "缓存输入 / 1M", "输出价格 / 1M")
	h.assertTexts(tester, "每 1M tokens 的美元价格；留空或 0 表示未配置。价格只用于成本估算，按当前价格重算。")

	clearAndType(t, h, tester, "输入价格 / 1M 输入", "2")
	clearAndType(t, h, tester, "缓存输入 / 1M 输入", "0.5")
	clearAndType(t, h, tester, "输出价格 / 1M 输入", "8")
	h.click(tester, "保存模型")
	tester.Frame()

	h.assertNoTexts(tester, "价格（可选）")
	h.assertTexts(tester, "价格：输入 $2/M · 缓存 $0.5/M · 输出 $8/M")
	models := catalogStoredModels(t, store)
	if len(models) != 1 {
		t.Fatalf("stored catalog = %+v", models)
	}
	got := models[0]
	if got.InputPricePerMillion != 2 || got.CachedInputPricePerMillion != 0.5 ||
		got.OutputPricePerMillion != 8 || !got.HasPricing() {
		t.Fatalf("stored prices = %+v", got)
	}
	if cost, ok := got.Cost(1_000_000, 0, 1_000_000); !ok || cost != 10 {
		t.Fatalf("Cost(1M,0,1M) = %v/%v, want 10/true", cost, ok)
	}
	// Cached tokens are priced at the cached rate: 2M prompt (1M cached) + 1M out.
	if cost, ok := got.Cost(2_000_000, 1_000_000, 1_000_000); !ok || cost != 2+0.5+8 {
		t.Fatalf("Cost(2M,1M,1M) = %v/%v, want 10.5/true", cost, ok)
	}
}

func TestModelPricingLeaveBlankAndZeroClear(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: modelCatalogRow(t, pricedFixture()),
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	h.assertTexts(tester, "价格：输入 $2/M · 缓存 $0.5/M · 输出 $8/M")

	openEditModal(t, h, tester, "deepseek-chat")
	expandCatalogAdvanced(t, h, tester)
	scrollDialogBody(t, tester, 300)
	// Prefilled with the configured values.
	if got := inputValue(t, tester, "输入价格 / 1M 输入"); got != "2" {
		t.Fatalf("input price prefill = %q, want 2", got)
	}
	clearAndType(t, h, tester, "输入价格 / 1M 输入", "")
	clearAndType(t, h, tester, "缓存输入 / 1M 输入", "0")
	clearAndType(t, h, tester, "输出价格 / 1M 输入", "")
	h.click(tester, "保存模型")
	tester.Frame()

	got := catalogStoredModels(t, store)[0]
	if got.HasPricing() || got.InputPricePerMillion != 0 ||
		got.CachedInputPricePerMillion != 0 || got.OutputPricePerMillion != 0 {
		t.Fatalf("cleared prices = %+v, want all unconfigured", got)
	}
	// The row drops the pricing line instead of showing $0.
	h.assertNoTexts(tester, "价格：", "输入 $")
}

func TestModelPricingValidationKeepsModalOpen(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: modelCatalogRow(t, deepseekFixture()),
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	openEditModal(t, h, tester, "deepseek-chat")
	expandCatalogAdvanced(t, h, tester)
	scrollDialogBody(t, tester, 300)

	cases := []struct{ raw, want string }{
		{"-1", "输入价格不能为负数"},
		{"abc", "输入价格必须是数字"},
		{"NaN", "输入价格必须是数字"},
		{"2000000000", "输入价格过大（上限 1,000,000,000）"},
	}
	for _, tc := range cases {
		clearAndType(t, h, tester, "输入价格 / 1M 输入", tc.raw)
		h.click(tester, "保存模型")
		tester.Frame()
		h.assertTexts(tester, tc.want, "保存模型")
		// A rejected save writes nothing.
		if stored := catalogStoredModels(t, store)[0]; stored.HasPricing() {
			t.Fatalf("invalid price %q was stored: %+v", tc.raw, stored)
		}
	}
	// Fixing the value saves normally.
	clearAndType(t, h, tester, "输入价格 / 1M 输入", "1.5")
	h.click(tester, "保存模型")
	tester.Frame()
	if got := catalogStoredModels(t, store)[0]; got.InputPricePerMillion != 1.5 {
		t.Fatalf("fixed input price = %+v, want 1.5", got)
	}
}

func TestModelPricingEnglishCopy(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: modelCatalogRow(t, pricedFixture()),
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Pricing: input $2/M · cached $0.5/M · output $8/M")

	h.click(tester, "Edit model deepseek-chat")
	tester.Frame()
	h.assertTexts(tester, "Edit model", "Save model")
	h.click(tester, "Generation, pricing & custom modes")
	tester.Frame()
	scrollDialogBody(t, tester, 300)
	h.assertTexts(tester, "Pricing (optional)",
		"USD per 1M tokens; blank or 0 means not configured. Prices are used for cost estimates only and are applied at current prices.",
		"Input price / 1M", "Cached input / 1M", "Output price / 1M")
}

func TestModelPricingNarrowWindow(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: modelCatalogRow(t, pricedFixture()),
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.SetSize(704, 720)
	tester.Frame()
	h.click(tester, "模型")
	tester.Frame()
	openEditModal(t, h, tester, "deepseek-chat")
	expandCatalogAdvanced(t, h, tester)
	scrollDialogBody(t, tester, 320)
	h.assertTexts(tester, "价格（可选）", "输入价格 / 1M", "缓存输入 / 1M", "输出价格 / 1M")
	// The modal still saves at the narrow width (the fields are laid out in three
	// equal columns that stay inside the clamped dialog).
	if got := inputValue(t, tester, "输入价格 / 1M 输入"); got != "2" {
		t.Fatalf("narrow input price prefill = %q, want 2", got)
	}
}

func TestModelPricingCollapsedSummaryMentionsPrices(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: modelCatalogRow(t, pricedFixture()),
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	openEditModal(t, h, tester, "deepseek-chat")
	// The collapsed advanced group summarizes the configured prices, so the
	// editor shows they are set without expanding it.
	h.assertTexts(tester, "价格：输入 $2/M · 缓存 $0.5/M · 输出 $8/M")
}
