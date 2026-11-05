//go:build !windows

package winenc

import "unicode/utf8"

// decodeComplete reports whether data is valid UTF-8. Non-Windows process
// output is expected to be UTF-8 already, so no code-page conversion applies.
func decodeComplete(data []byte) (string, bool) {
	if utf8.Valid(data) {
		return string(data), true
	}
	return "", false
}

// maxCharWidth is the longest UTF-8 sequence that can be split across chunks.
func maxCharWidth() int { return 3 }

// platformIncompleteTail has no platform-specific meaning: split UTF-8
// sequences are already handled by utf8IncompleteTail.
func platformIncompleteTail(data []byte) int { return 0 }

// invalidFallback preserves the historical passthrough behavior outside
// Windows: output bytes are handed through unchanged rather than rewritten.
func invalidFallback(data []byte) string {
	return string(data)
}
