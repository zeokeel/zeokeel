package session

// Unit tests for the composer permission button label: the button shows the
// current permission preset itself and only the all-permissions preset warns.
// Chat modes no longer have a composer surface.

import (
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// menuSettings is a minimal contracts.SettingsService for the label helper.
type menuSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func (s *menuSettings) Get(key, def string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return def
}

func (s *menuSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *menuSettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out
}

// TestPermissionMenuLabelFollowsPreset covers the button label: the current
// permission preset names the button, and only 所有权限 is warning-level.
func TestPermissionMenuLabelFollowsPreset(t *testing.T) {
	store := &menuSettings{values: map[string]string{}}
	st := &state{settings: store}
	for _, want := range []struct {
		mode  string
		label string
		warn  bool
	}{
		{contracts.PermissionWorkspace, "工作区修改", false},
		{contracts.PermissionReadOnly, "只读", false},
		{contracts.PermissionFull, "所有权限", true},
	} {
		store.values[contracts.SettingAgentPermission] = want.mode
		label, warn := st.permissionMenuLabel()
		if label != want.label {
			t.Fatalf("permission %q: label = %q, want %q", want.mode, label, want.label)
		}
		if warn != want.warn {
			t.Fatalf("permission %q: warn = %v, want %v", want.mode, warn, want.warn)
		}
	}

	// The removed chat-mode selector must not change the permission button.
	store.values[contracts.SettingAgentPermission] = contracts.PermissionWorkspace
	store.values[contracts.SettingAgentMode] = contracts.AgentModePlan
	store.values[contracts.SettingChatMode] = contracts.ChatModeCreator
	if label, warn := st.permissionMenuLabel(); label != "工作区修改" || warn {
		t.Fatalf("chat mode still drives the permission button: label = %q warn = %v", label, warn)
	}

	// Without a settings store the label falls back to the default preset and
	// stays quiet (the button itself is not built).
	st = &state{}
	if label, warn := st.permissionMenuLabel(); label != "工作区修改" || warn {
		t.Fatalf("missing settings: label = %q warn = %v", label, warn)
	}
}
