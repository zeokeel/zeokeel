package agent

// Tests for the sidebar's per-session run-state details: Waiting (the run was
// started but the model has not produced any output yet) and Unread (the run
// finished while the user was looking at another session). No network: the
// provider seam replays scripted streams, and gated streams model a silent
// in-flight request.

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// gatedStream holds the run at chosen points: the first Recv waits for first,
// and (when after is non-nil) the Recv following the first chunk waits for
// after. It models a model request that is in flight but has streamed nothing
// yet, and a stream that paused after its first output.
type gatedStream struct {
	first  <-chan struct{}
	after  <-chan struct{}
	chunks []rig.Chunk
	index  int
}

func (s *gatedStream) Recv() (rig.Chunk, error) {
	if s.index == 0 {
		<-s.first
	} else if s.after != nil {
		<-s.after
		s.after = nil
	}
	if s.index >= len(s.chunks) {
		return rig.Chunk{}, io.EOF
	}
	chunk := s.chunks[s.index]
	s.index++
	return chunk, nil
}

// scriptedGateProvider hands out the scripted streams, one per model call, and a plain
// final answer for any later call (the tool-call case needs a second call).
type scriptedGateProvider struct {
	mu      sync.Mutex
	streams []rig.Stream
	calls   int
}

func (p *scriptedGateProvider) Name() string { return "gated-script" }

func (p *scriptedGateProvider) Stream(context.Context, rig.Request) (rig.Stream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	index := p.calls
	p.calls++
	if index < len(p.streams) {
		return p.streams[index], nil
	}
	return &scriptedStream{chunks: []rig.Chunk{
		{Delta: "done"},
		{FinishReason: "stop"},
	}}, nil
}

// stalledProvider keeps the request in flight, producing no output, until the
// run is cancelled.
type stalledProvider struct{}

func (p *stalledProvider) Name() string { return "stalled" }

