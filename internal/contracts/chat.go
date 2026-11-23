package contracts

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Settings keys owned by the chat surface. Both the agent backend module and
// the settings UI read and write these through contracts.SettingsService, so
// the model configuration has one home.
const (
	SettingsChatModel        = "chat.model"
	SettingsChatBaseURL      = "chat.baseURL"
	SettingsChatAPIKey       = "chat.apiKey"
	SettingsChatSystemPrompt = "chat.systemPrompt"
	// SettingsChatContextBudget overrides the prompt-token budget used by the
	// automatic compactor; empty follows the module config and an explicit "0"
	// disables the budget (no automatic compaction).
	SettingsChatContextBudget = "chat.contextBudget"
)

// Host-owned UI preference keys. The process host persists these between runs;
// the appearance settings page writes them through contracts.SettingsService.
const (
	// SettingsUIThemeDark stores "true"/"false" for the dark theme.
	SettingsUIThemeDark = "ui.theme.dark"
	// SettingsUILanguage stores the UI language code ("zh"/"en").
	SettingsUILanguage = "ui.language"
)

// Chat roles used by ChatMessage.Role.
const (
	ChatRoleUser      = "user"
	ChatRoleAssistant = "assistant"
	ChatRoleTool      = "tool"
	ChatRoleSystem    = "system"
	// ChatRoleNotice is a quiet transcript receipt for a delegated child (a
	// teammate or subagent finished/failed). It is derived from the durable
	// child-session records, not from model history.
	ChatRoleNotice = "notice"
)

// ChatMessage is one message of a session as the UI reads it. The agent module
// owns the write path; UI modules treat it as a read model.
type ChatMessage struct {
	// ID is stable for the lifetime of the message.
	ID string
	// Role is one of the ChatRole* constants.
	Role string
	// Text is the message body (assistant text streams into it).
	Text string
	// Reasoning is the model's thinking text when the provider streams it.
	// It is the flat concatenation of every iteration's thinking (kept for
	// compatibility); ReasoningSegments carries the per-iteration boundaries.
	Reasoning string
	// ReasoningSegments splits the thinking text by model iteration so the
	// transcript can interleave thinking with text/tool cards chronologically
	// (2026-11 audit US-01/US-02). A live run folds all iterations into one
	// assistant message and records one segment per iteration; a restored
	// conversation has one assistant message per iteration, each with a single
	// segment. nil when the message carries no reasoning. Additive read model:
	// consumers that only know Reasoning keep working.
	ReasoningSegments []ReasoningSegment `json:"reasoningSegments,omitempty"`
	// Streaming marks the assistant message currently being produced.
	Streaming bool
	// Tools lists the tool calls that belong to this message, in call order.
	Tools []ToolCall
	// ToolTimelineKnown reports that the tool calls above carry a live-recorded
	// timeline: their TextOffset values say where in Text each call arrived.
	// It is false for messages restored from history without the durable
	// metadata (old app versions, imported sessions, ACP-backed sessions):
	// those keep the legacy whole-text-then-cards fallback because an all-zero
	// offset set is indistinguishable from "no offsets recorded".
	ToolTimelineKnown bool
	// Attachments are the files/images sent with a user message (display
	// read model; the image bytes also travel as rig image content parts).
	Attachments []Attachment
	// Usage is the token/time accounting of the assistant turn (nil when the
	// provider reported none).
	Usage *MessageUsage
	// Error is set when the message (usually an assistant turn) failed.
	Error string
	// ErrorKind classifies Error for localized UI copy ("" = unknown/no error,
	// auth, rate_limit, network, timeout, context, iterations, loop,
	// sandbox_denied, cancelled or other).
	ErrorKind string `json:"errorKind,omitempty"`
	// Truncated reports that the provider cut the answer at the output-token
	// limit (finish_reason "length"): the streamed text is a partial answer, not
	// a complete reply. The agent records it on the message and in the durable
	// run_end event so a restart keeps the hint.
	Truncated bool `json:"truncated,omitempty"`
	// GoalRound marks a user message the engine-side goal driver injected as an
	// automatic continuation round (nil for messages the human sent). The UI
	// renders it as a goal round instead of a human prompt; the durable
	// user_message event carries the same marker.
	GoalRound *GoalRoundInfo `json:"goalRound,omitempty"`
	// CreatedAt is the message timestamp (local time, display only).
	CreatedAt time.Time
}

