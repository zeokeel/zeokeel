package session_test

// 2026-11 UI review P2: the 推理等级 submenu's trade-off hints render as inline
// subtitles (a second muted line inside each row), not as floating tooltips. A
// tooltip hung above the submenu and covered the parent menu's first row value
// and target band; an inline subtitle adds no overlay layer, so the parent menu
// stays fully visible.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestReasoningSubmenuHintIsInlineNotTooltip(t *testing.T) {
	settings := modelCatalogSettings(t, []contracts.ModelInfo{{
		ID: "qwen3.8", Name: "Qwen 3.8", Family: contracts.ModelFamilyQwen,
		DefaultThinking: contracts.ThinkingModeQwenDeep,
	}})
	h := newModelThinkingHarness(t, modelThinkingChat("qwen3.8"), settings,
		settingsModelCatalog{settings})
	tester := h.tester()

	openChipMenu(t, h, tester, "qwen3.8")
	openReasoningLevelMenu(t, h, tester)

	// The trade-off copy is present as inline subtitle text inside the submenu.
	h.assertTexts(tester, "关闭思考：最快、最省，直接回答。", "开启思考：先推理再回答。")

	// No floating tooltip overlay is drawn (the old hint mechanism that covered
	// the parent menu).
	for _, info := range tester.AccessAll() {
		if info.Role == ui.RoleTooltip {
			t.Fatalf("the reasoning submenu still floats a tooltip overlay: %+v", info)
		}
	}

	// The parent menu's 推理等级 entry stays visible/reachable beside the open
	// submenu — nothing covers it.
	if _, ok := tester.Find("推理等级"); !ok {
		t.Fatalf("the parent menu entry is covered/missing; texts = %v", tester.Texts())
	}
}
