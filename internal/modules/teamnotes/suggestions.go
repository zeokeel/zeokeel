package teamnotes

// The suggestion queue (M1): proposals live apart from the shared board.
//
// Storage mirrors the board's own trick — a distinct memory type and subtree —
// so a board listing cannot see a suggestion by accident. Promotion is what puts
// a rule on the board, and it goes through Record so the resulting note is an
// ordinary note with an author and a review state.

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// SuggestionType is the memory type proposals are stored under.
const SuggestionType = "team-suggestion"

// SuggestionURIPrefix is where proposals live.
const SuggestionURIPrefix = "mem://user/self/memories/team-suggestion/"

// Suggest implements contracts.TeamNoteSuggestions.
func (s *Service) Suggest(ctx context.Context, suggestion contracts.TeamNoteSuggestion) (contracts.TeamNoteSuggestion, error) {
	if s == nil || s.memory == nil {
		return contracts.TeamNoteSuggestion{}, contracts.ErrTeamNotesUnavailable
	}
	suggestion.Title = strings.TrimSpace(suggestion.Title)
	suggestion.Author = strings.TrimSpace(suggestion.Author)
	suggestion.Body = strings.ReplaceAll(suggestion.Body, "\r\n", "\n")
	switch {
	case suggestion.Title == "":
		return contracts.TeamNoteSuggestion{}, invalid("suggestion title is empty")
	case utf8.RuneCountInString(suggestion.Title) > MaxTitleRunes:
		return contracts.TeamNoteSuggestion{}, invalid("suggestion title is %d runes, limit %d", utf8.RuneCountInString(suggestion.Title), MaxTitleRunes)
	case len(suggestion.Title) > MaxTitleBytes:
		return contracts.TeamNoteSuggestion{}, invalid("suggestion title is %d bytes, memory store limit %d", len(suggestion.Title), MaxTitleBytes)
	case utf8.RuneCountInString(suggestion.Body) > MaxBodyRunes:
		return contracts.TeamNoteSuggestion{}, invalid("suggestion body is too long")
	}
	if suggestion.At.IsZero() {
		suggestion.At = s.now()
	}
	suggestion.At = suggestion.At.UTC().Truncate(time.Second)
	suggestion.ID = SuggestionURIPrefix + generatedID(contracts.TeamNote{Title: suggestion.Title, At: suggestion.At})

	// The content uses the board's own metadata block, so a promoted proposal
	// parses back with its author instead of arriving anonymous.
	meta := fmt.Sprintf("- Task: %s\n- Author: %s\n- At: %s", "", suggestion.Author, suggestion.At.Format(time.RFC3339))
	content := "# " + suggestion.Title + "\n\n" + meta + "\n"
	if suggestion.Body != "" {
		content += "\n" + suggestion.Body
		if !strings.HasSuffix(suggestion.Body, "\n") {
			content += "\n"
		}
	}
	if _, err := s.memory.Write(ctx, s.root, contracts.MemoryDraft{
		URI:      suggestion.ID,
		Type:     SuggestionType,
		Title:    suggestion.Title,
		Summary:  oneLineSummary(suggestion.Body),
		Overview: "Proposed by " + authorOrUnknown(suggestion.Author) + " on " + suggestion.At.Format(time.RFC3339),
		Content:  content,
	}); err != nil {
		return contracts.TeamNoteSuggestion{}, fmt.Errorf("teamnotes: write suggestion %q: %w", suggestion.ID, err)
	}
	return suggestion, nil
}

// Suggestions implements contracts.TeamNoteSuggestions.
func (s *Service) Suggestions(ctx context.Context, limit int) ([]contracts.TeamNoteSuggestion, error) {
	if s == nil || s.memory == nil {
		return nil, contracts.ErrTeamNotesUnavailable
	}
	items, err := s.memory.List(ctx, s.root, "", []string{SuggestionType}, limit)
	if err != nil {
		return nil, fmt.Errorf("teamnotes: list suggestions: %w", err)
	}
	out := make([]contracts.TeamNoteSuggestion, 0, len(items))
	for _, item := range items {
		suggestion := contracts.TeamNoteSuggestion{ID: item.URI, Title: item.Title, At: item.Updated}
		if detail, err := s.memory.Get(ctx, s.root, item.URI); err == nil {
			parsed := parseContent(detail.Content)
			suggestion.Title = parsed.Title
			suggestion.Body = parsed.Body
			suggestion.Author = parsed.Author
			if !parsed.At.IsZero() {
				suggestion.At = parsed.At
			}
		}
		out = append(out, suggestion)
	}
	return out, nil
}

// Promote implements contracts.TeamNoteSuggestions.
func (s *Service) Promote(ctx context.Context, id, reviewer string) (contracts.TeamNote, error) {
	reviewer = strings.TrimSpace(reviewer)
	if reviewer == "" {
		return contracts.TeamNote{}, invalid("reviewer is empty")
	}
	suggestion, err := s.suggestion(ctx, id)
	if err != nil {
		return contracts.TeamNote{}, err
	}
	note, err := s.Record(ctx, contracts.TeamNote{
		Title:  suggestion.Title,
		Body:   suggestion.Body,
		Author: suggestion.Author,
	})
	if err != nil {
		return contracts.TeamNote{}, err
	}
	// The rule is on the board now; the proposal is answered. A failure to
	// remove it leaves a duplicate proposal, which is visible and harmless —
	// losing the note would not be.
	if err := s.memory.Delete(ctx, s.root, suggestion.ID); err != nil {
		return note, nil
	}
	return note, nil
}

// Dismiss implements contracts.TeamNoteSuggestions.
func (s *Service) Dismiss(ctx context.Context, id, reviewer string) error {
	if strings.TrimSpace(reviewer) == "" {
		return invalid("reviewer is empty")
	}
	suggestion, err := s.suggestion(ctx, id)
	if err != nil {
		return err
	}
	if err := s.memory.Delete(ctx, s.root, suggestion.ID); err != nil {
		return fmt.Errorf("teamnotes: dismiss suggestion %q: %w", id, err)
	}
	return nil
}

func (s *Service) suggestion(ctx context.Context, id string) (contracts.TeamNoteSuggestion, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return contracts.TeamNoteSuggestion{}, invalid("suggestion id is empty")
	}
	detail, err := s.memory.Get(ctx, s.root, id)
	if err != nil {
		return contracts.TeamNoteSuggestion{}, fmt.Errorf("%w: %v", contracts.ErrTeamNoteSuggestionUnknown, err)
	}
	if detail.Type != "" && detail.Type != SuggestionType {
		return contracts.TeamNoteSuggestion{}, contracts.ErrTeamNoteSuggestionUnknown
	}
	parsed := parseContent(detail.Content)
	return contracts.TeamNoteSuggestion{
		ID: id, Title: parsed.Title, Body: parsed.Body, Author: parsed.Author, At: parsed.At,
	}, nil
}

func authorOrUnknown(author string) string {
	if strings.TrimSpace(author) == "" {
		return "unknown"
	}
	return author
}

// oneLineSummary is the list-row text: the first line, bounded by the same rune
// and byte budgets the board uses (the store rejects an oversized summary).
func oneLineSummary(body string) string {
	first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(body), "\n", 2)[0])
	if first == "" {
		return "（没有说明）"
	}
	return truncate(first, 120, 300)
}

var _ contracts.TeamNoteSuggestions = (*Service)(nil)
