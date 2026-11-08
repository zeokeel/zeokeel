package toolkit

// Optional single LSP server wiring: when the lsp.server settings row names a
// language server, the toolkit starts it at activation and registers rig's
// lsp_* tools (hover/definition/references/...). Startup failures and duplicate
// tool names only warn; the module never fails because of LSP.

import (
	"context"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	cordis "github.com/zeokeel/zeokeel-kernel"
	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/tools/lsp"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// lspSession is the started-server handle the toolkit needs: the rig tool set
// for its client and the close hook. Tests inject it through lspStartFunc.
type lspSession interface {
	Tools(filePathToURI func(string) (string, error)) []rig.Tool
	Close() error
}

// lspClientSession adapts one rig *lsp.Client to lspSession.
type lspClientSession struct{ client *lsp.Client }

// Tools implements lspSession.
func (s lspClientSession) Tools(filePathToURI func(string) (string, error)) []rig.Tool {
	return lsp.Tools(s.client, filePathToURI)
}

// Close implements lspSession.
func (s lspClientSession) Close() error { return s.client.Close() }

// lspStartFunc starts the language server. It is a package seam so tests can
// exercise the registration/ownership paths without a real server process.
var lspStartFunc = func(ctx context.Context, cfg lsp.Config) (lspSession, error) {
	client, err := lsp.Start(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return lspClientSession{client: client}, nil
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
	session, err := lspStartFunc(ctx, lsp.Config{
		Command:    cfg.Command,
		Args:       cfg.Args,
		Env:        cfg.Env,
		Dir:        cfg.Dir,
		RootURI:    cfg.RootURI,
		LanguageID: cfg.LanguageID,
	})
	if err != nil {
		logf("toolkit: lsp: start %q: %v", cfg.Command, err)
		return nil, noop
	}

	names := make(map[string]bool, len(existing))
	for _, tool := range existing {
		if tool != nil {
			names[tool.Name()] = true
		}
	}
	candidates := session.Tools(filePathToURI)
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
		kept = append(kept, tool)
	}

	var once sync.Once
	closeSession := func() {
		once.Do(func() {
			if err := session.Close(); err != nil {
				logf("toolkit: lsp: close: %v", err)
			}
		})
	}
	return kept, closeSession
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

// filePathToURI converts a workspace file path into the file:// URI the LSP
// tools send to the server.
func filePathToURI(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}).String(), nil
}
