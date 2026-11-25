package teamnotes_test

// The suggestion queue (M1): a bot proposes a rule, nothing reaches the shared
// board until a human promotes it.

import (
	"context"
	"errors"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestSuggestionsStayOffTheBoardUntilPromoted(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()

	suggestion, err := service.Suggest(ctx, contracts.TeamNoteSuggestion{
		Title: "退款单超过 3 天必须人工复核", Body: "经验：自动通过时出过一次错。", Author: "bot-1",
	})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	board, err := service.List(ctx, 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(board) != 0 {
		t.Fatalf("a suggestion reached the board before approval: %+v", board)
	}

	queued, err := service.Suggestions(ctx, 10)
	if err != nil || len(queued) != 1 {
		t.Fatalf("Suggestions = (%d, %v), want one", len(queued), err)
	}
	if queued[0].Title != "退款单超过 3 天必须人工复核" || queued[0].Author != "bot-1" {
		t.Fatalf("queued = %+v", queued[0])
	}

	note, err := service.Promote(ctx, suggestion.ID, "lead")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if note.Title != queued[0].Title {
		t.Fatalf("note = %+v", note)
	}
	board, _ = service.List(ctx, 50)
	if len(board) != 1 {
		t.Fatalf("board = %d notes, want one after promotion", len(board))
	}
	queued, _ = service.Suggestions(ctx, 10)
	if len(queued) != 0 {
		t.Fatalf("the promoted suggestion is still queued: %+v", queued)
	}
}

func TestDismissRemovesTheProposalWithoutANote(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()
	suggestion, err := service.Suggest(ctx, contracts.TeamNoteSuggestion{Title: "一个坏主意", Author: "bot-1"})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if err := service.Dismiss(ctx, suggestion.ID, "lead"); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	if board, _ := service.List(ctx, 10); len(board) != 0 {
		t.Fatalf("dismissing created a note: %+v", board)
	}
	if queued, _ := service.Suggestions(ctx, 10); len(queued) != 0 {
		t.Fatalf("the dismissed suggestion is still queued: %+v", queued)
	}
}

func TestPromoteRequiresAReviewer(t *testing.T) {
	service, _ := newService(t)
	suggestion, _ := service.Suggest(context.Background(), contracts.TeamNoteSuggestion{Title: "规则"})
	if _, err := service.Promote(context.Background(), suggestion.ID, "  "); !errors.Is(err, contracts.ErrTeamNoteInvalid) {
		t.Fatalf("Promote(no reviewer) = %v, want ErrTeamNoteInvalid", err)
	}
}

func TestUnknownSuggestionIsTyped(t *testing.T) {
	service, _ := newService(t)
	if err := service.Dismiss(context.Background(), "mem://user/self/memories/team-suggestion/nope", "lead"); !errors.Is(err, contracts.ErrTeamNoteSuggestionUnknown) {
		t.Fatalf("Dismiss(unknown) = %v, want ErrTeamNoteSuggestionUnknown", err)
	}
}
