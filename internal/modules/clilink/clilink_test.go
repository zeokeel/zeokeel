package clilink

// Unit tests for the external-CLI link service: the version probe (success,
// timeout, non-zero exit, silent success), the DWS_NO_UPDATE_CHECK=1 probe
// environment, the cached read model and the not-installed path.
//
// Every CLI here is a throwaway shell script under t.TempDir and every lookup is
// injected, so the suite neither depends on nor touches a real dws install.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// --- fakes -----------------------------------------------------------------

// mapSettings is a minimal contracts.SettingsService.
type mapSettings map[string]string

func (m mapSettings) Get(key, def string) string {
	if value, ok := m[key]; ok {
		return value
	}
	return def
}

func (m mapSettings) Set(key, value string) error { m[key] = value; return nil }

func (m mapSettings) All() map[string]string {
	out := make(map[string]string, len(m))
	for key, value := range m {
		out[key] = value
	}
	return out
}

// fakeLogger records log lines.
type fakeLogger struct {
	lines []string
}

func (l *fakeLogger) Printf(format string, args ...any) {
	l.lines = append(l.lines, strings.TrimSpace(fmt.Sprintf(format, args...)))
}

func (l *fakeLogger) joined() string { return strings.Join(l.lines, "\n") }

// writeScript writes one executable shell script and returns its path.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dws")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

// lookupOnly resolves exactly one binary name and fails the rest, so a script
// path under t.TempDir is the only thing a test can resolve.
func lookupOnly(name, path string) func(string) (string, error) {
	return func(candidate string) (string, error) {
		if candidate == name {
			return path, nil
		}
		return "", os.ErrNotExist
	}
}

// newTestService builds the service with a settings row pointing at dwsPath (may
// be "") and a PATH that resolves nothing besides that row.
func newTestService(t *testing.T, dwsPath string, timeout time.Duration) (*service, *fakeLogger) {
	t.Helper()
	logger := &fakeLogger{}
	settings := mapSettings{}
	lookup := func(string) (string, error) { return "", os.ErrNotExist }
	if dwsPath != "" {
		settings[contracts.SettingCLIDingtalkPath] = dwsPath
	}
	service := newService(settings, t.TempDir(), logger, timeout, contracts.CLILinkOptions{LookPath: lookup})
	return service, logger
}

// --- descriptor ------------------------------------------------------------

// TestDescriptorMetadata pins the seam: the module provides the read model and
// requires only the logger. No channel, no inbox, no endpoint.
func TestDescriptorMetadata(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, ModuleID)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyCLILink.Name() {
		t.Fatalf("provides = %+v, want the clilink key", descriptor.Provides)
	}
	if len(descriptor.Requires) != 1 || descriptor.Requires[0].Name != contracts.KeyLogger.Name() {
		t.Fatalf("requires = %+v, want the logger only (a missing CLI must never block boot)", descriptor.Requires)
	}
}

// TestValidateConfigRejectsNegativeTimeout pins the one config rule.
func TestValidateConfigRejectsNegativeTimeout(t *testing.T) {
	if err := validateConfig(Config{ProbeTimeoutMs: -1}); err == nil {
		t.Fatal("negative probeTimeoutMs accepted")
	}
	if err := validateConfig(Config{ProbeTimeoutMs: 0}); err != nil {
		t.Fatalf("zero probeTimeoutMs rejected: %v", err)
	}
}

// TestProbeTimeoutClamps covers the config bounds.
func TestProbeTimeoutClamps(t *testing.T) {
	if got := (Config{}).probeTimeout(); got != DefaultProbeTimeout {
		t.Fatalf("default probe timeout = %v, want %v", got, DefaultProbeTimeout)
	}
	if got := (Config{ProbeTimeoutMs: 1}).probeTimeout(); got != MinProbeTimeout {
		t.Fatalf("tiny probe timeout = %v, want %v", got, MinProbeTimeout)
	}
	if got := (Config{ProbeTimeoutMs: 10_000_000}).probeTimeout(); got != MaxProbeTimeout {
		t.Fatalf("huge probe timeout = %v, want %v", got, MaxProbeTimeout)
	}
}

