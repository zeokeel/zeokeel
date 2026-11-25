package wechat

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestDescriptorMetadata pins the seam: the adapter provides KeyWeChat and
// requires only the inbox.
func TestDescriptorMetadata(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, ModuleID)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyWeChat.Name() {
		t.Fatalf("provides = %+v, want the wechat key", descriptor.Provides)
	}
	if len(descriptor.Requires) != 1 || descriptor.Requires[0].Name != contracts.KeyInbox.Name() {
		t.Fatalf("requires = %+v, want only the inbox", descriptor.Requires)
	}
}
