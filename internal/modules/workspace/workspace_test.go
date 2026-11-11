package workspace

// Tests for the workspace module: the durable registry, the sandboxed Files /
// ReadFile / Search API and persistence.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// testLogger is a goroutine-safe contracts.Logger test double.
type testLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *testLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *testLogger) contains(fragment string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, fragment) {
			return true
		}
	}
	return false
}

// harness is one loaded workspace module.
type harness struct {
	ctx     cordis.Context
	loader  *module.Loader
	service contracts.WorkspaceService
	logger  *testLogger
	once    sync.Once
}

func (h *harness) close() {
	h.once.Do(func() {
		_ = h.loader.Close()
		_ = h.ctx.DisposeAll()
	})
}

// loadWorkspace activates the module on a fresh kernel root.
func loadWorkspace(t *testing.T, dataDir string, cfg map[string]any) (*harness, error) {
	t.Helper()
	root := cordis.Root()
	if _, err := root.Provide(contracts.KeyDataDir, dataDir); err != nil {
		root.DisposeAll()
		return nil, fmt.Errorf("provide data dir: %w", err)
	}
	logger := &testLogger{}
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(logger)); err != nil {
		root.DisposeAll()
		return nil, fmt.Errorf("provide logger: %w", err)
	}
	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeyDataDir).AddBuiltin(contracts.KeyLogger)
	if err := registry.Register(Module()); err != nil {
		root.DisposeAll()
		return nil, fmt.Errorf("register workspace module: %w", err)
	}
	entry := module.Entry{ID: "workspace", Module: ModuleID}
	if cfg != nil {
		entry.Config = cfg
	}
	loader, err := module.Load(root, registry, []module.Entry{entry})
	if err != nil {
		_ = root.DisposeAll()
		return nil, err
	}
	service, ok := root.Get(contracts.KeyWorkspace)
	if !ok {
		_ = loader.Close()
		_ = root.DisposeAll()
		return nil, fmt.Errorf("contracts.KeyWorkspace was not provided")
	}
	h := &harness{ctx: root, loader: loader, service: service, logger: logger}
	t.Cleanup(h.close)
	return h, nil
}

func mustLoadWorkspace(t *testing.T, dataDir string, cfg map[string]any) *harness {
	t.Helper()
	h, err := loadWorkspace(t, dataDir, cfg)
	if err != nil {
		t.Fatalf("load workspace module: %v", err)
	}
	return h
}

// newDirectService builds a service directly (bypassing the kernel) for the
// file-API and persistence tests.
func newDirectService(t *testing.T, dataDir string, cfg Config) *Service {
	t.Helper()
	service, err := newService(dataDir, cfg, &testLogger{})
	if err != nil {
		t.Fatalf("newService: %v", err)
	}
	return service
}

// makeDir creates a directory below base and returns its absolute path.
func makeDir(t *testing.T, base string, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{base}, parts...)...)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	return path
}

// writeFile writes a file, creating parent directories.
func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir parent of %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// mustCreate registers one workspace and fails the test on error.
func mustCreate(t *testing.T, service contracts.WorkspaceService, name, root string) contracts.Workspace {
	t.Helper()
	workspace, err := service.Create(context.Background(), name, root)
	if err != nil {
		t.Fatalf("Create(%q, %q): %v", name, root, err)
	}
	return workspace
}

