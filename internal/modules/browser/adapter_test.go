package browser

// Adapter tests: wiring validation, page bookkeeping, error mapping (transport
// failure => unavailable, deadline => timeout), close, status with/without an
// endpoint and with a failing probe, and the module descriptor seam. Everything
// runs over the fake Transport: no browser, no display, no network.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const testEndpoint = "ws://127.0.0.1:9222/devtools/browser/abc"

// pageTransport answers every method the adapter can send, numbering targets.
func pageTransport(t *testing.T) *fakeTransport {
	t.Helper()
	var (
		mu   sync.Mutex
		next int
	)
	tr := &fakeTransport{}
	tr.reply = func(_ context.Context, method string, params any) (json.RawMessage, error) {
		switch method {
		case "Target.createTarget":
			mu.Lock()
			next++
			id := "T-" + strconv.Itoa(next)
			mu.Unlock()
			return json.RawMessage(`{"targetId":"` + id + `"}`), nil
		case "Runtime.evaluate":
			return json.RawMessage(`{"result":{"type":"string","value":"Title of ` + params.(evaluateParams).Expression + `"}}`), nil
		case "Page.captureScreenshot":
			return json.RawMessage(`{"data":"` + base64.StdEncoding.EncodeToString([]byte("png")) + `"}`), nil
		case "Target.closeTarget":
			return json.RawMessage(`{"success":true}`), nil
		default:
			t.Errorf("unexpected method %q", method)
			return nil, errors.New("unexpected method")
		}
	}
	return tr
}

func mustNew(t *testing.T, cfg Config) *Adapter {
	t.Helper()
	adapter, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return adapter
}

func TestNewDefaultsToVisibleWindow(t *testing.T) {
	adapter := mustNew(t, Config{Transport: &fakeTransport{}})
	if adapter.Headless() {
		t.Error("Headless() = true, want false: the product default is a visible window")
	}
	if adapter.Endpoint() != "" {
		t.Errorf("Endpoint() = %q, want empty", adapter.Endpoint())
	}
}

func TestNewRejectsEndpointWithoutTransport(t *testing.T) {
	adapter, err := New(Config{Endpoint: testEndpoint})
	if !errors.Is(err, contracts.ErrBrowserUnavailable) {
		t.Fatalf("New(endpoint, no transport) = (%+v, %v), want ErrBrowserUnavailable", adapter, err)
	}
	if adapter != nil {
		t.Errorf("adapter = %+v, want nil on a wiring error", adapter)
	}
}

func TestOperationsWithoutEndpointAreUnavailable(t *testing.T) {
	tr := &fakeTransport{}
	adapter := mustNew(t, Config{Transport: tr})
	ctx := context.Background()

	if _, err := adapter.Open(ctx, "https://example.com/"); !errors.Is(err, contracts.ErrBrowserUnavailable) {
		t.Errorf("Open = %v, want ErrBrowserUnavailable", err)
	}
	if _, err := adapter.Title(ctx, "T-1"); !errors.Is(err, contracts.ErrBrowserUnavailable) {
		t.Errorf("Title = %v, want ErrBrowserUnavailable", err)
	}
	if _, err := adapter.Screenshot(ctx, "T-1", false); !errors.Is(err, contracts.ErrBrowserUnavailable) {
		t.Errorf("Screenshot = %v, want ErrBrowserUnavailable", err)
	}
	if err := adapter.Close(ctx, "T-1"); !errors.Is(err, contracts.ErrBrowserUnavailable) {
		t.Errorf("Close = %v, want ErrBrowserUnavailable", err)
	}
	if got := tr.count(); got != 0 {
		t.Fatalf("sends = %d, want 0 without an endpoint", got)
	}
}

