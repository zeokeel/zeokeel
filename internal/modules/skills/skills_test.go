package skills

// Tests for the skills backend module: dynamic root ordering/deduplication, the
// rig FileStore composition (merge precedence, read, tolerance for invalid
// files), workspace switching and module wiring.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeLogger records log lines.
type fakeLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *fakeLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *fakeLogger) contains(fragment string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, fragment) {
			return true
		}
	}
	return false
}

// fakeWorkspace is a mutable contracts.WorkspaceService: only Current is used.
type fakeWorkspace struct {
	mu         sync.Mutex
	current    contracts.Workspace
	hasCurrent bool
	currentErr error
}

func (f *fakeWorkspace) setRoot(root string) {
	f.mu.Lock()
	f.current = contracts.Workspace{ID: "ws-skills", Root: root}
	f.hasCurrent = true
	f.currentErr = nil
	f.mu.Unlock()
}

func (f *fakeWorkspace) setError(err error) {
	f.mu.Lock()
	f.currentErr = err
	f.mu.Unlock()
}

func (f *fakeWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.currentErr != nil {
		return contracts.Workspace{}, false, f.currentErr
	}
	return f.current, f.hasCurrent, nil
}

func (f *fakeWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.hasCurrent {
		return nil, nil
	}
	return []contracts.Workspace{f.current}, nil
}
func (f *fakeWorkspace) Create(context.Context, string, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, errors.New("fake workspace: not implemented")
}
func (f *fakeWorkspace) Open(context.Context, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, errors.New("fake workspace: not implemented")
}
func (f *fakeWorkspace) Delete(context.Context, string) error {
	return errors.New("fake workspace: not implemented")
}
func (f *fakeWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, errors.New("fake workspace: not implemented")
}
func (f *fakeWorkspace) ReadFile(context.Context, string, int) ([]byte, error) {
	return nil, errors.New("fake workspace: not implemented")
}
func (f *fakeWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, errors.New("fake workspace: not implemented")
}

// writeFile writes one file creating parent directories.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// writeSkill writes <root>/<name>/SKILL.md with a front matter description.
func writeSkill(t *testing.T, root, name, description string) {
	t.Helper()
	writeFile(t, filepath.Join(root, name, "SKILL.md"), fmt.Sprintf(
		"---\ndescription: %s\n---\n\n# %s\n\nBody of %s.\n", description, name, name))
}

// writeTopLevel writes <root>/<name>.md (the rig FileStore's README-style
// skill).
func writeTopLevel(t *testing.T, root, name, content string) {
	t.Helper()
	writeFile(t, filepath.Join(root, name+".md"), content)
}

// newTestService builds a service directly (bypassing the kernel).
func newTestService(t *testing.T, dataDir string, cfg Config, workspace contracts.WorkspaceService) *service {
	t.Helper()
	return newService(dataDir, cfg, &fakeLogger{}, workspace)
}

// skillNames maps skills to their names.
func skillNames(skills []contracts.Skill) []string {
	names := make([]string, 0, len(skills))
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	return names
}

func TestModuleDescriptorAndValidation(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID || ModuleID != "zeokeel/skills" {
		t.Fatalf("module ID = %q, want zeokeel/skills", descriptor.ID)
	}
	if descriptor.Name == "" || descriptor.Version != Version {
		t.Fatalf("name/version = %q/%q", descriptor.Name, descriptor.Version)
	}
	provides := make(map[string]bool, len(descriptor.Provides))
	for _, provided := range descriptor.Provides {
		provides[provided.Name] = true
	}
	for _, key := range []string{contracts.KeySkills.Name(), contracts.KeySkillsEditor.Name()} {
		if !provides[key] {
			t.Fatalf("provides = %+v, missing %q", descriptor.Provides, key)
		}
	}
	required := make(map[string]bool, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		required[requirement.Name] = true
	}
	for _, name := range []string{contracts.KeyDataDir.Name(), contracts.KeyLogger.Name()} {
		if !required[name] {
			t.Fatalf("requires = %+v, missing %q", descriptor.Requires, name)
		}
	}
	if descriptor.Validate == nil {
		t.Fatal("module has no config validation hook")
	}
	for _, cfg := range []Config{{}, {Dirs: []string{"extra", "/tmp/skills"}}} {
		if err := descriptor.Validate(cfg); err != nil {
			t.Fatalf("Validate(%+v) = %v, want nil", cfg, err)
		}
	}
	if err := descriptor.Validate(Config{Dirs: []string{"bad\x00dir"}}); err == nil {
		t.Fatal("Validate accepted a NUL directory")
	}
}

