package tasklease_test

// TDD for the per-task tool binding (M5 execution policy). The binding is what
// makes a dispatched task's promise enforceable, so the tests concentrate on
// the ways a policy can quietly become wrong: a rebind that merges instead of
// replacing, a "clear" that leaves the old set behind, a validation error that
// is swallowed, a restart that forgets the policy, and a binding attached to a
// task that does not exist.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/tasklease"
)

func TestSetAndGetToolsRoundTrip(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, clock := newService(t, board, nil)
	ctx := context.Background()

	if _, ok, err := svc.Tools(ctx, "task-1"); err != nil || ok {
		t.Fatalf("Tools before binding = (_, %v, %v), want no binding", ok, err)
	}

	binding, err := svc.SetTools(ctx, "task-1", []string{"browser_open", "mcp__github__search"})
	if err != nil {
		t.Fatalf("SetTools: %v", err)
	}
	if binding.TaskID != "task-1" || len(binding.Tools) != 2 {
		t.Fatalf("SetTools = %+v, want task-1 with two tools", binding)
	}
	if !binding.UpdatedAt.Equal(clock.now()) {
		t.Fatalf("UpdatedAt = %v, want %v", binding.UpdatedAt, clock.now())
	}

	got, ok, err := svc.Tools(ctx, "task-1")
	if err != nil || !ok {
		t.Fatalf("Tools = (_, %v, %v), want found", ok, err)
	}
	if len(got.Tools) != 2 || got.Tools[0] != "browser_open" || got.Tools[1] != "mcp__github__search" {
		t.Fatalf("Tools = %v, want the bound names in order", got.Tools)
	}
	if !got.UpdatedAt.Equal(binding.UpdatedAt) {
		t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, binding.UpdatedAt)
	}

	// The caller's slice is copied: mutating it must not rewrite a stored policy.
	got.Tools[0] = "mutated"
	again, _, err := svc.Tools(ctx, "task-1")
	if err != nil {
		t.Fatalf("Tools after mutate: %v", err)
	}
	if again.Tools[0] != "browser_open" {
		t.Fatal("the binding shares its slice with the caller")
	}
}

func TestSetToolsReplacesThePreviousBinding(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, clock := newService(t, board, nil)
	ctx := context.Background()

	if _, err := svc.SetTools(ctx, "task-1", []string{"browser_open", "fs.read"}); err != nil {
		t.Fatalf("first SetTools: %v", err)
	}
	clock.advance(time.Minute)
	rebound, err := svc.SetTools(ctx, "task-1", []string{"shell.run"})
	if err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if len(rebound.Tools) != 1 || rebound.Tools[0] != "shell.run" {
		t.Fatalf("rebind = %v, want exactly [shell.run] (replace, not merge)", rebound.Tools)
	}
	if !rebound.UpdatedAt.Equal(clock.now()) {
		t.Fatalf("rebind UpdatedAt = %v, want %v", rebound.UpdatedAt, clock.now())
	}
	got, _, err := svc.Tools(ctx, "task-1")
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(got.Tools) != 1 || got.Tools[0] != "shell.run" {
		t.Fatalf("stored = %v, want the replacement", got.Tools)
	}
}

func TestSetToolsEmptySliceClearsTheBinding(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	if _, err := svc.SetTools(ctx, "task-1", []string{"browser_open"}); err != nil {
		t.Fatalf("SetTools: %v", err)
	}
	cleared, err := svc.SetTools(ctx, "task-1", []string{})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if len(cleared.Tools) != 0 {
		t.Fatalf("clear = %v, want no tools", cleared.Tools)
	}
	if _, ok, err := svc.Tools(ctx, "task-1"); err != nil || ok {
		t.Fatalf("Tools after clear = (_, %v, %v), want no binding", ok, err)
	}
	// Clearing twice is a no-op, not an error: a delete is idempotent.
	if _, err := svc.SetTools(ctx, "task-1", nil); err != nil {
		t.Fatalf("second clear: %v", err)
	}
}

