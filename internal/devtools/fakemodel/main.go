// Command fakemodel is the acceptance fixture's OpenAI-compatible model: a
// deterministic SSE endpoint so the end-to-end flow (create a session, send a
// turn, park on an approval, allow, stream to completion, reload) can run in a
// sandbox that has no provider key.
//
// It is a development tool, never part of the product: `go run` it, or let
// scripts/acceptance/serve-with-fake-model.sh build and start it. Nothing in
// internal/gateway or internal/modules knows it exists — the product reaches it
// the only way it reaches a real provider, through the settings rows
// chat.baseURL / chat.model / chat.apiKey.
//
// The script is chosen by the MODEL NAME the product sends (no environment
// variables, no hidden global state):
//
//	plain*  a short streamed answer, then done
//	tool*   one tool_call (web_fetch against this server's own /health, a network
//	        tool the workspace policy parks on the approval card), then — once the
//	        tool result comes back — a streamed summary
//	slow*   one token every 200ms for ~30s, for stop / disconnect testing
//	think*  reasoning_content deltas, then the answer (the Think surface)
//	rich*   markdown-rich answer: headings, lists, table, inline code, fences,
//	        a link, a blockquote — the renderer's whole vocabulary in one turn
//	long*   ~120 lines of prose, for scrolling and windowed rendering
//	multi*  two sequential tool calls in one turn, then a summary
//	fail*   a tool call that fails (a closed port), then a summary about it
//	fence*  an unclosed fence mid-stream that closes at the end
//
// Paths: POST /v1/chat/completions (and /chat/completions, so a base URL with or
// without the /v1 suffix works). GET /health answers "ok" for the launcher.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9977", "listen address")
	quiet := flag.Bool("quiet", false, "log only startup and errors")
	flag.Parse()
	if *quiet {
		log.SetOutput(os.Stderr)
	}

	server := &http.Server{Addr: *addr, Handler: &handler{addr: *addr}}
	fmt.Printf("fakemodel: listening on http://%s (scenarios by model name: plain*/tool*/slow*)\n", *addr)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		_ = server.Close()
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("fakemodel: %v", err)
	}
}

// handler serves the model endpoints.
type handler struct {
	requests atomic.Int64
	// addr is this server's listen address, so the tool script can fetch itself.
	addr string
}

// ServeHTTP routes the model surface.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/health":
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
		return
	case strings.HasSuffix(r.URL.Path, "/chat/completions") && r.Method == http.MethodPost:
		h.complete(w, r)
		return
	}
	http.NotFound(w, r)
}

