package settings_test

// Frame tests for RG-02 (2026-11 wave D): the Behavior page's OS sandbox status
// face is mode-aware. Under the global "所有权限" (full) preset the OS sandbox
// passes commands through unconfined, so the section must show a warning state
// ("完全访问：系统沙箱不包装命令") — not the green "已启用" success badge that
// would imply protection that is not there — and the "始终启用、不可关闭" promise
// is conditioned on the mode. Under workspace/read-only the available sandbox
// still reads as enabled.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// TestBehaviorPageSandboxFullAccessWarns covers the full preset: an available
// runner does NOT mean commands are wrapped, so the status is a warning, not a
// success, and the copy says the sandbox does not wrap commands.
func TestBehaviorPageSandboxFullAccessWarns(t *testing.T) {
	service := &fakeSandboxService{available: true, platform: "linux"}
	store := newFakeSettings(map[string]string{contracts.SettingAgentPermission: contracts.PermissionFull})
	h := newHarnessFull(t, &fakeChat{}, store, settings.Config{}, nil, contracts.AppInfo{}, nil, service)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为与限额")

	h.assertTexts(tester, "系统沙箱", "完全访问：系统沙箱不包装命令")
	// The green success badge would lie about protection under full access.
	h.assertNoTexts(tester, "已启用（平台：linux）")
	// The unconditional always-on promise is replaced by the full-access disclosure.
	h.assertNoTexts(tester, "始终启用、不可关闭")
}

// TestBehaviorPageSandboxWorkspaceEnabled is the contrast: under the default
// workspace preset an available runner reads as enabled with the always-on note.
func TestBehaviorPageSandboxWorkspaceEnabled(t *testing.T) {
	service := &fakeSandboxService{available: true, platform: "linux"}
	store := newFakeSettings(map[string]string{contracts.SettingAgentPermission: contracts.PermissionWorkspace})
	h := newHarnessFull(t, &fakeChat{}, store, settings.Config{}, nil, contracts.AppInfo{}, nil, service)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为与限额")

	h.assertTexts(tester, "已启用（平台：linux）", "不可关闭")
	h.assertNoTexts(tester, "完全访问：系统沙箱不包装命令")
}
