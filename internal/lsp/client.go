package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zeokeel/zeokeel/internal/winproc"
)

const (
	// clientName/clientVersion identify ZeoKeel in the initialize handshake.
	clientName    = "zeokeel"
	clientVersion = "1.0.0"

	// shutdownTimeout bounds the graceful shutdown request; exitGrace is how
	// long close waits for the child to exit before killing it.
	shutdownTimeout = 3 * time.Second
	exitGrace       = time.Second

	// stderrTailBytes is how much of a crashed server's stderr is kept for the
	// error message. Server stderr is diagnostic data; it is bounded and never
	// written to stdout.
	stderrTailBytes = 4 * 1024
)

// rpcOutcome is one response delivered to a waiting request.
type rpcOutcome struct {
	result json.RawMessage
	err    *wireError
}

// serverCapabilities is the subset of initialize capabilities that decides
// which operations the server supports.
type serverCapabilities struct {
	DefinitionProvider     json.RawMessage `json:"definitionProvider"`
	ReferencesProvider     json.RawMessage `json:"referencesProvider"`
	ImplementationProvider json.RawMessage `json:"implementationProvider"`
	HoverProvider          json.RawMessage `json:"hoverProvider"`
}

// supports reports whether the server advertised op.
func (caps serverCapabilities) supports(op Operation) bool {
	switch op {
	case OpGoToDefinition:
		return capabilityEnabled(caps.DefinitionProvider)
	case OpFindReferences:
		return capabilityEnabled(caps.ReferencesProvider)
	case OpGoToImplementation:
		return capabilityEnabled(caps.ImplementationProvider)
	case OpHover:
		return capabilityEnabled(caps.HoverProvider)
	default:
		return false
	}
}

// capabilityEnabled interprets an LSP capability value: false/null mean "not
// supported", true or an options object mean "supported".
func capabilityEnabled(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}
	switch string(trimmed) {
	case "false", "null":
		return false
	}
	return true
}

// client is one initialized stdio language-server session. It is not shared
// between queries: StdioProvider creates and closes one per Query, so a broken
// server cannot affect another file or language.
type client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *tailBuffer

	// writeMu serializes framed writes from the request path and the read
	// loop (which answers server-initiated requests).
	writeMu sync.Mutex

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan rpcOutcome
	readErr error
	caps    serverCapabilities

	readerDone chan struct{}

	// rootURI is the configured ServerConfig.RootURI override sent in the
	// initialize handshake (empty means "use the workspace root").
	rootURI string

	waitOnce sync.Once
	waitErr  error

	killOnce  sync.Once
	closeOnce sync.Once
}

// startClient spawns server, starts the JSON-RPC read loop and performs the
// initialize/initialized handshake. On any error the child is killed and
// reaped before returning.
func startClient(ctx context.Context, server ServerConfig, root string) (*client, error) {
	command := strings.TrimSpace(server.Command)
	if command == "" {
		return nil, errors.New(`language server "command" is empty`)
	}
	// CommandContext kills the child when the query context expires; the read
	// loop reaps it through Wait.
	cmd := exec.CommandContext(ctx, command, server.Args...)
	if root = strings.TrimSpace(root); root != "" {
		cmd.Dir = root
	}
	// The desktop app runs as a GUI executable; hide the child's console window
	// on Windows (a no-op elsewhere).
	winproc.Hide(cmd)
	// A configured server must not be able to read ZeoKeel secrets from the
	// inherited environment; explicit per-server Env overrides are applied
	// after scrubbing, so configuration remains the only way to pass one.
	env := winproc.ScrubEnv(os.Environ())
	if len(server.Env) > 0 {
		env = append(env, server.Env...)
	}
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("language server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("language server stdout: %w", err)
	}
	stderr := &tailBuffer{limit: stderrTailBytes}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start %q: %w", command, err)
	}
	session := &client{
		cmd:        cmd,
		rootURI:    strings.TrimSpace(server.RootURI),
		stdin:      stdin,
		stdout:     bufio.NewReaderSize(stdout, 64*1024),
		stderr:     stderr,
		pending:    make(map[int64]chan rpcOutcome),
		readerDone: make(chan struct{}),
	}
	go session.readLoop()
	if err := session.initialize(ctx, root); err != nil {
		session.kill()
		session.wait()
		_ = stdin.Close()
		return nil, err
	}
	return session, nil
}

