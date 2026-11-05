// Package presets defines ZeoKeel's declarative agent presets and personas
// (FP-06): a preset is a named combination of a tool allowlist, extra system
// prompt text, a persona prefix and an optional default model. Built-in
// presets map today's hard-coded agent modes (standard, minimal, creator) into
// data; users add or override presets with JSON files under
// <data dir>/presets/*.json.
//
// # Status (wave 3, STEP 1: GREEN)
//
// The catalog is implemented and `go test ./internal/presets` is green. The
// acceptance tests pin the contract: built-in catalog, user JSON merge
// (field-wise override, fail-closed on a broken file), tool allowlists,
// resolution/validation and ordered persona/prompt prompt sections.
//
// # Interface contract (planned wiring; owned by later rounds)
//
// Storage
//   - User presets: "<data dir>/presets/<id>.json", one preset per file, the
//     same JSON shape as Preset (see testdata/reviewer.json). Files are user
//     data: a malformed file fails the load with ErrInvalidJSON and keeps the
//     previous in-memory registry untouched; two user files declaring the same
//     ID fail with ErrDuplicateID. A user preset with a built-in ID overrides
//     that built-in field-wise (fields absent from the file keep the built-in
//     value; `"tools": []` deliberately clears the allowlist).
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
//     a silent drop (when KnownTools is provided at load time).
//   - Prompt composition: Registry.PersonaSections(presetID) returns ordered
//     prompt.Section values. The persona section (ID PersonaSectionID,
//     Order == prompt.OrderPersonaPrefix) replaces the deployment persona
//     prefix for that session; the instructions section (ID PromptSectionID,
//     Order == OrderPresetPrompt) appends the preset's extra prompt text after
//     it. The app is expected to pass them into prompt composition as preset
//     sections, sorted with the existing Section.Order rules, so the composed
//     prompt order stays stable.
//   - Model: a preset's Model is the default model for sessions that selected
//     it and have no explicit per-session model; empty keeps the app default.
//
// UI (fix-frontend)
//   - Settings > Agent presets lists Registry.List() (built-ins first, then
//     user presets by Order then ID), shows validation warnings from Load,
//     offers "Open preset folder" and "Reload presets", and a per-session
//     preset picker in the composer/session header. The picker writes through
//     SetSessionPreset and shows the preset's persona/prompt summary.
package presets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

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

// presetsDirName is the user preset directory inside the ZeoKeel data dir.
const presetsDirName = "presets"

// userPresetPattern restricts preset IDs to one safe file-name-like element.
// It also makes "<id>.json" a predictable file name for the UI's "open folder"
// affordance.
var userPresetPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

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
	presets []Preset
	byID    map[string]Preset
}

// builtins is the fixed built-in catalog. It is the single source of truth for
// the built-in IDs/order; Builtins returns a deep copy.
var builtins = []Preset{
	{
		ID:          BuiltinStandardID,
		Name:        "Standard",
		Description: "The full built-in tool set with the default persona and model.",
	},
	{
		ID:          BuiltinMinimalID,
		Name:        "Minimal",
		Description: "Terminal-only tool set for small, focused sessions.",
		Tools: []string{
			"terminal_open", "terminal_list", "terminal_read", "terminal_send",
			"terminal_signal", "terminal_close", "read_image", "working_directory",
		},
	},
	{
		ID:          BuiltinCreatorID,
		Name:        "Creator",
		Description: "Customize ZeoKeel through conversation and write reusable skills.",
		Prompt: "You customize ZeoKeel. Prefer changing whitelisted settings through the " +
			"settings tools, and write reusable capabilities as skills under " +
			"<workspace>/skills/<name>/SKILL.md with the standard file tools. " +
			"Explain each change and keep the user's existing configuration intact unless " +
			"they ask for it to change.",
	},
}

// Builtins returns the built-in presets: standard (all tools), minimal
// (terminal-only allowlist) and creator (customization guidance). The slice is
// a deep copy; callers may not mutate the catalog through it.
func Builtins() []Preset {
	return clonePresets(builtins)
}

