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
	case strings.Contains(name, "tool"):
		return "tool"
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
func (s *sseWriter) text(answer string, delay time.Duration) {
	words := strings.Fields(answer)
	for index, word := range words {
		piece := word
		if index > 0 {
			piece = " " + word
		}
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

// toolCall streams one tool call.
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
