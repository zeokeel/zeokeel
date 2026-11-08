package uimodtest

// DSH import end-to-end: a synthetic session.v4.jsonl is scanned, imported
// into the product store and appears as a normal conversation.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func writeDSHSession(t *testing.T, dshDir, workspaceRoot string) {
	t.Helper()
	sessionDir := filepath.Join(dshDir, "sessions", "ws", "sess-1")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("mkdir dsh session: %v", err)
	}
	lines := []map[string]any{
		{"type": "session", "version": 4, "id": "sess-1", "createdAt": 1700000000000, "cwd": workspaceRoot},
		{"type": "user/message", "seq": 1, "data": map[string]any{
			"role": "user", "source": map[string]any{"kind": "user"},
			"content": []map[string]any{{"type": "text", "text": "导入的提问"}},
		}},
		{"type": "assistant/message", "seq": 2, "data": map[string]any{
			"turn": 1,
			"message": map[string]any{"role": "assistant", "content": []map[string]any{
				{"type": "text", "text": "导入的回答"},
				{"type": "reasoning", "text": "思考过程"},
			}},
		}},
	}
	var payload []byte
	for _, line := range lines {
		encoded, err := json.Marshal(line)
		if err != nil {
			t.Fatalf("marshal dsh line: %v", err)
		}
		payload = append(payload, encoded...)
		payload = append(payload, '\n')
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "session.v4.jsonl"), payload, 0o600); err != nil {
		t.Fatalf("write dsh session: %v", err)
	}
}

func TestDSHImportEndToEnd(t *testing.T) {
	dshDir := t.TempDir()
	workspaceRoot := t.TempDir()
	writeDSHSession(t, dshDir, workspaceRoot)

	dataDir := t.TempDir()
	root, _, loader := bootDesktop(t, dataDir, &memSettings{values: map[string]string{}})
	importer, err := root.Require(contracts.KeyImporter)
	if err != nil {
		t.Fatalf("require importer: %v", err)
	}
	preview, err := importer.Scan(context.Background(), dshDir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if preview.Sessions != 1 || preview.Messages != 2 || preview.Workspaces != 1 {
		t.Fatalf("preview = %+v", preview)
	}
	result, err := importer.Import(context.Background(), dshDir)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.ImportedSessions != 1 || result.ImportedMessages != 2 || result.ImportedWorkspaces != 1 {
		t.Fatalf("result = %+v", result)
	}
	// A second import is idempotent.
	if second, err := importer.Import(context.Background(), dshDir); err != nil || second.ImportedSessions != 0 {
		t.Fatalf("second import = %+v (err %v)", second, err)
	}

	// The agent read model is loaded at activation, so restart the product to
	// see the imported rows (exactly like a real first launch after import).
	if err := loader.Close(); err != nil {
		t.Fatalf("close loader: %v", err)
	}
	if err := root.DisposeAll(); err != nil {
		t.Fatalf("dispose root: %v", err)
	}
	root, uiRoot, loader2 := bootDesktop(t, dataDir, &memSettings{values: map[string]string{}})
	defer func() {
		_ = loader2.Close()
		_ = root.DisposeAll()
	}()
	chat := requireChat(t, root)
	session, ok := chat.Session("dsh-sess-1")
	if !ok {
		t.Fatalf("imported session missing: %+v", chat.Sessions())
	}
	if session.Title != "导入的提问" || len(session.Messages) != 2 {
		t.Fatalf("imported session = %+v", session)
	}
	if session.Messages[0].Text != "导入的提问" || session.Messages[1].Text != "导入的回答" {
		t.Fatalf("imported messages = %+v", session.Messages)
	}

	// The imported conversation appears in the sidebar and the settings import
	// page renders.
	if err := uiRoot.Navigator().Push("home"); err != nil {
		t.Fatalf("Push(home): %v", err)
	}
	tester := ui.NewTester(uiRoot.View, 1280, 800)
	tester.Frame()
	if !tester.HasText("导入的提问") {
		t.Fatalf("imported session missing from the sidebar; texts = %v", tester.Texts())
	}
	uiRoot.Navigator().ToggleSettings()
	tester.Frame()
	// 导入 lives in the collapsed 高级 settings group.
	if err := tester.Click("高级"); err != nil {
		t.Fatalf("expand advanced settings: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if err := tester.Click("导入"); err != nil {
		t.Fatalf("click import page: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	for _, want := range []string{"导入", "DSH 数据目录", "浏览", "扫描"} {
		if !tester.HasText(want) {
			t.Fatalf("import page missing %q; texts = %v", want, tester.Texts())
		}
	}
}
