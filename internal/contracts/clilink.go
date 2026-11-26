package contracts

// External-CLI links: the contract for "a vendor CLI exists on this machine and
// the agent may run it with its ordinary shell tool".
//
// The model is DeepSeek Harness's dsh-dingtalk / dsh-feishu plugins: those are
// NOT MCP servers and NOT inbound channels. They locate a CLI, put its ABSOLUTE
// path in front of the model, index the vendor's skill bundle, and let the model
// drive the CLI through the shell tool. This file is the shared half of that
// model: resolution (which executable, from where), the state names, and the
// model-facing runtime-context text. The clilink module adds the version probe
// and the read model; the agent renders the context section. Both sides sit on
// this leaf surface so neither backend module has to import the other.
//
// Nothing here opens a socket, registers an endpoint, receives a message or
// sends one. A CLI link is a local executable plus a sentence about it.
//
// Resolution precedence (both questions have the same answer order):
//
//	1. the settings row cli.<id>.path — an absolute path the user pointed at;
//	2. PATH lookup of the binary name (dws / lark-cli);
//	3. not installed. That is a normal answer, not an error: the module reports
//	   it, the product boots anyway and the prompt says nothing.
//
// A resolved path is an EXECUTION decision, not just a display string: the
// module later runs it (one bounded `<path> --version`). So a path is accepted
// only when it is this CLI's own executable (CLIToolExecutableAllowed: the
// basename must be the CLI's binary name, the file must be regular, and on a
// Unix permission model it must carry an executable bit) and it contains no
// control characters. Anything else stays not-installed with the reason on the
// row, because the product must never run an arbitrary file just because a
// settings row named it.
//
// The model-facing text is a shell command the model will copy, so every path
// is shell-quoted (shellQuoteArg: single quotes, inner quotes escaped) and every
// settings-derived value is one-lined before it enters the prompt
// (sanitizePromptValue). An unquoted path with a space splits into two
// arguments, and one with a `;` or `$()` is an executable injection into a
// prompt whose shell tool is approval-free.
//
// The skill-bundle answer uses the SAME scope rule as the skills module: with a
// workspace open only that workspace's roots and its own
// skills.dirs.<workspaceID> row count; without one the global skills.dirs row
// (plus the data directory and the rig-compatible env/home roots) counts. A
// directory counts as the CLI's skill only when it holds a regular SKILL.md and
// its name carries a real skill name after the CLI's prefix — the vendored
// `lark-cli` source checkout is not a `lark-*` skill, and a directory with no
// SKILL.md is not one either.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	cordis "github.com/zeokeel/zeokeel-kernel"
	rigworkspace "github.com/zeokeel/zeokeel-rig/workspace"
)

// CLIToolState values: the answer to "can this run use the CLI at all".
const (
	// CLIToolReady means an executable was resolved (settings row or PATH) and
	// no probe has contradicted it.
	CLIToolReady = "ready"
	// CLIToolNotInstalled means no usable executable was found: no settings
	// row and nothing on PATH, or a settings row that points at nothing. Callers
	// must treat this as a normal state (no error, nothing to retry).
	CLIToolNotInstalled = "not-installed"
	// CLIToolUnusable means an executable WAS found but does not answer
	// `--version` (non-zero exit, timeout, or no output). The path is still
	// reported so a human can look at it.
	CLIToolUnusable = "unusable"
)

// CLIToolSource values: where the executable path came from.
const (
	// CLIToolSourceSettings is the cli.<id>.path settings row.
	CLIToolSourceSettings = "settings"
	// CLIToolSourcePath is a PATH lookup of the binary name.
	CLIToolSourcePath = "path"
)

// CLISkillsState values: the answer to "is the CLI's skill bundle indexed".
//
// "Indexed" is a factual claim about the CURRENT run's skill scope: the same
// roots the skills module scans for this run, holding directories the skill tool
// would actually list. It is never a claim about the machine in general — a
// bundle that only another workspace's skills.dirs row points at is not indexed
// here, because the skill tool could not load one of its skills in this run.
const (
	// CLISkillsIndexed means at least one in-scope skill root (see
	// CLISkillsState.Roots) holds skill directories for this CLI, so the skills
	// module already lists them for this run.
	CLISkillsIndexed = "indexed"
	// CLISkillsMissing means the user configured extra skill directories for
	// this scope but none of the existing roots holds a skill for this CLI
	// (wrong directory, or the bundle was never unpacked there).
	CLISkillsMissing = "missing"
	// CLISkillsUnconfigured means the effective scope has no user-configured
	// skill directory at all (no skills.dirs row for the open workspace, or the
	// global row when no workspace is open).
	CLISkillsUnconfigured = "unconfigured"
)

