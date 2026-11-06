package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/state"
)

// eventSink serializes event delivery for one turn. The turn goroutine and any
// tool goroutine publishing an approval request both send through it, so the
// output channel is closed exactly once and never while a send is in flight.
// A send that loses a race with the end of the turn is dropped instead of
// panicking.
type eventSink struct {
	mu     sync.Mutex
	ch     chan<- state.Event
	closed bool
}

func newEventSink(ch chan<- state.Event) *eventSink {
	return &eventSink{ch: ch}
}

// send publishes ev, returning false when ctx ends or the turn is over. It may
// block while the consumer drains the channel, exactly like the previous
// direct channel send.
func (s *eventSink) send(ctx context.Context, ev state.Event) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	select {
	case s.ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// terminalSendGrace is how long trySend waits for room in a full output
// channel. Terminal events (the final "idle/stopped" Status, a cancelled
// ToolEnd, trailing usage) decide how the turn is recorded, so silently
// dropping them can persist "completed" for a stopped turn (RT-13). The wait is
// bounded: if the consumer really is gone, the turn goroutine must not leak.
const terminalSendGrace = 2 * time.Second

// trySend publishes ev, preferring delivery over a drop-on-full policy: it first
// tries the non-blocking send, then waits up to terminalSendGrace for the
// consumer to drain the channel. Only a closed sink or a consumer that stays
// blocked for the whole grace period loses the event, which keeps terminal state
// ("stopped", failed tool) correct under transient UI/database backpressure
// without hanging a cancelled turn.
func (s *eventSink) trySend(ev state.Event) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- ev:
		return
	default:
	}
	timer := time.NewTimer(terminalSendGrace)
	defer timer.Stop()
	select {
	case s.ch <- ev:
	case <-timer.C:
	}
}

// close closes the event channel and makes later sends no-ops. Only the run
// goroutine calls close.
func (s *eventSink) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
}

// turnContextKey carries per-turn runtime state into tool invocations. eino
// invokes tools with a context derived from the runner's context, so this is
// how a tool knows which turn's event sink to publish an approval request on.
type turnContextKey struct{}

type turnContext struct {
	sink   *eventSink
	record *turnRecord
	// userRequest is the user text that started this turn. The automatic
	// reviewer uses it as authorization evidence (never as an instruction).
	userRequest string
	// reviewContext supplies the reviewer's environment per review: the
	// current working directory and the project constraints. Nil leaves both
	// empty.
	reviewContext func() (workDir, constraints string)

	// failedTools records the calls whose Invoke returned an error. The
	// adapter delivers the error to the model as a tool result (so one bad
	// command does not end the turn); the ToolEnd event still needs the
	// error state for the UI's red card and the tool_calls table.
	//
	// advisories are model-visible notes queued for the next provider request
	// (soft cost-budget crossings and repeated-tool reminders); the provider
	// adapter drains them into the next tool result. repeatSeen counts
	// identical tool name+args calls within this turn. mu guards all of these.
	mu          sync.Mutex
	failedTools map[string]bool
	advisories  []string
	repeatSeen  map[string]int

	// Soft cost-budget state. The provider adapter feeds it while converting
	// the stream (which happens before eino can build the next request), so a
	// crossing raised there is injected into that next request deterministically.
	budgetSteps    []int
	budgetTokens   []int
	renderAdvisory func(kind string, steps, tokens int) string
	advSteps       int // completed model calls seen by the adapter
	advOut         int // output tokens reported so far
	stepWarnAt     int
	tokenWarnAt    int
	footerAdvisory string
}

// repeatWarnThresholds are the identical-call counts that raise a soft
// repeated-tool reminder (never a hard stop).
var repeatWarnThresholds = []int{3, 5, 8}

