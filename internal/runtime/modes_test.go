package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/zeokeel/zeokeel/internal/state"
)

// scriptState is the shared call counter and tool log of one scripted model.
type scriptState struct {
	mu    sync.Mutex
	calls int
	tools [][]string
}

func (s *scriptState) record(tools []string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	call := s.calls
	s.calls++
	s.tools = append(s.tools, tools)
	return call
}

func (s *scriptState) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *scriptState) toolsAt(i int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i < 0 || i >= len(s.tools) {
		return nil
	}
	return s.tools[i]
}

// scriptModel is a deterministic tool-calling eino chat model for the mode and
// subagent tests. WithTools returns a copy that records the bound tools, so the
// planner/replanner (which bind tools up front) are observable too.
type scriptModel struct {
	state  *scriptState
	bound  []string
	script func(call int) ([]*schema.Message, error)
}

var (
	_ model.BaseModel[*schema.Message] = (*scriptModel)(nil)
	_ model.ToolCallingChatModel       = (*scriptModel)(nil)
	_ model.BaseChatModel              = (*scriptModel)(nil)
)

func newScriptModel(script func(call int) ([]*schema.Message, error)) *scriptModel {
	return &scriptModel{state: &scriptState{}, script: script}
}

func (m *scriptModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	stream, err := m.Stream(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	var chunks []*schema.Message
	for {
		msg, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			return nil, recvErr
		}
		if msg != nil {
			chunks = append(chunks, msg)
		}
	}
	if len(chunks) == 0 {
		return schema.AssistantMessage("", nil), nil
	}
	return schema.ConcatMessages(chunks)
}

func (m *scriptModel) Stream(ctx context.Context, _ []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	common := model.GetCommonOptions(nil, opts...)
	tools := append([]string(nil), m.bound...)
	for _, info := range common.Tools {
		if info != nil {
			tools = append(tools, info.Name)
		}
	}
	call := m.state.record(tools)
	chunks, err := m.script(call)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](8)
	go func() {
		defer sw.Close()
		for _, chunk := range chunks {
			if sw.Send(chunk, nil) {
				return
			}
		}
	}()
	_ = ctx
	return sr, nil
}

func (m *scriptModel) BindTools(_ []*schema.ToolInfo) error { return nil }

func (m *scriptModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	names := make([]string, 0, len(tools))
	for _, info := range tools {
		if info != nil {
			names = append(names, info.Name)
		}
	}
	return &scriptModel{state: m.state, bound: names, script: m.script}, nil
}

// toolNamesAt returns the tools the model saw at call i.
func (m *scriptModel) toolNamesAt(i int) []string { return m.state.toolsAt(i) }

func hasTool(tools []string, name string) bool {
	for _, tool := range tools {
		if tool == name {
			return true
		}
	}
	return false
}

func finalText(text string) []*schema.Message {
	return []*schema.Message{{Role: schema.Assistant, Content: text}}
}

func toolCall(name, args string) []*schema.Message {
	return []*schema.Message{{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID:       "call-" + name,
			Type:     "function",
			Function: schema.FunctionCall{Name: name, Arguments: args},
		}},
	}}
}

// runModeTurn runs one turn and drains it.
func runModeTurn(t *testing.T, a *Agent, sessionID, text string) []state.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ch, err := a.Run(ctx, sessionID, text)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return collectEvents(t, ch)
}

func TestModesAndNormalizeMode(t *testing.T) {
	want := []string{ModeStandard, ModePTC, ModeMinimal, ModeCreator}
	got := Modes()
	if len(got) != len(want) {
		t.Fatalf("Modes() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Modes() = %v, want %v", got, want)
		}
	}
	for _, mode := range append([]string{""}, want...) {
		normalized, err := NormalizeMode(mode)
		if err != nil {
			t.Fatalf("NormalizeMode(%q): %v", mode, err)
		}
		if mode == "" && normalized != ModeStandard {
			t.Fatalf("NormalizeMode(\"\") = %q, want standard", normalized)
		}
	}
	if _, err := NormalizeMode("turbo"); err == nil {
		t.Fatal("NormalizeMode(turbo) = nil error, want failure")
	}
	// The legacy aliases stay accepted for saved sessions and are
	// case-insensitive and whitespace-tolerant.
	for alias, want := range map[string]string{
		" DEEP ":      ModeDeep,
		"SUPERVISOR":  ModeSupervisor,
		"planexecute": ModePlanExecute,
		"cordis":      ModeCreator,
		"ptc-mode":    ModePTC,
	} {
		if mode, err := NormalizeMode(alias); err != nil || mode != want {
			t.Fatalf("NormalizeMode(%q) = %q, %v; want %q", alias, mode, err, want)
		}
	}
}