// fileNames returns entry names for assertions.
func fileNames(entries []contracts.FileEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

// --- tests -----------------------------------------------------------------

func TestModuleDescriptorAndValidation(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID || ModuleID != "zeokeel/workspace" {
		t.Fatalf("module ID = %q, want zeokeel/workspace", descriptor.ID)
	}
	if descriptor.Name == "" || descriptor.Version != Version {
		t.Fatalf("name/version = %q/%q", descriptor.Name, descriptor.Version)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyWorkspace.Name() {
		t.Fatalf("provides = %+v, want KeyWorkspace", descriptor.Provides)
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
	for _, cfg := range []Config{{}, {Path: "nested/workspaces.json", ReadLimitBytes: 10, SearchDepth: 2}} {
		if err := descriptor.Validate(cfg); err != nil {
			t.Fatalf("Validate(%+v) = %v, want nil", cfg, err)
		}
	}
	if err := descriptor.Validate(Config{Path: "bad\x00path"}); err == nil {
		t.Fatal("Validate accepted a NUL path")
	}
	if err := descriptor.Validate(Config{ReadLimitBytes: -1}); err == nil {
		t.Fatal("Validate accepted a negative read limit")
	}
	if err := descriptor.Validate(Config{SearchDepth: -1}); err == nil {
		t.Fatal("Validate accepted a negative search depth")
	}
}

func TestCreateListCurrentDeleteAndPersistence(t *testing.T) {
	base := t.TempDir()
	rootA := makeDir(t, base, "project-a")
	rootB := makeDir(t, base, "project-b")
	dataDir := t.TempDir()
	h := mustLoadWorkspace(t, dataDir, nil)
	service := h.service
	ctx := context.Background()

	if _, current, err := service.Current(ctx); err != nil || current {
		t.Fatalf("initial Current = %v/%v, want no workspace", current, err)
	}
	if _, err := service.Files(ctx, ""); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("Files without a workspace = %v, want ErrNoWorkspace", err)
	}

	first := mustCreate(t, service, "Project One", rootA)
	if !strings.HasPrefix(first.ID, "ws-") || first.Name != "Project One" || first.CreatedAt.IsZero() {
		t.Fatalf("created workspace = %+v", first)
	}
	if workspace, current, err := service.Current(ctx); err != nil || !current || workspace.ID != first.ID {
		t.Fatalf("Current after first = %+v/%v/%v", workspace, current, err)
	}

	second := mustCreate(t, service, "", rootB)
	if second.Name != filepath.Base(rootB) {
		t.Fatalf("default name = %q, want the root base name", second.Name)
	}
	list, err := service.List(ctx)
	if err != nil || len(list) != 2 || list[0].ID != first.ID || list[1].ID != second.ID {
		t.Fatalf("List = %+v/%v, want insertion order", list, err)
	}

	// The same canonical root is reused and becomes current again.
	duplicate := mustCreate(t, service, "Other name", rootA)
	if duplicate.ID != first.ID {
		t.Fatalf("duplicate Create ID = %q, want %q", duplicate.ID, first.ID)
	}
	if workspace, _, _ := service.Current(ctx); workspace.ID != first.ID {
		t.Fatalf("current after duplicate = %q, want %q", workspace.ID, first.ID)
	}
	if list, _ := service.List(ctx); len(list) != 2 {
		t.Fatalf("duplicate Create added an entry: %+v", list)
	}

	if opened, err := service.Open(ctx, second.ID); err != nil || opened.ID != second.ID {
		t.Fatalf("Open(second) = %+v/%v", opened, err)
	}
	if _, err := service.Open(ctx, "ws-missing"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("Open(unknown) = %v, want ErrWorkspaceNotFound", err)
	}

	// The registry file is the documented document.
	raw, err := os.ReadFile(filepath.Join(dataDir, DefaultFileName))
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var document registryDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("registry JSON: %v", err)
	}
	if len(document.Workspaces) != 2 || document.CurrentID != second.ID {
		t.Fatalf("registry document = %+v, want two workspaces and current second", document)
	}

	// A fresh module over the same dataDir restores the registry.
	restarted := mustLoadWorkspace(t, dataDir, nil)
	list, err = restarted.service.List(ctx)
	if err != nil || len(list) != 2 || list[0].ID != first.ID || list[1].ID != second.ID {
		t.Fatalf("restored List = %+v/%v", list, err)
	}
	if workspace, current, err := restarted.service.Current(ctx); err != nil || !current || workspace.ID != second.ID {
		t.Fatalf("restored Current = %+v/%v/%v", workspace, current, err)
	}

	if err := restarted.service.Delete(ctx, first.ID); err != nil {
		t.Fatalf("Delete(first): %v", err)
	}
	if workspace, current, _ := restarted.service.Current(ctx); !current || workspace.ID != second.ID {
		t.Fatalf("current after deleting a non-current workspace = %+v/%v", workspace, current)
	}
	if err := restarted.service.Delete(ctx, second.ID); err != nil {
		t.Fatalf("Delete(second): %v", err)
	}
	if _, current, _ := restarted.service.Current(ctx); current {
		t.Fatal("current survived deleting the current workspace")
	}
	if err := restarted.service.Delete(ctx, second.ID); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("Delete(unknown) = %v, want ErrWorkspaceNotFound", err)
	}
}

