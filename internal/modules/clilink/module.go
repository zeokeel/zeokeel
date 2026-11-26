// Package clilink is ZeoKeel's external-CLI link: it answers three questions
// about a vendor command-line tool (DingTalk's `dws`, Feishu's `lark-cli`) and
// publishes the answers as a read model.
//
//  1. Where is the executable? The `cli.<id>.path` settings row wins, then a
//     PATH lookup, and "nowhere" is the third answer (contracts.CLIToolNotInstalled
//     — a normal state, not an error, and never a boot blocker).
//
//  2. What does it report? One bounded `<path> --version` probe at activation
//     (and on Refresh). "Bounded" is load-bearing, because that probe is the one
//     process this module spawns, it runs outside the sandbox and outside the
//     approval surface, and the path it runs comes from a settings row:
//     the executable is first checked against the CLI allowlist
//     (contracts.CLIToolExecutableAllowed: basename `dws` / `lark-cli`, regular
//     file, executable bit) and the check is repeated right before exec; the
//     child gets a minimal environment (probeEnv: PATH/HOME/TMPDIR/LANG and
//     friends, never the host's API keys); both output streams are capped
//     (maxProbeOutputBytes, still drained so a chatty CLI cannot block); and the
//     whole call is bounded by the configured timeout plus WaitDelay.
//
//     The environment carries DWS_NO_UPDATE_CHECK=1 because dws otherwise
//     spends ~0.7s per invocation on an online version check. That flag is a
//     dws-only courtesy: lark-cli does not know it, and no vendor CLI promises
//     `--version` is offline. "The probe does not touch the network" is
//     therefore NOT a guarantee of this module — read it as "we do not ask dws
//     to check, and we do not give the child any credentials to check with".
//
//  3. Is its skill bundle indexed? Yes when a skill root that is in scope for
//     THIS run (the open workspace's roots and its skills.dirs.<id> row, or the
//     global row when no workspace is open) holds the CLI's `dingtalk-*` /
//     `lark-*` skill directories, each with a SKILL.md — the same test the
//     skills module applies, so the count can never claim a skill the skill tool
//     would not list.
//
// What this module deliberately is NOT: an inbound channel. It registers no
// endpoint, receives no message, sends no message and downloads nothing. The
// product's model is DeepSeek Harness's dsh-dingtalk / dsh-feishu plugins — a
// CLI plus a skill bundle plus a sentence in the prompt — not MCP and not a
// long-connection adapter. The channel adapters (internal/modules/dingtalk,
// internal/modules/feishu) are a different, outbound-or-inbound surface that
// this module never touches.
//
// Two limits stated plainly, because both were once implied to be more:
//
//   - The cached read model published under contracts.KeyCLILink has no
//     consumer in the product today. The prompt does not read it (the agent
//     re-resolves the settings rows itself on every run), the settings page
//     reads the rows, and nothing Requires the key.
//   - Refresh is not subscribed to settings changes: activation fills the
//     cache and only an explicit Refresh replaces it. "Editing cli.<id>.path
//     needs no restart" holds for the prompt (resolved per run) and for a fresh
//     Refresh — not for the snapshot already in memory.
//
// The resolution, the state names and the model-facing text live in
// internal/contracts/clilink.go: the agent renders its runtime-context section
// from the same leaf surface, so the read model and the prompt cannot drift.
package clilink

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/clilink"
	// Version is the module's semantic version.
	Version = "1.0.0"

	// DefaultProbeTimeout bounds one `<path> --version` probe. A real dws
	// answers in ~30ms; the bound exists so a hung or GUI-blocked executable
	// can never delay module activation indefinitely.
	DefaultProbeTimeout = 3 * time.Second
	// MinProbeTimeout / MaxProbeTimeout bound the configured probe timeout.
	MinProbeTimeout = 100 * time.Millisecond
	MaxProbeTimeout = 30 * time.Second

	// probeEnvKey suppresses the DingTalk CLI's per-invocation online version
	// check. It is set for every probe so a one-shot `--version` never waits on
	// dws's own update check; lark-cli ignores the variable, so it must never be
	// read as "the probe stays offline".
	probeEnvKey = "DWS_NO_UPDATE_CHECK"

	// maxProbeOutputBytes caps how much of each stream a probe retains. A vendor
	// CLI that dumps a whole manual page on `--version` must not be able to grow
	// the probe's memory; 8 KiB is far more than one version line, and the
	// excess is drained rather than left in the pipe (see cappedWriter).
	maxProbeOutputBytes = 8 << 10

	// maxVersionRunes keeps one reported version line small.
	maxVersionRunes = 160
)