// completionRequest is the part of the OpenAI request the scripts read.
type completionRequest struct {
	Model string `json:"model"`
	Tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

// complete runs the scenario the model name selects.
func (h *handler) complete(w http.ResponseWriter, r *http.Request) {
	var request completionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, `{"error":{"message":"bad request body"}}`, http.StatusBadRequest)
		return
	}
	number := h.requests.Add(1)
	scenario := scenarioOf(request.Model)
	names := make([]string, 0, len(request.Tools))
	for _, tool := range request.Tools {
		names = append(names, tool.Function.Name)
	}
	log.Printf("fakemodel: request %d model=%q messages=%d scenario=%s tools=%s", number, request.Model, len(request.Messages), scenario, strings.Join(names, ","))

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	streamer := &sseWriter{w: w, flush: func() {
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}}

	switch scenario {
	case "tool":
		if !hasToolResult(request) {
			// web_fetch is a network tool: the workspace policy queues it for the
			// user instead of auto-allowing it like the file tools, and pointing
			// it at our own /health keeps the acceptance run offline.
			arguments, _ := json.Marshal(map[string]string{"url": "http://" + h.addr + "/health"})
			streamer.toolCall("web_fetch", string(arguments))
			streamer.finish("tool_calls", "")
			streamer.done()
			return
		}
		answer := "The fetch returned ok, so the approval chain works."
		streamer.text(answer, 25*time.Millisecond)
		streamer.finish("stop", answer)
		streamer.done()
	case "think":
		if !hasToolResult(request) {
			streamer.reasoning("The user asked a question. I should look at the workspace first, then answer with a short list. Checking the two files that matter. ", 20*time.Millisecond)
			answer := "Here is the short answer after thinking."
			streamer.text(answer, 25*time.Millisecond)
			streamer.finish("stop", answer)
			streamer.done()
			return
		}
		answer := "Done."
		streamer.text(answer, 25*time.Millisecond)
		streamer.finish("stop", answer)
		streamer.done()
	case "rich":
		answer := richAnswer
		streamer.text(answer, 8*time.Millisecond)
		streamer.finish("stop", answer)
		streamer.done()
	case "long":
		answer := longAnswer()
		streamer.text(answer, 2*time.Millisecond)
		streamer.finish("stop", answer)
		streamer.done()
	case "fence":
		answer := "Here is a snippet:\n\n```go\nfunc main() {\n\tprintln(\"hi\")\n}\n```\n\nThat is all."
		streamer.text(answer, 30*time.Millisecond)
		streamer.finish("stop", answer)
		streamer.done()
	case "thinktwice":
		// Two thinking rounds around one tool: the transcript must interleave
		// them, not stack both thoughts above the card.
		results := toolResultCount(request)
		if results == 0 {
			streamer.reasoning("First I should look at the endpoint. ", 20*time.Millisecond)
			arguments, _ := json.Marshal(map[string]string{"url": "http://" + h.addr + "/health"})
			streamer.toolCallAt("web_fetch", string(arguments), 0)
			streamer.finish("tool_calls", "")
			streamer.done()
			return
		}
		streamer.reasoning("The endpoint answered ok, so I can summarise. ", 20*time.Millisecond)
		answer := "It answered ok, so here is the summary."
		streamer.text(answer, 25*time.Millisecond)
		streamer.finish("stop", answer)
		streamer.done()
	case "longcmd":
		// A tool call whose argument and output are both one long unbreakable
		// token: the card must scroll inside itself, not widen the transcript.
		if !hasToolResult(request) {
			long := strings.Repeat("A", 300)
			arguments, _ := json.Marshal(map[string]string{"command": "echo " + long})
			streamer.toolCall("bash", string(arguments))
			streamer.finish("tool_calls", "")
			streamer.done()
			return
		}
		answer := "Done."
		streamer.text(answer, 25*time.Millisecond)
		streamer.finish("stop", answer)
		streamer.done()
	case "thinktool":
		// Reasoning, then a tool, then the answer: the whole loop in one turn.
		if !hasToolResult(request) {
			streamer.reasoning("I need the health endpoint before I answer. Fetching it now. ", 20*time.Millisecond)
			arguments, _ := json.Marshal(map[string]string{"url": "http://" + h.addr + "/health"})
			streamer.toolCall("web_fetch", string(arguments))
			streamer.finish("tool_calls", "")
			streamer.done()
			return
		}
		answer := "The endpoint answered, so here is the summary."
		streamer.text(answer, 25*time.Millisecond)
		streamer.finish("stop", answer)
		streamer.done()
	case "multi":
		results := toolResultCount(request)
		if results < 2 {
			arguments, _ := json.Marshal(map[string]string{"url": "http://" + h.addr + "/health"})
			streamer.toolCallAt("web_fetch", string(arguments), results)
			streamer.finish("tool_calls", "")
			streamer.done()
			return
		}
		answer := "Both fetches finished."
		streamer.text(answer, 25*time.Millisecond)
		streamer.finish("stop", answer)
		streamer.done()
	case "fail":
		if !hasToolResult(request) {
			// A closed port: the approval is allowed, the tool itself fails.
			arguments, _ := json.Marshal(map[string]string{"url": "http://127.0.0.1:9/health"})
			streamer.toolCall("web_fetch", string(arguments))
			streamer.finish("tool_calls", "")
			streamer.done()
			return
		}
		answer := "The fetch failed, so I am reporting that instead."
		streamer.text(answer, 25*time.Millisecond)
		streamer.finish("stop", answer)
		streamer.done()
	case "slow":
		streamer.text(strings.Repeat("slow stream ", 30), 200*time.Millisecond)
		streamer.finish("stop", "")
		streamer.done()
	default:
		answer := "Hello from the acceptance fixture. This answer is deterministic."
		streamer.text(answer, 25*time.Millisecond)
		streamer.finish("stop", answer)
		streamer.done()
	}
}

// scenarioOf maps the model name to a script.
func scenarioOf(model string) string {
	name := strings.ToLower(model)
	switch {
	case strings.Contains(name, "thinktwice"):
		return "thinktwice"
	case strings.Contains(name, "longcmd"):
		return "longcmd"
	case strings.Contains(name, "thinktool"):
		return "thinktool"
	case strings.Contains(name, "multi"):
		return "multi"
	case strings.Contains(name, "fail"):
		return "fail"
	case strings.Contains(name, "think"):
		return "think"
	// `tool` last among the tool-ish names: "thinktool" contains "tool" too.
	case strings.Contains(name, "tool"):
		return "tool"
	case strings.Contains(name, "rich"):
		return "rich"
	case strings.Contains(name, "long"):
		return "long"
	case strings.Contains(name, "fence"):
		return "fence"
	case strings.Contains(name, "slow"):
		return "slow"
	default:
		return "plain"
	}
}

