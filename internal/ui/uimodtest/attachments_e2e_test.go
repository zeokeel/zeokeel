package uimodtest

// Attachment end-to-end: the desktop assembly sends an image through the
// optional contracts.AttachmentSender, the provider receives an image content
// part (data URL) and the chat frame shows the attachment chip.

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// tinyPNG is a 1x1 transparent PNG.
const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

func TestAttachmentMessageEndToEnd(t *testing.T) {
	pngBytes, err := base64.StdEncoding.DecodeString(tinyPNG)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	var sawImage atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 && containsText(string(body), "data:image/png;base64,") {
			sawImage.Store(true)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"收到图片\"},\"index\":0}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"index\":0,\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	settings := &memSettings{values: map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	}}
	root, uiRoot, loader := bootDesktop(t, t.TempDir(), settings)
	defer func() {
		_ = loader.Close()
		_ = root.DisposeAll()
	}()
	chat := requireChat(t, root)
	sender, ok := chat.(contracts.AttachmentSender)
	if !ok {
		t.Fatal("agent backend does not implement contracts.AttachmentSender")
	}
	sessionID, err := chat.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	attachment := contracts.Attachment{ID: "att-1", Name: "pic.png", MIME: "image/png", Data: pngBytes}
	if err := sender.SendWithAttachments(sessionID, "看看这张图", []contracts.Attachment{attachment}); err != nil {
		t.Fatalf("SendWithAttachments: %v", err)
	}
	waitIdle(t, chat, sessionID, 10*time.Second)
	if !sawImage.Load() {
		t.Fatal("provider request did not carry an image content part")
	}
	session, ok := chat.Session(sessionID)
	if !ok {
		t.Fatalf("Session(%q) missing", sessionID)
	}
	found := false
	for _, message := range session.Messages {
		if len(message.Attachments) == 1 && message.Attachments[0].Name == "pic.png" {
			found = true
		}
	}
	if !found {
		t.Fatalf("user message read model lacks the attachment: %+v", session.Messages)
	}

	if err := uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	tester := ui.NewTester(uiRoot.View, 1280, 800)
	tester.Frame()
	for _, want := range []string{"pic.png", "收到图片", "看看这张图"} {
		if !tester.HasText(want) {
			t.Fatalf("chat frame missing %q; texts = %v", want, tester.Texts())
		}
	}
}

func containsText(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for index := 0; index+len(needle) <= len(haystack); index++ {
			if haystack[index:index+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
