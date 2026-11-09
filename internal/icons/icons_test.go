package icons

import (
	"encoding/xml"
	"strings"
	"testing"
)

// svgRoot is the only part of an icon file this test reads.
type svgRoot struct {
	XMLName     xml.Name `xml:"svg"`
	Width       string   `xml:"width,attr"`
	Height      string   `xml:"height,attr"`
	ViewBox     string   `xml:"viewBox,attr"`
	StrokeWidth string   `xml:"stroke-width,attr"`
	Stroke      string   `xml:"stroke,attr"`
	Fill        string   `xml:"fill,attr"`
}

// gridWidth returns the square side of a "min-x min-y width height" viewBox.
func gridWidth(viewBox string) string {
	fields := strings.Fields(viewBox)
	if len(fields) != 4 {
		return ""
	}
	return fields[2]
}

// TestIconsParseAndKeepTheirGrid locks the icon-set invariants the product
// depends on: every embedded SVG is well-formed, its viewBox is one of the two
// grids the set documents (16x16 DSH line art and 24x24 Lucide art), and the
// stroke width is the one that grid renders at an equal optical weight (1.2 for
// 16, 1.8 for 24). Adding an icon with another grid or stroke fails here instead
// of showing up as a visually mismatched glyph later (mygo icon.md: one icon
// family in one style).
func TestIconsParseAndKeepTheirGrid(t *testing.T) {
	entries, err := files.ReadDir("svg")
	if err != nil {
		t.Fatalf("read embedded svg dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no embedded icons")
	}
	wantStroke := map[string]string{"16": "1.2", "24": "1.8"}
	counts := map[string]int{}
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".svg")
		if !strings.HasSuffix(entry.Name(), ".svg") {
			t.Fatalf("%s is not an .svg file", entry.Name())
		}
		data, err := files.ReadFile("svg/" + entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		var root svgRoot
		if err := xml.Unmarshal(data, &root); err != nil {
			t.Fatalf("%s is not well-formed SVG: %v", entry.Name(), err)
		}
		grid := gridWidth(root.ViewBox)
		if grid != "16" && grid != "24" {
			t.Fatalf("%s viewBox = %q, want a 16x16 or 24x24 grid", entry.Name(), root.ViewBox)
		}
		counts[grid]++
		// The set is otherwise line art; a filled glyph (the ⋯ dots) is the one
		// documented exception and carries no stroke width.
		filled := root.Stroke == "none" && root.Fill == "currentColor"
		if !filled && root.StrokeWidth != wantStroke[grid] {
			t.Fatalf("%s is a %s grid but stroke-width = %q, want %q", entry.Name(), grid, root.StrokeWidth, wantStroke[grid])
		}
		if root.Width != "" && root.Width != grid {
			t.Fatalf("%s width = %q, want %q for its grid", entry.Name(), root.Width, grid)
		}
		if root.Height != "" && root.Height != grid {
			t.Fatalf("%s height = %q, want %q for its grid", entry.Name(), root.Height, grid)
		}
		// Every semantic accessor must resolve: an embedded file with no
		// accessor is dead weight, and an accessor with no file panics at use.
		if svg := get(name); svg == nil {
			t.Fatalf("icon %q resolved to nil", name)
		}
	}
	if counts["16"] == 0 || counts["24"] == 0 {
		t.Fatalf("grid counts = %v, want icons on both documented grids", counts)
	}
}
