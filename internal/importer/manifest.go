package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Import manifest: idempotency for repeated `zeokeel import` runs.
//
// The manifest is a small JSON file (typically inside the ZeoKeel data
// directory) that records every DSH session this data directory already
// imported, keyed by DSH home + session directory, together with the source
// file's size, mtime and SHA-256. A second run skips a session whose content is
// unchanged instead of creating a duplicate ZeoKeel session (PD-04: repeated
// imports used to duplicate everything). A changed source (new content, e.g. a
// session continued in DSH after the import) has a different hash and is
// imported again.
//
// Options.ManifestPath == "" keeps the historical non-idempotent behaviour.
const manifestFormatVersion = 1

// ErrManifest reports an unreadable or incompatible import manifest. The run
// stops before writing anything so a corrupted manifest cannot silently turn
// into duplicate imports.
var ErrManifest = errors.New("importer: import manifest is invalid")

// sourceIdentity is one DSH session file's identity for idempotency checks.
type sourceIdentity struct {
	Size            int64
	ModTimeUnixNano int64
	SHA256          string
}

// inspectSourceFile hashes the raw session file (compressed bytes for zstd
// sessions: stable for the same on-disk content) and records its size/mtime.
func inspectSourceFile(path string) (sourceIdentity, error) {
	f, err := os.Open(path)
	if err != nil {
		return sourceIdentity{}, fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // read error wins

	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return sourceIdentity{}, fmt.Errorf("hash %q: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		return sourceIdentity{}, fmt.Errorf("stat %q: %w", path, err)
	}
	return sourceIdentity{
		Size:            info.Size(),
		ModTimeUnixNano: info.ModTime().UnixNano(),
		SHA256:          hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

// manifestKey scopes one DSH session id to its DSH home. Two different data
// sources may legitimately use the same session id, so the home is part of the
// key; a moved DSH home re-imports (better than silently skipping).
func manifestKey(dshHome, sessionFile string) string {
	return filepath.Clean(dshHome) + "|" + sessionDirName(sessionFile)
}

// sessionDirName extracts the DSH session id from the session file path. DSH
// stores sessions as <sessions>/<workspace>/<session-id>/session.v4.jsonl[.zstd].
// A hand-made export with the file directly inside a bucket falls back to the
// file name.
func sessionDirName(sessionFile string) string {
	dir := filepath.Base(filepath.Dir(sessionFile))
	if strings.HasPrefix(dir, "session.") || strings.HasPrefix(dir, "session-") {
		return filepath.Base(sessionFile)
	}
	return dir
}

// manifestEntry records one imported session.
type manifestEntry struct {
	DSHHome             string    `json:"dsh_home,omitempty"`
	SessionID           string    `json:"dsh_session_id"`
	SourceFile          string    `json:"source_file"`
	SourceSize          int64     `json:"source_size"`
	SourceMTimeUnixNano int64     `json:"source_mtime_unix_nano,omitempty"`
	SHA256              string    `json:"sha256"`
	ImportedAt          time.Time `json:"imported_at"`
	ZeoKeelSessionID    string    `json:"zeokeel_session_id,omitempty"`
	Title               string    `json:"title,omitempty"`
}

// manifestFile is the on-disk JSON form. path is the load/save destination and
// is not serialized.
type manifestFile struct {
	Version   int                      `json:"version"`
	UpdatedAt time.Time                `json:"updated_at,omitempty"`
	Sessions  map[string]manifestEntry `json:"sessions,omitempty"`

	path string
}

// loadManifest reads the manifest at path. An empty path disables idempotency
// (nil, nil). A missing file is an empty manifest; a corrupt or incompatible
// file is ErrManifest so the caller stops before creating duplicate sessions.
func loadManifest(path string) (*manifestFile, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &manifestFile{
				Version:  manifestFormatVersion,
				Sessions: make(map[string]manifestEntry),
				path:     path,
			}, nil
		}
		return nil, fmt.Errorf("importer: read manifest %q: %w", path, err)
	}
	var m manifestFile
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrManifest, path, err)
	}
	if m.Version != manifestFormatVersion {
		return nil, fmt.Errorf("%w: %s: version %d, this build writes %d", ErrManifest, path, m.Version, manifestFormatVersion)
	}
	if m.Sessions == nil {
		m.Sessions = make(map[string]manifestEntry)
	}
	m.path = path
	return &m, nil
}

// alreadyImported reports whether key was imported with the same content.
func (m *manifestFile) alreadyImported(key string, id sourceIdentity) bool {
	if m == nil || m.Sessions == nil {
		return false
	}
	entry, ok := m.Sessions[key]
	if !ok {
		return false
	}
	return entry.SourceSize == id.Size && entry.SHA256 != "" && entry.SHA256 == id.SHA256
}

// record stores/updates one imported session entry.
func (m *manifestFile) record(key string, entry manifestEntry) {
	if m == nil || key == "" {
		return
	}
	if m.Sessions == nil {
		m.Sessions = make(map[string]manifestEntry)
	}
	m.Sessions[key] = entry
	m.UpdatedAt = time.Now().UTC()
}

// save writes the manifest atomically: a temp file in the same directory,
// fsync, then rename (0600 file, 0700 directory). A crash can therefore never
// leave a half-written manifest that would make the next run skip or duplicate
// sessions unexpectedly.
func (m *manifestFile) save() error {
	if m == nil || strings.TrimSpace(m.path) == "" {
		return nil
	}
	m.Version = manifestFormatVersion
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("importer: encode manifest: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("importer: create manifest directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".dsh-import-manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("importer: create manifest temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("importer: write manifest: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("importer: sync manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("importer: close manifest: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("importer: secure manifest: %w", err)
	}
	if err := os.Rename(tmpName, m.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("importer: replace manifest: %w", err)
	}
	return nil
}
