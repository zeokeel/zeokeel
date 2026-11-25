// Package teamnotes is the team's shared note board: a thin, typed layer over
// the memory store. It never keeps its own storage — a note is one memory of
// type "team-note" under mem://user/self/memories/team-note/<id>, so the Memory
// tab, search and retention all work on notes for free.
//
// The namespace and the type are not aesthetic choices: the store behind
// memorystore (zeokeel-rig/memoryvfs) only accepts "mem://" URIs whose segments
// use [a-z0-9._-], only enumerates under mem://user/self/memories, and requires
// a memory type matching [a-z0-9-]+. A note outside that subtree would write
// nowhere visible and a dotted type ("team.note") is rejected outright.
package teamnotes

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// NoteType is the memory type every shared note is stored under. It matches
// the store's [a-z0-9-]+ rule for memory types (a dot is rejected).
const NoteType = "team-note"

// URIPrefix is the memory namespace shared notes live in. It must stay inside
// the store's memories root (mem://user/self/memories), otherwise List — which
// enumerates that subtree — would never see a note.
const URIPrefix = "mem://user/self/memories/team-note/"

// Agent-scoped notes live under their own memory type and subtree. That is the
// whole enforcement: a team listing filters on the team type, so a bot's private
// note cannot appear on the shared board even by accident.
const (
	AgentNoteType  = "agent-note"
	AgentURIPrefix = "mem://user/self/memories/agent-note/"
)

// Validation limits. The notes' own budgets are rune counts (a CJK title and a
// Latin one get the same room); MaxTitleBytes is the store's separate hard cap.
const (
	// MaxTitleRunes is the note-board budget for a title.
	MaxTitleRunes = 200
	// MaxTitleBytes is the memory store's cap (memoryvfs.MaxTitleBytes). A title
	// that passes the rune check can still exceed it (CJK is ~3 bytes per rune),
	// and the store would then reject the whole write — so Record reports the
	// byte cap as an invalid title, not as a store error.
	MaxTitleBytes  = 200
	MaxBodyRunes   = 8000
	MaxAuthorRunes = 64
	// maxSlugRunes bounds the task-derived part of a URI. The task id itself is
	// not limited here; only its printable projection is.
	maxSlugRunes = 48
	// summaryRunes bounds the Memory-tab excerpt; maxSummaryBytes is the
	// store's cap (memoryvfs.MaxSummaryBytes), which a 160-emoji excerpt would
	// otherwise exceed.
	summaryRunes    = 160
	maxSummaryBytes = 512
)

// Review state is stored in the note's own Markdown, so a review survives a
// restart with no schema of its own and stays readable in the Memory tab. The
// metadata block grows one status line plus two attribution lines:
//
//   - Task: T-42
//   - Author: ann
//   - At: 2026-11-25T09:30:00Z
//   - Reviewed: accepted          (or "rejected")
//   - ReviewedBy: ross
//   - ReviewedAt: 2026-11-25T10:00:00Z
//
// Only reviewed notes carry the last three lines; an unreviewed note's block is
// byte-identical to what earlier versions wrote, so old notes keep parsing. A
// rejected note also gets rejectionSummaryPrefix on its summary, because the
// Memory tab lists summaries and never shows a body.
const (
	reviewAccepted = "accepted"
	reviewRejected = "rejected"

	// rejectionSummaryPrefix marks a rejected note where the board shows it
	// first (the listed excerpt).
	rejectionSummaryPrefix = "[rejected] "
)

// reviewState is the review half of a note when it is rendered. It is kept out
// of contracts.TeamNote because the public note carries only Reviewed/
// ReviewedBy/ReviewedAt, while rendering must also know which way the review
// went: a rejection is recorded in the content, not in a separate field.
type reviewState struct {
	reviewed bool
	accepted bool
	reviewer string
	at       time.Time
}

// Config is the typed assembly config.
type Config struct {
	// WorkspaceRoot scopes the notes to one workspace. Empty means "." (the
	// process working directory), i.e. the same root the agent's own memories
	// use by default.
	WorkspaceRoot string `json:"workspaceRoot,omitempty"`
}

