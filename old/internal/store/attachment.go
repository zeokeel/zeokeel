package store

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Attachment kinds persisted in attachments.kind. Text attachments are
// injected into the model prompt (bounded by the caller); image attachments
// become runtime image parts; every other file is metadata-only.
const (
	AttachmentKindText  = "text"
	AttachmentKindImage = "image"
	AttachmentKindFile  = "file"
)

// Attachment is one file the user attached to a session. Bytes live outside
// SQLite, in <data dir>/attachments/<session id>/<id>-<name>; Path points at
// that copy. MessageID is empty while the attachment is pending and is set to
// the owning user message once the turn is sent.
type Attachment struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	MessageID string    `json:"message_id,omitempty"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	MIME      string    `json:"mime"`
	Kind      string    `json:"kind"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

const (
	attachmentDirName = "attachments"
	attachmentColumns = `id, session_id, message_id, name, path, mime, kind, size, created_at`
)

// AttachmentDir returns <data dir>/attachments/<sessionID>, validating the
// session id so the directory can never escape the data directory.
func (s *Store) AttachmentDir(sessionID string) (string, error) {
	if err := validateSessionID(sessionID); err != nil {
		return "", fmt.Errorf("store: attachment directory: %w", err)
	}
	return filepath.Join(s.Dir(), attachmentDirName, sessionID), nil
}

// RemoveAttachmentDir deletes <data dir>/attachments/<sessionID>. A missing
// directory is not an error: sessions without attachments have none.
func (s *Store) RemoveAttachmentDir(sessionID string) error {
	dir, err := s.AttachmentDir(sessionID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("store: remove attachment directory %q: %w", dir, err)
	}
	return nil
}

// RemoveAttachmentFile deletes one stored attachment file. The path must be
// inside <data dir>/attachments; anything else is rejected so a persisted path
// can never be used to delete arbitrary files. A missing file is not an error.
func (s *Store) RemoveAttachmentFile(path string) error {
	abs, err := s.attachmentPath(path)
	if err != nil {
		return err
	}
	if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("store: remove attachment file %q: %w", abs, err)
	}
	return nil
}

// ReadAttachmentData reads at most limit bytes of a stored attachment file.
// A limit <= 0 reads the whole file. The stored path is re-validated against
// the attachments root before reading.
func (s *Store) ReadAttachmentData(a Attachment, limit int64) ([]byte, error) {
	abs, err := s.attachmentPath(a.Path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, fmt.Errorf("store: read attachment %q: %w", abs, err)
	}
	defer f.Close() //nolint:errcheck // read error wins

	var r io.Reader = f
	if limit > 0 {
		r = io.LimitReader(f, limit)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("store: read attachment %q: %w", abs, err)
	}
	return data, nil
}

// attachmentPath resolves path and verifies it stays under the attachment
// root.
func (s *Store) attachmentPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%w: empty attachment path", ErrInvalidArgument)
	}
	root, err := filepath.Abs(filepath.Join(s.Dir(), attachmentDirName))
	if err != nil {
		return "", fmt.Errorf("store: resolve attachment root: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("store: resolve attachment path %q: %w", path, err)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: attachment path %q escapes %q", ErrInvalidArgument, path, root)
	}
	return abs, nil
}

// CreateAttachment persists a row for a file already copied into the
// attachment directory. When a.ID is empty a fresh UUID is assigned; when
// CreatedAt is zero the current time is used.
func (s *Store) CreateAttachment(a Attachment) (*Attachment, error) {
	if strings.TrimSpace(a.SessionID) == "" {
		return nil, fmt.Errorf("%w: create attachment: empty session id", ErrInvalidArgument)
	}
	if strings.TrimSpace(a.Name) == "" {
		return nil, fmt.Errorf("%w: create attachment: empty name", ErrInvalidArgument)
	}
	if strings.TrimSpace(a.Path) == "" {
		return nil, fmt.Errorf("%w: create attachment: empty path", ErrInvalidArgument)
	}
	out := a
	if out.ID == "" {
		id, err := newID()
		if err != nil {
			return nil, err
		}
		out.ID = id
	}
	if out.CreatedAt.IsZero() {
		out.CreatedAt = nowUTC()
	} else {
		out.CreatedAt = out.CreatedAt.UTC()
	}
	if out.Kind == "" {
		out.Kind = AttachmentKindFile
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: create attachment: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	ok, err := sessionExists(tx, out.SessionID)
	if err != nil {
		return nil, fmt.Errorf("store: create attachment: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("store: create attachment: session %q: %w", out.SessionID, ErrNotFound)
	}
	var messageID any
	if strings.TrimSpace(out.MessageID) != "" {
		messageID = out.MessageID
	}
	if _, err := tx.Exec(
		`INSERT INTO attachments(`+attachmentColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		out.ID, out.SessionID, messageID, out.Name, out.Path, out.MIME, out.Kind, out.Size,
		encodeTime(out.CreatedAt),
	); err != nil {
		return nil, fmt.Errorf("store: create attachment for session %q: insert: %w", out.SessionID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: create attachment for session %q: commit: %w", out.SessionID, err)
	}
	return &out, nil
}

// SetAttachmentMessage links a pending attachment to the user message it was
// sent with. An empty messageID clears the link.
func (s *Store) SetAttachmentMessage(id, messageID string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: set attachment message: empty id", ErrInvalidArgument)
	}
	var message any
	if strings.TrimSpace(messageID) != "" {
		message = messageID
	}
	res, err := s.db.Exec(`UPDATE attachments SET message_id = ? WHERE id = ?`, message, id)
	if err != nil {
		return fmt.Errorf("store: set attachment %q message: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: set attachment %q message: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: attachment %q: %w", id, ErrNotFound)
	}
	return nil
}

// GetAttachment returns one attachment row by id.
func (s *Store) GetAttachment(id string) (*Attachment, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: get attachment: empty id", ErrInvalidArgument)
	}
	row := s.db.QueryRow(`SELECT `+attachmentColumns+` FROM attachments WHERE id = ?`, id)
	a, err := scanAttachment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: attachment %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get attachment %q: %w", id, err)
	}
	return a, nil
}

// ListAttachments returns a session's attachments in creation order.
func (s *Store) ListAttachments(sessionID string) ([]Attachment, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("%w: list attachments: empty session id", ErrInvalidArgument)
	}
	rows, err := s.db.Query(
		`SELECT `+attachmentColumns+` FROM attachments WHERE session_id = ? ORDER BY created_at, rowid`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list attachments for session %q: %w", sessionID, err)
	}
	defer rows.Close()

	list := make([]Attachment, 0)
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list attachments for session %q: %w", sessionID, err)
		}
		list = append(list, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list attachments for session %q: %w", sessionID, err)
	}
	return list, nil
}

// DeleteAttachment removes one attachment row. The stored file is owned by
// the caller, which removes it after the row is gone.
func (s *Store) DeleteAttachment(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: delete attachment: empty id", ErrInvalidArgument)
	}
	res, err := s.db.Exec(`DELETE FROM attachments WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete attachment %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete attachment %q: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: attachment %q: %w", id, ErrNotFound)
	}
	return nil
}

func scanAttachment(row scanner) (*Attachment, error) {
	var (
		a       Attachment
		message sql.NullString
		created string
	)
	if err := row.Scan(&a.ID, &a.SessionID, &message, &a.Name, &a.Path, &a.MIME, &a.Kind, &a.Size, &created); err != nil {
		return nil, err
	}
	a.MessageID = message.String
	t, err := decodeTime(created)
	if err != nil {
		return nil, err
	}
	a.CreatedAt = t
	return &a, nil
}
