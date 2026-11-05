package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

// requireExactToolSet asserts that got contains exactly want, independent of
// order. It is the strict form of requireTools used by the acceptance tests:
// an extra tool is a failure, not just a missing one.
func requireExactToolSet(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s tools = %v, want exactly %v", label, got, want)
	}
	wanted := make(map[string]bool, len(want))
	for _, name := range want {
		if wanted[name] {
			t.Fatalf("%s want list repeats %q", label, name)
		}
		wanted[name] = true
	}
	seen := make(map[string]bool, len(got))
	for _, name := range got {
		if !wanted[name] {
			t.Fatalf("%s tools = %v, unexpected %q (want exactly %v)", label, got, name, want)
		}
		if seen[name] {
			t.Fatalf("%s tools = %v, duplicate %q", label, got, name)
		}
		seen[name] = true
	}
}

// TestPTCBatchExecution proves the ptc tool executes one ordered batch of
// {tool,args} calls in a single invocation, returns one result per call, and
// runs the calls strictly sequentially (each call finishes before the next
// starts). It also covers the two argument spellings a tool-calling model may
// emit: a JSON object and a JSON string containing an object.
func TestPTCBatchExecution(t *testing.T) {
	var (
		mu     sync.Mutex
		events []string
	)
	record := func(event string) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}
	parseText := func(args json.RawMessage) (string, error) {
		var in struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		return in.Text, nil
	}

	reg := NewRegistry()
	register := func(tool *stubTool) {
		t.Helper()
		if err := reg.Register(tool); err != nil {
			t.Fatal(err)
		}
	}
	register(&stubTool{name: "first", invoke: func(_ context.Context, args json.RawMessage) (string, error) {
		text, err := parseText(args)
		if err != nil {
			return "", err
		}
		record("first-start:" + text)
		time.Sleep(5 * time.Millisecond)
		record("first-end:" + text)
		return "first:" + text, nil
	}})
	register(&stubTool{name: "second", invoke: func(_ context.Context, args json.RawMessage) (string, error) {
		text, err := parseText(args)
		if err != nil {
			return "", err
		}
		record("second-start:" + text)
		time.Sleep(5 * time.Millisecond)
		record("second-end:" + text)
		return "second:" + text, nil
	}})
	register(&stubTool{name: "third", invoke: func(_ context.Context, args json.RawMessage) (string, error) {
		if string(args) != "{}" {
			return "", errors.New("omitted args must be normalized to {}")
		}
		return "third", nil
	}})

	agent := newTestAgent(t, Config{
		einoModel: newScriptModel(func(int) ([]*schema.Message, error) { return finalText("ok"), nil }),
		Tools:     reg,
		Mode:      ModePTC,
	})
	ptc, ok := agent.ToolByName(ptcToolName)
	if !ok {
		t.Fatalf("PTC mode tools = %v, want %q", agent.ToolNames(), ptcToolName)
	}

	out, err := ptc.Invoke(context.Background(), json.RawMessage(`{"calls":[
		{"tool":"first","args":{"text":"a"}},
		{"tool":"second","args":"{\"text\":\"b\"}"},
		{"tool":"third"}
	]}`))
	if err != nil {
		t.Fatalf("ptc batch: %v", err)
	}
	var response PTCResponse
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatalf("decode ptc result %q: %v", out, err)
	}
	wantTools := []string{"first", "second", "third"}
	wantOutputs := []string{"first:a", "second:b", "third"}
	if len(response.Results) != len(wantTools) {
		t.Fatalf("ptc results = %+v, want %d entries", response.Results, len(wantTools))
	}
	for i := range wantTools {
		result := response.Results[i]
		if result.Index != i || result.Tool != wantTools[i] || !result.OK || result.Output != wantOutputs[i] || result.Error != "" {
			t.Fatalf("ptc result[%d] = %+v, want index=%d tool=%q ok=true output=%q", i, result, i, wantTools[i], wantOutputs[i])
		}
	}

	mu.Lock()
	gotEvents := append([]string(nil), events...)
	mu.Unlock()
	wantEvents := []string{"first-start:a", "first-end:a", "second-start:b", "second-end:b"}
	if len(gotEvents) != len(wantEvents) {
		t.Fatalf("ptc call events = %v, want sequential %v", gotEvents, wantEvents)
	}
	for i := range wantEvents {
		if gotEvents[i] != wantEvents[i] {
			t.Fatalf("ptc call events = %v, want sequential %v", gotEvents, wantEvents)
		}
	}
}

