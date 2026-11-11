package toolkit

// Tests for the sandbox shell policy the toolkit builds from its module config:
// the default command allowlist must cover routine development commands, the
// strict single-command grammar stays the default, and assemblies can opt into
// shell operators without losing the allowlist on the first command.

import (
	"testing"
	"time"

	"github.com/zeokeel/zeokeel-rig/sandbox"
)

// TestDefaultAllowlistCoversRoutineDevelopmentCommands guards the usability fix:
// an approved shell call must be able to run pwd/echo/mkdir/... instead of
// failing with "not in the allowlist" and burning agent iterations.
func TestDefaultAllowlistCoversRoutineDevelopmentCommands(t *testing.T) {
	want := []string{
		"pwd", "cd", "echo", "printf", "env", "mkdir", "cp", "mv", "rm",
		"touch", "date", "seq", "sort", "test", "true", "false",
		"git", "go", "make", "gofmt", "python3", "node", "npm",
	}
	have := make(map[string]bool, len(defaultCommands))
	for _, command := range defaultCommands {
		have[command] = true
	}
	for _, command := range want {
		if !have[command] {
			t.Errorf("defaultCommands lacks %q", command)
		}
	}
	policy := newPolicy(Config{}, t.TempDir(), time.Second, sandbox.ModeWorkspaceWrite)
	for _, command := range want {
		if err := policy.CheckCommand([]string{command}, "."); err != nil {
			t.Errorf("CheckCommand(%q) = %v, want nil", command, err)
		}
	}
}

// TestPolicyKeepsStrictGrammarByDefault documents the pre-existing default:
// without the assembly opt-in a shell line with operators is still rejected.
func TestPolicyKeepsStrictGrammarByDefault(t *testing.T) {
	policy := newPolicy(Config{}, t.TempDir(), time.Second, sandbox.ModeWorkspaceWrite)
	if err := policy.CheckShellCommand("echo hi && echo bye"); err == nil {
		t.Fatal("strict policy accepted a compound command, want denial")
	}
	if err := policy.CheckShellCommand("echo hi > out.txt"); err == nil {
		t.Fatal("strict policy accepted a redirection, want denial")
	}
}

// TestPolicyAllowsShellOperatorsWhenConfigured covers the product assemblies'
// opt-in: pipes, lists and leading environment assignments parse and the
// allowlist still applies to the first command.
func TestPolicyAllowsShellOperatorsWhenConfigured(t *testing.T) {
	policy := newPolicy(Config{AllowShellOperators: true}, t.TempDir(), time.Second, sandbox.ModeWorkspaceWrite)
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
	// The allowlist still governs the first command of the line.
	if err := policy.CheckCommand([]string{"definitely-not-allowed-xyz"}, "."); err == nil {
		t.Fatal("allowlist accepted an unknown command under allowShellOperators")
	}
	argv, err := policy.ParseShellCommand("echo hi && echo bye")
	if err != nil {
		t.Fatalf("ParseShellCommand: %v", err)
	}
	if len(argv) == 0 || argv[0] != "echo" {
		t.Fatalf("ParseShellCommand first words = %v, want echo first", argv)
	}
}

// TestExplicitEmptyAllowlistDisablesShell keeps the documented escape hatch:
// an explicit empty list in assembly.json disables shell execution.
func TestExplicitEmptyAllowlistDisablesShell(t *testing.T) {
	policy := newPolicy(Config{CommandAllowlist: []string{}}, t.TempDir(), time.Second, sandbox.ModeWorkspaceWrite)
	if err := policy.CheckCommand([]string{"echo"}, "."); err == nil {
		t.Fatal("empty allowlist accepted a command, want denial")
	}
}
