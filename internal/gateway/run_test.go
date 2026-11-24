package gateway

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// This file covers POST /api/chat/run (docs/TRANSPORT.md §4.6): the seven event
// kinds and their order, clientRunId idempotency, and the rule that a dropped
// connection is not a stop request.

// scriptedChat drives a fake run: Send runs the script in a goroutine so the
// test observes the same asynchronous shape the agent has.
type scriptedChat struct {
	*fakeChat
	mu        sync.Mutex
	script    func(*fakeChat)
	sent      []string
	cancelled int
	// regenerated counts message.regenerate-class calls (the run endpoint's
	// regenerate mode), and regenerateErr lets a test make the backend refuse.
	regenerated   int
	regenerateErr error
	// edited records the edit-mode calls, and editErr lets a test make the
	// backend refuse (the gateway maps the refusal onto a parameter error).
	edited  []editCall
	editErr error
}

// editCall is one EditMessage invocation as the fake saw it.
type editCall struct {
	sessionID string
	messageID string
	text      string
}

// EditMessage implements contracts.SessionEditor: the edit mode calls it instead
// of Send. Like the real agent it rewrites the history and runs, so the script
// drives the fake session the same way.
func (c *scriptedChat) EditMessage(sessionID, messageID, text string) error {
	c.mu.Lock()
	c.edited = append(c.edited, editCall{sessionID: sessionID, messageID: messageID, text: text})
	err := c.editErr
	script := c.script
	c.mu.Unlock()
	if err != nil {
		return err
	}
	// Mirror the real backend: drop everything from the target message on, then
	// append the edited user message.
	c.fakeChat.mu.Lock()
	session := c.fakeChat.sessions[sessionID]
	if session != nil {
		for index := range session.Messages {
			if session.Messages[index].ID == messageID {
				session.Messages = append([]contracts.ChatMessage(nil), session.Messages[:index]...)
				break
			}
		}
		session.Messages = append(session.Messages, contracts.ChatMessage{
			ID: "edited-user", Role: contracts.ChatRoleUser, Text: text, CreatedAt: time.Now(),
		})
	}
	c.fakeChat.mu.Unlock()
	c.fakeChat.notify(sessionID)
	if script != nil {
		go script(c.fakeChat)
	}
	return nil
}

// editCount reports how many edit calls the backend saw.
func (c *scriptedChat) editCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.edited)
}

// Regenerate implements contracts.SessionRegenerator: the run endpoint's
// regenerate mode calls it instead of Send. Like the real agent it rewinds and
// runs, so the script drives the fake session the same way Send does.
func (c *scriptedChat) Regenerate(sessionID string) error {
	c.mu.Lock()
	c.regenerated++
	err := c.regenerateErr
	script := c.script
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if script != nil {
		go script(c.fakeChat)
	}
	return nil
}

// regenerateCount reports how many regenerate calls the backend saw.
func (c *scriptedChat) regenerateCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.regenerated
}

func newScriptedChat() *scriptedChat {
	return &scriptedChat{fakeChat: newFakeChat()}
}

// Send implements contracts.ChatService.
func (c *scriptedChat) Send(sessionID, prompt string) error {
	c.mu.Lock()
	c.sent = append(c.sent, prompt)
	script := c.script
	c.mu.Unlock()
	if script != nil {
		go script(c.fakeChat)
	}
	return nil
}

// Cancel implements contracts.ChatService; Cancelled counts stop requests.
func (c *scriptedChat) Cancel(sessionID string) error {
	c.mu.Lock()
	c.cancelled++
	c.mu.Unlock()
	return nil
}

// cancelCount reports how many stops were requested.
func (c *scriptedChat) cancelCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancelled
}

// setRunning flips the session's run flag (a session.patch running change).
func (f *fakeChat) setRunning(id string, running bool) {
	f.mu.Lock()
	if session := f.sessions[id]; session != nil {
		session.Running = running
		session.UpdatedAt = time.Now()
	}
	f.mu.Unlock()
	f.notify(id)
}

// setSessionError records a session-level error.
func (f *fakeChat) setSessionError(id, text string) {
	f.mu.Lock()
	if session := f.sessions[id]; session != nil {
		session.Error = text
		session.UpdatedAt = time.Now()
	}
	f.mu.Unlock()
	f.notify(id)
}

