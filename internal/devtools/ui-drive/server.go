package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

// scriptMode selects the reply the scripted OpenAI-compatible server sends for
// the next chat completion request.
type scriptMode int

const (
	// scriptAnswer replies immediately with one markdown answer (the default).
	scriptAnswer scriptMode = iota
	// scriptToolCallThenAnswer replies with a team_task_create tool call on the
	// first request (a write the default workspace permission still gates, so it
	// parks on the approval gate) and the markdown answer on the request after
	// the tool result. working_directory is auto-allowed now, so it can no
	// longer exercise the dialog.
	scriptToolCallThenAnswer
	// scriptSlowAnswer streams a long answer in small delayed chunks so the
	// driver can observe the running state and press Stop.
	scriptSlowAnswer
	// scriptSpawnTeamThenAnswer replies with a spawn_teammate tool call on the
	// first request and the markdown answer afterwards, so a driver step can
	// start a real teammate from inside a chat turn.
	scriptSpawnTeamThenAnswer
)

// scriptedAnswer is the final markdown body every scripted turn ends with.
const scriptedAnswer = "## 脚本回复\n\n- 工作目录已确认\n- 可以继续下一步了\n\n```bash\ngo test ./... -count=1\n```"

// scriptServer is an OpenAI-compatible SSE server whose reply depends on the
// script mode the driver selects before each send.
type scriptServer struct {
	srv *httptest.Server

	mu        sync.Mutex
	mode      scriptMode
	requests  int
	slowTotal time.Duration
}

func newScriptServer() *scriptServer {
	s := &scriptServer{mode: scriptAnswer, slowTotal: 3 * time.Second}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// URL returns the base URL the product settings point at.
func (s *scriptServer) URL() string { return s.srv.URL }

// Close shuts the server down.
func (s *scriptServer) Close() { s.srv.Close() }

// SetMode selects the reply script and resets the per-turn request counter.
func (s *scriptServer) SetMode(mode scriptMode) {
	s.mu.Lock()
	s.mode = mode
	s.requests = 0
	s.mu.Unlock()
}

// mode returns the current script and increments the per-turn request count.
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
	mode, request := s.next()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	switch mode {
	case scriptToolCallThenAnswer:
		if request == 1 {
			emitSSE(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"team_task_create","arguments":"{}"}}]},"index":0,"finish_reason":"tool_calls"}]}`)
		} else {
			emitAnswer(w)
		}
	case scriptSlowAnswer:
		emitSlowAnswer(w, r, s.slowTotal)
	case scriptSpawnTeamThenAnswer:
		if request == 1 {
			emitSSE(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-team-1","type":"function","function":{"name":"spawn_teammate","arguments":"{\"name\":\"研究员\",\"prompt\":\"研究一下当前工作目录并给出摘要\"}"}}]},"index":0,"finish_reason":"tool_calls"}]}`)
		} else {
			emitAnswer(w)
		}
	default:
		emitAnswer(w)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// emitAnswer writes one complete scripted markdown answer with usage.
func emitAnswer(w http.ResponseWriter) {
	emitSSE(w, `{"choices":[{"delta":{"content":`+jsonString(scriptedAnswer)+`},"index":0}]}`)
	emitSSE(w, `{"choices":[{"delta":{},"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":120,"completion_tokens":340,"total_tokens":460,"prompt_cache_hit_tokens":20}}`)
}

// emitSlowAnswer streams content in delayed chunks until the client cancels
// (the driver's Stop) or the scripted duration elapses.
func emitSlowAnswer(w http.ResponseWriter, r *http.Request, total time.Duration) {
	chunks := []string{"## 慢速流式回复\n\n", "第一段：正在检查工作目录。\n\n", "第二段：正在汇总结果。\n\n", "第三段：即将完成。"}
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
		case <-time.After(180 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			break
		}
		emitSSE(w, `{"choices":[{"delta":{"content":`+jsonString(chunks[index])+`},"index":0}]}`)
		if flusher != nil {
			flusher.Flush()
		}
	}
	// One more delayed chunk keeps the turn running long enough for the driver
	// to see the Stop control after the first frames.
	select {
	case <-r.Context().Done():
		return
	case <-time.After(2500 * time.Millisecond):
	}
	emitSSE(w, `{"choices":[{"delta":{},"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":90,"completion_tokens":30,"total_tokens":120}}`)
}

// emitSSE writes one data frame.
func emitSSE(w http.ResponseWriter, payload string) {
	fmt.Fprint(w, "data: "+payload+"\n\n")
}

// jsonString encodes text as a JSON string literal (enough for the scripted
// payloads; no control characters are used).
func jsonString(text string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", ``, "\t", `\t`)
	return `"` + replacer.Replace(text) + `"`
}
