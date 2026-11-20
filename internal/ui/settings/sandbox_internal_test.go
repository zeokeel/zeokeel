package settings

// Deterministic unit tests for the OS sandbox status line copy, mode-aware
// (RG-02): full access passes commands through unconfined (a warning, not the
// "已启用" success), workspace/read-only with a usable runner is enabled, and a
// host without a runner fails closed.

import (
	"runtime"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// sandboxLocaleStub is a fixed-language contracts.LocaleService.
type sandboxLocaleStub struct{ lang string }

func (l sandboxLocaleStub) Language() string     { return l.lang }
func (l sandboxLocaleStub) SetLanguage(_ string) {}
func (l sandboxLocaleStub) T(key string) string  { return key }

var _ contracts.LocaleService = sandboxLocaleStub{}

func TestSandboxStatusText(t *testing.T) {
	zh := sandboxLocaleStub{lang: "zh"}
	en := sandboxLocaleStub{lang: "en"}
	cases := []struct {
		name     string
		locale   contracts.LocaleService
		status   contracts.SandboxStatus
		mode     string
		want     string
		wantKind uikit.StatusKind
	}{
		{
			name:     "available zh",
			locale:   zh,
			status:   contracts.SandboxStatus{Available: true, Platform: "linux"},
			mode:     contracts.PermissionWorkspace,
			want:     "已启用（平台：linux）",
			wantKind: uikit.StatusSuccess,
		},
		{
			name:     "available trims the platform",
			locale:   zh,
			status:   contracts.SandboxStatus{Available: true, Platform: " linux "},
			mode:     contracts.PermissionWorkspace,
			want:     "已启用（平台：linux）",
			wantKind: uikit.StatusSuccess,
		},
		{
			name:     "available falls back to the host platform",
			locale:   zh,
			status:   contracts.SandboxStatus{Available: true},
			mode:     contracts.PermissionReadOnly,
			want:     "已启用（平台：" + runtime.GOOS + "）",
			wantKind: uikit.StatusSuccess,
		},
		{
			name:     "unavailable zh",
			locale:   zh,
			status:   contracts.SandboxStatus{Available: false, Platform: "linux", Reason: "bwrap not found"},
			mode:     contracts.PermissionWorkspace,
			want:     "本机不可用：bwrap not found",
			wantKind: uikit.StatusNeedsAction,
		},
		{
			name:     "unavailable without a reason zh",
			locale:   zh,
			status:   contracts.SandboxStatus{Available: false, Platform: "linux"},
			mode:     contracts.PermissionWorkspace,
			want:     "本机不可用：未找到可用的平台沙箱",
			wantKind: uikit.StatusNeedsAction,
		},
		{
			name:     "available en",
			locale:   en,
			status:   contracts.SandboxStatus{Available: true, Platform: "darwin"},
			mode:     contracts.PermissionWorkspace,
			want:     "On (platform: darwin)",
			wantKind: uikit.StatusSuccess,
		},
		{
			name:     "unavailable en",
			locale:   en,
			status:   contracts.SandboxStatus{Available: false, Platform: "windows", Reason: "seatbelt probe failed"},
			mode:     contracts.PermissionWorkspace,
			want:     "Unavailable on this host: seatbelt probe failed",
			wantKind: uikit.StatusNeedsAction,
		},
		{
			name:     "full access warns even with a runner zh",
			locale:   zh,
			status:   contracts.SandboxStatus{Available: true, Platform: "linux"},
			mode:     contracts.PermissionFull,
			want:     "完全访问：系统沙箱不包装命令（平台：linux）",
			wantKind: uikit.StatusNeedsAction,
		},
		{
			name:     "full access warns en",
			locale:   en,
			status:   contracts.SandboxStatus{Available: true, Platform: "darwin"},
			mode:     contracts.PermissionFull,
			want:     "Full access: the OS sandbox does not wrap commands (platform: darwin)",
			wantKind: uikit.StatusNeedsAction,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, kind := sandboxStatusText(test.locale, test.status, test.mode)
			if got != test.want {
				t.Fatalf("sandboxStatusText() = %q, want %q", got, test.want)
			}
			if kind != test.wantKind {
				t.Fatalf("sandboxStatusText() kind = %v, want %v", kind, test.wantKind)
			}
		})
	}
}

// TestSandboxNoteTextModeConditioned pins the mode-conditioned note (RG-02): the
// unconditional always-on promise holds only for wrapping modes; full access
// discloses the passthrough instead.
func TestSandboxNoteTextModeConditioned(t *testing.T) {
	zh := sandboxLocaleStub{lang: "zh"}
	wrapping := sandboxNoteText(zh, contracts.PermissionWorkspace)
	if !strings.Contains(wrapping, "始终启用、不可关闭") {
		t.Fatalf("workspace note lost the always-on promise: %q", wrapping)
	}
	full := sandboxNoteText(zh, contracts.PermissionFull)
	if strings.Contains(full, "始终启用、不可关闭") {
		t.Fatalf("full-access note still claims always-on wrapping: %q", full)
	}
	if !strings.Contains(full, "所有权限") || !strings.Contains(full, "不经系统沙箱包装") {
		t.Fatalf("full-access note does not disclose the passthrough: %q", full)
	}
	readOnly := sandboxNoteText(zh, contracts.PermissionReadOnly)
	if !strings.Contains(readOnly, "不执行 shell 命令") || !strings.Contains(readOnly, "工具侧直接拒绝") {
		t.Fatalf("read-only note does not say what a host without a runner does: %q", readOnly)
	}
}
