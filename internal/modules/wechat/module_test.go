package wechat

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestDescriptorMetadata pins the seam: the adapter provides KeyWeChat, requires
// the inbox, and requires the product data dir because the login token must
// survive a restart (token.json lives under <dataDir>/wechat).
func TestDescriptorMetadata(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, ModuleID)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyWeChat.Name() {
		t.Fatalf("provides = %+v, want the wechat key", descriptor.Provides)
	}
	required := make(map[string]bool, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		required[requirement.Name] = true
		if requirement.Optional {
			t.Fatalf("requirement %q is optional, want required", requirement.Name)
		}
	}
	if len(required) != 2 || !required[contracts.KeyInbox.Name()] || !required[contracts.KeyDataDir.Name()] {
		t.Fatalf("requires = %+v, want the inbox and the data dir", descriptor.Requires)
	}
}
