package notifyroute

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestDescriptorMetadata pins the seam: the router provides KeyNotifyRoute and
// depends on nothing (channels read it; it must not require a channel).
func TestDescriptorMetadata(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, ModuleID)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyNotifyRoute.Name() {
		t.Fatalf("provides = %+v, want the notify route key", descriptor.Provides)
	}
	if len(descriptor.Requires) != 0 {
		t.Fatalf("requires = %+v, want none", descriptor.Requires)
	}
}
