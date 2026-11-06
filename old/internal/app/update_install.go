package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/updater"
)

// GUI/embedding-side updater seam.
//
// The shell's Updates page owns the state machine and a future GUI install
// button can delegate the security-sensitive part here: check the feed,
// download and verify the asset, schedule the install for the next start and
// (when the platform allows it) apply the staged executable immediately. All
// failures leave the running binary untouched: a download is only moved into
// place after the SHA-256 and size verification, and a failed replace restores
// the previous executable (see appExeReplacer / updater.ApplyPendingInstall).

// UpdateInstallResult reports what DownloadAndInstallUpdate did.
type UpdateInstallResult struct {
	// CurrentVersion is the running build version used for the check.
	CurrentVersion string
	// ReleaseVersion is the newer release's version (empty when up to date).
	ReleaseVersion string
	// StagedPath is the verified download in <data dir>/updates.
	StagedPath string
	// Staged reports that a verified download exists and is scheduled for
	// install-on-restart (the pending marker was written).
	Staged bool
	// Applied reports that the running executable was replaced right away.
	// When false with Staged true, the update installs on the next start.
	Applied bool
	// UpToDate reports that the feed had no newer release (nothing downloaded).
	UpToDate bool
}

// updateDataDir is the update staging root: the live store's data directory
// (the same directory the shell updater uses), falling back to DataDir().
func (a *app) updateDataDir() string {
	if a != nil && a.services != nil {
		if st := a.services.Store(); st != nil {
			if dir := strings.TrimSpace(st.Dir()); dir != "" {
				return dir
			}
		}
	}
	return DataDir()
}

// updaterReplacer returns the configured executable-replace seam (tests inject
// a fake; production renames the current binary aside and moves the verified
// staged binary into place).
func (a *app) updaterReplacer() updater.Replacer {
	if a != nil && a.updateReplacer != nil {
		return a.updateReplacer
	}
	return appExeReplacer{}
}

// updateInstallConfig builds the updater configuration shared by the GUI install
// path and the startup pending-install application.
func (a *app) updateInstallConfig(feed string) updater.Config {
	return updater.Config{
		CurrentVersion:    BuildVersion,
		FeedURL:           feed,
		DataDir:           a.updateDataDir(),
		Replacer:          a.updaterReplacer(),
		AllowInsecureHTTP: os.Getenv("ZEOKEEL_UPDATE_ALLOW_HTTP") == "1",
	}
}

