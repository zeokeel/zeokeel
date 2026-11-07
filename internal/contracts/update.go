package contracts

import (
	"context"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// UpdateInfo is the result of one update check (the old shell's updater
// "check" phase: manifest fetch + version comparison, no download).
type UpdateInfo struct {
	// CurrentVersion is the running product version.
	CurrentVersion string
	// LatestVersion is the newest version advertised by the release feed.
	LatestVersion string
	// Available reports LatestVersion > CurrentVersion.
	Available bool
	// Notes are the release notes from the manifest (Markdown).
	Notes string
	// URL is the download URL of the asset matching this platform ("" when
	// the manifest has no matching asset).
	URL string
	// SHA256 and Size describe that asset (informational).
	SHA256 string
	Size   int64
}

// UpdateService performs release-feed update checks.
type UpdateService interface {
	// Check fetches the configured release feed and compares its version with
	// the running product version.
	Check(ctx context.Context) (UpdateInfo, error)
}

// KeyUpdate is provided by the update backend module and consumed by the
// settings 更新 page.
var KeyUpdate = cordis.NewServiceKey[UpdateService]("zeokeel/update")
