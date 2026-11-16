package contracts

import cordis "github.com/zeokeel/zeokeel-kernel"

// ToolCatalog is the narrow read-only view of the assembled tool surface
// (2026-11 audit AR-06). Diagnostics and other consumers that only need to
// know WHICH tools exist depend on this interface instead of the concrete
// *rigtool.Registry the legacy KeyToolkit exposes, so a UI module never has to
// import the backend tool framework to render a tool count or name list.
//
// The interface deliberately carries no rig types. The toolkit module provides
// it over its registry; the legacy KeyToolkit key stays until every consumer
// has switched (the B/D waves), and is removed last (E2).
type ToolCatalog interface {
	// Names returns the registered tool names in name order (the rig registry
	// sorts by name). The slice is a copy the caller may retain and mutate.
	Names() []string
	// Len reports the number of registered tools (the cheap diagnostics
	// count; no slice allocation).
	Len() int
}

// KeyToolCatalog is provided by the toolkit module alongside the legacy
// KeyToolkit: the rig-free diagnostic surface of the same registry.
var KeyToolCatalog = cordis.NewServiceKey[ToolCatalog]("zeokeel/toolcatalog")
