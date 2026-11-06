package memory

import (
	"strings"

	"github.com/zeokeel/zeokeel/old/internal/store"
)

// Extraction prompt bounds. Recent conversation is clipped newest-first to
// extractionRecentBudgetBytes; each message and each existing memory is clipped
// so one huge row cannot crowd out the rest of the prompt.
const (
	extractionRecentBudgetBytes = 30 * 1024
	extractionExistingBudget    = 8 * 1024
	extractionMaxMessageBytes   = 4 * 1024
	extractionMaxExistingItem   = 500
)

// extractionSystemPrompt is the fixed extractor instruction. The rules match
// docs/design/auto-memory.md §5: at most 3 durable candidates, scope/kind
// taxonomy, no secrets/derivable/one-off content, no duplicates of memories the
// prompt lists.
const extractionSystemPrompt = `You extract durable memories from a conversation for an AI coding assistant.

Return exactly one JSON object, with no prose and no markdown fences:
{"memories":[{"scope":"global|workspace","kind":"user|feedback|project|reference","content":"...","confidence":0.0}]}

Rules:
- At most 3 memories. Fewer is better; {"memories":[]} is a valid answer.
- scope: "global" for the user's durable personal preferences that apply across projects; "workspace" for conventions, decisions, constraints and facts about this project.
- kind: "user" (identity or preferences), "feedback" (corrections about how the assistant should work), "project" (decisions, conventions, constraints), "reference" (durable pointers to external information).
- content: one self-contained sentence, at most 8192 bytes. Never store secrets, credentials, API keys, tokens or passwords; never store one-off or transient details; never store facts that are derivable from the code or the conversation.
- Do not duplicate an existing memory listed below. If new information refines one, write the improved replacement instead.
- confidence: a number between 0.0 and 1.0.`

// ExtractionPrompt builds the (system, user) pair for the background memory
// extractor (docs/design/auto-memory.md §5 path B).
//
// system states the JSON output schema and the durable-memory rules (at most 3
// candidates, scope/kind taxonomy, no secrets/derivable/one-off content, no
// duplicates). user lists the existing global/workspace memories to avoid
// duplicating and the recent conversation, bounded to
// extractionRecentBudgetBytes.
func ExtractionPrompt(global, workspace []store.Memory, recent []Message) (system, user string) {
	var builder strings.Builder
	builder.WriteString("Existing global memories:\n")
	writeExistingMemories(&builder, global)
	builder.WriteString("\nExisting workspace memories:\n")
	writeExistingMemories(&builder, workspace)
	builder.WriteString("\nRecent conversation:\n")
	writeRecentConversation(&builder, recent)
	return extractionSystemPrompt, strings.TrimSpace(builder.String())
}

// writeExistingMemories writes one scope's memories, bounded so a large history
// cannot crowd the recent conversation out of the prompt.
func writeExistingMemories(builder *strings.Builder, memories []store.Memory) {
	written := 0
	budget := extractionExistingBudget
	for _, memory := range memories {
		content := strings.TrimSpace(memory.Content)
		if content == "" {
			continue
		}
		content = clipText(content, extractionMaxExistingItem)
		line := "- " + content + "\n"
		if len(line) > budget {
			builder.WriteString("- ...more memories omitted\n")
			return
		}
		builder.WriteString(line)
		budget -= len(line)
		written++
	}
	if written == 0 {
		builder.WriteString("- (none)\n")
	}
}

// writeRecentConversation writes the newest conversation turns in order (oldest
// kept turn first) within extractionRecentBudgetBytes. The newest message is
// always kept, because extraction runs on the just-finished turn.
func writeRecentConversation(builder *strings.Builder, recent []Message) {
	lines := make([]string, 0, len(recent))
	budget := extractionRecentBudgetBytes
	wrote := false
	for index := len(recent) - 1; index >= 0; index-- {
		message := recent[index]
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		role := strings.TrimSpace(message.Role)
		if role == "" {
			role = "user"
		}
		content = clipText(content, extractionMaxMessageBytes)
		line := role + ": " + content + "\n"
		if len(line) > budget {
			if wrote {
				builder.WriteString("(older conversation omitted)\n")
			} else {
				// The newest message alone exceeds the budget: clip it so the
				// extractor still sees the tail of the turn.
				builder.WriteString(clipText(strings.TrimSuffix(line, "\n"), budget) + "\n")
				wrote = true
			}
			break
		}
		lines = append(lines, line)
		budget -= len(line)
		wrote = true
	}
	for index := len(lines) - 1; index >= 0; index-- {
		builder.WriteString(lines[index])
	}
	if !wrote {
		builder.WriteString("(none)\n")
	}
}

// clipText bounds text to limit bytes, marking a cut with a stable suffix and
// keeping the result valid UTF-8.
func clipText(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	suffix := " ...[truncated]"
	if limit <= len(suffix) {
		return text[:limit]
	}
	cut := limit - len(suffix)
	for cut > 0 && (text[cut]&0xC0) == 0x80 {
		cut--
	}
	return text[:cut] + suffix
}
