package memory

import (
	"regexp"
	"strings"
	"unicode"
)

// Secret scrubbers, narrow on purpose: each pattern targets a credential shape
// that is unambiguous in free text, so a memory about "the token budget" is not
// redacted while an actual token assignment is.
var (
	// privateKeyBlock matches PEM private key blocks (RSA, EC, OPENSSH, ...).
	privateKeyBlock = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)

	// bearerToken matches "Bearer <token>" authorization headers.
	bearerToken = regexp.MustCompile(`(?i)\b(bearer)\s+[A-Za-z0-9._~+/-]{8,}`)

	// basicToken matches "Basic <base64 credentials>" authorization headers.
	basicToken = regexp.MustCompile(`(?i)\b(basic)\s+[A-Za-z0-9+/=]{8,}`)

	// skAPIKey matches sk-style provider API keys.
	skAPIKey = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`)

	// awsAccessKey matches AWS access key ids.
	awsAccessKey = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)

	// secretAssignment matches "key = value" shapes for credential names. The
	// value class excludes brackets/quotes so an already-redacted value cannot
	// match again (idempotence).
	secretAssignment = regexp.MustCompile(`(?i)\b(api[_-]?key|apikey|access[_-]?key|secret[_-]?key|client[_-]?secret|token|password|passwd|credential)\b(\s*[:=]\s*)(["']?)([A-Za-z0-9._~+/-]{6,})`)
)

// ScrubSecrets detects credential-like content in text and returns the text
// with every detected secret replaced by a redaction marker. found reports
// whether anything was redacted; the caller uses it to reject a candidate
// instead of persisting it.
//
// Covered shapes: private key blocks, AWS access key ids (AKIA...), sk-style
// API keys, bearer/basic authorization tokens, and credential assignments. The
// detector is deliberately narrow so ordinary prose ("the token budget is
// 10k") is not flagged.
func ScrubSecrets(text string) (redacted string, found bool) {
	if text == "" {
		return "", false
	}
	redacted = text
	if privateKeyBlock.MatchString(redacted) {
		redacted = privateKeyBlock.ReplaceAllString(redacted, "[redacted private key]")
		found = true
	}
	if bearerToken.MatchString(redacted) {
		redacted = bearerToken.ReplaceAllStringFunc(redacted, func(match string) string {
			if index := strings.IndexFunc(match, unicode.IsSpace); index >= 0 {
				return match[:index+1] + "[redacted]"
			}
			return "[redacted]"
		})
		found = true
	}
	if basicToken.MatchString(redacted) {
		redacted = basicToken.ReplaceAllStringFunc(redacted, func(match string) string {
			if index := strings.IndexFunc(match, unicode.IsSpace); index >= 0 {
				return match[:index+1] + "[redacted]"
			}
			return "[redacted]"
		})
		found = true
	}
	if skAPIKey.MatchString(redacted) {
		redacted = skAPIKey.ReplaceAllString(redacted, "[redacted]")
		found = true
	}
	if awsAccessKey.MatchString(redacted) {
		redacted = awsAccessKey.ReplaceAllString(redacted, "[redacted]")
		found = true
	}
	if secretAssignment.MatchString(redacted) {
		redacted = secretAssignment.ReplaceAllString(redacted, "$1$2$3[redacted]")
		found = true
	}
	return redacted, found
}
