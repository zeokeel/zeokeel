package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/lsp"
	"github.com/zeokeel/zeokeel/internal/runtime"
)

// The lsp tool exposes a configured language server to the model.
//
// It is one read-only tool with DSH tool-lsp's four operations. It does not
// start a server at build time: the configuration is resolved when the agent is
// built (internal/app), and each call starts, queries and stops its own stdio
// server through the internal/lsp provider. Coordinates are one-based with
// UTF-16 code-unit columns, matching LSP and DSH; results are bounded.
const (
	// LSPMaxOutputBytes caps one lsp tool result (16 KiB).
	LSPMaxOutputBytes = 16 * 1024

	// LSPMaxLocations caps how many locations one result renders.
	LSPMaxLocations = 50
)

const lspDescription = `Query a configured language server for code intelligence (read-only).

Call it with a JSON object:
  {"operation":"goToDefinition","file":"internal/app/services.go","line":42,"column":7}
  {"operation":"findReferences","file":"main.go","line":10,"column":2}
  {"operation":"goToImplementation","file":"main.go","line":10,"column":2}
  {"operation":"hover","file":"main.go","line":10,"column":2}

"operation" is one of goToDefinition, findReferences, goToImplementation or
hover. "file" is a workspace-relative path (workspace-absolute paths are also
accepted). "line" and "column" are required and one-based; "column" counts
UTF-16 code units (the LSP coordinate model), so an emoji before the cursor
counts as two.

Language servers are configured with the lsp.servers setting, a JSON object
keyed by language id: {"go":{"command":"gopls","args":["serve"]}}. A workspace
file .zeokeel/lsp.json uses the same shape but is only read after an explicit
lsp.workspace_config=allow opt-in. Without a server configured for the file's
language the tool fails with a stable "no language server is configured" error.

The tool never writes workspace files. Output is bounded: at most 50
locations and 16384 bytes per call, and hover content is normalized to text.`

// LSPConfig configures the lsp tool.
type LSPConfig struct {
	// WorkspaceRoot confines "file" arguments and resolves rendered relative
	// paths. Required when the tool is invoked.
	WorkspaceRoot string

	// Provider runs the queries. Nil reports "no language server configured".
	Provider lsp.Provider

	// ConfigError records an lsp.servers / workspace-file configuration problem
	// found at build time; it is reported by Invoke instead of failing the
	// whole agent build.
	ConfigError error

	// Timeout bounds one query. Non-positive values use lsp.DefaultTimeout.
	Timeout time.Duration

	// MaxOutputBytes and MaxLocations bound one result. Non-positive values use
	// LSPMaxOutputBytes / LSPMaxLocations.
	MaxOutputBytes int
	MaxLocations   int
}

// NewLSPTool returns the lsp runtime.Tool. Construction never fails; invalid
// configuration is reported by Invoke.
func NewLSPTool(cfg LSPConfig) runtime.Tool {
	tool := &lspTool{
		provider:     cfg.Provider,
		configErr:    cfg.ConfigError,
		timeout:      cfg.Timeout,
		maxOutput:    cfg.MaxOutputBytes,
		maxLocations: cfg.MaxLocations,
	}
	if tool.timeout <= 0 {
		tool.timeout = lsp.DefaultTimeout
	}
	if tool.maxOutput <= 0 {
		tool.maxOutput = LSPMaxOutputBytes
	}
	if tool.maxLocations <= 0 {
		tool.maxLocations = LSPMaxLocations
	}
	if root := strings.TrimSpace(cfg.WorkspaceRoot); root != "" {
		if sb, err := newSandbox(root); err != nil {
			tool.initErr = fmt.Errorf("lsp: %w", err)
		} else {
			tool.sb = sb
		}
	} else {
		tool.initErr = errors.New("lsp: workspace root is not configured")
	}
	return tool
}

type lspTool struct {
	provider     lsp.Provider
	configErr    error
	initErr      error
	timeout      time.Duration
	maxOutput    int
	maxLocations int
	sb           *sandbox
}

// Name implements runtime.Tool.
func (t *lspTool) Name() string { return "lsp" }

// Description implements runtime.Tool.
func (t *lspTool) Description() string { return lspDescription }