// Service records and lists team notes through the memory service.
type Service struct {
	memory contracts.MemoryService
	root   string
	now    func() time.Time
}

var _ contracts.TeamNotesService = (*Service)(nil)

// New builds the note board over memory. A nil memory service is allowed: the
// service then reports ErrTeamNotesUnavailable instead of pretending to store.
func New(memory contracts.MemoryService, cfg Config) *Service {
	root := strings.TrimSpace(cfg.WorkspaceRoot)
	if root == "" {
		root = "."
	}
	return &Service{memory: memory, root: root, now: time.Now}
}

// NewWithClock is New with an injected clock, following the constructors the
// other modules use (heartbeat, lead): the assembly passes time.Now, tests pin
// "now" so a stamped note is comparable.
func NewWithClock(memory contracts.MemoryService, cfg Config, now func() time.Time) *Service {
	service := New(memory, cfg)
	if now != nil {
		service.now = now
	}
	return service
}

// Record validates one note and writes it as a "team-note" memory. The note is
// always written unreviewed: recording is for facts, and only Review (a human)
// turns one into something the team treats as agreed.
//
// The URI is deterministic: mem://user/self/memories/team-note/<task slug> for
// a task note, so re-recording the same task replaces the note (and clears any
// review on it) instead of piling up near duplicates. A note without a task gets
// a generated id. Nothing from the body ever reaches the URI, so a body that
// looks like JSON, a heading or a metadata line cannot retarget the write.
func (s *Service) Record(ctx context.Context, note contracts.TeamNote) (contracts.TeamNote, error) {
	if s == nil || s.memory == nil {
		return contracts.TeamNote{}, contracts.ErrTeamNotesUnavailable
	}
	note.Title = strings.TrimSpace(note.Title)
	note.Author = strings.TrimSpace(note.Author)
	note.TaskID = strings.TrimSpace(note.TaskID)
	note.Body = strings.ReplaceAll(note.Body, "\r\n", "\n")
	// Every note starts unreviewed. Review state belongs to Review: a caller
	// cannot smuggle "already approved" in through Record, and re-recording a
	// task note sends its previous review back to unreviewed (the text changed,
	// so the approval no longer describes what is stored).
	note.Reviewed = false
	note.ReviewedBy = ""
	note.ReviewedAt = time.Time{}
	if err := validate(note); err != nil {
		return contracts.TeamNote{}, err
	}
	if note.At.IsZero() {
		note.At = s.now()
	}
	// RFC3339 is second precision, and that is what the Markdown block stores;
	// truncating here keeps Record's return value equal to a later List row.
	note.At = note.At.UTC().Truncate(time.Second)
	note.ID = noteURI(note)

	if _, err := s.memory.Write(ctx, s.root, draftOf(note, reviewState{})); err != nil {
		return contracts.TeamNote{}, fmt.Errorf("teamnotes: write note %q: %w", note.ID, err)
	}
	return note, nil
}

