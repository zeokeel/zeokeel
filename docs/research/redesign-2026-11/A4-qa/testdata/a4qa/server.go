package main

// The scripted OpenAI-compatible SSE server, copied from
// internal/devtools/ui-drive/server.go (a main package cannot be imported):
// one immediate markdown answer, and one slow streamed answer the QA cases use
// to reach the running/Stop state.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

type scriptMode int

const (
	scriptAnswer scriptMode = iota
	scriptSlowAnswer
)

// scriptedAnswer is the final markdown body every scripted turn ends with.
const scriptedAnswer = "## QA 脚本回复\n\n- 工作目录已确认\n- 可以继续下一步了\n\n```bash\ngo test ./... -count=1\n```"

type scriptServer struct {
	srv       *httptest.Server
	mu        sync.Mutex
	mode      scriptMode
	requests  int
	slowTotal time.Duration
}

func newScriptServer() *scriptServer {
	s := &scriptServer{mode: scriptAnswer, slowTotal: 8 * time.Second}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

func (s *scriptServer) URL() string { return s.srv.URL }
func (s *scriptServer) Close()      { s.srv.Close() }

func (s *scriptServer) SetMode(mode scriptMode) {
	s.mu.Lock()
	s.mode = mode
	s.requests = 0
	s.mu.Unlock()
}

func (s *scriptServer) next() (scriptMode, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	return s.mode, s.requests
}

func (s *scriptServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	mode, _ := s.next()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	if mode == scriptSlowAnswer {
		emitSlowAnswer(w, r, s.slowTotal)
	} else {
		emitAnswer(w)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func emitAnswer(w http.ResponseWriter) {
	emitSSE(w, `{"choices":[{"delta":{"content":`+jsonString(scriptedAnswer)+`},"index":0}]}`)
	emitSSE(w, `{"choices":[{"delta":{},"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":120,"completion_tokens":340,"total_tokens":460}}`)
}

// emitSlowAnswer streams chunks with delays, then keeps the turn open until the
// scripted duration elapses or the client cancels (the QA Stop case).
func emitSlowAnswer(w http.ResponseWriter, r *http.Request, total time.Duration) {
	chunks := []string{
		"## QA 慢速流式回复\n\n",
		"第一段：正在检查工作目录。\n\n",
		"第二段：正在汇总结果。\n\n",
		"第三段：即将完成。",
	}
	emitSSE(w, `{"choices":[{"delta":{"content":`+jsonString(chunks[0])+`},"index":0}]}`)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}
	deadline := time.Now().Add(total)
	for index := 1; index < len(chunks); index++ {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(120 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			break
		}
		emitSSE(w, `{"choices":[{"delta":{"content":`+jsonString(chunks[index])+`},"index":0}]}`)
		if flusher != nil {
			flusher.Flush()
		}
	}
	select {
	case <-r.Context().Done():
		return
	case <-time.After(5 * time.Second):
	}
	emitSSE(w, `{"choices":[{"delta":{},"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":90,"completion_tokens":30,"total_tokens":120}}`)
}

func emitSSE(w http.ResponseWriter, payload string) {
	fmt.Fprint(w, "data: "+payload+"\n\n")
}

func jsonString(text string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", "", "\t", `\t`)
	return `"` + replacer.Replace(text) + `"`
}
