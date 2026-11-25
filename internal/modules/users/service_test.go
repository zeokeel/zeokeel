package users

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fixedNow keeps CreatedAt assertions exact instead of "some time around now".
var fixedNow = time.Date(2026, 11, 25, 9, 30, 0, 0, time.UTC)

func newTestService(t *testing.T) *Service {
	t.Helper()
	service := NewService()
	service.now = func() time.Time { return fixedNow }
	return service
}

// TestFreshStoreSeedsTheOwner pins the one thing a fresh product must be able to
// do before anyone edits the roster: be administered.
func TestFreshStoreSeedsTheOwner(t *testing.T) {
	service := newTestService(t)
	members, err := service.Users(context.Background())
	if err != nil {
		t.Fatalf("Users: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("Users = %+v, want exactly the seeded owner", members)
	}
	owner := members[0]
	if owner.ID != SeedUserID || owner.Name != "Owner" || owner.Role != contracts.UserRoleOwner {
		t.Fatalf("seeded owner = %+v, want id/name/role owner/Owner/owner", owner)
	}
	if owner.CreatedAt.IsZero() {
		// The seed is created when the store is constructed, so it carries the
		// store's first-open time rather than a test clock; what matters is that
		// the record is complete enough to persist.
		t.Fatal("seeded owner has no creation time")
	}
}

// TestSeedingIsIdempotent: seeding never duplicates a member and never overwrites
// a record the user retuned. "owner" is an id, not a title — renaming the
// account holder must survive the next startup.
func TestSeedingIsIdempotent(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if _, err := service.Put(ctx, contracts.User{ID: SeedUserID, Name: "小明", Role: contracts.UserRoleOwner}); err != nil {
		t.Fatalf("Put rename: %v", err)
	}
	service.seedLocked()
	service.seedLocked()

	members, err := service.Users(ctx)
	if err != nil {
		t.Fatalf("Users: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("Users after reseeding = %+v, want one member", members)
	}
	if members[0].ID != SeedUserID || members[0].Name != "小明" {
		t.Fatalf("seeded owner = %+v, want the renamed owner kept", members[0])
	}
}

// TestPutGetList is the ordinary round trip: create, replace, read back,
// deterministic order.
func TestPutGetList(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()

	alice, err := service.Put(ctx, contracts.User{ID: "  alice  ", Name: "Alice", Role: contracts.UserRoleManager})
	if err != nil {
		t.Fatalf("Put alice: %v", err)
	}
	if alice.ID != "alice" {
		t.Fatalf("stored id = %q, want the trimmed alice", alice.ID)
	}
	if !alice.CreatedAt.Equal(fixedNow) {
		t.Fatalf("CreatedAt = %v, want %v", alice.CreatedAt, fixedNow)
	}
	if _, err := service.Put(ctx, contracts.User{ID: "bob", Name: "Bob", Role: contracts.UserRoleCollaborator}); err != nil {
		t.Fatalf("Put bob: %v", err)
	}

	got, err := service.Get(ctx, "alice")
	if err != nil {
		t.Fatalf("Get alice: %v", err)
	}
	if got.ID != "alice" || got.Name != "Alice" || got.Role != contracts.UserRoleManager {
		t.Fatalf("Get alice = %+v, want the stored manager", got)
	}

	// A second Put on the same id replaces name and role; CreatedAt is history.
	updated, err := service.Put(ctx, contracts.User{ID: "alice", Name: "Alice Chen", Role: contracts.UserRoleCollaborator})
	if err != nil {
		t.Fatalf("Put update alice: %v", err)
	}
	if updated.Name != "Alice Chen" || updated.Role != contracts.UserRoleCollaborator {
		t.Fatalf("updated alice = %+v, want the new name and role", updated)
	}
	if !updated.CreatedAt.Equal(fixedNow) {
		t.Fatalf("updated CreatedAt = %v, want the original %v", updated.CreatedAt, fixedNow)
	}

	members, err := service.Users(ctx)
	if err != nil {
		t.Fatalf("Users: %v", err)
	}
	ids := make([]string, 0, len(members))
	for _, member := range members {
		ids = append(ids, member.ID)
	}
	want := []string{"alice", "bob", SeedUserID}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("Users ids = %v, want %v (sorted by id)", ids, want)
	}

	if _, err := service.Get(ctx, "nobody"); !errors.Is(err, contracts.ErrUserUnknown) {
		t.Fatalf("Get unknown = %v, want ErrUserUnknown", err)
	}
}

// TestValidationTable is the record rule: the id alphabet, the name budget and
// the closed role set. Every invalid row must be ErrUserInvalid (fix the form),
// never a different error.
func TestValidationTable(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	longID := strings.Repeat("a", MaxIDChars+1)
	validID := strings.Repeat("a", MaxIDChars)

	cases := []struct {
		name  string
		user  contracts.User
		valid bool // want a valid write
	}{
		{name: "minimal manager", user: contracts.User{ID: "alice", Name: "Alice", Role: contracts.UserRoleManager}, valid: true},
		{name: "dashed id", user: contracts.User{ID: "a-b", Name: "A B", Role: contracts.UserRoleCollaborator}, valid: true},
		{name: "id at the cap", user: contracts.User{ID: validID, Name: "A", Role: contracts.UserRoleOwner}, valid: true},
		{name: "name at the cap", user: contracts.User{ID: "carol", Name: strings.Repeat("名", MaxNameRunes), Role: contracts.UserRoleManager}, valid: true},
		{name: "empty id", user: contracts.User{ID: "", Name: "Nobody", Role: contracts.UserRoleManager}},
		{name: "one-char id", user: contracts.User{ID: "a", Name: "A", Role: contracts.UserRoleManager}},
		{name: "uppercase id", user: contracts.User{ID: "Alice", Name: "Alice", Role: contracts.UserRoleManager}},
		{name: "id with space", user: contracts.User{ID: "al ice", Name: "Alice", Role: contracts.UserRoleManager}},
		{name: "id with underscore", user: contracts.User{ID: "al_ice", Name: "Alice", Role: contracts.UserRoleManager}},
		{name: "id over the cap", user: contracts.User{ID: longID, Name: "A", Role: contracts.UserRoleManager}},
		{name: "empty name", user: contracts.User{ID: "dave", Name: "", Role: contracts.UserRoleCollaborator}},
		{name: "blank name", user: contracts.User{ID: "dave", Name: "   ", Role: contracts.UserRoleCollaborator}},
		{name: "name over the cap", user: contracts.User{ID: "dave", Name: strings.Repeat("名", MaxNameRunes+1), Role: contracts.UserRoleCollaborator}},
		{name: "empty role", user: contracts.User{ID: "dave", Name: "Dave"}},
		{name: "unknown role", user: contracts.User{ID: "dave", Name: "Dave", Role: "admin"}},
		{name: "role is case sensitive", user: contracts.User{ID: "dave", Name: "Dave", Role: "Owner"}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := service.Put(ctx, testCase.user)
			if testCase.valid {
				if err != nil {
					t.Fatalf("Put(%+v) = %v, want success", testCase.user, err)
				}
				return
			}
			if !errors.Is(err, contracts.ErrUserInvalid) {
				t.Fatalf("Put(%+v) = %v, want ErrUserInvalid", testCase.user, err)
			}
		})
	}
}

