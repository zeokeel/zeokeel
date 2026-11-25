package skilldraft

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// clock is the injected "now" for these tests: drafts are ordered by CreatedAt,
// so a frozen clock that steps by whole seconds makes "newest first" a real
// assertion instead of a race with the wall clock.
type clock struct{ at time.Time }

func newClock() *clock {
	return &clock{at: time.Date(2026, time.November, 25, 10, 0, 0, 0, time.UTC)}
}

func (c *clock) now() time.Time { return c.at }

func (c *clock) advance(d time.Duration) { c.at = c.at.Add(d) }

// openStore opens a store in dir with a pinned clock.
func openStore(t *testing.T, dir string, clock *clock) *Service {
	t.Helper()
	service, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(%q): %v", dir, err)
	}
	if clock != nil {
		service.now = clock.now
	}
	return service
}

// proposal is a valid draft: one section for the tests to bend.
func proposal(name string) contracts.TaskSkillDraft {
	return contracts.TaskSkillDraft{
		TaskID:  "task-42",
		Name:    name,
		Summary: "Use when the release notes need writing.",
		Body:    "Run the release script.\nThen read the changelog.",
	}
}

func draft(t *testing.T, service *Service, name string) contracts.TaskSkillDraft {
	t.Helper()
	created, err := service.Draft(context.Background(), proposal(name))
	if err != nil {
		t.Fatalf("Draft(%q): %v", name, err)
	}
	return created
}

// TestDraftPersistsAndListsNewestFirst is the round trip the completion card
// depends on: the draft a task proposed is still there after a restart, and the
// list puts the most recent proposal first.
func TestDraftPersistsAndListsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	clock := newClock()
	service := openStore(t, dir, clock)
	ctx := context.Background()

	oldest := draft(t, service, "alpha-skill")
	clock.advance(time.Second)
	middle := draft(t, service, "beta-skill")
	clock.advance(time.Second)
	newest := draft(t, service, "gamma-skill")

	if oldest.Status != contracts.SkillDraftPending {
		t.Fatalf("new draft status = %q, want pending", oldest.Status)
	}
	if oldest.ID == "" || oldest.ID == middle.ID || middle.ID == newest.ID {
		t.Fatalf("draft ids are not distinct: %q %q %q", oldest.ID, middle.ID, newest.ID)
	}
	if oldest.CreatedAt.IsZero() || !oldest.CreatedAt.Equal(clock.at.Add(-2*time.Second)) {
		t.Fatalf("CreatedAt = %v, want the clock's first tick", oldest.CreatedAt)
	}
	// The body is rendered on the way in, so what was stored is what a human
	// reviews — not the raw text.
	for _, heading := range SectionHeadings {
		if !strings.Contains(oldest.Body, "## "+heading+"\n") {
			t.Fatalf("stored body is missing the %q section:\n%s", heading, oldest.Body)
		}
	}

	listed, err := service.List(ctx, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{newest.ID, middle.ID, oldest.ID}
	if got := ids(listed); !equalStrings(got, want) {
		t.Fatalf("List = %v, want newest first %v", got, want)
	}

	reopened := openStore(t, dir, nil)
	listed, err = reopened.List(ctx, 0)
	if err != nil {
		t.Fatalf("List after reopen: %v", err)
	}
	if got := ids(listed); !equalStrings(got, want) {
		t.Fatalf("List after reopen = %v, want %v", got, want)
	}
	if listed[2].Name != oldest.Name || listed[2].TaskID != oldest.TaskID || listed[2].Body != oldest.Body {
		t.Fatalf("reopened draft = %+v, want the one that was stored (%+v)", listed[2], oldest)
	}
	if !listed[2].CreatedAt.Equal(oldest.CreatedAt) {
		t.Fatalf("reopened CreatedAt = %v, want %v", listed[2].CreatedAt, oldest.CreatedAt)
	}

	limited, err := reopened.List(ctx, 2)
	if err != nil {
		t.Fatalf("List(limit 2): %v", err)
	}
	if got := ids(limited); !equalStrings(got, want[:2]) {
		t.Fatalf("List(limit 2) = %v, want %v", got, want[:2])
	}
}

