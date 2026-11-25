package skilldraft

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestDescriptorMetadata pins the seam: the drafts store must provide
// contracts.KeySkillDrafts and require the product data directory (a draft
// outlives the task that proposed it). It must not provide the skills editor —
// accepting a draft records a decision, it does not install anything.
func TestDescriptorMetadata(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, ModuleID)
	}
	if len(descriptor.Provides) != 1 {
		t.Fatalf("provides = %+v, want exactly the skill drafts key", descriptor.Provides)
	}
	if name := descriptor.Provides[0].Name; name != contracts.KeySkillDrafts.Name() {
		t.Fatalf("provides = %q, want %q", name, contracts.KeySkillDrafts.Name())
	}
	if name := descriptor.Provides[0].Name; name == contracts.KeySkillsEditor.Name() {
		t.Fatalf("provides = %q: the drafts store must not provide the skills editor", name)
	}
	if len(descriptor.Requires) != 1 || descriptor.Requires[0].Name != contracts.KeyDataDir.Name() {
		t.Fatalf("requires = %+v, want exactly the data dir", descriptor.Requires)
	}
	if descriptor.Name == "" {
		t.Fatal("descriptor has no display name")
	}
}
