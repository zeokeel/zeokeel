package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/compose"
	"github.com/google/uuid"
)

// QuestionGate is the runtime seam for interactive questions (FP-16): the
// ask_user tool call publishes a question card through the gate and blocks until
// the user answers, the turn is cancelled, or the question times out. The
// implementation lives in the app/controller (it owns the UI and the answer
// plumbing); the runtime only routes the tool call through it.
//
// Ask returns nil error with the answer text when the user answered. Returning
// ErrQuestionTimeout asks the runtime to fall back to the historical
// fire-and-forget behaviour (the tool's "pending" result ends the turn and the
// user's answer arrives as the next user message). Any other error fails the
// tool call; a cancelled turn is reported through the context.
type QuestionGate interface {
	Ask(ctx context.Context, callID, question string, options []string) (answer string, err error)
}

// ErrQuestionTimeout reports that the interactive question timed out: the
// runtime degrades to the pending/fire-and-forget result so an unattended turn
// still ends cleanly.
var ErrQuestionTimeout = errors.New("runtime: question timed out")

// askUserToolName is the tool the question gate intercepts.
const askUserToolName = "ask_user"

// questionGateArgs is the ask_user argument subset the gate needs. The runtime
// deliberately does not import the tools package; the wire format is agreed with
// tools.QuestionResult.
type questionGateArgs struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Context  string   `json:"context,omitempty"`
}

// questionGateResult is the JSON result returned in place of the tool's pending
// document when the user answered inside the turn. Field names match
// tools.QuestionResult (+answer).
type questionGateResult struct {
	Kind     string   `json:"kind"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Context  string   `json:"context,omitempty"`
	Status   string   `json:"status"`
	Answer   string   `json:"answer,omitempty"`
}

// askUserQuestion intercepts an ask_user invocation when a gate is configured.
// handled=false means the caller keeps the historical path (invoke the tool,
// which returns the pending document). A non-nil error fails the tool call.
func (e *einoInvokableTool) askUserQuestion(ctx context.Context, args json.RawMessage) (result string, handled bool, err error) {
	if e == nil || e.questionGate == nil || e.tool.Name() != askUserToolName {
		return "", false, nil
	}
	// Questions need the turn's answer channel; a delegated subagent has no
	// interactive card, so children keep the pending semantics.
	if turnContextFromContext(ctx) == nil || SubagentDepth(ctx) > 0 {
		return "", false, nil
	}
	var in questionGateArgs
	if len(args) == 0 || json.Unmarshal(args, &in) != nil {
		return "", false, nil // let the tool report invalid arguments
	}
	question := strings.TrimSpace(in.Question)
	if question == "" {
		return "", false, nil
	}
	callID := compose.GetToolCallID(ctx)
	if strings.TrimSpace(callID) == "" {
		callID = uuid.NewString()
	}
	answer, askErr := e.questionGate.Ask(ctx, callID, question, in.Options)
	switch {
	case askErr == nil:
		document, encErr := json.Marshal(questionGateResult{
			Kind:     "question",
			Question: question,
			Options:  in.Options,
			Context:  in.Context,
			Status:   "answered",
			Answer:   strings.TrimSpace(answer),
		})
		if encErr != nil {
			return "", true, fmt.Errorf("runtime: encode answered question: %w", encErr)
		}
		return string(document), true, nil
	case errors.Is(askErr, ErrQuestionTimeout):
		// Compatibility path: the tool returns its pending document and the
		// turn ends, exactly like before the gate existed.
		return "", false, nil
	default:
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", true, ctxErr
		}
		return "", true, fmt.Errorf("runtime: question gate: %w", askErr)
	}
}
