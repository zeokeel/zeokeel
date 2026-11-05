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

// manifestKey scopes one DSH session to its DSH home using the session file's
// path relative to <DSH home>/sessions, e.g.
// "--data-mygo--/session-<uuid>/session.v4.jsonl.zstd". The full relative path
// (not a guessed "session id") is the key because real DSH session directories
// are named session-<uuid> while older ones use a bare uuid: only the path is
// guaranteed to be unique. Two different data sources may legitimately reuse a
// session id, so the home is part of the key; a moved DSH home re-imports
// (better than silently skipping).
func manifestKey(dshHome, sessionsRoot, sessionFile string) string {
	home := filepath.Clean(dshHome)
	rel, err := filepath.Rel(filepath.Clean(sessionsRoot), sessionFile)
	if err == nil && rel != "." && rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return home + "|" + filepath.ToSlash(rel)
	}
	// A session file outside the standard layout (hand-made export) still gets
	// a stable key from its base name.
	return home + "|" + filepath.Base(sessionFile)
}

// sessionPathIdentity derives a session identity from the file's path under
// the DSH sessions root: "--data-mygo--/session-<uuid>" ->
// "session-<uuid>", and a hand-made "<bucket>/session.v4.jsonl" ->
// "session.v4" (file stem). It is the fallback for sessions whose header has
// no id.
func sessionPathIdentity(sessionsRoot, sessionFile string) (string, error) {
	rel, err := filepath.Rel(filepath.Clean(sessionsRoot), sessionFile)
	if err != nil || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("session file %q is not under %q", sessionFile, sessionsRoot)
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		// Directly inside the sessions root: use the file stem.
		return trimSessionExt(parts[len(parts)-1]), nil
	}
	parent := parts[len(parts)-2]
	if isDSHWorkspaceBucket(parent) {
		// Hand-made export placed directly in a workspace bucket.
		return trimSessionExt(parts[len(parts)-1]), nil
	}
	return parent, nil
}

// trimSessionExt removes the JSONL (and optional .zstd) suffix from a session
// file name.
func trimSessionExt(name string) string {
	name = strings.TrimSuffix(name, ".zstd")
	return strings.TrimSuffix(name, ".jsonl")
}

// isDSHWorkspaceBucket reports whether name is a DSH workspace directory
// ("--data-mygo--", i.e. a decoded path wrapped in double dashes).
func isDSHWorkspaceBucket(name string) bool {
	return strings.HasPrefix(name, "--") && strings.HasSuffix(name, "--") && len(name) > 4
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
	if err := m.migrateLegacyKeys(); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrManifest, path, err)
	}
	m.path = path
	return &m, nil
}

// migrateLegacyKeys rewrites wave-2 manifest keys to the current relative-path
// scheme. The old key was "<home>|<session dir name or session file base>": for
// session-<uuid> directories it used the file base (colliding across every
// session) and for bare-uuid directories the uuid. Without this migration an
// upgrade would compute the new relative-path key, miss the old entry and
// import every previously imported session a second time.
//
// Migration is fail-closed: an entry whose source cannot be reconstructed is
// an invalid manifest and the run stops before creating anything. Entries that
// already use the current scheme are kept as-is; when two keys collapse onto
// one, identical hashes are de-duplicated and conflicting hashes are rejected.
func (m *manifestFile) migrateLegacyKeys() error {
	if m == nil || len(m.Sessions) == 0 {
		return nil
	}
	migrated := make(map[string]manifestEntry, len(m.Sessions))
	for legacyKey, entry := range m.Sessions {
		canonical, changed, err := canonicalManifestKey(legacyKey, entry)
		if err != nil {
			return err
		}
		if existing, ok := migrated[canonical]; ok {
			if !changed {
				return fmt.Errorf("manifest key %q appears twice", canonical)
			}
			if existing.SHA256 != entry.SHA256 || existing.SourceSize != entry.SourceSize {
				return fmt.Errorf("legacy keys for %q disagree about the imported content", canonical)
			}
			continue // duplicate legacy key for the same source: keep one
		}
		_ = changed
		migrated[canonical] = entry
	}
	m.Sessions = migrated
	return nil
}

// canonicalManifestKey maps one stored key to the current scheme. changed
// reports that the key was rewritten; a key already in the current form is
// returned unchanged. An entry that cannot be placed is an error.
func canonicalManifestKey(legacyKey string, entry manifestEntry) (key string, changed bool, err error) {
	home := strings.TrimSpace(entry.DSHHome)
	source := strings.TrimSpace(entry.SourceFile)
	if home == "" || source == "" {
		// Recover a home from the old "<home>|..." prefix when the header fields
		// are missing (older hand-edited manifests), but only if the key suffix
		// still looks like a relative path; otherwise fail closed.
		if pipe := strings.LastIndex(legacyKey, "|"); pipe > 0 {
			candidateHome := legacyKey[:pipe]
			suffix := legacyKey[pipe+1:]
			if strings.ContainsAny(suffix, "/\\") && filepath.IsAbs(candidateHome) {
				return legacyKey, false, nil
			}
		}
		return "", false, fmt.Errorf("manifest entry %q cannot be migrated: missing dsh_home/source_file", legacyKey)
	}
	sessionsRoot := filepath.Join(filepath.Clean(home), sessionsDirName)
	canonical := manifestKey(filepath.Clean(home), sessionsRoot, source)
	if canonical == legacyKey {
		return legacyKey, false, nil
	}
	return canonical, true, nil
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