func TestSetToolsValidation(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	many := make([]string, 0, contracts.MaxTaskTools+1)
	for i := 0; i <= contracts.MaxTaskTools; i++ {
		many = append(many, "tool"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	longName := strings.Repeat("a", contracts.TaskToolNameMaxLen+1)

	cases := []struct {
		name  string
		tools []string
		want  error
	}{
		{name: "empty name", tools: []string{""}, want: contracts.ErrTaskToolsInvalid},
		{name: "uppercase name", tools: []string{"Browser_Open"}, want: contracts.ErrTaskToolsInvalid},
		{name: "space in name", tools: []string{"browser open"}, want: contracts.ErrTaskToolsInvalid},
		{name: "slash in name", tools: []string{"../secret"}, want: contracts.ErrTaskToolsInvalid},
		{name: "too long", tools: []string{longName}, want: contracts.ErrTaskToolsInvalid},
		{name: "too many", tools: many, want: contracts.ErrTaskToolsInvalid},
		{name: "duplicate", tools: []string{"browser_open", "browser_open"}, want: contracts.ErrTaskToolsInvalid},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := svc.SetTools(ctx, "task-1", testCase.tools); !errors.Is(err, testCase.want) {
				t.Fatalf("SetTools(%v) err = %v, want %v", testCase.tools, err, testCase.want)
			}
			// A rejected request must leave no trace: no partial binding.
			if _, ok, err := svc.Tools(ctx, "task-1"); err != nil || ok {
				t.Fatalf("binding after a rejected request = (_, %v, %v), want none", ok, err)
			}
		})
	}

	// The boundaries themselves are accepted.
	if _, err := svc.SetTools(ctx, "task-1", []string{longName[:contracts.TaskToolNameMaxLen]}); err != nil {
		t.Fatalf("a %d-char name must be accepted: %v", contracts.TaskToolNameMaxLen, err)
	}
	if _, err := svc.SetTools(ctx, "task-2", many[:contracts.MaxTaskTools]); !errors.Is(err, contracts.ErrTaskToolsUnknownTask) {
		t.Fatalf("exactly %d tools err = %v, want only the unknown-task error", contracts.MaxTaskTools, err)
	}
	// The alphabet allows the dots/colons/dashes real tool names use.
	if _, err := svc.SetTools(ctx, "task-1", []string{"mcp__github.search:issue", "fs-1:read"}); err != nil {
		t.Fatalf("real tool names rejected: %v", err)
	}
}

func TestSetToolsOnUnknownTaskIsTyped(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	_, err := svc.SetTools(ctx, "nope", []string{"browser_open"})
	if !errors.Is(err, contracts.ErrTaskToolsUnknownTask) {
		t.Fatalf("SetTools(unknown) err = %v, want ErrTaskToolsUnknownTask", err)
	}
	if _, ok, err := svc.Tools(ctx, "nope"); err != nil || ok {
		t.Fatalf("Tools(unknown) = (_, %v, %v), want no binding", ok, err)
	}
}

func TestToolsBindingSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	board := newFakeBoard("task-1", "task-2")
	clock := newClock()
	cfg := tasklease.Config{Now: clock.now, TTL: time.Minute}
	ctx := context.Background()

	first := openLeaseStore(t, dir, board, cfg)
	bound, err := first.SetTools(ctx, "task-1", []string{"browser_open", "mcp__github__search"})
	if err != nil {
		t.Fatalf("SetTools: %v", err)
	}
	if _, err := first.SetTools(ctx, "task-2", []string{"fs.read"}); err != nil {
		t.Fatalf("SetTools task-2: %v", err)
	}
	// Clearing one binding must not disturb the other, and must be durable.
	if _, err := first.SetTools(ctx, "task-2", nil); err != nil {
		t.Fatalf("clear task-2: %v", err)
	}

	second := openLeaseStore(t, dir, board, cfg)
	restored, ok, err := second.Tools(ctx, "task-1")
	if err != nil || !ok {
		t.Fatalf("Tools after reopen = (_, %v, %v), want the binding back", ok, err)
	}
	if len(restored.Tools) != 2 || restored.Tools[0] != "browser_open" || restored.Tools[1] != "mcp__github__search" {
		t.Fatalf("restored = %v, want the bound names", restored.Tools)
	}
	if !restored.UpdatedAt.Equal(bound.UpdatedAt) {
		t.Fatalf("restored UpdatedAt = %v, want %v", restored.UpdatedAt, bound.UpdatedAt)
	}
	if _, ok, err := second.Tools(ctx, "task-2"); err != nil || ok {
		t.Fatalf("cleared binding came back: (_, %v, %v)", ok, err)
	}
}

