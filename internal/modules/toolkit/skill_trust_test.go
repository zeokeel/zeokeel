package toolkit

// Regression tests for FR-20: the skill tool's list/search/load output marks
// skills discovered from the current workspace as untrusted repository content,
// while data-dir/global skills stay unmarked.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeSkillStore is a contracts.SkillsService over a fixed skill list.
type fakeSkillStore struct {
	skills []rig.Skill
}

func (f *fakeSkillStore) List(context.Context) ([]rig.Skill, error) { return f.skills, nil }

func (f *fakeSkillStore) Read(_ context.Context, name string) (rig.Skill, error) {
	for _, skill := range f.skills {
		if skill.Name == name {
			return skill, nil
		}
	}
	return rig.Skill{}, osErrNotExist
}

func (f *fakeSkillStore) Roots() []string { return nil }

// osErrNotExist is the "skill not found" error of the fake store.
var osErrNotExist = &skillNotFoundError{}

type skillNotFoundError struct{}

func (*skillNotFoundError) Error() string { return "skill not found" }

// activateWithWorkspaceAndSkills activates the toolkit with a workspace and a
// skills service so the trust-aware skill tool is registered.
func activateWithWorkspaceAndSkills(t *testing.T, workspace contracts.WorkspaceService,
	skills contracts.SkillsService, config map[string]any) (*rigtool.Registry, *captureLogger) {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	logger := &captureLogger{}
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(logger)); err != nil {
		t.Fatalf("provide logger: %v", err)
	}
	if _, err := root.Provide(contracts.KeyWorkspace, workspace); err != nil {
		t.Fatalf("provide workspace: %v", err)
	}
	if _, err := root.Provide(contracts.KeySkills, skills); err != nil {
		t.Fatalf("provide skills: %v", err)
	}
	kernelRegistry := module.NewRegistry()
	if err := kernelRegistry.Register(Module()); err != nil {
		t.Fatalf("register module: %v", err)
	}
	loader, err := module.Load(root, kernelRegistry, []module.Entry{{
		ID: "toolkit", Module: "zeokeel/toolkit", Config: config,
	}})
	if err != nil {
		t.Fatalf("load toolkit: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	registry, err := root.Require(contracts.KeyToolkit)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}
	return registry, logger
}

// skillFixture builds a workspace skill (repository content) and a global
// (data-dir) skill outside the workspace.
func skillFixture(t *testing.T) (*fakeSkillStore, contracts.WorkspaceService, string, string) {
	t.Helper()
	workspaceDir := t.TempDir()
	globalDir := t.TempDir()
	repoPath := filepath.Join(workspaceDir, "skills", "repo-skill", "SKILL.md")
	globalPath := filepath.Join(globalDir, "skills", "global-skill", "SKILL.md")
	store := &fakeSkillStore{skills: []rig.Skill{
		{Name: "repo-skill", Description: "repository workflow", Path: repoPath, Body: "repo body"},
		{Name: "global-skill", Description: "installed workflow", Path: globalPath, Body: "global body"},
	}}
	workspace := &fakeWorkspace{}
	workspace.set(workspaceDir)
	return store, workspace, workspaceDir, globalDir
}

// findSkillLine returns the rendered list line of one skill (the trust marker
// may prefix it).
func findSkillLine(t *testing.T, content, name string) string {
	t.Helper()
	prefix := "- " + name
	for _, line := range strings.Split(content, "\n") {
		candidate := strings.TrimPrefix(line, untrustedSkillMarker+" ")
		if candidate == prefix || strings.HasPrefix(candidate, prefix+":") {
			return line
		}
	}
	t.Fatalf("skill %q not found in list output: %q", name, content)
	return ""
}

// TestSkillListMarksWorkspaceSkills covers action=list.
func TestSkillListMarksWorkspaceSkills(t *testing.T) {
	store, workspace, _, _ := skillFixture(t)
	registry, _ := activateWithWorkspaceAndSkills(t, workspace, store, map[string]any{"root": "."})
	result, err := registry.Invoke(context.Background(), rig.ToolCall{Name: "skill", Arguments: `{"action":"list"}`})
	if err != nil || result.IsError {
		t.Fatalf("skill list = %q/%v", result.Content, err)
	}
	repoLine := findSkillLine(t, result.Content, "repo-skill")
	if !strings.Contains(repoLine, untrustedSkillMarker) {
		t.Fatalf("workspace skill line is not marked: %q", repoLine)
	}
	globalLine := findSkillLine(t, result.Content, "global-skill")
	if strings.Contains(globalLine, untrustedSkillMarker) {
		t.Fatalf("data-dir skill line was marked: %q", globalLine)
	}
}

// TestSkillLoadMarksWorkspaceSkillBody covers action=load and the DSH name-only
// call.
func TestSkillLoadMarksWorkspaceSkillBody(t *testing.T) {
	store, workspace, _, _ := skillFixture(t)
	registry, _ := activateWithWorkspaceAndSkills(t, workspace, store, map[string]any{"root": "."})

	for _, arguments := range []string{
		`{"action":"load","name":"repo-skill"}`,
		`{"name":"repo-skill"}`,
	} {
		result, err := registry.Invoke(context.Background(), rig.ToolCall{Name: "skill", Arguments: arguments})
		if err != nil || result.IsError {
			t.Fatalf("skill load %s = %q/%v", arguments, result.Content, err)
		}
		if !strings.Contains(result.Content, untrustedSkillMarker) || !strings.Contains(result.Content, untrustedSkillNote) {
			t.Fatalf("workspace skill body lacks the trust markers (%s): %q", arguments, result.Content)
		}
	}

	global, err := registry.Invoke(context.Background(), rig.ToolCall{Name: "skill", Arguments: `{"action":"load","name":"global-skill"}`})
	if err != nil || global.IsError {
		t.Fatalf("global skill load = %q/%v", global.Content, err)
	}
	if strings.Contains(global.Content, untrustedSkillMarker) || strings.Contains(global.Content, untrustedSkillNote) {
		t.Fatalf("data-dir skill body was marked: %q", global.Content)
	}
}

// TestSkillSearchMarksWorkspaceSkills covers action=search (same list format).
func TestSkillSearchMarksWorkspaceSkills(t *testing.T) {
	store, workspace, _, _ := skillFixture(t)
	registry, _ := activateWithWorkspaceAndSkills(t, workspace, store, map[string]any{"root": "."})
	result, err := registry.Invoke(context.Background(), rig.ToolCall{Name: "skill", Arguments: `{"action":"search","query":"workflow"}`})
	if err != nil || result.IsError {
		t.Fatalf("skill search = %q/%v", result.Content, err)
	}
	if !strings.Contains(findSkillLine(t, result.Content, "repo-skill"), untrustedSkillMarker) {
		t.Fatalf("search result did not mark the workspace skill: %q", result.Content)
	}
	if strings.Contains(findSkillLine(t, result.Content, "global-skill"), untrustedSkillMarker) {
		t.Fatalf("search result marked the data-dir skill: %q", result.Content)
	}
}

// TestSkillWithoutWorkspaceIsUnmarked: with no workspace open every skill is
// non-repository content, so nothing is marked.
func TestSkillWithoutWorkspaceIsUnmarked(t *testing.T) {
	store, workspace, _, _ := skillFixture(t)
	workspace.(*fakeWorkspace).mu.Lock()
	workspace.(*fakeWorkspace).open = false
	workspace.(*fakeWorkspace).mu.Unlock()
	registry, _ := activateWithWorkspaceAndSkills(t, workspace, store, map[string]any{"root": "."})
	result, err := registry.Invoke(context.Background(), rig.ToolCall{Name: "skill", Arguments: `{"action":"list"}`})
	if err != nil || result.IsError {
		t.Fatalf("skill list = %q/%v", result.Content, err)
	}
	if strings.Contains(result.Content, untrustedSkillMarker) {
		t.Fatalf("skills marked without an open workspace: %q", result.Content)
	}
}