// ReasoningSegment is one model iteration's reasoning text inside an assistant
// message (2026-11 audit US-01/US-02). It reuses the ToolCall.TextOffset
// timeline mechanism: TextOffset says where in the message's cumulative Text
// this iteration's thinking began, so a renderer can interleave thinking,
// prose and tool cards in arrival order instead of drawing one aggregated
// thinking block above the whole reply.
type ReasoningSegment struct {
	// Text is this iteration's reasoning text (its concatenated deltas).
	Text string `json:"text"`
	// Iteration is the 1-based model iteration within the run that produced
	// this segment (rig Event.Iteration). Restored messages derive it from
	// their position in the turn: each persisted assistant message is one
	// iteration.
	Iteration int `json:"iteration"`
	// TextOffset is the assistant text length (in bytes) when this iteration's
	// reasoning began. Restored single-iteration messages carry 0: their text
	// belongs to the same iteration and rig history records no intra-iteration
	// ordering.
	TextOffset int `json:"textOffset"`
}

// ChatSession is the read model of one conversation.
type ChatSession struct {
	ID    string
	Title string
	// Model is the effective model for this session: the session override when
	// one was chosen in the composer, else the configured chat.model.
	Model    string
	Messages []ChatMessage
	Running  bool
	Error    string
	// Interrupted marks a session restored after a run that never finished (the
	// durable checkpoint is still in requested/tool_pending phase, e.g. the app
	// crashed or was killed). It is a read-model hint, not persisted: the UI can
	// show "上次运行被中断" and offer a retry. The next run start clears it.
	Interrupted bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ChatSessionInfo is the list-item view of a session for the sidebar.
type ChatSessionInfo struct {
	ID string
	// WorkspaceID is the workspace the session was created in ("" when none
	// was open or the backend does not track workspaces).
	WorkspaceID string
	Title       string
	// Pinned marks a pinned session: the sidebar sorts pinned sessions first
	// within their workspace group.
	Pinned bool
	// Archived marks an archived session: the sidebar always hides it; the
	// settings surface's Archive page is the one place that lists archived
	// sessions and can restore or delete them (H-series usability fix).
	Archived     bool
	MessageCount int
	Running      bool
	// Waiting marks a running session whose model request has not produced any
	// output yet: the user sent a prompt and is waiting for the first reply
	// token/tool call. It is only meaningful while Running; a finished run is
	// never Waiting. The sidebar uses it to tell 等待回复 from an actively
	// streaming 运行中 run.
	Waiting bool
	// Unread marks a run that finished while the user was not looking at the
	// session: the reply arrived in the background and has not been viewed yet.
	// Becoming the active session (SetActiveSession) or starting the next run
	// clears it. It is persisted through SessionUnreadSettingKey, so a reply
	// that arrives while the app is closed behind another session survives a
	// restart.
	Unread    bool
	UpdatedAt time.Time
	// Origin records who created the session: "" (a user session), "automation"
	// (an automation-run session) or "subagent" (a child conversation once
	// child sessions become durable). Sidebar surfaces hide non-user origins.
	Origin string
	// PermissionScope reports where the session's APPROVAL policy comes from:
	// PermissionScopeGlobal (the global agent.permission preset) or
	// PermissionScopeSession (a per-session override is active). The override
	// only changes the approval gate — the OS file sandbox always follows the
	// global mode (2026-11 audit (a), disclosure route: a session-level "all
	// permissions" does NOT lift the OS wall).
	PermissionScope string `json:"permissionScope,omitempty"`
	// EffectiveApprovalMode is the session's effective approval policy
	// (read-only / workspace / full): the session override when one is set,
	// else the global preset. It is the gate's policy, not the OS mode.
	EffectiveApprovalMode string `json:"effectiveApprovalMode,omitempty"`
	// EffectiveSandboxMode is the actual OS file-sandbox mode (read-only /
	// workspace / full). It ALWAYS follows the global agent.permission setting:
	// per-session OS modes are not implemented, so a session override never
	// changes this value (disclosure, not a new capability). A read-only
	// session under a global full preset still runs unconfined at the OS level
	// (the reverse-leak disclosure of audit (a)).
	EffectiveSandboxMode string `json:"effectiveSandboxMode,omitempty"`
}

// Permission scope values for ChatSessionInfo.PermissionScope.
const (
	// PermissionScopeGlobal means the session follows the global approval
	// preset (no per-session override).
	PermissionScopeGlobal = "global"
	// PermissionScopeSession means a per-session approval override is active
	// (approval gate only; the OS sandbox still follows the global mode).
	PermissionScopeSession = "session"
)

// Session origin values for ChatSessionInfo.Origin.
const (
	SessionOriginUser       = ""
	SessionOriginAutomation = "automation"
	SessionOriginSubagent   = "subagent"
)

// SessionOriginSettingKey is the settings key that records one session's
// origin. The automation backend writes it when it starts a run; the chat read
// model and sidebar read it without a session-store schema change.
func SessionOriginSettingKey(sessionID string) string {
	return "session." + sessionID + ".origin"
}

// ChatConfig is the model configuration the agent module uses for new runs.
// Empty fields fall back to module config, then to the settings store keys
// above (SettingsChat*), then to the process environment (OPENAI_API_KEY).
type ChatConfig struct {
	Model        string
	BaseURL      string
	APIKey       string
	SystemPrompt string
	// MaxIterations is the assembly-level model/tool round cap (0 = unlimited).
	// It is product definition, not a user setting: no settings row overrides
	// it (the chat.maxIterations knob was removed in v0.2.50).
	MaxIterations int
	ContextBudget int
	// ReasoningEffort is the default openai reasoning_effort ("low", "medium",
	// "high" or "" for the provider default). Per-session overrides live in
	// settings (SessionEffortSettingKey).
	ReasoningEffort string
}

// Manual context compaction (the /compact command). The agent backend replaces
// the session history with the compactor's result; the error values let the UI
// render a specific message.
var (
	ErrCompactRunning     = errors.New("session is running")
	ErrCompactUnavailable = errors.New("context compaction is unavailable")
	ErrCompactNothing     = errors.New("nothing to compact")
	ErrCompactSave        = errors.New("failed to save compacted history")
	// ErrCompactConflict reports that the durable history changed between the
	// compaction snapshot and the write-back (a concurrent writer, e.g. a late
	// run write): the compaction is aborted instead of erasing that work. The
	// UI should ask the user to retry.
	ErrCompactConflict = errors.New("session history changed during compaction")
	// ErrChatCompacting is returned by Send/Regenerate while the session history
	// is being compacted (/compact): compaction is a multi-second model call and
	// appending a turn halfway through would be erased by the history write-back.
	// The draft should be retried once the compaction settles.
	ErrChatCompacting = errors.New("session context is being compacted; retry after /compact finishes")
	// ErrCompactArchive reports that the raw history could not be archived
	// durably, so the compaction was aborted instead of losing messages. The
	// history is unchanged and the command can be retried.
	ErrCompactArchive = errors.New("failed to archive the history being compacted")
	// ErrSessionRunning is returned by ChatService.Send/Regenerate while the
	// session already has a run in flight. It is exposed here (not only in the
	// agent module) so a host can classify it and route the prompt to the
	// session's PromptQueue inbox instead of surfacing a dead end.
	ErrSessionRunning = errors.New("session is already running")
)

// CompactResult reports one manual compaction: the message counts and the
// estimated token counts before/after (0 when unknown).
type CompactResult struct {
	Replaced     int
	Saved        int
	BeforeTokens int
	AfterTokens  int
}

// SessionCompactor is the optional half of the chat service that compacts one
// idle session's persisted history on demand.
type SessionCompactor interface {
	CompactNow(ctx context.Context, sessionID string) (CompactResult, error)
}

// MessageErrorKind values classify a failed turn for localized UI copy.
const (
	MessageErrorKindUnknown   = ""
	MessageErrorKindAuth      = "auth"
	MessageErrorKindRateLimit = "rate_limit"
	MessageErrorKindNetwork   = "network"
	MessageErrorKindTimeout   = "timeout"
	MessageErrorKindContext   = "context"
	// MessageErrorKindIterations marks the assembly-level model/tool iteration
	// cap (agent.Config.MaxIterations) running out: the turn is not a provider
	// failure and the localized copy should point at the limit instead of
	// dumping rig's internal English diagnostic. There is no user settings knob
	// for the cap (v0.2.50 removal); the default assembly is unlimited.
	MessageErrorKindIterations = "iterations"
	// MessageErrorKindLoop is historical: loop protection was removed in
	// v0.2.50, so nothing produces it anymore. The kind stays so a durable
	// run_end persisted by an older version's loop breaker still restores
	// classified, and the UI renders one neutral historical note.
	MessageErrorKindLoop = "loop"
	// MessageErrorKindSandboxDenied marks a failure the OS file sandbox
	// plausibly caused: rig (v0.18.1+) appends a stable "[sandbox: suspected OS
	// sandbox denial …]" attribution line to shell/jobs results that match a
	// known denial dialect (2026-11 audit RG-03). The localized copy should
	// point at the permission mode / OS wall instead of a generic tool failure.
	MessageErrorKindSandboxDenied = "sandbox_denied"
	MessageErrorKindCancelled     = "cancelled"
	MessageErrorKindOther         = "other"
)

// ClassifyError maps one backend error (or its message) to a MessageErrorKind
// by matching the stable provider/network phrases. Rules are checked in order:
// cancellation, the rig OS-sandbox attribution line, auth, rate limit, timeout,
// network, context length, iteration budget, then other.
func ClassifyError(err error, msg string) string {
	text := strings.ToLower(strings.TrimSpace(msg))
	if text == "" && err != nil {
		text = strings.ToLower(err.Error())
	}
	if text == "" {
		return MessageErrorKindOther
	}
	has := func(needles ...string) bool {
		for _, needle := range needles {
			if strings.Contains(text, needle) {
				return true
			}
		}
		return false
	}
	switch {
	case has("context canceled", "context cancelled", "operation was canceled", "operation was cancelled"):
		return MessageErrorKindCancelled
	case has("[sandbox:"):
		// rig v0.18.1+ appends this stable attribution line to shell/jobs
		// results whose stderr matched a known OS-sandbox denial dialect
		// (2026-11 audit RG-03). The explicit marker wins over incidental
		// phrases in the command output; bare denial dialects without the
		// marker stay unclassified (the marker is the attribution signal).
		return MessageErrorKindSandboxDenied
	case has("401", "403", "invalid api key", "unauthorized", "authentication failed", "incorrect api key"):
		return MessageErrorKindAuth
	case has("429", "rate limit", "rate_limit", "quota", "too many requests"):
		return MessageErrorKindRateLimit
	case has("timeout", "timed out", "deadline exceeded"):
		return MessageErrorKindTimeout
	case has("connection refused", "no such host", "network unreachable", "network is unreachable",
		"dns", "connection reset", "tls handshake"):
		return MessageErrorKindNetwork
	case has("context length", "context_length", "token limit", "too many tokens",
		"maximum context", "context window"):
		return MessageErrorKindContext
	case has("max iterations", "iteration limit", "without a final answer",
		"rounds without"):
		return MessageErrorKindIterations
	case has("loop guard:", "loop guard", "no file diff", "identical tool call repeated"):
		// Historical only: an old version's loop breaker persisted these
		// spellings; a restored run_end keeps its classified kind so the UI
		// can render the neutral legacy note.
		return MessageErrorKindLoop
	default:
		return MessageErrorKindOther
	}
}

// MessageUsage is one assistant turn's accounting (rig Usage + wall time).
type MessageUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CachedTokens     int
	Iterations       int
	DurationMS       int64
	// ContextTokens is the prompt-token count of the LAST model request of the
	// turn: the size of the conversation context the model actually saw. Rig
	// accumulates PromptTokens over every model call of the turn, so that
	// number is a billing total and can exceed the context window; ContextTokens
	// is the right value for "context used / remaining" displays. 0 when the
	// provider reported no per-call usage.
	ContextTokens int
}

