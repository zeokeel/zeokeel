package approvals

// TDD: the chat-reply parser is the other half of "回一句同意就行". It must be
// narrow and boring: a small vocabulary, an explicit index when several
// approvals wait, and no guessing. Anything unclear returns false so the caller
// asks "回 1 / 2 / 3" instead of acting on an interpretation.

import "testing"

func TestParseReplySinglePending(t *testing.T) {
	cases := []struct {
		text string
		want DecisionKind
	}{
		{"同意", DecisionApprove},
		{"同意！", DecisionApprove},
		{"  可以  ", DecisionApprove},
		{"发吧", DecisionApprove},
		{"行", DecisionApprove},
		{"ok", DecisionApprove},
		{"OK", DecisionApprove},
		{"批准", DecisionApprove},
		{"不同意", DecisionReject},
		{"不行", DecisionReject},
		{"别发", DecisionReject},
		{"拒绝", DecisionReject},
		{"no", DecisionReject},
		{"以后都同意", DecisionApproveAlways},
		{"一直同意", DecisionApproveAlways},
		{"改主意", DecisionUndo},
		{"撤回", DecisionUndo},
	}
	for _, tc := range cases {
		got, ok := ParseReply(tc.text, 1)
		if !ok {
			t.Errorf("ParseReply(%q, 1) not understood, want %v", tc.text, tc.want)
			continue
		}
		if got.Kind != tc.want {
			t.Errorf("ParseReply(%q, 1).Kind = %v, want %v", tc.text, got.Kind, tc.want)
		}
	}
}

func TestParseReplyNeedsIndexWhenSeveralPending(t *testing.T) {
	if _, ok := ParseReply("同意", 3); ok {
		t.Fatal("bare 同意 with three pending must not be understood (it would be a guess)")
	}
	if _, ok := ParseReply("不同意", 2); ok {
		t.Fatal("bare 不同意 with two pending must not be understood")
	}
}

func TestParseReplyWithIndex(t *testing.T) {
	cases := []struct {
		text  string
		index int
		want  DecisionKind
	}{
		{"2 不同意", 2, DecisionReject},
		{"同意 #2", 2, DecisionApprove},
		{"1同意", 1, DecisionApprove},
		{"第 3 条同意", 3, DecisionApprove},
		{"3: 不同意", 3, DecisionReject},
	}
	for _, tc := range cases {
		got, ok := ParseReply(tc.text, 3)
		if !ok {
			t.Errorf("ParseReply(%q, 3) not understood", tc.text)
			continue
		}
		if got.Kind != tc.want || got.Index != tc.index {
			t.Errorf("ParseReply(%q, 3) = %+v, want kind %v index %d", tc.text, got, tc.want, tc.index)
		}
	}
}

func TestParseReplyRejectsOutOfRangeIndex(t *testing.T) {
	if _, ok := ParseReply("5 同意", 3); ok {
		t.Fatal("index 5 with three pending must not be understood")
	}
	if _, ok := ParseReply("0 同意", 3); ok {
		t.Fatal("index 0 must not be understood")
	}
}

func TestParseReplyIgnoresUnrelatedText(t *testing.T) {
	for _, text := range []string{
		"", "   ", "这个报价单帮我改一下", "👍", "嗯", "同意吗？我再想想",
		"> 同意", // quoted text is not the user's decision
	} {
		if _, ok := ParseReply(text, 1); ok {
			t.Errorf("ParseReply(%q, 1) understood; want none", text)
		}
	}
}

func TestParseReplyUndoNeedsSinglePending(t *testing.T) {
	if _, ok := ParseReply("撤回", 2); ok {
		t.Fatal("undo with two pending must not be understood without an index")
	}
	if got, ok := ParseReply("撤回 #2", 2); !ok || got.Kind != DecisionUndo || got.Index != 2 {
		t.Fatalf("ParseReply(\"撤回 #2\", 2) = (%+v, %v), want undo index 2", got, ok)
	}
}
