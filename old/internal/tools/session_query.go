package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// SessionQueryConfig configures the read-only session history tools. Store is
// required at call time; a tool constructed without it reports a clear error.
// WorkspaceID scopes every result to one workspace; empty preserves the
// historical global behavior (tests and direct tool use).
type SessionQueryConfig struct {
	Store       *store.Store
	WorkspaceID string
}

const (
	// sessionQueryMaxOutput caps every session-query result (64 KiB).
	sessionQueryMaxOutput = DefaultMaxOutputBytes
	// sessionSearchDefaultLimit and sessionSearchMaxLimit bound a session
	// search before the output cap is applied.
	sessionSearchDefaultLimit = 20
	sessionSearchMaxLimit     = 100
	// sessionEventSearchMaxEvents bounds one event search.
	sessionEventSearchMaxEvents = 100
	// sessionEventExcerptBytes bounds one event summary's excerpt.
	sessionEventExcerptBytes = 256
	// sessionEventPayloadBytes bounds the payload read back by event_read.
	sessionEventPayloadBytes = 48 * 1024
)

const sessionSearchDescription = `Search sessions in this workspace by title or message content.

Call it with a JSON object:
  {"query":"release checklist","limit":10}

"query" is required and is matched literally against session titles and
persisted message content. "limit" is optional (default 20, maximum 100).

The tool returns a JSON document {"kind":"session_search","query":...,
"total":...,"count":...,"truncated":...,"sessions":[{...}]}. Each session
carries id, workspace_id, title, status, timestamps and a short matching
excerpt. The result is read-only and capped at 64 KiB.`

const sessionEventSearchDescription = `Search the persisted event trace of one session.

Call it with a JSON object:
  {"session_id":"<id>","query":"tool_end"}
  {"session_id":"<id>"}

"session_id" is required. "query" is optional and is matched literally,
case-insensitively, against the event type and payload. Without a query every
event matches.

The tool returns {"kind":"session_event_search","session_id":...,
"query":...,"total":...,"count":...,"truncated":...,"events":[{seq,type,ts,
excerpt}]} capped at 64 KiB. Follow a hit with session_event_read or
session_event_trace.`

const sessionEventReadDescription = `Read one full event of a session by sequence number.

Call it with a JSON object:
  {"session_id":"<id>","seq":12}

"session_id" and "seq" are required. The tool returns
{"kind":"session_event_read","event":{"session_id","seq","type","ts",
"payload",...}}. A payload larger than 48 KiB is replaced by a truncated JSON
string and marked "payload_truncated":true. The whole result is capped at
64 KiB.`

const sessionEventTraceDescription = `Trace one event's immediate neighborhood and cited relationships.

Call it with a JSON object:
  {"session_id":"<id>","seq":12}

The tool returns {"kind":"session_event_trace","session_id":...,
"event":{...},"before":[...],"after":[...],"relationships":[...],"note":...}.
"before" and "after" summarize up to two neighboring events; relationships are
sequence links declared in the event payload (source_seq, parent_seq, replaces,
caused_by, related_seq). This store does not persist replacement or citation
metadata beyond those payload fields.`

const sessionTraceDescription = `Summarize one session and its event trace.

Call it with a JSON object:
  {"session_id":"<id>"}

The tool returns {"kind":"session_trace","session":{...},"event_count":...,
"first_seq":...,"last_seq":...,"event_types":{...},"recent_events":[...],
"lineage":{...}}. This store persists no session parent/child relation, so
lineage is reported as unavailable instead of being guessed. The result is
read-only and capped at 64 KiB.`

// NewSessionQueryTools returns the five read-only session history tools.
func NewSessionQueryTools(cfg SessionQueryConfig) []runtime.Tool {
	base := sessionQueryToolBase{st: cfg.Store, workspaceID: strings.TrimSpace(cfg.WorkspaceID)}
	return []runtime.Tool{
		&sessionSearchTool{sessionQueryToolBase: base},
		&sessionEventSearchTool{sessionQueryToolBase: base},
		&sessionEventReadTool{sessionQueryToolBase: base},
		&sessionEventTraceTool{sessionQueryToolBase: base},
		&sessionTraceTool{sessionQueryToolBase: base},
	}
}

