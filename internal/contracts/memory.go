package contracts

import (
	cordis "github.com/zeokeel/zeokeel-kernel"
	rig "github.com/zeokeel/zeokeel-rig"
)

// Memory is one stored memory entry (a rig semantic memory: the text, its
// scope and its creation time).
type Memory = rig.Memory

// MemoryStore is the product's durable memory surface: the rig MemoryStore the
// agent's memory tools read and write, plus what the UI needs to manage it.
type MemoryStore interface {
	rig.MemoryStore
}

// KeyMemory is provided by the memory backend module and consumed by the
// session Memory tab (and registered into the toolkit as the rig memory tools'
// store).
var KeyMemory = cordis.NewServiceKey[MemoryStore]("zeokeel/memory")
