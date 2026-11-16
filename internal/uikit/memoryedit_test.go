package uikit_test

// Headless frame tests for uikit.MemoryEditDialog (AR-17): the settings
// memory page and the session Memory tab share one edit-dialog anatomy. The
// caller owns the draft, the save/cancel semantics and the notifications; the
// entry owns the modal, the title, the input, the error line and the footer.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

func TestMemoryEditDialogSingleLine(t *testing.T) {
	open := true
	text := "原文"
	saves, cancels := 0, 0
	view := func(c *ui.Context) {
		uikit.MemoryEditDialog(c, design.Of(c), uikit.MemoryEditOptions{
			Open:         &open,
			Text:         &text,
			FieldLabel:   "记忆内容",
			Description:  "修改这条记忆的内容；作用域和创建时间保持不变。",
			SaveDisabled: false,
			OnSave:       func() { saves++ },
			OnCancel:     func() { cancels++ },
		})
	}
	tester := ui.NewTester(view, 900, 700)
	tester.Frame()
	for _, want := range []string{"编辑记忆", "记忆内容", "保存", "取消", "修改这条记忆的内容；作用域和创建时间保持不变。"} {
		if !tester.HasText(want) {
			t.Fatalf("dialog missing %q; texts = %v", want, tester.Texts())
		}
	}
	if err := tester.Click("保存"); err != nil {
		t.Fatalf("save not clickable: %v", err)
	}
	tester.Frame()
	if saves != 1 {
		t.Fatalf("save callback ran %d times, want 1", saves)
	}
	if err := tester.Click("取消"); err != nil {
		t.Fatalf("cancel not clickable: %v", err)
	}
	tester.Frame()
	if cancels != 1 {
		t.Fatalf("cancel callback ran %d times, want 1", cancels)
	}
}

func TestMemoryEditDialogEnterSubmitsSingleLine(t *testing.T) {
	open := true
	text := "原文"
	saves := 0
	view := func(c *ui.Context) {
		uikit.MemoryEditDialog(c, design.Of(c), uikit.MemoryEditOptions{
			Open:       &open,
			Text:       &text,
			FieldLabel: "记忆内容",
			OnSave:     func() { saves++ },
			OnCancel:   func() {},
		})
	}
	tester := ui.NewTester(view, 900, 700)
	tester.Frame()
	input, ok := tester.AccessByRole(ui.RoleTextField, "记忆内容")
	if !ok {
		t.Fatalf("single-line input not found by label; texts = %v", tester.Texts())
	}
	tester.ClickAt(input.Bounds.X+input.Bounds.W/2, input.Bounds.Y+input.Bounds.H/2)
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if saves != 1 {
		t.Fatalf("Enter in the input ran save %d times, want 1", saves)
	}
}

func TestMemoryEditDialogDisabledSaveAndError(t *testing.T) {
	open := true
	text := ""
	saves := 0
	view := func(c *ui.Context) {
		uikit.MemoryEditDialog(c, design.Of(c), uikit.MemoryEditOptions{
			Open:         &open,
			Text:         &text,
			FieldLabel:   "记忆内容",
			Error:        "更新记忆失败：disk full",
			SaveDisabled: true,
			OnSave:       func() { saves++ },
			OnCancel:     func() {},
		})
	}
	tester := ui.NewTester(view, 900, 700)
	tester.Frame()
	if !tester.HasText("更新记忆失败：disk full") {
		t.Fatalf("error line not shown; texts = %v", tester.Texts())
	}
	// A disabled primary does not run the action (mouse or Enter).
	_ = tester.Click("保存")
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if saves != 0 {
		t.Fatalf("disabled save ran %d times, want 0", saves)
	}
}

func TestMemoryEditDialogMultiline(t *testing.T) {
	open := true
	text := "原文"
	saves := 0
	view := func(c *ui.Context) {
		uikit.MemoryEditDialog(c, design.Of(c), uikit.MemoryEditOptions{
			Open:       &open,
			Text:       &text,
			FieldLabel: "编辑内容",
			Multiline:  true,
			OnSave:     func() { saves++ },
			OnCancel:   func() {},
		})
	}
	tester := ui.NewTester(view, 900, 700)
	tester.Frame()
	if !tester.HasText("编辑记忆") || !tester.HasText("编辑内容") {
		t.Fatalf("multiline dialog missing chrome; texts = %v", tester.Texts())
	}
	// The settings page's accessible input name keeps the "… 输入" convention.
	if _, ok := tester.AccessByRole(ui.RoleTextField, "编辑内容 输入"); !ok {
		t.Fatalf("multiline input not found by the settings a11y name; texts = %v", tester.Texts())
	}
	// Enter belongs to the text area (newline), not to the save action.
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if saves != 0 {
		t.Fatalf("Enter submitted the multiline dialog %d times, want 0", saves)
	}
	if err := tester.Click("保存"); err != nil {
		t.Fatalf("save not clickable: %v", err)
	}
	tester.Frame()
	if saves != 1 {
		t.Fatalf("save callback ran %d times, want 1", saves)
	}
}
