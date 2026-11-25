package contracts

import (
	"context"
	"errors"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Skill draft lifecycle (G12). A finished task can propose a skill; nothing
// becomes teachable until a human accepts the draft, and an accepted draft is
// still only a draft — writing the real SKILL.md is a separate step.
const (
	// SkillDraftPending is a draft nobody has decided on yet. Drafting always
	// produces this status; a caller cannot smuggle an accepted draft in.
	SkillDraftPending = "pending"
	// SkillDraftAccepted means a human accepted the draft. The draft is kept
	// verbatim (the file a skill is built from), and re-accepting it is a
	// no-op that only refreshes who decided.
	SkillDraftAccepted = "accepted"
	// SkillDraftRejected means a human refused it. A rejection is terminal:
	// ErrSkillDraftRejected is returned if anything tries to accept it later,
	// so a refused draft can never quietly become a skill.
	SkillDraftRejected = "rejected"
)

// TaskSkillDraft is a skill proposed at the end of a finished task (G12): "这单
// 活干完了，要不要把它变成技能". Body is the six-section Markdown a human edits
// before the draft is accepted; Status is one of the SkillDraft* constants and
// is owned by the store (Draft always writes pending, Decide moves it).
//
// The type is named TaskSkillDraft, not SkillDraft, because SkillDraft in
// skills.go is already the settings page's create payload ({Name, Description,
// Body}). Keeping the two apart matters: that payload is written straight to
// <dataDir>/skills/<slug>/SKILL.md, while a task draft is a proposal that only
// a human acceptance makes worth writing.
type TaskSkillDraft struct {
	// ID is the store's own key for the draft; Draft fills it in. Ids are
	// opaque and random, because one task can propose more than one skill.
	ID string
	// TaskID is the finished task the draft came from; empty means the draft
	// was not derived from a task.
	TaskID string
	// Name is the skill slug ([a-z0-9-]{2,64}), i.e. the directory name the
	// accepted draft would become.
	Name string
	// Summary is the one-line "when to use" excerpt the list shows.
	Summary string
	// Body is the rendered six-section Markdown (When to use / Inputs / Steps /
	// Checks / Failure modes / Example). A human edits this, so Decide keeps it
	// byte for byte.
	Body string
	// CreatedAt is when the draft was recorded.
	CreatedAt time.Time
	// Status is SkillDraftPending, SkillDraftAccepted or SkillDraftRejected.
	Status string
	// Reviewer names the human behind the decision (empty while pending). It is
	// recorded on every Decide call, including a repeat decision, so the last
	// decision on a draft always says who made it.
	Reviewer string
}

// SkillDraftsService is the drafts half of "从任务沉淀技能": propose (Draft),
// review the queue (List) and decide (Decide). It never writes a skill file
// itself — turning an accepted draft into a real skill is the skills editor's
// job, fed by a human.
type SkillDraftsService interface {
	// Draft validates and stores one proposal. The returned draft carries the
	// store's id, status pending and the rendered six-section body; caller
	// supplied ID/Status/Reviewer are ignored.
	Draft(ctx context.Context, draft TaskSkillDraft) (TaskSkillDraft, error)
	// List returns drafts newest first. limit <= 0 means "no limit".
	List(ctx context.Context, limit int) ([]TaskSkillDraft, error)
	// Decide records a human decision. Repeating the same decision is
	// idempotent (status is kept, Reviewer is refreshed). Accepting a rejected
	// draft fails with ErrSkillDraftRejected, and an unknown id fails with
	// ErrSkillDraftUnknown.
	Decide(ctx context.Context, id string, accept bool, reviewer string) (TaskSkillDraft, error)
}

// Skill draft errors are typed so the completion card can tell "this draft is
// not valid" (fix the proposal) from "that draft is gone" (refresh the card)
// from "this draft was refused" (stop offering to save it).
var (
	// ErrSkillDraftInvalid means the proposal failed validation (name slug,
	// summary/body budget, empty body) or a decision carried no reviewer.
	ErrSkillDraftInvalid = errors.New("skilldraft: invalid draft")
	// ErrSkillDraftUnknown means the id names no stored draft.
	ErrSkillDraftUnknown = errors.New("skilldraft: unknown draft")
	// ErrSkillDraftRejected means the draft was refused by a human and cannot
	// be accepted afterwards.
	ErrSkillDraftRejected = errors.New("skilldraft: draft was rejected")
	// ErrSkillDraftStoreCorrupt means the persisted draft file could not be
	// decoded (or was written by a newer build). Open still returns a usable
	// empty store so the product boots; drafts are proposals, so losing them
	// costs nothing but must never brick the process.
	ErrSkillDraftStoreCorrupt = errors.New("skilldraft: corrupt draft store")
)

// KeySkillDrafts is provided by the skilldraft module and consumed by the
// completion card / task UI (and later by the assembly step that turns an
// accepted draft into a skill).
var KeySkillDrafts = cordis.NewServiceKey[SkillDraftsService]("zeokeel/skilldrafts")
