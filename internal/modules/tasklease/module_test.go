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
	// Two keys, one store: the lease itself and the held-send half (a consumer
	// that only parks outbound messages should not need thirteen lease methods).
	provided := map[string]bool{}
	for _, provide := range descriptor.Provides {
		provided[provide.Name] = true
	}
	if !provided[contracts.KeyTaskLease.Name()] || !provided[contracts.KeyTaskSendHolder.Name()] {
		t.Fatalf("provides = %+v, want the lease and held-send keys", descriptor.Provides)
	}
	if len(descriptor.Provides) != 2 {
		t.Fatalf("provides = %d, want exactly two", len(descriptor.Provides))
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