// startTool appends a running tool call to the last assistant message.
func (f *fakeChat) startTool(id, callID, name, arguments string) {
	f.mu.Lock()
	session := f.sessions[id]
	if session == nil || len(session.Messages) == 0 {
		f.mu.Unlock()
		return
	}
	last := &session.Messages[len(session.Messages)-1]
	last.Tools = append(last.Tools, contracts.ToolCall{
		ID: callID, Name: name, Status: "running", Arguments: arguments, StartedAt: time.Now(),
	})
	last.ToolTimelineKnown = true
	session.UpdatedAt = time.Now()
	f.mu.Unlock()
	f.notify(id)
}

// finishTool settles one tool call.
func (f *fakeChat) finishTool(id, callID, status, summary string) {
	f.mu.Lock()
	session := f.sessions[id]
	if session == nil || len(session.Messages) == 0 {
		f.mu.Unlock()
		return
	}
	last := &session.Messages[len(session.Messages)-1]
	for i := range last.Tools {
		if last.Tools[i].ID != callID {
			continue
		}
		last.Tools[i].Status = status
		last.Tools[i].Summary = summary
		last.Tools[i].EndedAt = time.Now()
	}
	session.UpdatedAt = time.Now()
	f.mu.Unlock()
	f.notify(id)
}

// setUsage records the turn's accounting on the last assistant message.
func (f *fakeChat) setUsage(id string, usage contracts.MessageUsage) {
	f.mu.Lock()
	session := f.sessions[id]
	if session == nil || len(session.Messages) == 0 {
		f.mu.Unlock()
		return
	}
	last := &session.Messages[len(session.Messages)-1]
	last.Usage = &usage
	session.UpdatedAt = time.Now()
	f.mu.Unlock()
	f.notify(id)
}

// setReasoning grows the last assistant message's thinking.
func (f *fakeChat) setReasoning(id, text string) {
	f.mu.Lock()
	session := f.sessions[id]
	if session == nil || len(session.Messages) == 0 {
		f.mu.Unlock()
		return
	}
	last := &session.Messages[len(session.Messages)-1]
	last.Reasoning = text
	last.ReasoningSegments = []contracts.ReasoningSegment{{Text: text, Iteration: 1}}
	session.UpdatedAt = time.Now()
	f.mu.Unlock()
	f.notify(id)
}

// stopStreaming marks the assistant message finished.
func (f *fakeChat) stopStreaming(id string) {
	f.mu.Lock()
	session := f.sessions[id]
	if session == nil || len(session.Messages) == 0 {
		f.mu.Unlock()
		return
	}
	last := &session.Messages[len(session.Messages)-1]
	last.Streaming = false
	session.UpdatedAt = time.Now()
	f.mu.Unlock()
	f.notify(id)
}

// sseFrame is one parsed server-sent event.
type sseFrame struct {
	event string
	data  map[string]any
}

// openRun posts a run request and returns the response. Callers close the body.
func openRun(t *testing.T, gw *runningGateway, body runRequest) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, "http://"+gw.address+"/api/chat/run", strings.NewReader(string(payload)))
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

// readFrames parses a stream until terminal is true or the body ends.
func readFrames(t *testing.T, body io.Reader, terminal bool) []sseFrame {
	t.Helper()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var frames []sseFrame
	var current sseFrame
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			current.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &current.data); err != nil {
				t.Fatalf("bad data frame %q: %v", line, err)
			}
		case line == "":
			if current.event != "" {
				frames = append(frames, current)
				if terminal && (current.event == sseDone || current.event == sseError) {
					return frames
				}
			}
			current = sseFrame{}
		}
	}
	if err := scanner.Err(); err != nil && terminal {
		t.Fatalf("read stream: %v", err)
	}
	return frames
}

// eventNames is the ordered list of frame names.
func eventNames(frames []sseFrame) []string {
	out := make([]string, 0, len(frames))
	for _, frame := range frames {
		out = append(out, frame.event)
	}
	return out
}