func TestCreateValidation(t *testing.T) {
	base := t.TempDir()
	root := makeDir(t, base, "root")
	file := filepath.Join(base, "plain.txt")
	writeFile(t, file, []byte("x"))
	service := newDirectService(t, t.TempDir(), Config{})
	ctx := context.Background()

	if _, err := service.Create(ctx, "x", ""); err == nil {
		t.Fatal("Create accepted an empty root")
	}
	if _, err := service.Create(ctx, "x", "bad\x00root"); err == nil {
		t.Fatal("Create accepted a NUL root")
	}
	if _, err := service.Create(ctx, "x", filepath.Join(base, "missing")); err == nil {
		t.Fatal("Create accepted a missing root")
	}
	if _, err := service.Create(ctx, "x", file); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("Create(file) = %v, want ErrNotDirectory", err)
	}
	created := mustCreate(t, service, "x", root)
	if created.Root != canonicalPath(t, root) {
		t.Fatalf("root = %q, want the canonical %q", created.Root, canonicalPath(t, root))
	}
}

// canonicalPath resolves a test path the way Create does.
func canonicalPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", path, err)
	}
	return resolved
}

func TestFilesListingAndSandbox(t *testing.T) {
	base := t.TempDir()
	root := makeDir(t, base, "workspace")
	makeDir(t, root, "Alpha")
	makeDir(t, root, "beta")
	writeFile(t, filepath.Join(root, "Alpha", "inner.txt"), []byte("inner"))
	writeFile(t, filepath.Join(root, "a.txt"), []byte("a"))
	writeFile(t, filepath.Join(root, "B.txt"), []byte("b"))
	writeFile(t, filepath.Join(root, "c.md"), []byte("c"))
	writeFile(t, filepath.Join(root, ".hidden"), []byte("h"))
	outside := makeDir(t, base, "outside")
	writeFile(t, filepath.Join(outside, "secret.txt"), []byte("secret"))
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatalf("symlink escape: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "a.txt"), filepath.Join(root, "inside-link")); err != nil {
		t.Fatalf("symlink inside: %v", err)
	}

	service := newDirectService(t, t.TempDir(), Config{})
	mustCreate(t, service, "files", root)
	ctx := context.Background()

	entries, err := service.Files(ctx, "")
	if err != nil {
		t.Fatalf("Files(root): %v", err)
	}
	names := fileNames(entries)
	want := []string{"Alpha", "beta", ".hidden", "a.txt", "B.txt", "c.md", "escape", "inside-link"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("Files(root) = %v, want %v (dirs first, case-insensitive names)", names, want)
	}
	// Directories first and their flags/paths are set.
	if !entries[0].Dir || !entries[1].Dir {
		t.Fatalf("first entries are not directories: %+v", entries[:2])
	}
	for _, entry := range entries {
		if entry.Name == "Alpha" && entry.Path != "Alpha" {
			t.Fatalf("Alpha path = %q", entry.Path)
		}
		if entry.Name == "a.txt" && (entry.Dir || entry.Size != 1 || entry.ModTime.IsZero()) {
			t.Fatalf("a.txt entry = %+v", entry)
		}
	}

	nested, err := service.Files(ctx, "Alpha")
	if err != nil || len(nested) != 1 || nested[0].Path != "Alpha/inner.txt" {
		t.Fatalf("Files(Alpha) = %+v/%v", nested, err)
	}
	if _, err := service.Files(ctx, "missing"); err == nil {
		t.Fatal("Files(missing) returned nil error")
	}
	for _, escape := range []string{"..", "../", "../outside", "/etc", "escape", "escape/secret.txt"} {
		if _, err := service.Files(ctx, escape); !errors.Is(err, ErrPathOutsideRoot) && !errors.Is(err, ErrNotDirectory) {
			t.Fatalf("Files(%q) = %v, want an escape/dir error", escape, err)
		}
	}
	// A nil context is tolerated.
	if _, err := service.Files(nil, ""); err != nil {
		t.Fatalf("Files(nil ctx) = %v", err)
	}
}