// Load reads the built-in presets and merges the user preset files under
// <opts.UserDir>/presets/*.json.
//
// Override rule: a user preset with a built-in ID overrides that built-in
// field-wise. A JSON field present in the file replaces the built-in field; an
// absent field keeps the built-in value, so an override may be as small as
// {"id":"minimal","tools":["read_file"]} and still inherit the built-in name,
// description, prompt and persona. `"tools": []` deliberately clears the
// allowlist (= every registered tool). Validation runs on the merged preset, so
// a built-in's required name does not have to be repeated in the override; a
// brand-new user preset (no built-in with that ID) must carry both id and name.
// A missing user directory is not an error; a malformed file, duplicate user
// ID or invalid preset fails the whole load (fail closed, no partial registry).
func Load(opts Options) (*Registry, error) {
	known := normalizeToolNames(opts.KnownTools)
	merged := clonePresets(builtins)
	index := make(map[string]int, len(merged))
	for i := range merged {
		index[merged[i].ID] = i
	}

	userDir := strings.TrimSpace(opts.UserDir)
	var userPresets []Preset
	if userDir != "" {
		dir := filepath.Join(userDir, presetsDirName)
		entries, err := os.ReadDir(dir)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("presets: read user preset directory %q: %w", dir, err)
		}
		// Only regular *.json files are presets; README/notes files are
		// ignored so the folder can document itself.
		var names []string
		for _, entry := range entries {
			if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
				continue
			}
			names = append(names, entry.Name())
		}
		sort.Strings(names)

		seen := make(map[string]string, len(names))
		for _, name := range names {
			path := filepath.Join(dir, name)
			preset, fields, perr := parseUserPresetFile(path)
			if perr != nil {
				return nil, perr
			}
			if preset.ID != "" {
				if first, ok := seen[preset.ID]; ok {
					return nil, fmt.Errorf("%w: %q is declared by both %s and %s",
						ErrDuplicateID, preset.ID, first, name)
				}
				seen[preset.ID] = name
			}
			userPresets = append(userPresets, preset)
			if i, ok := index[preset.ID]; ok {
				// Override a built-in: merge first, validate the merged result.
				// The user file may omit any field (name/description/tools/...),
				// so the built-in value is inherited and required-field checks
				// run on what the session will actually see (PD-04 P3: an
				// override without "name" must not fail).
				mergedPreset := mergePreset(merged[i], preset, fields)
				if err := validatePreset(mergedPreset, nil, nil); err != nil {
					return nil, fmt.Errorf("%s: %w", path, err)
				}
				// Tool membership is checked only for the allowlist the user
				// file actually declares: inherited built-in tools are part of
				// the product contract and may name tools outside a caller's
				// live registry subset (same rule as Validate).
				if fields["tools"] {
					if err := validateToolAllowlist(preset.ID, preset.Tools, known); err != nil {
						return nil, fmt.Errorf("%s: %w", path, err)
					}
				}
				merged[i] = mergedPreset
				continue
			}
			// A brand-new preset is validated on its own: id and name are
			// required because there is no built-in to inherit them from.
			if err := validatePreset(preset, fields, known); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			index[preset.ID] = len(merged)
			merged = append(merged, preset)
		}
	}

	// Built-ins keep their fixed display order; user-only presets are sorted
	// by Order then ID after them.
	out := make([]Preset, 0, len(merged))
	rest := make([]Preset, 0, len(userPresets))
	for _, p := range merged {
		if isBuiltinID(p.ID) {
			out = append(out, p)
			continue
		}
		rest = append(rest, p)
	}
	sort.SliceStable(rest, func(i, j int) bool {
		if rest[i].Order != rest[j].Order {
			return rest[i].Order < rest[j].Order
		}
		return rest[i].ID < rest[j].ID
	})
	out = append(out, rest...)

	reg := &Registry{presets: out, byID: make(map[string]Preset, len(out))}
	for _, p := range out {
		reg.byID[p.ID] = p
	}
	if err := reg.Validate(); err != nil {
		return nil, err
	}
	return reg, nil
}

