// Command check verifies that a clean module can use every v0.13.0 SDK gap
// feature from the published version: compaction, spill, checkpoints,
// ask/review gates, goal driver, presets, reasoning effort, owner scopes,
// LSP references/implementation and MCP notifications.
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
	"github.com/zeokeel/zeokeel-rig/provider/openai"
	"github.com/zeokeel/zeokeel-rig/sandbox"
	"github.com/zeokeel/zeokeel-rig/session"
	"github.com/zeokeel/zeokeel-rig/session/compact"
	"github.com/zeokeel/zeokeel-rig/tools/full"
	"github.com/zeokeel/zeokeel-rig/tools/hosttools"
	"github.com/zeokeel/zeokeel-rig/tools/jobs"
	"github.com/zeokeel/zeokeel-rig/tools/lsp"
	"github.com/zeokeel/zeokeel-rig/tools/mcp"
	"github.com/zeokeel/zeokeel-rig/tools/presets"
	"github.com/zeokeel/zeokeel-rig/tools/terminal"
)

// ---- fake provider ---------------------------------------------------------

type fakeProvider struct{ answer string }

func (p fakeProvider) Name() string { return "fake" }
func (p fakeProvider) Stream(ctx context.Context, req rig.Request) (rig.Stream, error) {
	return &fakeStream{chunks: []rig.Chunk{{Delta: p.answer}, {FinishReason: "stop"}}}, nil
}

type fakeStream struct {
	chunks []rig.Chunk
	i      int
}

func (s *fakeStream) Recv() (rig.Chunk, error) {
	if s.i >= len(s.chunks) {
		return rig.Chunk{}, io.EOF
	}
	c := s.chunks[s.i]
	s.i++
	return c, nil
}

// ---- fake checkpoint store -------------------------------------------------

type memCheckpoints struct{ saved []*rig.Checkpoint }

func (m *memCheckpoints) Load(ctx context.Context, sessionID string) (*rig.Checkpoint, error) {
	return nil, nil
}
func (m *memCheckpoints) Save(ctx context.Context, sessionID string, cp *rig.Checkpoint) error {
	m.saved = append(m.saved, cp)
	return nil
}
func (m *memCheckpoints) Clear(ctx context.Context, sessionID string) error { return nil }

// ---- fake asker / reviewer -------------------------------------------------

type fakeAsker struct{}

func (fakeAsker) Ask(ctx context.Context, question string, options []string) (string, error) {
	return "allow", nil
}

type fakeReviewer struct{}

func (fakeReviewer) ReviewTool(ctx context.Context, req gate.ToolReviewRequest) (gate.ToolReviewDecision, error) {
	return gate.ToolReviewDecision{Allow: true, Reason: "read-only"}, nil
}

func main() {
	root, _ := os.MkdirTemp("", "rig-v0130-check")
	defer os.RemoveAll(root)

	// 1) Compaction: window + LLM of the new session/compact package.
	window := compact.WindowCompactor{KeepLast: 2}
	llm := compact.LLMCompactor{Provider: fakeProvider{answer: "summary"}, Model: "fake", Fallback: window}
	tokens := rig.EstimateTokens([]rig.Message{{Role: rig.RoleUser, Content: []rig.ContentPart{rig.TextPart("你好 hello")}}})
	fmt.Printf("1) compaction: window+llm+tokens=%d\n", tokens)

	// 2) Spill policy + checkpoint store.
	spill := &rig.SpillPolicy{Dir: root + "/spill", ThresholdBytes: 16, MaxFiles: 10}
	checkpoints := &memCheckpoints{}

	// 3) Gates: ask + review (fallback to ask).
	askGate := gate.Ask(fakeAsker{}, gate.WithAskTimeout(time.Second))
	reviewGate := gate.Review(fakeReviewer{},
		gate.WithReviewAllowlist("read_file"),
		gate.WithReviewTimeout(2*time.Second),
		gate.WithReviewFallback(askGate),
	)
	decision, err := reviewGate.Approve(context.Background(), nil, rig.ToolCall{Name: "bash", Arguments: "{}"})
	fmt.Printf("4) gates: review decision=%v err=%v\n", decision, err)

	// 4) Goal round driver + tool presets.
	driver := agent.NewGoalRoundDriver(agent.GoalRoundDriverConfig{MaxRounds: 3})
	fmt.Printf("5) goal driver: rounds=%d\n", driver.Rounds("s1"))

	policy := sandbox.New(sandbox.WithRoots(root))
	all := full.All(full.AllOptions{Policy: policy, Root: root, Host: hosttools.Options{Interactive: false}})
	minimal, err := presets.Filter(presets.PresetMinimal, all)
	fmt.Printf("6) presets: all=%d minimal=%d err=%v (presets=%v)\n", len(all), len(minimal), err, presets.Available())

	// 5) Reasoning effort + owner scopes + LSP/MCP new APIs (compile-level).
	openaiConfig := openai.Config{DefaultModel: "fake", DefaultReasoningEffort: "low"}
	_ = openaiConfig
	_ = terminal.WithOwner("host")
	_ = jobs.WithOwner("host")
	_ = lsp.NewReferencesTool
	_ = lsp.NewImplementationTool
	if pos, err := lsp.ValidatePosition("hello\nworld", 2, 3); err == nil {
		fmt.Printf("7) lsp: utf16 pos=%d:%d\n", pos.Line, pos.Character)
	}
	_ = (*mcp.Client).OnNotification
	_ = (*mcp.Client).OnNotificationError

	// 6) Assemble an agent with compaction + spill + checkpoints and run it.
	history := session.NewMemory()
	a, err := agent.New(rig.AgentSpec{
		Provider:        fakeProvider{answer: "pong"},
		Model:           "fake",
		Tools:           all,
		History:         history,
		Gate:            reviewGate,
		ContextBudget:   4096,
		Compactor:       llm,
		Spill:           spill,
		CheckpointStore: checkpoints,
	})
	if err != nil {
		fail("agent.New", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := a.Run(ctx, rig.RunRequest{
		SessionID: "s1",
		Input: []rig.Message{{Role: rig.RoleUser, Content: []rig.ContentPart{rig.TextPart("ping")}}},
	})
	if err != nil {
		fail("agent.Run", err)
	}
	fmt.Printf("8) agent.Run: finish=%s iterations=%d checkpoints=%d\n", res.FinishReason, res.Iterations, len(checkpoints.saved))

	// 7) kit facade with the new options.
	quick, err := kit.New(kit.Config{
		Model:           "fake",
		Provider:        fakeProvider{answer: "hello from kit"},
		Root:            root,
		ContextBudget:   1024,
		Compactor:       window,
		Spill:           spill,
		ReasoningEffort: "high",
		ToolPreset:      "minimal",
	})
	if err != nil {
		fail("kit.New", err)
	}
	text, result, err := quick.RunText(ctx, "s2", "ping", nil)
	if err != nil {
		fail("kit.RunText", err)
	}
	fmt.Printf("9) kit.RunText: %q finish=%s iterations=%d\n", text, result.FinishReason, result.Iterations)
}

func fail(step string, err error) {
	fmt.Fprintf(os.Stderr, "check: %s: %v\n", step, err)
	os.Exit(1)
}
