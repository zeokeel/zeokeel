package icons

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// allIcons lists every exported getter once, so the tests below iterate the
// whole library instead of naming a subset.
func allIcons() map[string]*ui.SVG {
	return map[string]*ui.SVG{
		"Folder":        Folder(),
		"FolderOpen":    FolderOpen(),
		"FolderPlus":    FolderPlus(),
		"Plugin":        Plugin(),
		"Plug":          Plug(),
		"Clock":         Clock(),
		"Search":        Search(),
		"Plus":          Plus(),
		"Sliders":       Sliders(),
		"PanelLeft":     PanelLeft(),
		"More":          More(),
		"ChevronRight":  ChevronRight(),
		"ChevronDown":   ChevronDown(),
		"Message":       Message(),
		"Sparkles":      Sparkles(),
		"Terminal":      Terminal(),
		"NewChat":       NewChat(),
		"Data":          Data(),
		"Reload":        Reload(),
		"AlertTriangle": AlertTriangle(),
	}
}

// TestIconGettersNonNil checks every exported getter returns parsed artwork.
// A missing SVG file panics inside get on first use, so this also guards the
// embed list against a renamed or deleted file.
func TestIconGettersNonNil(t *testing.T) {
	for name, icon := range allIcons() {
		if icon == nil {
			t.Errorf("%s() returned nil", name)
			continue
		}
		w, h := icon.Size()
		if w <= 0 || h <= 0 {
			t.Errorf("%s size = %vx%v, want positive", name, w, h)
		}
	}
}

// TestSidebarIconsAre16Grid pins the DSH geometry contract: the sidebar and
// composer glyphs are 16x16 artwork so a 16px icon is drawn at 1:1 with a
// 1.2 stroke instead of a downscaled 24px Lucide glyph.
func TestSidebarIconsAre16Grid(t *testing.T) {
	for name, icon := range map[string]*ui.SVG{
		"folder":        Folder(),
		"folder-open":   FolderOpen(),
		"folder-plus":   FolderPlus(),
		"plugin":        Plugin(),
		"chevron-right": ChevronRight(),
		"chevron-down":  ChevronDown(),
		"sliders":       Sliders(),
		"plus":          Plus(),
		"search":        Search(),
		"more":          More(),
		"panel-left":    PanelLeft(),
		"new-chat":      NewChat(),
		"terminal":      Terminal(),
		"data":          Data(),
		"reload":        Reload(),
	} {
		w, h := icon.Size()
		if w != 16 || h != 16 {
			t.Errorf("%s = %vx%v, want 16x16", name, w, h)
		}
	}
}

// TestSemanticAliases checks the aliases point at the same artwork, which is
// what lets a call site pick the readable name without a second file.
func TestSemanticAliases(t *testing.T) {
	if FolderClosed() != Folder() {
		t.Error("FolderClosed is not the closed folder artwork")
	}
	if Ellipsis() != More() {
		t.Error("Ellipsis is not the more-ellipsis artwork")
	}
	if SessionLog() != Data() {
		t.Error("SessionLog is not the data glyph")
	}
}

// TestUnknownIconPanics checks the loader fails loudly rather than drawing
// nothing for a typo.
func TestUnknownIconPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("get(unknown) did not panic")
		}
	}()
	_ = get("does-not-exist")
}
