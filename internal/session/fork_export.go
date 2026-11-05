package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// ForkSession copies a session and makes the copy current. The copy lives in
// the source session's workspace and is titled "<title> (fork)"; its messages,
// events, tool calls, feedback and attachments are copied by
// store.ForkSession. Opening the fork loads that copied history into the live
// snapshot, so the UI switches to the branch immediately.
//
// A failure to open the new session removes the copy again, so a failed fork
// cannot leave an invisible half-session behind.
func (c *Controller) ForkSession(ctx context.Context, sessionID string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("%w: fork session: empty id", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return "", ErrClosed
	}

	forked, err := c.store.ForkSession(sessionID)
	if err != nil {
		return "", fmt.Errorf("session: fork %q: %w", sessionID, err)
	}
	if _, err := c.OpenSession(ctx, forked.ID); err != nil {
		_ = c.store.DeleteSession(forked.ID)
		return "", fmt.Errorf("session: fork %q: open copy: %w", sessionID, err)
	}
	return forked.ID, nil
}

// RecordFeedback appends one feedback row for sessionID and updates the live
// snapshot so the action bar reflects the new rating without a reopen. An
// empty messageID records a session-level marker under "session".
//
// The controller also mirrors the rating into the legacy
// "feedback.<session>.<message>" setting. Older builds and the existing UI
// tests read that key; keeping both views consistent means a downgrade still
// sees the rating.
func (c *Controller) RecordFeedback(ctx context.Context, sessionID, messageID, rating, note string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID = strings.TrimSpace(sessionID)
	messageID = strings.TrimSpace(messageID)
	rating = strings.TrimSpace(rating)
	if sessionID == "" {
		return fmt.Errorf("%w: record feedback: empty session id", ErrInvalidArgument)
	}
	if rating == "" {
		return fmt.Errorf("%w: record feedback: empty rating", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}

	settingID := messageID
	if settingID == "" {
		settingID = "session"
	}
	if _, err := c.store.AppendFeedback(store.Feedback{
		SessionID: sessionID,
		MessageID: settingID,
		Rating:    rating,
		Note:      strings.TrimSpace(note),
	}); err != nil {
		return fmt.Errorf("session: record feedback for %q: %w", sessionID, err)
	}
	if err := c.store.SetSetting(feedbackSettingKey(sessionID, settingID), rating); err != nil {
		return fmt.Errorf("session: record feedback for %q: store legacy setting: %w", sessionID, err)
	}

	var updated *state.Snapshot
	c.mu.Lock()
	if snap := c.views[sessionID]; snap != nil {
		feedback := make(map[string]string, len(snap.Feedback)+1)
		for id, value := range snap.Feedback {
			feedback[id] = value
		}
		feedback[settingID] = rating
		next := *snap
		next.Feedback = feedback
		c.views[sessionID] = &next
		updated = &next
	}
	c.mu.Unlock()
	if updated != nil {
		c.notify(sessionID, updated)
	}
	return nil
}

// Feedback returns the persisted feedback rows for a session in append order.
func (c *Controller) Feedback(sessionID string) ([]store.Feedback, error) {
	return c.store.ListFeedback(sessionID)
}

// feedbackSettingKey mirrors a rating into the settings table for builds that
// predate the feedback rows. It is also what the existing message-action
// acceptance test inspects.
func feedbackSettingKey(sessionID, messageID string) string {
	return "feedback." + sessionID + "." + messageID
}

// ExportSession writes the session's durable history as JSONL to path. The
// file contains one session header record followed by its messages,
// attachments (including their embedded bytes), events and feedback, each line
// a standalone JSON object tagged with "type".
//
// When path is omitted the export is written under
// <store dir>/exports/<session-id>.jsonl. The write is atomic (a sibling
// temporary file is fsynced and renamed), so a crash cannot leave a truncated
// export and a failed export does not overwrite an older good file.
func (c *Controller) ExportSession(ctx context.Context, sessionID string, path ...string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("%w: export session: empty id", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}

	target := ""
	for _, candidate := range path {
		if strings.TrimSpace(candidate) != "" {
			target = candidate
			break
		}
	}
	if target == "" {
		name := filepath.Base(sessionID)
		if name == "." || name == ".." || name == string(filepath.Separator) {
			return fmt.Errorf("%w: export session: invalid id %q", ErrInvalidArgument, sessionID)
		}
		dir := filepath.Join(c.store.Dir(), "exports")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("session: export %q: create exports directory: %w", sessionID, err)
		}
		target = filepath.Join(dir, name+".jsonl")
	}

	data, err := c.store.ExportSessionJSONL(sessionID)
	if err != nil {
		return fmt.Errorf("session: export %q: %w", sessionID, err)
	}
	if err := writeExportFile(target, data); err != nil {
		return fmt.Errorf("session: export %q: %w", sessionID, err)
	}
	return nil
}

// writeExportFile writes data to path atomically with mode 0600.
func writeExportFile(path string, data []byte) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%w: export: empty path", ErrInvalidArgument)
	}
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, ".zeokeel-export-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary export: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("set export permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write export: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync export: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close export: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		// Windows cannot replace an existing file with os.Rename. Fall back
		// to removing the old export and retrying, so re-exporting to the
		// same chosen path works there too.
		if removeErr := os.Remove(path); removeErr == nil || os.IsNotExist(removeErr) {
			if retryErr := os.Rename(tmpName, path); retryErr == nil {
				return nil
			}
		}
		_ = os.Remove(tmpName)
		return fmt.Errorf("replace export: %w", err)
	}
	return nil
}
