package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/skills"
)

func hasToolName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func requireAgentTools(t *testing.T, mode string, names []string, present, absent []string) {
	t.Helper()
	for _, name := range present {
		if !hasToolName(names, name) {
			t.Fatalf("%s tools = %v, want %q", mode, names, name)
		}
	}
	for _, name := range absent {
		if hasToolName(names, name) {
			t.Fatalf("%s tools = %v, do not want %q", mode, names, name)
		}
	}
}

// TestAgentModeToolRoutingInProduction proves the app's production registry
// routes to the DSH tool sets per mode and that the prompt carries the mode
// policy for each one.
func TestAgentModeToolRoutingInProduction(t *testing.T) {
	st, dir := newToolStore(t)
	configuredProvider(t, st)
	if err := st.SetSetting("ui.language", "en"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		mode    string
		present []string
		absent  []string
		policy  string
	}{
		{
			mode:    runtime.ModeStandard,
			present: []string{"bash", "read_file", "write_file", "terminal_open", "read_image", "working_directory"},
			absent:  []string{"ptc", "settings_get", "settings_set", "PTC mode is active", "Minimal mode is active"},
		},
		{
			mode:    runtime.ModePTC,
			present: []string{"bash", "read_file", "write_file", "terminal_open", "read_image", "working_directory", "ptc"},
			absent:  []string{"settings_get", "settings_set"},
			policy:  "PTC mode is active",
		},
		{
			mode:    runtime.ModeMinimal,
			present: []string{"terminal_open", "terminal_read", "terminal_send", "terminal_signal", "terminal_close", "terminal_list", "read_image", "working_directory"},
			absent:  []string{"bash", "read_file", "write_file", "edit_file", "glob", "grep", "ptc", "settings_get", "settings_set"},
			policy:  "Minimal mode is active",
		},
		{
			mode:    runtime.ModeCreator,
			present: []string{"bash", "read_file", "write_file", "terminal_open", "read_image", "working_directory", "settings_get", "settings_set"},
			absent:  []string{"ptc"},
			policy:  "Creator mode is active",
		},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			bundle, err := buildAgentWithOptions(st, dir, "instruction", registryOptions{Mode: tc.mode})
			if err != nil {
				t.Fatalf("buildAgentWithOptions(%s): %v", tc.mode, err)
			}
			if bundle == nil {
				t.Fatalf("%s: no bundle", tc.mode)
			}
			t.Cleanup(func() {
				if bundle.Close != nil {
					_ = bundle.Close()
				}
			})
			if got := bundle.Agent.Mode(); got != tc.mode {
				t.Fatalf("Agent.Mode() = %q, want %q", got, tc.mode)
			}
			names := bundle.Agent.ToolNames()
			requireAgentTools(t, tc.mode, names, tc.present, tc.absent)
			instruction := bundle.Agent.Instruction()
			for _, absentPolicy := range tc.absent {
				if strings.HasPrefix(absentPolicy, "PTC mode is active") || strings.HasPrefix(absentPolicy, "Minimal mode is active") {
					if strings.Contains(instruction, absentPolicy) {
						t.Fatalf("%s prompt unexpectedly contains %q", tc.mode, absentPolicy)
					}
				}
			}
			if tc.policy != "" && !strings.Contains(instruction, tc.policy) {
				t.Fatalf("%s prompt missing %q:\n%s", tc.mode, tc.policy, instruction)
			}
		})
	}
}

