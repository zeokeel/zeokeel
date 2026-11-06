//go:build windows

package winenc

import (
	"strings"
	"testing"
)

// TestDecodeProcessOutputGBKFixture covers the real Windows code-page decode:
// the bytes below are the CP936 encoding of the Chinese text from the BUG-4
// session evidence, and the process code page on a Chinese Windows host is 936.
func TestDecodeProcessOutputGBKFixture(t *testing.T) {
	for _, tc := range []struct {
		in   []byte
		want string
	}{
		{gbkTempFiles, "临时文件"},
		{gbkNotFound, "找不到路径"},
		{append([]byte("Get-Content : "), gbkNotFound...), "Get-Content : 找不到路径"},
	} {
		if got := DecodeProcessOutput(tc.in); got != tc.want {
			t.Fatalf("DecodeProcessOutput(% x) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestStreamDecoderGBKSplit proves a DBCS character split across chunks is
// carried instead of becoming a replacement rune (terminal_send deltas).
func TestStreamDecoderGBKSplit(t *testing.T) {
	data := []byte{0xC1, 0xD9, 0xCA, 0xB1, 0xCE, 0xC4, 0xBC, 0xFE} // 临时文件
	for cut := 1; cut < len(data); cut++ {
		d := &StreamDecoder{}
		var out strings.Builder
		out.WriteString(d.Decode(data[:cut]))
		out.WriteString(d.Decode(data[cut:]))
		if got := out.String(); got != "临时文件" {
			t.Fatalf("split at %d decoded to %q, want 临时文件", cut, got)
		}
	}
}
