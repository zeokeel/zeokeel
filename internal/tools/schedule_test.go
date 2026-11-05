package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/store"
)

func scheduleToolsFor(st *store.Store, now time.Time) map[string]runtime.Tool {
	out := make(map[string]runtime.Tool)
	for _, tool := range NewScheduleTools(ScheduleConfig{Store: st, Now: func() time.Time { return now }}) {
		out[tool.Name()] = tool
	}
	return out
}

func TestScheduleCreateListUpdateDeleteAndPersistence(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	closed := false
	closeStore := func() {
		t.Helper()
		if closed {
			return
		}
		closed = true
		if err := st.Close(); err != nil {
			t.Fatalf("store.Close: %v", err)
		}
	}
	defer closeStore()

	now := time.Date(2026, 1, 1, 0, 2, 0, 0, time.UTC)
	tools := scheduleToolsFor(st, now)
	want := []string{"schedule_create", "schedule_list", "schedule_update", "schedule_delete"}
	if len(tools) != len(want) {
		t.Fatalf("tools = %d, want %d", len(tools), len(want))
	}
	for _, name := range want {
		if tools[name] == nil {
			t.Fatalf("missing tool %q", name)
		}
		if strings.TrimSpace(tools[name].Description()) == "" {
			t.Fatalf("tool %q has an empty description", name)
		}
	}

	if _, err := sqInvoke(t, tools["schedule_create"], `{"title":"Bad","prompt":"p","cron":"not-a-cron"}`); err == nil {
		t.Fatal("schedule_create with an invalid cron = nil error")
	}
	if _, err := sqInvoke(t, tools["schedule_create"], `{"title":"","prompt":"p","cron":"@daily"}`); err == nil {
		t.Fatal("schedule_create with an empty title = nil error")
	}
	if _, err := sqInvoke(t, tools["schedule_create"], `{"title":"T","prompt":" ","cron":"@daily"}`); err == nil {
		t.Fatal("schedule_create with an empty prompt = nil error")
	}
	if _, err := sqInvoke(t, tools["schedule_create"], `{"title":"`+strings.Repeat("x", 121)+`","prompt":"p","cron":"@daily"}`); err == nil {
		t.Fatal("schedule_create with an overlong title = nil error")
	}

	out, err := sqInvoke(t, tools["schedule_create"], `{"title":"  Digest  ","prompt":"  Summarize changes  ","cron":"*/5 * * * *"}`)
	if err != nil {
		t.Fatalf("schedule_create: %v", err)
	}
	var created struct {
		Kind     string            `json:"kind"`
		Action   string            `json:"action"`
		Schedule *store.Automation `json:"schedule"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("decode create: %v\n%s", err, out)
	}
	if created.Kind != "schedule" || created.Action != "create" || created.Schedule == nil {
		t.Fatalf("created = %+v", created)
	}
	task := created.Schedule
	if task.ID == "" || task.Title != "Digest" || task.Prompt != "Summarize changes" || !task.Enabled {
		t.Fatalf("task = %+v", task)
	}
	wantNext := time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)
	if task.NextRun == nil || !task.NextRun.Equal(wantNext) {
		t.Fatalf("next_run = %v, want %v", task.NextRun, wantNext)
	}

	// Close and reopen to prove the tool persisted through the store rather
	// than keeping state in memory.
	closeStore()
	st2, err := store.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	closed = true
	tools = scheduleToolsFor(st2, now)

	out, err = sqInvoke(t, tools["schedule_list"], `{}`)
	if err != nil {
		t.Fatalf("schedule_list after reopen: %v", err)
	}
	var listed struct {
		Kind      string             `json:"kind"`
		Count     int                `json:"count"`
		Schedules []store.Automation `json:"schedules"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("decode list: %v\n%s", err, out)
	}
	if listed.Count != 1 || len(listed.Schedules) != 1 || listed.Schedules[0].ID != task.ID {
		t.Fatalf("listed = %+v", listed)
	}

	if _, err := sqInvoke(t, tools["schedule_update"], `{"id":"`+task.ID+`"}`); err == nil {
		t.Fatal("schedule_update with no changes = nil error")
	}
	if _, err := sqInvoke(t, tools["schedule_update"], `{"id":"missing","title":"x"}`); err == nil {
		t.Fatal("schedule_update for a missing id = nil error")
	}
	if _, err := sqInvoke(t, tools["schedule_update"], `{"id":"`+task.ID+`","cron":"bad"}`); err == nil {
		t.Fatal("schedule_update with an invalid cron = nil error")
	}

	out, err = sqInvoke(t, tools["schedule_update"], `{"id":"`+task.ID+`","title":"Renamed","cron":"0 9 * * *","enabled":true}`)
	if err != nil {
		t.Fatalf("schedule_update: %v", err)
	}
	var updated struct {
		Schedule *store.Automation `json:"schedule"`
	}
	if err := json.Unmarshal([]byte(out), &updated); err != nil {
		t.Fatalf("decode update: %v\n%s", err, out)
	}
	if updated.Schedule == nil || updated.Schedule.Title != "Renamed" || updated.Schedule.Cron != "0 9 * * *" {
		t.Fatalf("updated = %+v", updated.Schedule)
	}
	if updated.Schedule.NextRun == nil || !updated.Schedule.NextRun.After(now) {
		t.Fatalf("updated next_run = %v, want after %v", updated.Schedule.NextRun, now)
	}

	out, err = sqInvoke(t, tools["schedule_update"], `{"id":"`+task.ID+`","enabled":false}`)
	if err != nil {
		t.Fatalf("schedule_update disable: %v", err)
	}
	var disabled struct {
		Schedule *store.Automation `json:"schedule"`
	}
	if err := json.Unmarshal([]byte(out), &disabled); err != nil {
		t.Fatalf("decode disable: %v", err)
	}
	if disabled.Schedule == nil || disabled.Schedule.Enabled || disabled.Schedule.NextRun != nil {
		t.Fatalf("disabled schedule = %+v, want enabled=false and next_run=nil", disabled.Schedule)
	}

	out, err = sqInvoke(t, tools["schedule_delete"], `{"id":"`+task.ID+`"}`)
	if err != nil {
		t.Fatalf("schedule_delete: %v", err)
	}
	var deleted struct {
		Deleted bool `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(out), &deleted); err != nil || !deleted.Deleted {
		t.Fatalf("delete result = %+v, %v", deleted, err)
	}
	if _, err := sqInvoke(t, tools["schedule_delete"], `{"id":"`+task.ID+`"}`); err == nil {
		t.Fatal("second schedule_delete = nil error")
	}
	out, err = sqInvoke(t, tools["schedule_list"], `{}`)
	if err != nil {
		t.Fatalf("schedule_list after delete: %v", err)
	}
	var finalList struct {
		Count     int                `json:"count"`
		Schedules []store.Automation `json:"schedules"`
	}
	if err := json.Unmarshal([]byte(out), &finalList); err != nil {
		t.Fatalf("decode final list: %v", err)
	}
	if finalList.Count != 0 || len(finalList.Schedules) != 0 {
		t.Fatalf("final list = %+v, want empty", finalList)
	}
}

func TestScheduleCreateDisabledAndValidation(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	now := time.Date(2026, 2, 3, 4, 5, 0, 0, time.UTC)
	tools := scheduleToolsFor(st, now)

	out, err := sqInvoke(t, tools["schedule_create"], `{"title":"Paused","prompt":"p","cron":"@daily","enabled":false}`)
	if err != nil {
		t.Fatalf("schedule_create disabled: %v", err)
	}
	var created struct {
		Schedule *store.Automation `json:"schedule"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Schedule == nil || created.Schedule.Enabled || created.Schedule.NextRun != nil {
		t.Fatalf("disabled create = %+v", created.Schedule)
	}
	if _, err := sqInvoke(t, tools["schedule_create"], `{"title":"T","prompt":"p","cron":"*/0 * * * *"}`); err == nil {
		t.Fatal("schedule_create with a zero interval = nil error")
	}
	if _, err := sqInvoke(t, tools["schedule_create"], `{`); err == nil {
		t.Fatal("schedule_create with invalid JSON = nil error")
	}
}

func TestScheduleToolsWithoutStore(t *testing.T) {
	for _, tool := range NewScheduleTools(ScheduleConfig{}) {
		if _, err := tool.Invoke(context.Background(), json.RawMessage(`{}`)); err == nil {
			t.Fatalf("%s without a store = nil error", tool.Name())
		}
	}
}

// TestScheduleCreateUsesLocalTimezone pins RT-09 at the tool boundary: with a
// local-zone clock, @daily must resolve to the next local midnight and persist
// that instant in UTC.
func TestScheduleCreateUsesLocalTimezone(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	shanghai := time.FixedZone("CST", 8*60*60)
	now := time.Date(2026, 1, 1, 0, 30, 0, 0, shanghai)
	tools := scheduleToolsFor(st, now)
	out, err := sqInvoke(t, tools["schedule_create"], `{"title":"Daily","prompt":"p","cron":"@daily"}`)
	if err != nil {
		t.Fatalf("schedule_create: %v", err)
	}
	var created struct {
		Schedule *store.Automation `json:"schedule"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if created.Schedule == nil || created.Schedule.NextRun == nil {
		t.Fatal("schedule_create returned no next_run")
	}
	want := time.Date(2026, 1, 2, 0, 0, 0, 0, shanghai).UTC()
	if !created.Schedule.NextRun.Equal(want) {
		t.Fatalf("next_run = %v, want %v", created.Schedule.NextRun, want)
	}
	stored, err := st.GetAutomation(created.Schedule.ID)
	if err != nil {
		t.Fatalf("GetAutomation: %v", err)
	}
	if stored.NextRun == nil || !stored.NextRun.Equal(want) {
		t.Fatalf("stored next_run = %v, want %v", stored.NextRun, want)
	}
}
