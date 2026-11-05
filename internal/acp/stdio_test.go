//go:build tdd_red

package acp

// TDD RED test for the stdio transport contract (see doc.go): newline-framed
// JSON-RPC over one byte-stream pair, concurrent requests correlated by id.
//
// Run: go test -count=1 ./internal/acp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// TestStdioLoopStreamsResponses drives Serve over io.Pipe pairs (the standard
// stdio stand-in) and sends two requests concurrently. A correct server must
// answer both, correlating each response to its request id even when responses
// complete out of order, and keep notifications/response frames on separate
// newline-terminated lines.
func TestStdioLoopStreamsResponses(t *testing.T) {
	agent := &fakeAgent{}
	srv, err := NewServer(Config{Agent: agent, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// client -> server and server -> client byte streams.
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	defer func() { _ = inR.Close(); _ = inW.Close(); _ = outR.Close(); _ = outW.Close() }()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ctx, inR, outW) }()

	// Two requests written concurrently, with the higher id written first so a
	// naive sequential implementation could still pass ordering but a broken
	// id correlation would not.
	lines := []string{
		rpcRequest(t, 102, MethodInitialize, InitializeRequest{ProtocolVersion: ProtocolVersion}),
		rpcRequest(t, 101, MethodInitialize, InitializeRequest{ProtocolVersion: ProtocolVersion}),
	}
	// Write the requests from one goroutine without waiting for the server: a
	// RED server returns before reading, and a blocking pipe write would hang
	// the test instead of failing it. Deferred pipe closes unblock the writer.
	go func() {
		for _, line := range lines {
			if _, werr := inW.Write([]byte(line + "\n")); werr != nil {
				return
			}
		}
		_ = inW.Close()
	}()

	type readResult struct {
		msgs []wireMessage
		err  error
	}
	readDone := make(chan readResult, 1)
	go func() {
		var buf bytes.Buffer
		_, copyErr := io.Copy(&buf, outR)
		if copyErr != nil {
			readDone <- readResult{err: copyErr}
			return
		}
		msgs, decodeErr := decodeFrames(buf.Bytes())
		readDone <- readResult{msgs: msgs, err: decodeErr}
	}()

	select {
	case serr := <-serveErr:
		if errors.Is(serr, ErrNotImplemented) {
			t.Fatalf("RED (TDD acceptance not yet implemented): stdio loop returned %v; implement newline-framed JSON-RPC with id-correlated concurrent responses", serr)
		}
		t.Fatalf("stdin reached EOF before any response was collected (serve error: %v)", serr)
	case res := <-readDone:
		if res.err != nil {
			t.Fatalf("read protocol stream: %v", res.err)
		}
		byID := map[string]bool{}
		for _, msg := range res.msgs {
			if msg.Method != "" || msg.ID == nil {
				continue // notification or unsolicited frame
			}
			byID[string(bytes.TrimSpace(msg.ID))] = true
			if msg.Error != nil {
				t.Fatalf("initialize over stdio returned error %+v", msg.Error)
			}
		}
		if !byID["101"] || !byID["102"] {
			t.Fatalf("response ids = %v, want both 101 and 102 correlated on the same connection", byID)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("RED: no response within 3s on the stdio transport (server loop not implemented)")
	}
}
