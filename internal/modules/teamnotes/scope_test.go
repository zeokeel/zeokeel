package teamnotes_test

// Agent-scoped notes are private to their bot. The enforcement is the storage
// type and subtree: the team board filters on the team type, so a private note
// cannot leak onto the shared board even if someone asks for everything.

import (
	"context"
	"errors"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/teamnotes"
)

func TestAgentScopedNotesStayOffTheTeamBoard(t *testing.T) {
	service, memory := newService(t)
	ctx := context.Background()

	if _, err := service.Record(ctx, contracts.TeamNote{
		Title: "团队事实", Body: "shared", Author: "lead",
	}); err != nil {
		t.Fatalf("record team note: %v", err)
	}
	private, err := service.Record(ctx, contracts.TeamNote{
		Title: "私有技巧", Body: "only for bot-1", Author: "bot-1",
		Scope: contracts.NoteScopeAgent, Owner: "bot-1",
	})
	if err != nil {
		t.Fatalf("record agent note: %v", err)
	}
	if private.ID == "" || private.Scope != contracts.NoteScopeAgent {
		t.Fatalf("private note = %+v", private)
	}

	team, err := service.List(ctx, 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, note := range team {
		if note.Title == "私有技巧" {
			t.Fatalf("an agent-scoped note appeared on the team board: %+v", note)
		}
	}
	if len(team) != 1 {
		t.Fatalf("team notes = %d, want only the shared one", len(team))
	}

	// And the private note really is stored, under its own type.
	types := memory.typesSeen()
	if len(types) == 0 {
		t.Fatal("no writes reached the store")
	}
	if !contains(types, teamnotes.AgentNoteType) {
		t.Fatalf("stored types = %v, want the agent type", types)
	}
}

func TestAgentScopeNeedsAnOwner(t *testing.T) {
	service, _ := newService(t)
	_, err := service.Record(context.Background(), contracts.TeamNote{
		Title: "私有", Scope: contracts.NoteScopeAgent,
	})
	if !errors.Is(err, contracts.ErrTeamNoteInvalid) {
		t.Fatalf("err = %v, want ErrTeamNoteInvalid", err)
	}
}

func TestUnknownScopeIsRefused(t *testing.T) {
	service, _ := newService(t)
	if _, err := service.Record(context.Background(), contracts.TeamNote{
		Title: "x", Scope: "universe",
	}); !errors.Is(err, contracts.ErrTeamNoteInvalid) {
		t.Fatalf("err = %v, want ErrTeamNoteInvalid", err)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// M6: the same bot serves two people, and what one taught it stays theirs.
func TestUserScopedNotesAreInvisibleToOthers(t *testing.T) {
	service, memory := newService(t)
	ctx := context.Background()

	if _, err := service.Record(ctx, contracts.TeamNote{
		Title: "alice 的偏好", Body: "她喜欢先看结论", Author: "bot-1",
		Scope: contracts.NoteScopeUser, Owner: "alice",
	}); err != nil {
		t.Fatalf("record user note: %v", err)
	}
	board, err := service.List(ctx, 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(board) != 0 {
		t.Fatalf("a user-scoped note reached the shared board: %+v", board)
	}
	if !contains(memory.typesSeen(), teamnotes.UserNoteType) {
		t.Fatalf("stored types = %v, want the user type", memory.typesSeen())
	}
}

func TestUserScopeNeedsAnOwner(t *testing.T) {
	service, _ := newService(t)
	if _, err := service.Record(context.Background(), contracts.TeamNote{
		Title: "私事", Scope: contracts.NoteScopeUser,
	}); !errors.Is(err, contracts.ErrTeamNoteInvalid) {
		t.Fatalf("err = %v, want ErrTeamNoteInvalid", err)
	}
}
