package contracts

import (
	"context"
	"errors"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// TeamNote is one shared note a team member leaves for the others. ID is the
// memory URI the note lives under (mem://user/self/memories/team-note/<id>), so
// it is stable across restarts and readable in the session Memory tab.
type TeamNote struct {
	// ID is the memory URI (mem://user/self/memories/team-note/<id>); Record
	// fills it in.
	ID string
	// TaskID ties the note to a task; empty means a free-standing note.
	TaskID string
	// Title is the one-line subject (required).
	Title string
	// Body is the Markdown body (may be empty).
	Body string
	// Author names the writer; empty means unattributed.
	Author string
	// At is when the note was recorded (zero means "now" on Record).
	At time.Time
}

// TeamNotesService is the team's shared note board over the memory store. It is
// deliberately tiny: notes are append/replace records, not a task system. Notes
// are stored as memories of type "team-note", so they show up in the session
// Memory tab and in memory search like any other memory.
type TeamNotesService interface {
	// Record stores one note and returns it with ID and At filled in. Recording
	// the same task twice replaces that task's note (the URI is deterministic).
	Record(ctx context.Context, note TeamNote) (TeamNote, error)
	// List returns the newest notes first, at most limit of them (limit <= 0
	// means "the store's own default").
	List(ctx context.Context, limit int) ([]TeamNote, error)
}

// Team note errors are typed so callers can distinguish "you wrote a bad note"
// (fix the input) from "this product has no memory store" (nothing to fix).
var (
	// ErrTeamNoteInvalid means the note failed validation (title/body/author).
	ErrTeamNoteInvalid = errors.New("teamnotes: invalid note")
	// ErrTeamNotesUnavailable means the product has no memory service, so added
	// notes could not be stored.
	ErrTeamNotesUnavailable = errors.New("teamnotes: memory service unavailable")
)

// KeyTeamNotes is provided by the teamnotes module.
var KeyTeamNotes = cordis.NewServiceKey[TeamNotesService]("zeokeel/teamnotes")