func TestReadFile(t *testing.T) {
	base := t.TempDir()
	root := makeDir(t, base, "workspace")
	writeFile(t, filepath.Join(root, "hello.txt"), []byte("hello world"))
	writeFile(t, filepath.Join(root, "dir", "ignored"), []byte("x"))
	binary := []byte{0x00, 0x01, 0xff, 0x10}
	writeFile(t, filepath.Join(root, "blob.bin"), binary)
	outside := makeDir(t, base, "outside")
	writeFile(t, filepath.Join(outside, "secret.txt"), []byte("secret"))
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatalf("symlink escape: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "hello.txt"), filepath.Join(root, "inside-link")); err != nil {
		t.Fatalf("symlink inside: %v", err)
	}

	service := newDirectService(t, t.TempDir(), Config{ReadLimitBytes: 4})
	mustCreate(t, service, "read", root)
	ctx := context.Background()

	data, err := service.ReadFile(ctx, "hello.txt", 0)
	if err != nil || string(data) != "hell" {
		t.Fatalf("ReadFile(default limit) = %q/%v, want the configured 4-byte default", data, err)
	}
	data, err = service.ReadFile(ctx, "hello.txt", 100)
	if err != nil || string(data) != "hello world" {
		t.Fatalf("ReadFile(100) = %q/%v", data, err)
	}
	data, err = service.ReadFile(ctx, "inside-link", 0)
	if err != nil || string(data) != "hell" {
		t.Fatalf("ReadFile(inside symlink) = %q/%v", data, err)
	}
	data, err = service.ReadFile(ctx, "blob.bin", 4)
	if err != nil || string(data) != string(binary) {
		t.Fatalf("ReadFile(binary) = %x/%v", data, err)
	}
	if _, err := service.ReadFile(ctx, "hello.txt", -1); !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("ReadFile(-1) = %v, want ErrInvalidLimit", err)
	}
	if _, err := service.ReadFile(ctx, "", 0); err == nil {
		t.Fatal("ReadFile(empty) returned nil error")
	}
	if _, err := service.ReadFile(ctx, "missing.txt", 0); err == nil {
		t.Fatal("ReadFile(missing) returned nil error")
	}
	if _, err := service.ReadFile(ctx, "dir", 0); !errors.Is(err, ErrNotFile) {
		t.Fatalf("ReadFile(directory) = %v, want ErrNotFile", err)
	}
	for _, escape := range []string{"../", "/etc/passwd", "escape/secret.txt", "escape"} {
		if _, err := service.ReadFile(ctx, escape, 0); !errors.Is(err, ErrPathOutsideRoot) && !errors.Is(err, ErrNotFile) {
			t.Fatalf("ReadFile(%q) = %v, want an escape error", escape, err)
		}
	}
}

func TestSearch(t *testing.T) {
	base := t.TempDir()
	root := makeDir(t, base, "workspace")
	writeFile(t, filepath.Join(root, "Needle.txt"), []byte("1"))
	writeFile(t, filepath.Join(root, "sub", "needle-two.md"), []byte("2"))
	writeFile(t, filepath.Join(root, "sub", "deep", "needle-three.go"), []byte("3"))
	writeFile(t, filepath.Join(root, "sub", "deep", "deeper", "needle-four.txt"), []byte("4"))
	writeFile(t, filepath.Join(root, ".hidden", "needle-hidden.txt"), []byte("5"))
	writeFile(t, filepath.Join(root, "other.txt"), []byte("6"))
	if err := os.Symlink(makeDir(t, base, "outside"), filepath.Join(root, "sub", "outside")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	service := newDirectService(t, t.TempDir(), Config{SearchDepth: 1})
	mustCreate(t, service, "search", root)
	ctx := context.Background()

	results, err := service.Search(ctx, "NEEDLE", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	paths := make([]string, 0, len(results))
	for _, entry := range results {
		paths = append(paths, entry.Path)
	}
	want := []string{"Needle.txt", "sub/needle-two.md"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("Search(depth 1) = %v, want %v (deep and hidden skipped)", paths, want)
	}
	if entries, err := service.Search(ctx, "needle", 1); err != nil || len(entries) != 1 {
		t.Fatalf("Search(limit 1) = %d/%v", len(entries), err)
	}
	if entries, err := service.Search(ctx, "   ", 0); err != nil || len(entries) != 0 {
		t.Fatalf("Search(empty query) = %d/%v, want empty", len(entries), err)
	}
	if _, err := service.Search(ctx, "needle", -1); !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("Search(-1) = %v, want ErrInvalidLimit", err)
	}
	if entries, err := service.Search(ctx, "other", 0); err != nil || len(entries) != 1 || entries[0].Path != "other.txt" {
		t.Fatalf("Search(other) = %+v/%v", entries, err)
	}
}

func TestRegistryCorruptFileQuarantined(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, DefaultFileName)
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatalf("write corrupt registry: %v", err)
	}
	h, err := loadWorkspace(t, dataDir, nil)
	if err != nil {
		t.Fatalf("module activation with a corrupt registry = %v, want quarantine", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("corrupt registry still at %s (err=%v)", path, err)
	}
	backups, globErr := filepath.Glob(path + ".corrupt-*")
	if globErr != nil || len(backups) != 1 {
		t.Fatalf("quarantine backups = %v/%v, want exactly one", backups, globErr)
	}
	if !h.logger.contains("corrupt") || !h.logger.contains("quarantine") {
		t.Fatalf("recovery was not logged: %v", h.logger.lines)
	}
	workspaces, listErr := h.service.List(context.Background())
	if listErr != nil || len(workspaces) != 0 {
		t.Fatalf("List after quarantine = %+v/%v, want empty", workspaces, listErr)
	}
	// The registry keeps working: a new workspace persists to the canonical path.
	root := makeDir(t, t.TempDir(), "recovered")
	if _, createErr := h.service.Create(context.Background(), "recovered", root); createErr != nil {
		t.Fatalf("Create after recovery: %v", createErr)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("registry not rewritten after recovery: %v", statErr)
	}
}

