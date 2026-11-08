package regression

// Adversarial audit: the host built-in module inventory. The host package
// unit-tests the mapping from kernel instances; this regression-layer file
// reaches the unexported lister through test-only //go:linkname declarations and
// pins the snapshot semantics: load order preserved, states passed through, and
// Modules returns a defensive copy while the last snapshot is kept (also after a
// clear/set).

import (
	"reflect"
	"testing"
	"unsafe"
	_ "unsafe"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// hostLoadedModule mirrors host.loadedModule for the moduleInfos mapping check.
type hostLoadedModule struct {
	EntryID  string
	ModuleID string
	State    string
}

//go:linkname hostNewModuleLister github.com/zeokeel/zeokeel/internal/host.newModuleLister
func hostNewModuleLister() unsafe.Pointer

//go:linkname hostModuleListerSet github.com/zeokeel/zeokeel/internal/host.(*moduleLister).set
func hostModuleListerSet(lister unsafe.Pointer, modules []contracts.ModuleInfo)

//go:linkname hostModuleListerModules github.com/zeokeel/zeokeel/internal/host.(*moduleLister).Modules
func hostModuleListerModules(lister unsafe.Pointer) []contracts.ModuleInfo

//go:linkname hostModuleInfos github.com/zeokeel/zeokeel/internal/host.moduleInfos
func hostModuleInfos(modules []hostLoadedModule) []contracts.ModuleInfo

// TestHostModuleInfosMapping covers the kernel-instance mapping: load order is
// preserved and entry/module id/state pass through unchanged.
func TestHostModuleInfosMapping(t *testing.T) {
	infos := hostModuleInfos([]hostLoadedModule{
		{EntryID: "appinfo", ModuleID: "zeokeel/appinfo", State: "active"},
		{EntryID: "store", ModuleID: "zeokeel/sessionstore", State: "failed"},
	})
	if len(infos) != 2 {
		t.Fatalf("moduleInfos length = %d, want 2", len(infos))
	}
	want := []contracts.ModuleInfo{
		{EntryID: "appinfo", ModuleID: "zeokeel/appinfo", State: "active"},
		{EntryID: "store", ModuleID: "zeokeel/sessionstore", State: "failed"},
	}
	if !reflect.DeepEqual(infos, want) {
		t.Fatalf("moduleInfos = %+v, want %+v", infos, want)
	}
	if empty := hostModuleInfos(nil); len(empty) != 0 {
		t.Fatalf("moduleInfos(nil) = %+v, want empty", empty)
	}
}

// TestHostModuleListerSnapshot covers the lister: an empty start, ordered
// snapshot, defensive copies, replacement and the kept last snapshot.
func TestHostModuleListerSnapshot(t *testing.T) {
	lister := hostNewModuleLister()
	if lister == nil {
		t.Fatal("newModuleLister returned nil")
	}
	if got := hostModuleListerModules(lister); len(got) != 0 {
		t.Fatalf("empty lister snapshot = %+v", got)
	}

	first := []contracts.ModuleInfo{
		{EntryID: "a", ModuleID: "mod/a", State: "active"},
		{EntryID: "b", ModuleID: "mod/b", State: "failed"},
	}
	hostModuleListerSet(lister, first)
	first[0].EntryID = "mutated-input"
	got := hostModuleListerModules(lister)
	if len(got) != 2 || got[0].EntryID != "a" || got[1].EntryID != "b" {
		t.Fatalf("snapshot after set = %+v (want the stored copy, order preserved)", got)
	}

	// The returned slice must be a copy: mutating it cannot corrupt the snapshot.
	got[0].EntryID = "mutated-output"
	if again := hostModuleListerModules(lister); again[0].EntryID != "a" {
		t.Fatalf("Modules returned the internal slice: %+v", again)
	}

	// Replacing the snapshot keeps the newest snapshot; clearing yields empty.
	hostModuleListerSet(lister, []contracts.ModuleInfo{{EntryID: "c", ModuleID: "mod/c", State: "disposed"}})
	if got := hostModuleListerModules(lister); len(got) != 1 || got[0].EntryID != "c" {
		t.Fatalf("snapshot after replace = %+v", got)
	}
	hostModuleListerSet(lister, nil)
	if got := hostModuleListerModules(lister); len(got) != 0 {
		t.Fatalf("snapshot after clear = %+v", got)
	}
	// The last non-nil snapshot is not resurrected by later reads.
	if got := hostModuleListerModules(lister); len(got) != 0 {
		t.Fatalf("cleared lister returned %+v", got)
	}
}
