// Package presets defines ZeoKeel's declarative agent presets and personas
// (FP-06): a preset is a named combination of a tool allowlist, extra system
// prompt text, a persona prefix and an optional default model. Built-in
// presets map today's hard-coded agent modes (standard, minimal, creator) into
// data; users add or override presets with JSON files under
// <data dir>/presets/*.json.
//
// # TDD state (wave 3, STEP 0: RED)
//
// This package currently ships the domain types, the error contract and the
// acceptance tests only. Every exported function is a stub that returns a zero
// value, so `go test ./internal/presets` fails on real behaviour assertions
// (see presets_test.go). The next implementation round turns the same tests
// GREEN without changing their assertions:
//
//	go build ./internal/presets   # passes (stubs compile)
//	go test  ./internal/presets   # RED: 5 failing acceptance tests
//
// # Interface contract (planned wiring; owned by later rounds)
//
// Storage
//   - User presets: "<data dir>/presets/<id>.json", one preset per file, the
//     same JSON shape as Preset. Files are user data: a malformed file fails
//     the load with ErrInvalidJSON and keeps the in-memory registry untouched.
//   - Per-session selection: settings key "session.preset.<session id>" holds a
//     preset ID. Writes go through the existing store settings API; the
//     legacy "session.mode.<session id>" / "agent.preset" mode keys keep
//     working as aliases of the built-in presets until a migration exists.
//   - Default preset: settings key "agent.preset" (existing) stays the default
//     when a session has no explicit selection; a missing or unknown ID
//     resolves to PresetFallbackID with a warning (never an error, so a
//     deleted user preset cannot brick session open).
//
// Session / app API (consumed by fix-security, fix-frontend)
//   - Controller.Presets() *presets.Registry (nil-safe; rebuilt on load/reload)
//   - Controller.ReloadPresets() (*presets.Registry, error) — re-reads user
//     files; the active agent rebuilds on the next turn (same hook as a
//     plugin/mode change).
//   - Controller.SetSessionPreset(sessionID, presetID string) error — persists
//     "session.preset.<id>" and schedules a rebuild; unknown IDs are rejected
//     with ErrNotFound (UI shows the warning instead of silently falling back).
//   - Controller.SessionPresetID(sessionID) string — "" means "resolve from
//     agent.preset".
//   - The agent build path filters its registry with
//     Registry.VisibleTools(presetID, registeredNames): an empty allowlist
//     means every registered tool; a non-empty allowlist keeps registered order
//     and drops everything else. Unknown allowlist names are a load error, not
//     a silent drop.
//   - Prompt composition: Registry.PersonaSections(presetID) returns ordered
//     prompt.Section values. The persona section (ID PersonaSectionID,
//     Order == prompt.OrderPersonaPrefix) replaces the deployment persona
//     prefix for that session; the instructions section (ID PromptSectionID,
//     Order == OrderPresetPrompt) appends the preset's extra prompt text after
//     it. The app is expected to pass them into prompt composition as preset
//     sections, sorted with the existing Section.Order rules, so the composed
//     prompt order stays stable.
//
// UI (fix-frontend)
//   - Settings > Agent presets lists Registry.List() (built-ins first, then
//     user presets by Order then ID), shows validation warnings from Load,
//     offers "Open preset folder" and "Reload presets", and a per-session
//     preset picker in the composer/session header. The picker writes through
//     SetSessionPreset and shows the preset's persona/prompt summary.
package presets

import (
	"errors"

	"github.com/zeokeel/zeokeel/internal/prompt"
)

// PresetFallbackID is resolved when a session asks for an unknown or empty
// preset. It is also the first built-in preset, so a brand-new app keeps the
// historical standard agent behaviour.
const PresetFallbackID = "standard"

// Built-in preset IDs. They mirror the runtime agent modes so an existing
// installation keeps working before user presets are added.
const (
	BuiltinStandardID = "standard"
	BuiltinMinimalID  = "minimal"
	BuiltinCreatorID  = "creator"
)

// Prompt section identity/order contract for persona injection. PersonaSectionID
// is never "deployment:persona-prefix": a preset-scoped section only replaces
// the deployment persona for sessions that selected that preset.
const (
	// PersonaSectionID is the preset persona prefix section ID.
	PersonaSectionID = "preset:persona-prefix"
	// PromptSectionID is the preset extra-instructions section ID.
	PromptSectionID = "preset:instructions"
	// OrderPresetPrompt places a preset's extra instructions right after the
	// persona prefix and far before the deployment persona suffix.
	OrderPresetPrompt = prompt.OrderPersonaPrefix + 1
)

// Errors returned by load/validation. Callers classify with errors.Is; a
// load error must leave the previous registry untouched (fail closed).
var (
	// ErrInvalidPreset reports a preset missing a required field or violating
	// the ID/allowlist rules.
	ErrInvalidPreset = errors.New("presets: invalid preset")
	// ErrInvalidJSON reports a user preset file that is not a valid preset
	// JSON object. A broken user file never silently drops the built-ins.
	ErrInvalidJSON = errors.New("presets: invalid preset JSON")
	// ErrDuplicateID reports two user files declaring the same preset ID.
	ErrDuplicateID = errors.New("presets: duplicate preset id")
	// ErrUnknownTool reports an allowlist entry that is not a registered tool
	// (only checked when Options.KnownTools is provided).
	ErrUnknownTool = errors.New("presets: unknown tool in preset allowlist")
	// ErrNotFound reports Get/Resolve calls for an ID absent from the registry
	// (Resolve still returns the fallback preset plus a warning).
	ErrNotFound = errors.New("presets: preset not found")
)

