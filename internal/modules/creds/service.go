package creds

// The credential store: the single place channel secrets (Feishu/WeChat/
// DingTalk app keys, bot tokens, ...) live between restarts.
//
// Design rules, all of them visible in the code below:
//
//   - One versioned JSON document, <dir>/credentials.json, mode 0600 inside a
//     0700 directory. Every write goes to a temp file in the same directory and
//     is renamed into place, so a crash can never leave a half-written store
//     that looks valid.
//   - A corrupt document is EVIDENCE, not something to paper over: Open returns
//     an empty, fully usable store together with ErrStoreCorrupt and leaves the
//     bytes untouched. The caller decides what to do with the file (the module
//     quarantines it as credentials.json.corrupt-<unix> before writing again),
//     so a bad writer can never silently destroy the last copy.
//   - Reads are deep copies: callers cannot mutate the store through the map
//     they were handed, and the store cannot mutate a map a caller kept.
//   - Secrets never leak through the read model: List returns
//     contracts.CredentialInfo, a type that has no place to put a field value.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// FileName is the store document inside the store directory.
const FileName = "credentials.json"

// storeVersion is the on-disk schema version. A document without it, or with
// any other value, is corrupt: guessing at a future/older layout is how a store
// silently loses secrets.
const storeVersion = 1

// Bounds for one credential. They are deliberately small: a credential is a
// handful of named parameters, not a blob store.
const (
	// MaxFields is how many field entries one credential may hold.
	MaxFields = 16
	// MaxValueBytes is the largest single field value.
	MaxValueBytes = 4096
	// IdentMax is the longest kind/id (the alphabet below allows 1..32 chars).
	IdentMax = 32
)

// ErrStoreCorrupt means the on-disk document exists but cannot be trusted (not
// JSON, wrong version, invalid entry). Open reports it and still returns an
// empty, usable store; the file itself is left byte-for-byte in place.
//
// It is the same sentinel as contracts.ErrStoreCorrupt so callers can match
// either name with errors.Is.
var ErrStoreCorrupt = contracts.ErrStoreCorrupt

// ErrCredentialInvalid means the credential is malformed. Alias of
// contracts.ErrCredentialInvalid.
var ErrCredentialInvalid = contracts.ErrCredentialInvalid

// ErrCredentialNotFound means the (kind, id) pair is well formed but absent.
// Alias of contracts.ErrCredentialNotFound.
var ErrCredentialNotFound = contracts.ErrCredentialNotFound

// identPattern is the kind/id alphabet: a lowercase letter, then up to 31
// lowercase letters, digits, underscores or dashes. It also keeps ids safe as
// file/diagnostic material.
var identPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// Service is a directory-backed credential store. It is safe for concurrent
// use: every method takes the lock and only ever touches one file.
type Service struct {
	mu   sync.RWMutex
	dir  string
	data map[string]map[string]map[string]string // kind -> id -> fields
}

// Open prepares the store under dir (creating it 0700 when missing) and loads
// <dir>/credentials.json.
//
// A missing document is a fresh store, not an error. A document that exists but
// cannot be trusted yields ErrStoreCorrupt AND a usable Service with an empty
// set: the product keeps running, and the caller may quarantine the file and
// reopen. A directory that cannot be created is a hard error and returns nil.
//
// Open never writes: the corrupt bytes are still there after it returns.
func Open(dir string) (*Service, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("creds: store: dir must not be empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creds: store: create dir %s: %w", dir, err)
	}
	service := &Service{dir: dir, data: make(map[string]map[string]map[string]string)}
	if err := service.load(); err != nil {
		return service, err
	}
	return service, nil
}

// Dir is the directory the store lives in.
func (s *Service) Dir() string { return s.dir }

// Path is the document the store writes.
func (s *Service) Path() string { return filepath.Join(s.dir, FileName) }

// storedCredential is one entry of the on-disk document.
type storedCredential struct {
	Kind   string            `json:"kind"`
	ID     string            `json:"id"`
	Fields map[string]string `json:"fields,omitempty"`
}

// storeDocument is the on-disk schema. Credentials are a list (not a map) so the
// file stays inspectable by a human, and sorted so a rewrite is deterministic.
type storeDocument struct {
	Version     int                `json:"version"`
	Credentials []storedCredential `json:"credentials"`
}

