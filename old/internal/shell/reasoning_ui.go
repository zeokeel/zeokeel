package shell

// reasoning_ui.go renders the Chat transcript's reasoning disclosure row: the
// DSH ReasoningRow equivalent. Each turn with reasoning shows one 24 DIP row
// above its process block:
//
//	[›] Think · Let me inspect the renderer
//	[›] Thought for a while
//
// While the reasoning is still being written the title is the running label and
// the collapsed row carries a one-line live preview (the last completed
// paragraph's first line, clipped with an ellipsis). Once answer text arrives
// (or the turn ends) the title settles to the completed label and the preview
// is hidden; the row stays collapsed by default so completed turns keep a
// compact line without hiding the final answer. Clicking the row (or pressing
// Enter/Space while it has the focus) expands the full reasoning body below it;
// the expanded state is remembered per assistant message in liveMenus, so it
// survives frames, tab switches and the turn ending.
//
// Shimmer: mygo has no CSS mask, so the running preview animates through the
// opacity/color fallback the task asks for. c.AnimationFrame keeps the frame
// clock running while the row is in its running state; the pulse is derived
// from the frame time, which makes the animation deterministic for ui.NewTester
// frames. Text always stays present in the accessibility tree and the frame's
// text list, so tests can assert the live preview regardless of the pulse.

import (
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
	"github.com/zeokeel/zeokeel/old/internal/icons"
	"github.com/zeokeel/zeokeel/old/internal/state"
)

const (
	// reasoningRowHeight is the collapsed row's fixed height in DIPs, matching
	// DSH's `contain: size layout; height: 24px` collapsed ReasoningRow.
	reasoningRowHeight = 24
	// reasoningBodyIndent lines the expanded body up under the disclosure
	// title (DSH pads the body 22px from the leading edge).
	reasoningBodyIndent = 22
)

// reasoningParagraphBreak matches a blank-line run between reasoning
// paragraphs, as DSH's ReasoningRow does: one newline followed by one or more
// whitespace-only lines.
var reasoningParagraphBreak = regexp.MustCompile(`\n(?:[\t ]*\n)+`)

// reasoningPreview returns the collapsed row's one-line preview: the first line
// of the last completed paragraph of the reasoning. A paragraph counts as
// completed once a blank line follows it (or once its first line is terminated
// by a newline). While the first paragraph is still being typed the row falls
// back to the first line being written, so a streaming row always has a live
// preview. The result never contains a newline.
func reasoningPreview(text string) string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	if strings.TrimSpace(normalized) == "" {
		return ""
	}
	matches := reasoningParagraphBreak.FindAllStringIndex(normalized, -1)
	summary := ""
	paragraphStart := 0
	for i := 0; ; i++ {
		paragraphEnd := len(normalized)
		nextStart := -1
		if i < len(matches) {
			paragraphEnd = matches[i][0]
			nextStart = matches[i][1]
		}
		if newline := strings.Index(normalized[paragraphStart:], "\n"); newline >= 0 &&
			paragraphStart+newline <= paragraphEnd {
			if candidate := strings.TrimSpace(normalized[paragraphStart : paragraphStart+newline]); candidate != "" {
				summary = candidate
			}
		}
		if nextStart < 0 {
			break
		}
		paragraphStart = nextStart
	}
	if summary == "" {
		// The first paragraph is still one unterminated line: show it live.
		for _, line := range strings.Split(normalized, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				return line
			}
		}
	}
	return summary
}

// reasoningPulse returns the running preview's opacity at now: a smooth
// sine pulse on the design motion scale, the dependency-free fallback for a
// left-to-right text shimmer the shell cannot express without a CSS mask.
func reasoningPulse(now time.Time) float32 {
	cycle := int64(design.DurationPulse)
	if cycle <= 0 {
		return 1
	}
	position := float64(now.UnixNano()%cycle) / float64(cycle)
	wave := 0.5 + 0.5*math.Sin(2*math.Pi*position)
	return float32(0.55 + 0.45*wave)
}

