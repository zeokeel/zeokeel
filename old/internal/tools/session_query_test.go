package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// sqFixture is a store seeded with one session, one message and three events.
type sqFixture struct {
	st          *store.Store
	workspaceID string
	sessionID   string
}

func newSQFixture(t *testing.T) *sqFixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})
	ws, err := st.CreateWorkspace("test", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess, err := st.CreateSession(ws.ID, "Release checklist")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := st.AppendMessage(store.Message{
		SessionID: sess.ID,
		Role:      "user",
		Content:   "the deploy pipeline is broken",
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	events := []store.Event{
		{Type: "message", Payload: json.RawMessage(`{"text":"hello world"}`)},
		{Type: "tool_end", Payload: json.RawMessage(`{"result":"deploy succeeded"}`)},
		{Type: "custom", Payload: json.RawMessage(`{"source_seq":1,"replaces":[2],"note":"release"}`)},
	}
	for _, ev := range events {
		if _, err := st.AppendEvent(sess.ID, ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
	return &sqFixture{st: st, workspaceID: ws.ID, sessionID: sess.ID}
}

func sqTools(t *testing.T, f *sqFixture) map[string]runtime.Tool {
	t.Helper()
	out := make(map[string]runtime.Tool)
	for _, tool := range NewSessionQueryTools(SessionQueryConfig{Store: f.st}) {
		out[tool.Name()] = tool
	}
	want := []string{"session_search", "session_event_search", "session_event_read", "session_event_trace", "session_trace"}
	if len(out) != len(want) {
		t.Fatalf("tools = %v, want %v", out, want)
	}
	for _, name := range want {
		tool, ok := out[name]
		if !ok {
			t.Fatalf("missing tool %q", name)
		}
		if strings.TrimSpace(tool.Description()) == "" {
			t.Fatalf("tool %q has an empty description", name)
		}
	}
	return out
}

func sqInvoke(t *testing.T, tool runtime.Tool, args string) (string, error) {
	t.Helper()
	return tool.Invoke(context.Background(), json.RawMessage(args))
}

func TestSessionSearchMatchesTitleAndContent(t *testing.T) {
	f := newSQFixture(t)
	tools := sqTools(t, f)

	other, err := f.st.CreateSession("", "")
	if err == nil {
		t.Fatalf("CreateSession with an empty workspace unexpectedly succeeded: %+v", other)
	}
	// A second session under the same workspace proves the search spans
	// sessions and reports both title and content matches.
	sessions, err := f.st.ListWorkspaces()
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ListWorkspaces = %+v, %v", sessions, err)
	}
	second, err := f.st.CreateSession(sessions[0].ID, "Garden notes")
	if err != nil {
		t.Fatalf("CreateSession second: %v", err)
	}
	if _, err := f.st.AppendMessage(store.Message{SessionID: second.ID, Role: "user", Content: "release notes for tomatoes"}); err != nil {
		t.Fatalf("AppendMessage second: %v", err)
	}

	out, err := sqInvoke(t, tools["session_search"], `{"query":"release"}`)
	if err != nil {
		t.Fatalf("session_search: %v", err)
	}
	var result struct {
		Kind      string        `json:"kind"`
		Query     string        `json:"query"`
		Total     int           `json:"total"`
		Count     int           `json:"count"`
		Truncated bool          `json:"truncated"`
		Sessions  []sessionView `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode session_search: %v\n%s", err, out)
	}
	if result.Kind != "session_search" || result.Query != "release" || result.Total != 2 || result.Count != 2 || result.Truncated {
		t.Fatalf("result = %+v", result)
	}

	out, err = sqInvoke(t, tools["session_search"], `{"query":"pipeline"}`)
	if err != nil {
		t.Fatalf("session_search pipeline: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode filtered search: %v", err)
	}
	if result.Total != 1 || len(result.Sessions) != 1 || !strings.Contains(result.Sessions[0].Excerpt, "pipeline") {
		t.Fatalf("filtered result = %+v", result)
	}

	out, err = sqInvoke(t, tools["session_search"], `{"query":"release","limit":1}`)
	if err != nil {
		t.Fatalf("session_search limit: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode limited search: %v", err)
	}
	if result.Count != 1 || result.Total != 2 || !result.Truncated {
		t.Fatalf("limited result = %+v", result)
	}

	if _, err := sqInvoke(t, tools["session_search"], `{"query":"  "}`); err == nil {
		t.Fatal("session_search with a blank query = nil error")
	}
	if _, err := sqInvoke(t, tools["session_search"], `{"query":"a\u0000b"}`); err == nil {
		t.Fatal("session_search with a NUL query = nil error")
	}
	if _, err := sqInvoke(t, tools["session_search"], `{`); err == nil {
		t.Fatal("session_search with invalid JSON = nil error")
	}
}

func TestSessionEventSearchReadAndTrace(t *testing.T) {
	f := newSQFixture(t)
	tools := sqTools(t, f)

	out, err := sqInvoke(t, tools["session_event_search"], `{"session_id":"`+f.sessionID+`","query":"tool_end"}`)
	if err != nil {
		t.Fatalf("session_event_search: %v", err)
	}
	var search struct {
		Kind   string         `json:"kind"`
		Total  int            `json:"total"`
		Events []eventSummary `json:"events"`
	}
	if err := json.Unmarshal([]byte(out), &search); err != nil {
		t.Fatalf("decode event search: %v\n%s", err, out)
	}
	if search.Kind != "session_event_search" || search.Total != 1 || len(search.Events) != 1 || search.Events[0].Seq != 2 {
		t.Fatalf("event search = %+v", search)
	}

	out, err = sqInvoke(t, tools["session_event_search"], `{"session_id":"`+f.sessionID+`"}`)
	if err != nil {
		t.Fatalf("session_event_search without query: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &search); err != nil {
		t.Fatalf("decode unfiltered event search: %v", err)
	}
	if search.Total != 3 {
		t.Fatalf("unfiltered total = %d, want 3", search.Total)
	}

	out, err = sqInvoke(t, tools["session_event_read"], `{"session_id":"`+f.sessionID+`","seq":2}`)
	if err != nil {
		t.Fatalf("session_event_read: %v", err)
	}
	var read struct {
		Kind  string    `json:"kind"`
		Event eventView `json:"event"`
	}
	if err := json.Unmarshal([]byte(out), &read); err != nil {
		t.Fatalf("decode event read: %v\n%s", err, out)
	}
	if read.Kind != "session_event_read" || read.Event.Seq != 2 || read.Event.Type != "tool_end" {
		t.Fatalf("event read = %+v", read)
	}
	var payload map[string]string
	if err := json.Unmarshal(read.Event.Payload, &payload); err != nil {
		t.Fatalf("decode event payload: %v", err)
	}
	if payload["result"] != "deploy succeeded" {
		t.Fatalf("payload = %+v", payload)
	}

	out, err = sqInvoke(t, tools["session_event_trace"], `{"session_id":"`+f.sessionID+`","seq":3}`)
	if err != nil {
		t.Fatalf("session_event_trace: %v", err)
	}
	var trace struct {
		Kind          string              `json:"kind"`
		Before        []eventSummary      `json:"before"`
		After         []eventSummary      `json:"after"`
		Relationships []eventRelationship `json:"relationships"`
	}
	if err := json.Unmarshal([]byte(out), &trace); err != nil {
		t.Fatalf("decode event trace: %v\n%s", err, out)
	}
	if trace.Kind != "session_event_trace" || len(trace.Before) != 2 || len(trace.After) != 0 {
		t.Fatalf("event trace = %+v", trace)
	}
	if len(trace.Relationships) != 2 ||
		trace.Relationships[0] != (eventRelationship{Seq: 1, Relation: "source"}) ||
		trace.Relationships[1] != (eventRelationship{Seq: 2, Relation: "replaces"}) {
		t.Fatalf("relationships = %+v", trace.Relationships)
	}

	if _, err := sqInvoke(t, tools["session_event_read"], `{"session_id":"`+f.sessionID+`","seq":99}`); err == nil {
		t.Fatal("session_event_read for a missing seq = nil error")
	}
	if _, err := sqInvoke(t, tools["session_event_read"], `{"session_id":"`+f.sessionID+`","seq":0}`); err == nil {
		t.Fatal("session_event_read for seq 0 = nil error")
	}
	if _, err := sqInvoke(t, tools["session_event_search"], `{"session_id":"missing"}`); err == nil {
		t.Fatal("session_event_search for a missing session = nil error")
	}
}

func TestSessionTraceSummarizesMetadataAndEvents(t *testing.T) {
	f := newSQFixture(t)
	tools := sqTools(t, f)

	out, err := sqInvoke(t, tools["session_trace"], `{"session_id":"`+f.sessionID+`"}`)
	if err != nil {
		t.Fatalf("session_trace: %v", err)
	}
	var trace struct {
		Kind       string         `json:"kind"`
		Session    sessionView    `json:"session"`
		EventCount int            `json:"event_count"`
		FirstSeq   int64          `json:"first_seq"`
		LastSeq    int64          `json:"last_seq"`
		EventTypes map[string]int `json:"event_types"`
		Recent     []eventSummary `json:"recent_events"`
		Lineage    struct {
			Available bool `json:"available"`
		} `json:"lineage"`
	}
	if err := json.Unmarshal([]byte(out), &trace); err != nil {
		t.Fatalf("decode session trace: %v\n%s", err, out)
	}
	if trace.Kind != "session_trace" || trace.Session.ID != f.sessionID || trace.Session.Title != "Release checklist" {
		t.Fatalf("trace = %+v", trace)
	}
	if trace.EventCount != 3 || trace.FirstSeq != 1 || trace.LastSeq != 3 {
		t.Fatalf("event stats = %+v", trace)
	}
	if trace.EventTypes["tool_end"] != 1 || len(trace.Recent) != 3 {
		t.Fatalf("event types/recent = %+v / %+v", trace.EventTypes, trace.Recent)
	}
	if trace.Lineage.Available {
		t.Fatal("lineage.available = true, want false")
	}
	if !strings.Contains(out, "not persisted") {
		t.Fatalf("trace does not explain missing lineage: %s", out)
	}
	if _, err := sqInvoke(t, tools["session_trace"], `{"session_id":"missing"}`); err == nil {
		t.Fatal("session_trace for a missing session = nil error")
	}
}

func TestSessionEventReadBoundsLargePayload(t *testing.T) {
	f := newSQFixture(t)
	tools := sqTools(t, f)
	huge := `{"blob":"` + strings.Repeat("x", 80*1024) + `"}`
	if _, err := f.st.AppendEvent(f.sessionID, store.Event{Type: "huge", Payload: json.RawMessage(huge)}); err != nil {
		t.Fatalf("AppendEvent huge: %v", err)
	}
	out, err := sqInvoke(t, tools["session_event_read"], `{"session_id":"`+f.sessionID+`","seq":4}`)
	if err != nil {
		t.Fatalf("session_event_read huge: %v", err)
	}
	if len(out) > sessionQueryMaxOutput {
		t.Fatalf("result is %d bytes, want <= %d", len(out), sessionQueryMaxOutput)
	}
	if !json.Valid([]byte(out)) {
		t.Fatal("result is not valid JSON")
	}
	var read struct {
		Event eventView `json:"event"`
	}
	if err := json.Unmarshal([]byte(out), &read); err != nil {
		t.Fatalf("decode huge read: %v", err)
	}
	if !read.Event.PayloadTruncated || read.Event.PayloadSize < 80*1024 {
		t.Fatalf("payload metadata = %+v", read.Event)
	}
}

func TestSessionQueryToolsWithoutStore(t *testing.T) {
	for _, tool := range NewSessionQueryTools(SessionQueryConfig{}) {
		if _, err := tool.Invoke(context.Background(), json.RawMessage(`{}`)); err == nil {
			t.Fatalf("%s without a store = nil error", tool.Name())
		}
	}
}

// TestSessionQueryWorkspaceScoped proves the tools honor the workspace scope
// the app configures: search cannot see another workspace's sessions and the
// per-session reads reject an out-of-workspace id.
func TestSessionQueryWorkspaceScoped(t *testing.T) {
	f := newSQFixture(t)
	otherWS, err := f.st.CreateWorkspace("other", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace other: %v", err)
	}
	otherSession, err := f.st.CreateSession(otherWS.ID, "Release elsewhere")
	if err != nil {
		t.Fatalf("CreateSession other: %v", err)
	}
	if _, err := f.st.AppendMessage(store.Message{
		SessionID: otherSession.ID, Role: "user", Content: "release in another workspace",
	}); err != nil {
		t.Fatalf("AppendMessage other: %v", err)
	}
	if _, err := f.st.AppendEvent(otherSession.ID, store.Event{Type: "message"}); err != nil {
		t.Fatalf("AppendEvent other: %v", err)
	}

	byName := make(map[string]runtime.Tool)
	for _, tool := range NewSessionQueryTools(SessionQueryConfig{Store: f.st, WorkspaceID: f.workspaceID}) {
		byName[tool.Name()] = tool
	}

	out, err := sqInvoke(t, byName["session_search"], `{"query":"release"}`)
	if err != nil {
		t.Fatalf("session_search: %v", err)
	}
	var result struct {
		Total    int           `json:"total"`
		Sessions []sessionView `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode session_search: %v", err)
	}
	if result.Total != 1 || len(result.Sessions) != 1 {
		t.Fatalf("workspace-scoped search = %+v, want only the fixture session", result)
	}
	if result.Sessions[0].WorkspaceID != f.workspaceID {
		t.Fatalf("search leaked workspace %q", result.Sessions[0].WorkspaceID)
	}

	if _, err := sqInvoke(t, byName["session_trace"], `{"session_id":"`+otherSession.ID+`"}`); err == nil {
		t.Fatal("session_trace read a session from another workspace")
	}
	if _, err := sqInvoke(t, byName["session_event_search"], `{"session_id":"`+otherSession.ID+`"}`); err == nil {
		t.Fatal("session_event_search read a session from another workspace")
	}
	if _, err := sqInvoke(t, byName["session_event_read"], `{"session_id":"`+otherSession.ID+`","seq":1}`); err == nil {
		t.Fatal("session_event_read read a session from another workspace")
	}
	if _, err := sqInvoke(t, byName["session_event_trace"], `{"session_id":"`+otherSession.ID+`","seq":1}`); err == nil {
		t.Fatal("session_event_trace read a session from another workspace")
	}
}