// --- probe -----------------------------------------------------------------

// TestRefreshProbesVersionAndSetsTheUpdateCheckEnv is the happy path: the script
// reports a version, and it can only see DWS_NO_UPDATE_CHECK=1 because the probe
// put it in the environment.
func TestRefreshProbesVersionAndSetsTheUpdateCheckEnv(t *testing.T) {
	script := writeScript(t, `echo "dws version v9.9.9-update-check=$DWS_NO_UPDATE_CHECK"`)
	service, _ := newTestService(t, script, 5*time.Second)
	status := refreshStatus(t, service, "dingtalk")
	if status.State != contracts.CLIToolReady {
		t.Fatalf("state = %q (error %q), want ready", status.State, status.Error)
	}
	if !strings.Contains(status.Version, "v9.9.9") {
		t.Fatalf("version = %q, want the script's version line", status.Version)
	}
	if !strings.Contains(status.Version, "update-check=1") {
		t.Fatalf("version = %q, want DWS_NO_UPDATE_CHECK=1 in the probe environment", status.Version)
	}
	if status.Path != script || status.Source != contracts.CLIToolSourceSettings {
		t.Fatalf("path/source = %q/%q, want %q/settings", status.Path, status.Source, script)
	}
}

// TestRefreshNonZeroExitMarksUnusable proves a failing probe is reported, not
// fatal: the path stays visible and the stderr head explains the failure.
func TestRefreshNonZeroExitMarksUnusable(t *testing.T) {
	script := writeScript(t, `echo "unknown flag: --version" >&2; exit 3`)
	service, _ := newTestService(t, script, 5*time.Second)
	status := refreshStatus(t, service, "dingtalk")
	if status.State != contracts.CLIToolUnusable {
		t.Fatalf("state = %q, want unusable", status.State)
	}
	if status.Path != script {
		t.Fatalf("path = %q, want the found executable kept for diagnosis", status.Path)
	}
	if !strings.Contains(status.Error, "exit status 3") || !strings.Contains(status.Error, "unknown flag") {
		t.Fatalf("error = %q, want the exit status and the stderr head", status.Error)
	}
	if status.Version != "" {
		t.Fatalf("version = %q, want empty for a failed probe", status.Version)
	}
}

// TestRefreshTimeoutMarksUnusable proves the probe is bounded: a hanging CLI
// costs one configured timeout and activation still completes.
func TestRefreshTimeoutMarksUnusable(t *testing.T) {
	script := writeScript(t, `sleep 5; echo never`)
	service, _ := newTestService(t, script, 100*time.Millisecond)
	started := time.Now()
	status := refreshStatus(t, service, "dingtalk")
	elapsed := time.Since(started)
	if status.State != contracts.CLIToolUnusable {
		t.Fatalf("state = %q (error %q), want unusable", status.State, status.Error)
	}
	if !strings.Contains(status.Error, "timed out") {
		t.Fatalf("error = %q, want a timeout", status.Error)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("probe took %v, want it bounded by the 100ms timeout", elapsed)
	}
}

// TestRefreshSilentSuccessMarksUnusable covers a CLI that exits 0 with no
// output: there is no version to report, so the row says so instead of claiming
// "ready with version ”".
func TestRefreshSilentSuccessMarksUnusable(t *testing.T) {
	script := writeScript(t, `exit 0`)
	service, _ := newTestService(t, script, 5*time.Second)
	status := refreshStatus(t, service, "dingtalk")
	if status.State != contracts.CLIToolUnusable || !strings.Contains(status.Error, "printed nothing") {
		t.Fatalf("status = %+v, want unusable \"printed nothing\"", status)
	}
}

// TestRefreshStderrVersionIsAccepted covers a CLI that reports its version on
// stderr (some vendor CLIs do).
func TestRefreshStderrVersionIsAccepted(t *testing.T) {
	script := writeScript(t, `echo "dws version v1.2.3" >&2`)
	service, _ := newTestService(t, script, 5*time.Second)
	status := refreshStatus(t, service, "dingtalk")
	if status.State != contracts.CLIToolReady || status.Version != "dws version v1.2.3" {
		t.Fatalf("status = %+v, want the stderr line as the version", status)
	}
}