// parseUserPresetFile decodes one user preset JSON object while tracking which
// fields the file actually declared, so a same-ID override is field-wise and an
// omitted field keeps the built-in value (`"tools": []` still clears it).
func parseUserPresetFile(path string) (Preset, map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Preset{}, nil, fmt.Errorf("presets: read user preset %q: %w", path, err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Preset{}, nil, fmt.Errorf("%w: %s: %v", ErrInvalidJSON, path, err)
	}
	if raw == nil {
		return Preset{}, nil, fmt.Errorf("%w: %s: preset must be a JSON object", ErrInvalidJSON, path)
	}
	var preset Preset
	fields := make(map[string]bool, len(raw))
	decode := func(key string, dst any) error {
		value, ok := raw[key]
		if !ok {
			return nil
		}
		if err := json.Unmarshal(value, dst); err != nil {
			return fmt.Errorf("%w: %s: field %q: %v", ErrInvalidJSON, path, key, err)
		}
		fields[key] = true
		return nil
	}
	if err := decode("id", &preset.ID); err != nil {
		return Preset{}, nil, err
	}
	if err := decode("name", &preset.Name); err != nil {
		return Preset{}, nil, err
	}
	if err := decode("description", &preset.Description); err != nil {
		return Preset{}, nil, err
	}
	if err := decode("tools", &preset.Tools); err != nil {
		return Preset{}, nil, err
	}
	if err := decode("prompt", &preset.Prompt); err != nil {
		return Preset{}, nil, err
	}
	if err := decode("persona", &preset.Persona); err != nil {
		return Preset{}, nil, err
	}
	if err := decode("model", &preset.Model); err != nil {
		return Preset{}, nil, err
	}
	if err := decode("order", &preset.Order); err != nil {
		return Preset{}, nil, err
	}
	return preset, fields, nil
}

// mergePreset applies the fields one user preset declared to base.
func mergePreset(base, user Preset, fields map[string]bool) Preset {
	out := clonePreset(base)
	if fields["id"] {
		out.ID = user.ID
	}
	if fields["name"] {
		out.Name = user.Name
	}
	if fields["description"] {
		out.Description = user.Description
	}
	if fields["tools"] {
		out.Tools = append([]string(nil), user.Tools...)
	}
	if fields["prompt"] {
		out.Prompt = user.Prompt
	}
	if fields["persona"] {
		out.Persona = user.Persona
	}
	if fields["model"] {
		out.Model = user.Model
	}
	if fields["order"] {
		out.Order = user.Order
	}
	return out
}

// validatePreset checks one preset's required fields and allowlist.
func validatePreset(p Preset, _ map[string]bool, knownTools []string) error {
	id := strings.TrimSpace(p.ID)
	if id == "" {
		return fmt.Errorf("%w: preset id is required", ErrInvalidPreset)
	}
	if !userPresetPattern.MatchString(id) {
		return fmt.Errorf("%w: preset id %q must match %s", ErrInvalidPreset, id, userPresetPattern.String())
	}
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%w: preset %q: name is required", ErrInvalidPreset, id)
	}
	return validateToolAllowlist(id, p.Tools, knownTools)
}

// validateToolAllowlist checks one allowlist's shape (no empty/duplicate names)
// and, when knownTools is non-empty, that every name is a registered tool. It
// is separate from validatePreset so an override can validate only the tools
// the user file declared, not inherited built-in tools.
func validateToolAllowlist(id string, tools, knownTools []string) error {
	seen := make(map[string]bool, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool)
		if name == "" {
			return fmt.Errorf("%w: preset %q: empty tool name in allowlist", ErrInvalidPreset, id)
		}
		if seen[name] {
			return fmt.Errorf("%w: preset %q: duplicate tool %q in allowlist", ErrInvalidPreset, id, name)
		}
		seen[name] = true
		if len(knownTools) > 0 && !containsString(knownTools, name) {
			return fmt.Errorf("%w: preset %q lists unknown tool %q", ErrUnknownTool, id, name)
		}
	}
	return nil
}

// List returns the merged presets in display order (built-ins first, then user
// presets by Order then ID). The slice is a deep copy.
func (r *Registry) List() []Preset {
	if r == nil {
		return nil
	}
	return clonePresets(r.presets)
}

// Get returns the preset with id and whether it exists.
func (r *Registry) Get(id string) (Preset, bool) {
	if r == nil {
		return Preset{}, false
	}
	p, ok := r.byID[strings.TrimSpace(id)]
	if !ok {
		return Preset{}, false
	}
	return clonePreset(p), true
}

