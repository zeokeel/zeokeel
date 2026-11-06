package openai

import "encoding/json"

// chatRequest is the OpenAI-compatible chat-completions request body.
//
// Only fields the provider actually sets are modelled; everything else is
// left to the server's defaults.
type chatRequest struct {
	Model           string         `json:"model"`
	Messages        []chatMessage  `json:"messages"`
	Tools           []chatTool     `json:"tools,omitempty"`
	Stream          bool           `json:"stream"`
	StreamOptions   *streamOptions `json:"stream_options,omitempty"`
	Temperature     *float32       `json:"temperature,omitempty"`
	MaxTokens       int            `json:"max_tokens,omitempty"`
	ReasoningEffort string         `json:"reasoning_effort,omitempty"`
}

// streamOptions asks OpenAI-compatible servers to append a final chunk that
// carries token accounting. Without it the stream reports no usage.
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// chatMessage is one wire-format conversation entry. Content is always sent,
// even when empty: some servers reject a message without the field. Content is
// a plain JSON string for text-only messages and an array of parts when the
// runtime attached images.
type chatMessage struct {
	Role       string         `json:"role"`
	Content    chatContent    `json:"content"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// chatContent is a message's content. With no parts it marshals to a plain JSON
// string, byte-identical to the historical text-only request; with parts it
// marshals to the OpenAI multimodal content array.
type chatContent struct {
	text  string
	parts []chatContentPart
}

// MarshalJSON keeps text-only messages as strings and only switches to the
// part array when an image is actually attached.
func (c chatContent) MarshalJSON() ([]byte, error) {
	if len(c.parts) == 0 {
		return json.Marshal(c.text)
	}
	return json.Marshal(c.parts)
}

// chatContentPart is one element of a multimodal content array. Text is a
// pointer so an intentionally empty text part still serializes as "text":"".
type chatContentPart struct {
	Type     string        `json:"type"`
	Text     *string       `json:"text,omitempty"`
	ImageURL *chatImageURL `json:"image_url,omitempty"`
}

// chatImageURL is the image_url payload of an image content part.
type chatImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatFunctionCall `json:"function"`
}

type chatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// chatTool is the OpenAI function-schema declaration of one tool.
type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// streamEvent is one decoded `data:` payload. OpenAI servers put a single
// choice delta in each event; usage arrives in a final event with an empty
// choices list.
type streamEvent struct {
	Choices []streamChoice `json:"choices"`
	Usage   *streamUsage   `json:"usage"`
	Error   *streamError   `json:"error"`
}

type streamChoice struct {
	Delta        streamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type streamDelta struct {
	Content          string           `json:"content"`
	ReasoningContent string           `json:"reasoning_content"`
	Reasoning        string           `json:"reasoning"`
	ToolCalls        []streamToolCall `json:"tool_calls"`
}

type streamToolCall struct {
	Index    int              `json:"index"`
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function streamToolCallFn `json:"function"`
}

type streamToolCallFn struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type streamUsage struct {
	PromptTokens            int                     `json:"prompt_tokens"`
	CompletionTokens        int                     `json:"completion_tokens"`
	TotalTokens             int                     `json:"total_tokens"`
	PromptTokensDetails     *promptTokensDetails    `json:"prompt_tokens_details"`
	CompletionTokensDetails *completionTokenDetails `json:"completion_tokens_details"`
	// PromptCacheHitTokens is DeepSeek's flat spelling of cached prompt
	// tokens; OpenAI nests the same number under prompt_tokens_details.
	PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
}

type promptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type completionTokenDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// streamError is the error object an OpenAI-compatible server may send in the
// place of a delta after the stream has already been established.
type streamError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

func (e *streamError) text() string {
	if e == nil {
		return ""
	}
	switch {
	case e.Message != "" && e.Type != "":
		return e.Type + ": " + e.Message
	case e.Message != "":
		return e.Message
	default:
		return e.Type
	}
}
