package agent

// Large tool-result spilling for the persisted conversation history.
//
// The old shell wrote tool results over 32 KiB to
// <workspace>/.zeokeel/tool-results/ and persisted only a short head plus the
// file path. ZeoKeel keeps that behavior at exactly one seam: the rig.History
// wrapper the agent run persists through. The live run's conversation and the
// model's view keep the full text (the wrapper only transforms the messages
// handed to history.Append), so the current turn is never degraded; the next
// turn and after a restart read the head + path and can open the file when the
// complete output is needed.
//
// The transform is best effort and idempotent: a missing/read-only workspace
// falls back to persisting the full text with a warning; already-spilled
// messages are left alone; files are content-addressed and the directory keeps
// the 100 most recently written files (mtime prune).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// spillThresholdBytes is the tool-result size above which the persisted
	// history is replaced by a head + path (32 KiB).
	spillThresholdBytes = 32 << 10
	// spillHeadBytes is the complete-prefix size kept in the persisted message.
	spillHeadBytes = 2 << 10
	// spillKeepFiles is the number of spill files kept per workspace.
	spillKeepFiles = 100
	// spillDirName is the workspace-relative spill directory (slash separated).
	spillDirName = ".zeokeel/tool-results"
	// spillMarkerPrefix starts the persisted pointer to the complete result.
	spillMarkerPrefix = "\n…[完整结果: "
	// spillMarkerSuffix closes the persisted pointer.
	spillMarkerSuffix = "]\n"
	// spillDetectMarker marks a message that already carries a spill pointer.
	spillDetectMarker = "[完整结果: "
)

// spillHistory wraps the rig.History the agent loop persists to and spills large
// tool-result messages before handing them to the durable store. Load delegates
// unchanged; the live run keeps its full-text conversation.
type spillHistory struct {
	inner  rig.History
	dir    func(context.Context) string
	logger contracts.Logger

	pruneMu sync.Mutex
}

var _ rig.History = (*spillHistory)(nil)

// newSpillHistory wraps inner; dir resolves the spill directory for a context
// ("" means "no workspace available: fall back to full text").
func newSpillHistory(inner rig.History, dir func(context.Context) string, logger contracts.Logger) *spillHistory {
	if dir == nil {
		dir = func(context.Context) string { return "" }
	}
	if logger == nil {
		logger = discardLogger{}
	}
	return &spillHistory{inner: inner, dir: dir, logger: logger}
}

// Load delegates to the durable history.
func (h *spillHistory) Load(ctx context.Context, sessionID string) ([]rig.Message, error) {
	return h.inner.Load(ctx, sessionID)
}

// Append spills the large tool-result messages of this persistence batch and
// forwards everything to the durable history.
func (h *spillHistory) Append(ctx context.Context, sessionID string, messages ...rig.Message) error {
	transformed := make([]rig.Message, len(messages))
	for index, message := range messages {
		transformed[index] = h.spillMessage(ctx, message)
	}
	return h.inner.Append(ctx, sessionID, transformed...)
}

// spillMessage rewrites one oversized tool-result message using head + path; any
// other message, a message that already carries a pointer, or a message whose
// file could not be written is returned unchanged.
func (h *spillHistory) spillMessage(ctx context.Context, message rig.Message) rig.Message {
	if message.Role != rig.RoleTool {
		return message
	}
	text := message.Text()
	if len([]byte(text)) <= spillThresholdBytes {
		return message
	}
	if strings.Contains(text, spillDetectMarker) {
		return message
	}
	dir := strings.TrimSpace(h.dir(ctx))
	if dir == "" {
		h.logger.Printf("[agent] spill tool result: no current workspace; persisting the full %d-byte result", len([]byte(text)))
		return message
	}
	path, err := writeSpillFile(dir, text)
	if err != nil {
		h.logger.Printf("[agent] spill tool result: %v; persisting the full %d-byte result", err, len([]byte(text)))
		return message
	}
	h.prune(dir)
	return spilledMessage(message, path)
}

