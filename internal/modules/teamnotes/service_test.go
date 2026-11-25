package teamnotes_test

// TDD for the shared note board. The board owns no storage: every rule here is
// about what it hands to the memory service (deterministic URI, stable type,
// verbatim body) and what it promises back (newest first, never a hard failure
// on one unreadable row, an honest "unavailable" without a memory store).

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/teamnotes"
)

// fakeMemory is an in-memory contracts.MemoryService: it honours the interface
// (Write keeps content, List filters by type and sorts newest first, Get reads
// one row) so the tests exercise the real call shapes, not a stub.
type fakeMemory struct {
	mu      sync.Mutex
	rows    map[string]contracts.MemoryDetail
	order   []string
	writes  []contracts.MemoryDraft
	broken  map[string]bool
	listErr error
	clock   time.Time
	// returnAll makes List ignore limit, so a test can prove the service caps
	// its own per-row Get calls.
	returnAll bool

	listRoot  string
	listTypes []string
	listLimit int
	listCalls int
	getCalls  int
}

func newFakeMemory() *fakeMemory {
	return &fakeMemory{
		rows:   make(map[string]contracts.MemoryDetail),
		broken: make(map[string]bool),
		clock:  time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC),
	}
}

func (f *fakeMemory) Write(_ context.Context, _ string, draft contracts.MemoryDraft) (contracts.MemoryItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	summary := strings.TrimSpace(draft.Summary)
	if summary == "" {
		summary = strings.TrimSpace(draft.Title)
	}
	item := contracts.MemoryItem{
		URI:     draft.URI,
		Type:    draft.Type,
		Title:   strings.TrimSpace(draft.Title),
		Summary: summary,
		Updated: f.clock,
	}
	f.clock = f.clock.Add(time.Second)
	if _, ok := f.rows[draft.URI]; !ok {
		f.order = append(f.order, draft.URI)
	}
	f.rows[draft.URI] = contracts.MemoryDetail{
		MemoryItem: item,
		Overview:   draft.Overview,
		Content:    draft.Content,
	}
	f.writes = append(f.writes, draft)
	return item, nil
}

func (f *fakeMemory) List(_ context.Context, workspaceRoot, query string, types []string, limit int) ([]contracts.MemoryItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	f.listRoot, f.listTypes, f.listLimit = workspaceRoot, append([]string(nil), types...), limit
	if f.listErr != nil {
		return nil, f.listErr
	}
	allowed := make(map[string]bool, len(types))
	for _, typ := range types {
		allowed[typ] = true
	}
	items := make([]contracts.MemoryItem, 0, len(f.order))
	for _, uri := range f.order {
		detail := f.rows[uri]
		if len(allowed) > 0 && !allowed[detail.Type] {
			continue
		}
		items = append(items, detail.MemoryItem)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Updated.After(items[j].Updated) })
	if !f.returnAll && limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (f *fakeMemory) Get(_ context.Context, _ string, uri string) (contracts.MemoryDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	if f.broken[uri] {
		return contracts.MemoryDetail{}, fmt.Errorf("fake: %s is unreadable", uri)
	}
	detail, ok := f.rows[uri]
	if !ok {
		return contracts.MemoryDetail{}, fmt.Errorf("fake: %s is unknown", uri)
	}
	return detail, nil
}

func (f *fakeMemory) Delete(_ context.Context, _ string, uri string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, uri)
	return nil
}

// seed puts one row in the store without going through Record, so a test can
// model a note written by another tool (or a row whose content is gone).
func (f *fakeMemory) seed(item contracts.MemoryItem, content string, broken bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if item.URI == "" {
		item.URI = teamnotes.URIPrefix + "seeded"
	}
	f.rows[item.URI] = contracts.MemoryDetail{MemoryItem: item, Content: content}
	f.order = append(f.order, item.URI)
	if broken {
		f.broken[item.URI] = true
	}
}

// row returns the stored row for one URI.
func (f *fakeMemory) row(t *testing.T, uri string) contracts.MemoryDetail {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	detail, ok := f.rows[uri]
	if !ok {
		t.Fatalf("no row stored under %q (writes: %d)", uri, len(f.writes))
	}
	return detail
}

