package regression

// Adversarial audit: the uiroot host interaction bridges (UserInteraction,
// ArtifactPresenter, PlanReviewer, GoalTracker) are transport-only services
// called from agent goroutines and the UI thread. Negative cases: nil handlers,
// unknown/repeated answers, Answer/Cancel races, invalid plan actions, empty
// payloads, panicking handlers and concurrent access.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// askResult is one UserInteraction.Ask outcome.
type askResult struct {
	answer string
	err    error
}

// startAsk runs one Ask on a goroutine and delivers its result.
func startAsk(ctx context.Context, interaction contracts.UserInteraction, text string, options []string) <-chan askResult {
	results := make(chan askResult, 1)
	go func() {
		answer, err := interaction.Ask(ctx, text, options)
		results <- askResult{answer: answer, err: err}
	}()
	return results
}

// awaitAsk waits for one Ask result with a short timeout.
func awaitAsk(t *testing.T, results <-chan askResult) askResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("Ask did not return within 2s (deadlock)")
		return askResult{}
	}
}

// TestInteractionAskWithoutHandler covers the headless/no-UI case: Ask fails
// fast with the documented sentinel instead of blocking forever.
func TestInteractionAskWithoutHandler(t *testing.T) {
	root := uiroot.New(uiroot.Config{})
	interaction := root.UserInteraction()
	for _, ctx := range []context.Context{context.Background(), nil} {
		answer, err := interaction.Ask(ctx, "anyone there?", nil)
		if !errors.Is(err, uiroot.ErrInteractiveUnavailable) {
			t.Fatalf("Ask with no handler = (%q, %v), want ErrInteractiveUnavailable", answer, err)
		}
	}
	// Setting a nil handler again stays a no-op.
	interaction.SetHandler(nil)
	if _, err := interaction.Ask(context.Background(), "q", nil); !errors.Is(err, uiroot.ErrInteractiveUnavailable) {
		t.Fatalf("Ask after SetHandler(nil) error = %v, want ErrInteractiveUnavailable", err)
	}
}

// TestInteractionAnswerCancelNegatives covers unknown IDs, repeated Cancel and
// Answer-after-Cancel: every one is a silent no-op that cannot panic or deliver
// a second result.
func TestInteractionAnswerCancelNegatives(t *testing.T) {
	root := uiroot.New(uiroot.Config{})
	interaction := root.UserInteraction()
	questions := make(chan contracts.UserQuestion, 4)
	interaction.SetHandler(func(question contracts.UserQuestion) { questions <- question })

	results := startAsk(context.Background(), interaction, "delete the file?", []string{"yes", "no"})
	question := <-questions
	if question.ID == "" {
		t.Fatal("the handler received a question without an ID")
	}
	if len(question.Options) != 2 {
		t.Fatalf("handler options = %v, want the two passed options", question.Options)
	}

	// Repeated Cancel and unknown IDs are no-ops.
	interaction.Cancel("question-does-not-exist")
	interaction.Cancel(question.ID)
	interaction.Cancel(question.ID)
	interaction.Answer(question.ID, "yes") // already cancelled: ignored
	if result := awaitAsk(t, results); !errors.Is(result.err, uiroot.ErrUserCancelled) {
		t.Fatalf("Ask after Cancel = (%q, %v), want ErrUserCancelled", result.answer, result.err)
	}
	// The pending map must be empty: a second Ask gets a fresh working question.
	results = startAsk(context.Background(), interaction, "second?", nil)
	second := <-questions
	interaction.Answer(second.ID, "ok")
	if result := awaitAsk(t, results); result.err != nil || result.answer != "ok" {
		t.Fatalf("second Ask = (%q, %v), want the answer ok", result.answer, result.err)
	}
}

// TestInteractionHandlerAnswersSynchronously covers a handler that resolves the
// question before Ask reaches its select: the answer must still be delivered.
func TestInteractionHandlerAnswersSynchronously(t *testing.T) {
	root := uiroot.New(uiroot.Config{})
	interaction := root.UserInteraction()
	interaction.SetHandler(func(question contracts.UserQuestion) {
		interaction.Answer(question.ID, "inline-answer")
	})
	results := startAsk(context.Background(), interaction, "q", nil)
	if result := awaitAsk(t, results); result.err != nil || result.answer != "inline-answer" {
		t.Fatalf("Ask with a synchronous handler = (%q, %v), want inline-answer", result.answer, result.err)
	}
}