// A document written before the binding existed (v1, no `tools` key) must keep
// loading: the additive key is why the store version was not bumped.
func TestOldStoreWithoutToolsStillLoads(t *testing.T) {
	dir := t.TempDir()
	board := newFakeBoard("task-1")
	clock := newClock()
	cfg := tasklease.Config{Now: clock.now, TTL: time.Minute}
	ctx := context.Background()
	seed := `{"version":1,"leases":[{"TaskID":"task-1","Owner":"bot-1",` +
		`"Until":"2026-11-25T09:10:00Z","Heartbeat":"2026-11-25T09:00:00Z","State":"claimed"}]}`
	if err := writeLeaseFile(dir, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	svc := openLeaseStore(t, dir, board, cfg)
	if lease, ok, err := svc.Lease(ctx, "task-1"); err != nil || !ok || lease.Owner != "bot-1" {
		t.Fatalf("legacy lease = (%+v, %v, %v), want bot-1", lease, ok, err)
	}
	if _, ok, err := svc.Tools(ctx, "task-1"); err != nil || ok {
		t.Fatalf("legacy tools = (_, %v, %v), want no binding", ok, err)
	}
	// Binding after the legacy load writes a document that still reloads.
	if _, err := svc.SetTools(ctx, "task-1", []string{"browser_open"}); err != nil {
		t.Fatalf("SetTools on a legacy store: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "leases.json"))
	if err != nil {
		t.Fatalf("read rewritten store: %v", err)
	}
	if !strings.Contains(string(raw), `"tools"`) {
		t.Fatalf("rewritten store has no tools key: %s", raw)
	}
	reopened := openLeaseStore(t, dir, board, cfg)
	if binding, ok, err := reopened.Tools(ctx, "task-1"); err != nil || !ok || len(binding.Tools) != 1 {
		t.Fatalf("binding after legacy store rewrite = (%+v, %v, %v)", binding, ok, err)
	}
	if lease, ok, err := reopened.Lease(ctx, "task-1"); err != nil || !ok || lease.Owner != "bot-1" {
		t.Fatalf("lease after legacy store rewrite = (%+v, %v, %v), want bot-1 preserved", lease, ok, err)
	}
}

// The binding belongs to the task, not the lease: a release and a fresh claim
// must not silently drop the execution policy.
func TestToolsBindingOutlivesTheLease(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	if _, err := svc.SetTools(ctx, "task-1", []string{"browser_open"}); err != nil {
		t.Fatalf("SetTools: %v", err)
	}
	if _, err := svc.Claim(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := svc.Release(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := svc.Claim(ctx, "task-1", "bot-2"); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	binding, ok, err := svc.Tools(ctx, "task-1")
	if err != nil || !ok {
		t.Fatalf("Tools after release+reclaim = (_, %v, %v), want the binding kept", ok, err)
	}
	if len(binding.Tools) != 1 || binding.Tools[0] != "browser_open" {
		t.Fatalf("binding = %v, want [browser_open]", binding.Tools)
	}
}

// writeLeaseFile seeds dir/leases.json with raw bytes (a document written by an
// older build, or a hand-edited one).
func writeLeaseFile(dir, content string) error {
	return os.WriteFile(filepath.Join(dir, "leases.json"), []byte(content), 0o600)
}