func at(hour, minute int) time.Time {
	return time.Date(2026, 11, 25, hour, minute, 0, 0, time.UTC)
}

func newService(t *testing.T) (*teamnotes.Service, *fakeMemory) {
	t.Helper()
	memory := newFakeMemory()
	return teamnotes.New(memory, teamnotes.Config{}), memory
}

func TestRecordWritesIntoTheDeterministicTeamURI(t *testing.T) {
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
	if want := "mem://user/self/memories/team-note/t-42"; recorded.ID != want {
		t.Fatalf("recorded ID = %q, want %q", recorded.ID, want)
	}
	if len(memory.writes) != 1 {
		t.Fatalf("writes = %d, want exactly 1", len(memory.writes))
	}
	draft := memory.writes[0]
	if draft.URI != "mem://user/self/memories/team-note/t-42" {
		t.Fatalf("draft URI = %q, want mem://user/self/memories/team-note/t-42", draft.URI)
	}
	if draft.Type != "team-note" || teamnotes.NoteType != "team-note" {
		t.Fatalf("draft type = %q, want team-note (the store rejects a dotted type)", draft.Type)
	}
	if draft.Title != "Deploy window moved" {
		t.Fatalf("draft title = %q", draft.Title)
	}
	for _, want := range []string{
		"# Deploy window moved",
		"- Task: T-42",
		"- Author: ann",
		"- At: 2026-11-25T09:30:00Z",
		"Move the deploy to 21:00 UTC so the on-call is awake.",
	} {
		if !strings.Contains(draft.Content, want) {
			t.Fatalf("draft content is missing %q:\n%s", want, draft.Content)
		}
	}
	taskAt := strings.Index(draft.Content, "- Task: T-42")
	authorAt := strings.Index(draft.Content, "- Author: ann")
	bodyAt := strings.Index(draft.Content, "Move the deploy")
	if !(0 < taskAt && taskAt < authorAt && bodyAt > authorAt) {
		t.Fatalf("content order wrong (heading/metadata/body):\n%s", draft.Content)
	}
	if !recorded.At.Equal(at(9, 30)) {
		t.Fatalf("recorded At = %s, want the caller's time", recorded.At)
	}
}

func TestRecordReplacesTheSameTaskNote(t *testing.T) {
	service, memory := newService(t)
	ctx := context.Background()

	first, err := service.Record(ctx, contracts.TeamNote{TaskID: "T-7", Title: "one", Body: "first"})
	if err != nil {
		t.Fatalf("Record(first): %v", err)
	}
	second, err := service.Record(ctx, contracts.TeamNote{TaskID: "t 7", Title: "two", Body: "second"})
	if err != nil {
		t.Fatalf("Record(second): %v", err)
	}
	// "T-7" is already URI-clean; "t 7" projects to t-7 plus a hash, so the two
	// spellings are different tasks, not the same one.
	if first.ID == second.ID {
		t.Fatalf("distinct task ids collapsed onto one URI: %q", first.ID)
	}
	if len(memory.rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(memory.rows))
	}

	again, err := service.Record(ctx, contracts.TeamNote{TaskID: "T-7", Title: "one again", Body: "third"})
	if err != nil {
		t.Fatalf("Record(again): %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("re-recording T-7 moved the URI: %q -> %q", first.ID, again.ID)
	}
	if len(memory.rows) != 2 {
		t.Fatalf("rows after re-record = %d, want 2 (same URI replaced)", len(memory.rows))
	}
	if got := memory.row(t, again.ID).Content; !strings.Contains(got, "third") {
		t.Fatalf("re-record did not replace the content:\n%s", got)
	}
}

