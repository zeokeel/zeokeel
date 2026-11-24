package gateway

// The GitHub update surface (docs/TRANSPORT.md §5.1.12): check, download and
// stage a release, and hand "install and restart" to the process host.
//
// The update bit existed since the native shell: internal/modules/update already
// checks GitHub Releases (contracts.UpdateService), verifies and stages the
// platform asset (UpdateDownloader) and records an install-on-restart plan
// (UpdateInstaller). What was missing is the wire between that backend and the
// browser — no method, no capability, no automatic check — so from the client's
// side the product simply never updated. This file is that wire.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// updateCheckInterval is how often the gateway re-checks the release feed
	// while it runs. GitHub's unauthenticated API allows far more, and an update
	// is not urgent: every six hours is enough to notice a release the same day.
	updateCheckInterval = 6 * time.Hour
	// updateCheckStartupDelay keeps the first check off the startup path: the
	// gateway should answer the first screen before it spends a request on GitHub.
	updateCheckStartupDelay = 30 * time.Second
)

// updateState is the last thing the gateway knows about updates. Guarded by
// Gateway.updateMu.
type updateState struct {
	info      contracts.UpdateInfo
	checkedAt time.Time
	// err is the last check's failure, kept for the UI: "could not check" and
	// "you are current" are different answers.
	err     error
	running bool
}

// updateStatusResult is the answer of every update method (§5.1.12). The staged
// half is the install-on-restart plan the host will apply.
type updateStatusResult struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion,omitempty"`
	Available      bool   `json:"available"`
	Notes          string `json:"notes,omitempty"`
	URL            string `json:"url,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
	Size           int64  `json:"size,omitempty"`
	// CheckedAt is when the last successful check ran (RFC 3339, empty = never).
	CheckedAt string `json:"checkedAt,omitempty"`
	// Error is the last check's failure, in the module's user-facing wording.
	// A check that failed is reported, never turned into "up to date".
	Error  string           `json:"error,omitempty"`
	Staged *stagedUpdateDTO `json:"staged,omitempty"`
}

// stagedUpdateDTO is the downloaded update waiting for install-on-restart.
// stagedUpdateDTO is what a restart would install. No size: the install plan
// carries the checksum and the layout, and inventing a byte count here would be
// a second source of truth.
type stagedUpdateDTO struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256,omitempty"`
	Kind    string `json:"kind,omitempty"`
}

// updateInstallParams is the update.install body: restart asks the host to apply
// the staged update now instead of at the next start.
type updateInstallParams struct {
	Restart bool `json:"restart,omitempty"`
}

// updateService reads the update backend, or reports that this build has none.
func (g *Gateway) updateService() (contracts.UpdateService, *protocolError) {
	service, ok := g.root.Get(contracts.KeyUpdate)
	if !ok || service == nil {
		return nil, appError(codeCapabilityUnsupported, "update_not_supported", false)
	}
	return service, nil
}

// currentVersionString is the running product version the update check compares
// against (the same value the handshake advertises).
func (g *Gateway) currentVersionString() string {
	return strings.TrimSpace(g.opts.Version)
}

// updateStatus answers what the gateway knows without touching the network.
func (g *Gateway) updateStatus() any {
	return g.snapshotUpdateStatus()
}

// snapshotUpdateStatus builds the current status under the lock.
func (g *Gateway) snapshotUpdateStatus() updateStatusResult {
	result := updateStatusResult{CurrentVersion: g.currentVersionString()}

	g.updateMu.Lock()
	info, checkedAt, stateErr := g.update.info, g.update.checkedAt, g.update.err
	g.updateMu.Unlock()

	if !checkedAt.IsZero() {
		result.CheckedAt = checkedAt.UTC().Format(time.RFC3339)
	}
	if info.LatestVersion != "" {
		result.LatestVersion = info.LatestVersion
		result.Available = info.Available
		result.Notes = info.Notes
		result.URL = info.URL
		result.SHA256 = info.SHA256
		result.Size = info.Size
	}
	if stateErr != nil {
		result.Error = stateErr.Error()
	}

	// The staged plan lives in the update module (pending.json), which is also
	// what the host reads: asking it means the button state cannot disagree with
	// what a restart would actually do.
	if service, ok := g.root.Get(contracts.KeyUpdate); ok && service != nil {
		if installer, ok := service.(contracts.UpdateInstaller); ok {
			if plan, pending := installer.PendingInstall(); pending {
				result.Staged = &stagedUpdateDTO{
					Version: plan.Version,
					SHA256:  plan.SHA256,
					Kind:    plan.Kind,
				}
			}
		}
	}
	return result
}