func TestRootsOrderDedupAndWorkspaceSwitch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZEOKEEL_SKILLS", "")
	dataDir := t.TempDir()
	extraAbs := t.TempDir()
	makeExtraRelative := filepath.Join(dataDir, "extra-relative")
	if err := os.MkdirAll(makeExtraRelative, 0o700); err != nil {
		t.Fatalf("mkdir relative extra: %v", err)
	}
	workspaceOne := t.TempDir()
	workspaceTwo := t.TempDir()
	workspace := &fakeWorkspace{}
	workspace.setRoot(workspaceOne)

	service := newTestService(t, dataDir, Config{Dirs: []string{
		extraAbs,
		" ",
		"extra-relative",
		extraAbs,                            // duplicate
		filepath.Join(dataDir, DataDirName), // already the implicit data dir
	}}, workspace)

	// S-02: the workspace roots come first (project skills override global);
	// configured dirs, data dir, env and home follow.
	want := []string{
		filepath.Join(workspaceOne, WorkspaceDirName),
		filepath.Join(workspaceOne, ".agents", "skills"),
		filepath.Join(workspaceOne, ".zeokeel", "skills"),
		extraAbs,
		filepath.Join(dataDir, "extra-relative"),
		filepath.Join(dataDir, DataDirName),
		filepath.Join(home, ".zeokeel", "skills"),
	}
	if got := service.Roots(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Roots = %v, want %v (workspace roots, configured dirs, data dir, home; deduplicated)", got, want)
	}

	// A workspace switch changes the workspace roots immediately (no caching).
	workspace.setRoot(workspaceTwo)
	want[0] = filepath.Join(workspaceTwo, WorkspaceDirName)
	want[1] = filepath.Join(workspaceTwo, ".agents", "skills")
	want[2] = filepath.Join(workspaceTwo, ".zeokeel", "skills")
	if got := service.Roots(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Roots after workspace switch = %v, want %v", got, want)
	}
}

func TestListAndReadMergePrecedence(t *testing.T) {
	dataDir := t.TempDir()
	extra := t.TempDir()
	workspaceRoot := t.TempDir()
	workspace := &fakeWorkspace{}
	workspace.setRoot(workspaceRoot)

	// Configured dir: highest precedence (pdf overrides dataDir's pdf).
	writeTopLevel(t, extra, "other", "# Other\n")
	writeSkill(t, extra, "pdf", "PDF override")
	// Data dir.
	writeSkill(t, filepath.Join(dataDir, DataDirName), "pdf", "PDF base")
	writeTopLevel(t, filepath.Join(dataDir, DataDirName), "note", "# Note\n")
	writeFile(t, filepath.Join(dataDir, DataDirName, "ignored.txt"), "not a skill\n")
	if err := os.MkdirAll(filepath.Join(dataDir, DataDirName, "no-skill"), 0o700); err != nil {
		t.Fatalf("mkdir no-skill: %v", err)
	}
	writeFile(t, filepath.Join(dataDir, DataDirName, ".md"), "empty base name\n")
	// Workspace: highest precedence (project skills override global ones, S-02).
	writeTopLevel(t, filepath.Join(workspaceRoot, WorkspaceDirName), "note", "# Workspace note\n")
	writeSkill(t, filepath.Join(workspaceRoot, WorkspaceDirName), "task", "Task")

	service := newTestService(t, dataDir, Config{Dirs: []string{extra}}, workspace)
	skills, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	wantNames := []string{"note", "task", "other", "pdf"}
	if got := skillNames(skills); strings.Join(got, ",") != strings.Join(wantNames, ",") {
		t.Fatalf("List names = %v, want %v (workspace, config, data dir order)", got, wantNames)
	}
	byName := make(map[string]contracts.Skill, len(skills))
	for _, skill := range skills {
		byName[skill.Name] = skill
	}
	if got := byName["pdf"].Description; got != "PDF override" {
		t.Fatalf("pdf description = %q, want the configured dir's override", got)
	}
	if got := byName["note"].Description; got != "Workspace note" {
		t.Fatalf("note description = %q, want the workspace's version", got)
	}
	if got := byName["task"].Description; got != "Task" {
		t.Fatalf("task description = %q", got)
	}
	if !strings.HasPrefix(byName["pdf"].Path, filepath.Join(extra, "pdf")) {
		t.Fatalf("pdf path = %q, want it below the configured dir", byName["pdf"].Path)
	}

	// Read hits the merged view and passes unknown/blank errors through.
	pdf, err := service.Read(context.Background(), "pdf")
	if err != nil || pdf.Name != "pdf" || pdf.Description != "PDF override" {
		t.Fatalf("Read(pdf) = %+v/%v", pdf, err)
	}
	task, err := service.Read(context.Background(), " task ")
	if err != nil || task.Name != "task" || !strings.Contains(task.Body, "Body of task") {
		t.Fatalf("Read(task) = %+v/%v", task, err)
	}
	if _, err := service.Read(context.Background(), "missing"); err == nil {
		t.Fatal("Read(missing) returned nil error")
	}
	if _, err := service.Read(context.Background(), "   "); err == nil {
		t.Fatal("Read(blank) returned nil error")
	}
}

func TestRootsWithoutWorkspaceAndWithErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZEOKEEL_SKILLS", "")
	dataDir := t.TempDir()
	writeSkill(t, filepath.Join(dataDir, DataDirName), "base", "Base")

	noWorkspace := newTestService(t, dataDir, Config{}, nil)
	wantRoots := []string{filepath.Join(dataDir, DataDirName), filepath.Join(home, ".zeokeel", "skills")}
	if got := noWorkspace.Roots(); strings.Join(got, "\n") != strings.Join(wantRoots, "\n") {
		t.Fatalf("Roots without workspace = %v, want %v", got, wantRoots)
	}
	if skills, err := noWorkspace.List(context.Background()); err != nil || len(skills) != 1 || skills[0].Name != "base" {
		t.Fatalf("List without workspace = %v/%v", skillNames(skills), err)
	}

	failing := &fakeWorkspace{}
	failing.setError(errors.New("workspace exploded"))
	errorService := newTestService(t, dataDir, Config{}, failing)
	if got := errorService.Roots(); strings.Join(got, "\n") != strings.Join(wantRoots, "\n") {
		t.Fatalf("Roots with a failing workspace = %v, want %v", got, wantRoots)
	}
	if skills, err := errorService.List(context.Background()); err != nil || len(skills) != 1 {
		t.Fatalf("List with a failing workspace = %v/%v", skillNames(skills), err)
	}
}

func TestNonexistentRootsAreIgnored(t *testing.T) {
	dataDir := t.TempDir()
	writeSkill(t, filepath.Join(dataDir, DataDirName), "base", "Base")
	missing := filepath.Join(dataDir, "does-not-exist")
	service := newTestService(t, dataDir, Config{Dirs: []string{missing}}, nil)
	skills, err := service.List(context.Background())
	if err != nil || len(skills) != 1 || skills[0].Name != "base" {
		t.Fatalf("List with a missing root = %v/%v, want only base", skillNames(skills), err)
	}
}

func TestListToleratesInvalidSkillEntries(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, DataDirName)
	writeSkill(t, root, "good", "Good")
	// A directory without SKILL.md, a non-Markdown file, a directory named
	// SKILL.md, an empty-base ".md" and a broken symlink must all be skipped
	// without failing the whole list.
	if err := os.MkdirAll(filepath.Join(root, "empty-dir"), 0o700); err != nil {
		t.Fatalf("mkdir empty-dir: %v", err)
	}
	writeFile(t, filepath.Join(root, "readme.txt"), "# Not a skill\n")
	if err := os.MkdirAll(filepath.Join(root, "dir-as-skill", "SKILL.md"), 0o700); err != nil {
		t.Fatalf("mkdir dir-as-skill: %v", err)
	}
	writeFile(t, filepath.Join(root, ".md"), "empty base\n")
	if err := os.Symlink(filepath.Join(root, "missing-target"), filepath.Join(root, "broken.md")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	service := newTestService(t, dataDir, Config{}, nil)
	skills, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List with invalid entries: %v", err)
	}
	if names := skillNames(skills); strings.Join(names, ",") != "good" {
		t.Fatalf("List names = %v, want only good", names)
	}
}

