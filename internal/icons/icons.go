// Package icons holds the app's icon library: DSH-style 16x16 line SVGs
// (leading 16px, stroke 1.2) and the older 24x24 Lucide artwork that has no
// DSH counterpart yet. Icons are semantic, not pixel copies of DSH's
// artwork; where DSH's geometry is available it is used so the sidebar and
// composer line up with the baseline.
//
// The browser client is the only consumer now: web/scripts/gen-icons.mjs reads
// svg/*.svg from disk and generates the React components. The Go side therefore
// only embeds the files (so they cannot be dropped from the module by accident
// and remain readable to a Go tool); it no longer parses them into the native
// engine's SVG type, which is what kept this package tied to mygo until S6.
package icons

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
)

//go:embed svg/*.svg
var files embed.FS

// FS exposes the embedded icon files (svg/<name>.svg).
func FS() fs.FS { return files }

// Names lists the icon names (file base names, sorted).
func Names() []string {
	entries, err := files.ReadDir("svg")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".svg") {
			continue
		}
		names = append(names, strings.TrimSuffix(entry.Name(), ".svg"))
	}
	sort.Strings(names)
	return names
}

// SVG returns one icon's file contents, or nil when the name is unknown.
func SVG(name string) []byte {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "/\\") {
		return nil
	}
	data, err := files.ReadFile("svg/" + name + ".svg")
	if err != nil {
		return nil
	}
	return data
}
