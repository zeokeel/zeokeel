package sandbox

import (
	"errors"
	"strings"
	"testing"
	"time"

	rigsandbox "github.com/zeokeel/zeokeel-rig/sandbox"
	"github.com/zeokeel/zeokeel-rig/sandbox/osrunner"
)

// fakeRunner records whether it was called and can fail.
type fakeRunner struct {
	confined bool
	err      error
}

func (r *fakeRunner) Confine(argv []string, _ *rigsandbox.Policy) (rigsandbox.ConfinedCommand, error) {
	r.confined = true
	if r.err != nil {
		return rigsandbox.ConfinedCommand{}, r.err
	}
	return rigsandbox.ConfinedCommand{Argv: append([]string(nil), argv...), Enforcement: rigsandbox.EnforcementFull}, nil
}

// newTestService builds a service with an injected runner factory.
func newTestService(t *testing.T, factory runnerFactory) *service {
	t.Helper()
	return newService(Config{}, nil, factory)
}

// 可用：Runner 返回平台 runner，Status 报告可用；沙箱没有开关。
func TestServiceAvailable(t *testing.T) {
	runner := &fakeRunner{}
	svc := newTestService(t, func(osrunner.Config) (rigsandbox.Runner, error) {
		return runner, nil
	})
	if !svc.Available() {
		t.Fatal("sandbox is unavailable with a working fake runner")
	}
	if svc.Runner() != runner {
		t.Fatal("Runner did not return the factory runner")
	}
	status := svc.Status()
	if !status.Available {
		t.Fatalf("Status = %+v, want available", status)
	}
	if status.Reason != "" {
		t.Fatalf("Status.Reason = %q, want empty", status.Reason)
	}
	if status.Platform == "" {
		t.Fatal("Status.Platform is empty")
	}
}

// 本机不可用：个人客户端走"尽力而为"——Runner 返回 nil，命令以宿主权限运行
// （仍受应用层策略与审批约束），启动不失败；Status 说明原因。
func TestServiceUnavailableReturnsNilRunner(t *testing.T) {
	probeErr := errors.New("no landlock and no bwrap")
	svc := newTestService(t, func(osrunner.Config) (rigsandbox.Runner, error) {
		return nil, probeErr
	})
	if svc.Available() {
		t.Fatal("Available = true despite a probe error")
	}
	if runner := svc.Runner(); runner != nil {
		t.Fatalf("Runner = %v, want nil (best effort, not fail closed)", runner)
	}
	status := svc.Status()
	if status.Available {
		t.Fatal("Status.Available = true despite a probe error")
	}
	if !strings.Contains(status.Reason, "no landlock") {
		t.Fatalf("Status.Reason = %q, want the probe error", status.Reason)
	}
}

// nil service 同样返回 nil：装配里没有 sandbox 模块时不能假装有围栏。
func TestNilServiceReturnsNilRunner(t *testing.T) {
	var svc *service
	if svc.Available() {
		t.Fatal("nil service reports available")
	}
	if runner := svc.Runner(); runner != nil {
		t.Fatalf("nil service Runner = %v, want nil", runner)
	}
	if status := svc.Status(); status.Available {
		t.Fatalf("nil service Status = %+v", status)
	}
}

// 探测使用配置的超时；未配置时用 osrunner 默认值。
func TestServiceProbeTimeout(t *testing.T) {
	var got time.Duration
	newService(Config{ProbeTimeoutMs: 1234}, nil, func(cfg osrunner.Config) (rigsandbox.Runner, error) {
		got = cfg.ProbeTimeout
		return &fakeRunner{}, nil
	})
	if got != 1234*time.Millisecond {
		t.Fatalf("probe timeout = %v, want 1.234s", got)
	}

	newService(Config{}, nil, func(cfg osrunner.Config) (rigsandbox.Runner, error) {
		got = cfg.ProbeTimeout
		return &fakeRunner{}, nil
	})
	if got != osrunner.DefaultProbeTimeout {
		t.Fatalf("default probe timeout = %v, want %v", got, osrunner.DefaultProbeTimeout)
	}
}
