package memory

import (
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/store"
)

func TestExtractionPromptContainsSchemaAndRules(t *testing.T) {
	system, user := ExtractionPrompt(nil, nil, nil)
	lowerSystem := strings.ToLower(system)
	for _, want := range []string{
		`"memories"`, `"scope"`, `"kind"`, `"content"`, `"confidence"`,
		"at most 3", "secret", "derivable", "duplicate", "JSON",
		ScopeGlobal, ScopeWorkspace, KindUser, KindFeedback, KindProject, KindReference,
	} {
		if !strings.Contains(lowerSystem, strings.ToLower(want)) {
			t.Errorf("system prompt does not contain %q:\n%s", want, system)
		}
	}
	if strings.TrimSpace(user) == "" {
		t.Fatal("user prompt is empty")
	}
	if !strings.Contains(user, "Recent conversation") {
		t.Errorf("user prompt does not label the conversation:\n%s", user)
	}
}

func TestExtractionPromptIncludesExistingMemories(t *testing.T) {
	global := []store.Memory{
		{ID: "g1", Scope: ScopeGlobal, Content: "Prefers small focused commits.", UpdatedAt: time.Unix(100, 0)},
	}
	workspace := []store.Memory{
		{ID: "w1", Scope: ScopeWorkspace, WorkspaceID: "ws-1", Content: "Release branch is release/2026.", UpdatedAt: time.Unix(100, 0)},
	}
	_, user := ExtractionPrompt(global, workspace, nil)
	for _, want := range []string{
		"Existing global memories", "Prefers small focused commits.",
		"Existing workspace memories", "Release branch is release/2026.",
	} {
		if !strings.Contains(user, want) {
			t.Errorf("user prompt does not contain %q:\n%s", want, user)
		}
	}
}

func TestExtractionPromptIncludesRecentConversation(t *testing.T) {
	recent := []Message{
		{Role: "user", Content: "We decided to tag releases on main."},
		{Role: "assistant", Content: "Noted."},
	}
	_, user := ExtractionPrompt(nil, nil, recent)
	for _, want := range []string{"user:", "We decided to tag releases on main.", "assistant:", "Noted."} {
		if !strings.Contains(user, want) {
			t.Errorf("user prompt does not contain %q:\n%s", want, user)
		}
	}
}

func TestExtractionPromptBoundsRecentConversation(t *testing.T) {
	recent := make([]Message, 0, 100)
	for i := 0; i < 100; i++ {
		// Each message is ~1 KiB; the 30 KiB budget cannot hold all of them.
		recent = append(recent, Message{
			Role:    "user",
			Content: strings.Repeat("x", 1000) + "-marker-oldest-" + itoa(i),
		})
	}
	_, user := ExtractionPrompt(nil, nil, recent)
	if len(user) > extractionRecentBudgetBytes+extractionExistingBudget+4096 {
		t.Fatalf("user prompt is %d bytes, want it bounded", len(user))
	}
	// Newest messages are kept, the oldest are dropped.
	if !strings.Contains(user, "marker-oldest-99") {
		t.Errorf("user prompt dropped the newest message")
	}
	if strings.Contains(user, "marker-oldest-0") {
		t.Errorf("user prompt kept the oldest message despite the budget")
	}
}

func TestExtractionPromptClipLongSingleMessage(t *testing.T) {
	recent := []Message{{Role: "user", Content: strings.Repeat("y", extractionMaxMessageBytes*3)}}
	_, user := ExtractionPrompt(nil, nil, recent)
	if !strings.Contains(user, "truncated") {
		t.Errorf("long message was not marked as truncated:\n%.200s", user)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 3)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
