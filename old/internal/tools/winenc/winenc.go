// Package winenc normalizes raw child-process output to valid UTF-8.
//
// Windows console programs choose their own output encoding. Windows
// PowerShell 5.1 writes the system ANSI code page (CP936/GBK on Chinese
// Windows) when its stdout is redirected and it has no console, and cmd.exe
// built-ins use the OEM code page. Go's os/exec does no conversion, so a tool
// that turns those bytes into a Go string must decode them first or they become
// irreversible U+FFFD in the session log.
//
// The package is a no-op outside Windows: UTF-8 already passes through and
// non-UTF-8 bytes are returned unchanged, matching the pre-existing behavior.
package winenc

import "unicode/utf8"

// replacement is what a decoder emits for bytes it cannot interpret.
const replacement = "\uFFFD"

// DecodeProcessOutput converts one complete child-process output buffer into a
// valid UTF-8 string. Valid UTF-8 is returned untouched. On Windows, bytes that
// are not UTF-8 are decoded with the process code page; if that fails too, the
// invalid bytes are replaced with U+FFFD so JSON persistence cannot destroy
// surrounding text. Other platforms return the bytes unchanged.
func DecodeProcessOutput(data []byte) string {
	return decodeWith(data, decodeComplete, invalidFallback)
}

// decodeWith is the platform-independent policy behind DecodeProcessOutput.
// complete reports whether the whole buffer decodes cleanly under the platform
// code page; fallback renders bytes that cannot be decoded on this platform.
func decodeWith(data []byte, complete func([]byte) (string, bool), fallback func([]byte) string) string {
	if len(data) == 0 {
		return ""
	}
	if utf8.Valid(data) {
		return string(data)
	}
	if text, ok := complete(data); ok {
		return text
	}
	return fallback(data)
}

// utf8IncompleteTail returns the length of a trailing incomplete UTF-8
// sequence (1..3 bytes), or 0 when the bytes are valid UTF-8 or contain an
// invalid sequence that more bytes cannot complete. The caller must have
// already established that data is not valid UTF-8.
func utf8IncompleteTail(data []byte) int {
	for k := 1; k <= 3 && k <= len(data); k++ {
		prefix := data[:len(data)-k]
		if !utf8.Valid(prefix) {
			// The invalid bytes are earlier in the buffer, not a split tail at
			// this width; try a wider trailing fragment.
			continue
		}
		tail := data[len(data)-k:]
		if utf8.RuneStart(tail[0]) && !utf8.FullRune(tail) {
			return k
		}
	}
	return 0
}

// StreamDecoder decodes a byte stream whose chunks may split a multi-byte
// character across calls (a terminal session's incremental output). An
// incomplete trailing character is held until a later chunk completes it.
// A zero value is ready to use.
type StreamDecoder struct {
	carry []byte
}

// Decode consumes the next chunk and returns the text that is complete so far.
// Bytes of an incomplete trailing character are retained internally.
func (d *StreamDecoder) Decode(chunk []byte) string {
	if len(chunk) == 0 {
		return ""
	}
	data := make([]byte, 0, len(d.carry)+len(chunk))
	data = append(data, d.carry...)
	data = append(data, chunk...)

	// A UTF-8 character split across the boundary is held before any code-page
	// attempt: the platform decoder would otherwise misread its leading bytes.
	if n := utf8IncompleteTail(data); n > 0 {
		d.carry = append(d.carry[:0], data[len(data)-n:]...)
		return string(data[:len(data)-n])
	}
	// A DBCS code page (CP936/CP932/CP949/CP950) can leave a trailing lead byte
	// with no trail byte; the platform helper reports those bytes.
	if n := platformIncompleteTail(data); n > 0 {
		d.carry = append(d.carry[:0], data[len(data)-n:]...)
		if text, ok := decodeComplete(data[:len(data)-n]); ok {
			return text
		}
		return invalidFallback(data[:len(data)-n])
	}

	if text, ok := decodeComplete(data); ok {
		d.carry = d.carry[:0]
		return text
	}

	// Hold the shortest trailing fragment that makes the rest decode cleanly:
	// that fragment is a character split across the chunk boundary.
	width := maxCharWidth()
	for cut := 1; cut <= width && cut < len(data); cut++ {
		if text, ok := decodeComplete(data[:len(data)-cut]); ok {
			d.carry = append(d.carry[:0], data[len(data)-cut:]...)
			return text
		}
	}

	// The chunk is not merely truncated: it starts inside a character from a
	// stream region that is no longer contiguous. Drop the short leading
	// remnant and decode the rest.
	for lead := 1; lead <= width && lead < len(data); lead++ {
		if text, ok := decodeComplete(data[lead:]); ok {
			d.carry = d.carry[:0]
			return invalidFallback(data[:lead]) + text
		}
	}

	d.carry = d.carry[:0]
	return invalidFallback(data)
}
