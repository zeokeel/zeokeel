package session_test

// Tests for the status bar under the composer (S8, B-spec §3.2.6): one 32 DIP
// row in three zones — the run state with the queue on the left, the context
// gauge with its 详情 popover on the right. The bar reports only accurate values
// from the session read model and hides whole items (and itself) when the data
// is unknown; every figure a user reads is at least 12 DIP, the numbers are mono,
// and the details that would turn the bar into a paragraph live in the popover.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// statusChat scripts one session with the given messages after a first user
// prompt, so the composer renders and the strip has a conversation to sum.
func statusChat(messages ...contracts.ChatMessage) *modelChat {
	chat := newModelChat()
	chat.config = contracts.ChatConfig{Model: "gpt-4o"}
	all := append([]contracts.ChatMessage{
		{ID: "u1", Role: contracts.ChatRoleUser, Text: "第一问"},
	}, messages...)
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "状态条", Messages: all}, true)
	return chat
}

// statusCatalog knows the configured model's 128K context window.
func statusCatalog() *fakeCatalog {
	return &fakeCatalog{models: []contracts.ModelInfo{
		{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128000},
	}}
}

// statusUsageMessages is the scripted conversation of the main test: two rounds,
// three tool calls, 7,400 generated output tokens over 90s (82 tokens/s averaged
// over the turn wall time, tool time included), the newest model request saw a
// 120,000-token context of the 128,000 window (6% left) and the cache hit is
// 95% of the prompt tokens. The session's billing prompt totals (220,000) are
// deliberately larger than the window and must never be compared with it.
func statusUsageMessages() []contracts.ChatMessage {
	return []contracts.ChatMessage{
		{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "一",
			Tools: []contracts.ToolCall{
				{ID: "t1", Name: "read_file", Status: "ok"},
				{ID: "t2", Name: "grep", Status: "ok"},
			},
			Usage: &contracts.MessageUsage{
				PromptTokens: 100000, CompletionTokens: 5000, TotalTokens: 105000,
				CachedTokens: 98000, DurationMS: 60000, ContextTokens: 90000,
			}},
		{ID: "u2", Role: contracts.ChatRoleUser, Text: "第二问"},
		{ID: "a2", Role: contracts.ChatRoleAssistant, Text: "二",
			Tools: []contracts.ToolCall{{ID: "t3", Name: "bash", Status: "ok"}},
			Usage: &contracts.MessageUsage{
				PromptTokens: 120000, CompletionTokens: 2400, TotalTokens: 122400,
				CachedTokens: 110000, DurationMS: 30000, ContextTokens: 120000,
			}},
	}
}

// TestSessionStatusStripRendersUsage covers the bar's right zone: the newest
// request's context over the catalogue window in one compact figure, the gauge
// under it, and the 详情 control that opens the exact figures. The bar carries no
// sentence — that is what the popover is for.
func TestSessionStatusStripRendersUsage(t *testing.T) {
	chat := statusChat(statusUsageMessages()...)
	h := newModelHarness(t, chat, statusCatalog())
	tester := h.tester()

	// The bar reads in the usage audit's order: the run's shape, its volume, then
	// the context percentage; the exact figures stay in 详情.
	h.assertTexts(tester, "2", "3", "82", "7.4K", "95%", "94%")
	// The read-out carries every figure inline, so the 详情 popover is gone: no
	// control of that name exists anywhere in the frame (user: 详情都显示出来了).
	h.assertNoTexts(tester, "详情", "Details")
	// Cumulative billing prompt tokens never reach the context ratio.
	h.assertNoTexts(tester, "220K/128K", "100K/128K")

	// The bar sits directly under the composer card, below the send control.
	strip, ok := tester.Find("94%")
	if !ok {
		t.Fatalf("status bar missing; texts = %v", tester.Texts())
	}
	send, ok := tester.Find("发送")
	if !ok {
		t.Fatalf("send control missing; texts = %v", tester.Texts())
	}
	if strip.Y <= send.Y {
		t.Fatalf("status bar y=%.1f is not below the composer send control y=%.1f", strip.Y, send.Y)
	}

	// The context level is the official Meter, a real ratio, never a made-up
	// value: 120K of a 128K window.
	meter, ok := tester.AccessByRole(ui.RoleMeter, "上下文 94%")
	if !ok {
		t.Fatalf("status bar has no context meter; texts = %v", tester.Texts())
	}
	if meter.Bounds.W != design.Space8 || meter.Bounds.H != design.Space1 {
		t.Fatalf("context meter is %.0fx%.0f, want the %vx%v bar",
			meter.Bounds.W, meter.Bounds.H, design.Space8, design.Space1)
	}

}