// probeEnvNames is the probe's environment allowlist: the variables a
// well-behaved CLI needs to start (its PATH for the interpreter and its own
// helpers, a home for its config, a temp directory, a locale, and the Windows
// equivalents). Everything else the host process carries — provider API keys,
// channel tokens, proxy credentials — stays out of the child.
var probeEnvNames = []string{
	"PATH",
	"HOME",
	"USERPROFILE",
	"TMPDIR",
	"TMP",
	"TEMP",
	"LANG",
	"LC_ALL",
	"LC_CTYPE",
	"TZ",
	"SHELL",
	"SystemRoot",
	"ComSpec",
	"PATHEXT",
	"WINDIR",
	"APPDATA",
	"LOCALAPPDATA",
	"ProgramData",
	"ProgramFiles",
}

// Config is the module's typed assembly config.
type Config struct {
	// ProbeTimeoutMs bounds one `--version` probe in milliseconds (0/unset keeps
	// the 3s default; a positive value is clamped to [100, 30000]).
	ProbeTimeoutMs int `json:"probeTimeoutMs,omitempty"`
}

// probeTimeout resolves the effective probe timeout.
func (c Config) probeTimeout() time.Duration {
	ms := c.ProbeTimeoutMs
	switch {
	case ms <= 0:
		return DefaultProbeTimeout
	case ms < int(MinProbeTimeout/time.Millisecond):
		return MinProbeTimeout
	case ms > int(MaxProbeTimeout/time.Millisecond):
		return MaxProbeTimeout
	default:
		return time.Duration(ms) * time.Millisecond
	}
}

// Module is the external-CLI link descriptor: it provides contracts.KeyCLILink
// and requires only the host logger. The settings store and the data directory
// are optional — without them every CLI reports not-installed (and a relative
// skills.dirs value resolves against the process working directory), which is
// exactly the honest answer for a product assembled without a user scope.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		logger, err := ctx.Require(contracts.KeyLogger)
		if err != nil {
			return fmt.Errorf("%s: require logger: %w", ModuleID, err)
		}
		settings, _ := ctx.Get(contracts.KeySettings)
		dataDir, _ := ctx.Get(contracts.KeyDataDir)
		service := newService(settings, dataDir, logger, cfg.probeTimeout(), contracts.CLILinkOptions{})
		// Resolve + probe once at activation. Nothing here can fail the boot:
		// not-installed is a state, and a probe error is recorded on the row.
		service.refreshAndLog(context.Background())
		if _, err := ctx.Provide(contracts.KeyCLILink, contracts.CLILinkService(service)); err != nil {
			return fmt.Errorf("%s: provide cli link: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyCLILink)).
		AddRequires(module.Requires(contracts.KeyLogger)).
		WithName("ZeoKeel external CLI links").
		WithVersion(Version).
		WithValidate(validateConfig)
}

// validateConfig rejects configs the service could not honour.
func validateConfig(cfg Config) error {
	if cfg.ProbeTimeoutMs < 0 {
		return fmt.Errorf("%s: probeTimeoutMs must not be negative", ModuleID)
	}
	return nil
}

// service is the contracts.CLILinkService implementation: a cached snapshot plus
// the probe that fills in versions.
type service struct {
	mu       sync.Mutex
	statuses []contracts.CLIToolStatus
	settings contracts.SettingsService
	dataDir  string
	logger   contracts.Logger
	timeout  time.Duration
	options  contracts.CLILinkOptions
}

// newService builds the service. options is the lookup seam: production passes
// the zero value (real filesystem and PATH), tests pass fakes so a dev machine
// that happens to have dws installed cannot change the result.
func newService(settings contracts.SettingsService, dataDir string, logger contracts.Logger, timeout time.Duration, options contracts.CLILinkOptions) *service {
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	return &service{
		statuses: nil,
		settings: settings,
		dataDir:  strings.TrimSpace(dataDir),
		logger:   logger,
		timeout:  timeout,
		options:  options,
	}
}

// Status implements contracts.CLILinkService: the cached snapshot, one row per
// known CLI in CLILinkTools order. It never probes (a prompt render must not
// spawn a process).
func (s *service) Status() []contracts.CLIToolStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneStatuses(s.statuses)
}