// TestLastOwnerProtection is the invariant every permission bottoms out in: the
// roster must always keep an owner, so demoting the only one is refused. Adding
// a second owner makes the demotion legal.
func TestLastOwnerProtection(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()

	if _, err := service.Put(ctx, contracts.User{ID: SeedUserID, Name: "Owner", Role: contracts.UserRoleManager}); !errors.Is(err, contracts.ErrUserLastOwner) {
		t.Fatalf("demoting the only owner = %v, want ErrUserLastOwner", err)
	}
	// The refusal changed nothing.
	stored, err := service.Get(ctx, SeedUserID)
	if err != nil {
		t.Fatalf("Get after refused demotion: %v", err)
	}
	if stored.Role != contracts.UserRoleOwner {
		t.Fatalf("role after refused demotion = %q, want owner", stored.Role)
	}

	// A second owner makes the first demotable.
	if _, err := service.Put(ctx, contracts.User{ID: "root", Name: "Root", Role: contracts.UserRoleOwner}); err != nil {
		t.Fatalf("Put second owner: %v", err)
	}
	if _, err := service.Put(ctx, contracts.User{ID: SeedUserID, Name: "Owner", Role: contracts.UserRoleCollaborator}); err != nil {
		t.Fatalf("demote with another owner present: %v", err)
	}
	if _, err := service.Get(ctx, "root"); err != nil {
		t.Fatalf("Get root: %v", err)
	}

	// Now "root" is the last owner: demoting it is refused again.
	if _, err := service.Put(ctx, contracts.User{ID: "root", Name: "Root", Role: contracts.UserRoleManager}); !errors.Is(err, contracts.ErrUserLastOwner) {
		t.Fatalf("demoting the last owner = %v, want ErrUserLastOwner", err)
	}
	// Renaming an owner in place is not a demotion and stays allowed.
	if _, err := service.Put(ctx, contracts.User{ID: "root", Name: "Root Owner", Role: contracts.UserRoleOwner}); err != nil {
		t.Fatalf("renaming the last owner: %v", err)
	}
}

