package plugins_test

// Frame tests for the stdio/LSP trust-channel disclosure (2026-11 audit (d)):
// stdio/command connectors and LSP start child processes directly from
// configuration, outside the OS sandbox — configuration is code execution.
// The connector form's STDIO section and the workspace mcp.json opt-in both
// disclose it (zh + en), in the LSP settings risk-line style. Personal-agent
// scale: disclosure only, no spawn approval is added.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	stdioTrustZh = "stdio/命令型连接器与 LSP 由配置直接启动子进程，不经 OS 沙箱包装；配置即代码执行，只应添加你信任的来源"
	stdioTrustEn = "stdio/command connectors and LSP start child processes directly from configuration, without OS sandbox wrapping; configuration is code execution — only add sources you trust"
)

// TestConnectorStdioFormTrustDisclosure covers the add/edit form: choosing the
// STDIO transport reveals the trust disclosure; the HTTP transport (no local
// child process) does not show it.
func TestConnectorStdioFormTrustDisclosure(t *testing.T) {
	h, _ := newConnectorHarness(t, &connectorPlugins{}, nil)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "添加连接器")

	chooseConnectorTransport(t, h, tester, "STDIO")
	h.assertTexts(tester, stdioTrustZh)

	// Back to HTTP: the stdio trust line goes away with the subprocess fields.
	chooseConnectorTransport(t, h, tester, "HTTP")
	h.assertNoTexts(tester, stdioTrustZh)
}

// TestConnectorStdioFormTrustDisclosureEnglish covers the English copy after a
// language switch (the disclosure is bilingual like the rest of the form).
func TestConnectorStdioFormTrustDisclosureEnglish(t *testing.T) {
	h, _ := newConnectorHarness(t, &connectorPlugins{}, nil)
	tester := h.tester()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.click(tester, "Connectors")
	h.click(tester, "Add connector")

	// The transport selector's accessible name is localized; the option values
	// (HTTP/STDIO) are not.
	h.click(tester, "Transport input")
	if err := tester.Click("STDIO"); err != nil {
		t.Fatalf("choose STDIO: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, stdioTrustEn)
	h.assertNoTexts(tester, stdioTrustZh)
}

// TestConnectorWorkspaceOptInTrustDisclosure covers the workspace mcp.json
// opt-in: the options disclosure carries the trust sentence next to the path
// hint, so enabling repository-provided servers is an informed choice.
func TestConnectorWorkspaceOptInTrustDisclosure(t *testing.T) {
	root := t.TempDir()
	h, _ := newConnectorHarness(t, &connectorPlugins{}, nil)
	provide[contracts.WorkspaceService](t, h.ctx, contracts.KeyWorkspace, &connectorWorkspace{root: root})
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "连接器选项")

	h.assertTexts(tester, "读取工作区 mcp.json", stdioTrustZh)

	// English too.
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, stdioTrustEn)
}

// TestConnectorEditStdioShowsTrustDisclosure covers the edit path: an existing
// stdio row opens the form with the disclosure already visible.
func TestConnectorEditStdioShowsTrustDisclosure(t *testing.T) {
	h, _ := newConnectorHarness(t, &connectorPlugins{}, map[string]string{
		contracts.SettingMCPServers: connectorStdioJSON,
	})
	tester := h.tester()
	h.click(tester, "连接器")
	scrollConnectorTo(t, tester, "连接器操作：srv")
	h.click(tester, "连接器操作：srv")
	h.click(tester, "编辑")

	h.assertTexts(tester, "编辑连接器", stdioTrustZh)
}
