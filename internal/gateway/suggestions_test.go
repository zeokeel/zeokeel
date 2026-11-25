package gateway

// The suggestion queue over the wire: a proposal is invisible to the board until
// a human promotes it, and dismissal creates nothing.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakeSuggestions struct {
	mu    sync.Mutex
	queue []contracts.TeamNoteSuggestion
	notes []contracts.TeamNote
	err   error
}

func (f *fakeSuggestions) Suggest(_ context.Context, suggestion contracts.TeamNoteSuggestion) (contracts.TeamNoteSuggestion, error) {
	if f.err != nil {
		return contracts.TeamNoteSuggestion{}, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	suggestion.ID = "sug-" + suggestion.Title
	suggestion.At = time.Unix(0, 0).UTC()
	f.queue = append(f.queue, suggestion)
	return suggestion, nil
}

func (f *fakeSuggestions) Suggestions(context.Context, int) ([]contracts.TeamNoteSuggestion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.TeamNoteSuggestion(nil), f.queue...), nil
}

func (f *fakeSuggestions) Promote(_ context.Context, id, reviewer string) (contracts.TeamNote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, suggestion := range f.queue {
		if suggestion.ID != id {
			continue
		}
		f.queue = append(f.queue[:i], f.queue[i+1:]...)
		note := contracts.TeamNote{ID: "note-1", Title: suggestion.Title, Body: suggestion.Body, Author: suggestion.Author}
		f.notes = append(f.notes, note)
		return note, nil
	}
	return contracts.TeamNote{}, contracts.ErrTeamNoteSuggestionUnknown
}

func (f *fakeSuggestions) Dismiss(_ context.Context, id, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, suggestion := range f.queue {
		if suggestion.ID == id {
			f.queue = append(f.queue[:i], f.queue[i+1:]...)
			return nil
		}
	}
	return contracts.ErrTeamNoteSuggestionUnknown
}

func newSuggestionGateway(t *testing.T) (*Gateway, *fakeSuggestions) {
	t.Helper()
	root := testRoot(t, newFakeChat())
	store := &fakeSuggestions{}
	if _, err := root.Provide(contracts.KeyTeamNoteSuggestions, contracts.TeamNoteSuggestions(store)); err != nil {
		t.Fatalf("provide suggestions: %v", err)
	}
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return gateway, store
}

func TestSuggestionLifecycleOverTheWire(t *testing.T) {
	gateway, store := newSuggestionGateway(t)
	ctx := context.Background()

	raw, _ := json.Marshal(map[string]any{"title": "退款单必须人工复核", "author": "bot-1"})
	if _, problem := gateway.noteSuggest(ctx, raw); problem != nil {
		t.Fatalf("suggest: %+v", problem)
	}
	listed, problem := gateway.noteSuggestions(ctx, nil)
	if problem != nil {
		t.Fatalf("list: %+v", problem)
	}
	result, ok := listed.(noteSuggestionsResult)
	if !ok || len(result.Suggestions) != 1 || result.Suggestions[0].Title == "" {
		t.Fatalf("listed = %+v", listed)
	}

	promote, _ := json.Marshal(map[string]any{"id": result.Suggestions[0].ID, "reviewer": "lead"})
	promoted, problem := gateway.noteSuggestionPromote(ctx, promote)
	if problem != nil {
		t.Fatalf("promote: %+v", problem)
	}
	if note, ok := promoted.(teamNoteDTO); !ok || note.Title != "退款单必须人工复核" {
		t.Fatalf("promoted = %+v", promoted)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.queue) != 0 || len(store.notes) != 1 {
		t.Fatalf("queue=%d notes=%d, want the proposal answered and one note", len(store.queue), len(store.notes))
	}
}

func TestDismissIsWireVisible(t *testing.T) {
	gateway, store := newSuggestionGateway(t)
	ctx := context.Background()
	raw, _ := json.Marshal(map[string]any{"title": "一个坏主意"})
	if _, problem := gateway.noteSuggest(ctx, raw); problem != nil {
		t.Fatalf("suggest: %+v", problem)
	}
	dismiss, _ := json.Marshal(map[string]any{"id": "sug-一个坏主意", "reviewer": "lead"})
	if _, problem := gateway.noteSuggestionDismiss(ctx, dismiss); problem != nil {
		t.Fatalf("dismiss: %+v", problem)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.queue) != 0 || len(store.notes) != 0 {
		t.Fatalf("queue=%d notes=%d, want nothing left", len(store.queue), len(store.notes))
	}
}