// Resolve resolves a session's preset selection. Empty, unknown or deleted IDs
// resolve to the fallback preset (standard) with a non-empty warning that names
// the requested ID; a known ID resolves without warnings.
func (r *Registry) Resolve(id string) (Preset, []string) {
	if r == nil {
		return Preset{}, []string{"preset registry is unavailable; using the default agent behaviour"}
	}
	requested := strings.TrimSpace(id)
	if requested == "" {
		if p, ok := r.Get(PresetFallbackID); ok {
			return p, nil
		}
		return Preset{}, []string{"default preset is missing; using the default agent behaviour"}
	}
	if p, ok := r.Get(requested); ok {
		return p, nil
	}
	fallback, ok := r.Get(PresetFallbackID)
	if !ok {
		return Preset{}, []string{fmt.Sprintf("preset %q not found and the fallback is missing; using the default agent behaviour", requested)}
	}
	return fallback, []string{fmt.Sprintf("preset %q not found; falling back to %q", requested, PresetFallbackID)}
}

// VisibleTools applies presetID's tool allowlist to the registered tool names.
// An empty allowlist returns every registered name (same order); a non-empty
// allowlist returns only the named tools, in registration order. Unknown
// preset IDs return ErrNotFound; allowlist entries that were accepted at load
// time never silently drop here.
func (r *Registry) VisibleTools(presetID string, registered []string) ([]string, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: preset registry is unavailable", ErrNotFound)
	}
	p, ok := r.Get(presetID)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, presetID)
	}
	if len(p.Tools) == 0 {
		out := make([]string, len(registered))
		copy(out, registered)
		return out, nil
	}
	allowed := make(map[string]bool, len(p.Tools))
	for _, name := range p.Tools {
		allowed[name] = true
	}
	out := make([]string, 0, len(registered))
	for _, name := range registered {
		if allowed[name] {
			out = append(out, name)
		}
	}
	return out, nil
}

// PersonaSections returns the ordered prompt sections a session's preset
// contributes: the persona prefix (only when Persona is non-empty) and the
// preset instructions (only when Prompt is non-empty). Order is stable across
// calls and across registry reloads for the same content. Unknown preset IDs
// return nil (the caller resolves first, so this is not an error).
func (r *Registry) PersonaSections(presetID string) []prompt.Section {
	if r == nil {
		return nil
	}
	p, ok := r.Get(presetID)
	if !ok {
		return nil
	}
	sections := make([]prompt.Section, 0, 2)
	if text := strings.TrimSpace(p.Persona); text != "" {
		sections = append(sections, prompt.Section{
			ID:    PersonaSectionID,
			Order: prompt.OrderPersonaPrefix,
			EN:    text,
			ZH:    text,
		})
	}
	if text := strings.TrimSpace(p.Prompt); text != "" {
		sections = append(sections, prompt.Section{
			ID:    PromptSectionID,
			Order: OrderPresetPrompt,
			EN:    text,
			ZH:    text,
		})
	}
	return sections
}

// Validate reports whether the registry's presets are internally valid: every
// ID/Name is set, IDs are unique and match the allowed character set, and
// every non-empty tool allowlist only names known registered tools (when
// KnownTools was provided at load time).
func (r *Registry) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: nil registry", ErrInvalidPreset)
	}
	seen := make(map[string]bool, len(r.presets))
	for _, p := range r.presets {
		// Built-ins are part of the product contract and may name tools that a
		// caller's live registry subset does not include; unknown-tool
		// membership is enforced for user presets at load time.
		if err := validatePreset(p, nil, nil); err != nil {
			return err
		}
		if seen[p.ID] {
			return fmt.Errorf("%w: duplicate preset id %q", ErrDuplicateID, p.ID)
		}
		seen[p.ID] = true
	}
	return nil
}

// isBuiltinID reports whether id is one of the fixed built-in preset IDs.
func isBuiltinID(id string) bool {
	for _, b := range builtins {
		if b.ID == id {
			return true
		}
	}
	return false
}

// containsString reports whether list contains want.
func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// clonePresets deep-copies a preset slice (including tool allowlists).
func clonePresets(in []Preset) []Preset {
	out := make([]Preset, 0, len(in))
	for _, p := range in {
		out = append(out, clonePreset(p))
	}
	return out
}

// clonePreset deep-copies one preset.
func clonePreset(p Preset) Preset {
	out := p
	if p.Tools != nil {
		out.Tools = append([]string(nil), p.Tools...)
	}
	return out
}

// normalizeToolNames returns known tool names as a trimmed, de-duplicated slice
// preserving order. Nil/empty means allowlist validation is disabled.
func normalizeToolNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}
