package automation

// The optional degraded-state banner: the automation backend can be present but
// unable to run tasks (another process owns the registry lock). The screen still
// shows the task list and inline CRUD; a warning banner explains that scheduled
// runs and Run now are unavailable here.

import (
	"context"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
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
// badge (warning semantic, never color alone), the localized explanation and the
// held lock path as secondary metadata. Only design tokens are used.
func buildAutomationStatusBanner(c *ui.Context, t *design.Tokens, st *state, status contracts.AutomationStatus) {
	if status.Available {
		return
	}
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		uikit.StatusBadge(c, t, uikit.StatusNeedsAction, st.tr("已降级", "Degraded"))
		uikit.WarningLine(c, t, st.tr(
			"计划任务已暂停，立即运行也不可用；本窗口仍可查看和编辑任务。",
			"Scheduled runs are paused and Run now is unavailable here; you can still view and edit tasks."))
		if lockedBy := strings.TrimSpace(status.LockedBy); lockedBy != "" {
			design.Apply(ui.Text(c, st.tr("锁文件：", "Lock file: ")+lockedBy),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).Selectable()
		}
	})
}
