package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

// NormalizeContent returns the canonical stored form of one memory body: line
// endings normalized to LF, surrounding whitespace trimmed. Empty content,
// content containing a NUL byte, and content larger than MaxContentBytes are
// rejected with a stable error.
func NormalizeContent(content string) (string, error) {
	normalized := canonicalText(content)
	if normalized == "" {
		return "", fmt.Errorf("memory: content is empty")
	}
	if strings.IndexByte(normalized, 0) >= 0 {
		return "", fmt.Errorf("memory: content contains a NUL byte")
	}
	if len(normalized) > MaxContentBytes {
		return "", fmt.Errorf("memory: content is %d bytes, maximum is %d", len(normalized), MaxContentBytes)
	}
	return normalized, nil
}

// canonicalText applies the stored-form normalization without validating:
// CRLF/CR to LF, then surrounding whitespace trimmed.
func canonicalText(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return strings.TrimSpace(content)
}

// Hash returns the lowercase sha256 hex digest of the canonical content. It is
// the value stored in memories.content_hash, so two contents that differ only
// in surrounding whitespace or line endings hash identically.
func Hash(content string) string {
	sum := sha256.Sum256([]byte(canonicalText(content)))
	return hex.EncodeToString(sum[:])
}

// Similarity returns the Jaccard similarity in [0, 1] of the rune-trigram sets
// of a and b after lowercasing and collapsing whitespace runs. Identical
// normalized texts score 1; texts shorter than three runes are compared as one
// whole gram; two empty texts score 1.
func Similarity(a, b string) float64 {
	gramsA := trigramSet(a)
	gramsB := trigramSet(b)
	if len(gramsA) == 0 && len(gramsB) == 0 {
		return 1
	}
	if len(gramsA) == 0 || len(gramsB) == 0 {
		return 0
	}
	intersection := 0
	for gram := range gramsA {
		if _, ok := gramsB[gram]; ok {
			intersection++
		}
	}
	union := len(gramsA) + len(gramsB) - intersection
	if union == 0 {
		return 1
	}
	return float64(intersection) / float64(union)
}

// trigramSet returns the set of rune trigrams of the normalized text. Texts
// shorter than three runes contribute their whole (trimmed) text as one gram,
// so two short identical strings score 1.
func trigramSet(text string) map[string]struct{} {
	runes := []rune(normalizeText(text))
	grams := make(map[string]struct{})
	switch {
	case len(runes) == 0:
		return grams
	case len(runes) < 3:
		grams[string(runes)] = struct{}{}
		return grams
	}
	for i := 0; i+3 <= len(runes); i++ {
		grams[string(runes[i:i+3])] = struct{}{}
	}
	return grams
}

// normalizeText lowercases and collapses every whitespace run to one ASCII
// space, then trims. Punctuation is preserved: it carries meaning in code and
// paths, and the 0.8 threshold still tolerates small edits.
func normalizeText(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))
	inSpace := false
	for _, r := range strings.ToLower(text) {
		if unicode.IsSpace(r) {
			if !inSpace {
				builder.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		inSpace = false
		builder.WriteRune(r)
	}
	return strings.TrimSpace(builder.String())
}