// sessionQueryToolBase carries the shared store handle and workspace scope.
type sessionQueryToolBase struct {
	st          *store.Store
	workspaceID string
}

// requireStore returns a clear failure for a tool constructed without a store.
func (b *sessionQueryToolBase) requireStore() error {
	if b.st == nil {
		return errors.New("session query: no store configured")
	}
	return nil
}

// requireSession checks that sessionID exists and, when a workspace scope is
// configured, that it belongs to that workspace. The resolved row is returned
// so callers do not read the session twice.
func (b *sessionQueryToolBase) requireSession(sessionID string) (*store.Session, error) {
	sess, err := b.st.GetSession(sessionID)
	if err != nil {
		return nil, err
	}
	if b.workspaceID != "" && sess.WorkspaceID != b.workspaceID {
		return nil, fmt.Errorf("session %q is outside this workspace", sessionID)
	}
	return sess, nil
}

// sessionView is one session row in a search or trace result.
type sessionView struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Title       string    `json:"title"`
	Status      string    `json:"status"`
	Archived    bool      `json:"archived,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Excerpt     string    `json:"excerpt,omitempty"`
}

// eventSummary is the bounded projection used by search and trace.
type eventSummary struct {
	Seq         int64     `json:"seq"`
	Type        string    `json:"type"`
	TS          time.Time `json:"ts"`
	Excerpt     string    `json:"excerpt,omitempty"`
	PayloadSize int       `json:"payload_bytes,omitempty"`
}

// eventView is one event including its payload, optionally truncated.
type eventView struct {
	SessionID        string          `json:"session_id,omitempty"`
	Seq              int64           `json:"seq"`
	Type             string          `json:"type"`
	TS               time.Time       `json:"ts"`
	Payload          json.RawMessage `json:"payload,omitempty"`
	PayloadSize      int             `json:"payload_bytes,omitempty"`
	PayloadTruncated bool            `json:"payload_truncated,omitempty"`
}

// sessionSearchTool implements session_search.
type sessionSearchTool struct {
	sessionQueryToolBase
}

func (t *sessionSearchTool) Name() string        { return "session_search" }
func (t *sessionSearchTool) Description() string { return sessionSearchDescription }

func (t *sessionSearchTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if err := sessionQueryContext(ctx); err != nil {
		return "", err
	}
	if err := t.requireStore(); err != nil {
		return "", err
	}
	var in struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return "", errors.New(`session_search: "query" is required and must contain non-whitespace text`)
	}
	if strings.IndexByte(query, 0) >= 0 {
		return "", errors.New("session_search: query must not contain a NUL byte")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = sessionSearchDefaultLimit
	}
	if limit > sessionSearchMaxLimit {
		limit = sessionSearchMaxLimit
	}

	sessions, err := t.st.QuerySessions(store.SessionQuery{
		Search:      query,
		WorkspaceID: t.workspaceID,
	})
	if err != nil {
		return "", fmt.Errorf("session_search: %w", err)
	}
	total := len(sessions)
	if len(sessions) > limit {
		sessions = sessions[:limit]
	}
	views := make([]sessionView, 0, len(sessions))
	needle := strings.ToLower(query)
	for _, sess := range sessions {
		view := sessionView{
			ID:          sess.ID,
			WorkspaceID: sess.WorkspaceID,
			Title:       sess.Title,
			Status:      string(sess.Status),
			CreatedAt:   sess.CreatedAt,
			UpdatedAt:   sess.UpdatedAt,
			Excerpt:     sessionExcerpt(t.st, sess, needle),
		}
		if withArchive, err := t.st.GetSessionWithArchive(sess.ID); err == nil {
			view.Archived = withArchive.Archived
		}
		views = append(views, view)
	}
	out := struct {
		Kind      string        `json:"kind"`
		Query     string        `json:"query"`
		Total     int           `json:"total"`
		Count     int           `json:"count"`
		Truncated bool          `json:"truncated"`
		Sessions  []sessionView `json:"sessions"`
	}{
		Kind:      "session_search",
		Query:     query,
		Total:     total,
		Count:     len(views),
		Truncated: total > len(views),
		Sessions:  views,
	}
	return boundedToolJSON(out, sessionQueryMaxOutput, t.Name())
}

// sessionExcerpt returns a short matching message excerpt, or "" when the
// query matched only the title.
func sessionExcerpt(st *store.Store, sess store.Session, needle string) string {
	if needle == "" {
		return ""
	}
	if strings.Contains(strings.ToLower(sess.Title), needle) {
		return ""
	}
	msgs, err := st.ListMessages(sess.ID)
	if err != nil {
		return ""
	}
	for _, msg := range msgs {
		if strings.Contains(strings.ToLower(msg.Content), needle) {
			return truncateBytes(msg.Content, 300, "excerpt truncated")
		}
	}
	return ""
}

// sessionEventSearchTool implements session_event_search.
type sessionEventSearchTool struct {
	sessionQueryToolBase
}

func (t *sessionEventSearchTool) Name() string        { return "session_event_search" }
func (t *sessionEventSearchTool) Description() string { return sessionEventSearchDescription }

func (t *sessionEventSearchTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if err := sessionQueryContext(ctx); err != nil {
		return "", err
	}
	if err := t.requireStore(); err != nil {
		return "", err
	}
	var in struct {
		SessionID string `json:"session_id"`
		Query     string `json:"query"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	sessionID := strings.TrimSpace(in.SessionID)
	if sessionID == "" {
		return "", errors.New(`session_event_search: "session_id" is required`)
	}
	query := strings.TrimSpace(in.Query)
	if strings.IndexByte(query, 0) >= 0 {
		return "", errors.New("session_event_search: query must not contain a NUL byte")
	}
	if _, err := t.requireSession(sessionID); err != nil {
		return "", fmt.Errorf("session_event_search: session %q: %w", sessionID, err)
	}
	events, err := t.st.ListEvents(sessionID)
	if err != nil {
		return "", fmt.Errorf("session_event_search: %w", err)
	}
	needle := strings.ToLower(query)
	matched := make([]store.Event, 0, len(events))
	for _, ev := range events {
		if needle == "" || eventMatches(ev, needle) {
			matched = append(matched, ev)
		}
	}
	total := len(matched)
	truncated := false
	if len(matched) > sessionEventSearchMaxEvents {
		matched = matched[:sessionEventSearchMaxEvents]
		truncated = true
	}
	summaries := make([]eventSummary, 0, len(matched))
	for _, ev := range matched {
		summaries = append(summaries, summarizeEvent(ev))
	}
	out := struct {
		Kind      string         `json:"kind"`
		SessionID string         `json:"session_id"`
		Query     string         `json:"query,omitempty"`
		Total     int            `json:"total"`
		Count     int            `json:"count"`
		Truncated bool           `json:"truncated"`
		Events    []eventSummary `json:"events"`
	}{
		Kind:      "session_event_search",
		SessionID: sessionID,
		Query:     query,
		Total:     total,
		Count:     len(summaries),
		Truncated: truncated || total > len(summaries),
		Events:    summaries,
	}
	return boundedToolJSON(out, sessionQueryMaxOutput, t.Name())
}

