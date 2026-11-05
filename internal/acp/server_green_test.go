package acp

// GREEN acceptance additions for the minimum ACP server: cancel settles the
// prompt with stopReason "cancelled", and the server forwards the adapter's
// semantic update sequence (text/thought/tool/usage) in order before the
// response.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// blockingAgent blocks Prompt until its context is cancelled (the behavior a
// real headless runtime has while a turn is running).
type blockingAgent struct {
	started chan struct{}
}

func (b *blockingAgent) NewSession(context.Context, NewSessionRequest) (NewSessionResponse, error) {
	return NewSessionResponse{SessionID: "session-block"}, nil
}

func (b *blockingAgent) Prompt(ctx context.Context, _ PromptRequest, _ UpdateSink) (PromptResponse, error) {
	if b.started != nil {
		select {
		case b.started <- struct{}{}:
		default:
		}
	}
	<-ctx.Done()
	return PromptResponse{}, ctx.Err()
}

func (b *blockingAgent) Cancel(context.Context, string) error { return nil }
func (b *blockingAgent) Close(context.Context) error          { return nil }

// interactive drives one Serve connection over io.Pipe pairs so a test can
// interleave frames deterministically (the preloaded-input style cannot express
// "cancel only after the prompt is actually running").
type interactive struct {
	t      *testing.T
	inW    *io.PipeWriter
	outR   *io.PipeReader
	lines  chan []byte
	srvErr chan error
	cancel context.CancelFunc
}

func startInteractive(t *testing.T, srv *Server) *interactive {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	i := &interactive{t: t, inW: inW, outR: outR, lines: make(chan []byte, 16), srvErr: make(chan error, 1), cancel: cancel}
	go func() { i.srvErr <- srv.Serve(ctx, inR, outW) }()
	go func() {
		reader := bufio.NewReader(outR)
		for {
			line, err := reader.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) > 0 {
				i.lines <- line
			}
			if err != nil {
				close(i.lines)
				return
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = inW.Close()
		_ = inR.Close()
		_ = outR.Close()
		_ = outW.Close()
		select {
		case <-i.srvErr:
		case <-time.After(2 * time.Second):
			t.Errorf("Serve did not return after the connection closed")
		}
	})
	return i
}

func (i *interactive) send(line string) {
	i.t.Helper()
	if _, err := i.inW.Write([]byte(line + "\n")); err != nil {
		i.t.Fatalf("write frame: %v", err)
	}
}

// readFrame returns the next decoded frame (response or notification).
func (i *interactive) readFrame(timeout time.Duration) wireMessage {
	i.t.Helper()
	select {
	case line, ok := <-i.lines:
		if !ok {
			i.t.Fatalf("protocol stream closed while waiting for a frame")
		}
		msgs, err := decodeFrames(line)
		if err != nil {
			i.t.Fatalf("decode frame: %v (line %q)", err, string(line))
		}
		if len(msgs) != 1 {
			i.t.Fatalf("expected one frame per line, got %d", len(msgs))
		}
		return msgs[0]
	case <-time.After(timeout):
		i.t.Fatalf("no protocol frame within %s", timeout)
		return wireMessage{}
	}
}

// readResponse skips notifications until it finds the response for id.
func (i *interactive) readResponse(id int, timeout time.Duration) wireMessage {
	i.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			i.t.Fatalf("no response for id %d within %s", id, timeout)
		}
		msg := i.readFrame(remaining)
		if msg.Method == "" && bytes.Equal(bytes.TrimSpace(msg.ID), []byte(jsonInt(id))) {
			return msg
		}
	}
}