func TestRecordProjectsHostileTaskIDsAndGeneratesOneWithoutATask(t *testing.T) {
	service, memory := newService(t)
	ctx := context.Background()

	lossy, err := service.Record(ctx, contracts.TeamNote{TaskID: "Sprint 7 / deploy", Title: "clean up"})
	if err != nil {
		t.Fatalf("Record(lossy): %v", err)
	}
	if matched, _ := regexp.MatchString(`^mem://user/self/memories/team-note/sprint-7-deploy-[0-9a-f]{8}$`, lossy.ID); !matched {
		t.Fatalf("lossy task URI = %q, want mem://user/self/memories/team-note/sprint-7-deploy-<hash8>", lossy.ID)
	}

	unspeakable, err := service.Record(ctx, contracts.TeamNote{TaskID: "###", Title: "no usable slug"})
	if err != nil {
		t.Fatalf("Record(unspeakable): %v", err)
	}
	if matched, _ := regexp.MatchString(`^mem://user/self/memories/team-note/[0-9a-f]{16}$`, unspeakable.ID); !matched {
		t.Fatalf("unspeakable task URI = %q, want a generated id", unspeakable.ID)
	}

	free, err := service.Record(ctx, contracts.TeamNote{Title: "stand-alone note"})
	if err != nil {
		t.Fatalf("Record(free): %v", err)
	}
	if matched, _ := regexp.MatchString(`^mem://user/self/memories/team-note/[0-9a-f]{16}$`, free.ID); !matched {
		t.Fatalf("task-less URI = %q, want mem://user/self/memories/team-note/<generated id>", free.ID)
	}
	other, err := service.Record(ctx, contracts.TeamNote{Title: "another stand-alone note"})
	if err != nil {
		t.Fatalf("Record(other): %v", err)
	}
	if other.ID == free.ID {
		t.Fatalf("two task-less notes shared the URI %q", free.ID)
	}
	if len(memory.rows) != 4 {
		t.Fatalf("rows = %d, want 4 distinct notes", len(memory.rows))
	}
}

func TestRecordValidationTable(t *testing.T) {
	service, memory := newService(t)
	ctx := context.Background()

	longTitle := strings.Repeat("é", teamnotes.MaxTitleRunes+1)
	edgeTitle := strings.Repeat("t", teamnotes.MaxTitleRunes)
	// 200 runes but 400 bytes: the note board's rune budget is fine, the
	// memory store's MaxTitleBytes is not, and the store would reject the
	// write — so this must already be an ErrTeamNoteInvalid.
	wideTitle := strings.Repeat("é", teamnotes.MaxTitleRunes)
	longBody := strings.Repeat("b", teamnotes.MaxBodyRunes+1)
	edgeBody := strings.Repeat("b", teamnotes.MaxBodyRunes)
	longAuthor := strings.Repeat("a", teamnotes.MaxAuthorRunes+1)
	edgeAuthor := strings.Repeat("a", teamnotes.MaxAuthorRunes)

	cases := []struct {
		name string
		note contracts.TeamNote
		ok   bool
	}{
		{name: "empty title", note: contracts.TeamNote{Body: "b"}},
		{name: "blank title", note: contracts.TeamNote{Title: "   \t "}},
		{name: "title one rune over", note: contracts.TeamNote{Title: longTitle}},
		{name: "title at the rune and byte limit", note: contracts.TeamNote{Title: edgeTitle}, ok: true},
		{name: "title 200 runes but 400 bytes", note: contracts.TeamNote{Title: wideTitle}},
		{name: "body one rune over", note: contracts.TeamNote{Title: "t", Body: longBody}},
		{name: "body at the limit", note: contracts.TeamNote{Title: "t", Body: edgeBody}, ok: true},
		{name: "author one rune over", note: contracts.TeamNote{Title: "t", Author: longAuthor}},
		{name: "author at the limit", note: contracts.TeamNote{Title: "t", Author: edgeAuthor}, ok: true},
		{name: "author empty", note: contracts.TeamNote{Title: "t"}, ok: true},
		{name: "no body", note: contracts.TeamNote{Title: "t"}, ok: true},
	}
	written := 0
	for _, testCase := range cases {
		_, err := service.Record(ctx, testCase.note)
		if testCase.ok {
			if err != nil {
				t.Fatalf("%s: Record = %v, want success", testCase.name, err)
			}
			written++
			continue
		}
		if !errors.Is(err, contracts.ErrTeamNoteInvalid) {
			t.Fatalf("%s: Record = %v, want ErrTeamNoteInvalid", testCase.name, err)
		}
		if len(memory.writes) != written {
			t.Fatalf("%s: wrote through to memory on an invalid note", testCase.name)
		}
	}
	if len(memory.writes) != written || written == 0 {
		t.Fatalf("writes = %d, want %d valid notes", len(memory.writes), written)
	}
}

