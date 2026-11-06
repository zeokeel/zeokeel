package presets_test

// Acceptance tests for FP-06 (declarative agent presets/personas).
//
// TDD state: STEP 1 is GREEN. The acceptance assertions below were written
// against the RED skeleton and are unchanged; internal/presets/presets.go now
// implements the catalog.

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/presets"
	"github.com/zeokeel/zeokeel/old/internal/prompt"
)

// knownTools is the registry surface the preset allowlists are validated
// against in these tests (a subset of the real registry is enough).
var knownTools = []string{
	"read_file", "write_file", "edit_file", "bash",
	"terminal_open", "terminal_read", "read_image",
	"settings_get", "settings_set",
}

// writePresetFile writes one user preset JSON file under <dataDir>/presets.
func writePresetFile(t *testing.T, dataDir, name, body string) {
	t.Helper()
	dir := filepath.Join(dataDir, "presets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create presets dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write preset %s: %v", name, err)
	}
}

// mustLoad loads a registry and fails when loading returns an error or nil.
func mustLoad(t *testing.T, opts presets.Options) *presets.Registry {
	t.Helper()
	reg, err := presets.Load(opts)
	if err != nil {
		t.Fatalf("Load(%+v): %v", opts, err)
	}
	if reg == nil {
		t.Fatal("Load returned a nil registry; the preset catalog is not implemented yet (RED)")
	}
	return reg
}

// presetIDs lists registry preset IDs in display order.
func presetIDs(reg *presets.Registry) []string {
	list := reg.List()
	ids := make([]string, 0, len(list))
	for _, p := range list {
		ids = append(ids, p.ID)
	}
	return ids
}

func sectionIDs(sections []prompt.Section) []string {
	ids := make([]string, 0, len(sections))
	for _, s := range sections {
		ids = append(ids, s.ID)
	}
	return ids
}

// TestBuiltinPresetsPresent pins the built-in catalog: the three presets that
// replace today's hard-coded modes exist, are displayable, and declare the
// fields the session picker and prompt composition need.
func TestBuiltinPresetsPresent(t *testing.T) {
	builtins := presets.Builtins()
	if len(builtins) != 3 {
		t.Fatalf("Builtins() returned %d presets, want 3 (standard, minimal, creator)", len(builtins))
	}
	byID := make(map[string]presets.Preset, len(builtins))
	for _, p := range builtins {
		byID[p.ID] = p
	}
	for _, want := range []string{presets.BuiltinStandardID, presets.BuiltinMinimalID, presets.BuiltinCreatorID} {
		p, ok := byID[want]
		if !ok {
			t.Fatalf("missing built-in preset %q (got IDs %v)", want, presetIDsOf(builtins))
		}
		if strings.TrimSpace(p.Name) == "" {
			t.Errorf("built-in preset %q has an empty Name", want)
		}
		if strings.TrimSpace(p.Description) == "" {
			t.Errorf("built-in preset %q has an empty Description", want)
		}
	}

	// Standard keeps the historical behaviour: empty allowlist = every tool,
	// and no persona override (the deployment persona still applies).
	if std := byID[presets.BuiltinStandardID]; len(std.Tools) != 0 {
		t.Errorf("standard preset Tools = %v, want empty (= all registered tools)", std.Tools)
	}
	// Minimal is an explicit terminal-only allowlist.
	minimal := byID[presets.BuiltinMinimalID]
	if len(minimal.Tools) == 0 {
		t.Errorf("minimal preset has no tool allowlist; want an explicit terminal-only list")
	}
	if !contains(minimal.Tools, "terminal_open") {
		t.Errorf("minimal preset Tools = %v, want it to allow terminal_open", minimal.Tools)
	}
	// Creator carries customization guidance (prompt and/or persona text).
	creator := byID[presets.BuiltinCreatorID]
	if strings.TrimSpace(creator.Prompt) == "" && strings.TrimSpace(creator.Persona) == "" {
		t.Errorf("creator preset has neither Prompt nor Persona text; want customization guidance")
	}
}

