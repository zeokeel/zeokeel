package memory

import (
	"math"
	"strings"
	"testing"
)

func TestNormalizeContent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"trim", "  hello  ", "hello"},
		{"crlf", "line one\r\nline two", "line one\nline two"},
		{"cr", "line one\rline two", "line one\nline two"},
		{"inner whitespace kept", "a  b\tc", "a  b\tc"},
	}
	for _, tc := range cases {
		got, err := NormalizeContent(tc.in)
		if err != nil {
			t.Errorf("%s: NormalizeContent: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: NormalizeContent(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}

	for _, bad := range []string{"", " \n\t ", "nul\x00byte", strings.Repeat("a", MaxContentBytes+1)} {
		if _, err := NormalizeContent(bad); err == nil {
			t.Errorf("NormalizeContent(%q) succeeded, want an error", truncateForLog(bad))
		}
	}
	exact := strings.Repeat("a", MaxContentBytes)
	if got, err := NormalizeContent(exact); err != nil || got != exact {
		t.Errorf("NormalizeContent(exactly %d bytes) = %d bytes, %v", MaxContentBytes, len(got), err)
	}
	multibyte := strings.Repeat("中", MaxContentBytes/len("中")+1)
	if _, err := NormalizeContent(multibyte); err == nil {
		t.Error("NormalizeContent(over-limit multibyte) succeeded, want an error")
	}
}

func truncateForLog(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}

func TestHash(t *testing.T) {
	first := Hash("  the release branch is release/2026  ")
	if len(first) != 64 {
		t.Fatalf("Hash = %q (len %d), want 64 hex chars", first, len(first))
	}
	if again := Hash("the release branch is release/2026"); again != first {
		t.Errorf("Hash is not trim-stable: %q != %q", again, first)
	}
	crlf := Hash("line one\r\nline two")
	lf := Hash("line one\nline two")
	if crlf != lf {
		t.Errorf("Hash is not line-ending-stable: %q != %q", crlf, lf)
	}
	if Hash("Release") == Hash("release") {
		t.Error("Hash ignores case; exact-hash dedupe would merge different content")
	}
	if Hash("a") == Hash("b") {
		t.Error("Hash collided for different content")
	}
}

func TestSimilarity(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		min  float64
		max  float64
	}{
		{"identical", "the release branch is release/2026", "the release branch is release/2026", 1, 1},
		{"case only", "Hello World", "hello world", 1, 1},
		{"whitespace only", "a  b\tc", "a b c", 1, 1},
		{"trailing punctuation", "the release branch is release/2026.", "the release branch is release/2026", 0.9, 1},
		{"chinese punctuation", "会话引用会作为第二条用户消息注入。", "会话引用会作为第二条用户消息注入", 0.9, 1},
		{"short identical", "go", "go", 1, 1},
		{"short unrelated", "go", "rust", 0, 0},
		{"empty identical", "", "", 1, 1},
		{"empty vs text", "", "anything", 0, 0},
		{"unrelated english", "the parser handles nested expressions", "deploy the release to staging tonight", 0, 0.15},
		{"unrelated chinese", "项目使用制表符缩进", "项目使用空格缩进", 0, 0.5},
	}
	for _, tc := range cases {
		got := Similarity(tc.a, tc.b)
		if got < tc.min-1e-9 || got > tc.max+1e-9 {
			t.Errorf("%s: Similarity(%q, %q) = %.4f, want in [%.2f, %.2f]",
				tc.name, tc.a, tc.b, got, tc.min, tc.max)
		}
		if reversed := Similarity(tc.b, tc.a); math.Abs(reversed-got) > 1e-9 {
			t.Errorf("%s: Similarity is not symmetric: %.6f vs %.6f", tc.name, got, reversed)
		}
	}

	// Exact hash equality must imply maximal similarity: MergeCandidates relies
	// on that ordering (update before supersede).
	content := "The team uses Conventional Commits for every change."
	if got := Similarity(content, content); got != 1 {
		t.Errorf("Similarity(exact content) = %v, want 1", got)
	}
}
