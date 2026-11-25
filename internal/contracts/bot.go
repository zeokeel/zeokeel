package contracts

// The bot entity (plan G1). A bot is not a session: it is a岗位 that outlives
// every conversation it takes part in.
//
//	Bot = 模板 + 记忆作用域 + 工具授予 + 默认模型
//
// The split between the two types is the whole point:
//
//   - BotTemplate is reusable and shareable. It says what the岗位 *is*
//     (name, role, prompt, default model, the tools the role needs, and the
//     memory scope it works in). Two bots can be created from one template and
//     still drift apart, because...
//   - BotInstance is the persistent, owned entity. It carries its own id (the
//     stable identity a task is assigned to — never a session id), who owns it,
//     when it was created and last seen, and the *resolved* policy it was
//     created with.
//
// Resolution happens once, at create time, and the instance stores the result.
// That is deliberate: a bot's tool grant and memory scope are a permission
// surface, so editing the template later must not silently widen what an
// existing bot may touch. A template edit therefore only affects bots created
// after it — and the reverse is also true, an instance may override any of the
// three policy fields at creation.
//
// Nothing here runs a bot or binds it to a live session. Creating the entity,
// persisting it and validating the rules is this layer's whole job; waking a
// bot and handing it a session is a later step that reads these records.

import (
	"context"
	"errors"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Bot status values. A bot is created active; pausing keeps its identity, its
// memory scope and its grants (so it resumes where it left off) while taking it
// out of dispatch, and retiring is the terminal state: a retired bot keeps its
// record for the history but must never be handed new work.
const (
	// BotStatusActive means the bot may be given work.
	BotStatusActive = "active"
	// BotStatusPaused means the bot is offline on purpose: it keeps its identity
	// and grants, and dispatch skips it.
	BotStatusPaused = "paused"
	// BotStatusRetired means the bot is gone for good. The record is kept (a
	// finished task still names who did it) but nothing may reactivate it.
	BotStatusRetired = "retired"
)

// Memory scopes a bot can be created in. Both map onto the existing memory
// tracks: "agent" is the bot's own private memory (its rules and work log),
// "team" additionally lets it work in the team-shared scope, where changes are
// reviewed rather than written straight through. An empty scope means "follow
// the template", and a template with no scope defaults to agent.
const (
	// BotMemoryScopeAgent is private memory: the bot remembers its own work and
	// nothing else.
	BotMemoryScopeAgent = "agent"
	// BotMemoryScopeTeam runs in the team scope: shared facts are readable, and
	// writes go through the review queue rather than landing directly.
	BotMemoryScopeTeam = "team"
)

// BotTemplate is a reusable岗位 definition: the thing a user picks when they
// say "建一个研究 Bot". It is data, not behaviour — the system prompt is text,
// the tool list is a whitelist, and the memory scope is one of two constants.
type BotTemplate struct {
	// ID is the template slug ([a-z0-9-]{2,64}), e.g. "researcher". It is stable
	// across edits, because instances name it.
	ID string
	// Name is the display name ("研究 Bot", "Lead").
	Name string
	// Role is the short岗位 label shown next to the name ("Lead", "Researcher").
	Role string
	// Description is the one-line purpose: when a human picks a template, this
	// is the sentence they read.
	Description string
	// DefaultModel is the model new instances start on ("" = follow the
	// session's model). It is a hint, not a lock: a model that disappears must
	// not leave the bot unusable.
	DefaultModel string
	// SystemPrompt is the岗位说明 (the six-section "when to use / inputs / steps
	// / checks / failure modes / example" text). Empty means the built-in
	// default prompt.
	SystemPrompt string
	// AllowedTools is the tool whitelist the role needs, in the tool-name
	// alphabet the tool surface already uses (`browser_open`,
	// `mcp__server__tool`, `fs.read`). An empty list means "the read-only
	// default set", never "everything".
	AllowedTools []string
	// MemoryScope is BotMemoryScopeAgent or BotMemoryScopeTeam ("" = agent).
	MemoryScope string
}

// BotInstance is one persistent bot: a template plus its own identity, owner and
// resolved policy. It is never a session id — sessions come and go, a bot does
// not.
type BotInstance struct {
	// ID is the stable bot id ([a-z0-9-]{2,64}), e.g. "bot-1". Tasks reference
	// this, so it cannot change for the life of the record.
	ID string
	// TemplateID names the template this bot was created from. It cannot change
	// either: "what this bot is for" is not something a bot acquires later.
	TemplateID string
	// Name is the bot's own display name; it may differ from the template's.
	Name string
	// Owner identifies who the bot belongs to ("" = the local user / unowned).
	// Team sharing is a later phase; the field exists now so its shape does not
	// change when it lands.
	Owner string
	// MemoryScope is the resolved memory scope (agent or team) the bot works in.
	MemoryScope string
	// DefaultModel is the resolved default model ("" = follow the session).
	DefaultModel string
	// AllowedTools is the resolved tool whitelist. It is the effective grant,
	// stored on the instance: a template edit does not reach back into it.
	AllowedTools []string
	// CreatedAt is when the record was created. It cannot change.
	CreatedAt time.Time
	// LastSeenAt is the last time the bot was observed alive. Zero means "never
	// seen since creation" — the binding step writes this, not creation.
	LastSeenAt time.Time
	// Status is one of the BotStatus* constants.
	Status string
}

// BotService owns template and instance records. Both halves are here because
// they are one document and one lock: an instance can only be created from a
// template that exists, so splitting the interface would invite a caller to
// race the two halves apart.
type BotService interface {
	// Templates lists the known templates, sorted by id. The built-in templates
	// are always present, even before the first write.
	Templates(ctx context.Context) ([]BotTemplate, error)
	// PutTemplate creates or replaces one template. A built-in template may be
	// overridden (a user may want to retune "lead"), but its id stays taken.
	// Invalid templates are ErrBotInvalid.
	PutTemplate(ctx context.Context, template BotTemplate) (BotTemplate, error)
	// Instances lists every bot, sorted by id, including paused and retired
	// ones: the history needs them even when dispatch ignores them.
	Instances(ctx context.Context) ([]BotInstance, error)
	// Create persists a new bot. The template must exist (ErrBotTemplateUnknown),
	// the id must be free (ErrBotDuplicate), and the instance inherits the
	// template's default model, tool whitelist and memory scope for every field
	// it does not set itself.
	Create(ctx context.Context, instance BotInstance) (BotInstance, error)
	// Update changes a bot that already exists (ErrBotUnknown otherwise) and
	// returns the stored record.
	//
	// ID, TemplateID and CreatedAt cannot change: they are the record's identity,
	// and an update that tries is ErrBotInvalid. The rest is the new state, with
	// one rule for what an empty value means: a field whose empty value is a real
	// value is written verbatim (DefaultModel "" = follow the session, Owner "" =
	// unowned), and every other field treats empty as "keep what is stored"
	// (Name, MemoryScope, Status, LastSeenAt). AllowedTools is the exception that
	// proves the rule: nil keeps the stored grant and a non-nil empty slice clears
	// it, so a caller that did not touch the grants cannot clear them by accident,
	// while a caller that means to revoke everything can.
	//
	// The practical shape is Get → modify → Update (what the settings page does).
	// Retiring is terminal: a retired bot cannot be moved to another status.
	Update(ctx context.Context, instance BotInstance) (BotInstance, error)
	// Get returns one bot by id (ErrBotUnknown when absent).
	Get(ctx context.Context, id string) (BotInstance, error)
}

// Bot errors are typed so a UI can tell "the form is wrong" (fix the input) from
// "that bot is gone" (refresh the list) from "that template is gone" (pick
// another) from "that id is taken" (rename).
var (
	// ErrBotInvalid means the record is malformed: a bad id slug, a name/role/
	// model over budget, a tool outside the tool alphabet, an unknown memory
	// scope or status, or an update that tried to change the record's identity.
	ErrBotInvalid = errors.New("bot: invalid bot")
	// ErrBotUnknown means the id names no stored bot.
	ErrBotUnknown = errors.New("bot: unknown bot")
	// ErrBotTemplateUnknown means the instance names no stored template.
	ErrBotTemplateUnknown = errors.New("bot: unknown bot template")
	// ErrBotDuplicate means a bot with that id already exists. Creation never
	// overwrites: silently replacing a bot would replace its grants too.
	ErrBotDuplicate = errors.New("bot: duplicate bot id")
	// ErrBotStoreCorrupt means the persisted document exists but cannot be
	// trusted (not JSON, unsupported version, or a row this build cannot
	// honour). Open reports it and still returns an empty, usable store — bots
	// are configuration, and a bad file must not brick the product — while
	// leaving the bytes in place so an operator can look at them.
	ErrBotStoreCorrupt = errors.New("bot: corrupt bot store")
)

// KeyBots is provided by the bot module. It is the store the settings page reads
// and writes, and the entity a later dispatch step resolves a task's owner
// against.
var KeyBots = cordis.NewServiceKey[BotService]("zeokeel/bots")
