package agent

// Tests for the product-side stream-completeness policy: a stream that closes
// without a finish_reason is always treated as a failed turn. The behavior is
// fixed (it is no longer a Settings control), so neither the
// chat.requireFinishReason row nor the module config can turn it off; the
// detection itself lives in zeokeel-rig behind RequireCompleteStream.

import (
	"testing"

	rigtools "github.com/zeokeel/zeokeel-rig/tools"
)

func newFinishReasonTestService(t *testing.T, cfg Config, settings map[string]string) *service {
	t.Helper()
	return newService(cfg, rigtools.NewRegistry(), newFakeSettings(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
}

// TestRequireFinishReasonAlwaysOn pins the A2 fixed behavior: streams always
// require a clean finish_reason, whatever the stored row or module config says.
func TestRequireFinishReasonAlwaysOn(t *testing.T) {
	cases := []struct {
		name    string
		setting string
		cfg     bool
	}{
		{name: "unset, config off", setting: "", cfg: false},
		{name: "unset, config on", setting: "", cfg: true},
		{name: "setting false, config off", setting: "false", cfg: false},
		{name: "setting false, config on", setting: "false", cfg: true},
		{name: "setting off", setting: "off", cfg: true},
		{name: "setting zero", setting: "0", cfg: true},
		{name: "setting no", setting: "no", cfg: true},
		{name: "setting true, config off", setting: "true", cfg: false},
		{name: "setting on", setting: "on", cfg: false},
		{name: "setting one", setting: "1", cfg: false},
		{name: "setting yes", setting: "yes", cfg: false},
		{name: "setting garbage, config off", setting: "bogus", cfg: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := map[string]string{}
			if tc.setting != "" {
				settings["chat.requireFinishReason"] = tc.setting
			}
			svc := newFinishReasonTestService(t, Config{RequireFinishReason: tc.cfg}, settings)
			if got := svc.requireFinishReason(); !got {
				t.Errorf("requireFinishReason(setting=%q, config=%v) = false, want true (fixed behavior)",
					tc.setting, tc.cfg)
			}
		})
	}
}
