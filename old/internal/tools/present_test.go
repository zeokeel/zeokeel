package tools

import (
	"encoding/json"
	"testing"
)

func decodeArtifactResult(t *testing.T, raw string) ArtifactResult {
	t.Helper()
	var out ArtifactResult
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("present result is not JSON: %v\n%s", err, raw)
	}
	return out
}

func TestPresentToolRecordsArtifacts(t *testing.T) {
	tool := NewPresentTool()
	if got := tool.Name(); got != "present" {
		t.Fatalf("Name = %q, want present", got)
	}

	file := decodeArtifactResult(t, mustInvoke(t, tool, map[string]any{
		"title": "Test report", "path": "reports/unit.md",
	}))
	if file.Kind != "artifact" || file.Title != "Test report" || file.Path != "reports/unit.md" || file.Type != artifactFile {
		t.Fatalf("file result = %+v", file)
	}

	markdown := decodeArtifactResult(t, mustInvoke(t, tool, map[string]any{
		"title": "Plan", "markdown": "# Plan\n1. ship", "type": "markdown",
	}))
	if markdown.Type != artifactMarkdown || markdown.Markdown == "" {
		t.Fatalf("markdown result = %+v", markdown)
	}

	artifacts := tool.Artifacts()
	if len(artifacts) != 2 {
		t.Fatalf("Artifacts() = %+v, want 2", artifacts)
	}
	if artifacts[0].Title != "Test report" || artifacts[0].Path != "reports/unit.md" {
		t.Fatalf("first artifact = %+v", artifacts[0])
	}
	if artifacts[1].Kind != artifactMarkdown || artifacts[1].Markdown != "# Plan\n1. ship" {
		t.Fatalf("second artifact = %+v", artifacts[1])
	}

	// Artifacts returns a copy of the slice.
	artifacts[0].Title = "mutated"
	if got := tool.Artifacts()[0].Title; got != "Test report" {
		t.Fatalf("tool artifact changed through Artifacts(): %q", got)
	}
}

func TestPresentToolValidatesArguments(t *testing.T) {
	tool := NewPresentTool()
	cases := []map[string]any{
		{},
		{"path": "a.md"},
		{"title": "only a title"},
		{"title": "bad type", "path": "a.md", "type": "video"},
	}
	for _, args := range cases {
		if out, err := invokeTool(t, tool, args); err == nil {
			t.Fatalf("args %v: err = nil, out = %q", args, out)
		}
	}
	if got := len(tool.Artifacts()); got != 0 {
		t.Fatalf("Artifacts() after failures = %d, want 0", got)
	}
}
