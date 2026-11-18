package uikit_test

// A4-I-02: a dialog hands the keyboard back to what opened it. mygo restores
// the focus it recorded as an overlay opened; a dialog that focuses a control in
// its opening frame makes that record point at itself, and the keyboard lands
// nowhere when the dialog closes. These frames pin both ways out of a
// ConfirmDanger — the safe Enter path and the Escape path — back to the trigger.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// confirmDangerFocusHarness draws a page with one destructive trigger and the
// confirmation it opens.
func confirmDangerFocusHarness(state *struct {
	open      bool
	confirmed int
}) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		trigger := ui.Button(c, "删除会话")
		if trigger.Clicked() {
			state.open = true
			c.Invalidate()
		}
		uikit.ConfirmDanger(c, &state.open, uikit.ConfirmDangerOptions{
			Key: "confirm-focus", Title: "删除这个会话？", Message: "删除后无法恢复。",
			Confirm: "删除", Cancel: "取消",
			OnConfirm: func() { state.confirmed++ },
		})
	}, 800, 600)
}

func TestConfirmDangerReturnsFocusAfterEnter(t *testing.T) {
	state := &struct {
		open      bool
		confirmed int
	}{}
	tester := confirmDangerFocusHarness(state)
	tester.Frame()
	if err := tester.Click("删除会话"); err != nil {
		t.Fatalf("click the trigger: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	if !tester.HasText("删除后无法恢复。") {
		t.Fatalf("the confirmation did not open; texts = %v", tester.Texts())
	}
	// Enter is the safe action (Cancel), and the keyboard goes back to the
	// trigger afterwards.
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	tester.Frame()
	if state.confirmed != 0 {
		t.Fatalf("Enter confirmed the dangerous action; confirmed = %d", state.confirmed)
	}
	if state.open {
		t.Fatal("the confirmation stayed open")
	}
	if !tester.Focused("删除会话") {
		t.Fatalf("the focus did not return to the trigger after Enter; texts = %v", tester.Texts())
	}
}

func TestConfirmDangerReturnsFocusAfterEscape(t *testing.T) {
	state := &struct {
		open      bool
		confirmed int
	}{}
	tester := confirmDangerFocusHarness(state)
	tester.Frame()
	if err := tester.Click("删除会话"); err != nil {
		t.Fatalf("click the trigger: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.Frame()
	if state.confirmed != 0 || state.open {
		t.Fatalf("Escape did not cancel: confirmed = %d, open = %v", state.confirmed, state.open)
	}
	if !tester.Focused("删除会话") {
		t.Fatalf("the focus did not return to the trigger after Escape; texts = %v", tester.Texts())
	}
}

// TestConfirmDangerDoesNotConfirmOnStrayEnter pins the safety contract beside the
// focus fix: the dialog's first frame never lets a stray Enter reach the danger
// action.
func TestConfirmDangerDoesNotConfirmOnStrayEnter(t *testing.T) {
	state := &struct {
		open      bool
		confirmed int
	}{}
	tester := confirmDangerFocusHarness(state)
	tester.Frame()
	if err := tester.Click("删除会话"); err != nil {
		t.Fatalf("click the trigger: %v", err)
	}
	tester.Frame()
	// The same frame the dialog appeared in: Enter must decide nothing.
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	// Enter on the opening frame may cancel (the safe outcome) but must never
	// reach the destructive action.
	if state.confirmed != 0 {
		t.Fatalf("a stray Enter confirmed the dangerous action; confirmed = %d", state.confirmed)
	}
}

// confirmDangerMenuHarness draws the shape the product uses: a "⋯" button whose
// menu opens the confirmation. The dialog is opened from a menu item, so mygo's
// own focus record points at the item (gone when the dialog closes); the caller
// binds the button's handle so the keyboard has somewhere to go.
func confirmDangerMenuHarness(state *struct {
	open      bool
	confirmed int
	handle    ui.Handle
}) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		menu := uikit.IconButton(c, uikit.IconButtonOptions{
			Key: "menu-trigger", Label: "会话操作", Icon: icons.More(), Size: design.IconSizeS,
		})
		menu.Bind(&state.handle)
		menu.PopoverMenuButton(func(m *ui.Menu) {
			if m.Item("删除").Chosen() {
				state.open = true
				c.Invalidate()
			}
		})
		uikit.ConfirmDanger(c, &state.open, uikit.ConfirmDangerOptions{
			Key: "menu-confirm", Title: "删除这个会话？", Message: "删除后无法恢复。",
			Confirm: "删除", Cancel: "取消", Trigger: &state.handle,
			OnConfirm: func() { state.confirmed++ },
		})
	}, 800, 600)
}

// TestConfirmDangerReturnsFocusToTheMenuButton covers the product's real shape:
// the confirmation is opened from a menu item, and both ways out give the
// keyboard back to the button that opened the menu.
func TestConfirmDangerReturnsFocusToTheMenuButton(t *testing.T) {
	for _, dismiss := range []ui.Key{ui.KeyEscape, ui.KeyEnter} {
		state := &struct {
			open      bool
			confirmed int
			handle    ui.Handle
		}{}
		tester := confirmDangerMenuHarness(state)
		tester.Frame()
		if err := tester.Click("会话操作"); err != nil {
			t.Fatalf("open the menu: %v; texts = %v", err, tester.Texts())
		}
		tester.Frame()
		if err := tester.Click("删除"); err != nil {
			t.Fatalf("choose the destructive item: %v; texts = %v", err, tester.Texts())
		}
		tester.Frame()
		tester.Frame()
		if !tester.HasText("删除后无法恢复。") {
			t.Fatalf("the confirmation did not open; texts = %v", tester.Texts())
		}
		tester.Key(0, dismiss)
		tester.Frame()
		tester.Frame()
		if state.confirmed != 0 {
			t.Fatalf("%v confirmed the dangerous action; confirmed = %d", dismiss, state.confirmed)
		}
		if !tester.Focused("会话操作") {
			t.Fatalf("%v did not return the keyboard to the menu button; texts = %v", dismiss, tester.Texts())
		}
	}
}
