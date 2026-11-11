package agent

// OS-sandbox shell auto-allow. When the optional contracts.SandboxService
// (provided by zeokeel/sandbox) reports a usable platform runner, the OS
// profile already enforces the file effects of command execution, so the shell
// family no longer needs a per-call approval prompt. The service is read on
// every approval: switching the sandbox off or a host without a usable runner
// restores the previous approval flow without a restart.

import "strings"

// sandboxShellTools is the shell family covered by the auto-allow: command
// execution (bash/pwsh/job_start) plus the interactive terminal session tools.
// terminal_list/terminal_read are already in the shared read-only allowlist and
// are intentionally not duplicated here.
var sandboxShellTools = map[string]bool{
	"bash":            true,
	"pwsh":            true,
	"job_start":       true,
	"terminal_open":   true,
	"terminal_send":   true,
	"terminal_signal": true,
	"terminal_close":  true,
}

// sandboxShellAutoAllowed 报告 shell 家族工具能否因 OS 沙箱可用而跳过审批。
// 仅当 sandbox 服务存在且其平台 runner 可用（Available）且工具名裁剪空白后
// 精确命中 shell 家族时为 true；服务为 nil 视为不可用，保持原有审批流程。
func (s *service) sandboxShellAutoAllowed(name string) bool {
	if s == nil || s.sandboxService == nil || !s.sandboxService.Available() {
		return false
	}
	return sandboxShellTools[strings.TrimSpace(name)]
}
