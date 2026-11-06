package lsp

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// DefaultTimeout bounds one query: server start, initialize, the request and
// the graceful shutdown.
const DefaultTimeout = 15 * time.Second

// Operation is one supported LSP query.
type Operation string

// Supported operations, matching DSH tool-lsp's operation names.
const (
	OpGoToDefinition     Operation = "goToDefinition"
	OpFindReferences     Operation = "findReferences"
	OpGoToImplementation Operation = "goToImplementation"
	OpHover              Operation = "hover"
)

// Position is a zero-based LSP position. Character counts UTF-16 code units.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is an LSP range.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is one source range returned by a language server.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// Result is the normalized answer to one Query.
type Result struct {
	// Locations are the definition/references/implementation locations.
	Locations []Location
	// Hover is the normalized hover text (empty when the server has none).
	Hover string
}

// Query describes one read-only language-server query.
type Query struct {
	Operation  Operation
	LanguageID string
	// Path is the absolute path of the queried file.
	Path string
	// Text is the current file content sent through textDocument/didOpen.
	Text string
	// Line and Column are one-based; Column counts UTF-16 code units.
	Line   int
	Column int
}

// Provider runs one language-server query. It is the seam the model-facing
// tool depends on; tests substitute in-memory providers for the stdio one.
type Provider interface {
	Query(ctx context.Context, q Query) (*Result, error)
}

// NoServerError reports that no language server is configured for a language.
type NoServerError struct {
	// LanguageID is the language deduced from the file extension, empty when
	// the extension is unknown.
	LanguageID string
	// Extension is the file extension (with dot) when LanguageID is empty.
	Extension string
}

func (e *NoServerError) Error() string {
	switch {
	case e.LanguageID != "":
		return fmt.Sprintf("no language server is configured for language %q; set lsp.servers in settings (language -> {command, args})", e.LanguageID)
	case e.Extension != "":
		return fmt.Sprintf("no language server is configured for %q files; set lsp.servers in settings (language -> {command, args})", e.Extension)
	default:
		return "no language server is configured; set lsp.servers in settings (language -> {command, args})"
	}
}

// UnsupportedError reports that the server is up but does not advertise the
// capability the operation needs.
type UnsupportedError struct {
	Operation Operation
	Server    string
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("language server %q does not support %s", e.Server, e.Operation)
}

// TimeoutError reports that a query exceeded its bounded lifetime; the server
// process is killed before this error is returned.
type TimeoutError struct {
	Operation Operation
	Timeout   time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("%s timed out after %s; the language server did not respond", e.Operation, e.Timeout)
}

// operationAliases maps the accepted spellings (lowercased) to the canonical
// operation, so the model may pass either DSH's names or the short forms.
var operationAliases = map[string]Operation{
	"gotodefinition":      OpGoToDefinition,
	"goto_definition":     OpGoToDefinition,
	"definition":          OpGoToDefinition,
	"findreferences":      OpFindReferences,
	"find_references":     OpFindReferences,
	"references":          OpFindReferences,
	"gotoimplementation":  OpGoToImplementation,
	"goto_implementation": OpGoToImplementation,
	"implementation":      OpGoToImplementation,
	"hover":               OpHover,
}

// NormalizeOperation validates and canonicalizes an operation name. The
// canonical DSH names are accepted case-insensitively, plus the short aliases
// definition/references/implementation/hover.
func NormalizeOperation(raw string) (Operation, error) {
	value := strings.TrimSpace(raw)
	if operation, ok := operationAliases[strings.ToLower(value)]; ok {
		return operation, nil
	}
	const valid = "goToDefinition, findReferences, goToImplementation or hover"
	if value == "" {
		return "", fmt.Errorf(`"operation" is required (want %s)`, valid)
	}
	return "", fmt.Errorf("unknown operation %q (want %s)", value, valid)
}

// languageByExtension maps a lowercased file extension to the language id used
// as the key in lsp.servers.
var languageByExtension = map[string]string{
	".go":    "go",
	".py":    "python",
	".pyi":   "python",
	".ts":    "typescript",
	".tsx":   "typescript",
	".mts":   "typescript",
	".cts":   "typescript",
	".js":    "javascript",
	".jsx":   "javascript",
	".mjs":   "javascript",
	".cjs":   "javascript",
	".rs":    "rust",
	".java":  "java",
	".json":  "json",
	".jsonc": "json",
	".yaml":  "yaml",
	".yml":   "yaml",
	".c":     "c",
	".h":     "c",
	".cc":    "cpp",
	".cpp":   "cpp",
	".cxx":   "cpp",
	".hpp":   "cpp",
	".hxx":   "cpp",
	".hh":    "cpp",
	".lua":   "lua",
	".rb":    "ruby",
	".php":   "php",
	".cs":    "csharp",
	".kt":    "kotlin",
	".kts":   "kotlin",
	".swift": "swift",
	".scala": "scala",
}

// LanguageForPath maps a file path's extension to a language id (the key in
// lsp.servers). It returns "" for unknown or extensionless paths.
func LanguageForPath(path string) string {
	extension := extensionOf(path)
	if extension == "" {
		return ""
	}
	return languageByExtension[extension]
}