// Review records a human review on one stored note and returns the rewritten
// note.
//
// The rewrite goes to the same URI, so a review replaces the row instead of
// adding one and reviewing twice is idempotent: the second call updates
// ReviewedBy/ReviewedAt and leaves one row behind. Title and body are kept
// verbatim — Read/modify/Write only ever adds the review lines to the metadata
// block. Because the row is rewritten, the store's Updated (what List sorts by)
// moves to the review time; the note's own At stays the time it was recorded.
func (s *Service) Review(ctx context.Context, review contracts.TeamNoteReview) (contracts.TeamNote, error) {
	if s == nil || s.memory == nil {
		return contracts.TeamNote{}, contracts.ErrTeamNotesUnavailable
	}
	review.NoteID = strings.TrimSpace(review.NoteID)
	review.Reviewer = strings.TrimSpace(review.Reviewer)
	if review.NoteID == "" {
		return contracts.TeamNote{}, invalid("note id is empty")
	}
	if review.Reviewer == "" {
		return contracts.TeamNote{}, invalid("reviewer is empty")
	}
	if utf8.RuneCountInString(review.Reviewer) > MaxAuthorRunes {
		return contracts.TeamNote{}, invalid("reviewer is %d runes, limit %d", utf8.RuneCountInString(review.Reviewer), MaxAuthorRunes)
	}
	// Only notes in the board's own namespace are reviewable. Without this a
	// stray id would rewrite some other memory under the same root.
	if !strings.HasPrefix(review.NoteID, URIPrefix) || len(review.NoteID) == len(URIPrefix) {
		return contracts.TeamNote{}, unknown(review.NoteID, "not a team note URI")
	}
	detail, err := s.memory.Get(ctx, s.root, review.NoteID)
	if err != nil {
		// Unknown and unreadable are the same answer to the reviewer: there is
		// no note here to review. The store's error stays in the chain so a
		// caller can still log the cause.
		return contracts.TeamNote{}, unknown(review.NoteID, "%v", err)
	}
	if detail.Type != "" && detail.Type != NoteType {
		return contracts.TeamNote{}, unknown(review.NoteID, "memory type is %q, not %q", detail.Type, NoteType)
	}

	// Rebuild the note from the stored content, falling back to the row for
	// anything the content does not carry. The body is never re-derived, so a
	// review cannot rewrite what the author wrote.
	note := parseContent(detail.Content)
	note.ID = review.NoteID
	if note.Title == "" {
		note.Title = strings.TrimSpace(detail.Title)
	}
	if note.TaskID == "" {
		note.TaskID = taskIDFromURI(review.NoteID)
	}
	if note.At.IsZero() {
		note.At = detail.Updated
	}
	note.At = note.At.UTC().Truncate(time.Second)

	at := review.At
	if at.IsZero() {
		at = s.now()
	}
	note.Reviewed = true
	note.ReviewedBy = review.Reviewer
	note.ReviewedAt = at.UTC().Truncate(time.Second)
	// Review never invents content: the title must already be there (from the
	// heading or from the row). A title-less row is a note the board cannot
	// render, and asking a human to review it would only conceal that.
	if strings.TrimSpace(note.Title) == "" {
		return contracts.TeamNote{}, invalid("note %q has no title to review", review.NoteID)
	}
	if len(note.Title) > MaxTitleBytes {
		// The store would refuse this write anyway; say so in the board's own
		// terms instead of surfacing a store error.
		return contracts.TeamNote{}, invalid("note %q title is %d bytes, memory store limit %d", review.NoteID, len(note.Title), MaxTitleBytes)
	}

	state := reviewState{reviewed: true, accepted: review.Accept, reviewer: note.ReviewedBy, at: note.ReviewedAt}
	if _, err := s.memory.Write(ctx, s.root, draftOf(note, state)); err != nil {
		return contracts.TeamNote{}, fmt.Errorf("teamnotes: write review for %q: %w", note.ID, err)
	}
	return note, nil
}

// List returns the newest notes first.
//
// The store's rows (contracts.MemoryItem) carry URI/Title/Summary/Updated but
// no content, so a row on its own cannot restore TaskID, Author or Body. Each
// returned row is therefore enriched with one Get, and the rows are cut to
// limit *before* the reads so a List call costs at most limit+1 store accesses.
//
// A row whose Get fails, or whose content is foreign or half-parsed, still
// lists from its own fields; one broken note must never hide the rest of the
// board.
func (s *Service) List(ctx context.Context, limit int) ([]contracts.TeamNote, error) {
	if s == nil || s.memory == nil {
		return nil, contracts.ErrTeamNotesUnavailable
	}
	items, err := s.memory.List(ctx, s.root, "", []string{NoteType}, limit)
	if err != nil {
		return nil, fmt.Errorf("teamnotes: list notes: %w", err)
	}
	if limit > 0 && len(items) > limit {
		// Some providers return more than asked; never spend more Gets than the
		// caller asked notes for.
		items = items[:limit]
	}
	notes := make([]contracts.TeamNote, 0, len(items))
	for _, item := range items {
		notes = append(notes, s.noteOf(ctx, item))
	}
	return notes, nil
}

