package settings

// OS sandbox section of the Behavior page. The OS sandbox is a basic agent
// capability, so it is always on and has no switch: commands must not run
// unconfined. The section explains what the OS profile enforces (file
// writes/deletes — it is not a container, reads and network stay unconfined),
// how the default permission mode maps onto the OS profile, the shell
// auto-allow, and the fail-closed behavior when this host has no usable runner.

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// sandboxStatusText resolves the one-line status copy of the OS sandbox and
// whether the state is a failure. There is no "off" state: the sandbox is
// always enabled, and the only failure is a host with no usable runner.
func sandboxStatusText(locale contracts.LocaleService, status contracts.SandboxStatus) (text string, failed bool) {
	if status.Available {
		platform := strings.TrimSpace(status.Platform)
		if platform == "" {
			platform = runtime.GOOS
		}
		return fmt.Sprintf(localized(locale, "已启用（平台：%s）", "On (platform: %s)"), platform), false
	}
	reason := strings.TrimSpace(status.Reason)
	if reason == "" {
		reason = localized(locale, "未找到可用的平台沙箱", "no usable platform sandbox found")
	}
	return localized(locale, "本机不可用：", "Unavailable on this host: ") + reason, true
}

// sandboxStatusKind folds the OS sandbox state into the product's one status
// vocabulary: a usable runner is a success and an unavailable host is a failure
// (DESIGN.md §5.2).
func sandboxStatusKind(status contracts.SandboxStatus, failed bool) uikit.StatusKind {
	if failed {
		return uikit.StatusError
	}
	return uikit.StatusSuccess
}

// buildSandboxSection renders the always-on OS sandbox section: the live status
// line and the always-on/restart note. There is no switch; a stripped assembly
// without the module renders the unavailable state.
func buildSandboxSection(c *ui.Context, locale contracts.LocaleService, service contracts.SandboxService) {
	t := design.Of(c)
	uikit.Section(c, localized(locale, "OS 沙箱", "OS sandbox"), func(*ui.Context) {
		status := contracts.SandboxStatus{Platform: runtime.GOOS}
		if service != nil {
			status = service.Status()
		} else {
			status.Reason = localized(locale, "未加载 sandbox 模块", "sandbox module is not mounted")
		}
		buildSandboxStatusRow(c, t, locale, status)
		// The one sentence keeps the two consequences a user cannot infer from
		// the status row: shell tools need no approval while the sandbox is
		// available, and a host without it fails commands instead of running
		// them unconfined. The mode mapping itself stays in the select's labels.
		design.Apply(ui.Text(c, localized(locale,
			"始终启用、不可关闭；可用时 shell 类工具自动放行，不可用时命令失败；修改权限模式下次构建工具时生效。",
			"Always on and not configurable; shell tools are auto-allowed when available and fail closed when not; a permission-mode change applies on the next tool build.")),
			t.TextMuted(design.TypeXXS))
	})
}

// buildSandboxStatusRow renders the one-line status of the OS sandbox.
func buildSandboxStatusRow(c *ui.Context, t *design.Tokens, locale contracts.LocaleService,
	status contracts.SandboxStatus) {
	text, failed := sandboxStatusText(locale, status)
	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, localized(locale, "状态：", "Status: ")), t.Font(design.TypeXS)).
			TextColor(t.LabelSecondary).Shrink(0)
		uikit.StatusText(c, t, design.TypeXS, sandboxStatusKind(status, failed), text).
			Grow(1).Selectable()
	})
}