func TestPersistenceWriteFailureRollsBack(t *testing.T) {
	base := t.TempDir()
	rootA := makeDir(t, base, "a")
	rootB := makeDir(t, base, "b")
	dataDir := t.TempDir()
	service := newDirectService(t, dataDir, Config{})
	first := mustCreate(t, service, "a", rootA)

	// Replace the registry file with a directory so the next atomic write fails.
	if err := os.Remove(service.Path()); err != nil {
		t.Fatalf("remove registry: %v", err)
	}
	if err := os.Mkdir(service.Path(), 0o700); err != nil {
		t.Fatalf("replace registry with directory: %v", err)
	}
	ctx := context.Background()
	if _, err := service.Create(ctx, "b", rootB); err == nil {
		t.Fatal("Create with an unwritable registry returned nil error")
	}
	list, err := service.List(ctx)
	if err != nil || len(list) != 1 || list[0].ID != first.ID {
		t.Fatalf("List after failed Create = %+v/%v, want only the first workspace", list, err)
	}
	if workspace, current, _ := service.Current(ctx); !current || workspace.ID != first.ID {
		t.Fatalf("current changed by a failed Create: %+v/%v", workspace, current)
	}
	if _, err := service.Open(ctx, first.ID); err == nil {
		t.Fatal("Open with an unwritable registry returned nil error")
	}
	if err := service.Delete(ctx, first.ID); err == nil {
		t.Fatal("Delete with an unwritable registry returned nil error")
	}
	if list, _ := service.List(ctx); len(list) != 1 {
		t.Fatalf("failed mutations changed the in-memory registry: %+v", list)
	}
}

func TestConcurrentAccess(t *testing.T) {
	base := t.TempDir()
	dataDir := t.TempDir()
	service := newDirectService(t, dataDir, Config{})
	const workers = 8
	roots := make([]string, 0, workers)
	for index := 0; index < workers; index++ {
		roots = append(roots, makeDir(t, base, fmt.Sprintf("project-%d", index)))
	}
	var waitGroup sync.WaitGroup
	errs := make(chan error, workers)
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func(number int) {
			defer waitGroup.Done()
			if _, err := service.Create(context.Background(), fmt.Sprintf("p%d", number), roots[number]); err != nil {
				errs <- err
				return
			}
			_, _ = service.List(context.Background())
			_, _, _ = service.Current(context.Background())
			_, _ = service.Files(context.Background(), "")
			_, _ = service.Search(context.Background(), "project", 0)
		}(index)
	}
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	list, err := service.List(context.Background())
	if err != nil || len(list) != workers {
		t.Fatalf("List after concurrent creates = %d/%v, want %d", len(list), err, workers)
	}
	ids := make(map[string]bool, workers)
	for _, workspace := range list {
		if ids[workspace.ID] {
			t.Fatalf("duplicate workspace ID %q", workspace.ID)
		}
		ids[workspace.ID] = true
	}
}

