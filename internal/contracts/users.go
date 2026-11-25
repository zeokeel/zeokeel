package contracts

// The people who share this product (plan M6, second phase: Team Bot,
// Owner/Manager/Collaborator). A user is not a login: this build has one
// browser session, no passwords and no accounts to authenticate. What a user
// record buys is a name for the answer to the only question sharing a bot
// raises: *who* is asking the bot to do something, and therefore what they are
// allowed to decide on their own.
//
// That is why the interesting part of this file is not the struct but the
// action policy at the bottom. A role is a promise about five concrete actions
// — hand work out, agree to it, change the roster, approve shared memory and
// move money — and the promise is written once, here, so the gateway, the task
// board and the memory reviewer all enforce the same table instead of each
// re-deriving "managers can do that, right?".
//
// Two shaping decisions:
//
//   - The role is a plain string with three constants, like BotStatus. It is
//     read from JSON and compared by exact match, so an enum type would only
//     move the parse into every caller without making a bad value impossible.
//   - Allowed() denies by default. An unknown role or an action added without a
//     row returns false: a permission surface must fail closed, because the
//     failure that matters is silently granting, never silently denying.

import (
	"context"
	"errors"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// User role values. They are ordered by how much of the product they may
// change, and the order is the policy: the owner is the account, a manager is a
// trusted person who may sign off work and shared knowledge, and a
// collaborator is someone who may give the shared bot work and nothing else.
const (
	// UserRoleOwner is the account holder. There is exactly one on a fresh
	// install, and the store refuses to demote the last one: an account with no
	// owner has nobody who may manage bots, approve memory or decide a budget.
	UserRoleOwner = "owner"
	// UserRoleManager is a trusted person the owner delegates to: dispatch,
	// approve, review shared memory and decide the budget.
	UserRoleManager = "manager"
	// UserRoleCollaborator is a person who shares the bot's work but not its
	// policy: dispatch only.
	UserRoleCollaborator = "collaborator"
)

// User is one member of the roster: a persistent actor a bot, a task or a
// memory note can be attributed to.
//
// The record is deliberately small. It carries no credential, no contact
// address and no session: authentication is the browser session's business, and
// channel identities live with the connector that binds them (plan §6.11.1).
// What is here is the identity and the role, which is what the permission table
// needs and what a task card shows.
type User struct {
	// ID is the roster identity ([a-z0-9-]{2,64}), the same alphabet bots and
	// tasks use so a task can name its owner with a string both sides accept.
	// It is the record's identity: a "rename" is a different person.
	ID string
	// Name is the display name shown in the roster and next to a task ("小明",
	// "Owner"). At most 80 runes; it must not be empty, because a nameless
	// member cannot be shown or attributed to.
	Name string
	// Role is one of the UserRole* constants. It is a permission promise, so a
	// value this build does not know is invalid rather than "some default".
	Role string
	// CreatedAt is when the member was added. It cannot change: it is a fact
	// about the record's history, not a setting.
	CreatedAt time.Time
}

// UserAction is one thing a person may do to the shared product. The five
// actions are the surface a role decides; anything finer (which tool, which
// file) stays with the existing approval gate and sandbox, not with roles.
type UserAction string

const (
	// ActionDispatch hands work to a bot (create a task for it, send it a
	// message). It does not by itself perform an external action.
	ActionDispatch UserAction = "dispatch"
	// ActionApprove answers an approval: it is a human agreeing to an action
	// that changes the world (write, send, delete, spend). It never runs on a
	// bot's own behalf.
	ActionApprove UserAction = "approve"
	// ActionManageBots changes the roster of bots: create or retire a bot, or
	// retune its tool grant, memory scope and default model.
	ActionManageBots UserAction = "manage-bots"
	// ActionReviewMemory approves a team-scope memory write so it becomes
	// visible to everyone's bots (the shared-memory reviewer in the plan).
	ActionReviewMemory UserAction = "review-memory"
	// ActionDecideBudget raises a spending limit or accepts an overrun.
	ActionDecideBudget UserAction = "decide-budget"
)

// userPermission is one row of the role→action table. All three verdicts sit on
// the same row so the matrix reads as a matrix: a new action is added by
// filling one row, and the compiler-visible shape makes it hard to grant a
// role a power by editing the wrong list.
type userPermission struct {
	action       UserAction
	owner        bool
	manager      bool
	collaborator bool
}

// userPermissions is the whole policy in one place. Each row states the verdict
// and why; "why" is the part that must not be lost, because a role boundary is
// only defensible as an argument, not as a default.
var userPermissions = []userPermission{
	// Dispatch: everyone. Giving a shared bot work is the reason it is shared,
	// and dispatch by itself changes nothing outside the machine — it spends
	// nothing (a budget overrun stops the run and comes back as a decision) and
	// edits no policy. Gating it on a manager would leave the collaborator role
	// with nothing to do.
	{action: ActionDispatch, owner: true, manager: true, collaborator: true},

	// Approve: owner and manager. Approving is the human "yes" the product's
	// whole safety story rests on: it releases a write, a send or a delete. The
	// second phase introduces Manager precisely to share that duty with a
	// trusted person; a collaborator may propose work but must not sign it off,
	// and a bot never answers for a human at all.
	{action: ActionApprove, owner: true, manager: true, collaborator: false},

	// ManageBots: owner only. This is the one action that changes *who may act
	// and what they may touch*: creating a bot, retiring one, or widening a tool
	// grant. A manager who could edit grants could grant themselves every other
	// permission on this table, so the roster stays with the account holder.
	{action: ActionManageBots, owner: true, manager: false, collaborator: false},

	// ReviewMemory: owner and manager. Approving a team-scope write publishes a
	// belief to every bot in the product; that is what the reviewer role exists
	// for, and it is a second pair of eyes by definition. Collaborators write
	// suggestions (that is how they contribute what they learn) but approving
	// their own would make the review queue decorative.
	{action: ActionReviewMemory, owner: true, manager: true, collaborator: false},

	// DecideBudget: owner and manager. Money is the least reversible resource
	// here, and a budget is account-level: a collaborator's dispatch runs
	// against a limit someone else set, and accepting an overrun commits the
	// account, so only the people accountable for it may move the number.
	{action: ActionDecideBudget, owner: true, manager: true, collaborator: false},
}

// Allowed reports whether role may perform action. It is the single enforcement
// point for the table above; callers must not re-derive role rules.
//
// Deny by default: an unknown role, an unknown action, and an action that has no
// row all return false. Failing closed matters because the incident that hurts
// is an accidental grant, and a table lookup that "did not find anything" must
// never read as permission.
func Allowed(role string, action UserAction) bool {
	for _, row := range userPermissions {
		if row.action != action {
			continue
		}
		switch role {
		case UserRoleOwner:
			return row.owner
		case UserRoleManager:
			return row.manager
		case UserRoleCollaborator:
			return row.collaborator
		}
		return false
	}
	return false
}

// UserService is the roster: the persistent people a bot, a task and a memory
// note can be attributed to. It is a small store because the record is small;
// the policy it feeds is Allowed, above.
type UserService interface {
	// Users lists every member, sorted by id. The order is deterministic so the
	// settings page and any diff of the file are stable.
	Users(ctx context.Context) ([]User, error)
	// Put creates or replaces one member and returns the stored record.
	// Invalid members are ErrUserInvalid.
	//
	// The write is a replace, not a patch: every field is taken from the
	// argument. CreatedAt is the exception in one direction — a zero value keeps
	// the stored creation time on an update and means "now" on a create, and a
	// different non-zero time on an existing member is ErrUserInvalid, because
	// when someone joined is history.
	//
	// Put refuses to demote the last owner (ErrUserLastOwner). There is no
	// delete: retiring a member is a later, audited step, and the roster must
	// keep a record of who did things. The refusal is what guarantees the
	// invariant "an account always has at least one owner" that every other
	// permission depends on.
	Put(ctx context.Context, user User) (User, error)
	// Get returns one member by id. An unknown id is ErrUserUnknown: unlike a
	// bot template (whose absence renders as an empty form), every read here is
	// "who is this task/bot/memory note about", and a missing answer is a fault.
	// The returned value is a copy.
	Get(ctx context.Context, id string) (User, error)
}

// KeyUsers is provided by the users module (zeokeel/users) and consumed by the
// gateway (roster UI + action checks), the bot surface (Owner resolution) and
// the memory reviewer.
var KeyUsers = cordis.NewServiceKey[UserService]("zeokeel/users")

// Typed user errors. Callers match with errors.Is, so implementations wrap these
// instead of inventing message-only failures; each one names a different
// recovery (fix the form, refresh the list, promote someone else first).
var (
	// ErrUserInvalid means the record is malformed: an id outside
	// [a-z0-9-]{2,64}, a missing or over-budget name, an unknown role, or an
	// update that tried to rewrite the creation time.
	ErrUserInvalid = errors.New("users: invalid user")
	// ErrUserUnknown means the id names no member. A well-formed id that is
	// absent is still an error here: the caller was asked about a specific
	// person and cannot answer.
	ErrUserUnknown = errors.New("users: unknown user")
	// ErrUserLastOwner means the write would leave the roster with no owner:
	// demoting the only owner. Refusing is the rule, not a limit — an account
	// without an owner can no longer manage bots, approve shared memory or
	// decide a budget, and no other role may restore one.
	ErrUserLastOwner = errors.New("users: refusing to demote the last owner")
	// ErrUserStoreCorrupt means the persisted document exists but cannot be
	// trusted (not JSON, unsupported version, an invalid or duplicated row, or a
	// roster with no owner). Open reports it and still returns a usable store
	// carrying the seeded owner — the roster is configuration, and a bad file
	// must not brick the product — while leaving the bytes in place as evidence
	// for the module to quarantine.
	ErrUserStoreCorrupt = errors.New("users: corrupt user store")
)