// CLIToolSpec is the static description of one linkable CLI. It is data, not
// code: the two entries below are the whole registry, and a third tool means one
// more entry plus its settings key.
type CLIToolSpec struct {
	// ID is the short stable name ("dingtalk"), also the cli.<id>.path segment.
	ID string
	// Title is the human/model-facing product name ("DingTalk").
	Title string
	// Binary is the executable name searched on PATH ("dws").
	Binary string
	// SettingKey is the absolute-path override row (contracts.SettingCLI*Path).
	SettingKey string
	// SkillPrefixes are the directory-name prefixes that identify this CLI's
	// skills in a skills.dirs root (the dws bundle unpacks dingtalk-<product>
	// directories).
	SkillPrefixes []string
	// SchemaCommand is the CLI's own command index, when it has one; it is
	// rendered into the context line so the model can explore the surface
	// instead of guessing (`dws schema <service>`). Empty means "--help only".
	SchemaCommand string
	// EnvPrefix is the environment assignment that suppresses a CLI's per-call
	// online update check, when it has one. The probe sets it, and the context
	// line tells the model to use it too.
	//
	// It is a courtesy flag for ONE vendor CLI, not a network policy: dws honours
	// DWS_NO_UPDATE_CHECK (without it every dws call spends ~0.7s checking for a
	// new release), while lark-cli does not know the variable at all. The module
	// therefore makes no promise that a probe stays offline — `--version` of an
	// arbitrary vendor CLI may do anything, and the probe's job is only to bound
	// it in time, memory and environment (see internal/modules/clilink).
	EnvPrefix string
}

// CLILinkTools returns the CLIs this product knows how to link. The order is
// stable (DingTalk first), so the prompt and the read model agree.
func CLILinkTools() []CLIToolSpec {
	return []CLIToolSpec{
		{
			ID:            "dingtalk",
			Title:         "DingTalk",
			Binary:        "dws",
			SettingKey:    SettingCLIDingtalkPath,
			SkillPrefixes: []string{"dingtalk-"},
			SchemaCommand: "schema",
			EnvPrefix:     "DWS_NO_UPDATE_CHECK=1",
		},
		{
			ID:            "feishu",
			Title:         "Feishu",
			Binary:        "lark-cli",
			SettingKey:    SettingCLIFeishuPath,
			SkillPrefixes: []string{"lark-", "feishu-"},
		},
	}
}

// CLISkillsState is the per-CLI answer to "is the skill bundle indexed".
type CLISkillsState struct {
	// State is CLISkillsIndexed / CLISkillsMissing / CLISkillsUnconfigured.
	State string
	// Roots are the configured directories that actually hold this CLI's
	// skills (absolute, deduplicated). Empty unless State is indexed.
	Roots []string
	// Count is how many skill directories matched across Roots.
	Count int
}

// CLIToolStatus is the resolved, readable state of one CLI link. It is the
// read model the clilink module publishes; the agent uses the same struct to
// render its context line, so "what the user sees" and "what the model is told"
// cannot drift.
type CLIToolStatus struct {
	// ID / Title / Binary mirror CLIToolSpec.
	ID     string
	Title  string
	Binary string
	// State is CLIToolReady / CLIToolNotInstalled / CLIToolUnusable.
	State string
	// Source is where Path came from (empty when no path was resolved).
	Source string
	// Path is the ABSOLUTE executable path; empty when nothing was resolved.
	// The model-facing line always uses this, never the bare binary name. A path
	// that is set here passed CLIToolExecutableAllowed, so it is safe to execute
	// as this CLI (the module re-checks right before exec anyway).
	Path string
	// ConfiguredPath is the raw cli.<id>.path row when one is set, so a row that
	// points at nothing (or at the wrong file) can be reported instead of
	// silently ignored.
	ConfiguredPath string
	// Version is the first line of `<path> --version` (empty until probed, and
	// for a CLI that answers nothing).
	Version string
	// Error is the human-readable reason a configured or discovered CLI is not
	// ready (missing file, probe timeout, non-zero exit). Empty when ready.
	Error string
	// Skills reports whether the CLI's skill bundle is indexed.
	Skills CLISkillsState
}