func TestServerCancelSettlesPromptAsCancelled(t *testing.T) {
	agent := &blockingAgent{started: make(chan struct{}, 1)}
	srv, err := NewServer(Config{Agent: agent, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ic := startInteractive(t, srv)
	ic.send(rpcRequest(t, 1, MethodInitialize, InitializeRequest{ProtocolVersion: ProtocolVersion}))
	if resp := ic.readResponse(1, 2*time.Second); resp.Error != nil {
		t.Fatalf("initialize: %+v", resp.Error)
	}
	ic.send(rpcRequest(t, 2, MethodSessionNew, NewSessionRequest{Cwd: t.TempDir()}))
	if resp := ic.readResponse(2, 2*time.Second); resp.Error != nil {
		t.Fatalf("session/new: %+v", resp.Error)
	}
	ic.send(rpcRequest(t, 3, MethodSessionPrompt, PromptRequest{SessionID: "session-block", Prompt: []ContentBlock{{Type: "text", Text: "long task"}}}))
	select {
	case <-agent.started:
	case <-time.After(2 * time.Second):
		t.Fatal("agent.Prompt did not start")
	}
	// The prompt is running: cancel it.
	ic.send(rpcNotification(t, MethodSessionCancel, CancelNotification{SessionID: "session-block"}))
	resp := ic.readResponse(3, 3*time.Second)
	if resp.Error != nil {
		t.Fatalf("cancelled prompt returned an error instead of a stopReason: %+v", resp.Error)
	}
	var result PromptResponse
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("prompt result: %v", err)
	}
	if result.StopReason != StopReasonCancelled {
		t.Fatalf("stopReason = %q, want %q", result.StopReason, StopReasonCancelled)
	}
}

// updateAgent streams one update of each projected kind, then settles.
type updateAgent struct{}

func (updateAgent) NewSession(context.Context, NewSessionRequest) (NewSessionResponse, error) {
	return NewSessionResponse{SessionID: "session-updates"}, nil
}

func (updateAgent) Prompt(_ context.Context, req PromptRequest, updates UpdateSink) (PromptResponse, error) {
	text := ContentBlock{Type: "text", Text: "answer"}
	updates(SessionUpdateNotification{SessionID: req.SessionID, Update: SessionUpdate{SessionUpdate: UpdateAgentMessageChunk, Content: &text}})
	thought := ContentBlock{Type: "text", Text: "thinking"}
	updates(SessionUpdateNotification{SessionID: req.SessionID, Update: SessionUpdate{SessionUpdate: UpdateAgentThoughtChunk, Content: &thought}})
	updates(SessionUpdateNotification{SessionID: req.SessionID, Update: SessionUpdate{SessionUpdate: UpdateToolCall, ToolCallID: "call-1", Title: "bash", Status: "pending"}})
	updates(SessionUpdateNotification{SessionID: req.SessionID, Update: SessionUpdate{SessionUpdate: UpdateToolCallUpdate, ToolCallID: "call-1", Status: "completed"}})
	updates(SessionUpdateNotification{SessionID: req.SessionID, Update: SessionUpdate{SessionUpdate: UpdateUsage, Usage: &Usage{InputTokens: 10, OutputTokens: 4, TotalTokens: 14}}})
	return PromptResponse{StopReason: StopReasonEndTurn}, nil
}

func (updateAgent) Cancel(context.Context, string) error { return nil }
func (updateAgent) Close(context.Context) error          { return nil }

func TestServerForwardsOrderedSessionUpdates(t *testing.T) {
	srv, err := NewServer(Config{Agent: updateAgent{}, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	input := strings.Join([]string{
		rpcRequest(t, 1, MethodInitialize, InitializeRequest{ProtocolVersion: ProtocolVersion}),
		rpcRequest(t, 2, MethodSessionNew, NewSessionRequest{Cwd: t.TempDir()}),
		rpcRequest(t, 3, MethodSessionPrompt, PromptRequest{SessionID: "session-updates", Prompt: []ContentBlock{{Type: "text", Text: "go"}}}),
	}, "\n") + "\n"
	msgs := serveLines(t, srv, input)

	wantKinds := []string{UpdateAgentMessageChunk, UpdateAgentThoughtChunk, UpdateToolCall, UpdateToolCallUpdate, UpdateUsage}
	var got []string
	responseIdx := -1
	for i, msg := range msgs {
		if msg.Method == MethodSessionUpdate {
			var params SessionUpdateNotification
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				t.Fatalf("session/update params: %v", err)
			}
			if params.SessionID == "" {
				t.Fatalf("session/update %q has an empty sessionId", params.Update.SessionUpdate)
			}
			got = append(got, params.Update.SessionUpdate)
			continue
		}
		if msg.Method == "" && string(msg.ID) == "3" {
			responseIdx = i
		}
	}
	if len(got) != len(wantKinds) {
		t.Fatalf("session/update sequence = %v, want %v", got, wantKinds)
	}
	for i := range wantKinds {
		if got[i] != wantKinds[i] {
			t.Fatalf("session/update sequence = %v, want %v", got, wantKinds)
		}
	}
	if responseIdx < 0 {
		t.Fatal("no session/prompt response")
	}
	// All updates must precede the prompt response (ordered delivery).
	lastUpdate := -1
	for i, msg := range msgs {
		if msg.Method == MethodSessionUpdate {
			lastUpdate = i
		}
	}
	if lastUpdate > responseIdx {
		t.Fatalf("session/update delivered after the prompt response (lastUpdate=%d response=%d)", lastUpdate, responseIdx)
	}
}

// TestServeStopsOnContextCancellation pins the shutdown contract: cancelling
// the Serve context must return promptly even while stdin has no data (SIGINT
// from an editor must not wait for the next client line).
func TestServeStopsOnContextCancellation(t *testing.T) {
	srv := newTestServer(t)
	inR, inW := io.Pipe()
	out := &bytes.Buffer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { _ = inR.Close(); _ = inW.Close() }()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ctx, inR, out) }()
	// Give Serve a moment to block in the stdin read, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve returned %v, want nil/context.Canceled on shutdown", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return within 2s of context cancellation (blocking stdio read)")
	}
}
