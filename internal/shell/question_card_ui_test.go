package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/tools"
)

// questionCardFixture starts one turn that asks a question with options and
// returns the live app with the card on screen.
func questionCardFixture(t *testing.T) *App {
	t.Helper()
	provider := &m6Provider{steps: []m6Step{
		{tool: "ask_user", args: `{"question":"Which database should I use?","options":["sqlite","postgres"]}`},
		{text: "waiting for your answer"},
		{text: "Using sqlite."},
	}}
	app := m6LiveApp(t, provider, tools.NewAskUserTool())
	m6Submit(t, app, "choose a database")
	return app
}

// TestQuestionCardOptionButtonsAnswer pins FP-16's option half: suggested
// answers are real buttons, and clicking one submits that answer without
// typing.
func TestQuestionCardOptionButtonsAnswer(t *testing.T) {
	app := questionCardFixture(t)
	tester := ui.NewTester(app.View, 1600, 1000)
	if _, ok := tester.Find("Choose sqlite"); !ok {
		t.Fatalf("option button missing; texts: %v", tester.Texts())
	}
	if err := tester.Click("Choose sqlite"); err != nil {
		t.Fatalf("click option: %v", err)
	}
	m6WaitIdle(t, app.Ctrl)
	app.Ctrl.Wait()
	app.SetSnapshot(app.Ctrl.Snapshot())
	tester.Frame()
	if got := app.Ctrl.Questions(); len(got) != 0 {
		t.Fatalf("questions after clicking an option = %+v, want none", got)
	}
	if !tester.HasText("Using sqlite.") {
		t.Fatalf("option answer did not reach the model; texts: %v", tester.Texts())
	}
}

// TestQuestionCardEmptyAnswerIsRefused pins FP-16's validation: sending an
// empty answer keeps the card (and the pending question) and explains what is
// missing instead of submitting a blank message.
func TestQuestionCardEmptyAnswerIsRefused(t *testing.T) {
	app := questionCardFixture(t)
	tester := ui.NewTester(app.View, 1600, 1000)
	if err := tester.Click("Send answer"); err != nil {
		t.Fatalf("click Send answer with an empty draft: %v", err)
	}
	tester.Frame()
	if got := app.Ctrl.Questions(); len(got) != 1 {
		t.Fatalf("empty answer resolved the question: %+v", got)
	}
	if !tester.HasText("Enter an answer") {
		t.Fatalf("empty-answer validation missing; texts: %v", tester.Texts())
	}
	if !tester.HasText("Answer input") {
		t.Fatalf("question card disappeared after an empty send; texts: %v", tester.Texts())
	}
}
