package acp

// Server -> client request seam.
//
// ACP is bidirectional: the agent may call client-provided methods
// (fs/read_text_file, fs/write_text_file, the terminal/* family) and ask the
// user for permission (session/request_permission). The server owns the
// JSON-RPC connection, so an Agent reaches the client through the Client value
// planted on the request context by the connection (ClientFromContext). The
// connection correlates responses by id and bounds every request with
// Config.ClientRequestTimeout, so an unresponsive client can never hang a turn.
//
// Capability safety: method calls are gated on the clientCapabilities the
// client advertised in initialize. Calling a method the client did not
// advertise returns a method-not-found JSON-RPC error (never a panic) and sends
// nothing on the wire. Config.ClientRequest can replace the transport seam for
// an embedder or a unit test.
//
// Venue contract: server->client requests are only allowed from an
// asynchronous handler (session/prompt, which runs on its own goroutine). A
// synchronous control handler (initialize, session/new, session/list,
// session/load, session/cancel, ...) runs on the connection's read loop, so a
// request there could never receive its response; the seam returns an explicit
// internal error immediately instead of blocking until the request timeout.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Client is the server's seam to the ACP client. Typed methods mirror the ACP
// v1 client methods; each is capability-gated and bounded by the configured
// client-request timeout. Implementations must be safe for concurrent use (one
// connection may run several sessions at once).
type Client interface {
	// Capabilities returns the capabilities the client advertised in
	// initialize (zero value before initialize).
	Capabilities() ClientCapabilities
	// ReadTextFile implements fs/read_text_file.
	ReadTextFile(ctx context.Context, req ReadTextFileRequest) (ReadTextFileResponse, error)
	// WriteTextFile implements fs/write_text_file.
	WriteTextFile(ctx context.Context, req WriteTextFileRequest) (WriteTextFileResponse, error)
	// CreateTerminal implements terminal/create.
	CreateTerminal(ctx context.Context, req CreateTerminalRequest) (CreateTerminalResponse, error)
	// TerminalOutput implements terminal/output.
	TerminalOutput(ctx context.Context, req TerminalOutputRequest) (TerminalOutputResponse, error)
	// WaitForTerminalExit implements terminal/wait_for_exit.
	WaitForTerminalExit(ctx context.Context, req WaitForTerminalExitRequest) (WaitForTerminalExitResponse, error)
	// KillTerminal implements terminal/kill.
	KillTerminal(ctx context.Context, req KillTerminalRequest) (KillTerminalResponse, error)
	// ReleaseTerminal implements terminal/release.
	ReleaseTerminal(ctx context.Context, req ReleaseTerminalRequest) (ReleaseTerminalResponse, error)
	// RequestPermission implements session/request_permission.
	RequestPermission(ctx context.Context, req RequestPermissionRequest) (RequestPermissionResponse, error)
}

// clientContextKey is the context key carrying a Client.
type clientContextKey struct{}

// DefaultClientRequestTimeout bounds one server->client request when Config
// does not set ClientRequestTimeout.
const DefaultClientRequestTimeout = 30 * time.Second

// WithClient returns ctx carrying client for the Agent's server->client calls.
// A nil ctx or client is returned unchanged.
func WithClient(ctx context.Context, client Client) context.Context {
	if ctx == nil || client == nil {
		return ctx
	}
	return context.WithValue(ctx, clientContextKey{}, client)
}

// ClientFromContext returns the Client planted on ctx by the connection.
func ClientFromContext(ctx context.Context) (Client, bool) {
	if ctx == nil {
		return nil, false
	}
	client, ok := ctx.Value(clientContextKey{}).(Client)
	if !ok || client == nil {
		return nil, false
	}
	return client, true
}

// outgoingRequest is one server->client JSON-RPC request frame.
type outgoingRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// incomingResponse is one server<-client JSON-RPC response frame.
type incomingResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *RPCError       `json:"error"`
}

// clientResult is delivered to the waiting requestClient call.
type clientResult struct {
	result json.RawMessage
	err    error
}

