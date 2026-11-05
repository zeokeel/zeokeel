package winenc

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

// GBK fixtures for the Chinese text from the BUG-4 session evidence.
var (
	gbkTempFiles = []byte{0xC1, 0xD9, 0xCA, 0xB1, 0xCE, 0xC4, 0xBC, 0xFE}             // 临时文件
	gbkNotFound  = []byte{0xD5, 0xD2, 0xB2, 0xBB, 0xB5, 0xBD, 0xC2, 0xB7, 0xBE, 0xB6} // 找不到路径
)

func TestDecodeProcessOutputPassesValidUTF8(t *testing.T) {
	for _, want := range []string{
		"",
		"plain ascii",
		"中文路径 C:\\Users\\LostManiac\\Desktop\\临时文件",
		"emoji 😀 and accents é",
		strings.Repeat("中文 abc ", 5000),
	} {
		if got := DecodeProcessOutput([]byte(want)); got != want {
			t.Fatalf("DecodeProcessOutput(%d bytes) = %q, want passthrough", len(want), got)
		}
	}
	if got := DecodeProcessOutput(nil); got != "" {
		t.Fatalf("nil = %q, want empty", got)
	}
}

// TestDecodeWithPolicy covers the shared decode policy: valid UTF-8 wins over
// the platform converter, a clean platform decode wins over the fallback, and an
// undecodable buffer goes to the fallback.
func TestDecodeWithPolicy(t *testing.T) {
	converter := func(data []byte) (string, bool) {
		if string(data) == string(gbkTempFiles) {
			return "临时文件", true
		}
		if string(data) == string(gbkNotFound) {
			return "找不到路径", true
		}
		return "", false
	}
	fallback := func([]byte) string { return "FALLBACK" }

	if got := decodeWith([]byte("乱码? 中文"), converter, fallback); got != "乱码? 中文" {
		t.Fatalf("valid UTF-8 was not passed through: %q", got)
	}
	if got := decodeWith(gbkTempFiles, converter, fallback); got != "临时文件" {
		t.Fatalf("GBK fixture decoded to %q", got)
	}
	if got := decodeWith(gbkNotFound, converter, fallback); got != "找不到路径" {
		t.Fatalf("GBK fixture decoded to %q", got)
	}
	if got := decodeWith([]byte{0x80, 0x81, 0x82}, converter, fallback); got != "FALLBACK" {
		t.Fatalf("undecodable buffer = %q, want fallback", got)
	}
}

func TestDecodeProcessOutputNeverPanicsOnBinary(t *testing.T) {
	for _, data := range [][]byte{
		{0x00, 0x01, 0x02},
		{0xff, 0xfe, 0xfd},
		bytes.Repeat([]byte{0x81}, 64),
	} {
		_ = DecodeProcessOutput(data) // must not panic; platform policy decides bytes
	}
}

func TestDecodeProcessOutputLargeBuffer(t *testing.T) {
	big := bytes.Repeat([]byte("ascii 中文 "), 80000) // ~1.1 MB valid UTF-8
	if got := DecodeProcessOutput(big); got != string(big) {
		t.Fatalf("large UTF-8 buffer was rewritten (%d -> %d bytes)", len(big), len(got))
	}
	invalid := append(append([]byte{}, big...), 0xff)
	got := DecodeProcessOutput(invalid)
	if runtime.GOOS == "windows" && !utf8.ValidString(got) {
		t.Fatalf("Windows decode left invalid UTF-8: %q", got[len(got)-8:])
	}
}

func TestStreamDecoderSplitsUTF8(t *testing.T) {
	// 中 is E4 B8 AD; split it one byte at a time.
	d := &StreamDecoder{}
	if got := d.Decode([]byte{0xE4}); got != "" {
		t.Fatalf("first fragment produced %q, want hold", got)
	}
	if got := d.Decode([]byte{0xB8}); got != "" {
		t.Fatalf("second fragment produced %q, want hold", got)
	}
	if got := d.Decode([]byte{0xAD}); got != "中" {
		t.Fatalf("completed fragment = %q, want 中", got)
	}
	if got := d.Decode([]byte("done 中文")); got != "done 中文" {
		t.Fatalf("complete chunk after carry = %q", got)
	}

	// A 4-byte emoji split across four chunks.
	d = &StreamDecoder{}
	var text strings.Builder
	for _, b := range []byte("😀") {
		text.WriteString(d.Decode([]byte{b}))
	}
	if text.String() != "😀" {
		t.Fatalf("split emoji = %q", text.String())
	}

	// A complete chunk followed by a chunked split.
	d = &StreamDecoder{}
	var out strings.Builder
	out.WriteString(d.Decode([]byte("prefix ")))
	out.WriteString(d.Decode([]byte{0xE4, 0xB8}))
	out.WriteString(d.Decode([]byte{0xAD, ' ', 'x'}))
	if got := out.String(); got != "prefix 中 x" {
		t.Fatalf("chunked stream = %q", got)
	}
}

func TestStreamDecoderEmptyChunkKeepsCarry(t *testing.T) {
	d := &StreamDecoder{}
	if got := d.Decode([]byte{0xE4}); got != "" {
		t.Fatalf("hold = %q", got)
	}
	if got := d.Decode(nil); got != "" {
		t.Fatalf("empty chunk = %q, want empty", got)
	}
	if got := d.Decode([]byte{0xB8, 0xAD}); got != "中" {
		t.Fatalf("carry across empty chunk = %q", got)
	}
}
