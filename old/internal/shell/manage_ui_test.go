package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestMCPPageValidatesAndSaves pins PD-07's MCP page: invalid JSON never
// reaches the store, a valid list does, and the summary lists the servers.
func TestMCPPageValidatesAndSaves(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openSettingsPage(t, h, tt, "MCP servers")
	if !tt.HasText("None configured") {
		t.Fatalf("empty MCP page missing its state: %q", tt.Texts())
	}

	// Invalid JSON: refused with the parse reason, store untouched.
	h.app.shellUI().mcpDraft = "[{"
	tt.Frame()
	if err := tt.Click("Save servers"); err != nil {
		t.Fatalf("save invalid MCP JSON: %v", err)
	}
	tt.Frame()
	if got, _ := h.store.GetSetting(settingMCPServers); got != "" {
		t.Fatalf("invalid MCP JSON was saved: %q", got)
	}

	// Valid JSON: saved and summarized.
	h.app.shellUI().mcpDraft = `[{"name":"files","command":"npx","args":["-y","server-files"]}]`
	tt.Frame()
	if err := tt.Click("Save servers"); err != nil {
		t.Fatalf("save MCP JSON: %v", err)
	}
	tt.Frame()
	got, err := h.store.GetSetting(settingMCPServers)
	if err != nil || !strings.Contains(got, `"files"`) {
		t.Fatalf("%s = %q, %v", settingMCPServers, got, err)
	}
	if !tt.HasText("files") {
		t.Fatalf("saved server missing from the summary: %q", tt.Texts())
	}
}

// TestSkillsPageScansAndOpensFolder pins the Skills page: it lists the
// SKILL.md folders under the workspace root, flags a missing description, and
// the row's action opens the folder through the injected opener.
func TestSkillsPageScansAndOpensFolder(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	root := h.app.workspaceRoot()
	skillsDir := filepath.Join(root, "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, "writer"), 0o755); err != nil {
		t.Fatalf("create skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "writer", "SKILL.md"),
		[]byte("---\nname: Writer\ndescription: Drafts release notes.\n---\n\nBody.\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(skillsDir, "broken"), 0o755); err != nil {
		t.Fatalf("create broken dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "broken", "SKILL.md"),
		[]byte("---\nname: Broken\n---\n"), 0o644); err != nil {
		t.Fatalf("write broken skill: %v", err)
	}
	opened := ""
	h.app.pluginUI().openPath = func(path string) error {
		opened = path
		return nil
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openSettingsPage(t, h, tt, "Skills")
	for _, want := range []string{"Writer", "Drafts release notes.", "Broken"} {
		if !tt.HasText(want) {
			t.Fatalf("skills page missing %q: %q", want, tt.Texts())
		}
	}
	if !tt.HasText("missing a description") {
		t.Fatalf("skills page did not flag the broken skill: %q", tt.Texts())
	}
	if err := tt.Click("Open folder"); err != nil {
		t.Fatalf("open skill folder: %v", err)
	}
	if opened == "" || !strings.Contains(opened, "skills") {
		t.Fatalf("open folder path = %q", opened)
	}
}

// TestAgentsMDEditorCreatesAndSaves pins the workspace AGENTS.md editor:
// creating it writes the template, saving the draft writes the file, and the
// page then reports it present.
func TestAgentsMDEditorCreatesAndSaves(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openSettingsPage(t, h, tt, "Skills")
	path := filepath.Join(h.app.workspaceRoot(), "AGENTS.md")
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("fixture unexpectedly has AGENTS.md")
	}
	if err := tt.Click("Create"); err != nil {
		t.Fatalf("open AGENTS.md editor: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Edit AGENTS.md") {
		t.Fatalf("editor missing: %q", tt.Texts())
	}
	h.app.shellUI().agentsDraft = "# AGENTS.md\n\nUse short answers.\n"
	if err := tt.Click("Save AGENTS.md"); err != nil {
		t.Fatalf("save AGENTS.md: %v", err)
	}
	tt.Frame()
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "Use short answers.") {
		t.Fatalf("AGENTS.md = %q, %v", string(data), err)
	}
	if !tt.HasText("Present") {
		t.Fatalf("page does not report AGENTS.md present: %q", tt.Texts())
	}
}