// requestClient sends one server->client request and waits for its response.
// Config.ClientRequest, when set, replaces the transport (embedders/tests).
func (c *conn) requestClient(ctx context.Context, method string, params any) (json.RawMessage, error) {
	// Contract guard: a server->client request issued from a synchronous
	// read-loop handler would wait for a response that the blocked read loop
	// can never deliver. Fail fast with an explicit error instead of hanging
	// until ClientRequestTimeout.
	if isSynchronousHandler(ctx) {
		return nil, &RPCError{
			Code: CodeInternalError,
			Message: fmt.Sprintf(
				"acp: server-to-client request %s cannot be issued from a synchronous control handler; issue it from session/prompt",
				method),
		}
	}
	if c.srv.clientRequest != nil {
		return c.srv.clientRequest(ctx, method, params)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout := c.srv.clientTimeout; timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	id := c.nextClientID.Add(1)
	ch := make(chan clientResult, 1)
	c.pendingMu.Lock()
	if c.pending == nil {
		c.pending = make(map[int64]chan clientResult)
	}
	c.pending[id] = ch
	closed := c.closed.Load()
	c.pendingMu.Unlock()
	if closed {
		c.removePending(id)
		return nil, &RPCError{Code: CodeInternalError, Message: "acp: connection is closed"}
	}

	if err := c.out.write(outgoingRequest{JSONRPC: JSONRPCVersion, ID: id, Method: method, Params: params}); err != nil {
		c.removePending(id)
		return nil, fmt.Errorf("acp: write client request %s: %w", method, err)
	}

	select {
	case res := <-ch:
		return res.result, res.err
	case <-ctx.Done():
		c.removePending(id)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, &RPCError{Code: CodeInternalError, Message: fmt.Sprintf("acp: client request %s timed out", method)}
		}
		return nil, &RPCError{Code: CodeRequestCancelled, Message: fmt.Sprintf("acp: client request %s was cancelled", method)}
	}
}

// removePending drops the waiter for id, if still registered.
func (c *conn) removePending(id int64) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

// handleIncomingResponse routes one inbound JSON-RPC response frame to the
// requestClient call waiting for its id. Unknown or late ids are ignored.
func (c *conn) handleIncomingResponse(line []byte) {
	var resp incomingResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		c.srv.logf("acp: decode client response: %v", err)
		return
	}
	var id int64
	if err := json.Unmarshal(resp.ID, &id); err != nil {
		c.srv.logf("acp: client response has a non-numeric id %s", resp.ID)
		return
	}
	c.pendingMu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.pendingMu.Unlock()
	if ch == nil {
		return
	}
	if resp.Error != nil {
		ch <- clientResult{err: resp.Error}
		return
	}
	ch <- clientResult{result: resp.Result}
}

// failPending unblocks every in-flight client request when the connection ends
// so an agent turn cannot hang on a client that went away.
func (c *conn) failPending() {
	c.closed.Store(true)
	c.pendingMu.Lock()
	pending := c.pending
	c.pending = nil
	c.pendingMu.Unlock()
	for _, ch := range pending {
		ch <- clientResult{err: errors.New("acp: connection closed while waiting for the client")}
	}
}

// Capabilities implements Client.
func (c *conn) Capabilities() ClientCapabilities {
	if caps := c.clientCaps.Load(); caps != nil {
		return *caps
	}
	return ClientCapabilities{}
}

// capabilityError builds the JSON-RPC error returned when the client did not
// advertise the capability method needs.
func capabilityError(method, capability string) *RPCError {
	return &RPCError{
		Code:    CodeMethodNotFound,
		Message: fmt.Sprintf("acp: client does not advertise the %s capability required by %s", capability, method),
	}
}

// callClient sends method through the seam and decodes its result into T. An
// empty/null result is an error: ACP responses carry a method-specific object.
func callClient[T any](c *conn, ctx context.Context, method string, params any) (T, error) {
	var out T
	raw, err := c.requestClient(ctx, method, params)
	if err != nil {
		return out, err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return out, &RPCError{Code: CodeInternalError, Message: fmt.Sprintf("acp: client returned an empty result for %s", method)}
	}
	if err := json.Unmarshal(trimmed, &out); err != nil {
		return out, &RPCError{Code: CodeInternalError, Message: fmt.Sprintf("acp: malformed result for %s: %v", method, err)}
	}
	return out, nil
}

// callClientEmpty sends method through the seam and accepts an empty result.
func callClientEmpty(c *conn, ctx context.Context, method string, params any) error {
	raw, err := c.requestClient(ctx, method, params)
	if err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" || string(trimmed) == "{}" || string(trimmed) == `{"_meta":null}` {
		return nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return &RPCError{Code: CodeInternalError, Message: fmt.Sprintf("acp: malformed result for %s: %v", method, err)}
	}
	return nil
}

