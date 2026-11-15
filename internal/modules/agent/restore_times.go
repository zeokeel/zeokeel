package agent

// Restored message clocks.
//
// The durable Rig history carries no per-message timestamps, so a restored
// conversation used to show no clocks at all (the UI then drew an isolated ⋯
// per turn). The session log does record the turn boundaries, and restore uses
// them honestly:
//
//   - a user_message event's time is the clock of the human (or goal-round)
//     message with the same text, matched in order — a compacted, rewound or
//     forked history that no longer contains the text simply keeps no clock;
//   - the run_end that follows it is the clock of the step that ended that turn
//     (the final assistant message before the next user message), never of the
//     tool steps in the middle of it.
//
// Nothing is invented: messages without a matching event stay at the zero time,
// and a run_end without a turn it can end (the bounded event window cut the
// user_message off) is ignored.

import (
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Durable event types the restore-time backfill reads (the same literals the
// agent appends when it records a turn).
const (
	restoreUserMessageEvent = "user_message"
	restoreRunEndEvent      = "run_end"
)

// restoredTurnTime is one durable turn boundary: the user message's text and
// clock, plus the clock of the run_end that ended the turn (zero when the run
// never finished, e.g. the app crashed mid-turn).
type restoredTurnTime struct {
	text   string
	userAt time.Time
	endAt  time.Time
}

// hydrateRestoredTimes backfills the restored read model from the durable turn
// boundaries. restored turns are matched greedily by text from a cursor, so
// compacted-away or rewound turns are skipped instead of handing their clocks
// to the wrong message.
func hydrateRestoredTimes(state *sessionState, turns []restoredTurnTime) {
	if state == nil || len(state.messages) == 0 || len(turns) == 0 {
		return
	}
	cursor := 0
	for index := range state.messages {
		message := &state.messages[index]
		if message.Role != contracts.ChatRoleUser {
			continue
		}
		match := -1
		for scan := cursor; scan < len(turns); scan++ {
			if strings.TrimSpace(turns[scan].text) == strings.TrimSpace(message.Text) {
				match = scan
				break
			}
		}
		if match < 0 {
			continue
		}
		cursor = match + 1
		turn := turns[match]
		if message.CreatedAt.IsZero() && !turn.userAt.IsZero() {
			message.CreatedAt = turn.userAt
		}
		if turn.endAt.IsZero() {
			continue
		}
		if final := finalAssistantOfTurn(state.messages, index); final >= 0 {
			if state.messages[final].CreatedAt.IsZero() {
				state.messages[final].CreatedAt = turn.endAt
			}
		}
	}
}

// finalAssistantOfTurn returns the index of the last assistant step before the
// next user message (the step that ended the turn), or -1 when the turn has no
// assistant message.
func finalAssistantOfTurn(messages []contracts.ChatMessage, userIndex int) int {
	final := -1
	for index := userIndex + 1; index < len(messages); index++ {
		switch messages[index].Role {
		case contracts.ChatRoleUser:
			return final
		case contracts.ChatRoleAssistant:
			final = index
		}
	}
	return final
}
