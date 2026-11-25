package tasklease

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestDescriptorMetadata pins the seam: the lease layer must provide
// contracts.KeyTaskLease and require the task board it wraps plus the product
// data directory its claims are persisted in (a claim that survives a restart
// is the whole point of the durable half).
func TestDescriptorMetadata(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, ModuleID)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyTaskLease.Name() {
		t.Fatalf("provides = %+v, want the task lease key", descriptor.Provides)
	}
	required := map[string]bool{}
	for _, requirement := range descriptor.Requires {
		required[requirement.Name] = true
	}
	if !required[contracts.KeyTaskBoard.Name()] || !required[contracts.KeyDataDir.Name()] {
		t.Fatalf("requires = %v, want the task board and the data dir", required)
	}
	if len(descriptor.Requires) != 2 {
		t.Fatalf("requires = %v, want exactly board + data dir", descriptor.Requires)
	}
}