// TestListIsDeterministicForOneSecond checks the tie-break: two drafts written
// inside the same second must not swap places between calls, because the card
// list would then flicker.
func TestListIsDeterministicForOneSecond(t *testing.T) {
	service := openStore(t, "", newClock())
	ctx := context.Background()
	first := draft(t, service, "first-skill")
	second := draft(t, service, "second-skill")

	before, err := service.List(ctx, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for attempt := 0; attempt < 5; attempt++ {
		again, err := service.List(ctx, 0)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if got, want := ids(again), ids(before); !equalStrings(got, want) {
			t.Fatalf("List order changed between calls: %v then %v", want, got)
		}
	}
	if len(before) != 2 {
		t.Fatalf("List = %v, want both drafts", ids(before))
	}
	seen := map[string]bool{first.ID: true, second.ID: true}
	for _, draft := range before {
		delete(seen, draft.ID)
	}
	if len(seen) != 0 {
		t.Fatalf("List is missing drafts: %v", seen)
	}
}

// TestDraftValidation is the table the UI leans on: a proposal that cannot become
// a skill directory is rejected on the way in, with a typed error.
func TestDraftValidation(t *testing.T) {
	longName := strings.Repeat("a", MaxNameRunes+1)
	shortName := "a"
	validName := "deploy-web"
	tests := []struct {
		name   string
		mutate func(*contracts.TaskSkillDraft)
		want   bool
	}{
		{name: "valid", mutate: func(*contracts.TaskSkillDraft) {}, want: true},
		{name: "valid with task id", mutate: func(d *contracts.TaskSkillDraft) { d.TaskID = "task-35" }, want: true},
		{name: "valid without task id", mutate: func(d *contracts.TaskSkillDraft) { d.TaskID = "" }, want: true},
		{name: "valid without summary", mutate: func(d *contracts.TaskSkillDraft) { d.Summary = "" }, want: true},
		{name: "name is required", mutate: func(d *contracts.TaskSkillDraft) { d.Name = "" }},
		{name: "name too short", mutate: func(d *contracts.TaskSkillDraft) { d.Name = shortName }},
		{name: "name too long", mutate: func(d *contracts.TaskSkillDraft) { d.Name = longName }},
		{name: "name uppercase", mutate: func(d *contracts.TaskSkillDraft) { d.Name = "Deploy-Web" }},
		{name: "name underscore", mutate: func(d *contracts.TaskSkillDraft) { d.Name = "deploy_web" }},
		{name: "name space", mutate: func(d *contracts.TaskSkillDraft) { d.Name = "deploy web" }},
		{name: "name path traversal", mutate: func(d *contracts.TaskSkillDraft) { d.Name = "../evil" }},
		{name: "summary over budget", mutate: func(d *contracts.TaskSkillDraft) {
			d.Summary = strings.Repeat("字", MaxSummaryRunes+1)
		}},
		{name: "summary at budget", mutate: func(d *contracts.TaskSkillDraft) {
			d.Summary = strings.Repeat("字", MaxSummaryRunes)
		}, want: true},
		{name: "body is required", mutate: func(d *contracts.TaskSkillDraft) { d.Body = "   \n\t\n" }},
		{name: "body over budget", mutate: func(d *contracts.TaskSkillDraft) {
			d.Body = strings.Repeat("字", MaxBodyRunes+1)
		}},
		{name: "task id over budget", mutate: func(d *contracts.TaskSkillDraft) {
			d.TaskID = strings.Repeat("t", MaxTaskIDRunes+1)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := openStore(t, "", newClock())
			candidate := proposal(validName)
			test.mutate(&candidate)
			created, err := service.Draft(context.Background(), candidate)
			if test.want {
				if err != nil {
					t.Fatalf("Draft = error %v, want a stored draft", err)
				}
				if created.ID == "" {
					t.Fatal("stored draft has no id")
				}
				return
			}
			if !errors.Is(err, contracts.ErrSkillDraftInvalid) {
				t.Fatalf("Draft err = %v, want ErrSkillDraftInvalid", err)
			}
			// A rejected proposal must not leave a row behind.
			listed, listErr := service.List(context.Background(), 0)
			if listErr != nil {
				t.Fatalf("List: %v", listErr)
			}
			if len(listed) != 0 {
				t.Fatalf("invalid proposal was stored: %+v", listed)
			}
		})
	}
}

// TestDraftCannotArriveAccepted proves a proposal cannot skip the human: the
// store owns status, id and reviewer.
func TestDraftCannotArriveAccepted(t *testing.T) {
	service := openStore(t, "", newClock())
	candidate := proposal("sneaky-skill")
	candidate.ID = "chosen-id"
	candidate.Status = contracts.SkillDraftAccepted
	candidate.Reviewer = "ghost"

	created, err := service.Draft(context.Background(), candidate)
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if created.Status != contracts.SkillDraftPending {
		t.Fatalf("status = %q, want pending", created.Status)
	}
	if created.ID == "chosen-id" || created.ID == "" {
		t.Fatalf("id = %q, want a store-generated id", created.ID)
	}
	if created.Reviewer != "" {
		t.Fatalf("reviewer = %q, want empty", created.Reviewer)
	}
}

// TestDecideAcceptAndReject covers the two decisions a human can make.
func TestDecideAcceptAndReject(t *testing.T) {
	dir := t.TempDir()
	service := openStore(t, dir, newClock())
	ctx := context.Background()

	accepted := draft(t, service, "keep-me")
	decided, err := service.Decide(ctx, accepted.ID, true, "ann")
	if err != nil {
		t.Fatalf("Decide(accept): %v", err)
	}
	if decided.Status != contracts.SkillDraftAccepted || decided.Reviewer != "ann" {
		t.Fatalf("accepted draft = %+v, want accepted by ann", decided)
	}
	// The decision is about the text the human read: it must not be re-rendered
	// or trimmed by the decision.
	if decided.Body != accepted.Body {
		t.Fatalf("Decide changed the body:\n%s\n---\n%s", accepted.Body, decided.Body)
	}

	rejected := draft(t, service, "drop-me")
	decided, err = service.Decide(ctx, rejected.ID, false, "ross")
	if err != nil {
		t.Fatalf("Decide(reject): %v", err)
	}
	if decided.Status != contracts.SkillDraftRejected || decided.Reviewer != "ross" {
		t.Fatalf("rejected draft = %+v, want rejected by ross", decided)
	}

	// Both decisions survive a restart.
	reopened := openStore(t, dir, nil)
	listed, err := reopened.List(ctx, 0)
	if err != nil {
		t.Fatalf("List after reopen: %v", err)
	}
	statuses := map[string]string{}
	for _, draft := range listed {
		statuses[draft.ID] = draft.Status + "/" + draft.Reviewer
	}
	if statuses[accepted.ID] != contracts.SkillDraftAccepted+"/ann" {
		t.Fatalf("reopened accepted draft = %q, want accepted/ann", statuses[accepted.ID])
	}
	if statuses[rejected.ID] != contracts.SkillDraftRejected+"/ross" {
		t.Fatalf("reopened rejected draft = %q, want rejected/ross", statuses[rejected.ID])
	}
}

// TestDecideIsIdempotent: a double click must not stack rows or flip status, and
// the reviewer recorded is always the person who decided last.
func TestDecideIsIdempotent(t *testing.T) {
	service := openStore(t, "", newClock())
	ctx := context.Background()
	created := draft(t, service, "double-click")

	for attempt := 0; attempt < 3; attempt++ {
		decided, err := service.Decide(ctx, created.ID, true, fmt.Sprintf("reviewer-%d", attempt))
		if err != nil {
			t.Fatalf("Decide #%d: %v", attempt, err)
		}
		if decided.Status != contracts.SkillDraftAccepted {
			t.Fatalf("status after Decide #%d = %q, want accepted", attempt, decided.Status)
		}
	}
	decided, err := service.Decide(ctx, created.ID, true, "ann")
	if err != nil {
		t.Fatalf("Decide(final): %v", err)
	}
	if decided.Reviewer != "ann" {
		t.Fatalf("reviewer = %q, want the last decision's ann", decided.Reviewer)
	}
	listed, err := service.List(ctx, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List = %d drafts, want exactly 1 (a re-decision is not a new row)", len(listed))
	}

	// Re-rejecting a rejected draft is idempotent too.
	other := draft(t, service, "double-reject")
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := service.Decide(ctx, other.ID, false, "ross"); err != nil {
			t.Fatalf("Decide(reject) #%d: %v", attempt, err)
		}
	}
	listed, err = service.List(ctx, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("List = %d drafts, want 2", len(listed))
	}
}

