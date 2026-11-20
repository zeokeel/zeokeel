package toolkit

// Opt-in hidden-paths settings wiring (toolkit.deniedGlobs).
//
// Disclosure: hidden paths hide files from the FILE tools only (read_file,
// glob, grep, list_dir, editor, image, LSP path checks). The shell bypass
// remains — bash/pwsh can still `cat` a hidden file, and the command allowlist
// is advisory — so this key is defense in depth for the model's file surface,
// NOT a security boundary. The default is empty (opt-in): personal-scale use
// gets no friction until the user asks for it.
//
// This file resolves WHICH patterns apply (settings row over assembly config)
// and widens them for the SDK. Enforcement itself is rig's: v0.25.0 introduced
// sandbox.WithHidden, which participates in every path decision (read, write,
// list, glob/grep and search results). The product used to carry its own gate
// because v0.24.0 had downgraded the old "denied globs" to record-only; that
// gate is gone and hiddenPatterns below is all that is left to preserve the
// setting's "this path is invisible, subtree included" promise.

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

// hiddenPatterns widens the configured globs for sandbox.WithHidden.
//
// rig's hidden matcher compares a pattern against ONE root-relative path, so a
// bare "**/.git" hides the directory entry but not ".git/config", and
// "secrets/**" hides the contents but not the "secrets" entry. The product's
// setting has always meant "this path is invisible to the model, subtree
// included" (the pre-v0.25 denied-globs rule refused a matching directory's
// children as well), so each entry is widened here:
//
//	**/.env     -> **/.env, **/.env/**          (dir entry + subtree)
//	secrets/**  -> secrets, secrets/**          (dir entry + subtree)
//	secrets/    -> secrets, secrets/**          (trailing slash = directory)
//	id_rsa      -> id_rsa, **/id_rsa/**         (no "/" = basename at any depth)
//
// Matching stays rig's: "**" spans segments, "*"/"?" stay inside one segment,
// a pattern without "/" also compares against the basename. Blank entries are
// dropped and duplicates are removed; an empty result keeps WithHidden a no-op.
func hiddenPatterns(globs []string) []string {
	if len(globs) == 0 {
		return nil
	}
	out := make([]string, 0, len(globs)*2)
	seen := make(map[string]bool, len(globs)*2)
	add := func(pattern string) {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || seen[pattern] {
			return
		}
		seen[pattern] = true
		out = append(out, pattern)
	}
	for _, glob := range globs {
		trimmed := strings.ReplaceAll(strings.TrimSpace(glob), "\\", "/")
		if trimmed == "" {
			continue
		}
		if strings.HasSuffix(trimmed, "/") {
			// "dir/" names a directory: hide the entry and everything below it.
			directory := strings.TrimSuffix(trimmed, "/")
			add(directory)
			add(directory + "/**")
			continue
		}
		if directory, ok := strings.CutSuffix(trimmed, "/**"); ok {
			// "dir/**" already covers the contents; add the directory entry too.
			add(directory)
			add(directory + "/**")
			continue
		}
		add(trimmed)
		if strings.Contains(trimmed, "/") {
			add(trimmed + "/**")
			continue
		}
		add("**/" + trimmed + "/**")
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
