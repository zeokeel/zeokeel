package terminal

// Tests for the terminal backend module: shell discovery/selection, one-process
// runs with combined output, exit codes, truncation, timeout/cancel and
// concurrency.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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

// fakeWorkspace is a minimal contracts.WorkspaceService: only Current is used.
type fakeWorkspace struct {
	mu         sync.Mutex
	current    contracts.Workspace
	hasCurrent bool
	currentErr error
}

func (f *fakeWorkspace) set(root string) {
	f.mu.Lock()
	f.current = contracts.Workspace{ID: "ws-test", Root: root}
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

// newTestService builds a service directly (bypassing the kernel).
func newTestService(t *testing.T, cfg Config, workspace contracts.WorkspaceService) *service {
	t.Helper()
	service, err := newService(cfg, &fakeLogger{}, workspace)
	if err != nil {
		t.Fatalf("newService: %v", err)
	}
	return service
}

// requireShell skips a run test when the machine has no discoverable shell.
func requireShell(t *testing.T, service *service) {
	t.Helper()
	if len(service.Shells()) == 0 {
		t.Skip("no shell available on this machine")
	}
}

// canonical resolves symlinks (t.TempDir may live below a symlinked /tmp).
func canonical(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", path, err)
	}
	return resolved
}

func TestModuleDescriptorAndValidation(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID || ModuleID != "zeokeel/terminal" {
		t.Fatalf("module ID = %q, want zeokeel/terminal", descriptor.ID)
	}
	if descriptor.Name == "" || descriptor.Version != Version {
		t.Fatalf("name/version = %q/%q", descriptor.Name, descriptor.Version)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyTerminal.Name() {
		t.Fatalf("provides = %+v, want KeyTerminal", descriptor.Provides)
	}
	required := make(map[string]bool, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		required[requirement.Name] = true
	}
	if !required[contracts.KeyLogger.Name()] {
		t.Fatalf("requires = %+v, missing KeyLogger", descriptor.Requires)
	}
	if descriptor.Validate == nil {
		t.Fatal("module has no config validation hook")
	}
	for _, cfg := range []Config{{}, {Shell: "sh", TimeoutMs: 10, MaxOutputBytes: 10}} {
		if err := descriptor.Validate(cfg); err != nil {
			t.Fatalf("Validate(%+v) = %v, want nil", cfg, err)
		}
	}
	if err := descriptor.Validate(Config{TimeoutMs: -1}); err == nil {
		t.Fatal("Validate accepted a negative timeoutMs")
	}
	if err := descriptor.Validate(Config{MaxOutputBytes: -1}); err == nil {
		t.Fatal("Validate accepted a negative maxOutputBytes")
	}
	if err := descriptor.Validate(Config{Shell: "bad\x00shell"}); err == nil {
		t.Fatal("Validate accepted a NUL shell")
	}
}

// TestModuleActivation covers the kernel descriptor providing KeyTerminal and
// consuming the optional workspace service.
func TestModuleActivation(t *testing.T) {
	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()
	logger := &fakeLogger{}
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(logger)); err != nil {
		t.Fatalf("provide logger: %v", err)
	}
	workspaceRoot := t.TempDir()
	workspace := &fakeWorkspace{}
	workspace.set(workspaceRoot)
	if _, err := root.Provide(contracts.KeyWorkspace, contracts.WorkspaceService(workspace)); err != nil {
		t.Fatalf("provide workspace: %v", err)
	}
	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeyLogger)
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register module: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{ID: "terminal", Module: ModuleID}})
	if err != nil {
		t.Fatalf("load module: %v", err)
	}
	defer func() { _ = loader.Close() }()
	service, ok := root.Get(contracts.KeyTerminal)
	if !ok {
		t.Fatal("KeyTerminal was not provided")
	}
	if got := service.WorkingDir(context.Background()); got != workspaceRoot {
		t.Fatalf("WorkingDir = %q, want the workspace root %q", got, workspaceRoot)
	}
	_ = logger
}