// TestRejectedDraftCannotBeAccepted is the one-way door: a rejection is terminal.
func TestRejectedDraftCannotBeAccepted(t *testing.T) {
	service := openStore(t, "", newClock())
	ctx := context.Background()
	created := draft(t, service, "never-again")

	if _, err := service.Decide(ctx, created.ID, false, "ross"); err != nil {
		t.Fatalf("Decide(reject): %v", err)
	}
	_, err := service.Decide(ctx, created.ID, true, "ann")
	if !errors.Is(err, contracts.ErrSkillDraftRejected) {
		t.Fatalf("Decide(accept after reject) err = %v, want ErrSkillDraftRejected", err)
	}
	listed, err := service.List(ctx, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List = %d drafts, want 1", len(listed))
	}
	if listed[0].Status != contracts.SkillDraftRejected {
		t.Fatalf("status = %q, want the rejection to stand", listed[0].Status)
	}
	if listed[0].Reviewer != "ross" {
		t.Fatalf("reviewer = %q, want the rejection's ross", listed[0].Reviewer)
	}
}

// TestDecideUnknownAndInvalid pins the two other decision errors.
func TestDecideUnknownAndInvalid(t *testing.T) {
	service := openStore(t, "", newClock())
	ctx := context.Background()
	created := draft(t, service, "some-skill")

	for _, id := range []string{"", "no-such-draft", created.ID + "x"} {
		if _, err := service.Decide(ctx, id, true, "ann"); !errors.Is(err, contracts.ErrSkillDraftUnknown) {
			t.Fatalf("Decide(id %q) err = %v, want ErrSkillDraftUnknown", id, err)
		}
	}
	for _, reviewer := range []string{"", "   "} {
		if _, err := service.Decide(ctx, created.ID, true, reviewer); !errors.Is(err, contracts.ErrSkillDraftInvalid) {
			t.Fatalf("Decide(reviewer %q) err = %v, want ErrSkillDraftInvalid", reviewer, err)
		}
	}
	if _, err := service.Decide(ctx, created.ID, true, strings.Repeat("r", MaxSummaryRunes+1)); !errors.Is(err, contracts.ErrSkillDraftInvalid) {
		t.Fatalf("Decide(long reviewer) err = %v, want ErrSkillDraftInvalid", err)
	}
	// None of the failed decisions changed the draft.
	listed, err := service.List(ctx, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].Status != contracts.SkillDraftPending || listed[0].Reviewer != "" {
		t.Fatalf("List = %+v, want the draft still pending", listed)
	}
}

