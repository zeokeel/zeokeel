package contracts

import (
	"context"
	"time"

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

// MemoryPolicy bounds one deterministic memory consolidation run. A zero value
// keeps every entry (MaxEntries 0 = unlimited, MaxAge 0 = no expiry).
type MemoryPolicy struct {
	// MaxEntries is the maximum number of entries to keep (0 = unlimited).
	MaxEntries int
	// MaxAge is the retention window; entries created before now-MaxAge are
	// removed (0 = no expiry).
	MaxAge time.Duration
}

// MemoryConsolidationReport summarizes one consolidation run.
type MemoryConsolidationReport struct {
	// RemovedExpired counts entries deleted by the MaxAge window.
	RemovedExpired int
	// RemovedOverCap counts entries deleted by the MaxEntries cap.
	RemovedOverCap int
	// Remaining is the entry count after consolidation.
	Remaining int
}

// MemoryConsolidator is the optional half of the memory store that runs a
// deterministic consolidation (no model call). The settings Memory page calls
// it with the user-configured policy.
type MemoryConsolidator interface {
	Consolidate(ctx context.Context, policy MemoryPolicy) (MemoryConsolidationReport, error)
}

// SettingMemoryMaxEntries stores the consolidation cap (0/empty = unlimited).
const SettingMemoryMaxEntries = "memory.maxEntries"

// SettingMemoryMaxAgeDays stores the retention window in days (0/empty = no
// expiry).
const SettingMemoryMaxAgeDays = "memory.maxAgeDays"
