package agent

// Hard loop breaker: per-run wall-clock/token budgets, exact repeat calls and
// consecutive read-only calls with no mutation. The advisory repeat guard is
// covered by loop_guard_test.go; this guard actually cancels the run.

import (
	"context"
	"strings"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestRunLoopGuardWallClock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	guard := newRunLoopGuard(cancel, runLoopGuardConfig{WallClock: 20 * time.Millisecond})
	guard.start()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("wall-clock guard did not cancel")
	}
	guard.stop()
	if !guard.tripped() || !strings.Contains(guard.reason(), "wall clock") {
		t.Fatalf("guard tripped=%v reason=%q", guard.tripped(), guard.reason())
	}
}

func TestRunLoopGuardTokenBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	guard := newRunLoopGuard(cancel, runLoopGuardConfig{TokenBudget: 100})
	guard.observeUsage(60)
	guard.observeUsage(40)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("token guard did not cancel")
	}
	if !strings.Contains(guard.reason(), "token") {
		t.Fatalf("reason = %q", guard.reason())
	}
}

func TestRunLoopGuardRepeatCalls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	guard := newRunLoopGuard(cancel, runLoopGuardConfig{RepeatLimit: 3})
	if ok := guard.observeTool("read_file", `{"path":"a"}`); !ok {
		t.Fatal("first call tripped")
	}
	if ok := guard.observeTool("read_file", `{"path":"a"}`); !ok {
		t.Fatal("second call tripped")
	}
	if ok := guard.observeTool("read_file", `{"path":"a"}`); ok {
		t.Fatal("third identical call did not trip")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("repeat guard did not cancel")
	}
}

func TestRunLoopGuardNoDiff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx
	guard := newRunLoopGuard(cancel, runLoopGuardConfig{NoDiffCalls: 3})
	guard.observeTool("read_file", `{"path":"a"}`)
	guard.observeTool("grep", `{"pattern":"x"}`)
	if ok := guard.observeTool("glob", `{"pattern":"*"}`); ok {
		t.Fatal("third read-only call did not trip no-diff")
	}
	if !strings.Contains(guard.reason(), "diff") {
		t.Fatalf("reason = %q", guard.reason())
	}
}

func TestRunLoopGuardMutationResetsNoDiff(t *testing.T) {
	guard := newRunLoopGuard(func() {}, runLoopGuardConfig{NoDiffCalls: 3})
	guard.observeTool("read_file", `{"path":"a"}`)
	guard.observeTool("read_file", `{"path":"b"}`)
	guard.observeTool("write_file", `{"path":"a","content":"x"}`)
	guard.observeTool("read_file", `{"path":"a"}`)
	guard.observeTool("read_file", `{"path":"b"}`)
	if ok := guard.observeTool("read_file", `{"path":"c"}`); ok {
		t.Fatal("read-only chain after a mutation tripped too early")
	}
}

func TestRunLoopBreakerStopsRepeatedCalls(t *testing.T) {
	settings := newFakeSettings(map[string]string{contracts.SettingAgentLoopRepeatLimit: "2"})
	provider := &repeatingToolProvider{}
	svc := newTestService(t, Config{}, settings, nil, &echoTool{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	id, err := svc.NewSession("loop breaker")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "go"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, id)
	if len(session.Messages) == 0 {
		t.Fatal("no messages after loop breaker")
	}
	last := session.Messages[len(session.Messages)-1]
	if last.ErrorKind != contracts.MessageErrorKindLoop {
		t.Fatalf("error kind = %q, want %q (error %q)", last.ErrorKind, contracts.MessageErrorKindLoop, last.Error)
	}
	if !strings.Contains(last.Error, "loop guard:") {
		t.Fatalf("error = %q, want loop guard reason", last.Error)
	}
}
