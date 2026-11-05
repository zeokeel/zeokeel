package store

import (
	"strings"
	"testing"
)

// searchFixture seeds three sessions: one title match, one message-content
// match and one unrelated session.
func searchFixture(t *testing.T) (s *Store, titleMatch, contentMatch, unrelated *Session) {
	t.Helper()
	s = openTestStore(t)
	ws := mustWorkspace(t, s, "alpha", "/tmp/alpha")
	titleMatch = mustSession(t, s, ws.ID, "budget review plan")
	contentMatch = mustSession(t, s, ws.ID, "random notes")
	unrelated = mustSession(t, s, ws.ID, "unrelated session")

	if _, err := s.AppendMessage(Message{SessionID: contentMatch.ID, Role: "user", Content: "we talked about the budget for next quarter"}); err != nil {
		t.Fatalf("AppendMessage(contentMatch): %v", err)
	}
	if _, err := s.AppendMessage(Message{SessionID: unrelated.ID, Role: "user", Content: "nothing to do with money"}); err != nil {
		t.Fatalf("AppendMessage(unrelated): %v", err)
	}
	return s, titleMatch, contentMatch, unrelated
}

// TestQuerySessionsSearchRanksTitleMatchFirst pins FP-09's ranking: the
// LIKE/filter semantics stay the same, but title hits are ordered before
// message-content hits.
func TestQuerySessionsSearchRanksTitleMatchFirst(t *testing.T) {
	s, titleMatch, contentMatch, _ := searchFixture(t)
	got, err := s.QuerySessions(SessionQuery{Search: "budget"})
	if err != nil {
		t.Fatalf("QuerySessions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("search returned %d sessions, want 2 (title + content match)", len(got))
	}
	if got[0].ID != titleMatch.ID {
		t.Fatalf("first result = %q, want the title match %q", got[0].Title, titleMatch.Title)
	}
	if got[1].ID != contentMatch.ID {
		t.Fatalf("second result = %q, want the content match %q", got[1].Title, contentMatch.Title)
	}
}

// TestQuerySessionsSearchIndexesNewMessages pins incremental maintenance: a
// message appended after the index exists is searchable on the next query.
func TestQuerySessionsSearchIndexesNewMessages(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "alpha", "/tmp/alpha")
	sess := mustSession(t, s, ws.ID, "later additions")

	before, err := s.QuerySessions(SessionQuery{Search: "zebra"})
	if err != nil {
		t.Fatalf("QuerySessions before: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("search before append = %d sessions, want 0", len(before))
	}
	if _, err := s.AppendMessage(Message{SessionID: sess.ID, Role: "user", Content: "a zebra crossing appears"}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	after, err := s.QuerySessions(SessionQuery{Search: "zebra"})
	if err != nil {
		t.Fatalf("QuerySessions after: %v", err)
	}
	if len(after) != 1 || after[0].ID != sess.ID {
		t.Fatalf("search after append = %v, want the seeded session", after)
	}
}

// TestQuerySessionsSearchFindsCJKContent pins the CJK path: a multi-character
// Chinese substring is found through the trigram index.
func TestQuerySessionsSearchFindsCJKContent(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "alpha", "/tmp/alpha")
	sess := mustSession(t, s, ws.ID, "中文会话")
	if _, err := s.AppendMessage(Message{SessionID: sess.ID, Role: "user", Content: "请帮我检查预算报告中的数字"}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	got, err := s.QuerySessions(SessionQuery{Search: "预算报告"})
	if err != nil {
		t.Fatalf("QuerySessions: %v", err)
	}
	if len(got) != 1 || got[0].ID != sess.ID {
		t.Fatalf("CJK search = %v, want the seeded session", got)
	}
}

// TestQuerySessionsSearchShortQueryFallsBackToLike pins the documented
// degradation: queries shorter than the trigram minimum still work through the
// LIKE filter (ranking is skipped, the set is not).
func TestQuerySessionsSearchShortQueryFallsBackToLike(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "alpha", "/tmp/alpha")
	sess := mustSession(t, s, ws.ID, "短查询")
	if _, err := s.AppendMessage(Message{SessionID: sess.ID, Role: "user", Content: "预算"}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	got, err := s.QuerySessions(SessionQuery{Search: "预算"})
	if err != nil {
		t.Fatalf("QuerySessions: %v", err)
	}
	if len(got) != 1 || got[0].ID != sess.ID {
		t.Fatalf("short-query search = %v, want the seeded session", got)
	}
}

// TestQuerySessionsSearchKeepsLikeSemantics pins that FTS ranking does not
// change the filter: LIKE wildcards stay literal and non-matches stay out.
func TestQuerySessionsSearchKeepsLikeSemantics(t *testing.T) {
	s, titleMatch, _, unrelated := searchFixture(t)
	// A literal '%' must not turn into a wildcard.
	got, err := s.QuerySessions(SessionQuery{Search: "%"})
	if err != nil {
		t.Fatalf("QuerySessions(%%): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("literal %% matched %d sessions, want 0", len(got))
	}
	// A query that matches nothing still returns nothing.
	got, err = s.QuerySessions(SessionQuery{Search: "no-such-term-anywhere"})
	if err != nil {
		t.Fatalf("QuerySessions(no match): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("non-matching search = %v, want empty", got)
	}
	// Control: the fixture's title match is still found.
	got, err = s.QuerySessions(SessionQuery{Search: "review plan"})
	if err != nil {
		t.Fatalf("QuerySessions(review plan): %v", err)
	}
	if len(got) != 1 || got[0].ID != titleMatch.ID || got[0].ID == unrelated.ID {
		t.Fatalf("review-plan search = %v, want only %q", got, titleMatch.Title)
	}
}

// TestDeleteSessionClearsSearchIndex pins that deleting a session removes its
// content from the FTS index too, so a deleted session cannot be found (and its
// text does not linger in the index shadow tables).
func TestDeleteSessionClearsSearchIndex(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "alpha", "/tmp/alpha")
	doomed := mustSession(t, s, ws.ID, "doomed session")
	survivor := mustSession(t, s, ws.ID, "surviving session")
	if _, err := s.AppendMessage(Message{SessionID: doomed.ID, Role: "user", Content: "secret searchable needle"}); err != nil {
		t.Fatalf("AppendMessage(doomed): %v", err)
	}
	if _, err := s.AppendMessage(Message{SessionID: survivor.ID, Role: "user", Content: "another needle in the haystack"}); err != nil {
		t.Fatalf("AppendMessage(survivor): %v", err)
	}
	// Two matching sessions make the ranking path build the FTS index.
	if got, err := s.QuerySessions(SessionQuery{Search: "needle"}); err != nil || len(got) != 2 {
		t.Fatalf("search before delete = %v, %v; want two hits", got, err)
	}
	if err := s.DeleteSession(doomed.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	got, err := s.QuerySessions(SessionQuery{Search: "searchable"})
	if err != nil {
		t.Fatalf("search after delete: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("deleted session content is still searchable: %v", got)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT count(*) FROM ` + sessionSearchTable).Scan(&rows); err != nil {
		t.Fatalf("count search index rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("search index holds %d rows, want only the surviving session's row", rows)
	}
}

// TestSessionSearchScoresShortQuerySkipsIndex pins that a short query never
// touches the FTS index (no error, no scores) so the LIKE path is used.
func TestSessionSearchScoresShortQuerySkipsIndex(t *testing.T) {
	s := openTestStore(t)
	scores, err := s.sessionSearchScores("ab")
	if err != nil {
		t.Fatalf("sessionSearchScores(short): %v", err)
	}
	if scores != nil {
		t.Fatalf("short query scores = %v, want nil", scores)
	}
}

// TestQuerySessionsSearchDoesNotLeakArchivedSessions pins that the FTS ranking
// path keeps the archived filter (the ranking only reorders filtered rows).
func TestQuerySessionsSearchDoesNotLeakArchivedSessions(t *testing.T) {
	s, titleMatch, _, _ := searchFixture(t)
	if _, err := s.SetSessionArchived(titleMatch.ID, true); err != nil {
		t.Fatalf("SetSessionArchived: %v", err)
	}
	got, err := s.QuerySessions(SessionQuery{Search: "budget"})
	if err != nil {
		t.Fatalf("QuerySessions: %v", err)
	}
	for _, sess := range got {
		if sess.ID == titleMatch.ID {
			t.Fatalf("archived session %q leaked into search results", strings.TrimSpace(sess.Title))
		}
	}
}
