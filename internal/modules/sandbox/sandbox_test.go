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

// 本机不可用：必须返回 fail-closed runner，而不是 nil，且没有关闭路径。
func TestServiceUnavailableFailsClosed(t *testing.T) {
	probeErr := errors.New("no landlock and no bwrap")
	svc := newTestService(t, func(osrunner.Config) (rigsandbox.Runner, error) {
		return nil, probeErr
	})
	if svc.Available() {
		t.Fatal("Available = true despite a probe error")
	}
	runner := svc.Runner()
	if runner == nil {
		t.Fatal("Runner = nil, want a fail-closed runner")
	}
	_, err := runner.Confine([]string{"/bin/sh", "-c", "echo hi"}, rigsandbox.New())
	if err == nil {
		t.Fatal("Confine succeeded, want a fail-closed error")
	}
	if !errors.Is(err, rigsandbox.ErrSandboxUnavailable) {
		t.Fatalf("Confine error = %v, want ErrSandboxUnavailable", err)
	}
	status := svc.Status()
	if status.Available {
		t.Fatal("Status.Available = true despite a probe error")
	}
	if !strings.Contains(status.Reason, "no landlock") {
		t.Fatalf("Status.Reason = %q, want the probe error", status.Reason)
	}
}

// nil service 也必须 fail closed（防御性）。
func TestNilServiceFailsClosed(t *testing.T) {
	var svc *service
	if svc.Available() {
		t.Fatal("nil service reports available")
	}
	runner := svc.Runner()
	if runner == nil {
		t.Fatal("nil service Runner = nil, want a fail-closed runner")
	}
	if _, err := runner.Confine([]string{"/bin/sh"}, rigsandbox.New()); err == nil {
		t.Fatal("nil service Confine succeeded")
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