// readLoop dispatches server messages until the stream ends, then reaps the
// child and releases waiters. Server-initiated requests are answered (LSP
// servers may block their response until the client replies to
// workspace/configuration); notifications are ignored.
func (c *client) readLoop() {
	for {
		msg, err := readMessage(c.stdout)
		if err != nil {
			c.mu.Lock()
			if c.readErr == nil {
				c.readErr = err
			}
			c.mu.Unlock()
			break
		}
		if msg.Method != "" {
			if len(msg.ID) > 0 && !isJSONNull(msg.ID) {
				c.replyToServer(msg)
			}
			continue
		}
		var id int64
		if err := json.Unmarshal(msg.ID, &id); err != nil {
			continue
		}
		c.mu.Lock()
		waiter := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if waiter == nil {
			continue
		}
		outcome := rpcOutcome{result: msg.Result, err: msg.Error}
		waiter <- outcome
	}
	c.wait()
	close(c.readerDone)
}

// replyToServer answers a server-initiated request. workspace/configuration
// gets one null (unconfigured) entry per requested item; everything else gets
// a null result, which is a valid response for the capability registration
// and progress requests servers commonly send.
func (c *client) replyToServer(msg wireMessage) {
	result := json.RawMessage("null")
	if msg.Method == "workspace/configuration" {
		result = configurationReply(msg.Params)
	}
	_ = c.write(wireMessage{JSONRPC: "2.0", ID: msg.ID, Result: result})
}

// configurationReply returns a JSON array of nulls matching the number of
// requested configuration items (an empty array when the request is malformed).
func configurationReply(params json.RawMessage) json.RawMessage {
	var request struct {
		Items []json.RawMessage `json:"items"`
	}
	count := 0
	if err := json.Unmarshal(params, &request); err == nil {
		count = len(request.Items)
	}
	entries := make([]json.RawMessage, count)
	for i := range entries {
		entries[i] = json.RawMessage("null")
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return json.RawMessage("[]")
	}
	return encoded
}

// write sends one framed message, serialized with the read loop's replies.
func (c *client) write(msg wireMessage) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := writeMessage(c.stdin, msg); err != nil {
		return fmt.Errorf("write %s: %w", msg.Method, err)
	}
	return nil
}

// notify sends a notification (no id, no response expected).
func (c *client) notify(method string, params any) error {
	msg := wireMessage{JSONRPC: "2.0", Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("encode %s: %w", method, err)
		}
		msg.Params = raw
	}
	return c.write(msg)
}

// request sends one request and waits for its response, the context deadline,
// or the child exiting.
func (c *client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id := atomic.AddInt64(&c.nextID, 1)
	waiter := make(chan rpcOutcome, 1)
	c.mu.Lock()
	if c.readErr != nil {
		c.mu.Unlock()
		return nil, c.connectionError()
	}
	c.pending[id] = waiter
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	msg := wireMessage{JSONRPC: "2.0", ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", method, err)
		}
		msg.Params = raw
	}
	if err := c.write(msg); err != nil {
		return nil, err
	}

	select {
	case outcome := <-waiter:
		if outcome.err != nil {
			return nil, outcome.err
		}
		return outcome.result, nil
	case <-ctx.Done():
		// A timed-out request kills the server: a server that ignored a
		// bounded deadline is not trusted to answer the next query.
		c.kill()
		return nil, ctx.Err()
	case <-c.readerDone:
		select {
		case outcome := <-waiter:
			if outcome.err != nil {
				return nil, outcome.err
			}
			return outcome.result, nil
		default:
		}
		return nil, c.connectionError()
	}
}

