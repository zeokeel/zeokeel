package plugins_test

// i18n frame tests: the Plugins screen copy (sections, MCP cards, tool
// catalogue categories and empties) follows the host locale on every frame.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/plugins"
)

func TestPluginsEnglishCopyAndLanguageSwitch(t *testing.T) {
	service := &fakePlugins{mcp: []contracts.MCPPlugin{{
		Name: "files", Status: "已连接", Tools: 2, ServerName: "fake-files",
	}}}
	h := newHarness(t, service, newToolRegistry(), plugins.Config{})
	tester := h.tester()

	// Default Chinese copy is unchanged.
	h.assertTexts(tester, "插件", "MCP 插件", "刷新", "工具目录", "搜索工具",
		"文件", "终端与任务", "网络", "会话与记忆", "团队与编排", "其他",
		"6 个工具", "2 个工具")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Plugins", "MCP plugins", "Refresh", "Tool catalogue",
		"Search tools", "6 tools", "2 tools",
		"Files", "Terminal & jobs", "Web", "Session & memory",
		"Team & orchestration", "Other")
	h.assertNoTexts(tester, "MCP 插件", "刷新", "工具目录", "搜索工具",
		"终端与任务", "团队与编排")

	// Search and empty states are translated too.
	h.click(tester, "Search tools")
	tester.Type("zzz")
	tester.Frame()
	h.assertTexts(tester, "No matching tools")

	// Clear the filter, then switching back restores the Chinese copy.
	h.click(tester, "Search tools")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
	h.uiRoot.Locale().SetLanguage("zh")
	tester.Frame()
	h.assertTexts(tester, "插件", "MCP 插件", "工具目录", "搜索工具", "文件")
	h.assertNoTexts(tester, "MCP plugins", "Tool catalogue", "Search tools")
}

func TestPluginsEnglishEmptyStates(t *testing.T) {
	h := newHarness(t, nil, nil, plugins.Config{})
	tester := h.tester()
	h.assertTexts(tester, "插件服务未启用", "工具目录未启用")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Plugins service not enabled", "Tool catalogue not enabled")
	h.assertNoTexts(tester, "插件服务未启用", "工具目录未启用")
}
