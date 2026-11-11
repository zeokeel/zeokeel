package contracts

import (
	"context"
	"strings"
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

// MemoryUpdater is the optional update half of the memory surface: it replaces
// one entry's text while keeping the entry's ID and creation time. The rig
// MemoryStore has no update action, so the settings/session memory UIs
// type-assert a MemoryStore to MemoryUpdater; a backend that cannot edit
// entries simply does not implement it and the edit controls stay hidden.
type MemoryUpdater interface {
	// Update replaces the text of the memory with the given ID. The scope is
	// optional: an empty scope keeps the entry's current scope, a non-empty
	// one is validated like any stored scope. Unknown IDs, empty text and
	// invalid scopes are errors; a successful update returns the stored entry
	// (same ID and creation time).
	Update(ctx context.Context, id, text, scope string) (Memory, error)
}

// Memory scope values. An entry added with scope "workspace" is persisted with
// the identity of the workspace current at write time ("workspace:<id>"), so a
// "workspace" memory is only listed inside that workspace. "global" entries are
// visible everywhere. Legacy entries whose scope is the bare "workspace" (written
// before workspace identity existed) or any other historical scope stay visible
// so upgrading never hides existing data.
const (
	// MemoryScopeGlobal marks an entry visible in every workspace.
	MemoryScopeGlobal = "global"
	// MemoryScopeWorkspace is the unbound workspace scope (legacy rows and
	// callers that ask for "the current workspace" before an id is known).
	MemoryScopeWorkspace = "workspace"
	// MemoryScopeWorkspacePrefix prefixes a bound workspace scope.
	MemoryScopeWorkspacePrefix = "workspace:"
)

// MemoryWorkspaceScope renders the persisted scope for a bound workspace.
func MemoryWorkspaceScope(workspaceID string) string {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return MemoryScopeWorkspace
	}
	return MemoryScopeWorkspacePrefix + workspaceID
}

// MemoryScopeWorkspaceID extracts the workspace identity of a bound scope.
func MemoryScopeWorkspaceID(scope string) (string, bool) {
	scope = strings.TrimSpace(scope)
	if !strings.HasPrefix(strings.ToLower(scope), MemoryScopeWorkspacePrefix) {
		return "", false
	}
	id := strings.TrimSpace(scope[len(MemoryScopeWorkspacePrefix):])
	if id == "" {
		return "", false
	}
	return id, true
}

// MemoryAllLister is the optional management half of the memory store: it lists
// every entry of every workspace so the settings page can manage them, while the
// model-facing List/Search stay scoped to the current workspace.
type MemoryAllLister interface {
	ListAll(ctx context.Context) ([]Memory, error)
}

// MemoryVersioner is the optional change-counter half of the memory store: it
// advances on every successful mutation so the UI can notice entries written
// out of band (the model's memory tool) and refresh its cached read model.
type MemoryVersioner interface {
	Version() uint64
}

// MemoryRecoveryReporter is the optional recovery half of the memory store: it
// reports a quarantined memory file so the UI can tell the user their previous
// entries were backed up instead of silently dropped. An empty notice means the
// store loaded healthy.
type MemoryRecoveryReporter interface {
	RecoveryNotice() string
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

// SettingMemoryExtractEnabled enables model-driven memory extraction (only the
// exact "true" value enables it; default off).
const SettingMemoryExtractEnabled = "memory.extract"

// MemoryExtractionService turns recent session messages into new stored
// memories through the model (the session Memory tab's opt-in extract action).
type MemoryExtractionService interface {
	// ExtractFromSession extracts at most max memories from sessionID and adds
	// the non-duplicate ones to the memory store. max<=0 means 5, capped at 5.
	ExtractFromSession(ctx context.Context, sessionID string, max int) ([]Memory, error)
}

// KeyMemoryExtractor is provided by the agent module when a memory store is
// available.
var KeyMemoryExtractor = cordis.NewServiceKey[MemoryExtractionService]("zeokeel/memory/extract")
