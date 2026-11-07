package toolkit

// Optional single LSP server wiring: when the lsp.server settings row names a
// language server, the toolkit starts it at activation and registers rig's
// lsp_* tools (hover/definition/references/...). Startup failures and duplicate
// tool names only warn; the module never fails because of LSP.

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

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

// startLSPTools reads the optional lsp.server settings row and, when it names a
// command, starts the server and returns its tools plus the ownership cleanup.
// It never fails: parse/start/conflict problems only log warnings.
func startLSPTools(
	ctx context.Context,
	logf func(string, ...any),
	settings contracts.SettingsService,
	hasSettings bool,
	existing []rig.Tool,
) ([]rig.Tool, func()) {
	noop := func() {}
	if !hasSettings || settings == nil {
		return nil, noop
	}
	cfg, err := contracts.ParseLSPServerConfig(settings.Get(contracts.SettingLSPServer, ""))
	if err != nil {
		logf("toolkit: lsp: ignoring %s: %v", contracts.SettingLSPServer, err)
		return nil, noop
	}
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, noop
	}
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

// filePathToURI converts a workspace file path into the file:// URI the LSP
// tools send to the server.
func filePathToURI(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}).String(), nil
}
