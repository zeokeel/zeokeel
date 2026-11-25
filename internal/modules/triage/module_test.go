package triage

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestDescriptorMetadata pins the seam: triage must provide KeyTriage and
// require both sides it joins (inbox and task board). If someone drops a
// Require, the module may boot before its dependency and the failure would only
// show up at the first real triage.
func TestDescriptorMetadata(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, ModuleID)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyTriage.Name() {
		t.Fatalf("provides = %+v, want the triage key", descriptor.Provides)
	}
	required := map[string]bool{}
	for _, requirement := range descriptor.Requires {
		required[requirement.Name] = true
	}
	if !required[contracts.KeyInbox.Name()] || !required[contracts.KeyTaskBoard.Name()] {
		t.Fatalf("requires = %v, want inbox and task board", required)
	}
}