// TestPutRefusesChangingCreationTime: when a member joined is history, the same
// way a bot's creation time is. A zero value means "keep what is stored".
func TestPutRefusesChangingCreationTime(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	joined := fixedNow.Add(-24 * time.Hour)

	if _, err := service.Put(ctx, contracts.User{ID: "alice", Name: "Alice", Role: contracts.UserRoleManager, CreatedAt: joined}); err != nil {
		t.Fatalf("Put with explicit CreatedAt: %v", err)
	}
	if _, err := service.Put(ctx, contracts.User{ID: "alice", Name: "Alice", Role: contracts.UserRoleManager, CreatedAt: fixedNow}); !errors.Is(err, contracts.ErrUserInvalid) {
		t.Fatalf("rewriting CreatedAt = %v, want ErrUserInvalid", err)
	}
	kept, err := service.Put(ctx, contracts.User{ID: "alice", Name: "Alice", Role: contracts.UserRoleCollaborator})
	if err != nil {
		t.Fatalf("Put with zero CreatedAt: %v", err)
	}
	if !kept.CreatedAt.Equal(joined) {
		t.Fatalf("CreatedAt = %v, want the stored %v", kept.CreatedAt, joined)
	}
}

// TestInMemoryStore: an empty dir is the same service without the durable half,
// which is what tests and validation-only callers use.
func TestInMemoryStore(t *testing.T) {
	service, err := Open("")
	if err != nil {
		t.Fatalf("Open(\"\"): %v", err)
	}
	if service.Path() != "" {
		t.Fatalf("Path = %q, want empty for an in-memory store", service.Path())
	}
	if _, err := service.Put(context.Background(), contracts.User{ID: "alice", Name: "Alice", Role: contracts.UserRoleManager}); err != nil {
		t.Fatalf("Put: %v", err)
	}
}

// TestCanceledContext: the roster is called from request paths, so a canceled
// context must stop the work instead of reporting a successful read.
func TestCanceledContext(t *testing.T) {
	service := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Users(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Users(canceled) = %v, want context.Canceled", err)
	}
	if _, err := service.Get(ctx, SeedUserID); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get(canceled) = %v, want context.Canceled", err)
	}
	if _, err := service.Put(ctx, contracts.User{ID: "alice", Name: "Alice", Role: contracts.UserRoleManager}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Put(canceled) = %v, want context.Canceled", err)
	}
}
