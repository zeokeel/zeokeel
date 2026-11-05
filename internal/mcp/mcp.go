// Package mcp manages Model Context Protocol servers and exposes their tools
// to the ZeoKeel agent runtime.
//
// Servers are launched over stdio with the official
// github.com/modelcontextprotocol/go-sdk client and CommandTransport. Each
// server is connected independently: a broken command or a failing handshake
// only removes that server's tools and is reported in the Connect error, while
// every other server keeps working.
//
// Discovered tools are adapted to runtime.Tool with the DSH-style name
// "mcp__<server>__<tool>". The adapter forwards the model's JSON arguments to
// tools/call, flattens text content, and renders non-text content as a short
// descriptive marker. A server-side tool error (result.IsError) is returned as
// a Go error so the runtime reports a failed tool call to the model.
//
// The Manager owns the subprocesses and their MCP sessions; call Close to
// terminate them. Close is idempotent and Connect after Close fails.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

const (
	// DefaultConnectTimeout bounds one server's start, initialization and
	// tools/list handshake.
	DefaultConnectTimeout = 10 * time.Second

	// defaultTerminate is how long the SDK waits after closing a server's
	// stdin before signalling it during Close.
	defaultTerminate = 2 * time.Second

	// toolNamePrefix and toolNameSeparator give "mcp__<server>__<tool>",
	// matching DSH's MCP tool naming.
	toolNamePrefix    = "mcp__"
	toolNameSeparator = "__"

	clientName    = "zeokeel"
	clientVersion = "1.0.0"

	maxToolNameLen = 128
)

// ServerConfig describes one stdio MCP server.
type ServerConfig struct {
	// Name identifies the server in tool names. It must be non-empty and
	// unique within a Manager. Keep it to letters, digits, '.', '_' and '-'.
	Name string

	// Command is the executable to start. Required.
	Command string

	// Args are passed to Command verbatim.
	Args []string

	// Env entries are "KEY=VALUE" overrides appended to the current process
	// environment for the server process.
	Env []string
}

// Config is a set of MCP servers to connect.
type Config struct {
	Servers []ServerConfig
}

// Manager owns the connected MCP servers and their adapted tools.
//
// Manager is safe for concurrent use. Connect is serialized per call; Tools and
// Close may run concurrently with it.
type Manager struct {
	baseCtx context.Context
	cancel  context.CancelFunc

	mu       sync.RWMutex
	sessions map[string]*sdk.ClientSession
	tools    []runtime.Tool
	closed   bool

	// connectTimeout, requestTimeout and terminate are fields rather than
	// consts so package tests can shorten them; production always uses the
	// defaults.
	connectTimeout time.Duration
	requestTimeout time.Duration
	terminate      time.Duration
}

// NewManager returns an empty manager. ctx bounds the lifetime of connection
// attempts; canceling it does not close already connected sessions (call
// Close for that).
func NewManager(ctx context.Context) *Manager {
	if ctx == nil {
		ctx = context.Background()
	}
	baseCtx, cancel := context.WithCancel(ctx)
	return &Manager{
		baseCtx:        baseCtx,
		cancel:         cancel,
		sessions:       make(map[string]*sdk.ClientSession),
		connectTimeout: DefaultConnectTimeout,
		requestTimeout: DefaultRequestTimeout,
		terminate:      defaultTerminate,
	}
}

