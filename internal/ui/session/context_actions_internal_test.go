package session

// Unit tests for the Context page's settings deep link (openSettingsPageAt): the
// anchored host capability is used when present, and a host without it falls
// back to the page-level link (or the plain surface toggle) instead of doing
// nothing.

import "testing"

// recordingNavigator is a contracts.Navigator that records the deep-link methods
// it is asked for; its optional interfaces model the host capabilities.
type recordingNavigator struct {
	toggled   int
	opened    []string
	openedAt  [][2]string
	openAtErr error
}

func (n *recordingNavigator) Current() string    { return "" }
func (n *recordingNavigator) Push(string) error  { return nil }
func (n *recordingNavigator) Back() bool         { return false }
func (n *recordingNavigator) SettingsOpen() bool { return false }
func (n *recordingNavigator) ToggleSettings()    { n.toggled++ }
func (n *recordingNavigator) CloseSettings()     {}
func (n *recordingNavigator) OpenSettingsPage(page string) error {
	n.opened = append(n.opened, page)
	return nil
}

// anchoredNavigator adds the optional anchored deep-link capability.
type anchoredNavigator struct{ recordingNavigator }

func (n *anchoredNavigator) OpenSettingsPageAt(pageID, anchorKey string) error {
	if n.openAtErr != nil {
		return n.openAtErr
	}
	n.openedAt = append(n.openedAt, [2]string{pageID, anchorKey})
	return nil
}

// TestOpenSettingsPageAtUsesAnchoredCapability covers the host that supports the
// anchor: the page and the anchor key are handed over in one call.
func TestOpenSettingsPageAtUsesAnchoredCapability(t *testing.T) {
	nav := &anchoredNavigator{}
	openSettingsPageAt(nav, "model", "model.context-budget")
	if len(nav.openedAt) != 1 || nav.openedAt[0] != [2]string{"model", "model.context-budget"} {
		t.Fatalf("anchored deep link calls = %v, want model/model.context-budget", nav.openedAt)
	}
	if len(nav.opened) != 0 {
		t.Fatalf("the fallback page link also ran: %v", nav.opened)
	}
}

// TestOpenSettingsPageAtFallsBackToPageLink covers a host with the page deep link
// but without the anchored one: the page still opens.
func TestOpenSettingsPageAtFallsBackToPageLink(t *testing.T) {
	nav := &recordingNavigator{}
	openSettingsPageAt(nav, "model", "model.context-budget")
	if len(nav.opened) != 1 || nav.opened[0] != "model" {
		t.Fatalf("fallback page link calls = %v, want model", nav.opened)
	}
	if nav.toggled != 0 {
		t.Fatalf("the plain surface toggle ran before the page link: %d", nav.toggled)
	}
}

// TestOpenSettingsPageAtFallsBackToToggle covers a stripped host with neither
// deep link: the settings surface still opens.
func TestOpenSettingsPageAtFallsBackToToggle(t *testing.T) {
	nav := &plainNavigator{}
	openSettingsPageAt(nav, "model", "model.context-budget")
	if nav.toggled != 1 {
		t.Fatalf("plain surface toggles = %d, want 1", nav.toggled)
	}
}

// TestOpenSettingsPageAtNilNavigator covers a stripped host without a navigator:
// the action is a quiet no-op instead of a panic.
func TestOpenSettingsPageAtNilNavigator(t *testing.T) {
	openSettingsPageAt(nil, "model", "model.context-budget")
}

// plainNavigator implements only the minimal contracts.Navigator surface.
type plainNavigator struct {
	toggled int
}

func (n *plainNavigator) Current() string    { return "" }
func (n *plainNavigator) Push(string) error  { return nil }
func (n *plainNavigator) Back() bool         { return false }
func (n *plainNavigator) SettingsOpen() bool { return false }
func (n *plainNavigator) ToggleSettings()    { n.toggled++ }
func (n *plainNavigator) CloseSettings()     {}