// TestCreatorCanSetSettingAndWriteSkill exercises the two Creator-mode
// authoring paths end to end through the production registry: settings_set/
// settings_get hit the session store, and the standard fs tools write a valid
// <root>/skills/<name>/SKILL.md that the skill loader then reads back.
func TestCreatorCanSetSettingAndWriteSkill(t *testing.T) {
	st, dir := newToolStore(t)
	configuredProvider(t, st)
	if err := st.SetSetting("ui.language", "en"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	bundle, err := buildAgentWithOptions(st, dir, "instruction", registryOptions{Mode: runtime.ModeCreator})
	if err != nil {
		t.Fatalf("buildAgentWithOptions(creator): %v", err)
	}
	if bundle == nil {
		t.Fatal("no bundle")
	}
	t.Cleanup(func() {
		if bundle.Close != nil {
			_ = bundle.Close()
		}
	})

	set, ok := bundle.Agent.ToolByName("settings_set")
	if !ok {
		t.Fatalf("creator has no settings_set: %v", bundle.Agent.ToolNames())
	}
	get, ok := bundle.Agent.ToolByName("settings_get")
	if !ok {
		t.Fatalf("creator has no settings_get: %v", bundle.Agent.ToolNames())
	}
	ctx := context.Background()
	if _, err := set.Invoke(ctx, json.RawMessage(`{"key":"ui.font_size","value":14}`)); err != nil {
		t.Fatalf("settings_set: %v", err)
	}
	value, err := get.Invoke(ctx, json.RawMessage(`{"key":"ui.font_size"}`))
	if err != nil || value != "14" {
		t.Fatalf("settings_get = %q, %v; want 14", value, err)
	}
	if stored, err := st.GetSetting("ui.font_size"); err != nil || stored != "14" {
		t.Fatalf("stored ui.font_size = %q, %v", stored, err)
	}
	if _, err := set.Invoke(ctx, json.RawMessage(`{"key":"provider.api_key","value":"leak"}`)); err == nil {
		t.Fatal("settings_set accepted a secret key")
	}

	write, ok := bundle.Agent.ToolByName("write_file")
	if !ok {
		t.Fatalf("creator has no write_file: %v", bundle.Agent.ToolNames())
	}
	content := "---\nname: creator-probe\ndescription: Created by Creator mode.\n---\n# Probe\n\nUse this skill when testing Creator mode.\n"
	args, _ := json.Marshal(map[string]string{"path": "skills/creator-probe/SKILL.md", "content": content})
	if _, err := write.Invoke(ctx, args); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	path := filepath.Join(dir, "skills", "creator-probe", "SKILL.md")
	if data, err := os.ReadFile(path); err != nil || string(data) != content {
		t.Fatalf("SKILL.md = %q, %v", data, err)
	}
	loaded, err := skills.Load(filepath.Join(dir, "skills"))
	if err != nil {
		t.Fatalf("skills.Load: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "creator-probe" || !strings.Contains(loaded[0].Body, "Creator mode") {
		t.Fatalf("loaded skills = %+v", loaded)
	}

	// The skill tool sees the catalog at build time; a Creator-written skill
	// becomes available after the next registry rebuild, not silently within
	// the turn that wrote it.
	skillTool, ok := bundle.Agent.ToolByName("skill")
	if !ok {
		t.Fatalf("creator has no skill tool: %v", bundle.Agent.ToolNames())
	}
	if out, err := skillTool.Invoke(ctx, json.RawMessage(`{"action":"list"}`)); err != nil {
		t.Fatalf("skill list: %v", err)
	} else if !strings.Contains(out, "No skills available") {
		t.Fatalf("pre-existing skill catalog = %q, want empty before rebuild", out)
	}
	rebuilt, err := buildAgentWithOptions(st, dir, "instruction", registryOptions{Mode: runtime.ModeCreator})
	if err != nil || rebuilt == nil {
		t.Fatalf("rebuild creator: %v", err)
	}
	t.Cleanup(func() {
		if rebuilt.Close != nil {
			_ = rebuilt.Close()
		}
	})
	skillTool, ok = rebuilt.Agent.ToolByName("skill")
	if !ok {
		t.Fatalf("rebuilt creator has no skill tool: %v", rebuilt.Agent.ToolNames())
	}
	if out, err := skillTool.Invoke(ctx, json.RawMessage(`{"action":"list"}`)); err != nil {
		t.Fatalf("rebuilt skill list: %v", err)
	} else if !strings.Contains(out, "creator-probe") {
		t.Fatalf("rebuilt skill catalog = %q, want creator-probe", out)
	}
}

// TestMinimalPromptMatchesVisibleTools proves the production Minimal build does
// not instruct the model to use tools the mode hides, and does not advertise a
// skill catalog it cannot load. Before the fix composeInstructionForModes
// passed the unfiltered registry to prompt.Compose, so the Minimal instruction
// said "use the read_file tool", "use the glob tool", etc. while ToolNames for
// the same agent exposed only terminal_*, read_image and working_directory.
func TestMinimalPromptMatchesVisibleTools(t *testing.T) {
	st, dir := newToolStore(t)
	configuredProvider(t, st)
	if err := st.SetSetting("ui.language", "en"); err != nil {
		t.Fatal(err)
	}
	// A real skills directory: the catalog must stay out of the Minimal
	// prompt because the skill tool is not visible there.
	skillDir := filepath.Join(dir, "skills", "probe-skill")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: probe-skill\ndescription: Prompt parity probe.\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, err := buildAgentWithOptions(st, dir, "instruction", registryOptions{Mode: runtime.ModeMinimal})
	if err != nil {
		t.Fatalf("buildAgentWithOptions(minimal): %v", err)
	}
	if bundle == nil {
		t.Fatal("no bundle")
	}
	t.Cleanup(func() {
		if bundle.Close != nil {
			_ = bundle.Close()
		}
	})
	instruction := bundle.Agent.Instruction()
	if !strings.Contains(instruction, "Minimal mode is active") {
		t.Fatalf("minimal policy missing:\n%s", instruction)
	}
	// Guidance for tools the Minimal tool set hides must be absent.
	hidden := []string{
		"Check the [exit code: N] marker on every bash result",
		"Use the read_file tool",
		"Read an existing file before overwriting it with write_file",
		"Read a file before editing it with edit_file",
		"Use the glob tool",
		"Use the grep tool",
		"Track every background job id you start",
		"Tokens prefixed with @ are paths the user explicitly referenced",
		"Use the skill tool",
		"<available_skills>",
		"probe-skill",
	}
	for _, marker := range hidden {
		if strings.Contains(instruction, marker) {
			t.Fatalf("minimal prompt contains guidance for a hidden tool %q:\n%s", marker, instruction)
		}
	}
	// The tools that are visible keep their guidance.
	if !strings.Contains(instruction, "persistent terminal state") {
		t.Fatalf("minimal prompt is missing the terminal guidance:\n%s", instruction)
	}
}

// TestMinimalPromptMatchesVisibleToolsUnderRestrictedBuilds covers the two
// production builds where the terminal group disappears: a per-session Read
// Only permission and a disabled Shell plugin. Minimal then exposes only
// read_image and working_directory, so the policy must not tell the model to
// inspect and change files through a terminal it does not have.
func TestMinimalPromptMatchesVisibleToolsUnderRestrictedBuilds(t *testing.T) {
	cases := []struct {
		name       string
		permission string
		shellOff   bool
	}{
		{name: "read-only", permission: session.PermissionAsk},
		{name: "shell-off", shellOff: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, dir := newToolStore(t)
			configuredProvider(t, st)
			if err := st.SetSetting("ui.language", "en"); err != nil {
				t.Fatal(err)
			}
			if tc.shellOff {
				if err := st.SetSetting("plugin.shell.enabled", "false"); err != nil {
					t.Fatal(err)
				}
			}
			bundle, err := buildAgentWithOptions(st, dir, "instruction", registryOptions{
				Mode:               runtime.ModeMinimal,
				PermissionOverride: tc.permission,
			})
			if err != nil {
				t.Fatalf("buildAgentWithOptions(minimal/%s): %v", tc.name, err)
			}
			if bundle == nil {
				t.Fatalf("minimal/%s: no bundle", tc.name)
			}
			t.Cleanup(func() {
				if bundle.Close != nil {
					_ = bundle.Close()
				}
			})
			names := bundle.Agent.ToolNames()
			requireAgentTools(t, tc.name, names,
				[]string{"read_image", "working_directory"},
				[]string{
					"terminal_open", "terminal_read", "terminal_send", "terminal_signal",
					"terminal_close", "terminal_list", "bash", "read_file",
					"write_file", "edit_file", "glob", "grep",
				})
			instruction := bundle.Agent.Instruction()
			if !strings.Contains(instruction, "Minimal mode is active") {
				t.Fatalf("%s: minimal policy missing:\n%s", tc.name, instruction)
			}
			if !strings.Contains(instruction, "Only read_image and working_directory are available") {
				t.Fatalf("%s: minimal policy does not match the visible tools:\n%s", tc.name, instruction)
			}
			for _, marker := range []string{
				"terminal_* tools",
				"through the terminal",
				"Use a terminal session",
				"persistent terminal state",
				"Check the [exit code: N]",
			} {
				if strings.Contains(instruction, marker) {
					t.Fatalf("%s: minimal prompt advertises a missing terminal tool via %q:\n%s", tc.name, marker, instruction)
				}
			}
		})
	}
}

// TestCreatorPromptMatchesVisibleToolsWhenWritesDisabled covers Creator under
// Read Only and Shell-off: settings authoring still works, but write_file and
// edit_file are absent, so the policy must not tell the model to write skills
// with standard file tools.
func TestCreatorPromptMatchesVisibleToolsWhenWritesDisabled(t *testing.T) {
	cases := []struct {
		name       string
		permission string
		shellOff   bool
	}{
		{name: "read-only", permission: session.PermissionAsk},
		{name: "shell-off", shellOff: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, dir := newToolStore(t)
			configuredProvider(t, st)
			if err := st.SetSetting("ui.language", "en"); err != nil {
				t.Fatal(err)
			}
			if tc.shellOff {
				if err := st.SetSetting("plugin.shell.enabled", "false"); err != nil {
					t.Fatal(err)
				}
			}
			bundle, err := buildAgentWithOptions(st, dir, "instruction", registryOptions{
				Mode:               runtime.ModeCreator,
				PermissionOverride: tc.permission,
			})
			if err != nil {
				t.Fatalf("buildAgentWithOptions(creator/%s): %v", tc.name, err)
			}
			if bundle == nil {
				t.Fatalf("creator/%s: no bundle", tc.name)
			}
			t.Cleanup(func() {
				if bundle.Close != nil {
					_ = bundle.Close()
				}
			})
			names := bundle.Agent.ToolNames()
			requireAgentTools(t, tc.name, names,
				[]string{"settings_get", "settings_set", "read_file", "read_image", "working_directory"},
				[]string{"write_file", "edit_file", "bash"})
			instruction := bundle.Agent.Instruction()
			if !strings.Contains(instruction, "Creator mode is active") {
				t.Fatalf("%s: creator policy missing:\n%s", tc.name, instruction)
			}
			if !strings.Contains(instruction, "skill writing is unavailable under the current policy") {
				t.Fatalf("%s: creator policy does not state the skill-writing limit:\n%s", tc.name, instruction)
			}
			for _, marker := range []string{
				"with the standard file tools",
				"write reusable skills to",
				"Read an existing file before overwriting it with write_file",
				"Read a file before editing it with edit_file",
			} {
				if strings.Contains(instruction, marker) {
					t.Fatalf("%s: creator prompt advertises missing write tools via %q:\n%s", tc.name, marker, instruction)
				}
			}
		})
	}
}