// TestPTCBatchPartialFailure proves a failing call is reported as that call's
// error while the rest of the batch still runs and the overall ptc invocation
// succeeds: partial failure is data, not a failed tool call.
func TestPTCBatchPartialFailure(t *testing.T) {
	reg := NewRegistry()
	register := func(tool *stubTool) {
		t.Helper()
		if err := reg.Register(tool); err != nil {
			t.Fatal(err)
		}
	}
	register(&stubTool{name: "ok", invoke: func(context.Context, json.RawMessage) (string, error) {
		return "ok-output", nil
	}})
	register(&stubTool{name: "boom", invoke: func(context.Context, json.RawMessage) (string, error) {
		return "", errors.New("boom failure")
	}})
	register(&stubTool{name: "after", invoke: func(context.Context, json.RawMessage) (string, error) {
		return "after-output", nil
	}})
	// A creator-only settings tool is registered in the registry but must not
	// be callable through a PTC batch.
	register(&stubTool{name: "settings_get"})

	agent := newTestAgent(t, Config{
		einoModel: newScriptModel(func(int) ([]*schema.Message, error) { return finalText("ok"), nil }),
		Tools:     reg,
		Mode:      ModePTC,
	})
	ptc, ok := agent.ToolByName(ptcToolName)
	if !ok {
		t.Fatalf("PTC mode tools = %v, want %q", agent.ToolNames(), ptcToolName)
	}

	out, err := ptc.Invoke(context.Background(), json.RawMessage(`{"calls":[
		{"tool":"ok"},
		{"tool":"boom"},
		{"tool":"after"},
		{"tool":"missing"},
		{"tool":"settings_get"}
	]}`))
	if err != nil {
		t.Fatalf("partial ptc batch failed as a whole: %v", err)
	}
	var response PTCResponse
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatalf("decode ptc result %q: %v", out, err)
	}
	if len(response.Results) != 5 {
		t.Fatalf("ptc results = %+v, want 5 entries", response.Results)
	}

	if r := response.Results[0]; !r.OK || r.Output != "ok-output" || r.Error != "" {
		t.Fatalf("ptc result[0] = %+v, want the successful call", r)
	}
	if r := response.Results[1]; r.OK || r.Output != "" || !strings.Contains(r.Error, "boom failure") {
		t.Fatalf("ptc result[1] = %+v, want the boom failure as a per-call error", r)
	}
	if r := response.Results[2]; !r.OK || r.Output != "after-output" {
		t.Fatalf("ptc result[2] = %+v, want the batch to continue after a failure", r)
	}
	if r := response.Results[3]; r.OK || !strings.Contains(r.Error, "unknown tool") {
		t.Fatalf("ptc result[3] = %+v, want an unknown-tool error", r)
	}
	if r := response.Results[4]; r.OK || !strings.Contains(r.Error, "not available inside a PTC batch") {
		t.Fatalf("ptc result[4] = %+v, want creator tools rejected inside a batch", r)
	}
}

// TestMinimalToolRestriction pins Minimal mode to DSH's exact preset:
// terminal_open, terminal_list, terminal_read, terminal_send, terminal_signal,
// terminal_close, read_image and working_directory. Every other registry tool
// is hidden, including a terminal_-prefixed tool that is not one of the six
// DSH terminal tools. The pure ModeTools helper must agree with the live agent.
func TestMinimalToolRestriction(t *testing.T) {
	minimal := []string{
		"terminal_open",
		"terminal_list",
		"terminal_read",
		"terminal_send",
		"terminal_signal",
		"terminal_close",
		"read_image",
		"working_directory",
	}
	forbidden := []string{
		"bash", "read_file", "write_file", "edit_file", "glob", "grep",
		"search_file", "settings_get", "settings_set", ptcToolName,
		"terminal_debug", "terminal_run", "terminal_manager",
	}
	reg := NewRegistry()
	for _, name := range append(append([]string(nil), minimal...), forbidden...) {
		name := name
		if err := reg.Register(&stubTool{name: name}); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}

	agent := newTestAgent(t, Config{
		einoModel: newScriptModel(func(int) ([]*schema.Message, error) { return finalText("ok"), nil }),
		Tools:     reg,
		Mode:      ModeMinimal,
	})
	requireExactToolSet(t, "minimal", agent.ToolNames(), minimal)
	for _, name := range forbidden {
		if tool, ok := agent.ToolByName(name); ok {
			t.Fatalf("minimal ToolByName(%q) = %q, want hidden", name, tool.Name())
		}
	}

	available := make([]string, 0, len(minimal)+len(forbidden))
	available = append(available, minimal...)
	available = append(available, forbidden...)
	requireExactToolSet(t, "ModeTools(minimal)", ModeTools(ModeMinimal, available), minimal)

	// Standard keeps the ordinary tools and the registry's extra terminal_*
	// stub, while still hiding the creator-only settings pair and ptc.
	standard := newTestAgent(t, Config{
		einoModel: newScriptModel(func(int) ([]*schema.Message, error) { return finalText("ok"), nil }),
		Tools:     reg,
		Mode:      ModeStandard,
	})
	standardNames := standard.ToolNames()
	for _, name := range []string{"bash", "read_file", "terminal_open", "terminal_debug"} {
		if !hasTool(standardNames, name) {
			t.Fatalf("standard tools = %v, want %q", standardNames, name)
		}
	}
	for _, name := range []string{"settings_get", "settings_set", ptcToolName} {
		if hasTool(standardNames, name) {
			t.Fatalf("standard tools = %v, must hide %q", standardNames, name)
		}
	}
}

