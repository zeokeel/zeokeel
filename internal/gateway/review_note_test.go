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

type fakeDispatchRecorder struct {
	mu       sync.Mutex
	requests []string
	err      error
}

func (f *fakeDispatchRecorder) Deliver(_ context.Context, category, text string) (contracts.DispatchReceipt, error) {
	if f.err != nil {
		return contracts.DispatchReceipt{}, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, category+"|"+text)
	return contracts.DispatchReceipt{Channel: contracts.NotifyChannelInApp}, nil
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
func (f *fakeLease) SetTools(_ context.Context, taskID string, tools []string) (contracts.TaskTools, error) {
	return contracts.TaskTools{TaskID: taskID, Tools: tools}, nil
}
func (f *fakeLease) Tools(context.Context, string) (contracts.TaskTools, bool, error) {
	return contracts.TaskTools{}, false, nil
}
func (f *fakeLease) SetApproval(context.Context, string, string) (string, error) {
	return contracts.TaskApprovalNone, nil
}
func (f *fakeLease) Approval(context.Context, string) (string, bool, error) {
	return contracts.TaskApprovalNone, false, nil
}
func (f *fakeLease) Approve(context.Context, string, string) (contracts.TaskLease, error) {
	return contracts.TaskLease{}, nil
}
func (f *fakeLease) RejectStart(context.Context, string, string, string) error { return nil }
func (f *fakeLease) SetBudget(context.Context, string, contracts.TaskBudget) (contracts.TaskBudget, error) {
	return contracts.TaskBudget{}, nil
}
func (f *fakeLease) Budget(context.Context, string) (contracts.TaskBudget, bool, error) {
	return contracts.TaskBudget{}, false, nil
}
func (f *fakeLease) Consume(context.Context, string, int) (contracts.TaskBudget, error) {
	return contracts.TaskBudget{}, nil
}

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

func TestAcceptNotifiesTheHuman(t *testing.T) {
	dispatcher := &fakeDispatchRecorder{}
	gw := newReviewGateway(t, &fakeLease{}, nil)
	if _, err := gw.root.Provide(contracts.KeyChannelDispatch, contracts.ChannelDispatchService(dispatcher)); err != nil {
		t.Fatalf("provide dispatch: %v", err)
	}

	if problem := review(t, gw, map[string]any{
		"id": "t1", "decision": "accept", "reviewer": "lead", "note": "verified",
	}); problem != nil {
		t.Fatalf("review failed: %+v", problem)
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if len(dispatcher.requests) != 1 {
		t.Fatalf("dispatches = %v, want one", dispatcher.requests)
	}
	if !stringsHas(dispatcher.requests[0], contracts.NotifyCategoryTaskDone) {
		t.Fatalf("category = %q, want the task_done route", dispatcher.requests[0])
	}
}

func TestRejectNotifiesWithTheFailureCategory(t *testing.T) {
	dispatcher := &fakeDispatchRecorder{}
	gw := newReviewGateway(t, &fakeLease{}, nil)
	if _, err := gw.root.Provide(contracts.KeyChannelDispatch, contracts.ChannelDispatchService(dispatcher)); err != nil {
		t.Fatalf("provide dispatch: %v", err)
	}

	if problem := review(t, gw, map[string]any{
		"id": "t1", "decision": "reject", "reviewer": "lead", "note": "证据不够",
	}); problem != nil {
		t.Fatalf("review failed: %+v", problem)
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if len(dispatcher.requests) != 1 || !stringsHas(dispatcher.requests[0], contracts.NotifyCategoryTaskFailed) {
		t.Fatalf("dispatches = %v, want one task_failed route", dispatcher.requests)
	}
}

func TestNotificationFailureDoesNotUndoTheReview(t *testing.T) {
	dispatcher := &fakeDispatchRecorder{err: contracts.ErrNotifyRouteInvalid}
	gw := newReviewGateway(t, &fakeLease{}, nil)
	if _, err := gw.root.Provide(contracts.KeyChannelDispatch, contracts.ChannelDispatchService(dispatcher)); err != nil {
		t.Fatalf("provide dispatch: %v", err)
	}
	if problem := review(t, gw, map[string]any{
		"id": "t1", "decision": "accept", "reviewer": "lead", "note": "ok",
	}); problem != nil {
		t.Fatalf("a notification failure failed the review: %+v", problem)
	}
}

func stringsHas(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

type fakeSkillDrafts struct {
	mu     sync.Mutex
	drafts []contracts.TaskSkillDraft
	err    error
}

func (f *fakeSkillDrafts) Draft(_ context.Context, draft contracts.TaskSkillDraft) (contracts.TaskSkillDraft, error) {
	if f.err != nil {
		return contracts.TaskSkillDraft{}, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	draft.ID = "draft-" + draft.Name
	draft.Status = contracts.SkillDraftPending
	f.drafts = append(f.drafts, draft)
	return draft, nil
}

func (f *fakeSkillDrafts) List(context.Context, int) ([]contracts.TaskSkillDraft, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.TaskSkillDraft(nil), f.drafts...), nil
}

func (f *fakeSkillDrafts) Decide(_ context.Context, id string, accept bool, reviewer string) (contracts.TaskSkillDraft, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.drafts {
		if f.drafts[i].ID == id {
			if accept {
				f.drafts[i].Status = contracts.SkillDraftAccepted
			} else {
				f.drafts[i].Status = contracts.SkillDraftRejected
			}
			f.drafts[i].Reviewer = reviewer
			return f.drafts[i], nil
		}
	}
	return contracts.TaskSkillDraft{}, contracts.ErrSkillDraftUnknown
}

func TestAcceptProposesASkillDraft(t *testing.T) {
	drafts := &fakeSkillDrafts{}
	gw := newReviewGateway(t, &fakeLease{}, nil)
	if _, err := gw.root.Provide(contracts.KeySkillDrafts, contracts.SkillDraftsService(drafts)); err != nil {
		t.Fatalf("provide drafts: %v", err)
	}

	if problem := review(t, gw, map[string]any{
		"id": "t1", "decision": "accept", "reviewer": "lead", "note": "verified",
	}); problem != nil {
		t.Fatalf("review failed: %+v", problem)
	}
	drafts.mu.Lock()
	defer drafts.mu.Unlock()
	if len(drafts.drafts) != 1 {
		t.Fatalf("drafts = %+v, want one proposal", drafts.drafts)
	}
	if drafts.drafts[0].Status != contracts.SkillDraftPending || drafts.drafts[0].TaskID != "t1" {
		t.Fatalf("draft = %+v, want a pending proposal tied to the task", drafts.drafts[0])
	}
}

func TestRejectDoesNotProposeASkill(t *testing.T) {
	drafts := &fakeSkillDrafts{}
	gw := newReviewGateway(t, &fakeLease{}, nil)
	if _, err := gw.root.Provide(contracts.KeySkillDrafts, contracts.SkillDraftsService(drafts)); err != nil {
		t.Fatalf("provide drafts: %v", err)
	}
	if problem := review(t, gw, map[string]any{
		"id": "t1", "decision": "reject", "reviewer": "lead", "note": "no",
	}); problem != nil {
		t.Fatalf("review failed: %+v", problem)
	}
	drafts.mu.Lock()
	defer drafts.mu.Unlock()
	if len(drafts.drafts) != 0 {
		t.Fatalf("a rejected task proposed a skill: %+v", drafts.drafts)
	}
}

func TestSkillProposalFailureDoesNotUndoTheReview(t *testing.T) {
	drafts := &fakeSkillDrafts{err: contracts.ErrSkillDraftInvalid}
	gw := newReviewGateway(t, &fakeLease{}, nil)
	if _, err := gw.root.Provide(contracts.KeySkillDrafts, contracts.SkillDraftsService(drafts)); err != nil {
		t.Fatalf("provide drafts: %v", err)
	}
	if problem := review(t, gw, map[string]any{
		"id": "t1", "decision": "accept", "reviewer": "lead", "note": "ok",
	}); problem != nil {
		t.Fatalf("a skill proposal failure failed the review: %+v", problem)
	}
}

func TestSkillNameProjectionRefusesHopelessIDs(t *testing.T) {
	cases := map[string]string{
		"task-1":        "task-1",
		"Task 42 / ops": "task-42-ops",
		"":              "",
		"!!!":           "",
	}
	for input, want := range cases {
		if got := skillNameFromTaskID(input); got != want {
			t.Errorf("skillNameFromTaskID(%q) = %q, want %q", input, got, want)
		}
	}
}
