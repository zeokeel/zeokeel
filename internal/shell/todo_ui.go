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
	th := t.Theme()
	done := 0
	for _, todo := range todos {
		if todo.Status == tools.TodoCompleted {
			done++
		}
	}
	progress := float32(done) * 100 / float32(len(todos))
	color := t.Accent
	if done == len(todos) {
		color = t.Success
	}
	ui.Column(c).Width(th.Space(68)).Shrink(0).Padding(th.Space(4)).
		Gap(th.Space(2)).Background(t.BgSidebar).
		BorderWidth(0, 0, 0, 1).BorderColor(t.BorderL2).
		Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(th.Space(2)).Children(func() {
				ui.Text(c, a.T("session.todos")).FontSize(12).FixedLineHeight(18).
					FontWeight(600).TextColor(t.LabelPrimary).Grow(1)
				count := ui.Row(c).Height(th.Space(5)).PaddingX(th.Space(1.5)).
					Radius(design.RadiusMax).Background(t.BgHover).AlignItems(ui.Center)
				count.Children(func() {
					ui.Text(c, fmt.Sprintf("%d/%d", done, len(todos))).FontSize(11).
						FixedLineHeight(16).TextColor(t.LabelTertiary)
				})
			})
			// The list's progress at a glance: one thin brand track under the
			// header, green once every task is done.
			track := ui.Row(c).Height(th.Space(1)).Radius(design.RadiusMax).
				Background(t.BgHover)
			track.Children(func() {
				fill := ui.Box(c).Height(th.Space(1)).Radius(design.RadiusMax).
					WidthPercent(progress).Background(color)
				_ = fill
			})
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).Gap(th.Space(1)).Children(func() {
					for _, todo := range todos {
						a.todoRow(c, t, todo)
					}
				})
			})
		})
}

// todoRow draws one task: a brand status mark (empty ring, accent dot while
// running, filled success check when done), the task text and the status word.
// A completed task is struck through and muted, the usual todo convention.
func (a *App) todoRow(c *ui.Context, t *design.Tokens, todo tools.Todo) {
	th := t.Theme()
	color := t.LabelTertiary
	ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Start).
		Padding(th.Space(1), 0).Children(func() {
		mark := ui.Row(c).Size(th.Space(3.5), th.Space(3.5)).Center().
			Radius(th.Space(1.75)).Border(1.5, color).Shrink(0).
			Margin(0, th.Space(0.5), 0, 0)
		switch todo.Status {
		case tools.TodoInProgress:
			color = t.Accent
			mark = mark.Border(1.5, color)
			mark.Children(func() {
				ui.Box(c).Size(th.Space(1.5), th.Space(1.5)).
					Radius(th.Space(0.75)).Background(color)
			})
		case tools.TodoCompleted:
			color = t.Success
			mark = mark.Border(0, t.Success).Background(color)
			mark.Children(func() {
				ui.Text(c, "✓").FontSize(9).FontWeight(700).
					TextColor(t.BgRaised).NoWrap()
			})
		}
		text := ui.Text(c, todo.Task).FontSize(12).FixedLineHeight(18).
			TextColor(t.LabelPrimary).Grow(1).MaxLines(3).Ellipsis("…")
		if todo.Status == tools.TodoCompleted {
			text.TextColor(t.LabelTertiary).Strikethrough()
		}
		status := a.todoStatusLabel(string(todo.Status))
		statusColor := t.LabelTertiary
		if todo.Status == tools.TodoInProgress {
			statusColor = t.Accent
		} else if todo.Status == tools.TodoCompleted {
			statusColor = t.Success
		}
		ui.Text(c, status).FontSize(10).FixedLineHeight(18).
			TextColor(statusColor).NoWrap()
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
	th := t.Theme()
	for _, question := range questions {
		question := question
		a.menuFrame(c, t, a.sessionComposerWidthFor(c), func() {
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
					FontSize(13).Height(th.Space(9))
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
	th := t.Theme()
	uiState := a.uiState()
	ui.Column(c).Key("artifacts").Gap(th.Space(1)).Children(func() {
		ui.Text(c, a.T("session.presented")).FontSize(11).FixedLineHeight(16).
			TextColor(t.LabelTertiary)
		for _, artifact := range artifacts {
			artifact := artifact
			open := uiState.artifactRaw[artifact.CallID]
			card := ui.ButtonBase(c).FillWidth().Padding(th.Space(2)).
				Radius(design.RadiusSM).Border(1, t.BorderL2).
				Justify(ui.Start).Background(t.BgRaised).
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