// Found reports whether a usable executable path was resolved.
func (s CLIToolStatus) Found() bool { return strings.TrimSpace(s.Path) != "" }

// CLILinkService is the external-CLI read model published by the clilink module
// (contracts.KeyCLILink). It is read-only by design: nothing is installed,
// downloaded or configured through it.
//
// Two honest limits, so nobody builds on a promise the wiring does not make:
//
//  1. There is currently NO consumer of this service in the product: the prompt
//     does not read it (the agent re-resolves the rows itself on every run, see
//     ConfiguredCLITools), the settings UI reads the settings rows, and no other
//     module Requires KeyCLILink. It exists for the assembled product to inspect
//     (and for tests); treat "nobody uses it" as the current state, not as a
//     guarantee that the snapshot is fresh.
//  2. Refresh is NOT subscribed to settings changes. The cached snapshot is
//     filled at activation and replaced only by an explicit Refresh call. So
//     "editing cli.<id>.path applies without a restart" is true for the PROMPT
//     (re-resolved each run) and false for this cached read model.
type CLILinkService interface {
	// Status returns the cached snapshot: resolved paths, probed versions and
	// skill-directory state, one row per CLILinkTools entry in that order.
	Status() []CLIToolStatus
	// Refresh re-resolves and re-probes the CLIs (the settings rows may have
	// changed since activation) and replaces the cache. It never returns an
	// error: an unusable CLI is reported in its row.
	Refresh(ctx context.Context) []CLIToolStatus
}

// KeyCLILink is provided by the clilink backend module.
var KeyCLILink = cordis.NewServiceKey[CLILinkService]("zeokeel/clilink")

// CLILinkOptions injects the lookups, the skill-directory source and the
// workspace scope, so the resolution is fully testable without a dev machine
// that happens to have dws installed. A nil field uses the real operating
// system.
type CLILinkOptions struct {
	// LookPath resolves a bare binary name (nil = exec.LookPath).
	LookPath func(string) (string, error)
	// Stat reports one path's file info (nil = os.Stat).
	Stat func(string) (os.FileInfo, error)
	// ReadDir lists one skill root (nil = os.ReadDir).
	ReadDir func(string) ([]os.DirEntry, error)
	// ReadFile reads one registry file (nil = os.ReadFile). It backs the
	// workspace-scope lookup when Workspace is nil.
	ReadFile func(string) ([]byte, error)
	// SkillDirs returns the configured extra skill directories (nil reads the
	// settings row in scope). Tests use it to point at a t.TempDir bundle.
	SkillDirs func(settings SettingsService) []string
	// Workspace reports the open workspace: its ID and root, ok=false when none
	// is open. nil derives it from the workspace registry the workspace module
	// persists under dataDir (currentWorkspaceScope), which is the production
	// path for callers that have only settings and a data directory.
	Workspace func() (id, root string, ok bool)
}

// ResolveCLITools resolves every known CLI with the full precedence: the
// settings row first, then PATH. This is the read model's resolution.
func ResolveCLITools(settings SettingsService, dataDir string, opts CLILinkOptions) []CLIToolStatus {
	specs := CLILinkTools()
	statuses := make([]CLIToolStatus, 0, len(specs))
	// The workspace scope is resolved once: both CLIs must describe the same
	// run, and the registry file must not be read twice.
	scope := currentWorkspaceScope(dataDir, opts)
	for _, spec := range specs {
		statuses = append(statuses, resolveCLITool(spec, settings, dataDir, scope, true, opts))
	}
	return statuses
}

// ConfiguredCLITools resolves only the cli.<id>.path rows; it never looks at
// PATH. This is the prompt path: the rendered system prompt must not change
// because a developer's machine happens to have dws on PATH (the prompt is
// byte-compared in tests and a user-facing product should describe what the
// user configured, not what leaked into the process environment).
func ConfiguredCLITools(settings SettingsService, dataDir string, opts CLILinkOptions) []CLIToolStatus {
	specs := CLILinkTools()
	statuses := make([]CLIToolStatus, 0, len(specs))
	scope := currentWorkspaceScope(dataDir, opts)
	for _, spec := range specs {
		statuses = append(statuses, resolveCLITool(spec, settings, dataDir, scope, false, opts))
	}
	return statuses
}