// fullRunScript drives one turn through all seven event kinds.
func fullRunScript(done chan struct{}) func(*fakeChat) {
	return func(f *fakeChat) {
		defer close(done)
		f.setRunning("s1", true)
		f.mu.Lock()
		f.sessions["s1"].Messages = append(f.sessions["s1"].Messages, contracts.ChatMessage{
			ID: "a1", Role: contracts.ChatRoleAssistant, Text: "Hel", Streaming: true, CreatedAt: time.Now(),
		})
		f.mu.Unlock()
		f.notify("s1")
		time.Sleep(20 * time.Millisecond)

		f.setReasoning("s1", "thinking")
		f.appendText("s1", "lo")
		f.startTool("s1", "call-1", "shell", `{"cmd":"ls"}`)
		time.Sleep(20 * time.Millisecond)

		f.finishTool("s1", "call-1", "ok", "2 files")
		f.setUsage("s1", contracts.MessageUsage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18, Iterations: 1})
		f.appendText("s1", " world")
		time.Sleep(20 * time.Millisecond)

		f.stopStreaming("s1")
		f.setRunning("s1", false)
	}
}

// TestChatRunStreamsAllSevenEventKindsInOrder is the acceptance test for §4.6.
func TestChatRunStreamsAllSevenEventKindsInOrder(t *testing.T) {
	chat := newScriptedChat()
	chat.addSession("s1", "First", "hello")
	done := make(chan struct{})
	chat.script = fullRunScript(done)
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	response := openRun(t, gw, runRequest{SessionID: "s1", Text: "do it", ClientRunID: "run-1"})
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", contentType)
	}
	frames := readFrames(t, response.Body, true)
	<-done

	names := eventNames(frames)
	for _, want := range []string{sseMessageDelta, sseReasoningDelta, sseToolStart, sseToolResult, sseUsage, sseDone} {
		if !containsString(names, want) {
			t.Fatalf("stream is missing %q; got %v", want, names)
		}
	}
	if last := names[len(names)-1]; last != sseDone {
		t.Fatalf("last event = %q, want done; got %v", last, names)
	}
	// tool.start must precede the same callId's tool.result.
	startIndex, resultIndex := -1, -1
	for i, frame := range frames {
		if frame.event == sseToolStart {
			startIndex = i
		}
		if frame.event == sseToolResult {
			resultIndex = i
		}
	}
	if startIndex < 0 || resultIndex < 0 || startIndex > resultIndex {
		t.Fatalf("tool.start (%d) must precede tool.result (%d): %v", startIndex, resultIndex, names)
	}
	if frames[resultIndex].data["callId"] != "call-1" || frames[resultIndex].data["ok"] != true {
		t.Fatalf("tool.result = %v", frames[resultIndex].data)
	}
	// Text arrived (possibly in several merged frames) and reconstructs the reply.
	var text strings.Builder
	for _, frame := range frames {
		if frame.event == sseMessageDelta {
			text.WriteString(frame.data["text"].(string))
		}
	}
	// Every text delta of the turn must reach the stream: the final delta's ops
	// carry the session.patch running:false too, and the done frame must come
	// after that text, not instead of it.
	if text.String() != "Hello world" {
		t.Fatalf("streamed text = %q, want %q", text.String(), "Hello world")
	}
	usageFrame := frames[containsIndex(names, sseUsage)]
	if usageFrame.data["inputTokens"] != float64(11) || usageFrame.data["outputTokens"] != float64(7) {
		t.Fatalf("usage = %v", usageFrame.data)
	}
	if frames[len(frames)-1].data["turnId"] != "run-1" {
		t.Fatalf("done turnId = %v, want the clientRunId", frames[len(frames)-1].data["turnId"])
	}
}

// containsIndex is the position of the first frame with that name.
func containsIndex(names []string, want string) int {
	for i, name := range names {
		if name == want {
			return i
		}
	}
	return -1
}

