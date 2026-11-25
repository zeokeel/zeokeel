package skilldraft

// The six-section body. This is the "六段式草稿" from plan G12: every draft a
// human is asked to accept has the same six headings in the same order, so the
// completion card, the drafts list and a person editing the Markdown all agree on
// what a skill draft looks like. The strings below are the contract — change
// them and every stored draft reads differently.

import (
	"fmt"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// SectionHeadings is the fixed order of the draft body's sections. Tests and
// humans rely on these exact strings (including the spaces and the capital
// "When"), so they are spelled once here. Treat it as read-only.
var SectionHeadings = []string{
	"When to use",
	"Inputs",
	"Steps",
	"Checks",
	"Failure modes",
	"Example",
}

// Section indexes, named so the renderer does not use magic numbers.
const (
	sectionWhenToUse = iota
	sectionInputs
	sectionSteps
	sectionChecks
	sectionFailureModes
	sectionExample
)

// SectionPlaceholder stands in for a section the caller left out. It is
// deliberately not empty: an empty heading looks like a rendering bug, while a
// visible "fill this in" is an honest instruction to the human editing the
// draft. Nothing invents content — a draft never claims a check it never ran.
const SectionPlaceholder = "_(to be filled in)_"

// renderBody builds the six-section Markdown from a proposal.
//
// The caller's text is the raw material, and it may already be partly
// structured: if it carries any of the canonical `## <heading>` sections they are
// kept as written, the text before the first of them is carried into Steps
// (alone when there is no Steps section, in front of it when there is), and the
// summary becomes "When to use" unless the body spells that section out itself.
// Missing sections get SectionPlaceholder.
//
// A leading `# title` line is dropped: the rendered body has its own title, from
// the draft's name, and keeping two would leave the human renaming one of them.
func renderBody(draft contracts.TaskSkillDraft) string {
	preamble, sections := splitDraftBody(draft.Body)
	whenToUse := strings.TrimSpace(draft.Summary)
	if whenToUse == "" {
		whenToUse = strings.TrimSpace(sections[sectionWhenToUse])
	}
	// The preamble (whatever the proposer wrote before its first canonical
	// heading) is the raw material for Steps. When the proposer also wrote a
	// Steps section, the two are concatenated instead of one winning: dropping
	// either would silently lose text a human is about to accept.
	steps := strings.TrimSpace(sections[sectionSteps])
	if lead := strings.TrimSpace(strings.Join(preamble, "\n")); lead != "" {
		if steps == "" {
			steps = lead
		} else {
			steps = lead + "\n\n" + steps
		}
	}
	// Indexed by section index, in the same order as SectionHeadings; every
	// slot is filled from either the caller or the summary, so the loop below
	// never reads past the array.
	contents := make([]string, len(SectionHeadings))
	contents[sectionWhenToUse] = whenToUse
	contents[sectionInputs] = sections[sectionInputs]
	contents[sectionSteps] = steps
	contents[sectionChecks] = sections[sectionChecks]
	contents[sectionFailureModes] = sections[sectionFailureModes]
	contents[sectionExample] = sections[sectionExample]

	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s\n", draft.Name)
	for index, heading := range SectionHeadings {
		content := strings.TrimSpace(contents[index])
		if content == "" {
			content = SectionPlaceholder
		}
		fmt.Fprintf(&builder, "\n## %s\n\n%s\n", heading, content)
	}
	return builder.String()
}

// splitDraftBody cuts raw proposal text at the canonical `## <heading>` lines.
// It returns the lines before the first canonical heading and one body per
// canonical section. Unknown headings are ordinary content: they stay in the
// section they appear in, verbatim, so nothing a proposer wrote is lost.
func splitDraftBody(raw string) ([]string, []string) {
	var preamble []string
	parts := make([][]string, len(SectionHeadings))
	current := -1
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if heading, ok := cutMarkdownHeading(line, 2); ok {
			if index := sectionIndex(heading); index >= 0 {
				current = index
				continue
			}
			// A level-2 heading that is not one of the six is the proposer's own
			// structure, not a seventh section. Demoting it to level 3 keeps
			// its text while leaving the six headings as the only level-2
			// headings in a draft body — so a reader (and a test) can find the
			// sections by level alone.
			line = "### " + heading
		}
		if current >= 0 {
			parts[current] = append(parts[current], line)
		} else if _, ok := cutMarkdownHeading(line, 1); ok {
			// The proposal's own title: the rendered body supplies one.
			continue
		} else {
			preamble = append(preamble, line)
		}
	}
	sections := make([]string, len(SectionHeadings))
	for index := range sections {
		sections[index] = strings.TrimSpace(strings.Join(parts[index], "\n"))
	}
	return preamble, sections
}

// cutMarkdownHeading returns the text of an ATX heading of exactly level hashes
// ("# " for 1, "## " for 2). A deeper heading (`### ...`) is not a section of the
// draft, so it stays content.
func cutMarkdownHeading(line string, level int) (string, bool) {
	trimmed := strings.TrimSpace(line)
	prefix := strings.Repeat("#", level) + " "
	if !strings.HasPrefix(trimmed, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(trimmed, prefix)
	if strings.HasPrefix(rest, "#") {
		return "", false // "### x" when level is 2
	}
	return strings.TrimSpace(strings.TrimRight(rest, "#")), true
}

// sectionIndex maps a heading to its canonical section, ignoring case, repeated
// whitespace and a closing "##" so "## when  to use" still lands in the right
// slot.
func sectionIndex(heading string) int {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimRight(heading, "#")), " "))
	for index, name := range SectionHeadings {
		if strings.ToLower(name) == normalized {
			return index
		}
	}
	return -1
}