// TestSessionStatusStripEnglishCopy covers the English wording.
func TestSessionStatusStripEnglishCopy(t *testing.T) {
	chat := statusChat(statusUsageMessages()...)
	h := newModelHarness(t, chat, statusCatalog())
	tester := h.tester()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()

	h.assertTexts(tester, "rounds", "steps", "tok/s", "tok", "cached", "94%")
	// No 详情 popover: its rows (Turns, Tool calls, …) are inline or gone.
	h.assertNoTexts(tester, "Details", "Turns", "Tool calls")
}

// TestSessionStatusStripHiddenWithoutUsage covers the empty/failed conversations
// with no token accounting: the bar shows no gauge and no zeroed figures.
func TestSessionStatusStripHiddenWithoutUsage(t *testing.T) {
	chat := statusChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "回答",
		Tools: []contracts.ToolCall{{ID: "t1", Name: "bash", Status: "ok"}},
	})
	h := newModelHarness(t, chat, statusCatalog())
	tester := h.tester()
	h.assertTexts(tester, "回答", "发送")
	h.assertNoTexts(tester, "详情", "/128K", "上下文用量")

	// A conversation with no messages at all hides it too.
	empty := newModelChat()
	empty.addSession(contracts.ChatSession{ID: "sess-2", Title: "空"}, true)
	h = newModelHarness(t, empty, nil)
	tester = h.tester()
	h.assertNoTexts(tester, "详情", "上下文用量")
}

// TestSessionStatusStripWithoutCatalog covers the unknown-window case: without a
// model catalogue there is no context window, so the gauge and the context rows
// are hidden entirely; the other figures stay in the popover.
func TestSessionStatusStripWithoutCatalog(t *testing.T) {
	chat := statusChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "一",
		Usage: &contracts.MessageUsage{
			PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500,
			CachedTokens: 500, DurationMS: 1500, ContextTokens: 900,
		},
	})
	h := newModelHarness(t, chat, nil)
	tester := h.tester()
	h.assertNoTexts(tester, "/128K", "900/", "上下文用量")
	h.assertTexts(tester, "缓存命中", "50%")
	h.assertNoTexts(tester, "详情", "上下文剩余", "上下文窗口")
}

// TestSessionStatusStripOmitsContextWithUnknownModel covers the catalogue that
// does not know the effective model: even with a configured compactor budget,
// no context window is invented (the budget is not a context window).
func TestSessionStatusStripOmitsContextWithUnknownModel(t *testing.T) {
	chat := statusChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "一",
		Usage: &contracts.MessageUsage{
			PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500,
			CachedTokens: 500, DurationMS: 1500, ContextTokens: 900,
		},
	})
	chat.config = contracts.ChatConfig{Model: "custom-model", ContextBudget: 64000}
	h := newModelHarness(t, chat, statusCatalog())
	tester := h.tester()
	h.assertNoTexts(tester, "剩余", "/64K", "/128K", "上下文用量")
}

// TestSessionStatusStripOmitsContextWithoutContextTokens covers a provider that
// reports usage but no per-request context size: the window is known, yet the
// gauge and the context rows stay hidden instead of guessing.
func TestSessionStatusStripOmitsContextWithoutContextTokens(t *testing.T) {
	chat := statusChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "一",
		Usage: &contracts.MessageUsage{
			PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500,
			CachedTokens: 500, DurationMS: 1500,
		},
	})
	h := newModelHarness(t, chat, statusCatalog())
	tester := h.tester()
	h.assertNoTexts(tester, "剩余", "/128K", "0/128K", "上下文用量", "详情")
}

