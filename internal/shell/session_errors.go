package shell

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/state"
)

// sessionErrorKind is the product-facing class of a failed turn. The raw
// runtime error is technical text most users cannot act on, so the transcript
// card leads with the class, its cause and the action that fixes it, and keeps
// the raw text behind a Technical details toggle (PD-09).
type sessionErrorKind int

const (
	sessionErrorGeneric sessionErrorKind = iota
	sessionErrorAuth
	sessionErrorRateLimit
	sessionErrorNetwork
	sessionErrorContext
	sessionErrorModel
)

// classifySessionError maps an error's text to the card that explains it. The
// patterns are the wire messages the providers in this build produce (HTTP
// status codes, OpenAI-compatible bodies, Go net errors); anything else stays
// generic instead of guessing a wrong cause.
func classifySessionError(text string) sessionErrorKind {
	lower := strings.ToLower(text)
	switch {
	case hasAny(lower, "401", "403", "unauthorized", "invalid api key", "invalid_api_key",
		"authentication", "permission denied", "api key"):
		return sessionErrorAuth
	case hasAny(lower, "429", "rate limit", "rate_limit", "too many requests", "overloaded"):
		return sessionErrorRateLimit
	case hasAny(lower, "context length", "context window", "maximum context", "token limit",
		"too many tokens", "prompt is too long"):
		return sessionErrorContext
	case hasAny(lower, "dial tcp", "connection refused", "no such host", "connection reset",
		"timeout", "timed out", "deadline exceeded", "network is unreachable", "eof"):
		return sessionErrorNetwork
	case hasAny(lower, "404", "model not found", "model_not_found", "does not exist",
		"unknown model", "no such model"):
		return sessionErrorModel
	}
	return sessionErrorGeneric
}

func hasAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

// sessionErrorCopy is the localized title, cause and follow-up action of one
// error class.
type sessionErrorCopy struct {
	title, cause string
	openModels   bool
	retry        bool
}

func (a *App) sessionErrorCopy(kind sessionErrorKind) sessionErrorCopy {
	switch kind {
	case sessionErrorAuth:
		return sessionErrorCopy{
			title:      a.T("session.error.auth_title"),
			cause:      a.T("session.error.auth_cause"),
			openModels: true,
		}
	case sessionErrorRateLimit:
		return sessionErrorCopy{
			title: a.T("session.error.rate_limit_title"),
			cause: a.T("session.error.rate_limit_cause"),
			retry: true,
		}
	case sessionErrorNetwork:
		return sessionErrorCopy{
			title: a.T("session.error.network_title"),
			cause: a.T("session.error.network_cause"),
			retry: true,
		}
	case sessionErrorContext:
		return sessionErrorCopy{
			title: a.T("session.error.context_title"),
			cause: a.T("session.error.context_cause"),
			retry: true,
		}
	case sessionErrorModel:
		return sessionErrorCopy{
			title:      a.T("session.error.model_title"),
			cause:      a.T("session.error.model_cause"),
			openModels: true,
		}
	default:
		return sessionErrorCopy{
			title: a.T("session.error.generic_title"),
			cause: a.T("session.error.generic_cause"),
			retry: true,
		}
	}
}

// liveErrorCard is one failed turn's transcript card: a danger-tinted panel
// with the classified title, the one-line cause, the fix (open the model
// settings or retry) and the raw error folded behind Technical details. The
// card replaces the old single red line so a failure reads as an actionable
// state instead of leaked internals.
func (a *App) liveErrorCard(c *ui.Context, t *design.Tokens, info state.ErrorInfo) {
	th := t.Theme()
	copy := a.sessionErrorCopy(classifySessionError(info.Text))
	st := a.liveMenus()
	open := st.errorDetails[info.Seq]
	ui.Column(c).Gap(th.Space(2)).Padding(th.Space(3)).Radius(design.RadiusLG).
		Background(t.Danger.Alpha(0.06).Over(t.BgRaised)).
		Border(1, t.Danger.Alpha(0.35).Over(t.BgRaised)).
		Children(func() {
			ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.AlertTriangle()).Size(th.Space(4), th.Space(4)).
					TextColor(t.DangerText)
				ui.Text(c, copy.title).FontSize(13).FontWeight(600).
					TextColor(t.DangerText).Grow(1)
			})
			ui.Text(c, copy.cause).FontSize(12).FixedLineHeight(18).
				TextColor(t.LabelSecondary).MaxLines(3)
			ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
				if copy.openModels {
					label := a.T("session.error.open_model_settings")
					if ui.Button(c, label).Height(th.Space(8)).Label(label).Clicked() {
						a.shellUI().settingsTab = settingsTabModels
						a.openSettings()
					}
				}
				if copy.retry {
					label := a.T("session.error.retry")
					if ui.Button(c, label).Height(th.Space(8)).Label(label).Clicked() {
						a.retryLastUserMessage(c)
					}
				}
				ui.Spacer(c).Grow(1)
				label := a.T("session.error.details")
				if open {
					label = a.T("session.error.hide_details")
				}
				button := ui.Button(c, label).Height(th.Space(8)).Label(label)
				button.OnClick(func() {
					if st.errorDetails == nil {
						st.errorDetails = make(map[uint64]bool)
					}
					st.errorDetails[info.Seq] = !open
				})
			})
			if open {
				ui.Text(c, info.Text).Font(monoFont).FontSize(11).
					FixedLineHeight(17).TextColor(t.DangerText).MaxLines(10)
			}
		})
}
