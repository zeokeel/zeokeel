package settings_test

// TDD tests for the Behavior page's OS sandbox section: the sandbox is always on
// (no switch), the status line derives from contracts.SandboxService.Status(),
// and the copy explains file-effect enforcement, the permission-mode mapping,
// shell auto-allow and fail-closed behavior.

import (
	"testing"

	rigsandbox "github.com/zeokeel/zeokeel-rig/sandbox"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// fakeSandboxService is a contracts.SandboxService with a fixed status.
type fakeSandboxService struct {
	available bool
	reason    string
	platform  string
}

var _ contracts.SandboxService = (*fakeSandboxService)(nil)

func (s *fakeSandboxService) Runner() rigsandbox.Runner { return nil }

func (s *fakeSandboxService) Available() bool { return s.available }

func (s *fakeSandboxService) Status() contracts.SandboxStatus {
	if s == nil {
		return contracts.SandboxStatus{}
	}
	return contracts.SandboxStatus{Available: s.available, Platform: s.platform, Reason: s.reason}
}

// newSandboxHarness opens the settings surface with the sandbox service
// provided before the settings module activates.
func newSandboxHarness(t *testing.T, service contracts.SandboxService) *harness {
	t.Helper()
	return newHarnessFull(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, nil, nil,
		contracts.AppInfo{}, nil, service)
}

// TestBehaviorPageSandboxAlwaysOn covers the always-on section: a switch-free
// status row, the mode mapping, and the fail-closed copy.
func TestBehaviorPageSandboxAlwaysOn(t *testing.T) {
	service := &fakeSandboxService{available: true, platform: "linux"}
	h := newSandboxHarness(t, service)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	h.assertTexts(tester, "OS 沙箱", "状态：", "已启用（平台：linux）",
		"不可关闭")
	// There is no enable/disable control anymore.
	h.assertNoTexts(tester, "启用 OS 沙箱")
	if raw := h.settings.Get("sandbox.os", ""); raw != "" {
		t.Fatalf("rendering the always-on section wrote a sandbox setting: %q", raw)
	}
}

// TestBehaviorPageSandboxUnavailable covers a host without a usable runner: the
// status line carries the reason and the page explains fail-closed behavior.
func TestBehaviorPageSandboxUnavailable(t *testing.T) {
	service := &fakeSandboxService{available: false, reason: "bwrap not found", platform: "linux"}
	h := newSandboxHarness(t, service)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	h.assertTexts(tester, "本机不可用：bwrap not found")
	h.assertNoTexts(tester, "启用 OS 沙箱")
}

// TestBehaviorPageSandboxWithoutService covers a stripped assembly without the
// sandbox module: the section still renders and reports the missing module.
func TestBehaviorPageSandboxWithoutService(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	h.assertTexts(tester, "OS 沙箱", "未加载 sandbox 模块")
}

// TestBehaviorPageSandboxEnglish covers the English copy and status line.
func TestBehaviorPageSandboxEnglish(t *testing.T) {
	service := &fakeSandboxService{available: true, platform: "darwin"}
	h := newLocaleHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, "en", service)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Behavior")
	h.assertTexts(tester, "OS sandbox",
		"On (platform: darwin)",
		"not configurable")
	h.assertNoTexts(tester, "Enable OS sandbox")
}