// ReadTextFile implements Client.
func (c *conn) ReadTextFile(ctx context.Context, req ReadTextFileRequest) (ReadTextFileResponse, error) {
	fs := c.Capabilities().Fs
	if fs == nil || !fs.ReadTextFile {
		return ReadTextFileResponse{}, capabilityError(MethodFSReadTextFile, "fs.readTextFile")
	}
	return callClient[ReadTextFileResponse](c, ctx, MethodFSReadTextFile, req)
}

// WriteTextFile implements Client.
func (c *conn) WriteTextFile(ctx context.Context, req WriteTextFileRequest) (WriteTextFileResponse, error) {
	fs := c.Capabilities().Fs
	if fs == nil || !fs.WriteTextFile {
		return WriteTextFileResponse{}, capabilityError(MethodFSWriteTextFile, "fs.writeTextFile")
	}
	if err := callClientEmpty(c, ctx, MethodFSWriteTextFile, req); err != nil {
		return WriteTextFileResponse{}, err
	}
	return WriteTextFileResponse{}, nil
}

// requireTerminal gates every terminal/* method on the client capability.
func (c *conn) requireTerminal(method string) error {
	if !c.Capabilities().Terminal {
		return capabilityError(method, "terminal")
	}
	return nil
}

// CreateTerminal implements Client.
func (c *conn) CreateTerminal(ctx context.Context, req CreateTerminalRequest) (CreateTerminalResponse, error) {
	if err := c.requireTerminal(MethodTerminalCreate); err != nil {
		return CreateTerminalResponse{}, err
	}
	return callClient[CreateTerminalResponse](c, ctx, MethodTerminalCreate, req)
}

// TerminalOutput implements Client.
func (c *conn) TerminalOutput(ctx context.Context, req TerminalOutputRequest) (TerminalOutputResponse, error) {
	if err := c.requireTerminal(MethodTerminalOutput); err != nil {
		return TerminalOutputResponse{}, err
	}
	return callClient[TerminalOutputResponse](c, ctx, MethodTerminalOutput, req)
}

// WaitForTerminalExit implements Client.
func (c *conn) WaitForTerminalExit(ctx context.Context, req WaitForTerminalExitRequest) (WaitForTerminalExitResponse, error) {
	if err := c.requireTerminal(MethodTerminalWaitForExit); err != nil {
		return WaitForTerminalExitResponse{}, err
	}
	return callClient[WaitForTerminalExitResponse](c, ctx, MethodTerminalWaitForExit, req)
}

// KillTerminal implements Client.
func (c *conn) KillTerminal(ctx context.Context, req KillTerminalRequest) (KillTerminalResponse, error) {
	if err := c.requireTerminal(MethodTerminalKill); err != nil {
		return KillTerminalResponse{}, err
	}
	if err := callClientEmpty(c, ctx, MethodTerminalKill, req); err != nil {
		return KillTerminalResponse{}, err
	}
	return KillTerminalResponse{}, nil
}

// ReleaseTerminal implements Client.
func (c *conn) ReleaseTerminal(ctx context.Context, req ReleaseTerminalRequest) (ReleaseTerminalResponse, error) {
	if err := c.requireTerminal(MethodTerminalRelease); err != nil {
		return ReleaseTerminalResponse{}, err
	}
	if err := callClientEmpty(c, ctx, MethodTerminalRelease, req); err != nil {
		return ReleaseTerminalResponse{}, err
	}
	return ReleaseTerminalResponse{}, nil
}

// RequestPermission implements Client. Permission requests are part of the ACP
// baseline (not capability-gated); an error, timeout or empty outcome is
// reported to the agent, which is expected to fail closed.
func (c *conn) RequestPermission(ctx context.Context, req RequestPermissionRequest) (RequestPermissionResponse, error) {
	out, err := callClient[RequestPermissionResponse](c, ctx, MethodSessionRequestPermission, req)
	if err != nil {
		return out, err
	}
	switch out.Outcome.Outcome {
	case "selected", "cancelled":
		return out, nil
	default:
		return out, &RPCError{Code: CodeInternalError, Message: "acp: client returned no permission outcome"}
	}
}