func TestNewUnknownModeFails(t *testing.T) {
	_, err := New(Config{
		Provider: &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
			return []Chunk{{Text: "x", Done: true}}, nil
		}},
		Mode: "turbo",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown agent mode") {
		t.Fatalf("New unknown mode error = %v", err)
	}
}

func TestStandardModeUsesRegisteredTools(t *testing.T) {
	clock := &fakeClock{result: "now"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	m := newScriptModel(func(call int) ([]*schema.Message, error) {
		if call == 0 {
			return toolCall("clock", `{}`), nil
		}
		return finalText("it is now"), nil
	})
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg, Mode: ModeStandard})
	if agent.Mode() != ModeStandard {
		t.Fatalf("Mode() = %q, want standard", agent.Mode())
	}
	events := runModeTurn(t, agent, "standard", "time?")
	if clock.callCount() != 1 {
		t.Fatalf("clock calls = %d, want 1", clock.callCount())
	}
	if !strings.Contains(strings.Join(tokenTexts(events), ""), "it is now") {
		t.Fatalf("tokens = %v", tokenTexts(events))
	}
	if tools := m.toolNamesAt(0); !hasTool(tools, "clock") {
		t.Fatalf("standard tools = %v, want clock", tools)
	}
}

func TestDeepModeAddsPlanningAndTaskTools(t *testing.T) {
	clock := &fakeClock{result: "now"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	m := newScriptModel(func(int) ([]*schema.Message, error) { return finalText("deep done"), nil })
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg, Mode: ModeDeep})
	if agent.Mode() != ModeDeep {
		t.Fatalf("Mode() = %q, want deep", agent.Mode())
	}
	events := runModeTurn(t, agent, "deep", "plan something")
	if !strings.Contains(strings.Join(tokenTexts(events), ""), "deep done") {
		t.Fatalf("tokens = %v", tokenTexts(events))
	}
	tools := m.toolNamesAt(0)
	for _, want := range []string{"clock", "task", "write_todos"} {
		if !hasTool(tools, want) {
			t.Fatalf("deep tools = %v, want %q", tools, want)
		}
	}
}

func TestSupervisorModeExposesTransferTool(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&fakeClock{result: "now"}); err != nil {
		t.Fatal(err)
	}
	m := newScriptModel(func(int) ([]*schema.Message, error) { return finalText("supervisor done"), nil })
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg, Mode: ModeSupervisor})
	if agent.Mode() != ModeSupervisor {
		t.Fatalf("Mode() = %q, want supervisor", agent.Mode())
	}
	events := runModeTurn(t, agent, "supervisor", "delegate something")
	if !strings.Contains(strings.Join(tokenTexts(events), ""), "supervisor done") {
		t.Fatalf("tokens = %v", tokenTexts(events))
	}
	if tools := m.toolNamesAt(0); !hasTool(tools, "transfer_to_agent") {
		t.Fatalf("supervisor tools = %v, want transfer_to_agent", tools)
	}
}

