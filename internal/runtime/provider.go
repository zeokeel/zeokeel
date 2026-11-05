// Package runtime is ZeoKeel's agent runtime. It wraps the eino ADK
// ChatModelAgent + Runner behind a small Go interface and translates the
// agent's streaming output into ZeoKeel state events.
//
// The public surface is intentionally tiny:
//
//	a, err := runtime.New(runtime.Config{Provider: p, Tools: reg})
//	events, err := a.Run(ctx, sessionID, "hello")
//	for ev := range events { ... }
//
// Optional conversation state is attached through Config.History (multi-turn
// history), Config.Compactor + Config.ContextBudget (context compaction),
// Config.CheckpointStore (turn-boundary recovery), Config.Gate (tool
// approvals), Config.ToolReviewer (automatic review before a prompt) and
// Config.ReviewContext (the working directory and project constraints that
// review receives); all default to nil, which keeps an Agent stateless,
// uncompacted, un-gated and prompt-driven. Config.Mode selects the eino agent topology (standard, deep,
// supervisor or planexecute); it can be changed between turns with
// Agent.SetMode.
//
// Everything eino-specific is confined to model_adapter.go, agent.go,
// stream.go, modes.go and mode_deep.go.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Role identifies the author of a chat message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall is a model-requested tool invocation carried on an assistant
// message. It mirrors the information a provider needs to round-trip a
// tool-calling conversation.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ImagePart is one image attached to a Message. URL is an image URL, normally
// a base64 data: URL; Detail is the provider's quality hint ("auto", "low" or
// "high") and defaults to "auto" when empty.
type ImagePart struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// UserAttachment is one file attached to the user turn that is starting. Text
// is the textual content injected into the prompt (the caller applies the size
// cap); Image, when set, travels as a multimodal image part; Path, when set,
// is a read-only stored copy the model should read with read_file/read_image
// instead of receiving the bytes inline (FP-14: binary/large attachments).
// Name is the display name used in the injected text block.
type UserAttachment struct {
	Name  string
	Text  string
	Image *ImagePart
	// Path is the absolute path of the stored read-only attachment copy.
	Path string
	// MIME and Size describe the stored copy when Path is set.
	MIME string
	Size int64
}

// attachmentNameEscaper escapes the characters that would otherwise break the
// `<attachment name="...">` header. Only the name is escaped; the attachment
// body stays verbatim between the opening and closing tags.
var attachmentNameEscaper = strings.NewReplacer(
	"&", "&amp;",
	`"`, "&quot;",
	"<", "&lt;",
	">", "&gt;",
)

// escapeAttachmentName makes a file name safe to embed in the quoted name
// attribute of the injected attachment header.
func escapeAttachmentName(name string) string {
	return attachmentNameEscaper.Replace(name)
}

// AttachmentPrompt returns text extended with the attachment blocks the model
// sees for one turn. Text attachments are appended after the user's text; a
// turn without text attachments is returned unchanged. Image parts are handled
// separately by AttachmentImages. The attachment name is XML-escaped so a
// quote or angle bracket in a file name cannot break the header.
func AttachmentPrompt(text string, attachments []UserAttachment) string {
	if len(attachments) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(text)
	changed := false
	for _, att := range attachments {
		name := strings.TrimSpace(att.Name)
		if name == "" {
			name = "attachment"
		}
		if att.Text == "" && att.Path != "" {
			// No inline bytes: reference the stored read-only copy and let the
			// model fetch exactly what it needs. This keeps binary and large
			// attachments out of the prompt/context entirely.
			changed = true
			b.WriteString("\n\n<attachment name=\"")
			b.WriteString(escapeAttachmentName(name))
			b.WriteString("\" path=\"")
			b.WriteString(escapeAttachmentName(att.Path))
			b.WriteString("\"")
			if att.MIME != "" {
				b.WriteString(" mime=\"")
				b.WriteString(escapeAttachmentName(att.MIME))
				b.WriteString("\"")
			}
			if att.Size > 0 {
				b.WriteString(" size=\"")
				b.WriteString(fmt.Sprintf("%d", att.Size))
				b.WriteString("\"")
			}
			b.WriteString(" />\n")
			b.WriteString("The file itself was not inlined in this message; its read-only copy is at the path above. ")
			b.WriteString("Read it with read_file, or with read_image when it is an image. Do not modify the stored copy; write any output to the workspace instead.")
			continue
		}
		if att.Text == "" {
			continue
		}
		changed = true
		b.WriteString("\n\n<attachment name=\"")
		b.WriteString(escapeAttachmentName(name))
		b.WriteString("\">\n")
		b.WriteString(att.Text)
		if !strings.HasSuffix(att.Text, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("</attachment>")
	}
	if !changed {
		return text
	}
	return b.String()
}

// AttachmentImages returns the image parts carried by attachments, in order.
func AttachmentImages(attachments []UserAttachment) []ImagePart {
	var images []ImagePart
	for _, att := range attachments {
		if att.Image == nil || att.Image.URL == "" {
			continue
		}
		part := *att.Image
		if strings.TrimSpace(part.Detail) == "" {
			part.Detail = "auto"
		}
		images = append(images, part)
	}
	return images
}

// Message is one entry of the conversation handed to a Provider.
type Message struct {
	Role      Role       `json:"role"`
	Content   string     `json:"content,omitempty"`
	Reasoning string     `json:"reasoning,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// Images carries multimodal image parts for user/assistant messages. It is
	// ignored for tool/system messages, which stay text-only on the wire.
	Images []ImagePart `json:"images,omitempty"`
	// ToolCallID/ToolName are set on RoleTool messages.
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
}

// ToolSpec is the model-facing declaration of a tool.
type ToolSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Parameters is an optional JSON Schema for the tool arguments. It is nil
	// when the tool takes no arguments.
	Parameters json.RawMessage `json:"parameters,omitempty"`
}

// Request is one streaming completion request.
type Request struct {
	// SessionID routes the request to per-session provider state (prompt
	// cache, conversation affinity). It is informational for most providers.
	SessionID string     `json:"session_id,omitempty"`
	Model     string     `json:"model,omitempty"`
	Messages  []Message  `json:"messages"`
	Tools     []ToolSpec `json:"tools,omitempty"`
}

// Usage reports token accounting for one completion. Providers that do not
// report usage leave the zero value; the runtime does not fabricate numbers.
type Usage struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	CachedInputTokens int `json:"cached_input_tokens,omitempty"`
	ReasoningTokens   int `json:"reasoning_tokens,omitempty"`
	TotalTokens       int `json:"total_tokens"`
}

// Chunk is one incremental piece of a streamed completion.
//
// Exactly one of Text, Reasoning, ToolCalls, Usage or Err is normally
// populated per chunk; Done marks the last chunk of the stream. A provider
// must close the returned channel after sending a chunk with Done=true or
// Err!=nil.
//
// ToolCalls on a chunk are complete calls: ID, Name and the full JSON
// arguments are all present. A provider that receives tool-call deltas from
// its upstream API must accumulate them and emit whole calls, because the
// runtime does not correlate fragments across chunks.
type Chunk struct {
	Text      string
	Reasoning string
	ToolCalls []ToolCall
	Usage     *Usage
	Done      bool
	Err       error
}

// Provider streams a completion for req. Stream returns once the stream is
// established; the returned channel carries chunks until completion. The
// channel must be closed by the provider. Cancelling ctx must abort the
// stream and close the channel promptly.
type Provider interface {
	Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}
