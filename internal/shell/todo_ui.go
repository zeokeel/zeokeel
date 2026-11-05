package shell

import (
	"context"
	"fmt"
	"path/filepath"
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
	a.trackQuestionCards(questions)
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
					// Suggested answers are one click away: the chips submit
					// that answer directly, so the common case needs no typing
					// (FP-16).
					ui.Row(c).Gap(design.Space2).Wrap().AlignItems(ui.Center).Children(func() {
						for _, option := range question.Options {
							option := option
							label := a.menuText("Choose "+option, "选择 "+option)
							chip := ui.Button(c, option).Height(th.Space(8)).Label(label)
							chip.OnClick(func() { a.answerQuestionWith(question, option) })
						}
					})
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

// trackQuestionCards notices when a pending card disappears without the user
// answering it: a blocking question whose turn context ended or timed out is
// withdrawn by the controller, and the user should see why their card is gone
// instead of wondering whether the answer was lost (FP-16).
func (a *App) trackQuestionCards(questions []tools.Question) {
	if a.Ctrl == nil {
		return
	}
	st := a.shellUI()
	sessionID := a.Ctrl.CurrentSessionID()
	if st.questionSession != sessionID {
		st.questionSession = sessionID
		st.questionSeen = nil
		st.questionAnswered = nil
	}
	seen := make(map[string]bool, len(questions))
	for _, question := range questions {
		seen[question.CallID] = true
	}
	for id := range st.questionSeen {
		if seen[id] || st.questionAnswered[id] {
			continue
		}
		a.toastDeferred(nil, a.menuText(
			"The question was withdrawn (it timed out or the turn ended); the agent continues.",
			"问题卡已撤回（超时或回合结束）；代理将继续处理。"))
	}
	for id := range st.questionAnswered {
		if !seen[id] {
			delete(st.questionAnswered, id)
		}
	}
	st.questionSeen = seen
}

// answerQuestion submits the question card's draft through the controller.
// The controller removes the card on success and restores it on failure, so
// the error stays visible here. An empty (or whitespace-only) draft is refused
// locally: it would start a blank turn and lose the question.
func (a *App) answerQuestion(question tools.Question) {
	if a.Ctrl == nil {
		return
	}
	uiState := a.uiState()
	st := a.shellUI()
	if st.questionAnswered == nil {
		st.questionAnswered = make(map[string]bool)
	}
	st.questionAnswered[question.CallID] = true
	uiState.questionErr = ""
	answer := strings.TrimSpace(uiState.questionDraft)
	if answer == "" {
		uiState.questionErr = a.menuText("Enter an answer before sending.",
			"请输入回答后再发送。")
		return
	}
	if err := a.Ctrl.AnswerQuestion(context.Background(), question.CallID, answer); err != nil {
		uiState.questionErr = err.Error()
		return
	}
	uiState.questionDraft = ""
}

// answerQuestionWith submits one of the question's suggested answers directly.
func (a *App) answerQuestionWith(question tools.Question, answer string) {
	uiState := a.uiState()
	uiState.questionDraft = answer
	uiState.questionErr = ""
	a.answerQuestion(question)
}

// liveArtifacts lists the artifacts the agent presented through the present
// tool. Clicking the card's title row expands inline Markdown; a presented file
// also carries its native actions: preview it in the Files tab, or hand it to
// the OS default application / file manager. The shell never reads a path
// outside the session workspace, so such a card explains that instead.
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
			card := ui.Column(c).FillWidth().Padding(th.Space(2)).
				Radius(design.RadiusSM).Border(1, t.BorderL2).
				Background(t.BgRaised).Transition(design.ColorTransition())
			card.Children(func() {
				// The title row stays the expand control, so a click on the
				// title still shows or hides the inline Markdown.
				toggle := ui.ButtonBase(c).FillWidth().Padding(0).Justify(ui.Start)
				toggle.Children(func() {
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
				})
				if toggle.Clicked() {
					uiState.artifactRaw[artifact.CallID] = !open
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
				if artifact.Path != "" {
					a.artifactActions(c, t, artifact)
				}
			})
		}
	})
}

// artifactActions is the present card's action row: preview the file in the
// Files tab, and, when the desktop backend is available, open it with the OS
// default application or reveal it in the file manager. A path outside the
// session workspace gets the explanation only; the card never opens a file the
// panel would refuse to read.
func (a *App) artifactActions(c *ui.Context, t *design.Tokens, artifact tools.Artifact) {
	th := t.Theme()
	rel, previewable := a.artifactPreviewRel(artifact.Path)
	ui.Column(c).Gap(th.Space(1)).Children(func() {
		if !previewable {
			ui.Text(c, a.T("files.that_path_is_outside_the_workspace")).
				FontSize(th.FontSize - 3).FixedLineHeight(th.Space(4)).
				TextColor(t.WarningText).MaxLines(2).Ellipsis("…")
			return
		}
		available := a.filesNativeAvailable()
		ui.Row(c).Gap(th.Space(1)).Wrap().AlignItems(ui.Center).Children(func() {
			a.artifactActionButton(c, t, a.T("session.preview_in_sidebar"),
				func() { a.openArtifactPreview(artifact.Path) })
			if !available {
				return
			}
			abs := filepath.Join(a.filesRoot(), filepath.FromSlash(rel))
			a.artifactActionButton(c, t, a.T("session.open_artifact_in_system"),
				func() { a.filesNativeRun(c, abs, a.filesOpener()) })
			a.artifactActionButton(c, t, a.T("session.reveal_artifact_in_file_manager"),
				func() { a.filesNativeRun(c, abs, a.filesRevealer()) })
		})
		if !available {
			// The reference product explains why the native controls are
			// missing and what still works; the card keeps the preview action.
			ui.Text(c, a.T("session.host_note")).FontSize(th.FontSize - 3).
				FixedLineHeight(th.Space(4)).TextColor(t.LabelTertiary).
				MaxLines(2).Ellipsis("…")
		}
	})
}

// artifactActionButton is one compact text action of the present card, on the
// same native ButtonBase the rest of the panel uses.
func (a *App) artifactActionButton(c *ui.Context, t *design.Tokens, label string, onClick func()) {
	th := t.Theme()
	button := ui.ButtonBase(c).Height(th.Space(6)).PaddingX(th.Space(2)).
		Radius(design.RadiusXS).Label(label).Tooltip(label)
	if button.Hovered() {
		button = button.Background(t.BgHover)
	}
	button.Children(func() {
		ui.Text(c, label).FontSize(th.FontSize - 3).
			FixedLineHeight(th.Space(4)).TextColor(t.LabelSecondary)
	})
	button.OnClick(onClick)
}