func TestPlanExecuteModeRunsPlannerExecutorReplanner(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&fakeClock{result: "now"}); err != nil {
		t.Fatal(err)
	}
	m := newScriptModel(func(call int) ([]*schema.Message, error) {
		switch call {
		case 0:
			return toolCall("plan", `{"steps":["inspect the workspace"]}`), nil
		case 1:
			return finalText("inspected"), nil
		default:
			return toolCall("respond", `{"response":"all done"}`), nil
		}
	})
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg, Mode: ModePlanExecute})
	if agent.Mode() != ModePlanExecute {
		t.Fatalf("Mode() = %q, want planexecute", agent.Mode())
	}
	events := runModeTurn(t, agent, "planexec", "do the thing")
	if m.state.callCount() != 3 {
		t.Fatalf("model calls = %d, want 3 (planner, executor, replanner)", m.state.callCount())
	}
	if tools := m.toolNamesAt(0); !hasTool(tools, "plan") {
		t.Fatalf("planner tools = %v, want plan", tools)
	}
	if tools := m.toolNamesAt(1); !hasTool(tools, "clock") {
		t.Fatalf("executor tools = %v, want clock", tools)
	}
	if tools := m.toolNamesAt(2); !hasTool(tools, "respond") {
		t.Fatalf("replanner tools = %v, want respond", tools)
	}
	if joined := strings.Join(tokenTexts(events), ""); !strings.Contains(joined, "all done") {
		t.Fatalf("tokens = %q, want the replanner response", joined)
	}
	if phase := lastPhase(events); phase != state.PhaseIdle {
		t.Fatalf("final phase = %v, want idle", phase)
	}
}

func TestPlanExecuteRequiresToolCallingModel(t *testing.T) {
	plain := &fakeChatModel{script: func(context.Context, int, []*schema.Message) ([]*schema.Message, error) {
		return finalText("x"), nil
	}}
	_, err := New(Config{einoModel: plain, Mode: ModePlanExecute})
	if err == nil || !strings.Contains(err.Error(), "tool-calling") {
		t.Fatalf("New planexecute with plain model error = %v", err)
	}
}

func TestSetModeRebuildsTopology(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&fakeClock{result: "now"}); err != nil {
		t.Fatal(err)
	}
	m := newScriptModel(func(int) ([]*schema.Message, error) { return finalText("ok"), nil })
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg})

	if err := agent.SetMode(" DEEP "); err != nil {
		t.Fatalf("SetMode(deep): %v", err)
	}
	if agent.Mode() != ModeDeep {
		t.Fatalf("Mode() = %q, want deep", agent.Mode())
	}
	runModeTurn(t, agent, "switch", "hello")
	if tools := m.toolNamesAt(0); !hasTool(tools, "write_todos") {
		t.Fatalf("after SetMode(deep) tools = %v, want write_todos", tools)
	}

	if err := agent.SetMode("standard"); err != nil {
		t.Fatalf("SetMode(standard): %v", err)
	}
	runModeTurn(t, agent, "switch", "hello again")
	if tools := m.toolNamesAt(1); hasTool(tools, "write_todos") {
		t.Fatalf("after SetMode(standard) tools = %v, want no write_todos", tools)
	}
	if err := agent.SetMode("nope"); err == nil {
		t.Fatal("SetMode(nope) = nil, want error")
	}
	if agent.Mode() != ModeStandard {
		t.Fatalf("failed SetMode changed mode to %q", agent.Mode())
	}
}

// lastPhase returns the last status phase in events.
func lastPhase(events []state.Event) state.Phase {
	phase := state.Phase("")
	for _, ev := range events {
		if st, ok := ev.(state.Status); ok {
			phase = st.Phase
		}
	}
	return phase
}

// stubTool is a nameable runtime.Tool for the mode routing tests.
type stubTool struct {
	name   string
	invoke func(context.Context, json.RawMessage) (string, error)
}

func (t *stubTool) Name() string        { return t.name }
func (t *stubTool) Description() string { return "stub " + t.name }
func (t *stubTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if t.invoke != nil {
		return t.invoke(ctx, args)
	}
	return t.name + " ok", nil
}

// fakeSettings is a deterministic runtime.SettingsStore.
type fakeSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func (s *fakeSettings) GetSetting(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[key]
	if !ok {
		return "", errors.New("not found")
	}
	return value, nil
}

func (s *fakeSettings) SetSetting(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.values[key] = value
	return nil
}

func requireTools(t *testing.T, mode string, got []string, present, absent []string) {
	t.Helper()
	for _, name := range present {
		if !hasTool(got, name) {
			t.Fatalf("%s tools = %v, want %q", mode, got, name)
		}
	}
	for _, name := range absent {
		if hasTool(got, name) {
			t.Fatalf("%s tools = %v, do not want %q", mode, got, name)
		}
	}
}