func (p *stalledProvider) Stream(ctx context.Context, _ rig.Request) (rig.Stream, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// newStateTestService builds a service whose provider comes from factory, with a
// model configured so Send starts a real run through the rig loop.
func newStateTestService(t *testing.T, factory func(contracts.ChatConfig) (rig.Provider, error), tools ...rig.Tool) (*service, *fakeSettings) {
	t.Helper()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	svc := newTestService(t, Config{}, settings, nil, tools...)
	svc.providerFactory = factory
	return svc, settings
}

// mustSession returns one session read model or fails.
func mustSession(t *testing.T, svc *service, sessionID string) contracts.ChatSession {
	t.Helper()
	session, ok := svc.Session(sessionID)
	if !ok {
		t.Fatalf("session %q missing", sessionID)
	}
	return session
}

// TestWaitingClearsOnFirstModelOutput covers the Waiting window: it is already
// true when Send returns, stays true while the model is silent, clears on the
// first text/reasoning/tool-call event while the run keeps streaming, and is
// false after the run finishes.
func TestWaitingClearsOnFirstModelOutput(t *testing.T) {
	type testCase struct {
		name  string
		tools []rig.Tool
		// streams builds the per-model-call streams from the two release gates:
		// firstRelease unblocks the silent in-flight request, afterRelease
		// unblocks the pause that keeps the run streaming after its first
		// output (or the next model call for a tool round).
		streams func(firstRelease, afterRelease <-chan struct{}) []rig.Stream
		// first runs while the run is still streaming, right after the first
		// output was applied.
		first func(t *testing.T, session contracts.ChatSession)
		// final runs on the settled session.
		final func(t *testing.T, session contracts.ChatSession)
	}
	cases := []testCase{
		{
			name: "text delta",
			streams: func(firstRelease, afterRelease <-chan struct{}) []rig.Stream {
				return []rig.Stream{&gatedStream{
					first: firstRelease, after: afterRelease,
					chunks: []rig.Chunk{{Delta: "hello"}, {FinishReason: "stop"}},
				}}
			},
			first: func(t *testing.T, session contracts.ChatSession) {
				if got := session.Messages[1].Text; got != "hello" {
					t.Fatalf("assistant text after the first delta = %q, want hello", got)
				}
			},
			final: func(t *testing.T, session contracts.ChatSession) {
				if last := session.Messages[1]; last.Streaming || last.Text != "hello" {
					t.Fatalf("final assistant = %+v, want a completed hello", last)
				}
			},
		},
		{
			name: "reasoning delta",
			streams: func(firstRelease, afterRelease <-chan struct{}) []rig.Stream {
				return []rig.Stream{&gatedStream{
					first: firstRelease, after: afterRelease,
					chunks: []rig.Chunk{{Reasoning: "thinking"}, {Delta: "answer"}, {FinishReason: "stop"}},
				}}
			},
			first: func(t *testing.T, session contracts.ChatSession) {
				if got := session.Messages[1].Reasoning; got != "thinking" {
					t.Fatalf("reasoning after the first delta = %q, want thinking", got)
				}
			},
			final: func(t *testing.T, session contracts.ChatSession) {
				last := session.Messages[1]
				if last.Streaming || last.Reasoning != "thinking" || last.Text != "answer" {
					t.Fatalf("final assistant = %+v, want reasoning then answer", last)
				}
			},
		},
		{
			name:  "tool call",
			tools: []rig.Tool{&echoTool{}},
			// Rig emits tool-call events only after the model round ends, so the
			// pause that keeps the run streaming after the first output is the
			// gated second model call (the tool round itself must run).
			streams: func(firstRelease, afterRelease <-chan struct{}) []rig.Stream {
				return []rig.Stream{
					&gatedStream{first: firstRelease, chunks: []rig.Chunk{
						{ToolCalls: []rig.ToolCallDelta{{Index: 0, ID: "call-1", Name: "echo", Arguments: `{"text":"hi"}`}}},
						{FinishReason: "tool_calls"},
					}},
					&gatedStream{first: afterRelease, chunks: []rig.Chunk{
						{Delta: "done"},
						{FinishReason: "stop"},
					}},
				}
			},
			first: func(t *testing.T, session contracts.ChatSession) {
				tools := session.Messages[1].Tools
				if len(tools) != 1 || tools[0].ID != "call-1" {
					t.Fatalf("tools after the first tool_call event = %+v, want call-1", tools)
				}
				if text := session.Messages[1].Text; text != "" {
					t.Fatalf("assistant text = %q before the final reply", text)
				}
			},
			final: func(t *testing.T, session contracts.ChatSession) {
				last := session.Messages[1]
				if last.Text != "done" || len(last.Tools) != 1 || last.Tools[0].Status != "ok" {
					t.Fatalf("final assistant = %+v, want the done answer with a finished tool", last)
				}
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			firstRelease := make(chan struct{})
			afterRelease := make(chan struct{})
			provider := &scriptedGateProvider{streams: test.streams(firstRelease, afterRelease)}
			svc, _ := newStateTestService(t, func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }, test.tools...)
			// Unblock the gated reads on cleanup too, so a failed assertion
			// cannot leave the run goroutine stuck in Recv.
			releaseFirst := sync.OnceFunc(func() { close(firstRelease) })
			releaseAfter := sync.OnceFunc(func() { close(afterRelease) })
			t.Cleanup(releaseFirst)
			t.Cleanup(releaseAfter)

			id, err := svc.NewSession("")
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			if err := svc.Send(id, "prompt"); err != nil {
				t.Fatalf("Send: %v", err)
			}
			// Deterministic: Send sets running+waiting under the lock before it
			// returns, and the stream is gated before any output.
			info := sessionInfoOf(t, svc, id)
			if !info.Running || !info.Waiting {
				t.Fatalf("after Send: Running=%v Waiting=%v, want both true while the model is silent", info.Running, info.Waiting)
			}

			releaseFirst()
			waitFor(t, "Waiting to clear after the first model output", func() bool {
				return !sessionInfoOf(t, svc, id).Waiting
			})
			streaming := mustSession(t, svc, id)
			if !streaming.Running {
				t.Fatalf("run finished although its stream tail is still gated: %+v", streaming)
			}
			test.first(t, streaming)
			if info := sessionInfoOf(t, svc, id); info.Waiting || !info.Running {
				t.Fatalf("streaming session: Running=%v Waiting=%v, want Running=true Waiting=false", info.Running, info.Waiting)
			}

			releaseAfter()
			session := waitIdle(t, svc, id)
			if info := sessionInfoOf(t, svc, id); info.Waiting || info.Running {
				t.Fatalf("finished session: Running=%v Waiting=%v, want both false", info.Running, info.Waiting)
			}
			if session.Messages[1].Error != "" {
				t.Fatalf("assistant error = %q, want none", session.Messages[1].Error)
			}
			test.final(t, session)
		})
	}
}

