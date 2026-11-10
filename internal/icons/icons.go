// Package icons holds the app's icon library: DSH-style 16x16 line SVGs
// (leading 16px, stroke 1.2) and the older 24x24 Lucide artwork that has no
// DSH counterpart yet. Icons are semantic, not pixel copies of DSH's
// artwork; where DSH's geometry is available it is used so the sidebar and
// composer line up with the baseline.
package icons

import (
	"embed"
	"sync"

	"github.com/egoist/mygo/ui"
)

//go:embed svg/*.svg
var files embed.FS

var (
	once sync.Once
	m    map[string]*ui.SVG
)

func get(name string) *ui.SVG {
	once.Do(func() {
		m = make(map[string]*ui.SVG)
		entries, err := files.ReadDir("svg")
		if err != nil {
			panic("icons: " + err.Error())
		}
		for _, e := range entries {
			data, err := files.ReadFile("svg/" + e.Name())
			if err != nil {
				panic("icons: " + err.Error())
			}
			if len(e.Name()) < 5 {
				continue
			}
			m[e.Name()[:len(e.Name())-4]] = ui.MustParseSVG(data)
		}
	})
	s, ok := m[name]
	if !ok {
		panic("icons: unknown icon " + name)
	}
	return s
}

// The sidebar, composer and right-panel icons. Folder is the closed folder
// (the default file-tree glyph); FolderOpen is the expanded one. Chevron
// right/down are the disclosure glyphs; NewChat is the composer's new-session
// bubble; Home is the home destination (a house, never the new-chat bubble; the
// fix-round audit found both actions drawn with the same glyph); Data is the
// Session Log's storage glyph.
var (
	Folder        = func() *ui.SVG { return get("folder") }
	FolderOpen    = func() *ui.SVG { return get("folder-open") }
	Plug          = func() *ui.SVG { return get("plug") }
	Plugin        = func() *ui.SVG { return get("plugin") }
	Clock         = func() *ui.SVG { return get("clock") }
	Search        = func() *ui.SVG { return get("search") }
	Plus          = func() *ui.SVG { return get("plus") }
	Sliders       = func() *ui.SVG { return get("sliders") }
	PanelLeft     = func() *ui.SVG { return get("panel-left") }
	More          = func() *ui.SVG { return get("more") }
	ChevronRight  = func() *ui.SVG { return get("chevron-right") }
	ChevronDown   = func() *ui.SVG { return get("chevron-down") }
	Message       = func() *ui.SVG { return get("message") }
	Sparkles      = func() *ui.SVG { return get("sparkles") }
	Terminal      = func() *ui.SVG { return get("terminal") }
	NewChat       = func() *ui.SVG { return get("new-chat") }
	Home          = func() *ui.SVG { return get("home") }
	Data          = func() *ui.SVG { return get("data") }
	Reload        = func() *ui.SVG { return get("reload") }
	AlertTriangle = func() *ui.SVG { return get("alert-triangle") }
	Shield        = func() *ui.SVG { return get("shield") }
	Users         = func() *ui.SVG { return get("users") }
	Activity      = func() *ui.SVG { return get("activity") }
	Globe         = func() *ui.SVG { return get("globe") }
	Close         = func() *ui.SVG { return get("x") }
	Check         = func() *ui.SVG { return get("check") }
	Send          = func() *ui.SVG { return get("send") }
	Stop          = func() *ui.SVG { return get("square") }
	Trash         = func() *ui.SVG { return get("trash") }
	Pencil        = func() *ui.SVG { return get("pencil") }
	File          = func() *ui.SVG { return get("file") }
	Image         = func() *ui.SVG { return get("image") }
	PanelRight    = func() *ui.SVG { return get("panel-right") }
	Copy          = func() *ui.SVG { return get("copy") }
)