// Agent operating modes. Plan mode restricts the tool gate to read-only
// exploration plus the planning tools; normal mode uses the permission policy.
const (
	SettingAgentMode = "agent.mode"
	AgentModeNormal  = "normal"
	AgentModePlan    = "plan"
)

// Chat modes are the old shell's standard/minimal/creator tool policies: the
// standard mode keeps the normal approval flow, minimal only allows read-only
// exploration, and creator hides the coding tools (writing persona).
const (
	SettingChatMode  = "chat.mode"
	ChatModeStandard = "standard"
	ChatModeMinimal  = "minimal"
	ChatModeCreator  = "creator"
)

// Attachment is one file or image the user attached to a prompt.
type Attachment struct {
	// ID identifies the chip while it is staged.
	ID string
	// Name is the file name shown in the UI.
	Name string
	// MIME is the detected content type (image/png, text/plain, ...).
	MIME string
	// Data is the raw file content; empty for restored/read-only chips.
	Data []byte
	// Bytes is the persisted size in bytes for a restored chip whose Data is not
	// loaded (the read model keeps the name and size even when the content is not
	// fetched back). Size prefers live Data and falls back to Bytes, so a hot chip
	// and a restored chip report the same size (2026-11 UI review C). Zero means
	// unknown; old records without it still derive the size from Data.
	Bytes int
}

