package contracts

import (
	"context"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// ImportPreview is the result of scanning a foreign data directory (the old
// shell's "import from DSH" preview).
type ImportPreview struct {
	// SourceDir is the scanned directory.
	SourceDir string
	// Sessions is how many session logs were found and parsable.
	Sessions int
	// Messages is how many conversation messages would be imported.
	Messages int
	// Workspaces is how many distinct session working directories would be
	// registered as workspaces.
	Workspaces int
	// Skipped is how many session logs are unusable (unsupported version,
	// compressed without zstd, corrupt, ...).
	Skipped int
	// Notes lists human-readable findings (skips, unsupported entries).
	Notes []string
}

// ImportResult reports what one import run actually wrote.
type ImportResult struct {
	ImportedSessions   int
	ImportedMessages   int
	ImportedWorkspaces int
	Skipped            int
	// Errors lists non-fatal per-session problems.
	Errors []string
}

// ImporterService scans and imports foreign (DSH) desktop data into the
// product store.
type ImporterService interface {
	// Scan inspects dir (the DSH data home) without writing anything.
	Scan(ctx context.Context, dir string) (ImportPreview, error)
	// Import writes the scan's sessions/workspaces through the product
	// store; sessions already imported are skipped.
	Import(ctx context.Context, dir string) (ImportResult, error)
}

// KeyImporter is provided by the importer backend module and consumed by the
// settings 导入 page.
var KeyImporter = cordis.NewServiceKey[ImporterService]("zeokeel/importer")
