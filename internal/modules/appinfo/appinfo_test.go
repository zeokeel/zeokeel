package appinfo

// AR-21 (2026-11 audit): appinfo had no tests. These pin the module identity,
// the Provide of contracts.KeyAppInfo on a real kernel root, and the config
// fallbacks every UI screen reads (name/version/commit/buildTime).

import (
	goruntime "runtime"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestDescriptorMetadata(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != "zeokeel/appinfo" {
		t.Fatalf("module ID = %q, want zeokeel/appinfo", descriptor.ID)
	}
	if descriptor.Name == "" {
		t.Fatal("module has no display name")
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyAppInfo.Name() {
		t.Fatalf("provides = %+v, want the appinfo key", descriptor.Provides)
	}
	if len(descriptor.Requires) != 0 {
		t.Fatalf("requires = %+v, want none (appinfo is a leaf provider)", descriptor.Requires)
	}
}

// loadAppInfo activates the module on a fresh kernel root with the given
// config and returns the provided build identity.
func loadAppInfo(t *testing.T, config map[string]any) contracts.AppInfo {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	entry := module.Entry{ID: "appinfo", Module: "zeokeel/appinfo"}
	if config != nil {
		entry.Config = config
	}
	loader, err := module.Load(root, registry, []module.Entry{entry})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	info, err := root.Require(contracts.KeyAppInfo)
	if err != nil {
		t.Fatalf("require KeyAppInfo: %v", err)
	}
	return info
}

func TestModuleActivationProvidesConfiguredIdentity(t *testing.T) {
	info := loadAppInfo(t, map[string]any{
		"name": "ZeoKeel Test", "version": "v9.9.9", "commit": "abc123", "buildTime": "2026-11-17T00:00:00Z",
	})
	if info.Name != "ZeoKeel Test" || info.Version != "v9.9.9" || info.Commit != "abc123" || info.BuildTime != "2026-11-17T00:00:00Z" {
		t.Fatalf("AppInfo = %+v, want the configured identity", info)
	}
	if info.GoVersion != goruntime.Version() {
		t.Fatalf("GoVersion = %q, want the toolchain version %q", info.GoVersion, goruntime.Version())
	}
}

func TestModuleActivationDefaults(t *testing.T) {
	info := loadAppInfo(t, nil)
	if info.Name != "ZeoKeel" {
		t.Fatalf("default Name = %q, want ZeoKeel", info.Name)
	}
	if info.Version != "dev" {
		t.Fatalf("default Version = %q, want dev", info.Version)
	}
	if info.Commit != "unknown" || info.BuildTime != "unknown" {
		t.Fatalf("default Commit/BuildTime = %q/%q, want unknown/unknown", info.Commit, info.BuildTime)
	}
	if info.GoVersion != goruntime.Version() {
		t.Fatalf("GoVersion = %q, want the toolchain version %q", info.GoVersion, goruntime.Version())
	}
}

func TestModuleActivationPartialConfigKeepsOtherDefaults(t *testing.T) {
	info := loadAppInfo(t, map[string]any{"version": "v0.2.48"})
	if info.Version != "v0.2.48" {
		t.Fatalf("Version = %q, want the configured v0.2.48", info.Version)
	}
	if info.Name != "ZeoKeel" || info.Commit != "unknown" || info.BuildTime != "unknown" {
		t.Fatalf("AppInfo = %+v, want the remaining defaults", info)
	}
}
