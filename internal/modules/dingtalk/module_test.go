package dingtalk

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestDescriptorMetadata pins the seam: the sender provides KeyDingTalk and
// requires nothing — in particular not the inbox, because DingTalk has no
// inbound path any more (an ingress requirement here would be the first half of
// quietly re-adding one).
func TestDescriptorMetadata(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, ModuleID)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyDingTalk.Name() {
		t.Fatalf("provides = %+v, want the dingtalk key", descriptor.Provides)
	}
	if len(descriptor.Requires) != 0 {
		t.Fatalf("requires = %+v, want none (the sender must not depend on the inbox)", descriptor.Requires)
	}
}
