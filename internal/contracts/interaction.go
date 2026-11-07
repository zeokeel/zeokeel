package contracts

import (
	"context"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// UserQuestion is one blocking question an agent host tool asks the user (the
// old shell's ask_user interaction).
type UserQuestion struct {
	// ID identifies the pending question (Answer/Cancel use it).
	ID string
	// Text is the question body.
	Text string
	// Options are the selectable answers; empty means free text.
	Options []string
}

// UserInteraction bridges agent-side questions to the desktop UI: the process
// host provides it, a UI module registers the renderer/answer handler, and the
// agent's ask_user tool parks on Ask until the user answers.
type UserInteraction interface {
	// SetHandler installs the question handler (nil clears it).
	SetHandler(fn func(UserQuestion))
	// Ask blocks until the user answers or cancels the question.
	Ask(ctx context.Context, text string, options []string) (string, error)
	// Answer resolves the pending question id with the chosen answer.
	Answer(id, answer string)
	// Cancel dismisses the pending question id with an error.
	Cancel(id string)
}

// KeyUserInteraction is provided by the desktop host.
var KeyUserInteraction = cordis.NewServiceKey[UserInteraction]("zeokeel/interaction")
