// Package textenc repairs command output that arrives in a non-UTF-8 Windows
// console encoding.
//
// On a Chinese Windows machine, native programs (python, cmd built-ins, ...)
// write console-code-page bytes (cp936/GBK) to stdout/stderr; when a parent
// captures the output of a Windows PowerShell host it can also be UTF-16LE. Rig
// exposes captured bytes as a Go string, so those bytes are invalid UTF-8: they
// render as U+FFFD mojibake in the tool card and reach the model corrupted.
//
// RepairOutput/RepairBytes convert such output back to valid UTF-8. Output that
// is already well-formed UTF-8 is returned unchanged, so normal Linux/macOS
// output and the tool's own UTF-8 text are never altered.
package textenc

import (
	"encoding/binary"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// RepairBytes decodes one command-output byte slice to UTF-8 text.
func RepairBytes(raw []byte) string {
	return RepairOutput(string(raw))
}

// RepairOutput returns s unchanged when it is well-formed UTF-8. Otherwise it
// tries to decode s as UTF-16LE (explicit BOM first, then text that clearly has
// the UTF-16LE NUL pattern) and then as GBK/cp936, returning the first decoding
// that covers the whole input. When no encoding fits, invalid bytes become
// U+FFFD while every valid UTF-8 segment (all ASCII included) is preserved.
//
// Capture limits can cut a multi-byte character in half before the repair runs
// (Rig's shell tools bound combined output by bytes and append a truncation
// notice). Such an artifact is a single short invalid run right before the
// trailing notice; it is repaired as damaged UTF-8 instead of being misread as
// GBK, so a large valid UTF-8 output never turns into mojibake.
//
// RepairOutput is total: it never panics and always returns valid UTF-8.
func RepairOutput(s string) string {
	if s == "" {
		return s
	}
	raw := []byte(s)
	// A byte-order mark is unambiguous: trust it before the UTF-8 fast path
	// because UTF-16LE ASCII text is technically valid UTF-8 full of NULs.
	if text, ok := decodeUTF16LEWithBOM(raw); ok {
		return text
	}
	// Pure-ASCII UTF-16LE and mixed UTF-16LE both put 0x00 in most odd byte
	// positions. The all-zero-odd case is unambiguous, so it also precedes the
	// UTF-8 fast path; the weaker ratio check runs after GBK, which wins for
	// the (common) GBK majority case.
	if text, ok := decodeUTF16LENoBOM(raw, 1); ok {
		return text
	}
	if utf8.ValidString(s) {
		return s
	}
	if isTruncatedUTF8Capture(s) {
		return sanitizeUTF8(s)
	}
	if text, ok := decodeGBK(raw); ok {
		return text
	}
	if text, ok := decodeUTF16LENoBOM(raw, 4); ok {
		return text
	}
	return sanitizeUTF8(s)
}

// repair constants.
const (
	// maxCaptureRepairBytes is the largest invalid run treated as a capture-cut
	// artifact (a partial 1-4 byte character: one invalid run of at most 3-4
	// bytes, with slack for a known truncation notice).
	maxCaptureRepairBytes = 8
	// captureTailWindow bounds how close to the end that run must be. Rig's
	// truncation notice ("\n[truncated: ...]") is shorter than this.
	captureTailWindow = 128
	// minGBKToleranceLength is the smallest decoded text that accepts rare
	// replacement runes from a cut-off tail. Capture limits only truncate large
	// outputs, so small damaged strings (e.g. UTF-8 plus one stray byte) still
	// fall through to byte repair instead of being guessed as GBK.
	minGBKToleranceLength = 1024
	// maxGBKReplacementRatio tolerates one undecodable byte per this many bytes
	// in a large GBK output cut mid-character.
	maxGBKReplacementRatio = 256
)

// isTruncatedUTF8Capture reports whether s looks like valid UTF-8 that a
// capture limit cut mid-character: one short invalid byte run near the end and
// otherwise valid text.
func isTruncatedUTF8Capture(s string) bool {
	invalid, runs, firstInvalidStart, lastInvalidEnd := damagedUTF8(s)
	if invalid == 0 || invalid > maxCaptureRepairBytes || runs != 1 {
		return false
	}
	if lastInvalidEnd < len(s) {
		// Truncated by a bounded capture: valid suffix (the truncation notice)
		// follows the cut, close to the end of the content.
		return len(s)-lastInvalidEnd <= captureTailWindow && invalid*20 <= len(s)
	}
	// Cut exactly at the end, without a notice: only treat this as damaged
	// UTF-8 when the prefix is real multi-byte UTF-8 text. A large GBK output
	// ending in Chinese keeps the invalid bytes scattered, so it does not
	// match (runs != 1), and an ASCII-only prefix is left for the GBK decode.
	return len(s) >= minGBKToleranceLength &&
		countNonASCIIBytes(s[:firstInvalidStart]) >= 64 &&
		invalid*20 <= len(s)
}

// damagedUTF8 returns the number of invalid bytes in s, how many contiguous
// invalid byte runs they form, and the first/last offsets around those runs.
func damagedUTF8(s string) (invalid, runs, firstInvalidStart, lastInvalidEnd int) {
	inRun := false
	for index := 0; index < len(s); {
		r, size := utf8.DecodeRuneInString(s[index:])
		if r == utf8.RuneError && size == 1 {
			if invalid == 0 {
				firstInvalidStart = index
			}
			invalid++
			if !inRun {
				runs++
				inRun = true
			}
			lastInvalidEnd = index + 1
			index++
			continue
		}
		inRun = false
		index += size
	}
	return invalid, runs, firstInvalidStart, lastInvalidEnd
}

// countNonASCIIBytes returns how many bytes of the valid UTF-8 prefix s are
// part of multi-byte runes.
func countNonASCIIBytes(s string) int {
	nonASCII := 0
	for index := 0; index < len(s); index++ {
		if s[index] >= utf8.RuneSelf {
			nonASCII++
		}
	}
	return nonASCII
}

// utf16LEBOM is the little-endian byte-order mark.
var utf16LEBOM = [...]byte{0xFF, 0xFE}

// decodeUTF16LEWithBOM decodes raw when it starts with the UTF-16LE BOM.
func decodeUTF16LEWithBOM(raw []byte) (string, bool) {
	if len(raw) < len(utf16LEBOM) || raw[0] != utf16LEBOM[0] || raw[1] != utf16LEBOM[1] {
		return "", false
	}
	return decodeUTF16LE(raw[len(utf16LEBOM):])
}

// decodeUTF16LENoBOM decodes raw as UTF-16LE when at least 1/perPairZeros of
// its odd byte positions are 0x00 and the result is plausible text. perPairZeros
// is 1 for "every odd byte is NUL" (unambiguous ASCII UTF-16LE) and larger for
// the ratio heuristic. Even length is required: text is encoded in code units.
func decodeUTF16LENoBOM(raw []byte, perPairZeros int) (string, bool) {
	pairs := len(raw) / 2
	if perPairZeros < 1 || pairs < 2 || len(raw)%2 != 0 {
		return "", false
	}
	zeros := 0
	for index := 1; index < len(raw); index += 2 {
		if raw[index] == 0 {
			zeros++
		}
	}
	// zeros/pairs >= 1/perPairZeros, without floats.
	if zeros*perPairZeros < pairs {
		return "", false
	}
	text, ok := decodeUTF16LE(raw)
	if !ok || !plausibleText(text) {
		return "", false
	}
	return text, true
}

// decodeUTF16LE decodes little-endian UTF-16, replacing unpaired surrogates with
// U+FFFD (the standard library's utf16.Decode behavior).
func decodeUTF16LE(raw []byte) (string, bool) {
	if len(raw)%2 != 0 {
		return "", false
	}
	units := make([]uint16, len(raw)/2)
	for index := range units {
		units[index] = binary.LittleEndian.Uint16(raw[index*2:])
	}
	return string(utf16.Decode(units)), true
}

// decodeGBK decodes raw as GBK/cp936 when the whole input is representable.
//
// The x/text GBK decoder substitutes U+FFFD for undecodable bytes instead of
// returning an error, so "the whole input decoded" means no replacement rune in
// the result. A decoded NUL is rejected too: text output carrying NUL almost
// always means the bytes were really UTF-16LE, and returning the GBK reading
// there would keep the mojibake.
//
// Large outputs may still be cut mid-character by a capture limit; for those a
// single rare replacement rune (one bad byte before the truncation notice) is
// tolerated so the readable rest of the output is decoded rather than lost.
func decodeGBK(raw []byte) (string, bool) {
	decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(raw)
	if err != nil {
		return "", false
	}
	text := string(decoded)
	if strings.IndexByte(text, 0) >= 0 {
		return "", false
	}
	replacements := strings.Count(text, "\uFFFD")
	if replacements == 0 {
		return text, true
	}
	if len(text) >= minGBKToleranceLength && replacements*maxGBKReplacementRatio <= len(text) {
		return text, true
	}
	return "", false
}

// plausibleText reports whether a decode candidate looks like console text
// rather than binary: no NUL/unpaired-surrogate replacements, and no control
// characters other than tab, carriage return and line feed.
func plausibleText(text string) bool {
	if text == "" {
		return false
	}
	for _, r := range text {
		switch r {
		case '\t', '\n', '\r':
			continue
		}
		if r == utf8.RuneError || r < 0x20 || (r >= 0x7F && r < 0xA0) {
			return false
		}
	}
	return true
}

// sanitizeUTF8 keeps every valid UTF-8 rune of s and replaces each invalid byte
// with U+FFFD. It is the last resort; ASCII and any readable segments survive.
func sanitizeUTF8(s string) string {
	var builder strings.Builder
	builder.Grow(len(s) + 8)
	for index := 0; index < len(s); {
		r, size := utf8.DecodeRuneInString(s[index:])
		if r == utf8.RuneError && size == 1 {
			builder.WriteRune(utf8.RuneError)
			index++
			continue
		}
		builder.WriteString(s[index : index+size])
		index += size
	}
	return builder.String()
}