// --- read model ------------------------------------------------------------

// TestStatusIsACachedSnapshot proves Status never probes (the prompt path calls
// it) and that the snapshot cannot be mutated by a caller.
func TestStatusIsACachedSnapshot(t *testing.T) {
	script := writeScript(t, `echo "dws version v9.9.9"`)
	service, _ := newTestService(t, script, 5*time.Second)
	// Before any refresh the cache is empty: Status must not resolve or probe.
	if statuses := service.Status(); statuses != nil {
		t.Fatalf("Status() = %+v before Refresh, want nil", statuses)
	}
	service.Refresh(context.Background())
	statuses := service.Status()
	if len(statuses) != len(contracts.CLILinkTools()) {
		t.Fatalf("Status() = %d rows, want one per known CLI", len(statuses))
	}
	if statuses[0].ID != "dingtalk" || statuses[1].ID != "feishu" {
		t.Fatalf("row order = %q,%q, want the CLILinkTools order", statuses[0].ID, statuses[1].ID)
	}
	statuses[0].Path = "/mutated"
	statuses[0].Skills.Roots = append(statuses[0].Skills.Roots, "/mutated")
	if fresh := service.Status(); fresh[0].Path != script || len(fresh[0].Skills.Roots) != 0 {
		t.Fatalf("the returned snapshot shares memory with the cache: %+v", fresh[0])
	}
}

// TestRefreshReportsNotInstalledWithoutError proves the third answer is a state:
// no settings row, nothing on PATH, both rows not-installed and no error text.
func TestRefreshReportsNotInstalledWithoutError(t *testing.T) {
	service, logger := newTestService(t, "", 200*time.Millisecond)
	statuses := service.refreshAndLog(context.Background())
	if len(statuses) != 2 {
		t.Fatalf("statuses = %d, want 2", len(statuses))
	}
	for _, status := range statuses {
		if status.State != contracts.CLIToolNotInstalled || status.Error != "" {
			t.Fatalf("status = %+v, want a clean not-installed", status)
		}
	}
	// The log answers "is it linked" without a UI, and says what to set.
	log := logger.joined()
	if !strings.Contains(log, "dingtalk CLI not installed") || !strings.Contains(log, contracts.SettingCLIDingtalkPath) {
		t.Fatalf("log = %q, want a not-installed line naming the settings row", log)
	}
	if strings.Contains(log, "panic") {
		t.Fatalf("log = %q", log)
	}
}

// TestRefreshLogsReadyCLI covers the log line for a linked CLI (path + version).
func TestRefreshLogsReadyCLI(t *testing.T) {
	script := writeScript(t, `echo "dws version v2.0.0"`)
	service, logger := newTestService(t, script, 5*time.Second)
	service.refreshAndLog(context.Background())
	log := logger.joined()
	if !strings.Contains(log, "dingtalk CLI ready: "+script) || !strings.Contains(log, "dws version v2.0.0") {
		t.Fatalf("log = %q, want path and version", log)
	}
}

// TestRefreshFindsSkillsBundle proves the read model answers the third question:
// a skills.dirs root holding dingtalk-* skill directories (each with a SKILL.md)
// is reported as indexed.
func TestRefreshFindsSkillsBundle(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "multi")
	for _, name := range []string{"dingtalk-chat", "dingtalk-todo"} {
		writeTestSkill(t, bundle, name)
	}
	settings := mapSettings{contracts.SettingSkillsDirs: bundle}
	service := newService(settings, dir, &fakeLogger{}, time.Second, contracts.CLILinkOptions{
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
	})
	status := refreshStatus(t, service, "dingtalk")
	if status.Skills.State != contracts.CLISkillsIndexed || status.Skills.Count != 2 {
		t.Fatalf("skills = %+v, want indexed with 2 skills", status.Skills)
	}
}

