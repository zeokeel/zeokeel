package contracts

import cordis "github.com/zeokeel/zeokeel-kernel"

// ModuleInfo describes one loaded kernel module instance (the desktop's
// built-in plugin surface).
type ModuleInfo struct {
	// EntryID is the stable assembly entry id.
	EntryID string
	// ModuleID is the compile-time module identity.
	ModuleID string
	// State is the fiber lifecycle state name ("active", "disposed", ...).
	State string
}

// ModuleLister exposes the loaded module instances to the UI (diagnostics and
// the plugins page's built-in section).
type ModuleLister interface {
	Modules() []ModuleInfo
}

// KeyModuleLister is provided by the process host after the assembly loads.
var KeyModuleLister = cordis.NewServiceKey[ModuleLister]("zeokeel/modules")
