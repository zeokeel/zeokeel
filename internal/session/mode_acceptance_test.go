package session_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
)

// modeToolRecordingProvider records the model-visible tool names of every
// request and answers with plain text, so a test can prove the first model call
// of a turn used the target session's mode topology.
type modeToolRecordingProvider struct {
	mu    sync.Mutex
	tools [][]string
}

func (p *modeToolRecordingProvider) Stream(_ context.Context, req runtime.Request) (<-chan runtime.Chunk, error) {
	names := make([]string, 0, len(req.Tools))
	for _, tool := range req.Tools {
		names = append(names, tool.Name)
	}
	p.mu.Lock()
	p.tools = append(p.tools, names)
	p.mu.Unlock()

	out := make(chan runtime.Chunk, 1)
	out <- runtime.Chunk{Text: "ok", Done: true}
	close(out)
	return out, nil
}

func (p *modeToolRecordingProvider) firstTools() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.tools) == 0 {
		return nil
	}
	return append([]string(nil), p.tools[0]...)
}

// testRequireModeToolSet asserts that names contains exactly want, ignoring
// order, so a mode cannot silently leak an extra tool.
func testRequireModeToolSet(t *testing.T, label string, names, want []string) {
	t.Helper()
	if len(names) != len(want) {
		t.Fatalf("%s tools = %v, want exactly %v", label, names, want)
	}
	wanted := make(map[string]bool, len(want))
	for _, name := range want {
		wanted[name] = true
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if !wanted[name] {
			t.Fatalf("%s tools = %v, unexpected %q (want exactly %v)", label, names, name, want)
		}
		if seen[name] {
			t.Fatalf("%s tools = %v, duplicate %q", label, names, name)
		}
		seen[name] = true
	}
}

// TestModePersistRebuild proves the mode is persisted per session, and that
// selecting it rebuilds the shared agent with that preset's exact tool set.
// Switching to another session restores the first session's persisted mode and
// its tools; opening it again from a fresh current session does the same.
func TestModePersistRebuild(t *testing.T) {
	h := newModeHarness(t, &echoOnlyProvider{}, func(reg *runtime.Registry) {
		for _, name := range []string{
			"bash", "read_file", "terminal_open", "terminal_list", "terminal_read",
			"terminal_send", "terminal_signal", "terminal_close", "terminal_debug",
			"read_image", "working_directory", "settings_get", "settings_set",
		} {
			if err := reg.Register(&modeNamedTool{name: name}); err != nil {
				t.Fatalf("register %s: %v", name, err)
			}
		}
	})
	h.ctrl.SetAgentModeSwitcher(h.agent.SetMode)
	ctx := context.Background()
	first := h.sessionID
	firstKey := session.SessionModeKey(first)

	minimalTools := []string{
		"terminal_open", "terminal_list", "terminal_read", "terminal_send",
		"terminal_signal", "terminal_close", "read_image", "working_directory",
	}
	assertCurrent := func(label, key, wantMode string, wantTools []string) {
		t.Helper()
		if got := h.ctrl.Mode(); got != wantMode {
			t.Fatalf("%s Ctrl.Mode() = %q, want %q", label, got, wantMode)
		}
		if got := h.agent.Mode(); got != wantMode {
			t.Fatalf("%s Agent.Mode() = %q, want %q", label, got, wantMode)
		}
		if stored, err := h.store.GetSetting(key); err != nil || stored != wantMode {
			t.Fatalf("%s stored mode = %q, %v; want %q", label, stored, err, wantMode)
		}
		testRequireModeToolSet(t, label, h.agent.ToolNames(), wantTools)
	}

	// Minimal mode persists and rebuilds the exact terminal/image/workdir set.
	if err := h.ctrl.SetMode(runtime.ModeMinimal); err != nil {
		t.Fatalf("SetMode(minimal): %v", err)
	}
	assertCurrent("minimal", firstKey, runtime.ModeMinimal, minimalTools)

	// A second session starts Standard (no persisted default), and its own
	// mode switch must not rewrite the first session's persisted mode.
	second, err := h.ctrl.CreateSession(ctx, "second")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got := h.ctrl.Mode(); got != runtime.ModeStandard {
		t.Fatalf("new session Mode() = %q, want standard", got)
	}
	if got := h.agent.Mode(); got != runtime.ModeStandard {
		t.Fatalf("new session Agent.Mode() = %q, want standard", got)
	}
	secondKey := session.SessionModeKey(second)
	if err := h.ctrl.SetMode(runtime.ModeCreator); err != nil {
		t.Fatalf("SetMode(creator): %v", err)
	}
	if stored, err := h.store.GetSetting(secondKey); err != nil || stored != runtime.ModeCreator {
		t.Fatalf("second session stored mode = %q, %v; want creator", stored, err)
	}
	if stored, err := h.store.GetSetting(firstKey); err != nil || stored != runtime.ModeMinimal {
		t.Fatalf("first session stored mode changed to %q, %v; want minimal", stored, err)
	}
	if !sessionHasTool(h.agent.ToolNames(), "settings_set") {
		t.Fatalf("Creator session tools = %v, want the Creator settings pair", h.agent.ToolNames())
	}

	// Reopening the first session restores Minimal and rebuilds its tool set.
	if _, err := h.ctrl.OpenSession(ctx, first); err != nil {
		t.Fatalf("OpenSession(first): %v", err)
	}
	assertCurrent("reopened minimal", firstKey, runtime.ModeMinimal, minimalTools)

	// Reopening the second session restores Creator and its settings tools.
	if _, err := h.ctrl.OpenSession(ctx, second); err != nil {
		t.Fatalf("OpenSession(second): %v", err)
	}
	if got := h.ctrl.Mode(); got != runtime.ModeCreator {
		t.Fatalf("reopened second Ctrl.Mode() = %q, want creator", got)
	}
	if got := h.agent.Mode(); got != runtime.ModeCreator {
		t.Fatalf("reopened second Agent.Mode() = %q, want creator", got)
	}
	if !sessionHasTool(h.agent.ToolNames(), "settings_set") {
		t.Fatalf("reopened Creator tools = %v, want the settings pair", h.agent.ToolNames())
	}
}

