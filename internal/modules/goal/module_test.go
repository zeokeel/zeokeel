package goal

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestDescriptorMetadata pins the module identity and its service contract: the
// goal backend provides contracts.KeyGoal and requires the durable session
// store. The assembly tests load it for real.
func TestDescriptorMetadata(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, ModuleID)
	}
	if descriptor.Name == "" {
		t.Fatal("module has no display name")
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyGoal.Name() {
		t.Fatalf("provides = %+v, want the goal key", descriptor.Provides)
	}
	if len(descriptor.Requires) != 1 || descriptor.Requires[0].Name != contracts.KeyStore.Name() {
		t.Fatalf("requires = %+v, want the store key", descriptor.Requires)
	}
}

// TestDefaultMaxRoundsConfig pins the config override: 0 keeps the DSH default,
// a positive value replaces it.
func TestDefaultMaxRoundsConfig(t *testing.T) {
	if service := newService(nil, nil, nil, 0); service.defaultMaxRounds != contracts.DefaultMaxGoalRounds {
		t.Fatalf("default max rounds = %d, want %d", service.defaultMaxRounds, contracts.DefaultMaxGoalRounds)
	}
	if service := newService(nil, nil, nil, 12); service.defaultMaxRounds != 12 {
		t.Fatalf("configured max rounds = %d, want 12", service.defaultMaxRounds)
	}
}
