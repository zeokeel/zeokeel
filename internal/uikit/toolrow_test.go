package uikit_test

// Headless tests for the shared transcript tool row: it is one official mygo
// disclosure (ui.CollapsibleBase), so a click, Enter and Space toggle it and
// assistive technology sees its expanded state and description.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// toolRowState is the caller-owned fold state of one row, as a transcript keeps
// it between frames.
type toolRowState struct {
	open    bool
	toggles []bool
}

func toolRowView(state *toolRowState) func(*ui.Context) {
	return func(c *ui.Context) {
		t := design.Of(c)
		uikit.ToolRow(c, uikit.ToolRowOptions{
			Key:         "test.tool." + "read_file",
			Icon:        icons.File(),
			Name:        "读取文件",
			RawName:     "read_file",
			Status:      "完成",
			StatusColor: t.SuccessText,
			Preview:     "auth.go",
			Open:        state.open,
			Toggle: func(next bool) {
				state.toggles = append(state.toggles, next)
				state.open = next
			},
			Sections: []uikit.ToolPanelSection{{
				Label: "完整结果标题",
				Text:  "完整结果",
				Mono:  true,
			}},
		})
	}
}

func TestToolRowIsOfficialDisclosure(t *testing.T) {
	state := &toolRowState{}
	tester := ui.NewTester(toolRowView(state), 800, 200)

	info, ok := tester.AccessByRole(ui.RoleDisclosure, "读取文件 · 完成")
	if !ok {
		t.Fatalf("tool row is not a disclosure; texts = %v", tester.Texts())
	}
	if info.Description != "read_file" {
		t.Fatalf("description = %q, want the raw tool id", info.Description)
	}
	if info.Expanded || tester.HasText("完整结果") {
		t.Fatalf("closed row: %+v shows detail %v", info, tester.HasText("完整结果"))
	}

	if err := tester.Click("读取文件"); err != nil {
		t.Fatalf("click: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if len(state.toggles) != 1 || state.toggles[0] != true {
		t.Fatalf("toggle calls = %v, want [true]", state.toggles)
	}
	if info, _ := tester.AccessByRole(ui.RoleDisclosure, "读取文件 · 完成"); !info.Expanded {
		t.Fatalf("click did not open the row: %+v", info)
	}
	if !tester.HasText("完整结果") {
		t.Fatalf("open row does not show its detail; texts = %v", tester.Texts())
	}

	tester.Key(0, ui.KeySpace)
	tester.Frame()
	if state.open || len(state.toggles) != 2 || state.toggles[1] != false {
		t.Fatalf("Space: open %v, toggles %v", state.open, state.toggles)
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if !state.open || len(state.toggles) != 3 || state.toggles[2] != true {
		t.Fatalf("Enter: open %v, toggles %v", state.open, state.toggles)
	}
}

func TestToolRowWithoutDetailIsAPlainLine(t *testing.T) {
	tester := ui.NewTester(func(c *ui.Context) {
		uikit.ToolRow(c, uikit.ToolRowOptions{
			Key:     "test.plain",
			Name:    "读取文件",
			RawName: "read_file",
			Status:  "完成",
		})
	}, 800, 200)
	if !tester.HasText("读取文件") || !tester.HasText("完成") {
		t.Fatalf("plain row missing its copy; texts = %v", tester.Texts())
	}
	if _, ok := tester.AccessByRole(ui.RoleDisclosure, "读取文件 · 完成"); ok {
		t.Fatal("a row without a detail body is still a disclosure")
	}
}

func TestToolRowRunningShowsLabeledSpinner(t *testing.T) {
	tester := ui.NewTester(func(c *ui.Context) {
		t := design.Of(c)
		uikit.ToolRow(c, uikit.ToolRowOptions{
			Key:         "test.running",
			Name:        "运行命令",
			RawName:     "bash",
			Status:      "运行中",
			StatusColor: t.WarningText,
			Running:     true,
			Sections:    []uikit.ToolPanelSection{{Label: "输出", Text: "结果"}},
		})
	}, 800, 200)
	running := 0
	for _, text := range tester.Texts() {
		if text == "运行中" {
			running++
		}
	}
	// The status text and the spinner's Label.
	if running != 2 {
		t.Fatalf("running row shows %d 运行中 labels, want the status and the spinner label; texts = %v", running, tester.Texts())
	}
	if _, ok := tester.AccessByRole(ui.RoleDisclosure, "运行命令 · 运行中"); !ok {
		t.Fatalf("running disclosure name missing; texts = %v", tester.Texts())
	}
}
