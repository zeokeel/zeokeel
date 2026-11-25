package wechat

// TokenStore persists the bot login token so the adapter reports Connected
// again after a restart without a new QR scan.
//
// The store is deliberately a single file with strict semantics:
//
//   - <dir>/token.json, mode 0600, written to a temp file in the same directory
//     and renamed into place, so a crash can never leave a half-written token
//     that would look like a valid login.
//   - Load never writes. A corrupt file yields ErrTokenStoreCorrupt and the file
//     is left byte-for-byte untouched: the caller decides what to do with it
//     (the module quarantines it as token.json.corrupt-<unix> and starts
//     disconnected), so a bad writer can never destroy the last good login.
//   - Save("") is Clear(): there is no such thing as an empty token, and the QR
//     flow's "log out" is the same code path as clearing.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TokenFileName is the file the store writes inside its directory.
const TokenFileName = "token.json"

// ErrTokenStoreCorrupt means token.json exists but is not a token file. The file
// is left in place; the caller may quarantine it.
var ErrTokenStoreCorrupt = errors.New("wechat: token store corrupt")

// TokenStore is a directory-backed login-token store. It is safe for concurrent
// use: the adapter holds one and Save/Load only ever touch one path.
type TokenStore struct {
	dir string
}

// OpenTokenStore prepares the store under dir, creating the directory (0700) when
// it is missing. It does not read or write token.json — Load does that.
func OpenTokenStore(dir string) (*TokenStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("wechat: token store: dir must not be empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("wechat: token store: create dir %s: %w", dir, err)
	}
	return &TokenStore{dir: dir}, nil
}

// Dir is the directory the store writes into.
func (s *TokenStore) Dir() string { return s.dir }

// Path is the absolute-ish path of token.json.
func (s *TokenStore) Path() string { return filepath.Join(s.dir, TokenFileName) }

// tokenFile is the on-disk shape. UpdatedAt is informational only; nothing keys
// behaviour on it (clock skew must never log a user out).
type tokenFile struct {
	Token     string `json:"token"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// Save writes token atomically with mode 0600. A blank token clears the store.
func (s *TokenStore) Save(token string) error {
	if strings.TrimSpace(token) == "" {
		return s.Clear()
	}
	payload, err := json.Marshal(tokenFile{Token: token, UpdatedAt: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return fmt.Errorf("wechat: token store: encode: %w", err)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("wechat: token store: create dir %s: %w", s.dir, err)
	}
	temp, err := os.CreateTemp(s.dir, TokenFileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("wechat: token store: temp file: %w", err)
	}
	tempName := temp.Name()
	// Any failure below must not leave a stray temp file behind.
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}
	if err := temp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("wechat: token store: chmod temp: %w", err)
	}
	if _, err := temp.Write(payload); err != nil {
		cleanup()
		return fmt.Errorf("wechat: token store: write: %w", err)
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("wechat: token store: sync: %w", err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("wechat: token store: close: %w", err)
	}
	if err := os.Rename(tempName, s.Path()); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("wechat: token store: rename: %w", err)
	}
	return nil
}

// Load returns the stored token. ok is false when nothing is stored yet (a fresh
// install is not an error). A file that exists but is not a token file yields
// ErrTokenStoreCorrupt and is not modified.
func (s *TokenStore) Load() (token string, ok bool, err error) {
	raw, err := os.ReadFile(s.Path())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("wechat: token store: read %s: %w", s.Path(), err)
	}
	var decoded tokenFile
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", false, fmt.Errorf("%w: %s is not JSON: %v", ErrTokenStoreCorrupt, s.Path(), err)
	}
	stored := strings.TrimSpace(decoded.Token)
	if stored == "" {
		return "", false, fmt.Errorf("%w: %s has no token field", ErrTokenStoreCorrupt, s.Path())
	}
	return stored, true, nil
}

// Clear removes the stored token. Clearing a missing store is not an error.
func (s *TokenStore) Clear() error {
	if err := os.Remove(s.Path()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("wechat: token store: clear: %w", err)
	}
	return nil
}
