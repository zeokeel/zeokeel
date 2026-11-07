package uiroot

// Host interaction bridge: Ask parks until Answer/Cancel/ctx end; the handler
// receives the question and UI modules resolve it by id.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type askOutcome struct {
	answer string
	err    error
}

// startAsk invokes Ask in a goroutine and returns its result channel.
func startAsk(service *interactionService, ctx context.Context, text string, options []string) <-chan askOutcome {
	out := make(chan askOutcome, 1)
	go func() {
		answer, err := service.Ask(ctx, text, options)
		out <- askOutcome{answer: answer, err: err}
	}()
	return out
}

func TestInteractionAskUntilAnswer(t *testing.T) {
	root := newTestRoot(t)
	service := root.UserInteraction()
	questions := make(chan contracts.UserQuestion, 1)
	service.SetHandler(func(question contracts.UserQuestion) { questions <- question })

	out := startAsk(service, context.Background(), "Which database?", []string{"sqlite", "postgres"})
	question := <-questions
	if question.ID == "" {
		t.Fatal("question ID is empty")
	}
	if question.Text != "Which database?" {
		t.Fatalf("question text = %q", question.Text)
	}
	if len(question.Options) != 2 || question.Options[0] != "sqlite" || question.Options[1] != "postgres" {
		t.Fatalf("question options = %v", question.Options)
	}

	// The pending map is keyed by ID: the wrong ID must not resolve it.
	service.Answer("question-99", "bogus")
	select {
	case outcome := <-out:
		t.Fatalf("Ask resolved by an unknown ID: %+v", outcome)
	default:
	}
	service.Answer(question.ID, "postgres")
	outcome := <-out
	if outcome.err != nil || outcome.answer != "postgres" {
		t.Fatalf("Ask outcome = %+v, want postgres/nil", outcome)
	}
	// Duplicate and unknown resolutions are no-ops.
	service.Answer(question.ID, "again")
	service.Cancel(question.ID)
}

func TestInteractionHandlerMayAnswerSynchronously(t *testing.T) {
	root := newTestRoot(t)
	service := root.UserInteraction()
	service.SetHandler(func(question contracts.UserQuestion) {
		service.Answer(question.ID, "sync")
	})
	if answer, err := service.Ask(context.Background(), "q", nil); err != nil || answer != "sync" {
		t.Fatalf("Ask = %q, %v; want sync/nil", answer, err)
	}
}

func TestInteractionCancel(t *testing.T) {
	root := newTestRoot(t)
	service := root.UserInteraction()
	questions := make(chan contracts.UserQuestion, 1)
	service.SetHandler(func(question contracts.UserQuestion) { questions <- question })

	out := startAsk(service, context.Background(), "q", nil)
	question := <-questions
	service.Cancel(question.ID)
	outcome := <-out
	if !errors.Is(outcome.err, ErrUserCancelled) {
		t.Fatalf("Cancel outcome = %+v, want ErrUserCancelled", outcome)
	}
	if outcome.err.Error() != "用户取消" {
		t.Fatalf("Cancel message = %q", outcome.err.Error())
	}
	// A cancelled ID is no longer registered.
	service.Answer(question.ID, "late")
	service.Cancel("unknown")
}

func TestInteractionContextCancellation(t *testing.T) {
	root := newTestRoot(t)
	service := root.UserInteraction()
	questions := make(chan contracts.UserQuestion, 1)
	service.SetHandler(func(question contracts.UserQuestion) { questions <- question })

	ctx, cancel := context.WithCancel(context.Background())
	out := startAsk(service, ctx, "q", nil)
	question := <-questions
	cancel()
	outcome := <-out
	if !errors.Is(outcome.err, context.Canceled) {
		t.Fatalf("Ask after ctx cancel = %+v, want context.Canceled", outcome)
	}
	// The cancelled request must be removed so a late Answer is a no-op.
	service.Answer(question.ID, "late")

	preCtx, preCancel := context.WithCancel(context.Background())
	preCancel()
	if _, err := service.Ask(preCtx, "q", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Ask(already cancelled) = %v, want context.Canceled", err)
	}
}

func TestInteractionWithoutHandler(t *testing.T) {
	root := newTestRoot(t)
	service := root.UserInteraction()
	if _, err := service.Ask(context.Background(), "q", nil); !errors.Is(err, ErrInteractiveUnavailable) {
		t.Fatalf("Ask without handler = %v, want ErrInteractiveUnavailable", err)
	}
	service.SetHandler(func(contracts.UserQuestion) {})
	service.SetHandler(nil)
	if _, err := service.Ask(context.Background(), "q", nil); !errors.Is(err, ErrInteractiveUnavailable) {
		t.Fatalf("Ask after SetHandler(nil) = %v, want ErrInteractiveUnavailable", err)
	}
}

func TestInteractionNilContextUsesBackground(t *testing.T) {
	root := newTestRoot(t)
	service := root.UserInteraction()
	service.SetHandler(func(question contracts.UserQuestion) { service.Answer(question.ID, "ok") })
	if answer, err := service.Ask(nil, "q", nil); err != nil || answer != "ok" {
		t.Fatalf("Ask(nil ctx) = %q, %v", answer, err)
	}
}

func TestInteractionConcurrentQuestions(t *testing.T) {
	root := newTestRoot(t)
	service := root.UserInteraction()
	const count = 3
	questions := make(chan contracts.UserQuestion, count)
	service.SetHandler(func(question contracts.UserQuestion) { questions <- question })

	var wg sync.WaitGroup
	outcomes := make(chan askOutcome, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			answer, err := service.Ask(context.Background(), "q", nil)
			outcomes <- askOutcome{answer: answer, err: err}
		}()
	}
	ids := make(map[string]bool, count)
	for i := 0; i < count; i++ {
		question := <-questions
		if question.ID == "" || ids[question.ID] {
			t.Fatalf("question id %q is empty or duplicated", question.ID)
		}
		ids[question.ID] = true
		service.Answer(question.ID, "answer-"+question.ID)
	}
	wg.Wait()
	close(outcomes)
	seen := make(map[string]bool, count)
	for outcome := range outcomes {
		if outcome.err != nil || outcome.answer == "" {
			t.Fatalf("outcome = %+v", outcome)
		}
		if seen[outcome.answer] {
			t.Fatalf("duplicate answer %q", outcome.answer)
		}
		seen[outcome.answer] = true
	}
}

func TestInteractionConcurrentAnswerCancel(t *testing.T) {
	root := newTestRoot(t)
	service := root.UserInteraction()
	questions := make(chan contracts.UserQuestion, 1)
	service.SetHandler(func(question contracts.UserQuestion) { questions <- question })
	out := startAsk(service, context.Background(), "q", nil)
	question := <-questions

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); service.Answer(question.ID, "answer") }()
	go func() { defer wg.Done(); service.Cancel(question.ID) }()
	wg.Wait()
	outcome := <-out
	if outcome.err != nil && !errors.Is(outcome.err, ErrUserCancelled) {
		t.Fatalf("outcome = %+v", outcome)
	}
	if outcome.err == nil && outcome.answer != "answer" {
		t.Fatalf("outcome = %+v", outcome)
	}
}