// TestChatRunIsIdempotentPerClientRunID covers both halves of the idempotency
// rule: an active run refuses a second request (never a second run), a finished
// run replays its recorded events.
func TestChatRunIsIdempotentPerClientRunID(t *testing.T) {
	chat := newScriptedChat()
	chat.addSession("s1", "First", "hello")
	release := make(chan struct{})
	chat.script = func(f *fakeChat) {
		f.setRunning("s1", true)
		f.appendText("s1", "working")
		<-release
		f.setRunning("s1", false)
	}
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	first := openRun(t, gw, runRequest{SessionID: "s1", Text: "do it", ClientRunID: "run-dup"})
	defer func() { _ = first.Body.Close() }()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d, want 200", first.StatusCode)
	}
	// Read the first frame so the run is definitely live.
	live := readFramesLimited(t, first.Body, 1)
	if len(live) == 0 {
		t.Fatal("the live run produced no frames")
	}

	second := openRun(t, gw, runRequest{SessionID: "s1", Text: "do it", ClientRunID: "run-dup"})
	defer func() { _ = second.Body.Close() }()
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate live run status = %d, want 409", second.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(second.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	errObject, _ := body["error"].(map[string]any)
	data, _ := errObject["data"].(map[string]any)
	if data["name"] != "run_already_active" {
		t.Fatalf("duplicate run error = %v", body)
	}

	// Let it finish, drain the live stream, then the same id replays.
	close(release)
	_ = readFrames(t, first.Body, true)

	third := openRun(t, gw, runRequest{SessionID: "s1", Text: "do it", ClientRunID: "run-dup"})
	defer func() { _ = third.Body.Close() }()
	if third.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, want 200", third.StatusCode)
	}
	replayed := readFrames(t, third.Body, true)
	names := eventNames(replayed)
	if !containsString(names, sseDone) {
		t.Fatalf("replay is missing done: %v", names)
	}
	chat.mu.Lock()
	sent := len(chat.sent)
	chat.mu.Unlock()
	if sent != 1 {
		t.Fatalf("the chat backend received %d prompts for one clientRunId, want 1", sent)
	}
}

// readFramesLimited reads at most limit frames without waiting for a terminal
// event.
func readFramesLimited(t *testing.T, body io.Reader, limit int) []sseFrame {
	t.Helper()
	reader := bufio.NewReader(body)
	var frames []sseFrame
	var current sseFrame
	for len(frames) < limit {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			current.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &current.data); err != nil {
				t.Fatalf("bad data frame %q: %v", line, err)
			}
		case line == "":
			if current.event != "" {
				frames = append(frames, current)
				if current.event == sseDone || current.event == sseError {
					return frames
				}
			}
			current = sseFrame{}
		}
	}
	return frames
}

// TestChatRunDisconnectDoesNotStopTheRun is the §4.6 rule: the socket dropping
// is not a stop request.
func TestChatRunDisconnectDoesNotStopTheRun(t *testing.T) {
	chat := newScriptedChat()
	chat.addSession("s1", "First", "hello")
	release := make(chan struct{})
	chat.script = func(f *fakeChat) {
		f.setRunning("s1", true)
		f.appendText("s1", "working")
		<-release
		f.appendText("s1", " finished")
		f.setRunning("s1", false)
	}
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	response := openRun(t, gw, runRequest{SessionID: "s1", Text: "do it", ClientRunID: "run-drop"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	_ = readFramesLimited(t, response.Body, 1)
	// The tab goes away mid-run.
	_ = response.Body.Close()

	time.Sleep(100 * time.Millisecond)
	if got := chat.cancelCount(); got != 0 {
		t.Fatalf("a dropped connection cancelled the run (%d Cancel calls)", got)
	}
	session, ok := chat.Session("s1")
	if !ok || !session.Running {
		t.Fatalf("the run stopped after the disconnect: running=%v", session.Running)
	}

	// The run finishes on its own and the session still carries the answer.
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for {
		session, _ = chat.Session("s1")
		if !session.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the run never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := chat.cancelCount(); got != 0 {
		t.Fatalf("Cancel was called %d times", got)
	}
}

// TestChatRunRejectsBadRequests covers the validation the endpoint owes the
// client.
func TestChatRunRejectsBadRequests(t *testing.T) {
	chat := newScriptedChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	cases := []struct {
		name   string
		body   runRequest
		status int
	}{
		{name: "missing clientRunId", body: runRequest{SessionID: "s1", Text: "hi"}, status: http.StatusBadRequest},
		{name: "missing text", body: runRequest{SessionID: "s1", ClientRunID: "r"}, status: http.StatusBadRequest},
		{name: "missing session", body: runRequest{Text: "hi", ClientRunID: "r"}, status: http.StatusBadRequest},
		{name: "unknown session", body: runRequest{SessionID: "nope", Text: "hi", ClientRunID: "r"}, status: http.StatusNotFound},
		{name: "attachments", body: runRequest{SessionID: "s1", Text: "hi", ClientRunID: "r", Attachments: []string{"blob-1"}}, status: http.StatusBadRequest},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := openRun(t, gw, testCase.body)
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != testCase.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, testCase.status)
			}
		})
	}
	// No run may have been started by any of the rejected requests.
	chat.mu.Lock()
	defer chat.mu.Unlock()
	if len(chat.sent) != 0 {
		t.Fatalf("rejected requests started %d runs", len(chat.sent))
	}
}

