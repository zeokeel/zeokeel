package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/tools"
)

// PTC/voice removal guards.
//
// The product direction removed the PTC (programmatic tool calling) preset and
// the voice-input plugin. These tests are the app-side TDD guards: the runtime
// side is removed by the parity work, while this package must stop registering
// the ptc tool and stop advertising the voice_input plugin. Keep them failing
// until the removal is complete.

// TestToolRegistryHasNoPTC asserts the production tool registry no longer
// contains the "ptc" batch tool.
func TestToolRegistryHasNoPTC(t *testing.T) {
	st, dir := newToolStore(t)
	reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
	if err != nil {
		t.Fatalf("register tools: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	for _, tool := range reg.List() {
		if tool.Name() == "ptc" {
			t.Fatalf("production registry still registers the PTC batch tool: %v", reg.List())
		}
	}
}

// TestAppModesExcludePTC asserts no agent mode routing re-introduces the PTC
// tool, including the historical "ptc" preset name. The preset is gone, so an
// unknown name must fall back to the standard set instead of synthesizing ptc.
func TestAppModesExcludePTC(t *testing.T) {
	st, dir := newToolStore(t)
	reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
	if err != nil {
		t.Fatalf("register tools: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	names := make([]string, 0, len(reg.List()))
	for _, tool := range reg.List() {
		names = append(names, tool.Name())
	}
	for _, mode := range []string{"standard", "ptc", "minimal", "creator", "deep", "supervisor", "planexecute", "unknown-mode"} {
		for _, name := range runtime.ModeTools(mode, names) {
			if name == "ptc" {
				t.Fatalf("mode %q tool set still contains ptc: %v", mode, runtime.ModeTools(mode, names))
			}
		}
	}
}

// TestInspectPluginCatalogHasNoVoice asserts the inspect tool no longer lists
// the voice_input plugin (the plugin was removed from the product surface).
func TestInspectPluginCatalogHasNoVoice(t *testing.T) {
	st, dir := newToolStore(t)
	tool := tools.NewInspectTool(tools.InspectConfig{Store: st, Root: dir})
	args, err := json.Marshal(map[string]any{"section": "plugins"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Invoke(context.Background(), args)
	if err != nil {
		t.Fatalf("inspect plugins: %v", err)
	}
	if strings.Contains(out, "voice_input") {
		t.Fatalf("inspect still advertises the removed voice_input plugin:\n%s", out)
	}
}
