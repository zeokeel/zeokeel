package gateway

// Migrated from internal/regression/automation_history_test.go +
// automation_model_test.go (both backend-only, 330 + 176 lines) before S4
// deletes internal/regression.
//
// Behaviour kept: one complete automation flow — create a task, run it now, the
// run creates its session, the prompt reaches the model, and the run is recorded
// in the task's history — with the gateway's session domain showing the new
// conversation.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// automationService returns the module's service from the rig.
func automationService(t *testing.T, rig *e2eRig) contracts.AutomationService {
	t.Helper()
	return requireValue(t, rig.root, contracts.KeyAutomation)
}

// TestE2EAutomationFullFlow is the migrated automation regression: the whole
// create → run → session → history chain, driven through the real module.
func TestE2EAutomationFullFlow(t *testing.T) {
	rig := newE2ERig(t, func(body []byte, _ int) (int, string) {
		// The automation run sends the task's prompt; answer deterministically.
		return 0, sseAnswer("automation answered: " + requestContent(body, "user"))
	}, e2eOptions{activateAutomation: true})
	rig.connect()
	service := automationService(t, rig)

	task, err := service.Create(context.Background(), contracts.AutomationTask{
		Name:     "migrated-flow",
		Prompt:   "collect the daily report",
		Schedule: "every 60s",
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.ID == "" {
		t.Fatal("Create returned no id")
	}
	if task.NextRun.IsZero() {
		t.Fatal("an enabled task has no NextRun")
	}

	before := len(rig.chat.Sessions())
	if err := service.RunNow(context.Background(), task.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}

	// The run creates a session and drives one turn through the backend agent.
	waitFor(t, "the automation run to create a session", func() bool {
		return len(rig.chat.Sessions()) > before
	})
	// The prompt reached the model (the fake model echoes it back).
	waitFor(t, "the model to receive the task prompt", func() bool {
		return strings.Contains(string(rig.model.lastBody()), "collect the daily report")
	})
	// And the run is recorded on the task.
	waitFor(t, "the task history to record the run", func() bool {
		tasks, listErr := service.List(context.Background())
		if listErr != nil {
			return false
		}
		for _, listed := range tasks {
			if listed.ID != task.ID {
				continue
			}
			if !listed.LastRun.IsZero() || len(listed.RunHistory) > 0 {
				return true
			}
		}
		return false
	})

	tasks, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var listed contracts.AutomationTask
	for _, candidate := range tasks {
		if candidate.ID == task.ID {
			listed = candidate
		}
	}
	if listed.Name != "migrated-flow" {
		t.Fatalf("listed task = %+v", listed)
	}
	if listed.LastError != "" {
		t.Fatalf("the run reported an error: %q", listed.LastError)
	}
	if len(listed.RunHistory) == 0 {
		t.Fatalf("no run history after RunNow: %+v", listed)
	}
	if !listed.RunHistory[0].StartedAt.Before(time.Now().Add(time.Minute)) {
		t.Fatalf("run record has a future start time: %+v", listed.RunHistory[0])
	}

	// The gateway's session domain agrees: the automation's session is listed.
	conn := rig.connect()
	listing := resultOf(t, conn.call("session.list.snapshot", nil))
	sessions, _ := listing["sessions"].([]any)
	if len(sessions) < before+1 {
		t.Fatalf("session.list.snapshot shows %d sessions, want at least %d", len(sessions), before+1)
	}
	found := false
	for _, raw := range sessions {
		item, _ := raw.(map[string]any)
		if strings.Contains(strings.ToLower(valueString(item["title"])), "migrated-flow") ||
			strings.Contains(strings.ToLower(valueString(item["title"])), "daily report") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the automation session is missing from the wire list: %v", sessions)
	}
}

// valueString renders one wire value for a title comparison.
func valueString(value any) string {
	text, _ := value.(string)
	return text
}

// TestE2EAutomationRunAppliesModelOverride keeps the deleted model test's
// invariant: a task's model override is applied before the turn is sent.
func TestE2EAutomationRunAppliesModelOverride(t *testing.T) {
	rig := newE2ERig(t, func(body []byte, _ int) (int, string) {
		return 0, sseAnswer("ok")
	}, e2eOptions{activateAutomation: true})
	rig.connect()
	service := automationService(t, rig)

	task, err := service.Create(context.Background(), contracts.AutomationTask{
		Name:     "model-override",
		Prompt:   "run with a model",
		Schedule: "every 60s",
		Enabled:  true,
		Model:    "  override-model  ",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.Model != "override-model" {
		t.Fatalf("created Model = %q, want the trimmed override-model", task.Model)
	}
	if err := service.RunNow(context.Background(), task.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	waitFor(t, "the overridden model to be requested", func() bool {
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(rig.model.lastBody(), &payload); err != nil {
			return false
		}
		return payload.Model == "override-model"
	})
	// Let the run finish before teardown: a run still writing its session while
	// the temp dir is removed makes the cleanup flaky.
	waitFor(t, "the automation run to settle", func() bool {
		for _, session := range rig.chat.Sessions() {
			if session.Running {
				return false
			}
		}
		return true
	})
}