func TestOpenRecordsPagesAndStatusCountsThem(t *testing.T) {
	tr := pageTransport(t)
	adapter := mustNew(t, Config{Endpoint: testEndpoint, Transport: tr})
	ctx := context.Background()

	first, err := adapter.Open(ctx, "https://example.com/one")
	if err != nil {
		t.Fatalf("Open(first): %v", err)
	}
	second, err := adapter.Open(ctx, "https://example.com/two")
	if err != nil {
		t.Fatalf("Open(second): %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("both pages got id %q", first.ID)
	}
	status, err := adapter.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Connected || status.Endpoint != testEndpoint || status.Pages != 2 {
		t.Fatalf("status = %+v, want connected, %q, 2 pages", status, testEndpoint)
	}
}

func TestOpenBadURLSendsNothing(t *testing.T) {
	tr := pageTransport(t)
	adapter := mustNew(t, Config{Endpoint: testEndpoint, Transport: tr})
	if _, err := adapter.Open(context.Background(), "file:///etc/passwd"); !errors.Is(err, contracts.ErrBrowserBadURL) {
		t.Fatalf("Open(file) = %v, want ErrBrowserBadURL", err)
	}
	if got := tr.count(); got != 0 {
		t.Fatalf("sends = %d, want 0", got)
	}
}

func TestTitleAndScreenshotThroughTheAdapter(t *testing.T) {
	tr := pageTransport(t)
	adapter := mustNew(t, Config{Endpoint: testEndpoint, Transport: tr})
	ctx := context.Background()
	page, err := adapter.Open(ctx, "https://example.com/")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	title, err := adapter.Title(ctx, page.ID)
	if err != nil {
		t.Fatalf("Title: %v", err)
	}
	if title == "" {
		t.Fatal("Title = \"\", want the evaluate value")
	}

	shot, err := adapter.Screenshot(ctx, page.ID, true)
	if err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if string(shot) != "png" {
		t.Errorf("Screenshot = %q, want png", shot)
	}
}

func TestUnknownPageIsNotFoundWithoutSending(t *testing.T) {
	tr := pageTransport(t)
	adapter := mustNew(t, Config{Endpoint: testEndpoint, Transport: tr})
	ctx := context.Background()

	if _, err := adapter.Title(ctx, "T-missing"); !errors.Is(err, contracts.ErrBrowserNotFound) {
		t.Errorf("Title(unknown) = %v, want ErrBrowserNotFound", err)
	}
	if _, err := adapter.Screenshot(ctx, "", false); !errors.Is(err, contracts.ErrBrowserNotFound) {
		t.Errorf("Screenshot(empty) = %v, want ErrBrowserNotFound", err)
	}
	if err := adapter.Close(ctx, "T-missing"); !errors.Is(err, contracts.ErrBrowserNotFound) {
		t.Errorf("Close(unknown) = %v, want ErrBrowserNotFound", err)
	}
	if got := tr.count(); got != 0 {
		t.Fatalf("sends = %d, want 0 for unknown pages", got)
	}
}

func TestTransportErrorIsUnavailable(t *testing.T) {
	tr := &fakeTransport{reply: func(context.Context, string, any) (json.RawMessage, error) {
		return nil, errors.New("dial tcp 127.0.0.1:9222: connect: connection refused")
	}}
	adapter := mustNew(t, Config{Endpoint: testEndpoint, Transport: tr})
	_, err := adapter.Open(context.Background(), "https://example.com/")
	if !errors.Is(err, contracts.ErrBrowserUnavailable) {
		t.Fatalf("Open = %v, want ErrBrowserUnavailable", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error %q lost the transport cause", err)
	}
}

// blockingTransport never answers until the context ends, which is exactly what
// a hung browser looks like.
func blockingTransport() *fakeTransport {
	return &fakeTransport{reply: func(ctx context.Context, _ string, _ any) (json.RawMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
}

func TestConfiguredTimeoutIsATimeout(t *testing.T) {
	adapter := mustNew(t, Config{
		Endpoint:  testEndpoint,
		Transport: blockingTransport(),
		Timeout:   15 * time.Millisecond,
	})
	_, err := adapter.Open(context.Background(), "https://example.com/")
	if !errors.Is(err, contracts.ErrBrowserTimeout) {
		t.Fatalf("Open(hung browser) = %v, want ErrBrowserTimeout", err)
	}
}

func TestCallerDeadlineIsATimeout(t *testing.T) {
	adapter := mustNew(t, Config{
		Endpoint:  testEndpoint,
		Transport: blockingTransport(),
		Timeout:   time.Hour,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	_, err := adapter.Open(ctx, "https://example.com/")
	if !errors.Is(err, contracts.ErrBrowserTimeout) {
		t.Fatalf("Open(caller deadline) = %v, want ErrBrowserTimeout", err)
	}
}

func TestCloseForgetsThePage(t *testing.T) {
	tr := pageTransport(t)
	adapter := mustNew(t, Config{Endpoint: testEndpoint, Transport: tr})
	ctx := context.Background()
	page, err := adapter.Open(ctx, "https://example.com/")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := adapter.Close(ctx, page.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	status, err := adapter.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Pages != 0 {
		t.Errorf("status.Pages = %d, want 0 after Close", status.Pages)
	}
	if err := adapter.Close(ctx, page.ID); !errors.Is(err, contracts.ErrBrowserNotFound) {
		t.Fatalf("second Close = %v, want ErrBrowserNotFound", err)
	}
	if got := tr.last(t).method; got != "Target.closeTarget" {
		t.Errorf("last method = %q, want Target.closeTarget", got)
	}
}

func TestStatusWithoutEndpointIsDisconnectedNotAnError(t *testing.T) {
	adapter := mustNew(t, Config{Transport: &fakeTransport{}})
	status, err := adapter.Status(context.Background())
	if err != nil {
		t.Fatalf("Status = %v, want nil (a report, not a probe failure)", err)
	}
	if status.Connected || status.Endpoint != "" || status.Pages != 0 {
		t.Fatalf("status = %+v, want disconnected with no endpoint and no pages", status)
	}
}

func TestStatusUsesTheProbe(t *testing.T) {
	failing := mustNew(t, Config{
		Endpoint:  testEndpoint,
		Transport: &fakeTransport{},
		Probe:     func(context.Context) error { return errors.New("cdp handshake refused") },
	})
	status, err := failing.Status(context.Background())
	if err != nil {
		t.Fatalf("Status(failing probe) = %v, want nil", err)
	}
	if status.Connected {
		t.Fatalf("status = %+v, want Connected=false when the probe fails", status)
	}
	if status.Endpoint != testEndpoint {
		t.Errorf("status.Endpoint = %q, want the configured endpoint", status.Endpoint)
	}

	healthy := mustNew(t, Config{
		Endpoint:  testEndpoint,
		Transport: &fakeTransport{},
		Probe:     func(context.Context) error { return nil },
	})
	status, err = healthy.Status(context.Background())
	if err != nil {
		t.Fatalf("Status(healthy probe) = %v", err)
	}
	if !status.Connected {
		t.Fatalf("status = %+v, want Connected=true", status)
	}
}

func TestModuleDescriptorMetadata(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, ModuleID)
	}
	if descriptor.Name != "ZeoKeel browser (CDP)" {
		t.Errorf("name = %q, want ZeoKeel browser (CDP)", descriptor.Name)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyBrowser.Name() {
		t.Fatalf("provides = %+v, want the browser key", descriptor.Provides)
	}
	if len(descriptor.Requires) != 0 {
		t.Fatalf("requires = %+v, want none: a browser needs no inbox and no store", descriptor.Requires)
	}
}

func TestModuleActivationProvidesDisconnectedService(t *testing.T) {
	root := cordis.Root()
	t.Cleanup(func() {
		if err := root.DisposeAll(); err != nil {
			t.Errorf("DisposeAll: %v", err)
		}
	})
	if err := Module().Factory(root, Config{}); err != nil {
		t.Fatalf("factory: %v", err)
	}
	service, ok := root.Get(contracts.KeyBrowser)
	if !ok || service == nil {
		t.Fatal("KeyBrowser was not provided")
	}
	status, err := service.Status(context.Background())
	if err != nil || status.Connected {
		t.Fatalf("status = (%+v, %v), want disconnected without an error", status, err)
	}
	if _, err := service.Open(context.Background(), "https://example.com/"); !errors.Is(err, contracts.ErrBrowserUnavailable) {
		t.Fatalf("Open without a browser = %v, want ErrBrowserUnavailable", err)
	}
}