func TestShellsAndSetShell(t *testing.T) {
	service := newTestService(t, Config{}, nil)
	shells := service.Shells()
	for index, name := range shells {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatalf("Shells reported %q which is not on PATH", name)
		}
		if index > 0 && shellRank(shells[index-1]) >= shellRank(name) {
			t.Fatalf("Shells = %v, want the preference order", shells)
		}
	}
	if len(shells) == 0 {
		if got := service.Shell(); got != "" {
			t.Fatalf("Shell = %q without shells, want empty", got)
		}
		if err := service.SetShell(""); err != nil {
			t.Fatalf("SetShell(empty) without shells: %v", err)
		}
		return
	}
	if got := service.Shell(); got != shells[0] {
		t.Fatalf("default Shell = %q, want %q", got, shells[0])
	}
	last := shells[len(shells)-1]
	if err := service.SetShell(" " + last + " "); err != nil {
		t.Fatalf("SetShell(%q): %v", last, err)
	}
	if got := service.Shell(); got != last {
		t.Fatalf("Shell after SetShell = %q, want %q", got, last)
	}
	if err := service.SetShell(""); err != nil {
		t.Fatalf("SetShell(empty): %v", err)
	}
	if got := service.Shell(); got != shells[0] {
		t.Fatalf("Shell after reset = %q, want %q", got, shells[0])
	}
	if err := service.SetShell("definitely-not-a-shell"); !errors.Is(err, ErrUnknownShell) {
		t.Fatalf("SetShell(unknown) = %v, want ErrUnknownShell", err)
	}
}

// shellRank returns a shell's index in the preference list.
func shellRank(name string) int {
	for index, candidate := range shellPreference {
		if candidate == name {
			return index
		}
	}
	return len(shellPreference)
}

func TestRunSuccessAndWorkingDirectory(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace := &fakeWorkspace{}
	workspace.set(workspaceRoot)
	service := newTestService(t, Config{}, workspace)
	requireShell(t, service)
	ctx := context.Background()

	result, err := service.Run(ctx, "echo hello")
	if err != nil {
		t.Fatalf("Run(echo): %v", err)
	}
	if result.ExitCode != 0 || strings.TrimSpace(result.Output) != "hello" {
		t.Fatalf("Run(echo) = %+v", result)
	}
	result, err = service.Run(ctx, "pwd")
	if err != nil {
		t.Fatalf("Run(pwd): %v", err)
	}
	if got := strings.TrimSpace(result.Output); got != canonical(t, workspaceRoot) {
		t.Fatalf("pwd = %q, want the workspace root %q", got, canonical(t, workspaceRoot))
	}
	// stdout and stderr are combined.
	result, err = service.Run(ctx, "echo out; echo err >&2")
	if err != nil {
		t.Fatalf("Run(combined): %v", err)
	}
	if !strings.Contains(result.Output, "out") || !strings.Contains(result.Output, "err") {
		t.Fatalf("combined output = %q", result.Output)
	}
}

func TestRunNonZeroExit(t *testing.T) {
	service := newTestService(t, Config{}, nil)
	requireShell(t, service)
	result, err := service.Run(context.Background(), "exit 3")
	if err == nil {
		t.Fatal("Run(exit 3) returned nil error")
	}
	if result.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", result.ExitCode)
	}
	if !strings.Contains(err.Error(), "3") {
		t.Fatalf("error = %v, want the exit status", err)
	}
}

func TestRunOutputTruncation(t *testing.T) {
	service := newTestService(t, Config{MaxOutputBytes: 8}, nil)
	requireShell(t, service)
	result, err := service.Run(context.Background(), "printf 'abcdefghij'")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Output != "abcdefgh" {
		t.Fatalf("truncated output = %q, want abcdefgh", result.Output)
	}
}

func TestRunTimeout(t *testing.T) {
	service := newTestService(t, Config{TimeoutMs: 50}, nil)
	requireShell(t, service)
	start := time.Now()
	result, err := service.Run(context.Background(), "sleep 2")
	elapsed := time.Since(start)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run(timeout) error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("timeout took %s, want a fast kill", elapsed)
	}
	if result.ExitCode != -1 {
		t.Fatalf("timeout exit code = %d, want -1", result.ExitCode)
	}
}

func TestRunContextCancel(t *testing.T) {
	service := newTestService(t, Config{}, nil)
	requireShell(t, service)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := service.Run(ctx, "sleep 2")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(cancelled) error = %v, want context.Canceled", err)
	}
	if result.ExitCode != -1 {
		t.Fatalf("cancelled exit code = %d, want -1", result.ExitCode)
	}
}

