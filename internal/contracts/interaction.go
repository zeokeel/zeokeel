package contracts

import (
	"context"
	"strings"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// UserQuestion is one blocking question an agent host tool asks the user (the
// old shell's ask_user interaction).
type UserQuestion struct {
	// ID identifies the pending question (Answer/Cancel use it).
	ID string
	// Text is the question body.
	Text string
	// Options are the selectable answers; empty means free text.
	Options []string
}

// UserInteraction bridges agent-side questions to the desktop UI: the process
// host provides it, a UI module registers the renderer/answer handler, and the
// agent's ask_user tool parks on Ask until the user answers.
type UserInteraction interface {
	// SetHandler installs the question handler (nil clears it).
	SetHandler(fn func(UserQuestion))
	// Ask blocks until the user answers or cancels the question.
	Ask(ctx context.Context, text string, options []string) (string, error)
	// Answer resolves the pending question id with the chosen answer.
	Answer(id, answer string)
	// Cancel dismisses the pending question id with an error.
	Cancel(id string)
}

// KeyUserInteraction is provided by the desktop host.
var KeyUserInteraction = cordis.NewServiceKey[UserInteraction]("zeokeel/interaction")

// Artifact is one presented artifact (the old shell's present tool preview).
type Artifact struct {
	// ID is the artifact identity returned to the agent tool.
	ID string
	// Title and Body are the presented content (Body is Markdown).
	Title string
	Body  string
	// CreatedAt is when the artifact was presented.
	CreatedAt time.Time
}

// ArtifactLister is the optional history half of the artifact presenter: the
// inspector lists previously presented artifacts and can preview them again.
type ArtifactLister interface {
	// Artifacts returns the retained history, newest first.
	Artifacts() []Artifact
}

// ArtifactPresenter bridges the agent's present tool to the desktop UI: the
// host provides it, a UI module registers the display handler, and Present
// records the artifact and returns its id immediately.
type ArtifactPresenter interface {
	SetArtifactHandler(fn func(Artifact))
	Present(ctx context.Context, title, body string) (string, error)
}

// PlanReview is the plan-mode state surfaced to the UI.
type PlanReview struct {
	// Content is the plan under review (Markdown).
	Content string
	// State is "idle", "reviewing", "approved" or "cancelled".
	State string
}

// PlanReviewer bridges the agent's plan_review / exit_plan_mode tools to the
// desktop UI: Review parks a plan for display, Exit applies the user/model
// decision.
type PlanReviewer interface {
	SetPlanHandler(fn func(PlanReview))
	Review(ctx context.Context, content string) (string, error)
	Exit(action string) (string, error)
	State() string
}

// KeyArtifactPresenter and KeyPlanReviewer are provided by the desktop host.
var (
	KeyArtifactPresenter = cordis.NewServiceKey[ArtifactPresenter]("zeokeel/artifacts")
	KeyPlanReviewer      = cordis.NewServiceKey[PlanReviewer]("zeokeel/plan")
)

// Desktop integration settings applied by the process host.
const (
	// SettingAutostart installs/removes the per-user autostart entry (Linux
	// XDG autostart today).
	SettingAutostart = "ui.autostart"
	// SettingCloseToTray keeps the app alive in the system tray instead of
	// quitting when the window is closed (desktop only; default false).
	SettingCloseToTray = "ui.closeToTray"
	// SettingNotifications toggles the desktop toast overlay (default true).
	SettingNotifications = "ui.notifications"
	// SettingAgentsFile is the workspace instruction file appended to the
	// system prompt (default "AGENTS.md"; empty disables the injection).
	SettingAgentsFile = "agent.agentsFile"
	// SettingChatFontSize scales the conversation transcript: "small",
	// "standard" (default) or "large".
	SettingChatFontSize = "ui.chatFontSize"
	// SettingWorkStepsMode controls how much of a running/finished turn's work
	// steps the transcript shows (DSH-aligned): WorkStepsCompact,
	// WorkStepsStandard (default), WorkStepsDetailed or WorkStepsVerbose.
	SettingWorkStepsMode = "ui.workSteps.mode"
	// SettingWorkStepsCollapse controls when a completed turn's work steps fold:
	// WorkStepsCollapseCompletion (default) or WorkStepsCollapseNextInput.
	SettingWorkStepsCollapse = "ui.workSteps.collapse"
	// SettingThemeMode is the appearance source: "light", "dark" or "system"
	// (the legacy ui.theme.dark boolean is still written for older builds).
	SettingThemeMode = "ui.theme.mode"
	// SettingGlobalHotkey is a system-wide accelerator (e.g.
	// "CmdOrCtrl+Shift+Space") that shows and focuses the window; empty disables
	// it (desktop only).
	SettingGlobalHotkey = "ui.globalHotkey"
)

// Work-step presentation values (SettingWorkStepsMode / SettingWorkStepsCollapse).
const (
	WorkStepsCompact  = "compact"
	WorkStepsStandard = "standard"
	WorkStepsDetailed = "detailed"
	WorkStepsVerbose  = "verbose"

	WorkStepsCollapseCompletion = "completion"
	WorkStepsCollapseNextInput  = "next-input"
)

// NormalizeWorkStepsMode returns one documented work-steps mode, falling back to
// WorkStepsStandard for empty or unknown stored values.
func NormalizeWorkStepsMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case WorkStepsCompact:
		return WorkStepsCompact
	case WorkStepsDetailed:
		return WorkStepsDetailed
	case WorkStepsVerbose:
		return WorkStepsVerbose
	default:
		return WorkStepsStandard
	}
}

// NormalizeWorkStepsCollapse returns one documented collapse timing, falling back
// to WorkStepsCollapseCompletion for empty or unknown stored values.
func NormalizeWorkStepsCollapse(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case WorkStepsCollapseNextInput:
		return WorkStepsCollapseNextInput
	default:
		return WorkStepsCollapseCompletion
	}
}
