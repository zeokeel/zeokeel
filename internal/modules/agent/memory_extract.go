package agent

// Model-driven memory extraction (opt-in): the session Memory tab asks the
// agent to distill recent user/assistant turns into a few durable global
// memories. The feature is disabled unless memory.extract is exactly "true";
// extraction is single-flight, tolerates a fenced JSON answer and skips
// memories that already exist (normalized text comparison).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// memoryExtractDefaultMax is the default/cap for one extraction call.
	memoryExtractDefaultMax = 5
	// memoryExtractMessageLimit bounds the recent messages considered.
	memoryExtractMessageLimit = 20
	// memoryExtractMessageRunes bounds one message in the prompt.
	memoryExtractMessageRunes = 2000
)

var _ contracts.MemoryExtractionService = (*service)(nil)

// ErrMemoryExtractBusy reports a concurrent extraction attempt.
var ErrMemoryExtractBusy = errors.New("agent: memory extraction is already running")

// ExtractFromSession implements contracts.MemoryExtractionService.
func (s *service) ExtractFromSession(ctx context.Context, sessionID string, max int) ([]contracts.Memory, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !s.memoryExtractEnabled() {
		return nil, errors.New("agent: memory extraction is disabled (memory.extract must be \"true\")")
	}
	if s.memoryStore == nil {
		return nil, errors.New("agent: no memory store is available for extraction")
	}
	if !s.extracting.CompareAndSwap(false, true) {
		return nil, ErrMemoryExtractBusy
	}
	defer s.extracting.Store(false)
	if max <= 0 {
		max = memoryExtractDefaultMax
	}
	if max > memoryExtractDefaultMax {
		max = memoryExtractDefaultMax
	}

	s.mu.Lock()
	state, ok := s.sessions[strings.TrimSpace(sessionID)]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	messages := make([]contracts.ChatMessage, len(state.messages))
	copy(messages, state.messages)
	cfg := s.configForSessionLocked(sessionID)
	s.mu.Unlock()
	if len(messages) < 4 {
		return nil, errors.New("agent: at least four messages are required for memory extraction")
	}

	transcript := memoryExtractTranscript(messages)
	prompt := "You extract durable user memories from a conversation.\n" +
		"Return ONLY JSON: {\"memories\":[\"...\"]}. At most " + fmt.Sprint(max) + " items, each at most 200 characters.\n" +
		"Deduplicate; never include secrets, API keys or passwords.\n\nConversation:\n" + transcript
	provider, err := s.newProvider(cfg, s.sessionEffort(sessionID))
	if err != nil {
		return nil, fmt.Errorf("agent: memory extraction provider: %w", err)
	}
	stream, err := provider.Stream(ctx, rig.Request{
		Model:    cfg.Model,
		Messages: []rig.Message{rig.NewMessage(rig.RoleUser, prompt)},
	})
	if err != nil {
		return nil, fmt.Errorf("agent: memory extraction request: %w", err)
	}
	var output strings.Builder
	for {
		chunk, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			return nil, fmt.Errorf("agent: memory extraction stream: %w", recvErr)
		}
		output.WriteString(chunk.Delta)
	}
	candidates, err := parseExtractedMemories(output.String())
	if err != nil {
		return nil, err
	}
	existing, err := s.memoryStore.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("agent: list memories: %w", err)
	}
	seen := make(map[string]bool, len(existing)+len(candidates))
	for _, memory := range existing {
		seen[normalizeMemoryText(memory.Text)] = true
	}
	added := make([]contracts.Memory, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		key := normalizeMemoryText(candidate)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		stored, addErr := s.memoryStore.Add(ctx, contracts.Memory{Text: candidate, Scope: "global"})
		if addErr != nil {
			return added, fmt.Errorf("agent: add memory: %w", addErr)
		}
		added = append(added, stored)
		if len(added) >= max {
			break
		}
	}
	return added, nil
}

// memoryExtractEnabled reports whether memory.extract is exactly "true".
func (s *service) memoryExtractEnabled() bool {
	if s.settings == nil {
		return false
	}
	return strings.TrimSpace(s.settings.Get(contracts.SettingMemoryExtractEnabled, "")) == "true"
}

// memoryExtractTranscript renders the most recent bounded messages.
func memoryExtractTranscript(messages []contracts.ChatMessage) string {
	start := len(messages) - memoryExtractMessageLimit
	if start < 0 {
		start = 0
	}
	lines := make([]string, 0, len(messages)-start)
	for _, message := range messages[start:] {
		role := strings.TrimSpace(message.Role)
		if role != contracts.ChatRoleUser && role != contracts.ChatRoleAssistant {
			continue
		}
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}
		lines = append(lines, role+": "+truncateRunes(text, memoryExtractMessageRunes))
	}
	return strings.Join(lines, "\n")
}

// parseExtractedMemories decodes the model answer, tolerating ``` fences.
func parseExtractedMemories(raw string) ([]string, error) {
	text := strings.TrimSpace(raw)
	if strings.HasPrefix(text, "```") {
		if newline := strings.IndexByte(text, '\n'); newline >= 0 {
			text = text[newline+1:]
		} else {
			text = strings.TrimPrefix(text, "```")
		}
		text = strings.TrimSpace(text)
		text = strings.TrimSuffix(text, "```")
		text = strings.TrimSpace(text)
	}
	var payload struct {
		Memories []string `json:"memories"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return nil, fmt.Errorf("agent: parse extracted memories: %w", err)
	}
	return payload.Memories, nil
}

// normalizeMemoryText folds text for duplicate detection.
func normalizeMemoryText(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(text))), " ")
}

// truncateRunes bounds s to limit runes.
func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}
