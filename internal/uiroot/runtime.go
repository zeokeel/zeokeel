package uiroot

import (
	"fmt"
	"sort"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// runtime implements contracts.UIRuntime and contracts.ScreenLister: the five
// contribution registries plus the frame-start Post queue.
//
// Concurrency: Register* is meant for assembly/startup on the UI thread, but the
// mutex keeps a rogue caller from corrupting the maps; command runs and Posts
// may come from any goroutine (executed on the UI thread at frame start).
type runtime struct {
	root *Root

	mu        sync.Mutex
	screens   map[string]contracts.ScreenSpec
	slots     map[string]map[string]contracts.SlotItem
	pages     map[string]contracts.SettingsPageSpec
	renderers map[string]map[string]contracts.ToolRenderer
	commands  map[string]contracts.CommandSpec
	posts     []func()
}

func newRuntime(root *Root) *runtime {
	return &runtime{
		root:      root,
		screens:   make(map[string]contracts.ScreenSpec),
		slots:     make(map[string]map[string]contracts.SlotItem),
		pages:     make(map[string]contracts.SettingsPageSpec),
		renderers: make(map[string]map[string]contracts.ToolRenderer),
		commands:  make(map[string]contracts.CommandSpec),
	}
}

// --- contracts.UIRuntime ---

func (r *runtime) Post(fn func()) {
	if fn == nil {
		return
	}
	r.mu.Lock()
	r.posts = append(r.posts, fn)
	r.mu.Unlock()
	r.root.requestFrame()
}

func (r *runtime) Invalidate() { r.root.requestFrame() }

func (r *runtime) Frame() uint64 { return r.root.Frames() }

func (r *runtime) RegisterScreen(spec contracts.ScreenSpec) (func(), error) {
	if spec.ID == "" {
		return nil, fmt.Errorf("uiroot: screen with empty ID")
	}
	if spec.Build == nil {
		return nil, fmt.Errorf("uiroot: screen %q has nil Build", spec.ID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.screens[spec.ID]; ok {
		return nil, fmt.Errorf("uiroot: duplicate screen ID %q", spec.ID)
	}
	r.screens[spec.ID] = spec
	r.root.requestFrame()
	return onceUnregister(func() {
		r.mu.Lock()
		delete(r.screens, spec.ID)
		r.mu.Unlock()
		r.root.requestFrame()
	}), nil
}

func (r *runtime) RegisterSlotItem(item contracts.SlotItem) (func(), error) {
	if item.Slot == "" || item.ID == "" {
		return nil, fmt.Errorf("uiroot: slot item needs Slot and ID")
	}
	if item.Build == nil {
		return nil, fmt.Errorf("uiroot: slot %q item %q has nil Build", item.Slot, item.ID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	items, ok := r.slots[item.Slot]
	if !ok {
		items = make(map[string]contracts.SlotItem)
		r.slots[item.Slot] = items
	}
	if _, exists := items[item.ID]; exists {
		return nil, fmt.Errorf("uiroot: duplicate slot item %q in slot %q", item.ID, item.Slot)
	}
	items[item.ID] = item
	r.root.requestFrame()
	return onceUnregister(func() {
		r.mu.Lock()
		if items, ok := r.slots[item.Slot]; ok {
			delete(items, item.ID)
			if len(items) == 0 {
				delete(r.slots, item.Slot)
			}
		}
		r.mu.Unlock()
		r.root.requestFrame()
	}), nil
}

func (r *runtime) RegisterSettingsPage(page contracts.SettingsPageSpec) (func(), error) {
	if page.ID == "" {
		return nil, fmt.Errorf("uiroot: settings page with empty ID")
	}
	if page.Build == nil {
		return nil, fmt.Errorf("uiroot: settings page %q has nil Build", page.ID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.pages[page.ID]; ok {
		return nil, fmt.Errorf("uiroot: duplicate settings page ID %q", page.ID)
	}
	r.pages[page.ID] = page
	r.root.requestFrame()
	return onceUnregister(func() {
		r.mu.Lock()
		delete(r.pages, page.ID)
		r.mu.Unlock()
		r.root.requestFrame()
	}), nil
}

func (r *runtime) RegisterToolRenderer(renderer contracts.ToolRenderer) (func(), error) {
	if renderer.Tool == "" || renderer.ID == "" {
		return nil, fmt.Errorf("uiroot: tool renderer needs Tool and ID")
	}
	if renderer.Build == nil {
		return nil, fmt.Errorf("uiroot: tool renderer %q/%q has nil Build", renderer.Tool, renderer.ID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	items, ok := r.renderers[renderer.Tool]
	if !ok {
		items = make(map[string]contracts.ToolRenderer)
		r.renderers[renderer.Tool] = items
	}
	if _, exists := items[renderer.ID]; exists {
		return nil, fmt.Errorf("uiroot: duplicate tool renderer %q for %q", renderer.ID, renderer.Tool)
	}
	items[renderer.ID] = renderer
	r.root.requestFrame()
	return onceUnregister(func() {
		r.mu.Lock()
		if items, ok := r.renderers[renderer.Tool]; ok {
			delete(items, renderer.ID)
			if len(items) == 0 {
				delete(r.renderers, renderer.Tool)
			}
		}
		r.mu.Unlock()
		r.root.requestFrame()
	}), nil
}

func (r *runtime) RegisterCommand(cmd contracts.CommandSpec) (func(), error) {
	if cmd.ID == "" {
		return nil, fmt.Errorf("uiroot: command with empty ID")
	}
	if cmd.Run == nil {
		return nil, fmt.Errorf("uiroot: command %q has nil Run", cmd.ID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.commands[cmd.ID]; ok {
		return nil, fmt.Errorf("uiroot: duplicate command ID %q", cmd.ID)
	}
	r.commands[cmd.ID] = cmd
	r.root.requestFrame()
	return onceUnregister(func() {
		r.mu.Lock()
		delete(r.commands, cmd.ID)
		r.mu.Unlock()
		r.root.requestFrame()
	}), nil
}

// --- frame-time rendering helpers (UI thread, frame scope) ---

// RenderSlot builds every contribution of one slot in deterministic order. It
// is the child-slot mechanism: the host uses it for reserved slots, modules use
// it for extension points they expose to other modules.
func (r *runtime) RenderSlot(c *ui.Context, slot string) {
	if c == nil {
		return
	}
	for _, item := range r.slotItems(slot) {
		r.root.safeBuild(c, item.Build)
	}
}

// RenderTool builds the registered tool-card renderers for one call. It
// reports whether any renderer matched (the caller then skips its fallback).
func (r *runtime) RenderTool(c *ui.Context, call contracts.ToolCall) bool {
	if c == nil {
		return false
	}
	renderers := r.toolRenderers(call.Name)
	for _, renderer := range renderers {
		r.root.safeToolBuild(c, renderer, call)
	}
	return len(renderers) > 0
}

// Slots reports the slot names with contributions, sorted.
func (r *runtime) Slots() []string {
	r.mu.Lock()
	names := make([]string, 0, len(r.slots))
	for name, items := range r.slots {
		if len(items) > 0 {
			names = append(names, name)
		}
	}
	r.mu.Unlock()
	sort.Strings(names)
	return names
}

// --- contracts.ScreenLister ---

func (r *runtime) Screens() []string {
	specs := r.screenSpecs()
	ids := make([]string, len(specs))
	for i, spec := range specs {
		ids[i] = spec.ID
	}
	return ids
}

// --- frame helpers (UI thread) ---

// drain runs the queued framework work at frame start, on the UI thread.
func (r *runtime) drain() {
	r.mu.Lock()
	posts := r.posts
	r.posts = nil
	r.mu.Unlock()
	for _, fn := range posts {
		runSafely(fn)
	}
}

func (r *runtime) screenSpecs() []contracts.ScreenSpec {
	r.mu.Lock()
	specs := make([]contracts.ScreenSpec, 0, len(r.screens))
	for _, spec := range r.screens {
		specs = append(specs, spec)
	}
	r.mu.Unlock()
	sort.Slice(specs, func(i, j int) bool {
		if specs[i].Order != specs[j].Order {
			return specs[i].Order < specs[j].Order
		}
		return specs[i].ID < specs[j].ID
	})
	return specs
}

func (r *runtime) screen(id string) (contracts.ScreenSpec, bool) {
	r.mu.Lock()
	spec, ok := r.screens[id]
	r.mu.Unlock()
	return spec, ok
}

func (r *runtime) hasScreen(id string) bool {
	_, ok := r.screen(id)
	return ok
}

func (r *runtime) slotItems(slot string) []contracts.SlotItem {
	r.mu.Lock()
	items := make([]contracts.SlotItem, 0, len(r.slots[slot]))
	for _, item := range r.slots[slot] {
		items = append(items, item)
	}
	r.mu.Unlock()
	sort.Slice(items, func(i, j int) bool {
		if items[i].Order != items[j].Order {
			return items[i].Order < items[j].Order
		}
		return items[i].ID < items[j].ID
	})
	return items
}

// slotNames returns slot names with the given prefix in ascending order.
func (r *runtime) slotNames(prefix string) []string {
	r.mu.Lock()
	names := make([]string, 0, len(r.slots))
	for name := range r.slots {
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			names = append(names, name)
		}
	}
	r.mu.Unlock()
	sort.Strings(names)
	return names
}

func (r *runtime) settingsPages() []contracts.SettingsPageSpec {
	r.mu.Lock()
	pages := make([]contracts.SettingsPageSpec, 0, len(r.pages))
	for _, page := range r.pages {
		pages = append(pages, page)
	}
	r.mu.Unlock()
	sort.Slice(pages, func(i, j int) bool {
		if pages[i].Section != pages[j].Section {
			return pages[i].Section < pages[j].Section
		}
		if pages[i].Order != pages[j].Order {
			return pages[i].Order < pages[j].Order
		}
		return pages[i].ID < pages[j].ID
	})
	return pages
}

func (r *runtime) commandSpecs() []contracts.CommandSpec {
	r.mu.Lock()
	cmds := make([]contracts.CommandSpec, 0, len(r.commands))
	for _, cmd := range r.commands {
		cmds = append(cmds, cmd)
	}
	r.mu.Unlock()
	sort.Slice(cmds, func(i, j int) bool {
		if cmds[i].Order != cmds[j].Order {
			return cmds[i].Order < cmds[j].Order
		}
		return cmds[i].ID < cmds[j].ID
	})
	return cmds
}

func (r *runtime) toolRenderers(tool string) []contracts.ToolRenderer {
	r.mu.Lock()
	items := make([]contracts.ToolRenderer, 0, len(r.renderers[tool]))
	for _, item := range r.renderers[tool] {
		items = append(items, item)
	}
	r.mu.Unlock()
	sort.Slice(items, func(i, j int) bool {
		if items[i].Order != items[j].Order {
			return items[i].Order < items[j].Order
		}
		return items[i].ID < items[j].ID
	})
	return items
}

func (r *runtime) validate() error {
	r.mu.Lock()
	n := len(r.screens)
	r.mu.Unlock()
	if n == 0 {
		return fmt.Errorf("uiroot: no screen registered (UI assembly is empty)")
	}
	return nil
}

// onceUnregister makes a registration cleanup one-shot. The registration
// functions hand their unregister function to ctx.Effect, which runs it during
// kernel disposal; if a module cleans up twice (or a test does) the second call
// is a no-op, and a stale closure can never delete a newer registration that
// reused the same identity after this one was removed.
func onceUnregister(remove func()) func() {
	var once sync.Once
	return func() { once.Do(remove) }
}

func runSafely(fn func()) {
	defer func() {
		// A lost Post must not take down the UI thread. Module panics inside
		// builders are contained by the caller's own recover.
		_ = recover()
	}()
	fn()
}
