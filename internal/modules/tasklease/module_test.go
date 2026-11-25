package tasklease

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestDescriptorMetadata pins the seam: the lease layer must provide
// contracts.KeyTaskLease and require the task board it wraps.
func TestDescriptorMetadata(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, ModuleID)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyTaskLease.Name() {
		t.Fatalf("provides = %+v, want the task lease key", descriptor.Provides)
	}
	if len(descriptor.Requires) != 1 || descriptor.Requires[0].Name != contracts.KeyTaskBoard.Name() {
		t.Fatalf("requires = %+v, want the task board key", descriptor.Requires)
	}
}