func TestModeToolSets(t *testing.T) {
	reg := NewRegistry()
	for _, name := range []string{
		"bash", "read_file", "write_file", "terminal_open", "terminal_read",
		"read_image", "working_directory", "settings_get", "settings_set",
	} {
		if err := reg.Register(&stubTool{name: name}); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		mode    string
		present []string
		absent  []string
	}{
		{
			mode:    ModeStandard,
			present: []string{"bash", "read_file", "write_file", "terminal_open", "read_image", "working_directory"},
			absent:  []string{"ptc", "settings_get", "settings_set"},
		},
		{
			mode:    ModePTC,
			present: []string{"bash", "read_file", "write_file", "terminal_open", "read_image", "working_directory", "ptc"},
			absent:  []string{"settings_get", "settings_set"},
		},
		{
			mode:    ModeMinimal,
			present: []string{"terminal_open", "terminal_read", "read_image", "working_directory"},
			absent:  []string{"bash", "read_file", "write_file", "ptc", "settings_get", "settings_set"},
		},
		{
			mode:    ModeCreator,
			present: []string{"bash", "read_file", "write_file", "terminal_open", "read_image", "working_directory", "settings_get", "settings_set"},
			absent:  []string{"ptc"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			m := newScriptModel(func(int) ([]*schema.Message, error) { return finalText("done"), nil })
			agent := newTestAgent(t, Config{einoModel: m, Tools: reg, Mode: tc.mode})
			runModeTurn(t, agent, "mode-"+tc.mode, "hello")
			requireTools(t, tc.mode, m.toolNamesAt(0), tc.present, tc.absent)
			requireTools(t, tc.mode+" ToolNames()", agent.ToolNames(), tc.present, tc.absent)
		})
	}
}