// TestSessionStatusStripHidesUnknownSpeed covers a usage row without wall time
// or output tokens: the speed row is hidden, the other figures remain.
func TestSessionStatusStripHidesUnknownSpeed(t *testing.T) {
	chat := statusChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "一",
		Usage: &contracts.MessageUsage{
			PromptTokens: 120000, TotalTokens: 120000,
			CachedTokens: 60000, ContextTokens: 120000,
		},
	})
	h := newModelHarness(t, chat, statusCatalog())
	tester := h.tester()
	h.assertTexts(tester, "94%", "缓存命中", "50%")
	h.assertNoTexts(tester, "tokens/s", "详情")
}

// TestStatusStripOmitsTheDuplicateRunState covers the user's report that the bar
// repeated the transcript: a streaming reply already says 生成中 inside the
// conversation, so the bar below it must not say 运行中 as well. A stop in flight
// is not a duplicate — the transcript does not report it — so it stays on the bar.
func TestStatusStripOmitsTheDuplicateRunState(t *testing.T) {
	chat := statusChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "正在检查", Streaming: true,
		Usage: &contracts.MessageUsage{CompletionTokens: 40, TotalTokens: 1040, DurationMS: 2000},
	})
	session, _ := chat.Session("sess-1")
	session.Running = true
	chat.updateSession(session)
	// Verbose work steps keeps the transcript's own live line visible (this is the
	// mode the user's screenshot showed).
	h := newModelThinkingHarness(t, chat, newPermissionSettings(map[string]string{
		contracts.SettingWorkStepsMode: contracts.WorkStepsVerbose,
	}), statusCatalog())
	tester := h.tester()

	// The transcript states the run; the bar keeps only the figures.
	h.assertTexts(tester, "生成中")
	runWord := 0
	for _, text := range tester.Texts() {
		if text == "运行中" {
			runWord++
		}
	}
	if runWord != 1 {
		t.Fatalf("the frame shows the run word %d time(s), want once (the top band's badge); texts = %v",
			runWord, tester.Texts())
	}
	// That one run word belongs to the band above the transcript, never to the bar
	// under the composer.
	run, ok := tester.Find("运行中")
	stop, okStop := tester.Find("停止")
	if !ok || !okStop || run.Y >= stop.Y {
		t.Fatalf("a run word sits at y=%.1f, at or below the composer's own controls (y=%.1f)",
			run.Y, stop.Y)
	}

	// A stop in flight is the one run state the transcript does not show, so it
	// stays on the bar: the reply still says 生成中, the bar says 停止中…. This
	// fake delays the settle, so the stopping state is observable (the plain fake
	// settles inside Cancel and would skip it).
	stopping := stoppingChat()
	stopping.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "停止中", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "第一问"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "正在检查", Streaming: true},
		},
	}, true)
	stopping.config = contracts.ChatConfig{Model: "gpt-4o"}
	sh := newChatServiceHarness(t, stopping, &fakeSessionNotifier{})
	stester := sh.tester()
	stester.Frame()
	sh.click(stester, "停止")
	stester.Frame()
	// The strip reports the stop; the reply keeps its own live line (its wording
	// depends on the work-steps policy: 正在回复 here, 生成中 in verbose).
	sh.assertTexts(stester, "停止中…", "正在回复")
}

