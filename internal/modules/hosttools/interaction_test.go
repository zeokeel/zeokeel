package hosttools

// The host-tools module must route ask_user through the desktop interaction
// bridge when the host provides one.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type askedQuestion struct {
	id      string
	text    string
	options []string
}

// pendingInteraction is a contracts.UserInteraction whose Ask parks until the
// test calls Answer/Cancel, like the real uiroot bridge.
type pendingInteraction struct {
	mu      sync.Mutex
	next    int
	pending map[string]chan string
	asked   chan askedQuestion
	askCall atomic.Int32
}

func newPendingInteraction() *pendingInteraction {
	return &pendingInteraction{
		pending: make(map[string]chan string),
		asked:   make(chan askedQuestion, 4),
	}
}

func (i *pendingInteraction) SetHandler(func(contracts.UserQuestion)) {}

func (i *pendingInteraction) Ask(ctx context.Context, text string, options []string) (string, error) {
	i.askCall.Add(1)
	i.mu.Lock()
	i.next++
	id := fmt.Sprintf("question-%d", i.next)
	answer := make(chan string, 1)
	i.pending[id] = answer
	i.mu.Unlock()
	i.asked <- askedQuestion{id: id, text: text, options: append([]string(nil), options...)}
	select {
	case value := <-answer:
		return value, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (i *pendingInteraction) Answer(id, answer string) {
	i.mu.Lock()
	pending := i.pending[id]
	delete(i.pending, id)
	i.mu.Unlock()
	if pending != nil {
		pending <- answer
	}
}

func (i *pendingInteraction) Cancel(id string) {
	// Cancelling is not needed by these tests; keep the no-op semantics.
}

// loadProductWithInteraction loads hosttools with a provided UserInteraction.
func loadProductWithInteraction(t *testing.T, interaction contracts.UserInteraction, interactive bool) *product {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	if _, err := root.Provide(contracts.KeyUserInteraction, interaction); err != nil {
		t.Fatalf("provide interaction: %v", err)
	}
	rigRegistry := rigtool.NewRegistry()
	registry := newKernelRegistry(t, rigRegistry, nil)
	loader, err := module.Load(root, registry, []module.Entry{
		{ID: "toolkit", Module: "test/fake-toolkit"},
		{ID: "hosttools", Module: ModuleID, Config: map[string]any{"interactive": interactive}},
	})
	if err != nil {
		t.Fatalf("load hosttools product: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	return &product{root: root, loader: loader, registry: rigRegistry}
}

// invokeAsync runs one tool call on a goroutine because ask_user blocks.
func invokeAsync(registry *rigtool.Registry, name, arguments string) <-chan toolOutcome {
	out := make(chan toolOutcome, 1)
	go func() {
		result, err := registry.Invoke(context.Background(), rig.ToolCall{ID: "call-1", Name: name, Arguments: arguments})
		out <- toolOutcome{result: result, err: err}
	}()
	return out
}

type toolOutcome struct {
	result rig.ToolResult
	err    error
}

func TestAskUserUsesProvidedInteractionBridge(t *testing.T) {
	interaction := newPendingInteraction()
	product := loadProductWithInteraction(t, interaction, true)
	out := invokeAsync(product.registry, ToolAskUser, `{"question":"Which database?","options":["sqlite","postgres"]}`)
	asked := <-interaction.asked
	if asked.text != "Which database?" || len(asked.options) != 2 || asked.options[1] != "postgres" {
		t.Fatalf("asked = %+v", asked)
	}
	// The tool is parked; resolve it through the interaction service.
	interaction.Answer(asked.id, "postgres")
	outcome := <-out
	if outcome.err != nil {
		t.Fatalf("ask_user: %v", outcome.err)
	}
	if outcome.result.IsError || outcome.result.Content != "user answered: postgres" {
		t.Fatalf("ask_user result = %+v", outcome.result)
	}
}

func TestAskUserInteractiveFalseSkipsBridge(t *testing.T) {
	interaction := newPendingInteraction()
	product := loadProductWithInteraction(t, interaction, false)
	result, err := product.call(t, ToolAskUser, `{"question":"Which database?"}`)
	if err != nil {
		t.Fatalf("ask_user: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "no interactive UI") {
		t.Fatalf("ask_user with interactive=false = %+v, want the no-interactive result", result)
	}
	if interaction.askCall.Load() != 0 {
		t.Fatalf("interaction bridge was called %d times with interactive=false", interaction.askCall.Load())
	}
}

func TestAskUserWithoutInteractionServiceKeepsMemoryBehavior(t *testing.T) {
	product := loadProduct(t, nil, true)
	result, err := product.call(t, ToolAskUser, `{"question":"Which database?"}`)
	if err != nil {
		t.Fatalf("ask_user: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "no interactive UI") {
		t.Fatalf("ask_user without bridge = %+v, want no-interactive", result)
	}
}