// reasoningTextSignature is the O(1) change detector for one reasoning text. A
// live reasoning text only ever grows (a reasoning delta appends), so its
// length plus a hash of a bounded prefix and suffix identifies a change
// without scanning or copying the whole text on every frame. The 128 sampled
// bytes are cheap to hash; equal-length texts that differ only in their middle
// and share both sampled windows are the only false negatives, and a later
// delta changes the length and refreshes the preview.
type reasoningTextSignature struct {
	length int
	sample uint64
}

// reasoningSignature computes the cheap signature of text.
func reasoningSignature(text string) reasoningTextSignature {
	const fnvOffset = 14695981039346656037
	const fnvPrime = 1099511628211
	hash := uint64(fnvOffset)
	feed := func(b byte) { hash = (hash ^ uint64(b)) * fnvPrime }
	const window = 64
	if len(text) <= 2*window {
		for i := 0; i < len(text); i++ {
			feed(text[i])
		}
		return reasoningTextSignature{length: len(text), sample: hash}
	}
	for i := 0; i < window; i++ {
		feed(text[i])
	}
	for i := len(text) - window; i < len(text); i++ {
		feed(text[i])
	}
	return reasoningTextSignature{length: len(text), sample: hash}
}

// reasoningPreviewEntry is one cached disclosure preview.
type reasoningPreviewEntry struct {
	signature reasoningTextSignature
	trimmed   string
	preview   string
	// animateUntil bounds the pulse window: a frame is only requested while
	// the running preview is still animating, not for the whole turn.
	animateUntil time.Time
}

// reasoningPreviewCache memoizes the collapsed preview of the transcript's
// reasoning rows for one App. The view rebuilds every frame, but this cache
// makes the reasoning part of that rebuild O(1): an unchanged text is identified
// by its signature and its cached trimmed text and preview are reused, so a
// 100 KiB thought is not rescanned at 60fps.
type reasoningPreviewCache struct {
	entries         map[string]reasoningPreviewEntry
	previewComputes int
}

// reasoningFrameCaches maps each App to its reasoning preview cache, keyed the
// same way motion.go and home_live_menus.go key their per-App state.
var reasoningFrameCaches sync.Map // map[*App]*reasoningPreviewCache

// reasoningFrameCache returns the reasoning preview cache of a, making it on
// first use.
func (a *App) reasoningFrameCache() *reasoningPreviewCache {
	if cached, ok := reasoningFrameCaches.Load(a); ok {
		return cached.(*reasoningPreviewCache)
	}
	cache := &reasoningPreviewCache{entries: make(map[string]reasoningPreviewEntry)}
	actual, _ := reasoningFrameCaches.LoadOrStore(a, cache)
	return actual.(*reasoningPreviewCache)
}

// preview returns the cached trimmed text, collapsed preview and pulse deadline
// of one disclosure. trimmed/preview are recomputed only when the text changes;
// the unchanged path compares a signature and returns the cached strings, which
// allocates nothing and copies nothing.
func (c *reasoningPreviewCache) preview(key, text string, now time.Time) (trimmed, preview string, animateUntil time.Time) {
	signature := reasoningSignature(text)
	if entry, ok := c.entries[key]; ok && entry.signature == signature {
		return entry.trimmed, entry.preview, entry.animateUntil
	}
	// Keep a bounded map: transcript keys are per message, so this only trims
	// state a very long-lived App accumulated across many sessions.
	if len(c.entries) >= 1024 {
		clear(c.entries)
	}
	trimmed = strings.TrimSpace(text)
	preview = reasoningPreview(trimmed)
	animateUntil = now.Add(design.DurationPulse)
	c.previewComputes++
	c.entries[key] = reasoningPreviewEntry{signature: signature, trimmed: trimmed, preview: preview, animateUntil: animateUntil}
	return trimmed, preview, animateUntil
}

// reasoningTurnEntry is the memoized aggregation of one turn plan's reasoning.
type reasoningTurnEntry struct {
	text          string
	key           string
	hasText       bool
	visibleAnswer bool
}

