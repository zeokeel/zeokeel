package acp

import (
	"encoding/json"
	"fmt"
)

// JSONRPCVersion is the only JSON-RPC version this server speaks.
const JSONRPCVersion = "2.0"

// ProtocolVersion is the ACP protocol version advertised by initialize.
const ProtocolVersion = 1

// JSON-RPC 2.0 error codes used by the transport.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// RPCError is one JSON-RPC 2.0 error object. It implements error so handlers
// can return it directly and the loop can answer with its code.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error implements error.
func (e *RPCError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message)
}

// request is one JSON-RPC request frame. ID is raw so string and number ids
// are echoed byte-for-byte, as the spec requires.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// response is one JSON-RPC response frame. Exactly one of Result/Error is set.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// notification is one JSON-RPC notification frame (no id).
type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// ACP method names (the minimum automation surface).
const (
	MethodInitialize    = "initialize"
	MethodSessionNew    = "session/new"
	MethodSessionPrompt = "session/prompt"
	MethodSessionCancel = "session/cancel"
	MethodSessionUpdate = "session/update"
)

// ACP stop reasons for session/prompt.
const (
	StopReasonEndTurn   = "end_turn"
	StopReasonCancelled = "cancelled"
	StopReasonMaxTokens = "max_tokens"
	StopReasonRefusal   = "refusal"
)

// Session update discriminators for the session/update notification projection
// (a deliberately small subset of ACP v1).
const (
	UpdateAgentMessageChunk = "agent_message_chunk"
	UpdateAgentThoughtChunk = "agent_thought_chunk"
	UpdateToolCall          = "tool_call"
	UpdateToolCallUpdate    = "tool_call_update"
	UpdateUsage             = "usage_update"
	UpdatePlan              = "plan"
)

// Implementation identifies one protocol endpoint.
type Implementation struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

// ClientCapabilities is the capability set an ACP client may advertise.
type ClientCapabilities struct {
	Fs       *FsCapabilities `json:"fs,omitempty"`
	Terminal bool            `json:"terminal,omitempty"`
}

// FsCapabilities advertises the client file-system surface (unused by the
// minimum server; kept for forward compatibility).
type FsCapabilities struct {
	ReadTextFile  bool `json:"readTextFile,omitempty"`
	WriteTextFile bool `json:"writeTextFile,omitempty"`
}

// InitializeRequest is the params of initialize.
type InitializeRequest struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities,omitempty"`
	ClientInfo         Implementation     `json:"clientInfo,omitempty"`
}

// PromptCapabilities advertises which prompt content types are accepted.
type PromptCapabilities struct {
	Image           bool `json:"image,omitempty"`
	Audio           bool `json:"audio,omitempty"`
	EmbeddedContext bool `json:"embeddedContext,omitempty"`
}

// AgentCapabilities is the initialize capability result.
type AgentCapabilities struct {
	LoadSession        bool               `json:"loadSession,omitempty"`
	PromptCapabilities PromptCapabilities `json:"promptCapabilities"`
}

// InitializeResponse is the result of initialize.
type InitializeResponse struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
	AgentInfo         Implementation    `json:"agentInfo,omitempty"`
	AuthMethods       []string          `json:"authMethods,omitempty"`
}

// McpServer declares one MCP server mounted into a new session. The minimum
// server validates but does not start these; the runtime that owns tools is
// responsible for that (see doc.go).
type McpServer struct {
	Name    string            `json:"name"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     []string          `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// NewSessionRequest is the params of session/new.
type NewSessionRequest struct {
	Cwd        string      `json:"cwd"`
	McpServers []McpServer `json:"mcpServers,omitempty"`
}

// NewSessionResponse is the result of session/new.
type NewSessionResponse struct {
	SessionID string `json:"sessionId"`
}

// ContentBlock is one prompt content block. The minimum server accepts text
// only; image is defined so the capability handshake and validation are
// testable.
type ContentBlock struct {
	Type     string `json:"type"` // "text" | "image"
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// PromptRequest is the params of session/prompt.
type PromptRequest struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

// PromptResponse is the result of session/prompt: it settles only after the
// turn's updates were delivered.
type PromptResponse struct {
	StopReason string `json:"stopReason"`
}

// CancelNotification is the params of session/cancel (a JSON-RPC
// notification in ACP; the minimum server also accepts it as a request).
type CancelNotification struct {
	SessionID string `json:"sessionId"`
}

// Usage is the token accounting an adapter can project as a usage_update.
type Usage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`
}

// SessionUpdate is one semantic session update; a single struct keeps the
// minimum projection readable while allowing the ACP sub-shapes to grow.
type SessionUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"`
	Content       *ContentBlock   `json:"content,omitempty"`
	Title         string          `json:"title,omitempty"`
	ToolCallID    string          `json:"toolCallId,omitempty"`
	Status        string          `json:"status,omitempty"`
	RawInput      json.RawMessage `json:"rawInput,omitempty"`
	Usage         *Usage          `json:"usage,omitempty"`
}

// SessionUpdateNotification is the params of session/update.
type SessionUpdateNotification struct {
	SessionID string        `json:"sessionId"`
	Update    SessionUpdate `json:"update"`
}
