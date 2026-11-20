package agent

// The undo turn hook is the "snapshot before the turn" half of the undo tool:
// every run asks the toolkit to snapshot its own workspace root, and a snapshot
// failure is logged, never fatal.

import (
	"context"
	"errors"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
)

// fakeUndo records the roots it was asked to snapshot.
type fakeUndo struct {
	roots []string
	err   error
}

func (f *fakeUndo) BeginTurn(root string) (int, error) {
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