// updateCheck runs one check against GitHub Releases and remembers the answer.
func (g *Gateway) updateCheck(ctx context.Context) (any, *protocolError) {
	service, protocolErr := g.updateService()
	if protocolErr != nil {
		return nil, protocolErr
	}

	g.updateMu.Lock()
	if g.update.running {
		// A second caller waits for the first: hammering GitHub from a button
		// that is already in flight helps nobody.
		g.updateMu.Unlock()
		return g.snapshotUpdateStatus(), nil
	}
	g.update.running = true
	g.updateMu.Unlock()

	defer func() {
		g.updateMu.Lock()
		g.update.running = false
		g.updateMu.Unlock()
	}()

	info, err := service.Check(ctx)

	g.updateMu.Lock()
	if err != nil {
		g.update.err = err
	} else {
		g.update.info = info
		g.update.err = nil
		g.update.checkedAt = time.Now()
	}
	g.updateMu.Unlock()

	// A failed check is an answer the UI has to show ("could not reach GitHub"),
	// not a protocol error: the page reports it next to the current version.
	return g.snapshotUpdateStatus(), nil
}

// updateDownload fetches the release asset, verifies it and stages it, then
// records the install-on-restart plan.
func (g *Gateway) updateDownload(ctx context.Context) (any, *protocolError) {
	service, protocolErr := g.updateService()
	if protocolErr != nil {
		return nil, protocolErr
	}
	downloader, ok := service.(contracts.UpdateDownloader)
	if !ok {
		return nil, appError(codeCapabilityUnsupported, "update_download_not_supported", false)
	}
	installer, ok := service.(contracts.UpdateInstaller)
	if !ok {
		return nil, appError(codeCapabilityUnsupported, "update_install_not_supported", false)
	}
	download, err := downloader.Download(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	if _, err := installer.ScheduleInstall(download); err != nil {
		return nil, internalError(err)
	}
	// The plan now exists; refresh the status so the answer carries it.
	return g.snapshotUpdateStatus(), nil
}

// updateInstall hands the staged update to the host.
//
// Without restart the plan simply stays queued — the host applies it at the next
// start (internal/host/install.go). With restart the gateway writes the marker
// the host checks after the gateway stops and asks for shutdown, so the host can
// apply the update and relaunch instead of waiting for the user to restart.
func (g *Gateway) updateInstall(raw []byte) (any, *protocolError) {
	var params updateInstallParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	service, protocolErr := g.updateService()
	if protocolErr != nil {
		return nil, protocolErr
	}
	installer, ok := service.(contracts.UpdateInstaller)
	if !ok {
		return nil, appError(codeCapabilityUnsupported, "update_install_not_supported", false)
	}
	if _, pending := installer.PendingInstall(); !pending {
		// Nothing staged: say so instead of restarting into the same version.
		return nil, appError(codeNotReady, "update_not_downloaded", false)
	}
	if !params.Restart {
		return g.snapshotUpdateStatus(), nil
	}
	if err := contracts.RequestUpdateInstallNow(g.opts.DataDir); err != nil {
		return nil, internalError(err)
	}
	g.requestShutdown("update install requested")
	return g.snapshotUpdateStatus(), nil
}

// startUpdateChecks runs the periodic check while the gateway serves.
//
// It is opt-in (Options.AutoCheckUpdates) because the e2e fixtures run real
// gateways: a background check would reach for GitHub (or a test proxy) from
// every one of them.
func (g *Gateway) startUpdateChecks(ctx context.Context) {
	if !g.opts.AutoCheckUpdates {
		return
	}
	delay := g.opts.UpdateCheckStartupDelay
	if delay <= 0 {
		delay = updateCheckStartupDelay
	}
	interval := g.opts.UpdateCheckInterval
	if interval <= 0 {
		interval = updateCheckInterval
	}
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		g.checkAndNotify(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				g.checkAndNotify(ctx)
			}
		}
	}()
}

// checkAndNotify runs one background check and, when a newer release exists,
// announces it once per version.
//
// The announcement is a toast (and, where the host has an OS sink, a
// notification): the About page is a place people visit, not one they watch.
func (g *Gateway) checkAndNotify(ctx context.Context) {
	service, ok := g.root.Get(contracts.KeyUpdate)
	if !ok || service == nil {
		return
	}
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	info, err := service.Check(checkCtx)
	g.updateMu.Lock()
	if err != nil {
		g.update.err = err
		g.updateMu.Unlock()
		return
	}
	g.update.info = info
	g.update.err = nil
	g.update.checkedAt = time.Now()
	announce := info.Available && info.LatestVersion != g.announcedUpdate
	if announce {
		g.announcedUpdate = info.LatestVersion
	}
	g.updateMu.Unlock()
	if !announce {
		return
	}
	if g.notifier != nil {
		g.notifier.NotifySystem("info", fmt.Sprintf("发现新版本 %s（当前 %s），可在设置 → 关于 中更新",
			info.LatestVersion, g.currentVersionString()))
	}
}