func TestPTCExecutesBatchSequentially(t *testing.T) {
	var (
		mu    sync.Mutex
		order []string
	)
	reg := NewRegistry()
	if err := reg.Register(&stubTool{name: "echo", invoke: func(_ context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		mu.Lock()
		order = append(order, in.Text)
		mu.Unlock()
		return "echo:" + in.Text, nil
	}}); err != nil {
		t.Fatal(err)
	}

	m := newScriptModel(func(call int) ([]*schema.Message, error) {
		if call == 0 {
			return toolCall("ptc", `{"calls":[
				{"tool":"echo","args":{"text":"one"}},
				{"tool":"echo","args":{"text":"two"}},
				{"tool":"echo","args":"{\"text\":\"three\"}"}
			]}`), nil
		}
		return finalText("batch done"), nil
	})
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg, Mode: ModePTC})
	if _, ok := agent.ToolByName("ptc"); !ok {
		t.Fatalf("ModePTC effective tools = %v, want ptc", agent.ToolNames())
	}
	events := runModeTurn(t, agent, "ptc", "batch please")
	end, ok := findToolEnd(events, "call-ptc")
	if !ok {
		t.Fatalf("ptc tool end missing: %v", events)
	}
	if end.IsError {
		t.Fatalf("ptc result is an error: %s", end.Result)
	}
	var response PTCResponse
	if err := json.Unmarshal([]byte(end.Result), &response); err != nil {
		t.Fatalf("ptc result %q: %v", end.Result, err)
	}
	if len(response.Results) != 3 {
		t.Fatalf("ptc results = %+v, want 3", response.Results)
	}
	for i, want := range []string{"echo:one", "echo:two", "echo:three"} {
		if !response.Results[i].OK || response.Results[i].Output != want {
			t.Fatalf("ptc result[%d] = %+v, want %q", i, response.Results[i], want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(order, ",") != "one,two,three" {
		t.Fatalf("ptc call order = %v, want sequential one,two,three", order)
	}
}

func TestPTCBatchReportsUnknownAndRejectsNestedPTC(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&stubTool{name: "echo"}); err != nil {
		t.Fatal(err)
	}
	agent := newTestAgent(t, Config{
		einoModel: newScriptModel(func(int) ([]*schema.Message, error) { return finalText("x"), nil }),
		Tools:     reg,
		Mode:      ModePTC,
	})
	ptc, ok := agent.ToolByName("ptc")
	if !ok {
		t.Fatalf("ptc missing from %v", agent.ToolNames())
	}
	out, err := ptc.Invoke(context.Background(), json.RawMessage(`{"calls":[{"tool":"missing"},{"tool":"ptc"},{"tool":"echo"}]}`))
	if err != nil {
		t.Fatalf("ptc batch: %v", err)
	}
	var response PTCResponse
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(response.Results) != 3 || response.Results[0].OK || response.Results[1].OK || !response.Results[2].OK {
		t.Fatalf("ptc mixed results = %+v", response.Results)
	}
	if !strings.Contains(response.Results[0].Error, "unknown tool") {
		t.Fatalf("unknown tool error = %q", response.Results[0].Error)
	}
	if _, err := ptc.Invoke(context.Background(), json.RawMessage(`{"calls":[]}`)); err == nil {
		t.Fatal("ptc accepted an empty batch")
	}
}

func TestCreatorSettingsTools(t *testing.T) {
	settings := &fakeSettings{}
	agent := newTestAgent(t, Config{
		einoModel: newScriptModel(func(int) ([]*schema.Message, error) { return finalText("x"), nil }),
		Mode:      ModeCreator,
		Settings:  settings,
	})
	set, ok := agent.ToolByName("settings_set")
	if !ok {
		t.Fatalf("creator tools = %v, want settings_set", agent.ToolNames())
	}
	get, ok := agent.ToolByName("settings_get")
	if !ok {
		t.Fatalf("creator tools = %v, want settings_get", agent.ToolNames())
	}
	if _, err := set.Invoke(context.Background(), json.RawMessage(`{"key":"ui.language","value":"zh"}`)); err != nil {
		t.Fatalf("settings_set: %v", err)
	}
	value, err := get.Invoke(context.Background(), json.RawMessage(`{"key":"ui.language"}`))
	if err != nil || value != "zh" {
		t.Fatalf("settings_get = %q, %v; want zh", value, err)
	}
	if settings.values["ui.language"] != "zh" {
		t.Fatalf("settings store = %v, want ui.language=zh", settings.values)
	}
	if _, err := set.Invoke(context.Background(), json.RawMessage(`{"key":"provider.api_key","value":"leak"}`)); err == nil {
		t.Fatal("settings_set accepted a secret key")
	}
	if _, err := get.Invoke(context.Background(), json.RawMessage(`{"key":"not.a.real.setting","value":"x"}`)); err == nil {
		t.Fatal("settings_get accepted a key outside the whitelist")
	}

	// Standard mode must not expose the Creator tools even though the same
	// settings store is available.
	standard := newTestAgent(t, Config{
		einoModel: newScriptModel(func(int) ([]*schema.Message, error) { return finalText("x"), nil }),
		Mode:      ModeStandard,
		Settings:  settings,
	})
	if _, ok := standard.ToolByName("settings_set"); ok {
		t.Fatalf("standard tools include settings_set: %v", standard.ToolNames())
	}
}

func TestSetModeRebuildsToolSet(t *testing.T) {
	reg := NewRegistry()
	for _, name := range []string{"bash", "read_file", "terminal_open", "read_image", "working_directory"} {
		if err := reg.Register(&stubTool{name: name}); err != nil {
			t.Fatal(err)
		}
	}
	m := newScriptModel(func(int) ([]*schema.Message, error) { return finalText("x"), nil })
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg})
	// checkTurn verifies both the public tool view and the tools the rebuilt
	// eino topology actually binds on the next model call.
	checkTurn := func(call int, label string, present, absent []string) {
		t.Helper()
		requireTools(t, label, agent.ToolNames(), present, absent)
		runModeTurn(t, agent, "switch-"+label, "hello")
		requireTools(t, label+" model", m.toolNamesAt(call), present, absent)
	}
	checkTurn(0, "standard", []string{"bash", "read_file"}, []string{"ptc", "settings_get"})
	if err := agent.SetMode(ModeMinimal); err != nil {
		t.Fatalf("SetMode(minimal): %v", err)
	}
	checkTurn(1, "minimal", []string{"terminal_open", "read_image", "working_directory"}, []string{"bash", "read_file", "ptc"})
	if err := agent.SetMode(ModePTC); err != nil {
		t.Fatalf("SetMode(ptc): %v", err)
	}
	checkTurn(2, "ptc", []string{"bash", "ptc"}, []string{"settings_get"})
	if err := agent.SetMode(ModeCreator); err != nil {
		t.Fatalf("SetMode(creator): %v", err)
	}
	checkTurn(3, "creator", []string{"bash", "settings_get", "settings_set"}, []string{"ptc"})
	if err := agent.SetMode(ModeStandard); err != nil {
		t.Fatalf("SetMode(standard): %v", err)
	}
	checkTurn(4, "standard again", []string{"bash", "read_file"}, []string{"ptc", "settings_get", "settings_set"})
}

