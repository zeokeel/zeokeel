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
//	  attachments/v1/            (image/file objects referenced by messages)
//	  profiles/                  (detected, not imported)
//
// Session format: UTF-8 JSONL, one event per line, optional zstd compression.
// The first line is a "session" header (version, id, createdAt, cwd, origin,
// ...); later lines are {type, seq, time, data} events. Only the DSH v4 format
// is understood: any other header version fails with ErrUnsupportedVersion and
// is reported as an explicit unsupported_version skip instead of being
// half-parsed under v4 assumptions.
//
// What becomes a conversation row: genuine user/message events
// (source.kind == "user") and assistant/message events that carry visible
// text (plus their reasoning). DSH injects most per-turn context through
// user/message events with source.kind time-context / runtime-context /
// agent-message / subagent-settled / ... (≈89% of real user events); those are
// context, not the human's words, and are filtered. Step-level assistant
// events (reasoning/tool-call only, ≈96% of real assistant events) are also
// filtered so the migrated transcript is the actual conversation instead of
// per-step noise. Tool calls/results and system messages are counted in the
// report but not persisted: ZeoKeel's messages table has no tool-call columns,
// so replaying synthetic tool roles would corrupt later model calls.
//
// Images and file attachments: DSH content parts of type image/file reference
// content-addressed objects under <DSH home>/attachments/v1 (and older exports
// may embed base64 bytes). Their bytes are copied into the ZeoKeel data
// directory as attachment rows linked to the imported message (MessageID), so
// images render in the transcript and file attachments remain readable; the
// messages table itself is not extended. Sources that are missing, above the
// per-file cap (25 MiB) or corrupt are counted as skips (Report.Skips /
// ImagesDropped / AttachmentsDropped) instead of failing a session.
//
// Idempotency: with Options.ManifestPath the importer records each imported
// DSH session (id, source size/mtime/SHA-256) in a JSON manifest and skips
// unchanged sessions on later runs; an empty path keeps the non-idempotent
// behaviour. The manifest format is versioned and migrated in place (legacy
// keys are re-mapped so an upgrade does not re-import), and a damaged manifest
// is recovered from its rolling backup or preserved and rebuilt with a warning.
//
// Security: workspace/session content is untrusted data. The importer never
// executes anything from the DSH home, never prints credential values (it
// returns only ref names), and writes only through the store's validated API.
// Compressed sessions need the external "zstd" binary; without it the
// importer reports ErrZstdUnavailable instead of silently skipping sessions.
package importer