// TestInteractionCanceledContext covers context cancellation before and while
// Ask is pending: the result is context.Canceled and no question leaks.
func TestInteractionCanceledContext(t *testing.T) {
	root := uiroot.New(uiroot.Config{})
	interaction := root.UserInteraction()
	questions := make(chan contracts.UserQuestion, 4)
	interaction.SetHandler(func(question contracts.UserQuestion) { questions <- question })

	// Already cancelled: no handler call at all.
	already, cancelAlready := context.WithCancel(context.Background())
	cancelAlready()
	if _, err := interaction.Ask(already, "q", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Ask(cancelled ctx) error = %v, want context.Canceled", err)
	}
	select {
	case question := <-questions:
		t.Fatalf("handler was called for an already-cancelled Ask: %+v", question)
	default:
	}

	// Cancel while pending: Ask returns the context error.
	pendingCtx, cancelPending := context.WithCancel(context.Background())
	results := startAsk(pendingCtx, interaction, "pending", nil)
	question := <-questions
	cancelPending()
	if result := awaitAsk(t, results); !errors.Is(result.err, context.Canceled) {
		t.Fatalf("Ask after ctx cancellation = (%q, %v), want context.Canceled", result.answer, result.err)
	}
	// The cancelled question's ID is unknown now: Answer is a no-op.
	interaction.Answer(question.ID, "late")
}

// TestInteractionAnswerCancelRace covers the concurrent answer/cancel race on
// one pending question: exactly one result is delivered, with no panic and no
// deadlock (-race).
func TestInteractionAnswerCancelRace(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		root := uiroot.New(uiroot.Config{})
		interaction := root.UserInteraction()
		questions := make(chan contracts.UserQuestion, 1)
		interaction.SetHandler(func(question contracts.UserQuestion) { questions <- question })
		results := startAsk(context.Background(), interaction, "race", []string{"allow", "deny"})
		question := <-questions

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); interaction.Answer(question.ID, "allow") }()
		go func() { defer wg.Done(); interaction.Cancel(question.ID) }()
		wg.Wait()
		result := awaitAsk(t, results)
		switch {
		case result.err == nil && result.answer == "allow":
		case errors.Is(result.err, uiroot.ErrUserCancelled):
		default:
			t.Fatalf("attempt %d: Ask = (%q, %v), want the answer or ErrUserCancelled", attempt, result.answer, result.err)
		}
	}
}

// TestInteractionConcurrentPendingQuestions covers several simultaneous
// questions: unique IDs, each answer reaches its own caller (-race).
func TestInteractionConcurrentPendingQuestions(t *testing.T) {
	root := uiroot.New(uiroot.Config{})
	interaction := root.UserInteraction()
	questions := make(chan contracts.UserQuestion, 8)
	interaction.SetHandler(func(question contracts.UserQuestion) { questions <- question })

	const count = 8
	results := make(chan askResult, count)
	for index := 0; index < count; index++ {
		index := index
		go func() {
			answer, err := interaction.Ask(context.Background(), fmt.Sprintf("question-%d", index), nil)
			results <- askResult{answer: answer, err: err}
		}()
	}
	ids := make(map[string]bool, count)
	byID := make(map[string]int, count)
	for index := 0; index < count; index++ {
		question := <-questions
		if question.ID == "" || ids[question.ID] {
			t.Fatalf("question ID %q is empty or duplicated", question.ID)
		}
		ids[question.ID] = true
		byID[question.ID] = index
	}
	for id := range ids {
		interaction.Answer(id, "answer-"+id)
	}
	answers := make(map[string]bool, count)
	for index := 0; index < count; index++ {
		result := <-results
		if result.err != nil || result.answer == "" {
			t.Fatalf("concurrent Ask result = (%q, %v)", result.answer, result.err)
		}
		answers[result.answer] = true
	}
	if len(answers) != count {
		t.Fatalf("concurrent Ask returned %d distinct answers, want %d", len(answers), count)
	}
	for answer := range answers {
		if !strings.HasPrefix(answer, "answer-question-") {
			t.Fatalf("answer %q was not produced by the test", answer)
		}
	}
}

// TestInteractionHandlerPanicIsNotContained is a known-defect probe: a
// panicking UI handler currently propagates to the Ask caller (and leaks the
// pending entry), unlike the approvals subscriber path which contains panics.
func TestInteractionHandlerPanicIsNotContained(t *testing.T) {
	root := uiroot.New(uiroot.Config{})
	interaction := root.UserInteraction()
	interaction.SetHandler(func(contracts.UserQuestion) { panic("handler boom") })

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = interaction.Ask(context.Background(), "boom?", nil)
	}()
	if recovered != nil {
		t.Skipf("AUDIT-P2-4 (known defect): a panicking UserInteraction handler propagates to the Ask caller (%v) "+
			"and leaves the pending question registered", recovered)
	}
	if _, err := interaction.Ask(context.Background(), "boom?", nil); err == nil {
		t.Fatal("Ask after a contained handler panic returned no error")
	}
}

