package settings

// System sandbox section of the Behavior page. The system sandbox is a basic
// agent capability with no switch of its own, but whether it actually wraps
// commands depends on the global permission mode: under "所有权限" (full) the
// runner passes commands through unconfined, so the status face must say so as
// a warning instead of claiming protection (2026-11 audit RG-02). Under
// workspace/read-only an available runner enforces file effects (and lets the
// agent gate auto-allow shell tools); a host without a usable runner runs
// commands with the host's own permissions under the application-level policy
// (personal-client best effort, shown as a needs-action row).
//
// The copy is the only full statement of the sandbox facts (A5 §3.1: one
// concept, one wording) — the behavior page's permission block explains the
// approval presets and does not repeat them.

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// sandboxStatusText resolves the one-line status copy of the system sandbox and
// its status kind, mode-aware (RG-02):
//   - full: the runner does not wrap commands, so the state is a warning
//     ("完全访问：系统沙箱不包装命令"), never the green "已启用" success that would
//     imply protection that is not there;
//   - workspace/read-only with a usable runner: success ("已启用");
//   - no usable runner: needs-action (no OS fence; commands run with the host's
//     own permissions under the application-level policy).
func sandboxStatusText(locale contracts.LocaleService, status contracts.SandboxStatus, mode string) (string, uikit.StatusKind) {
	platform := strings.TrimSpace(status.Platform)
	if platform == "" {
		platform = runtime.GOOS
	}
	if mode == contracts.PermissionFull {
		return fmt.Sprintf(localized(locale,
				"完全访问：系统沙箱不包装命令（平台：%s）",
				"Full access: the OS sandbox does not wrap commands (platform: %s)"), platform),
			uikit.StatusNeedsAction
	}
	if status.Available {
		return fmt.Sprintf(localized(locale, "已启用（平台：%s）", "On (platform: %s)"), platform),
			uikit.StatusSuccess
	}
	reason := strings.TrimSpace(status.Reason)
	if reason == "" {
		reason = localized(locale, "未找到可用的平台沙箱", "no usable platform sandbox found")
	}
	return localized(locale, "本机不可用：", "Unavailable on this host: ") + reason, uikit.StatusNeedsAction
}

// sandboxNoteText is the one sentence under the status row, conditioned on the
// mode (RG-02): the unconditional "始终启用、不可关闭" promise only holds while a
// wrapping mode is active; under full access the sentence discloses that
// commands run with the host's own permissions instead.
func sandboxNoteText(locale contracts.LocaleService, mode string) string {
	if mode == contracts.PermissionFull {
		return localized(locale,
			"当前为「所有权限」：命令不经系统沙箱包装，直接以宿主权限运行；切回「工作区修改」或「只读」后恢复包装。",
			"Under All permissions the OS sandbox does not wrap commands: they run with the host's own permissions; switch back to Workspace-write or Read-only to restore wrapping.")
	}
	if mode == contracts.PermissionReadOnly {
		return localized(locale,
			"始终启用、不可关闭；可用时 shell 类工具自动放行（命令在系统沙箱内运行，写被限制到策略允许的范围）；本机没有系统沙箱时不执行 shell 命令（工具侧直接拒绝），文件类工具仍只读。",
			"Always on and not configurable; shell tools are auto-allowed when the OS sandbox is available (commands run inside it and writes are limited to the policy's writable surface); without one, shell commands are refused by the tool and file tools stay read-only.")
	}
	return localized(locale,
		"始终启用、不可关闭；可用时 shell 类工具自动放行（命令在系统沙箱内运行，写被限制到策略允许的范围）；本机没有系统沙箱时命令以宿主权限运行，只受应用层策略与审批约束。",
		"Always on and not configurable; shell tools are auto-allowed when the OS sandbox is available (commands run inside it and writes are limited to the policy's writable surface); without one, commands run with the host's own permissions under the application-level policy and approval only.")
}

// buildSandboxSection renders the system sandbox section: the mode-aware live
// status line, the mode-conditioned note and the one rule that is neither the
// status nor the note — a session-level override never lifts the sandbox, and a
// permission change lands on the next tool build. There is no switch; a
// stripped assembly without the module renders the unavailable state. store
// supplies the global permission mode the status face must reflect (RG-02).
func buildSandboxSection(c *ui.Context, locale contracts.LocaleService, service contracts.SandboxService, store contracts.SettingsService) {
	t := design.Of(c)
	mode := contracts.PermissionWorkspace
	if store != nil {
		mode = contracts.NormalizePermissionMode(store.Get(contracts.SettingAgentPermission, ""))
	}
	uikit.Section(c, localized(locale, "系统沙箱", "OS sandbox"), func(*ui.Context) {
		status := contracts.SandboxStatus{Platform: runtime.GOOS}
		if service != nil {
			status = service.Status()
		} else {
			status.Reason = localized(locale, "未加载 sandbox 模块", "sandbox module is not mounted")
		}
		buildSandboxStatusRow(c, t, locale, status, mode)
		ui.Column(c).FillWidth().Gap(design.SpaceRow).MaxWidth(design.ProseMeasure).Children(func() {
			design.Apply(ui.Text(c, sandboxNoteText(locale, mode)), descStyle(t)).MaxLines(2)
			design.Apply(ui.Text(c, localized(locale,
				"会话或自动化的「所有权限」只放宽审批，不解除沙箱；修改在下次构建工具时生效。",
				"A session or automation-level All permissions only relaxes approval, never lifts the sandbox; a change applies on the next tool build.")),
				descStyle(t)).MaxLines(2)
		})
	})
}

// buildSandboxStatusRow renders the one-line mode-aware status of the system
// sandbox.
func buildSandboxStatusRow(c *ui.Context, t *design.Tokens, locale contracts.LocaleService,
	status contracts.SandboxStatus, mode string) {
	text, kind := sandboxStatusText(locale, status, mode)
	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, localized(locale, "状态：", "Status: ")), t.Font(design.TypeXS)).
			TextColor(t.LabelSecondary).Shrink(0)
		uikit.StatusText(c, t, design.TypeXS, kind, text).
			Grow(1).Selectable()
	})
}
