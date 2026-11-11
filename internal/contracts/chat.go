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
	// SettingsChatMaxIterations overrides the agent module's MaxIterations
	// (model/tool rounds per run); empty or a non-positive value follows the
	// module config.
	SettingsChatMaxIterations = "chat.maxIterations"
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
	Reasoning string
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
	// auth, rate_limit, network, timeout, context, iterations, cancelled or
	// other).
	ErrorKind string `json:"errorKind,omitempty"`
	// Truncated reports that the provider cut the answer at the output-token
	// limit (finish_reason "length"): the streamed text is a partial answer, not
	// a complete reply. The agent records it on the message and in the durable
	// run_end event so a restart keeps the hint.
	Truncated bool `json:"truncated,omitempty"`
	// CreatedAt is the message timestamp (local time, display only).
	CreatedAt time.Time
}

// ChatSession is the read model of one conversation.
type ChatSession struct {
	ID    string
	Title string
	// Model is the effective model for this session: the session override when
	// one was chosen in the composer, else the configured chat.model.
	Model     string
	Messages  []ChatMessage
	Running   bool
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
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
	UpdatedAt    time.Time
	// Origin records who created the session: "" (a user session), "automation"
	// (an automation-run session) or "subagent" (a child conversation once
	// child sessions become durable). Sidebar surfaces hide non-user origins.
	Origin string
}

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
	Model         string
	BaseURL       string
	APIKey        string
	SystemPrompt  string
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
	// MessageErrorKindIterations marks the model/tool iteration budget
	// (chat.maxIterations) running out: the turn is not a provider failure and
	// the localized copy should point at the limit instead of dumping rig's
	// internal English diagnostic.
	MessageErrorKindIterations = "iterations"
	MessageErrorKindCancelled  = "cancelled"
	MessageErrorKindOther      = "other"
)

// ClassifyError maps one backend error (or its message) to a MessageErrorKind
// by matching the stable provider/network phrases. Rules are checked in order:
// cancellation, auth, rate limit, timeout, network, context length, iteration
// budget, then other.
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
}

// IsImage reports whether the attachment is an image the provider can accept
// as a content part.
func (a Attachment) IsImage() bool {
	return strings.HasPrefix(a.MIME, "image/")
}

// Size is the attachment size in bytes.
func (a Attachment) Size() int { return len(a.Data) }

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

// SessionArchivedSettingKey is the settings row storing a session's archive
// flag (pins and archives both live in settings, not in the schema).
func SessionArchivedSettingKey(sessionID string) string {
	return "session." + sessionID + ".archived"
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