// load reads the document into memory. It never writes.
func (s *Service) load() error {
	raw, err := os.ReadFile(s.Path())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("creds: store: read %s: %w", s.Path(), err)
	}
	var document storeDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("%w: %s is not JSON: %v", ErrStoreCorrupt, s.Path(), err)
	}
	if document.Version != storeVersion {
		return fmt.Errorf("%w: %s has version %d, want %d", ErrStoreCorrupt, s.Path(), document.Version, storeVersion)
	}
	loaded := make(map[string]map[string]map[string]string, len(document.Credentials))
	for _, entry := range document.Credentials {
		fields, err := normalizeFields(entry.Fields)
		if err != nil {
			return fmt.Errorf("%w: %s: entry %s/%s: %v", ErrStoreCorrupt, s.Path(), entry.Kind, entry.ID, err)
		}
		if err := validateIdent("kind", entry.Kind); err != nil {
			return fmt.Errorf("%w: %s: entry %s/%s: %v", ErrStoreCorrupt, s.Path(), entry.Kind, entry.ID, err)
		}
		if err := validateIdent("id", entry.ID); err != nil {
			return fmt.Errorf("%w: %s: entry %s/%s: %v", ErrStoreCorrupt, s.Path(), entry.Kind, entry.ID, err)
		}
		byKind, ok := loaded[entry.Kind]
		if !ok {
			byKind = make(map[string]map[string]string)
			loaded[entry.Kind] = byKind
		}
		if _, duplicate := byKind[entry.ID]; duplicate {
			return fmt.Errorf("%w: %s: duplicate entry %s/%s", ErrStoreCorrupt, s.Path(), entry.Kind, entry.ID)
		}
		byKind[entry.ID] = fields
	}
	s.mu.Lock()
	s.data = loaded
	s.mu.Unlock()
	return nil
}

// Get returns one credential. found is false (nil error) when the pair is not
// stored. The value is a deep copy.
func (s *Service) Get(ctx context.Context, kind, id string) (contracts.Credential, bool, error) {
	if err := ctx.Err(); err != nil {
		return contracts.Credential{}, false, err
	}
	if err := validateIdent("kind", kind); err != nil {
		return contracts.Credential{}, false, err
	}
	if err := validateIdent("id", id); err != nil {
		return contracts.Credential{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	fields, ok := s.data[kind][id]
	if !ok {
		return contracts.Credential{}, false, nil
	}
	return contracts.Credential{Kind: kind, ID: id, Fields: cloneFields(fields)}, true, nil
}

// Set stores credential, replacing any credential with the same (kind, id).
//
// The disk write happens before the change is committed in memory: a failed
// write rolls the in-memory state back, so the store never claims to hold
// something a restart would not see.
func (s *Service) Set(ctx context.Context, credential contracts.Credential) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateIdent("kind", credential.Kind); err != nil {
		return err
	}
	if err := validateIdent("id", credential.ID); err != nil {
		return err
	}
	fields, err := normalizeFields(credential.Fields)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	previous, hadPrevious := s.lookupLocked(credential.Kind, credential.ID)
	s.storeLocked(credential.Kind, credential.ID, fields)
	if err := s.persistLocked(); err != nil {
		if hadPrevious {
			s.storeLocked(credential.Kind, credential.ID, previous)
		} else {
			s.removeLocked(credential.Kind, credential.ID)
		}
		return err
	}
	return nil
}

// Delete removes one credential. An unknown (but well-formed) pair is
// ErrCredentialNotFound, so a caller cannot mistake a typo for a successful
// logout.
func (s *Service) Delete(ctx context.Context, kind, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateIdent("kind", kind); err != nil {
		return err
	}
	if err := validateIdent("id", id); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	previous, hadPrevious := s.lookupLocked(kind, id)
	if !hadPrevious {
		return fmt.Errorf("%w: %s/%s", ErrCredentialNotFound, kind, id)
	}
	s.removeLocked(kind, id)
	if err := s.persistLocked(); err != nil {
		s.storeLocked(kind, id, previous)
		return err
	}
	return nil
}

// List returns the identities stored under kind, sorted by id. It cannot return
// a field value: contracts.CredentialInfo has no field map.
func (s *Service) List(ctx context.Context, kind string) ([]contracts.CredentialInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateIdent("kind", kind); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	byKind := s.data[kind]
	infos := make([]contracts.CredentialInfo, 0, len(byKind))
	for id := range byKind {
		infos = append(infos, contracts.CredentialInfo{Kind: kind, ID: id})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].ID < infos[j].ID })
	return infos, nil
}

