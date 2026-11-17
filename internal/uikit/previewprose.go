package uikit

// Prose previews for the surfaces that show a slice of an answer rather than a
// tool command: the trajectory body preview, a teammate/subagent completion
// receipt, and a sidebar search-hit snippet. The backend hands these surfaces
// raw Markdown, so a one-line preview must strip the structure before it
// collapses the whitespace — otherwise the line reads as "## … ```bash … - …"
// noise (2026-11 UI review P2).
//
// PreviewProse is the prose sibling of ToolPreview. ToolPreview keeps a tool
// command verbatim (the raw text IS the information); PreviewProse drops
// Markdown structure. Never swap one for the other: a command preview must not
// lose its flags/markers, and a prose preview must not keep its heading/list/
// fence markers. The fence handling mirrors the agent's collapseTeamText
// (internal/modules/agent) so the two read-model summaries stay consistent.

import "strings"

// PreviewProse flattens Markdown-bearing prose into one clean single-line
// preview: it drops fenced code blocks (the fence lines, their language tag and
// their content), leading heading/quote/list markers (# > - * +) and inline
// code/emphasis markers (` * _), collapses runs of whitespace to one space, and
// cuts the result to max runes with a trailing ellipsis. max <= 0 leaves the
// length unbounded. Empty or all-structure input previews as "".
func PreviewProse(text string, max int) string {
	var kept []string
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		// A fence line toggles the block; everything inside (and the language
		// tag on the opening fence) is dropped, not flattened into the preview.
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		trimmed = strings.TrimLeft(trimmed, "#>-*+ \t")
		trimmed = strings.ReplaceAll(trimmed, "`", "")
		// Doubles before singles so **bold** / __bold__ lose both markers.
		trimmed = strings.ReplaceAll(trimmed, "**", "")
		trimmed = strings.ReplaceAll(trimmed, "__", "")
		trimmed = strings.ReplaceAll(trimmed, "*", "")
		trimmed = strings.ReplaceAll(trimmed, "_", "")
		if trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	collapsed := strings.Join(strings.Fields(strings.Join(kept, " ")), " ")
	runes := []rune(collapsed)
	if max <= 0 || len(runes) <= max {
		return collapsed
	}
	return string(runes[:max]) + "…"
}
