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

	// Reviewed reports whether a human has reviewed the note. Notes are always
	// recorded unreviewed: a fact may be written down as-is, and only a review
	// turns it into something the team treats as agreed. Record clears these
	// three fields, so re-recording a note sends it back to unreviewed.
	Reviewed bool
	// ReviewedBy names the human who reviewed it (empty when Reviewed is
	// false).
	ReviewedBy string
	// ReviewedAt is when the review happened (zero when Reviewed is false). It
	// is not At: a note keeps the time it was recorded.
	ReviewedAt time.Time

	// Scope decides who may read the note: NoteScopeTeam (the default) is the
	// shared board every bot and human reads; NoteScopeAgent is private to the
	// bot named in Owner. The store enforces it by writing agent notes under a
	// different memory type, so a team listing cannot see them by accident.
	Scope string
	// Owner is the bot an agent-scoped note belongs to (required when Scope is
	// agent; ignored otherwise).
	Owner string
}

// TeamNoteReview is one human review of a stored note. Accepting a note is the
// only way a note becomes "agreed"; rejecting it keeps the note (and its
// content) but records who rejected it and when.
type TeamNoteReview struct {
	// NoteID is the URI of the note being reviewed (TeamNote.ID).
	NoteID string
	// Reviewer names the human who reviewed it (required).
	Reviewer string
	// Accept records agreement. False records a rejection, which is written
	// into the note itself — the stored metadata block says
	// "- Reviewed: rejected" and the note's summary is prefixed with
	// "[rejected]", so the rejection is visible wherever the note is shown.
	Accept bool
	// At is when the review happened (zero means "now").
	At time.Time
}

// Note scopes.
const (
	// NoteScopeTeam is the shared board (the default).
	NoteScopeTeam = "team"
	// NoteScopeAgent is private to one bot.
	NoteScopeAgent = "agent"
	// NoteScopeUser is private to one human (M6): the same bot can serve two
	// people, and what one taught it must not be visible to the other.
	NoteScopeUser = "user"
)

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
	// Review marks one stored note as reviewed by a human and returns it.
	//
	// It rewrites the note in place (same URI), so reviewing twice updates the
	// reviewer and the time instead of piling up rows. A note keeps its title
	// and body; Review only adds the review fields. An id that is not a stored
	// note reports ErrTeamNoteUnknown.
	Review(ctx context.Context, review TeamNoteReview) (TeamNote, error)
}

// Team note errors are typed so callers can distinguish "you wrote a bad note"
// (fix the input) from "this product has no memory store" (nothing to fix).
var (
	// ErrTeamNoteInvalid means the note or review failed validation
	// (title/body/author, or the reviewer on a review).
	ErrTeamNoteInvalid = errors.New("teamnotes: invalid note")
	// ErrTeamNotesUnavailable means the product has no memory service, so added
	// notes could not be stored.
	ErrTeamNotesUnavailable = errors.New("teamnotes: memory service unavailable")
	// ErrTeamNoteUnknown means the id passed to Review names no reviewable note:
	// it is outside the notes namespace, it holds another kind of memory, or it
	// can no longer be read (so its body could not be preserved). It is separate
	// from ErrTeamNoteInvalid so a caller can tell "check your input" from
	// "that note does not exist".
	ErrTeamNoteUnknown = errors.New("teamnotes: note not found")
)

// KeyTeamNotes is provided by the teamnotes module.
var KeyTeamNotes = cordis.NewServiceKey[TeamNotesService]("zeokeel/teamnotes")
