package home_test

// Frame tests for US-07 (2026-11 wave D, Home model menu side): the reasoning
// submenu has a SINGLE default expression (系列默认) — the redundant "默认" mode
// item that meant the same thing is dropped — and the strength levels carry a
// plain-language trade-off hint (deeper thinking is slower and costlier).

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/home"
)

// openHomeReasoningSubmenu opens the Home model menu's 推理等级 submenu for a
// DeepSeek-family model (default / low / medium / high strength modes).
func openHomeReasoningSubmenu(t *testing.T) *ui.Tester {
	t.Helper()
	settings := homeModelSettings(t, []contracts.ModelInfo{{
		ID: "deepseek-chat", Name: "DeepSeek Chat", Family: contracts.ModelFamilyDeepSeek,
	}})
	chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "deepseek-chat"}}}
	h := newComposerHarness(t, chat, settings, nil)
	provide(t, h.ctx, contracts.KeyModelCatalog, contracts.ModelCatalogService(homeCatalogSettings{settings}))
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	clickHome(t, tester, "deepseek-chat")
	clickHome(t, tester, "推理等级")
	return tester
}

// TestHomeReasoningMenuSingleDefault pins the dedup: the submenu offers 系列默认
// as the one default expression, not a second near-synonymous "默认" mode item.
func TestHomeReasoningMenuSingleDefault(t *testing.T) {
	tester := openHomeReasoningSubmenu(t)
	if _, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "系列默认"); !ok {
		t.Fatalf("the single default expression 系列默认 is missing; texts = %v", tester.Texts())
	}
	if _, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "默认"); ok {
		t.Fatalf("US-07: the redundant near-synonymous 默认 mode item is still offered; texts = %v", tester.Texts())
	}
	for _, label := range []string{"低强度", "中强度", "高强度"} {
		if _, ok := tester.AccessByRole(ui.RoleMenuItemRadio, label); !ok {
			t.Fatalf("strength level %q missing; texts = %v", label, tester.Texts())
		}
	}
}

// TestHomeReasoningTradeoffHints covers the strength trade-off disclosure: the
// high level says deeper thinking is slower and costlier.
func TestHomeReasoningTradeoffHints(t *testing.T) {
	tester := openHomeReasoningSubmenu(t)
	if !tester.HasText("更慢更贵") {
		t.Fatalf("US-07: the reasoning submenu has no strength trade-off hint; texts = %v", tester.Texts())
	}
}
