package session_test

// US-01 / US-02 (2026-11 audit): the transcript interleaves per-iteration
// reasoning segments with prose and tool cards by their TextOffset timeline,
// live and restored alike. Before the fix the live read model's single
// aggregated Reasoning block was drawn above the whole reply, so a
// two-iteration turn showed ONE thinking disclosure instead of two interleaved
// ones (audit/probes/ext/us01_us02_test.go is the real-assembly counterpart).

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// twoIterationLiveMessage is the probe's live shape: ONE assistant message
// folds both iterations; segment 1 thought before the tool call, segment 2
// thought after it, and the final prose came last. All offsets are 0 because no
// prose arrived before the final answer (the pure tool-loop case).
func twoIterationLiveMessage() contracts.ChatMessage {
	return contracts.ChatMessage{
		ID:        "a1",
		Role:      contracts.ChatRoleAssistant,
		Text:      "FINAL-ANSWER",
		Reasoning: "THINK-ONE-A;THINK-ONE-B;" + "THINK-TWO-A;THINK-TWO-B;",
		ReasoningSegments: []contracts.ReasoningSegment{
			{Text: "THINK-ONE-A;THINK-ONE-B;", Iteration: 1, TextOffset: 0},
			{Text: "THINK-TWO-A;THINK-TWO-B;", Iteration: 2, TextOffset: 0},
		},
		Tools: []contracts.ToolCall{
			{ID: "t1", Name: "read_file", Status: "ok", Summary: "auth.go", TextOffset: 0},
		},
		ToolTimelineKnown: true,
		CreatedAt:         time.Unix(1700000001, 0),
	}
}

// countReasoningDisclosures counts accessible disclosure triggers by exact
// name (Texts() double-counts the trigger's Label and inner Text node).
func countReasoningDisclosures(tester *ui.Tester, name string) int {
	n := 0
	for _, info := range tester.AccessAll() {
		if info.Role == ui.RoleDisclosure && info.Name == name {
			n++
		}
	}
	return n
}

// TestLiveReasoningSegmentsInterleaveWithTools is the decisive US-01 frame
// assertion: a finished two-iteration live turn in verbose mode shows TWO
// reasoning disclosures, and their positions interleave with the tool row and
// the final prose: think(1) → tool → think(2) → answer.
func TestLiveReasoningSegmentsInterleaveWithTools(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "交错呈现",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "跑两轮", CreatedAt: time.Unix(1700000000, 0)},
			twoIterationLiveMessage(),
		},
	}, true)
	h := newVerboseWorkStepsHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	if n := countReasoningDisclosures(tester, "思考"); n != 2 {
		t.Fatalf("US-01: live two-iteration turn shows %d reasoning disclosures, want 2; texts = %v",
			n, tester.Texts())
	}
	// Positions: the first thinking row precedes the tool row, the tool row
	// precedes the second thinking row, and the answer comes last.
	disclosures := make([]ui.Rect, 0, 2)
	for _, info := range tester.AccessAll() {
		if info.Role == ui.RoleDisclosure && info.Name == "思考" {
			disclosures = append(disclosures, info.Bounds)
		}
	}
	tool, okTool := tester.Find("读取文件")
	answer, okAnswer := tester.Find("FINAL-ANSWER")
	if !okTool || !okAnswer {
		t.Fatalf("tool row or answer missing; texts = %v", tester.Texts())
	}
	if len(disclosures) != 2 {
		t.Fatalf("disclosure bounds = %d, want 2", len(disclosures))
	}
	if !(disclosures[0].Y < tool.Y && tool.Y < disclosures[1].Y && disclosures[1].Y < answer.Y) {
		t.Fatalf("US-01: timeline not interleaved: think1.Y=%.1f tool.Y=%.1f think2.Y=%.1f answer.Y=%.1f",
			disclosures[0].Y, tool.Y, disclosures[1].Y, answer.Y)
	}
	// Each disclosure holds ONLY its own iteration's thinking.
	tester.ClickAt(disclosures[0].X+disclosures[0].W/2, disclosures[0].Y+disclosures[0].H/2)
	tester.Advance(300 * time.Millisecond)
	tester.Frame()
	h.assertTexts(tester, "THINK-ONE-A;THINK-ONE-B;")
	h.assertNoTexts(tester, "THINK-TWO-A;THINK-TWO-B;")
}