// initialize performs the LSP handshake and records the server capabilities.
func (c *client) initialize(ctx context.Context, root string) error {
	params := map[string]any{
		"processId":  os.Getpid(),
		"clientInfo": map[string]string{"name": clientName, "version": clientVersion},
		"capabilities": map[string]any{
			"workspace": map[string]any{
				"configuration":    true,
				"workspaceFolders": true,
			},
			"textDocument": map[string]any{
				"definition":     map[string]any{"linkSupport": true},
				"references":     map[string]any{},
				"implementation": map[string]any{"linkSupport": true},
				"hover":          map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
			},
		},
	}
	// ServerConfig.RootURI overrides the workspace root in the handshake (some
	// servers need a repository/subtree root). Without it the workspace root is
	// the rootUri, preserving the default.
	rootURI := c.rootURI
	if rootURI == "" && root != "" {
		rootURI = PathToURI(root)
	}
	if rootURI != "" {
		params["rootUri"] = rootURI
		folderName := ""
		if root != "" {
			params["rootPath"] = root
			folderName = filepath.Base(root)
		}
		if overridePath, err := URIToPath(rootURI); err == nil && strings.TrimSpace(overridePath) != "" {
			folderName = filepath.Base(overridePath)
		}
		if folderName == "" {
			folderName = clientName
		}
		params["workspaceFolders"] = []map[string]string{
			{"uri": rootURI, "name": folderName},
		}
	}
	raw, err := c.request(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	var initialized struct {
		Capabilities serverCapabilities `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &initialized); err != nil {
		return fmt.Errorf("initialize: invalid result: %w", err)
	}
	c.caps = initialized.Capabilities
	if err := c.notify("initialized", map[string]any{}); err != nil {
		return fmt.Errorf("initialized notification: %w", err)
	}
	return nil
}

// didOpen announces the queried file's current content.
func (c *client) didOpen(uri, languageID, text string) error {
	return c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": languageID,
			"version":    1,
			"text":       text,
		},
	})
}

// didClose closes the transiently opened document.
func (c *client) didClose(uri string) {
	_ = c.notify("textDocument/didClose", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
}

// query sends one textDocument request at pos.
func (c *client) query(ctx context.Context, op Operation, uri string, pos Position) (json.RawMessage, error) {
	params := map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": pos.Line, "character": pos.Character},
	}
	var method string
	switch op {
	case OpGoToDefinition:
		method = "textDocument/definition"
	case OpFindReferences:
		method = "textDocument/references"
		params["context"] = map[string]any{"includeDeclaration": true}
	case OpGoToImplementation:
		method = "textDocument/implementation"
	case OpHover:
		method = "textDocument/hover"
	default:
		return nil, fmt.Errorf("unsupported operation %q", op)
	}
	return c.request(ctx, method, params)
}

// kill forcibly terminates the child. It is idempotent.
func (c *client) kill() {
	c.killOnce.Do(func() {
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	})
}

// wait reaps the child exactly once.
func (c *client) wait() {
	c.waitOnce.Do(func() {
		if c.cmd != nil {
			c.waitErr = c.cmd.Wait()
		}
	})
}

// close performs a best-effort graceful shutdown (shutdown request, exit
// notification), waits briefly, then kills and reaps the child. It is
// idempotent and safe to call after a failed query.
func (c *client) close() {
	c.closeOnce.Do(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		if _, err := c.request(shutdownCtx, "shutdown", nil); err != nil {
			c.kill()
		}
		cancel()
		_ = c.notify("exit", nil)
		select {
		case <-c.readerDone:
		case <-time.After(exitGrace):
			c.kill()
		}
		c.kill()
		c.wait()
		_ = c.stdin.Close()
	})
}

// connectionError renders a stable "the server died" error with its bounded
// stderr tail, so a crash is diagnosable instead of an opaque read error.
func (c *client) connectionError() error {
	c.mu.Lock()
	readErr := c.readErr
	c.mu.Unlock()
	message := "language server exited before responding"
	if c.waitErr != nil {
		message += " (" + c.waitErr.Error() + ")"
	} else if readErr != nil && !errors.Is(readErr, io.EOF) {
		message += ": " + readErr.Error()
	}
	if tail := strings.TrimSpace(c.stderr.String()); tail != "" {
		message += ": " + tail
	}
	return errors.New(message)
}

// tailBuffer keeps the last limit bytes written to it. It is used to capture a
// bounded stderr tail without blocking the child on a full pipe.
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > b.limit {
		b.data = b.data[len(b.data)-b.limit:]
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
