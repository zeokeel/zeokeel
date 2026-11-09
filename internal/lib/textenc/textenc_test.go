package textenc

import (
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// mustGBK encodes UTF-8 text as GBK/cp936 console bytes and checks that the
// bytes are not valid UTF-8, so a table case really exercises the decoder.
func mustGBK(t *testing.T, text string) []byte {
	t.Helper()
	encoded, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(text))
	if err != nil {
		t.Fatalf("encode %q as GBK: %v", text, err)
	}
	if utf8.Valid(encoded) {
		t.Fatalf("GBK bytes for %q are valid UTF-8; the case would not test decoding", text)
	}
	return encoded
}

// mustUTF16LE encodes text as little-endian UTF-16, with or without a BOM.
func mustUTF16LE(t *testing.T, text string, withBOM bool) []byte {
	t.Helper()
	units := utf16.Encode([]rune(text))
	raw := make([]byte, 0, 2*len(units)+2)
	if withBOM {
		raw = append(raw, 0xFF, 0xFE)
	}
	for _, unit := range units {
		raw = binary.LittleEndian.AppendUint16(raw, unit)
	}
	return raw
}

// repairOutputCases are the shared table rows; the property test below reuses
// their inputs. Every case starts from raw output bytes captured on Windows.
func repairOutputCases(t *testing.T) []struct{ name, input, want string } {
	t.Helper()
	gbkChinese := "构建成功：中文输出"
	gbkError := "\x1b[31m错误: 编译失败\x1b[0m\n"
	utf16Mixed := "构建 ok\n"

	// Rig's shell tools bound captured output by bytes and append this notice,
	// so a Windows/UTF-8 capture can be cut mid-character before repair runs.
	notice := "\n[truncated: output exceeded 32768 bytes]"
	longUTF8 := strings.Repeat("构建成功", 1000) // 12000 bytes, 3 per rune
	utf8Cut := 5000                          // 3*1666 + 2: the next rune is split
	longGBKText := strings.Repeat("构建成功输出", 1000)
	longGBK := mustGBK(t, longGBKText) // 12000 bytes, 2 per rune

	return []struct{ name, input, want string }{
		{name: "empty", input: "", want: ""},
		{name: "plain ascii untouched", input: "plain ascii\n", want: "plain ascii\n"},
		{name: "valid chinese utf8 untouched", input: "构建成功：中文输出 ✅\n", want: "构建成功：中文输出 ✅\n"},
		{name: "gbk chinese decoded", input: string(mustGBK(t, gbkChinese)), want: gbkChinese},
		{name: "gbk error line with ansi decoded", input: string(mustGBK(t, gbkError)), want: gbkError},
		{name: "utf16le bom chinese decoded", input: string(mustUTF16LE(t, "中文输出\n", true)), want: "中文输出\n"},
		{name: "utf16le bom ascii decoded", input: string(mustUTF16LE(t, "hello\n", true)), want: "hello\n"},
		{name: "utf16le without bom ascii decoded", input: string(mustUTF16LE(t, "hello\n", false)), want: "hello\n"},
		{name: "utf16le without bom mixed text decoded", input: string(mustUTF16LE(t, utf16Mixed, false)), want: utf16Mixed},
		{name: "single invalid byte replaced", input: "\xff", want: "\uFFFD"},
		{name: "ascii kept around invalid bytes", input: "abc\xffdef", want: "abc\uFFFDdef"},
		{name: "valid chinese kept around invalid byte", input: "中文\xff", want: "中文\uFFFD"},
		// 0xE4 0xB8 is a valid GBK sequence, so per the documented ordering
		// ("whole input decodes as GBK") the GBK reading wins over byte repair.
		{name: "gbk-decodable bytes prefer gbk over per byte repair", input: "\xe4\xb8", want: "涓"},
		{name: "undecodable invalid bytes replaced per byte", input: "\xe4\xb8\xff", want: "\uFFFD\uFFFD\uFFFD"},
		{name: "gbk with undecodable tail falls back", input: string(append(mustGBK(t, "中文"), 0xFF)), want: strings.Repeat("\uFFFD", 5)},
		// Capture-limit truncation must not turn large valid UTF-8 into GBK
		// mojibake: only the split character is repaired.
		{
			name:  "large valid utf8 cut mid-rune repaired not gbk decoded",
			input: longUTF8[:utf8Cut] + notice,
			want:  longUTF8[:utf8Cut-2] + "\uFFFD\uFFFD" + notice,
		},
		{
			name:  "large valid utf8 cut at rune boundary untouched",
			input: longUTF8[:utf8Cut-2] + notice,
			want:  longUTF8[:utf8Cut-2] + notice,
		},
		{
			name:  "large valid utf8 with stray trailing byte repaired",
			input: longUTF8 + "\xff",
			want:  longUTF8 + "\uFFFD",
		},
		// A large GBK output cut mid-character still decodes: the single split
		// byte becomes U+FFFD and the rest is readable Chinese.
		{
			name:  "large gbk output cut mid-character decoded",
			input: string(longGBK[:5001]) + notice,
			want:  string([]rune(longGBKText)[:2500]) + "\uFFFD" + notice,
		},
	}
}

