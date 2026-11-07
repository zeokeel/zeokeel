package sessionstore

// Decoded search text: messages and event payloads are stored as JSON, but the
// search queries must match the decoded text (quotes and backslashes are user
// content, not escaping). The `search_text` column holds the decoded,
// case-insensitive match surface; the `text` column keeps the plain message text
// used for snippets.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"
)

// encodedMessage is one message prepared for insertion: the JSON payload plus
// the decoded text columns.
type encodedMessage struct {
	payload    []byte
	text       string
	searchText string
}

// messageSearchTextColumns returns the snippet text (the message's plain text)
// and the decoded searchable aggregate. Tool-call argument JSON is decoded
// recursively so its values are searchable without reintroducing JSON escaping;
// reasoning and non-text content metadata are searchable too, matching the old
// raw-payload surface minus structural punctuation.
func messageSearchTextColumns(message rig.Message) (text, searchText string) {
	text = message.Text()
	var builder strings.Builder
	appendSearchToken(&builder, string(message.Role))
	appendSearchToken(&builder, text)
	appendSearchToken(&builder, message.Reasoning)
	appendSearchToken(&builder, message.Name)
	appendSearchToken(&builder, message.ToolCallID)
	for _, part := range message.Content {
		appendSearchToken(&builder, part.ImageURL)
		appendSearchToken(&builder, part.Detail)
	}
	for _, call := range message.ToolCalls {
		appendSearchToken(&builder, call.ID)
		appendSearchToken(&builder, call.Name)
		if arguments := strings.TrimSpace(call.Arguments); arguments != "" {
			var decoded any
			if err := json.Unmarshal([]byte(arguments), &decoded); err == nil {
				appendJSONSearchText(&builder, decoded)
			}
		}
	}
	return text, strings.TrimSpace(builder.String())
}

// eventPayloadSearchText decodes one raw JSON payload into searchable text. The
// event type is matched separately; this is the payload half.
func eventPayloadSearchText(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return ""
	}
	var builder strings.Builder
	appendJSONSearchText(&builder, decoded)
	return strings.TrimSpace(builder.String())
}

// appendJSONSearchText appends every scalar in a decoded JSON value (object keys
// included, so field names remain searchable), without structural JSON
// punctuation.
func appendJSONSearchText(builder *strings.Builder, value any) {
	switch typed := value.(type) {
	case nil:
		return
	case string:
		appendSearchToken(builder, typed)
	case bool:
		appendSearchToken(builder, strconv.FormatBool(typed))
	case float64:
		appendSearchToken(builder, strconv.FormatFloat(typed, 'g', -1, 64))
	case json.Number:
		appendSearchToken(builder, typed.String())
	case []any:
		for _, entry := range typed {
			appendJSONSearchText(builder, entry)
		}
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			appendSearchToken(builder, key)
			appendJSONSearchText(builder, typed[key])
		}
	default:
		appendSearchToken(builder, fmt.Sprint(typed))
	}
}

// appendSearchToken appends one non-empty token separated by a space.
func appendSearchToken(builder *strings.Builder, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if builder.Len() > 0 {
		builder.WriteByte(' ')
	}
	builder.WriteString(value)
}

