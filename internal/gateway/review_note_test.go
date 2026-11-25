package gateway

// Accepting a task is the one moment work becomes team memory. The note must be
// written on accept and not on reject, and a memory failure must never undo a
// review the human already made.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakeTeamNotes struct {
	mu    sync.Mutex
	notes []contracts.TeamNote
	err   error
}

func (f *fakeTeamNotes) Record(_ context.Context, note contracts.TeamNote) (contracts.TeamNote, error) {
	if f.err != nil {
		return contracts.TeamNote{}, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notes = append(f.notes, note)
	return note, nil
}

func (f *fakeTeamNotes) Review(_ context.Context, review contracts.TeamNoteReview) (contracts.TeamNote, error) {
	if f.err != nil {
		return contracts.TeamNote{}, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, note := range f.notes {
		if note.ID != "" && note.ID == review.NoteID {
			note.Reviewed, note.ReviewedBy, note.ReviewedAt = true, review.Reviewer, review.At
			f.notes[i] = note
			return note, nil
		}
	}
	return contracts.TeamNote{}, contracts.ErrTeamNoteUnknown
}

func (f *fakeTeamNotes) List(context.Context, int) ([]contracts.TeamNote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.TeamNote(nil), f.notes...), nil
}

type fakeLease struct {
	state   string
	applied []string
}

func (f *fakeLease) Claim(context.Context, string, string) (contracts.TaskLease, error) {
	return contracts.TaskLease{TaskID: "t1", Owner: "bot-1", State: contracts.LeaseStateClaimed}, nil
}
func (f *fakeLease) Heartbeat(context.Context, string, string) (contracts.TaskLease, error) {
	return contracts.TaskLease{}, nil
}
func (f *fakeLease) Release(context.Context, string, string) error { return nil }
func (f *fakeLease) Submit(context.Context, string, string, []contracts.Evidence) (contracts.TaskLease, error) {
	return contracts.TaskLease{}, nil
}
func (f *fakeLease) Accept(_ context.Context, taskID, reviewer, note string) (contracts.TaskLease, error) {
	f.applied = append(f.applied, "accept:"+taskID)
	return contracts.TaskLease{
		TaskID: taskID, Owner: "bot-1", State: contracts.LeaseStateDone, ReviewNote: note,
		Evidence: []contracts.Evidence{{Kind: "link", Ref: "gh://12"}},
	}, nil
}
func (f *fakeLease) Reject(_ context.Context, taskID, reviewer, note string) (contracts.TaskLease, error) {
	f.applied = append(f.applied, "reject:"+taskID)
	return contracts.TaskLease{TaskID: taskID, State: contracts.LeaseStateRejected, ReviewNote: note}, nil
}
func (f *fakeLease) Lease(context.Context, string) (contracts.TaskLease, bool, error) {
	return contracts.TaskLease{}, false, nil
}
func (f *fakeLease) Expire(context.Context) ([]string, error) { return nil, nil }

func newReviewGateway(t *testing.T, lease contracts.TaskLeaseService, notes contracts.TeamNotesService) *Gateway {
	t.Helper()
	root := testRoot(t, newFakeChat())
	if _, err := root.Provide(contracts.KeyTaskLease, lease); err != nil {
		t.Fatalf("provide lease: %v", err)
	}
	if notes != nil {
		if _, err := root.Provide(contracts.KeyTeamNotes, notes); err != nil {
			t.Fatalf("provide notes: %v", err)
		}
	}
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return gateway
}

func review(t *testing.T, gw *Gateway, params map[string]any) *protocolError {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, problem := gw.taskReview(context.Background(), raw)
	return problem
}

func TestAcceptRecordsATeamNote(t *testing.T) {
	notes := &fakeTeamNotes{}
	gw := newReviewGateway(t, &fakeLease{}, notes)

	if problem := review(t, gw, map[string]any{
		"id": "t1", "decision": "accept", "reviewer": "lead", "note": "验证过退款率",
	}); problem != nil {
		t.Fatalf("review failed: %+v", problem)
	}
	notes.mu.Lock()
	defer notes.mu.Unlock()
	if len(notes.notes) != 1 {
		t.Fatalf("notes = %+v, want one", notes.notes)
	}
	note := notes.notes[0]
	if note.TaskID != "t1" || note.Author != "lead" {
		t.Fatalf("note = %+v", note)
	}
	if !containsAll(note.Body, "gh://12", "验证过退款率") {
		t.Fatalf("note body = %q, want the evidence and the review note", note.Body)
	}
}

func TestRejectDoesNotWriteANote(t *testing.T) {
	notes := &fakeTeamNotes{}
	gw := newReviewGateway(t, &fakeLease{}, notes)

	if problem := review(t, gw, map[string]any{
		"id": "t1", "decision": "reject", "reviewer": "lead", "note": "证据不够",
	}); problem != nil {
		t.Fatalf("review failed: %+v", problem)
	}
	notes.mu.Lock()
	defer notes.mu.Unlock()
	if len(notes.notes) != 0 {
		t.Fatalf("a rejected task wrote a memory note: %+v", notes.notes)
	}
}

func TestMemoryFailureDoesNotUndoTheReview(t *testing.T) {
	notes := &fakeTeamNotes{err: contracts.ErrTeamNotesUnavailable}
	gw := newReviewGateway(t, &fakeLease{}, notes)

	if problem := review(t, gw, map[string]any{
		"id": "t1", "decision": "accept", "reviewer": "lead", "note": "ok",
	}); problem != nil {
		t.Fatalf("accept failed because memory failed: %+v", problem)
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if !stringsContains(haystack, needle) {
			return false
		}
	}
	return true
}

func stringsContains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

var _ = time.Now
