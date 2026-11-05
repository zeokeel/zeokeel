package session

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// User attachment limits. Files are copied into
// <data dir>/attachments/<session id>/ so the model and the transcript never
// depend on a path the user may move or delete. A turn carries at most five
// files; each file is capped at 25 MiB. Text attachments are injected into the
// prompt up to 64 KiB, images become runtime.ImagePart data URLs.
const (
	// MaxAttachmentBytes is the per-file size cap (25 MiB).
	MaxAttachmentBytes int64 = 25 << 20
	// MaxAttachmentsPerTurn is the per-turn attachment count cap.
	MaxAttachmentsPerTurn = 5
	// MaxTextAttachmentBytes is how much of a text attachment is injected into
	// the model prompt (64 KiB).
	MaxTextAttachmentBytes int64 = 64 << 10
)

// Attachment errors. Callers classify them with errors.Is.
var (
	// ErrAttachmentLimit reports more than MaxAttachmentsPerTurn files pending.
	ErrAttachmentLimit = errors.New("session: too many attachments for one turn")
	// ErrAttachmentTooLarge reports a file larger than MaxAttachmentBytes.
	ErrAttachmentTooLarge = errors.New("session: attachment exceeds the per-file limit")
	// ErrAttachmentNotFound reports an unknown attachment id or one that
	// belongs to another session.
	ErrAttachmentNotFound = errors.New("session: attachment not found")
)

// Attachment is the UI chip type returned by AttachFiles and stored in
// state.Snapshot.Attachments / state.Message.Attachments.
type Attachment = state.Attachment

// AttachFile is the single-file convenience form of AttachFiles.
func (c *Controller) AttachFile(ctx context.Context, sessionID, path string) (state.Attachment, error) {
	attachments, err := c.AttachFiles(ctx, sessionID, []string{path})
	if err != nil {
		return state.Attachment{}, err
	}
	if len(attachments) == 0 {
		return state.Attachment{}, fmt.Errorf("%w: attach file: empty result", ErrInvalidArgument)
	}
	return attachments[len(attachments)-1], nil
}

// AttachFiles copies paths into the session's attachment directory and stores
// one attachment row per file. The whole batch is validated before any copy,
// so a rejected batch never leaves partial rows or files behind.
//
// A controller may hold at most MaxAttachmentsPerTurn pending attachments for
// one session; once a turn is submitted they move onto the user message and a
// new turn starts empty.
func (c *Controller) AttachFiles(ctx context.Context, sessionID string, paths []string) ([]state.Attachment, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("%w: attach files: empty session id", ErrInvalidArgument)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%w: attach files: no paths", ErrInvalidArgument)
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}

	sess, err := c.store.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("session: attach files: %w", err)
	}
	if err := c.ensureView(sess); err != nil {
		return nil, fmt.Errorf("session: attach files: %w", err)
	}

	// Validate everything before touching the data directory.
	pending, err := c.pendingAttachmentRows(sessionID)
	if err != nil {
		return nil, fmt.Errorf("session: attach files: %w", err)
	}
	if len(pending)+len(paths) > MaxAttachmentsPerTurn {
		return nil, fmt.Errorf("%w: %d pending + %d new exceeds %d",
			ErrAttachmentLimit, len(pending), len(paths), MaxAttachmentsPerTurn)
	}
	sources := make([]attachmentSource, 0, len(paths))
	for _, path := range paths {
		src, err := inspectAttachment(path)
		if err != nil {
			return nil, fmt.Errorf("session: attach files: %w", err)
		}
		sources = append(sources, src)
	}

	// Serialize admission only after validation: two simultaneous picks each
	// validate against a snapshot of pending rows, so re-check under the lock.
	c.attachMu.Lock()
	defer c.attachMu.Unlock()
	pending, err = c.pendingAttachmentRows(sessionID)
	if err != nil {
		return nil, fmt.Errorf("session: attach files: %w", err)
	}
	if len(pending)+len(sources) > MaxAttachmentsPerTurn {
		return nil, fmt.Errorf("%w: %d pending + %d new exceeds %d",
			ErrAttachmentLimit, len(pending), len(sources), MaxAttachmentsPerTurn)
	}

	dir, err := c.store.AttachmentDir(sessionID)
	if err != nil {
		return nil, fmt.Errorf("session: attach files: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("session: attach files: create directory %q: %w", dir, err)
	}

	created := make([]store.Attachment, 0, len(sources))
	rollback := func() {
		for _, a := range created {
			_ = c.store.DeleteAttachment(a.ID)
			_ = os.Remove(a.Path)
		}
	}
	for _, src := range sources {
		id := newID()
		dest := filepath.Join(dir, id+"-"+sanitizeAttachmentName(src.name))
		size, err := copyAttachmentFile(src.path, dest)
		if err != nil {
			rollback()
			return nil, fmt.Errorf("session: attach file %q: %w", src.path, err)
		}
		stored, err := c.store.CreateAttachment(store.Attachment{
			ID:        id,
			SessionID: sessionID,
			Name:      src.name,
			Path:      dest,
			MIME:      src.mime,
			Kind:      src.kind,
			Size:      size,
		})
		if err != nil {
			_ = os.Remove(dest)
			rollback()
			return nil, fmt.Errorf("session: attach file %q: %w", src.path, err)
		}
		created = append(created, *stored)
	}

	rows, err := c.pendingAttachmentRows(sessionID)
	if err != nil {
		return nil, fmt.Errorf("session: attach files: %w", err)
	}
	c.setPendingAttachments(sessionID, rows)
	return stateAttachments(rows), nil
}

