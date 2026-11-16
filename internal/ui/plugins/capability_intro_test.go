package plugins_test

// Frame tests for US-08 (2026-11 wave D): the Capabilities page's expert tabs
// carry a plain-language intro so a non-developer learns what they are instead
// of facing a terminology wall — skills are task manuals for the agent, and
// connectors bring external MCP (third-party tool) services into the session.

import (
	"testing"
)

// TestSkillsTabPlainIntro covers the skills tab's teaching line: it explains
// what a skill is in plain language (not only the developer-facing SKILL.md
// empty-state instruction).
func TestSkillsTabPlainIntro(t *testing.T) {
	h, _, _ := newSkillsHarness(t, newFakeSkills(), nil)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "技能", "任务说明书")
}

// TestConnectorsTabExplainsMCP covers the connectors tab's teaching line: MCP is
// glossed in plain language (third-party tool services), not left as an
// unexplained acronym.
func TestConnectorsTabExplainsMCP(t *testing.T) {
	h, _ := newConnectorHarness(t, nil, nil)
	tester := h.tester()
	openConnectorTab(t, h, tester)
	h.assertTexts(tester, "连接器", "第三方工具服务")
}