// spilledMessage rebuilds message with the head + pointer content while keeping
// its role, tool identity, reasoning and any non-text parts.
func spilledMessage(message rig.Message, path string) rig.Message {
	content := make([]rig.ContentPart, 0, len(message.Content))
	content = append(content, rig.TextPart(spillHead(message.Text())+spillMarkerPrefix+path+spillMarkerSuffix))
	for _, part := range message.Content {
		if part.Type != rig.PartText {
			content = append(content, part)
		}
	}
	spilled := message
	spilled.Content = content
	return spilled
}

// spillHead returns the complete-prefix kept in the persisted message (2 KiB,
// cut on a UTF-8 boundary).
func spillHead(text string) string {
	if len(text) <= spillHeadBytes {
		return text
	}
	cut := spillHeadBytes
	for cut > 0 && (text[cut]&0xC0) == 0x80 {
		cut--
	}
	return text[:cut]
}

// writeSpillFile atomically writes text to a content-addressed file in dir and
// returns its path. The file mode is 0644.
func writeSpillFile(dir, text string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create spill dir %q: %w", dir, err)
	}
	digest := sha256.Sum256([]byte(text))
	name := "result-" + hex.EncodeToString(digest[:])[:16] + ".txt"
	target := filepath.Join(dir, name)
	temporary, err := os.CreateTemp(dir, ".spill-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create spill file: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(0o644); err != nil {
		cleanup()
		return "", fmt.Errorf("chmod spill file: %w", err)
	}
	if _, err := temporary.WriteString(text); err != nil {
		cleanup()
		return "", fmt.Errorf("write spill file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return "", fmt.Errorf("close spill file: %w", err)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		_ = os.Remove(temporaryPath)
		return "", fmt.Errorf("replace spill file: %w", err)
	}
	absolute, err := filepath.Abs(target)
	if err != nil {
		return target, nil
	}
	return absolute, nil
}

// prune keeps the most recent keep spill files (by mtime) and removes the rest;
// failures are ignored because pruning never matters for correctness.
func (h *spillHistory) prune(dir string) {
	h.pruneMu.Lock()
	defer h.pruneMu.Unlock()
	pruneSpillDir(dir, spillKeepFiles)
}

// pruneSpillDir removes all but the newest keep "result-*" files in dir.
func pruneSpillDir(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type spillFile struct {
		path string
		at   time.Time
	}
	files := make([]spillFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "result-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, spillFile{path: filepath.Join(dir, entry.Name()), at: info.ModTime()})
	}
	if len(files) <= keep {
		return
	}
	sort.Slice(files, func(left, right int) bool {
		return files[left].at.After(files[right].at)
	})
	for _, file := range files[keep:] {
		_ = os.Remove(file.path)
	}
}

// spillDir resolves the current workspace's spill directory ("" when no
// workspace is open or the workspace backend is unavailable).
func (s *service) spillDir(ctx context.Context) string {
	if s.workspace != nil {
		workspace, ok, err := s.workspace.Current(ctx)
		if err != nil {
			s.logger.Printf("[agent] spill dir: current workspace: %v", err)
		} else if ok && strings.TrimSpace(workspace.Root) != "" {
			return filepath.Join(workspace.Root, filepath.FromSlash(spillDirName))
		}
	}
	// Headless/ACP sessions have no registered workspace; spill under the
	// product data directory instead of resending oversized tool results in
	// every following model round (verification finding ZK-13).
	if dataDir := strings.TrimSpace(s.dataDir); dataDir != "" {
		return filepath.Join(dataDir, filepath.FromSlash(spillDirName))
	}
	return ""
}

// spilledHistory wraps the agent run's persistence history in the spill
// transform (nil stays nil).
func (s *service) spilledHistory(history rig.History) rig.History {
	if history == nil {
		return nil
	}
	return newSpillHistory(history, s.spillDir, s.logger)
}