// eventMatches reports whether ev's type or payload contains needle. needle is
// already lower-cased.
func eventMatches(ev store.Event, needle string) bool {
	if strings.Contains(strings.ToLower(ev.Type), needle) {
		return true
	}
	return strings.Contains(strings.ToLower(string(ev.Payload)), needle)
}

// eventExcerpt renders a short text projection of an event payload.
func eventExcerpt(ev store.Event) string {
	raw := strings.TrimSpace(string(ev.Payload))
	if raw == "" {
		return ev.Type
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(ev.Payload, &obj); err == nil {
		for _, key := range []string{"text", "content", "result", "message", "name", "task"} {
			if value, ok := obj[key]; ok {
				var s string
				if json.Unmarshal(value, &s) == nil && strings.TrimSpace(s) != "" {
					return truncateBytes(s, sessionEventExcerptBytes, "excerpt truncated")
				}
			}
		}
	}
	return truncateBytes(raw, sessionEventExcerptBytes, "excerpt truncated")
}

// summarizeEvent projects one event without its full payload.
func summarizeEvent(ev store.Event) eventSummary {
	return eventSummary{
		Seq:         ev.Seq,
		Type:        ev.Type,
		TS:          ev.TS,
		Excerpt:     eventExcerpt(ev),
		PayloadSize: len(ev.Payload),
	}
}

// eventViewOf projects an event, truncating an oversized payload into a JSON
// string so the result stays valid JSON.
func eventViewOf(ev store.Event, maxPayload int) eventView {
	out := eventView{
		SessionID:   ev.SessionID,
		Seq:         ev.Seq,
		Type:        ev.Type,
		TS:          ev.TS,
		PayloadSize: len(ev.Payload),
	}
	if len(ev.Payload) == 0 {
		return out
	}
	if maxPayload > 0 && len(ev.Payload) > maxPayload {
		preview := string(ev.Payload)
		preview = truncateBytes(preview, maxPayload, "payload truncated")
		encoded, err := json.Marshal(preview)
		if err != nil {
			encoded = []byte(`"<payload omitted>"`)
		}
		out.Payload = encoded
		out.PayloadTruncated = true
		return out
	}
	out.Payload = append(json.RawMessage(nil), ev.Payload...)
	return out
}

// sessionEventReadTool implements session_event_read.
type sessionEventReadTool struct {
	sessionQueryToolBase
}

func (t *sessionEventReadTool) Name() string        { return "session_event_read" }
func (t *sessionEventReadTool) Description() string { return sessionEventReadDescription }

func (t *sessionEventReadTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if err := sessionQueryContext(ctx); err != nil {
		return "", err
	}
	if err := t.requireStore(); err != nil {
		return "", err
	}
	var in struct {
		SessionID string `json:"session_id"`
		Seq       int64  `json:"seq"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	sessionID := strings.TrimSpace(in.SessionID)
	if sessionID == "" {
		return "", errors.New(`session_event_read: "session_id" is required`)
	}
	if in.Seq <= 0 {
		return "", errors.New(`session_event_read: "seq" must be a positive integer`)
	}
	if _, err := t.requireSession(sessionID); err != nil {
		return "", fmt.Errorf("session_event_read: session %q: %w", sessionID, err)
	}
	ev, err := t.st.GetEvent(sessionID, in.Seq)
	if err != nil {
		return "", fmt.Errorf("session_event_read: %w", err)
	}
	out := struct {
		Kind  string    `json:"kind"`
		Event eventView `json:"event"`
	}{
		Kind:  "session_event_read",
		Event: eventViewOf(*ev, sessionEventPayloadBytes),
	}
	return boundedToolJSON(out, sessionQueryMaxOutput, t.Name())
}

// eventRelationship is one sequence link cited by an event payload.
type eventRelationship struct {
	Seq      int64  `json:"seq"`
	Relation string `json:"relation"`
}

// sessionEventTraceTool implements session_event_trace.
type sessionEventTraceTool struct {
	sessionQueryToolBase
}

func (t *sessionEventTraceTool) Name() string        { return "session_event_trace" }
func (t *sessionEventTraceTool) Description() string { return sessionEventTraceDescription }

func (t *sessionEventTraceTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if err := sessionQueryContext(ctx); err != nil {
		return "", err
	}
	if err := t.requireStore(); err != nil {
		return "", err
	}
	var in struct {
		SessionID string `json:"session_id"`
		Seq       int64  `json:"seq"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	sessionID := strings.TrimSpace(in.SessionID)
	if sessionID == "" {
		return "", errors.New(`session_event_trace: "session_id" is required`)
	}
	if in.Seq <= 0 {
		return "", errors.New(`session_event_trace: "seq" must be a positive integer`)
	}
	if _, err := t.requireSession(sessionID); err != nil {
		return "", fmt.Errorf("session_event_trace: session %q: %w", sessionID, err)
	}
	ev, err := t.st.GetEvent(sessionID, in.Seq)
	if err != nil {
		return "", fmt.Errorf("session_event_trace: %w", err)
	}
	events, err := t.st.ListEvents(sessionID)
	if err != nil {
		return "", fmt.Errorf("session_event_trace: %w", err)
	}
	before, after := eventNeighbors(events, in.Seq)
	out := struct {
		Kind          string              `json:"kind"`
		SessionID     string              `json:"session_id"`
		Event         eventView           `json:"event"`
		Before        []eventSummary      `json:"before"`
		After         []eventSummary      `json:"after"`
		Relationships []eventRelationship `json:"relationships"`
		Note          string              `json:"note"`
	}{
		Kind:          "session_event_trace",
		SessionID:     sessionID,
		Event:         eventViewOf(*ev, 16*1024),
		Before:        before,
		After:         after,
		Relationships: eventRelationships(*ev),
		Note:          "before/after are adjacent sequence events; relationships are sequence links declared in the payload.",
	}
	return boundedToolJSON(out, sessionQueryMaxOutput, t.Name())
}

// eventNeighbors returns up to two summaries immediately before and after seq.
func eventNeighbors(events []store.Event, seq int64) ([]eventSummary, []eventSummary) {
	before := make([]eventSummary, 0, 2)
	after := make([]eventSummary, 0, 2)
	idx := -1
	for i := range events {
		if events[i].Seq == seq {
			idx = i
			break
		}
	}
	if idx < 0 {
		return before, after
	}
	for i := idx - 1; i >= 0 && len(before) < 2; i-- {
		before = append(before, summarizeEvent(events[i]))
	}
	// before was collected nearest-first; reverse it so it reads oldest-first.
	for i, j := 0, len(before)-1; i < j; i, j = i+1, j-1 {
		before[i], before[j] = before[j], before[i]
	}
	for i := idx + 1; i < len(events) && len(after) < 2; i++ {
		after = append(after, summarizeEvent(events[i]))
	}
	return before, after
}

// eventRelationships extracts sequence links declared in an event payload.
func eventRelationships(ev store.Event) []eventRelationship {
	out := make([]eventRelationship, 0)
	if len(ev.Payload) == 0 {
		return out
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(ev.Payload, &obj); err != nil {
		return out
	}
	relations := []struct {
		key      string
		relation string
	}{
		{"source_seq", "source"},
		{"source_event_seq", "source"},
		{"parent_seq", "parent"},
		{"replaces", "replaces"},
		{"caused_by", "caused_by"},
		{"related_seq", "related"},
	}
	seen := make(map[eventRelationship]bool)
	for _, rel := range relations {
		raw, ok := obj[rel.key]
		if !ok {
			continue
		}
		for _, seq := range decodeSequenceLinks(raw) {
			link := eventRelationship{Seq: seq, Relation: rel.relation}
			if !seen[link] {
				seen[link] = true
				out = append(out, link)
			}
		}
	}
	return out
}

// decodeSequenceLinks accepts one positive sequence number or an array of them.
func decodeSequenceLinks(raw json.RawMessage) []int64 {
	var one int64
	if err := json.Unmarshal(raw, &one); err == nil {
		if one > 0 {
			return []int64{one}
		}
		return nil
	}
	var many []int64
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil
	}
	out := make([]int64, 0, len(many))
	for _, seq := range many {
		if seq > 0 {
			out = append(out, seq)
		}
	}
	return out
}

// sessionTraceTool implements session_trace.
type sessionTraceTool struct {
	sessionQueryToolBase
}

func (t *sessionTraceTool) Name() string        { return "session_trace" }
func (t *sessionTraceTool) Description() string { return sessionTraceDescription }

func (t *sessionTraceTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if err := sessionQueryContext(ctx); err != nil {
		return "", err
	}
	if err := t.requireStore(); err != nil {
		return "", err
	}
	var in struct {
		SessionID string `json:"session_id"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	sessionID := strings.TrimSpace(in.SessionID)
	if sessionID == "" {
		return "", errors.New(`session_trace: "session_id" is required`)
	}
	if _, err := t.requireSession(sessionID); err != nil {
		return "", fmt.Errorf("session_trace: session %q: %w", sessionID, err)
	}
	sess, err := t.st.GetSessionWithArchive(sessionID)
	if err != nil {
		return "", fmt.Errorf("session_trace: session %q: %w", sessionID, err)
	}
	events, err := t.st.ListEvents(sessionID)
	if err != nil {
		return "", fmt.Errorf("session_trace: %w", err)
	}
	counts := make(map[string]int)
	recent := make([]eventSummary, 0, 10)
	for i, ev := range events {
		counts[ev.Type]++
		if i >= len(events)-10 {
			recent = append(recent, summarizeEvent(ev))
		}
	}
	var firstSeq, lastSeq int64
	if len(events) > 0 {
		firstSeq = events[0].Seq
		lastSeq = events[len(events)-1].Seq
	}
	out := struct {
		Kind       string         `json:"kind"`
		Session    sessionView    `json:"session"`
		EventCount int            `json:"event_count"`
		FirstSeq   int64          `json:"first_seq,omitempty"`
		LastSeq    int64          `json:"last_seq,omitempty"`
		EventTypes map[string]int `json:"event_types"`
		Recent     []eventSummary `json:"recent_events"`
		Lineage    struct {
			Available bool     `json:"available"`
			Parent    *string  `json:"parent,omitempty"`
			Children  []string `json:"children"`
			Note      string   `json:"note"`
		} `json:"lineage"`
	}{
		Kind:       "session_trace",
		Session:    sessionView{ID: sess.ID, WorkspaceID: sess.WorkspaceID, Title: sess.Title, Status: string(sess.Status), Archived: sess.Archived, CreatedAt: sess.CreatedAt, UpdatedAt: sess.UpdatedAt},
		EventCount: len(events),
		FirstSeq:   firstSeq,
		LastSeq:    lastSeq,
		EventTypes: counts,
		Recent:     recent,
	}
	out.Lineage.Available = false
	out.Lineage.Children = []string{}
	out.Lineage.Note = "session lineage is not persisted by this store; only the session metadata and event trace are available."
	return boundedToolJSON(out, sessionQueryMaxOutput, t.Name())
}

// sessionQueryContext normalizes and checks a tool context.
func sessionQueryContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

// boundedToolJSON marshals v and guarantees the result stays under limit bytes.
// If the value is unexpectedly larger, it substitutes a small valid JSON
// envelope instead of returning a truncated (and invalid) document.
func boundedToolJSON(v any, limit int, kind string) (string, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("%s: encode result: %w", kind, err)
	}
	if limit <= 0 || len(encoded) <= limit {
		return string(encoded), nil
	}
	fallback := struct {
		Kind      string `json:"kind"`
		Truncated bool   `json:"truncated"`
		Message   string `json:"message"`
	}{
		Kind:      kind,
		Truncated: true,
		Message:   fmt.Sprintf("result exceeds the %d-byte limit; narrow the request", limit),
	}
	encoded, err = json.Marshal(fallback)
	if err != nil {
		return "", fmt.Errorf("%s: encode fallback result: %w", kind, err)
	}
	return string(encoded), nil
}
