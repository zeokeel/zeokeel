package agent

// The undo turn hook is the "snapshot before the turn" half of the undo tool:
// every run asks the toolkit to snapshot its own workspace root, and a snapshot
// failure is logged, never fatal.

import (
	"context"
	"errors"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeUndo records the roots it was asked to snapshot.
type fakeUndo struct {
	roots []string
	err   error
}

func (f *fakeUndo) BeginTurn(_ context.Context, root string) (int, error) {
	f.roots = append(f.roots, root)
	if f.err != nil {
		return 0, f.err
	}
	return 1, nil
}

func TestUndoTurnHookSnapshotsTheRunRoot(t *testing.T) {
	undo := &fakeUndo{}
	svc := newService(Config{}, nil, nil, &fakeLogger{}, newFakeStore(), nil)
	svc.undo = undo

	hooks := svc.turnHooks("/work/project")
	if len(hooks) != 1 {
		t.Fatalf("turn hooks = %d, want the undo hook", len(hooks))
	}
	if err := hooks[0].BeforeRun(context.Background(), rig.RunRequest{}); err != nil {
		t.Fatalf("BeforeRun: %v", err)
	}
	if len(undo.roots) != 1 || undo.roots[0] != "/work/project" {
		t.Fatalf("snapshot roots = %v, want the run workspace", undo.roots)
	}
}

func TestUndoTurnHookFailureIsNotFatal(t *testing.T) {
	undo := &fakeUndo{err: errors.New("no git, no snapshot dir")}
	svc := newService(Config{}, nil, nil, &fakeLogger{}, newFakeStore(), nil)
	svc.undo = undo
	hooks := svc.turnHooks("")
	if len(hooks) != 1 {
		t.Fatalf("turn hooks = %d, want the undo hook", len(hooks))
	}
	if err := hooks[0].BeforeRun(context.Background(), rig.RunRequest{}); err != nil {
		t.Fatalf("BeforeRun must not fail the run: %v", err)
	}
}

func TestNoUndoServiceNoHooks(t *testing.T) {
	svc := newService(Config{}, nil, nil, &fakeLogger{}, newFakeStore(), nil)
	if hooks := svc.turnHooks("/work"); len(hooks) != 0 {
		t.Fatalf("turn hooks = %d, want none without an undo service", len(hooks))
	}
}

// TestUndoTurnHookSilentForUnavailable pins the log policy: "no tracker for this
// workspace" is a normal read-only condition, other snapshot failures are logged.
func TestUndoTurnHookSilentForUnavailable(t *testing.T) {
	quiet := &fakeUndo{err: contracts.ErrUndoUnavailable}
	logger := &fakeLogger{}
	svc := newService(Config{}, nil, nil, logger, newFakeStore(), nil)
	svc.undo = quiet
	if err := svc.turnHooks("/work")[0].BeforeRun(context.Background(), rig.RunRequest{}); err != nil {
		t.Fatalf("BeforeRun: %v", err)
	}
	logger.mu.Lock()
	lines := len(logger.lines)
	logger.mu.Unlock()
	if lines != 0 {
		t.Fatalf("unavailable tracker logged %d lines, want none", lines)
	}

	noisy := &fakeUndo{err: errors.New("disk full")}
	logger = &fakeLogger{}
	svc = newService(Config{}, nil, nil, logger, newFakeStore(), nil)
	svc.undo = noisy
	if err := svc.turnHooks("/work")[0].BeforeRun(context.Background(), rig.RunRequest{}); err != nil {
		t.Fatalf("BeforeRun: %v", err)
	}
	logger.mu.Lock()
	lines = len(logger.lines)
	logger.mu.Unlock()
	if lines != 1 {
		t.Fatalf("real snapshot failure logged %d lines, want one", lines)
	}
}
