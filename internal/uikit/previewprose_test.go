package uikit_test

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/uikit"
)

// TestPreviewProseStripsMarkdown pins the prose preview cleaning: each Markdown
// structure the backend hands a one-line preview is dropped, not flattened into
// noise (2026-11 UI review P2). One case per structure kind.
func TestPreviewProseStripsMarkdown(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "fence block with language tag and content is dropped",
			in:   "先说明\n```bash\nls -la\nrm -rf x\n```\n后结论",
			want: "先说明 后结论",
		},
		{
			name: "tilde fence is dropped too",
			in:   "before\n~~~go\nfunc main() {}\n~~~\nafter",
			want: "before after",
		},
		{
			name: "heading markers",
			in:   "## 发布检查\n正文内容",
			want: "发布检查 正文内容",
		},
		{
			name: "list markers",
			in:   "- 第一项\n* 第二项\n+ 第三项",
			want: "第一项 第二项 第三项",
		},
		{
			name: "quote marker",
			in:   "> 引用一句\n> 再一句",
			want: "引用一句 再一句",
		},
		{
			name: "emphasis markers",
			in:   "**加粗** 和 __下划__ 与 *斜体*",
			want: "加粗 和 下划 与 斜体",
		},
		{
			name: "inline code markers",
			in:   "运行 `go test` 即可",
			want: "运行 go test 即可",
		},
		{
			name: "whitespace collapses to one line",
			in:   "第一行\n\n\t  第二行    spaced  \n第三行",
			want: "第一行 第二行 spaced 第三行",
		},
		{
			name: "all-structure input previews empty",
			in:   "```go\npackage main\n```\n## \n- ",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := uikit.PreviewProse(tc.in, 0); got != tc.want {
				t.Fatalf("PreviewProse(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestPreviewProseTruncates pins the rune bound and the trailing ellipsis (UTF-8
// safe, like ToolPreview).
func TestPreviewProseTruncates(t *testing.T) {
	in := "这是一段很长的散文预览内容需要被截断"
	got := uikit.PreviewProse(in, 8)
	if want := "这是一段很长的散…"; got != want {
		t.Fatalf("PreviewProse bound = %q, want %q", got, want)
	}
	// Under the bound: no ellipsis.
	if got := uikit.PreviewProse("短散文", 8); got != "短散文" {
		t.Fatalf("short prose = %q, want unchanged", got)
	}
	// max <= 0 leaves the length unbounded.
	long := strings.Repeat("字", 200)
	if got := uikit.PreviewProse(long, 0); got != long {
		t.Fatalf("max<=0 should not bound; got %d runes", len([]rune(got)))
	}
}

// TestPreviewProseKeepsToolCommandDistinct guards the boundary with ToolPreview:
// a tool command keeps its markers verbatim (ToolPreview), while prose loses
// them (PreviewProse). The two must not be swapped.
func TestPreviewProseKeepsToolCommandDistinct(t *testing.T) {
	command := "grep -n '*foo*' file.go"
	if got := uikit.ToolPreview(command, 0); got != command {
		t.Fatalf("ToolPreview must keep the command verbatim; got %q", got)
	}
	if got := uikit.PreviewProse(command, 0); got == command {
		t.Fatalf("PreviewProse should strip emphasis markers from prose; got %q", got)
	}
}
