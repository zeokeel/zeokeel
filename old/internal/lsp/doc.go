// Package lsp is ZeoKeel's language-server capability seam.
//
// It has two layers, mirroring DSH's split between dsh-lsp (the capability
// seam) and dsh-lsp-stdio (the stdio provider):
//
//   - Provider/Query/Result are the abstract seam a model-facing tool depends
//     on: one read-only query (goToDefinition, findReferences,
//     goToImplementation or hover) answered for one source file.
//   - StdioProvider is the generic provider: it starts the configured language
//     server as a child process, speaks Content-Length framed JSON-RPC over
//     its stdin/stdout (initialize/initialized, textDocument/didOpen with the
//     current file content, textDocument/definition|references|implementation|
//     hover, shutdown/exit) and isolates every failure: a server that crashes,
//     hangs or lacks a capability only fails that one query.
//
// Configuration is a language-id keyed map (lsp.servers, or a gated
// .zeokeel/lsp.json workspace file); see config.go. Coordinates crossing the
// seam are one-based line/column pairs whose column counts UTF-16 code units,
// matching LSP's default position encoding and DSH's tool-lsp contract.
package lsp
