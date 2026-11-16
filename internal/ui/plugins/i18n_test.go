package plugins_test

// i18n frame tests: the Capabilities screen copy (page title, the parallel tab
// labels, the tab pages' empty states and the shared diagnostic footer) follows
// the host locale on every frame.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/plugins"
)

func TestCapabilitiesEnglishCopyAndLanguageSwitch(t *testing.T) {
	service := &fakePlugins{mcp: []contracts.MCPPlugin{{
		Name: "files", Status: "已连接", Tools: 2, ServerName: "fake-files",
	}}}
	h := newHarness(t, service, nil, plugins.Config{})
	tester := h.tester()

	h.assertTexts(tester, "能力", "技能", "连接器", "技能后端未启用", "诊断与设置")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Capabilities", "Skills", "Connectors",
		"Skills backend is not enabled", "Diagnostics & settings")
	h.assertNoTexts(tester, "能力", "技能后端未启用", "诊断与设置")

	// The other parallel tab is translated too.
	h.click(tester, "Connectors")
	h.assertTexts(tester, "Connectors")
	h.assertNoTexts(tester, "Skills backend is not enabled")

	h.uiRoot.Locale().SetLanguage("zh")
	tester.Frame()
	h.assertTexts(tester, "能力", "技能", "连接器")
	h.assertNoTexts(tester, "Capabilities", "Connectors")
}

func TestCapabilitiesEnglishEmptyStates(t *testing.T) {
	h := newHarness(t, nil, nil, plugins.Config{})
	tester := h.tester()
	h.assertTexts(tester, "技能后端未启用")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Skills backend is not enabled")
	h.assertNoTexts(tester, "技能后端未启用")

	h.click(tester, "Connectors")
	h.assertTexts(tester, "Connector service not enabled")
}
