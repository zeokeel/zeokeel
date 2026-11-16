package automation

// Tests for the DSH semantics of automation runs: original-session delivery,
// the delivered vs completed two-state record, settlement from the chat session
// state (success/failed/interrupted), subscription lifetime, restart catch-up
// and old-registry compatibility.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// waitTerminal waits until the task's latest run record reached a terminal
// state and returns the task.
func waitTerminal(t *testing.T, service contracts.AutomationService, id string) contracts.AutomationTask {
	t.Helper()
	return waitTask(t, service, id, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) > 0 && task.RunHistory[0].Terminal()
	})
}

// TestRunsReuseBoundSession covers the default DSH behavior: the first run
// creates the task's conversation, remembers it durably, and later runs deliver
// into it (including after a restart).
func TestRunsReuseBoundSession(t *testing.T) {
	dir := t.TempDir()
	chat := newFakeChat()
	service := newTestService(t, dir, Config{TickMs: 3600000}, chat)
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "同会话", Prompt: "run", Schedule: "every 60s", Enabled: true,
	})
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow #1: %v", err)
	}
	firstCall := waitCall(t, chat)
	first := waitTerminal(t, service, created.ID)
	if first.SessionID != firstCall.SessionID {
		t.Fatalf("task bound session = %q, want %q", first.SessionID, firstCall.SessionID)
	}
	if first.RunHistory[0].SessionID != firstCall.SessionID {
		t.Fatalf("run record session = %q, want %q", first.RunHistory[0].SessionID, firstCall.SessionID)
	}

	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow #2: %v", err)
	}
	secondCall := waitCall(t, chat)
	if secondCall.SessionID != firstCall.SessionID {
		t.Fatalf("second run session = %q, want the bound %q", secondCall.SessionID, firstCall.SessionID)
	}
	if chat.sessionCount() != 1 {
		t.Fatalf("chat sessions = %d, want the one bound session", chat.sessionCount())
	}
	// Wait for the second record itself: the first record is already terminal,
	// so a bare waitTerminal can return the previous snapshot while the second
	// run is still being recorded (flake under the race detector's load).
	second := waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) >= 2 && task.RunHistory[0].Terminal()
	})
	if len(second.RunHistory) != 2 {
		t.Fatalf("history = %d records, want 2", len(second.RunHistory))
	}
	service.Close()

	// The binding is durable: a restarted service reuses the same conversation.
	reloaded := newStoredService(t, dir, chat)
	tasks, err := reloaded.List(context.Background())
	if err != nil || len(tasks) != 1 || tasks[0].SessionID != firstCall.SessionID {
		t.Fatalf("reloaded tasks = %+v/%v, want the bound session", tasks, err)
	}
	if err := reloaded.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow after reload: %v", err)
	}
	thirdCall := waitCall(t, chat)
	if thirdCall.SessionID != firstCall.SessionID {
		t.Fatalf("reloaded run session = %q, want %q", thirdCall.SessionID, firstCall.SessionID)
	}
	if chat.sessionCount() != 1 {
		t.Fatalf("chat sessions after reload = %d, want 1", chat.sessionCount())
	}
}

// TestNewSessionPerRunOption covers the explicit legacy-mode option: every run
// starts a fresh conversation and the task does not remember one.
func TestNewSessionPerRunOption(t *testing.T) {
	chat := newFakeChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "每次新建", Prompt: "run", Schedule: "every 60s", Enabled: true,
		NewSessionPerRun: true,
	})
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow #1: %v", err)
	}
	firstCall := waitCall(t, chat)
	waitTerminal(t, service, created.ID)
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow #2: %v", err)
	}
	secondCall := waitCall(t, chat)
	if secondCall.SessionID == firstCall.SessionID {
		t.Fatalf("new-session mode reused %q", firstCall.SessionID)
	}
	task := waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) >= 2 && task.RunHistory[0].Terminal()
	})
	if task.SessionID != "" {
		t.Fatalf("new-session task remembered %q, want none", task.SessionID)
	}
	if chat.sessionCount() != 2 {
		t.Fatalf("chat sessions = %d, want 2", chat.sessionCount())
	}
}

