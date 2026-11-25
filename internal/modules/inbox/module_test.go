package inbox

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestDescriptorMetadata pins the module identity and its service contract: the
// inbox is the single external entry point, so it must provide contracts.KeyInbox
// and require nothing (it cannot depend on the Lead or the task board — they
// depend on it). The assembly tests load it for real.
func TestDescriptorMetadata(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, ModuleID)
	}
	if descriptor.Name == "" {
		t.Fatal("module has no display name")
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyInbox.Name() {
		t.Fatalf("provides = %+v, want the inbox key", descriptor.Provides)
	}
	// The only dependency allowed here is the data directory (durability): the
	// entry point must not depend on any consumer (Lead, task board, approvals).
	if len(descriptor.Requires) != 1 || descriptor.Requires[0].Name != contracts.KeyDataDir.Name() {
		t.Fatalf("requires = %+v, want only the data dir", descriptor.Requires)
	}
}

// TestServiceSatisfiesContract makes the wiring failure show up here rather than
// as a runtime type assertion panic inside the gateway.
func TestServiceSatisfiesContract(t *testing.T) {
	var service any = NewService(Config{})
	if _, ok := service.(contracts.InboxService); !ok {
		t.Fatal("service does not satisfy contracts.InboxService")
	}
}
