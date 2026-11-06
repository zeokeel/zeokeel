package acp

// Full ACP v1 surface: method names, protocol types and constants for
// session/list, session/load, authenticate, session/request_permission and the
// client-provided fs/* + terminal/* methods. The transport-level types
// (request/response/notification) live in protocol.go; the server->client
// request seam lives in client.go; the session/auth handlers live in
// session_ops.go.
//
// Shapes follow the ACP v1 JSON schema (agentclientprotocol.com): camelCase
// wire fields, absolute cwd paths, string session ids and "selected"/
// "cancelled" permission outcomes.

import "encoding/json"

// ACP v1 method names added by the full surface.
const (
	// MethodAuthenticate selects an advertised authentication method.
	MethodAuthenticate = "authenticate"
	// MethodSessionList lists persisted sessions the agent owns.
	MethodSessionList = "session/list"
	// MethodSessionLoad restores an existing session and replays its history.
	MethodSessionLoad = "session/load"
	// MethodSessionRequestPermission is a server->client approval prompt.
	MethodSessionRequestPermission = "session/request_permission"

	// Client file-system methods (server->client).
	MethodFSReadTextFile  = "fs/read_text_file"
	MethodFSWriteTextFile = "fs/write_text_file"

	// Client terminal methods (server->client).
	MethodTerminalCreate      = "terminal/create"
	MethodTerminalOutput      = "terminal/output"
	MethodTerminalWaitForExit = "terminal/wait_for_exit"
	MethodTerminalKill        = "terminal/kill"
	MethodTerminalRelease     = "terminal/release"
)

// ACP-specific JSON-RPC error codes (the reserved -32000..-32099 range).
const (
	// CodeRequestCancelled reports a request cancelled by the peer.
	CodeRequestCancelled = -32800
	// CodeAuthRequired reports that authentication is required or failed.
	CodeAuthRequired = -32000
	// CodeResourceNotFound reports an unknown session/resource id.
	CodeResourceNotFound = -32002
)

// Session update discriminator for replayed user messages (session/load).
const UpdateUserMessageChunk = "user_message_chunk"

// Permission option kinds (and the option ids the ZeoKeel server uses for
// them, so a client can echo the selected kind back verbatim).
const (
	PermissionKindAllowOnce    = "allow_once"
	PermissionKindAllowAlways  = "allow_always"
	PermissionKindRejectOnce   = "reject_once"
	PermissionKindRejectAlways = "reject_always"
)

// PermissionOptions returns the standard four permission choices the server
// offers on every session/request_permission. Option ids equal option kinds so
// the selected option maps onto the approval decision without a side table.
func PermissionOptions() []PermissionOption {
	return []PermissionOption{
		{OptionID: PermissionKindAllowOnce, Name: "Allow once", Kind: PermissionKindAllowOnce},
		{OptionID: PermissionKindAllowAlways, Name: "Always allow", Kind: PermissionKindAllowAlways},
		{OptionID: PermissionKindRejectOnce, Name: "Reject", Kind: PermissionKindRejectOnce},
		{OptionID: PermissionKindRejectAlways, Name: "Always reject", Kind: PermissionKindRejectAlways},
	}
}

// AuthMethod describes one authentication method advertised by initialize.
type AuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// SessionCapabilities is the session lifecycle capability set an agent may
// advertise in initialize.
type SessionCapabilities struct {
	// List advertises session/list support when non-nil.
	List *SessionListCapabilities `json:"list,omitempty"`
}

// SessionListCapabilities advertises session/list (an empty capability object).
type SessionListCapabilities struct{}

// ListSessionsRequest is the params of session/list.
type ListSessionsRequest struct {
	// Cwd filters the sessions by absolute workspace path (optional).
	Cwd string `json:"cwd,omitempty"`
	// Cursor is an opaque pagination token (accepted, currently unused).
	Cursor string `json:"cursor,omitempty"`
}

// SessionInfo is one session/list entry.
type SessionInfo struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Title     string `json:"title,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// ListSessionsResponse is the result of session/list. Sessions are expected
// most-recently-updated first.
type ListSessionsResponse struct {
	Sessions   []SessionInfo `json:"sessions"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

// LoadSessionRequest is the params of session/load.
type LoadSessionRequest struct {
	SessionID             string      `json:"sessionId"`
	Cwd                   string      `json:"cwd"`
	McpServers            []McpServer `json:"mcpServers,omitempty"`
	AdditionalDirectories []string    `json:"additionalDirectories,omitempty"`
}

// LoadSessionResponse is the (empty) result of session/load.
type LoadSessionResponse struct{}

