package uikit_test

// Headless frame tests for uikit.AskDialog (AGENT-SURFACES.md S6): the
// product's ask-user dialog. They pin the equal-weight option layout, the
// no-stray-Enter safety contract (Cancel is the Default while the options
// show), the "Other…" inline input (Enter submits, Escape collapses) and the
// free-text answer that never submits empty.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// askDialogTestState is the caller-owned state of one ask dialog.
type askDialogTestState struct {
	otherOpen bool
	answer    string
	answers   []string
	cancels   int
}

// askDialogTestStateView draws a fixed question over a plain product frame.
func askDialogTestView(state *askDialogTestState, question contracts.UserQuestion) func(*ui.Context) {
	options := uikit.AskDialogOptions{
		Question:  question,
		OtherOpen: &state.otherOpen,
		Answer:    &state.answer,
		OnAnswer: func(answer string) {
			state.answers = append(state.answers, answer)
		},
		OnCancel: func() {
			state.cancels++
		},
	}
	return func(c *ui.Context) {
		ui.Text(c, "主屏幕")
		uikit.AskDialog(c, options)
	}
}

// askDialogHarness starts one ask dialog and returns its tester.
func askDialogHarness(t *testing.T, question contracts.UserQuestion) (*askDialogTestState, *ui.Tester) {
	t.Helper()
	state := &askDialogTestState{}
	tester := ui.NewTester(askDialogTestView(state, question), 1200, 800)
	tester.Frame()
	return state, tester
}

// askDialogFocus focuses a dialog input by its label, as a pointer click does.
func askDialogFocus(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	rect, ok := tester.Find(label)
	if !ok {
		t.Fatalf("input %q not found; texts = %v", label, tester.Texts())
	}
	tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Frame()
}

