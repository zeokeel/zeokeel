package uikit

// The product's one status vocabulary and its one status anatomy. A tool call,
// a teammate run and a task-board item all carry a status string, and before
// Phase 2b each module painted its own copy and color (13 functions); a
// cancelled stop could render as a red failure in one pane and as a neutral
// label in another. DESIGN.md §5.2 fixes the semantics this file now owns:
//
//	running / in progress → WarningText (work in flight is not a failure)
//	success               → SuccessText
//	error                 → DangerText
//	cancelled             → LabelTertiary (a user stop is neutral)
//	interrupted / idle / pending / unknown → LabelTertiary
//
// Availability is not a run: no parser here turns "active" (a member's
// availability) into running (DESIGN.md AP-08). A status is never carried by
// color alone — the label is the caller's mandatory copy (localized by the
// domain helpers below).

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// StatusKind is the product's one status vocabulary. The domain statuses
// (ToolStatus, RunState, TaskStatus) fold into it, so one color/anatomy serves
// every surface.
type StatusKind int

const (
	// StatusUnknown is a status the backend did not name (older versions,
	// future states): it stays neutral and shows the raw value as its label.
	StatusUnknown StatusKind = iota
	// StatusRunning is work in flight (a tool call, a teammate run).
	StatusRunning
	// StatusInProgress is a task being worked on; same warning color as
	// running, different copy ("进行中").
	StatusInProgress
	// StatusSuccess finished well.
	StatusSuccess
	// StatusError failed.
	StatusError
	// StatusCancelled was stopped by the user: neutral, never a failure.
	StatusCancelled
	// StatusInterrupted is a run interrupted by the runtime (the teammate stays
	// available); neutral, not a failure.
	StatusInterrupted
	// StatusIdle is available with no run in flight.
	StatusIdle
	// StatusPending is a task waiting to start.
	StatusPending
)

// ToolStatus is a tool call's status as the contracts carry it, after folding
// the spellings Rig and MCP servers use.
type ToolStatus string

const (
	// ToolStatusRunning is a call that has started but not returned.
	ToolStatusRunning ToolStatus = "running"
	// ToolStatusSuccess is a call that returned a result.
	ToolStatusSuccess ToolStatus = "success"
	// ToolStatusError is a call that failed.
	ToolStatusError ToolStatus = "error"
	// ToolStatusCancelled is a call the user stopped: its own neutral state.
	ToolStatusCancelled ToolStatus = "cancelled"
	// ToolStatusUnknown is a status word the product does not know.
	ToolStatusUnknown ToolStatus = ""
)

// ParseToolStatus folds the tool-call status spellings onto ToolStatus. "active"
// is deliberately not running: it is an availability word, not a run state
// (DESIGN.md AP-08).
func ParseToolStatus(status string) ToolStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "in_progress", "in-progress", "started":
		return ToolStatusRunning
	case "ok", "success", "succeeded", "done", "complete", "completed":
		return ToolStatusSuccess
	case "error", "failed", "failure":
		return ToolStatusError
	case "canceled", "cancelled", "cancelled_by_user":
		return ToolStatusCancelled
	default:
		return ToolStatusUnknown
	}
}

// Kind is the tool status's place in the shared status vocabulary.
func (s ToolStatus) Kind() StatusKind {
	switch s {
	case ToolStatusRunning:
		return StatusRunning
	case ToolStatusSuccess:
		return StatusSuccess
	case ToolStatusError:
		return StatusError
	case ToolStatusCancelled:
		return StatusCancelled
	default:
		return StatusUnknown
	}
}

// ToolStatusLabel is the localized label of a tool status (the tool-card and
// inspector copy). An unknown non-empty status keeps the backend's own word, so
// a future state stays readable instead of being flattened to "未知".
func ToolStatusLabel(locale contracts.LocaleService, status string) string {
	switch ParseToolStatus(status) {
	case ToolStatusRunning:
		return T(locale, "运行中", "Running")
	case ToolStatusSuccess:
		return T(locale, "完成", "Completed")
	case ToolStatusError:
		return T(locale, "失败", "Failed")
	case ToolStatusCancelled:
		return T(locale, "已取消", "Cancelled")
	default:
		if trimmed := strings.TrimSpace(status); trimmed != "" {
			return trimmed
		}
		return T(locale, "未知", "Unknown")
	}
}

// RunState is the tracked run state of a teammate (contracts.TeamRun*): whether
// a run is in flight, apart from the manager's availability word.
type RunState string

const (
	// RunStateRunning is a run in flight.
	RunStateRunning RunState = "running"
	// RunStateIdle is available with no run in flight.
	RunStateIdle RunState = "idle"
	// RunStateDone is a run that returned an answer.
	RunStateDone RunState = "done"
	// RunStateFailed is a run that failed.
	RunStateFailed RunState = "failed"
	// RunStateInterrupted is a run the runtime interrupted.
	RunStateInterrupted RunState = "interrupted"
	// RunStateUnknown is a run state an older backend did not report.
	RunStateUnknown RunState = ""
)

// ParseRunState folds the tracked run-state spellings onto RunState.
// Availability words ("active", "interrupted" as a member status) are not run
// states: "active" stays unknown so it can never be drawn as running.
func ParseRunState(state string) RunState {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "running":
		return RunStateRunning
	case "idle":
		return RunStateIdle
	case "done", "complete", "completed", "success", "succeeded":
		return RunStateDone
	case "failed", "error", "failure":
		return RunStateFailed
	case "interrupted":
		return RunStateInterrupted
	default:
		return RunStateUnknown
	}
}