// resolveCLITool resolves one CLI. withPath enables the PATH fallback.
func resolveCLITool(spec CLIToolSpec, settings SettingsService, dataDir string, scope workspaceScope, withPath bool, opts CLILinkOptions) CLIToolStatus {
	status := CLIToolStatus{
		ID:     spec.ID,
		Title:  spec.Title,
		Binary: spec.Binary,
		State:  CLIToolNotInstalled,
		Skills: resolveCLISkills(spec, settings, dataDir, scope, opts),
	}
	configured := ""
	if settings != nil {
		configured = strings.TrimSpace(settings.Get(spec.SettingKey, ""))
	}
	if configured != "" {
		status.ConfiguredPath = resolveAgainst(configured, dataDir)
		if reason, bad := cliPathProblem(spec, status.ConfiguredPath, opts); bad {
			// A row that points at the wrong thing is reported, not ignored and
			// never executed: silently falling back to PATH would hide a typo,
			// and running whatever the row names is exactly the hole this check
			// closes.
			status.Source = ""
			status.Error = spec.SettingKey + " points at " + status.ConfiguredPath + ", but " + reason
			return status
		}
		status.Source = CLIToolSourceSettings
		status.Path = status.ConfiguredPath
		status.State = CLIToolReady
		return status
	}
	if !withPath {
		return status
	}
	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	found, err := lookPath(spec.Binary)
	if err != nil || strings.TrimSpace(found) == "" {
		return status
	}
	absolute, absErr := filepath.Abs(found)
	if absErr != nil {
		absolute = found
	}
	status.Path = filepath.Clean(absolute)
	// exec.LookPath already guarantees "executable file named spec.Binary";
	// re-running the shared guard keeps one rule for both sources (and catches
	// a lookPath seam that returns something else).
	info, statErr := statPath(opts, status.Path)
	if statErr != nil {
		status.Path = ""
		return status
	}
	if reason, allowed := CLIToolExecutableAllowed(spec, status.Path, info); !allowed {
		status.Error = status.Path + " is not usable: " + reason
		status.Path = ""
		return status
	}
	status.Source = CLIToolSourcePath
	status.State = CLIToolReady
	return status
}

// CLILinkContextSection renders the model-facing runtime-context section: the
// absolute path of every CLI this run may use, how to explore its command
// surface, and where its skill bundle is indexed. It returns "" when there is
// nothing to say (the common case: no CLI configured), so the prompt stays
// unchanged for products that never link one.
//
// A configured-but-missing CLI still produces a line: the model should tell the
// user the CLI is not installed instead of inventing commands for it.
func CLILinkContextSection(statuses []CLIToolStatus) string {
	lines := CLILinkContextLines(statuses)
	if len(lines) == 0 {
		return ""
	}
	return "## External CLIs\n" + strings.Join(lines, "\n")
}

// CLILinkContextLines is CLILinkContextSection without the heading: one line
// per CLI the model should know about. Every emitted line is single-line and
// free of control characters (sanitizePromptValue): a settings value with a
// newline must not be able to append what looks like a new instruction, and the
// section is joined with "\n" by CLILinkContextSection.
func CLILinkContextLines(statuses []CLIToolStatus) []string {
	specs := make(map[string]CLIToolSpec, len(CLILinkTools()))
	for _, spec := range CLILinkTools() {
		specs[spec.ID] = spec
	}
	lines := make([]string, 0, len(statuses))
	for _, status := range statuses {
		spec := specs[status.ID]
		switch {
		case status.Found():
			lines = append(lines, sanitizePromptValue(cliReadyLine(spec, status), maxPromptLineRunes))
		case strings.TrimSpace(status.ConfiguredPath) != "":
			lines = append(lines, sanitizePromptValue("- "+status.Title+" CLI ("+status.Binary+"): "+spec.SettingKey+
				" points at "+shellQuoteArg(status.ConfiguredPath)+", which is not there. Tell the user it needs installing or fixing; do not guess its commands.", maxPromptLineRunes))
		}
	}
	return lines
}