// RemoveAttachment deletes one pending or sent attachment: the row first, then
// its stored file. It also removes the chip from the live snapshot.
func (c *Controller) RemoveAttachment(ctx context.Context, sessionID, attachmentID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	sessionID = strings.TrimSpace(sessionID)
	attachmentID = strings.TrimSpace(attachmentID)
	if sessionID == "" || attachmentID == "" {
		return fmt.Errorf("%w: remove attachment: empty id", ErrInvalidArgument)
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}

	c.attachMu.Lock()
	defer c.attachMu.Unlock()

	a, err := c.store.GetAttachment(attachmentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%w: %s", ErrAttachmentNotFound, attachmentID)
		}
		return fmt.Errorf("session: remove attachment: %w", err)
	}
	if a.SessionID != sessionID {
		return fmt.Errorf("%w: %s", ErrAttachmentNotFound, attachmentID)
	}
	chip := stateAttachment(*a)
	if err := c.store.DeleteAttachment(attachmentID); err != nil {
		return fmt.Errorf("session: remove attachment: %w", err)
	}
	if err := c.store.RemoveAttachmentFile(a.Path); err != nil {
		return fmt.Errorf("session: remove attachment: %w", err)
	}
	c.removeAttachmentFromSnapshot(sessionID, chip)
	return nil
}

// ListAttachments returns every attachment row of a session, pending and sent.
func (c *Controller) ListAttachments(ctx context.Context, sessionID string) ([]state.Attachment, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("%w: list attachments: empty session id", ErrInvalidArgument)
	}
	rows, err := c.store.ListAttachments(sessionID)
	if err != nil {
		return nil, fmt.Errorf("session: list attachments: %w", err)
	}
	return stateAttachments(rows), nil
}

// PendingAttachments returns the attachments staged for the session's next
// turn.
func (c *Controller) PendingAttachments(ctx context.Context, sessionID string) ([]state.Attachment, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("%w: pending attachments: empty session id", ErrInvalidArgument)
	}
	rows, err := c.pendingAttachmentRows(sessionID)
	if err != nil {
		return nil, fmt.Errorf("session: pending attachments: %w", err)
	}
	return stateAttachments(rows), nil
}

// attachmentSource is one validated file ready to be copied.
type attachmentSource struct {
	path string
	name string
	mime string
	kind string
}

// inspectAttachment validates path and derives its display name, media type
// and kind.
func inspectAttachment(path string) (attachmentSource, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return attachmentSource{}, fmt.Errorf("%w: empty attachment path", ErrInvalidArgument)
	}
	info, err := os.Stat(path)
	if err != nil {
		return attachmentSource{}, fmt.Errorf("inspect %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return attachmentSource{}, fmt.Errorf("%w: %q is not a regular file", ErrInvalidArgument, path)
	}
	if info.Size() > MaxAttachmentBytes {
		return attachmentSource{}, fmt.Errorf("%w: %q is %d bytes", ErrAttachmentTooLarge, filepath.Base(path), info.Size())
	}
	name := filepath.Base(path)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "attachment"
	}
	mediaType := detectAttachmentMIME(path, name)
	return attachmentSource{
		path: path,
		name: name,
		mime: mediaType,
		kind: attachmentKind(name, mediaType),
	}, nil
}

