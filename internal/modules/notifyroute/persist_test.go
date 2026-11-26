package notifyroute

// Route persistence (M4 follow-up): a binding the user made must survive a
// restart. The failure this pins is quiet and expensive: the router silently
// falling back to "in-app only" makes WeChat delivery look broken.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type memSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func newMemSettings() *memSettings { return &memSettings{values: map[string]string{}} }

func (m *memSettings) Get(key, fallback string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if value, ok := m.values[key]; ok {
		return value
	}
	return fallback
}

func (m *memSettings) Set(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = value
	return nil
}

func (m *memSettings) All() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]string, len(m.values))
	for key, value := range m.values {
		out[key] = value
	}
	return out
}

func TestUpsertSurvivesARestart(t *testing.T) {
	settings := newMemSettings()
	first := NewService(nil).WithSettings(settings, nil)
	bound := contracts.NotifyRoute{
		Category: contracts.NotifyCategoryReport,
		Primary:  contracts.NotifyChannelWeChat,
		Enabled:  true,
	}
	if err := first.Upsert(context.Background(), bound); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// A new process: same settings row, fresh service.
	second := NewService(nil).WithSettings(settings, nil)
	routes, err := second.Routes(context.Background())
	if err != nil {
		t.Fatalf("Routes: %v", err)
	}
	for _, route := range routes {
		if route.Category != contracts.NotifyCategoryReport {
			continue
		}
		if route.Primary != contracts.NotifyChannelWeChat {
			t.Fatalf("route after restart = %+v, want the wechat binding kept", route)
		}
		return
	}
	t.Fatalf("report route missing after restart: %+v", routes)
}

func TestUnreadableRowKeepsTheDefaults(t *testing.T) {
	settings := newMemSettings()
	settings.values[contracts.SettingNotifyRoutes] = "{broken"
	service := NewService(nil).WithSettings(settings, nil)
	plan, err := service.Plan(context.Background(), contracts.NotifyCategoryReport, time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC), map[string]bool{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Channels) != 1 || plan.Channels[0] != contracts.NotifyChannelInApp {
		t.Fatalf("plan = %+v, want the default in-app route", plan)
	}
}

// Without settings (a stripped assembly) routing still works and Upsert does not
// fail: persistence is a convenience, not a requirement.
func TestUpsertWithoutSettingsStaysInMemory(t *testing.T) {
	service := NewService(nil)
	if err := service.Upsert(context.Background(), contracts.NotifyRoute{
		Category: contracts.NotifyCategoryTaskDone, Primary: contracts.NotifyChannelFeishu, Enabled: true,
	}); err != nil {
		t.Fatalf("Upsert without settings: %v", err)
	}
}