func TestCustomRegistryPathAndDefaults(t *testing.T) {
	base := t.TempDir()
	root := makeDir(t, base, "root")
	writeFile(t, filepath.Join(root, "big.txt"), []byte(strings.Repeat("x", 10)))
	dataDir := t.TempDir()
	h := mustLoadWorkspace(t, dataDir, map[string]any{
		"path":           "nested/workspaces.json",
		"readLimitBytes": 3,
		"searchDepth":    1,
	})
	mustCreate(t, h.service, "custom", root)
	if _, err := os.Stat(filepath.Join(dataDir, "nested", "workspaces.json")); err != nil {
		t.Fatalf("custom registry file missing: %v", err)
	}
	data, err := h.service.ReadFile(context.Background(), "big.txt", 0)
	if err != nil || len(data) != 3 {
		t.Fatalf("ReadFile(default) = %d/%v, want the configured 3-byte default", len(data), err)
	}
}

// TestModuleRestartPreservesCurrent covers a second activation reading the
// current ID from the registry.
func TestModuleRestartPreservesCurrent(t *testing.T) {
	base := t.TempDir()
	rootA := makeDir(t, base, "a")
	rootB := makeDir(t, base, "b")
	dataDir := t.TempDir()
	first := mustLoadWorkspace(t, dataDir, nil)
	workspaceA := mustCreate(t, first.service, "a", rootA)
	workspaceB := mustCreate(t, first.service, "b", rootB)
	if _, err := first.service.Open(context.Background(), workspaceA.ID); err != nil {
		t.Fatalf("Open: %v", err)
	}
	first.close()

	second := mustLoadWorkspace(t, dataDir, nil)
	if workspace, current, err := second.service.Current(context.Background()); err != nil || !current || workspace.ID != workspaceA.ID {
		t.Fatalf("restored current = %+v/%v/%v, want %q", workspace, current, err, workspaceA.ID)
	}
	list, err := second.service.List(context.Background())
	if err != nil || len(list) != 2 || list[1].ID != workspaceB.ID {
		t.Fatalf("restored List = %+v/%v", list, err)
	}
}

// assertCurrent fails unless the service's current workspace is wantID ("" means
// "no current workspace").
func assertCurrent(t *testing.T, service contracts.WorkspaceService, wantID string) {
	t.Helper()
	ctx := context.Background()
	workspace, current, err := service.Current(ctx)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if wantID == "" {
		if current {
			t.Fatalf("current workspace = %+v, want none", workspace)
		}
		return
	}
	if !current || workspace.ID != wantID {
		t.Fatalf("current workspace = %+v/%t, want %q", workspace, current, wantID)
	}
}

// TestDeleteCurrentFallsBackToMostRecentRemaining pins the fix for deleting the
// open workspace: clearing currentID silently made every file/shell tool fail
// closed on an empty sandbox root. The most recently created remaining
// workspace must become current, and the choice must survive a reload.
func TestDeleteCurrentFallsBackToMostRecentRemaining(t *testing.T) {
	base := t.TempDir()
	dataDir := t.TempDir()
	service := newDirectService(t, dataDir, Config{})
	ctx := context.Background()

	first := mustCreate(t, service, "first", makeDir(t, base, "first"))
	second := mustCreate(t, service, "second", makeDir(t, base, "second"))
	third := mustCreate(t, service, "third", makeDir(t, base, "third"))
	assertCurrent(t, service, third.ID)

	// Deleting the current newest workspace picks the newest remaining one.
	if err := service.Delete(ctx, third.ID); err != nil {
		t.Fatalf("Delete(current third): %v", err)
	}
	assertCurrent(t, service, second.ID)
	assertCurrent(t, newDirectService(t, dataDir, Config{}), second.ID)

	// Deleting the current oldest workspace also picks the newest remaining.
	if _, err := service.Open(ctx, first.ID); err != nil {
		t.Fatalf("Open(first): %v", err)
	}
	if err := service.Delete(ctx, first.ID); err != nil {
		t.Fatalf("Delete(current first): %v", err)
	}
	assertCurrent(t, service, second.ID)
	assertCurrent(t, newDirectService(t, dataDir, Config{}), second.ID)

	// Deleting a non-current workspace leaves the current one alone.
	fourth := mustCreate(t, service, "fourth", makeDir(t, base, "fourth"))
	if _, err := service.Open(ctx, second.ID); err != nil {
		t.Fatalf("Open(second): %v", err)
	}
	if err := service.Delete(ctx, fourth.ID); err != nil {
		t.Fatalf("Delete(non-current fourth): %v", err)
	}
	assertCurrent(t, service, second.ID)
	assertCurrent(t, newDirectService(t, dataDir, Config{}), second.ID)
}