// IsImage reports whether the attachment is an image the provider can accept
// as a content part.
func (a Attachment) IsImage() bool {
	return strings.HasPrefix(a.MIME, "image/")
}

// Size is the attachment size in bytes: the live content length when Data is
// loaded, otherwise the persisted Bytes of a restored chip.
func (a Attachment) Size() int {
	if len(a.Data) > 0 {
		return len(a.Data)
	}
	return a.Bytes
}

// DataURL encodes the attachment as a data URL (used for rig image parts).
func (a Attachment) DataURL() string {
	if len(a.Data) == 0 {
		return ""
	}
	return "data:" + a.MIME + ";base64," + base64.StdEncoding.EncodeToString(a.Data)
}

// AttachmentSender is the optional half of the chat service that accepts file
// and image attachments with a prompt. UI modules type-assert it so a backend
// without attachment support still implements ChatService.
type AttachmentSender interface {
	// SendWithAttachments appends a user message carrying the prompt and the
	// attachments, then starts one asynchronous agent run.
	SendWithAttachments(sessionID, prompt string, attachments []Attachment) error
}

// SessionPinnedSettingKey is the settings row storing a session's pin flag
// (the old shell persisted pins in settings, not in the session schema).
func SessionPinnedSettingKey(sessionID string) string {
	return "session." + sessionID + ".pinned"
}

