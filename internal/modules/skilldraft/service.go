// Package skilldraft is the drafts half of "从任务沉淀技能" (plan G12): when a
// task finishes, the completion card offers "存为技能", which proposes a skill
// draft — a name, a one-line summary and a six-section Markdown body.
//
// Nothing here teaches the agent anything. A draft is a proposal that a human
// accepts or rejects; accepting it records the decision and keeps the Markdown
// byte for byte, and turning that accepted Markdown into a real
// <dataDir>/skills/<slug>/SKILL.md is the skills editor's job (contracts.
// SkillsEditor), driven by a human. The split is deliberate: a task that went
// well is evidence that a skill might be worth having, not permission to add one
// to the agent's instructions.
//
// Storage is one JSON document (<dir>/drafts.json, mode 0600, atomic rewrite).
// Losing it costs only proposals, so a corrupt file never bricks the product: it
// is reported as contracts.ErrSkillDraftStoreCorrupt together with an empty,
// usable store.
package skilldraft

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Validation budgets. Names are slugs (a directory name, so the alphabet is
// deliberately narrow); summary and body are rune counts so a CJK draft gets the
// same room as a Latin one.
const (
	// MinNameRunes and MaxNameRunes bound the [a-z0-9-]{2,64} rule.
	MinNameRunes = 2
	// MaxNameRunes is the slug cap.
	MaxNameRunes = 64
	// MaxSummaryRunes is the one-line excerpt budget.
	MaxSummaryRunes = 200
	// MaxBodyRunes is the raw draft budget, checked before the six-section body
	// is rendered (the rendered body is slightly larger by construction).
	MaxBodyRunes = 20000
	// MaxTaskIDRunes bounds the provenance field so a draft cannot smuggle a
	// document in through the task id.
	MaxTaskIDRunes = 200
)

// namePattern is the skill slug rule, stated once so validation and any future
// UI check cannot drift: lower-case ASCII, digits and dashes, 2..64 of them.
var namePattern = regexp.MustCompile(`^[a-z0-9-]{2,64}$`)

// Service stores skill drafts. The in-memory map is the running source of truth
// and every mutation rewrites the document, so a restart sees exactly what the
// last decision left behind.
type Service struct {
	mu     sync.Mutex
	drafts map[string]contracts.TaskSkillDraft
	// path is the durable document (<dir>/drafts.json); empty means in-memory
	// only (tests, and a product that passed no directory).
	path string
	now  func() time.Time
}

var _ contracts.SkillDraftsService = (*Service)(nil)

// NewService builds an in-memory-only draft store. Open is the durable
// constructor; this one is for tests and for a caller that only wants the
// validation and rendering rules.
func NewService() *Service {
	return &Service{drafts: make(map[string]contracts.TaskSkillDraft), now: time.Now}
}

// Draft validates one proposal and stores it as pending.
//
// The caller supplies raw material, not the finished document: the six-section
// body is rendered here (see body.go), so every draft a human reviews has the
// same shape and the same literal headings. Caller supplied ID, Status,
// Reviewer and a zero CreatedAt are ignored/overwritten — a proposal cannot
// arrive already accepted.
func (s *Service) Draft(ctx context.Context, draft contracts.TaskSkillDraft) (contracts.TaskSkillDraft, error) {
	if err := ctx.Err(); err != nil {
		return contracts.TaskSkillDraft{}, err
	}
	draft.ID = ""
	draft.Status = ""
	draft.Reviewer = ""
	draft.Name = strings.TrimSpace(draft.Name)
	draft.TaskID = strings.TrimSpace(draft.TaskID)
	draft.Summary = strings.TrimSpace(draft.Summary)
	draft.Body = strings.ReplaceAll(draft.Body, "\r\n", "\n")
	if err := validate(draft); err != nil {
		return contracts.TaskSkillDraft{}, err
	}
	if draft.CreatedAt.IsZero() {
		draft.CreatedAt = s.now()
	}
	// The store formats CreatedAt at second precision (RFC 3339), so truncating
	// here keeps Draft's return value equal to the row a later List shows.
	draft.CreatedAt = draft.CreatedAt.UTC().Truncate(time.Second)

	s.mu.Lock()
	defer s.mu.Unlock()
	draft.ID = s.nextID()
	draft.Status = contracts.SkillDraftPending
	draft.Body = renderBody(draft)
	s.drafts[draft.ID] = draft
	if err := s.save(); err != nil {
		// The map already holds the draft; drop it again so a failed write does
		// not leave the running process believing in something a restart will
		// not have.
		delete(s.drafts, draft.ID)
		return contracts.TaskSkillDraft{}, err
	}
	return draft, nil
}

// List returns drafts newest first. limit <= 0 means no limit. The order is
// CreatedAt descending with the id as a stable tie-break, so two drafts written
// in the same second still list deterministically.
func (s *Service) List(ctx context.Context, limit int) ([]contracts.TaskSkillDraft, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	drafts := make([]contracts.TaskSkillDraft, 0, len(s.drafts))
	for _, draft := range s.drafts {
		drafts = append(drafts, draft)
	}
	s.mu.Unlock()

	sort.Slice(drafts, func(i, j int) bool {
		if !drafts[i].CreatedAt.Equal(drafts[j].CreatedAt) {
			return drafts[i].CreatedAt.After(drafts[j].CreatedAt)
		}
		return drafts[i].ID > drafts[j].ID
	})
	if limit > 0 && len(drafts) > limit {
		drafts = drafts[:limit]
	}
	return drafts, nil
}

// Decide records a human decision on one draft and returns it.
//
// Decisions are idempotent: deciding the same way twice keeps the status and
// only refreshes Reviewer (and re-writes the same document), so a double click
// on the completion card is harmless. A rejection is terminal — accepting a
// rejected draft fails with contracts.ErrSkillDraftRejected rather than
// resurrecting it. The body is never re-rendered here: what the human decided on
// is what stays stored.
func (s *Service) Decide(ctx context.Context, id string, accept bool, reviewer string) (contracts.TaskSkillDraft, error) {
	if err := ctx.Err(); err != nil {
		return contracts.TaskSkillDraft{}, err
	}
	id = strings.TrimSpace(id)
	reviewer = strings.TrimSpace(reviewer)
	if id == "" {
		return contracts.TaskSkillDraft{}, unknown("", "draft id is empty")
	}
	if reviewer == "" {
		return contracts.TaskSkillDraft{}, invalid("reviewer is empty")
	}
	if utf8.RuneCountInString(reviewer) > MaxSummaryRunes {
		return contracts.TaskSkillDraft{}, invalid("reviewer is %d runes, limit %d", utf8.RuneCountInString(reviewer), MaxSummaryRunes)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	draft, ok := s.drafts[id]
	if !ok {
		return contracts.TaskSkillDraft{}, unknown(id, "no draft with this id")
	}
	if draft.Status == contracts.SkillDraftRejected && accept {
		return contracts.TaskSkillDraft{}, fmt.Errorf("%w: %q was rejected by %q", contracts.ErrSkillDraftRejected, id, draft.Reviewer)
	}
	status := contracts.SkillDraftRejected
	if accept {
		status = contracts.SkillDraftAccepted
	}
	draft.Status = status
	draft.Reviewer = reviewer
	s.drafts[id] = draft
	if err := s.save(); err != nil {
		return contracts.TaskSkillDraft{}, err
	}
	return draft, nil
}

// validate rejects proposals no human could turn into a skill.
func validate(draft contracts.TaskSkillDraft) error {
	switch {
	case draft.Name == "":
		return invalid("name is empty")
	case !namePattern.MatchString(draft.Name):
		return invalid("name %q is not a slug matching %s", draft.Name, namePattern.String())
	case utf8.RuneCountInString(draft.Summary) > MaxSummaryRunes:
		return invalid("summary is %d runes, limit %d", utf8.RuneCountInString(draft.Summary), MaxSummaryRunes)
	case strings.TrimSpace(draft.Body) == "":
		return invalid("body is empty")
	case utf8.RuneCountInString(draft.Body) > MaxBodyRunes:
		return invalid("body is %d runes, limit %d", utf8.RuneCountInString(draft.Body), MaxBodyRunes)
	case utf8.RuneCountInString(draft.TaskID) > MaxTaskIDRunes:
		return invalid("task id is %d runes, limit %d", utf8.RuneCountInString(draft.TaskID), MaxTaskIDRunes)
	}
	return nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", contracts.ErrSkillDraftInvalid, fmt.Sprintf(format, args...))
}

// unknown reports an id that names no stored draft.
func unknown(id, detail string) error {
	if id == "" {
		return fmt.Errorf("%w: %s", contracts.ErrSkillDraftUnknown, detail)
	}
	return fmt.Errorf("%w: %q: %s", contracts.ErrSkillDraftUnknown, id, detail)
}

// nextID returns an id no draft in the store holds. Random bytes are the natural
// key here: one task can propose several skills, so a task-derived id would
// collide. Must be called with the lock held.
func (s *Service) nextID() string {
	for {
		var buf [8]byte
		if _, err := rand.Read(buf[:]); err != nil {
			// crypto/rand does not fail on any supported platform; falling back
			// to a timestamp keeps Draft usable if it ever does.
			return fmt.Sprintf("%x", s.now().UnixNano())
		}
		id := hex.EncodeToString(buf[:])
		if _, taken := s.drafts[id]; !taken {
			return id
		}
	}
}
