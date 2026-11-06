package toolkit

import (
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestModuleProvidesRigToolset pins the product/SDK seam: the toolkit module
// builds the registry from the published ZeoKeel Rig toolset and provides it as
// a kernel service.
func TestModuleProvidesRigToolset(t *testing.T) {
	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()

	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID:     "toolkit",
		Module: "zeokeel/toolkit",
		Config: map[string]any{"root": ".", "shellTimeoutMs": 5000},
	}})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer func() { _ = loader.Close() }()

	tools, err := root.Require(contracts.KeyToolkit)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}
	if got := len(tools.Specs()); got < 9 {
		t.Fatalf("registry tools = %d, want at least 9 (7 builtin + working_directory + todo_write)", got)
	}
}