// sameToolSet reports whether two name sets contain exactly the same tools.
func sameToolSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for name := range a {
		if !b[name] {
			return false
		}
	}
	return true
}

// TestAgentModeToolSetsAreExact pins each DSH-aligned preset to its full
// production tool set, not just the sample names other tests assert:
//
//   - Standard is the registry minus the mode-owned creator and PTC tools.
//   - PTC is Standard + ptc.
//   - Minimal is exactly terminal_* + read_image + working_directory.
//   - Creator is Standard + settings_get/settings_set.
func TestAgentModeToolSetsAreExact(t *testing.T) {
	st, dir := newToolStore(t)
	configuredProvider(t, st)
	if err := st.SetSetting("ui.language", "en"); err != nil {
		t.Fatal(err)
	}
	toolSet := func(mode string) map[string]bool {
		t.Helper()
		bundle, err := buildAgentWithOptions(st, dir, "instruction", registryOptions{Mode: mode})
		if err != nil {
			t.Fatalf("buildAgentWithOptions(%s): %v", mode, err)
		}
		if bundle == nil {
			t.Fatalf("%s: no bundle", mode)
		}
		if bundle.Close != nil {
			t.Cleanup(func() { _ = bundle.Close() })
		}
		set := make(map[string]bool)
		for _, name := range bundle.Agent.ToolNames() {
			set[name] = true
		}
		return set
	}
	standard := toolSet(runtime.ModeStandard)
	ptc := toolSet(runtime.ModePTC)
	minimal := toolSet(runtime.ModeMinimal)
	creator := toolSet(runtime.ModeCreator)

	ptcWant := make(map[string]bool, len(standard)+1)
	for name := range standard {
		ptcWant[name] = true
	}
	ptcWant["ptc"] = true
	if !sameToolSet(ptc, ptcWant) {
		t.Fatalf("PTC tools = %v, want standard + ptc %v", ptc, ptcWant)
	}

	creatorWant := make(map[string]bool, len(standard)+2)
	for name := range standard {
		creatorWant[name] = true
	}
	creatorWant["settings_get"] = true
	creatorWant["settings_set"] = true
	if !sameToolSet(creator, creatorWant) {
		t.Fatalf("Creator tools = %v, want standard + settings %v", creator, creatorWant)
	}

	for name := range minimal {
		if name != "read_image" && name != "working_directory" && !strings.HasPrefix(name, "terminal_") {
			t.Fatalf("Minimal tools = %v, found %q (only terminal_*, read_image and working_directory may appear)", minimal, name)
		}
	}
	for _, want := range []string{"terminal_open", "read_image", "working_directory"} {
		if !minimal[want] {
			t.Fatalf("Minimal tools = %v, want %q", minimal, want)
		}
	}
	if standard["ptc"] || minimal["ptc"] || !ptc["ptc"] {
		t.Fatalf("PTC ownership wrong: standard=%v ptc=%v minimal=%v", standard, ptc, minimal)
	}
	for _, absent := range []string{"settings_get", "settings_set"} {
		if standard[absent] || ptc[absent] || minimal[absent] || !creator[absent] {
			t.Fatalf("creator tool %q ownership wrong: standard=%v ptc=%v minimal=%v creator=%v",
				absent, standard, ptc, minimal, creator)
		}
	}
}

