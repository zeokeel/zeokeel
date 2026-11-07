package session

// /feedback slash command: rate the latest assistant answer from a native modal
// and keep the message action row's Feedback value in sync.

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// normalizeSessionFeedback keeps only the two documented rating values.
func normalizeSessionFeedback(value string) string {
	switch trimmed := strings.TrimSpace(value); trimmed {
	case contracts.FeedbackGood, contracts.FeedbackBad:
		return trimmed
	default:
		return ""
	}
}

// latestAssistantTarget returns the active session's newest assistant message.
func latestAssistantTarget(chat contracts.ChatService) (contracts.ChatSession, contracts.ChatMessage, bool) {
	if chat == nil {
		return contracts.ChatSession{}, contracts.ChatMessage{}, false
	}
	id := chat.ActiveSession()
	if strings.TrimSpace(id) == "" {
		return contracts.ChatSession{}, contracts.ChatMessage{}, false
	}
	session, ok := chat.Session(id)
	if !ok {
		return contracts.ChatSession{}, contracts.ChatMessage{}, false
	}
	for index := len(session.Messages) - 1; index >= 0; index-- {
		if session.Messages[index].Role == contracts.ChatRoleAssistant {
			return session, session.Messages[index], true
		}
	}
	return session, contracts.ChatMessage{}, false
}

// feedbackSummary truncates the answer preview shown in the modal.
func feedbackSummary(text string) string {
	trimmed := strings.TrimSpace(text)
	const limit = 120
	runes := []rune(trimmed)
	if len(runes) <= limit {
		return trimmed
	}
	return string(runes[:limit]) + "…"
}

// openFeedbackDialog opens /feedback for the current newest assistant message.
func openFeedbackDialog(st *state, runtime contracts.UIRuntime, chat contracts.ChatService) {
	if _, ok := chat.(contracts.SessionFeedbacker); !ok {
		return
	}
	session, message, ok := latestAssistantTarget(chat)
	if !ok || strings.TrimSpace(message.ID) == "" {
		return
	}
	st.feedbackOpen = true
	st.feedbackSessionID = session.ID
	st.feedbackMessageID = message.ID
	st.feedbackMessageText = message.Text
	st.feedbackCurrent = normalizeSessionFeedback(message.Feedback)
	st.feedbackDialogErr = ""
	runtime.Invalidate()
}

// buildFeedbackDialog renders the /feedback native modal.
func buildFeedbackDialog(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	if !st.feedbackOpen {
		return
	}
	chat, _ := st.ctx.Get(contracts.KeyChat)
	feedbacker, ok := chat.(contracts.SessionFeedbacker)
	if !ok {
		st.feedbackOpen = false
		return
	}
	apply := func(value string) {
		if err := feedbacker.SetMessageFeedback(st.feedbackSessionID, st.feedbackMessageID, value); err != nil {
			st.feedbackDialogErr = st.tr("反馈保存失败：", "Failed to save feedback: ") + err.Error()
			runtime.Invalidate()
			return
		}
		st.feedbackOpen = false
		st.feedbackDialogErr = ""
		runtime.Invalidate()
	}
	ui.Modal(c, &st.feedbackOpen, func() {
		design.Apply(ui.Column(c).Width(480).Gap(design.Space3), func(el ui.Element) ui.Element {
			return el.PaddingX(design.Space5).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, st.tr("评价回答", "Rate answer")), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			summary := feedbackSummary(st.feedbackMessageText)
			if summary == "" {
				summary = st.tr("（这条回答没有文本）", "(This answer has no text)")
			}
			design.Apply(ui.Text(c, summary), t.TextMuted(design.TypeXS)).MaxLines(2).Ellipsis("…")
			if st.feedbackCurrent != "" {
				design.Apply(ui.Badge(c, st.tr("已反馈", "Rated")), t.Badge()).TextColor(t.Success)
			}
			if st.feedbackDialogErr != "" {
				buildDialogError(c, t, st.feedbackDialogErr)
			}
			ui.Row(c).FillWidth().Justify(ui.End).Gap(design.Space2).Children(func() {
				good := ui.PrimaryButton(c, st.tr("有帮助", "Helpful"))
				design.Apply(good, t.PrimaryButton())
				if good.Clicked() {
					apply(contracts.FeedbackGood)
				}
				bad := ui.Button(c, st.tr("没帮助", "Not helpful"))
				design.Apply(bad, func(el ui.Element) ui.Element {
					if st.feedbackCurrent == contracts.FeedbackBad {
						return el.BorderColor(t.Accent)
					}
					return el
				})
				if bad.Clicked() {
					apply(contracts.FeedbackBad)
				}
				if ui.Button(c, st.tr("清除评价", "Clear rating")).Clicked() {
					apply("")
				}
				if ui.Button(c, st.tr("取消", "Cancel")).Clicked() {
					st.feedbackOpen = false
					st.feedbackDialogErr = ""
					runtime.Invalidate()
				}
			})
		})
	})
}