// Connect starts every server in cfg, initializes it, lists its tools and
// registers the adapted tools. Failures are per-server: an error is returned
// only after all servers were attempted, and the successfully connected
// servers remain usable. Calling Connect again adds servers; names must not
// already be connected.
func (m *Manager) Connect(ctx context.Context, cfg Config) error {
	if m == nil {
		return errors.New("mcp: nil manager")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.RLock()
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return errors.New("mcp: manager is closed")
	}

	var errs []error
	seen := make(map[string]struct{}, len(cfg.Servers))
	for _, server := range cfg.Servers {
		server.Name = strings.TrimSpace(server.Name)
		if err := validateServerConfig(server); err != nil {
			errs = append(errs, fmt.Errorf("mcp: server %q: %w", server.Name, err))
			continue
		}
		if _, duplicate := seen[server.Name]; duplicate {
			errs = append(errs, fmt.Errorf("mcp: server %q: duplicate server name in config", server.Name))
			continue
		}
		seen[server.Name] = struct{}{}

		m.mu.RLock()
		_, already := m.sessions[server.Name]
		m.mu.RUnlock()
		if already {
			errs = append(errs, fmt.Errorf("mcp: server %q: already connected", server.Name))
			continue
		}

		if err := m.connectServer(ctx, server); err != nil {
			errs = append(errs, fmt.Errorf("mcp: server %q: %w", server.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) connectServer(parent context.Context, server ServerConfig) error {
	ctx, cancel := m.operationContext(parent)
	defer cancel()
	timeout := m.connectTimeout
	if timeout <= 0 {
		timeout = DefaultConnectTimeout
	}
	ctx, cancelTimeout := context.WithTimeout(ctx, timeout)
	defer cancelTimeout()

	cmd := exec.Command(server.Command, server.Args...)
	// Inherit a scrubbed environment: a configured server must not be able to
	// read the provider key or another ZeoKeel secret. An operator can still
	// pass a variable explicitly through the server's own Env list, so the
	// configuration remains the only way to hand a secret to a server.
	cmd.Env = mergeEnv(scrubMCPEnv(os.Environ()), server.Env)
	transport := &sdk.CommandTransport{Command: cmd, TerminateDuration: m.terminate}

	client := sdk.NewClient(&sdk.Implementation{Name: clientName, Version: clientVersion}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	connected := true
	defer func() {
		if connected {
			_ = session.Close()
		}
	}()

	listed, err := listTools(ctx, session)
	if err != nil {
		return fmt.Errorf("list tools: %w", err)
	}
	adapters := make([]runtime.Tool, 0, len(listed))
	names := make(map[string]string, len(listed))
	for _, tool := range listed {
		adapter, err := newToolAdapter(server.Name, tool, session)
		if err != nil {
			return err
		}
		if previous, duplicate := names[adapter.Name()]; duplicate {
			return fmt.Errorf("tool name %q is produced by both %q and %q", adapter.Name(), previous, tool.Name)
		}
		names[adapter.Name()] = tool.Name
		adapters = append(adapters, adapter)
	}

	if err := m.addServer(server.Name, session, adapters); err != nil {
		return err
	}
	connected = false // the manager now owns the session
	return nil
}

// operationContext combines the caller's context with the manager lifetime.
func (m *Manager) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.baseCtx, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func (m *Manager) addServer(name string, session *sdk.ClientSession, adapters []runtime.Tool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("manager closed during connect")
	}
	if _, exists := m.sessions[name]; exists {
		return fmt.Errorf("server %q is already connected", name)
	}
	existing := make(map[string]struct{}, len(m.tools))
	for _, tool := range m.tools {
		existing[tool.Name()] = struct{}{}
	}
	for _, tool := range adapters {
		if _, collision := existing[tool.Name()]; collision {
			return fmt.Errorf("tool name %q collides with an existing MCP tool", tool.Name())
		}
	}
	m.sessions[name] = session
	m.tools = append(m.tools, adapters...)
	return nil
}

// Tools returns a snapshot of the adapted tools sorted by name. The slice is
// safe to modify; the tools themselves are shared and safe for concurrent
// invocation.
func (m *Manager) Tools() []runtime.Tool {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	out := append([]runtime.Tool(nil), m.tools...)
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Servers returns the names of currently connected servers, sorted.
func (m *Manager) Servers() []string {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.sessions))
	for name := range m.sessions {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Close terminates every connected server and clears the tool list. It is
// idempotent; subsequent calls return nil. Errors from individual sessions are
// joined and returned.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.cancel()
	sessions := m.sessions
	m.sessions = make(map[string]*sdk.ClientSession)
	m.tools = nil
	m.mu.Unlock()

	var errs []error
	for name, session := range sessions {
		if err := session.Close(); err != nil {
			errs = append(errs, fmt.Errorf("mcp: close server %q: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func listTools(ctx context.Context, session *sdk.ClientSession) ([]*sdk.Tool, error) {
	var tools []*sdk.Tool
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		if tool == nil {
			continue
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

// toolAdapter is one MCP tool exposed as a runtime.Tool.
type toolAdapter struct {
	server      string
	toolName    string
	name        string
	description string
	session     *sdk.ClientSession
}

var _ runtime.Tool = (*toolAdapter)(nil)

func newToolAdapter(server string, tool *sdk.Tool, session *sdk.ClientSession) (*toolAdapter, error) {
	if tool == nil {
		return nil, errors.New("server returned a nil tool definition")
	}
	toolName := strings.TrimSpace(tool.Name)
	if err := validateToolName(toolName); err != nil {
		return nil, fmt.Errorf("tool %q: %w", tool.Name, err)
	}
	description := strings.TrimSpace(tool.Description)
	if description == "" {
		description = fmt.Sprintf("MCP tool %q from server %q.", toolName, server)
	}
	if tool.InputSchema != nil {
		if schema, err := json.Marshal(tool.InputSchema); err == nil && len(schema) > 2 {
			description += "\n\nArgs JSON schema:\n" + string(schema)
		}
	}
	return &toolAdapter{
		server:      server,
		toolName:    toolName,
		name:        toolNamePrefix + server + toolNameSeparator + toolName,
		description: description,
		session:     session,
	}, nil
}

// Name implements runtime.Tool.
func (a *toolAdapter) Name() string { return a.name }

// Description implements runtime.Tool.
func (a *toolAdapter) Description() string { return a.description }

// Invoke implements runtime.Tool. JSON object arguments are forwarded
// unchanged to tools/call; empty arguments become {}.
func (a *toolAdapter) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if a == nil || a.session == nil {
		return "", errors.New("mcp: tool is not connected")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	arguments := any(map[string]any{})
	if trimmed := bytes.TrimSpace(args); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &object); err != nil {
			return "", fmt.Errorf("%s: arguments must be a JSON object: %w", a.name, err)
		}
		arguments = json.RawMessage(append([]byte(nil), trimmed...))
	}

	result, err := a.session.CallTool(ctx, &sdk.CallToolParams{
		Name:      a.toolName,
		Arguments: arguments,
	})
	if err != nil {
		return "", fmt.Errorf("mcp: call tool %q on server %q: %w", a.toolName, a.server, err)
	}
	text, err := formatResult(result)
	if err != nil {
		return "", fmt.Errorf("mcp: format result of tool %q on server %q: %w", a.toolName, a.server, err)
	}
	if result != nil && result.IsError {
		if strings.TrimSpace(text) == "" {
			text = "(no error text)"
		}
		return "", fmt.Errorf("mcp: tool %q on server %q reported an error: %s", a.toolName, a.server, text)
	}
	return text, nil
}

// formatResult flattens MCP content blocks into text. Non-text blocks get a
// short human-readable marker so binary payloads never bloat the transcript.
func formatResult(result *sdk.CallToolResult) (string, error) {
	if result == nil {
		return "", nil
	}
	parts := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		switch c := content.(type) {
		case *sdk.TextContent:
			if c.Text != "" {
				parts = append(parts, c.Text)
			}
		case *sdk.ImageContent:
			parts = append(parts, fmt.Sprintf("[image content: %s, %d base64 bytes]", c.MIMEType, len(c.Data)))
		case *sdk.AudioContent:
			parts = append(parts, fmt.Sprintf("[audio content: %s, %d base64 bytes]", c.MIMEType, len(c.Data)))
		case *sdk.ResourceLink:
			parts = append(parts, fmt.Sprintf("[resource link: %s <%s>]", c.Name, c.URI))
		case *sdk.EmbeddedResource:
			if c.Resource == nil {
				parts = append(parts, "[embedded resource]")
				continue
			}
			switch {
			case c.Resource.Text != "":
				parts = append(parts, c.Resource.Text)
			case len(c.Resource.Blob) > 0:
				parts = append(parts, fmt.Sprintf("[embedded resource: %s, %s, %d bytes]", c.Resource.URI, c.Resource.MIMEType, len(c.Resource.Blob)))
			default:
				parts = append(parts, fmt.Sprintf("[embedded resource: %s]", c.Resource.URI))
			}
		default:
			if content == nil {
				continue
			}
			raw, err := json.Marshal(content)
			if err != nil {
				return "", fmt.Errorf("marshal content: %w", err)
			}
			parts = append(parts, string(raw))
		}
	}
	if len(parts) == 0 && result.StructuredContent != nil {
		raw, err := json.MarshalIndent(result.StructuredContent, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal structured content: %w", err)
		}
		parts = append(parts, string(raw))
	}
	return strings.Join(parts, "\n"), nil
}

var serverNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func validateServerConfig(server ServerConfig) error {
	if server.Name == "" {
		return errors.New("name is required")
	}
	if !serverNamePattern.MatchString(server.Name) {
		return fmt.Errorf("invalid name %q: use letters, digits, '.', '_' or '-' (max 64 chars)", server.Name)
	}
	if strings.TrimSpace(server.Command) == "" {
		return errors.New("command is required")
	}
	for _, entry := range server.Env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return fmt.Errorf("env entry %q must be KEY=VALUE", entry)
		}
	}
	return nil
}

func validateToolName(name string) error {
	if name == "" {
		return errors.New("name is empty")
	}
	if len(name) > maxToolNameLen {
		return fmt.Errorf("name exceeds %d characters", maxToolNameLen)
	}
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("name %q contains whitespace or control characters", name)
		}
	}
	return nil
}

// scrubMCPEnv removes ZeoKeel and provider secrets from the environment a
// configured MCP server inherits. Matching is case-insensitive, as the bash,
// job, pwsh and terminal tools' scrubEnv is, so Windows-style names are
// covered. Entries the operator lists in ServerConfig.Env are merged after
// this and are not filtered.
func scrubMCPEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name := entry
		if i := strings.IndexByte(entry, '='); i >= 0 {
			name = entry[:i]
		}
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "ZEOKEEL_") || strings.HasPrefix(upper, "DEEPSEEK") {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// mergeEnv returns base with overrides applied by key. Overrides whose key is
// already present replace the base value in place; new keys are appended.
func mergeEnv(base, overrides []string) []string {
	if len(overrides) == 0 {
		return base
	}
	out := append([]string(nil), base...)
	index := make(map[string]int, len(out))
	for i, entry := range out {
		if key, _, ok := strings.Cut(entry, "="); ok {
			index[key] = i
		}
	}
	for _, entry := range overrides {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if i, exists := index[key]; exists {
			out[i] = entry
		} else {
			index[key] = len(out)
			out = append(out, entry)
		}
	}
	return out
}
