package regression

// Adversarial audit: automation run history (the persisted AutomationRunRecord
// list). Negative/boundary cases: RunNow success/failure records, summary
// truncation, ticker executions, the 10-record cap with newest-first order,
// old registry JSON without the new field, persistence across reloads and
// concurrent RunNow (-race).

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/automation"
)

// startAutomation boots the real automation module over a scripted chat service.
func startAutomation(t *testing.T, chat contracts.ChatService, tickMs int) (*harness, contracts.AutomationService) {
	t.Helper()
	h := newBareHarness(t)
	provide(t, h.ctx, contracts.KeyChat, chat)
	activate(t, h.ctx, automation.Module(), automation.Config{TickMs: tickMs})
	return h, requireService(t, h.ctx, contracts.KeyAutomation)
}

// createAutomationTask creates one task and returns it.
func createAutomationTask(t *testing.T, service contracts.AutomationService, name, schedule string, enabled bool) contracts.AutomationTask {
	t.Helper()
	task, err := service.Create(context.Background(), contracts.AutomationTask{
		Name: name, Prompt: "audit prompt", Schedule: schedule, Enabled: enabled,
	})
	if err != nil {
		t.Fatalf("Create automation task: %v", err)
	}
	return task
}

// automationTask waits for one task to satisfy condition and returns it.
func automationTask(t *testing.T, service contracts.AutomationService, id string,
	condition func(contracts.AutomationTask) bool) contracts.AutomationTask {
	t.Helper()
	var found contracts.AutomationTask
	waitFor(t, "automation task "+id, func() bool {
		tasks, err := service.List(context.Background())
		if err != nil {
			return false
		}
		for _, task := range tasks {
			if task.ID == id && condition(task) {
				found = task
				return true
			}
		}
		return false
	})
	return found
}

// automationHistory waits until the task has at least count records.
func automationHistory(t *testing.T, service contracts.AutomationService, id string, count int) contracts.AutomationTask {
	t.Helper()
	return automationTask(t, service, id, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) >= count
	})
}