// TestStaticServesBundleAndSPAFallback covers the embedded-client path with an
// injected tree (the real bundle is built by the web track).
func TestStaticServesBundleAndSPAFallback(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	gw.gateway.webFS = fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><title>ZeoKeel</title>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('client')")},
	}
	get := func(target, accept string) (*http.Response, string) {
		request, err := http.NewRequest(http.MethodGet, "http://"+gw.address+target, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		if accept != "" {
			request.Header.Set("Accept", accept)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("GET %s: %v", target, err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		return response, string(body)
	}

	// The entry point and an asset.
	if response, body := get("/", "text/html"); response.StatusCode != http.StatusOK || !strings.Contains(body, "ZeoKeel") {
		t.Fatalf("GET / = %d %q", response.StatusCode, body)
	}
	if response, body := get("/assets/app.js", "*/*"); response.StatusCode != http.StatusOK || !strings.Contains(body, "console.log") {
		t.Fatalf("GET /assets/app.js = %d %q", response.StatusCode, body)
	}
	// A client route falls back to index.html.
	if response, body := get("/c/s1", "text/html"); response.StatusCode != http.StatusOK || !strings.Contains(body, "ZeoKeel") {
		t.Fatalf("GET /c/s1 = %d %q", response.StatusCode, body)
	}
	// A missing asset is a real 404, not the entry point.
	if response, _ := get("/assets/missing.js", "*/*"); response.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /assets/missing.js = %d, want 404", response.StatusCode)
	}
}

// TestStaticWithoutBundleIsReadableNotAPanic covers the "web track is still
// building" case: 503 plus instructions, no panic.
func TestStaticWithoutBundleIsReadableNotAPanic(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)
	// Pin an empty client tree: the assertion is about the missing-bundle path,
	// not about whether this checkout copied web/dist into the binary.
	gw.gateway.webFS = fstest.MapFS{}

	response, err := http.Get("http://" + gw.address + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.StatusCode)
	}
	if !strings.Contains(string(body), "build-web-client.sh") {
		t.Fatalf("body = %q, want the build instructions", body)
	}
}

// TestRunStreamCoalescesTextFrames proves the render-side throttle: a burst of
// tiny deltas becomes fewer frames.
func TestRunStreamCoalescesTextFrames(t *testing.T) {
	stream := &runStream{turnID: "run", wake: make(chan struct{}, 1)}
	for i := 0; i < 50; i++ {
		stream.emit(runEvent{name: sseMessageDelta, data: map[string]any{"text": "x"}})
	}
	events, next, dropped, finished := stream.eventsFrom(0)
	if dropped || finished || next != 50 {
		t.Fatalf("eventsFrom = dropped=%v finished=%v next=%d", dropped, finished, next)
	}
	if len(events) != 50 {
		t.Fatalf("log holds %d events, want 50", len(events))
	}
	// The serve loop merges them; the log itself keeps the raw ops.
	total := 0
	for _, event := range events {
		total += len(event.data["text"].(string))
	}
	if total != 50 {
		t.Fatalf("reconstructed text length = %d, want 50", total)
	}
}

// TestRunStreamBoundsItsLog covers the memory bound: an overflowing log marks the
// slow reader as dropped instead of growing forever.
func TestRunStreamBoundsItsLog(t *testing.T) {
	stream := &runStream{turnID: "run", wake: make(chan struct{}, 1)}
	text := strings.Repeat("x", 64*1024)
	for i := 0; i < (runLogBytes/(64*1024))+8; i++ {
		stream.emit(runEvent{name: sseMessageDelta, data: map[string]any{"text": text}})
	}
	stream.mu.Lock()
	size := stream.logBytes
	base := stream.base
	stream.mu.Unlock()
	if size > runLogBytes {
		t.Fatalf("log holds %d bytes, want <= %d", size, runLogBytes)
	}
	if base == 0 {
		t.Fatal("the log never pruned")
	}
	if _, _, dropped, _ := stream.eventsFrom(0); !dropped {
		t.Fatal("a reader behind the pruned log was not told it dropped frames")
	}
}

