package regression

// Adversarial audit: the automation service's shutdown contract. The scheduler
// tracks in-flight runs (sync.WaitGroup) so Close waits for them before the
// registry is considered final: a RunNow immediately followed by a close must
// leave a valid, fully written automations.json and must not write again after
// Close returns (temp-file cleanup and -race stay clean).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/automation"
)

// automationCloseHarness boots the real automation module on a fresh data dir.
func automationCloseHarness(t *testing.T) (*harness, contracts.AutomationService) {
	t.Helper()
	h := newBareHarness(t)
	chat := newFakeChat()
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	activate(t, h.ctx, automation.Module(), automation.Config{TickMs: 3_600_000})
	return h, requireService(t, h.ctx, contracts.KeyAutomation)
}

// automationRegistryTasks decodes the on-disk registry.
func automationRegistryTasks(t *testing.T, dataDir string) []struct {
	ID         string                          `json:"id"`
	RunHistory []contracts.AutomationRunRecord `json:"runHistory"`
} {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dataDir, automation.DefaultFileName))
	if err != nil {
		t.Fatalf("read automation registry: %v", err)
	}
	var document struct {
		Tasks []struct {
			ID         string                          `json:"id"`
			RunHistory []contracts.AutomationRunRecord `json:"runHistory"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("registry is not valid JSON after close: %v", err)
	}
	return document.Tasks
}

// automationTempFiles lists leftover atomic-write temp files.
func automationTempFiles(t *testing.T, dataDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatalf("read data dir: %v", err)
	}
	leftovers := make([]string, 0)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".automations-") {
			leftovers = append(leftovers, entry.Name())
		}
	}
	return leftovers
}

// TestAutomationCloseWaitsForInFlightRunNow covers RunNow immediately followed by
// module disposal: the run is flushed before Close returns and nothing writes
// afterwards.
func TestAutomationCloseWaitsForInFlightRunNow(t *testing.T) {
	for iteration := 0; iteration < 5; iteration++ {
		h, service := automationCloseHarness(t)
		task, err := service.Create(context.Background(), contracts.AutomationTask{
			Name: "close-race", Prompt: "run", Schedule: "every 60s", Enabled: true,
		})
		if err != nil {
			t.Fatalf("#%d Create: %v", iteration, err)
		}
		if err := service.RunNow(context.Background(), task.ID); err != nil {
			t.Fatalf("#%d RunNow: %v", iteration, err)
		}
		// Close (via module disposal) while the run may still be executing.
		if err := h.ctx.DisposeAll(); err != nil {
			t.Fatalf("#%d DisposeAll: %v", iteration, err)
		}

		tasks := automationRegistryTasks(t, h.dataDir)
		if len(tasks) != 1 {
			t.Fatalf("#%d registry tasks = %d, want 1", iteration, len(tasks))
		}
		if len(tasks[0].RunHistory) != 1 {
			t.Fatalf("#%d run history = %d after Close, want the flushed run (1)",
				iteration, len(tasks[0].RunHistory))
		}
		if status := tasks[0].RunHistory[0].Status; status != contracts.AutomationRunSuccess {
			t.Fatalf("#%d run status = %q, want success", iteration, status)
		}
		if leftovers := automationTempFiles(t, h.dataDir); len(leftovers) != 0 {
			t.Fatalf("#%d leftover temp files after Close: %v", iteration, leftovers)
		}

		// The registry is final: no write may land after Close.
		path := filepath.Join(h.dataDir, automation.DefaultFileName)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("#%d read after Close: %v", iteration, err)
		}
		time.Sleep(20 * time.Millisecond)
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("#%d reread after Close: %v", iteration, err)
		}
		if string(before) != string(after) {
			t.Fatalf("#%d registry changed after Close returned", iteration)
		}
	}
}

// TestAutomationCloseWaitsForConcurrentRuns covers several tasks with in-flight
// RunNow calls disposed together: every run is flushed exactly once, the
// registry remains valid and the concurrent teardown is race-detector clean.
func TestAutomationCloseWaitsForConcurrentRuns(t *testing.T) {
	h, service := automationCloseHarness(t)
	tasks := make([]contracts.AutomationTask, 0, 3)
	for index := 0; index < 3; index++ {
		task, err := service.Create(context.Background(), contracts.AutomationTask{
			Name: "close-many-" + string(rune('a'+index)), Prompt: "run",
			Schedule: "every 60s", Enabled: true,
		})
		if err != nil {
			t.Fatalf("Create #%d: %v", index, err)
		}
		tasks = append(tasks, task)
	}
	var wg sync.WaitGroup
	for _, task := range tasks {
		task := task
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := service.RunNow(context.Background(), task.ID); err != nil {
				t.Errorf("RunNow(%s): %v", task.ID, err)
			}
		}()
	}
	wg.Wait()
	if err := h.ctx.DisposeAll(); err != nil {
		t.Fatalf("DisposeAll: %v", err)
	}
	registryTasks := automationRegistryTasks(t, h.dataDir)
	if len(registryTasks) != len(tasks) {
		t.Fatalf("registry tasks = %d, want %d", len(registryTasks), len(tasks))
	}
	for _, task := range registryTasks {
		if len(task.RunHistory) != 1 {
			t.Fatalf("task %s history = %d after Close, want 1", task.ID, len(task.RunHistory))
		}
		if status := task.RunHistory[0].Status; status != contracts.AutomationRunSuccess {
			t.Fatalf("task %s status = %q, want success", task.ID, status)
		}
	}
	if leftovers := automationTempFiles(t, h.dataDir); len(leftovers) != 0 {
		t.Fatalf("leftover temp files after concurrent Close: %v", leftovers)
	}
}
