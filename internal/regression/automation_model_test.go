package regression

// Adversarial audit: the automation per-task model override. A run creates the
// session, applies the model through contracts.SessionModelSwitcher before
// sending, and the choice persists in the registry. Negative cases: a backend
// without the optional switcher still runs (best effort), a failing switch still
// runs with the default model, and a task without an override never calls it.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/automation"
)

// automationModelChat records model switches and sends in call order.
type automationModelChat struct {
	*fakeChat
	mu         sync.Mutex
	events     []string
	modelCalls []string
	switchErr  error
}

func (c *automationModelChat) SetSessionModel(sessionID, model string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.modelCalls = append(c.modelCalls, sessionID+"="+model)
	c.events = append(c.events, "model:"+sessionID+"="+model)
	return c.switchErr
}

func (c *automationModelChat) Send(sessionID, prompt string) error {
	c.mu.Lock()
	c.events = append(c.events, "send:"+sessionID)
	c.mu.Unlock()
	return c.fakeChat.Send(sessionID, prompt)
}

func (c *automationModelChat) eventLog() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.events...)
}

// startAutomationModel boots the automation module over the recording chat.
func startAutomationModel(t *testing.T, chat contracts.ChatService) (*harness, contracts.AutomationService) {
	t.Helper()
	h := newBareHarness(t)
	provide(t, h.ctx, contracts.KeyChat, chat)
	activate(t, h.ctx, automation.Module(), automation.Config{TickMs: 3_600_000})
	return h, requireService(t, h.ctx, contracts.KeyAutomation)
}

// TestAutomationModelOverrideAppliedBeforeSend covers the success path: the model
// is switched before Send, the choice persists in the registry JSON, and the run
// succeeds.
func TestAutomationModelOverrideAppliedBeforeSend(t *testing.T) {
	chat := &automationModelChat{fakeChat: newFakeChat()}
	h, service := startAutomationModel(t, chat)
	task, err := service.Create(context.Background(), contracts.AutomationTask{
		Name: "model-override", Prompt: "run with a model", Schedule: "every 60s",
		Enabled: true, Model: "  gpt-audit  ",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.Model != "gpt-audit" {
		t.Fatalf("created Model = %q, want the trimmed gpt-audit", task.Model)
	}
	if err := service.RunNow(context.Background(), task.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	updated := automationHistory(t, service, task.ID, 1)
	if updated.RunHistory[0].Status != contracts.AutomationRunSuccess {
		t.Fatalf("run status = %q, want success", updated.RunHistory[0].Status)
	}
	events := chat.eventLog()
	if len(events) != 2 || !strings.HasPrefix(events[0], "model:") || !strings.HasPrefix(events[1], "send:") {
		t.Fatalf("event order = %v, want model before send", events)
	}
	if got := chat.modelCalls; len(got) != 1 || !strings.HasSuffix(got[0], "=gpt-audit") {
		t.Fatalf("model calls = %v, want the task override", got)
	}

	// The override is persisted in the registry document.
	body, err := os.ReadFile(filepath.Join(h.dataDir, automation.DefaultFileName))
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var document struct {
		Tasks []struct {
			ID    string `json:"id"`
			Model string `json:"model"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("decode registry: %v", err)
	}
	if len(document.Tasks) != 1 || document.Tasks[0].Model != "gpt-audit" {
		t.Fatalf("registry model = %+v", document.Tasks)
	}

	// Best effort: a backend without the optional switcher or a failing switch
	// still records a successful run (default model); no override means no call.
	t.Run("best-effort", func(t *testing.T) {
		// Backend without the switcher: run proceeds, warning is logged.
		plain := newFakeChat()
		_, plainService := startAutomationModel(t, plain)
		task, err := plainService.Create(context.Background(), contracts.AutomationTask{
			Name: "no-switcher", Prompt: "run", Schedule: "every 60s", Enabled: true, Model: "m1",
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := plainService.RunNow(context.Background(), task.ID); err != nil {
			t.Fatalf("RunNow: %v", err)
		}
		updated := automationHistory(t, plainService, task.ID, 1)
		if updated.RunHistory[0].Status != contracts.AutomationRunSuccess {
			t.Fatalf("no-switcher run status = %q, want success", updated.RunHistory[0].Status)
		}
		if got := len(plain.recordedSends()); got != 1 {
			t.Fatalf("no-switcher sends = %d, want 1", got)
		}

		// Failing switcher: still runs, only warns.
		failing := &automationModelChat{fakeChat: newFakeChat(), switchErr: errors.New("model denied")}
		_, failingService := startAutomationModel(t, failing)
		task, err = failingService.Create(context.Background(), contracts.AutomationTask{
			Name: "switch-error", Prompt: "run", Schedule: "every 60s", Enabled: true, Model: "m2",
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := failingService.RunNow(context.Background(), task.ID); err != nil {
			t.Fatalf("RunNow: %v", err)
		}
		updated = automationHistory(t, failingService, task.ID, 1)
		if updated.RunHistory[0].Status != contracts.AutomationRunSuccess {
			t.Fatalf("switch-error run status = %q, want success", updated.RunHistory[0].Status)
		}
		if got := failing.eventLog(); len(got) < 2 || !strings.HasPrefix(got[0], "model:") || !strings.HasPrefix(got[1], "send:") {
			t.Fatalf("switch-error events = %v, want model then send", got)
		}

		// A task without an override never touches the switcher.
		noOverride := &automationModelChat{fakeChat: newFakeChat()}
		_, noOverrideService := startAutomationModel(t, noOverride)
		task, err = noOverrideService.Create(context.Background(), contracts.AutomationTask{
			Name: "no-model", Prompt: "run", Schedule: "every 60s", Enabled: true,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := noOverrideService.RunNow(context.Background(), task.ID); err != nil {
			t.Fatalf("RunNow: %v", err)
		}
		automationHistory(t, noOverrideService, task.ID, 1)
		if got := noOverride.modelCalls; len(got) != 0 {
			t.Fatalf("no-override task switched the model: %v", got)
		}
		if got := noOverride.recordedSends(); !reflect.DeepEqual(got, []string{"sess-1:run"}) {
			t.Fatalf("no-override sends = %v", got)
		}
	})
}