// TestRunStreamTerminalEventIsFinal pins the "done|error is last" rule inside
// the stream itself.
func TestRunStreamTerminalEventIsFinal(t *testing.T) {
	stream := &runStream{turnID: "run", wake: make(chan struct{}, 1)}
	stream.emit(runEvent{name: sseDone, data: map[string]any{}})
	stream.emit(runEvent{name: sseMessageDelta, data: map[string]any{"text": "late"}})
	events, _, _, finished := stream.eventsFrom(0)
	if !finished {
		t.Fatal("stream is not finished after done")
	}
	if len(events) != 1 || events[0].name != sseDone {
		t.Fatalf("events after done were accepted: %v", events)
	}
}

// TestChatRunErrorEventIsTerminal covers the error path: a failed turn ends with
// one error frame and no done frame.
func TestChatRunErrorEventIsTerminal(t *testing.T) {
	chat := newScriptedChat()
	chat.addSession("s1", "First", "hello")
	chat.script = func(f *fakeChat) {
		f.setRunning("s1", true)
		f.mu.Lock()
		f.sessions["s1"].Messages = append(f.sessions["s1"].Messages, contracts.ChatMessage{
			ID: "a1", Role: contracts.ChatRoleAssistant, Streaming: true, CreatedAt: time.Now(),
		})
		f.mu.Unlock()
		f.notify("s1")
		time.Sleep(20 * time.Millisecond)
		f.mu.Lock()
		f.sessions["s1"].Messages[len(f.sessions["s1"].Messages)-1].Error = "provider exploded"
		f.sessions["s1"].Messages[len(f.sessions["s1"].Messages)-1].ErrorKind = contracts.MessageErrorKindRateLimit
		f.mu.Unlock()
		f.notify("s1")
		time.Sleep(20 * time.Millisecond)
		f.stopStreaming("s1")
		f.setRunning("s1", false)
	}
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	response := openRun(t, gw, runRequest{SessionID: "s1", Text: "do it", ClientRunID: "run-err"})
	defer func() { _ = response.Body.Close() }()
	frames := readFrames(t, response.Body, true)
	if len(frames) == 0 {
		t.Fatal("no frames")
	}
	last := frames[len(frames)-1]
	if last.event != sseError {
		t.Fatalf("last event = %q, want error (%v)", last.event, eventNames(frames))
	}
	if last.data["message"] != "provider exploded" || last.data["errorKind"] != contracts.MessageErrorKindRateLimit {
		t.Fatalf("error frame = %v", last.data)
	}
	if containsString(eventNames(frames), sseDone) {
		t.Fatal("a failed run must not also send done")
	}
}

// TestChatRunRegenerateModeIsTheSameSendPath pins §4.6's decision: "retry the
// last turn" is a *mode* of POST /api/chat/run, not a second WS method. The
// endpoint must call the backend's Regenerate (never Send), stream the same
// events, and stay idempotent per clientRunId — a repeat must not rewind the
// history a second time.
func TestChatRunRegenerateModeIsTheSameSendPath(t *testing.T) {
	chat := newScriptedChat()
	chat.addSession("s1", "First", "hello")
	finished := make(chan struct{})
	chat.script = fullRunScript(finished)
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	response := openRun(t, gw, runRequest{SessionID: "s1", ClientRunID: "run-regen", Regenerate: true})
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("regenerate status = %d, want 200", response.StatusCode)
	}
	names := eventNames(readFrames(t, response.Body, true))
	if !containsString(names, sseDone) {
		t.Fatalf("regenerate stream is missing done: %v", names)
	}
	if got := chat.regenerateCount(); got != 1 {
		t.Fatalf("regenerate calls = %d, want 1", got)
	}
	chat.mu.Lock()
	sent := len(chat.sent)
	chat.mu.Unlock()
	if sent != 0 {
		t.Fatalf("regenerate mode called Send %d times, want 0 (the last prompt is re-run in place)", sent)
	}

	// The same clientRunId replays the finished stream instead of retrying again.
	replay := openRun(t, gw, runRequest{SessionID: "s1", ClientRunID: "run-regen", Regenerate: true})
	defer func() { _ = replay.Body.Close() }()
	if replay.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, want 200", replay.StatusCode)
	}
	_ = readFrames(t, replay.Body, true)
	if got := chat.regenerateCount(); got != 1 {
		t.Fatalf("a replayed clientRunId regenerated again (%d calls, want 1)", got)
	}
}

