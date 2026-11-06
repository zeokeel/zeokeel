package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/tools"
)

// questionTimeout bounds how long an interactive ask_user blocks a turn before
// the runtime degrades to the historical pending behaviour (FP-16). Five
// minutes matches the approval TTL the UI already uses; a user away from the
// desk still gets a clean turn end instead of a stuck agent.
const questionTimeout = 5 * time.Minute

// appQuestionGate is the runtime.QuestionGate implementation for the desktop
// app: it turns an ask_user tool call into the controller's blocking question
// card and returns the answer as the same tool call's result, so the model
// continues in the same turn. A missing controller or an unanswered question
// degrades to runtime.ErrQuestionTimeout (the historical pending result).
type appQuestionGate struct {
	app *app
	// sessionID is the build-time session for per-session agents; empty means
	// resolve it from the turn context (shared/fallback bundles).
	sessionID string
	timeout   time.Duration
}

// Ask implements runtime.QuestionGate.
func (g appQuestionGate) Ask(ctx context.Context, callID, question string, options []string) (string, error) {
	if g.app == nil || g.app.services == nil {
		return "", runtime.ErrQuestionTimeout
	}
	ctrl := g.app.services
	sessionID := strings.TrimSpace(g.sessionID)
	if id := strings.TrimSpace(runtime.SessionIDFromContext(ctx)); id != "" {
		sessionID = id
	}
	if sessionID == "" {
		sessionID = ctrl.CurrentSessionID()
	}
	timeout := g.timeout
	if timeout <= 0 {
		timeout = questionTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	answer, err := ctrl.AskQuestion(waitCtx, sessionID, callID, tools.Question{
		Question: question,
		Options:  options,
	})
	if err != nil {
		// Turn cancellation/stop propagates as-is; only our own deadline
		// degrades to the pending fallback.
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return "", runtime.ErrQuestionTimeout
		}
		return "", err
	}
	return answer, nil
}

// newQuestionGate returns the gate for one agent build. The desktop app installs
// it so ask_user blocks inside the turn; the ACP/headless runtime leaves it nil
// (no UI: the pending result ends the turn, which is its documented degrade).
func (a *app) newQuestionGate(sessionID string) runtime.QuestionGate {
	return appQuestionGate{app: a, sessionID: sessionID}
}

// questionGateForSession returns a gate bound to sessionID for tests and for
// per-session agent builds (nil app keeps the pending fallback).
func (a *app) questionGateForSession(sessionID string) runtime.QuestionGate {
	return a.newQuestionGate(sessionID)
}

var _ runtime.QuestionGate = appQuestionGate{}
var _ = session.ErrClosed
