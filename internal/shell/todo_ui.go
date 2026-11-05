package shell

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/tools"
)

// todos is the live session's todo data source. The controller folds the todo
// tool's structured results into per-session state, so the panel shows exactly
// the list the agent last reported.
func (a *App) todos() []tools.Todo {
	if a.Ctrl == nil {
		return nil
	}
	return a.Ctrl.Todos()
}

// questions is the live session's pending ask_user questions.
func (a *App) questions() []tools.Question {
	if a.Ctrl == nil {
		return nil
	}
	return a.Ctrl.Questions()
}

// artifacts is the live session's presented artifacts (metadata only).
func (a *App) artifacts() []tools.Artifact {
	if a.Ctrl == nil {
		return nil
	}
	return a.Ctrl.Artifacts()
}

// liveTodoPanel is the task list beside the transcript. It only renders when
// the agent has reported tasks, so an empty session keeps the full-width
// transcript. The panel scrolls independently of the transcript.
func (a *App) liveTodoPanel(c *ui.Context, t *design.Tokens) {
	todos := a.todos()
	if len(todos) == 0 {
		return
	}
	done := 0
	for _, todo := range todos {
		if todo.Status == tools.TodoCompleted {
			done++
		}
	}
	ui.Column(c).Width(272).Shrink(0).Padding(design.Space4, design.Space4, design.Space4, design.Space4).
		Gap(design.Space2).BorderWidth(0, 0, 0, 1).BorderColor(t.BorderL2).
		Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				ui.Text(c, a.T("session.todos")).FontSize(12).FixedLineHeight(18).
					FontWeight(600).TextColor(t.LabelPrimary).Grow(1)
				ui.Text(c, fmt.Sprintf("%d/%d", done, len(todos))).FontSize(11).
					FixedLineHeight(18).TextColor(t.LabelTertiary)
			})
			ui.Scroll(c).Grow(1).Children(func() {
				for _, todo := range todos {
					a.todoRow(c, t, todo)
				}
			})
		})
}

// todoRow draws one task: a status glyph, the task text, and the status word.
// A completed task is struck through and muted, the usual todo convention.
func (a *App) todoRow(c *ui.Context, t *design.Tokens, todo tools.Todo) {
	glyph, color := "○", t.LabelTertiary
	switch todo.Status {
	case tools.TodoInProgress:
		glyph, color = "◐", t.Accent
	case tools.TodoCompleted:
		glyph, color = "●", t.Success
	}
	ui.Row(c).Gap(design.Space2).AlignItems(ui.Start).Padding(2, 0, 2, 0).
		Children(func() {
			ui.Text(c, glyph).FontSize(12).FixedLineHeight(18).TextColor(color).NoWrap()
			text := ui.Text(c, todo.Task).FontSize(12).FixedLineHeight(18).
				TextColor(t.LabelPrimary).Grow(1).MaxLines(3).Ellipsis("…")
			if todo.Status == tools.TodoCompleted {
				text.TextColor(t.LabelTertiary).Strikethrough()
			}
			ui.Text(c, a.todoStatusLabel(string(todo.Status))).FontSize(10).FixedLineHeight(18).
				TextColor(t.LabelTertiary).NoWrap()
		})
}