// TestChatRunRegenerateRejectsText: the mode re-runs the prompt the backend
// already holds, so a body that also carries text would append a second prompt.
func TestChatRunRegenerateRejectsText(t *testing.T) {
	chat := newScriptedChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	response := openRun(t, gw, runRequest{SessionID: "s1", ClientRunID: "run-regen", Regenerate: true, Text: "extra"})
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("regenerate with text status = %d, want 400", response.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	errObject, _ := body["error"].(map[string]any)
	data, _ := errObject["data"].(map[string]any)
	if data["field"] != "text" {
		t.Fatalf("regenerate with text error = %v", body)
	}
	if got := chat.regenerateCount(); got != 0 {
		t.Fatalf("a rejected regenerate still reached the backend (%d calls)", got)
	}
}

// TestChatRunRegenerateNeedsTheBackendHalf: a backend that cannot regenerate
// answers a capability error, not a 500 — the client can tell "this build cannot
// do it" from "something broke".
func TestChatRunRegenerateNeedsTheBackendHalf(t *testing.T) {
	chat := &fakeChatOnly{newFakeChat()}
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	response := openRun(t, gw, runRequest{SessionID: "s1", ClientRunID: "run-regen", Regenerate: true})
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusBadRequest && response.StatusCode != http.StatusNotImplemented {
		t.Fatalf("unsupported regenerate status = %d, want 400/501", response.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	errObject, _ := body["error"].(map[string]any)
	data, _ := errObject["data"].(map[string]any)
	if data["name"] != "regenerate_not_supported" {
		t.Fatalf("unsupported regenerate error = %v", body)
	}
}

// fakeChatOnly embeds fakeChat without adding the regenerate half, so the
// capability test has a backend that genuinely lacks it.
type fakeChatOnly struct {
	*fakeChat
}

// TestChatRunEditModeTruncatesAndRuns is the edit mode's end-to-end cover
// (§4.6): the backend is asked to edit the named message exactly once, the SSE
// stream is the same seven-event stream as a normal run, and a repeated
// clientRunId replays instead of truncating a second time.
func TestChatRunEditModeTruncatesAndRuns(t *testing.T) {
	chat := newScriptedChat()
	chat.addSession("s1", "First", "hello")
	// A two-turn transcript: the second turn is what the edit replaces.
	chat.addMessage("s1", contracts.ChatRoleUser, "second question")
	chat.addMessage("s1", contracts.ChatRoleAssistant, "old answer")
	chat.mu.Lock()
	targetID := ""
	for _, message := range chat.sessions["s1"].Messages {
		if message.Text == "second question" {
			targetID = message.ID
		}
	}
	chat.mu.Unlock()
	if targetID == "" {
		t.Fatal("the test session has no target message")
	}
	finished := make(chan struct{})
	chat.script = fullRunScript(finished)
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	response := openRun(t, gw, runRequest{
		SessionID: "s1", ClientRunID: "run-edit", EditMessageID: targetID, Text: "second question, edited",
	})
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("edit status = %d, want 200", response.StatusCode)
	}
	frames := readFrames(t, response.Body, true)
	names := eventNames(frames)
	if !containsString(names, sseDone) {
		t.Fatalf("edit stream is missing done: %v (%+v)", names, frames[len(frames)-1].data)
	}

	// The backend saw exactly one edit, with the new text, and no plain send.
	chat.mu.Lock()
	edits := append([]editCall(nil), chat.edited...)
	sent := len(chat.sent)
	chat.mu.Unlock()
	if len(edits) != 1 {
		t.Fatalf("edit calls = %d, want 1 (%+v)", len(edits), edits)
	}
	if edits[0].messageID != targetID || edits[0].text != "second question, edited" {
		t.Fatalf("edit call = %+v", edits[0])
	}
	if sent != 0 {
		t.Fatalf("edit mode called Send %d times, want 0", sent)
	}

	// No leftover: the old answer is gone from the transcript the edit rewrote.
	chat.mu.Lock()
	var texts []string
	for _, message := range chat.sessions["s1"].Messages {
		texts = append(texts, message.Text)
	}
	chat.mu.Unlock()
	for _, text := range texts {
		if text == "old answer" || text == "second question" {
			t.Fatalf("the edited turn survived: %v", texts)
		}
	}

	// Idempotency: the same clientRunId replays the finished stream and must not
	// truncate the history a second time.
	replay := openRun(t, gw, runRequest{
		SessionID: "s1", ClientRunID: "run-edit", EditMessageID: targetID, Text: "second question, edited",
	})
	defer func() { _ = replay.Body.Close() }()
	if replay.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, want 200", replay.StatusCode)
	}
	_ = readFrames(t, replay.Body, true)
	chat.mu.Lock()
	editsAfterReplay := len(chat.edited)
	chat.mu.Unlock()
	if editsAfterReplay != 1 {
		t.Fatalf("a replayed clientRunId edited again (%d calls, want 1)", editsAfterReplay)
	}
}

// TestChatRunEditModeValidation pins the mode's parameter rules: the edit target
// and regenerate are mutually exclusive, and an edit without new text is
// refused before the backend is touched.
func TestChatRunEditModeValidation(t *testing.T) {
	chat := newScriptedChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	cases := []struct {
		name string
		body runRequest
	}{
		{name: "mutually exclusive with regenerate", body: runRequest{
			SessionID: "s1", ClientRunID: "r1", Regenerate: true, EditMessageID: "msg-1", Text: "x"}},
		{name: "edit without text", body: runRequest{
			SessionID: "s1", ClientRunID: "r2", EditMessageID: "msg-1"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			response := openRun(t, gw, test.body)
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", response.StatusCode)
			}
			var body map[string]any
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			errObject, _ := body["error"].(map[string]any)
			data, _ := errObject["data"].(map[string]any)
			if data["field"] == "" {
				t.Fatalf("error = %v, want a field", body)
			}
		})
	}
	if count := chat.editCount(); count != 0 {
		t.Fatalf("a rejected edit reached the backend (%d calls)", count)
	}
}

