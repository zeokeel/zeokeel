package automation_test

// Frame tests for the optional automation status banner: a degraded backend
// (another process owns the registry lock) shows a warning badge + localized
// explanation + lock path, while a healthy backend or a backend without the
// optional status provider shows no banner.

import (
	"context"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/automation"
)

// statusAutomation is the fake automation service plus the optional status
// provider half.
type statusAutomation struct {
	*fakeAutomation
	status contracts.AutomationStatus
}

func (s *statusAutomation) Status(context.Context) contracts.AutomationStatus {
	return s.status
}

func statusTask() contracts.AutomationTask {
	return contracts.AutomationTask{
		ID:       "task-1",
		Name:     "Nightly check",
		Prompt:   "check deps",
		Schedule: "every 60s",
		Enabled:  true,
	}
}

func TestDegradedStatusShowsLocalizedBanner(t *testing.T) {
	const lockPath = "/tmp/zeokeel-data/.automations.lock"
	service := &statusAutomation{
		fakeAutomation: newFakeAutomation(statusTask()),
		status: contracts.AutomationStatus{
			Available: false,
			Reason:    "another instance owns the automation registry",
			LockedBy:  lockPath,
		},
	}
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()

	h.assertTexts(tester, "Nightly check", "已降级",
		"计划任务已暂停，立即运行也不可用；本窗口仍可查看和编辑任务。",
		"锁文件："+lockPath)
	h.assertNoTexts(tester, "Degraded", "Lock file:")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Nightly check", "Degraded",
		"Scheduled runs are paused and Run now is unavailable here; you can still view and edit tasks.",
		"Lock file: "+lockPath)
	h.assertNoTexts(tester, "已降级", "锁文件：")

	h.uiRoot.Locale().SetLanguage("zh")
	tester.Frame()
	h.assertTexts(tester, "已降级", "计划任务已暂停")
}

func TestHealthyStatusShowsNoBanner(t *testing.T) {
	service := &statusAutomation{
		fakeAutomation: newFakeAutomation(statusTask()),
		status:         contracts.AutomationStatus{Available: true},
	}
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()

	h.assertTexts(tester, "Nightly check", "自动化")
	h.assertNoTexts(tester, "已降级", "Degraded", "锁文件：", "Lock file:")
}

func TestBackendWithoutStatusProviderShowsNoBanner(t *testing.T) {
	h := newHarness(t, newFakeAutomation(statusTask()), automation.Config{})
	tester := h.tester()

	h.assertTexts(tester, "Nightly check")
	h.assertNoTexts(tester, "已降级", "Degraded", "锁文件：", "Lock file:")
}

func TestDegradedStatusWithoutLockPathOmitsIt(t *testing.T) {
	service := &statusAutomation{
		fakeAutomation: newFakeAutomation(statusTask()),
		status:         contracts.AutomationStatus{Available: false, Reason: "scheduler unavailable"},
	}
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()

	h.assertTexts(tester, "已降级", "计划任务已暂停")
	h.assertNoTexts(tester, "锁文件：")
}
