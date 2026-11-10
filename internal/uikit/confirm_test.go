package uikit_test

// Headless frame tests for uikit.ConfirmDanger: the product's one confirmation
// for irreversible actions. The safe outcome (Cancel) carries the default and
// Enter, and the destructive action is never the default (DESIGN.md AP-12).

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/uikit"
)

func TestConfirmDangerDefaultsToCancel(t *testing.T) {
	open := true
	confirmed := 0
	view := func(c *ui.Context) {
		ui.Text(c, "主屏幕")
		uikit.ConfirmDanger(c, &open, uikit.ConfirmDangerOptions{
			Key:       "test.delete",
			Title:     "删除模型",
			Message:   "确认删除这个模型？此操作无法撤销。",
			Confirm:   "确认删除",
			Cancel:    "取消",
			OnConfirm: func() { confirmed++ },
		})
	}
	tester := ui.NewTester(view, 900, 700)
	tester.Frame()
	if !tester.HasText("删除模型") || !tester.HasText("确认删除") {
		t.Fatalf("confirmation not shown; texts = %v", tester.Texts())
	}
	if !tester.Focused("取消") {
		t.Fatalf("safe action does not carry the focus")
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if confirmed != 0 {
		t.Fatalf("Enter confirmed the destructive action (%d times)", confirmed)
	}
	if open {
		t.Fatalf("Enter did not close the confirmation")
	}

	open = true
	tester.Frame()
	if !tester.HasText("确认删除") {
		t.Fatalf("reopened confirmation not shown; texts = %v", tester.Texts())
	}
	if err := tester.Click("确认删除"); err != nil {
		t.Fatalf("confirm action not clickable: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if confirmed != 1 {
		t.Fatalf("explicit confirm did not run the action (%d)", confirmed)
	}
	if open {
		t.Fatalf("confirm did not close the dialog")
	}
}