// Preset is one declarative agent preset. The JSON tags are the on-disk user
// file shape (<data dir>/presets/*.json).
type Preset struct {
	// ID is the stable identifier a session stores. Required; lowercase
	// letters, digits, '.', '_' and '-' only.
	ID string `json:"id"`
	// Name is the human display name. Required.
	Name string `json:"name"`
	// Description is the one-line summary shown by the preset picker.
	Description string `json:"description,omitempty"`
	// Tools is the model-visible tool allowlist. Empty means "every
	// registered tool" (the historical standard behaviour); a non-empty list
	// keeps only named tools in registration order.
	Tools []string `json:"tools,omitempty"`
	// Prompt is extra system-prompt guidance appended after the persona.
	Prompt string `json:"prompt,omitempty"`
	// Persona is the persona prefix text for sessions that select this
	// preset. Empty keeps the deployment persona prefix.
	Persona string `json:"persona,omitempty"`
	// Model is the preset's default model ID. Empty means the app default.
	Model string `json:"model,omitempty"`
	// Order sorts user presets in the picker (built-ins keep their fixed
	// order); ties fall back to ID.
	Order int `json:"order,omitempty"`
}

// Options configures one registry load.
type Options struct {
	// UserDir is the ZeoKeel data directory; user presets are read from
	// <UserDir>/presets/*.json. Empty loads the built-ins only.
	UserDir string
	// KnownTools is the list of registered tool names used to validate
	// non-empty allowlists. Nil/empty skips allowlist validation (useful for
	// static callers such as the settings UI).
	KnownTools []string
}

// Registry is an immutable set of presets (built-ins merged with user files).
// The zero value is not a registry; use Load. All methods tolerate a nil
// receiver and report an empty/zero result so callers never panic on a failed
// load.
type Registry struct {
	// presets is the merged list in display order; byID is the lookup index.
	presets []Preset
	byID    map[string]Preset
}

// Builtins returns the built-in presets: standard (all tools), minimal
// (terminal-only allowlist) and creator (customization guidance). The slice is
// a copy; callers may not mutate the registry through it.
//
// TDD RED: returns nil until the next round implements the built-in catalog.
func Builtins() []Preset {
	return nil
}

// Load reads the built-in presets and merges the user preset files under
// <opts.UserDir>/presets/*.json. A user preset with a built-in ID overrides
// that built-in (field-wise: a value present in the user preset replaces the
// built-in field, an absent one keeps the built-in value). A missing user
// directory is not an error; a malformed file, duplicate user ID or invalid
// preset fails the whole load (fail closed, no partial registry).
//
// TDD RED: returns (nil, nil) until the next round implements loading.
func Load(opts Options) (*Registry, error) {
	return nil, nil
}

// List returns the merged presets in display order (built-ins first, then user
// presets by Order then ID). The slice is a copy.
//
// TDD RED: returns nil until implemented.
func (r *Registry) List() []Preset {
	if r == nil {
		return nil
	}
	return nil
}

// Get returns the preset with id and whether it exists.
//
// TDD RED: always reports false until implemented.
func (r *Registry) Get(id string) (Preset, bool) {
	if r == nil {
		return Preset{}, false
	}
	return Preset{}, false
}

// Resolve resolves a session's preset selection. Empty, unknown or deleted IDs
// resolve to the fallback preset (standard) with a non-empty warning that names
// the requested ID; a known ID resolves without warnings.
//
// TDD RED: returns a zero preset and no warnings until implemented.
func (r *Registry) Resolve(id string) (Preset, []string) {
	if r == nil {
		return Preset{}, nil
	}
	return Preset{}, nil
}

// VisibleTools applies presetID's tool allowlist to the registered tool names.
// An empty allowlist returns every registered name (same order); a non-empty
// allowlist returns only the named tools, in registration order. Unknown
// preset IDs return ErrNotFound; allowlist entries that were accepted at load
// time never silently drop here.
//
// TDD RED: returns (nil, nil) until implemented.
func (r *Registry) VisibleTools(presetID string, registered []string) ([]string, error) {
	if r == nil {
		return nil, ErrNotFound
	}
	return nil, nil
}

// PersonaSections returns the ordered prompt sections a session's preset
// contributes: the persona prefix (only when Persona is non-empty) and the
// preset instructions (only when Prompt is non-empty). Order is stable across
// calls and across registry reloads for the same content. Unknown preset IDs
// return nil (the caller resolves first, so this is not an error).
//
// TDD RED: returns nil until implemented.
func (r *Registry) PersonaSections(presetID string) []prompt.Section {
	if r == nil {
		return nil
	}
	return nil
}

// Validate reports whether the registry's presets are internally valid:
// every ID/Name is set, IDs are unique and match the allowed character set,
// and every non-empty tool allowlist only names known registered tools (when
// KnownTools was provided at load time).
//
// TDD RED: returns nil until implemented.
func (r *Registry) Validate() error {
	if r == nil {
		return ErrInvalidPreset
	}
	return nil
}
