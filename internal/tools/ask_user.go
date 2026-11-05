package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Question is a user question an agent asked through the ask_user tool. The
// controller records it as a pending question for the session (keyed by the
// tool call id) and the live session renders it as a card with a text input.
type Question struct {
	// CallID is the runtime tool call id. The tool cannot see it from inside
	// Invoke, so the controller fills it from the tool_end event.
	CallID string `json:"call_id,omitempty"`
	// Question is the text shown to the user.
	Question string `json:"question"`
	// Options is an optional set of suggested answers.
	Options []string `json:"options,omitempty"`
	// Context is optional background the model wants the answer measured
	// against (for example the decision being made).
	Context string `json:"context,omitempty"`
	// Status is "pending" until the user answers, then "answered".
	Status string `json:"status,omitempty"`
	// AskedAt is filled by the controller from the end event.
	AskedAt time.Time `json:"asked_at,omitempty"`
}

// QuestionResult is the JSON document AskUserTool returns. The controller
// parses it from the successful tool_end payload, so the question survives in
// the session state even though the tool is stateless across turns.
type QuestionResult struct {
	Kind     string   `json:"kind"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Context  string   `json:"context,omitempty"`
	Status   string   `json:"status"`
}

const (
	questionKind    = "question"
	questionPending = "pending"
)

const askUserDescription = `Ask the user a clarifying question and stop the turn.

Call it with a JSON object:
  {"question":"Which database should I target?","options":["sqlite","postgres"],"context":"The schema differs."}

"question" is required. "options" is an optional list of suggested answers.
"context" is optional background that helps the user answer.

The tool returns a JSON document {"kind":"question","question":...,
"status":"pending"} immediately. The live session shows the question as a card
with a text input; the user's answer becomes the next user turn, so the
conversation resumes with their reply. Do not guess an answer after calling
this tool: wait for the user.`

// AskUserTool asks the user a question. It keeps an in-memory record of the
// questions this agent instance asked (guarded by mu) so tests and future
// wiring can inspect them; the authoritative pending-question state for a
// session lives in the controller, derived from the returned result.
type AskUserTool struct {
	mu    sync.Mutex
	asked []Question
}

// NewAskUserTool returns an empty ask_user tool.
func NewAskUserTool() *AskUserTool {
	return &AskUserTool{}
}

// Name implements runtime.Tool.
func (t *AskUserTool) Name() string { return "ask_user" }

// Description implements runtime.Tool.
func (t *AskUserTool) Description() string { return askUserDescription }

// askUserArgs is the decoded ask_user argument object.
type askUserArgs struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Context  string   `json:"context,omitempty"`
}

// Invoke implements runtime.Tool. The invocation returns as soon as the
// question is recorded: blocking here would deadlock the turn, because the
// only channel for the answer is the next user message.
func (t *AskUserTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var in askUserArgs
	if len(strings.TrimSpace(string(args))) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", fmt.Errorf("ask_user: invalid arguments: %w", err)
		}
	}
	question := strings.TrimSpace(in.Question)
	if question == "" {
		return "", errors.New(`ask_user: "question" is required`)
	}
	options := cleanOptions(in.Options)
	context := strings.TrimSpace(in.Context)

	record := Question{
		Question: question,
		Options:  options,
		Context:  context,
		Status:   questionPending,
		AskedAt:  time.Now().UTC(),
	}

	t.mu.Lock()
	t.asked = append(t.asked, record)
	t.mu.Unlock()

	out, err := json.Marshal(QuestionResult{
		Kind:     questionKind,
		Question: question,
		Options:  options,
		Context:  context,
		Status:   questionPending,
	})
	if err != nil {
		return "", fmt.Errorf("ask_user: encode result: %w", err)
	}
	return string(out), nil
}

// Pending returns a copy of the questions this tool instance asked, oldest
// first. It never blocks on a user answer.
func (t *AskUserTool) Pending() []Question {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Question, len(t.asked))
	copy(out, t.asked)
	for i := range out {
		out[i].Options = append([]string(nil), out[i].Options...)
	}
	return out
}

// cleanOptions drops blank and duplicate suggestions while preserving order.
func cleanOptions(options []string) []string {
	if len(options) == 0 {
		return nil
	}
	out := make([]string, 0, len(options))
	seen := make(map[string]bool, len(options))
	for _, option := range options {
		option = strings.TrimSpace(option)
		if option == "" || seen[option] {
			continue
		}
		seen[option] = true
		out = append(out, option)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
