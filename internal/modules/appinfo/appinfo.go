// Package appinfo is ZeoKeel's build-identity module. It is a product module:
// it is registered in the product's compile-time registry and configured by the
// product's assembly. The SDK (github.com/zeokeel/zeokeel-rig) deliberately does
// not provide it — assembly belongs to the process, not to the toolset.
package appinfo

import (
	goruntime "runtime"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/lib/textutil"
)

// Config is the module's typed assembly config (assembly.json).
type Config struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"buildTime"`
}

// Module is the compile-time registration of the appinfo module: it provides
// the build identity every UI screen may read.
func Module() *module.Descriptor[Config] {
	return module.Define("zeokeel/appinfo", func(ctx cordis.Context, cfg Config) error {
		info := contracts.AppInfo{
			Name:      textutil.FirstNonEmpty(cfg.Name, "ZeoKeel"),
			Version:   textutil.FirstNonEmpty(cfg.Version, "dev"),
			Commit:    textutil.FirstNonEmpty(cfg.Commit, "unknown"),
			BuildTime: textutil.FirstNonEmpty(cfg.BuildTime, "unknown"),
			GoVersion: goruntime.Version(),
		}
		_, err := ctx.Provide(contracts.KeyAppInfo, info)
		return err
	}).AddProvides(module.Provides(contracts.KeyAppInfo)).WithName("ZeoKeel app info")
}
