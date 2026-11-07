package settings_test

// Tests for the editable model catalog settings page (CRUD over
// modelcatalog.models). The page writes the same row the modelcatalog module
// reads dynamically, so the composer model menu follows immediately.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

func TestModelCatalogPageDefaults(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	if err := tester.Click("模型目录"); err != nil {
		t.Fatalf("click 模型目录: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "模型目录", "DeepSeek Chat", "deepseek-chat",
		"DeepSeek Reasoner", "deepseek-reasoner", "恢复默认", "添加")
	if got := store.get(contracts.SettingModelCatalogModels); got != "" {
		t.Fatalf("defaults wrote settings: %q", got)
	}
}

func TestModelCatalogPageAddAndDelete(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	if err := tester.Click("模型目录"); err != nil {
		t.Fatalf("click 模型目录: %v", err)
	}
	tester.Frame()

	clearAndType(t, h, tester, "名称 输入", "My Model")
	clearAndType(t, h, tester, "模型 ID 输入", "my-model")
	clearAndType(t, h, tester, "备注 输入", "test note")
	if err := tester.Click("添加"); err != nil {
		t.Fatalf("add model: %v", err)
	}
	tester.Frame()
	models, err := contracts.ParseModelInfoList(store.get(contracts.SettingModelCatalogModels))
	if err != nil {
		t.Fatalf("parse stored catalog: %v", err)
	}
	if len(models) != 3 || models[2].ID != "my-model" || models[2].Name != "My Model" {
		t.Fatalf("stored catalog = %+v", models)
	}

	// Duplicate IDs are rejected (case-insensitive) and do not write a row.
	clearAndType(t, h, tester, "名称 输入", "Dup")
	clearAndType(t, h, tester, "模型 ID 输入", "MY-MODEL")
	if err := tester.Click("添加"); err != nil {
		t.Fatalf("add duplicate: %v", err)
	}
	tester.Frame()
	models, _ = contracts.ParseModelInfoList(store.get(contracts.SettingModelCatalogModels))
	if len(models) != 3 {
		t.Fatalf("duplicate add changed catalog: %+v", models)
	}

	// Removing the first row rewrites the remaining list.
	if err := tester.Click("删除"); err != nil {
		t.Fatalf("remove model: %v", err)
	}
	tester.Frame()
	models, _ = contracts.ParseModelInfoList(store.get(contracts.SettingModelCatalogModels))
	if len(models) != 2 {
		t.Fatalf("after remove catalog = %+v", models)
	}
}

func TestModelCatalogPageRestoreDefaults(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"x","name":"X"}]`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	if err := tester.Click("模型目录"); err != nil {
		t.Fatalf("click 模型目录: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "X")
	if err := tester.Click("恢复默认"); err != nil {
		t.Fatalf("restore defaults: %v", err)
	}
	tester.Frame()
	if got := store.get(contracts.SettingModelCatalogModels); got != "[]" {
		t.Fatalf("restore wrote %q, want []", got)
	}
}