// migrateSearchColumns adds the decoded-text columns to databases created
// before they existed and backfills every existing row once. SQLite has no ADD
// COLUMN IF NOT EXISTS, so the column list is inspected first; reopening an
// already-migrated database is a no-op.
func (s *store) migrateSearchColumns(ctx context.Context) error {
	messageTextAdded, err := s.ensureColumn(ctx, "messages", "text", "TEXT NOT NULL DEFAULT ''")
	if err != nil {
		return err
	}
	messageSearchAdded, err := s.ensureColumn(ctx, "messages", "search_text", "TEXT NOT NULL DEFAULT ''")
	if err != nil {
		return err
	}
	eventTextAdded, err := s.ensureColumn(ctx, "events", "text", "TEXT NOT NULL DEFAULT ''")
	if err != nil {
		return err
	}
	if messageTextAdded || messageSearchAdded {
		if err := s.backfillMessageSearchText(ctx, ""); err != nil {
			return err
		}
	} else if err := s.backfillMessageSearchText(ctx, "WHERE COALESCE(search_text, '') = ''"); err != nil {
		// A partially migrated/older writer may have left the decoded column
		// empty; fill those rows on open without touching populated rows.
		return err
	}
	if eventTextAdded {
		if err := s.backfillEventSearchText(ctx, ""); err != nil {
			return err
		}
	} else if err := s.backfillEventSearchText(ctx, "WHERE COALESCE(text, '') = ''"); err != nil {
		return err
	}
	return nil
}

// ensureColumn adds one column when missing and reports whether it was added.
func (s *store) ensureColumn(ctx context.Context, table, column, definition string) (bool, error) {
	exists, err := s.hasColumn(ctx, table, column)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	if _, err := s.db.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+column+" "+definition); err != nil {
		return false, fmt.Errorf("sessionstore: add %s.%s: %w", table, column, err)
	}
	return true, nil
}

// backfillMessageSearchText populates text/search_text for pre-migration rows
// from their JSON payloads. Rows with corrupt payloads get empty text; they were
// already skipped by the pre-fix search path.
func (s *store) backfillMessageSearchText(ctx context.Context, where string) error {
	type row struct {
		id      int64
		payload []byte
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, payload FROM messages `+where)
	if err != nil {
		return fmt.Errorf("sessionstore: backfill message text: %w", err)
	}
	var pending []row
	for rows.Next() {
		var entry row
		if err := rows.Scan(&entry.id, &entry.payload); err != nil {
			_ = rows.Close()
			return fmt.Errorf("sessionstore: backfill message text: %w", err)
		}
		pending = append(pending, entry)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("sessionstore: backfill message text: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("sessionstore: backfill message text: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sessionstore: backfill message text: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	statement, err := tx.PrepareContext(ctx, `UPDATE messages SET text = ?, search_text = ? WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("sessionstore: backfill message text: %w", err)
	}
	defer func() { _ = statement.Close() }()
	for _, entry := range pending {
		var message rig.Message
		if err := json.Unmarshal(entry.payload, &message); err != nil {
			continue
		}
		text, searchText := messageSearchTextColumns(message)
		if _, err := statement.ExecContext(ctx, text, searchText, entry.id); err != nil {
			return fmt.Errorf("sessionstore: backfill message text: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sessionstore: backfill message text: %w", err)
	}
	return nil
}

// backfillEventSearchText populates events.text from decoded payloads.
func (s *store) backfillEventSearchText(ctx context.Context, where string) error {
	type row struct {
		seq     int64
		payload []byte
	}
	rows, err := s.db.QueryContext(ctx, `SELECT seq, payload FROM events `+where)
	if err != nil {
		return fmt.Errorf("sessionstore: backfill event text: %w", err)
	}
	var pending []row
	for rows.Next() {
		var entry row
		if err := rows.Scan(&entry.seq, &entry.payload); err != nil {
			_ = rows.Close()
			return fmt.Errorf("sessionstore: backfill event text: %w", err)
		}
		pending = append(pending, entry)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("sessionstore: backfill event text: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("sessionstore: backfill event text: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sessionstore: backfill event text: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	statement, err := tx.PrepareContext(ctx, `UPDATE events SET text = ? WHERE seq = ?`)
	if err != nil {
		return fmt.Errorf("sessionstore: backfill event text: %w", err)
	}
	defer func() { _ = statement.Close() }()
	for _, entry := range pending {
		if _, err := statement.ExecContext(ctx, eventPayloadSearchText(entry.payload), entry.seq); err != nil {
			return fmt.Errorf("sessionstore: backfill event text: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sessionstore: backfill event text: %w", err)
	}
	return nil
}
