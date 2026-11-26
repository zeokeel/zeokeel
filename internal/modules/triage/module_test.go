package triage

import (
	"testing"

	"github.com/zeokeel/zeokeel-kernel/module"

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

// TestDescriptorDeclaresTheChatDependency pins the declaration the dispatch
// wake-up needs.
//
// The chat service is legitimately optional (a product assembled without it
// still triages, it just never runs anything by itself), so this requirement must
// stay non-gating. It still has to be *declared*: the loader builds its schedule
// from declared requirements and places every provider before its consumer, which
// is the only thing that makes an activation-time probe of the service correct.
//
// The bug this pins: the dependency existed only in a comment, triage activated
// before agent (entry 11 vs 32), the probe found nothing, and every dispatch to
// the lead role was silently inert.
func TestDescriptorDeclaresTheChatDependency(t *testing.T) {
	descriptor := Module()
	var chat *module.Requirement
	for i := range descriptor.Requires {
		if descriptor.Requires[i].Name == contracts.KeyChat.Name() {
			chat = &descriptor.Requires[i]
			break
		}
	}
	if chat == nil {
		t.Fatalf("requires = %+v, want the chat service declared: an undeclared dependency lets triage "+
			"activate before its provider and the wake-up connector becomes a silent nil",
			descriptor.Requires)
	}
	if !chat.Optional {
		t.Fatal("the chat requirement must be optional: a product without a chat service still triages")
	}
}
