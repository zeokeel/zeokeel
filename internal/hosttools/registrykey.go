package hosttools

import (
	cordis "github.com/zeokeel/zeokeel-kernel"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"
)

// KeyRegistry is the service key of the concrete ZeoKeel Rig tool registry
// (*tools.Registry) assembled by the toolkit module: backend modules that
// register, unregister or invoke tools depend on it. It lives in this shared
// backend seam rather than in contracts so the contract surface stays free of
// the tool framework (2026-11 audit AR-06); the UI layer cannot import this
// package (import-boundary allowlist), and UI/diagnostics consumers that only
// need tool names or a count read contracts.KeyToolCatalog instead.
//
// The service name is the stable assembly contract ("zeokeel/toolkit"): it
// predates the key's move out of contracts and does not change with it, so
// assembly documents and preflight are unaffected.
var KeyRegistry = cordis.NewServiceKey[*rigtool.Registry]("zeokeel/toolkit")