// cliReadyLine renders one ready CLI's sentence.
//
// The path is shell-quoted everywhere it appears: this text is copied into a
// shell tool that does not stop for approval, so `'` quoting (and the escaped
// inner quote) is what keeps a path with a space, a `;` or a `$()` one argument
// instead of an extra command.
func cliReadyLine(spec CLIToolSpec, status CLIToolStatus) string {
	path := shellQuoteArg(status.Path)
	var line strings.Builder
	line.WriteString("- ")
	line.WriteString(status.Title)
	line.WriteString(" CLI (")
	line.WriteString(status.Binary)
	line.WriteString("): ")
	line.WriteString(path)
	if status.Source == CLIToolSourceSettings {
		line.WriteString(" (from " + spec.SettingKey + ")")
	}
	if status.Version != "" {
		line.WriteString(", " + status.Version)
	}
	line.WriteString(". Run it with the shell tool using that absolute path; do not rely on PATH and do not guess its commands: ")
	explore := []string{"`" + path + " --help` lists its commands"}
	if spec.SchemaCommand != "" {
		explore = append(explore, "`"+path+" "+spec.SchemaCommand+" <service>` lists one service's commands")
	}
	explore = append(explore, "`"+path+" <service> --help` shows that command's flags and examples")
	line.WriteString(strings.Join(explore, ", "))
	line.WriteString(".")
	if spec.EnvPrefix != "" {
		line.WriteString(" Prefix shell calls with `" + spec.EnvPrefix + "` to skip its per-call online update check.")
	}
	switch status.Skills.State {
	case CLISkillsIndexed:
		quoted := make([]string, 0, len(status.Skills.Roots))
		for _, root := range status.Skills.Roots {
			quoted = append(quoted, shellQuoteArg(root))
		}
		line.WriteString(" Its skill bundle is indexed from ")
		line.WriteString(strings.Join(quoted, ", "))
		line.WriteString(" (")
		line.WriteString(strconv.Itoa(status.Skills.Count))
		line.WriteString(" skills): load one with the skill tool before driving the CLI by hand.")
	case CLISkillsMissing:
		line.WriteString(" No " + strings.Join(spec.SkillPrefixes, "/") + "* skill is indexed: the skill roots this run scans do not hold its bundle, so read the CLI's own help instead of assuming the skills exist.")
	default:
		line.WriteString(" Its skill bundle is not indexed: the user can point skills.dirs at the CLI's skills directory, and until then read the CLI's own help.")
	}
	return line.String()
}

// resolveCLISkills reports whether an in-scope skill root holds this CLI's
// skills. The scope is the skills module's scope (see cliSkillRoots); the
// skills module still owns discovery, this only answers "would the skill tool
// list one of them in this run".
func resolveCLISkills(spec CLIToolSpec, settings SettingsService, dataDir string, scope workspaceScope, opts CLILinkOptions) CLISkillsState {
	roots, extraConfigured := cliSkillRoots(settings, dataDir, scope, opts)
	state := CLISkillsState{State: CLISkillsUnconfigured}
	if extraConfigured {
		// The user pointed somewhere; "missing" is the honest answer until one
		// of those roots actually holds the bundle.
		state.State = CLISkillsMissing
	}
	for _, root := range roots {
		entries, err := readDirPath(opts, root)
		if err != nil {
			continue
		}
		matched := 0
		for _, entry := range entries {
			if entry == nil || !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !isCLISkillName(name, spec) {
				continue
			}
			if !hasSkillDocument(opts, filepath.Join(root, name)) {
				continue
			}
			matched++
		}
		if matched == 0 {
			continue
		}
		state.State = CLISkillsIndexed
		state.Roots = append(state.Roots, root)
		state.Count += matched
	}
	sort.Strings(state.Roots)
	return state
}

// hasSkillDocument reports whether one directory is a skill: it must hold a
// regular SKILL.md. This is the same test the skills module (rig store) applies,
// so clilink never counts a directory the skill tool would skip — a vendored
// `lark-cli` checkout or an old backup directory is not a skill.
func hasSkillDocument(opts CLILinkOptions, dir string) bool {
	info, err := statPath(opts, filepath.Join(dir, "SKILL.md"))
	return err == nil && info != nil && info.Mode().IsRegular()
}