// hasToolResult reports whether the request already carries a tool result (so
// the tool script answers instead of asking for the tool again).
func hasToolResult(request completionRequest) bool {
	for _, message := range request.Messages {
		if message.Role == "tool" || message.Role == "function" {
			return true
		}
	}
	return false
}

// sseWriter writes OpenAI streaming frames.
type sseWriter struct {
	w     http.ResponseWriter
	flush func()
}

// text streams one answer in word-sized chunks.
//
// Newlines are part of the payload: `strings.Fields` would collapse the whole
// answer onto one line, which silently turned every markdown fixture into a
// single heading (the round-4 component audit caught it). Words stay word-sized
// so the stream still arrives incrementally.
func (s *sseWriter) text(answer string, delay time.Duration) {
	for _, piece := range splitKeepingLines(answer) {
		payload, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{"content": piece},
			}},
		})
		fmt.Fprintf(s.w, "data: %s\n\n", payload)
		s.flush()
		time.Sleep(delay)
	}
}

// splitKeepingLines splits into streamable chunks without losing whitespace:
// each chunk is a word plus the whitespace that follows it (newlines included).
func splitKeepingLines(text string) []string {
	var chunks []string
	start := 0
	inSpace := false
	for index, r := range text {
		space := r == ' ' || r == '\n' || r == '\t' || r == '\r'
		if !space && inSpace && index > start {
			chunks = append(chunks, text[start:index])
			start = index
		}
		inSpace = space
	}
	if start < len(text) {
		chunks = append(chunks, text[start:])
	}
	return chunks
}

// reasoning streams reasoning_content deltas (the Think surface's input).
func (s *sseWriter) reasoning(text string, delay time.Duration) {
	words := strings.Fields(text)
	for index, word := range words {
		piece := word
		if index > 0 {
			piece = " " + word
		}
		payload, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{"reasoning_content": piece},
			}},
		})
		fmt.Fprintf(s.w, "data: %s\n\n", payload)
		s.flush()
		time.Sleep(delay)
	}
}

// toolResultCount is how many tool results the conversation already carries.
func toolResultCount(request completionRequest) int {
	count := 0
	for _, message := range request.Messages {
		if message.Role == "tool" || message.Role == "function" {
			count++
		}
	}
	return count
}

// toolCall streams one tool call with a unique id per round.
func (s *sseWriter) toolCall(name, arguments string) {
	payload, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"index": 0,
			"delta": map[string]any{
				"tool_calls": []any{map[string]any{
					"index":    0,
					"id":       "call-acceptance",
					"type":     "function",
					"function": map[string]any{"name": name, "arguments": arguments},
				}},
			},
		}},
	})
	fmt.Fprintf(s.w, "data: %s\n\n", payload)
	s.flush()
}

// toolCallAt is toolCall with an explicit round, so sequential calls in one turn
// do not share an id.
func (s *sseWriter) toolCallAt(name, arguments string, round int) {
	payload, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"index": 0,
			"delta": map[string]any{
				"tool_calls": []any{map[string]any{
					"index":    0,
					"id":       fmt.Sprintf("call-acceptance-%d", round),
					"type":     "function",
					"function": map[string]any{"name": name, "arguments": arguments},
				}},
			},
		}},
	})
	fmt.Fprintf(s.w, "data: %s\n\n", payload)
	s.flush()
}

// finish streams the terminal chunk, with usage when the answer was text.
func (s *sseWriter) finish(reason, answer string) {
	choices := map[string]any{
		"index":         0,
		"delta":         map[string]any{},
		"finish_reason": reason,
	}
	chunk := map[string]any{"choices": []any{choices}}
	if answer != "" {
		prompt := 16
		completion := len(strings.Fields(answer))
		chunk["usage"] = map[string]any{
			"prompt_tokens":     prompt,
			"completion_tokens": completion,
			"total_tokens":      prompt + completion,
		}
	}
	payload, _ := json.Marshal(chunk)
	fmt.Fprintf(s.w, "data: %s\n\n", payload)
	s.flush()
}

// done terminates the stream.
func (s *sseWriter) done() {
	fmt.Fprint(s.w, "data: [DONE]\n\n")
	s.flush()
}
