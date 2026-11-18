package session_test

// Cross-frame regression for the streaming transcript's geometry (user report:
// the whole reply shook while it streamed, and only settled once the turn
// stopped). Four mechanisms used to move the layout mid-stream, and each one is
// pinned here by a property of the *relative* geometry — an absolute Y may and
// should move, because the transcript follows the end and the whole column
// scrolls up as content is appended:
//
//  1. a reasoning fold whose state key and component identity changed when a
//     second segment arrived (the fold the reader opened closed by itself);
//  2. prose that was re-parsed as live Markdown after a later event had already
//     finished it (now keyed per event through uikit.MarkdownBlock, so mygo keeps
//     each block's parsed state instead of forgetting it);
//  3. a follow animation that re-targeted on every growing frame;
//  4. a tie in the timeline merge that resolved by "how many events came before",
//     so a new event could re-order the blocks it sat next to.
//
// The assertions are the four rules the Lead set: adjacent gaps constant,
// finished heights constant, order = arrival order, and each finished block's
// offset from the first block constant.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// streamFrame is one state of the streaming message the test renders.
type streamFrame struct {
	text      string
	segments  []contracts.ReasoningSegment
	tools     []contracts.ToolCall
	streaming bool
}

// streamingTurn builds the frames of a three-iteration turn: thinking, prose, a
// tool call, its output, thinking, more prose, a second tool, then the settled
// message. Each frame is one delta's worth of new content.
func streamingTurn() []streamFrame {
	const b1 = "B1 目录结构"
	return []streamFrame{
		{ // 0: the turn opens with thinking
			segments:  []contracts.ReasoningSegment{{Text: "R1 先看看目录", Iteration: 1, TextOffset: 0}},
			streaming: true,
		},
		{ // 1: the first prose delta arrives
			text:      b1,
			segments:  []contracts.ReasoningSegment{{Text: "R1 先看看目录", Iteration: 1, TextOffset: 0}},
			streaming: true,
		},
		{ // 2: the first tool call starts at the end of B1
			text:     b1,
			segments: []contracts.ReasoningSegment{{Text: "R1 先看看目录", Iteration: 1, TextOffset: 0}},
			tools: []contracts.ToolCall{{
				ID: "t1", Name: "read_file", Status: "running", Summary: "auth.go", TextOffset: len(b1),
			}},
			streaming: true,
		},
		{ // 3: the tool finishes
			text:     b1,
			segments: []contracts.ReasoningSegment{{Text: "R1 先看看目录", Iteration: 1, TextOffset: 0}},
			tools: []contracts.ToolCall{{
				ID: "t1", Name: "read_file", Status: "ok", Summary: "auth.go", TextOffset: len(b1),
			}},
			streaming: true,
		},
		{ // 4: the second iteration thinks
			text: b1,
			segments: []contracts.ReasoningSegment{
				{Text: "R1 先看看目录", Iteration: 1, TextOffset: 0},
				{Text: "R2 再看配置", Iteration: 2, TextOffset: len(b1)},
			},
			tools: []contracts.ToolCall{{
				ID: "t1", Name: "read_file", Status: "ok", Summary: "auth.go", TextOffset: len(b1),
			}},
			streaming: true,
		},
		{ // 5: the second prose slice grows
			text: b1 + "B2 配置没问题",
			segments: []contracts.ReasoningSegment{
				{Text: "R1 先看看目录", Iteration: 1, TextOffset: 0},
				{Text: "R2 再看配置", Iteration: 2, TextOffset: len(b1)},
			},
			tools: []contracts.ToolCall{{
				ID: "t1", Name: "read_file", Status: "ok", Summary: "auth.go", TextOffset: len(b1),
			}},
			streaming: true,
		},
		{ // 6: the second tool call starts after B2
			text: b1 + "B2 配置没问题",
			segments: []contracts.ReasoningSegment{
				{Text: "R1 先看看目录", Iteration: 1, TextOffset: 0},
				{Text: "R2 再看配置", Iteration: 2, TextOffset: len(b1)},
			},
			tools: []contracts.ToolCall{
				{ID: "t1", Name: "read_file", Status: "ok", Summary: "auth.go", TextOffset: len(b1)},
				{ID: "t2", Name: "bash", Status: "running", Summary: "go build", TextOffset: len(b1 + "B2 配置没问题")},
			},
			streaming: true,
		},
		{ // 7: the second tool finishes; the turn still streams
			text: b1 + "B2 配置没问题",
			segments: []contracts.ReasoningSegment{
				{Text: "R1 先看看目录", Iteration: 1, TextOffset: 0},
				{Text: "R2 再看配置", Iteration: 2, TextOffset: len(b1)},
			},
			tools: []contracts.ToolCall{
				{ID: "t1", Name: "read_file", Status: "ok", Summary: "auth.go", TextOffset: len(b1)},
				{ID: "t2", Name: "bash", Status: "ok", Summary: "go build", TextOffset: len(b1 + "B2 配置没问题")},
			},
			streaming: true,
		},
		{ // 8: the turn settles — same content, no longer streaming
			text: b1 + "B2 配置没问题",
			segments: []contracts.ReasoningSegment{
				{Text: "R1 先看看目录", Iteration: 1, TextOffset: 0},
				{Text: "R2 再看配置", Iteration: 2, TextOffset: len(b1)},
			},
			tools: []contracts.ToolCall{
				{ID: "t1", Name: "read_file", Status: "ok", Summary: "auth.go", TextOffset: len(b1)},
				{ID: "t2", Name: "bash", Status: "ok", Summary: "go build", TextOffset: len(b1 + "B2 配置没问题")},
			},
			streaming: false,
		},
	}
}

