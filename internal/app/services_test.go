package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/store"
)

func TestBuildServicesCreatesIndexAndCleansUp(t *testing.T) {
	dir := t.TempDir()

	ctrl, cleanup, err := BuildServices(dir, nil)
	if err != nil {
		t.Fatalf("BuildServices: %v", err)
	}
	if ctrl == nil {
		t.Fatal("BuildServices returned a nil controller")
	}
	if cleanup == nil {
		t.Fatal("BuildServices returned a nil cleanup")
	}

	index := filepath.Join(dir, "index.sqlite")
	if _, err := os.Stat(index); err != nil {
		t.Fatalf("index.sqlite not created: %v", err)
	}

	ctx := context.Background()
	id, err := ctrl.CreateSession(ctx, "headless")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if id == "" {
		t.Fatal("CreateSession returned an empty id")
	}
	if snap := ctrl.Snapshot(); snap == nil || snap.SessionID != id {
		t.Fatalf("Snapshot after CreateSession = %+v, want session %q", snap, id)
	}

	// With no Agent the controller is still readable/history-capable, but a
	// turn cannot run.
	if err := ctrl.Submit(ctx, id, "hello"); !errors.Is(err, session.ErrNoAgent) {
		t.Fatalf("Submit without agent error = %v, want ErrNoAgent", err)
	}

	if err := cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("second cleanup: %v", err)
	}

	// Reopen the index to prove the session was committed and cleanup released
	// the database.
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st.Close() //nolint:errcheck // test cleanup
	sessions, err := st.ListSessions("")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != id {
		t.Fatalf("sessions = %+v, want the created session", sessions)
	}
}

func TestBuildServicesRejectsBadDirectory(t *testing.T) {
	if _, _, err := BuildServices("", nil); err == nil {
		t.Fatal("BuildServices(empty) must fail")
	}

	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, _, err := BuildServices(file, nil); err == nil {
		t.Fatal("BuildServices over a regular file must fail")
	}
}

// TestShellStackFor pins the per-platform shell stack: Windows exposes the
// pwsh tool only, every other platform exposes bash only, mirroring DSH's
// composition gate. The helper takes goos so both branches run on one machine.
func TestShellStackFor(t *testing.T) {
	for _, tc := range []struct {
		goos      string
		allowBash bool
		allowPwsh bool
	}{
		{"windows", false, true},
		{"Windows", false, true},
		{"linux", true, false},
		{"darwin", true, false},
		{"freebsd", true, false},
		{"", true, false},
	} {
		allowBash, allowPwsh := shellStackFor(tc.goos)
		if allowBash != tc.allowBash || allowPwsh != tc.allowPwsh {
			t.Fatalf("shellStackFor(%q) = bash:%v pwsh:%v, want bash:%v pwsh:%v",
				tc.goos, allowBash, allowPwsh, tc.allowBash, tc.allowPwsh)
		}
		if allowBash && allowPwsh {
			t.Fatalf("shellStackFor(%q) exposed both shell stacks", tc.goos)
		}
	}
}

// TestComposeInstructionReportsRuntimeEnvironment proves the app wires the
// build platform and the platform-gated shell tool into the prompt: the model
// reads its real OS/arch and the one shell dialect the registry exposes.
func TestComposeInstructionReportsRuntimeEnvironment(t *testing.T) {
	st, dir := newToolStore(t)
	if err := st.SetSetting("ui.language", "en"); err != nil {
		t.Fatalf("set language: %v", err)
	}
	reg, closeFn, err := registerAllTools(st, dir, func(key, env, def string) string { return def })
	if err != nil {
		t.Fatalf("registerAllTools: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	instruction := composeInstruction(st, dir, reg, "", session.PermissionWorkspace, "")
	wantPlatform := "Runtime environment: " + goruntime.GOOS + "/" + goruntime.GOARCH
	if !strings.Contains(instruction, wantPlatform) {
		t.Fatalf("instruction does not report %q:\n%s", wantPlatform, instruction)
	}
	if goruntime.GOOS == "windows" {
		if !strings.Contains(instruction, "The shell tool is `pwsh`") {
			t.Fatalf("Windows instruction does not describe the pwsh shell:\n%s", instruction)
		}
		if strings.Contains(instruction, "The shell tool is `bash`") {
			t.Fatalf("Windows instruction advertises bash:\n%s", instruction)
		}
		if !strings.Contains(instruction, "On Windows a killed process settles") {
			t.Fatalf("Windows instruction lacks the pwsh exit-1 guidance:\n%s", instruction)
		}
	} else {
		if !strings.Contains(instruction, "The shell tool is `bash`") {
			t.Fatalf("POSIX instruction does not describe the bash shell:\n%s", instruction)
		}
		if strings.Contains(instruction, "The shell tool is `pwsh`") {
			t.Fatalf("POSIX instruction advertises pwsh:\n%s", instruction)
		}
		if !strings.Contains(instruction, "Check the [exit code: N]") {
			t.Fatalf("POSIX instruction lacks the bash exit-code guidance:\n%s", instruction)
		}
	}
	if strings.Contains(instruction, "On Windows a killed process settles") && goruntime.GOOS != "windows" {
		t.Fatalf("POSIX instruction includes the Windows-only pwsh section")
	}
}
