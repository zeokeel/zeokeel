// Package textutil holds the small string helpers the product shared through
// private copies. The fourteen firstNonEmpty duplicates of the 2026-11 audit
// (AR-17) converged here; the two variants preserve the two behaviors the
// copies actually had (verbatim vs trimmed), so a call site switches without
// changing semantics.
package textutil

import "strings"

// FirstNonEmpty returns the first non-empty value verbatim ("" when every
// value is empty). Whitespace-only values count as values: use
// FirstNonEmptyTrimmed when blank strings should be skipped.
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// FirstNonEmptyTrimmed returns the first non-blank value, trimmed of
// surrounding whitespace ("" when every value is blank).
func FirstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
