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

// UpdateDownload is one verified download staged under the data directory.
type UpdateDownload struct {
	// Version is the manifest version that was downloaded.
	Version string
	// Path is the staged file inside <dataDir>/updates.
	Path string
	// SHA256 is the verified asset checksum (hex).
	SHA256 string
	// Size is the written byte count.
	Size int64
}

// UpdateDownloader is the optional download half of the update service: it
// fetches the manifest's platform asset, verifies SHA-256/size and stages it
// under <dataDir>/updates. Installing/replacing the running binary is a
// separate, deliberate host step.
type UpdateDownloader interface {
	Download(ctx context.Context) (UpdateDownload, error)
}

// UpdateInstallPlan is a downloaded update scheduled for install-on-restart.
type UpdateInstallPlan struct {
	// Version is the staged version.
	Version string
	// Path is the staged asset inside <dataDir>/updates.
	Path string
	// SHA256 is the verified checksum.
	SHA256 string
}

// UpdateInstaller is the optional install half of the update service: it
// schedules a downloaded update for install-on-restart. Replacing the running
// executable is the host's deliberate step on the next start.
type UpdateInstaller interface {
	// ScheduleInstall records the downloaded update as pending.
	ScheduleInstall(download UpdateDownload) (UpdateInstallPlan, error)
	// PendingInstall reports the scheduled install (ok=false when none).
	PendingInstall() (UpdateInstallPlan, bool)
}
