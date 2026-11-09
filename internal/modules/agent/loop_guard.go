package agent

// Loop hygiene: the advisory repeat-tool-call reminder.
//
// A model can get stuck calling the same tool with identical arguments without
// making progress (the real Windows session export shows one turn with 80 tool
// calls and six background jobs, several of them recreating the same test run).
// DeepSeek Harness solves this with @deepseek-ai/dsh-repeat-tool-reminder, an
// advisory guard enabled in the dsh base bundle with reminders at 3, 5 and 8
// consecutive identical calls. This file ports that loop-hygiene behavior to
// ZeoKeel's Rig tool seam:
//
//   - Detection is by exact (tool name, canonical arguments) identity: argument
//     JSON is parsed and re-marshaled so property order does not matter (the
//     same canonicalization DSH performs with JSON.parse + JSON.stringify).
//   - The repeat chain is per run and cleared by a new user message (every chat
//     run in ZeoKeel is one turn, so the guard is created per run). Bookkeeping
//     tools (todo_write/todo) are transparent: they neither count nor reset the
//     chain, exactly like DSH's exclude list.
//   - At each threshold the guard appends one short advisory reminder to the
//     repeated call's result. It never blocks, delays or fails a call: a
//     legitimate repeat keeps working, it just carries a nudge.
//
// Rig has no tools/post-execute waterfall to attach a synthetic user message to,
// so the reminder rides the repeated call's tool result instead of a separate
// message. That keeps the reminder ordered after the result it is about and
// visible to the next model round. Tool calls that end in a Go error (unknown
// tool, cancelled run) are rejected before the wrapper or have their result
// replaced by Rig, so those are not counted.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	rig "github.com/zeokeel/zeokeel-rig"
)

// repeatReminderThresholds are the consecutive-repeat counts that trigger a
// reminder. They mirror the dsh base bundle's [3, 5, 8] configuration: the
// first threshold is gentle, later ones name the tool and arguments.
var repeatReminderThresholds = []int{3, 5, 8}

// repeatReminderExcludedTools never participate in the repeat chain (nor reset
// it). They are pure bookkeeping calls a model legitimately repeats with the
// same arguments; DSH excludes its todo tool the same way.
var repeatReminderExcludedTools = map[string]bool{
	"todo_write": true,
	"todo":       true,
}

// repeatReminderPreviewChars bounds the argument text quoted in a detailed
// reminder, matching DSH's argumentsPreviewChars default.
const repeatReminderPreviewChars = 500

// repeatGentleReminder is the first-threshold nudge (DSH's GENTLE_REMINDER).
const repeatGentleReminder = "You are repeating the exact same tool call with identical arguments. " +
	"Carefully analyze the previous result before calling again: if the task is not complete, " +
	"try a different approach or different arguments instead of repeating the call."

// repeatDetailedReminder is the later-threshold nudge; it names the tool, the
// run length and the canonical arguments so the model can recognize the loop.
func repeatDetailedReminder(toolName string, count int, canonicalArguments string) string {
	return "Repeated tool call detected:\n" +
		"- tool: " + toolName + "\n" +
		fmt.Sprintf("- consecutive_calls: %d\n", count) +
		"- arguments: " + canonicalArguments + "\n" +
		"The repeated calls are not making progress. Do not call this tool with these exact arguments " +
		"again. Inspect the latest result and choose a different action, different arguments, or " +
		"finish the task if enough evidence has been gathered."
}

// repeatToolGuard tracks one run's consecutive repeat chain. It is safe for
// concurrent use because Rig may execute a round's tool calls in parallel; the
// chain then advances in completion order.
type repeatToolGuard struct {
	mu           sync.Mutex
	key          string
	count        int
	thresholds   []int
	thresholdSet map[int]bool
	excluded     map[string]bool
	previewChars int
}

// newRepeatToolGuard returns a guard with the product defaults (DSH's
// [3, 5, 8] thresholds, todo tools excluded, 500-char argument preview).
func newRepeatToolGuard() *repeatToolGuard {
	return newRepeatToolGuardFor(repeatReminderThresholds, repeatReminderExcludedTools, repeatReminderPreviewChars)
}