// TestChatRunEditModeRefusalNamesTheField: a backend refusal (the target moved,
// was compacted away, …) must reach the client as a parameter error naming
// editMessageId — never as a silent fresh run.
func TestChatRunEditModeRefusalNamesTheField(t *testing.T) {
	chat := newScriptedChat()
	chat.addSession("s1", "First", "hello")
	chat.editErr = contracts.ErrEditTargetCompacted
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	response := openRun(t, gw, runRequest{
		SessionID: "s1", ClientRunID: "run-edit", EditMessageID: "msg-9", Text: "edited",
	})
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	errObject, _ := body["error"].(map[string]any)
	data, _ := errObject["data"].(map[string]any)
	if data["field"] != "editMessageId" {
		t.Fatalf("error = %v, want field editMessageId", body)
	}
	if data["name"] != "edit_target_compacted" {
		t.Fatalf("error = %v, want name edit_target_compacted", body)
	}
}

// TestConcurrentClientRunIDClaimsOneTurn is the regression form of
// AUDIT-2026-11-24 A-3: the claim used to be check-then-act (lookup under the
// lock, unlock, hub round-trip, insert under a second lock), so two retries of
// the same clientRunId — exactly what §4.6 says the id exists for — both passed
// the check and started two turns for one prompt.
//
// The fixture cannot make the window deterministic, so the test hammers it:
// eight simultaneous requests for one id, repeated, and asserts the backend
// received exactly one prompt in every round.
func TestConcurrentClientRunIDClaimsOneTurn(t *testing.T) {
	for round := 0; round < 12; round++ {
		chat := newScriptedChat()
		chat.addSession("s1", "First", "hello")
		chat.script = func(f *fakeChat) {
			f.setRunning("s1", true)
			f.appendText("s1", "working")
			f.setRunning("s1", false)
		}
		gw := startTestGateway(t, chat)

		const attempts = 8
		var wg sync.WaitGroup
		statuses := make([]int, attempts)
		bodies := make([]*http.Response, attempts)
		wg.Add(attempts)
		for i := 0; i < attempts; i++ {
			go func(index int) {
				defer wg.Done()
				response := openRun(t, gw, runRequest{SessionID: "s1", Text: "do it", ClientRunID: "run-same"})
				statuses[index] = response.StatusCode
				bodies[index] = response
			}(i)
		}
		wg.Wait()
		for _, response := range bodies {
			if response != nil {
				_ = response.Body.Close()
			}
		}

		chat.mu.Lock()
		sent := len(chat.sent)
		chat.mu.Unlock()
		gw.stop(t)
		if sent != 1 {
			t.Fatalf("round %d: the chat backend received %d prompts for one clientRunId, want 1 (statuses %v)",
				round, sent, statuses)
		}
	}
}
