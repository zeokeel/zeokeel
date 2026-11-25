package contracts

// Team note suggestions: the queue M1 asks for.
//
// Today a note is written and then reviewed, so an unreviewed note is already on
// the board. That is review after the fact. A suggestion is different: a bot
// proposes a rule and NOTHING reaches the shared board until a human promotes it,
// which is what "团队建议经你批准后另一队友可见" means.
//
// It is its own service key, like held sends, so a consumer that only queues
// suggestions does not have to implement the whole note board.

import (
	"context"
	"errors"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// TeamNoteSuggestion is one proposed team rule waiting for a human.
type TeamNoteSuggestion struct {
	// ID identifies the suggestion (not the eventual note).
	ID string
	// Title is the one-line rule (required).
	Title string
	// Body is the Markdown explanation (may be empty).
	Body string
	// Author names the bot that proposed it.
	Author string
	// At is when it was proposed.
	At time.Time
}

// ErrTeamNoteSuggestionUnknown means no suggestion has that id.
var ErrTeamNoteSuggestionUnknown = errors.New("team note suggestions: unknown suggestion")

// TeamNoteSuggestions is the proposal queue in front of the shared board.
type TeamNoteSuggestions interface {
	// Suggest queues a proposal. It never touches the board.
	Suggest(ctx context.Context, suggestion TeamNoteSuggestion) (TeamNoteSuggestion, error)
	// Suggestions lists queued proposals, newest first.
	Suggestions(ctx context.Context, limit int) ([]TeamNoteSuggestion, error)
	// Promote turns one proposal into a real board note (via the note board, so
	// the note carries who promoted it) and removes it from the queue.
	Promote(ctx context.Context, id, reviewer string) (TeamNote, error)
	// Dismiss removes a proposal without creating a note.
	Dismiss(ctx context.Context, id, reviewer string) error
}

// KeyTeamNoteSuggestions is provided by the team notes module.
var KeyTeamNoteSuggestions = cordis.NewServiceKey[TeamNoteSuggestions]("zeokeel/teamnotes/suggestions")