func TestRecordFillsAndTruncatesTheTimestamp(t *testing.T) {
	memory := newFakeMemory()
	fixed := time.Date(2026, 11, 25, 12, 34, 56, 789_000_000, time.UTC)
	// A fixed clock keeps "now" deterministic; the service must still prefer a
	// caller-provided time.
	service := teamnotes.NewWithClock(memory, teamnotes.Config{}, func() time.Time { return fixed })

	zero, err := service.Record(context.Background(), contracts.TeamNote{Title: "no time given"})
	if err != nil {
		t.Fatalf("Record(zero): %v", err)
	}
	if want := fixed.Truncate(time.Second); !zero.At.Equal(want) {
		t.Fatalf("At = %s, want the clock truncated to %s", zero.At, want)
	}

	given := fixed.Add(-time.Hour).Add(500 * time.Millisecond)
	explicit, err := service.Record(context.Background(), contracts.TeamNote{TaskID: "T-1", Title: "stamped", At: given})
	if err != nil {
		t.Fatalf("Record(explicit): %v", err)
	}
	want := given.Truncate(time.Second)
	if !explicit.At.Equal(want) {
		t.Fatalf("At = %s, want caller time %s", explicit.At, want)
	}
	if !strings.Contains(memory.row(t, explicit.ID).Content, "- At: "+want.Format(time.RFC3339)) {
		t.Fatalf("stored content does not carry the caller time")
	}
}

func TestListReturnsNewestFirstAndMapsEveryFieldBack(t *testing.T) {
	service, memory := newService(t)
	ctx := context.Background()

	first, err := service.Record(ctx, contracts.TeamNote{TaskID: "T-1", Title: "oldest", Body: "body one", Author: "ann", At: at(9, 0)})
	if err != nil {
		t.Fatalf("Record(first): %v", err)
	}
	second, err := service.Record(ctx, contracts.TeamNote{TaskID: "T-2", Title: "middle", Body: "body two", Author: "bob", At: at(10, 0)})
	if err != nil {
		t.Fatalf("Record(second): %v", err)
	}
	third, err := service.Record(ctx, contracts.TeamNote{TaskID: "T-3", Title: "newest", Body: "body three", Author: "cleo", At: at(11, 0)})
	if err != nil {
		t.Fatalf("Record(third): %v", err)
	}

	notes, err := service.List(ctx, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(notes) != 3 {
		t.Fatalf("notes = %d, want 3", len(notes))
	}
	wantOrder := []contracts.TeamNote{third, second, first}
	for index, want := range wantOrder {
		got := notes[index]
		if got.ID != want.ID || got.Title != want.Title || got.TaskID != want.TaskID ||
			got.Author != want.Author || got.Body != want.Body || !got.At.Equal(want.At) {
			t.Fatalf("note[%d] = %+v, want %+v", index, got, want)
		}
	}
	if memory.listTypes[0] != "team-note" || len(memory.listTypes) != 1 {
		t.Fatalf("List types = %v, want [team-note]", memory.listTypes)
	}
	if memory.listRoot != "." {
		t.Fatalf("List root = %q, want the default %q", memory.listRoot, ".")
	}
}

func TestListHonoursTheLimitAndTheConfiguredRoot(t *testing.T) {
	memory := newFakeMemory()
	service := teamnotes.New(memory, teamnotes.Config{WorkspaceRoot: "/tmp/ws-team"})
	ctx := context.Background()
	for index := 0; index < 3; index++ {
		if _, err := service.Record(ctx, contracts.TeamNote{
			TaskID: fmt.Sprintf("T-%d", index),
			Title:  fmt.Sprintf("note %d", index),
		}); err != nil {
			t.Fatalf("Record(%d): %v", index, err)
		}
	}
	notes, err := service.List(ctx, 2)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("notes = %d, want 2", len(notes))
	}
	if memory.listLimit != 2 {
		t.Fatalf("List limit passed through = %d, want 2", memory.listLimit)
	}
	if memory.listRoot != "/tmp/ws-team" {
		t.Fatalf("List root = %q, want the configured root", memory.listRoot)
	}
	if !strings.HasPrefix(notes[0].Title, "note 2") {
		t.Fatalf("newest note = %q, want note 2 first", notes[0].Title)
	}
}