// writeTestSkill creates one skill directory (root/name/SKILL.md).
func writeTestSkill(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o644); err != nil {
		t.Fatalf("write %s/SKILL.md: %v", dir, err)
	}
	return dir
}

// --- probe hardening -------------------------------------------------------

// TestProbeEnvIsMinimal is the environment pin: the child gets the allowlisted
// starting variables and DWS_NO_UPDATE_CHECK=1, and nothing else — in particular
// not the host's secrets, and not an inherited DWS_NO_UPDATE_CHECK=0.
func TestProbeEnvIsMinimal(t *testing.T) {
	t.Setenv("ZEOKEEL_PROBE_SECRET", "must-not-leak")
	t.Setenv("OPENAI_API_KEY", "sk-must-not-leak")
	t.Setenv("DWS_NO_UPDATE_CHECK", "0")
	script := writeScript(t, `echo "dws version v1 secret=${ZEOKEEL_PROBE_SECRET:-none} api=${OPENAI_API_KEY:-none} path=${PATH:+set} check=$DWS_NO_UPDATE_CHECK"`)
	service, _ := newTestService(t, script, 5*time.Second)
	status := refreshStatus(t, service, "dingtalk")
	if status.State != contracts.CLIToolReady {
		t.Fatalf("state = %q (error %q), want ready", status.State, status.Error)
	}
	if !strings.Contains(status.Version, "secret=none") {
		t.Fatalf("version = %q, want the probe to hide ZEOKEEL_PROBE_SECRET", status.Version)
	}
	if !strings.Contains(status.Version, "api=none") {
		t.Fatalf("version = %q, want the probe to hide OPENAI_API_KEY", status.Version)
	}
	if !strings.Contains(status.Version, "path=set") {
		t.Fatalf("version = %q, want PATH kept (the CLI needs it to find its runtime)", status.Version)
	}
	if !strings.Contains(status.Version, "check=1") {
		t.Fatalf("version = %q, want DWS_NO_UPDATE_CHECK forced to 1", status.Version)
	}
}

// TestProbeEnvAllowlistHasNoWildcard guards the list itself: it is an allowlist
// of concrete names, so a new host variable is not inherited by accident.
func TestProbeEnvAllowlistHasNoWildcard(t *testing.T) {
	for _, name := range probeEnvNames {
		if strings.TrimSpace(name) != name || name == "" {
			t.Fatalf("probeEnvNames entry %q is malformed", name)
		}
		if strings.ContainsAny(name, "*=?") {
			t.Fatalf("probeEnvNames entry %q is a pattern, want a concrete name", name)
		}
	}
	// DWS_NO_UPDATE_CHECK is appended explicitly, never inherited.
	for _, name := range probeEnvNames {
		if strings.EqualFold(name, probeEnvKey) {
			t.Fatalf("probeEnvNames must not inherit %s; probeEnv sets it to 1", probeEnvKey)
		}
	}
}

// TestCappedWriterBoundsAndDrains proves the output sink keeps at most its limit,
// drops the rest, and still reports a full write (so the CLI never blocks and
// never sees a short write).
func TestCappedWriterBoundsAndDrains(t *testing.T) {
	sink := newCappedWriter(16)
	blob := strings.Repeat("x", 1<<16)
	written, err := sink.Write([]byte(blob))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if written != len(blob) {
		t.Fatalf("Write reported %d bytes, want the full %d (a short write would block the CLI)", written, len(blob))
	}
	if sink.buf.Len() != 16 {
		t.Fatalf("retained %d bytes, want the 16-byte cap", sink.buf.Len())
	}
	if !sink.overflowed() {
		t.Fatal("overflow was not recorded")
	}
	// Chunked writes keep the same bound.
	chunked := newCappedWriter(8)
	for index := 0; index < 10; index++ {
		if _, err := chunked.Write([]byte("abcd")); err != nil {
			t.Fatalf("chunked Write: %v", err)
		}
	}
	if chunked.buf.Len() != 8 || !chunked.overflowed() {
		t.Fatalf("chunked sink = %d bytes/overflow=%v, want 8/true", chunked.buf.Len(), chunked.overflowed())
	}
}

