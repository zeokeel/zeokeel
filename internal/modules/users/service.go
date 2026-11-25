package users

// The user roster: the persistent people who share this product (plan M6,
// second phase). One versioned JSON document holds the records; the action
// policy they feed lives in contracts (Allowed).
//
// The rules that shape this file:
//
//   - A fresh store always has an owner. The "owner" record is seeded in
//     memory, so a brand-new product can be operated (manage bots, approve,
//     decide a budget) before anyone edits the roster, and a corrupt document
//     still leaves a usable account behind.
//   - The last owner cannot be demoted. Every role on the permission table
//     bottoms out in "the owner may always do this", so a roster with no owner
//     is unadministrable — the write is refused with
//     contracts.ErrUserLastOwner instead of being repaired silently.
//   - Records are validated on the way in AND on the way out of the file. A row
//     this build cannot honor makes the document corrupt rather than partially
//     loaded: half a roster silently changes who may act.
//   - Identity is immutable. Put replaces a record's name and role, but the id
//     is the person and CreatedAt is history; changing either is
//     contracts.ErrUserInvalid.
//   - There is no delete. Retiring a member is a later, audited step; keeping
//     every record preserves "who did that" in the task history.

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Validation budgets. A member is a small record: an id, a name and a role.
const (
	// MaxIDChars is the [a-z0-9-]{2,64} id cap; the same bound bots use, so an
	// id copied between the two surfaces is always valid on both.
	MaxIDChars = 64
	// MaxNameRunes bounds a display name. It is also what a task card can show
	// without truncating.
	MaxNameRunes = 80
)

// idPattern is the id rule, stated once so Put and the loader cannot drift:
// lower-case ASCII, digits and dashes, 2..64 of them. It also keeps an id safe
// as a diagnostic fragment and as a git-able key in the JSON document.
var idPattern = regexp.MustCompile(`^[a-z0-9-]{2,64}$`)

// SeedUserID is the member every fresh store starts with: the local account
// holder. It is exported so tests and later wiring can name it without
// hard-coding the string.
const SeedUserID = "owner"

// seedName is the seeded owner's display name. It is a name, not a title: the
// user may rename it through Put.
const seedName = "Owner"

// Service is the roster store. It is safe for concurrent use; the map is the
// running source of truth and every mutation rewrites the document.
type Service struct {
	mu    sync.Mutex
	path  string // <dir>/users.json; empty means in-memory only
	users map[string]contracts.User
	now   func() time.Time
}

var _ contracts.UserService = (*Service)(nil)

// NewService builds an in-memory roster carrying the seeded owner. Open is the
// durable constructor; this one is for tests and for a caller that only wants
// the validation rules.
func NewService() *Service {
	service := &Service{
		users: make(map[string]contracts.User),
		now:   time.Now,
	}
	service.seedLocked()
	return service
}

// seedLocked installs the owner record when the roster is empty. The caller
// holds the lock (NewService is single-threaded).
//
// It is deliberately "empty", not "the owner id is missing": once a store has
// been written, the stored roster is authoritative, and re-adding a local owner
// to a roster someone arranged by hand would be a silent change of who may act.
func (s *Service) seedLocked() {
	if len(s.users) > 0 {
		return
	}
	s.users[SeedUserID] = contracts.User{
		ID:        SeedUserID,
		Name:      seedName,
		Role:      contracts.UserRoleOwner,
		CreatedAt: s.now().UTC().Truncate(time.Second),
	}
}

