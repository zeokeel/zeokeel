package uiroot

import "sync"

// navigator is the host-owned navigation state (contracts.Navigator). Screens
// are pushed by ID and validated against the registry; the host renders the
// current screen. Because screens come from modules, an unknown ID is an error
// (returned to the caller) rather than a silent fallback.
type navigator struct {
	root *Root
	rt   *runtime

	mu       sync.Mutex
	stack    []string
	settings bool
}

func newNavigator(root *Root) *navigator {
	return &navigator{root: root, rt: root.rt}
}

// Current returns the screen at the top of the stack, or "" when no screen has
// been chosen yet (the host then renders the first registered screen).
func (n *navigator) Current() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.stack) == 0 {
		return ""
	}
	return n.stack[len(n.stack)-1]
}

// Push switches to a registered screen.
func (n *navigator) Push(screenID string) error {
	if !n.rt.hasScreen(screenID) {
		return &UnknownScreenError{ID: screenID}
	}
	n.mu.Lock()
	if len(n.stack) == 0 || n.stack[len(n.stack)-1] != screenID {
		n.stack = append(n.stack, screenID)
	}
	n.mu.Unlock()
	n.root.requestFrame()
	return nil
}

// Back pops the current screen; it reports whether the stack changed.
func (n *navigator) Back() bool {
	n.mu.Lock()
	if len(n.stack) < 2 {
		n.mu.Unlock()
		return false
	}
	n.stack = n.stack[:len(n.stack)-1]
	n.mu.Unlock()
	n.root.requestFrame()
	return true
}

// SettingsOpen reports whether the settings surface is shown.
func (n *navigator) SettingsOpen() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.settings
}

// ToggleSettings flips the settings surface. Opening it scrolls the remembered
// page into view in the grouped navigation, so the current page is never left
// off-screen (the closing/reopening case the reviewer found).
func (n *navigator) ToggleSettings() {
	n.mu.Lock()
	n.settings = !n.settings
	opened := n.settings
	n.mu.Unlock()
	if opened {
		n.root.settingsScrollToSelected = true
	}
	n.root.requestFrame()
}

// OpenSettings shows the settings surface, keeping the page it last showed.
func (n *navigator) OpenSettings() {
	n.mu.Lock()
	changed := !n.settings
	n.settings = true
	n.mu.Unlock()
	if changed {
		n.root.requestFrame()
	}
}

// OpenSettingsPage shows the settings surface on one contributed page, for
// commands and deep links ("open the model settings"). The pages are addressed
// in the same grouped order the settings navigation renders, so the index is
// stable across frames.
func (n *navigator) OpenSettingsPage(pageID string) error {
	ordered := flattenSettingsGroups(groupSettingsPages(visiblePages(n.rt.settingsPages())))
	for index, page := range ordered {
		if page.ID != pageID {
			continue
		}
		n.mu.Lock()
		n.settings = true
		n.root.settingsTab = index
		if page.Section == "高级" {
			n.root.settingsAdvancedOpen = true
		}
		n.root.settingsScrollToSelected = true
		n.mu.Unlock()
		n.root.requestFrame()
		return nil
	}
	return &UnknownSettingsPageError{ID: pageID}
}

// CloseSettings hides the settings surface.
func (n *navigator) CloseSettings() {
	n.mu.Lock()
	changed := n.settings
	n.settings = false
	n.mu.Unlock()
	if changed {
		n.root.requestFrame()
	}
}

// UnknownSettingsPageError reports a deep link to a settings page no module
// registered (or one that is currently hidden).
type UnknownSettingsPageError struct{ ID string }

func (e *UnknownSettingsPageError) Error() string {
	return "uiroot: unknown settings page " + e.ID
}

// UnknownScreenError reports a navigation to a screen ID no module registered.
type UnknownScreenError struct{ ID string }

func (e *UnknownScreenError) Error() string {
	return "uiroot: unknown screen " + e.ID
}