// TestBackgroundRunMarksUnreadUntilOpened covers the unread lifecycle: a run
// that finishes while another session is active is marked unread and persisted,
// opening it clears the flag and the row, and a new run (in the now-active
// session) stays clear.
func TestBackgroundRunMarksUnreadUntilOpened(t *testing.T) {
	svc, settings := newStateTestService(t, func(contracts.ChatConfig) (rig.Provider, error) {
		return &captureProvider{}, nil
	})
	background, err := svc.NewSession("background")
	if err != nil {
		t.Fatalf("NewSession(background): %v", err)
	}
	active, err := svc.NewSession("active")
	if err != nil {
		t.Fatalf("NewSession(active): %v", err)
	}
	if got := svc.ActiveSession(); got != active {
		t.Fatalf("active = %q, want %q", got, active)
	}
	key := contracts.SessionUnreadSettingKey(background)

	if err := svc.Send(background, "run in the background"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, "background run to finish unread", func() bool {
		info := sessionInfoOf(t, svc, background)
		return !info.Running && info.Unread
	})
	if info := sessionInfoOf(t, svc, active); info.Unread || info.Running {
		t.Fatalf("active session info = %+v, want neither running nor unread", info)
	}
	if got := settings.Get(key, "false"); got != "true" {
		t.Fatalf("persisted unread row = %q, want true", got)
	}

	// Opening the session consumes the completion and persists the clear.
	if err := svc.SetActiveSession(background); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	if info := sessionInfoOf(t, svc, background); info.Unread {
		t.Fatalf("opened session is still unread: %+v", info)
	}
	if got := settings.Get(key, "true"); got != "false" {
		t.Fatalf("unread row after opening = %q, want false", got)
	}

	// Starting the next run clears a stale row at run start, and finishing it
	// in the active session does not mark it again.
	if err := settings.Set(key, "true"); err != nil {
		t.Fatalf("Set unread row: %v", err)
	}
	if err := svc.Send(background, "run in the foreground"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := settings.Get(key, "true"); got != "false" {
		t.Fatalf("unread row right after Send = %q, want false (cleared at run start)", got)
	}
	waitIdle(t, svc, background)
	if info := sessionInfoOf(t, svc, background); info.Unread || info.Waiting || info.Running {
		t.Fatalf("active foreground run info = %+v, want idle and read", info)
	}
	if got := settings.Get(key, "true"); got != "false" {
		t.Fatalf("unread row after the active run = %q, want false", got)
	}
}

// TestBackgroundFailureAndCancelMarkUnread covers the non-success outcomes: a
// failed or cancelled background run is still a completion the user did not
// see, so it marks the session unread (and clears Waiting).
func TestBackgroundFailureAndCancelMarkUnread(t *testing.T) {
	t.Run("failed run", func(t *testing.T) {
		svc, settings := newStateTestService(t, func(contracts.ChatConfig) (rig.Provider, error) {
			return errorProvider{err: errors.New("provider down")}, nil
		})
		background, err := svc.NewSession("background")
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		if _, err := svc.NewSession("active"); err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		if err := svc.Send(background, "hi"); err != nil {
			t.Fatalf("Send: %v", err)
		}
		waitFor(t, "failed background run to finish unread", func() bool {
			info := sessionInfoOf(t, svc, background)
			return !info.Running && !info.Waiting && info.Unread
		})
		if got := settings.Get(contracts.SessionUnreadSettingKey(background), "false"); got != "true" {
			t.Fatalf("persisted unread row = %q, want true", got)
		}
	})

	t.Run("cancelled run", func(t *testing.T) {
		svc, settings := newStateTestService(t, func(contracts.ChatConfig) (rig.Provider, error) {
			return &stalledProvider{}, nil
		})
		background, err := svc.NewSession("background")
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		if _, err := svc.NewSession("active"); err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		if err := svc.Send(background, "hi"); err != nil {
			t.Fatalf("Send: %v", err)
		}
		// The stalled request has produced nothing: still waiting.
		waitFor(t, "run to wait for the model", func() bool {
			info := sessionInfoOf(t, svc, background)
			return info.Running && info.Waiting
		})
		if err := svc.Cancel(background); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		waitFor(t, "cancelled background run to finish unread", func() bool {
			info := sessionInfoOf(t, svc, background)
			return !info.Running && !info.Waiting && info.Unread
		})
		if got := settings.Get(contracts.SessionUnreadSettingKey(background), "false"); got != "true" {
			t.Fatalf("persisted unread row = %q, want true", got)
		}
	})
}

// TestSessionsUnreadReadsSettings proves the list flag comes from the settings
// row (so it survives a restart) instead of in-memory state.
func TestSessionsUnreadReadsSettings(t *testing.T) {
	settings := newFakeSettings(nil)
	svc := newTestService(t, Config{}, settings, nil)
	id, err := svc.NewSession("A")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if info := sessionInfoOf(t, svc, id); info.Unread || info.Waiting {
		t.Fatalf("new session info = %+v, want no run-state flags", info)
	}
	key := contracts.SessionUnreadSettingKey(id)
	if err := settings.Set(key, "true"); err != nil {
		t.Fatalf("Set unread row: %v", err)
	}
	if info := sessionInfoOf(t, svc, id); !info.Unread {
		t.Fatalf("session info = %+v, want Unread read from the settings row", info)
	}
	if err := settings.Set(key, "false"); err != nil {
		t.Fatalf("Set unread row: %v", err)
	}
	if info := sessionInfoOf(t, svc, id); info.Unread {
		t.Fatalf("session info = %+v, want Unread=false", info)
	}
}

// TestDeleteSessionClearsUnreadSetting covers the delete cleanup: SettingsService
// has no delete API, so the row is written to "false" like pin/archive do.
func TestDeleteSessionClearsUnreadSetting(t *testing.T) {
	settings := newFakeSettings(nil)
	svc := newTestService(t, Config{}, settings, nil)
	id, err := svc.NewSession("A")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	key := contracts.SessionUnreadSettingKey(id)
	if err := settings.Set(key, "true"); err != nil {
		t.Fatalf("Set unread row: %v", err)
	}
	if err := svc.DeleteSession(id); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if got := settings.Get(key, "true"); got != "false" {
		t.Fatalf("unread row after delete = %q, want false", got)
	}
}

// TestSessionsWithoutSettingsNeverUnread covers the no-settings service: the
// list flags stay false instead of panicking.
func TestSessionsWithoutSettingsNeverUnread(t *testing.T) {
	svc := newService(Config{}, nil, nil, nil, nil, nil)
	if _, err := svc.NewSession("A"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	infos := svc.Sessions()
	if len(infos) != 1 {
		t.Fatalf("Sessions = %+v, want one", infos)
	}
	if infos[0].Unread || infos[0].Waiting {
		t.Fatalf("session info = %+v, want false flags without a settings store", infos[0])
	}
}
