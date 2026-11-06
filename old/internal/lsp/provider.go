package lsp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ProviderConfig configures the generic stdio provider.
type ProviderConfig struct {
	// Servers maps a language id to its stdio server. A language with no
	// entry makes Query fail with a stable NoServerError.
	Servers map[string]ServerConfig
	// Root is the workspace root: the child's working directory and the
	// default rootUri/workspace folder.
	Root string
	// Timeout bounds one query. Non-positive values use DefaultTimeout.
	Timeout time.Duration
}

// StdioProvider is the generic stdio language-server provider.
//
// Every Query owns one child process: it is started, initialized, sent
// textDocument/didOpen with the current content, asked the one question, then
// shut down. This keeps queries independent (one server crashing or hanging
// cannot affect another) at the cost of a process start per query.
type StdioProvider struct {
	servers map[string]ServerConfig
	root    string
	timeout time.Duration
}

// NewStdioProvider returns the stdio provider for cfg.
func NewStdioProvider(cfg ProviderConfig) *StdioProvider {
	return &StdioProvider{servers: cfg.Servers, root: cfg.Root, timeout: cfg.Timeout}
}

// Query implements Provider.
func (p *StdioProvider) Query(ctx context.Context, q Query) (*Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	languageID := strings.TrimSpace(q.LanguageID)
	if languageID == "" {
		return nil, &NoServerError{Extension: extensionOf(q.Path)}
	}
	server, ok := p.servers[languageID]
	if !ok {
		return nil, &NoServerError{LanguageID: languageID}
	}
	// Validate the cursor before starting a process: an out-of-range position
	// is the caller's error, not a server failure.
	position, err := ValidatePosition(q.Text, q.Line, q.Column)
	if err != nil {
		return nil, err
	}
	timeout := p.timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client, err := startClient(queryCtx, server, p.root)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &TimeoutError{Operation: "initialize", Timeout: timeout}
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("could not start language server %q: %w", strings.TrimSpace(server.Command), err)
	}
	defer client.close()

	if !client.caps.supports(q.Operation) {
		return nil, &UnsupportedError{Operation: q.Operation, Server: strings.TrimSpace(server.Command)}
	}

	uri := PathToURI(q.Path)
	if err := client.didOpen(uri, languageID, q.Text); err != nil {
		return nil, fmt.Errorf("textDocument/didOpen failed: %w", err)
	}
	defer client.didClose(uri)

	raw, err := client.query(queryCtx, q.Operation, uri, position)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &TimeoutError{Operation: q.Operation, Timeout: timeout}
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("%s failed: %w", q.Operation, err)
	}

	if q.Operation == OpHover {
		hover, hoverErr := parseHoverResult(raw)
		if hoverErr != nil {
			return nil, fmt.Errorf("%s returned invalid hover: %w", q.Operation, hoverErr)
		}
		return &Result{Hover: hover}, nil
	}
	locations, locationsErr := parseLocations(raw)
	if locationsErr != nil {
		return nil, fmt.Errorf("%s returned invalid locations: %w", q.Operation, locationsErr)
	}
	return &Result{Locations: locations}, nil
}

// extensionOf returns the lowercased file extension including the dot ("" when
// there is none), for "no server configured" diagnostics.
func extensionOf(path string) string {
	if path == "" {
		return ""
	}
	base := path
	if i := strings.LastIndexAny(base, "/\\"); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		return strings.ToLower(base[i:])
	}
	return ""
}