// TestDeliveredThenCompleted covers the two-state record: the prompt delivery is
// recorded immediately (status delivered, deliveredAt set), the terminal state
// is written back when the chat session settles.
func TestDeliveredThenCompleted(t *testing.T) {
	chat := newFakeChat()
	chat.setHold(true)
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "两态", Prompt: "run", Schedule: "every 60s", Enabled: true,
	})
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	call := waitCall(t, chat)
	delivered := waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) == 1 && task.RunHistory[0].Status == contracts.AutomationRunDelivered
	})
	record := delivered.RunHistory[0]
	if record.DeliveredAt.IsZero() || !record.CompletedAt.IsZero() {
		t.Fatalf("delivered record timestamps = %+v", record)
	}
	if record.SessionID != call.SessionID {
		t.Fatalf("delivered record session = %q, want %q", record.SessionID, call.SessionID)
	}
	if !strings.Contains(record.Summary, call.SessionID) {
		t.Fatalf("delivered summary = %q, want the session reference", record.Summary)
	}

	chat.complete(call.SessionID)
	completed := waitTerminal(t, service, created.ID)
	record = completed.RunHistory[0]
	if record.Status != contracts.AutomationRunSuccess {
		t.Fatalf("settled status = %q, want success", record.Status)
	}
	if record.CompletedAt.Before(record.DeliveredAt) {
		t.Fatalf("completed %s before delivered %s", record.CompletedAt, record.DeliveredAt)
	}
	if record.DurationMS < 0 {
		t.Fatalf("duration = %d", record.DurationMS)
	}
	if completed.LastError != "" {
		t.Fatalf("LastError = %q, want empty", completed.LastError)
	}
}

// TestRunFailureSettles covers a failed agent turn settling the delivered run to
// a visible failure with the error text.
func TestRunFailureSettles(t *testing.T) {
	chat := newFakeChat()
	chat.setHold(true)
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "失败回写", Prompt: "run", Schedule: "every 60s", Enabled: true,
	})
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	call := waitCall(t, chat)
	waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) == 1 && task.RunHistory[0].Status == contracts.AutomationRunDelivered
	})
	chat.fail(call.SessionID, "provider exploded")
	failed := waitTerminal(t, service, created.ID)
	record := failed.RunHistory[0]
	if record.Status != contracts.AutomationRunFailure {
		t.Fatalf("settled status = %q, want failure", record.Status)
	}
	if !strings.Contains(record.Error, "provider exploded") {
		t.Fatalf("record error = %q", record.Error)
	}
	if !strings.Contains(failed.LastError, "provider exploded") {
		t.Fatalf("LastError = %q", failed.LastError)
	}
}

// TestRunInterruptedSettles covers a user-cancelled run: the record settles to
// the neutral interrupted state and does not claim a failure.
func TestRunInterruptedSettles(t *testing.T) {
	chat := newFakeChat()
	chat.setHold(true)
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "中断回写", Prompt: "run", Schedule: "every 60s", Enabled: true,
	})
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	call := waitCall(t, chat)
	waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) == 1 && task.RunHistory[0].Status == contracts.AutomationRunDelivered
	})
	chat.interrupt(call.SessionID)
	interrupted := waitTerminal(t, service, created.ID)
	record := interrupted.RunHistory[0]
	if record.Status != contracts.AutomationRunInterrupted {
		t.Fatalf("settled status = %q, want interrupted", record.Status)
	}
	if interrupted.LastError != "" {
		t.Fatalf("LastError = %q, want empty for an interruption", interrupted.LastError)
	}
}

