package teamnotes_test

// TDD for the human review half of the note board. The rule it encodes: a Lead
// may write facts down freely, so every note is recorded unreviewed, and only a
// human review (accept or reject) records that someone stands behind it. The
// review lives in the note's own Markdown, so it survives a restart, is visible
// in the Memory tab, and needs no storage of its own.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/teamnotes"
)

func TestRecordedNotesAreUnreviewed(t *testing.T) {
	service, memory := newService(t)
	ctx := context.Background()

	recorded, err := service.Record(ctx, contracts.TeamNote{
		TaskID: "T-90", Title: "fact, not yet agreed", Body: "the deploy moved", Author: "ann", At: at(9, 0),
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if recorded.Reviewed || recorded.ReviewedBy != "" || !recorded.ReviewedAt.IsZero() {
		t.Fatalf("Record returned a reviewed note: %+v", recorded)
	}
	content := memory.row(t, recorded.ID).Content
	if strings.Contains(content, "- Reviewed") {
		t.Fatalf("an unreviewed note says it was reviewed:\n%s", content)
	}

	// A caller cannot smuggle a review in through Record: the fields are cleared.
	smuggled, err := service.Record(ctx, contracts.TeamNote{
		TaskID: "T-91", Title: "already approved, honest", Reviewed: true, ReviewedBy: "mallory", ReviewedAt: at(9, 30),
	})
	if err != nil {
		t.Fatalf("Record(smuggled): %v", err)
	}
	if smuggled.Reviewed || smuggled.ReviewedBy != "" || !smuggled.ReviewedAt.IsZero() {
		t.Fatalf("Record honoured caller-supplied review state: %+v", smuggled)
	}
	if got := memory.row(t, smuggled.ID).Content; strings.Contains(got, "mallory") {
		t.Fatalf("caller-supplied reviewer reached storage:\n%s", got)
	}

	notes, err := service.List(ctx, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, note := range notes {
		if note.Reviewed || note.ReviewedBy != "" || !note.ReviewedAt.IsZero() {
			t.Fatalf("listed note %q is reviewed without a review: %+v", note.ID, note)
		}
	}
}

func TestReviewPersistsAcrossAFreshList(t *testing.T) {
	service, memory := newService(t)
	ctx := context.Background()

	recorded, err := service.Record(ctx, contracts.TeamNote{
		TaskID: "T-42",
		Title:  "Deploy window moved",
		Body:   "Move the deploy to 21:00 UTC so the on-call is awake.",
		Author: "ann",
		At:     at(9, 30),
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	reviewed, err := service.Review(ctx, contracts.TeamNoteReview{
		NoteID: recorded.ID, Reviewer: "ross", Accept: true, At: at(10, 15),
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if reviewed.ID != recorded.ID {
		t.Fatalf("Review moved the note: %q -> %q", recorded.ID, reviewed.ID)
	}
	if !reviewed.Reviewed || reviewed.ReviewedBy != "ross" || !reviewed.ReviewedAt.Equal(at(10, 15)) {
		t.Fatalf("reviewed note = %+v, want reviewed by ross at 10:15", reviewed)
	}
	// The note keeps what its author wrote, and its own timestamp.
	if reviewed.Title != recorded.Title || reviewed.Body != recorded.Body || reviewed.Author != "ann" || reviewed.TaskID != "T-42" {
		t.Fatalf("Review rewrote the note: %+v", reviewed)
	}
	if !reviewed.At.Equal(recorded.At) {
		t.Fatalf("Review moved the note's At: %s -> %s", recorded.At, reviewed.At)
	}
	if len(memory.rows) != 1 || len(memory.writes) != 2 {
		t.Fatalf("rows = %d writes = %d, want one row replaced by one review write", len(memory.rows), len(memory.writes))
	}
	content := memory.row(t, recorded.ID).Content
	for _, want := range []string{
		"# Deploy window moved",
		"- Task: T-42",
		"- Author: ann",
		"- At: 2026-11-25T09:30:00Z",
		"- Reviewed: accepted",
		"- ReviewedBy: ross",
		"- ReviewedAt: 2026-11-25T10:15:00Z",
		"Move the deploy to 21:00 UTC so the on-call is awake.",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("reviewed content is missing %q:\n%s", want, content)
		}
	}

	// "Persists" means a store read, not service state: a new Service over the
	// same memory sees the review.
	fresh := teamnotes.New(memory, teamnotes.Config{})
	notes, err := fresh.List(ctx, 10)
	if err != nil {
		t.Fatalf("List(fresh): %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %d, want 1", len(notes))
	}
	listed := notes[0]
	if !listed.Reviewed || listed.ReviewedBy != "ross" || !listed.ReviewedAt.Equal(at(10, 15)) {
		t.Fatalf("listed review state = %+v, want reviewed by ross at 10:15", listed)
	}
	if listed.Body != recorded.Body || listed.Title != recorded.Title || !listed.At.Equal(at(9, 30)) {
		t.Fatalf("listed note lost its content: %+v", listed)
	}
}

func TestReviewIsIdempotent(t *testing.T) {
	service, memory := newService(t)
	ctx := context.Background()

	recorded, err := service.Record(ctx, contracts.TeamNote{
		TaskID: "T-7", Title: "one row only", Body: "body stays", Author: "ann", At: at(9, 0),
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if _, err := service.Review(ctx, contracts.TeamNoteReview{NoteID: recorded.ID, Reviewer: "ross", Accept: true, At: at(10, 0)}); err != nil {
		t.Fatalf("Review(first): %v", err)
	}
	second, err := service.Review(ctx, contracts.TeamNoteReview{NoteID: recorded.ID, Reviewer: "cleo", Accept: true, At: at(11, 0)})
	if err != nil {
		t.Fatalf("Review(second): %v", err)
	}
	if !second.Reviewed || second.ReviewedBy != "cleo" || !second.ReviewedAt.Equal(at(11, 0)) {
		t.Fatalf("second review = %+v, want the updated reviewer and time", second)
	}
	if len(memory.rows) != 1 {
		t.Fatalf("rows = %d, want 1 (a review replaces, never appends)", len(memory.rows))
	}
	notes, err := service.List(ctx, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %d, want 1 (no duplicate row)", len(notes))
	}
	content := memory.row(t, recorded.ID).Content
	if got := strings.Count(content, "- Reviewed:"); got != 1 {
		t.Fatalf("content has %d review lines, want 1:\n%s", got, content)
	}
	for _, stale := range []string{"ross", "10:00:00"} {
		if strings.Contains(content, stale) {
			t.Fatalf("content kept the first review's %q:\n%s", stale, content)
		}
	}
	if !strings.Contains(content, "- ReviewedBy: cleo") || !strings.Contains(content, "- ReviewedAt: 2026-11-25T11:00:00Z") {
		t.Fatalf("content does not carry the second review:\n%s", content)
	}
	if notes[0].Body != "body stays" || notes[0].Title != "one row only" {
		t.Fatalf("reviewing twice changed the note: %+v", notes[0])
	}
}

// TestReviewRejectionSaysSoInTheContent pins the representation: a rejection is
// reviewed=true like an acceptance, and the note itself says "rejected" — in the
// metadata block and at the front of the listed summary.
func TestReviewRejectionSaysSoInTheContent(t *testing.T) {
	service, memory := newService(t)
	ctx := context.Background()

	recorded, err := service.Record(ctx, contracts.TeamNote{
		TaskID: "T-3", Title: "pin the toolchain", Body: "pin Go 1.99 everywhere", Author: "ann", At: at(9, 0),
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	rejected, err := service.Review(ctx, contracts.TeamNoteReview{
		NoteID: recorded.ID, Reviewer: "cleo", Accept: false, At: at(12, 0),
	})
	if err != nil {
		t.Fatalf("Review(reject): %v", err)
	}
	if !rejected.Reviewed || rejected.ReviewedBy != "cleo" || !rejected.ReviewedAt.Equal(at(12, 0)) {
		t.Fatalf("rejected note = %+v, want reviewed by cleo at 12:00", rejected)
	}
	detail := memory.row(t, recorded.ID)
	if !strings.Contains(detail.Content, "- Reviewed: rejected") {
		t.Fatalf("rejected content does not say rejected:\n%s", detail.Content)
	}
	if strings.Contains(detail.Content, "accepted") {
		t.Fatalf("rejected content also says accepted:\n%s", detail.Content)
	}
	if !strings.HasPrefix(detail.Summary, "[rejected] ") {
		t.Fatalf("summary = %q, want the rejection prefix", detail.Summary)
	}
	if len(detail.Summary) > 512 {
		t.Fatalf("summary is %d bytes, want at most 512 (memoryvfs.MaxSummaryBytes)", len(detail.Summary))
	}
	if detail.Title != "pin the toolchain" || !strings.Contains(detail.Content, "pin Go 1.99 everywhere") {
		t.Fatalf("rejection lost the note's title or body:\n%s", detail.Content)
	}

	// A later acceptance replaces the rejection: one note, one status.
	accepted, err := service.Review(ctx, contracts.TeamNoteReview{
		NoteID: recorded.ID, Reviewer: "ross", Accept: true, At: at(13, 0),
	})
	if err != nil {
		t.Fatalf("Review(accept): %v", err)
	}
	if !accepted.Reviewed || accepted.ReviewedBy != "ross" {
		t.Fatalf("acceptance after rejection = %+v", accepted)
	}
	detail = memory.row(t, recorded.ID)
	if !strings.Contains(detail.Content, "- Reviewed: accepted") || strings.Contains(detail.Content, "rejected") {
		t.Fatalf("acceptance did not replace the rejection:\n%s", detail.Content)
	}
	if strings.HasPrefix(detail.Summary, "[rejected] ") {
		t.Fatalf("summary still says rejected: %q", detail.Summary)
	}
	if len(memory.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(memory.rows))
	}
}

func TestReviewUnknownNoteIsTyped(t *testing.T) {
	service, memory := newService(t)
	ctx := context.Background()

	// A row exists outside the notes namespace: it is a real memory, but not a
	// note, and reviewing it must not rewrite it.
	memory.seed(contracts.MemoryItem{
		URI:     "mem://user/self/memories/preferences/some-pref",
		Type:    "preferences",
		Title:   "not a note",
		Updated: at(8, 0),
	}, "not a note", false)
	// A note URI carrying another memory type: same answer.
	memory.seed(contracts.MemoryItem{
		URI:     teamnotes.URIPrefix + "wrong-type",
		Type:    "preferences",
		Title:   "type mismatch",
		Updated: at(8, 5),
	}, "# type mismatch\n\n- Task: x\n- Author: y\n- At: 2026-11-25T08:05:00Z", false)
	// A heading the memory store would refuse to store again (200 runes, but
	// 400 bytes): reviewing it must say so in the board's own terms.
	memory.seed(contracts.MemoryItem{
		URI:     teamnotes.URIPrefix + "wide-title",
		Type:    teamnotes.NoteType,
		Updated: at(8, 10),
	}, "# "+strings.Repeat("\u00e9", teamnotes.MaxTitleRunes)+"\n\nbody", false)

	cases := []struct {
		name   string
		review contracts.TeamNoteReview
		want   error
	}{
		{name: "missing note", review: contracts.TeamNoteReview{NoteID: teamnotes.URIPrefix + "nope", Reviewer: "ross", Accept: true}, want: contracts.ErrTeamNoteUnknown},
		{name: "outside the namespace", review: contracts.TeamNoteReview{NoteID: "mem://user/self/memories/preferences/some-pref", Reviewer: "ross", Accept: true}, want: contracts.ErrTeamNoteUnknown},
		{name: "wrong memory type", review: contracts.TeamNoteReview{NoteID: teamnotes.URIPrefix + "wrong-type", Reviewer: "ross", Accept: true}, want: contracts.ErrTeamNoteUnknown},
		{name: "namespace root", review: contracts.TeamNoteReview{NoteID: strings.TrimSuffix(teamnotes.URIPrefix, "/"), Reviewer: "ross", Accept: true}, want: contracts.ErrTeamNoteUnknown},
		{name: "empty id", review: contracts.TeamNoteReview{NoteID: "  ", Reviewer: "ross", Accept: true}, want: contracts.ErrTeamNoteInvalid},
		{name: "empty reviewer", review: contracts.TeamNoteReview{NoteID: teamnotes.URIPrefix + "nope", Accept: true}, want: contracts.ErrTeamNoteInvalid},
		{name: "reviewer too long", review: contracts.TeamNoteReview{NoteID: teamnotes.URIPrefix + "nope", Reviewer: strings.Repeat("a", teamnotes.MaxAuthorRunes+1), Accept: true}, want: contracts.ErrTeamNoteInvalid},
		{name: "title over the store byte cap", review: contracts.TeamNoteReview{NoteID: teamnotes.URIPrefix + "wide-title", Reviewer: "ross", Accept: true}, want: contracts.ErrTeamNoteInvalid},
	}
	before := len(memory.writes)
	for _, testCase := range cases {
		if _, err := service.Review(ctx, testCase.review); !errors.Is(err, testCase.want) {
			t.Fatalf("%s: Review = %v, want %v", testCase.name, err, testCase.want)
		}
	}
	if len(memory.writes) != before {
		t.Fatalf("a failed review wrote through to memory (%d -> %d writes)", before, len(memory.writes))
	}

	// A broken (unreadable) note cannot be reviewed either: its body could not
	// be preserved, so pretending to review it would destroy the note.
	memory.seed(contracts.MemoryItem{
		URI: teamnotes.URIPrefix + "unreadable", Type: teamnotes.NoteType, Title: "gone", Updated: at(9, 0),
	}, "", true)
	if _, err := service.Review(ctx, contracts.TeamNoteReview{NoteID: teamnotes.URIPrefix + "unreadable", Reviewer: "ross", Accept: true}); !errors.Is(err, contracts.ErrTeamNoteUnknown) {
		t.Fatalf("Review(unreadable) = %v, want ErrTeamNoteUnknown", err)
	}

	// Without a memory store the answer is "unavailable", not "unknown".
	offline := teamnotes.New(nil, teamnotes.Config{})
	if _, err := offline.Review(ctx, contracts.TeamNoteReview{NoteID: teamnotes.URIPrefix + "x", Reviewer: "ross"}); !errors.Is(err, contracts.ErrTeamNotesUnavailable) {
		t.Fatalf("Review without memory = %v, want ErrTeamNotesUnavailable", err)
	}
	var nilService *teamnotes.Service
	if _, err := nilService.Review(ctx, contracts.TeamNoteReview{NoteID: teamnotes.URIPrefix + "x", Reviewer: "ross"}); !errors.Is(err, contracts.ErrTeamNotesUnavailable) {
		t.Fatalf("nil Review = %v, want ErrTeamNotesUnavailable", err)
	}
}

func TestListKeepsPartialRowsWithReviewState(t *testing.T) {
	service, memory := newService(t)
	ctx := context.Background()

	// A reviewed note written by another tool: no status line, only the
	// attribution it knew about. It still counts as reviewed.
	memory.seed(contracts.MemoryItem{
		URI:     teamnotes.URIPrefix + "half-review",
		Type:    teamnotes.NoteType,
		Title:   "Half a review",
		Summary: "written elsewhere",
		Updated: at(9, 0),
	}, "# Half a review\n\n- Task: B-2\n- Author: ann\n- At: 2026-11-25T08:00:00Z\n- ReviewedBy: dan\n- ReviewedAt: 2026-11-25T09:00:00Z\n\nbody kept anyway", false)
	// An unreviewed note with an unrecognised status token: not reviewed, and
	// the rest of the header still parses.
	memory.seed(contracts.MemoryItem{
		URI:     teamnotes.URIPrefix + "odd-status",
		Type:    teamnotes.NoteType,
		Title:   "Odd status",
		Updated: at(8, 0),
	}, "# Odd status\n\n- Task: B-3\n- Author: bea\n- At: 2026-11-25T07:00:00Z\n- Reviewed: maybe\n\nbody", false)
	// A row whose content cannot be read, and one whose content is foreign: both
	// must still list, unreviewed, with whatever the row itself carries.
	memory.seed(contracts.MemoryItem{
		URI: teamnotes.URIPrefix + "gone", Type: teamnotes.NoteType, Title: "Gone", Updated: at(7, 30),
	}, "", true)
	memory.seed(contracts.MemoryItem{
		URI: teamnotes.URIPrefix + "foreign", Type: teamnotes.NoteType, Title: "Foreign", Updated: at(7, 0),
	}, "plain text from the CLI, no heading and no metadata", false)

	notes, err := service.List(ctx, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(notes) != 4 {
		t.Fatalf("notes = %d, want 4 partial rows", len(notes))
	}
	byID := make(map[string]contracts.TeamNote, len(notes))
	for _, note := range notes {
		byID[note.ID] = note
	}

	half := byID[teamnotes.URIPrefix+"half-review"]
	if !half.Reviewed || half.ReviewedBy != "dan" || !half.ReviewedAt.Equal(at(9, 0)) {
		t.Fatalf("half review = %+v, want reviewed by dan at 09:00", half)
	}
	if half.Title != "Half a review" || half.TaskID != "B-2" || half.Author != "ann" {
		t.Fatalf("half review lost its header: %+v", half)
	}
	if !strings.Contains(half.Body, "body kept anyway") {
		t.Fatalf("half review lost its body: %q", half.Body)
	}

	odd := byID[teamnotes.URIPrefix+"odd-status"]
	if odd.Reviewed || odd.ReviewedBy != "" || !odd.ReviewedAt.IsZero() {
		t.Fatalf("unrecognised status counted as a review: %+v", odd)
	}
	if odd.TaskID != "B-3" || odd.Author != "bea" {
		t.Fatalf("odd status note lost its header: %+v", odd)
	}

	gone := byID[teamnotes.URIPrefix+"gone"]
	if gone.Title != "Gone" || gone.Reviewed || gone.Body != "" {
		t.Fatalf("unreadable row = %+v, want its row fields and no review", gone)
	}

	foreign := byID[teamnotes.URIPrefix+"foreign"]
	if foreign.Title != "Foreign" || !strings.Contains(foreign.Body, "plain text from the CLI") {
		t.Fatalf("foreign row = %+v, want its own title and the raw content", foreign)
	}

	// A partial row is still reviewable: the review adds review lines and keeps
	// the body it found.
	reviewed, err := service.Review(ctx, contracts.TeamNoteReview{
		NoteID: teamnotes.URIPrefix + "foreign", Reviewer: "ross", Accept: true, At: at(10, 0),
	})
	if err != nil {
		t.Fatalf("Review(foreign): %v", err)
	}
	if !reviewed.Reviewed || reviewed.Body != "plain text from the CLI, no heading and no metadata" {
		t.Fatalf("reviewed foreign note = %+v, want the body kept", reviewed)
	}
}

func TestReRecordClearsAnEarlierReview(t *testing.T) {
	service, memory := newService(t)
	ctx := context.Background()

	if _, err := service.Record(ctx, contracts.TeamNote{TaskID: "T-5", Title: "first", Body: "before", Author: "ann", At: at(9, 0)}); err != nil {
		t.Fatalf("Record(first): %v", err)
	}
	reviewed, err := service.Review(ctx, contracts.TeamNoteReview{NoteID: teamnotes.URIPrefix + "t-5", Reviewer: "ross", Accept: true, At: at(10, 0)})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !reviewed.Reviewed {
		t.Fatalf("note is not reviewed after Review: %+v", reviewed)
	}

	// Re-recording the same task replaces the content, so the old approval no
	// longer describes what is stored: the note goes back to unreviewed.
	if _, err := service.Record(ctx, contracts.TeamNote{TaskID: "T-5", Title: "first", Body: "after", Author: "ann", At: at(11, 0)}); err != nil {
		t.Fatalf("Record(again): %v", err)
	}
	notes, err := service.List(ctx, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %d, want 1", len(notes))
	}
	if notes[0].Reviewed || notes[0].ReviewedBy != "" || !notes[0].ReviewedAt.IsZero() {
		t.Fatalf("re-recording kept the old review: %+v", notes[0])
	}
	content := memory.row(t, notes[0].ID).Content
	if strings.Contains(content, "- Reviewed") {
		t.Fatalf("re-recorded content still carries the review:\n%s", content)
	}
	if !strings.Contains(content, "after") {
		t.Fatalf("re-recorded content is stale:\n%s", content)
	}
}

// TestReviewKeepsABodyThatLooksLikeMetadata guards the round-trip: the body is
// never re-interpreted as the note's header, so a body whose first line is
// "- Reviewed: rejected" stays body across reviews.
func TestReviewKeepsABodyThatLooksLikeMetadata(t *testing.T) {
	service, memory := newService(t)
	ctx := context.Background()

	body := strings.Join([]string{
		"- Reviewed: rejected",
		"- ReviewedBy: mallory",
		"- At: 1999-01-01T00:00:00Z",
		"# not a heading",
	}, "\n")
	recorded, err := service.Record(ctx, contracts.TeamNote{TaskID: "T-8", Title: "looks like metadata", Body: body, Author: "ann", At: at(9, 0)})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	first, err := service.Review(ctx, contracts.TeamNoteReview{NoteID: recorded.ID, Reviewer: "ross", Accept: true, At: at(10, 0)})
	if err != nil {
		t.Fatalf("Review(first): %v", err)
	}
	if first.Body != body {
		t.Fatalf("review rewrote the body:\n%q\nwant\n%q", first.Body, body)
	}
	second, err := service.Review(ctx, contracts.TeamNoteReview{NoteID: recorded.ID, Reviewer: "ross", Accept: true, At: at(10, 0)})
	if err != nil {
		t.Fatalf("Review(second): %v", err)
	}
	if second.Body != body {
		t.Fatalf("second review rewrote the body:\n%q\nwant\n%q", second.Body, body)
	}
	if !second.Reviewed || second.ReviewedBy != "ross" || !second.ReviewedAt.Equal(at(10, 0)) {
		t.Fatalf("second review = %+v", second)
	}
	// Reviewing twice with the same reviewer and time is a byte-for-byte
	// rewrite: same row content, one row.
	if got := strings.Count(memory.row(t, recorded.ID).Content, "- Reviewed: accepted"); got != 1 {
		t.Fatalf("content has %d accepted lines, want 1", got)
	}
	if len(memory.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(memory.rows))
	}
}
