package tools

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTerminalDecodeDeltaCarriesSplitUTF8 covers the terminal_send carry
// (BUG-4): a multi-byte character split across two contiguous deltas is emitted
// once, not as replacement characters.
func TestTerminalDecodeDeltaCarriesSplitUTF8(t *testing.T) {
	s := &terminalSession{}
	// 中 = E4 B8 AD across two sends.
	if got := s.decodeDelta([]byte{0xE4}, 0, 1); got != "" {
		t.Fatalf("first delta = %q, want empty (held)", got)
	}
	if got := s.decodeDelta([]byte{0xB8, 0xAD}, 1, 3); got != "中" {
		t.Fatalf("second delta = %q, want 中", got)
	}
	if got := s.decodeDelta([]byte(" tail"), 3, 8); got != " tail" {
		t.Fatalf("following delta = %q", got)
	}
}

// TestTerminalDecodeDeltaResync covers a non-contiguous window: the decoder must
// restart rather than prepend a stale carry from an older region.
func TestTerminalDecodeDeltaResync(t *testing.T) {
	s := &terminalSession{}
	if got := s.decodeDelta([]byte{0xE4}, 0, 1); got != "" {
		t.Fatalf("held prefix = %q", got)
	}
	got := s.decodeDelta([]byte{0xE4, 0xB8, 0xAD}, 10, 13)
	if got != "中" {
		t.Fatalf("resynced window = %q, want 中", got)
	}
}

// TestCappedBufferDecodesBeforeTruncating covers the "decode then truncate"
// order: a capped Chinese buffer stays valid UTF-8 even when truncation lands
// inside a multi-byte character.
func TestCappedBufferDecodesBeforeTruncating(t *testing.T) {
	b := newCappedBuffer(6)
	if _, err := b.Write([]byte("中文路径")); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := b.String()
	if !utf8.ValidString(out) {
		t.Fatalf("truncated output is not valid UTF-8: %q", out)
	}
	if !strings.HasPrefix(out, "中文") {
		t.Fatalf("truncated output lost the decoded prefix: %q", out)
	}

	small := newCappedBuffer(64)
	if _, err := small.Write([]byte("中文路径")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := small.String(); got != "中文路径" {
		t.Fatalf("untuncated output = %q, want 中文路径", got)
	}
}
