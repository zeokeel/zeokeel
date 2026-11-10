package uikit

// The conversation transcript container (S1 in docs/AGENT-SURFACES.md): the
// official List of messages on one centered reading column, with the container
// behavior every transcript needs — virtualization, stable row identity, the
// streaming follow, the floating "back to latest" action, the first-run hero —
// while the message content itself stays the caller's business slot. The
// session module keeps its body/action/usage rendering in Row; a host without
// its own body gets the entry's default text rows.
//
// The row label never repeats a message's prose: the rendered content is the
// message (its Markdown source must not leak into the accessibility tree), so
// the label is the author role plus the row ordinal, as the session module's
// transcript did before this entry existed.

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// SessionThreadOptions is one conversation transcript.
type SessionThreadOptions struct {
	// Messages are the transcript entries in order; the List virtualizes them
	// (only visible rows build) and keys each row by message ID, so a streaming
	// reply grows its row in place and the user's place survives new content.
	Messages []contracts.ChatMessage
	// Running is true while the turn streams; the default row shows the
	// generating indicator for a streaming reply while it runs.
	Running bool
	// Empty builds the first-run hero, shown through the List's children slot
	// while there are no rows (no blank scroll area first).
	Empty func(c *ui.Context)
	// State is the caller's transcript List state: the thread owns its
	// configuration (row key/label, FollowEnd, HoldPlace) and the caller keeps
	// the scroll offset and selection.
	State *ui.ListState
	// OnRetry is the retry action of the thread-level error band (shown with
	// Error); the product's rows render their own per-turn retry and pass nil.
	OnRetry func()
	// Row builds one message's whole content (the product's rich body slot:
	// prose, reasoning, tool cards, actions, usage). Nil renders the entry's
	// default text rows.
	Row func(c *ui.Context, message contracts.ChatMessage)
	// SameTurn reports whether next continues the same transcript row as prev.
	// When set, a run of consecutive messages it accepts shares one List row
	// (and one accessibility label range) instead of one row per message, so a
	// multi-step assistant turn folds its process once and shows the final
	// result under it. Nil keeps one row per message.
	SameTurn func(prev, next contracts.ChatMessage) bool
	// GroupRow builds one grouped row (a run with more than one message); Row
	// still builds single-message runs. Nil falls back to Row for the first
	// message of a run (no loss on single-message turns).
	GroupRow func(c *ui.Context, messages []contracts.ChatMessage)
	// Markdown is the reply Markdown theme of the default rows; nil uses the
	// library defaults.
	Markdown *markdown.Options
	// Locale localizes the List label, the row labels and the back-to-latest
	// action; nil falls back to the product's Chinese copy.
	Locale contracts.LocaleService
	// HoldPlace is the caller's one-shot fold flag: a reasoning or tool toggle
	// sets it, and the thread turns it into ListState.HoldPlace for the next
	// layout so the toggled row does not jump under the pointer.
	HoldPlace *bool
	// FollowEnd is the caller-persisted follow state: the thread writes back the
	// AtEnd result each frame and the back-to-latest action resumes it. Nil lets
	// the thread track the follow alone (a host that remounts the thread loses
	// the scrolled-up state then).
	FollowEnd *bool
	// LaidOut is the caller-persisted first-layout gate: it waits one laid-out
	// frame before reading AtEnd, so the floating action never flashes on the
	// first frame. Nil falls back to a thread-local gate.
	LaidOut *bool
	// Error is the session-level error shown under the list; callers pass it
	// only when it should show (the session module already suppresses an error
	// its failed turn repeats inline).
	Error string
}

