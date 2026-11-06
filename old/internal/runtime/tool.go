package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// Tool is a capability the agent can invoke. Implementations must be safe for
// concurrent use: the agent may invoke different tools, or the same tool in
// different sessions, concurrently.
type Tool interface {
	Name() string
	Description() string
	// Invoke runs the tool with JSON-encoded arguments and returns its textual
	// result. A non-nil error is surfaced to the model as a tool failure.
	Invoke(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry is the immutable-after-seal set of tools available to an Agent.
// Register before New; the registry may be shared by multiple agents.
type Registry struct {
	mu     sync.RWMutex
	tools  map[string]Tool
	sealed bool
}

// NewRegistry returns an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds t. It fails on duplicate names, nil tools, or empty names, and
// after the registry has been sealed by New.
func (r *Registry) Register(t Tool) error {
	if t == nil {
		return fmt.Errorf("runtime: cannot register nil tool")
	}
	name := t.Name()
	if name == "" {
		return fmt.Errorf("runtime: tool has empty name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return fmt.Errorf("runtime: registry is sealed, cannot register %q", name)
	}
	if _, ok := r.tools[name]; ok {
		return fmt.Errorf("runtime: duplicate tool %q", name)
	}
	r.tools[name] = t
	return nil
}

// Get returns the tool named name.
func (r *Registry) Get(name string) (Tool, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns the registered tools sorted by name.
func (r *Registry) List() []Tool {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// seal makes the registry read-only for the lifetime of an Agent.
func (r *Registry) seal() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sealed = true
}
