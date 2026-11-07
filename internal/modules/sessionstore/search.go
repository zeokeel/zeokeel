package sessionstore

// Cross-session message full-text search: the durable half behind the old
// shell's session search. The SQL is a case-insensitive substring match over the
// JSON payload (which carries the message text), newest message first; snippets
// are built from the decoded rig.Message text.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// defaultSearchMessageLimit is used for limit <= 0.
	defaultSearchMessageLimit = 50
	// maxSearchMessageLimit caps one SearchMessages result set.
	maxSearchMessageLimit = 200
	// searchSnippetContext is how many runes are kept on each side of a match.
	searchSnippetContext = 60
	// searchSnippetFallback is the snippet length when the query only matched
	// JSON fields other than the message text.
	searchSnippetFallback = 120
	// unnamedSessionTitle is shown when a session has no display title.
	unnamedSessionTitle = "未命名会话"
)

var _ contracts.MessageSearcher = (*store)(nil)

// SearchMessages implements contracts.MessageSearcher. It finds messages whose
// payload text contains query (case-insensitive), newest first, bounded by
// limit. LIKE wildcards in query are matched literally.
func (s *store) SearchMessages(ctx context.Context, query string, limit int) ([]contracts.MessageSearchHit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return []contracts.MessageSearchHit{}, nil
	}
	if limit <= 0 {
		limit = defaultSearchMessageLimit
	}
	if limit > maxSearchMessageLimit {
		limit = maxSearchMessageLimit
	}
	pattern := escapeLike(trimmed)
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.session_id, m.payload, m.created_at, COALESCE(s.title, '')
		FROM messages m
		JOIN sessions s ON s.id = m.session_id
		WHERE LOWER(CAST(m.payload AS TEXT)) LIKE '%' || LOWER(?) || '%' ESCAPE '\'
		ORDER BY m.id DESC
		LIMIT ?`, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("sessionstore: search messages: %w", err)
	}
	defer rows.Close()

	hits := make([]contracts.MessageSearchHit, 0, limit)
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var (
			sessionID string
			payload   []byte
			created   sql.NullInt64
			title     string
		)
		if err := rows.Scan(&sessionID, &payload, &created, &title); err != nil {
			return nil, fmt.Errorf("sessionstore: scan message search hit: %w", err)
		}
		var message rig.Message
		if err := json.Unmarshal(payload, &message); err != nil {
			// Corrupt payloads are not searchable text; skip instead of failing
			// the whole query.
			continue
		}
		if strings.TrimSpace(title) == "" {
			title = unnamedSessionTitle
		}
		text := message.Text()
		hits = append(hits, contracts.MessageSearchHit{
			SessionID:    sessionID,
			SessionTitle: title,
			Role:         string(message.Role),
			Snippet:      buildSearchSnippet(text, trimmed),
			CreatedAt:    timeFromNanos(created.Int64),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sessionstore: search messages: %w", err)
	}
	return hits, nil
}

// plainSnippetText makes a transcript excerpt readable in a search result row:
// whitespace collapsed, Markdown code fences/backticks and heading markers
// dropped (the stored message text is Markdown).
func plainSnippetText(raw string) string {
	collapsed := collapseWhitespace(raw)
	collapsed = strings.ReplaceAll(collapsed, "```", "")
	collapsed = strings.ReplaceAll(collapsed, "`", "")
	runes := []rune(collapsed)
	out := make([]rune, 0, len(runes))
	for index := 0; index < len(runes); index++ {
		if runes[index] == '#' {
			// Drop a run of heading hashes only when it starts a heading
			// (at the start or after a space) and is followed by a space.
			headingStart := index == 0 || runes[index-1] == ' '
			if headingStart {
				end := index
				for end < len(runes) && runes[end] == '#' {
					end++
				}
				if end < len(runes) && runes[end] == ' ' {
					index = end
					continue
				}
			}
		}
		out = append(out, runes[index])
	}
	return strings.TrimSpace(string(out))
}

// buildSearchSnippet returns a whitespace-collapsed excerpt around the first
// occurrence of query in text (rune safe, with … markers), or the first 120
// runes when the match is not in the decoded text.
func buildSearchSnippet(text, query string) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}
	lowerText := strings.ToLower(text)
	lowerQuery := strings.ToLower(query)
	byteIndex := strings.Index(lowerText, lowerQuery)
	if byteIndex < 0 {
		return plainSnippetText(string(runes[:minInt(len(runes), searchSnippetFallback)]))
	}
	start := utf8.RuneCountInString(lowerText[:byteIndex])
	matchRunes := utf8.RuneCountInString(lowerText[byteIndex : byteIndex+len(lowerQuery)])
	windowStart := start - searchSnippetContext
	if windowStart < 0 {
		windowStart = 0
	}
	windowEnd := start + matchRunes + searchSnippetContext
	if windowEnd > len(runes) {
		windowEnd = len(runes)
	}
	snippet := plainSnippetText(string(runes[windowStart:windowEnd]))
	if windowStart > 0 {
		snippet = "…" + snippet
	}
	if windowEnd < len(runes) {
		snippet += "…"
	}
	return snippet
}

// collapseWhitespace folds runs of whitespace into single spaces.
func collapseWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