// AuthenticateRequest is the params of authenticate.
type AuthenticateRequest struct {
	MethodID string `json:"methodId"`
}

// AuthenticateResponse is the (empty) result of authenticate.
type AuthenticateResponse struct{}

// EnvVariable is one environment variable of a terminal/create request.
type EnvVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ReadTextFileRequest is a server->client fs/read_text_file request.
type ReadTextFileRequest struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	// Line starts the read at the 1-based line number (0: from the start).
	Line int `json:"line,omitempty"`
	// Limit caps the returned line count (0: to the end).
	Limit int `json:"limit,omitempty"`
}

// ReadTextFileResponse is the result of fs/read_text_file.
type ReadTextFileResponse struct {
	Content string `json:"content"`
}

// WriteTextFileRequest is a server->client fs/write_text_file request.
type WriteTextFileRequest struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Content   string `json:"content"`
}

// WriteTextFileResponse is the (empty) result of fs/write_text_file.
type WriteTextFileResponse struct{}

// CreateTerminalRequest is a server->client terminal/create request.
type CreateTerminalRequest struct {
	SessionID       string        `json:"sessionId"`
	Command         string        `json:"command"`
	Args            []string      `json:"args,omitempty"`
	Env             []EnvVariable `json:"env,omitempty"`
	Cwd             string        `json:"cwd,omitempty"`
	OutputByteLimit int           `json:"outputByteLimit,omitempty"`
}

// CreateTerminalResponse is the result of terminal/create.
type CreateTerminalResponse struct {
	TerminalID string `json:"terminalId"`
}

// TerminalOutputRequest is a server->client terminal/output request.
type TerminalOutputRequest struct {
	SessionID  string `json:"sessionId"`
	TerminalID string `json:"terminalId"`
}

// TerminalExitStatus describes how a terminal command ended.
type TerminalExitStatus struct {
	ExitCode *int   `json:"exitCode,omitempty"`
	Signal   string `json:"signal,omitempty"`
}

// TerminalOutputResponse is the result of terminal/output.
type TerminalOutputResponse struct {
	Output     string              `json:"output"`
	Truncated  bool                `json:"truncated"`
	ExitStatus *TerminalExitStatus `json:"exitStatus,omitempty"`
}

// WaitForTerminalExitRequest is a server->client terminal/wait_for_exit
// request.
type WaitForTerminalExitRequest struct {
	SessionID  string `json:"sessionId"`
	TerminalID string `json:"terminalId"`
}

// WaitForTerminalExitResponse is the result of terminal/wait_for_exit.
type WaitForTerminalExitResponse struct {
	ExitCode *int   `json:"exitCode,omitempty"`
	Signal   string `json:"signal,omitempty"`
}

// KillTerminalRequest is a server->client terminal/kill request.
type KillTerminalRequest struct {
	SessionID  string `json:"sessionId"`
	TerminalID string `json:"terminalId"`
}

// KillTerminalResponse is the (empty) result of terminal/kill.
type KillTerminalResponse struct{}

// ReleaseTerminalRequest is a server->client terminal/release request.
type ReleaseTerminalRequest struct {
	SessionID  string `json:"sessionId"`
	TerminalID string `json:"terminalId"`
}

// ReleaseTerminalResponse is the (empty) result of terminal/release.
type ReleaseTerminalResponse struct{}

// ToolCallKind is the ACP tool-call classification used in permission
// prompts. Only the kinds the adapter maps are defined.
type ToolCallKind = string

// Tool call kinds.
const (
	ToolKindEdit    ToolCallKind = "edit"
	ToolKindExecute ToolCallKind = "execute"
	ToolKindFetch   ToolCallKind = "fetch"
	ToolKindOther   ToolCallKind = "other"
)

// ToolCallUpdate identifies the tool call an approval request is about.
type ToolCallUpdate struct {
	ToolCallID string          `json:"toolCallId"`
	Kind       string          `json:"kind,omitempty"`
	Status     string          `json:"status,omitempty"`
	Title      string          `json:"title,omitempty"`
	RawInput   json.RawMessage `json:"rawInput,omitempty"`
}

// PermissionOption is one choice offered to the client's user.
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

// RequestPermissionRequest is the params of session/request_permission
// (server->client).
type RequestPermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  ToolCallUpdate     `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

// RequestPermissionOutcome is the user's decision: either "selected" with an
// optionId, or "cancelled".
type RequestPermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

// RequestPermissionResponse is the result of session/request_permission.
type RequestPermissionResponse struct {
	Outcome RequestPermissionOutcome `json:"outcome"`
}