// TestSetModeRefreshesInstructionPolicy proves a mode switch updates the
// composed prompt immediately, not only on the next turn: the prompt policy is
// part of the mode contract, and a controller has already persisted the new
// mode before it calls Agent.SetMode.
func TestSetModeRefreshesInstructionPolicy(t *testing.T) {
	current := ModeStandard
	m := newScriptModel(func(int) ([]*schema.Message, error) { return finalText("x"), nil })
	agent := newTestAgent(t, Config{
		einoModel:   m,
		Instruction: "base",
		InstructionFunc: func() string {
			if current == "" {
				return ""
			}
			return "preset=" + current
		},
	})
	// New publishes the literal Config.Instruction; InstructionFunc is applied
	// on the next mode/instruction rebuild.
	if got := agent.Instruction(); got != "base" {
		t.Fatalf("initial Instruction() = %q, want base", got)
	}
	for _, mode := range []string{ModePTC, ModeMinimal, ModeCreator, ModeStandard} {
		current = mode
		if err := agent.SetMode(mode); err != nil {
			t.Fatalf("SetMode(%s): %v", mode, err)
		}
		if got := agent.Instruction(); got != "preset="+mode {
			t.Fatalf("Instruction() after SetMode(%s) = %q, want preset=%s", mode, got, mode)
		}
	}
	// An InstructionFunc that renders nothing keeps the current instruction,
	// matching refreshInstruction's behavior.
	current = ""
	if err := agent.SetMode(ModePTC); err != nil {
		t.Fatalf("SetMode(ptc) with empty instruction: %v", err)
	}
	if got := agent.Instruction(); got != "preset=standard" {
		t.Fatalf("Instruction() after empty render = %q, want the previous text", got)
	}
}

// TestSubagentToolSetsFollowMode proves delegated children inherit the
// parent's model-visible mode tool set (minus orchestration) instead of the
// unfiltered registry. The PTC batch tool stays root-only because its nested
// calls resolve against the registry, which would bypass the child filter.
func TestSubagentToolSetsFollowMode(t *testing.T) {
	reg := NewRegistry()
	for _, name := range []string{"bash", "terminal_open", "read_image", "settings_get", "settings_set", "subagent"} {
		if err := reg.Register(&stubTool{name: name}); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		mode    string
		present []string
		absent  []string
	}{
		{
			mode:    ModeStandard,
			present: []string{"bash", "terminal_open", "read_image"},
			absent:  []string{"settings_get", "settings_set", "subagent", "ptc"},
		},
		{
			mode:    ModePTC,
			present: []string{"bash", "terminal_open", "read_image"},
			absent:  []string{"settings_get", "settings_set", "subagent", "ptc"},
		},
		{
			mode:    ModeMinimal,
			present: []string{"terminal_open", "read_image"},
			absent:  []string{"bash", "settings_get", "settings_set", "subagent", "ptc"},
		},
		{
			mode:    ModeCreator,
			present: []string{"bash", "terminal_open", "read_image", "settings_get", "settings_set"},
			absent:  []string{"subagent", "ptc"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			m := newScriptModel(func(int) ([]*schema.Message, error) { return finalText("child done"), nil })
			agent := newTestAgent(t, Config{
				einoModel: m,
				Tools:     reg,
				Mode:      tc.mode,
				Settings:  &fakeSettings{},
			})
			result, err := agent.RunSubagent(context.Background(), SubagentRequest{Task: "t", Agent: "worker", Depth: 1})
			if err != nil {
				t.Fatalf("RunSubagent(%s): %v", tc.mode, err)
			}
			requireTools(t, tc.mode+" child", result.Tools, tc.present, tc.absent)
			requireTools(t, tc.mode+" child model", m.toolNamesAt(0), tc.present, tc.absent)
		})
	}
}