// noteOf maps one stored row back to a note. Every field is best-effort.
func (s *Service) noteOf(ctx context.Context, item contracts.MemoryItem) contracts.TeamNote {
	note := contracts.TeamNote{
		ID:     item.URI,
		TaskID: taskIDFromURI(item.URI),
		Title:  item.Title,
		At:     item.Updated,
	}
	detail, err := s.memory.Get(ctx, s.root, item.URI)
	if err != nil {
		return note
	}
	parsed := parseContent(detail.Content)
	note.Body = parsed.Body
	if note.Title == "" {
		note.Title = parsed.Title
	}
	if parsed.TaskID != "" {
		note.TaskID = parsed.TaskID
	}
	note.Author = parsed.Author
	if !parsed.At.IsZero() {
		note.At = parsed.At
	}
	// Review state comes from the note's own content: a row with no review line
	// is unreviewed, whatever else the store shows.
	note.Reviewed = parsed.Reviewed
	note.ReviewedBy = parsed.ReviewedBy
	note.ReviewedAt = parsed.ReviewedAt
	return note
}

// validate rejects notes the note board cannot render honestly.
func validate(note contracts.TeamNote) error {
	switch note.Scope {
	case "", contracts.NoteScopeTeam:
	case contracts.NoteScopeAgent:
		if strings.TrimSpace(note.Owner) == "" {
			return invalid("agent-scoped note needs an owner")
		}
	default:
		return invalid("unknown scope %q", note.Scope)
	}
	switch {
	case note.Title == "":
		return invalid("title is empty")
	case utf8.RuneCountInString(note.Title) > MaxTitleRunes:
		return invalid("title is %d runes, limit %d", utf8.RuneCountInString(note.Title), MaxTitleRunes)
	case len(note.Title) > MaxTitleBytes:
		return invalid("title is %d bytes, memory store limit %d", len(note.Title), MaxTitleBytes)
	case utf8.RuneCountInString(note.Body) > MaxBodyRunes:
		return invalid("body is %d runes, limit %d", utf8.RuneCountInString(note.Body), MaxBodyRunes)
	case utf8.RuneCountInString(note.Author) > MaxAuthorRunes:
		return invalid("author is %d runes, limit %d", utf8.RuneCountInString(note.Author), MaxAuthorRunes)
	}
	return nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", contracts.ErrTeamNoteInvalid, fmt.Sprintf(format, args...))
}

// unknown reports an id that names no reviewable note. The detail is kept in
// the message (and, where there is one, the store's error in the chain) so a
// caller can log why the review found nothing.
func unknown(id, format string, args ...any) error {
	return fmt.Errorf("%w: %q: %s", contracts.ErrTeamNoteUnknown, id, fmt.Sprintf(format, args...))
}

// noteURI is the deterministic memory URI of one note.
func noteURI(note contracts.TeamNote) string {
	prefix := URIPrefix
	if note.Scope == contracts.NoteScopeAgent {
		prefix = AgentURIPrefix + agentSlug(note.Owner) + "/"
	}
	if slug, ok := taskSlug(note.TaskID); ok {
		return prefix + slug
	}
	return prefix + generatedID(note)
}

// agentSlug projects a bot id onto the URI charset; an id that cannot be
// projected keeps its own generated segment so two bots never share a subtree.
func agentSlug(owner string) string {
	if slug, ok := taskSlug(owner); ok {
		return slug
	}
	return generatedID(contracts.TeamNote{Author: owner})
}

