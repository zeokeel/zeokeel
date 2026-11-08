package plugins_test

// TDD tests (RED first) for the expandable MCP server cards: collapsed shows
// name/status/tool count, expanding reveals the connection details, and the
// refresh keeps the per-server expansion state.

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/plugins"
)

func mcpHTTPPlugin() contracts.MCPPlugin {
	return contracts.MCPPlugin{
		Name: "files", URL: "http://127.0.0.1:9000/mcp",
		ServerName: "fake-files", Version: "0.1.0", Protocol: "2024-11-05",
		Tools: 3, Status: "已连接",
	}
}

// textsContainingPlugins counts text nodes containing fragment.
func textsContainingPlugins(texts []string, fragment string) int {
	count := 0
	for _, text := range texts {
		if strings.Contains(text, fragment) {
			count++
		}
	}
	return count
}

// --- tests ------------------------------------------------------------------

func TestMCPCardCollapsedByDefault(t *testing.T) {
	h := newHarness(t, &fakePlugins{mcp: []contracts.MCPPlugin{mcpHTTPPlugin()}}, nil, plugins.Config{})
	tester := h.tester()

	h.assertTexts(tester, "files", "已连接", "3 个工具", "详情")
	h.assertNoTexts(tester, "http://127.0.0.1:9000/mcp", "fake-files", "0.1.0", "2024-11-05")
	if got := textsContainingPlugins(tester.Texts(), "类型"); got != 0 {
		t.Fatalf("collapsed card shows details; texts = %v", tester.Texts())
	}
}

func TestMCPCardExpandsAndCollapses(t *testing.T) {
	h := newHarness(t, &fakePlugins{mcp: []contracts.MCPPlugin{mcpHTTPPlugin()}}, nil, plugins.Config{})
	tester := h.tester()

	h.click(tester, "详情")
	h.assertTexts(tester, "详情", "类型", "HTTP", "http://127.0.0.1:9000/mcp",
		"fake-files", "0.1.0", "2024-11-05", "3 个工具", "已连接")

	h.click(tester, "详情")
	h.assertTexts(tester, "详情")
	h.assertNoTexts(tester, "http://127.0.0.1:9000/mcp")
}

func TestMCPCardStdioShowsLocalTransport(t *testing.T) {
	server := contracts.MCPPlugin{Name: "local-tools", Status: "已连接"}
	h := newHarness(t, &fakePlugins{mcp: []contracts.MCPPlugin{server}}, nil, plugins.Config{})
	tester := h.tester()

	h.click(tester, "详情")
	h.assertTexts(tester, "类型", "本地 stdio", "0 个工具")
	// The empty handshake fields render as em dashes.
	if got := textsContainingPlugins(tester.Texts(), "—"); got < 3 {
		t.Fatalf("empty detail fields = %d, want at least 3 em dashes; texts = %v", got, tester.Texts())
	}
}

func TestMCPRefreshKeepsExpansion(t *testing.T) {
	first := mcpHTTPPlugin()
	second := contracts.MCPPlugin{Name: "added", URL: "https://mcp.example.com/mcp", Status: "已连接"}
	service := &fakePlugins{
		mcp:       []contracts.MCPPlugin{first},
		refreshed: []contracts.MCPPlugin{first, second},
	}
	h := newHarness(t, service, nil, plugins.Config{})
	tester := h.tester()
	h.click(tester, "详情")
	h.assertTexts(tester, "http://127.0.0.1:9000/mcp")

	h.click(tester, "刷新")
	h.assertTexts(tester, "http://127.0.0.1:9000/mcp", "added", "详情")
	// The newly added server starts collapsed.
	h.assertNoTexts(tester, "https://mcp.example.com/mcp")
}

func TestMCPDetailsEnglishCopy(t *testing.T) {
	h := newHarness(t, &fakePlugins{mcp: []contracts.MCPPlugin{mcpHTTPPlugin()}}, nil, plugins.Config{})
	tester := h.tester()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()

	h.assertTexts(tester, "Details")
	h.click(tester, "Details")
	h.assertTexts(tester, "Details", "Type", "HTTP", "Server", "Version", "Protocol", "Status")
}
