package users

// The durable half of the roster: one versioned JSON document, <dir>/users.json,
// mode 0600 inside a 0700 directory, rewritten atomically (temp file + rename)
// on every mutation so a crash cannot leave a half-written file that still
// parses.
//
// The same two decisions as the bot store, for the same reasons:
//
//   - The loader is strict, not lenient. A row this build cannot honor makes the
//     whole document contracts.ErrUserStoreCorrupt: a role is a permission, so
//     silently dropping a member would silently remove — or worse, keep — an
//     authority the file's author did not intend. A roster with no owner at all
//     is corrupt too: only hand editing can produce it, and it cannot be
//     administered.
//   - Open never writes. The seeded owner exists in memory only, so a fresh
//     product has an account before anything is saved, and a corrupt document is
//     never overwritten just by starting up.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// FileName is the store document inside the store directory.
const FileName = "users.json"

// storeVersion is the on-disk schema version. A document without it, or with any
// other value, is corrupt: guessing at a future/older layout is how a roster
// silently changes who may act.
const storeVersion = 1

// storeDocument is the on-disk shape. Users is a list (not a map) so the file
// stays inspectable by a human, and it is written sorted so an unchanged roster
// writes an identical file.
type storeDocument struct {
	Version int              `json:"version"`
	Users   []contracts.User `json:"users"`
}

// Open builds a service whose records live in dir/users.json. An empty dir is an
// in-memory store: the same Service without the durable half.
//
// A missing document is a fresh start (the seeded owner only). A document that
// exists but cannot be trusted is contracts.ErrUserStoreCorrupt together with a
// usable store: the product keeps running with the seeded owner, and the caller
// (the module) may quarantine the file before the next write replaces it.
func Open(dir string) (*Service, error) {
	service := NewService()
	if strings.TrimSpace(dir) == "" {
		return service, nil
	}
	service.path = filepath.Join(dir, FileName)

	raw, err := os.ReadFile(service.Path())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return service, nil
		}
		return nil, fmt.Errorf("users: read user store: %w", err)
	}
	var document storeDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return service, fmt.Errorf("%w: %s is not JSON: %v", contracts.ErrUserStoreCorrupt, service.Path(), err)
	}
	if document.Version != storeVersion {
		return service, fmt.Errorf("%w: %s has version %d, want %d", contracts.ErrUserStoreCorrupt, service.Path(), document.Version, storeVersion)
	}

	loaded := make(map[string]contracts.User, len(document.Users))
	for _, user := range document.Users {
		normalized, err := normalizeUser(user)
		if err != nil {
			return service, fmt.Errorf("%w: %s: user %q: %v", contracts.ErrUserStoreCorrupt, service.Path(), user.ID, err)
		}
		if _, duplicate := loaded[normalized.ID]; duplicate {
			return service, fmt.Errorf("%w: %s: duplicate user %q", contracts.ErrUserStoreCorrupt, service.Path(), normalized.ID)
		}
		loaded[normalized.ID] = normalized
	}
	// A roster with members but no owner could not have been produced through
	// Put (which refuses to demote the last owner); it was edited by hand, and
	// nobody in it could manage bots or decide a budget. A completely empty list
	// is different: that is a document the seed can legitimately fill.
	if len(loaded) > 0 && !hasOwner(loaded) {
		return service, fmt.Errorf("%w: %s: no user has the %q role", contracts.ErrUserStoreCorrupt, service.Path(), contracts.UserRoleOwner)
	}

	// The stored roster wins over the seed. Only an empty document falls through
	// to seedLocked, so a roster arranged by hand is never re-opened with an
	// extra local owner added.
	service.users = loaded
	service.seedLocked()
	return service, nil
}

// Path is the document the store writes ("" for an in-memory store).
func (s *Service) Path() string { return s.path }

// hasOwner reports whether any loaded record carries the owner role.
func hasOwner(loaded map[string]contracts.User) bool {
	for _, user := range loaded {
		if user.Role == contracts.UserRoleOwner {
			return true
		}
	}
	return false
}

// saveLocked writes the whole roster atomically. The caller holds the lock, so
// two writers can never interleave a rename with a stale snapshot.
func (s *Service) saveLocked() error {
	if s.path == "" {
		return nil
	}
	document := storeDocument{Version: storeVersion}
	document.Users = make([]contracts.User, 0, len(s.users))
	for _, user := range s.users {
		document.Users = append(document.Users, user)
	}
	sort.Slice(document.Users, func(i, j int) bool { return document.Users[i].ID < document.Users[j].ID })

	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("users: encode user store: %w", err)
	}
	encoded = append(encoded, '\n')

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("users: create data dir: %w", err)
	}
	temp, err := os.CreateTemp(dir, FileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("users: temp file: %w", err)
	}
	tempName := temp.Name()
	// Every failure path below must remove the temp file: a stray file is both
	// clutter and a second copy of the roster.
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}
	if err := temp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("users: chmod temp: %w", err)
	}
	if _, err := temp.Write(encoded); err != nil {
		cleanup()
		return fmt.Errorf("users: write user store: %w", err)
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("users: sync user store: %w", err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("users: close user store: %w", err)
	}
	if err := os.Rename(tempName, s.path); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("users: replace user store: %w", err)
	}
	return nil
}
