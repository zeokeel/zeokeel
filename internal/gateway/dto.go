package gateway

import (
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// The wire shape of the read models.
//
// These DTOs deliberately exist instead of serializing the contracts structs
// directly: the contract types are Go read models (PascalCase fields, some []byte
// payloads, no stability promise), while the browser needs a stable
// lowerCamelCase shape it can generate types from. Everything here is additive
// only — removing a field is a protocol break.
//
// Timestamps use the contract's "zero means unknown" rule: a nil pointer is
// serialized as absent/null instead of a fabricated 0001-01-01 clock.

// sessionInfoDTO is one sidebar row (contracts.ChatSessionInfo).
type sessionInfoDTO struct {
	ID                    string     `json:"id"`
	WorkspaceID           string     `json:"workspaceId,omitempty"`
	Title                 string     `json:"title"`
	Pinned                bool       `json:"pinned"`
	Archived              bool       `json:"archived"`
	MessageCount          int        `json:"messageCount"`
	Running               bool       `json:"running"`
	Waiting               bool       `json:"waiting"`
	Unread                bool       `json:"unread"`
	UpdatedAt             *time.Time `json:"updatedAt,omitempty"`
	Origin                string     `json:"origin,omitempty"`
	PermissionScope       string     `json:"permissionScope,omitempty"`
	EffectiveApprovalMode string     `json:"effectiveApprovalMode,omitempty"`
	EffectiveSandboxMode  string     `json:"effectiveSandboxMode,omitempty"`
	// PresetID is the role the session selected; PresetProblem is a stable code
	// when that role cannot be resolved (the session then has no tools). Both are
	// `omitempty`: a healthy session must not look diagnosed.
	PresetID      string `json:"presetId,omitempty"`
	PresetProblem string `json:"presetProblem,omitempty"`
}

// sessionDTO is one conversation read model (contracts.ChatSession).
type sessionDTO struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Model       string       `json:"model"`
	Messages    []messageDTO `json:"messages"`
	Running     bool         `json:"running"`
	Error       string       `json:"error,omitempty"`
	Interrupted bool         `json:"interrupted"`
	CreatedAt   *time.Time   `json:"createdAt,omitempty"`
	UpdatedAt   *time.Time   `json:"updatedAt,omitempty"`
}

// messageDTO is one transcript message (contracts.ChatMessage).
type messageDTO struct {
	ID                string                `json:"id"`
	Role              string                `json:"role"`
	Text              string                `json:"text"`
	Reasoning         string                `json:"reasoning,omitempty"`
	ReasoningSegments []reasoningSegmentDTO `json:"reasoningSegments,omitempty"`
	Streaming         bool                  `json:"streaming,omitempty"`
	Tools             []toolCallDTO         `json:"tools,omitempty"`
	ToolTimelineKnown bool                  `json:"toolTimelineKnown,omitempty"`
	Attachments       []attachmentDTO       `json:"attachments,omitempty"`
	Usage             *messageUsageDTO      `json:"usage,omitempty"`
	Error             string                `json:"error,omitempty"`
	ErrorKind         string                `json:"errorKind,omitempty"`
	Truncated         bool                  `json:"truncated,omitempty"`
	GoalRound         *goalRoundDTO         `json:"goalRound,omitempty"`
	CreatedAt         *time.Time            `json:"createdAt,omitempty"`
}

// reasoningSegmentDTO is one model iteration's thinking (contracts.ReasoningSegment).
type reasoningSegmentDTO struct {
	Text       string `json:"text"`
	Iteration  int    `json:"iteration"`
	TextOffset int    `json:"textOffset"`
}

// toolCallDTO is one tool card (contracts.ToolCall).
type toolCallDTO struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Status     string            `json:"status"`
	Summary    string            `json:"summary,omitempty"`
	Arguments  string            `json:"arguments,omitempty"`
	Path       string            `json:"path,omitempty"`
	Generated  []toolCallFileDTO `json:"generated,omitempty"`
	TextOffset int               `json:"textOffset,omitempty"`
	StartedAt  *time.Time        `json:"startedAt,omitempty"`
	EndedAt    *time.Time        `json:"endedAt,omitempty"`
	ErrorKind  string            `json:"errorKind,omitempty"`
}