// newRepeatToolGuardFor builds a guard with explicit settings; it is the unit
// test seam for the thresholds and the exclusion list.
func newRepeatToolGuardFor(thresholds []int, excluded map[string]bool, previewChars int) *repeatToolGuard {
	guard := &repeatToolGuard{
		thresholds:   append([]int(nil), thresholds...),
		thresholdSet: make(map[int]bool, len(thresholds)),
		excluded:     excluded,
		previewChars: previewChars,
	}
	for _, threshold := range guard.thresholds {
		guard.thresholdSet[threshold] = true
	}
	return guard
}

// observe advances the chain for one tool call and returns the reminder text to
// append to its result, or "" when this call is not a threshold hit.
func (g *repeatToolGuard) observe(toolName, arguments string) string {
	if g == nil {
		return ""
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" || g.excluded[toolName] {
		// Untracked calls are transparent to the chain: they neither increment
		// nor reset the repeat count, so an interleaved todo_write does not
		// launder a loop.
		return ""
	}
	canonical := canonicalizeToolArguments(arguments)
	key := toolName + "\x00" + canonical

	g.mu.Lock()
	if g.key == key {
		g.count++
	} else {
		g.key = key
		g.count = 1
	}
	count := g.count
	g.mu.Unlock()

	if !g.thresholdSet[count] {
		return ""
	}
	if len(g.thresholds) > 0 && count == g.thresholds[0] {
		return repeatGentleReminder
	}
	return repeatDetailedReminder(toolName, count, previewToolArguments(canonical, g.previewChars))
}

// canonicalizeToolArguments returns a deterministic identity string for one
// call's raw argument JSON: parse and re-marshal, which sorts object keys at
// every level and normalizes numbers. Malformed (partial/streamed) arguments
// fall back to their trimmed raw text, matching DSH's raw-string fallback.
func canonicalizeToolArguments(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var value any
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return trimmed
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return trimmed
	}
	return string(canonical)
}

// previewToolArguments head-truncates canonical arguments for a reminder,
// marking exactly how much was omitted and never splitting a UTF-8 rune.
func previewToolArguments(canonical string, cap int) string {
	if cap <= 0 || len(canonical) <= cap {
		return canonical
	}
	cut := cap
	for cut > 0 && !utf8.RuneStart(canonical[cut]) {
		cut--
	}
	return canonical[:cut] + fmt.Sprintf("… (+%d more chars)", len(canonical)-cut)
}

// guardedTool is one run-scoped view of a tool: it forwards the model-visible
// metadata and the call itself, then appends the guard's reminder after a
// threshold hit. It is the only per-run wrapper the agent adds, so the tool
// surface the model sees is unchanged.
type guardedTool struct {
	rig.Tool
	guard *repeatToolGuard
}

// Invoke implements rig.Tool.
func (t *guardedTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	result, err := t.Tool.Invoke(ctx, call)
	reminder := t.guard.observe(t.Tool.Name(), call.Arguments)
	if reminder == "" {
		return result, err
	}
	if result.Content == "" {
		result.Content = reminder
	} else if strings.HasSuffix(result.Content, "\n") {
		result.Content += "\n" + reminder
	} else {
		result.Content += "\n\n" + reminder
	}
	return result, err
}

// noticeGuardedTool keeps a wrapped tool's optional rig.NoticeSource capability
// alive: the loop drains background-job completion notices from the tools a
// round called, so a wrapper must not hide the capability.
type noticeGuardedTool struct {
	*guardedTool
}

// PendingNotices implements rig.NoticeSource by delegating to the inner tool.
func (t *noticeGuardedTool) PendingNotices() []string {
	if source, ok := t.guardedTool.Tool.(rig.NoticeSource); ok {
		return source.PendingNotices()
	}
	return nil
}

// withRepeatGuard wraps every run tool with one repeat guard, preserving the
// optional NoticeSource capability. The guard is per run, so a new user message
// starts a fresh chain.
func withRepeatGuard(tools []rig.Tool, guard *repeatToolGuard) []rig.Tool {
	guarded := make([]rig.Tool, len(tools))
	for index, tool := range tools {
		wrapped := &guardedTool{Tool: tool, guard: guard}
		if _, ok := tool.(rig.NoticeSource); ok {
			guarded[index] = &noticeGuardedTool{guardedTool: wrapped}
			continue
		}
		guarded[index] = wrapped
	}
	return guarded
}