// TestCreatorSubagentGetsSynthesizedSettingsTools covers a bare runtime whose
// registry has no concrete settings tools: the parent synthesizes them, and
// the child's advertised and bound sets must agree with that mode contract.
func TestCreatorSubagentGetsSynthesizedSettingsTools(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&stubTool{name: "bash"}); err != nil {
		t.Fatal(err)
	}
	m := newScriptModel(func(int) ([]*schema.Message, error) { return finalText("child done"), nil })
	agent := newTestAgent(t, Config{
		einoModel: m,
		Tools:     reg,
		Mode:      ModeCreator,
		Settings:  &fakeSettings{},
	})
	result, err := agent.RunSubagent(context.Background(), SubagentRequest{Task: "t", Depth: 1})
	if err != nil {
		t.Fatalf("RunSubagent(creator): %v", err)
	}
	requireTools(t, "creator child", result.Tools, []string{"bash", "settings_get", "settings_set"}, []string{"ptc"})
	requireTools(t, "creator child model", m.toolNamesAt(0), []string{"bash", "settings_get", "settings_set"}, []string{"ptc"})
}

// drainEventsUntil drains ch but gives up after d without failing the test from
// a helper goroutine. Tests wrap a potentially deadlocking call in a goroutine
// and use it to report a clear failure instead of hanging until the go test
// timeout.
func drainEventsUntil(ch <-chan state.Event, d time.Duration) []state.Event {
	timer := time.NewTimer(d)
	defer timer.Stop()
	var events []state.Event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-timer.C:
			return events
		}
	}
}

// runTurnWithDeadline runs one turn in a goroutine and fails the test if it does
// not finish. The supervisor self-deadlock regression blocked inside Run before
// the turn channel existed, so a plain runModeTurn would only surface it at the
// package timeout.
func runTurnWithDeadline(t *testing.T, a *Agent, sessionID, text string, d time.Duration) []state.Event {
	t.Helper()
	type outcome struct {
		events []state.Event
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d)
		defer cancel()
		ch, err := a.Run(ctx, sessionID, text)
		if err != nil {
			done <- outcome{err: err}
			return
		}
		done <- outcome{events: drainEventsUntil(ch, d)}
	}()
	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("Run: %v", out.err)
		}
		return out.events
	case <-time.After(d + 2*time.Second):
		t.Fatalf("Run(%q) did not finish within %s (topology rebuild deadlocked)", text, d)
		return nil
	}
}

// TestSupervisorModeWithInstructionFuncDoesNotDeadlock is the regression for
// the supervisor self-deadlock: refreshInstruction/SetModel held agentMu while
// buildMode -> buildSubagentAgent -> subagentToolNames -> a.Mode() tried to take
// the same RWMutex. The build now receives an explicit snapshot, so the first
// Run and a model switch must complete.
func TestSupervisorModeWithInstructionFuncDoesNotDeadlock(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&stubTool{name: "clock"}); err != nil {
		t.Fatal(err)
	}
	var instructionCalls atomic.Int32
	agent := newTestAgent(t, Config{
		Provider: &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
			return []Chunk{{Text: "supervisor result", Done: true}}, nil
		}},
		Tools: reg,
		Mode:  ModeSupervisor,
		InstructionFunc: func() string {
			return fmt.Sprintf("dynamic instruction %d", instructionCalls.Add(1))
		},
	})
	events := runTurnWithDeadline(t, agent, "supervisor-deadlock", "delegate", 15*time.Second)
	if joined := strings.Join(tokenTexts(events), ""); !strings.Contains(joined, "supervisor result") {
		t.Fatalf("tokens = %q, want the supervisor answer", joined)
	}
	if got := agent.Instruction(); !strings.HasPrefix(got, "dynamic instruction") {
		t.Fatalf("Instruction() = %q, want the dynamic prompt", got)
	}
	if err := agent.SetModel("second-model"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	events = runTurnWithDeadline(t, agent, "supervisor-model", "delegate again", 15*time.Second)
	if joined := strings.Join(tokenTexts(events), ""); !strings.Contains(joined, "supervisor result") {
		t.Fatalf("tokens after SetModel = %q", joined)
	}
	if got := agent.ModelName(); got != "second-model" {
		t.Fatalf("ModelName() = %q, want second-model", got)
	}
}

