package host

// The host's built-in module inventory: the loaded assembly's kernel instances
// exposed to the diagnostics and plugins pages through
// contracts.KeyModuleLister. The lister is provided before the assembly loads
// and filled with the last loaded snapshot, so pages can read it during startup
// and after teardown (the snapshot is kept on purpose for diagnostics).

import (
	"sync"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// loadedModule is the host's read-only view of one kernel instance; the mapping
// from the kernel's *module.Instance is the part unit-tested here.
type loadedModule struct {
	EntryID  string
	ModuleID string
	State    string
}

// moduleInfos maps the loaded instances to the public read model in load order.
func moduleInfos(modules []loadedModule) []contracts.ModuleInfo {
	infos := make([]contracts.ModuleInfo, 0, len(modules))
	for _, loaded := range modules {
		infos = append(infos, contracts.ModuleInfo{
			EntryID:  loaded.EntryID,
			ModuleID: loaded.ModuleID,
			State:    loaded.State,
		})
	}
	return infos
}

// loadedModulesFromInstances reads the kernel instances in load order.
func loadedModulesFromInstances(instances []*module.Instance) []loadedModule {
	modules := make([]loadedModule, 0, len(instances))
	for _, instance := range instances {
		modules = append(modules, loadedModule{
			EntryID:  instance.ID(),
			ModuleID: instance.ModuleID(),
			State:    instance.State().String(),
		})
	}
	return modules
}

// moduleLister is the mutable contracts.ModuleLister the host fills after each
// assembly load. Modules returns a copy so callers cannot mutate the snapshot.
type moduleLister struct {
	mu      sync.RWMutex
	modules []contracts.ModuleInfo
}

var _ contracts.ModuleLister = (*moduleLister)(nil)

func newModuleLister() *moduleLister { return &moduleLister{} }

func (l *moduleLister) Modules() []contracts.ModuleInfo {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]contracts.ModuleInfo(nil), l.modules...)
}

// set replaces the snapshot (nil clears it); the last snapshot is otherwise kept
// through teardown for diagnostics.
func (l *moduleLister) set(modules []contracts.ModuleInfo) {
	l.mu.Lock()
	l.modules = append([]contracts.ModuleInfo(nil), modules...)
	l.mu.Unlock()
}

// provideModuleLister provides the lister on root before any assembly loads.
func provideModuleLister(root cordis.Context) (*moduleLister, error) {
	lister := newModuleLister()
	if _, err := root.Provide(contracts.KeyModuleLister, contracts.ModuleLister(lister)); err != nil {
		return nil, err
	}
	return lister, nil
}
