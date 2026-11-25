package skilldraft

import (
	"context"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// sectionText returns the body of one rendered section: everything between its
// heading and the next "## " heading.
func sectionText(t *testing.T, body, heading string) string {
	t.Helper()
	marker := "## " + heading + "\n"
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("body has no %q section:\n%s", heading, body)
	}
	rest := body[start+len(marker):]
	end := len(rest)
	if next := strings.Index(rest, "\n## "); next >= 0 {
		end = next
	}
	return strings.TrimSpace(rest[:end])
}

// TestBodyHasTheSixSectionsInOrder is the heart of G12: a draft is a 六段式
// Markdown, always the same six headings in the same order, because a human edits
// it and tests (and the UI) rely on those exact strings.
func TestBodyHasTheSixSectionsInOrder(t *testing.T) {
	want := []string{"When to use", "Inputs", "Steps", "Checks", "Failure modes", "Example"}
	if len(SectionHeadings) != len(want) {
		t.Fatalf("SectionHeadings = %v, want %v", SectionHeadings, want)
	}
	for index, heading := range want {
		if SectionHeadings[index] != heading {
			t.Fatalf("SectionHeadings[%d] = %q, want %q", index, SectionHeadings[index], heading)
		}
	}

	service := openStore(t, "", newClock())
	created, err := service.Draft(context.Background(), contracts.TaskSkillDraft{
		TaskID:  "task-42",
		Name:    "release-notes",
		Summary: "Use when a release needs notes.",
		Body:    "Run scripts/release.sh.\nThen read the changelog.",
	})
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}

	body := created.Body
	if !strings.HasPrefix(body, "# release-notes\n") {
		t.Fatalf("body does not start with the draft name as its title:\n%s", body)
	}
	last := -1
	for _, heading := range want {
		marker := "\n## " + heading + "\n"
		index := strings.Index(body, marker)
		if index < 0 {
			t.Fatalf("body is missing the %q heading:\n%s", heading, body)
		}
		if index < last {
			t.Fatalf("heading %q is out of order:\n%s", heading, body)
		}
		last = index
	}
	if count := strings.Count(body, "\n## "); count != len(want) {
		t.Fatalf("body has %d level-2 headings, want exactly %d:\n%s", count, len(want), body)
	}

	if got := sectionText(t, body, "When to use"); got != "Use when a release needs notes." {
		t.Fatalf("When to use = %q, want the summary", got)
	}
	if got := sectionText(t, body, "Steps"); !strings.Contains(got, "Run scripts/release.sh.") ||
		!strings.Contains(got, "Then read the changelog.") {
		t.Fatalf("Steps = %q, want the caller's body", got)
	}
	// Sections the proposer did not write are honestly empty, not invented.
	for _, heading := range []string{"Inputs", "Checks", "Failure modes", "Example"} {
		if got := sectionText(t, body, heading); got != SectionPlaceholder {
			t.Fatalf("%s = %q, want the placeholder %q", heading, got, SectionPlaceholder)
		}
	}
}

// TestBodyKeepsAStructuredProposal: a proposer (or a human) that already wrote
// some of the six sections keeps them, in the right slots.
func TestBodyKeepsAStructuredProposal(t *testing.T) {
	service := openStore(t, "", newClock())
	created, err := service.Draft(context.Background(), contracts.TaskSkillDraft{
		Name:    "migrate-db",
		Summary: "Use when a schema migration must ship.",
		Body: "Intro paragraph from the task.\n\n" +
			"## Inputs\n\n- the branch name\n\n" +
			"## Steps\n\n1. run the migration\n\n" +
			"## Checks\n\n- go test ./...\n\n" +
			"## Example\n\n```\nmake migrate\n```\n",
	})
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	body := created.Body
	if got := sectionText(t, body, "Inputs"); got != "- the branch name" {
		t.Fatalf("Inputs = %q", got)
	}
	if got := sectionText(t, body, "Checks"); got != "- go test ./..." {
		t.Fatalf("Checks = %q", got)
	}
	if got := sectionText(t, body, "Example"); !strings.Contains(got, "make migrate") {
		t.Fatalf("Example = %q", got)
	}
	if got := sectionText(t, body, "Steps"); !strings.Contains(got, "Intro paragraph from the task.") ||
		!strings.Contains(got, "1. run the migration") {
		t.Fatalf("Steps = %q, want the preamble carried in front of the steps", got)
	}
	if got := sectionText(t, body, "Failure modes"); got != SectionPlaceholder {
		t.Fatalf("Failure modes = %q, want the placeholder", got)
	}
	if strings.Count(body, "## Steps") != 1 {
		t.Fatalf("body has more than one Steps heading:\n%s", body)
	}
}