// Kind is the run state's place in the shared status vocabulary.
func (s RunState) Kind() StatusKind {
	switch s {
	case RunStateRunning:
		return StatusRunning
	case RunStateIdle:
		return StatusIdle
	case RunStateDone:
		return StatusSuccess
	case RunStateFailed:
		return StatusError
	case RunStateInterrupted:
		return StatusInterrupted
	default:
		return StatusUnknown
	}
}

// RunStateLabel is the localized label of a tracked run state (the Agents
// pane's badge and detail copy).
func RunStateLabel(locale contracts.LocaleService, state string) string {
	switch ParseRunState(state) {
	case RunStateRunning:
		return T(locale, "运行中", "Running")
	case RunStateIdle:
		return T(locale, "空闲", "Idle")
	case RunStateDone:
		return T(locale, "已完成", "Done")
	case RunStateFailed:
		return T(locale, "失败", "Failed")
	case RunStateInterrupted:
		return T(locale, "已中断", "Interrupted")
	default:
		if trimmed := strings.TrimSpace(state); trimmed != "" {
			return trimmed
		}
		return T(locale, "未知", "Unknown")
	}
}

// TaskStatus is one board task's status (contracts.TaskStatus*).
type TaskStatus string

const (
	// TaskStatusPending waits to start.
	TaskStatusPending TaskStatus = "pending"
	// TaskStatusInProgress is being worked on.
	TaskStatusInProgress TaskStatus = "in_progress"
	// TaskStatusCompleted is finished.
	TaskStatusCompleted TaskStatus = "completed"
	// TaskStatusUnknown is a board status the product does not know.
	TaskStatusUnknown TaskStatus = ""
)

// ParseTaskStatus folds the board's status spellings onto TaskStatus. "active"
// here is a board alias for in progress (the board is not a run), kept from the
// task pane's previous normalizer.
func ParseTaskStatus(status string) TaskStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "todo", "open", "waiting":
		return TaskStatusPending
	case "in_progress", "in-progress", "running", "active", "doing", "started":
		return TaskStatusInProgress
	case "completed", "complete", "done", "finished":
		return TaskStatusCompleted
	default:
		return TaskStatusUnknown
	}
}

// Kind is the task status's place in the shared status vocabulary.
func (s TaskStatus) Kind() StatusKind {
	switch s {
	case TaskStatusPending:
		return StatusPending
	case TaskStatusInProgress:
		return StatusInProgress
	case TaskStatusCompleted:
		return StatusSuccess
	default:
		return StatusUnknown
	}
}

// TaskStatusLabel is the localized label of a board task status (the Tasks
// pane's section headings and badges). An unknown non-empty status keeps the
// backend's own word.
func TaskStatusLabel(locale contracts.LocaleService, status string) string {
	switch ParseTaskStatus(status) {
	case TaskStatusPending:
		return T(locale, "待处理", "Pending")
	case TaskStatusInProgress:
		return T(locale, "进行中", "In progress")
	case TaskStatusCompleted:
		return T(locale, "已完成", "Completed")
	default:
		if trimmed := strings.TrimSpace(status); trimmed != "" {
			return trimmed
		}
		return T(locale, "未知", "Unknown")
	}
}

// StatusColor is the status's WCAG-AA foreground (text or icon): the semantic
// *Text token, so a status never uses a fill color as small text (DESIGN.md
// §5.2). Unnamed and non-failure states stay neutral.
func StatusColor(t *design.Tokens, kind StatusKind) ui.Color {
	switch kind {
	case StatusRunning, StatusInProgress:
		return t.WarningText
	case StatusSuccess:
		return t.SuccessText
	case StatusError:
		return t.DangerText
	default:
		return t.LabelTertiary
	}
}

// statusFill is the status's fill color beside StatusColor: the badge/dot/icon
// background before the tint alpha (DESIGN.md §5.2: status colors fill, they do
// not write text).
func statusFill(t *design.Tokens, kind StatusKind) ui.Color {
	switch kind {
	case StatusRunning, StatusInProgress:
		return t.Warning
	case StatusSuccess:
		return t.Success
	case StatusError:
		return t.Danger
	default:
		return t.LabelTertiary
	}
}

// badgeTintAlpha is the badge fill opacity of a status. The success tint is
// deliberately lighter (0.08) than the warning/danger tints (0.12): SuccessText
// on an 8% Success tint reaches WCAG-AA for small text, while 12% falls just
// short.
func badgeTintAlpha(kind StatusKind) float32 {
	if kind == StatusSuccess {
		return 0.08
	}
	return 0.12
}

// StatusText draws one status line: the caller's mandatory, localized label in
// the brand role and the status's WCAG-AA text color. A status is never carried
// by color alone, so the label argument is required.
func StatusText(c *ui.Context, t *design.Tokens, role design.TypeRole, kind StatusKind, label string) ui.Element {
	return design.Apply(ui.Text(c, label), t.Font(role)).TextColor(StatusColor(t, kind))
}

// StatusBadge draws one status badge: the caller's localized label on a tint of
// the status fill with the status's text color — the one status-badge anatomy the
// tool cards, the task board and the team pane share.
func StatusBadge(c *ui.Context, t *design.Tokens, kind StatusKind, label string) ui.Element {
	return design.Apply(ui.Badge(c, label), t.Badge(), func(el ui.Element) ui.Element {
		return el.Background(statusFill(t, kind).Alpha(badgeTintAlpha(kind))).TextColor(StatusColor(t, kind))
	})
}
