package assembly

import (
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
)

// GroupModuleID is the module ID of the no-op group container.
const GroupModuleID = "zeokeel/host/group"

// GroupConfig is the (empty) typed config of a group entry.
type GroupConfig struct{}

// GroupModule returns the no-op module that carries an Entry.Children subtree.
//
// A group owns no services and runs no logic. It exists so an assembly can
// nest a subtree (the UI group) under one entry that can be configured,
// disabled and disposed as a unit: disposing the group disposes the whole
// subtree through the kernel's child ownership, which runs every child's
// effects (contribution unregistration) in reverse order.
func GroupModule() *module.Descriptor[GroupConfig] {
	return module.Define(GroupModuleID, func(cordis.Context, GroupConfig) error {
		return nil
	}).WithName("ZeoKeel module group")
}
