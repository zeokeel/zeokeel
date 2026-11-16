package toolkit

// Opt-in denied-globs settings wiring (2026-11 audit).
//
// Disclosure: denied globs hide files from the FILE tools only (read_file,
// glob, grep, editor, image, LSP path checks). The shell bypass remains —
// bash/pwsh can still `cat` a denied file, and the command allowlist is
// advisory — so this key is defense in depth for the model's file surface,
// NOT a security boundary. The default is empty (opt-in): personal-scale use
// gets no friction until the user asks for it.

import (
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// The settings row is contracts.SettingToolkitDeniedGlobs (a JSON string list,
// e.g. ["**/.env","**/*.pem","**/*.key"]) that overrides the assembly's
// Config.DeniedGlobs. Absent/blank keeps the assembly value; an explicit empty
// list opts back out. Resolved once at activation like the other toolkit.*
// capability rows (a restart applies a change).

// resolveDeniedGlobs merges the opt-in settings row over the assembly config.
// A malformed row is logged and keeps the assembly value instead of failing
// activation (the same degradation rule as the disabled-tools lists).
func resolveDeniedGlobs(cfg Config, settings contracts.SettingsService, hasSettings bool, logf func(string, ...any)) []string {
	if !hasSettings || settings == nil {
		return cfg.DeniedGlobs
	}
	raw := strings.TrimSpace(settings.Get(contracts.SettingToolkitDeniedGlobs, ""))
	if raw == "" {
		return cfg.DeniedGlobs
	}
	globs, err := contracts.ParseStringListJSON(raw)
	if err != nil {
		if logf != nil {
			logf("[toolkit] parse %s: %v", contracts.SettingToolkitDeniedGlobs, err)
		}
		return cfg.DeniedGlobs
	}
	kept := make([]string, 0, len(globs))
	for _, glob := range globs {
		if trimmed := strings.TrimSpace(glob); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	return kept
}
