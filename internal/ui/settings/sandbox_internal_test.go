package settings

// Deterministic unit tests for the OS sandbox status line copy: the sandbox is
// always on, so the only two states are available and unavailable-on-this-host.

import (
	"runtime"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
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
		name       string
		locale     contracts.LocaleService
		status     contracts.SandboxStatus
		want       string
		wantFailed bool
	}{
		{
			name:   "available zh",
			locale: zh,
			status: contracts.SandboxStatus{Available: true, Platform: "linux"},
			want:   "已启用（平台：linux）",
		},
		{
			name:   "available trims the platform",
			locale: zh,
			status: contracts.SandboxStatus{Available: true, Platform: " linux "},
			want:   "已启用（平台：linux）",
		},
		{
			name:   "available falls back to the host platform",
			locale: zh,
			status: contracts.SandboxStatus{Available: true},
			want:   "已启用（平台：" + runtime.GOOS + "）",
		},
		{
			name:   "unavailable zh",
			locale: zh,
			status: contracts.SandboxStatus{Available: false, Platform: "linux",
				Reason: "bwrap not found"},
			want:       "本机不可用：bwrap not found",
			wantFailed: true,
		},
		{
			name:       "unavailable without a reason zh",
			locale:     zh,
			status:     contracts.SandboxStatus{Available: false, Platform: "linux"},
			want:       "本机不可用：未找到可用的平台沙箱",
			wantFailed: true,
		},
		{
			name:   "available en",
			locale: en,
			status: contracts.SandboxStatus{Available: true, Platform: "darwin"},
			want:   "On (platform: darwin)",
		},
		{
			name:   "unavailable en",
			locale: en,
			status: contracts.SandboxStatus{Available: false, Platform: "windows",
				Reason: "seatbelt probe failed"},
			want:       "Unavailable on this host: seatbelt probe failed",
			wantFailed: true,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, failed := sandboxStatusText(test.locale, test.status)
			if got != test.want {
				t.Fatalf("sandboxStatusText() = %q, want %q", got, test.want)
			}
			if failed != test.wantFailed {
				t.Fatalf("sandboxStatusText() failed = %v, want %v", failed, test.wantFailed)
			}
		})
	}
}