// Refresh implements contracts.CLILinkService: re-resolve and re-probe, then
// replace the cache. It never returns an error — an unusable CLI is reported in
// its row, by design (the whole module has no failure mode that stops a run).
func (s *service) Refresh(ctx context.Context) []contracts.CLIToolStatus {
	if ctx == nil {
		ctx = context.Background()
	}
	statuses := contracts.ResolveCLITools(s.settings, s.dataDir, s.options)
	// The probes are independent and short; running them together keeps module
	// activation at ~one probe, not one per CLI.
	var wait sync.WaitGroup
	for index := range statuses {
		wait.Add(1)
		go func(status *contracts.CLIToolStatus) {
			defer wait.Done()
			s.probe(ctx, status)
		}(&statuses[index])
	}
	wait.Wait()
	s.mu.Lock()
	s.statuses = statuses
	s.mu.Unlock()
	return cloneStatuses(statuses)
}

// refreshAndLog refreshes once and records one line per CLI, so the log answers
// "is the CLI linked" without a UI. A CLI nobody pointed at logs at most one
// short line and nothing else; a configured path that does not work is logged as
// the user's problem (fix the row), never as "not installed" (install it) — the
// two need different actions and the row's reason is carried into the line.
func (s *service) refreshAndLog(ctx context.Context) []contracts.CLIToolStatus {
	statuses := s.Refresh(ctx)
	if s.logger == nil {
		return statuses
	}
	for _, status := range statuses {
		switch {
		case status.State == contracts.CLIToolReady && status.Version != "":
			s.logger.Printf("[clilink] %s CLI ready: %s (%s, from %s)", status.ID, status.Path, status.Version, sourceOrDefault(status.Source))
		case status.State == contracts.CLIToolReady:
			s.logger.Printf("[clilink] %s CLI ready: %s (from %s)", status.ID, status.Path, sourceOrDefault(status.Source))
		case status.State == contracts.CLIToolUnusable:
			s.logger.Printf("[clilink] %s CLI unusable: %s (%v)", status.ID, status.Path, status.Error)
		case strings.TrimSpace(status.ConfiguredPath) != "":
			// Configured but unusable: the row is wrong (typo, wrong file, not
			// executable), so the hint is "fix or clear the row", not "install
			// the CLI or put it on PATH".
			s.logger.Printf("[clilink] %s CLI not usable: %s is not a usable executable (%v); fix or clear %s",
				status.ID, status.ConfiguredPath, status.Error, settingKeyFor(status.ID))
		default:
			// No row and nothing on PATH: the honest "not installed", with the
			// two things that would change that.
			s.logger.Printf("[clilink] %s CLI not installed (no %s row and %s is not on PATH)",
				status.ID, settingKeyFor(status.ID), status.Binary)
		}
		if status.Skills.State == contracts.CLISkillsIndexed {
			s.logger.Printf("[clilink] %s skills indexed from %s (%d skills)", status.ID, strings.Join(status.Skills.Roots, ", "), status.Skills.Count)
		}
	}
	return statuses
}

// probe fills in the version (or the reason there is none) for one resolved CLI.
// It is bounded by the configured timeout, refuses to run anything that is not
// the CLI's own executable, and never panics on odd output.
func (s *service) probe(ctx context.Context, status *contracts.CLIToolStatus) {
	if status == nil || !status.Found() {
		return
	}
	// The guard runs again here, immediately before exec: the row could have
	// been cached by an older snapshot (or built by a caller), and the process
	// this starts is outside the sandbox and outside approvals.
	spec, ok := contracts.CLIToolSpecByID(status.ID)
	if !ok {
		status.State = contracts.CLIToolUnusable
		status.Error = fmt.Sprintf("refusing to probe unknown CLI %q", status.ID)
		return
	}
	info, statErr := os.Stat(status.Path)
	if statErr != nil {
		status.State = contracts.CLIToolUnusable
		status.Error = fmt.Sprintf("%s cannot be inspected: %v", status.Path, statErr)
		return
	}
	if reason, allowed := contracts.CLIToolExecutableAllowed(spec, status.Path, info); !allowed {
		status.State = contracts.CLIToolUnusable
		status.Error = fmt.Sprintf("refusing to run %s: %s", status.Path, reason)
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	stdout := newCappedWriter(maxProbeOutputBytes)
	stderr := newCappedWriter(maxProbeOutputBytes)
	command := exec.CommandContext(probeCtx, status.Path, "--version")
	command.Env = probeEnv()
	command.Stdout = stdout
	command.Stderr = stderr
	// A child that ignores the kill (or leaves a grandchild holding the pipe)
	// must not pin the probe forever.
	command.WaitDelay = 500 * time.Millisecond
	err := command.Run()
	if probeCtx.Err() == context.DeadlineExceeded {
		status.State = contracts.CLIToolUnusable
		status.Error = fmt.Sprintf("%s --version timed out after %s", status.Path, s.timeout)
		return
	}
	// ErrWaitDelay is not the CLI failing: it means the process finished but a
	// background child kept its output pipe open past WaitDelay, so exec closed
	// the pipe. Whatever the CLI printed is already captured; do not throw it
	// away and do not describe it as a failure.
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		status.State = contracts.CLIToolUnusable
		status.Error = fmt.Sprintf("%s --version failed: %v", status.Path, err)
		if line := firstOutputLine(&stderr.buf); line != "" {
			status.Error += " (" + line + ")"
		}
		return
	}
	version := firstOutputLine(&stdout.buf)
	if version == "" {
		version = firstOutputLine(&stderr.buf)
	}
	if version == "" {
		status.State = contracts.CLIToolUnusable
		status.Error = status.Path + " --version printed nothing"
		if stdout.overflowed() || stderr.overflowed() {
			status.Error = fmt.Sprintf("%s --version printed no version line in its first %d bytes", status.Path, maxProbeOutputBytes)
		}
		return
	}
	status.Version = version
	status.Error = ""
	status.State = contracts.CLIToolReady
}

