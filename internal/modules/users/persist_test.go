package users

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestOpenMissingFileIsAFreshStore: a product that has never saved a roster
// boots with the seeded owner, and Open itself writes nothing (an absent or
// corrupt document must never be replaced just by starting up).
func TestOpenMissingFileIsAFreshStore(t *testing.T) {
	dir := t.TempDir()
	service, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if service.Path() != filepath.Join(dir, FileName) {
		t.Fatalf("Path = %q, want %q", service.Path(), filepath.Join(dir, FileName))
	}
	if _, statErr := os.Stat(service.Path()); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Open wrote %s (err %v); it must not", service.Path(), statErr)
	}
	members, err := service.Users(context.Background())
	if err != nil {
		t.Fatalf("Users: %v", err)
	}
	if len(members) != 1 || members[0].ID != SeedUserID || members[0].Role != contracts.UserRoleOwner {
		t.Fatalf("Users = %+v, want just the seeded owner", members)
	}
}

// TestPersistenceAcrossReopen is the "restart still knows who is who" half of
// M6: ids, names, roles and creation times survive the process.
func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	joined := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)

	first, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := first.Put(ctx, contracts.User{ID: "alice", Name: "Alice", Role: contracts.UserRoleManager, CreatedAt: joined}); err != nil {
		t.Fatalf("Put alice: %v", err)
	}
	if _, err := first.Put(ctx, contracts.User{ID: "bob", Name: "Bob", Role: contracts.UserRoleCollaborator}); err != nil {
		t.Fatalf("Put bob: %v", err)
	}
	wantUsers, err := first.Users(ctx)
	if err != nil {
		t.Fatalf("Users before reopen: %v", err)
	}

	second, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	gotUsers, err := second.Users(ctx)
	if err != nil {
		t.Fatalf("Users after reopen: %v", err)
	}
	if len(gotUsers) != len(wantUsers) {
		t.Fatalf("Users after reopen = %+v, want %+v", gotUsers, wantUsers)
	}
	for index := range wantUsers {
		want, got := wantUsers[index], gotUsers[index]
		if got.ID != want.ID || got.Name != want.Name || got.Role != want.Role || !got.CreatedAt.Equal(want.CreatedAt) {
			t.Fatalf("member %d after reopen = %+v, want %+v", index, got, want)
		}
	}
	alice, err := second.Get(ctx, "alice")
	if err != nil {
		t.Fatalf("Get alice after reopen: %v", err)
	}
	if !alice.CreatedAt.Equal(joined) {
		t.Fatalf("alice CreatedAt after reopen = %v, want %v", alice.CreatedAt, joined)
	}
	// Reopening must not append a second owner record.
	owners := 0
	for _, member := range gotUsers {
		if member.Role == contracts.UserRoleOwner {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("owners after reopen = %d, want 1 (seeding must be idempotent)", owners)
	}
}