// TestBodyIgnoresTheProposerTitleAndUnknownHeadings: a "# title" line is replaced
// by the draft's own title, and an unknown "## Notes" block is content, never a
// seventh section.
func TestBodyIgnoresTheProposerTitleAndUnknownHeadings(t *testing.T) {
	service := openStore(t, "", newClock())
	created, err := service.Draft(context.Background(), contracts.TaskSkillDraft{
		Name: "freeform",
		Body: "# my own title\n\nkeep this note\nmore notes\n",
	})
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	body := created.Body
	if strings.Contains(body, "my own title") {
		t.Fatalf("body kept the proposer's title:\n%s", body)
	}
	if strings.Count(body, "\n## ") != len(SectionHeadings) {
		t.Fatalf("body has %d sections, want %d:\n%s", strings.Count(body, "\n## "), len(SectionHeadings), body)
	}
	if got := sectionText(t, body, "Steps"); !strings.Contains(got, "keep this note") ||
		!strings.Contains(got, "more notes") {
		t.Fatalf("Steps = %q, want the freeform text", got)
	}

	unknown, err := service.Draft(context.Background(), contracts.TaskSkillDraft{
		Name:    "with-notes",
		Summary: "Use when notes exist.",
		Body:    "## Notes\n\nkeep me\n\n## Steps\n\nrun it\n",
	})
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if got := sectionText(t, unknown.Body, "Steps"); !strings.Contains(got, "### Notes") ||
		!strings.Contains(got, "keep me") || !strings.Contains(got, "run it") {
		t.Fatalf("Steps = %q, want the unknown heading carried as demoted content", got)
	}
	if strings.Count(unknown.Body, "\n## ") != len(SectionHeadings) {
		t.Fatalf("unknown heading became a section:\n%s", unknown.Body)
	}
}

// TestBodyFallsBackToTheStructuredWhenToUse: an empty summary is not a reason to
// lose a "When to use" section the proposer wrote.
func TestBodyFallsBackToTheStructuredWhenToUse(t *testing.T) {
	service := openStore(t, "", newClock())
	created, err := service.Draft(context.Background(), contracts.TaskSkillDraft{
		Name: "no-summary",
		Body: "## When to use\n\nwhen the build is red\n\n## Steps\n\nread the log\n",
	})
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if got := sectionText(t, created.Body, "When to use"); got != "when the build is red" {
		t.Fatalf("When to use = %q, want the section the proposer wrote", got)
	}
	if got := sectionText(t, created.Body, "Steps"); got != "read the log" {
		t.Fatalf("Steps = %q", got)
	}
}

// TestSectionIndexIgnoresCaseAndSpacing pins the forgiving heading match: a human
// typing "## when  to use" still edits the right section.
func TestSectionIndexIgnoresCaseAndSpacing(t *testing.T) {
	for _, heading := range []string{"When to use", "when to use", "  WHEN  TO   USE  ", "When to use ##"} {
		if index := sectionIndex(heading); index != sectionWhenToUse {
			t.Fatalf("sectionIndex(%q) = %d, want %d", heading, index, sectionWhenToUse)
		}
	}
	// The whole ATX closing sequence is accepted too: "## When to use ##".
	if heading, ok := cutMarkdownHeading("## When to use ##", 2); !ok || sectionIndex(heading) != sectionWhenToUse {
		t.Fatalf("cutMarkdownHeading(%q) = (%q, %v), want the When to use section", "## When to use ##", heading, ok)
	}
	if index := sectionIndex("Notes"); index != -1 {
		t.Fatalf("sectionIndex(Notes) = %d, want -1", index)
	}
	if level, ok := cutMarkdownHeading("### deep", 2); ok {
		t.Fatalf("a level-3 heading was read as a level-2 section (%q)", level)
	}
}