// probeEnv is the probe environment: the allowlisted variables (probeEnvNames)
// as the host has them, plus DWS_NO_UPDATE_CHECK=1. Nothing else is inherited —
// a vendor CLI has no business reading this process's credentials, and the
// probe only needs to start, find its runtime and answer `--version`.
func probeEnv() []string {
	host := os.Environ()
	env := make([]string, 0, len(probeEnvNames)+1)
	for _, name := range probeEnvNames {
		for _, entry := range host {
			key, value, found := strings.Cut(entry, "=")
			if !found || !strings.EqualFold(key, name) || strings.TrimSpace(value) == "" {
				continue
			}
			env = append(env, name+"="+value)
			break
		}
	}
	return append(env, probeEnvKey+"=1")
}

// cappedWriter is the probe's bounded output sink: it keeps the first limit
// bytes and discards the rest (io.LimitReader semantics on the write side). It
// always reports a full write, so a chatty CLI is drained instead of blocking on
// a full pipe — blocking would turn a verbose CLI into a spurious timeout — and
// never sees a short write.
type cappedWriter struct {
	buf   bytes.Buffer
	limit int
	over  bool
}

// newCappedWriter builds a sink that retains at most limit bytes.
func newCappedWriter(limit int) *cappedWriter {
	if limit < 0 {
		limit = 0
	}
	return &cappedWriter{limit: limit}
}

// Write implements io.Writer: it stores what fits, drops the rest, and reports
// the full length so the child's write succeeds.
func (w *cappedWriter) Write(p []byte) (int, error) {
	written := len(p)
	if room := w.limit - w.buf.Len(); room > 0 {
		if len(p) > room {
			p = p[:room]
			w.over = true
		}
		_, _ = w.buf.Write(p)
	} else if len(p) > 0 {
		w.over = true
	}
	return written, nil
}

// overflowed reports whether output was dropped.
func (w *cappedWriter) overflowed() bool { return w != nil && w.over }

// firstOutputLine returns the first non-blank line of one output buffer,
// single-lined and bounded.
func firstOutputLine(buffer *bytes.Buffer) string {
	if buffer == nil {
		return ""
	}
	for _, line := range strings.Split(buffer.String(), "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		if utf8.RuneCountInString(line) > maxVersionRunes {
			line = string([]rune(line)[:maxVersionRunes])
		}
		return line
	}
	return ""
}

// sourceOrDefault labels where a path came from in the log.
func sourceOrDefault(source string) string {
	if strings.TrimSpace(source) == "" {
		return "unknown"
	}
	return source
}

// settingKeyFor returns the settings row that overrides one CLI's path.
func settingKeyFor(id string) string {
	for _, spec := range contracts.CLILinkTools() {
		if spec.ID == id {
			return spec.SettingKey
		}
	}
	return "cli." + id + ".path"
}

// cloneStatuses copies the snapshot so a caller cannot mutate the cache (and a
// concurrent Refresh cannot race a reader). The Skills slices are copied too.
func cloneStatuses(statuses []contracts.CLIToolStatus) []contracts.CLIToolStatus {
	if statuses == nil {
		return nil
	}
	out := make([]contracts.CLIToolStatus, len(statuses))
	for index, status := range statuses {
		status.Skills.Roots = append([]string(nil), status.Skills.Roots...)
		out[index] = status
	}
	return out
}