// detectAttachmentMIME prefers the extension (stable for text formats) and
// falls back to sniffing the first bytes.
func detectAttachmentMIME(path, name string) string {
	if ext := strings.ToLower(filepath.Ext(name)); ext != "" {
		if mediaType := mime.TypeByExtension(ext); mediaType != "" {
			return normalizeMIME(mediaType)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close() //nolint:errcheck // metadata detection only
	var buf [512]byte
	n, _ := f.Read(buf[:])
	if n <= 0 {
		return "application/octet-stream"
	}
	return normalizeMIME(http.DetectContentType(buf[:n]))
}

func normalizeMIME(mediaType string) string {
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = mediaType[:i]
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType == "" {
		return "application/octet-stream"
	}
	return mediaType
}

// textAttachmentExtensions are formats whose extension alone marks a file as
// text even when no MIME registration exists in the platform table.
var textAttachmentExtensions = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".json": true,
	".jsonl": true, ".ndjson": true, ".yaml": true, ".yml": true,
	".toml": true, ".csv": true, ".tsv": true, ".xml": true,
	".html": true, ".htm": true, ".css": true, ".scss": true,
	".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true,
	".jsx": true, ".go": true, ".py": true, ".rb": true, ".rs": true,
	".java": true, ".kt": true, ".c": true, ".h": true, ".cpp": true,
	".hpp": true, ".sh": true, ".bash": true, ".zsh": true, ".sql": true,
	".log": true, ".ini": true, ".cfg": true, ".conf": true, ".env": true,
}

// attachmentKind classifies a file as text, image or generic.
func attachmentKind(name, mediaType string) string {
	mediaType = normalizeMIME(mediaType)
	if strings.HasPrefix(mediaType, "image/") {
		return store.AttachmentKindImage
	}
	if strings.HasPrefix(mediaType, "text/") {
		return store.AttachmentKindText
	}
	switch mediaType {
	case "application/json", "application/xml", "application/yaml",
		"application/x-yaml", "application/toml", "application/javascript":
		return store.AttachmentKindText
	}
	if textAttachmentExtensions[strings.ToLower(filepath.Ext(name))] {
		return store.AttachmentKindText
	}
	return store.AttachmentKindFile
}

// sanitizeAttachmentName makes a display name safe as one path component.
func sanitizeAttachmentName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		out = "attachment"
	}
	if len(out) > 80 {
		out = out[len(out)-80:]
	}
	return out
}

// copyAttachmentFile copies src to dest (created exclusively) and enforces the
// per-file cap again in case the file grew after validation.
func copyAttachmentFile(src, dest string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("open source: %w", err)
	}
	defer in.Close() //nolint:errcheck // copy error wins
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create copy: %w", err)
	}
	n, copyErr := io.Copy(out, io.LimitReader(in, MaxAttachmentBytes+1))
	if copyErr != nil {
		_ = out.Close()
		_ = os.Remove(dest)
		return 0, fmt.Errorf("copy bytes: %w", copyErr)
	}
	if n > MaxAttachmentBytes {
		_ = out.Close()
		_ = os.Remove(dest)
		return 0, fmt.Errorf("%w: %q grew beyond %d bytes", ErrAttachmentTooLarge, filepath.Base(src), MaxAttachmentBytes)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dest)
		return 0, fmt.Errorf("close copy: %w", err)
	}
	return n, nil
}

// pendingAttachmentRows lists attachments not yet linked to a message.
func (c *Controller) pendingAttachmentRows(sessionID string) ([]store.Attachment, error) {
	all, err := c.store.ListAttachments(sessionID)
	if err != nil {
		return nil, err
	}
	pending := make([]store.Attachment, 0, len(all))
	for _, a := range all {
		if strings.TrimSpace(a.MessageID) == "" {
			pending = append(pending, a)
		}
	}
	return pending, nil
}

