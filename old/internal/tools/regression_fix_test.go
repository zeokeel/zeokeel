package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWorkingDirectoryDescriptionHonest covers the sentence that claimed the
// returned absolute cwd could be passed to other tools, while every path
// helper rejects absolute paths.
func TestWorkingDirectoryDescriptionHonest(t *testing.T) {
	if strings.Contains(workingDirectoryDescription, "absolute path can be passed") {
		t.Fatal("description still tells the model to pass the absolute cwd")
	}
	if !strings.Contains(workingDirectoryDescription, "workspace-relative") {
		t.Fatalf("description does not name the accepted form:\n%s", workingDirectoryDescription)
	}
}

// TestTerminalSignalSchemaMatchesRuntime covers the advertised-but-always-
// rejected SIGKILL: the schema enum and the switch must offer exactly the
// signals that work.
func TestTerminalSignalSchemaMatchesRuntime(t *testing.T) {
	if strings.Contains(terminalSignalDescription, `"SIGKILL"`) {
		t.Fatal("schema still offers SIGKILL")
	}
	if _, ok := terminalSignalValues["SIGKILL"]; ok {
		t.Fatal("signal table still accepts SIGKILL")
	}
	for _, name := range []string{"SIGINT", "SIGTERM", "SIGHUP", "SIGTSTP"} {
		if _, ok := terminalSignalValues[name]; !ok {
			t.Fatalf("signal table is missing %s", name)
		}
		if !strings.Contains(terminalSignalDescription, `"`+name+`"`) {
			t.Fatalf("schema is missing %s", name)
		}
	}

	tool := &terminalSignalTool{mgr: newTerminalManager(TerminalConfig{WorkspaceRoot: t.TempDir()})}
	raw, _ := json.Marshal(map[string]string{"id": "term-1", "signal": "SIGKILL"})
	if _, err := tool.Invoke(t.Context(), raw); err == nil {
		t.Fatal("SIGKILL unexpectedly succeeded")
	} else if strings.Contains(err.Error(), "unknown signal") == false {
		t.Fatalf("SIGKILL error = %v, want the schema-mismatch error", err)
	}
}
