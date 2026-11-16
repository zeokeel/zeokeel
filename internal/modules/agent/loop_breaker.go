package agent

// Hard loop breaker. The advisory repeat guard in loop_guard.go only appends a
// reminder; this guard cancels the run when a task crosses a wall-clock, token,
// exact-repeat or no-mutation budget. Limits are per run and configurable
// through settings; zero disables one limit.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	defaultLoopWallClock   = 30 * time.Minute
	defaultLoopTokenBudget = 4_000_000
	defaultLoopRepeatLimit = 5
	defaultLoopNoDiffCalls = 40
)

type runLoopGuardConfig struct {
	WallClock   time.Duration
	TokenBudget int
	RepeatLimit int
	NoDiffCalls int
}

type runLoopGuard struct {
	mu         sync.Mutex
	cancel     context.CancelFunc
	stopOnce   sync.Once
	stopCh     chan struct{}
	done       bool
	tripReason string

	cfg runLoopGuardConfig

	tokens  int
	lastKey string
	repeat  int
	noDiff  int
}

func newRunLoopGuard(cancel context.CancelFunc, cfg runLoopGuardConfig) *runLoopGuard {
	if cancel == nil {
		cancel = func() {}
	}
	return &runLoopGuard{cancel: cancel, stopCh: make(chan struct{}), cfg: cfg}
}

func (g *runLoopGuard) start() {
	if g == nil || g.cfg.WallClock <= 0 {
		return
	}
	go func() {
		timer := time.NewTimer(g.cfg.WallClock)
		defer timer.Stop()
		select {
		case <-timer.C:
			g.trip("wall clock budget exceeded")
		case <-g.stopCh:
		}
	}()
}

func (g *runLoopGuard) stop() {
	if g == nil {
		return
	}
	g.stopOnce.Do(func() { close(g.stopCh) })
}

func (g *runLoopGuard) trip(reason string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	if g.done {
		g.mu.Unlock()
		return
	}
	g.done = true
	g.tripReason = reason
	g.mu.Unlock()
	g.cancel()
}

func (g *runLoopGuard) tripped() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.done
}

func (g *runLoopGuard) reasonText() string {
	if g == nil {
		return ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tripReason
}

func (g *runLoopGuard) reason() string { return g.reasonText() }

func (g *runLoopGuard) err() error {
	if !g.tripped() {
		return nil
	}
	return &loopGuardError{reason: g.reasonText()}
}

// loopGuardError is classified as MessageErrorKindLoop by the product.
type loopGuardError struct{ reason string }

func (e *loopGuardError) Error() string {
	if e == nil || strings.TrimSpace(e.reason) == "" {
		return "loop guard: task budget exceeded"
	}
	return "loop guard: " + e.reason
}

func (g *runLoopGuard) observeUsage(total int) {
	if g == nil || total <= 0 || g.cfg.TokenBudget <= 0 {
		return
	}
	g.mu.Lock()
	g.tokens += total
	exceeded := g.tokens >= g.cfg.TokenBudget
	g.mu.Unlock()
	if exceeded {
		g.trip(fmt.Sprintf("token budget exceeded (%d/%d)", g.tokens, g.cfg.TokenBudget))
	}
}

// observeTool records one tool call. It returns false when this call tripped a
// limit (the caller must not invoke the tool).
func (g *runLoopGuard) observeTool(name, arguments string) bool {
	if g == nil || g.tripped() {
		return false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return true
	}
	canonical := canonicalizeToolArguments(arguments)
	key := name + "\x00" + canonical

	g.mu.Lock()
	if g.lastKey == key {
		g.repeat++
	} else {
		g.lastKey = key
		g.repeat = 1
	}
	repeat := g.repeat
	if isMutatingTool(name) {
		g.noDiff = 0
	} else if !repeatReminderExcludedTools[name] {
		g.noDiff++
	}
	noDiff := g.noDiff
	g.mu.Unlock()

	if g.cfg.RepeatLimit > 0 && repeat >= g.cfg.RepeatLimit {
		g.trip(fmt.Sprintf("identical tool call repeated %d times: %s", repeat, name))
		return false
	}
	if g.cfg.NoDiffCalls > 0 && noDiff >= g.cfg.NoDiffCalls {
		g.trip(fmt.Sprintf("no file diff/mutation after %d calls", noDiff))
		return false
	}
	return true
}

// isMutatingTool reports whether a tool can change workspace or app state. The
// no-diff guard counts consecutive calls outside this set.
func isMutatingTool(name string) bool {
	switch name {
	case "read_file", "list_dir", "glob", "grep", "read_image", "working_directory",
		"session_event_read", "session_event_search", "session_event_trace",
		"session_search", "session_trace", "web_fetch", "web_search", "skill":
		return false
	default:
		return true
	}
}

// loopGuardedTool cancels the run before invoking a tool once a limit is hit.
type loopGuardedTool struct {
	rig.Tool
	guard *runLoopGuard
}

func (t *loopGuardedTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	if !t.guard.observeTool(t.Tool.Name(), call.Arguments) {
		return rig.ToolResult{Content: t.guard.err().Error(), IsError: true}, nil
	}
	return t.Tool.Invoke(ctx, call)
}

type loopNoticeGuardedTool struct{ *loopGuardedTool }

func (t *loopNoticeGuardedTool) PendingNotices() []string {
	if source, ok := t.loopGuardedTool.Tool.(rig.NoticeSource); ok {
		return source.PendingNotices()
	}
	return nil
}

func withLoopGuard(tools []rig.Tool, guard *runLoopGuard) []rig.Tool {
	guarded := make([]rig.Tool, len(tools))
	for i, tool := range tools {
		wrapped := &loopGuardedTool{Tool: tool, guard: guard}
		if _, ok := tool.(rig.NoticeSource); ok {
			guarded[i] = &loopNoticeGuardedTool{loopGuardedTool: wrapped}
			continue
		}
		guarded[i] = wrapped
	}
	return guarded
}

// loopGuardConfig resolves the per-run limits from settings. Explicit zero
// disables a limit; absent/malformed rows keep the product defaults.
func (s *service) loopGuardConfig() runLoopGuardConfig {
	wallSeconds := s.settingInt(contracts.SettingAgentLoopWallClockSeconds, int(defaultLoopWallClock/time.Second), 0)
	return runLoopGuardConfig{
		WallClock:   time.Duration(wallSeconds) * time.Second,
		TokenBudget: s.settingInt(contracts.SettingAgentLoopTokenBudget, defaultLoopTokenBudget, 0),
		RepeatLimit: s.settingInt(contracts.SettingAgentLoopRepeatLimit, defaultLoopRepeatLimit, 0),
		NoDiffCalls: s.settingInt(contracts.SettingAgentLoopNoDiffCalls, defaultLoopNoDiffCalls, 0),
	}
}