func TestListKeepsPartialRows(t *testing.T) {
	memory := newFakeMemory()
	service := teamnotes.New(memory, teamnotes.Config{})
	ctx := context.Background()

	// A note written by another tool: no recognisable block at all.
	memory.seed(contracts.MemoryItem{
		URI:     "mem://user/self/memories/team-note/imported-1",
		Type:    teamnotes.NoteType,
		Title:   "Imported note",
		Summary: "written elsewhere",
		Updated: at(8, 0),
	}, "plain text from the CLI, no heading and no metadata", false)
	// A row whose content cannot be read any more: Get fails here, and List must
	// still return that row from its MemoryItem fields (URI/Title/Updated) with
	// empty Author/Body — degrading one row, never failing the whole board.
	memory.seed(contracts.MemoryItem{
		URI:     "mem://user/self/memories/team-note/gone-2",
		Type:    teamnotes.NoteType,
		Title:   "Gone",
		Updated: at(9, 0),
	}, "", true)
	// A row with a header whose metadata breaks off halfway: the part that
	// parses is used, the rest stays in the body.
	memory.seed(contracts.MemoryItem{
		URI:     "mem://user/self/memories/team-note/broken-3",
		Type:    teamnotes.NoteType,
		Updated: at(7, 0),
	}, "# Half a header\n\n- Task: B-9\n- Author: dan\n- At: not-a-time\n\nbody kept anyway", false)

	notes, err := service.List(ctx, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(notes) != 3 {
		t.Fatalf("notes = %d, want 3 partial rows", len(notes))
	}

	newest := notes[0]
	if newest.ID != "mem://user/self/memories/team-note/gone-2" || newest.Title != "Gone" || newest.TaskID != "gone-2" {
		t.Fatalf("unreadable row = %+v, want URI/title/task from the row itself", newest)
	}
	if newest.Body != "" || newest.Author != "" {
		t.Fatalf("unreadable row invented content: %+v", newest)
	}
	if !newest.At.Equal(at(9, 0)) {
		t.Fatalf("unreadable row At = %s, want the row's updated time", newest.At)
	}

	older := notes[1]
	if older.Title != "Imported note" || older.TaskID != "imported-1" {
		t.Fatalf("foreign row = %+v, want its own title and a URI-derived task", older)
	}
	if older.Body != "plain text from the CLI, no heading and no metadata" {
		t.Fatalf("foreign row body = %q, want the raw content", older.Body)
	}
	if !older.At.Equal(at(8, 0)) {
		t.Fatalf("foreign row At = %s, want the row's updated time", older.At)
	}

	broken := notes[2]
	if broken.Title != "Half a header" || broken.TaskID != "B-9" || broken.Author != "dan" {
		t.Fatalf("partially parsed row = %+v, want the parsable header fields", broken)
	}
	if !broken.At.Equal(at(7, 0)) {
		t.Fatalf("partially parsed row At = %s, want the row's updated time", broken.At)
	}
	if !strings.Contains(broken.Body, "- At: not-a-time") || !strings.Contains(broken.Body, "body kept anyway") {
		t.Fatalf("partially parsed row lost its body: %q", broken.Body)
	}
}

func TestUnavailableWithoutAMemoryService(t *testing.T) {
	ctx := context.Background()

	service := teamnotes.New(nil, teamnotes.Config{})
	if _, err := service.Record(ctx, contracts.TeamNote{Title: "t"}); !errors.Is(err, contracts.ErrTeamNotesUnavailable) {
		t.Fatalf("Record without memory = %v, want ErrTeamNotesUnavailable", err)
	}
	if _, err := service.List(ctx, 10); !errors.Is(err, contracts.ErrTeamNotesUnavailable) {
		t.Fatalf("List without memory = %v, want ErrTeamNotesUnavailable", err)
	}

	var nilService *teamnotes.Service
	if _, err := nilService.List(ctx, 10); !errors.Is(err, contracts.ErrTeamNotesUnavailable) {
		t.Fatalf("nil List = %v, want ErrTeamNotesUnavailable", err)
	}
}

func TestBodyIsStoredVerbatimAndCannotRetargetTheURI(t *testing.T) {
	service, memory := newService(t)
	ctx := context.Background()

	body := strings.Join([]string{
		`{"uri": "mem://user/self/memories/team-note/hijacked", "type": "team.note"}`,
		"---",
		"- Author: mallory",
		"- At: 1999-01-01T00:00:00Z",
		"# a heading inside the body",
	}, "\n")

	first, err := service.Record(ctx, contracts.TeamNote{TaskID: "Deploy 7", Title: "hostile body", Body: body})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if strings.Contains(first.ID, "hijacked") || !strings.HasPrefix(first.ID, "mem://user/self/memories/team-note/deploy-7-") {
		t.Fatalf("body leaked into the URI: %q", first.ID)
	}

	// The stored content holds the body verbatim, metadata lines and all.
	content := memory.row(t, first.ID).Content
	if !strings.Contains(content, body) {
		t.Fatalf("body was rewritten in storage:\n%s", content)
	}

	// A different body on the same task replaces the note, it does not move it.
	second, err := service.Record(ctx, contracts.TeamNote{TaskID: "Deploy 7", Title: "hostile body", Body: "different body"})
	if err != nil {
		t.Fatalf("Record(second): %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("changing the body moved the URI: %q -> %q", first.ID, second.ID)
	}
	if got := memory.row(t, first.ID).Content; !strings.Contains(got, "different body") || strings.Contains(got, "hijacked") {
		t.Fatalf("replacement content is wrong:\n%s", got)
	}

	// Recording the hostile body again and listing gives it back untouched.
	if _, err := service.Record(ctx, contracts.TeamNote{TaskID: "Deploy 7", Title: "hostile body", Body: body}); err != nil {
		t.Fatalf("Record(third): %v", err)
	}
	notes, err := service.List(ctx, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %d, want 1", len(notes))
	}
	if notes[0].ID != first.ID {
		t.Fatalf("listed ID = %q, want %q", notes[0].ID, first.ID)
	}
	if notes[0].Body != body {
		t.Fatalf("listed body =\n%q\nwant\n%q", notes[0].Body, body)
	}
	if notes[0].TaskID != "Deploy 7" || notes[0].Author != "" {
		t.Fatalf("listed metadata = task %q author %q, want the parsed header block", notes[0].TaskID, notes[0].Author)
	}
	if notes[0].At.IsZero() {
		t.Fatalf("listed At is zero; Record must stamp the time")
	}
}

// TestURIIsInsideTheMemoryStoreSubtree is the regression nail for the 2026-11-25
// bug: the note board first used memory://team/<id> with type "team.note", and
// the store behind memorystore rejects both (ParseURI requires "mem://",
// ValidateEntry requires a [a-z0-9-]+ type), so every Record would have failed
// in the wired product while the fake-based tests stayed green.
func TestURIIsInsideTheMemoryStoreSubtree(t *testing.T) {
	service, memory := newService(t)
	ctx := context.Background()

	if teamnotes.NoteType != "team-note" {
		t.Fatalf("NoteType = %q, want team-note (the store rejects a dotted type)", teamnotes.NoteType)
	}
	if want := "mem://user/self/memories/team-note/"; teamnotes.URIPrefix != want {
		t.Fatalf("URIPrefix = %q, want %q", teamnotes.URIPrefix, want)
	}

	task, err := service.Record(ctx, contracts.TeamNote{TaskID: "T-1", Title: "in the subtree"})
	if err != nil {
		t.Fatalf("Record(task): %v", err)
	}
	free, err := service.Record(ctx, contracts.TeamNote{Title: "in the subtree too"})
	if err != nil {
		t.Fatalf("Record(free): %v", err)
	}

	// Assert against literals, not the constants: a future flip must fail here.
	const memoriesRoot = "mem://user/self/memories"
	const notesRoot = "mem://user/self/memories/team-note/"
	for _, uri := range []string{task.ID, free.ID} {
		if !strings.HasPrefix(uri, notesRoot) {
			t.Fatalf("note URI %q is not under %q, so the store would never list it", uri, notesRoot)
		}
		// The store's segment charset: a-z, 0-9, '.', '_' and '-'.
		for _, segment := range strings.Split(strings.TrimPrefix(uri, "mem://"), "/") {
			if segment == "" {
				t.Fatalf("note URI %q has an empty segment", uri)
			}
			for _, r := range segment {
				legal := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
				if !legal {
					t.Fatalf("note URI %q has segment %q outside the store charset", uri, segment)
				}
			}
		}
		if !strings.HasPrefix(uri, memoriesRoot+"/") {
			t.Fatalf("note URI %q is outside the memories root the store enumerates", uri)
		}
	}
	if got := memory.writes[0].Type; got != "team-note" {
		t.Fatalf("written type = %q, want team-note", got)
	}
	notes, err := service.List(ctx, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(notes) != 2 || !strings.HasPrefix(notes[0].ID, notesRoot) {
		t.Fatalf("List returned %d notes (%+v), want both under %q", len(notes), notes, notesRoot)
	}
}

// TestListCapsGetsAtTheLimit pins the N+1 cost: the rows are cut to limit before
// the per-row Get, so a provider that ignores limit cannot make one List read
// hundreds of bodies.
func TestListCapsGetsAtTheLimit(t *testing.T) {
	memory := newFakeMemory()
	memory.returnAll = true
	service := teamnotes.New(memory, teamnotes.Config{})
	ctx := context.Background()
	for index := 0; index < 5; index++ {
		if _, err := service.Record(ctx, contracts.TeamNote{
			TaskID: fmt.Sprintf("T-%d", index),
			Title:  fmt.Sprintf("note %d", index),
		}); err != nil {
			t.Fatalf("Record(%d): %v", index, err)
		}
	}
	memory.getCalls = 0

	notes, err := service.List(ctx, 2)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("notes = %d, want 2", len(notes))
	}
	if memory.getCalls != 2 {
		t.Fatalf("Get calls = %d, want 2 (bounded by limit)", memory.getCalls)
	}
}

// TestDraftStaysInsideTheStoreFieldCaps checks the write side against the
// store's byte caps, not just the note board's rune budgets: a title over 200
// bytes is refused (see the validation table) and the Memory-tab summary stays
// under 512 bytes even when every excerpt rune is 4 bytes wide.
func TestDraftStaysInsideTheStoreFieldCaps(t *testing.T) {
	service, memory := newService(t)
	ctx := context.Background()

	body := strings.Repeat("🚀", 400) // 1600 bytes, 400 runes
	if _, err := service.Record(ctx, contracts.TeamNote{Title: "emoji body", Body: body}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	draft := memory.writes[0]
	if len(draft.Summary) > 512 {
		t.Fatalf("summary is %d bytes, want at most 512 (memoryvfs.MaxSummaryBytes)", len(draft.Summary))
	}
	if draft.Summary == "" {
		t.Fatal("summary is empty; the store requires one")
	}
	if len(draft.Title) > 200 {
		t.Fatalf("title is %d bytes, want at most 200 (memoryvfs.MaxTitleBytes)", len(draft.Title))
	}
	if len(draft.Overview) > 16<<10 {
		t.Fatalf("overview is %d bytes, want at most 16KiB (memoryvfs.MaxOverviewBytes)", len(draft.Overview))
	}
	if len(draft.Content) > 1<<20 {
		t.Fatalf("content is %d bytes, want at most 1MiB (memoryvfs.MaxContentBytes)", len(draft.Content))
	}
}
