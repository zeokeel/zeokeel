package automation

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/store"
)

// fakeStore is an in-memory Store for the scheduler tests.
type fakeStore struct {
	mu    sync.Mutex
	tasks []store.Automation
	next  map[string]time.Time
	last  map[string]time.Time
	err   error
}

func newFakeStore(tasks ...store.Automation) *fakeStore {
	return &fakeStore{
		tasks: tasks,
		next:  make(map[string]time.Time),
		last:  make(map[string]time.Time),
	}
}

func (f *fakeStore) ListAutomations() ([]store.Automation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := make([]store.Automation, len(f.tasks))
	copy(out, f.tasks)
	return out, nil
}

func (f *fakeStore) SetAutomationNextRun(id string, next time.Time) (*store.Automation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.next[id] = next
	for i := range f.tasks {
		if f.tasks[i].ID == id {
			at := next
			f.tasks[i].NextRun = &at
			out := f.tasks[i]
			return &out, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeStore) MarkAutomationRun(id string, lastRun time.Time, nextRun *time.Time) (*store.Automation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.last[id] = lastRun
	for i := range f.tasks {
		if f.tasks[i].ID == id {
			at := lastRun
			f.tasks[i].LastRun = &at
			if nextRun != nil {
				next := *nextRun
				f.tasks[i].NextRun = &next
				f.next[id] = next
			} else {
				f.tasks[i].NextRun = nil
				delete(f.next, id)
			}
			out := f.tasks[i]
			return &out, nil
		}
	}
	return nil, store.ErrNotFound
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestSchedulerFiresDueTaskWithFakeClock(t *testing.T) {
	now := mustTime(t, "2026-11-04T10:07:00Z")
	due := store.Automation{
		ID: "a", Title: "every five", Cron: "*/5 * * * *", Enabled: true,
		NextRun: ptrTime(mustTime(t, "2026-11-04T10:05:00Z")),
	}
	future := store.Automation{
		ID: "b", Title: "later", Cron: "*/5 * * * *", Enabled: true,
		NextRun: ptrTime(mustTime(t, "2026-11-04T10:10:00Z")),
	}
	disabled := store.Automation{
		ID: "c", Title: "off", Cron: "*/5 * * * *", Enabled: false,
		NextRun: ptrTime(mustTime(t, "2026-11-04T10:00:00Z")),
	}
	fs := newFakeStore(due, future, disabled)
	var fired []store.Automation
	s, err := New(Config{
		Store:  fs,
		OnFire: func(task store.Automation) { fired = append(fired, task) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got := s.Tick(now)
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("Tick fired %+v, want only task a", got)
	}
	if len(fired) != 1 || fired[0].ID != "a" || fired[0].Title != "every five" {
		t.Fatalf("OnFire saw %+v", fired)
	}
	if last, ok := fs.last["a"]; !ok || !last.Equal(now) {
		t.Fatalf("last run = %v, %v; want %v", last, ok, now)
	}
	wantNext := mustTime(t, "2026-11-04T10:10:00Z")
	if next, ok := fs.next["a"]; !ok || !next.Equal(wantNext) {
		t.Fatalf("next run = %v, %v; want %v", next, ok, wantNext)
	}
	if _, ok := fs.last["b"]; ok {
		t.Fatal("future task fired")
	}
	if _, ok := fs.last["c"]; ok {
		t.Fatal("disabled task fired")
	}
	if err := s.LastError(); err != nil {
		t.Fatalf("LastError = %v", err)
	}
}

func TestSchedulerSchedulesTaskWithoutNextRun(t *testing.T) {
	now := mustTime(t, "2026-11-04T10:07:00Z")
	task := store.Automation{
		ID: "a", Title: "daily", Cron: "0 9 * * *", Enabled: true,
	}
	fs := newFakeStore(task)
	s, err := New(Config{Store: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := s.Tick(now); len(got) != 0 {
		t.Fatalf("Tick fired %+v, want none", got)
	}
	want := mustTime(t, "2026-11-05T09:00:00Z")
	if next, ok := fs.next["a"]; !ok || !next.Equal(want) {
		t.Fatalf("scheduled next = %v, %v; want %v", next, ok, want)
	}
	if _, ok := fs.last["a"]; ok {
		t.Fatal("scheduling a task recorded a run")
	}
	// The next tick before the due time does not fire it.
	if got := s.Tick(now.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("Tick before due fired %+v", got)
	}
	// At the due instant it fires.
	got := s.Tick(want)
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("Tick at due fired %+v", got)
	}
	if next, ok := fs.next["a"]; !ok || !next.Equal(want.AddDate(0, 0, 1)) {
		t.Fatalf("next after fire = %v, %v", next, ok)
	}
}

func TestSchedulerOrdersDueTasksAndSkipsInvalidCron(t *testing.T) {
	now := mustTime(t, "2026-11-04T10:07:00Z")
	fs := newFakeStore(
		store.Automation{ID: "late", Title: "late", Cron: "@hourly", Enabled: true,
			NextRun: ptrTime(mustTime(t, "2026-11-04T10:06:00Z"))},
		store.Automation{ID: "early", Title: "early", Cron: "@hourly", Enabled: true,
			NextRun: ptrTime(mustTime(t, "2026-11-04T10:00:00Z"))},
		store.Automation{ID: "bad", Title: "bad", Cron: "not a cron", Enabled: true,
			NextRun: ptrTime(mustTime(t, "2026-11-04T09:00:00Z"))},
	)
	var order []string
	s, err := New(Config{Store: fs, OnFire: func(task store.Automation) {
		order = append(order, task.ID)
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := s.Tick(now)
	if len(got) != 2 {
		t.Fatalf("Tick fired %d tasks (%+v), want 2", len(got), got)
	}
	if len(order) != 2 || order[0] != "early" || order[1] != "late" {
		t.Fatalf("fire order = %v, want [early late]", order)
	}
	if err := s.LastError(); err == nil {
		t.Fatal("invalid cron did not record an error")
	}
}

func TestSchedulerNilOnFireAdvancesSchedule(t *testing.T) {
	now := mustTime(t, "2026-11-04T10:07:00Z")
	fs := newFakeStore(store.Automation{
		ID: "a", Title: "a", Cron: "@hourly", Enabled: true,
		NextRun: ptrTime(mustTime(t, "2026-11-04T10:00:00Z")),
	})
	s, err := New(Config{Store: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := s.Tick(now); len(got) != 1 {
		t.Fatalf("Tick fired %+v", got)
	}
	if _, ok := fs.last["a"]; !ok {
		t.Fatal("a fired task without OnFire did not record its run")
	}
}

func TestSchedulerNewValidation(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New accepted a nil store")
	}
}

func TestSchedulerListError(t *testing.T) {
	fs := newFakeStore()
	fs.err = errors.New("boom")
	s, err := New(Config{Store: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := s.Tick(time.Now()); got != nil {
		t.Fatalf("Tick = %+v, want nil", got)
	}
	if err := s.LastError(); err == nil {
		t.Fatal("list error not recorded")
	}
}

func TestSchedulerStartStop(t *testing.T) {
	now := mustTime(t, "2026-11-04T10:07:00Z")
	fs := newFakeStore(store.Automation{
		ID: "a", Title: "a", Cron: "@hourly", Enabled: true,
		NextRun: ptrTime(now),
	})
	fired := make(chan string, 1)
	s, err := New(Config{
		Store:    fs,
		OnFire:   func(task store.Automation) { fired <- task.ID },
		Now:      func() time.Time { return now },
		Interval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Start(t.Context())
	if !s.Running() {
		t.Fatal("Start did not mark the scheduler running")
	}
	select {
	case id := <-fired:
		if id != "a" {
			t.Fatalf("fired %q, want a", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not fire within 2s")
	}
	s.Stop()
	if s.Running() {
		t.Fatal("Stop left the scheduler running")
	}
	s.Stop() // idempotent
}

func TestSchedulerWithStore(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	now := mustTime(t, "2026-11-04T10:07:00Z")
	past := now.Add(-time.Minute)
	created, err := st.CreateAutomation(store.Automation{
		Title: "real store", Prompt: "p", Cron: "*/5 * * * *",
		Enabled: true, NextRun: &past,
	})
	if err != nil {
		t.Fatalf("CreateAutomation: %v", err)
	}
	var fired []string
	s, err := New(Config{
		Store:  st,
		OnFire: func(task store.Automation) { fired = append(fired, task.ID) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := s.Tick(now); len(got) != 1 {
		t.Fatalf("Tick fired %+v, want one", got)
	}
	if len(fired) != 1 || fired[0] != created.ID {
		t.Fatalf("OnFire = %v", fired)
	}
	stored, err := st.GetAutomation(created.ID)
	if err != nil {
		t.Fatalf("GetAutomation: %v", err)
	}
	if stored.LastRun == nil || !stored.LastRun.Equal(now) {
		t.Fatalf("LastRun = %v, want %v", stored.LastRun, now)
	}
	wantNext := mustTime(t, "2026-11-04T10:10:00Z")
	if stored.NextRun == nil || !stored.NextRun.Equal(wantNext) {
		t.Fatalf("NextRun = %v, want %v", stored.NextRun, wantNext)
	}
}

// TestSchedulerDailyUsesLocalTimezone pins RT-09: @daily is a local wall-clock
// expression (local midnight), while the persisted instant stays UTC.
func TestSchedulerDailyUsesLocalTimezone(t *testing.T) {
	shanghai := time.FixedZone("CST", 8*60*60)
	localNow := time.Date(2026, 1, 1, 0, 30, 0, 0, shanghai) // local 00:30
	task := store.Automation{ID: "a", Title: "daily", Prompt: "p", Cron: "@daily", Enabled: true}
	fs := newFakeStore(task)
	s, err := New(Config{Store: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := s.Tick(localNow); len(got) != 0 {
		t.Fatalf("Tick = %d fired, want 0 before next run", len(got))
	}
	stored, ok := fs.next["a"]
	if !ok {
		t.Fatal("Tick did not schedule a task without NextRun")
	}
	wantLocal := time.Date(2026, 1, 2, 0, 0, 0, 0, shanghai) // next local midnight
	wantUTC := wantLocal.UTC()
	if !stored.Equal(wantUTC) {
		t.Fatalf("next run = %v (%v), want %v (local %v)", stored, stored.UTC(), wantUTC, wantLocal)
	}
	// The persisted value must be UTC so the store encoding stays uniform.
	if _, offset := stored.Zone(); offset != 0 {
		t.Fatalf("persisted next run offset = %d, want UTC", offset)
	}
}

// TestSchedulerHourlyUsesLocalTopOfHour pins the @hourly half of RT-09: from a
// local 10:07 the next @hourly is local 11:00 (not 10:00 UTC).
func TestSchedulerHourlyUsesLocalTopOfHour(t *testing.T) {
	shanghai := time.FixedZone("CST", 8*60*60)
	localNow := time.Date(2026, 1, 1, 10, 7, 0, 0, shanghai)
	task := store.Automation{ID: "h", Title: "hourly", Prompt: "p", Cron: "@hourly", Enabled: true}
	fs := newFakeStore(task)
	s, err := New(Config{Store: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Tick(localNow)
	stored := fs.next["h"]
	want := time.Date(2026, 1, 1, 11, 0, 0, 0, shanghai).UTC()
	if !stored.Equal(want) {
		t.Fatalf("next run = %v, want %v", stored, want)
	}
}