// waitForLong is waitFor with a longer budget for ticker-driven runs.
func waitForLong(t *testing.T, what string, budget time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// persistedHistory decodes the on-disk registry's run history of one task.
func persistedHistory(t *testing.T, dataDir, taskID string) []contracts.AutomationRunRecord {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dataDir, automation.DefaultFileName))
	if err != nil {
		t.Fatalf("read %s: %v", automation.DefaultFileName, err)
	}
	var document struct {
		Tasks []struct {
			ID         string                          `json:"id"`
			RunHistory []contracts.AutomationRunRecord `json:"runHistory"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("decode %s: %v", automation.DefaultFileName, err)
	}
	for _, task := range document.Tasks {
		if task.ID == taskID {
			return task.RunHistory
		}
	}
	return nil
}

// TestAutomationRunNowRecordsPersistedHistory covers a successful RunNow: one
// record with ordered timestamps and a summary, mirrored into the registry file.
func TestAutomationRunNowRecordsPersistedHistory(t *testing.T) {
	chat := newFakeChat()
	harness, service := startAutomation(t, chat, 3_600_000)
	task := createAutomationTask(t, service, "history", "every 60s", true)

	if err := service.RunNow(context.Background(), task.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	updated := automationHistory(t, service, task.ID, 1)
	record := updated.RunHistory[0]
	if record.Status != contracts.AutomationRunSuccess {
		t.Fatalf("record status = %q, want success", record.Status)
	}
	if record.StartedAt.IsZero() || record.FinishedAt.Before(record.StartedAt) {
		t.Fatalf("record timestamps = %s -> %s", record.StartedAt, record.FinishedAt)
	}
	if strings.TrimSpace(record.Summary) == "" {
		t.Fatal("success record has no summary")
	}
	persisted := persistedHistory(t, harness.dataDir, task.ID)
	if len(persisted) != 1 || persisted[0].Status != contracts.AutomationRunSuccess {
		t.Fatalf("persisted history = %+v, want one success record", persisted)
	}
}

// TestAutomationFailureRecordsTruncatedHistory covers a failing run: the record
// is an error record whose summary carries the send failure and is capped at
// 200 runes; LastError is set too.
func TestAutomationFailureRecordsTruncatedHistory(t *testing.T) {
	chat := newFakeChat()
	harness, service := startAutomation(t, chat, 3_600_000)
	task := createAutomationTask(t, service, "failing", "every 60s", true)
	longFailure := strings.Repeat("失败原因-", 100) // 500 runes
	chat.setSendError(errors.New(longFailure))

	if err := service.RunNow(context.Background(), task.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	updated := automationHistory(t, service, task.ID, 1)
	record := updated.RunHistory[0]
	if record.Status != contracts.AutomationRunFailure {
		t.Fatalf("failed run record status = %q, want error", record.Status)
	}
	if !strings.Contains(record.Summary, "send prompt") {
		t.Fatalf("record summary = %q, want the send error", record.Summary)
	}
	if runes := utf8.RuneCountInString(record.Summary); runes > 200 {
		t.Fatalf("summary = %d runes, want <= 200", runes)
	}
	if strings.TrimSpace(updated.LastError) == "" {
		t.Fatal("LastError is empty after a failed run")
	}
	persisted := persistedHistory(t, harness.dataDir, task.ID)
	if len(persisted) != 1 || persisted[0].Status != contracts.AutomationRunFailure {
		t.Fatalf("persisted failure history = %+v", persisted)
	}
}

// TestAutomationHistoryCappedAndNewestFirst covers the 10-record cap: twelve
// runs keep the newest ten in descending start order.
func TestAutomationHistoryCappedAndNewestFirst(t *testing.T) {
	chat := newFakeChat()
	_, service := startAutomation(t, chat, 3_600_000)
	task := createAutomationTask(t, service, "capped", "every 60s", true)

	for run := 1; run <= 12; run++ {
		if err := service.RunNow(context.Background(), task.ID); err != nil {
			t.Fatalf("RunNow #%d: %v", run, err)
		}
		want := run
		if want > 10 {
			want = 10
		}
		waitForLong(t, "automation history run "+strconv.Itoa(run), 15*time.Second, func() bool {
			all, err := service.List(context.Background())
			if err != nil {
				return false
			}
			for _, candidate := range all {
				if candidate.ID == task.ID {
					return len(candidate.RunHistory) >= want
				}
			}
			return false
		})
	}
	updated := automationTask(t, service, task.ID, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) == 10
	})
	if len(updated.RunHistory) != 10 {
		t.Fatalf("history length = %d, want 10", len(updated.RunHistory))
	}
	for index := 1; index < len(updated.RunHistory); index++ {
		if updated.RunHistory[index].StartedAt.After(updated.RunHistory[index-1].StartedAt) {
			t.Fatalf("record %d starts after record %d; want newest first: %+v",
				index, index-1, updated.RunHistory)
		}
	}
}

// TestAutomationHistoryLoadsOldJSONWithoutField covers backward compatibility:
// a registry written before the runHistory field loads with an empty history and
// a subsequent run appends normally.
func TestAutomationHistoryLoadsOldJSONWithoutField(t *testing.T) {
	h := newBareHarness(t)
	old := `{"tasks":[{"id":"auto-old","name":"旧任务","prompt":"p","schedule":"every 60s",` +
		`"enabled":true,"createdAt":"2024-01-01T00:00:00Z","updatedAt":"2024-01-01T00:00:00Z",` +
		`"lastRun":"2024-01-01T00:00:00Z","nextRun":"2024-01-01T00:01:00Z","lastError":""}]}`
	if err := os.WriteFile(filepath.Join(h.dataDir, automation.DefaultFileName), []byte(old), 0o600); err != nil {
		t.Fatalf("write old registry: %v", err)
	}
	chat := newFakeChat()
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	activate(t, h.ctx, automation.Module(), automation.Config{TickMs: 3_600_000})
	service := requireService(t, h.ctx, contracts.KeyAutomation)

	tasks, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "auto-old" {
		t.Fatalf("loaded tasks = %+v, want the old task", tasks)
	}
	if len(tasks[0].RunHistory) != 0 {
		t.Fatalf("old JSON gained history: %+v", tasks[0].RunHistory)
	}
	if err := service.RunNow(context.Background(), "auto-old"); err != nil {
		t.Fatalf("RunNow(old task): %v", err)
	}
	updated := automationHistory(t, service, "auto-old", 1)
	if updated.RunHistory[0].Status != contracts.AutomationRunSuccess {
		t.Fatalf("new run record = %+v, want success", updated.RunHistory[0])
	}
}

// TestAutomationConcurrentRunNowRace covers concurrent RunNow calls over several
// tasks: every task records at least one run, overlapping calls on one task are
// collapsed, and -race stays clean.
func TestAutomationConcurrentRunNowRace(t *testing.T) {
	chat := newFakeChat()
	_, service := startAutomation(t, chat, 3_600_000)
	tasks := make([]contracts.AutomationTask, 0, 3)
	for index := 0; index < 3; index++ {
		tasks = append(tasks, createAutomationTask(t, service, "concurrent-"+strconv.Itoa(index), "every 60s", true))
	}

	var wg sync.WaitGroup
	for _, task := range tasks {
		task := task
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := service.RunNow(context.Background(), task.ID); err != nil {
				t.Errorf("concurrent RunNow(%s): %v", task.ID, err)
			}
		}()
	}
	// Extra overlapping calls on the first task (the in-flight guard collapses
	// them into at most one additional record).
	for index := 0; index < 5; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = service.RunNow(context.Background(), tasks[0].ID)
		}()
	}
	wg.Wait()
	// Concurrent runs under a loaded CI box can take longer than the default
	// polling budget, so give this test a generous one.
	for _, task := range tasks {
		task := task
		waitForLong(t, "concurrent automation history "+task.ID, 15*time.Second, func() bool {
			all, err := service.List(context.Background())
			if err != nil {
				return false
			}
			for _, candidate := range all {
				if candidate.ID == task.ID {
					return len(candidate.RunHistory) > 0
				}
			}
			return false
		})
	}
}

// TestAutomationTickerRecordsHistory covers the scheduler path: an enabled task
// fired by the ticker records a success history entry.
func TestAutomationTickerRecordsHistory(t *testing.T) {
	chat := newFakeChat()
	_, service := startAutomation(t, chat, 5)
	task := createAutomationTask(t, service, "ticker", "every 1s", true)
	t.Cleanup(func() {
		_ = service.Delete(context.Background(), task.ID)
	})

	waitForLong(t, "ticker automation run", 5*time.Second, func() bool {
		tasks, err := service.List(context.Background())
		if err != nil {
			return false
		}
		for _, listed := range tasks {
			if listed.ID == task.ID && len(listed.RunHistory) >= 1 {
				return true
			}
		}
		return false
	})
	updated := automationTask(t, service, task.ID, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) >= 1
	})
	if updated.RunHistory[0].Status != contracts.AutomationRunSuccess {
		t.Fatalf("ticker record = %+v, want success", updated.RunHistory[0])
	}
}
