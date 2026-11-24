package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Attachments (TRANSPORT §6.4): bytes go to POST /api/blobs, the run carries the
// blob ids, and the gateway hands the chat service the real content. Before this,
// the run refused `attachments` outright, so the browser could not send an image.

type attachmentChat struct {
	*fakeChat
	got []contracts.Attachment
}

func (c *attachmentChat) SendWithAttachments(sessionID, prompt string, attachments []contracts.Attachment) error {
	c.got = attachments
	return c.send(sessionID, prompt)
}

func (c *attachmentChat) send(sessionID, prompt string) error {
	c.addSession(sessionID, "Attached", prompt)
	return nil
}

func uploadBlob(t *testing.T, gw *runningGateway, body []byte, mime, name string) (int, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, "http://"+gw.address+"/api/blobs?name="+name, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+gw.info.Token)
	request.Header.Set("Content-Type", mime)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST /api/blobs: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	var out strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := response.Body.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return response.StatusCode, out.String()
}

func TestBlobUploadAndFetchRoundTrip(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	status, body := uploadBlob(t, gw, []byte("\x89PNG\r\n\x1a\nfake"), "image/png", "shot.png")
	if status != http.StatusOK {
		t.Fatalf("upload = %d %q", status, body)
	}
	var created struct {
		ID    string `json:"id"`
		Mime  string `json:"mime"`
		Bytes int    `json:"bytes"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode: %v (%q)", err, body)
	}
	if created.ID == "" || created.Mime != "image/png" || created.Name != "shot.png" || created.Bytes != 12 {
		t.Fatalf("upload metadata = %+v", created)
	}

	request, err := http.NewRequest(http.MethodGet, "http://"+gw.address+"/api/blobs/"+created.ID, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+gw.info.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET blob: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("fetch = %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	// An unknown id is a 404, not an empty file.
	missing, err := http.NewRequest(http.MethodGet, "http://"+gw.address+"/api/blobs/deadbeef", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	missing.Header.Set("Authorization", "Bearer "+gw.info.Token)
	missingResponse, err := http.DefaultClient.Do(missing)
	if err != nil {
		t.Fatalf("GET missing blob: %v", err)
	}
	defer func() { _ = missingResponse.Body.Close() }()
	if missingResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown blob = %d, want 404", missingResponse.StatusCode)
	}
}

func TestRunCarriesUploadedAttachmentToTheChatService(t *testing.T) {
	chat := &attachmentChat{fakeChat: newFakeChat()}
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	status, body := uploadBlob(t, gw, []byte("hello-image-bytes"), "image/png", "a.png")
	if status != http.StatusOK {
		t.Fatalf("upload = %d %q", status, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(body), &created)

	payload := `{"sessionId":"s1","text":"看看这张图","attachments":["` + created.ID +
		`"],"clientRunId":"run-attach-1"}`
	response := openRunRaw(t, gw, payload)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		snippet := make([]byte, 300)
		n, _ := response.Body.Read(snippet)
		t.Fatalf("run = %d %q", response.StatusCode, snippet[:n])
	}
	if len(chat.got) != 1 {
		t.Fatalf("attachments reaching the chat service = %d", len(chat.got))
	}
	got := chat.got[0]
	if got.MIME != "image/png" || string(got.Data) != "hello-image-bytes" || got.Name != "a.png" {
		t.Fatalf("attachment = %+v", got)
	}
}

func TestRunRefusesUnknownAttachmentBlob(t *testing.T) {
	chat := &attachmentChat{fakeChat: newFakeChat()}
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	payload := `{"sessionId":"s1","text":"x","attachments":["nosuchblob"],"clientRunId":"run-attach-2"}`
	response := openRunRaw(t, gw, payload)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown blob run = %d, want 400", response.StatusCode)
	}
	if len(chat.got) != 0 {
		t.Fatalf("an unknown blob must not reach the chat service")
	}
}

func TestBlobUploadRejectsEmptyBody(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)
	if status, _ := uploadBlob(t, gw, nil, "image/png", "empty.png"); status != http.StatusBadRequest {
		t.Fatalf("empty upload = %d, want 400", status)
	}
}

func TestBlobNeedsTheToken(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)
	request, err := http.NewRequest(http.MethodGet, "http://"+gw.address+"/api/blobs/abc", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without a token = %d, want 401", response.StatusCode)
	}
}

// openRunRaw posts a run body with the bearer token and the served origin: the run
// endpoint authenticates like the rest of /api/ (§3.1).
func openRunRaw(t *testing.T, gw *runningGateway, payload string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, "http://"+gw.address+"/api/chat/run", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Origin", gw.info.Origin())
	request.Header.Set("Authorization", "Bearer "+gw.info.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST /api/chat/run: %v", err)
	}
	return response
}