// SessionModelSwitcher is the optional half of the chat service that changes
// the model of one session (the old shell's composer model switcher).
type SessionModelSwitcher interface {
	// SetSessionModel points sessionID at model for its following runs and
	// persists the choice.
	SetSessionModel(sessionID, model string) error
}

// Session model override settings key.
func SessionModelSettingKey(sessionID string) string {
	return "session." + sessionID + ".model"
}

// SessionRegenerator is the optional half of the chat service that retries the
// last exchange: it drops the trailing assistant reply and runs the last user
// message again (the old shell's 重新生成 action).
type SessionRegenerator interface {
	// Regenerate re-runs the last user message of sessionID.
	Regenerate(sessionID string) error
}

// SessionEditor is the optional half of the chat service that edits an earlier
// user message: it truncates the history to just before that message and runs
// the edited text as a new turn (the old shell's 编辑 + 重发 action).
//
// The durable rig history carries no message ids, so the target is located
// through the read model and then anchored durably by *user-message ordinal
// counted from the tail*, verified by (role, text). Every failure below is a
// refusal, never a silent "start from the beginning": quietly discarding the
// user's history is worse than not editing.
type SessionEditor interface {
	// EditMessage replaces the user message messageID of sessionID with text,
	// drops everything after it and runs the new turn.
	EditMessage(sessionID, messageID, text string) error
}

// Edit refusal vocabulary (SessionEditor.EditMessage). The gateway maps each to
// a parameter error naming editMessageId, so the client can explain instead of
// retrying.
var (
	// ErrEditTargetNotFound means the session or the message does not exist any
	// more (deleted, or the id belongs to a reloaded transcript).
	ErrEditTargetNotFound = errors.New("edit target message not found")
	// ErrEditTargetNotUser means the target is not a user message: editing an
	// assistant message would be "regenerate from here", a different feature.
	ErrEditTargetNotUser = errors.New("edit target is not a user message")
	// ErrEditTargetHasAttachments means the target carried files/images. The run
	// path has no attachment support, so allowing the edit would silently drop
	// them.
	ErrEditTargetHasAttachments = errors.New("edit target has attachments")
	// ErrEditTargetCompacted means the target fell outside the durable history
	// window (a compaction replaced the head), so it cannot be located safely.
	ErrEditTargetCompacted = errors.New("edit target was compacted away")
	// ErrEditTargetMoved means the durable anchor no longer matches the read
	// model: something rewrote the history between the click and the edit.
	ErrEditTargetMoved = errors.New("edit target moved")
)

