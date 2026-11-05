package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// DefaultRequestTimeout bounds one resources or prompts request against a
	// single server. It is a field on Manager so tests can shorten it.
	DefaultRequestTimeout = 10 * time.Second

	// MaxResourceContentBytes caps the raw bytes contributed by one resource
	// content item. Text is truncated on a UTF-8 boundary and blobs are
	// truncated before base64 encoding.
	MaxResourceContentBytes = 256 * 1024

	// MaxResourceTotalBytes caps the raw bytes contributed by all content
	// items of one resources/read response. Base64 expansion is not counted.
	MaxResourceTotalBytes = 1 << 20
)

// ResourceInfo describes one resource advertised by a connected MCP server.
type ResourceInfo struct {
	// Server is the name the resource's server was configured with.
	Server string `json:"server"`
	// URI identifies the resource within the server.
	URI string `json:"uri"`
	// Name is the server-provided display name.
	Name string `json:"name,omitempty"`
	// MIME is the server-provided MIME type, if any.
	MIME string `json:"mimeType,omitempty"`
	// Size is the server-reported size in bytes, if known.
	Size int64 `json:"size,omitempty"`
}

// ResourceContent is one content item returned by resources/read. Exactly one
// of Text or Blob is normally set. Blob holds standard base64 (not URL-safe)
// text so it can be carried in JSON unchanged.
type ResourceContent struct {
	Server    string `json:"server"`
	URI       string `json:"uri"`
	MIME      string `json:"mimeType,omitempty"`
	Text      string `json:"text,omitempty"`
	Blob      string `json:"blob,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// PromptArgumentInfo describes one argument accepted by an MCP prompt.
type PromptArgumentInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// PromptInfo describes one prompt advertised by a connected MCP server.
type PromptInfo struct {
	Server      string               `json:"server"`
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	Arguments   []PromptArgumentInfo `json:"arguments,omitempty"`
}

// PromptMessage is one message returned by prompts/get, flattened to text.
// Non-text content is rendered as a short descriptive marker so binary
// payloads never bloat the transcript.
type PromptMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// ListResources lists the resources of every connected server. Servers are
// queried independently; a failing server is reported in the joined error while
// the resources of the other servers are still returned. The result is sorted
// by server and URI.
func (m *Manager) ListResources(ctx context.Context) ([]ResourceInfo, error) {
	if m == nil {
		return nil, errors.New("mcp: nil manager")
	}
	names, sessions := m.sessionSnapshot()
	if len(names) == 0 {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var out []ResourceInfo
	var errs []error
	for _, name := range names {
		resources, err := m.listServerResources(ctx, name, sessions[name])
		if err != nil {
			errs = append(errs, fmt.Errorf("mcp: server %q: list resources: %w", name, err))
			continue
		}
		out = append(out, resources...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Server != out[j].Server {
			return out[i].Server < out[j].Server
		}
		return out[i].URI < out[j].URI
	})
	return out, errors.Join(errs...)
}

func (m *Manager) listServerResources(ctx context.Context, server string, session *sdk.ClientSession) ([]ResourceInfo, error) {
	if session == nil {
		return nil, errors.New("session is not connected")
	}
	ctx, cancel := m.requestContext(ctx)
	defer cancel()

	var out []ResourceInfo
	// The SDK iterator follows nextCursor pages automatically.
	for resource, err := range session.Resources(ctx, nil) {
		if err != nil {
			return nil, err
		}
		if resource == nil {
			continue
		}
		out = append(out, ResourceInfo{
			Server: server,
			URI:    resource.URI,
			Name:   resource.Name,
			MIME:   resource.MIMEType,
			Size:   resource.Size,
		})
	}
	return out, nil
}

// ReadResource reads one resource from one connected server. The server and URI
// are required. Each content item is capped at MaxResourceContentBytes and the
// whole response at MaxResourceTotalBytes; truncated items carry Truncated.
func (m *Manager) ReadResource(ctx context.Context, server, uri string) ([]ResourceContent, error) {
	if m == nil {
		return nil, errors.New("mcp: nil manager")
	}
	server = strings.TrimSpace(server)
	uri = strings.TrimSpace(uri)
	if server == "" {
		return nil, errors.New("mcp: server is required")
	}
	if uri == "" {
		return nil, errors.New("mcp: resource URI is required")
	}
	session, err := m.sessionByName(server)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := m.requestContext(ctx)
	defer cancel()

	result, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: uri})
	if err != nil {
		return nil, fmt.Errorf("mcp: read resource %q from server %q: %w", uri, server, err)
	}
	if result == nil || len(result.Contents) == 0 {
		return nil, fmt.Errorf("mcp: read resource %q from server %q: server returned no content", uri, server)
	}
	return capResourceContents(server, uri, result.Contents), nil
}

// capResourceContents applies the per-item and total raw-byte caps. It never
// returns a partial UTF-8 text string.
func capResourceContents(server, requestedURI string, contents []*sdk.ResourceContents) []ResourceContent {
	out := make([]ResourceContent, 0, len(contents))
	budget := MaxResourceTotalBytes
	for _, item := range contents {
		if item == nil {
			continue
		}
		if budget <= 0 {
			if len(out) > 0 {
				out[len(out)-1].Truncated = true
			}
			break
		}

		content := ResourceContent{
			Server: server,
			URI:    strings.TrimSpace(item.URI),
			MIME:   item.MIMEType,
		}
		if content.URI == "" {
			content.URI = requestedURI
		}
		limit := MaxResourceContentBytes
		if budget < limit {
			limit = budget
		}

		switch {
		case item.Text != "":
			text := item.Text
			if len(text) > limit {
				text = utf8Prefix(text, limit)
				content.Truncated = true
			}
			budget -= len(text)
			content.Text = text
		case len(item.Blob) > 0:
			blob := item.Blob
			if len(blob) > limit {
				blob = blob[:limit]
				content.Truncated = true
			}
			budget -= len(blob)
			content.Blob = base64.StdEncoding.EncodeToString(blob)
		}
		out = append(out, content)
	}
	return out
}

// ListPrompts lists the prompts of every connected server. Servers are queried
// independently; a failing server is reported in the joined error while the
// prompts of the other servers are still returned. The result is sorted by
// server and name.
func (m *Manager) ListPrompts(ctx context.Context) ([]PromptInfo, error) {
	if m == nil {
		return nil, errors.New("mcp: nil manager")
	}
	names, sessions := m.sessionSnapshot()
	if len(names) == 0 {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var out []PromptInfo
	var errs []error
	for _, name := range names {
		prompts, err := m.listServerPrompts(ctx, name, sessions[name])
		if err != nil {
			errs = append(errs, fmt.Errorf("mcp: server %q: list prompts: %w", name, err))
			continue
		}
		out = append(out, prompts...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Server != out[j].Server {
			return out[i].Server < out[j].Server
		}
		return out[i].Name < out[j].Name
	})
	return out, errors.Join(errs...)
}

func (m *Manager) listServerPrompts(ctx context.Context, server string, session *sdk.ClientSession) ([]PromptInfo, error) {
	if session == nil {
		return nil, errors.New("session is not connected")
	}
	ctx, cancel := m.requestContext(ctx)
	defer cancel()

	var out []PromptInfo
	// The SDK iterator follows nextCursor pages automatically.
	for prompt, err := range session.Prompts(ctx, nil) {
		if err != nil {
			return nil, err
		}
		if prompt == nil {
			continue
		}
		info := PromptInfo{
			Server:      server,
			Name:        prompt.Name,
			Description: prompt.Description,
		}
		for _, arg := range prompt.Arguments {
			if arg == nil {
				continue
			}
			info.Arguments = append(info.Arguments, PromptArgumentInfo{
				Name:        arg.Name,
				Description: arg.Description,
				Required:    arg.Required,
			})
		}
		out = append(out, info)
	}
	return out, nil
}

// GetPrompt renders one prompt from one connected server with the given
// arguments. Arguments may be nil; the server decides which ones are required.
func (m *Manager) GetPrompt(ctx context.Context, server, name string, args map[string]string) ([]PromptMessage, error) {
	if m == nil {
		return nil, errors.New("mcp: nil manager")
	}
	server = strings.TrimSpace(server)
	name = strings.TrimSpace(name)
	if server == "" {
		return nil, errors.New("mcp: server is required")
	}
	if name == "" {
		return nil, errors.New("mcp: prompt name is required")
	}
	session, err := m.sessionByName(server)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := m.requestContext(ctx)
	defer cancel()

	// Copy the arguments so a caller mutating its map cannot race the request.
	var arguments map[string]string
	if len(args) > 0 {
		arguments = make(map[string]string, len(args))
		for key, value := range args {
			arguments[key] = value
		}
	}
	result, err := session.GetPrompt(ctx, &sdk.GetPromptParams{Name: name, Arguments: arguments})
	if err != nil {
		return nil, fmt.Errorf("mcp: get prompt %q from server %q: %w", name, server, err)
	}
	if result == nil {
		return nil, fmt.Errorf("mcp: get prompt %q from server %q: server returned no result", name, server)
	}
	messages := make([]PromptMessage, 0, len(result.Messages))
	for _, message := range result.Messages {
		if message == nil {
			continue
		}
		messages = append(messages, PromptMessage{
			Role: string(message.Role),
			Text: promptContentText(message.Content),
		})
	}
	return messages, nil
}

// sessionSnapshot returns sorted server names and a copy of the session map.
func (m *Manager) sessionSnapshot() ([]string, map[string]*sdk.ClientSession) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.sessions))
	sessions := make(map[string]*sdk.ClientSession, len(m.sessions))
	for name, session := range m.sessions {
		names = append(names, name)
		sessions[name] = session
	}
	sort.Strings(names)
	return names, sessions
}

// sessionByName resolves one connected server. It reports whether the manager
// was closed separately from an unknown server name.
func (m *Manager) sessionByName(name string) (*sdk.ClientSession, error) {
	m.mu.RLock()
	session, ok := m.sessions[name]
	closed := m.closed
	m.mu.RUnlock()
	if ok {
		return session, nil
	}
	if closed {
		return nil, errors.New("mcp: manager is closed")
	}
	return nil, fmt.Errorf("mcp: server %q is not connected", name)
}

// requestContext combines the caller's context, the manager lifetime and the
// per-request timeout.
func (m *Manager) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := DefaultRequestTimeout
	if m != nil && m.requestTimeout > 0 {
		timeout = m.requestTimeout
	}
	var cancelBase context.CancelFunc
	if m != nil && m.baseCtx != nil {
		ctx, cancelBase = m.operationContext(ctx)
	}
	ctx, cancelTimeout := context.WithTimeout(ctx, timeout)
	return ctx, func() {
		cancelTimeout()
		if cancelBase != nil {
			cancelBase()
		}
	}
}

// promptContentText flattens one prompt content block into text. Non-text
// payloads become a short marker.
func promptContentText(content sdk.Content) string {
	switch c := content.(type) {
	case *sdk.TextContent:
		if c == nil {
			return ""
		}
		return c.Text
	case *sdk.ImageContent:
		if c == nil {
			return ""
		}
		return fmt.Sprintf("[image content: %s, %d base64 bytes]", c.MIMEType, len(c.Data))
	case *sdk.AudioContent:
		if c == nil {
			return ""
		}
		return fmt.Sprintf("[audio content: %s, %d base64 bytes]", c.MIMEType, len(c.Data))
	case *sdk.ResourceLink:
		if c == nil {
			return ""
		}
		return fmt.Sprintf("[resource link: %s <%s>]", c.Name, c.URI)
	case *sdk.EmbeddedResource:
		if c == nil {
			return ""
		}
		if c.Resource == nil {
			return "[embedded resource]"
		}
		switch {
		case c.Resource.Text != "":
			return c.Resource.Text
		case len(c.Resource.Blob) > 0:
			return fmt.Sprintf("[embedded resource: %s, %s, %d bytes]", c.Resource.URI, c.Resource.MIMEType, len(c.Resource.Blob))
		default:
			return fmt.Sprintf("[embedded resource: %s]", c.Resource.URI)
		}
	default:
		if content == nil {
			return ""
		}
		raw, err := json.Marshal(content)
		if err != nil {
			return fmt.Sprintf("[%T content]", content)
		}
		return string(raw)
	}
}

// utf8Prefix returns the longest prefix no longer than limit bytes that does
// not split a UTF-8 sequence.
func utf8Prefix(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}