// SessionThread draws the conversation transcript and returns nothing: the
// container, scroll follow, hero and floating action are the entry's, the
// message bodies are the caller's Row slot. A List row whose content is the
// message's prose keeps its accessibility name at the role+ordinal level.
func SessionThread(c *ui.Context, o SessionThreadOptions) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Grow(1).Children(func() {
		state := o.State
		if state == nil {
			state = &ui.ListState{}
		}
		messages := o.Messages
		runs := sessionRowRuns(messages, o.SameTurn)
		state.Key = func(index int) any {
			if index < 0 || index >= len(runs) {
				return nil
			}
			return messages[runs[index][0]].ID
		}
		state.Label = func(index int) string {
			if index < 0 || index >= len(runs) {
				return ""
			}
			run := runs[index]
			if len(run) > 1 {
				return sessionRunLabel(o.Locale, messages[run[0]], run[0], run[len(run)-1])
			}
			return sessionRowLabel(o.Locale, messages[run[0]], run[0])
		}
		// Each layout follows the end while the user stays there; scrolling away
		// stops the follow inside the List, and ScrollToEnd resumes it.
		state.FollowEnd = true
		if o.HoldPlace != nil && *o.HoldPlace {
			state.HoldPlace()
			*o.HoldPlace = false
		}
		list := ui.List(c, state, len(runs)).
			Label(T(o.Locale, "对话消息", "Conversation messages")).
			Gap(design.Space5).
			PaddingY(design.Space5).
			FillWidth().Grow(1)
		if len(runs) == 0 && o.Empty != nil {
			list.Children(func() {
				ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Start).Children(func() {
					design.Apply(ui.Column(c), t.Gap(design.Space5)).
						Width(design.TranscriptWidth).MaxWidthPercent(92).
						Children(func() {
							o.Empty(c)
						})
				})
			})
		}
		list.Rows(func(row ui.ListRow) {
			run := runs[row.Index]
			ui.Row(row.Context).FillWidth().Justify(ui.Center).AlignItems(ui.Start).Children(func() {
				design.Apply(ui.Column(row.Context), t.Gap(design.Space5)).
					Width(design.TranscriptWidth).MaxWidthPercent(92).Children(func() {
					if len(run) > 1 && o.GroupRow != nil {
						group := make([]contracts.ChatMessage, 0, len(run))
						for _, index := range run {
							group = append(group, messages[index])
						}
						o.GroupRow(row.Context, group)
						return
					}
					message := messages[run[0]]
					if o.Row != nil {
						o.Row(row.Context, message)
						return
					}
					sessionDefaultRow(row.Context, t, o, message)
				})
			})
		})
		// AtEnd reads the last laid-out frame, so the first layout uses the
		// caller's belief instead; after that the follow state tracks the end.
		laidOut := o.LaidOut
		if laidOut == nil {
			laidOut = ui.Local(list, "sessionThreadLaidOut", func() bool { return false })
		}
		follow := true
		if *laidOut {
			follow = state.AtEnd()
			if o.FollowEnd != nil {
				*o.FollowEnd = follow
			}
		} else if o.FollowEnd != nil {
			follow = *o.FollowEnd
		}
		*laidOut = true
		if o.Error != "" {
			ErrorLine(c, o.Error)
			if o.OnRetry != nil {
				if retry := design.Apply(ui.Button(c, T(o.Locale, "重试", "Retry")), t.SecondaryButton()); retry.Clicked() {
					o.OnRetry()
				}
			}
		}
		if !follow {
			sessionBackToLatest(c, t, state, o.FollowEnd,
				T(o.Locale, "回到最新", "Back to latest"))
		}
	})
}

// sessionBackToLatest shows the floating "back to latest" action over the
// transcript while the reader is away from its end: it scrolls the official List
// to the end and resumes the follow (list.md ScrollToEnd/FollowEnd).
func sessionBackToLatest(c *ui.Context, t *design.Tokens, state *ui.ListState,
	follow *bool, label string) {
	ui.Row(c).Absolute().Right(design.Space4).Bottom(design.Space4).Children(func() {
		button := design.Apply(ui.PrimaryButton(c, label), t.PrimaryButton()).Label(label)
		button.Children(func() {
			ui.Icon(c, icons.ChevronDown()).Size(design.Space3, design.Space3).TextColor(t.OnPrimary)
		})
		if button.Clicked() {
			state.ScrollToEnd()
			if follow != nil {
				*follow = true
			}
			c.Invalidate()
		}
	})
}

