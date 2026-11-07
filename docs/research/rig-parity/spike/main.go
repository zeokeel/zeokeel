// Command parity is a feasibility spike: can a fresh module that only requires
// github.com/zeokeel/zeokeel-rig@v0.12.1 assemble the agent loop + the full tool
// set (the part of old/ that Rig is meant to replace) and run it offline?
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/agent"
	"github.com/zeokeel/zeokeel-rig/gate"
	"github.com/zeokeel/zeokeel-rig/kit"
	"github.com/zeokeel/zeokeel-rig/sandbox"
	"github.com/zeokeel/zeokeel-rig/session"
	"github.com/zeokeel/zeokeel-rig/tools/full"
	"github.com/zeokeel/zeokeel-rig/tools/hosttools"
)

// fakeProvider streams one fixed assistant answer, so the spike runs offline.
type fakeProvider struct{ answer string }

func (p fakeProvider) Name() string { return "fake" }

func (p fakeProvider) Stream(ctx context.Context, req rig.Request) (rig.Stream, error) {
	answer := p.answer
	if answer == "" {
		answer = "pong"
	}
	return &fakeStream{chunks: []rig.Chunk{
		{Delta: answer},
		{FinishReason: "stop"},
	}}, nil
}

type fakeStream struct {
	chunks []rig.Chunk
	i      int
}

func (s *fakeStream) Recv() (rig.Chunk, error) {
	if s.i >= len(s.chunks) {
		return rig.Chunk{}, io.EOF
	}
	chunk := s.chunks[s.i]
	s.i++
	return chunk, nil
}

func main() {
	// 1) kit facade: one import, one constructor (no network until Run).
	quick, err := kit.New(kit.Config{Model: "fake-model", Root: "."})
	if err != nil {
		fail("kit.New", err)
	}
	_ = quick
	fmt.Println("1) kit.New: ok (default tools assembled, no API key needed)")

	// 2) explicit assembly, the "old runtime + tools" equivalent.
	policy := sandbox.New(
		sandbox.WithRoots("."),
		sandbox.WithCommandAllowlist("ls", "cat", "go", "git"),
	)
	host := hosttools.NewMemoryHost()
	tools := full.All(full.AllOptions{
		Policy: policy,
		Root:   ".",
		Host: hosttools.Options{
			Asker: host, Presenter: host, Plan: host, Goal: host,
			Interactive: false,
		},
	})
	fmt.Printf("2) full.All: %d static tools\n", len(tools))

	history := session.NewMemory()
	a, err := agent.New(rig.AgentSpec{
		Provider: fakeProvider{answer: "pong"},
		Model:    "fake-model",
		Tools:    tools,
		History:  history,
		Gate:     gate.AllowAll(),
	})
	if err != nil {
		fail("agent.New", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := a.Run(ctx, rig.RunRequest{
		SessionID: "parity-1",
		Input: []rig.Message{{
			Role:    rig.RoleUser,
			Content: []rig.ContentPart{rig.TextPart("ping")},
		}},
	})
	if err != nil {
		fail("agent.Run", err)
	}
	fmt.Printf("3) agent.Run: finish=%s iterations=%d messages=%d (tools available to the loop)\n",
		res.FinishReason, res.Iterations, len(res.Messages))

	// 3) kit agent with the fake provider actually running end to end.
	quick, err = kit.New(kit.Config{
		Model:    "fake-model",
		Provider: fakeProvider{answer: "hello from kit"},
		Approval: kit.ApprovalAuto,
		Root:     ".",
	})
	if err != nil {
		fail("kit.New(fake)", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	text, result, err := quick.RunText(ctx2, "parity-2", "ping", nil)
	if err != nil {
		fail("kit.RunText", err)
	}
	fmt.Printf("4) kit.RunText: %q finish=%s iterations=%d\n", text, result.FinishReason, result.Iterations)
}

func fail(step string, err error) {
	fmt.Fprintf(os.Stderr, "parity: %s: %v\n", step, err)
	os.Exit(1)
}