// reasoningTurnMemo caches per-turn reasoning aggregation for the snapshot the
// view is currently rendering. Snapshots are immutable, so an identical
// snapshot pointer means identical messages and the joined reasoning text can
// be reused instead of re-joining (and re-trimming) every assistant message on
// every frame.
type reasoningTurnMemo struct {
	mu       sync.Mutex
	snapshot *state.Snapshot
	entries  map[int]reasoningTurnEntry
	computes int
}

// reasoningTurns is the package's turn aggregation memo. Only the view
// goroutine writes entries; the mutex keeps a second App rendered from a test
// (or an embedder) safe.
var reasoningTurns = &reasoningTurnMemo{entries: make(map[int]reasoningTurnEntry)}

// computesCount returns how many times a turn's reasoning text was aggregated.
func (m *reasoningTurnMemo) computesCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.computes
}

// reasoningFrameStats is a small test/diagnostic view of the reasoning cache
// work: how many preview and turn-text computations happened.
type reasoningFrameStats struct {
	previewComputes int
	turnComputes    int
}

// reasoningStats reports the reasoning cache work done so far for this App.
func (a *App) reasoningStats() reasoningFrameStats {
	return reasoningFrameStats{
		previewComputes: a.reasoningFrameCache().previewComputes,
		turnComputes:    reasoningTurns.computesCount(),
	}
}

// turnReasoning returns the reasoning disclosure content for one turn plan: the
// turn's assistant reasoning text (messages may be split by the snapshot's
// step projection, so several are joined), the liveMenus key of the
// disclosure, and whether the reasoning is still being written.
//
// running is true only for the last turn of a running snapshot whose answer has
// not started streaming yet: once a visible assistant token exists the
// reasoning step is over, so the row shows the completed label while the turn
// continues.
func turnReasoning(snap *state.Snapshot, plan turnPlan, lastTurn bool) (text, key string, running bool) {
	if snap == nil {
		return "", "", false
	}
	memo := reasoningTurns
	memo.mu.Lock()
	defer memo.mu.Unlock()
	if memo.snapshot != snap {
		memo.snapshot = snap
		clear(memo.entries)
	}
	planKey := -1
	if len(plan.entries) > 0 {
		planKey = plan.entries[0].msgIndex
	}
	if entry, ok := memo.entries[planKey]; ok {
		running = entry.hasText && lastTurn && snap.Phase == state.PhaseRunning && !entry.visibleAnswer
		return entry.text, entry.key, running
	}

	msgs := snap.Messages
	start, end := 0, len(msgs)
	userIdx := -1
	for _, entry := range plan.entries {
		if entry.msgIndex >= 0 && entry.msgIndex < len(msgs) && msgs[entry.msgIndex].Role == state.RoleUser {
			userIdx = entry.msgIndex
			break
		}
	}
	if userIdx >= 0 {
		start = userIdx + 1
		for i := start; i < len(msgs); i++ {
			if msgs[i].Role == state.RoleUser {
				end = i
				break
			}
		}
	} else {
		// The preamble plan owns the messages before the first user message.
		for i := range msgs {
			if msgs[i].Role == state.RoleUser {
				end = i
				break
			}
		}
	}
	parts := make([]string, 0, 1)
	visibleAnswer := false
	for i := start; i < end; i++ {
		msg := msgs[i]
		if msg.Role != state.RoleAssistant {
			continue
		}
		if strings.TrimSpace(msg.Text) != "" {
			visibleAnswer = true
		}
		if reasoning := strings.TrimSpace(msg.Reasoning); reasoning != "" {
			parts = append(parts, reasoning)
			if key == "" {
				key = transcriptMessageKey(msg, i)
			}
		}
	}
	if len(parts) == 0 {
		memo.entries[planKey] = reasoningTurnEntry{}
		return "", "", false
	}
	text = strings.Join(parts, "\n\n")
	running = lastTurn && snap.Phase == state.PhaseRunning && !visibleAnswer
	memo.computes++
	memo.entries[planKey] = reasoningTurnEntry{
		text: text, key: key, hasText: true, visibleAnswer: visibleAnswer,
	}
	return text, key, running
}