func TestRunEmptyCommand(t *testing.T) {
	service := newTestService(t, Config{}, nil)
	if _, err := service.Run(context.Background(), "   "); !errors.Is(err, ErrEmptyCommand) {
		t.Fatalf("Run(blank) = %v, want ErrEmptyCommand", err)
	}
}

func TestNoShellAvailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	service := newTestService(t, Config{}, nil)
	if shells := service.Shells(); len(shells) != 0 {
		t.Fatalf("Shells with an empty PATH = %v, want none", shells)
	}
	if _, err := service.Run(context.Background(), "true"); !errors.Is(err, ErrNoShell) {
		t.Fatalf("Run(no shell) = %v, want ErrNoShell", err)
	}
}

func TestConfigShellValidation(t *testing.T) {
	if _, err := newService(Config{Shell: "definitely-not-a-shell"}, &fakeLogger{}, nil); !errors.Is(err, ErrUnknownShell) {
		t.Fatalf("newService(invalid shell) = %v, want ErrUnknownShell", err)
	}
	service := newTestService(t, Config{}, nil)
	if len(service.Shells()) > 0 {
		first := service.Shells()[0]
		selected := newTestService(t, Config{Shell: first}, nil)
		if got := selected.Shell(); got != first {
			t.Fatalf("configured Shell = %q, want %q", got, first)
		}
	}
}

func TestWorkingDirFallbacks(t *testing.T) {
	// No workspace service: the process working directory.
	service := newTestService(t, Config{}, nil)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if got := service.WorkingDir(context.Background()); got != cwd {
		t.Fatalf("WorkingDir without workspace = %q, want %q", got, cwd)
	}
	// A failing workspace lookup also falls back to the process directory.
	failing := &fakeWorkspace{}
	failing.setError(errors.New("workspace exploded"))
	service = newTestService(t, Config{}, failing)
	if got := service.WorkingDir(context.Background()); got != cwd {
		t.Fatalf("WorkingDir with a failing workspace = %q, want %q", got, cwd)
	}
}

func TestRunConcurrent(t *testing.T) {
	service := newTestService(t, Config{}, nil)
	requireShell(t, service)
	const workers = 4
	var waitGroup sync.WaitGroup
	errs := make(chan error, workers)
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func(number int) {
			defer waitGroup.Done()
			result, err := service.Run(context.Background(), fmt.Sprintf("echo worker-%d", number))
			if err != nil {
				errs <- err
				return
			}
			want := fmt.Sprintf("worker-%d", number)
			if strings.TrimSpace(result.Output) != want {
				errs <- fmt.Errorf("worker %d output = %q, want %q", number, result.Output, want)
			}
		}(index)
	}
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// TestDecodeCombinedOutput pins the Windows console encoding repair used by Run:
// GBK/cp936 and UTF-16LE command output becomes valid UTF-8, ordinary UTF-8 is
// untouched, and the byte budget is applied after decoding (never mid-rune).
func TestDecodeCombinedOutput(t *testing.T) {
	gbkChinese := []byte{0xD6, 0xD0, 0xCE, 0xC4} // "中文" in GBK/cp936
	utf16LE := []byte{0xFF, 0xFE, 0x2D, 0x4E, 0x87, 0x65}
	cases := []struct {
		name string
		raw  []byte
		max  int
		want string
	}{
		{name: "empty", raw: nil, max: 0, want: ""},
		{name: "ascii untouched", raw: []byte("hello\n"), max: 0, want: "hello\n"},
		{name: "valid utf8 untouched", raw: []byte("中文输出\n"), max: 0, want: "中文输出\n"},
		{name: "gbk chinese decoded", raw: gbkChinese, max: 0, want: "中文"},
		{name: "utf16le bom decoded", raw: utf16LE, max: 0, want: "中文"},
		{name: "truncation after decoding keeps whole runes", raw: gbkChinese, max: 3, want: "中"},
		{name: "truncation bounds repaired text", raw: []byte("abcdefghij"), max: 8, want: "abcdefgh"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := decodeCombinedOutput(testCase.raw, testCase.max)
			if got != testCase.want {
				t.Fatalf("decodeCombinedOutput(%q, %d) = %q, want %q", testCase.raw, testCase.max, got, testCase.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("decodeCombinedOutput returned invalid UTF-8 %q", got)
			}
		})
	}
}
