package agent

// Tests for the product-side stream-completeness policy: the module config and
// the chat.requireFinishReason setting resolve into the SDK's
// RequireCompleteStream option (the detection itself lives in zeokeel-rig).

import (
	"testing"

	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func newFinishReasonTestService(t *testing.T, cfg Config, settings map[string]string) *service {
	t.Helper()
	return newService(cfg, rigtools.NewRegistry(), newFakeSettings(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
}

func TestRequireFinishReasonResolution(t *testing.T) {
	cases := []struct {
		setting string
		cfg     bool
		want    bool
	}{
		{"", true, true},
		{"", false, false},
		{"false", true, false},
		{"off", true, false},
		{"0", true, false},
		{"true", false, true},
		{"on", false, true},
		{"1", false, true},
	}
	for _, tc := range cases {
		settings := map[string]string{}
		if tc.setting != "" {
			settings[contracts.SettingsChatRequireFinishReason] = tc.setting
		}
		svc := newFinishReasonTestService(t, Config{RequireFinishReason: tc.cfg}, settings)
		if got := svc.requireFinishReason(); got != tc.want {
			t.Errorf("requireFinishReason(setting=%q, config=%v) = %v, want %v",
				tc.setting, tc.cfg, got, tc.want)
		}
	}
}
