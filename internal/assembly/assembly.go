// Package assembly is ZeoKeel's assembly root.
//
// It holds the explicit compile-time module registry (registry.go), the
// embedded product assemblies (assemblies/*.json), and the pre-flight/loading
// entry points the process host calls. There is no init()-time registration and
// no runtime code loading: every module is linked into the binary, and an
// assembly only selects and configures the linked modules.
//
// A product is one JSON entry tree, read through module.ParseConfig (strict
// JSON: unknown fields fail loudly). Pre-flight validation runs before any
// fiber is created, so a broken assembly fails with a complete diagnostic
// instead of half-starting the UI.
package assembly

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Product names shipped in the binary.
const (
	// ProductDesktop is the windowed product: the UI group and its modules are
	// enabled, so the host also provides the uiroot services they require.
	ProductDesktop = "desktop"
	// ProductHeadless runs the same registry without UI modules: no window, no
	// uiroot and therefore no display environment is required.
	ProductHeadless = "headless"
)

//go:embed assemblies/*.json
var assemblyFS embed.FS

const (
	assemblyDir = "assemblies"
	assemblyExt = ".json"
)

// Products lists the embedded product names in ascending order.
func Products() []string {
	entries, err := fs.ReadDir(assemblyFS, assemblyDir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), assemblyExt) {
			continue
		}
		names = append(names, strings.TrimSuffix(entry.Name(), assemblyExt))
	}
	sort.Strings(names)
	return names
}

// Entries parses the embedded assembly of one product into a module entry
// tree. The document format is module.FileConfig
// ({"version":1,"entries":[...]}); module.ParseConfig also accepts a bare
// array.
func Entries(product string) ([]module.Entry, error) {
	name, err := normalizeProduct(product)
	if err != nil {
		return nil, err
	}
	data, err := assemblyFS.ReadFile(assemblyDir + "/" + name + assemblyExt)
	if err != nil {
		return nil, fmt.Errorf("assembly: read embedded product %q: %w", name, err)
	}
	entries, err := module.ParseConfig(data)
	if err != nil {
		return nil, fmt.Errorf("assembly: product %q: %w", name, err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("assembly: product %q has no entries", name)
	}
	return entries, nil
}

// Validate runs pre-flight validation for one product: unknown modules,
// duplicate instance IDs, missing or type-mismatched providers, duplicate
// providers, dependency cycles and rejected configs are all reported before any
// fiber is created.
func Validate(product string) error {
	entries, err := Entries(product)
	if err != nil {
		return err
	}
	return ValidateEntries(entries)
}

// ValidateEntries is Validate for an in-memory entry tree. It is the seam tests
// and tooling use to preflight an assembly that is not embedded yet.
func ValidateEntries(entries []module.Entry) error {
	_, err := registryFor(entries).Validate(entries)
	return err
}

// Load validates one product and starts its module tree on ctx.
//
// It must run on the UI thread: UI modules register their contributions while
// activating, so the host calls it from mygo's App.WhenReady callback. Loading
// is synchronous; when it returns, every required module is settled (Active or
// an error that already rolled the tree back).
func Load(ctx cordis.Context, product string) (*module.Loader, error) {
	return LoadWithBuildInfo(ctx, product, BuildInfo{})
}

// LoadWithBuildInfo is Load with the running binary's build identity overlaid
// onto the appinfo module's entry config (see buildinfo.go). The process host
// passes its ldflags identity here; tests may pass an empty BuildInfo.
func LoadWithBuildInfo(ctx cordis.Context, product string, info BuildInfo) (*module.Loader, error) {
	entries, err := Entries(product)
	if err != nil {
		return nil, err
	}
	registry := registryFor(entries)
	overlayBuildInfo(registry, entries, info)
	return module.Load(ctx, registry, entries)
}

// registryFor builds the registry one product needs: the compile-time modules
// plus the host services that product's assembly actually uses. UI services are
// only declared when an enabled entry requires one, because a headless host
// does not provide them (declaring them unconditionally would make the
// loader's builtin check reject every headless product).
func registryFor(entries []module.Entry) *module.Registry {
	registry := Registry()
	if usesUIServices(registry, entries) {
		declareUIServices(registry)
	}
	return registry
}

// declareUIServices declares the services internal/uiroot provides on the host
// root context.
func declareUIServices(registry *module.Registry) {
	registry.AddBuiltin(contracts.KeyUIRuntime)
	registry.AddBuiltin(contracts.KeyNavigator)
	registry.AddBuiltin(contracts.KeyTheme)
	registry.AddBuiltin(contracts.KeyLocale)
	registry.AddBuiltin(contracts.KeyScreenLister)
}

// uiServiceNames is the set of service names the UI host provides.
var uiServiceNames = map[string]bool{
	contracts.KeyUIRuntime.Name():    true,
	contracts.KeyNavigator.Name():    true,
	contracts.KeyTheme.Name():        true,
	contracts.KeyLocale.Name():       true,
	contracts.KeyScreenLister.Name(): true,
}

// usesUIServices reports whether any enabled entry's module requires a UI host
// service. Disabled entries are skipped: pre-flight skips them too, and a
// product must not be forced to provide UI services it never activates.
func usesUIServices(registry *module.Registry, entries []module.Entry) bool {
	var walk func([]module.Entry) bool
	walk = func(list []module.Entry) bool {
		for _, entry := range list {
			if entry.Disabled {
				continue
			}
			if info, ok := registry.Module(entry.Module); ok {
				for _, requirement := range info.Requires {
					if uiServiceNames[requirement.Name] {
						return true
					}
				}
			}
			if walk(entry.Children) {
				return true
			}
		}
		return false
	}
	return walk(entries)
}

// normalizeProduct accepts a product name or its file name ("desktop" or
// "desktop.json") and verifies it is embedded.
func normalizeProduct(product string) (string, error) {
	name := strings.TrimSpace(product)
	name = strings.TrimSpace(strings.TrimSuffix(name, assemblyExt))
	known := Products()
	if name == "" {
		return "", fmt.Errorf("assembly: product is required (known: %s)", strings.Join(known, ", "))
	}
	for _, candidate := range known {
		if name == candidate {
			return name, nil
		}
	}
	return "", fmt.Errorf("assembly: unknown product %q (known: %s)", name, strings.Join(known, ", "))
}
