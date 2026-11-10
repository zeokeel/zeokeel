package session_test

// Tests for the transcript's compact tool rows: the whole row is one official
// mygo disclosure (ui.CollapsibleBase), the friendly localized tool name is the
// visible copy (the protocol id is only its tooltip/AT description), the H15
// default-open rule and the per-row fold state survive, running calls show the
// official spinner, and click as well as Enter/Space toggle the row.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func toolChat(tools ...contracts.ToolCall) *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "工具行",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "跑一下"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "好的", Tools: tools},
		},
	}, true)
	return chat
}

// TestToolRowUsesFriendlyNameDisclosure covers the core row anatomy: one
// disclosure whose visible name is the friendly localized tool name, whose
// tooltip/description is the raw protocol id, and whose untouched H15 default
// (a short result) starts expanded.
func TestToolRowUsesFriendlyNameDisclosure(t *testing.T) {
	h := newVerboseWorkStepsHarness(t, toolChat(contracts.ToolCall{
		ID: "t1", Name: "read_file", Status: "ok", Summary: "auth.go",
	}))
	tester := h.tester()
	h.assertTexts(tester, "读取文件", "完成", "auth.go")
	h.assertNoTexts(tester, "read_file")
	info, ok := tester.AccessByRole(ui.RoleDisclosure, "读取文件 · 完成")
	if !ok {
		t.Fatalf("tool row is not a disclosure; texts = %v", tester.Texts())
	}
	if info.Description != "read_file" {
		t.Fatalf("tool row description = %q, want the raw tool id", info.Description)
	}
	if !info.Expanded {
		t.Fatalf("H15: a short successful result should start expanded: %+v", info)
	}
}

// TestToolRowTogglesByClickAndKeyboard covers the official disclosure input
// semantics: a click toggles, then Enter and Space toggle it again once the
// trigger has the keyboard focus.
func TestToolRowTogglesByClickAndKeyboard(t *testing.T) {
	h := newVerboseWorkStepsHarness(t, toolChat(contracts.ToolCall{
		ID: "t1", Name: "bash", Status: "ok", Summary: "stdout: 全部测试通过",
	}))
	tester := h.tester()
	info, ok := tester.AccessByRole(ui.RoleDisclosure, "运行命令 · 完成")
	if !ok || !info.Expanded {
		t.Fatalf("tool row disclosure: %+v found %v", info, ok)
	}

	h.click(tester, "运行命令")
	if info, _ := tester.AccessByRole(ui.RoleDisclosure, "运行命令 · 完成"); info.Expanded {
		t.Fatalf("click did not close the tool row: %+v", info)
	}

	tester.Key(0, ui.KeySpace)
	tester.Frame()
	if info, _ := tester.AccessByRole(ui.RoleDisclosure, "运行命令 · 完成"); !info.Expanded {
		t.Fatalf("Space did not open the tool row: %+v", info)
	}

	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if info, _ := tester.AccessByRole(ui.RoleDisclosure, "运行命令 · 完成"); info.Expanded {
		t.Fatalf("Enter did not close the tool row: %+v", info)
	}
}

// TestToolRowRunningShowsSpinner covers a running call: the official spinner is
// labeled with the status (its Label shows up as a frame label) beside the
// localized status text, and the disclosure name says the call is still running
// because the spinner inside it is not a node of its own for assistive
// technology.
func TestToolRowRunningShowsSpinner(t *testing.T) {
	h := newVerboseWorkStepsHarness(t, toolChat(contracts.ToolCall{
		ID: "t1", Name: "bash", Status: "running", Summary: "stdout: 测试进行中",
	}))
	tester := h.tester()
	// The spinner's Label and the status text both read exactly 运行中: two
	// labels, where a bare row without the spinner shows one.
	running := 0
	for _, text := range tester.Texts() {
		if text == "运行中" {
			running++
		}
	}
	if running != 2 {
		t.Fatalf("running row shows %d exact 运行中 labels, want the status text and the spinner label; texts = %v", running, tester.Texts())
	}
	info, ok := tester.AccessByRole(ui.RoleDisclosure, "运行命令 · 运行中")
	if !ok {
		t.Fatalf("running disclosure name: %+v found %v; texts = %v", info, ok, tester.Texts())
	}
	if info.Description != "bash" {
		t.Fatalf("running row description = %q, want the raw tool id", info.Description)
	}
}

// TestToolRowLongResultStaysFoldedAndRemembers covers the H15 fold: a long
// successful result starts folded, a click by the reader opens it and the state
// persists on the following frames.
func TestToolRowLongResultStaysFoldedAndRemembers(t *testing.T) {
	summary := strings.Repeat("行结果 ", 80) // over the 160-rune H15 bound
	h := newVerboseWorkStepsHarness(t, toolChat(contracts.ToolCall{
		ID: "t1", Name: "bash", Status: "ok", Summary: summary,
	}))
	tester := h.tester()
	info, ok := tester.AccessByRole(ui.RoleDisclosure, "运行命令 · 完成")
	if !ok || info.Expanded {
		t.Fatalf("a long result should start folded: %+v found %v", info, ok)
	}
	h.click(tester, "运行命令")
	if info, _ := tester.AccessByRole(ui.RoleDisclosure, "运行命令 · 完成"); !info.Expanded {
		t.Fatalf("the reader's click did not open the long result: %+v", info)
	}
	tester.Frame()
	if info, _ := tester.AccessByRole(ui.RoleDisclosure, "运行命令 · 完成"); !info.Expanded {
		t.Fatalf("the opened row did not stay open: %+v", info)
	}
}

// TestToolRowUnknownToolKeepsItsProtocolName keeps host-registered tools
// discoverable: the raw name is the visible fallback when the kit has no
// friendly translation.
func TestToolRowUnknownToolKeepsItsProtocolName(t *testing.T) {
	h := newVerboseWorkStepsHarness(t, toolChat(contracts.ToolCall{
		ID: "t1", Name: "mystery_tool", Status: "ok",
	}))
	tester := h.tester()
	h.assertTexts(tester, "mystery_tool")
	if info, ok := tester.AccessByRole(ui.RoleDisclosure, "mystery_tool · 完成"); ok && info.Description != "" {
		t.Fatalf("unknown tool row has a tooltip description %q, want none", info.Description)
	}
}
