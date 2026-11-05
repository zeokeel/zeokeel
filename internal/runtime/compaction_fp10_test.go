package runtime

import (
	"strings"
	"testing"
)

// TestEstimateTokensCJKAware pins FP-10: a CJK rune is charged about one token
// while ASCII keeps the chars/4 heuristic, so Chinese/Japanese prompts are no
// longer under-counted by 4x on their CJK portion.
func TestEstimateTokensCJKAware(t *testing.T) {
	ascii := []Message{{Role: RoleUser, Content: strings.Repeat("a", 400)}}
	if got := estimateTokens(ascii); got != 100 {
		t.Fatalf("ASCII 400 chars estimated %d tokens, want 100", got)
	}
	cjk := []Message{{Role: RoleUser, Content: strings.Repeat("中", 100)}}
	if got := estimateTokens(cjk); got != 100 {
		t.Fatalf("CJK 100 chars estimated %d tokens, want 100", got)
	}
	// The old chars/4 heuristic counted the 100 CJK runes as 300 bytes/4 =
	// 75 tokens; the rune-aware estimate must be strictly larger.
	if got, old := estimateTokens(cjk), len(strings.Repeat("中", 100))/4; got <= old {
		t.Fatalf("CJK estimate %d not greater than chars/4 estimate %d", got, old)
	}
	mixed := []Message{{Role: RoleUser, Content: "中a中a"}}
	if got := estimateTokens(mixed); got != 3 {
		t.Fatalf("mixed 2 CJK + 2 ASCII estimated %d tokens, want 3", got)
	}
	if got := estimateTokens(nil); got != 0 {
		t.Fatalf("nil messages estimated %d tokens, want 0", got)
	}
}

// TestEstimateTokensChargesImages pins that image parts are counted by a flat
// per-image amount instead of by their base64 data-URL length (which would
// massively overestimate) or not at all (which would underestimate).
func TestEstimateTokensChargesImages(t *testing.T) {
	textOnly := estimateTokens([]Message{{Role: RoleUser, Content: "hello"}})
	withImage := estimateTokens([]Message{{
		Role:    RoleUser,
		Content: "hello",
		Images:  []ImagePart{{URL: "data:image/png;base64," + strings.Repeat("A", 100000)}},
	}})
	if diff := withImage - textOnly; diff != estimatedImageTokens {
		t.Fatalf("image added %d tokens, want %d", diff, estimatedImageTokens)
	}
	if withImage >= 100000/4 {
		t.Fatalf("image estimate %d counted the data URL length", withImage)
	}
}

// TestWindowCompactorOffloadsOldImages pins FP-10's image offload: only the
// newest image-bearing messages keep their parts; older ones become a text
// placeholder, and the input conversation is not mutated.
func TestWindowCompactorOffloadsOldImages(t *testing.T) {
	image := func(text string) Message {
		return Message{
			Role:    RoleUser,
			Content: text,
			Images:  []ImagePart{{URL: "data:image/png;base64,AAAA"}},
		}
	}
	messages := []Message{
		image("one"),
		{Role: RoleAssistant, Content: "reply"},
		image("two"),
		{Role: RoleUser, Content: "plain"},
		image("three"),
		image("four"),
	}

	out, err := WindowCompactor{KeepLast: 10}.Compact(nil, "sess", messages, 100000)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(out) != len(messages) {
		t.Fatalf("compacted %d messages, want %d (nothing should be dropped)", len(out), len(messages))
	}
	// The newest two image-bearing messages (indexes 4 and 5) stay intact.
	for _, i := range []int{4, 5} {
		if len(out[i].Images) != 1 {
			t.Fatalf("recent message %d lost its image: %+v", i, out[i].Images)
		}
	}
	// Older images are offloaded to the placeholder.
	for _, i := range []int{0, 2} {
		if len(out[i].Images) != 0 {
			t.Fatalf("old message %d kept %d image parts", i, len(out[i].Images))
		}
		if !strings.Contains(out[i].Content, ImageOffloadPlaceholder) {
			t.Fatalf("old message %d content = %q, want the offload placeholder", i, out[i].Content)
		}
	}
	// The caller's slice must be untouched.
	if len(messages[0].Images) != 1 || len(messages[2].Images) != 1 {
		t.Fatalf("input messages were mutated: %+v / %+v", messages[0].Images, messages[2].Images)
	}
}

// TestOffloadOldImagesNoopWithoutImages pins that a text-only conversation is
// returned byte-identical (no needless copies or placeholders).
func TestOffloadOldImagesNoopWithoutImages(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: "a"},
		{Role: RoleAssistant, Content: "b"},
	}
	out := offloadOldImages(messages, 2)
	if len(out) != len(messages) {
		t.Fatalf("offload changed the message count: %d -> %d", len(messages), len(out))
	}
	for i := range out {
		if out[i].Content != messages[i].Content || len(out[i].Images) != 0 {
			t.Fatalf("message %d changed: %+v", i, out[i])
		}
	}
}
