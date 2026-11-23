package automation

// The optional degraded-state banner: the automation backend can be present but
// unable to run tasks (another process owns the registry lock). The screen still
// shows the task list and inline CRUD; a warning banner explains that scheduled
// runs and Run now are unavailable here.

import (
	"context"
	"errors"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// automationStatus reads the backend's optional runtime status. A backend
// without the optional provider is assumed healthy (older hosts).
func automationStatus(service contracts.AutomationService) (contracts.AutomationStatus, bool) {
	provider, ok := service.(contracts.AutomationStatusProvider)
	if !ok {
		return contracts.AutomationStatus{}, false
	}
	return provider.Status(context.Background()), true
}

// buildAutomationStatusBanner draws the degraded banner: one mandatory status
// badge (warning semantic, never color alone), the localized explanation, the
// held lock path as secondary metadata and — when the backend exposes the
// optional recovery half — a secondary Retry action. Only design tokens are
// used; Retry is never a filled/primary action (the screen's one primary stays
// the header's New task).
func buildAutomationStatusBanner(c *ui.Context, t *design.Tokens, st *state,
	service contracts.AutomationService, runtime uinative.UIRuntime, status contracts.AutomationStatus) {
	if status.Available {
		return
	}
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		uikit.StatusBadge(c, t, uikit.StatusNeedsAction, st.tr("已降级", "Degraded"))
		uikit.WarningLine(c, t, st.tr(
			"计划任务已暂停，立即运行也不可用；本窗口仍可查看和编辑任务。",
			"Scheduled runs are paused and Run now is unavailable here; you can still view and edit tasks."))
		recovery, canRetry := service.(contracts.AutomationRecovery)
		if !canRetry {
			if lockedBy := strings.TrimSpace(status.LockedBy); lockedBy != "" {
				design.Apply(ui.Text(c, st.tr("锁文件：", "Lock file: ")+lockedBy),
					t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).Selectable()
			}
			return
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			if lockedBy := strings.TrimSpace(status.LockedBy); lockedBy != "" {
				design.Apply(ui.Text(c, st.tr("锁文件：", "Lock file: ")+lockedBy),
					t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).Selectable().Grow(1)
			} else {
				ui.Spacer(c).Grow(1)
			}
			retry := design.Apply(ui.Button(c, st.tr("重试", "Retry")), t.SecondaryButton()).
				Tooltip(st.tr("再次尝试接管自动化注册表", "Try to take over the automation registry again"))
			if retry.Clicked() {
				if err := recovery.Retry(context.Background()); err != nil {
					st.pageErr = automationRetryErrorText(st, err)
				} else {
					st.pageErr = ""
				}
				runtime.Invalidate()
			}
		})
	})
}

// automationRetryErrorText localizes the common lock-contention retry failure;
// other failures keep the backend text behind the localized prefix.
func automationRetryErrorText(st *state, err error) string {
	if errors.Is(err, contracts.ErrAutomationLocked) {
		return st.tr("重试失败：另一个实例仍在运行，请稍后再试。",
			"Retry failed: another instance still owns the automation registry; try again shortly.")
	}
	return automationErrorText(st, st.tr("重试失败：", "Retry failed: "), err)
}
