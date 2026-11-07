package automation

// TDD tests (RED first) for the automation run history: every RunNow/ticker
// execution appends a capped, persisted AutomationRunRecord.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestRunNowRecordsSuccessHistory(t *testing.T) {
	chat := newFakeChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "history", Prompt: "run", Schedule: "every 60s", Enabled: true,
	})
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	_ = waitCall(t, chat)
	task := waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) == 1
	})
	record := task.RunHistory[0]
	if record.Status != contracts.AutomationRunSuccess {
		t.Fatalf("record status = %q, want success", record.Status)
	}
	if record.StartedAt.IsZero() || record.FinishedAt.Before(record.StartedAt) {
		t.Fatalf("record timestamps = %s → %s", record.StartedAt, record.FinishedAt)
	}
	if strings.TrimSpace(record.Summary) == "" {
		t.Fatal("success record has no summary")
	}
}

func TestRunNowRecordsErrorHistoryAndTruncatesSummary(t *testing.T) {
	chat := newFakeChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "failing-history", Prompt: "run", Schedule: "every 60s", Enabled: true,
	})
	chat.setSendError(errors.New(strings.Repeat("长", 500)))
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	task := waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) == 1
	})
	record := task.RunHistory[0]
	if record.Status != contracts.AutomationRunFailure {
		t.Fatalf("record status = %q, want failure", record.Status)
	}
	if !strings.Contains(record.Summary, "send prompt") {
		t.Fatalf("record summary = %q, want the send error", record.Summary)
	}
	if got := len([]rune(record.Summary)); got > 200 {
		t.Fatalf("record summary = %d runes, want at most 200", got)
	}
}

func TestTickerRunRecordsHistory(t *testing.T) {
	chat := newFakeChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 10}, chat)
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "ticker-history", Prompt: "tick", Schedule: "every 1s", Enabled: true,
	})
	_ = waitCall(t, chat)
	task := waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) == 1
	})
	if task.RunHistory[0].Status != contracts.AutomationRunSuccess {
		t.Fatalf("ticker record = %+v, want success", task.RunHistory[0])
	}
}

func TestRunHistoryCappedAtTenNewestFirst(t *testing.T) {
	chat := newFakeChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "many-runs", Prompt: "run", Schedule: "every 60s", Enabled: true,
	})
	for run := 1; run <= 12; run++ {
		if err := service.RunNow(context.Background(), created.ID); err != nil {
			t.Fatalf("RunNow #%d: %v", run, err)
		}
		_ = waitCall(t, chat)
		want := run
		if want > 10 {
			want = 10
		}
		waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
			return len(task.RunHistory) == want
		})
	}
	tasks, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 || len(tasks[0].RunHistory) != 10 {
		t.Fatalf("history = %d records, want 10", len(tasks[0].RunHistory))
	}
	records := tasks[0].RunHistory
	for index := 1; index < len(records); index++ {
		if records[index].StartedAt.After(records[index-1].StartedAt) {
			t.Fatalf("record %d is newer than %d; want newest first: %+v", index, index-1, records)
		}
	}
}

func TestRunHistoryPersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	chat := newFakeChat()
	service, err := newService(dir, Config{TickMs: 3600000}, &fakeLogger{}, chat)
	if err != nil {
		t.Fatalf("newService: %v", err)
	}
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "persisted", Prompt: "run", Schedule: "every 60s", Enabled: true,
	})
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	_ = waitCall(t, chat)
	before := waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) == 1
	}).RunHistory[0]
	service.Close()

	reloaded, err := newService(dir, Config{TickMs: 3600000}, &fakeLogger{}, newFakeChat())
	if err != nil {
		t.Fatalf("reload service: %v", err)
	}
	t.Cleanup(reloaded.Close)
	tasks, err := reloaded.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 || len(tasks[0].RunHistory) != 1 {
		t.Fatalf("reloaded history = %+v, want one record", tasks)
	}
	after := tasks[0].RunHistory[0]
	if !after.StartedAt.Equal(before.StartedAt) || after.Status != before.Status || after.Summary != before.Summary {
		t.Fatalf("reloaded record = %+v, want %+v", after, before)
	}
}

func TestRunHistoryLoadsOldJSONWithoutHistory(t *testing.T) {
	dir := t.TempDir()
	old := `{"tasks":[{"id":"auto-old","name":"旧任务","prompt":"p","schedule":"every 60s","enabled":true,"createdAt":"2024-01-01T00:00:00Z","updatedAt":"2024-01-01T00:00:00Z","lastRun":"2024-01-01T00:00:00Z","nextRun":"2024-01-01T00:01:00Z","lastError":""}]}`
	if err := os.WriteFile(filepath.Join(dir, DefaultFileName), []byte(old), fileMode); err != nil {
		t.Fatalf("write old registry: %v", err)
	}
	service := newTestService(t, dir, Config{TickMs: 3600000}, newFakeChat())
	tasks, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "auto-old" {
		t.Fatalf("loaded tasks = %+v", tasks)
	}
	if len(tasks[0].RunHistory) != 0 {
		t.Fatalf("old JSON gained history: %+v", tasks[0].RunHistory)
	}
}

func TestConcurrentRunNowRecordsHistory(t *testing.T) {
	chat := newFakeChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "concurrent", Prompt: "run", Schedule: "every 60s", Enabled: true,
	})
	var wg sync.WaitGroup
	for index := 0; index < 10; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = service.RunNow(context.Background(), created.ID)
		}()
	}
	wg.Wait()
	waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) >= 1
	})
	// Drain the asynchronous executions (the in-flight guard collapses
	// overlapping RunNow calls into at most one active run).
	// Generous budget: parallel CI load can delay the asynchronous runs.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		task := waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool { return true })
		if task.LastRun.IsZero() {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if !service.inFlightForTest(created.ID) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	tasks, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks[0].RunHistory) == 0 {
		t.Fatal("concurrent RunNow recorded no history")
	}
}

// inFlightForTest reports the in-flight flag under the service lock (the
// production map may be mutated by the scheduler goroutine).
func (s *service) inFlightForTest(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inFlight[taskID]
}