// lookupLocked returns the stored field map; the caller must hold the lock. The
// map must not be mutated (storeLocked installs copies).
func (s *Service) lookupLocked(kind, id string) (map[string]string, bool) {
	byKind, ok := s.data[kind]
	if !ok {
		return nil, false
	}
	fields, ok := byKind[id]
	return fields, ok
}

func (s *Service) storeLocked(kind, id string, fields map[string]string) {
	byKind, ok := s.data[kind]
	if !ok {
		byKind = make(map[string]map[string]string)
		s.data[kind] = byKind
	}
	byKind[id] = fields
}

func (s *Service) removeLocked(kind, id string) {
	byKind, ok := s.data[kind]
	if !ok {
		return
	}
	delete(byKind, id)
	if len(byKind) == 0 {
		delete(s.data, kind)
	}
}

// persistLocked writes the whole set atomically. The caller holds the write
// lock; the temp+rename sequence runs inside it so two writers can never
// interleave a rename with a stale snapshot.
func (s *Service) persistLocked() error {
	document := storeDocument{Version: storeVersion, Credentials: s.snapshotLocked()}
	payload, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("creds: store: encode: %w", err)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("creds: store: create dir %s: %w", s.dir, err)
	}
	temp, err := os.CreateTemp(s.dir, FileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("creds: store: temp file: %w", err)
	}
	tempName := temp.Name()
	// Every failure path below must remove the temp file: a stray file is both
	// clutter and a second copy of the secrets.
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}
	if err := temp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("creds: store: chmod temp: %w", err)
	}
	if _, err := temp.Write(payload); err != nil {
		cleanup()
		return fmt.Errorf("creds: store: write: %w", err)
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("creds: store: sync: %w", err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("creds: store: close: %w", err)
	}
	if err := os.Rename(tempName, s.Path()); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("creds: store: rename: %w", err)
	}
	return nil
}

// snapshotLocked flattens the set into a deterministic, sorted document.
func (s *Service) snapshotLocked() []storedCredential {
	credentials := make([]storedCredential, 0, len(s.data))
	kinds := make([]string, 0, len(s.data))
	for kind := range s.data {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		ids := make([]string, 0, len(s.data[kind]))
		for id := range s.data[kind] {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			credentials = append(credentials, storedCredential{Kind: kind, ID: id, Fields: s.data[kind][id]})
		}
	}
	return credentials
}

// validateIdent enforces the kind/id alphabet so identities stay path-safe and
// diagnosable.
func validateIdent(field, value string) error {
	if !identPattern.MatchString(value) {
		// The value is echoed: a kind/id is not a secret, and "which identifier
		// was rejected" is the whole point of the message.
		return fmt.Errorf("%w: %s %q must match [a-z][a-z0-9_-]{0,31} (%d chars max)", ErrCredentialInvalid, field, value, IdentMax)
	}
	return nil
}

// normalizeFields validates a field map and returns its private copy. Errors
// never include a value: a rejected secret must not leak into a log line.
func normalizeFields(fields map[string]string) (map[string]string, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	if len(fields) > MaxFields {
		return nil, fmt.Errorf("%w: %d fields, at most %d allowed", ErrCredentialInvalid, len(fields), MaxFields)
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(fields))
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("%w: field key must not be empty", ErrCredentialInvalid)
		}
		value := fields[key]
		if len(value) > MaxValueBytes {
			return nil, fmt.Errorf("%w: field %q is %d bytes, at most %d allowed", ErrCredentialInvalid, key, len(value), MaxValueBytes)
		}
		out[key] = value
	}
	return out, nil
}

// cloneFields returns an independent copy of fields.
func cloneFields(fields map[string]string) map[string]string {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]string, len(fields))
	for key, value := range fields {
		out[key] = value
	}
	return out
}

// Compile-time proof that Service is the store the contracts expect.
var _ contracts.CredentialStore = (*Service)(nil)
