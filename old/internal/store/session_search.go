package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// Session search ranking (FP-09).
//
// QuerySessions keeps the documented LIKE semantics as the filter: a search
// matches the session title or any message content literally, so existing
// callers, escaping rules and tests do not change. On top of that filter this
// file maintains an FTS5 index over the persisted messages and uses it to order
// the already-filtered results by relevance (title matches first, then BM25).
// If FTS5 is unavailable for any reason the index is skipped and the LIKE
// ordering is returned unchanged: search degrades, it does not break.
//
// The index is created lazily (a CREATE VIRTUAL TABLE, adopting the same
// additive-migration style as the sessions archive column) and maintained
// incrementally from a rowid high-water mark stored in the settings table.
// Index rows whose message row has since been deleted are dropped by the JOIN
// when ranking, and DeleteSession explicitly clears a deleted session's rows.
const (
	// sessionSearchTable is the FTS5 table name.
	sessionSearchTable = "session_search_fts"
	// sessionSearchWatermarkKey is the settings key holding the highest
	// indexed messages.rowid.
	sessionSearchWatermarkKey = "search.fts_rowid"
	// sessionSearchMaxRanked bounds one ranking query.
	sessionSearchMaxRanked = 500
	// sessionSearchMinRunes is the shortest query the trigram tokenizer can
	// match; shorter searches keep the LIKE-only path.
	sessionSearchMinRunes = 3
)

// sessionSearchReady records the stores whose FTS table has been created.
var sessionSearchReady sync.Map // map[*Store]bool

// ensureSessionSearch creates the FTS index table and backfills it once per
// store. A store that cannot create FTS5 (older SQLite build) reports the error
// and every caller then falls back to LIKE ordering.
func (s *Store) ensureSessionSearch() error {
	if _, ok := sessionSearchReady.Load(s); ok {
		return nil
	}
	ddl := `CREATE VIRTUAL TABLE IF NOT EXISTS ` + sessionSearchTable +
		` USING fts5(content, session_id UNINDEXED, msg_id UNINDEXED, tokenize='trigram')`
	if _, err := s.db.Exec(ddl); err != nil {
		return fmt.Errorf("store: create session search index: %w", err)
	}
	if err := s.syncSessionSearch(); err != nil {
		return err
	}
	sessionSearchReady.Store(s, true)
	return nil
}

// syncSessionSearch indexes every message appended since the last run. It is
// cheap when nothing new arrived (one settings read plus one max(rowid) read)
// and is called from the search path, so a search always sees recently
// persisted messages.
func (s *Store) syncSessionSearch() error {
	since := int64(0)
	if raw, err := s.GetSetting(sessionSearchWatermarkKey); err == nil {
		if parsed, perr := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); perr == nil {
			since = parsed
		}
	} else if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("store: sync session search index: read watermark: %w", err)
	}

	rows, err := s.db.Query(
		`SELECT rowid, id, session_id, content FROM messages WHERE rowid > ? ORDER BY rowid`, since,
	)
	if err != nil {
		return fmt.Errorf("store: sync session search index: query messages: %w", err)
	}
	type messageRow struct {
		rowid   int64
		id      string
		session string
		content string
	}
	var pending []messageRow
	for rows.Next() {
		var row messageRow
		if err := rows.Scan(&row.rowid, &row.id, &row.session, &row.content); err != nil {
			rows.Close()
			return fmt.Errorf("store: sync session search index: scan: %w", err)
		}
		pending = append(pending, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("store: sync session search index: %w", err)
	}
	rows.Close()
	if len(pending) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: sync session search index: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	stmt, err := tx.Prepare(
		`INSERT INTO ` + sessionSearchTable + `(rowid, content, session_id, msg_id) VALUES (?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("store: sync session search index: prepare: %w", err)
	}
	defer stmt.Close() //nolint:errcheck // closed by tx commit/rollback too
	for _, row := range pending {
		if _, err := stmt.Exec(row.rowid, row.content, row.session, row.id); err != nil {
			return fmt.Errorf("store: sync session search index: insert row %d: %w", row.rowid, err)
		}
	}
	watermark := strconv.FormatInt(pending[len(pending)-1].rowid, 10)
	if _, err := tx.Exec(
		`INSERT INTO settings(key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		sessionSearchWatermarkKey, watermark,
	); err != nil {
		return fmt.Errorf("store: sync session search index: store watermark: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: sync session search index: commit: %w", err)
	}
	return nil
}

// sessionSearchScores returns one relevance score per matching session. Lower
// is better (bm25 in SQLite returns more negative values for better matches).
// An empty map with a nil error means the query is too short for the trigram
// index: the caller keeps the LIKE ordering.
func (s *Store) sessionSearchScores(search string) (map[string]float64, error) {
	if utf8.RuneCountInString(search) < sessionSearchMinRunes {
		return nil, nil
	}
	if err := s.ensureSessionSearch(); err != nil {
		return nil, err
	}
	if err := s.syncSessionSearch(); err != nil {
		return nil, err
	}
	// The whole query is matched as one quoted phrase, mirroring the literal
	// substring semantics of the LIKE filter (quotes are doubled, not escaped).
	phrase := `"` + strings.ReplaceAll(search, `"`, `""`) + `"`
	// bm25() is an FTS5 auxiliary function and cannot be wrapped in an aggregate
	// expression, so the per-row scores are folded per session in Go.
	rows, err := s.db.Query(
		`SELECT f.session_id, bm25(`+sessionSearchTable+`) AS score
		 FROM `+sessionSearchTable+` f
		 JOIN messages m ON m.rowid = f.rowid
		 WHERE `+sessionSearchTable+` MATCH ?
		 LIMIT ?`,
		phrase, sessionSearchMaxRanked,
	)
	if err != nil {
		return nil, fmt.Errorf("store: search sessions: %w", err)
	}
	defer rows.Close()
	scores := make(map[string]float64)
	for rows.Next() {
		var (
			sessionID string
			score     sql.NullFloat64
		)
		if err := rows.Scan(&sessionID, &score); err != nil {
			return nil, fmt.Errorf("store: search sessions: scan: %w", err)
		}
		// Keep the best (most negative) score per session.
		if current, ok := scores[sessionID]; !ok || score.Float64 < current {
			scores[sessionID] = score.Float64
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: search sessions: %w", err)
	}
	return scores, nil
}

// orderSessionsByRelevance reorders an already-filtered session list by search
// relevance: exact title matches first, then FTS-ranked message matches in
// best-first order, then the remaining rows in their prior (updated) order.
// The filter itself is not changed, so the result set is identical to the LIKE
// path. A ranking failure leaves the existing order untouched.
func (s *Store) orderSessionsByRelevance(list []Session, search string) {
	if len(list) < 2 {
		return
	}
	scores, err := s.sessionSearchScores(strings.TrimSpace(search))
	if err != nil {
		return
	}
	needle := strings.ToLower(strings.TrimSpace(search))
	if needle == "" {
		return
	}
	rank := func(sess Session) (tier int, score float64) {
		if strings.Contains(strings.ToLower(sess.Title), needle) {
			return 0, 0
		}
		if value, ok := scores[sess.ID]; ok {
			return 1, value
		}
		return 2, 0
	}
	sort.SliceStable(list, func(i, j int) bool {
		ti, si := rank(list[i])
		tj, sj := rank(list[j])
		if ti != tj {
			return ti < tj
		}
		if ti == 1 && si != sj {
			return si < sj
		}
		return false
	})
}