// toolCallFileDTO is one file of a multi-file call (contracts.ToolCallFile).
type toolCallFileDTO struct {
	Path    string `json:"path"`
	Added   bool   `json:"added,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
}

// attachmentDTO is one attachment chip. The bytes are never inlined: the
// snapshot carries the size and a blob id is added when the blob endpoint
// lands (docs/TRANSPORT.md §6.4).
type attachmentDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	MIME  string `json:"mime"`
	Bytes int    `json:"bytes"`
}

// messageUsageDTO is one turn's accounting (contracts.MessageUsage).
type messageUsageDTO struct {
	PromptTokens     int   `json:"promptTokens"`
	CompletionTokens int   `json:"completionTokens"`
	TotalTokens      int   `json:"totalTokens"`
	CachedTokens     int   `json:"cachedTokens,omitempty"`
	Iterations       int   `json:"iterations,omitempty"`
	DurationMS       int64 `json:"durationMs,omitempty"`
	ContextTokens    int   `json:"contextTokens,omitempty"`
}

// goalRoundDTO marks a goal continuation round (contracts.GoalRoundInfo).
type goalRoundDTO struct {
	GoalID        string `json:"goalId,omitempty"`
	Round         int    `json:"round"`
	MaxGoalRounds int    `json:"maxGoalRounds,omitempty"`
}

// timePointer converts a contract timestamp to its wire form: nil when the
// contract recorded no clock (never a guessed one).
func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copied := value
	return &copied
}

// newSessionInfoDTO maps one list row.
func newSessionInfoDTO(info contracts.ChatSessionInfo) sessionInfoDTO {
	return sessionInfoDTO{
		ID:                    info.ID,
		WorkspaceID:           info.WorkspaceID,
		Title:                 info.Title,
		Pinned:                info.Pinned,
		Archived:              info.Archived,
		MessageCount:          info.MessageCount,
		Running:               info.Running,
		Waiting:               info.Waiting,
		Unread:                info.Unread,
		UpdatedAt:             timePointer(info.UpdatedAt),
		Origin:                info.Origin,
		PermissionScope:       info.PermissionScope,
		EffectiveApprovalMode: info.EffectiveApprovalMode,
		EffectiveSandboxMode:  info.EffectiveSandboxMode,
		PresetID:              info.PresetID,
		PresetProblem:         info.PresetProblem,
	}
}

// newSessionListDTO maps the session list.
func newSessionListDTO(infos []contracts.ChatSessionInfo) []sessionInfoDTO {
	out := make([]sessionInfoDTO, 0, len(infos))
	for _, info := range infos {
		out = append(out, newSessionInfoDTO(info))
	}
	return out
}

// newSessionDTO maps one conversation.
func newSessionDTO(session contracts.ChatSession) sessionDTO {
	out := sessionDTO{
		ID:          session.ID,
		Title:       session.Title,
		Model:       session.Model,
		Messages:    make([]messageDTO, 0, len(session.Messages)),
		Running:     session.Running,
		Error:       session.Error,
		Interrupted: session.Interrupted,
		CreatedAt:   timePointer(session.CreatedAt),
		UpdatedAt:   timePointer(session.UpdatedAt),
	}
	for _, message := range session.Messages {
		out.Messages = append(out.Messages, newMessageDTO(message))
	}
	return out
}

// newMessageDTO maps one message.
func newMessageDTO(message contracts.ChatMessage) messageDTO {
	out := messageDTO{
		ID:                message.ID,
		Role:              message.Role,
		Text:              message.Text,
		Reasoning:         message.Reasoning,
		Streaming:         message.Streaming,
		ToolTimelineKnown: message.ToolTimelineKnown,
		Error:             message.Error,
		ErrorKind:         message.ErrorKind,
		Truncated:         message.Truncated,
		GoalRound:         newGoalRoundDTO(message.GoalRound),
		CreatedAt:         timePointer(message.CreatedAt),
	}
	if len(message.ReasoningSegments) > 0 {
		out.ReasoningSegments = make([]reasoningSegmentDTO, 0, len(message.ReasoningSegments))
		for _, segment := range message.ReasoningSegments {
			out.ReasoningSegments = append(out.ReasoningSegments, reasoningSegmentDTO{
				Text:       segment.Text,
				Iteration:  segment.Iteration,
				TextOffset: segment.TextOffset,
			})
		}
	}
	if len(message.Tools) > 0 {
		out.Tools = make([]toolCallDTO, 0, len(message.Tools))
		for _, call := range message.Tools {
			out.Tools = append(out.Tools, newToolCallDTO(call))
		}
	}
	if len(message.Attachments) > 0 {
		out.Attachments = make([]attachmentDTO, 0, len(message.Attachments))
		for _, attachment := range message.Attachments {
			out.Attachments = append(out.Attachments, newAttachmentDTO(attachment))
		}
	}
	if message.Usage != nil {
		usage := newMessageUsageDTO(*message.Usage)
		out.Usage = &usage
	}
	return out
}

// newToolCallDTO maps one tool card.
func newToolCallDTO(call contracts.ToolCall) toolCallDTO {
	out := toolCallDTO{
		ID:         call.ID,
		Name:       call.Name,
		Status:     call.Status,
		Summary:    call.Summary,
		Arguments:  call.Arguments,
		Path:       call.Path,
		TextOffset: call.TextOffset,
		StartedAt:  timePointer(call.StartedAt),
		EndedAt:    timePointer(call.EndedAt),
		ErrorKind:  call.ErrorKind,
	}
	if len(call.Generated) > 0 {
		out.Generated = make([]toolCallFileDTO, 0, len(call.Generated))
		for _, file := range call.Generated {
			out.Generated = append(out.Generated, toolCallFileDTO{
				Path:    file.Path,
				Added:   file.Added,
				Deleted: file.Deleted,
			})
		}
	}
	return out
}

// newAttachmentDTO maps one attachment chip (metadata only).
func newAttachmentDTO(attachment contracts.Attachment) attachmentDTO {
	return attachmentDTO{
		ID:    attachment.ID,
		Name:  attachment.Name,
		MIME:  attachment.MIME,
		Bytes: attachment.Size(),
	}
}

// newMessageUsageDTO maps one turn's accounting.
func newMessageUsageDTO(usage contracts.MessageUsage) messageUsageDTO {
	return messageUsageDTO{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
		CachedTokens:     usage.CachedTokens,
		Iterations:       usage.Iterations,
		DurationMS:       usage.DurationMS,
		ContextTokens:    usage.ContextTokens,
	}
}

// newGoalRoundDTO maps the goal-round marker.
func newGoalRoundDTO(info *contracts.GoalRoundInfo) *goalRoundDTO {
	if info == nil {
		return nil
	}
	return &goalRoundDTO{
		GoalID:        info.GoalID,
		Round:         info.Round,
		MaxGoalRounds: info.MaxGoalRounds,
	}
}
