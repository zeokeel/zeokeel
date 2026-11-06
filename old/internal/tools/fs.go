package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

const readFileDescription = `Read a UTF-8 text file inside the workspace and return its contents.

The path is relative to the current working directory (the workspace root by default; the working_directory tool can change it). Directories, binary files (NUL bytes or invalid UTF-8) and files larger than 1 MiB are rejected.

Args JSON schema:
{"type":"object","properties":{"path":{"type":"string","description":"Path relative to the current working directory (workspace root by default) of the file to read."}},"required":["path"],"additionalProperties":false}`

type readFileTool struct {
	sb *sandbox
}

func (t *readFileTool) Name() string        { return "read_file" }
func (t *readFileTool) Description() string { return readFileDescription }

func (t *readFileTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	path, err := t.sb.resolve(a.Path)
	if err != nil {
		return "", fmt.Errorf("read_file: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read_file: %q: %w", a.Path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("read_file: %q is a directory", a.Path)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("read_file: %q is not a regular file", a.Path)
	}
	if info.Size() > MaxReadFileBytes {
		return "", fmt.Errorf("read_file: %q is %d bytes, larger than the %d byte limit", a.Path, info.Size(), MaxReadFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read_file: %q: %w", a.Path, err)
	}
	if len(data) > MaxReadFileBytes {
		return "", fmt.Errorf("read_file: %q is larger than the %d byte limit", a.Path, MaxReadFileBytes)
	}
	if isBinary(data) {
		return "", fmt.Errorf("read_file: %q is not valid UTF-8 text (binary file)", a.Path)
	}
	// Remember what was read so edit_file can require a fresh view: editing a
	// file the model has not seen (or that changed since it was read) risks
	// applying a replacement to content the model is not reasoning about
	// (FP-08 read-before-write).
	recordFileRead(runtime.SessionIDFromContext(ctx), path, data)
	return string(data), nil
}

const writeFileDescription = `Write a UTF-8 text file inside the workspace, creating parent directories as needed.

The path is relative to the current working directory (the workspace root by default; the working_directory tool can change it). Existing files are overwritten.

Args JSON schema:
{"type":"object","properties":{"path":{"type":"string","description":"Path relative to the current working directory (workspace root by default) of the file to write."},"content":{"type":"string","description":"Full file contents."}},"required":["path","content"],"additionalProperties":false}`

type writeFileTool struct {
	sb *sandbox
}

func (t *writeFileTool) Name() string        { return "write_file" }
func (t *writeFileTool) Description() string { return writeFileDescription }

func (t *writeFileTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	path, err := t.sb.resolve(a.Path)
	if err != nil {
		return "", fmt.Errorf("write_file: %w", err)
	}
	perm := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return "", fmt.Errorf("write_file: %q is a directory", a.Path)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("write_file: %q is not a regular file", a.Path)
		}
		perm = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("write_file: %q: %w", a.Path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("write_file: create parent directories for %q: %w", a.Path, err)
	}
	// Atomic write: a crash or full disk leaves the old file (or no file)
	// instead of a half-written one, and the model's write is either fully
	// visible to later tools or not at all (FP-08).
	if err := atomicWriteFile(path, []byte(a.Content), perm); err != nil {
		return "", fmt.Errorf("write_file: %q: %w", a.Path, err)
	}
	// The model authored this content, so count it as a fresh read for the
	// read-before-write guard on edit_file.
	recordFileRead(runtime.SessionIDFromContext(ctx), path, []byte(a.Content))
	return fmt.Sprintf("wrote %d bytes to %s", len(a.Content), t.sb.relative(path)), nil
}

const editFileDescription = `Replace exactly one occurrence of old text with new text in a UTF-8 file inside the workspace.

The path is relative to the current working directory (the workspace root by default; the working_directory tool can change it). The operation fails unless old occurs exactly once. The file must have been read with read_file in this session first, and it must not have changed since that read: call read_file again after an external change instead of editing stale content. Write the replacement with write_file when the whole file changes.

Args JSON schema:
{"type":"object","properties":{"path":{"type":"string","description":"Path relative to the current working directory (workspace root by default) of the file to edit."},"old":{"type":"string","description":"Exact text to replace; must occur exactly once."},"new":{"type":"string","description":"Replacement text."}},"required":["path","old","new"],"additionalProperties":false}`

type editFileTool struct {
	sb *sandbox
}

func (t *editFileTool) Name() string        { return "edit_file" }
func (t *editFileTool) Description() string { return editFileDescription }

func (t *editFileTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
		Old  string `json:"old"`
		New  string `json:"new"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	if a.Old == "" {
		return "", fmt.Errorf("edit_file: old text is required")
	}
	path, err := t.sb.resolve(a.Path)
	if err != nil {
		return "", fmt.Errorf("edit_file: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("edit_file: %q: %w", a.Path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("edit_file: %q is a directory", a.Path)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("edit_file: %q is not a regular file", a.Path)
	}
	if info.Size() > MaxReadFileBytes {
		return "", fmt.Errorf("edit_file: %q is %d bytes, larger than the %d byte limit", a.Path, info.Size(), MaxReadFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("edit_file: %q: %w", a.Path, err)
	}
	if len(data) > MaxReadFileBytes {
		return "", fmt.Errorf("edit_file: %q is larger than the %d byte limit", a.Path, MaxReadFileBytes)
	}
	if isBinary(data) {
		return "", fmt.Errorf("edit_file: %q is not valid UTF-8 text (binary file)", a.Path)
	}
	// Read-before-write (FP-08): the model must have seen this exact content in
	// this session. Checked before matching old text so a stale view reports the
	// real problem (the file changed) instead of a confusing "old text not
	// found". Editing a file the model never read could replace unintended
	// content.
	if sessionID := runtime.SessionIDFromContext(ctx); sessionID != "" {
		record, ok := fileReadFingerprint(sessionID, path)
		if !ok {
			return "", fmt.Errorf("edit_file: read %q with read_file before editing it", a.Path)
		}
		if record != sha256.Sum256(data) {
			return "", fmt.Errorf("edit_file: %q changed since it was read; call read_file again before editing", a.Path)
		}
	}
	content := string(data)
	switch n := strings.Count(content, a.Old); {
	case n == 0:
		return "", fmt.Errorf("edit_file: old text not found in %q", a.Path)
	case n > 1:
		return "", fmt.Errorf("edit_file: old text occurs %d times in %q; exactly one occurrence is required", n, a.Path)
	}
	updated := strings.Replace(content, a.Old, a.New, 1)
	if err := atomicWriteFile(path, []byte(updated), info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("edit_file: write %q: %w", a.Path, err)
	}
	// The edit itself is a fresh view of the file for follow-up edits.
	recordFileRead(runtime.SessionIDFromContext(ctx), path, []byte(updated))
	return fmt.Sprintf("edited %s (%d bytes -> %d bytes)", t.sb.relative(path), len(data), len(updated)), nil
}

// isBinary reports whether data looks like a binary file rather than UTF-8
// text. A NUL byte or invalid UTF-8 is treated as binary.
func isBinary(data []byte) bool {
	return bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data)
}

// atomicWriteFile writes data through a same-directory temporary file and an
// atomic rename, then fsyncs the file before the rename and the directory after
// it. A crash, a full disk or a concurrent reader sees either the old file or
// the complete new content, never a half-written one (FP-08).
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".zeokeel-write-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	// Set the final mode before closing so the renamed file never appears with
	// the temporary 0600 mode.
	if err := tmp.Chmod(perm); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	syncDirectory(dir)
	return nil
}

// syncDirectory fsyncs dir so a rename survives a crash. Best effort: Windows
// cannot open directories this way, and failing to fsync the directory must not
// fail an already-committed write.
func syncDirectory(dir string) {
	if goruntime.GOOS == "windows" {
		return
	}
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// fileReadRecord is one file's content fingerprint at the moment a session read
// it. edit_file only accepts a replacement when the current bytes still match.
type fileReadRecord struct {
	hash [sha256.Size]byte
	at   time.Time
}

// fileReadTrack is one session's read files. lastAt powers whole-session
// eviction; per-file at powers per-session eviction.
type fileReadTrack struct {
	files  map[string]fileReadRecord
	lastAt time.Time
}

const (
	// maxFileReadSessions bounds the process-wide session table so a long-lived
	// app with many sessions cannot grow it without limit.
	maxFileReadSessions = 64
	// maxFileReadsPerSession bounds one session's file table; a session that
	// reads thousands of files keeps the most recent ones.
	maxFileReadsPerSession = 512
)

// fileReads is the in-process read-before-write table. It deliberately lives
// in the tools package (not the sandbox) because a registry rebuild recreates
// tool instances mid-session; the session id from the turn context is the
// stable key.
var fileReads = struct {
	mu       sync.Mutex
	sessions map[string]*fileReadTrack
}{sessions: make(map[string]*fileReadTrack)}

// recordFileRead remembers that sessionID has seen data at path.
func recordFileRead(sessionID, path string, data []byte) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || strings.TrimSpace(path) == "" {
		return
	}
	fileReads.mu.Lock()
	defer fileReads.mu.Unlock()
	if fileReads.sessions == nil {
		fileReads.sessions = make(map[string]*fileReadTrack)
	}
	track := fileReads.sessions[sessionID]
	if track == nil {
		if len(fileReads.sessions) >= maxFileReadSessions {
			evictOldestReadSessionLocked()
		}
		track = &fileReadTrack{files: make(map[string]fileReadRecord)}
		fileReads.sessions[sessionID] = track
	}
	if len(track.files) >= maxFileReadsPerSession {
		evictOldestFileReadLocked(track)
	}
	now := time.Now()
	track.files[path] = fileReadRecord{hash: sha256.Sum256(data), at: now}
	track.lastAt = now
}

// fileReadFingerprint returns the content hash sessionID last saw for path.
func fileReadFingerprint(sessionID, path string) ([sha256.Size]byte, bool) {
	sessionID = strings.TrimSpace(sessionID)
	fileReads.mu.Lock()
	defer fileReads.mu.Unlock()
	track := fileReads.sessions[sessionID]
	if track == nil {
		return [sha256.Size]byte{}, false
	}
	record, ok := track.files[path]
	if !ok {
		return [sha256.Size]byte{}, false
	}
	return record.hash, true
}

// evictOldestReadSessionLocked drops the least recently active session. The
// caller holds fileReads.mu.
func evictOldestReadSessionLocked() {
	oldestID := ""
	var oldest time.Time
	for id, track := range fileReads.sessions {
		if oldestID == "" || track.lastAt.Before(oldest) {
			oldestID, oldest = id, track.lastAt
		}
	}
	if oldestID != "" {
		delete(fileReads.sessions, oldestID)
	}
}

// evictOldestFileReadLocked drops one session's least recently read file. The
// caller holds fileReads.mu.
func evictOldestFileReadLocked(track *fileReadTrack) {
	oldestPath := ""
	var oldest time.Time
	for path, record := range track.files {
		if oldestPath == "" || record.at.Before(oldest) {
			oldestPath, oldest = path, record.at
		}
	}
	if oldestPath != "" {
		delete(track.files, oldestPath)
	}
}
