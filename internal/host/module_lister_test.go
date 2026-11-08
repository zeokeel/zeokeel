package host

// Tests for the host's module lister: the loaded-instance → ModuleInfo mapping
// (order/state), the snapshot store and the service provision before assembly
// load. The real loader is exercised by the product smokes; these tests pin the
// mapping and the headless/desktop wiring contract.

import (
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestModuleInfosMapping(t *testing.T) {
	modules := []loadedModule{
		{EntryID: "appinfo", ModuleID: "zeokeel/appinfo", State: "active"},
		{EntryID: "store", ModuleID: "zeokeel/sessionstore", State: "active"},
		{EntryID: "approvals", ModuleID: "zeokeel/approvals", State: "failed"},
	}
	infos := moduleInfos(modules)
	if len(infos) != len(modules) {
		t.Fatalf("infos = %+v, want %d entries", infos, len(modules))
	}
	for index, info := range infos {
		if info.EntryID != modules[index].EntryID || info.ModuleID != modules[index].ModuleID ||
			info.State != modules[index].State {
			t.Fatalf("info[%d] = %+v, want %+v", index, info, modules[index])
		}
	}
	if len(moduleInfos(nil)) != 0 {
		t.Fatal("moduleInfos(nil) is not empty")
	}
}

func TestModuleListerStoresSnapshot(t *testing.T) {
	lister := newModuleLister()
	if modules := lister.Modules(); len(modules) != 0 {
		t.Fatalf("empty lister modules = %+v", modules)
	}
	snapshot := []contracts.ModuleInfo{
		{EntryID: "one", ModuleID: "mod/one", State: "active"},
		{EntryID: "two", ModuleID: "mod/two", State: "disposed"},
	}
	lister.set(snapshot)
	got := lister.Modules()
	if len(got) != 2 || got[0].EntryID != "one" || got[1].State != "disposed" {
		t.Fatalf("lister modules = %+v", got)
	}
	// Modules returns a copy: mutating it does not change the snapshot.
	got[0].EntryID = "mutated"
	if again := lister.Modules(); again[0].EntryID != "one" {
		t.Fatalf("snapshot mutated through Modules(): %+v", again)
	}
	// The last snapshot is kept for diagnostics after teardown.
	lister.set(nil)
	if modules := lister.Modules(); len(modules) != 0 {
		t.Fatalf("lister after empty snapshot = %+v", modules)
	}
}

func TestProvideModuleLister(t *testing.T) {
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	lister, err := provideModuleLister(root)
	if err != nil {
		t.Fatalf("provideModuleLister: %v", err)
	}
	if lister == nil {
		t.Fatal("provideModuleLister returned no lister")
	}
	provided, err := root.Require(contracts.KeyModuleLister)
	if err != nil {
		t.Fatalf("require module lister: %v", err)
	}
	if provided.Modules() == nil && lister.Modules() != nil {
		t.Fatal("provided lister is a different instance")
	}
	lister.set([]contracts.ModuleInfo{{EntryID: "x", ModuleID: "m", State: "active"}})
	if got := provided.Modules(); len(got) != 1 || got[0].EntryID != "x" {
		t.Fatalf("provided lister modules = %+v", got)
	}
}
