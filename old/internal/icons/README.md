# Icons

The sidebar, composer and right-panel glyphs follow DSH's icon geometry:
16×16 viewBox, `stroke="currentColor"`, `stroke-width="1.2"`, round caps
and joins. Their paths are the baseline's own artwork where it was
available (folder, folder-plus, chevrons, sliders, plus, search, ellipsis,
panel-left, new-chat, reload, and the sidebar's four-lobe Plugins/clover
glyph); terminal and data are semantic equivalents drawn on the same grid.
`folder-open.svg` adds the expanded-folder state.

Older 24×24 Lucide icons (ISC) remain for glyphs with no DSH counterpart
yet (`clock`, `plug`, `message`, `sparkles`); they are parsed the same way
and painted in the text color. Add an icon by dropping `svg/<name>.svg`
and referencing it from `icons.go`; the tests iterate every getter and pin
the 16×16 grid for the sidebar/composer set.
