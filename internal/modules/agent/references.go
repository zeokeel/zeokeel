package agent

// @session reference expansion: the composer writes @session:<id> tokens; Send
// expands the referenced transcripts into the model input for that one run,
// while the read model and durable history keep the user's original prompt.

import (
	"context"
	"fmt"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// maxReferenceMessages bounds how many recent messages one referenced
	// session contributes.
	maxReferenceMessages = 20
	// maxReferenceContextRunes bounds the injected context block (not the
	// original prompt). Exceeding it truncates the block with an ellipsis.
	maxReferenceContextRunes = 8000
	// referenceTitleFallback is used when the referenced session has no title.
	referenceTitleFallback = "未命名会话"
)

// expandSessionReferences returns the model input prompt with referenced
// session transcripts prepended, plus the IDs that were expanded. Unknown,
// empty and self references are skipped; duplicate tokens are already collapsed
// by contracts.ParseSessionReferences. A prompt without valid references is
// returned unchanged.
func (s *service) expandSessionReferences(sessionID, prompt string) (string, []string) {
	refs := contracts.ParseSessionReferences(prompt)
	if len(refs) == 0 || strings.TrimSpace(prompt) == "" {
		return prompt, nil
	}
	var (
		blocks []string
		ids    []string
		used   int
	)
	for _, ref := range refs {
		if ref == "" || ref == sessionID {
			continue
		}
		referenced, ok := s.Session(ref)
		if !ok {
			s.logger.Printf("[agent] session reference %q: unknown session", ref)
			continue
		}
		block := buildReferenceBlock(referenced)
		if block == "" {
			continue
		}
		remaining := maxReferenceContextRunes - used
		if remaining <= 0 {
			break
		}
		runes := []rune(block)
		if len(runes) > remaining {
			// Keep the block within the global budget, mark the cut and retain
			// the closing footer so the model can see the block boundary.
			const footerText = "[引用结束]\n\n"
			footer := []rune(footerText)
			if remaining > len(footer)+1 {
				block = string(runes[:remaining-len(footer)-1]) + "…" + footerText
			} else {
				block = string(runes[:remaining])
			}
		}
		blocks = append(blocks, block)
		ids = append(ids, ref)
		used += len([]rune(block))
		if used >= maxReferenceContextRunes {
			break
		}
	}
	if len(blocks) == 0 {
		return prompt, nil
	}
	return strings.Join(blocks, "") + prompt, ids
}

// buildReferenceBlock renders one referenced session's recent transcript as a
// bounded context block. Only user/assistant text is included; tool rows have no
// standalone text in the read model.
func buildReferenceBlock(session contracts.ChatSession) string {
	title := strings.TrimSpace(session.Title)
	if title == "" {
		title = referenceTitleFallback
	}
	messages := session.Messages
	if len(messages) > maxReferenceMessages {
		messages = messages[len(messages)-maxReferenceMessages:]
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "[引用的会话 %q (%s)]\n", title, session.ID)
	for _, message := range messages {
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}
		switch message.Role {
		case contracts.ChatRoleUser, contracts.ChatRoleAssistant:
			fmt.Fprintf(&builder, "<%s: %s>\n", message.Role, text)
		}
	}
	builder.WriteString("[引用结束]\n\n")
	return builder.String()
}

// rewritingHistory adapts a rig.History for one run that injects reference
// context: the model sees the expanded input, but the expanded user message is
// replaced with the original prompt before it reaches durable persistence.
type rewritingHistory struct {
	base         rig.History
	sessionID    string
	expanded     rig.Message
	original     rig.Message
	needsRewrite bool
}

// newRewritingHistory wraps base so the first matching expanded user append is
// persisted as original.
func newRewritingHistory(base rig.History, sessionID string, expanded, original rig.Message) rig.History {
	return &rewritingHistory{
		base:         base,
		sessionID:    sessionID,
		expanded:     expanded,
		original:     original,
		needsRewrite: true,
	}
}

// Load implements rig.History.
func (h *rewritingHistory) Load(ctx context.Context, sessionID string) ([]rig.Message, error) {
	return h.base.Load(ctx, sessionID)
}

// Append implements rig.History: it rewrites the injected user message once.
func (h *rewritingHistory) Append(ctx context.Context, sessionID string, messages ...rig.Message) error {
	if !h.needsRewrite || sessionID != h.sessionID {
		return h.base.Append(ctx, sessionID, messages...)
	}
	copied := make([]rig.Message, len(messages))
	copy(copied, messages)
	for index := range copied {
		if copied[index].Role == rig.RoleUser && copied[index].Text() == h.expanded.Text() {
			copied[index] = h.original
			h.needsRewrite = false
			break
		}
	}
	return h.base.Append(ctx, sessionID, copied...)
}