// questionCards draws one card per pending ask_user question above the
// composer. The answer is submitted as the next user turn: the tool returns
// immediately (a blocking tool would deadlock the turn), so the controller
// records the question and resumes the conversation with the answer.
func (a *App) questionCards(c *ui.Context, t *design.Tokens) {
	questions := a.questions()
	if len(questions) == 0 {
		return
	}
	uiState := a.uiState()
	for _, question := range questions {
		question := question
		a.menuFrame(c, t, 868, func() {
			ui.Column(c).Key("question:" + question.CallID).
				Gap(design.Space2).Children(func() {
				ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
					ui.Text(c, a.T("session.question")).FontSize(12).FixedLineHeight(18).
						TextColor(t.LabelTertiary).Grow(1)
					ui.Text(c, "ask_user").FontSize(11).FixedLineHeight(18).
						TextColor(t.LabelTertiary)
				})
				ui.Text(c, question.Question).FontSize(13).FixedLineHeight(20).
					TextColor(t.LabelPrimary)
				if len(question.Options) > 0 {
					ui.Text(c, a.T("session.suggested", strings.Join(question.Options, " · "))).
						FontSize(12).FixedLineHeight(18).TextColor(t.LabelSecondary).
						MaxLines(2).Ellipsis("…")
				}
				if question.Context != "" {
					ui.Text(c, question.Context).FontSize(12).FixedLineHeight(18).
						TextColor(t.LabelTertiary).MaxLines(2).Ellipsis("…")
				}
				ui.TextInput(c, &uiState.questionDraft).Label(a.T("session.answer_input")).
					FontSize(13).Height(34)
				ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
					send := design.Apply(ui.Button(c, a.T("session.send_answer")), t.PrimaryButton())
					if send.Clicked() {
						a.answerQuestion(question)
					}
					if uiState.questionErr != "" {
						ui.Text(c, uiState.questionErr).FontSize(11).
							TextColor(t.Danger).Grow(1).MaxLines(2).Ellipsis("…")
					}
				})
			})
		})
	}
}

// answerQuestion submits the question card's draft through the controller.
// The controller removes the card on success and restores it on failure, so
// the error stays visible here.
func (a *App) answerQuestion(question tools.Question) {
	if a.Ctrl == nil {
		return
	}
	uiState := a.uiState()
	uiState.questionErr = ""
	if err := a.Ctrl.AnswerQuestion(context.Background(), question.CallID, uiState.questionDraft); err != nil {
		uiState.questionErr = err.Error()
		return
	}
	uiState.questionDraft = ""
}

// liveArtifacts lists the artifacts the agent presented through the present
// tool. Clicking a card expands inline Markdown; the path is always shown and
// is never copied or opened by the shell.
func (a *App) liveArtifacts(c *ui.Context, t *design.Tokens) {
	artifacts := a.artifacts()
	if len(artifacts) == 0 {
		return
	}
	uiState := a.uiState()
	ui.Column(c).Key("artifacts").Gap(design.Space1).Children(func() {
		ui.Text(c, a.T("session.presented")).FontSize(11).FixedLineHeight(16).
			TextColor(t.LabelTertiary)
		for _, artifact := range artifacts {
			artifact := artifact
			open := uiState.artifactRaw[artifact.CallID]
			card := ui.Button(c, "").Padding(design.Space2).Radius(design.RadiusSM).
				Transition(design.ColorTransition())
			card.Children(func() {
				ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "▣").FontSize(12).TextColor(t.Accent)
					ui.Text(c, artifact.Title).FontSize(12).FixedLineHeight(18).
						FontWeight(600).TextColor(t.LabelPrimary).Grow(1).NoWrap().
						Ellipsis("…")
					ui.Text(c, artifact.Kind).FontSize(10).FixedLineHeight(18).
						TextColor(t.LabelTertiary).NoWrap()
				})
				if artifact.Path != "" {
					ui.Text(c, artifact.Path).FontSize(11).FixedLineHeight(16).
						TextColor(t.LabelSecondary).NoWrap().Ellipsis("…")
				}
				if open {
					if artifact.Markdown != "" {
						ui.Text(c, artifact.Markdown).FontSize(12).FixedLineHeight(18).
							TextColor(t.LabelPrimary).MaxLines(10).Ellipsis("…")
					} else {
						ui.Text(c, a.T("session.no_inline_content")).
							FontSize(11).TextColor(t.LabelTertiary)
					}
				}
			})
			if card.Clicked() {
				uiState.artifactRaw[artifact.CallID] = !open
			}
		}
	})
}