// TestSetModeRebuildsSubagentToolSet proves a Creator->Standard switch does not
// leave a cached child holding Creator-only settings tools: the child cache is
// invalidated by the topology generation and by SetMode's explicit clear.
func TestSetModeRebuildsSubagentToolSet(t *testing.T) {
	reg := NewRegistry()
	for _, name := range []string{"settings_get", "settings_set", "bash", "read_image"} {
		if err := reg.Register(&stubTool{name: name}); err != nil {
			t.Fatal(err)
		}
	}
	m := newScriptModel(func(int) ([]*schema.Message, error) { return finalText("child done"), nil })
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg, Mode: ModeCreator})

	creator, err := agent.RunSubagent(context.Background(), SubagentRequest{Task: "t", Agent: "worker", Depth: 1})
	if err != nil {
		t.Fatalf("RunSubagent(creator): %v", err)
	}
	if !hasTool(creator.Tools, "settings_get") || !hasTool(creator.Tools, "settings_set") {
		t.Fatalf("creator child tools = %v, want the settings tools", creator.Tools)
	}

	if err := agent.SetMode(ModeStandard); err != nil {
		t.Fatalf("SetMode(standard): %v", err)
	}
	standard, err := agent.RunSubagent(context.Background(), SubagentRequest{Task: "t", Agent: "worker", Depth: 1})
	if err != nil {
		t.Fatalf("RunSubagent(standard): %v", err)
	}
	for _, absent := range []string{"settings_get", "settings_set"} {
		if hasTool(standard.Tools, absent) {
			t.Fatalf("standard child tools = %v, must not cache Creator-only %q", standard.Tools, absent)
		}
	}
	if !hasTool(standard.Tools, "bash") {
		t.Fatalf("standard child tools = %v, want bash", standard.Tools)
	}
}

// TestConcurrentModeModelAndRun exercises the locked SetMode/SetModel rebuild
// paths against concurrent turns. Before the snapshot refactor this reported
// data races between modes.go reading instruction/chatModel and
// refreshInstruction/SetModel writing them.
func TestConcurrentModeModelAndRun(t *testing.T) {
	reg := NewRegistry()
	for _, name := range []string{"clock", "bash", "read_image"} {
		if err := reg.Register(&stubTool{name: name}); err != nil {
			t.Fatal(err)
		}
	}
	agent := newTestAgent(t, Config{
		Provider: &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
			return []Chunk{{Text: "ok", Done: true}}, nil
		}},
		Tools: reg,
		InstructionFunc: func() string {
			return "instruction " + time.Now().Format(time.RFC3339Nano)
		},
	})
	// eino's ChatModelAgent is not itself safe for two simultaneous Run calls,
	// so each iteration drives exactly one turn while SetMode and SetModel race
	// that turn's rebuild. This is the overlap the production controller has
	// (a model switch applies from the next turn while a turn may run).
	modes := []string{ModeStandard, ModePTC, ModeMinimal, ModeCreator}
	for i := 0; i < 8; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		ch, err := agent.Run(ctx, fmt.Sprintf("race-%d", i), "hello")
		if err != nil {
			cancel()
			t.Fatalf("Run: %v", err)
		}
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := agent.SetMode(modes[i%len(modes)]); err != nil {
				errs <- err
			}
		}(i)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := agent.SetModel(fmt.Sprintf("model-%d", i)); err != nil {
				errs <- err
			}
		}(i)
		for range ch {
		}
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("concurrent Run/SetMode/SetModel did not finish within 20s")
		}
		close(errs)
		for err := range errs {
			t.Fatalf("concurrent operation: %v", err)
		}
		cancel()
	}
}
