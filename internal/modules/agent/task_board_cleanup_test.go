package agent

// Tests for the DeleteSession task-board cleanup hook.

import (
	"context"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// recordingCleanerBoard is a minimal task board that records cleanup calls.
type recordingCleanerBoard struct {
	removed []string
}

var (
	_ contracts.TaskBoard        = (*recordingCleanerBoard)(nil)
	_ contracts.TaskBoardCleaner = (*recordingCleanerBoard)(nil)
)

func (b *recordingCleanerBoard) List(context.Context) ([]contracts.BoardTask, error) {
	return nil, nil
}

func (b *recordingCleanerBoard) Create(context.Context, string, string) (contracts.BoardTask, error) {
	return contracts.BoardTask{}, nil
}

func (b *recordingCleanerBoard) SetStatus(context.Context, string, string) (contracts.BoardTask, error) {
	return contracts.BoardTask{}, nil
}

func (b *recordingCleanerBoard) Delete(context.Context, string) error { return nil }

func (b *recordingCleanerBoard) RemoveTaskBoardForSession(sessionID string) error {
	b.removed = append(b.removed, sessionID)
	return nil
}

func TestDeleteSessionRemovesTaskBoard(t *testing.T) {
	svc, _, _, ctx := bootAgentModuleAt(t, t.TempDir())
	defer func() { _ = ctx.DisposeAll() }()
	board := &recordingCleanerBoard{}
	svc.taskBoard = board
	session, err := svc.NewSession("待删除")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.DeleteSession(session); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if len(board.removed) != 1 || board.removed[0] != session {
		t.Fatalf("cleanup calls = %v, want [%s]", board.removed, session)
	}
}