// TestCreatorSettingsRoundTrip proves Creator mode owns exactly Standard plus
// settings_get/settings_set, and that the pair round-trips whitelisted keys
// through the SettingsStore while rejecting secret and non-whitelisted names.
func TestCreatorSettingsRoundTrip(t *testing.T) {
	settings := &fakeSettings{}
	reg := NewRegistry()
	for _, name := range []string{"bash", "read_file", "read_image", "working_directory"} {
		if err := reg.Register(&stubTool{name: name}); err != nil {
			t.Fatal(err)
		}
	}
	newCreator := func() *Agent {
		t.Helper()
		return newTestAgent(t, Config{
			einoModel: newScriptModel(func(int) ([]*schema.Message, error) { return finalText("ok"), nil }),
			Tools:     reg,
			Mode:      ModeCreator,
			Settings:  settings,
		})
	}
	agent := newCreator()
	if !hasTool(agent.ToolNames(), "bash") {
		t.Fatalf("creator tools = %v, want the standard tools", agent.ToolNames())
	}
	set, ok := agent.ToolByName("settings_set")
	if !ok {
		t.Fatalf("creator tools = %v, want settings_set", agent.ToolNames())
	}
	get, ok := agent.ToolByName("settings_get")
	if !ok {
		t.Fatalf("creator tools = %v, want settings_get", agent.ToolNames())
	}

	ctx := context.Background()
	cases := []struct {
		key   string
		value string
		store string
	}{
		{key: "ui.theme", value: `"dark"`, store: "dark"},
		{key: "ui.font_size", value: "14", store: "14"},
		{key: "plugin.web_search.enabled", value: "true", store: "true"},
	}
	for _, tc := range cases {
		if _, err := set.Invoke(ctx, json.RawMessage(`{"key":"`+tc.key+`","value":`+tc.value+`}`)); err != nil {
			t.Fatalf("settings_set(%s): %v", tc.key, err)
		}
		got, err := get.Invoke(ctx, json.RawMessage(`{"key":"`+tc.key+`"}`))
		if err != nil {
			t.Fatalf("settings_get(%s): %v", tc.key, err)
		}
		if got != tc.store {
			t.Fatalf("settings_get(%s) = %q, want %q", tc.key, got, tc.store)
		}
		settings.mu.Lock()
		stored := settings.values[tc.key]
		settings.mu.Unlock()
		if stored != tc.store {
			t.Fatalf("store[%s] = %q, want %q", tc.key, stored, tc.store)
		}
	}

	// A rebuilt Creator agent reads the same persisted values.
	rebuilt := newCreator()
	rebuiltGet, ok := rebuilt.ToolByName("settings_get")
	if !ok {
		t.Fatalf("rebuilt creator tools = %v, want settings_get", rebuilt.ToolNames())
	}
	if got, err := rebuiltGet.Invoke(ctx, json.RawMessage(`{"key":"ui.theme"}`)); err != nil || got != "dark" {
		t.Fatalf("rebuilt settings_get(ui.theme) = %q, %v; want dark", got, err)
	}

	for _, key := range []string{"provider.api_key", "ui.auth_token", "not.a.real.setting"} {
		if _, err := set.Invoke(ctx, json.RawMessage(`{"key":"`+key+`","value":"leak"}`)); err == nil {
			t.Fatalf("settings_set accepted non-whitelisted key %q", key)
		}
		if _, err := get.Invoke(ctx, json.RawMessage(`{"key":"`+key+`"}`)); err == nil {
			t.Fatalf("settings_get accepted non-whitelisted key %q", key)
		}
	}

	// The settings pair is Creator-only: Standard and PTC hide it.
	for _, mode := range []string{ModeStandard, ModePTC} {
		other := newTestAgent(t, Config{
			einoModel: newScriptModel(func(int) ([]*schema.Message, error) { return finalText("ok"), nil }),
			Tools:     reg,
			Mode:      mode,
			Settings:  settings,
		})
		for _, name := range []string{"settings_get", "settings_set"} {
			if _, ok := other.ToolByName(name); ok {
				t.Fatalf("%s tools = %v, must hide %q", mode, other.ToolNames(), name)
			}
		}
	}
}
