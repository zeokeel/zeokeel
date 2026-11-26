package notifyroute

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestDescriptorMetadata pins the seam: the router provides KeyNotifyRoute and
// must never require a channel (channels read it, not the other way round). Its
// settings dependency is optional — persistence is a convenience, routing works
// without it.
func TestDescriptorMetadata(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, ModuleID)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyNotifyRoute.Name() {
		t.Fatalf("provides = %+v, want the notify route key", descriptor.Provides)
	}
	for _, requirement := range descriptor.Requires {
		if requirement.Name != contracts.KeySettings.Name() || !requirement.Optional {
			t.Fatalf("requires = %+v, want only an optional settings dependency", descriptor.Requires)
		}
	}
}