// sessionRowRuns groups message indexes into transcript rows: a run starts at
// every message SameTurn rejects (or at every message when SameTurn is nil), so
// the List virtualizes rows rather than raw messages.
func sessionRowRuns(messages []contracts.ChatMessage, sameTurn func(prev, next contracts.ChatMessage) bool) [][]int {
	runs := make([][]int, 0, len(messages))
	for index := range messages {
		if index == 0 || sameTurn == nil || !sameTurn(messages[index-1], messages[index]) {
			runs = append(runs, []int{index})
			continue
		}
		last := len(runs) - 1
		runs[last] = append(runs[last], index)
	}
	return runs
}

// sessionRunLabel names a grouped transcript row: the first author role plus
// the row ordinal range, still never the messages' prose.
func sessionRunLabel(locale contracts.LocaleService, first contracts.ChatMessage, start, end int) string {
	role := T(locale, "消息", "Message")
	switch first.Role {
	case contracts.ChatRoleUser:
		role = T(locale, "用户", "User")
	case contracts.ChatRoleAssistant:
		role = T(locale, "助手", "Assistant")
	case contracts.ChatRoleTool:
		role = T(locale, "工具", "Tool")
	case contracts.ChatRoleSystem:
		role = T(locale, "系统", "System")
	}
	return fmt.Sprintf("%s %d–%d", role, start+1, end+1)
}

// sessionRowLabel names one transcript row for assistive technology and the
// List's type-ahead: the author role plus the row ordinal, never the message's
// prose (the row's rendered content is the message).
func sessionRowLabel(locale contracts.LocaleService, message contracts.ChatMessage,
	index int) string {
	role := T(locale, "消息", "Message")
	switch message.Role {
	case contracts.ChatRoleUser:
		role = T(locale, "用户", "User")
	case contracts.ChatRoleAssistant:
		role = T(locale, "助手", "Assistant")
	case contracts.ChatRoleTool:
		role = T(locale, "工具", "Tool")
	case contracts.ChatRoleSystem:
		role = T(locale, "系统", "System")
	}
	return fmt.Sprintf("%s %d", role, index+1)
}

// sessionDefaultRow is the entry's fallback body for a host without its own Row
// slot: user text, a role badge for tool/system entries, and the reply Markdown
// (streamed while the turn runs) with the generating indicator and the error
// line. The product renders its richer body in Row instead.
func sessionDefaultRow(c *ui.Context, t *design.Tokens, o SessionThreadOptions,
	message contracts.ChatMessage) {
	switch message.Role {
	case contracts.ChatRoleUser:
		if text := strings.TrimSpace(message.Text); text != "" {
			design.Apply(ui.Text(c, message.Text), t.Font(design.TypeBody)).
				TextColor(t.LabelPrimary)
		}
	case contracts.ChatRoleTool, contracts.ChatRoleSystem:
		role := T(o.Locale, "工具", "Tool")
		if message.Role == contracts.ChatRoleSystem {
			role = T(o.Locale, "系统", "System")
		}
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Start).Children(func() {
			design.Apply(ui.Badge(c, role), t.Badge())
			design.Apply(ui.Text(c, message.Text), t.TextMuted(design.TypeXS))
		})
	default:
		if text := strings.TrimSpace(message.Text); text != "" {
			if message.Streaming {
				markdown.Stream(c, message.Text, o.Markdown)
			} else {
				markdown.View(c, message.Text, o.Markdown)
			}
		}
		if message.Streaming && o.Running {
			ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				ui.Spinner(c).Label(T(o.Locale, "生成中", "Generating"))
				design.Apply(ui.Text(c, T(o.Locale, "生成中", "Generating")),
					t.TextMuted(design.TypeXS))
			})
		}
		if message.Error != "" {
			ErrorLine(c, message.Error)
		}
	}
}