// DownloadAndInstallUpdate is the app seam a GUI install button (or an embedding
// client) calls: it checks the configured feed, streams the asset to
// <data dir>/updates with progress, verifies it, schedules the install for the
// next start and applies it immediately when possible.
//
// Return contract:
//   - no configured feed → updater.ErrDisabled;
//   - the feed has no newer release → (result{UpToDate:true}, nil);
//   - download/verify failure → (nil, error) and the running binary is untouched;
//   - schedule/apply failure → (result{Staged:true}, error): the verified
//     download is kept and will be installed on the next start, so callers must
//     not treat the update as lost;
//   - success → (result{Staged:true, Applied:true}, nil).
//
// progress (optional) is invoked from the download goroutine with the number of
// asset bytes read and the expected total (the manifest size, or the response
// Content-Length when the manifest omits it).
func (a *app) DownloadAndInstallUpdate(ctx context.Context, progress func(downloaded, total int64)) (*UpdateInstallResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	feed := a.settingString(updateFeedURLKey, "")
	if feed == "" {
		feed = strings.TrimSpace(os.Getenv("ZEOKEEL_UPDATE_FEED"))
	}
	result := &UpdateInstallResult{CurrentVersion: BuildVersion}
	if feed == "" {
		return nil, updater.ErrDisabled
	}
	cfg := a.updateInstallConfig(feed)
	release, available, err := updater.Check(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if !available || release == nil {
		result.UpToDate = true
		return result, nil
	}
	result.ReleaseVersion = release.Version

	downloadCfg := cfg
	if progress != nil {
		downloadCfg.HTTPClient = &http.Client{
			Timeout:   updater.DefaultHTTPTimeout,
			Transport: &updateProgressTransport{total: release.Asset.Size, progress: progress},
		}
	}
	staged, err := updater.Download(ctx, downloadCfg, *release)
	if err != nil {
		return nil, err
	}
	result.Staged = true
	result.StagedPath = staged.Path

	// Record the install for the next start before trying the immediate apply:
	// a failed replace then still installs on restart instead of losing the
	// verified download.
	if err := updater.ScheduleInstall(cfg, *staged); err != nil {
		return result, fmt.Errorf("app: schedule update install: %w", err)
	}
	applied, err := updater.ApplyPendingInstall(cfg)
	if err != nil {
		return result, fmt.Errorf("app: apply update install: %w", err)
	}
	result.Applied = applied
	return result, nil
}

// applyPendingUpdate applies a verified download scheduled by an earlier run
// (install-on-restart, including resuming an interrupted replace). It is called
// once at startup; it never blocks on the network and a missing/corrupt marker
// is a no-op (the updater self-heals corrupt markers).
func (a *app) applyPendingUpdate() (bool, error) {
	cfg := a.updateInstallConfig("")
	applied, err := updater.ApplyPendingInstall(cfg)
	if err != nil {
		return false, err
	}
	if applied {
		log.Printf("zeokeel: applied the pending update; the new build runs from the next launch")
	}
	return applied, nil
}

// updateProgressTransport wraps the HTTP client transport so Download reports
// progress without the updater package having to own a UI callback.
type updateProgressTransport struct {
	base     http.RoundTripper
	total    int64
	progress func(downloaded, total int64)
}

func (t *updateProgressTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if t.progress == nil || resp.Body == nil {
		return resp, nil
	}
	total := t.total
	if total <= 0 {
		total = resp.ContentLength
	}
	resp.Body = &updateProgressBody{ReadCloser: resp.Body, total: total, progress: t.progress}
	return resp, nil
}

// updateProgressBody counts bytes read from the asset response.
type updateProgressBody struct {
	io.ReadCloser
	total    int64
	read     int64
	progress func(downloaded, total int64)
}

func (b *updateProgressBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.read += int64(n)
		b.progress(b.read, b.total)
	}
	return n, err
}

// appExeReplacer is the production updater.Replacer for the app process path.
// It mirrors cmd/zeokeel's exeReplacer: rename the running executable aside,
// move the verified staged binary in, and restore the old binary if the move
// fails, so the install directory always keeps a working executable.
type appExeReplacer struct{}

var _ updater.Replacer = appExeReplacer{}

// CurrentExecutable implements updater.Replacer.
func (appExeReplacer) CurrentExecutable() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate current executable: %w", err)
	}
	if resolved, evalErr := filepath.EvalSymlinks(path); evalErr == nil {
		path = resolved
	}
	return path, nil
}

// Replace implements updater.Replacer.
func (appExeReplacer) Replace(currentPath, stagedPath string) error {
	if strings.TrimSpace(currentPath) == "" || strings.TrimSpace(stagedPath) == "" {
		return errors.New("replace: empty path")
	}
	backup := fmt.Sprintf("%s.old-%d", currentPath, time.Now().Unix())
	if err := os.Rename(currentPath, backup); err != nil {
		return fmt.Errorf("replace: move current binary aside: %w", err)
	}
	if err := os.Rename(stagedPath, currentPath); err != nil {
		if restoreErr := os.Rename(backup, currentPath); restoreErr != nil {
			return fmt.Errorf("replace: move staged binary: %w (rollback failed: %v; backup at %s)", err, restoreErr, backup)
		}
		return fmt.Errorf("replace: move staged binary: %w", err)
	}
	_ = os.Chmod(currentPath, 0o755)
	return nil
}