// TestSetModeRefreshesPromptPolicy is the production counterpart of the
// runtime test: the app's switcher calls Agent.SetMode after the controller
// persisted the new preset, so the mode policy section must change immediately.
func TestSetModeRefreshesPromptPolicy(t *testing.T) {
	st, dir := newToolStore(t)
	configuredProvider(t, st)
	if err := st.SetSetting("ui.language", "en"); err != nil {
		t.Fatal(err)
	}
	current := runtime.ModeStandard
	bundle, err := buildAgentWithOptions(st, dir, "instruction", registryOptions{
		Mode:     current,
		ModeFunc: func() string { return current },
	})
	if err != nil {
		t.Fatalf("buildAgentWithOptions: %v", err)
	}
	if bundle == nil {
		t.Fatal("no bundle")
	}
	t.Cleanup(func() {
		if bundle.Close != nil {
			_ = bundle.Close()
		}
	})

	markers := map[string]string{
		runtime.ModePTC:     "PTC mode is active",
		runtime.ModeMinimal: "Minimal mode is active",
		runtime.ModeCreator: "Creator mode is active",
	}
	for _, mode := range []string{runtime.ModePTC, runtime.ModeMinimal, runtime.ModeCreator, runtime.ModeStandard} {
		current = mode
		if err := bundle.Agent.SetMode(mode); err != nil {
			t.Fatalf("SetMode(%s): %v", mode, err)
		}
		instruction := bundle.Agent.Instruction()
		for otherMode, marker := range markers {
			if otherMode != mode && strings.Contains(instruction, marker) {
				t.Fatalf("prompt after SetMode(%s) still contains %q:\n%s", mode, marker, instruction)
			}
		}
		if marker := markers[mode]; marker != "" && !strings.Contains(instruction, marker) {
			t.Fatalf("prompt after SetMode(%s) missing %q:\n%s", mode, marker, instruction)
		}
	}
}
