package plugins_test

// Conformance frame tests for the v0.8.3 plugins fixes: the model-facing English
// tool documentation gets a one-line Chinese summary (the original phrase stays
// as the muted reference line and the whole text in the tooltip), a long MCP
// connection error shows a short cause with the raw status in the folded
// details, and Enter submits the parameter editor.

import (
	"strings"
	"testing"

	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/plugins"
)

// TestToolDescriptionsLocalized pins the localized catalogue line: Chinese UI
// summaries the English documentation and keeps the original phrase below it;
// the English UI shows the documentation summary alone.
func TestToolDescriptionsLocalized(t *testing.T) {
	desc := "Read a UTF-8 text file inside the tool root and return numbered lines. " +
		"Use offset and limit to page through large files."
	registry := rigtools.NewRegistry(fakeTool{name: "read_file", desc: desc})
	h := newHarness(t, nil, registry, plugins.Config{})
	tester := h.tester()

	h.assertTexts(tester, "读取工作区文件内容",
		"Read a UTF-8 text file inside the tool root and return numbered lines")
	h.assertNoTexts(tester, "Use offset and limit to page through large files")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertNoTexts(tester, "读取工作区文件内容")
	h.assertTexts(tester, "Read a UTF-8 text file inside the tool root and return numbered lines")
}

// TestMCPLongErrorShortReasonAndFoldedDetail pins the failure row: the collapsed
// card shows a bounded cause, and the whole raw status is in the details.
func TestMCPLongErrorShortReasonAndFoldedDetail(t *testing.T) {
	detail := strings.Repeat("connection refused by upstream; ", 8)
	status := "连接失败: " + detail
	service := &fakePlugins{mcp: []contracts.MCPPlugin{{Name: "broken", Status: status}}}
	h := newHarness(t, service, nil, plugins.Config{})
	tester := h.tester()

	h.assertTexts(tester, "broken", "连接失败")
	// The collapsed row never renders the raw line in full.
	if tester.HasText(strings.TrimSpace(detail)) {
		t.Fatalf("collapsed card shows the whole raw error; texts = %v", tester.Texts())
	}
	// The folded details keep the complete status text.
	h.click(tester, "详情")
	h.assertTexts(tester, strings.TrimSpace(detail))
}

// TestPluginParamEnterSubmits pins the dialog's field Enter: the parameter
// editor's only input takes the focus as it opens and Enter saves.
func TestPluginParamEnterSubmits(t *testing.T) {
	h, store := newParamsHarness(t, map[string]string{
		contracts.SettingWebSearchEndpoint: "https://old.example.com/search",
	})
	tester := h.tester()
	h.click(tester, "显示参数")
	h.click(tester, "编辑 搜索端点")
	h.assertTexts(tester, "编辑参数", "保存", "取消")

	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("https://new.example.com/search")
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if got := store.get(contracts.SettingWebSearchEndpoint); got != "https://new.example.com/search" {
		t.Fatalf("%s = %q after Enter", contracts.SettingWebSearchEndpoint, got)
	}
	h.assertTexts(tester, "已保存")
	h.assertNoTexts(tester, "编辑参数")
}