// TestSubmitAppliesTargetSessionMode is the regression for a direct Submit to a
// non-current session: reserve made the target current, but Submit never
// applied its mode, so the shared agent ran the previously viewed session's
// topology while Ctrl.Mode() reported the target's. Submit must switch the
// agent (and therefore the first model call's tool set) before Agent.Run.
func TestSubmitAppliesTargetSessionMode(t *testing.T) {
	provider := &modeToolRecordingProvider{}
	h := newModeHarness(t, provider, func(reg *runtime.Registry) {
		for _, name := range []string{"bash", "read_file", "settings_get", "settings_set"} {
			if err := reg.Register(&modeNamedTool{name: name}); err != nil {
				t.Fatalf("register %s: %v", name, err)
			}
		}
	})
	h.ctrl.SetAgentModeSwitcher(h.agent.SetMode)
	ctx := context.Background()

	standardID := h.sessionID
	creatorID, err := h.ctrl.CreateSession(ctx, "creator")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.SetMode(runtime.ModeCreator); err != nil {
		t.Fatalf("SetMode(creator): %v", err)
	}

	// Look at the Standard session again so the shared agent is Standard while
	// the Submit targets the Creator session.
	if _, err := h.ctrl.OpenSession(ctx, standardID); err != nil {
		t.Fatalf("OpenSession(standard): %v", err)
	}
	if got := h.ctrl.Mode(); got != runtime.ModeStandard {
		t.Fatalf("Ctrl.Mode() before submit = %q, want standard", got)
	}
	if got := h.agent.Mode(); got != runtime.ModeStandard {
		t.Fatalf("Agent.Mode() before submit = %q, want standard", got)
	}

	if err := h.ctrl.Submit(ctx, creatorID, "hello"); err != nil {
		t.Fatalf("Submit(creator): %v", err)
	}
	// Submit returns only after the mode switch, so the chip, the runtime and
	// the model call cannot disagree.
	if got := h.ctrl.Mode(); got != runtime.ModeCreator {
		t.Fatalf("Ctrl.Mode() after submit = %q, want creator", got)
	}
	if got := h.agent.Mode(); got != runtime.ModeCreator {
		t.Fatalf("Agent.Mode() after submit = %q, want creator", got)
	}

	waitControllerIdle(t, h.ctrl)
	if got := h.ctrl.CurrentSessionID(); got != creatorID {
		t.Fatalf("current session after submit = %q, want %q", got, creatorID)
	}
	testRequireModeToolSet(t, "first model call", provider.firstTools(), []string{"bash", "read_file", "settings_get", "settings_set"})
}