func presetIDsOf(list []presets.Preset) []string {
	ids := make([]string, 0, len(list))
	for _, p := range list {
		ids = append(ids, p.ID)
	}
	return ids
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestLoadUserPresetsFromJSON pins the merge/override/validation rules for
// <data dir>/presets/*.json: user presets append, same-ID user presets override
// the built-in field-wise, built-ins survive, and a broken file fails closed.
func TestLoadUserPresetsFromJSON(t *testing.T) {
	dataDir := t.TempDir()
	writePresetFile(t, dataDir, "reviewer.json", `{
		"id": "reviewer", "name": "Code reviewer",
		"description": "Reviews diffs without editing files",
		"tools": ["read_file", "bash"], "persona": "You are a strict code reviewer."
	}`)
	// Same ID as a built-in: overrides the display fields, keeps the built-in
	// values that the user file omits.
	writePresetFile(t, dataDir, "minimal.json", `{
		"id": "minimal", "name": "Minimal (custom)", "description": "Local override"
	}`)

	reg := mustLoad(t, presets.Options{UserDir: dataDir, KnownTools: knownTools})
	ids := presetIDs(reg)
	for _, want := range []string{presets.BuiltinStandardID, presets.BuiltinMinimalID, presets.BuiltinCreatorID, "reviewer"} {
		if !contains(ids, want) {
			t.Errorf("registry IDs = %v, want %q (built-ins must survive user loading)", ids, want)
		}
	}
	if len(ids) != 4 {
		t.Errorf("registry has %d presets (%v), want 4 (3 built-ins + reviewer)", len(ids), ids)
	}
	if got, ok := reg.Get("reviewer"); !ok || got.Name != "Code reviewer" {
		t.Errorf("Get(reviewer) = %+v, %v; want the user preset", got, ok)
	}
	if got, ok := reg.Get(presets.BuiltinMinimalID); !ok || got.Name != "Minimal (custom)" {
		t.Errorf("minimal override not applied: %+v, %v", got, ok)
	}
	if got, _ := reg.Get(presets.BuiltinMinimalID); len(got.Tools) == 0 {
		t.Errorf("minimal override lost the built-in tool allowlist (field-wise merge keeps absent fields)")
	}

	// A missing user directory is not an error: built-ins only.
	empty := mustLoad(t, presets.Options{UserDir: filepath.Join(t.TempDir(), "absent")})
	if got := presetIDs(empty); len(got) != 3 {
		t.Errorf("empty user dir produced %v, want the 3 built-ins", got)
	}

	// A malformed preset file fails closed with ErrInvalidJSON.
	badDir := t.TempDir()
	writePresetFile(t, badDir, "broken.json", `{"id": "broken", "name": `)
	if _, err := presets.Load(presets.Options{UserDir: badDir}); !errors.Is(err, presets.ErrInvalidJSON) {
		t.Errorf("Load with malformed JSON error = %v, want ErrInvalidJSON", err)
	}

	// Two user files declaring the same ID are ambiguous: ErrDuplicateID.
	dupDir := t.TempDir()
	writePresetFile(t, dupDir, "a.json", `{"id": "dup", "name": "A"}`)
	writePresetFile(t, dupDir, "b.json", `{"id": "dup", "name": "B"}`)
	if _, err := presets.Load(presets.Options{UserDir: dupDir}); !errors.Is(err, presets.ErrDuplicateID) {
		t.Errorf("Load with duplicate user IDs error = %v, want ErrDuplicateID", err)
	}
}

// TestPresetToolAllowlistApplied pins the tool visibility rule: a preset's
// allowlist filters the registered tools; empty means all; unknown tool names
// are rejected at load time instead of silently disappearing at run time.
func TestPresetToolAllowlistApplied(t *testing.T) {
	dataDir := t.TempDir()
	writePresetFile(t, dataDir, "reviewer.json", `{
		"id": "reviewer", "name": "Reviewer", "tools": ["bash", "read_file"]
	}`)
	writePresetFile(t, dataDir, "open.json", `{"id": "open", "name": "Open"}`)
	reg := mustLoad(t, presets.Options{UserDir: dataDir, KnownTools: knownTools})

	got, err := reg.VisibleTools("reviewer", knownTools)
	if err != nil {
		t.Fatalf("VisibleTools(reviewer): %v", err)
	}
	// Registration order wins, not allowlist order.
	want := []string{"read_file", "bash"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("VisibleTools(reviewer) = %v, want %v", got, want)
	}

	all, err := reg.VisibleTools("open", knownTools)
	if err != nil {
		t.Fatalf("VisibleTools(open): %v", err)
	}
	if !reflect.DeepEqual(all, knownTools) {
		t.Errorf("empty allowlist = %v, want every registered tool %v", all, knownTools)
	}

	if _, err := reg.VisibleTools("does-not-exist", knownTools); !errors.Is(err, presets.ErrNotFound) {
		t.Errorf("VisibleTools(unknown) error = %v, want ErrNotFound", err)
	}

	// Unknown allowlist entries must be caught when the load knows the tools.
	badDir := t.TempDir()
	writePresetFile(t, badDir, "bad.json", `{
		"id": "bad", "name": "Bad", "tools": ["read_file", "does_not_exist"]
	}`)
	if _, err := presets.Load(presets.Options{UserDir: badDir, KnownTools: knownTools}); !errors.Is(err, presets.ErrUnknownTool) {
		t.Errorf("Load with unknown allowlist tool error = %v, want ErrUnknownTool", err)
	}
}

// TestPresetResolutionAndValidation pins runtime resolution (empty/unknown falls
// back to standard with a warning) and load-time validation (ID/name required,
// model optional, ID charset).
func TestPresetResolutionAndValidation(t *testing.T) {
	dataDir := t.TempDir()
	writePresetFile(t, dataDir, "reviewer.json", `{
		"id": "reviewer", "name": "Reviewer", "description": "Reviews code", "tools": ["read_file"]
	}`)
	reg := mustLoad(t, presets.Options{UserDir: dataDir, KnownTools: knownTools})

	if got, warnings := reg.Resolve(""); got.ID != presets.PresetFallbackID || len(warnings) != 0 {
		t.Errorf("Resolve(\"\") = %q warnings=%v, want %q with no warnings", got.ID, warnings, presets.PresetFallbackID)
	}
	if got, warnings := reg.Resolve(presets.BuiltinMinimalID); got.ID != presets.BuiltinMinimalID || len(warnings) != 0 {
		t.Errorf("Resolve(minimal) = %q warnings=%v, want minimal", got.ID, warnings)
	}
	got, warnings := reg.Resolve("deleted-user-preset")
	if got.ID != presets.PresetFallbackID {
		t.Errorf("Resolve(unknown) = %q, want fallback %q", got.ID, presets.PresetFallbackID)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "deleted-user-preset") {
		t.Errorf("Resolve(unknown) warnings = %v, want one warning naming the requested ID", warnings)
	}

	// Model is optional: a user preset without one resolves (app default model).
	if p, ok := reg.Get("reviewer"); !ok {
		t.Fatalf("Get(reviewer) failed")
	} else if p.Model != "" {
		t.Errorf("reviewer Model = %q, want empty (optional)", p.Model)
	}

	// Missing name / bad ID are invalid presets.
	noNameDir := t.TempDir()
	writePresetFile(t, noNameDir, "noname.json", `{"id": "no-name"}`)
	if _, err := presets.Load(presets.Options{UserDir: noNameDir}); !errors.Is(err, presets.ErrInvalidPreset) {
		t.Errorf("Load with no name error = %v, want ErrInvalidPreset", err)
	}
	badIDDir := t.TempDir()
	writePresetFile(t, badIDDir, "badid.json", `{"id": "Bad ID", "name": "Bad"}`)
	if _, err := presets.Load(presets.Options{UserDir: badIDDir}); !errors.Is(err, presets.ErrInvalidPreset) {
		t.Errorf("Load with bad ID error = %v, want ErrInvalidPreset", err)
	}

	if err := reg.Validate(); err != nil {
		t.Errorf("Validate() on a valid registry = %v, want nil", err)
	}
}

// TestPersonaPromptInjected pins persona/prompt injection: a preset's persona
// becomes an ordered prompt section at the deployment persona-prefix slot, its
// extra prompt text follows it, and the order is stable across calls/reloads.
func TestPersonaPromptInjected(t *testing.T) {
	dataDir := t.TempDir()
	writePresetFile(t, dataDir, "reviewer.json", `{
		"id": "reviewer", "name": "Reviewer",
		"persona": "You are a terse systems engineer. Answer in short commands.",
		"prompt": "Prefer diffs over prose. Never edit files."
	}`)
	reg := mustLoad(t, presets.Options{UserDir: dataDir, KnownTools: knownTools})

	sections := reg.PersonaSections("reviewer")
	if len(sections) != 2 {
		t.Fatalf("PersonaSections(reviewer) returned %d sections, want 2 (persona + instructions): %v", len(sections), sectionIDs(sections))
	}
	persona, instructions := sections[0], sections[1]
	if persona.ID != presets.PersonaSectionID {
		t.Errorf("section[0].ID = %q, want %q", persona.ID, presets.PersonaSectionID)
	}
	if persona.Order != prompt.OrderPersonaPrefix {
		t.Errorf("persona section Order = %d, want prompt.OrderPersonaPrefix (%d)", persona.Order, prompt.OrderPersonaPrefix)
	}
	if !strings.Contains(persona.EN, "terse systems engineer") {
		t.Errorf("persona section EN = %q, want it to contain the preset persona", persona.EN)
	}
	if instructions.ID != presets.PromptSectionID {
		t.Errorf("section[1].ID = %q, want %q", instructions.ID, presets.PromptSectionID)
	}
	if instructions.Order <= persona.Order {
		t.Errorf("instructions Order = %d, want it after the persona (%d)", instructions.Order, persona.Order)
	}
	if instructions.Order >= prompt.OrderPersonaSuffix {
		t.Errorf("instructions Order = %d, want it before the deployment persona suffix (%d)", instructions.Order, prompt.OrderPersonaSuffix)
	}
	if !strings.Contains(instructions.EN, "Prefer diffs over prose") {
		t.Errorf("instructions EN = %q, want the preset prompt text", instructions.EN)
	}

	// Stable order: same IDs and orders on a second call and after a reload.
	again := reg.PersonaSections("reviewer")
	if !reflect.DeepEqual(sectionIDs(sections), sectionIDs(again)) {
		t.Errorf("persona section order changed between calls: %v vs %v", sectionIDs(sections), sectionIDs(again))
	}
	for i := range sections {
		if sections[i].Order != again[i].Order {
			t.Errorf("section %d order changed: %d vs %d", i, sections[i].Order, again[i].Order)
		}
	}
	reloaded := mustLoad(t, presets.Options{UserDir: dataDir, KnownTools: knownTools})
	if !reflect.DeepEqual(sectionIDs(reloaded.PersonaSections("reviewer")), sectionIDs(sections)) {
		t.Errorf("persona section order changed after reload")
	}

	// A preset without persona/prompt contributes no sections (standard keeps
	// the deployment persona).
	if got := reg.PersonaSections(presets.BuiltinStandardID); len(got) != 0 {
		t.Errorf("standard PersonaSections = %v, want none", sectionIDs(got))
	}
}

// TestBuiltinOverrideInheritsMissingFields pins the documented override rule
// (verifier-core P3): a user file that overrides a built-in may omit fields the
// built-in already provides — name, description, prompt and persona are
// inherited, validation runs on the merged preset, and the override wins for
// the fields it does declare.
func TestBuiltinOverrideInheritsMissingFields(t *testing.T) {
	dataDir := t.TempDir()
	// Smallest useful override: only the tool allowlist. No name/description.
	writePresetFile(t, dataDir, "minimal.json", `{
		"id": "minimal", "tools": ["read_file", "bash"]
	}`)
	// Persona-only override of the creator preset: name/prompt are inherited.
	writePresetFile(t, dataDir, "creator.json", `{
		"id": "creator", "persona": "You are a documentation editor."
	}`)

	reg := mustLoad(t, presets.Options{UserDir: dataDir, KnownTools: knownTools})

	builtinByID := make(map[string]presets.Preset)
	for _, b := range presets.Builtins() {
		builtinByID[b.ID] = b
	}

	minimal, ok := reg.Get(presets.BuiltinMinimalID)
	if !ok {
		t.Fatalf("Get(minimal) failed after override")
	}
	if want := builtinByID[presets.BuiltinMinimalID]; minimal.Name != want.Name || minimal.Description != want.Description {
		t.Errorf("minimal override lost inherited fields: name=%q description=%q, want %q / %q",
			minimal.Name, minimal.Description, want.Name, want.Description)
	}
	if !reflect.DeepEqual(minimal.Tools, []string{"read_file", "bash"}) {
		t.Errorf("minimal override Tools = %v, want the override's allowlist", minimal.Tools)
	}

	creator, ok := reg.Get(presets.BuiltinCreatorID)
	if !ok {
		t.Fatalf("Get(creator) failed after override")
	}
	builtinCreator := builtinByID[presets.BuiltinCreatorID]
	if creator.Name != builtinCreator.Name {
		t.Errorf("creator override Name = %q, want inherited %q", creator.Name, builtinCreator.Name)
	}
	if creator.Prompt != builtinCreator.Prompt {
		t.Errorf("creator override lost the inherited built-in prompt")
	}
	if creator.Persona != "You are a documentation editor." {
		t.Errorf("creator override Persona = %q, want the override's persona", creator.Persona)
	}

	// An override with an unknown tool still fails closed against the merged
	// preset's allowlist (the error names the file).
	badDir := t.TempDir()
	writePresetFile(t, badDir, "minimal.json", `{"id": "minimal", "tools": ["does_not_exist"]}`)
	if _, err := presets.Load(presets.Options{UserDir: badDir, KnownTools: knownTools}); !errors.Is(err, presets.ErrUnknownTool) {
		t.Errorf("Load override with unknown tool error = %v, want ErrUnknownTool", err)
	}

	// A new user preset still needs its own name (nothing to inherit).
	noNameDir := t.TempDir()
	writePresetFile(t, noNameDir, "fresh.json", `{"id": "fresh", "tools": ["read_file"]}`)
	if _, err := presets.Load(presets.Options{UserDir: noNameDir, KnownTools: knownTools}); !errors.Is(err, presets.ErrInvalidPreset) {
		t.Errorf("Load new preset without name error = %v, want ErrInvalidPreset", err)
	}
}