// SessionArchivedSettingKey is the settings row storing a session's archive
// flag (pins and archives both live in settings, not in the schema).
func SessionArchivedSettingKey(sessionID string) string {
	return "session." + sessionID + ".archived"
}

// SessionUnreadSettingKey is the settings row storing a session's unread
// completion flag ("true" when a run finished while the session was not the
// active one). The agent writes it on completion and clears it when the session
// is opened (SetActiveSession) or a new run starts.
func SessionUnreadSettingKey(sessionID string) string {
	return "session." + sessionID + ".unread"
}

// SessionArchiver is the optional half of the chat service that archives or
// restores a session. UI modules type-assert it.
type SessionArchiver interface {
	// SetSessionArchived persists the session's archive flag.
	SetSessionArchived(sessionID string, archived bool) error
}

// SettingSessionsShowArchived is the old sidebar view option that listed
// archived sessions.
//
// Deprecated: the sidebar always hides archived sessions now; the settings
// surface's Archive page lists them. The key stays defined for older workspaces
// that still carry the setting row.
const SettingSessionsShowArchived = "ui.sessions.showArchived"

// SessionPinner is the optional half of the chat service that pins or
// unpins a session. UI modules type-assert it.
type SessionPinner interface {
	// SetSessionPinned persists the session's pin flag.
	SetSessionPinned(sessionID string, pinned bool) error
}

// SessionAllowedToolsSettingKey is the settings row storing the tool names the
// user chose to always allow in one session (the approval dialog's
// "always allow in this session"). The value is a JSON array; a missing or
// empty row means nothing is remembered.
func SessionAllowedToolsSettingKey(sessionID string) string {
	return "session." + sessionID + ".allowedTools"
}

// SessionToolPermissions is the optional half of the chat service that manages
// the "always allow" tools remembered for one session. The agent backend
// persists the set across runs; the conversation UI shows the remembered
// count and can forget it (permission decisions never touch global settings).
type SessionToolPermissions interface {
	// AllowedTools returns the remembered tool names, sorted and deduplicated
	// (never nil).
	AllowedTools(sessionID string) []string
	// ClearAllowedTools forgets every remembered tool of the session.
	ClearAllowedTools(sessionID string) error
}

// SessionForker is the optional half of the chat service that duplicates a
// conversation into a new session (the old shell's "fork session" action).
type SessionForker interface {
	// ForkSession copies sessionID's messages into a new session, makes the
	// copy active and returns its ID.
	ForkSession(sessionID string) (string, error)
}

// ChatService is the product's conversation service: it owns chat sessions,
// runs the ZeoKeel Rig agent, and exposes a read model plus change
// notifications for the UI.
//
// Concurrency: every method is safe to call from any goroutine. Subscribers are
// called synchronously from the agent goroutine after a session changed; a UI
// module must hop to the UI thread itself (UIRuntime.Post).
type ChatService interface {
	// Config reports the effective model configuration.
	Config() ChatConfig
	// Sessions lists sessions, newest updated first.
	Sessions() []ChatSessionInfo
	// Session returns one session read model.
	Session(sessionID string) (ChatSession, bool)
	// ActiveSession is the session the UI currently shows ("" when none).
	ActiveSession() string
	// SetActiveSession switches the active session.
	SetActiveSession(sessionID string) error
	// NewSession creates an empty session, marks it active and returns its ID.
	NewSession(title string) (string, error)
	// DeleteSession removes a session; the active session becomes the newest
	// remaining one (or "").
	DeleteSession(sessionID string) error
	// Send appends the user prompt and starts one asynchronous agent run.
	// It returns a conflict error when the session is already running.
	Send(sessionID, prompt string) error
	// Cancel stops the in-flight run of a session (idempotent).
	Cancel(sessionID string) error
	// Subscribe receives the session ID whenever a session changed. The
	// returned function unsubscribes; nil callbacks are ignored.
	Subscribe(fn func(sessionID string)) (unsubscribe func(), err error)
}

// SessionRenamer is the optional half of the chat service that renames a
// session's display title. UI modules type-assert it so a host that only
// implements ChatService still works.
type SessionRenamer interface {
	// RenameSession updates the display title of an existing session.
	RenameSession(sessionID, title string) error
}

// KeyChat is provided by the agent backend module (zeokeel/agent) and consumed
// by the chat UI modules.
var KeyChat = cordis.NewServiceKey[ChatService]("zeokeel/chat")
