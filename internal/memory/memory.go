// Package memory is the model-free core of ZeoKeel's automatic-memory pipeline
// (docs/design/auto-memory.md §5). It provides content normalization/hashing,
// trigram similarity, secret scrubbing, candidate validation, the extraction
// prompt/parser, candidate merging, and the deterministic consolidation plan.
//
// It is a pure package: no SQL, no provider client, no session state. The app
// worker (internal/app/memory_auto.go) and the memory tool call these functions;
// the store only supplies and persists rows.
package memory

import (
	"fmt"
	"math"
	"strings"

	"github.com/zeokeel/zeokeel/internal/store"
)

// MaxCandidates is the per-turn extraction cap (§5: at most 3 candidates).
const MaxCandidates = 3

// MaxContentBytes bounds one memory body. It aliases the store write cap so a
// candidate accepted here can always be persisted.
const MaxContentBytes = store.MaxMemoryContentBytes

// Candidate is one memory proposed by the model-side extractor. Its JSON shape
// is the extraction schema the prompt asks the model to emit.
type Candidate struct {
	Scope      string  `json:"scope"`
	Kind       string  `json:"kind"`
	Content    string  `json:"content"`
	Confidence float64 `json:"confidence"`
}

// Message is one recent conversation turn used to build the extraction prompt.
// Role is the model role ("user"/"assistant") and Content is its text.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Memory scopes, kinds, statuses and sources. They alias the store schema v3
// constants so the wire/database values have exactly one definition.
const (
	ScopeGlobal    = store.MemoryScopeGlobal
	ScopeWorkspace = store.MemoryScopeWorkspace

	KindUser      = store.MemoryKindUser
	KindFeedback  = store.MemoryKindFeedback
	KindProject   = store.MemoryKindProject
	KindReference = store.MemoryKindReference

	StatusActive     = store.MemoryStatusActive
	StatusPending    = store.MemoryStatusPending
	StatusSuperseded = store.MemoryStatusSuperseded

	SourceManual = store.MemorySourceManual
	SourceAuto   = store.MemorySourceAuto
)

// Mode is one memory write mode, mirroring the ui.memory_*_mode settings.
type Mode string

// Supported write modes.
const (
	ModeOff     Mode = "off"
	ModeSuggest Mode = "suggest"
	ModeAuto    Mode = "auto"
)

var validScopes = map[string]bool{
	ScopeGlobal:    true,
	ScopeWorkspace: true,
}

var validKinds = map[string]bool{
	KindUser:      true,
	KindFeedback:  true,
	KindProject:   true,
	KindReference: true,
}

// NormalizeMode canonicalizes a raw settings value. Unknown values fail closed
// to off, matching the prompt/tool contract.
func NormalizeMode(raw string) Mode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(ModeAuto):
		return ModeAuto
	case string(ModeSuggest):
		return ModeSuggest
	default:
		return ModeOff
	}
}

// StatusForMode maps a write mode to the status a newly written memory starts
// in. allowed is false for off, when nothing may be written.
func StatusForMode(mode Mode) (status string, allowed bool) {
	switch NormalizeMode(string(mode)) {
	case ModeAuto:
		return StatusActive, true
	case ModeSuggest:
		return StatusPending, true
	default:
		return "", false
	}
}

// DefaultScope is the scope used when a candidate omits it: workspace, the
// conservative project-local choice.
func DefaultScope() string { return ScopeWorkspace }

// DefaultKind is the kind used when a candidate omits it.
func DefaultKind() string { return KindProject }

// ValidateScope reports whether scope is global or workspace. Matching is
// case-insensitive and ignores surrounding whitespace.
func ValidateScope(scope string) error {
	if validScopes[strings.ToLower(strings.TrimSpace(scope))] {
		return nil
	}
	return fmt.Errorf("memory: invalid scope %q (want %q or %q)", scope, ScopeGlobal, ScopeWorkspace)
}

// ValidateScopes validates every scope in scopes.
func ValidateScopes(scopes ...string) error {
	for _, scope := range scopes {
		if err := ValidateScope(scope); err != nil {
			return err
		}
	}
	return nil
}

// ValidateKind reports whether kind is one of the supported memory kinds.
func ValidateKind(kind string) error {
	if validKinds[strings.ToLower(strings.TrimSpace(kind))] {
		return nil
	}
	return fmt.Errorf("memory: invalid kind %q (want %q, %q, %q or %q)",
		kind, KindUser, KindFeedback, KindProject, KindReference)
}

// ValidateCandidate normalizes one candidate and rejects invalid ones. Missing
// scope/kind get their defaults; confidence is clamped into [0, 1] (NaN becomes
// 0); content is normalized with NormalizeContent.
func ValidateCandidate(c Candidate) (Candidate, error) {
	scope := strings.ToLower(strings.TrimSpace(c.Scope))
	if scope == "" {
		scope = DefaultScope()
	}
	if err := ValidateScope(scope); err != nil {
		return Candidate{}, err
	}
	kind := strings.ToLower(strings.TrimSpace(c.Kind))
	if kind == "" {
		kind = DefaultKind()
	}
	if err := ValidateKind(kind); err != nil {
		return Candidate{}, err
	}
	content, err := NormalizeContent(c.Content)
	if err != nil {
		return Candidate{}, err
	}
	confidence := c.Confidence
	if math.IsNaN(confidence) || confidence < 0 {
		confidence = 0
	} else if confidence > 1 {
		confidence = 1
	}
	return Candidate{Scope: scope, Kind: kind, Content: content, Confidence: confidence}, nil
}