// liveReasoningRow draws one reasoning disclosure row (collapsed) and, when it
// is open, the turn's full reasoning body below it. The state is kept in
// liveMenus under the disclosure key so it survives redraws and the turn
// ending; the row is a native ButtonBase, so the pointer and Enter/Space toggle
// it and assistive technology reads it as an expandable control.
func (a *App) liveReasoningRow(c *ui.Context, t *design.Tokens, key, text string, running bool) {
	st := a.liveMenus()
	if st.reasoningExpanded == nil {
		st.reasoningExpanded = make(map[string]bool)
	}
	expanded := st.reasoningExpanded[key]
	th := t.Theme()

	title := a.menuText("Think", "思考")
	if !running {
		title = a.menuText("Thought for a while", "已思考")
	}
	accessible := title
	if running {
		accessible += " · " + a.menuText("thinking", "思考中")
	}

	// The cached trimmed text and collapsed preview: unchanged reasoning costs a
	// signature comparison here, not a rescan of the whole thought.
	cache := a.reasoningFrameCache()
	now := c.Now()
	trimmed, preview, animateUntil := cache.preview(key, text, now)
	if trimmed == "" {
		return
	}
	// The pulse is decorative and bounded: it runs only for this row's pulse
	// window after a delta (never for the whole turn), and reduce-motion users
	// get the steady text without the frame clock.
	pulseRunning := running && !expanded && !c.Preferences().ReduceMotion && now.Before(animateUntil)
	pulse := float32(1)
	if pulseRunning {
		pulse = reasoningPulse(now)
	}

	// One disclosure container: the toggle row and (when open) the body are a
	// single transcript child, so the body participates in the turn's flow
	// instead of being a floating sibling between two keyed messages.
	ui.Column(c).Key("reasoning:" + key).Children(func() {
		row := ui.ButtonBase(c).
			Height(float32(reasoningRowHeight)).
			PaddingX(th.Space(1)).
			Gap(th.Space(1)).
			Radius(design.RadiusSM).
			AlignItems(ui.Center).
			Justify(ui.Start).
			Label(accessible).
			Expanded(expanded)
		if row.Hovered() {
			row = row.Background(t.BgHover)
		}
		row.Children(func() {
			caret := icons.ChevronRight()
			if expanded {
				caret = icons.ChevronDown()
			}
			ui.Icon(c, caret).Size(12, 12).TextColor(t.LabelTertiary)
			ui.Text(c, title).FontSize(13).FixedLineHeight(20).
				TextColor(t.LabelSecondary).NoWrap()
			if expanded || !running {
				return
			}
			if preview == "" {
				return
			}
			ui.Text(c, "·").FontSize(13).FixedLineHeight(20).
				TextColor(t.LabelTertiary).NoWrap()
			// The color pulse is the no-CSS-mask shimmer fallback: the text
			// always stays on screen and in the accessibility tree, the
			// opacity alone breathes on the motion scale.
			ui.Text(c, preview).FontSize(13).FixedLineHeight(20).
				TextColor(t.LabelTertiary).
				Opacity(pulse).
				NoWrap().MaxLines(1).Ellipsis("…").Grow(1).
				ContentTransition(ui.ContentTransition{Animation: &design.AnimFast})
		})
		if row.Clicked() {
			st.reasoningExpanded[key] = !expanded
			expanded = !expanded
		}
		if expanded {
			ui.Column(c).Key("reasoning-body:"+key).
				Transition(design.CollapseTransition()).
				Padding(th.Space(1), 0, th.Space(1), reasoningBodyIndent).
				Children(func() {
					body := ui.Text(c, trimmed)
					design.Apply(body, t.MonoFamily()).
						FontSize(12).FixedLineHeight(18).
						TextColor(t.LabelSecondary)
				})
		}
	})

	// Keep the frame clock running so the running preview keeps pulsing while
	// the snapshot itself changes only when a reasoning delta arrives.
	if pulseRunning {
		c.AnimationFrame()
	}
}