func TestContextCancellation(t *testing.T) {
	dataDir := t.TempDir()
	writeSkill(t, filepath.Join(dataDir, DataDirName), "base", "Base")
	service := newTestService(t, dataDir, Config{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("List(cancelled) = %v, want context.Canceled", err)
	}
	if _, err := service.Read(ctx, "base"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read(cancelled) = %v, want context.Canceled", err)
	}
}

func TestConcurrentListAndRead(t *testing.T) {
	dataDir := t.TempDir()
	workspaceRoot := t.TempDir()
	workspace := &fakeWorkspace{}
	workspace.setRoot(workspaceRoot)
	writeSkill(t, filepath.Join(dataDir, DataDirName), "base", "Base")
	writeSkill(t, filepath.Join(workspaceRoot, WorkspaceDirName), "work", "Work")
	service := newTestService(t, dataDir, Config{}, workspace)

	const workers = 4
	var waitGroup sync.WaitGroup
	errs := make(chan error, workers)
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			skills, err := service.List(context.Background())
			if err != nil {
				errs <- err
				return
			}
			if len(skills) != 2 {
				errs <- fmt.Errorf("List returned %d skills, want 2", len(skills))
				return
			}
			if _, err := service.Read(context.Background(), "base"); err != nil {
				errs <- err
			}
			if _, err := service.Read(context.Background(), "work"); err != nil {
				errs <- err
			}
		}()
	}
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestModuleActivation(t *testing.T) {
	dataDir := t.TempDir()
	writeSkill(t, filepath.Join(dataDir, DataDirName), "base", "Base")
	workspaceRoot := t.TempDir()
	writeSkill(t, filepath.Join(workspaceRoot, WorkspaceDirName), "work", "Work")
	workspace := &fakeWorkspace{}
	workspace.setRoot(workspaceRoot)

	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()
	logger := &fakeLogger{}
	if _, err := root.Provide(contracts.KeyDataDir, dataDir); err != nil {
		t.Fatalf("provide data dir: %v", err)
	}
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(logger)); err != nil {
		t.Fatalf("provide logger: %v", err)
	}
	if _, err := root.Provide(contracts.KeyWorkspace, contracts.WorkspaceService(workspace)); err != nil {
		t.Fatalf("provide workspace: %v", err)
	}
	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeyDataDir).AddBuiltin(contracts.KeyLogger)
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register module: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{ID: "skills", Module: ModuleID}})
	if err != nil {
		t.Fatalf("load module: %v", err)
	}
	defer func() { _ = loader.Close() }()
	service, ok := root.Get(contracts.KeySkills)
	if !ok {
		t.Fatal("KeySkills was not provided")
	}
	skills, err := service.List(context.Background())
	if err != nil || len(skills) != 2 {
		t.Fatalf("List through the module = %v/%v, want base+work", skillNames(skills), err)
	}
	if !logger.contains("initialized") {
		t.Fatalf("startup log missing: %v", logger.lines)
	}

	// Invalid config is rejected before activation by the module Validate hook.
	badRegistry := module.NewRegistry()
	badRegistry.AddBuiltin(contracts.KeyDataDir).AddBuiltin(contracts.KeyLogger)
	if err := badRegistry.Register(Module()); err != nil {
		t.Fatalf("register module: %v", err)
	}
	badRoot := cordis.Root()
	defer func() { _ = badRoot.DisposeAll() }()
	if _, err := badRoot.Provide(contracts.KeyDataDir, dataDir); err != nil {
		t.Fatalf("provide data dir: %v", err)
	}
	if _, err := badRoot.Provide(contracts.KeyLogger, contracts.Logger(&fakeLogger{})); err != nil {
		t.Fatalf("provide logger: %v", err)
	}
	_, err = module.Load(badRoot, badRegistry, []module.Entry{{ID: "skills", Module: ModuleID,
		Config: map[string]any{"dirs": []any{"bad\x00dir"}}}})
	if err == nil {
		t.Fatal("module loaded with a NUL skill directory")
	}
}
