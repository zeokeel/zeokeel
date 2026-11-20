package toolkit

// Tests for the sandbox shell policy the toolkit builds from its module config.
//
// Personal-client defaults (2026-11-20): no command-name allowlist and full
// shell grammar, because an already-approved command must simply run — the
// approval gate is the decision point, not a name list. Assemblies that want a
// restriction can still pass CommandAllowlist explicitly.

import (
	"testing"
	"time"

	"github.com/zeokeel/zeokeel-rig/sandbox"
)

// TestDefaultPolicyRunsAnyCommand pins the default: any command name runs,
// including ones no hand-maintained allowlist would have listed.
func TestDefaultPolicyRunsAnyCommand(t *testing.T) {
	policy := newPolicy(Config{}, t.TempDir(), time.Second, sandbox.ModeWorkspaceWrite)
	for _, command := range []string{"pwd", "echo", "mkdir", "git", "go", "node", "npm", "python3", "my-own-tool"} {
		if err := policy.CheckCommand([]string{command}, "."); err != nil {
			t.Errorf("CheckCommand(%q) = %v, want nil (any command is allowed by default)", command, err)
		}
	}
}

// TestDefaultPolicyAllowsShellGrammar pins the second default: ordinary
// development commands (pipes, lists, redirections, leading env assignments)
// parse without an assembly opt-in.
func TestDefaultPolicyAllowsShellGrammar(t *testing.T) {
	policy := newPolicy(Config{}, t.TempDir(), time.Second, sandbox.ModeWorkspaceWrite)
	for _, command := range []string{
		"echo hi",
		"echo hi && echo bye",
		"pwd; ls -la",
		"git status | head -5",
		"mkdir -p .cache && GOCACHE=$PWD/.cache/go-build go test ./...",
	} {
		if err := policy.CheckShellCommand(command); err != nil {
			t.Errorf("CheckShellCommand(%q) = %v, want nil", command, err)
		}
	}
	argv, err := policy.ParseShellCommand("echo hi && echo bye")
	if err != nil {
		t.Fatalf("ParseShellCommand: %v", err)
	}
	if len(argv) == 0 || argv[0] != "echo" {
		t.Fatalf("ParseShellCommand first words = %v, want echo first", argv)
	}
}

// TestExplicitAllowlistStillRestricts keeps the opt-in restriction working:
// a non-empty assembly list limits shell execution to those bare names.
func TestExplicitAllowlistStillRestricts(t *testing.T) {
	policy := newPolicy(Config{CommandAllowlist: []string{"git", "echo"}}, t.TempDir(), time.Second, sandbox.ModeWorkspaceWrite)
	for _, command := range []string{"git", "echo"} {
		if err := policy.CheckCommand([]string{command}, "."); err != nil {
			t.Errorf("CheckCommand(%q) = %v, want nil (listed)", command, err)
		}
	}
	if err := policy.CheckCommand([]string{"curl"}, "."); err == nil {
		t.Fatal("allowlist accepted an unlisted command, want denial")
	}
}
