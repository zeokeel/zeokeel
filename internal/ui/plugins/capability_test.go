package plugins_test

// Frame tests for the Capabilities screen skeleton: one page-level tab row
// whose tabs are parallel destination pages. The skills and connectors tabs are
// content; the shell owns the tab identity, the chosen tab and the shared
// diagnostic area below them. A future workflow tab is another entry in the
// shell's tab registry, not a new screen.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/ui/plugins"
)

func TestCapabilityShellTabs(t *testing.T) {
	h := newHarness(t, nil, nil, plugins.Config{})
	tester := h.tester()

	// The screen title is the capability hub's; the two parallel destinations
	// are its page-level tabs.
	h.assertTexts(tester, "能力", "技能", "连接器")
	// The first tab is the skills page (empty state without a backend).
	h.assertTexts(tester, "技能后端未启用")
	h.assertNoTexts(tester, "连接器服务未启用")

	// Choosing the connectors tab renders the connectors page, not the skills
	// one: the tabs are separate parallel pages, not one filtered list.
	h.click(tester, "连接器")
	h.assertTexts(tester, "连接器服务未启用")
	h.assertNoTexts(tester, "技能后端未启用")

	// Choosing back restores the skills page; the chosen tab survives frames.
	h.click(tester, "技能")
	h.assertTexts(tester, "技能后端未启用")
	tester.Frame()
	h.assertTexts(tester, "技能后端未启用")
	h.assertNoTexts(tester, "连接器服务未启用")
}

func TestCapabilityShellDropsToolCatalogue(t *testing.T) {
	// The capability page no longer carries the tool catalogue or its switches:
	// tools stay enabled by default and are not a capability surface. The
	// toolkit backend is still provided, so this asserts the UI removal, not a
	// missing backend.
	h := newHarness(t, nil, newToolRegistry(), plugins.Config{})
	tester := h.tester()
	h.assertNoTexts(tester, "工具目录", "搜索工具", "read_file", "仅显示已停用")
}

func TestCapabilityShellCustomTabLabels(t *testing.T) {
	h := newHarness(t, nil, nil, plugins.Config{
		Title:         "Capabilities",
		SkillsSection: "Skills",
		MCPSection:    "Connectors",
	})
	tester := h.tester()
	h.assertTexts(tester, "Capabilities", "Skills", "Connectors")
}
