package toolkit

// Optional single LSP server wiring: when the lsp.server settings row names a
// language server, the toolkit starts it at activation and registers rig's
// lsp_* tools (hover/definition/references/...). Startup failures and duplicate
// tool names only warn; the module never fails because of LSP.

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	cordis "github.com/zeokeel/zeokeel-kernel"
	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/sandbox"
	lspHost "github.com/zeokeel/zeokeel-rig/tools/lsp/host"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// lspHostHandle is the lifecycle surface toolkit needs from an optional LSP
// server. It is a seam so tests can exercise registration/ownership without a
// real language server process; production uses lsp/host.Host.
type lspHostHandle interface {
	// Start launches the server and completes the initialize handshake. It is
	// non-fatal: failures are logged and Tools stays empty.
	Start(ctx context.Context)
	// Tools returns the server's rig tools (empty when start failed).
	Tools() []rig.Tool
	// Close tears the server down; it is idempotent.
	Close() error
}

// lspNewHostFunc builds the LSP host. Tests replace it to inject a fake host.
var lspNewHostFunc = func(cfg lspHost.Config) (lspHostHandle, error) {
	return lspHost.New(cfg)
}

// startLSPTools reads the optional multi-server LSP settings rows (lsp.servers +
// lsp.active, falling back to the legacy lsp.server row), starts the ACTIVE
// server only and returns its tools plus the ownership cleanup. It never fails:
// parse/start/conflict problems only log warnings.
func startLSPTools(
	ctx context.Context,
	kctx cordis.Context,
	logf func(string, ...any),
	settings contracts.SettingsService,
	hasSettings bool,
	existing []rig.Tool,
	sandboxPolicy *sandbox.Policy,
) ([]rig.Tool, func()) {
	noop := func() {}
	if !hasSettings || settings == nil {
		return nil, noop
	}
	settingsServers, err := contracts.ResolveLSPServerConfigs(
		settings.Get(contracts.SettingLSPServers, ""),
		settings.Get(contracts.SettingLSPServer, ""),
	)
	if err != nil {
		logf("toolkit: lsp: ignoring %s/%s: %v",
			contracts.SettingLSPServers, contracts.SettingLSPServer, err)
		return nil, noop
	}
	legacyUsed := false
	if strings.TrimSpace(settings.Get(contracts.SettingLSPServers, "")) == "" &&
		len(settingsServers) > 0 {
		legacyUsed = true
	}
	// The workspace config file is read only after the explicit opt-in.
	workspaceServers := loadWorkspaceLSPServers(kctx, settings, logf)
	servers := mergeLSPServers(settingsServers, workspaceServers)
	if len(servers) == 0 {
		return nil, noop
	}
	cfg, ok := contracts.ActiveLSPServer(servers, settings.Get(contracts.SettingLSPActive, ""))
	if !ok {
		return nil, noop
	}
	sources := make([]string, 0, 2)
	if len(settingsServers) > 0 {
		if legacyUsed {
			sources = append(sources, "legacy")
		} else {
			sources = append(sources, "settings")
		}
	}
	if len(workspaceServers) > 0 {
		sources = append(sources, "workspace")
	}
	logf("toolkit: lsp: starting server %q (command %q, %d configured; sources %s)",
		cfg.Name, cfg.Command, len(servers), strings.Join(sources, ","))
	// The host owns process start, the initialize handshake, tool construction
	// and close. Its Logger is prefixed with the toolkit's diagnostic channel so
	// activation logs stay recognizable. host.Config has no LanguageID field;
	// rig then uses its documented "plaintext" default for didOpen.
	server, err := lspNewHostFunc(lspHost.Config{
		Command: cfg.Command,
		Args:    cfg.Args,
		Env:     cfg.Env,
		Dir:     cfg.Dir,
		RootURI: cfg.RootURI,
		Logger: func(format string, args ...any) {
			// The host prefixes its diagnostics with "lsp host: "; the toolkit
			// already names the subsystem, so strip it and keep the existing
			// "toolkit: lsp: ..." log contract.
			logf("toolkit: lsp: "+strings.TrimPrefix(format, "lsp host: "), args...)
		},
	})
	if err != nil {
		logf("toolkit: lsp: start %q: %v", cfg.Command, err)
		return nil, noop
	}
	if server == nil {
		logf("toolkit: lsp: start %q: host unavailable", cfg.Command)
		return nil, noop
	}
	server.Start(ctx)
	candidates := server.Tools()
	if len(candidates) == 0 {
		// Start is non-fatal and logs its own reason; add the product-level
		// activation line callers/tests use to see that LSP stayed degraded.
		logf("toolkit: lsp: start %q: no tools registered", cfg.Command)
		if closeErr := server.Close(); closeErr != nil {
			logf("toolkit: lsp: close after failed start: %v", closeErr)
		}
		return nil, noop
	}

	names := make(map[string]bool, len(existing))
	for _, tool := range existing {
		if tool != nil {
			names[tool.Name()] = true
		}
	}
	kept := make([]rig.Tool, 0, len(candidates))
	for _, tool := range candidates {
		if tool == nil {
			continue
		}
		name := tool.Name()
		if names[name] {
			logf("toolkit: lsp: skipping %q: tool name already registered", name)
			continue
		}
		names[name] = true
		// The rig LSP tools read the model-provided path directly. The wrapper
		// applies the sandbox policy (roots + deniedGlobs) and resolves a
		// relative path against the workspace root before the rig tool opens
		// it, so lsp_* cannot leave the sandbox and relative paths work.
		kept = append(kept, &sandboxedLSPTool{inner: tool, policy: sandboxPolicy, logf: logf})
	}

	var once sync.Once
	closeServer := func() {
		once.Do(func() {
			if err := server.Close(); err != nil {
				logf("toolkit: lsp: close: %v", err)
			}
		})
	}
	return kept, closeServer
}