// TestStreamingReasoningSegmentsInterleave covers the live streaming shape:
// iteration 1's segment is finished (folded 思考), iteration 2 is still thinking
// (open 思考中…), and the tool row sits between them.
func TestStreamingReasoningSegmentsInterleave(t *testing.T) {
	message := twoIterationLiveMessage()
	message.Streaming = true
	message.Text = ""
	message.Tools[0].Status = "ok"
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "流式交错", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "跑两轮"},
			message,
		},
	}, true)
	h := newVerboseWorkStepsHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	if n := countReasoningDisclosures(tester, "思考"); n != 1 {
		t.Fatalf("finished first segment: %d folded disclosures, want 1; texts = %v", n, tester.Texts())
	}
	if n := countReasoningDisclosures(tester, "思考中…"); n != 1 {
		t.Fatalf("streaming second segment: %d thinking disclosures, want 1; texts = %v", n, tester.Texts())
	}
	var folded, thinking ui.Rect
	for _, info := range tester.AccessAll() {
		if info.Role != ui.RoleDisclosure {
			continue
		}
		switch info.Name {
		case "思考":
			folded = info.Bounds
		case "思考中…":
			thinking = info.Bounds
		}
	}
	tool, ok := tester.Find("读取文件")
	if !ok {
		t.Fatalf("tool row missing; texts = %v", tester.Texts())
	}
	if !(folded.Y < tool.Y && tool.Y < thinking.Y) {
		t.Fatalf("streaming timeline not interleaved: think1.Y=%.1f tool.Y=%.1f thinking.Y=%.1f",
			folded.Y, tool.Y, thinking.Y)
	}
}

// TestRestoredTurnRendersSameInterleavedShape is the US-02 isomorphism: the
// restored shape (one assistant message per iteration, each with a single
// segment) renders the same think→tool→think→answer order as the live shape.
func TestRestoredTurnRendersSameInterleavedShape(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "恢复同构",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "跑两轮"},
			{
				ID: "a1", Role: contracts.ChatRoleAssistant,
				Reasoning:         "THINK-ONE-A;THINK-ONE-B;",
				ReasoningSegments: []contracts.ReasoningSegment{{Text: "THINK-ONE-A;THINK-ONE-B;", Iteration: 1, TextOffset: 0}},
				Tools: []contracts.ToolCall{
					{ID: "t1", Name: "read_file", Status: "ok", Summary: "auth.go", TextOffset: 0},
				},
				ToolTimelineKnown: true,
			},
			{
				ID: "a2", Role: contracts.ChatRoleAssistant, Text: "FINAL-ANSWER",
				Reasoning:         "THINK-TWO-A;THINK-TWO-B;",
				ReasoningSegments: []contracts.ReasoningSegment{{Text: "THINK-TWO-A;THINK-TWO-B;", Iteration: 2, TextOffset: 0}},
			},
		},
	}, true)
	h := newVerboseWorkStepsHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	if n := countReasoningDisclosures(tester, "思考"); n != 2 {
		t.Fatalf("US-02: restored turn shows %d reasoning disclosures, want 2; texts = %v", n, tester.Texts())
	}
	disclosures := make([]ui.Rect, 0, 2)
	for _, info := range tester.AccessAll() {
		if info.Role == ui.RoleDisclosure && info.Name == "思考" {
			disclosures = append(disclosures, info.Bounds)
		}
	}
	tool, okTool := tester.Find("读取文件")
	answer, okAnswer := tester.Find("FINAL-ANSWER")
	if !okTool || !okAnswer || len(disclosures) != 2 {
		t.Fatalf("restored frame missing parts; texts = %v", tester.Texts())
	}
	if !(disclosures[0].Y < tool.Y && tool.Y < disclosures[1].Y && disclosures[1].Y < answer.Y) {
		t.Fatalf("US-02: restored timeline not interleaved: think1.Y=%.1f tool.Y=%.1f think2.Y=%.1f answer.Y=%.1f",
			disclosures[0].Y, tool.Y, disclosures[1].Y, answer.Y)
	}
}

