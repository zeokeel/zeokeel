package lead

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestDescriptorMetadata pins the seam: the triage pass provides KeyLead and
// requires both halves it joins (inbox and triage) plus the product data
// directory its rules are persisted in (a restart must not silently drop the
// operator's rules).
func TestDescriptorMetadata(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, ModuleID)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyLead.Name() {
		t.Fatalf("provides = %+v, want the lead key", descriptor.Provides)
	}
	required := map[string]bool{}
	for _, requirement := range descriptor.Requires {
		required[requirement.Name] = true
	}
	if !required[contracts.KeyInbox.Name()] || !required[contracts.KeyTriage.Name()] || !required[contracts.KeyDataDir.Name()] {
		t.Fatalf("requires = %v, want inbox + triage + data dir", required)
	}
}
