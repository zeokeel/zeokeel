package memory

import (
	"strings"
	"testing"
)

func TestParseExtractionValidObject(t *testing.T) {
	raw := `{"memories":[
		{"scope":"global","kind":"user","content":"Prefers small commits.","confidence":0.9},
		{"scope":"workspace","kind":"project","content":"Release branch is release/2026.","confidence":0.7}
	]}`
	candidates, err := ParseExtraction(raw)
	if err != nil {
		t.Fatalf("ParseExtraction: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v, want 2", candidates)
	}
	if candidates[0].Scope != ScopeGlobal || candidates[0].Kind != KindUser || candidates[0].Content != "Prefers small commits." || candidates[0].Confidence != 0.9 {
		t.Errorf("candidate[0] = %+v", candidates[0])
	}
	if candidates[1].Scope != ScopeWorkspace || candidates[1].Kind != KindProject {
		t.Errorf("candidate[1] = %+v", candidates[1])
	}
}

func TestParseExtractionEmptyList(t *testing.T) {
	candidates, err := ParseExtraction(`{"memories":[]}`)
	if err != nil {
		t.Fatalf("ParseExtraction(empty list): %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %+v, want none", candidates)
	}
}

func TestParseExtractionToleratesFencesAndProse(t *testing.T) {
	cases := map[string]string{
		"fenced":             "```json\n{\"memories\":[{\"content\":\"Uses pnpm for JS work.\"}]}\n```",
		"fenced no language": "```\n{\"memories\":[{\"content\":\"Uses pnpm for JS work.\"}]}\n```",
		"prose then json":    "Here are the memories:\n{\"memories\":[{\"content\":\"Uses pnpm for JS work.\"}]}\nDone.",
		"bare array":         `[{"content":"Uses pnpm for JS work."}]`,
	}
	for name, raw := range cases {
		candidates, err := ParseExtraction(raw)
		if err != nil {
			t.Errorf("%s: ParseExtraction: %v", name, err)
			continue
		}
		if len(candidates) != 1 || candidates[0].Content != "Uses pnpm for JS work." {
			t.Errorf("%s: candidates = %+v", name, candidates)
		}
	}
}

func TestParseExtractionDropsInvalidEntries(t *testing.T) {
	raw := `{"memories":[
		{"scope":"global","kind":"user","content":"Valid entry one.","confidence":0.8},
		{"scope":"personal","content":"bad scope"},
		{"content":""},
		{"content":"nul\u0000byte"},
		{"scope":"workspace","kind":"user","content":"` + strings.Repeat("x", MaxContentBytes+1) + `"},
		{"scope":"workspace","kind":"project","content":"Valid entry two.","confidence":2.5}
	]}`
	candidates, err := ParseExtraction(raw)
	if err != nil {
		t.Fatalf("ParseExtraction(invalid entries): %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v, want the 2 valid entries", candidates)
	}
	if candidates[0].Content != "Valid entry one." || candidates[1].Content != "Valid entry two." {
		t.Errorf("candidates = %+v", candidates)
	}
	if candidates[1].Confidence != 1 {
		t.Errorf("clamped confidence = %v, want 1", candidates[1].Confidence)
	}
}

func TestParseExtractionCapsAtMaxCandidates(t *testing.T) {
	raw := `{"memories":[
		{"content":"entry number one about the release process"},
		{"content":"entry number two about the release process"},
		{"content":"entry number three about the release process"},
		{"content":"entry number four about the release process"},
		{"content":"entry number five about the release process"}
	]}`
	candidates, err := ParseExtraction(raw)
	if err != nil {
		t.Fatalf("ParseExtraction: %v", err)
	}
	if len(candidates) != MaxCandidates {
		t.Fatalf("candidates = %d, want %d", len(candidates), MaxCandidates)
	}
}

func TestParseExtractionDeduplicates(t *testing.T) {
	raw := `{"memories":[
		{"content":"The release branch is release/2026."},
		{"content":"  The release branch is release/2026.  "},
		{"content":"Something else entirely."}
	]}`
	candidates, err := ParseExtraction(raw)
	if err != nil {
		t.Fatalf("ParseExtraction: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v, want the duplicate dropped", candidates)
	}
}

func TestParseExtractionErrors(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":          "",
		"whitespace":     "   \n\t ",
		"prose only":     "I could not find any durable memories.",
		"malformed json": `{"memories":[{"content":"x"}`,
		"wrong shape":    `{"memories":"not an array"}`,
		"no memories":    `{"items":[]}`,
	} {
		if _, err := ParseExtraction(raw); err == nil {
			t.Errorf("%s: ParseExtraction(%q) succeeded, want an error", name, raw)
		}
	}
}
