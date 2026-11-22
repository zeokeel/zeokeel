package automation_test

// Frame tests for the optional automation status banner: a degraded backend
// (another process owns the registry lock) shows a warning badge + localized
// explanation + lock path, while a healthy backend or a backend without the
// optional status provider shows no banner.

import (
	"context"
	"fmt"
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

// recoveringAutomation adds the optional recovery half: Retry is scripted by
// the test (default: the lock became free and the backend is available).
type recoveringAutomation struct {
	*statusAutomation
	retry func(context.Context) error
}

func (r *recoveringAutomation) Retry(ctx context.Context) error {
	if r.retry != nil {
		return r.retry(ctx)
	}
	r.status.Available = true
	return nil
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
	h.assertNoTexts(tester, "Degraded", "Lock file:", "重试", "Retry")

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

func TestDegradedStatusRetryButtonSucceeds(t *testing.T) {
	service := &recoveringAutomation{statusAutomation: &statusAutomation{
		fakeAutomation: newFakeAutomation(statusTask()),
		status: contracts.AutomationStatus{
			Available: false,
			Reason:    "another instance owns the automation registry",
			LockedBy:  "/tmp/zeokeel-data/.automations.lock",
		},
	}}
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()

	h.assertTexts(tester, "已降级", "重试", "Nightly check")
	h.click(tester, "重试")
	h.assertTexts(tester, "Nightly check")
	h.assertNoTexts(tester, "已降级", "重试", "锁文件：")
	if service.status.Available != true {
		t.Fatal("Retry did not take over the backend")
	}
}

func TestDegradedStatusRetryFailureKeepsBanner(t *testing.T) {
	service := &recoveringAutomation{
		statusAutomation: &statusAutomation{
			fakeAutomation: newFakeAutomation(statusTask()),
			status: contracts.AutomationStatus{
				Available: false,
				Reason:    "another instance owns the automation registry",
				LockedBy:  "/tmp/zeokeel-data/.automations.lock",
			},
		},
		retry: func(context.Context) error {
			return fmt.Errorf("%w: another instance owns the registry lock", contracts.ErrAutomationLocked)
		},
	}
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()

	h.click(tester, "重试")
	h.assertTexts(tester, "已降级", "重试",
		"重试失败：另一个实例仍在运行，请稍后再试。",
		"锁文件：/tmp/zeokeel-data/.automations.lock")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Degraded", "Retry")
	h.click(tester, "Retry")
	h.assertTexts(tester, "Degraded", "Retry",
		"Retry failed: another instance still owns the automation registry; try again shortly.")
	h.assertNoTexts(tester, "重试失败：")
}

func TestDegradedStatusWithoutRecoveryShowsNoRetryButton(t *testing.T) {
	service := &statusAutomation{
		fakeAutomation: newFakeAutomation(statusTask()),
		status:         contracts.AutomationStatus{Available: false, Reason: "scheduler unavailable"},
	}
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()

	h.assertTexts(tester, "已降级")
	h.assertNoTexts(tester, "重试", "Retry")
}
