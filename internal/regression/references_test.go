package regression

// Adversarial audit: @session references. Parser negatives (empty, duplicates,
// punctuation, non-token text, self/unknown ids, oversized text) and the agent
// injection boundary: the expanded transcript reaches the model, while the read
// model and the durable history keep the user's original prompt.

import (
	"context"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/agent"
)

// TestParseSessionReferencesNegatives covers the documented parser contract and
// hostile text: empty input, prefix-only tokens, trailing punctuation, duplicate
// mentions and non-token text must not produce bogus references.
func TestParseSessionReferencesNegatives(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"empty", "", nil},
		{"plain text", "no references here", nil},
		{"prefix only", "@session:", nil},
		{"prefix with spaces", "@session:    ", nil},
		{"trailing comma", "look at @session:abc, then answer", []string{"abc"}},
		{"bracketed", "(@session:abc)", []string{"abc"}},
		{"sentence punctuation", "compare @session:abc; and @session:def.", []string{"abc", "def"}},
		{"duplicates collapse", "@session:abc @session:abc @session:abc", []string{"abc"}},
		{"first appearance order", "@session:b @session:a @session:b", []string{"b", "a"}},
		{"uppercase token is not a mention", "@SESSION:abc", nil},
		{"similar word is not a mention", "@sessions:abc", nil},
		{"CJK punctuation", "参考 @session:会话一。", []string{"会话一"}},
		{"id keeps inner punctuation", "@session:team/a.b", []string{"team/a.b"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := contracts.ParseSessionReferences(testCase.text)
			if len(got) != len(testCase.want) {
				t.Fatalf("ParseSessionReferences(%q) = %v, want %v", testCase.text, got, testCase.want)
			}
			for index := range testCase.want {
				if got[index] != testCase.want[index] {
					t.Fatalf("ParseSessionReferences(%q) = %v, want %v", testCase.text, got, testCase.want)
				}
			}
		})
	}
	// A huge token is parsed without panic or truncation; the agent later drops
	// it as unknown at lookup time.
	huge := "@session:" + strings.Repeat("x", 100_000)
	if refs := contracts.ParseSessionReferences(huge); len(refs) != 1 {
		t.Fatalf("huge token refs = %d, want 1", len(refs))
	}
	// Formatting round-trips through the parser.
	if refs := contracts.ParseSessionReferences(contracts.FormatSessionReference("  round-trip  ")); len(refs) != 1 || refs[0] != "round-trip" {
		t.Fatalf("FormatSessionReference/Parse round trip = %v", refs)
	}
}

// TestParseSessionReferencesMissedTrailingPunctuation documents a known gap: the
// trim set misses ], }, quotes and >, so references written in Markdown links or
// quoted text keep the punctuation and fail to resolve later (AUDIT-P3-6).
func TestParseSessionReferencesMissedTrailingPunctuation(t *testing.T) {
	for _, suffix := range []string{"]", "}", `"`, "'", ">", "」"} {
		text := "@session:abc" + suffix
		refs := contracts.ParseSessionReferences(text)
		if len(refs) != 1 {
			t.Fatalf("ParseSessionReferences(%q) = %v, want one ref", text, refs)
		}
		if refs[0] != "abc" {
			t.Logf("AUDIT-P3-6: trailing %q is not trimmed: ParseSessionReferences(%q) = %q", suffix, text, refs[0])
		}
	}
}

// TestParseSessionReferencesNoTokenBoundary documents a second known gap: the
// prefix is matched inside a larger token, so malformed text can yield a
// concatenated pseudo-ID (logged as AUDIT-P3-7; the agent drops it as unknown).
func TestParseSessionReferencesNoTokenBoundary(t *testing.T) {
	for _, text := range []string{
		"@session:abc,@session:def",
		"@session:a@session:b",
		"mail@session:abc",
	} {
		refs := contracts.ParseSessionReferences(text)
		if len(refs) == 0 {
			continue
		}
		if refs[0] != "abc" && refs[0] != "a" && refs[0] != "def" {
			t.Logf("AUDIT-P3-7: no token boundary: ParseSessionReferences(%q) = %v", text, refs)
		}
	}
}

// referenceRunBody returns the provider request body of one run that started
// after marker.
func referenceRunBody(t *testing.T, harness *agentHarness, marker int) []byte {
	t.Helper()
	calls := harness.bodiesAfter(marker)
	if len(calls) != 1 {
		t.Fatalf("run made %d provider calls, want exactly 1", len(calls))
	}
	return calls[0].Body
}