// lspInvocation is the decoded model-facing argument object.
type lspInvocation struct {
	Operation string `json:"operation"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
}

// Invoke implements runtime.Tool.
func (t *lspTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var input lspInvocation
	if err := decodeArgs(t.Name(), args, &input); err != nil {
		return "", err
	}
	if t.configErr != nil {
		return "", fmt.Errorf("lsp: %w", t.configErr)
	}
	if t.initErr != nil {
		return "", t.initErr
	}
	operation, err := lsp.NormalizeOperation(input.Operation)
	if err != nil {
		return "", fmt.Errorf("lsp: %w", err)
	}
	if strings.TrimSpace(input.File) == "" {
		return "", errors.New(`lsp: "file" is required`)
	}
	if input.Line < 1 {
		return "", errors.New(`lsp: "line" must be a positive integer (lines are 1-based)`)
	}
	if input.Column < 1 {
		return "", errors.New(`lsp: "column" must be a positive integer (columns are 1-based UTF-16 code units)`)
	}

	path, err := t.resolveFile(input.File)
	if err != nil {
		return "", err
	}
	text, err := t.readFile(input.File, path)
	if err != nil {
		return "", err
	}
	languageID := lsp.LanguageForPath(path)
	if languageID == "" {
		return "", fmt.Errorf("lsp: %w", &lsp.NoServerError{Extension: strings.ToLower(filepath.Ext(path))})
	}
	if t.provider == nil {
		return "", errors.New("lsp: no language server is configured; set lsp.servers in settings (language -> {command, args})")
	}

	queryCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	result, err := t.provider.Query(queryCtx, lsp.Query{
		Operation:  operation,
		LanguageID: languageID,
		Path:       path,
		Text:       text,
		Line:       input.Line,
		Column:     input.Column,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(queryCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("lsp: %s timed out after %s; the language server did not respond", operation, t.timeout)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", fmt.Errorf("lsp: %w", err)
	}
	return t.render(operation, path, input.Line, input.Column, result), nil
}

// resolveFile maps the model's file argument to an absolute, symlink-resolved
// path inside the workspace root. Absolute arguments are accepted only when
// they stay inside the root.
func (t *lspTool) resolveFile(file string) (string, error) {
	if t.sb == nil {
		return "", errors.New("lsp: workspace root is not configured")
	}
	if filepath.IsAbs(file) {
		clean := filepath.Clean(file)
		if err := t.sb.ensureWithin(clean); err != nil {
			return "", fmt.Errorf("lsp: %w", err)
		}
		real, err := filepath.EvalSymlinks(clean)
		if err != nil {
			if os.IsNotExist(err) {
				return "", fmt.Errorf("lsp: file %q does not exist", file)
			}
			return "", fmt.Errorf("lsp: cannot resolve file %q: %w", file, err)
		}
		if err := t.sb.ensureWithin(real); err != nil {
			return "", fmt.Errorf("lsp: %w", err)
		}
		return real, nil
	}
	return t.sb.resolve(file)
}

// readFile validates and reads the queried file. Language servers need the
// current content (sent as textDocument/didOpen), and the read is bounded like
// the other file tools.
func (t *lspTool) readFile(file, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("lsp: file %q does not exist", file)
		}
		return "", fmt.Errorf("lsp: cannot inspect file %q: %w", file, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("lsp: %q is not a regular file", file)
	}
	if info.Size() > MaxReadFileBytes {
		return "", fmt.Errorf("lsp: file %q is too large (%d bytes; limit %d)", file, info.Size(), MaxReadFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("lsp: read file %q: %w", file, err)
	}
	return string(data), nil
}

// render formats one bounded tool result.
func (t *lspTool) render(op lsp.Operation, path string, line, column int, result *lsp.Result) string {
	if result == nil {
		return t.bound(fmt.Sprintf("%s: %s:%d:%d\nno result", op, t.displayPath(path), line, column))
	}
	if op == lsp.OpHover {
		text := strings.TrimSpace(result.Hover)
		if text == "" {
			return fmt.Sprintf("hover: no hover information for %s:%d:%d", t.displayPath(path), line, column)
		}
		return t.bound(fmt.Sprintf("hover: %s:%d:%d\n\n%s", t.displayPath(path), line, column, text))
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "%s: %s:%d:%d", op, t.displayPath(path), line, column)
	seen := make(map[string]bool, len(result.Locations))
	rendered := make([]string, 0, min(len(result.Locations), t.maxLocations))
	omitted := 0
	for _, location := range result.Locations {
		entry := t.renderLocation(location)
		if entry == "" || seen[entry] {
			continue
		}
		if len(rendered) >= t.maxLocations {
			omitted++
			continue
		}
		seen[entry] = true
		rendered = append(rendered, entry)
	}
	fmt.Fprintf(&builder, "\n%d location(s)", len(rendered))
	for _, entry := range rendered {
		builder.WriteString("\n  ")
		builder.WriteString(entry)
	}
	if omitted > 0 {
		fmt.Fprintf(&builder, "\n  ... and %d more location(s) omitted", omitted)
	}
	return t.bound(builder.String())
}

// renderLocation renders one location as "path:line:column" with
// one-based coordinates; columns stay in LSP's UTF-16 code-unit model.
func (t *lspTool) renderLocation(location lsp.Location) string {
	path := strings.TrimSpace(location.URI)
	if hostPath, err := lsp.URIToPath(location.URI); err == nil {
		path = hostPath
		if t.sb != nil && t.sb.contains(hostPath) {
			path = t.sb.relative(hostPath)
		}
	}
	if path == "" {
		return ""
	}
	position := location.Range.Start
	return fmt.Sprintf("%s:%d:%d", filepath.ToSlash(path), position.Line+1, position.Character+1)
}

// displayPath renders the queried file workspace-relative when possible.
func (t *lspTool) displayPath(path string) string {
	if t.sb != nil && t.sb.contains(path) {
		return t.sb.relative(path)
	}
	return filepath.ToSlash(path)
}

// bound caps s at the output budget, keeping the result valid UTF-8.
func (t *lspTool) bound(s string) string {
	if len(s) <= t.maxOutput {
		return s
	}
	return capWithNote(s, t.maxOutput, fmt.Sprintf("lsp output truncated to %d bytes", t.maxOutput))
}