// TestTriggerFoldedWhileRunInFlight pins that a second trigger while the
// delivered run is still running is folded instead of starting a duplicate.
func TestTriggerFoldedWhileRunInFlight(t *testing.T) {
	chat := newFakeChat()
	chat.setHold(true)
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "折叠", Prompt: "run", Schedule: "every 60s", Enabled: true,
	})
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	call := waitCall(t, chat)
	waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) == 1 && task.RunHistory[0].Status == contracts.AutomationRunDelivered
	})
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow while running: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := chat.sendCount(); got != 1 {
		t.Fatalf("sends = %d, want the in-flight run folded", got)
	}
	task, err := service.List(context.Background())
	if err != nil || len(task) != 1 || len(task[0].RunHistory) != 1 {
		t.Fatalf("history while folded = %+v/%v", task, err)
	}
	chat.complete(call.SessionID)
	waitTerminal(t, service, created.ID)
}

// TestCloseReleasesChatSubscription pins the subscription lifetime: Close
// unsubscribes from the chat service (the module effect owns Close).
func TestCloseReleasesChatSubscription(t *testing.T) {
	chat := newFakeChat()
	service, err := newService(t.TempDir(), Config{TickMs: 3600000}, &fakeLogger{}, chat, nil)
	if err != nil {
		t.Fatalf("newService: %v", err)
	}
	if got := chat.subscriberCount(); got != 1 {
		t.Fatalf("chat subscribers = %d, want 1", got)
	}
	service.Close()
	if got := chat.subscriberCount(); got != 0 {
		t.Fatalf("chat subscribers after Close = %d, want 0", got)
	}
}

// TestRestartSettlesPersistedDeliveredRun covers restart durability of the
// two-state record: a run delivered before the shutdown is settled from the
// restored session read model when the service reopens.
func TestRestartSettlesPersistedDeliveredRun(t *testing.T) {
	dir := t.TempDir()
	chat := newFakeChat()
	chat.seedSession("sess-old", "自动化: 旧任务", false)
	registry := `{"tasks":[{"id":"auto-old","name":"旧任务","prompt":"p","schedule":"every 60s",` +
		`"enabled":true,"sessionId":"sess-old","createdAt":"2026-01-01T00:00:00Z",` +
		`"updatedAt":"2026-01-01T00:00:00Z","lastRun":"2026-01-01T00:00:00Z",` +
		`"nextRun":"2026-01-01T00:01:00Z","lastError":"","runHistory":[` +
		`{"id":"run-old","startedAt":"2026-01-01T00:00:00Z","deliveredAt":"2026-01-01T00:00:01Z",` +
		`"status":"delivered","sessionId":"sess-old","summary":"ran in chat session sess-old"}]}]}`
	if err := os.WriteFile(filepath.Join(dir, DefaultFileName), []byte(registry), fileMode); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	service := newTestService(t, dir, Config{TickMs: 3600000}, chat)
	tasks, err := service.List(context.Background())
	if err != nil || len(tasks) != 1 {
		t.Fatalf("List = %+v/%v", tasks, err)
	}
	if len(tasks[0].RunHistory) != 1 {
		t.Fatalf("history = %+v", tasks[0].RunHistory)
	}
	record := tasks[0].RunHistory[0]
	if record.Status != contracts.AutomationRunSuccess || record.CompletedAt.IsZero() {
		t.Fatalf("restart settlement = %+v, want a completed success", record)
	}
}

// TestRestartCatchesUpMostRecentRun covers DSH's restart catch-up: a task whose
// last target passed while the host was off fires once after startup and then
// continues from the current time.
func TestRestartCatchesUpMostRecentRun(t *testing.T) {
	dir := t.TempDir()
	registry := `{"tasks":[{"id":"auto-missed","name":"补跑","prompt":"p","schedule":"every 60s",` +
		`"enabled":true,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z",` +
		`"lastRun":"2026-01-01T00:00:00Z","nextRun":"2026-01-01T00:01:00Z","lastError":""}]}`
	if err := os.WriteFile(filepath.Join(dir, DefaultFileName), []byte(registry), fileMode); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	chat := newFakeChat()
	service := newTestService(t, dir, Config{TickMs: 10}, chat)
	ran := waitTask(t, service, "auto-missed", func(task contracts.AutomationTask) bool {
		return !task.LastRun.IsZero() && len(task.RunHistory) == 1
	})
	if !ran.NextRun.After(time.Now()) {
		t.Fatalf("NextRun after catch-up = %s, want a future instant", ran.NextRun)
	}
	if env := ran.NextRun; !env.IsZero() {
		// The catch-up happens exactly once; the next target is in the future.
		time.Sleep(150 * time.Millisecond)
		tasks, err := service.List(context.Background())
		if err != nil || len(tasks) != 1 || len(tasks[0].RunHistory) != 1 {
			t.Fatalf("history after catch-up = %+v/%v, want one run", tasks, err)
		}
	}
}