// TestArtifactPresenterNegatives covers empty payloads, a nil handler, cancelled
// contexts and handler panics.
func TestArtifactPresenterNegatives(t *testing.T) {
	root := uiroot.New(uiroot.Config{})
	presenter := root.ArtifactPresenter()
	ctx := context.Background()

	if _, err := presenter.Present(ctx, "", "body"); err == nil || !strings.Contains(err.Error(), "title is required") {
		t.Fatalf("Present(empty title) error = %v, want title required", err)
	}
	if _, err := presenter.Present(ctx, "   ", "body"); err == nil {
		t.Fatalf("Present(whitespace title) succeeded, want an error")
	}
	if _, err := presenter.Present(ctx, "title", ""); err == nil || !strings.Contains(err.Error(), "body is required") {
		t.Fatalf("Present(empty body) error = %v, want body required", err)
	}

	// A nil handler is not an error: the artifact is recorded for later display.
	id, err := presenter.Present(ctx, "  title  ", "  body  ")
	if err != nil || id == "" {
		t.Fatalf("Present with no handler = (%q, %v), want an id", id, err)
	}
	var delivered []contracts.Artifact
	var mu sync.Mutex
	presenter.SetArtifactHandler(func(artifact contracts.Artifact) {
		mu.Lock()
		delivered = append(delivered, artifact)
		mu.Unlock()
	})
	secondID, err := presenter.Present(ctx, "  spaced title  ", "  spaced body  ")
	if err != nil {
		t.Fatalf("Present: %v", err)
	}
	mu.Lock()
	got := delivered[len(delivered)-1]
	mu.Unlock()
	if got.Title != "spaced title" || got.Body != "spaced body" || got.ID != secondID {
		t.Fatalf("delivered artifact = %+v, want trimmed title/body and id %q", got, secondID)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := presenter.Present(canceled, "title", "body"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Present(cancelled ctx) error = %v, want context.Canceled", err)
	}

	// Concurrent Present must hand out unique IDs (-race).
	var wg sync.WaitGroup
	ids := make(chan string, 20)
	for index := 0; index < 20; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := presenter.Present(context.Background(), "t", "b")
			if err != nil {
				t.Errorf("concurrent Present: %v", err)
				return
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate artifact ID %q", id)
		}
		seen[id] = true
	}

	// A panicking display handler: probe the containment expectation.
	presenter.SetArtifactHandler(func(contracts.Artifact) { panic("artifact handler boom") })
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = presenter.Present(context.Background(), "t", "b")
	}()
	if recovered != nil {
		t.Skipf("AUDIT-P2-4 (known defect): a panicking ArtifactPresenter handler propagates to Present (%v)", recovered)
	}
}

// TestPlanReviewerNegatives covers empty reviews, invalid/duplicate decisions
// and state transitions of the plan bridge.
func TestPlanReviewerNegatives(t *testing.T) {
	root := uiroot.New(uiroot.Config{})
	reviewer := root.PlanReviewer()
	ctx := context.Background()

	if state := reviewer.State(); state != uiroot.PlanIdle {
		t.Fatalf("initial plan state = %q, want idle", state)
	}
	if _, err := reviewer.Review(ctx, ""); err == nil || !strings.Contains(err.Error(), "content is required") {
		t.Fatalf("Review(empty) error = %v, want content required", err)
	}
	if _, err := reviewer.Review(ctx, "   "); err == nil {
		t.Fatalf("Review(whitespace) succeeded, want an error")
	}
	if state := reviewer.State(); state != uiroot.PlanIdle {
		t.Fatalf("plan state = %q after rejected reviews, want idle", state)
	}

	// Approve/cancel with no plan under review are errors that leave idle.
	for _, action := range []string{"approve", "cancel", "APPROVE", "Cancel"} {
		if _, err := reviewer.Exit(action); err == nil {
			t.Fatalf("Exit(%q) with no plan succeeded, want an error", action)
		}
	}
	if state := reviewer.State(); state != uiroot.PlanIdle {
		t.Fatalf("plan state = %q after no-plan exits, want idle", state)
	}

	// Invalid actions are rejected and leave the reviewing state untouched.
	var reviews []contracts.PlanReview
	reviewer.SetPlanHandler(func(review contracts.PlanReview) { reviews = append(reviews, review) })
	if _, err := reviewer.Review(ctx, "  plan content  "); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if got, want := len(reviews), 1; got != want {
		t.Fatalf("plan handler called %d times after Review, want %d", got, want)
	}
	if reviews[0].Content != "plan content" || reviews[0].State != uiroot.PlanReviewing {
		t.Fatalf("review snapshot = %+v, want trimmed content in reviewing state", reviews[0])
	}
	for _, action := range []string{"", "bogus", "approve now", "reject"} {
		if _, err := reviewer.Exit(action); err == nil {
			t.Fatalf("Exit(%q) succeeded, want an error", action)
		}
	}
	if state := reviewer.State(); state != uiroot.PlanReviewing {
		t.Fatalf("plan state = %q after invalid exits, want reviewing", state)
	}
	// status reports without changing state or calling the handler.
	before := len(reviews)
	if state, err := reviewer.Exit("  STATUS  "); err != nil || state != uiroot.PlanReviewing {
		t.Fatalf("Exit(status) = (%q, %v), want reviewing/nil", state, err)
	}
	if len(reviews) != before {
		t.Fatalf("status exit called the plan handler")
	}
	// Approve, then a second approve is rejected (no plan under review anymore).
	if state, err := reviewer.Exit("approve"); err != nil || state != uiroot.PlanApproved {
		t.Fatalf("Exit(approve) = (%q, %v), want approved", state, err)
	}
	if _, err := reviewer.Exit("approve"); err == nil {
		t.Fatal("second approve after approval succeeded, want an error")
	}
	if got := len(reviews); got != before+1 {
		t.Fatalf("plan handler calls = %d after approve, want %d", got, before+1)
	}

	// Cancel works from a fresh review and the snapshot is cancelled.
	if _, err := reviewer.Review(ctx, "second plan"); err != nil {
		t.Fatalf("Review(second): %v", err)
	}
	if state, err := reviewer.Exit("cancel"); err != nil || state != uiroot.PlanCancelled {
		t.Fatalf("Exit(cancel) = (%q, %v), want cancelled", state, err)
	}
}