// TestProbeOutputIsBoundedOnAFloodingCLI is the memory pin at the process level:
// a CLI that dumps hundreds of kilobytes on `--version` is drained, the probe
// still finishes inside its timeout, and the reported version stays one bounded
// line.
func TestProbeOutputIsBoundedOnAFloodingCLI(t *testing.T) {
	script := writeScript(t, `dd if=/dev/zero bs=1024 count=200 2>/dev/null | tr '\0' 'x'; echo; echo "dws version v7"`)
	service, _ := newTestService(t, script, 10*time.Second)
	started := time.Now()
	status := refreshStatus(t, service, "dingtalk")
	elapsed := time.Since(started)
	if status.State != contracts.CLIToolReady {
		t.Fatalf("state = %q (error %q), want ready", status.State, status.Error)
	}
	if runes := []rune(status.Version); len(runes) > maxVersionRunes {
		t.Fatalf("version kept %d runes, want at most %d", len(runes), maxVersionRunes)
	}
	if strings.Contains(status.Version, "v7") {
		t.Fatalf("version = %q: the version line was 200 KiB in, so it must be beyond the cap", status.Version)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("flooding probe took %v, want it bounded", elapsed)
	}
}

// TestProbeKeepsWaitDelay proves the lingering-pipe guard survived: a CLI that
// leaves a background child holding stdout must not pin the probe, and the
// version it printed is still accepted (ErrWaitDelay is not a CLI failure).
func TestProbeKeepsWaitDelay(t *testing.T) {
	script := writeScript(t, `( sleep 30 ) & echo "dws version v3.4.5"`)
	service, _ := newTestService(t, script, 20*time.Second)
	started := time.Now()
	status := refreshStatus(t, service, "dingtalk")
	elapsed := time.Since(started)
	if status.State != contracts.CLIToolReady || status.Version != "dws version v3.4.5" {
		t.Fatalf("status = %+v, want ready with the printed version", status)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("probe took %v, want WaitDelay to cut the lingering pipe (~500ms)", elapsed)
	}
}

// TestProbeRefusesAForeignExecutable is the execution guard's second line: even
// if a row reached the probe (an older cached snapshot, a built-by-hand status),
// the probe re-checks the path and refuses to run a program that is not the CLI.
func TestProbeRefusesAForeignExecutable(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	script := filepath.Join(dir, "package.sh")
	body := "#!/bin/sh\ntouch " + marker + "\necho \"dws version v1\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	service := newService(mapSettings{}, dir, &fakeLogger{}, 2*time.Second, contracts.CLILinkOptions{
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
	})
	status := contracts.CLIToolStatus{
		ID: "dingtalk", Title: "DingTalk", Binary: "dws",
		State: contracts.CLIToolReady, Path: script, Source: contracts.CLIToolSourceSettings,
	}
	service.probe(context.Background(), &status)
	if status.State != contracts.CLIToolUnusable {
		t.Fatalf("state = %q, want unusable", status.State)
	}
	if !strings.Contains(status.Error, "refusing to run") {
		t.Fatalf("error = %q, want a refusal", status.Error)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the foreign executable was run")
	}
}

// TestProbeRefusesAnUnknownCLI keeps the guard closed for an id the registry does
// not know (nothing to compare the basename against).
func TestProbeRefusesAnUnknownCLI(t *testing.T) {
	script := writeScript(t, `echo "dws version v1"`)
	service := newService(mapSettings{}, t.TempDir(), &fakeLogger{}, time.Second, contracts.CLILinkOptions{
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
	})
	status := contracts.CLIToolStatus{ID: "mystery", Title: "Mystery", Binary: "mystery", State: contracts.CLIToolReady, Path: script}
	service.probe(context.Background(), &status)
	if status.State != contracts.CLIToolUnusable || !strings.Contains(status.Error, "unknown CLI") {
		t.Fatalf("status = %+v, want an unusable unknown-CLI refusal", status)
	}
}

