package session

// /feedback slash command: rate the latest assistant answer from a native modal
// and keep the message action row's Feedback value in sync. The dialog follows
// the product's footer convention: cancel on the left, save (accent, Enter) on
// the right; the two ratings are equal-weight choices, not one primary button.

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
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
	st.feedbackRating = st.feedbackCurrent
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
		if value == "" {
			st.toast(contracts.NotifyInfo, st.tr("已清除评价", "Rating cleared"))
		} else {
			st.toast(contracts.NotifySuccess, st.tr("已保存评价", "Rating saved"))
		}
		runtime.Invalidate()
	}
	save := func() { apply(st.feedbackRating) }
	ui.Modal(c, &st.feedbackOpen, func() {
		dialogEnter(c, save)
		design.Apply(ui.Column(c).Width(design.DialogWidthM).Gap(design.Space4), func(el ui.Element) ui.Element {
			return el.PaddingX(design.Space6).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, st.tr("评价回答", "Rate answer")), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			summary := feedbackSummary(st.feedbackMessageText)
			if summary == "" {
				summary = st.tr("（这条回答没有文本）", "(This answer has no text)")
			}
			design.Apply(ui.Text(c, summary), t.TextMuted(design.TypeXS)).MaxLines(2).Ellipsis("…")
			// The two ratings are two official Toggles in an official
			// ToggleGroup: pressed semantics, mutually exclusive, and pressing the
			// pressed one again clears it — both unpressed is 未评价, which the
			// dialog's Save/Clear actions write. (A Segmented control cannot say
			// "nothing chosen": it clamps an out-of-range index to the first
			// segment, which would preselect 有帮助 and dead-end its Save.)
			rating := st.feedbackRating
			goodOn := rating == contracts.FeedbackGood
			ui.ToggleGroup(c, func() {
				good := ui.Toggle(c.Key("session.feedback.good"), &goodOn, st.tr("有帮助", "Helpful"))
				if good.Changed() {
					switch {
					case goodOn:
						rating = contracts.FeedbackGood
					case rating == contracts.FeedbackGood:
						rating = ""
					}
				}
				badOn := rating == contracts.FeedbackBad
				bad := ui.Toggle(c.Key("session.feedback.bad"), &badOn, st.tr("没帮助", "Not helpful"))
				if bad.Changed() {
					switch {
					case badOn:
						rating = contracts.FeedbackBad
					case rating == contracts.FeedbackBad:
						rating = ""
					}
				}
			}).Label(st.tr("评价", "Rating"))
			if rating != st.feedbackRating {
				st.feedbackRating = rating
				runtime.Invalidate()
			}
			if st.feedbackDialogErr != "" {
				uikit.ErrorLine(c, st.feedbackDialogErr)
			}
			ui.Divider(c)
			actions := []uikit.DialogAction{
				{Label: st.tr("取消", "Cancel"), OnClick: func() {
					st.feedbackOpen = false
					st.feedbackDialogErr = ""
					runtime.Invalidate()
				}},
			}
			if st.feedbackCurrent != "" {
				actions = append(actions, uikit.DialogAction{
					Label:   st.tr("清除评价", "Clear rating"),
					OnClick: func() { apply("") },
				})
			}
			actions = append(actions, uikit.DialogAction{
				Label: st.tr("保存", "Save"), Primary: true,
				Disabled: st.feedbackRating == st.feedbackCurrent,
				OnClick:  save,
			})
			uikit.DialogFooter(c, actions)
		})
	})
}