// blockingBashProvider holds the first model call of a turn until the test
// releases it, then requests the "bash" tool and answers with text on the
// follow-up call. It records every request's tool names.
type blockingBashProvider struct {
	started chan struct{}
	release chan struct{}

	mu    sync.Mutex
	calls int
	tools [][]string
}

func newBlockingBashProvider() *blockingBashProvider {
	return &blockingBashProvider{started: make(chan struct{}), release: make(chan struct{})}
}

func (p *blockingBashProvider) Stream(ctx context.Context, req runtime.Request) (<-chan runtime.Chunk, error) {
	names := make([]string, 0, len(req.Tools))
	for _, tool := range req.Tools {
		names = append(names, tool.Name)
	}
	p.mu.Lock()
	call := p.calls
	p.calls++
	p.tools = append(p.tools, names)
	p.mu.Unlock()

	var chunks []runtime.Chunk
	if call == 0 {
		close(p.started)
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		chunks = []runtime.Chunk{{
			ToolCalls: []runtime.ToolCall{{
				ID:        "call-bash",
				Name:      "bash",
				Arguments: json.RawMessage(`{"command":"echo ok"}`),
			}},
			Done: true,
		}}
	} else {
		chunks = []runtime.Chunk{{Text: "turn complete", Done: true}}
	}
	out := make(chan runtime.Chunk, len(chunks))
	go func() {
		defer close(out)
		for _, c := range chunks {
			select {
			case out <- c:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (p *blockingBashProvider) toolsAt(i int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.tools) {
		return nil
	}
	return append([]string(nil), p.tools[i]...)
}

// TestModeSwitchDuringSubmitKeepsPinnedTools is the controller-level regression
// for a narrowing mode switch while a session turn is in flight. The Standard
// turn blocks inside the provider before its bash call; SetMode(Minimal) then
// rebuilds the shared agent. The in-flight turn must keep validating against
// (and running) the topology it pinned at Agent.Run time, so the bash call
// completes and the snapshot ends idle instead of failed with
// `model requested unknown tool "bash"`.
func TestModeSwitchDuringSubmitKeepsPinnedTools(t *testing.T) {
	provider := newBlockingBashProvider()
	h := newModeHarness(t, provider, func(reg *runtime.Registry) {
		for _, name := range []string{"bash", "terminal_open", "read_image", "working_directory"} {
			if err := reg.Register(&modeNamedTool{name: name}); err != nil {
				t.Fatalf("register %s: %v", name, err)
			}
		}
	})
	h.ctrl.SetAgentModeSwitcher(h.agent.SetMode)
	ctx := context.Background()

	if got := h.ctrl.Mode(); got != runtime.ModeStandard {
		t.Fatalf("initial Ctrl.Mode() = %q, want standard", got)
	}
	if err := h.ctrl.Submit(ctx, h.sessionID, "run bash"); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	select {
	case <-provider.started:
	case <-time.After(10 * time.Second):
		t.Fatal("provider never received the turn's first model call")
	}
	// The Standard turn is parked before its bash tool call. Narrow the shared
	// agent to Minimal, then let the call arrive.
	if err := h.ctrl.SetMode(runtime.ModeMinimal); err != nil {
		t.Fatalf("SetMode(minimal) during the turn: %v", err)
	}
	if got := h.agent.Mode(); got != runtime.ModeMinimal {
		t.Fatalf("Agent.Mode() after switch = %q, want minimal", got)
	}
	close(provider.release)
	waitControllerIdle(t, h.ctrl)

	snap := h.ctrl.Snapshot()
	if snap.LastError != nil {
		t.Fatalf("mid-turn mode switch failed the pinned turn: %s", snap.LastError.Text)
	}
	tool, ok := snap.Tools["call-bash"]
	if !ok {
		t.Fatalf("snapshot tools = %v, want the bash call the pinned turn ran", snap.Tools)
	}
	if tool.Name != "bash" || tool.IsError || tool.Status != state.ToolDone {
		t.Fatalf("bash tool state = %+v, want a successful done call", tool)
	}
	var text strings.Builder
	for _, msg := range snap.Messages {
		text.WriteString(msg.Text)
	}
	if !strings.Contains(text.String(), "turn complete") {
		t.Fatalf("snapshot messages = %q, want the post-tool answer", text.String())
	}
	// The blocked model call was the pinned Standard topology: it advertised
	// bash even though the live agent was switched while the call waited.
	if tools := provider.toolsAt(0); !sessionHasTool(tools, "bash") {
		t.Fatalf("first model call tools = %v, want the pinned Standard set", tools)
	}
}