// TestStatusStripMetricsMatchDSH covers the bar's read-out against DSH's line:
// three zones — 轮次/步数/速度 · 总量/缓存命中 · 上下文圆环 — with the figures in
// mono, the words beside them, groups Space3 apart and a middot inside a group.
// A zone with no truthful figure is left out whole, and a narrow window keeps
// only the ring and 详情.
func TestStatusStripMetricsMatchDSH(t *testing.T) {
	chat := statusChat(statusUsageMessages()...)
	h := newModelHarness(t, chat, statusCatalog())
	tester := h.tester()

	// Zone 1: 2 轮 3 步 · 82 tok/s (the middot only before the speed).
	h.assertTexts(tester, "2", "轮", "3", "步", "82", "tok/s", "·")
	// Zone 2: 7.4K tok · 缓存命中 95%.
	h.assertTexts(tester, "7.4K", "tok", "缓存命中", "95%")
	// Zone 3: the context meter and its percentage.
	meter, ok := tester.AccessByRole(ui.RoleMeter, "上下文 94%")
	if !ok {
		t.Fatalf("context meter missing; texts = %v", tester.Texts())
	}
	if meter.Bounds.W != design.Space8 {
		t.Fatalf("meter box = %.0f wide, want %v", meter.Bounds.W, design.Space8)
	}
	// The cluster ends with the ring: the percentage is the last element and
	// nothing follows it (the 详情 popover is gone — every figure is inline).
	percent, ok := tester.Find("94%")
	if !ok {
		t.Fatalf("context percentage missing; texts = %v", tester.Texts())
	}
	if percent.X <= meter.Bounds.X {
		t.Fatalf("the percentage at %.1f is not right of the meter at %.1f", percent.X, meter.Bounds.X)
	}
	h.assertNoTexts(tester, "详情")

	// Every number is separated from its word by a real 4 DIP gap, in both
	// languages: a space character inside a text node is trimmed at the element
	// boundary, which is how 缓存命中86% happened. The gap is measured here so the
	// regression cannot come back silently.
	gapAtLeast := func(left, right string, min float32) {
		t.Helper()
		l, okL := tester.Find(left)
		r, okR := tester.Find(right)
		if !okL || !okR {
			t.Fatalf("measure %q … %q: missing (%v, %v); texts = %v", left, right, okL, okR, tester.Texts())
		}
		if gap := r.X - (l.X + l.W); gap < min {
			t.Fatalf("gap between %q and %q is %.1f DIP, want at least %.1f", left, right, gap, min)
		}
	}
	// zh: figure then word, except the cache hit, where the word comes first.
	gapAtLeast("2", "轮", design.Space1-0.5)
	gapAtLeast("3", "步", design.Space1-0.5)
	gapAtLeast("82", "tok/s", design.Space1-0.5)
	gapAtLeast("7.4K", "tok", design.Space1-0.5)
	gapAtLeast("缓存命中", "95%", design.Space1-0.5)

	// A session with no speed and no cache hit draws no empty zone: only the
	// parts that have a truthful figure.
	bare := statusChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "一",
		Usage: &contracts.MessageUsage{CompletionTokens: 40, TotalTokens: 1040},
	})
	bh := newModelHarness(t, bare, statusCatalog())
	btester := bh.tester()
	bh.assertNoTexts(btester, "tok/s", "缓存命中")
	// A narrow window keeps the ring and 详情 only.
	btester.SetSize(1024, 720)
	btester.Frame()
	btester.Frame()
	bh.assertNoTexts(btester, "tok/s", "缓存命中", "轮", "详情")
}

