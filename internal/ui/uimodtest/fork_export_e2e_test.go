package uimodtest

// Session fork and export end-to-end: the agent backend duplicates a finished
// conversation (persisted through the store), the sidebar shows the copy, and
// the session header exports the active conversation as Markdown.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	sessionscreen "github.com/zeokeel/zeokeel/internal/ui/session"
)

func TestSessionForkAndExportEndToEnd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		if r.Method == http.MethodPost {
			// first (and only) model round: a plain text answer
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"第一句回答\"},\"index\":0}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"index\":0,\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	dataDir := t.TempDir()
	settings := &memSettings{values: map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	}}
	root, uiRoot, loader := bootDesktop(t, dataDir, settings)
	chat := requireChat(t, root)
	sessionID, err := chat.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := chat.Send(sessionID, "原始问题"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, chat, sessionID, 10*time.Second)

	forker, ok := chat.(contracts.SessionForker)
	if !ok {
		t.Fatal("agent backend does not implement contracts.SessionForker")
	}
	forkID, err := forker.ForkSession(sessionID)
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if forkID == sessionID {
		t.Fatal("fork reused the source session ID")
	}
	if active := chat.ActiveSession(); active != forkID {
		t.Fatalf("active session = %q, want the fork %q", active, forkID)
	}
	forked, ok := chat.Session(forkID)
	if !ok || len(forked.Messages) < 2 || !strings.HasPrefix(forked.Title, "副本 - ") {
		t.Fatalf("forked session = %+v (ok=%v)", forked, ok)
	}

	// Export the active (forked) conversation with the native-save seam.
	var exportedPath, exportedContent string
	originalSaver := sessionscreen.ExportSaver
	sessionscreen.ExportSaver = func(defaultName, content string) (string, error) {
		exportedPath = filepath.Join(t.TempDir(), defaultName)
		exportedContent = content
		return exportedPath, os.WriteFile(exportedPath, []byte(content), 0o600)
	}
	defer func() { sessionscreen.ExportSaver = originalSaver }()

	if err := uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	tester := ui.NewTester(uiRoot.View, 1280, 800)
	tester.Frame()
	if err := tester.Click("更多"); err != nil {
		t.Fatalf("open session actions: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if err := tester.Click("导出 Markdown"); err != nil {
		t.Fatalf("click export: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("已导出") {
		t.Fatalf("export badge missing; texts = %v", tester.Texts())
	}
	if !strings.Contains(exportedContent, "# 副本 - ") ||
		!strings.Contains(exportedContent, "原始问题") ||
		!strings.Contains(exportedContent, "第一句回答") {
		t.Fatalf("exported content = %q", exportedContent)
	}
	if _, err := os.Stat(exportedPath); err != nil {
		t.Fatalf("exported file missing: %v", err)
	}

	// Restart: the fork is restored from the session store.
	if err := loader.Close(); err != nil {
		t.Fatalf("close loader: %v", err)
	}
	if err := root.DisposeAll(); err != nil {
		t.Fatalf("dispose root: %v", err)
	}
	root2, _, loader2 := bootDesktop(t, dataDir, settings)
	defer func() {
		_ = loader2.Close()
		_ = root2.DisposeAll()
	}()
	chat2 := requireChat(t, root2)
	restored, ok := chat2.Session(forkID)
	if !ok || len(restored.Messages) < 2 || !strings.HasPrefix(restored.Title, "副本 - ") {
		t.Fatalf("restored fork = %+v (ok=%v)", restored, ok)
	}
}