// turnAttachmentInputs loads the pending attachments for a turn and converts
// them into the runtime view: bounded text plus image data URLs. A file that
// disappeared after admission is skipped rather than failing the turn; its
// chip is still linked to the persisted message.
func (c *Controller) turnAttachmentInputs(sessionID string) ([]store.Attachment, []runtime.UserAttachment) {
	rows, err := c.pendingAttachmentRows(sessionID)
	if err != nil {
		return nil, nil
	}
	inputs := make([]runtime.UserAttachment, 0, len(rows))
	for _, a := range rows {
		switch a.Kind {
		case store.AttachmentKindText:
			data, err := c.store.ReadAttachmentData(a, MaxTextAttachmentBytes)
			if err != nil || len(data) == 0 {
				continue
			}
			inputs = append(inputs, runtime.UserAttachment{Name: a.Name, Text: string(data)})
		case store.AttachmentKindImage:
			data, err := c.store.ReadAttachmentData(a, 0)
			if err != nil || len(data) == 0 {
				continue
			}
			url := "data:" + normalizeMIME(a.MIME) + ";base64," + base64.StdEncoding.EncodeToString(data)
			inputs = append(inputs, runtime.UserAttachment{
				Name:  a.Name,
				Image: &runtime.ImagePart{URL: url, Detail: "auto"},
			})
		}
	}
	return rows, inputs
}

// linkAttachments marks rows as sent with messageID and returns their UI
// chips. Persistence failures are ignored: the turn's user message is already
// stored, and the chip should still be visible for the session's lifetime.
func (c *Controller) linkAttachments(rows []store.Attachment, messageID string) []state.Attachment {
	if len(rows) == 0 {
		return nil
	}
	out := make([]state.Attachment, 0, len(rows))
	for _, a := range rows {
		if err := c.store.SetAttachmentMessage(a.ID, messageID); err != nil {
			// The row may have been removed between the turn starting and the
			// prompt being persisted; do not resurrect a chip for it.
			continue
		}
		a.MessageID = messageID
		out = append(out, stateAttachment(a))
	}
	return out
}

// stateAttachments converts store rows to immutable UI chips.
func stateAttachments(rows []store.Attachment) []state.Attachment {
	out := make([]state.Attachment, 0, len(rows))
	for _, a := range rows {
		out = append(out, stateAttachment(a))
	}
	return out
}

func stateAttachment(a store.Attachment) state.Attachment {
	return state.Attachment{
		ID:        a.ID,
		Name:      a.Name,
		MIME:      a.MIME,
		Kind:      a.Kind,
		Size:      a.Size,
		MessageID: a.MessageID,
	}
}

// setPendingAttachments replaces the snapshot's pending chips for sessionID.
// Attachments are copied into the immutable snapshot; the caller's slice may
// be reused afterwards.
func (c *Controller) setPendingAttachments(sessionID string, rows []store.Attachment) {
	chips := stateAttachments(rows)
	c.mu.Lock()
	snap := c.views[sessionID]
	if snap == nil {
		snap = state.NewSnapshot()
		snap.SessionID = sessionID
	}
	next := *snap
	next.SessionID = sessionID
	next.Attachments = chips
	c.views[sessionID] = &next
	current := c.currentID == sessionID
	c.mu.Unlock()
	if current {
		c.notify(sessionID, &next)
	}
}

// removeAttachmentFromSnapshot drops one chip from the pending list and from
// any user message that owns it.
func (c *Controller) removeAttachmentFromSnapshot(sessionID string, chip state.Attachment) {
	c.mu.Lock()
	snap := c.views[sessionID]
	if snap == nil {
		c.mu.Unlock()
		return
	}
	next := *snap
	if len(snap.Attachments) > 0 {
		next.Attachments = filterAttachments(snap.Attachments, chip.ID)
	}
	if len(snap.Messages) > 0 {
		messages := make([]state.Message, len(snap.Messages))
		copy(messages, snap.Messages)
		changed := false
		for i := range messages {
			if len(messages[i].Attachments) == 0 {
				continue
			}
			filtered := filterAttachments(messages[i].Attachments, chip.ID)
			if len(filtered) != len(messages[i].Attachments) {
				messages[i].Attachments = filtered
				changed = true
			}
		}
		if changed {
			next.Messages = messages
		}
	}
	c.views[sessionID] = &next
	current := c.currentID == sessionID
	c.mu.Unlock()
	if current {
		c.notify(sessionID, &next)
	}
}

// filterAttachments returns a copy without the attachment id.
func filterAttachments(in []state.Attachment, id string) []state.Attachment {
	out := make([]state.Attachment, 0, len(in))
	for _, a := range in {
		if a.ID == id {
			continue
		}
		out = append(out, a)
	}
	return out
}