// Users implements contracts.UserService.
func (s *Service) Users(ctx context.Context) ([]contracts.User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]contracts.User, 0, len(s.users))
	for _, user := range s.users {
		out = append(out, user)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Put implements contracts.UserService: it upserts one member.
//
// The order of checks is the order of the caller's recovery: a malformed record
// is ErrUserInvalid (fix the form) whatever else is true; only then does the
// last-owner rule apply (promote someone else first).
func (s *Service) Put(ctx context.Context, user contracts.User) (contracts.User, error) {
	if err := ctx.Err(); err != nil {
		return contracts.User{}, err
	}
	normalized, err := normalizeUser(user)
	if err != nil {
		return contracts.User{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	stored, existed := s.users[normalized.ID]
	if existed {
		// Identity is immutable. A supplied creation time must match the stored
		// one exactly (to the second, the store's resolution); a zero value means
		// "keep the stored time", which is what a Get → modify → Put caller
		// naturally sends.
		if normalized.CreatedAt.IsZero() {
			normalized.CreatedAt = stored.CreatedAt
		} else if !normalized.CreatedAt.Equal(stored.CreatedAt) {
			return contracts.User{}, fmt.Errorf("%w: user %q cannot change its creation time", contracts.ErrUserInvalid, normalized.ID)
		}
		if stored.Role == contracts.UserRoleOwner && normalized.Role != contracts.UserRoleOwner {
			if s.ownerCountLocked() <= 1 {
				return contracts.User{}, fmt.Errorf("%w: user %q is the only owner", contracts.ErrUserLastOwner, stored.ID)
			}
		}
	} else if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = s.now().UTC().Truncate(time.Second)
	}

	s.users[normalized.ID] = normalized
	if err := s.saveLocked(); err != nil {
		// The map already holds the change; revert it so a failed write does not
		// leave the process believing in something a restart will not have.
		if existed {
			s.users[normalized.ID] = stored
		} else {
			delete(s.users, normalized.ID)
		}
		return contracts.User{}, err
	}
	return normalized, nil
}

// Get implements contracts.UserService.
func (s *Service) Get(ctx context.Context, id string) (contracts.User, error) {
	if err := ctx.Err(); err != nil {
		return contracts.User{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[strings.TrimSpace(id)]
	if !ok {
		return contracts.User{}, fmt.Errorf("%w: %q", contracts.ErrUserUnknown, id)
	}
	return user, nil
}

// ownerCountLocked counts the owners in the current roster. The caller holds the
// lock. It is a scan, not a counter: the roster is tiny, and a counter is one
// more field that can drift from the map.
func (s *Service) ownerCountLocked() int {
	count := 0
	for _, user := range s.users {
		if user.Role == contracts.UserRoleOwner {
			count++
		}
	}
	return count
}

// normalizeUser trims and validates one record. Trimming first is deliberate:
// " alice " is the same person as "alice", and accepting the padded form would
// create a second, unreachable-looking record.
func normalizeUser(user contracts.User) (contracts.User, error) {
	user.ID = strings.TrimSpace(user.ID)
	user.Name = strings.TrimSpace(user.Name)
	user.Role = strings.TrimSpace(user.Role)
	if !user.CreatedAt.IsZero() {
		user.CreatedAt = user.CreatedAt.UTC().Truncate(time.Second)
	}
	if err := validateUser(user); err != nil {
		return contracts.User{}, err
	}
	return user, nil
}

// validateUser is the record rule shared by Put and the loader.
func validateUser(user contracts.User) error {
	if !idPattern.MatchString(user.ID) {
		return fmt.Errorf("%w: user id %q must match [a-z0-9-]{2,64}", contracts.ErrUserInvalid, user.ID)
	}
	if user.Name == "" {
		// A nameless member could be attributed to but never shown, which makes
		// every task card and roster row ambiguous.
		return fmt.Errorf("%w: user %q has no name", contracts.ErrUserInvalid, user.ID)
	}
	if runes := utf8.RuneCountInString(user.Name); runes > MaxNameRunes {
		return fmt.Errorf("%w: user name is %d runes, at most %d allowed", contracts.ErrUserInvalid, runes, MaxNameRunes)
	}
	switch user.Role {
	case contracts.UserRoleOwner, contracts.UserRoleManager, contracts.UserRoleCollaborator:
		return nil
	}
	// An unknown role is refused rather than defaulted: every unknown value would
	// be denied by Allowed anyway, and a record nobody can act on is a bug the
	// caller should see at write time.
	return fmt.Errorf("%w: user %q has unknown role %q", contracts.ErrUserInvalid, user.ID, user.Role)
}