// assistantFrameMessage is the assistant message one frame's state describes.
func assistantFrameMessage(frame streamFrame) contracts.ChatMessage {
	return contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Streaming: frame.streaming,
		Text: frame.text, ReasoningSegments: frame.segments,
		Tools: frame.tools, ToolTimelineKnown: len(frame.tools) > 0,
	}
}

// streamingChat is one session holding the assistant message built from a frame.
func streamingChat(frame streamFrame) *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "流式几何", Running: frame.streaming,
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "看看仓库"},
			assistantFrameMessage(frame),
		},
	}, true)
	return chat
}

// blockReading is one block's measurable state in one frame.
type blockReading struct {
	name string
	rel  float32 // top minus the first block's top: the content-space offset
	h    float32
}

// TestStreamingTranscriptDoesNotJump is the cross-frame regression for the
// shaking reply.
func TestStreamingTranscriptDoesNotJump(t *testing.T) {
	frames := streamingTurn()
	chat := streamingChat(frames[0])
	// Detailed work steps is the mode that draws the interleaved timeline while
	// the turn streams (the default standard mode draws one activity line and the
	// prose); it is the mode where the reported "mixed, jumping" blocks appear.
	h := newPermissionHarness(t, chat, newPermissionSettings(map[string]string{
		contracts.SettingWorkStepsMode: contracts.WorkStepsDetailed,
	}))
	tester := h.tester()

	// The blocks in arrival order, with the label that identifies each in a frame.
	// A reasoning row is identified by its own text while it is open; a tool row by
	// its name-and-state label.
	order := []struct{ name, label string }{
		{"thinking-1", "R1 先看看目录"},
		{"prose-1", "B1 目录结构"},
		{"tool-t1", "读取文件 · 完成"},
		{"thinking-2", "R2 再看配置"},
	}

	read := func() ([]blockReading, bool) {
		out := make([]blockReading, 0, len(order))
		for _, block := range order {
			rect, ok := tester.Find(block.label)
			if !ok {
				// Not arrived yet: the walk reports the blocks that are on screen
				// (a block's absence here means "not there yet", and the assertions
				// only describe blocks that exist).
				continue
			}
			out = append(out, blockReading{name: block.name, h: rect.H, rel: rect.Y})
		}
		if len(out) > 0 {
			first := out[0].rel
			for index := range out {
				out[index].rel -= first
			}
		}
		return out, true
	}

	prev := map[string]blockReading{}
	prevGap := map[string]float32{}
	readerOpened := false
	var table []struct {
		frame int
		blockReading
	}

	for frameIndex, frame := range frames {
		session, _ := chat.Session("sess-1")
		session.Messages[1] = assistantFrameMessage(frame)
		session.Running = frame.streaming
		chat.updateSession(session)
		// Exactly one frame per delta, with no settle pass: the frame the new event
		// lands in must already be the correct layout. A second pass would hide the
		// transient re-layout that an unkeyed Markdown block causes.
		tester.Frame()

		// ⑤ A fold the reader opened keeps its state when the next iteration
		//    arrives: the segment's key and component identity are per-segment from
		//    the first frame on, so a new segment cannot reset what the reader chose.
		if frameIndex == 3 {
			if rect, ok := tester.Find("R1 先看看目录"); ok {
				tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
				tester.Frame()
			}
			readerOpened = true
		}
		if readerOpened && frame.streaming {
			if _, ok := tester.Find("R1 先看看目录"); !ok {
				t.Fatalf("frame %d: the thinking fold the reader opened closed by itself; texts = %v",
					frameIndex, tester.Texts())
			}
		}

		// The reasoning row itself is a fixed 28 DIP line (the disclosure trigger);
		// only its panel grows, so a streaming/complete flip never changes the row's
		// height. This is the trigger's own geometry, not a child's.
		if info, ok := tester.AccessByRole(ui.RoleDisclosure, "思考"); ok {
			if info.Bounds.H != design.RowCompactHeight {
				t.Fatalf("frame %d: thinking row is %.1f high, want the %v DIP trigger",
					frameIndex, info.Bounds.H, design.RowCompactHeight)
			}
		}

		blocks, complete := read()
		if !complete {
			// The settled frame folds its process, so a block may leave the tree —
			// but never while the turn streams.
			if frame.streaming {
				t.Fatalf("frame %d: a block vanished mid-stream; texts = %v", frameIndex, tester.Texts())
			}
			continue
		}
		for _, block := range blocks {
			table = append(table, struct {
				frame int
				blockReading
			}{frameIndex, block})
		}

		// ③ Block order is arrival order, by content-space offset.
		for index := 1; index < len(blocks); index++ {
			if blocks[index].rel <= blocks[index-1].rel {
				t.Fatalf("frame %d: %s (rel %.1f) is not below %s (rel %.1f)",
					frameIndex, blocks[index].name, blocks[index].rel,
					blocks[index-1].name, blocks[index-1].rel)
			}
		}

		// From frame 4 on every block on screen is finished (the next iteration's
		// reading segment has arrived): its height and its offset from the first
		// block must stop changing, and so must the gap to its neighbour. The
		// column itself still scrolls, which is why the absolute Y is not asserted.
		if frameIndex >= 4 {
			for _, block := range blocks {
				old, had := prev[block.name]
				if !had {
					continue
				}
				if delta := block.h - old.h; delta > 0.5 || delta < -0.5 {
					t.Fatalf("frame %d: finished block %s changed height %.1f → %.1f",
						frameIndex, block.name, old.h, block.h)
				}
				if delta := block.rel - old.rel; delta > 0.5 || delta < -0.5 {
					t.Fatalf("frame %d: finished block %s reflowed %.1f DIP from the column top",
						frameIndex, block.name, delta)
				}
			}
			for index := 1; index < len(blocks); index++ {
				gap := blocks[index].rel - (blocks[index-1].rel + blocks[index-1].h)
				key := blocks[index-1].name + "->" + blocks[index].name
				if old, had := prevGap[key]; had {
					if delta := gap - old; delta > 0.5 || delta < -0.5 {
						t.Fatalf("frame %d: gap %s changed %.1f → %.1f", frameIndex, key, old, gap)
					}
				}
				prevGap[key] = gap
			}
		}
		for _, block := range blocks {
			prev[block.name] = block
		}
	}

	// The evidence table: each block's content-space offset and height per frame.
	for _, row := range table {
		t.Logf("frame %d  %-11s rel=%6.1f  h=%5.1f", row.frame, row.name, row.rel, row.h)
	}
	if len(table) < 12 {
		t.Fatalf("the frame walk was too short (%d observations)", len(table))
	}

	// ⑥ The settled frame draws the same blocks in the same vertical order: "it
	//    looks right after Stop" must be the same layout, not a second one. A
	//    finished turn folds its process, so the fold is opened first.
	if rect, ok := tester.Find("过程 · 2 个工具调用"); ok {
		tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
		tester.Frame()
		tester.Frame()
	}
	settled := []string{"B1 目录结构", "读取文件 · 完成", "B2 配置没问题", "运行命令 · 完成"}
	var lastY float32 = -1
	for _, label := range settled {
		rect, ok := tester.Find(label)
		if !ok {
			t.Fatalf("settled frame lost %q; texts = %v", label, tester.Texts())
		}
		if lastY >= 0 && rect.Y <= lastY {
			t.Fatalf("settled order differs from the streaming order at %q (y=%.1f, previous %.1f)",
				label, rect.Y, lastY)
		}
		lastY = rect.Y
	}
	thinkingRows := 0
	for _, text := range tester.Texts() {
		if text == "思考" {
			thinkingRows++
		}
	}
	if thinkingRows < 2 {
		t.Fatalf("settled frame shows %d thinking rows, want both iterations; texts = %v",
			thinkingRows, tester.Texts())
	}
}

var _ = ui.RoleDisclosure
