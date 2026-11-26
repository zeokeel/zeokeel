package agent

// Tests for the external-CLI runtime-context section (PromptSectionCLILink).
// They pin the two things that matter about it: the section appears only when a
// cli.<id>.path row names a real executable (so a product that links no CLI has a
// byte-identical prompt to before) and, when it does appear, it carries the
// ABSOLUTE path plus the exploration commands — never a bare binary name.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// writeFakeCLI writes an executable stub at dir/dws and returns its path.
func writeFakeCLI(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dws")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho dws version v9.9.9\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

// sectionByID returns one prompt section (empty when absent).
func sectionByID(sections []contracts.PromptSection, id string) contracts.PromptSection {
	for _, section := range sections {
		if section.ID == id {
			return section
		}
	}
	return contracts.PromptSection{}
}

// TestRunPromptHasNoCLISectionByDefault is the compatibility pin: no cli.*.path
// row means no section at all, so the prompt of a product that links no CLI is
// unchanged.
func TestRunPromptHasNoCLISectionByDefault(t *testing.T) {
	svc, _, _ := newPromptService(t, nil, fakeApprovals{}, presetTool{name: "bash"})
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	prompt, sections := svc.buildRunSystemPromptSections(context.Background(), sessionID,
		contracts.ChatConfig{}, map[string]bool{"bash": true}, "")
	if section := sectionByID(sections, PromptSectionCLILink); section.Text != "" {
		t.Fatalf("cli-link section = %q, want none", section.Text)
	}
	if strings.Contains(prompt, "## External CLIs") {
		t.Fatalf("prompt has a CLI section with no CLI configured: %q", prompt)
	}
}

// TestRunPromptAdvertisesConfiguredCLIWithAbsolutePath covers the section's job:
// the executable's absolute path (SHELL-QUOTED, because the model copies these
// commands into a shell tool that does not stop for approval), how to explore
// its command surface, the dws update-check prefix, and the skill pointer when
// the bundle is indexed.
func TestRunPromptAdvertisesConfiguredCLIWithAbsolutePath(t *testing.T) {
	exe := writeFakeCLI(t)
	bundle := filepath.Join(t.TempDir(), "multi")
	writePromptSkill(t, bundle, "dingtalk-chat")
	settings := newFakeSettings(map[string]string{
		contracts.SettingCLIDingtalkPath: exe,
		contracts.SettingSkillsDirs:      bundle,
	})
	svc, _, _ := newPromptService(t, settings, fakeApprovals{}, presetTool{name: "bash"})
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_, sections := svc.buildRunSystemPromptSections(context.Background(), sessionID,
		contracts.ChatConfig{}, map[string]bool{"bash": true}, "")
	section := sectionByID(sections, PromptSectionCLILink)
	if section.Text == "" {
		t.Fatalf("cli-link section missing; sections = %+v", sections)
	}
	if section.Source != "settings" {
		t.Fatalf("cli-link source = %q, want settings", section.Source)
	}
	quoted := "'" + exe + "'"
	for _, want := range []string{quoted, "--help", quoted + " schema <service>", "DWS_NO_UPDATE_CHECK=1", "'" + bundle + "'"} {
		if !strings.Contains(section.Text, want) {
			t.Fatalf("cli-link section %q lacks %q", section.Text, want)
		}
	}
	// The bare path must never start a command: that is the injection this
	// quoting closes (a path with a space would split, one with `;` would chain).
	if strings.Contains(section.Text, exe+" --help") || strings.Contains(section.Text, exe+" schema") {
		t.Fatalf("cli-link section %q embeds the unquoted path in a command", section.Text)
	}
}

// writePromptSkill creates one skill directory (root/name/SKILL.md), the shape
// the skills module discovers.
func writePromptSkill(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o644); err != nil {
		t.Fatalf("write %s/SKILL.md: %v", dir, err)
	}
}

// TestRunPromptReportsBrokenCLIPath keeps the honest case: a row pointing at
// nothing is disclosed instead of silently making the model guess.
func TestRunPromptReportsBrokenCLIPath(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SettingCLIDingtalkPath: filepath.Join(t.TempDir(), "missing", "dws"),
	})
	svc, _, _ := newPromptService(t, settings, fakeApprovals{}, presetTool{name: "bash"})
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_, sections := svc.buildRunSystemPromptSections(context.Background(), sessionID,
		contracts.ChatConfig{}, map[string]bool{"bash": true}, "")
	section := sectionByID(sections, PromptSectionCLILink)
	if !strings.Contains(section.Text, "not there") || !strings.Contains(section.Text, contracts.SettingCLIDingtalkPath) {
		t.Fatalf("cli-link section = %q, want the broken row disclosed", section.Text)
	}
}

// TestRunPromptTellsAShelllessRunItMustDelegate covers the other half of the
// contract (it replaces the older "a run with no shell tool is told nothing",
// which was the defect: silence left an orchestration session believing the
// machine had no CLI at all, while the settings page showed one configured).
//
// The reduced section states two things and nothing more: the fact (this CLI is
// configured here, at this absolute path) and the action (this run has no shell
// tool, so hand the work to a subagent that has one). It deliberately does NOT
// carry the shell commands the full section carries — a command this run cannot
// run is noise.
func TestRunPromptTellsAShelllessRunItMustDelegate(t *testing.T) {
	exe := writeFakeCLI(t)
	settings := newFakeSettings(map[string]string{
		contracts.SettingCLIDingtalkPath: exe,
	})
	svc, _, _ := newPromptService(t, settings, fakeApprovals{}, presetTool{name: "read_file"})
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// The surface of an orchestration session: tools, but no bash and no pwsh.
	toolNames := map[string]bool{"read_file": true, "subagent": true}
	prompt, sections := svc.buildRunSystemPromptSections(context.Background(), sessionID,
		contracts.ChatConfig{}, toolNames, "")
	section := sectionByID(sections, PromptSectionCLILink)
	if section.Text == "" {
		t.Fatalf("no cli-link section for a shell-less run with a configured CLI; sections = %+v", sections)
	}
	if !strings.Contains(prompt, "## External CLIs") {
		t.Fatalf("prompt = %q, want the external-CLI heading", prompt)
	}
	// The rendered section is the deliverable; log it so a reviewer reads the
	// exact text the model receives instead of guessing from the assertions.
	t.Logf("cli-link section:\n%s", section.Text)
	// The fact: the configured executable is named, so the model can say what is
	// installed instead of guessing.
	if !strings.Contains(section.Text, exe) {
		t.Fatalf("cli-link section %q does not name the configured path %q", section.Text, exe)
	}
	lower := strings.ToLower(section.Text)
	if !strings.Contains(lower, "delegate") || !strings.Contains(lower, "subagent") {
		t.Fatalf("cli-link section %q does not tell the run to delegate to a subagent", section.Text)
	}
	if !strings.Contains(lower, "no shell tool") {
		t.Fatalf("cli-link section %q does not say this run has no shell tool", section.Text)
	}
	// No command this run cannot run.
	if strings.Contains(section.Text, "--help") {
		t.Fatalf("cli-link section %q carries shell commands a shell-less run cannot use", section.Text)
	}
}