// TestDeleteOnlyWorkspaceClearsCurrent keeps the terminal state: with no
// workspace left there is nothing to fall back to and currentID is empty.
func TestDeleteOnlyWorkspaceClearsCurrent(t *testing.T) {
	dataDir := t.TempDir()
	service := newDirectService(t, dataDir, Config{})
	ctx := context.Background()
	only := mustCreate(t, service, "only", makeDir(t, t.TempDir(), "only"))

	if err := service.Delete(ctx, only.ID); err != nil {
		t.Fatalf("Delete(only): %v", err)
	}
	assertCurrent(t, service, "")

	reloaded := newDirectService(t, dataDir, Config{})
	assertCurrent(t, reloaded, "")
	if list, err := reloaded.List(ctx); err != nil || len(list) != 0 {
		t.Fatalf("List after deleting the only workspace = %+v/%v", list, err)
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, DefaultFileName))
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var document registryDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("registry JSON: %v", err)
	}
	if document.CurrentID != "" || len(document.Workspaces) != 0 {
		t.Fatalf("registry after deleting the only workspace = %+v", document)
	}
}

// TestDeleteCurrentPrefersCreatedAtOverRegistryOrder proves "most recently
// created" comes from CreatedAt, not from a possibly edited/legacy list order.
func TestDeleteCurrentPrefersCreatedAtOverRegistryOrder(t *testing.T) {
	dataDir := t.TempDir()
	now := time.Now().UTC()
	document := registryDocument{
		Workspaces: []workspaceRecord{
			{ID: "ws-newest", Name: "newest", Root: filepath.Join(t.TempDir(), "newest"), CreatedAt: now},
			{ID: "ws-oldest", Name: "oldest", Root: filepath.Join(t.TempDir(), "oldest"), CreatedAt: now.Add(-2 * time.Hour)},
			{ID: "ws-middle", Name: "middle", Root: filepath.Join(t.TempDir(), "middle"), CreatedAt: now.Add(-time.Hour)},
		},
		CurrentID: "ws-middle",
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, DefaultFileName), raw, 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	service := newDirectService(t, dataDir, Config{})
	if err := service.Delete(context.Background(), "ws-middle"); err != nil {
		t.Fatalf("Delete(current middle): %v", err)
	}
	assertCurrent(t, service, "ws-newest")
	// And it is persisted, so a restart makes the same workspace current.
	assertCurrent(t, newDirectService(t, dataDir, Config{}), "ws-newest")
}

// TestDeleteCurrentTieBreakIsDeterministic covers legacy registries whose
// workspaces share CreatedAt: the later registry entry (newer by insertion)
// must win, making the fallback stable across reloads.
func TestDeleteCurrentTieBreakIsDeterministic(t *testing.T) {
	dataDir := t.TempDir()
	created := time.Now().UTC()
	document := registryDocument{
		Workspaces: []workspaceRecord{
			{ID: "ws-a", Name: "a", Root: filepath.Join(t.TempDir(), "a"), CreatedAt: created.Add(-time.Hour)},
			{ID: "ws-b", Name: "b", Root: filepath.Join(t.TempDir(), "b"), CreatedAt: created},
			{ID: "ws-c", Name: "c", Root: filepath.Join(t.TempDir(), "c"), CreatedAt: created},
		},
		CurrentID: "ws-a",
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, DefaultFileName), raw, 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	service := newDirectService(t, dataDir, Config{})
	if err := service.Delete(context.Background(), "ws-a"); err != nil {
		t.Fatalf("Delete(current ws-a): %v", err)
	}
	assertCurrent(t, service, "ws-c")
	assertCurrent(t, newDirectService(t, dataDir, Config{}), "ws-c")
}
