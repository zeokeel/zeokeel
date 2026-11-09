package rightpanel

// Unit tests for the completion-toast / row preview cleaner: a teammate's final
// answer is markdown, and a raw preview used to leak fences, heading markers and
// list bullets into the one-line toast.

import (
	"strings"
	"testing"
)

func TestTeamOutputPreviewStripsMarkdown(t *testing.T) {
	answer := "# 脚本回复\n\n- 工作目录已确认\n- 可以继续下一步了\n\n> 引用一段\n\n```text\n/data/mygo\n```\n\n结尾 **加粗** `代码`"
	preview := teamOutputPreview(answer, 80)
	for _, want := range []string{"脚本回复", "工作目录已确认", "可以继续下一步了", "引用一段", "结尾"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview = %q, want %q", preview, want)
		}
	}
	for _, unwanted := range []string{"#", "```", "- ", "> ", "/data/mygo", "`"} {
		if strings.Contains(preview, unwanted) {
			t.Fatalf("preview = %q still contains markdown noise %q", preview, unwanted)
		}
	}
	if strings.Contains(preview, "\n") {
		t.Fatalf("preview = %q, want one line", preview)
	}
}

func TestTeamOutputPreviewNumbersAndTruncation(t *testing.T) {
	preview := teamOutputPreview("1. 第一项\n2) 第二项\n\n长文本 "+strings.Repeat("啊", 200), 40)
	if strings.HasPrefix(preview, "1.") || strings.HasPrefix(preview, "2)") {
		t.Fatalf("preview = %q, want the ordered-list markers stripped", preview)
	}
	runes := []rune(preview)
	if len(runes) > 41 || !strings.HasSuffix(preview, "…") {
		t.Fatalf("preview = %q (%d runes), want <=41 runes with an ellipsis", preview, len(runes))
	}
	if !strings.Contains(preview, "第一项") || !strings.Contains(preview, "第二项") {
		t.Fatalf("preview = %q, want both list items", preview)
	}
}

func TestTeamOutputPreviewFencedOnlyFallsBackEmpty(t *testing.T) {
	if preview := teamOutputPreview("```text\n/data/mygo\n```", 80); preview != "" {
		t.Fatalf("fenced-only preview = %q, want empty", preview)
	}
	if preview := teamOutputPreview("   \n\n", 80); preview != "" {
		t.Fatalf("blank preview = %q, want empty", preview)
	}
}