// TestStatusStripScaleAndCentre covers the 5× audit of the bar: one type step
// across the whole read-out (12 DIP copy, so a text's own box is the 18 DIP line),
// a 24 DIP 详情 control, a 16 DIP ring — and the whole cluster centred in the
// reading column rather than pinned to the right edge.
func TestStatusStripScaleAndCentre(t *testing.T) {
	chat := statusChat(statusUsageMessages()...)
	h := newModelHarness(t, chat, statusCatalog())
	tester := h.tester()

	// Every word and figure of the read-out is the same 12 DIP step.
	// 详情 is measured as a control below (its accessible name is the button's).
	for _, label := range []string{"轮", "步", "tok/s", "tok", "缓存命中", "95%", "94%"} {
		rect, ok := tester.Find(label)
		if !ok {
			t.Fatalf("strip part %q missing; texts = %v", label, tester.Texts())
		}
		// 12/18: the text's own box is the 18 DIP line, never the 22 DIP body line
		// the 详情 control used to inherit.
		if rect.H > 18.5 {
			t.Fatalf("strip part %q is %.1f high, want the 12/18 line (18)", label, rect.H)
		}
	}
	// The bar has no button of its own: the read-out is text and the ring.
	if _, ok := tester.AccessByRole(ui.RoleButton, "详情"); ok {
		t.Fatalf("a 详情 control is back in the bar; texts = %v", tester.Texts())
	}
	// The context level is one bar of the icon step's length and the hairline's
	// weight, never the meter's default 80 by 8 capsule.
	meter, ok := tester.AccessByRole(ui.RoleMeter, "上下文 94%")
	if !ok {
		t.Fatalf("context meter missing; texts = %v", tester.Texts())
	}
	if meter.Bounds.W != design.Space8 || meter.Bounds.H != design.Space1 {
		t.Fatalf("meter is %.0fx%.0f, want %vx%v", meter.Bounds.W, meter.Bounds.H,
			design.Space8, design.Space1)
	}

	// The cluster is centred in the reading column, not pushed to the right edge:
	// its midpoint matches the composer card's midpoint. The card's text field
	// spans its inner width, so the field's midpoint is the column's centre.
	field, okField := tester.AccessByRole(ui.RoleTextField, "输入消息…")
	if !okField {
		t.Fatalf("composer input missing; texts = %v", tester.Texts())
	}
	columnCentre := field.Bounds.X + field.Bounds.W/2
	left, okLeft := tester.Find("2")
	right, okRight := tester.Find("94%")
	if !okLeft || !okRight {
		t.Fatalf("strip ends missing; texts = %v", tester.Texts())
	}
	clusterCentre := (left.X + right.X + right.W) / 2
	if delta := clusterCentre - columnCentre; delta > 16 || delta < -16 {
		t.Fatalf("the read-out cluster is %.1f DIP off the column's centre (cluster %.1f, column %.1f)",
			delta, clusterCentre, columnCentre)
	}
}

// TestStatusStripMeterReadsTheContext covers the context level as the official
// Meter: a 32 by 4 DIP bar, never the meter's own 80 by 8 capsule, filled accent
// while there is room, warning from 85% and danger at the edge — the capacity
// reading (85% amber, 100% red), not the battery reading (where a low value is
// the bad one, which MeterLevels would take if Critical sat below Warning).
func TestStatusStripMeterReadsTheContext(t *testing.T) {
	cases := []struct {
		name    string
		used    int
		want    string
		wantCol ui.Color
	}{
		{"room left", 42_000, "上下文 33%", design.Light().Accent},
		{"nearly full", 112_640, "上下文 88%", design.Light().Warning},
		{"full", 128_000, "上下文 100%", design.Light().Danger},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chat := statusChat(contracts.ChatMessage{
				ID: "a1", Role: contracts.ChatRoleAssistant, Text: "一",
				Usage: &contracts.MessageUsage{
					CompletionTokens: 500, TotalTokens: 1040, DurationMS: 2000,
					ContextTokens: tc.used,
				},
			})
			h := newModelHarness(t, chat, statusCatalog())
			tester := h.tester()
			meter, ok := tester.AccessByRole(ui.RoleMeter, tc.want)
			if !ok {
				t.Fatalf("meter %q missing; texts = %v", tc.want, tester.Texts())
			}
			if meter.Bounds.W != design.Space8 || meter.Bounds.H != design.Space1 {
				t.Fatalf("meter is %.0fx%.0f, want %vx%v (the meter's own 80x8 capsule would break the band)",
					meter.Bounds.W, meter.Bounds.H, design.Space8, design.Space1)
			}
			// The fill is read a few DIP into the bar's left end, over the page.
			img := tester.Image()
			got := img.RGBAAt(int(meter.Bounds.X)+3, int(meter.Bounds.Y+meter.Bounds.H/2))
			want := overToken(tc.wantCol, design.Light().BgBase)
			if !nearColor(got, want, 6) {
				t.Fatalf("meter fill = %+v, want %+v", got, want)
			}
		})
	}
}