// loadWorkspaceLSPServers reads <workspace root>/.zeokeel/lsp.json, but only
// after the user opted in via lsp.workspace_config == "allow". Missing files
// and unreadable/invalid configs are ignored with a warning; the opt-in gate is
// checked before touching the filesystem.
func loadWorkspaceLSPServers(kctx cordis.Context, settings contracts.SettingsService,
	logf func(string, ...any)) []contracts.LSPServerConfig {
	if settings == nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(settings.Get(contracts.SettingLSPWorkspaceConfig, "")), "allow") {
		return nil
	}
	workspace, ok := kctx.Get(contracts.KeyWorkspace)
	if !ok || workspace == nil {
		return nil
	}
	current, open, err := workspace.Current(context.Background())
	if err != nil {
		logf("toolkit: lsp: workspace config: read current workspace: %v", err)
		return nil
	}
	if !open {
		return nil
	}
	root := strings.TrimSpace(current.Root)
	if root == "" {
		return nil
	}
	path := filepath.Join(root, filepath.FromSlash(contracts.LSPWorkspaceConfigFile))
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		logf("toolkit: lsp: workspace config: read %q: %v", path, err)
		return nil
	}
	servers, err := contracts.ParseLSPWorkspaceConfigFile(data)
	if err != nil {
		logf("toolkit: lsp: workspace config: ignoring %q: %v", path, err)
		return nil
	}
	if len(servers) > 0 {
		logf("toolkit: lsp: workspace config: loaded %d server(s) from %q", len(servers), path)
	}
	return servers
}

// mergeLSPServers combines the settings servers (primary, they win) with the
// workspace servers (secondary), matching names case-insensitively.
func mergeLSPServers(primary, secondary []contracts.LSPServerConfig) []contracts.LSPServerConfig {
	merged := append([]contracts.LSPServerConfig(nil), primary...)
	seen := make(map[string]bool, len(primary))
	for _, cfg := range primary {
		seen[strings.ToLower(strings.TrimSpace(cfg.Name))] = true
	}
	for _, cfg := range secondary {
		key := strings.ToLower(strings.TrimSpace(cfg.Name))
		if seen[key] {
			continue
		}
		seen[key] = true
		merged = append(merged, cfg)
	}
	return merged
}

// sandboxedLSPTool is the product-side registry entry of one rig lsp_* tool. It
// validates the model-provided "path" argument through the sandbox policy and
// rewrites it to the resolved absolute path (so relative paths resolve against
// the workspace root, not the process working directory) before delegating.
type sandboxedLSPTool struct {
	inner  rig.Tool
	policy *sandbox.Policy
	logf   func(string, ...any)
}

var _ rig.Tool = (*sandboxedLSPTool)(nil)

// Name implements rig.Tool.
func (t *sandboxedLSPTool) Name() string { return t.inner.Name() }

// Description implements rig.Tool.
func (t *sandboxedLSPTool) Description() string { return t.inner.Description() }

// Schema implements rig.Tool.
func (t *sandboxedLSPTool) Schema() map[string]any { return t.inner.Schema() }

// Invoke implements rig.Tool: the model path is resolved through the sandbox
// policy first, so an outside-root or denied-glob path is rejected before any
// file is opened, and the inner rig tool receives an absolute path.
func (t *sandboxedLSPTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	rewritten, err := t.rewritePath(call.Arguments)
	if err != nil {
		return rig.ToolResult{
			Content: t.inner.Name() + ": path blocked by policy: " + err.Error(),
			IsError: true,
		}, nil
	}
	call.Arguments = rewritten
	return t.inner.Invoke(ctx, call)
}

// rewritePath validates and absolutizes the "path" argument (when present).
func (t *sandboxedLSPTool) rewritePath(arguments string) (string, error) {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" || trimmed == "null" || t.policy == nil {
		return arguments, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil {
		return arguments, nil
	}
	raw, ok := fields["path"]
	if !ok {
		return arguments, nil
	}
	var path string
	if err := json.Unmarshal(raw, &path); err != nil {
		return arguments, nil
	}
	resolved, err := t.policy.ResolvePath(path)
	if err != nil {
		return "", err
	}
	fields["path"], _ = json.Marshal(resolved)
	encoded, err := json.Marshal(fields)
	if err != nil {
		return arguments, nil
	}
	return string(encoded), nil
}
