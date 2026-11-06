package assembly

import (
	"strings"

	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// BuildInfo is the build identity a release binary injects at link time
// (-ldflags -X). It is overlaid onto the appinfo module's entry config when the
// assembly loads, so a product's embedded JSON stays version-agnostic while the
// running binary reports its real identity (About page, diagnostics, --version).
type BuildInfo struct {
	Version   string
	Commit    string
	BuildTime string
}

// IsZero reports whether the info carries no build identity.
func (b BuildInfo) IsZero() bool {
	return strings.TrimSpace(b.Version) == "" &&
		strings.TrimSpace(b.Commit) == "" &&
		strings.TrimSpace(b.BuildTime) == ""
}

// overlayBuildInfo merges info into every enabled entry of the module that
// provides the AppInfo contract. Config keys info does not set are kept (the
// product name, for example).
func overlayBuildInfo(registry *module.Registry, entries []module.Entry, info BuildInfo) {
	if info.IsZero() {
		return
	}
	moduleID := appInfoModuleID(registry)
	if moduleID == "" {
		return
	}
	var walk func([]module.Entry)
	walk = func(list []module.Entry) {
		for index := range list {
			entry := &list[index]
			if entry.Module == moduleID && !entry.Disabled {
				config, ok := entry.Config.(map[string]any)
				if !ok || config == nil {
					config = make(map[string]any, 3)
				}
				setIfNotEmpty(config, "version", info.Version)
				setIfNotEmpty(config, "commit", info.Commit)
				setIfNotEmpty(config, "buildTime", info.BuildTime)
				entry.Config = config
			}
			walk(entry.Children)
		}
	}
	walk(entries)
}

// appInfoModuleID finds the registered module that provides the AppInfo
// contract, so build-info injection never hard-codes the appinfo module ID.
func appInfoModuleID(registry *module.Registry) string {
	want := contracts.KeyAppInfo.Name()
	for _, info := range registry.Modules() {
		for _, provided := range info.Provides {
			if provided.Name == want {
				return info.ID
			}
		}
	}
	return ""
}

func setIfNotEmpty(config map[string]any, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		config[key] = value
	}
}
