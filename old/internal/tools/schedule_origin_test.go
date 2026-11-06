package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// newScheduleTestStore opens a store for the schedule origin tests.
func newScheduleTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestScheduleCreateRecordsOriginSession is the FP-07 regression: the task
// remembers the session whose model created it so a fired run can be delivered
// back there, while a direct (session-less) call leaves the id empty.
func TestScheduleCreateRecordsOriginSession(t *testing.T) {
	st := newScheduleTestStore(t)
	tools := NewScheduleTools(ScheduleConfig{Store: st})
	var create runtime.Tool
	for _, tool := range tools {
		if tool.Name() == "schedule_create" {
			create = tool
		}
	}
	if create == nil {
		t.Fatal("schedule_create not found")
	}
	ctx := runtime.WithSessionID(context.Background(), "origin-session-1")
	args := json.RawMessage(`{"title":"Digest","prompt":"summarize","cron":"@daily"}`)
	out, err := create.Invoke(ctx, args)
	if err != nil {
		t.Fatalf("schedule_create in session: %v", err)
	}
	var result struct {
		Schedule store.Automation `json:"schedule"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Schedule.OriginSessionID != "origin-session-1" {
		t.Fatalf("origin_session_id = %q, want origin-session-1", result.Schedule.OriginSessionID)
	}
	loaded, err := st.GetAutomation(result.Schedule.ID)
	if err != nil || loaded.OriginSessionID != "origin-session-1" {
		t.Fatalf("loaded origin = %+v, %v", loaded, err)
	}

	// No session context: the id stays empty and the app creates a session when
	// the task fires.
	out, err = create.Invoke(context.Background(), json.RawMessage(`{"title":"No origin","prompt":"p","cron":"@hourly"}`))
	if err != nil {
		t.Fatalf("schedule_create without session: %v", err)
	}
	var plain struct {
		Schedule store.Automation `json:"schedule"`
	}
	if err := json.Unmarshal([]byte(out), &plain); err != nil {
		t.Fatal(err)
	}
	if plain.Schedule.OriginSessionID != "" {
		t.Fatalf("session-less origin = %q, want empty", plain.Schedule.OriginSessionID)
	}
}