// enqueueAdvisory queues one model-visible advisory for the next provider
// request. Duplicate texts are collapsed so retries cannot stack copies.
func (tc *turnContext) enqueueAdvisory(text string) {
	if tc == nil || text == "" {
		return
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	for _, existing := range tc.advisories {
		if existing == text {
			return
		}
	}
	tc.advisories = append(tc.advisories, text)
}

// takeAdvisories drains the queued advisories; the provider adapter injects
// them into the next request exactly once.
func (tc *turnContext) takeAdvisories() []string {
	if tc == nil {
		return nil
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if len(tc.advisories) == 0 {
		return nil
	}
	out := append([]string(nil), tc.advisories...)
	tc.advisories = nil
	return out
}

// noteUsageSample records the output tokens a provider chunk reported. Called
// from the adapter's stream goroutine, before eino builds the next request.
func (tc *turnContext) noteUsageSample(u *Usage) {
	if tc == nil || u == nil || u.OutputTokens <= 0 {
		return
	}
	tc.mu.Lock()
	tc.advOut += u.OutputTokens
	tc.checkBudgetLocked()
	tc.mu.Unlock()
}

// noteModelCall records one completed provider call for the step budget and
// fires any threshold it just crossed.
func (tc *turnContext) noteModelCall() {
	if tc == nil {
		return
	}
	tc.mu.Lock()
	tc.advSteps++
	tc.checkBudgetLocked()
	tc.mu.Unlock()
}

// checkBudgetLocked queues the next crossed cost advisory (steps/tokens) and
// records it for the footer. Each threshold fires at most once per turn; the
// turn never stops. tc.mu must be held.
func (tc *turnContext) checkBudgetLocked() {
	for tc.stepWarnAt < len(tc.budgetSteps) && tc.advSteps >= tc.budgetSteps[tc.stepWarnAt] {
		tc.stepWarnAt++
		if tc.renderAdvisory != nil {
			if text := tc.renderAdvisory("steps", tc.advSteps, tc.advOut); text != "" {
				tc.advisories = append(tc.advisories, text)
				tc.footerAdvisory = text
				return
			}
		}
	}
	for tc.tokenWarnAt < len(tc.budgetTokens) && tc.advOut >= tc.budgetTokens[tc.tokenWarnAt] {
		tc.tokenWarnAt++
		if tc.renderAdvisory != nil {
			if text := tc.renderAdvisory("tokens", tc.advSteps, tc.advOut); text != "" {
				tc.advisories = append(tc.advisories, text)
				tc.footerAdvisory = text
				return
			}
		}
	}
}

// takeFooterAdvisory drains the advisory the footer should show on the next
// usage delta.
func (tc *turnContext) takeFooterAdvisory() string {
	if tc == nil {
		return ""
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	text := tc.footerAdvisory
	tc.footerAdvisory = ""
	return text
}

// noteToolCall records one model-requested call (name + raw arguments) and
// queues a reminder when the same call repeats at a threshold. Different
// arguments are different signatures, so they never count against each other.
func (tc *turnContext) noteToolCall(name string, args json.RawMessage) {
	if tc == nil || name == "" {
		return
	}
	sig := name + "\x00" + string(args)
	tc.mu.Lock()
	if tc.repeatSeen == nil {
		tc.repeatSeen = make(map[string]int)
	}
	tc.repeatSeen[sig]++
	count := tc.repeatSeen[sig]
	tc.mu.Unlock()
	for _, threshold := range repeatWarnThresholds {
		if count != threshold {
			continue
		}
		tc.enqueueAdvisory(fmt.Sprintf(
			"Repeated tool call advisory (not a hard stop): %s has been called %d times with identical arguments in this turn. Check whether the loop is making progress; vary the arguments, try a different approach, or ask the user before repeating it again.",
			name, count))
		return
	}
}

// markToolFailure records callID as a failed invocation for the current turn.
func (tc *turnContext) markToolFailure(callID string) {
	if tc == nil || callID == "" {
		return
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if tc.failedTools == nil {
		tc.failedTools = make(map[string]bool)
	}
	tc.failedTools[callID] = true
}

// takeToolFailure reports whether callID failed and forgets the record.
func (tc *turnContext) takeToolFailure(callID string) bool {
	if tc == nil || callID == "" {
		return false
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	failed := tc.failedTools[callID]
	delete(tc.failedTools, callID)
	return failed
}

func withTurnContext(ctx context.Context, tc *turnContext) context.Context {
	return context.WithValue(ctx, turnContextKey{}, tc)
}

func turnContextFromContext(ctx context.Context) *turnContext {
	if ctx == nil {
		return nil
	}
	tc, _ := ctx.Value(turnContextKey{}).(*turnContext)
	return tc
}