func TestRepairOutput(t *testing.T) {
	for _, testCase := range repairOutputCases(t) {
		t.Run(testCase.name, func(t *testing.T) {
			got := RepairOutput(testCase.input)
			if got != testCase.want {
				t.Fatalf("RepairOutput(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("RepairOutput(%q) returned invalid UTF-8 %q", testCase.input, got)
			}
			if again := RepairOutput(got); again != got {
				t.Fatalf("RepairOutput is not idempotent for %q: %q -> %q", testCase.input, got, again)
			}
		})
	}
}

// TestRepairBytesMatchesRepairOutput pins the byte-slice entry point used by
// terminal capture.
func TestRepairBytesMatchesRepairOutput(t *testing.T) {
	raw := append(mustGBK(t, "终端输出"), '\n')
	if got, want := RepairBytes(raw), RepairOutput(string(raw)); got != want {
		t.Fatalf("RepairBytes = %q, want RepairOutput %q", got, want)
	}
	if got := RepairBytes([]byte("already utf-8 中文")); got != "already utf-8 中文" {
		t.Fatalf("RepairBytes changed valid UTF-8 to %q", got)
	}
}

// TestUTF16LEASCIIIsValidUTF8 documents why the BOM/NUL-pattern checks run
// before the UTF-8 fast path: pure-ASCII UTF-16LE is technically valid UTF-8
// full of NUL bytes, so the fast path alone would leave the mojibake in place.
func TestUTF16LEASCIIIsValidUTF8(t *testing.T) {
	raw := mustUTF16LE(t, "hello", false)
	if !utf8.Valid(raw) {
		t.Fatalf("test premise broken: ASCII UTF-16LE bytes %q are not valid UTF-8", raw)
	}
	if got := RepairBytes(raw); got != "hello" {
		t.Fatalf("RepairBytes(ASCII UTF-16LE) = %q, want hello", got)
	}
}

// TestRepairOutputNeverPanicsOnBinary is a small fuzz-style loop over hostile
// byte strings; the helper must always return valid UTF-8 without panicking.
func TestRepairOutputNeverPanicsOnBinary(t *testing.T) {
	inputs := []string{
		"\x00", "\x00\x00", "\xff\xfe", "\xfe\xff", "\x80",
		string([]byte{0xFF, 0xFE, 0x00}), // BOM + odd byte
		string([]byte{0xE4, 0xB8, 0xAD, 0xFF, 0x00, 0x01}),
		strings.Repeat("\xff", 16),
	}
	for _, input := range inputs {
		got := RepairOutput(input)
		if !utf8.ValidString(got) {
			t.Fatalf("RepairOutput(%q) = %q is not valid UTF-8", input, got)
		}
	}
}
