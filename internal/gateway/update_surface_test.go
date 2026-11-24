package gateway

// The GitHub update surface (AUDIT-2026-11-24 follow-up): the backend could
// check, download and stage a release since the modular shell, but nothing on
// the wire reached it — no method, no capability, no automatic check — so from
// the browser the product never updated. These tests drive the four methods and
// the install handoff with a fake update service, so no network is involved.

import (
	"context"
	"errors"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeUpdate is a contracts.Update* triple with scripted answers.
type fakeUpdate struct {
	info     contracts.UpdateInfo
	checkErr error
	download contracts.UpdateDownload
	downErr  error
	plan     contracts.UpdateInstallPlan
	schedule error
	checks   int
	staged   bool
}

func (f *fakeUpdate) Check(context.Context) (contracts.UpdateInfo, error) {
	f.checks++
	if f.checkErr != nil {
		return contracts.UpdateInfo{}, f.checkErr
	}
	return f.info, nil
}

func (f *fakeUpdate) Download(context.Context) (contracts.UpdateDownload, error) {
	if f.downErr != nil {
		return contracts.UpdateDownload{}, f.downErr
	}
	return f.download, nil
}

func (f *fakeUpdate) ScheduleInstall(download contracts.UpdateDownload) (contracts.UpdateInstallPlan, error) {
	if f.schedule != nil {
		return contracts.UpdateInstallPlan{}, f.schedule
	}
	f.staged = true
	f.plan.Version = download.Version
	f.plan.SHA256 = download.SHA256
	return f.plan, nil
}

func (f *fakeUpdate) PendingInstall() (contracts.UpdateInstallPlan, bool) {
	if !f.staged {
		return contracts.UpdateInstallPlan{}, false
	}
	return f.plan, true
}

// updateGateway builds a gateway whose root provides one fake update service.
func updateGateway(t *testing.T, update *fakeUpdate) (*Gateway, cordis.Context) {
	t.Helper()
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	root := testRoot(t, chat)
	if _, err := root.Provide(contracts.KeyUpdate, contracts.UpdateService(update)); err != nil {
		t.Fatalf("provide update: %v", err)
	}
	gateway, err := New(root, Options{
		DataDir: t.TempDir(),
		Port:    -1,
		Version: "v0.3.9",
		Logger:  testLogger{t: t},
		Out:     discardWriter{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Provide(root); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	return gateway, root
}

func TestUpdateStatusBeforeAnyCheck(t *testing.T) {
	update := &fakeUpdate{}
	gateway, _ := updateGateway(t, update)

	status, ok := gateway.updateStatus().(updateStatusResult)
	if !ok {
		t.Fatalf("updateStatus = %T", gateway.updateStatus())
	}
	if status.CurrentVersion != "v0.3.9" {
		t.Fatalf("currentVersion = %q, want the running version", status.CurrentVersion)
	}
	if status.LatestVersion != "" || status.Available || status.CheckedAt != "" {
		t.Fatalf("status before a check = %+v, want empty", status)
	}
	if status.Error != "" {
		t.Fatalf("status before a check carries error %q", status.Error)
	}
}

func TestUpdateCheckReportsAvailableAndFailureSeparately(t *testing.T) {
	update := &fakeUpdate{info: contracts.UpdateInfo{
		CurrentVersion: "v0.3.9",
		LatestVersion:  "v0.4.0",
		Available:      true,
		Notes:          "## 新版本\n- 更快",
		URL:            "https://example.test/zeokeel.AppImage",
		SHA256:         "abc",
		Size:           42,
	}}
	gateway, _ := updateGateway(t, update)

	result, protocolErr := gateway.updateCheck(context.Background())
	if protocolErr != nil {
		t.Fatalf("update.check: %v", protocolErr)
	}
	status := result.(updateStatusResult)
	if !status.Available || status.LatestVersion != "v0.4.0" || status.Notes == "" {
		t.Fatalf("status after a check = %+v", status)
	}
	if status.CheckedAt == "" {
		t.Fatal("checkedAt was not stamped")
	}

	// A failure is data for the UI, not a protocol error, and it must not be
	// reported as "up to date".
	update.checkErr = errors.New("连不上 GitHub")
	result, protocolErr = gateway.updateCheck(context.Background())
	if protocolErr != nil {
		t.Fatalf("a failed check returned a protocol error: %v", protocolErr)
	}
	status = result.(updateStatusResult)
	if status.Error == "" {
		t.Fatal("failed check did not surface its error")
	}
	// The last known answer stays (it is still what we saw), next to the error:
	// the UI shows both, and never turns a failure into "up to date".
	if !status.Available || status.LatestVersion != "v0.4.0" {
		t.Fatalf("failed check dropped the last known answer: %+v", status)
	}
	if status.CheckedAt == "" {
		t.Fatal("failed check cleared the last successful checkedAt")
	}
}

func TestUpdateDownloadStagesAndInstallRequiresAPlan(t *testing.T) {
	update := &fakeUpdate{
		info:     contracts.UpdateInfo{LatestVersion: "v0.4.0", Available: true, URL: "https://example.test/x"},
		download: contracts.UpdateDownload{Version: "v0.4.0", SHA256: "deadbeef", Size: 10, Path: "/tmp/x"},
	}
	gateway, _ := updateGateway(t, update)

	// Nothing staged yet: install must say so instead of restarting into the
	// same version.
	if _, protocolErr := gateway.updateInstall(nil); protocolErr == nil {
		t.Fatal("update.install without a staged update succeeded")
	} else if protocolErr.Data["name"] != "update_not_downloaded" {
		t.Fatalf("update.install error = %v", protocolErr)
	}

	result, protocolErr := gateway.updateDownload(context.Background())
	if protocolErr != nil {
		t.Fatalf("update.download: %v", protocolErr)
	}
	status := result.(updateStatusResult)
	if status.Staged == nil || status.Staged.Version != "v0.4.0" {
		t.Fatalf("staged after download = %+v", status.Staged)
	}

	// Without restart the plan just stays queued.
	result, protocolErr = gateway.updateInstall([]byte(`{"restart":false}`))
	if protocolErr != nil {
		t.Fatalf("update.install: %v", protocolErr)
	}
	if stage := result.(updateStatusResult).Staged; stage == nil {
		t.Fatal("install without restart dropped the plan")
	}
	if contracts.UpdateInstallNowRequested(gateway.opts.DataDir) {
		t.Fatal("install without restart wrote the restart marker")
	}

	// With restart it asks the host to install: marker + shutdown request.
	if _, protocolErr = gateway.updateInstall([]byte(`{"restart":true}`)); protocolErr != nil {
		t.Fatalf("update.install(restart): %v", protocolErr)
	}
	if !contracts.UpdateInstallNowRequested(gateway.opts.DataDir) {
		t.Fatal("install with restart did not write the handoff marker")
	}
	select {
	case <-gateway.shutdown:
	case <-time.After(2 * time.Second):
		t.Fatal("install with restart did not request a shutdown")
	}
}

func TestUpdateCapabilityFollowsTheBackend(t *testing.T) {
	update := &fakeUpdate{}
	gateway, _ := updateGateway(t, update)
	caps := gateway.gatewayCapabilities()
	found := false
	for _, capability := range caps {
		if capability == "update" {
			found = true
		}
	}
	if !found {
		t.Fatalf("capabilities = %v, want update", caps)
	}

	// A build without the module must not advertise the capability (and the
	// methods answer 1023 rather than pretending).
	bare, _ := func() (*Gateway, cordis.Context) {
		chat := newFakeChat()
		chat.addSession("s1", "First", "hello")
		root := testRoot(t, chat)
		gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return gateway, root
	}()
	for _, capability := range bare.gatewayCapabilities() {
		if capability == "update" {
			t.Fatal("a build without the update module advertised the update capability")
		}
	}
	if _, protocolErr := bare.updateCheck(context.Background()); protocolErr == nil {
		t.Fatal("update.check succeeded without the backend")
	}
}