// TestDraftNormalisesInput checks the trims a proposer can rely on: a name with
// stray whitespace is the same skill, and CRLF from a Windows-ish producer does
// not end up in the stored Markdown.
func TestDraftNormalisesInput(t *testing.T) {
	service := openStore(t, "", newClock())
	candidate := proposal("  padded-name  ")
	candidate.TaskID = "  task-7 "
	candidate.Summary = "  Use when padded.  "
	candidate.Body = "Run it.\r\nThen check.\r\n"

	created, err := service.Draft(context.Background(), candidate)
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if created.Name != "padded-name" || created.TaskID != "task-7" {
		t.Fatalf("draft = %+v, want trimmed name and task id", created)
	}
	if strings.Contains(created.Body, "\r") {
		t.Fatalf("stored body still carries CR:\n%q", created.Body)
	}
	if !strings.Contains(created.Body, "Use when padded.") {
		t.Fatalf("summary is not in the stored body:\n%s", created.Body)
	}
	if strings.Contains(created.Body, "  Use when padded.") {
		t.Fatalf("summary was stored untrimmed:\n%s", created.Body)
	}
}

// TestDraftIDIsUniquePerProposal: one task can propose several skills, so ids are
// not derived from the task.
func TestDraftIDIsUniquePerProposal(t *testing.T) {
	service := openStore(t, "", newClock())
	seen := map[string]bool{}
	for index := 0; index < 25; index++ {
		created := draft(t, service, fmt.Sprintf("same-task-%02d", index))
		if seen[created.ID] {
			t.Fatalf("duplicate draft id %q", created.ID)
		}
		seen[created.ID] = true
	}
}

// TestDraftDoesNotCollideOnDiskWithOtherProposals is a light guard: the store
// must never leave a second file (temp) behind in its directory.
func TestDraftLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	service := openStore(t, filepath.Join(dir, "skilldraft"), newClock())
	draft(t, service, "one-skill")
	draft(t, service, "two-skills")

	entries, err := readDir(t, filepath.Join(dir, "skilldraft"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0] != storeName {
		t.Fatalf("data dir holds %v, want only %s", entries, storeName)
	}
}

func ids(drafts []contracts.TaskSkillDraft) []string {
	out := make([]string, 0, len(drafts))
	for _, draft := range drafts {
		out = append(out, draft.ID)
	}
	return out
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