// taskSlug projects a task id onto the URI charset [a-z0-9-]. A task id that
// needs more than that (spaces, punctuation, upper case) is still projected
// readably, but gets a short hash of the original appended so two distinct
// task ids cannot collide on one note.
func taskSlug(taskID string) (string, bool) {
	trimmed := strings.ToLower(strings.TrimSpace(taskID))
	if trimmed == "" {
		return "", false
	}
	var builder strings.Builder
	lossy := false
	lastDash := false
	for _, r := range trimmed {
		if builder.Len() >= maxSlugRunes {
			lossy = true
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == ' ' || r == '/' || r == '.' || r == ':':
			if !lastDash && builder.Len() > 0 {
				builder.WriteByte('-')
				lastDash = true
			}
			if r != '-' {
				lossy = true
			}
		default:
			lossy = true
			if !lastDash && builder.Len() > 0 {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		return "", false
	}
	if lossy {
		sum := sha256.Sum256([]byte(trimmed))
		slug += "-" + hex.EncodeToString(sum[:])[:8]
	}
	return slug, true
}

// generatedID is the URI id of a note without a task: random, because there is
// no natural key, and 8 bytes is enough that notes never collide.
func generatedID(note contracts.TeamNote) string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	// crypto/rand does not fail on any supported platform; keeping a
	// content-derived fallback means Record still works if it ever does.
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d", note.Title, note.Author, note.Body, note.At.UnixNano())))
	return hex.EncodeToString(sum[:8])
}

// taskIDFromURI recovers the task id projection from a note URI. It is only a
// fallback for rows whose content is unreadable — the content holds the real id.
func taskIDFromURI(uri string) string {
	trimmed := strings.TrimSpace(uri)
	if !strings.HasPrefix(trimmed, URIPrefix) {
		return ""
	}
	id := strings.TrimPrefix(trimmed, URIPrefix)
	if id == "" || strings.ContainsAny(id, "/?#") {
		return ""
	}
	return id
}

// draftOf builds the memory write for one note. Record and Review both go
// through it so the stored shape can never drift between them.
func draftOf(note contracts.TeamNote, state reviewState) contracts.MemoryDraft {
	memoryType := NoteType
	if note.Scope == contracts.NoteScopeAgent {
		memoryType = AgentNoteType
	}
	return contracts.MemoryDraft{
		URI:      note.ID,
		Type:     memoryType,
		Title:    note.Title,
		Summary:  summaryOf(note, state),
		Overview: metaBlock(note, state),
		Content:  renderContent(note, state),
	}
}

// renderContent is the Markdown stored as the memory content: a heading and the
// metadata bullets, then the body verbatim.
func renderContent(note contracts.TeamNote, state reviewState) string {
	var builder strings.Builder
	builder.WriteString("# ")
	builder.WriteString(note.Title)
	builder.WriteString("\n\n")
	builder.WriteString(metaBlock(note, state))
	builder.WriteString("\n")
	if note.Body != "" {
		builder.WriteString("\n")
		builder.WriteString(note.Body)
		if !strings.HasSuffix(note.Body, "\n") {
			builder.WriteString("\n")
		}
	}
	return builder.String()
}

// metaBlock is the note's metadata: three bullets for the note itself, and
// three more once a human has reviewed it. The review lines are what makes a
// review survive a restart without any extra schema — and, for a rejection,
// they are how the stored content says the note was rejected.
func metaBlock(note contracts.TeamNote, state reviewState) string {
	block := fmt.Sprintf("- Task: %s\n- Author: %s\n- At: %s",
		note.TaskID, note.Author, note.At.UTC().Format(time.RFC3339))
	if !state.reviewed {
		return block
	}
	status := reviewRejected
	if state.accepted {
		status = reviewAccepted
	}
	return fmt.Sprintf("%s\n- Reviewed: %s\n- ReviewedBy: %s\n- ReviewedAt: %s",
		block, status, state.reviewer, state.at.UTC().Format(time.RFC3339))
}

// summaryOf is the one-line Memory-tab excerpt: the first non-empty body line,
// falling back to the title. A rejected note says so first, because the Memory
// tab lists summaries and never shows the metadata block.
func summaryOf(note contracts.TeamNote, state reviewState) string {
	excerpt := firstLine(note.Body)
	if excerpt == "" {
		excerpt = note.Title
	}
	if state.reviewed && !state.accepted {
		excerpt = rejectionSummaryPrefix + excerpt
	}
	return truncate(excerpt, summaryRunes, maxSummaryBytes)
}

func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// truncate cuts text to maxRunes and maxBytes at a rune boundary.
func truncate(text string, maxRunes, maxBytes int) string {
	runes := 0
	bytes := 0
	for index, r := range text {
		runes++
		bytes += utf8.RuneLen(r)
		if runes > maxRunes || bytes > maxBytes {
			return text[:index]
		}
	}
	return text
}

// parseContent reads the block renderContent wrote. It is deliberately
// forgiving: an unreadable (or foreign) content still yields a title-free note
// whose Body is the whole content, so the caller keeps the row.
func parseContent(content string) contracts.TeamNote {
	var note contracts.TeamNote
	text := strings.ReplaceAll(content, "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return note
	}
	lines := strings.Split(text, "\n")
	index := 0
	for index < len(lines) && strings.TrimSpace(lines[index]) == "" {
		index++
	}
	if index >= len(lines) {
		return note
	}
	heading, ok := strings.CutPrefix(strings.TrimSpace(lines[index]), "# ")
	if !ok {
		// Not our block: keep the whole content as the body.
		note.Body = strings.TrimSpace(text)
		return note
	}
	note.Title = strings.TrimSpace(heading)
	index++
	for index < len(lines) && strings.TrimSpace(lines[index]) == "" {
		index++
	}
	// metadataFieldLimit is the number of bullets metaBlock can write: three for
	// the note and three more for a review. The loop stops at a blank line or at
	// the first line that is not one of those bullets, so a body that happens to
	// start with "- Task:" is still body.
	const metadataFieldLimit = 6
	reviewed := false
metadata:
	for fields := 0; fields < metadataFieldLimit && index < len(lines); fields++ {
		line := strings.TrimSpace(lines[index])
		switch {
		case strings.HasPrefix(line, "- Task:"):
			note.TaskID = strings.TrimSpace(strings.TrimPrefix(line, "- Task:"))
		case strings.HasPrefix(line, "- Author:"):
			note.Author = strings.TrimSpace(strings.TrimPrefix(line, "- Author:"))
		case strings.HasPrefix(line, "- At:"):
			at, err := time.Parse(time.RFC3339, strings.TrimSpace(strings.TrimPrefix(line, "- At:")))
			if err != nil {
				// A malformed timestamp stops the header there; the rest is
				// still worth returning as the body.
				break metadata
			}
			note.At = at.UTC()
		case strings.HasPrefix(line, "- Reviewed:"):
			switch strings.TrimSpace(strings.TrimPrefix(line, "- Reviewed:")) {
			case reviewAccepted, reviewRejected:
				reviewed = true
			default:
				// An unknown status is not a review we can report; skip the
				// line rather than dumping the rest of the header into the body.
			}
		case strings.HasPrefix(line, "- ReviewedBy:"):
			note.ReviewedBy = strings.TrimSpace(strings.TrimPrefix(line, "- ReviewedBy:"))
			reviewed = true
		case strings.HasPrefix(line, "- ReviewedAt:"):
			at, err := time.Parse(time.RFC3339, strings.TrimSpace(strings.TrimPrefix(line, "- ReviewedAt:")))
			if err != nil {
				break metadata
			}
			note.ReviewedAt = at.UTC()
			reviewed = true
		default:
			break metadata
		}
		index++
	}
	if index < len(lines) && strings.TrimSpace(lines[index]) == "" {
		index++
	}
	note.Body = strings.TrimRight(strings.Join(lines[index:], "\n"), "\n")
	// A half-written review (only the reviewer, or only the time) still counts:
	// the note has been touched by a human, and the fields present say by whom
	// and when.
	note.Reviewed = reviewed
	return note
}