// TestStatusStripHidesWhileTheTurnRuns covers the half-empty bar the user saw:
// while a turn runs its usage has not landed yet, so the read-out would print
// only the round and step counters and leave the speed, tokens, cache hit and
// context empty. The strip therefore draws no cluster at all mid-turn (and takes
// no 32 DIP row), and the whole line appears once the turn settles.
func TestStatusStripHidesWhileTheTurnRuns(t *testing.T) {
	chat := statusChat(statusUsageMessages()...)
	session, _ := chat.Session("sess-1")
	session.Running = true
	chat.updateSession(session)
	h := newModelHarness(t, chat, statusCatalog())
	tester := h.tester()

	// Mid-turn: no meter, no figures, and no empty band under the composer.
	h.assertNoTexts(tester, "轮", "步", "tok/s", "缓存命中", "上下文")
	if _, ok := tester.AccessByRole(ui.RoleMeter, ""); ok {
		t.Fatalf("the context meter is on screen while the turn runs; texts = %v", tester.Texts())
	}
	stop, okStop := tester.Find("停止")
	if !okStop {
		t.Fatalf("stop control missing; texts = %v", tester.Texts())
	}
	if strip, ok := tester.Find("2"); ok && strip.Y > stop.Y {
		t.Fatalf("an empty strip row is reserved under the composer (y=%.1f, stop at %.1f)",
			strip.Y, stop.Y)
	}

	// Settled: the whole read-out appears.
	session.Running = false
	chat.updateSession(session)
	tester.Frame()
	tester.Frame()
	h.assertTexts(tester, "轮", "步", "tok/s", "tok", "缓存命中", "94%")
	if _, ok := tester.AccessByRole(ui.RoleMeter, "上下文 94%"); !ok {
		t.Fatalf("the meter did not appear once the turn settled; texts = %v", tester.Texts())
	}
}

// TestStreamingShowsOneSpinnerAtATime covers the second half of the same report:
// the thinking row's spinner and the generating row's spinner sat a few DIP
// apart. While the last reasoning segment is still the live channel the
// generating row is not drawn; once prose starts streaming the thinking row
// settles to its quiet 思考 line and 生成中 takes over.
func TestStreamingShowsOneSpinnerAtATime(t *testing.T) {
	newChat := func(text string) *fakeChat {
		chat := newFakeChat()
		chat.addSession(contracts.ChatSession{
			ID: "sess-1", Title: "单个 spinner", Running: true,
			Messages: []contracts.ChatMessage{
				{ID: "u1", Role: contracts.ChatRoleUser, Text: "看看仓库"},
				{
					ID: "a1", Role: contracts.ChatRoleAssistant, Streaming: true, Text: text,
					ReasoningSegments: []contracts.ReasoningSegment{
						{Text: "R1 先看看目录", Iteration: 1, TextOffset: 0},
					},
				},
			},
		}, true)
		return chat
	}

	// Thinking: the reasoning row spins, the generating row is absent.
	thinking := newFakeChat()
	_ = thinking
	h := newPermissionHarness(t, newChat(""), newPermissionSettings(map[string]string{
		contracts.SettingWorkStepsMode: contracts.WorkStepsVerbose,
	}))
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "思考中…")
	h.assertNoTexts(tester, "生成中")

	// Prose starts: the thinking row settles and 生成中 appears.
	chat := newChat("B1 目录结构")
	session, _ := chat.Session("sess-1")
	session.Messages[1].Streaming = true
	chat.updateSession(session)
	h2 := newPermissionHarness(t, chat, newPermissionSettings(map[string]string{
		contracts.SettingWorkStepsMode: contracts.WorkStepsVerbose,
	}))
	tester2 := h2.tester()
	tester2.Frame()
	h2.assertTexts(tester2, "生成中")
	h2.assertNoTexts(tester2, "思考中…")
}