// isCLISkillName reports whether a directory name is plausibly one of this
// CLI's skills: one of its prefixes followed by a non-empty name, and not the
// CLI's own binary name (the `lark-cli` source checkout sits right next to the
// `lark-*` skills and must not be counted as one).
func isCLISkillName(name string, spec CLIToolSpec) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if strings.EqualFold(name, strings.TrimSpace(spec.Binary)) {
		return false
	}
	for _, prefix := range spec.SkillPrefixes {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" || !strings.HasPrefix(name, prefix) {
			continue
		}
		if strings.TrimSpace(strings.TrimPrefix(name, prefix)) == "" {
			// "dingtalk-" alone names no skill.
			continue
		}
		return true
	}
	return false
}

// cliWorkspaceSkillDirs mirrors the skills module's workspace roots
// (skills.WorkspaceDirName plus the .agents/.zeokeel conventions of the two
// skill ecosystems). The list is duplicated here because contracts is a leaf
// package that the skills module imports — the dependency cannot be reversed, so
// keep the two lists in step (they are pinned by a test in both packages).
var cliWorkspaceSkillDirs = []string{
	"skills",
	filepath.Join(".agents", "skills"),
	filepath.Join(".zeokeel", "skills"),
}

// workspaceScope is the skill scope of the current run: the open workspace, when
// there is one, and nothing else.
type workspaceScope struct {
	ID   string
	Root string
	Open bool
}

// currentWorkspaceScope resolves the open workspace, following the skills
// module's rule that an open workspace with a root is what scopes discovery.
// CLILinkOptions.Workspace wins when injected; otherwise the workspace registry
// the workspace module persists (rigworkspace.DefaultFileName under the data
// directory) is read, so callers that only have settings + a data directory
// (the prompt render) still see the same scope as the skills module.
func currentWorkspaceScope(dataDir string, opts CLILinkOptions) workspaceScope {
	if opts.Workspace != nil {
		id, root, ok := opts.Workspace()
		if !ok {
			return workspaceScope{}
		}
		return workspaceScope{ID: strings.TrimSpace(id), Root: strings.TrimSpace(root), Open: true}
	}
	return registryWorkspaceScope(dataDir, opts)
}