// TestSessionReferenceExpansionAndPersistence is the E2E injection negative:
// the referenced transcript is injected into the model input (bounded at 8000
// runes), but the read model and the durable store keep the user's original
// @session prompt; self, unknown and duplicate references never inject content.
func TestSessionReferenceExpansionAndPersistence(t *testing.T) {
	const seedAnswer = "REFERENCE-ANSWER" // the small-answer server path
	harness := startAgent(t, agent.Config{}, func(body []byte) (int, string) {
		user := decodeRequestContent(body, "user")
		if strings.Contains(user, "seed-big") {
			return 0, sseAnswer(strings.Repeat("参考内容", 5_000)) // 20k runes
		}
		return 0, sseAnswer(seedAnswer + " for: " + user)
	})
	store := requireService(t, harness.h.ctx, contracts.KeyStore)
	ctx := context.Background()

	// Session B carries a large transcript to reference.
	sessionB := harness.newChatSession(t, "被引用的会话")
	harness.sendAndWait(t, sessionB, "seed-big question")

	// Session A references B: the injected block reaches the model.
	sessionA := harness.newChatSession(t, "引用方")
	marker := len(harness.model.callsSnapshot())
	promptA := contracts.FormatSessionReference(sessionB) + " please summarize"
	harness.sendAndWait(t, sessionA, promptA)
	bodyA := referenceRunBody(t, harness, marker)
	contentA := requestContent(t, bodyA, "user")
	if !strings.HasSuffix(contentA, promptA) {
		t.Fatalf("model input does not end with the original prompt; got %d runes", len([]rune(contentA)))
	}
	block := strings.TrimSuffix(contentA, promptA)
	for _, want := range []string{"[引用的会话", "参考内容", "[引用结束]"} {
		if !strings.Contains(block, want) {
			t.Fatalf("injected block lacks %q: %q", want, block)
		}
	}
	if runes := len([]rune(block)); runes > 8000 {
		t.Fatalf("injected block = %d runes, want the 8000-rune cap", runes)
	}
	if !strings.Contains(block, "…") {
		t.Fatal("oversized injected block is not marked as truncated with an ellipsis")
	}

	// Persistence boundary: original prompt in the read model and in SQLite.
	chatSessionA, ok := harness.chat.Session(sessionA)
	if !ok {
		t.Fatalf("Session(%q) missing", sessionA)
	}
	var persistedPrompt string
	for _, message := range chatSessionA.Messages {
		if message.Role == contracts.ChatRoleUser {
			persistedPrompt = message.Text
		}
	}
	if persistedPrompt != promptA {
		t.Fatalf("read model user message = %q, want the original prompt %q", persistedPrompt, promptA)
	}
	history, err := store.History().Load(ctx, sessionA)
	if err != nil {
		t.Fatalf("History.Load(%q): %v", sessionA, err)
	}
	lastUser := ""
	for _, message := range history {
		if message.Role == "user" {
			lastUser = message.Text()
		}
	}
	if lastUser != promptA {
		t.Fatalf("durable user message = %q, want the original prompt %q (injection leaked into history)", lastUser, promptA)
	}

	// Self reference: no injection, prompt untouched.
	sessionSelf := harness.newChatSession(t, "自引用")
	selfPrompt := contracts.FormatSessionReference(sessionSelf) + " self mention"
	marker = len(harness.model.callsSnapshot())
	harness.sendAndWait(t, sessionSelf, selfPrompt)
	selfContent := requestContent(t, referenceRunBody(t, harness, marker), "user")
	if selfContent != selfPrompt {
		t.Fatalf("self reference changed the model input: %q", selfContent)
	}

	// Unknown reference: no injection, one explanatory log line.
	sessionUnknown := harness.newChatSession(t, "未知引用")
	unknownPrompt := "@session:missing-session-id please"
	marker = len(harness.model.callsSnapshot())
	harness.sendAndWait(t, sessionUnknown, unknownPrompt)
	unknownContent := requestContent(t, referenceRunBody(t, harness, marker), "user")
	if unknownContent != unknownPrompt {
		t.Fatalf("unknown reference changed the model input: %q", unknownContent)
	}
	if !harness.h.logger.contains(`session reference "missing-session-id": unknown session`) {
		t.Fatalf("unknown reference was not logged: %v", harness.h.logger.lines)
	}

	// Duplicate references are expanded once.
	sessionDup := harness.newChatSession(t, "重复引用")
	dupPrompt := contracts.FormatSessionReference(sessionB) + " " + contracts.FormatSessionReference(sessionB) + " twice"
	marker = len(harness.model.callsSnapshot())
	harness.sendAndWait(t, sessionDup, dupPrompt)
	dupContent := requestContent(t, referenceRunBody(t, harness, marker), "user")
	if strings.Count(dupContent, "[引用的会话") != 1 {
		t.Fatalf("duplicate reference injected %d blocks, want 1", strings.Count(dupContent, "[引用的会话"))
	}
	history, err = store.History().Load(ctx, sessionDup)
	if err != nil {
		t.Fatalf("History.Load(%q): %v", sessionDup, err)
	}
	for _, message := range history {
		if message.Role == "user" && message.Text() != dupPrompt {
			t.Fatalf("durable duplicate prompt = %q, want %q", message.Text(), dupPrompt)
		}
	}
}