// TestRefreshLogDistinguishesUnconfiguredFromUnusable is the honest-log pin: a
// bad row means "fix the row", never "the CLI is not installed", and the row's
// reason reaches the log instead of being dropped.
func TestRefreshLogDistinguishesUnconfiguredFromUnusable(t *testing.T) {
	// 1. Nothing configured: the not-installed line names both remedies.
	service, logger := newTestService(t, "", 200*time.Millisecond)
	service.refreshAndLog(context.Background())
	log := logger.joined()
	if !strings.Contains(log, "dingtalk CLI not installed") || !strings.Contains(log, contracts.SettingCLIDingtalkPath) || !strings.Contains(log, "PATH") {
		t.Fatalf("log = %q, want the not-installed line with both remedies", log)
	}
	if strings.Contains(log, "not usable") {
		t.Fatalf("log = %q, want no unusable-row line when nothing is configured", log)
	}

	// 2. A configured path that does not work: fix the row, and the reason is
	// carried (it used to be discarded).
	missing := filepath.Join(t.TempDir(), "nope", "dws")
	badService, badLogger := newTestService(t, missing, 200*time.Millisecond)
	badService.refreshAndLog(context.Background())
	badLog := badLogger.joined()
	if !strings.Contains(badLog, "not usable") || !strings.Contains(badLog, missing) {
		t.Fatalf("log = %q, want the unusable row and its path", badLog)
	}
	if !strings.Contains(badLog, "missing or unreadable") {
		t.Fatalf("log = %q, want the row's reason", badLog)
	}
	for _, line := range badLogger.lines {
		if strings.Contains(line, "dingtalk CLI not installed") {
			t.Fatalf("log line = %q: a wrong row must not be reported as \"not installed\"", line)
		}
	}
}

// --- kernel activation -----------------------------------------------------

// TestModuleActivatesAndProvidesTheReadModel loads the module on a real kernel
// root: activation must succeed whether or not this machine has a CLI, and the
// service must be reachable under KeyCLILink.
func TestModuleActivatesAndProvidesTheReadModel(t *testing.T) {
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	logger := &fakeLogger{}
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(logger)); err != nil {
		t.Fatalf("provide logger: %v", err)
	}
	if _, err := root.Provide(contracts.KeySettings, contracts.SettingsService(mapSettings{})); err != nil {
		t.Fatalf("provide settings: %v", err)
	}
	if _, err := root.Provide(contracts.KeyDataDir, t.TempDir()); err != nil {
		t.Fatalf("provide data dir: %v", err)
	}
	registry := module.NewRegistry()
	// The host provides these in every product; the preflight needs them
	// declared so the module's Requires(KeyLogger) resolves.
	registry.AddBuiltin(contracts.KeyLogger).AddBuiltin(contracts.KeySettings).AddBuiltin(contracts.KeyDataDir)
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{ID: "clilink", Module: ModuleID}})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })

	service, err := root.Require(contracts.KeyCLILink)
	if err != nil {
		t.Fatalf("require KeyCLILink: %v", err)
	}
	statuses := service.Status()
	if len(statuses) != len(contracts.CLILinkTools()) {
		t.Fatalf("Status() = %d rows after activation, want %d", len(statuses), len(contracts.CLILinkTools()))
	}
	for _, status := range statuses {
		switch status.State {
		case contracts.CLIToolReady, contracts.CLIToolNotInstalled, contracts.CLIToolUnusable:
		default:
			t.Fatalf("status %q has an unknown state %q", status.ID, status.State)
		}
	}
	if !strings.Contains(logger.joined(), "clilink") {
		t.Fatalf("activation logged nothing: %q", logger.joined())
	}
}

// refreshStatus refreshes the service and returns one row.
func refreshStatus(t *testing.T, service *service, id string) contracts.CLIToolStatus {
	t.Helper()
	for _, status := range service.Refresh(context.Background()) {
		if status.ID == id {
			return status
		}
	}
	t.Fatalf("no %q row after Refresh", id)
	return contracts.CLIToolStatus{}
}