// TestStoreDocumentShape pins the on-disk contract the loader reads back: a
// versioned document with a users list sorted by id.
func TestStoreDocumentShape(t *testing.T) {
	dir := t.TempDir()
	service, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	if _, err := service.Put(ctx, contracts.User{ID: "zed", Name: "Zed", Role: contracts.UserRoleCollaborator}); err != nil {
		t.Fatalf("Put zed: %v", err)
	}
	if _, err := service.Put(ctx, contracts.User{ID: "alice", Name: "Alice", Role: contracts.UserRoleManager}); err != nil {
		t.Fatalf("Put alice: %v", err)
	}

	raw, err := os.ReadFile(service.Path())
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	var document struct {
		Version int              `json:"version"`
		Users   []contracts.User `json:"users"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("store is not JSON: %v\n%s", err, raw)
	}
	if document.Version != storeVersion {
		t.Fatalf("version = %d, want %d", document.Version, storeVersion)
	}
	if len(document.Users) != 3 {
		t.Fatalf("users = %+v, want owner, alice and zed", document.Users)
	}
	ids := make([]string, 0, len(document.Users))
	for _, member := range document.Users {
		ids = append(ids, member.ID)
	}
	if strings.Join(ids, ",") != "alice,owner,zed" {
		t.Fatalf("stored ids = %v, want alice,owner,zed (sorted)", ids)
	}
}

// TestCorruptStoreLeavesAUsableRoster: every document this build cannot honor is
// contracts.ErrUserStoreCorrupt, the store still answers with the seeded owner,
// and the bytes are left untouched as evidence for the module to quarantine.
func TestCorruptStoreLeavesAUsableRoster(t *testing.T) {
	cases := []struct {
		name     string
		document string
	}{
		{name: "not JSON", document: `{"version":1,"users":[`},
		{name: "wrong version", document: `{"version":2,"users":[]}`},
		{name: "unknown role", document: `{"version":1,"users":[{"ID":"alice","Name":"Alice","Role":"admin"}]}`},
		{name: "invalid id", document: `{"version":1,"users":[{"ID":"Alice","Name":"Alice","Role":"owner"}]}`},
		{name: "missing name", document: `{"version":1,"users":[{"ID":"alice","Role":"owner"}]}`},
		{name: "duplicate id", document: `{"version":1,"users":[
			{"ID":"alice","Name":"Alice","Role":"owner"},
			{"ID":"alice","Name":"Alice 2","Role":"manager"}
		]}`},
		{name: "no owner", document: `{"version":1,"users":[
			{"ID":"alice","Name":"Alice","Role":"manager"},
			{"ID":"bob","Name":"Bob","Role":"collaborator"}
		]}`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, FileName)
			if err := os.WriteFile(path, []byte(testCase.document), 0o600); err != nil {
				t.Fatalf("seed corrupt store: %v", err)
			}
			service, err := Open(dir)
			if !errors.Is(err, contracts.ErrUserStoreCorrupt) {
				t.Fatalf("Open(corrupt) = %v, want ErrUserStoreCorrupt", err)
			}
			if service == nil {
				t.Fatal("Open(corrupt) returned no store; the product must keep running")
			}
			members, listErr := service.Users(context.Background())
			if listErr != nil {
				t.Fatalf("Users on a corrupt store: %v", listErr)
			}
			if len(members) != 1 || members[0].ID != SeedUserID || members[0].Role != contracts.UserRoleOwner {
				t.Fatalf("Users on a corrupt store = %+v, want just the seeded owner", members)
			}
			still, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("corrupt store vanished: %v", readErr)
			}
			if string(still) != testCase.document {
				t.Fatalf("Open rewrote the corrupt document:\n%s", still)
			}
		})
	}
}

// TestStoreFilePermissions: the roster is a permission surface, so the document
// is 0600 and its directory 0700.
func TestStoreFilePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "users")
	service, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := service.Put(context.Background(), contracts.User{ID: "alice", Name: "Alice", Role: contracts.UserRoleManager}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	info, err := os.Stat(service.Path())
	if err != nil {
		t.Fatalf("stat store: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("store mode = %o, want 600", mode)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Fatalf("store dir mode = %o, want 700", mode)
	}
	// No temp file survives the write.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != FileName {
			t.Fatalf("stray file %q left behind; only %s should exist", entry.Name(), FileName)
		}
	}
}

// TestFailedWriteRollsBackInMemory: if the document cannot be replaced, the
// process must not believe in a member a restart will not have.
func TestFailedWriteRollsBackInMemory(t *testing.T) {
	dir := t.TempDir()
	service, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	if _, err := service.Put(ctx, contracts.User{ID: "alice", Name: "Alice", Role: contracts.UserRoleManager}); err != nil {
		t.Fatalf("Put alice: %v", err)
	}
	// Make the store directory unwritable so the next save fails after the map
	// was updated. Running as root ignores permission bits, so use a directory
	// where the temp file cannot be created instead.
	if os.Geteuid() == 0 {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatalf("remove store dir: %v", err)
		}
		if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
			t.Fatalf("block store dir: %v", err)
		}
	} else {
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatalf("chmod store dir: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	}

	if _, err := service.Put(ctx, contracts.User{ID: "bob", Name: "Bob", Role: contracts.UserRoleCollaborator}); err == nil {
		t.Fatal("Put succeeded although the store document cannot be written")
	}
	if _, err := service.Get(ctx, "bob"); !errors.Is(err, contracts.ErrUserUnknown) {
		t.Fatalf("Get bob after failed write = %v, want ErrUserUnknown (in-memory state rolled back)", err)
	}
	// The previous member is untouched.
	if _, err := service.Get(ctx, "alice"); err != nil {
		t.Fatalf("Get alice after failed write: %v", err)
	}
}