// registryWorkspaceScope reads the open workspace out of the workspace registry
// file (best effort, read-only, bounded). A missing, unreadable, malformed or
// foreign registry means "no workspace open", which is the global scope — the
// same answer the skills module gives when its workspace service has no current
// entry. A host that configured a non-default registry path is the one case this
// fallback cannot see; such a host loses the workspace scope here (it then
// reports the global scope, never another workspace's skills).
func registryWorkspaceScope(dataDir string, opts CLILinkOptions) workspaceScope {
	dir := strings.TrimSpace(dataDir)
	if dir == "" {
		return workspaceScope{}
	}
	raw, err := readPath(opts, filepath.Join(dir, rigworkspace.DefaultFileName))
	if err != nil || len(raw) == 0 {
		return workspaceScope{}
	}
	var document struct {
		Workspaces []struct {
			ID   string `json:"id"`
			Root string `json:"root"`
		} `json:"workspaces"`
		CurrentID string `json:"currentId"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return workspaceScope{}
	}
	current := strings.TrimSpace(document.CurrentID)
	if current == "" {
		return workspaceScope{}
	}
	for _, record := range document.Workspaces {
		if strings.TrimSpace(record.ID) != current {
			continue
		}
		return workspaceScope{ID: current, Root: strings.TrimSpace(record.Root), Open: true}
	}
	return workspaceScope{}
}

// cliSkillRoots returns the skill roots this run scans, in precedence order,
// together with whether the user configured any extra directory in this scope.
//
// The rule is the skills module's (S-02/S-03), because "indexed" must mean "the
// skill tool lists it in this run":
//
//   - workspace open: only <root>/skills, <root>/.agents/skills,
//     <root>/.zeokeel/skills and the scoped skills.dirs.<workspaceID> row. A
//     global row and another workspace's row are NOT in scope.
//   - no workspace: the global skills.dirs row, <dataDir>/skills, the
//     $ZEOKEEL_SKILLS roots and ~/.zeokeel/skills.
//
// Assembly-configured directories (the skills module's own Config.Dirs) are not
// visible from this leaf package, so they are the one intentional difference;
// they are build-time additions, not user configuration.
func cliSkillRoots(settings SettingsService, dataDir string, scope workspaceScope, opts CLILinkOptions) (roots []string, extraConfigured bool) {
	// A workspace with no root is "no workspace open" for the skills module too,
	// so the GLOBAL row is the extra-directory source in that case.
	workspaceOpen := scope.Open && scope.Root != ""
	key := SkillsDirsSettingKey("")
	if workspaceOpen {
		key = SkillsDirsSettingKey(scope.ID)
	}
	extra := extraSkillDirs(settings, dataDir, opts, key)
	extraConfigured = len(extra) > 0
	candidates := make([]string, 0, len(extra)+6)
	if workspaceOpen {
		for _, name := range cliWorkspaceSkillDirs {
			candidates = append(candidates, filepath.Join(scope.Root, name))
		}
		candidates = append(candidates, extra...)
		return dedupePaths(candidates), extraConfigured
	}
	candidates = append(candidates, extra...)
	if dir := strings.TrimSpace(dataDir); dir != "" {
		candidates = append(candidates, filepath.Join(dir, "skills"))
	}
	for _, dir := range envSkillRoots() {
		candidates = append(candidates, dir)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".zeokeel", "skills"))
	}
	return dedupePaths(candidates), extraConfigured
}

// extraSkillDirs reads ONE skills.dirs row (the scoped key for the open
// workspace, the global key otherwise), resolving relative values against
// dataDir exactly like the skills module's userDirs. Duplicates and empties are
// dropped.
func extraSkillDirs(settings SettingsService, dataDir string, opts CLILinkOptions, key string) []string {
	if opts.SkillDirs != nil {
		return dedupePaths(opts.SkillDirs(settings))
	}
	if settings == nil {
		return nil
	}
	raw := strings.TrimSpace(settings.Get(key, ""))
	if raw == "" {
		return nil
	}
	fields := strings.FieldsFunc(raw, func(char rune) bool {
		return char == os.PathListSeparator || char == '\n' || char == '\r'
	})
	dirs := make([]string, 0, len(fields))
	for _, field := range fields {
		if dir := strings.TrimSpace(field); dir != "" {
			dirs = append(dirs, resolveAgainst(dir, dataDir))
		}
	}
	return dedupePaths(dirs)
}

// envSkillRoots is the rig-compatible $ZEOKEEL_SKILLS path list (the skills
// module scans it in the no-workspace scope).
func envSkillRoots() []string {
	raw := strings.TrimSpace(os.Getenv("ZEOKEEL_SKILLS"))
	if raw == "" {
		return nil
	}
	return strings.Split(raw, string(os.PathListSeparator))
}

// resolveAgainst makes one configured path absolute: a relative value resolves
// against the product data directory (the skills module's rule for the same
// kind of row), and "" dataDir falls back to the process working directory.
func resolveAgainst(value, dataDir string) string {
	path := strings.TrimSpace(value)
	if path == "" {
		return ""
	}
	path = filepath.FromSlash(path)
	if !filepath.IsAbs(path) {
		base := strings.TrimSpace(dataDir)
		if base == "" {
			base = "."
		}
		path = filepath.Join(base, path)
	}
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	return filepath.Clean(path)
}

// dedupePaths keeps the first occurrence of each non-empty cleaned path.
func dedupePaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

// statPath is CLILinkOptions.Stat with the os default.
func statPath(opts CLILinkOptions, path string) (os.FileInfo, error) {
	if opts.Stat != nil {
		return opts.Stat(path)
	}
	return os.Stat(path)
}

// readDirPath is CLILinkOptions.ReadDir with the os default.
func readDirPath(opts CLILinkOptions, dir string) ([]os.DirEntry, error) {
	if opts.ReadDir != nil {
		return opts.ReadDir(dir)
	}
	return os.ReadDir(dir)
}

// readPath is CLILinkOptions.ReadFile with the os default, bounded by
// maxRegistryBytes: a registry is a small JSON document and a corrupt or huge
// file must not be able to make a prompt render allocate.
func readPath(opts CLILinkOptions, path string) ([]byte, error) {
	if opts.ReadFile != nil {
		return opts.ReadFile(path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(io.LimitReader(file, maxRegistryBytes))
}

// --- executable guard ------------------------------------------------------

// CLIToolSpecByID returns one registry entry (the probe re-checks the guard
// against the spec, so it needs the same list the resolution used).
func CLIToolSpecByID(id string) (CLIToolSpec, bool) {
	for _, spec := range CLILinkTools() {
		if spec.ID == id {
			return spec, true
		}
	}
	return CLIToolSpec{}, false
}

// CLIToolExecutableAllowed reports whether path may be executed as spec's CLI:
// the basename must be the CLI's own binary name (a `.exe` suffix accepted, so
// a Windows install and a test fixture behave the same), the target must be a
// regular file, and on a Unix permission model it must carry an executable bit.
// reason explains a rejection and is empty when allowed.
//
// This is the guard behind "a settings row cannot name an arbitrary program":
// the module runs `<path> --version` outside the sandbox and outside approvals,
// so the accepted set is exactly the two vendor binaries this product documents.
func CLIToolExecutableAllowed(spec CLIToolSpec, path string, info os.FileInfo) (reason string, allowed bool) {
	if strings.TrimSpace(path) == "" {
		return "there is no path to run", false
	}
	if info == nil {
		return "it does not exist", false
	}
	if info.IsDir() {
		return "it is a directory", false
	}
	if !info.Mode().IsRegular() {
		return "it is not a regular file", false
	}
	if !isCLIBinaryName(filepath.Base(path), spec.Binary) {
		return fmt.Sprintf("its name is not %s (the %s CLI's executable name)", spec.Binary, spec.Title), false
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return fmt.Sprintf("it is not executable (mode %04o)", info.Mode().Perm()), false
	}
	return "", true
}

// isCLIBinaryName reports whether base is the CLI's binary name, with or without
// a Windows .exe suffix.
func isCLIBinaryName(base, binary string) bool {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		return false
	}
	return base == binary || strings.EqualFold(base, binary+".exe")
}

// cliPathProblem validates one configured cli.<id>.path value. It returns the
// reason the row cannot be used, or ok=false when it can.
func cliPathProblem(spec CLIToolSpec, path string, opts CLILinkOptions) (reason string, bad bool) {
	if hasControlChars(path) {
		return "the path contains control characters", true
	}
	info, err := statPath(opts, path)
	if err != nil || info == nil {
		return "the file is missing or unreadable", true
	}
	if reason, allowed := CLIToolExecutableAllowed(spec, path, info); !allowed {
		return reason, true
	}
	return "", false
}

// hasControlChars reports whether value carries a control character (newline,
// tab, ESC, DEL) or an invisible formatting rune (bidi overrides, zero-width
// joiners). Such a value cannot be rendered on one prompt line and cannot be a
// CLI path a human typed on purpose.
func hasControlChars(value string) bool {
	for _, char := range value {
		if char < 0x20 || char == 0x7f || unicode.IsControl(char) || unicode.Is(unicode.Cf, char) {
			return true
		}
	}
	return false
}

// --- prompt-safe text ------------------------------------------------------

// Prompt bounds: one rendered CLI line, one echoed path, one echoed value.
const (
	maxPromptLineRunes = 900
	maxPromptPathRunes = 400
	maxRegistryBytes   = 1 << 20
)

// shellQuoteArg renders one argument for a POSIX shell: single quotes around the
// whole value, with every inner single quote closed, escaped and reopened
// ('\”). The result is always one shell word, so a path with a space stays one
// argument and a path with `;`, `&`, `|`, `$()` or backticks is data — which
// matters because the model copies this text into a shell tool that does not
// stop for approval.
func shellQuoteArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// sanitizePromptValue makes one value safe to embed in a one-line prompt entry:
// control characters and invisible formatting runes become spaces, whitespace
// runs collapse to one space, and the result is truncated to maxRunes (0 = no
// bound). It is the settings-value guard: a newline in a settings row must not
// be able to append a line that looks like a new instruction.
func sanitizePromptValue(value string, maxRunes int) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, char := range value {
		if char < 0x20 || char == 0x7f || unicode.IsControl(char) || unicode.Is(unicode.Cf, char) {
			builder.WriteByte(' ')
			continue
		}
		builder.WriteRune(char)
	}
	cleaned := strings.Join(strings.Fields(builder.String()), " ")
	if maxRunes > 0 && utf8.RuneCountInString(cleaned) > maxRunes {
		cleaned = string([]rune(cleaned)[:maxRunes])
	}
	return cleaned
}
