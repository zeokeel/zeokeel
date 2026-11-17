package contracts

import cordis "github.com/zeokeel/zeokeel-kernel"

// ToolCatalog is the narrow read-only view of the assembled tool surface
// (2026-11 audit AR-06). Diagnostics and other consumers that only need to
// know WHICH tools exist depend on this interface instead of the concrete
// *rigtool.Registry, so a UI module never has to import the backend tool
// framework to render a tool count or name list.
//
// The interface deliberately carries no rig types. The toolkit module provides
// it over its registry. Backend modules that register or invoke tools use the
// concrete registry key internal/hosttools.KeyRegistry (the backend seam);
// contracts declares no concrete-registry key and imports no part of the rig
// tool framework (guarded by TestContractsFreeOfToolRegistryImport).
type ToolCatalog interface {
	// Names returns the registered tool names in name order (the rig registry
	// sorts by name). The slice is a copy the caller may retain and mutate.
	Names() []string
	// Len reports the number of registered tools (the cheap diagnostics
	// count; no slice allocation).
	Len() int
}

// KeyToolCatalog is provided by the toolkit module: the rig-free diagnostic
// surface of the same registry the backend seam's KeyRegistry exposes.
var KeyToolCatalog = cordis.NewServiceKey[ToolCatalog]("zeokeel/toolcatalog")
