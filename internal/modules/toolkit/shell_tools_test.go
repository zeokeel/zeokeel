package toolkit

// Tests for platform-correct shell tool registration: Rig's bash tool runs
// /bin/sh and can never work on Windows, while pwsh only exists where a
// PowerShell binary is installed. Both branches are unit-tested through the
// injectable goos/lookPath seam.

import (
	"errors"
	"testing"
	"time"
)

// lookPathResult builds a fake exec.LookPath: found reports the names that
// resolve, everything else returns fs.ErrNotExist-like errors.
func lookPathResult(found ...string) func(string) (string, error) {
	available := make(map[string]bool, len(found))
	for _, name := range found {
		available[name] = true
	}
	return func(name string) (string, error) {
		if available[name] {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("executable file not found in $PATH")
	}
}

func TestShellToolsPerPlatform(t *testing.T) {
	cases := []struct {
		name     string
		goos     string
		lookPath func(string) (string, error)
		wantBash bool
		wantPwsh bool
	}{
		{name: "windows always has pwsh", goos: "windows", lookPath: lookPathResult(), wantBash: false, wantPwsh: true},
		{name: "windows ignores PATH probe", goos: "WINDOWS ", lookPath: lookPathResult("pwsh"), wantBash: false, wantPwsh: true},
		{name: "linux without pwsh", goos: "linux", lookPath: lookPathResult(), wantBash: true, wantPwsh: false},
		{name: "linux with pwsh", goos: "linux", lookPath: lookPathResult("pwsh"), wantBash: true, wantPwsh: true},
		{name: "linux with windows powershell", goos: "linux", lookPath: lookPathResult("powershell"), wantBash: true, wantPwsh: true},
		{name: "darwin without pwsh", goos: "darwin", lookPath: lookPathResult(), wantBash: true, wantPwsh: false},
		{name: "nil lookPath keeps bash only", goos: "linux", lookPath: nil, wantBash: true, wantPwsh: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bash, pwsh := shellTools(tc.goos, tc.lookPath)
			if bash != tc.wantBash || pwsh != tc.wantPwsh {
				t.Fatalf("shellTools(%q) = bash:%t pwsh:%t, want bash:%t pwsh:%t",
					tc.goos, bash, pwsh, tc.wantBash, tc.wantPwsh)
			}
		})
	}
}

// rootScopedToolNames returns the names of one root-scoped build.
func rootScopedToolNames(scoped *rootScoped) map[string]bool {
	names := make(map[string]bool, len(scoped.tools))
	for _, tool := range scoped.tools {
		names[tool.Name()] = true
	}
	return names
}

// TestBuildRootScopedWindowsDropsBash covers the Windows behavior user report:
// the unusable bash tool must not be offered, and the file tools must survive.
func TestBuildRootScopedWindowsDropsBash(t *testing.T) {
	scoped := buildRootScopedFor(Config{}, t.TempDir(), 5*time.Second, "windows", lookPathResult())
	defer scoped.close()
	names := rootScopedToolNames(scoped)
	if names["bash"] {
		t.Fatalf("windows build registered the unusable bash tool: %v", names)
	}
	if !names["pwsh"] {
		t.Fatalf("windows build lacks pwsh: %v", names)
	}
	for _, want := range []string{"read_file", "write_file", "edit_file", "list_dir", "glob", "grep"} {
		if !names[want] {
			t.Fatalf("windows build lacks file tool %q: %v", want, names)
		}
	}
}

// TestBuildRootScopedNonWindowsShellSelection covers the non-Windows behavior:
// bash always, pwsh only when a PowerShell binary resolves.
func TestBuildRootScopedNonWindowsShellSelection(t *testing.T) {
	withoutPwsh := buildRootScopedFor(Config{}, t.TempDir(), 5*time.Second, "linux", lookPathResult())
	defer withoutPwsh.close()
	names := rootScopedToolNames(withoutPwsh)
	if !names["bash"] || names["pwsh"] {
		t.Fatalf("linux build without pwsh = bash:%t pwsh:%t (have %v)", names["bash"], names["pwsh"], names)
	}

	withPwsh := buildRootScopedFor(Config{}, t.TempDir(), 5*time.Second, "linux", lookPathResult("pwsh"))
	defer withPwsh.close()
	names = rootScopedToolNames(withPwsh)
	if !names["bash"] || !names["pwsh"] {
		t.Fatalf("linux build with pwsh = bash:%t pwsh:%t (have %v)", names["bash"], names["pwsh"], names)
	}
}
