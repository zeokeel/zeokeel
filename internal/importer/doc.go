// Package importer migrates DSH (DeepSeek Harness) desktop data into ZeoKeel.
//
// The package is deliberately self-contained: it reads DSH's on-disk layout
// directly (it does not start DSH or import DSH code) and writes through the
// public internal/store API. It is the backend of the "import from DSH" flow
// planned for the app/CLI (PD-04); the UI/CLI wiring is owned by other files.
//
// DSH layout handled here:
//
//	~/.dsh/
//	  sessions/<workspace>/<session-id>/session.v4.jsonl[.zstd]
//	  .credentials.yaml          (only key NAMES are read, never values)
//	  profiles/, attachments/    (detected, not imported in this minimum)
//
// Session format: UTF-8 JSONL, one event per line, optional zstd compression.
// The first line is a "session" header (id, createdAt, cwd, origin, ...);
// later lines are {type, seq, time, data} events. The importer keeps the
// user-visible conversation: user/message and assistant/message (text +
// reasoning; tool-call parts are folded into readable assistant text), while
// tool calls/results/system messages are counted in the report but not
// persisted as conversation rows (ZeoKeel's messages table has no tool-call
// columns, so replaying synthetic tool roles would corrupt later model calls).
//
// Security: workspace/session content is untrusted data. The importer never
// executes anything from the DSH home, never prints credential values (it
// returns only ref names), and writes only through the store's validated API.
// Compressed sessions need the external "zstd" binary; without it the
// importer reports ErrZstdUnavailable instead of silently skipping sessions.
package importer