// TestCanonicalSchedulePersisted covers the canonical DSH selector strings the
// Rig tools store: they are accepted, normalized and scheduled in their zone.
func TestCanonicalSchedulePersisted(t *testing.T) {
	chat := newFakeChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "周报", Prompt: "p", Schedule: "weekly 09:00:00 Asia/Shanghai 5,1", Enabled: true,
	})
	if created.Schedule != "weekly 09:00:00.000 Asia/Shanghai 1,5" {
		t.Fatalf("stored schedule = %q", created.Schedule)
	}
	if created.NextRun.IsZero() || !created.NextRun.After(time.Now()) {
		t.Fatalf("NextRun = %s, want a future instant", created.NextRun)
	}

	updated, err := service.Update(context.Background(), contracts.AutomationTask{
		ID: created.ID, Name: created.Name, Prompt: created.Prompt, Enabled: true,
		Schedule: "cron 0-59/15 9-17 * * 1-5 Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("Update(cron): %v", err)
	}
	if updated.Schedule != "cron 0-45/15 9-17 * * 1-5 Asia/Shanghai" {
		t.Fatalf("cron schedule = %q", updated.Schedule)
	}
	if updated.NextRun.IsZero() || !updated.NextRun.After(time.Now()) {
		t.Fatalf("cron NextRun = %s", updated.NextRun)
	}
}

// TestPastAtScheduleRejected pins the DSH "at must be in the future" rule for
// enabled tasks and accepts a future absolute target.
func TestPastAtScheduleRejected(t *testing.T) {
	chat := newFakeChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	if _, err := service.Create(context.Background(), contracts.AutomationTask{
		Name: "过去", Prompt: "p", Schedule: "at 2020-01-01T00:00:00Z", Enabled: true,
	}); err == nil {
		t.Fatal("Create accepted a past at schedule")
	}
	target := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "未来", Prompt: "p", Schedule: "at " + target.Format(time.RFC3339), Enabled: true,
	})
	if !created.NextRun.Equal(target) {
		t.Fatalf("NextRun = %s, want %s", created.NextRun, target)
	}
}

// TestRunHistoryLoadsLegacyFinishedAt covers old registry run records: the old
// "finishedAt" timestamp loads as the completion instant (unknown otherwise).
func TestRunHistoryLoadsLegacyFinishedAt(t *testing.T) {
	dir := t.TempDir()
	registry := `{"tasks":[{"id":"auto-legacy","name":"旧记录","prompt":"p","schedule":"every 60s",` +
		`"enabled":false,"runHistory":[{"startedAt":"2026-01-01T00:00:00Z",` +
		`"finishedAt":"2026-01-01T00:00:05Z","status":"success","summary":"ran in chat session sess-1"}]}]}`
	if err := os.WriteFile(filepath.Join(dir, DefaultFileName), []byte(registry), fileMode); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	service := newTestService(t, dir, Config{TickMs: 3600000}, newFakeChat())
	tasks, err := service.List(context.Background())
	if err != nil || len(tasks) != 1 || len(tasks[0].RunHistory) != 1 {
		t.Fatalf("List = %+v/%v", tasks, err)
	}
	record := tasks[0].RunHistory[0]
	if record.Status != contracts.AutomationRunSuccess {
		t.Fatalf("legacy status = %q", record.Status)
	}
	if record.CompletedAt.IsZero() {
		t.Fatalf("legacy finishedAt did not load: %+v", record)
	}
	if record.SessionID != "" {
		t.Fatalf("legacy record gained a session id: %+v", record)
	}
}