// TestStandardFoldSummaryCountsReasoningSegments covers the standard mode's
// finished fold: the single summary row keeps its one-line shape but names the
// segment count, so a folded multi-iteration turn does not read as one thought.
func TestStandardFoldSummaryCountsReasoningSegments(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "折叠计数",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "跑两轮"},
			twoIterationLiveMessage(),
		},
	}, true)
	h := newHarness(t, chat) // standard mode (the product default)
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "过程", "2 段思考", "1 个工具调用")
	// The fold stays one summary row: no expanded thinking block by default.
	if n := countReasoningDisclosures(tester, "思考"); n != 0 {
		t.Fatalf("standard fold expanded %d thinking blocks, want 0; texts = %v", n, tester.Texts())
	}
	// Expanding the fold restores the interleaved timeline (two disclosures).
	h.click(tester, "过程 · 2 段思考 · 1 个工具调用")
	tester.Advance(300 * time.Millisecond)
	tester.Frame()
	if n := countReasoningDisclosures(tester, "思考"); n != 2 {
		t.Fatalf("expanded standard fold shows %d thinking blocks, want 2; texts = %v", n, tester.Texts())
	}
}

// TestTextOffsetInterleavesReasoningWithProse covers offsets > 0: prose before
// the first thought, a tool between the thoughts, and prose after — the full
// chronological merge of text, reasoning and tools.
func TestTextOffsetInterleavesReasoningWithProse(t *testing.T) {
	const before = "BEFORE-THINK."
	const middle = "BETWEEN-TOOL."
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "偏移交错",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "问题"},
			{
				ID: "a1", Role: contracts.ChatRoleAssistant,
				Text:      before + middle + "AFTER-ALL.",
				Reasoning: "THINK-ONE;THINK-TWO;",
				ReasoningSegments: []contracts.ReasoningSegment{
					{Text: "THINK-ONE;", Iteration: 1, TextOffset: len(before)},
					{Text: "THINK-TWO;", Iteration: 2, TextOffset: len(before) + len(middle)},
				},
				Tools: []contracts.ToolCall{
					{ID: "t1", Name: "read_file", Status: "ok", Summary: "auth.go", TextOffset: len(before)},
				},
				ToolTimelineKnown: true,
			},
		},
	}, true)
	h := newVerboseWorkStepsHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	beforeRect, okBefore := tester.Find(before)
	middleRect, okMiddle := tester.Find(middle)
	afterRect, okAfter := tester.Find("AFTER-ALL.")
	tool, okTool := tester.Find("读取文件")
	if !okBefore || !okMiddle || !okAfter || !okTool {
		t.Fatalf("prose/tool parts missing; texts = %v", tester.Texts())
	}
	disclosures := make([]ui.Rect, 0, 2)
	for _, info := range tester.AccessAll() {
		if info.Role == ui.RoleDisclosure && info.Name == "思考" {
			disclosures = append(disclosures, info.Bounds)
		}
	}
	if len(disclosures) != 2 {
		t.Fatalf("offset turn shows %d thinking blocks, want 2; texts = %v", len(disclosures), tester.Texts())
	}
	// BEFORE-THINK. → think1 → tool → BETWEEN-TOOL. → think2 → AFTER-ALL.
	if !(beforeRect.Y < disclosures[0].Y && disclosures[0].Y < tool.Y &&
		tool.Y < middleRect.Y && middleRect.Y < disclosures[1].Y && disclosures[1].Y < afterRect.Y) {
		t.Fatalf("offset timeline wrong order: before=%.1f think1=%.1f tool=%.1f middle=%.1f think2=%.1f after=%.1f",
			beforeRect.Y, disclosures[0].Y, tool.Y, middleRect.Y, disclosures[1].Y, afterRect.Y)
	}
}
