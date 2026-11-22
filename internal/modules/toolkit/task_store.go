package toolkit

// Product-side helpers for the durable shared task board.
//
// The board document itself (JSON shape, atomic writes, replay into a fresh rig
// task manager, tombstones, id renumbering, corrupt-document quarantine and the
// legacy-document content merge) is owned by the Rig SDK package
// github.com/zeokeel/zeokeel-rig/tools/team/taskstore, one Store per session.
//
// What stays here is the product's upgrade journal around the v1 process-wide
// document (claim/target/done marker), the bounded per-session directory
// pruning and the small file helpers the journal needs. The journal exists
// because a product release may have crashed between claiming the v1 document
// and persisting it into a session board; the SDK merge is idempotent, but the
// claim/target files tell the next process where the tasks were headed.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel-rig/tools/team/taskstore"
)

// taskBoardFilePath returns the SDK document path for one board scope: the
// process-wide legacy document for an empty session id, and
// team-tasks/<sanitized-session>.json otherwise.
func taskBoardFilePath(dataDir, sessionID string) string {
	return taskstore.New(dataDir, sessionID).Path()
}

// taskBoardDocumentUsable reports whether path looks like a readable board
// document with a supported schema version, without modifying anything. The UI
// read path uses it so a frame callback never quarantines a corrupt document,
// and journal recovery uses it to reject an unusable claim before migrating.
func taskBoardDocumentUsable(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return false
	}
	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.Version == taskstore.DocumentVersion
}

// fileExists reports whether path names an existing file.
func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// removeFile removes path, logging (not failing) unexpected errors.
func removeFile(path string, logf func(string, ...any)) {
	if strings.TrimSpace(path) == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) && logf != nil {
		logf("[toolkit] remove %s: %v", path, err)
	}
}

// writeFileAtomic writes one small file through a temp file + rename so a crash
// never leaves a partial journal/marker.
func writeFileAtomic(path string, data []byte) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

// taskBoardDocumentName recognizes a board document and its quarantined
// backups (`<name>.json`, `<name>.json.corrupt-<ts>`).
func taskBoardDocumentName(name string) bool {
	return strings.HasSuffix(name, ".json") || strings.Contains(name, ".json.corrupt-")
}

// pruneTaskBoardDir bounds the per-session documents: it keeps at most keep
// files, drops documents older than ttl and returns the retained paths (newest
// first). It runs at module activation, never on a UI frame.
func pruneTaskBoardDir(dir string, keep int, ttl time.Duration, logf func(string, ...any)) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type document struct {
		path    string
		modTime time.Time
	}
	documents := make([]document, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !taskBoardDocumentName(entry.Name()) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		documents = append(documents, document{
			path:    filepath.Join(dir, entry.Name()),
			modTime: info.ModTime(),
		})
	}
	sort.Slice(documents, func(i, j int) bool { return documents[i].modTime.After(documents[j].modTime) })
	cutoff := time.Now().Add(-ttl)
	kept := make([]string, 0, len(documents))
	for index, document := range documents {
		if index >= keep || document.modTime.Before(cutoff) {
			if removeErr := os.Remove(document.path); removeErr != nil && !os.IsNotExist(removeErr) {
				logf("[toolkit] prune task board %s: %v", document.path, removeErr)
			}
			continue
		}
		kept = append(kept, document.path)
	}
	return kept
}
