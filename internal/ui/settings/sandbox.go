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

// sandboxModeBullets explains how the default permission mode maps onto the OS
// profile. The mapping is the mode's file-effect meaning.
var sandboxModeBullets = []struct{ zh, en string }{
	{
		zh: "只读 → 只读：只能读取，不能写入或删除。",
		en: "Read-only → Read-only: read only, no writes or deletes.",
	},
	{
		zh: "工作区可写 → 工作区：工作区内可写，工作区外只读。",
		en: "Workspace-write → Workspace: writable inside the workspace, read-only outside.",
	},
	{
		zh: "所有权限 → 不包装：不启用 OS 限制，退回完全按审批模式运行。",
		en: "All permissions → Not wrapped: no OS confinement, back to approval-only behavior.",
	},
}

// buildSandboxSection renders the always-on OS sandbox section: the live status
// line, the permission-mode mapping and the shell/fail-closed note. There is no
// switch; a stripped assembly without the module renders the unavailable state.
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
		ui.Column(c).FillWidth().Gap(design.Space1).PaddingY(design.Space1).Children(func() {
			for _, bullet := range sandboxModeBullets {
				uikit.Bullet(c, localized(locale, bullet.zh, bullet.en))
			}
		})
		design.Apply(ui.Text(c, localized(locale,
			"始终启用、不可关闭：由操作系统在内核层限制工具的文件写入与删除；它不是容器，读取与网络不受限制。可用时 shell 类工具（bash、终端、后台任务等）自动放行，不再弹出审批；本机不可用时命令会失败（fail closed）。修改权限模式在下次构建工具时生效，无需重启。",
			"Always on and not configurable: the OS enforces tool file writes and deletes (it is not a container, so reads and network stay unconfined). When available, shell-family tools (bash, terminals, background jobs) are auto-allowed without an approval prompt; when unavailable on this host, commands fail closed. Changing the permission mode applies on the next tool build; no restart is needed.")),
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