// TestAskDialogOptionsAreEqualWeightAndStacked covers the option layout: one
// full-width row each in one column — no primary hierarchy that would silently
// rank them — with the fixed trailing "Other…" choice.
func TestAskDialogOptionsAreEqualWeightAndStacked(t *testing.T) {
	_, tester := askDialogHarness(t, contracts.UserQuestion{
		ID: "q1", Text: "部署到哪个环境？", Options: []string{"staging", "prod"},
	})
	for _, want := range []string{"需要你的确认", "部署到哪个环境？", "staging", "prod", "其他…", "取消"} {
		if !tester.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	first, ok := tester.Find("staging")
	if !ok {
		t.Fatalf("first option missing; texts = %v", tester.Texts())
	}
	second, ok := tester.Find("prod")
	if !ok {
		t.Fatalf("second option missing; texts = %v", tester.Texts())
	}
	other, ok := tester.Find("其他…")
	if !ok {
		t.Fatalf("Other choice missing; texts = %v", tester.Texts())
	}
	// Find returns the label's text box; the button boxes come from the access
	// tree and are the full-width equal choices.
	firstButton, ok := tester.AccessByRole(ui.RoleButton, "staging")
	if !ok {
		t.Fatalf("first option button missing from the access tree; texts = %v", tester.Texts())
	}
	secondButton, ok := tester.AccessByRole(ui.RoleButton, "prod")
	if !ok {
		t.Fatalf("second option button missing from the access tree; texts = %v", tester.Texts())
	}
	otherButton, ok := tester.AccessByRole(ui.RoleButton, "其他…")
	if !ok {
		t.Fatalf("Other button missing from the access tree; texts = %v", tester.Texts())
	}
	for _, button := range []ui.AccessInfo{firstButton, secondButton, otherButton} {
		if button.Bounds.W != firstButton.Bounds.W {
			t.Fatalf("the choices are not equal-width: %.1f, %.1f, %.1f",
				firstButton.Bounds.W, secondButton.Bounds.W, otherButton.Bounds.W)
		}
	}
	if first.X+first.W/2 != second.X+second.W/2 || second.X+second.W/2 != other.X+other.W/2 {
		t.Fatalf("the choices are not stacked in one column: centers %.1f, %.1f, %.1f",
			first.X+first.W/2, second.X+second.W/2, other.X+other.W/2)
	}
	if !(first.Y < second.Y && second.Y < other.Y) {
		t.Fatalf("the choices are not vertical: y = %.1f, %.1f, %.1f", first.Y, second.Y, other.Y)
	}
}

// TestAskDialogOptionAnswers covers the explicit choice: a click answers the
// question with that option.
func TestAskDialogOptionAnswers(t *testing.T) {
	state, tester := askDialogHarness(t, contracts.UserQuestion{
		ID: "q1", Text: "部署到哪个环境？", Options: []string{"staging", "prod"},
	})
	if err := tester.Click("prod"); err != nil {
		t.Fatalf("click option: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if len(state.answers) != 1 || state.answers[0] != "prod" {
		t.Fatalf("answers = %v, want [prod]", state.answers)
	}
	if state.cancels != 0 {
		t.Fatalf("cancels = %d, want none", state.cancels)
	}
	if tester.HasText("部署到哪个环境？") {
		t.Fatalf("the answered question is still rendered; texts = %v", tester.Texts())
	}
}

// TestAskDialogStrayEnterCancels covers C-03's safety contract: Enter right
// after the dialog opens (the user may still be pressing the key that sent the
// prompt) cancels instead of answering the first option.
func TestAskDialogStrayEnterCancels(t *testing.T) {
	state, tester := askDialogHarness(t, contracts.UserQuestion{
		ID: "q-stray", Text: "部署到哪个环境？", Options: []string{"生产环境", "测试环境"},
	})
	if !tester.Focused("取消") {
		t.Fatalf("no neutral initial focus: %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEnter)
	if len(state.answers) != 0 {
		t.Fatalf("stray Enter answered %v, want no answer", state.answers)
	}
	if state.cancels != 1 {
		t.Fatalf("cancellations = %d, want 1", state.cancels)
	}
	tester.Frame()
	if tester.HasText("部署到哪个环境？") {
		t.Fatalf("the cancelled question is still rendered; texts = %v", tester.Texts())
	}
}

// TestAskDialogEscapeCancels covers the native Modal dismissal: Escape is the
// question's cancel.
func TestAskDialogEscapeCancels(t *testing.T) {
	state, tester := askDialogHarness(t, contracts.UserQuestion{
		ID: "q4", Text: "确认删除？", Options: []string{"删除"},
	})
	tester.Key(0, ui.KeyEscape)
	if state.cancels != 1 || len(state.answers) != 0 {
		t.Fatalf("Escape = cancels %d answers %v; want one cancel and no answer", state.cancels, state.answers)
	}
	tester.Frame()
	if tester.HasText("确认删除？") {
		t.Fatalf("the dismissed question is still rendered; texts = %v", tester.Texts())
	}
}

// TestAskDialogOtherAnswer covers the fixed trailing "Other…" choice: it opens
// one inline input that takes the keyboard, keeps the options visible, and its
// non-empty text becomes the answer on Enter.
func TestAskDialogOtherAnswer(t *testing.T) {
	state, tester := askDialogHarness(t, contracts.UserQuestion{
		ID: "q-other", Text: "部署到哪个环境？", Options: []string{"staging", "prod"},
	})
	if err := tester.Click("其他…"); err != nil {
		t.Fatalf("click Other: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !state.otherOpen {
		t.Fatal("Other did not open its inline input")
	}
	if !tester.Focused("其他回答") {
		t.Fatalf("the Other input did not take the keyboard; texts = %v", tester.Texts())
	}
	// The option buttons stay visible: entering Other is not a commitment.
	for _, want := range []string{"staging", "prod", "Enter = 提交自定义回答 · Esc = 返回选项"} {
		if !tester.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	tester.Type("预发布环境")
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if len(state.answers) != 1 || state.answers[0] != "预发布环境" {
		t.Fatalf("answers = %v, want [预发布环境]", state.answers)
	}
	if state.cancels != 0 {
		t.Fatalf("cancels = %d, want none", state.cancels)
	}
}

// TestAskDialogOtherEscapeCollapses covers the Other input's Escape contract:
// it returns to the options instead of dismissing the question, and the choice
// state does not leak into the next question.
func TestAskDialogOtherEscapeCollapses(t *testing.T) {
	state, tester := askDialogHarness(t, contracts.UserQuestion{
		ID: "q-esc", Text: "部署到哪个环境？", Options: []string{"staging", "prod"},
	})
	if err := tester.Click("其他…"); err != nil {
		t.Fatalf("click Other: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Type("临时")
	tester.Frame()
	tester.Key(0, ui.KeyEscape)
	if len(state.answers) != 0 || state.cancels != 0 {
		t.Fatalf("Escape submitted/cancelled: answers=%v cancels=%d", state.answers, state.cancels)
	}
	if state.otherOpen {
		t.Fatal("Escape did not collapse the Other input")
	}
	if state.answer != "" {
		t.Fatalf("Escape kept the Other text %q", state.answer)
	}
	if !tester.HasText("其他…") || tester.HasText("输入自定义回答") {
		t.Fatalf("Escape did not return to the options; texts = %v", tester.Texts())
	}
	// The options still decide; picking one answers the question.
	if err := tester.Click("staging"); err != nil {
		t.Fatalf("click option after collapse: %v; texts = %v", err, tester.Texts())
	}
	if len(state.answers) != 1 || state.answers[0] != "staging" {
		t.Fatalf("answers = %v, want [staging]", state.answers)
	}
}

// TestAskDialogFreeText covers a question without options: one answer input, a
// Submit action that stays disabled while the input is empty, and the answer on
// Submit.
func TestAskDialogFreeText(t *testing.T) {
	state, tester := askDialogHarness(t, contracts.UserQuestion{ID: "q2", Text: "你的名字？"})
	for _, want := range []string{"需要你的确认", "你的名字？", "回答", "提交", "取消"} {
		if !tester.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	submit, ok := tester.AccessByRole(ui.RoleButton, "提交")
	if !ok {
		t.Fatalf("the Submit action is missing; texts = %v", tester.Texts())
	}
	if !submit.Disabled {
		t.Fatal("Submit is enabled with an empty answer")
	}
	// An empty answer can never be submitted, by click or by Enter.
	if err := tester.Click("提交"); err != nil {
		t.Fatalf("click disabled Submit: %v; texts = %v", err, tester.Texts())
	}
	tester.Key(0, ui.KeyEnter)
	if len(state.answers) != 0 {
		t.Fatalf("an empty answer was submitted: %v", state.answers)
	}

	askDialogFocus(t, tester, "回答")
	tester.Type("小明")
	tester.Frame()
	submit, ok = tester.AccessByRole(ui.RoleButton, "提交")
	if !ok || submit.Disabled {
		t.Fatalf("Submit is not enabled with a typed answer; info=%+v ok=%v", submit, ok)
	}
	if err := tester.Click("提交"); err != nil {
		t.Fatalf("click Submit: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if len(state.answers) != 1 || state.answers[0] != "小明" {
		t.Fatalf("answers = %v, want [小明]", state.answers)
	}
}

// TestAskDialogFreeTextEnterSubmits covers the keyboard path of a free-text
// question: Enter submits the typed, non-empty answer exactly once.
func TestAskDialogFreeTextEnterSubmits(t *testing.T) {
	state, tester := askDialogHarness(t, contracts.UserQuestion{ID: "q5", Text: "你的名字？"})
	askDialogFocus(t, tester, "回答")
	tester.Type("小明")
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if len(state.answers) != 1 || state.answers[0] != "小明" {
		t.Fatalf("answers = %v, want exactly [小明]", state.answers)
	}
	if state.cancels != 0 {
		t.Fatalf("cancels = %d, want none", state.cancels)
	}
}

// TestAskDialogNextQuestionStartsOnItsOptions covers the state reset between
// questions: the "Other…" input of a previous question is not shown by the next
// one.
func TestAskDialogNextQuestionStartsOnItsOptions(t *testing.T) {
	question := contracts.UserQuestion{
		ID: "q-first", Text: "部署到哪个环境？", Options: []string{"staging", "prod"},
	}
	state := &askDialogTestState{}
	view := func(c *ui.Context) {
		ui.Text(c, "主屏幕")
		uikit.AskDialog(c, uikit.AskDialogOptions{
			Question:  question,
			OtherOpen: &state.otherOpen,
			Answer:    &state.answer,
			OnAnswer: func(answer string) {
				state.answers = append(state.answers, answer)
			},
			OnCancel: func() { state.cancels++ },
		})
	}
	tester := ui.NewTester(view, 1200, 800)
	tester.Frame()
	if err := tester.Click("其他…"); err != nil {
		t.Fatalf("click Other: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !state.otherOpen {
		t.Fatal("Other did not open")
	}
	// The module pops the first question and shows the next one.
	state.otherOpen = false
	state.answer = ""
	question = contracts.UserQuestion{ID: "q-next", Text: "下一个？", Options: []string{"好"}}
	tester.Frame()
	for _, want := range []string{"下一个？", "好", "其他…"} {
		if !tester.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	if tester.HasText("输入自定义回答") {
		t.Fatalf("the previous question's Other input leaked into the next one; texts = %v", tester.Texts())
	}
}

// TestAskDialogEnglishCopy covers the localized copy of the component.
func TestAskDialogEnglishCopy(t *testing.T) {
	state := &askDialogTestState{}
	locale := &entryDialogTestLocale{lang: "en"}
	question := contracts.UserQuestion{ID: "e1", Text: "Deploy?", Options: []string{"yes"}}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Text(c, "主屏幕")
		uikit.AskDialog(c, uikit.AskDialogOptions{
			Question:  question,
			OtherOpen: &state.otherOpen,
			Answer:    &state.answer,
			Locale:    locale,
			OnAnswer:  func(answer string) { state.answers = append(state.answers, answer) },
			OnCancel:  func() { state.cancels++ },
		})
	}, 1200, 800)
	tester.Frame()
	for _, want := range []string{"Needs your input", "Deploy?", "yes", "Other…", "Cancel"} {
		if !tester.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}

	question = contracts.UserQuestion{ID: "e2", Text: "Your name?"}
	tester.Frame()
	for _, want := range []string{"Your name?", "Answer", "Submit", "Cancel"} {
		if !tester.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
}
