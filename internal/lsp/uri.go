package lsp

import (
	"fmt"
	"net/url"
	"path/filepath"
	goruntime "runtime"
	"strings"
)

// PathToURI converts a host filesystem path to a file:// URI usable in LSP
// messages. The path is made absolute and percent-encoded, so spaces and
// non-ASCII names survive the round trip.
func PathToURI(path string) string {
	if trimmed := strings.TrimSpace(path); trimmed != "" {
		if abs, err := filepath.Abs(trimmed); err == nil {
			path = abs
		}
	}
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return "file://" + escapeURIPath(slashed)
}

// URIToPath converts a file:// URI back to a host filesystem path. Non-file
// URIs (untitled:, https:, ...) are rejected with a stable error.
func URIToPath(uri string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(uri))
	if err != nil {
		return "", fmt.Errorf("lsp: invalid URI %q: %w", uri, err)
	}
	if !strings.EqualFold(parsed.Scheme, "file") {
		return "", fmt.Errorf("lsp: unsupported URI scheme %q (want a file:// URI)", parsed.Scheme)
	}
	path := parsed.Path
	if parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost") {
		path = "//" + parsed.Host + path
	}
	if goruntime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path), nil
}

// escapeURIPath percent-encodes everything outside RFC 3986's unreserved set
// and the path separators, keeping a readable file URI without losing spaces,
// emoji or CJK names. ':' is kept for Windows drive letters.
func escapeURIPath(path string) string {
	const hexDigits = "0123456789ABCDEF"
	var builder strings.Builder
	builder.Grow(len(path))
	for i := 0; i < len(path); i++ {
		c := path[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '/', c == ':', c == '-', c == '_', c == '.', c == '~',
			c == '@', c == '!', c == '$', c == '&', c == '\'', c == '(',
			c == ')', c == '*', c == '+', c == ',', c == ';', c == '=':
			builder.WriteByte(c)
		default:
			builder.WriteByte('%')
			builder.WriteByte(hexDigits[c>>4])
			builder.WriteByte(hexDigits[c&0x0F])
		}
	}
	return builder.String()
}