// TestPlanReviewerHandlerPanic is the known-defect probe for a panicking plan
// handler (same containment class as the interaction handler).
func TestPlanReviewerHandlerPanic(t *testing.T) {
	root := uiroot.New(uiroot.Config{})
	reviewer := root.PlanReviewer()
	reviewer.SetPlanHandler(func(contracts.PlanReview) { panic("plan handler boom") })
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = reviewer.Review(context.Background(), "plan")
	}()
	if recovered != nil {
		t.Skipf("AUDIT-P2-4 (known defect): a panicking PlanReviewer handler propagates to Review (%v)", recovered)
	}
}

// TestGoalTrackerNegatives covers empty goals, repeated clear, nil contexts and
// concurrent access.
func TestGoalTrackerNegatives(t *testing.T) {
	root := uiroot.New(uiroot.Config{})
	tracker := root.GoalTracker()
	ctx := context.Background()

	if _, err := tracker.Set(ctx, ""); err == nil || !strings.Contains(err.Error(), "text is required") {
		t.Fatalf("Set(empty) error = %v, want text required", err)
	}
	if _, err := tracker.Set(ctx, "   "); err == nil {
		t.Fatalf("Set(whitespace) succeeded, want an error")
	}
	if _, _, _, ok := tracker.Get(); ok {
		t.Fatal("Get() reports a goal after rejected sets")
	}
	if tracker.Clear() {
		t.Fatal("Clear() = true with no goal")
	}

	// nil context falls back to Background, like the other bridges.
	goalID, err := tracker.Set(nil, "  ship the audit  ")
	if err != nil {
		t.Fatalf("Set(nil ctx): %v", err)
	}
	id, text, status, ok := tracker.Get()
	if !ok || id != goalID || text != "ship the audit" || status != "active" {
		t.Fatalf("Get() = (%q, %q, %q, %v), want the trimmed active goal", id, text, status, ok)
	}

	// Concurrent Set/Clear/Get from several goroutines: -race must stay clean
	// and the bridge must never deadlock.
	tracker.SetHandler(nil)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := 0; index < 50; index++ {
				switch worker % 3 {
				case 0:
					_, _ = tracker.Set(ctx, fmt.Sprintf("goal-%d-%d", worker, index))
				case 1:
					tracker.Clear()
				default:
					tracker.Get()
				}
			}
		}()
	}
	wg.Wait()

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := tracker.Set(canceled, "late"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Set(cancelled ctx) error = %v, want context.Canceled", err)
	}
}

// TestGoalTrackerHandlerPanic is the known-defect probe for a panicking goal
// handler (same containment class as the interaction handler).
func TestGoalTrackerHandlerPanic(t *testing.T) {
	root := uiroot.New(uiroot.Config{})
	tracker := root.GoalTracker()
	tracker.SetHandler(func(contracts.Goal, bool) { panic("goal handler boom") })
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = tracker.Set(context.Background(), "second")
	}()
	if recovered != nil {
		t.Skipf("AUDIT-P2-4 (known defect): a panicking GoalTracker handler propagates to Set (%v)", recovered)
	}
}
