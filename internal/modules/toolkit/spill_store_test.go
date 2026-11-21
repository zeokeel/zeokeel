package toolkit

// The oversized-result store is the read-back half of spill: the agent writes a
// handle into it, and the model calls spill_read with that handle. Both sides
// must share one store instance.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/sandbox"
	"github.com/zeokeel/zeokeel-rig/spillfile"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/hosttools"
)

// TestSpillReadToolReadsTheSharedStore pins the loop: content saved into the
// store by the agent side is returned by the model-facing spill_read tool.
func TestSpillReadToolReadsTheSharedStore(t *testing.T) {
	store, err := spillfile.New(t.TempDir())
	if err != nil {
		t.Fatalf("spillfile.New: %v", err)
	}
	scoped := buildRootScopedWith(Config{}, t.TempDir(), 5*time.Second, sandbox.ModeWorkspaceWrite, nil,
		t.TempDir(), store, nil, "linux", func(string) (string, error) { return "", os.ErrNotExist })
	t.Cleanup(scoped.close)

	ref, err := store.Save(context.Background(), rig.ToolCall{Name: "bash"}, strings.Repeat("payload-", 100))
	if err != nil {
		t.Fatalf("store.Save: %v", err)
	}
	args, err := json.Marshal(map[string]string{"id": ref.ID})
	if err != nil {
		t.Fatalf("encode args: %v", err)
	}
	result := invokeScopedTool(t, scoped, "spill_read", string(args))
	if result.IsError {
		t.Fatalf("spill_read failed: %q", result.Content)
	}
	if !strings.Contains(result.Content, "payload-payload-") {
		t.Fatalf("spill_read content = %q, want the spilled body", result.Content)
	}
}

// TestModuleProvidesSpillStore pins the wiring: with a data directory the
// toolkit creates the store, provides it to the agent and registers spill_read.
func TestModuleProvidesSpillStore(t *testing.T) {
	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()
	dataDir := t.TempDir()
	if _, err := root.Provide(contracts.KeySettings, contracts.SettingsService(testSettings{})); err != nil {
		t.Fatalf("provide settings: %v", err)
	}
	if _, err := root.Provide(contracts.KeyDataDir, dataDir); err != nil {
		t.Fatalf("provide data dir: %v", err)
	}
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "toolkit", Module: "zeokeel/toolkit", Config: map[string]any{"root": ".", "shellTimeoutMs": 5000},
	}})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer func() { _ = loader.Close() }()

	store, err := root.Require(contracts.KeySpillStore)
	if err != nil || store == nil {
		t.Fatalf("require spill store: %v/%v", store, err)
	}
	tools, err := root.Require(hosttools.KeyRegistry)
	if err != nil {
		t.Fatalf("require registry: %v", err)
	}
	found := false
	for _, spec := range tools.Specs() {
		if spec.Name == "spill_read" {
			found = true
		}
	}
	if !found {
		t.Fatal("registry lacks spill_read")
	}
}
