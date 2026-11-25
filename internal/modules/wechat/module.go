package wechat

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/wechat"

// Module is the WeChat adapter descriptor: it requires the inbox (its only
// destination) and the product data directory (the login token from the QR flow
// lives in <dataDir>/wechat/token.json so it survives a restart), and provides
// contracts.KeyWeChat. Configuration can still supply a token explicitly; without
// one the adapter reports unauthorized instead of pretending to be connected.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		inboxService, err := ctx.Require(contracts.KeyInbox)
		if err != nil {
			return fmt.Errorf("%s: require inbox: %w", ModuleID, err)
		}
		dataDir, err := ctx.Require(contracts.KeyDataDir)
		if err != nil {
			return fmt.Errorf("%s: require data dir: %w", ModuleID, err)
		}
		storeDir := filepath.Join(dataDir, "wechat")
		adapter, err := openWithStore(inboxService, cfg, storeDir, ctx)
		if err != nil {
			return fmt.Errorf("%s: open adapter: %w", ModuleID, err)
		}
		if _, err := ctx.Provide(contracts.KeyWeChat, contracts.WeChatService(adapter)); err != nil {
			return fmt.Errorf("%s: provide wechat: %w", ModuleID, err)
		}
		// The QR login surface rides the same adapter: one login per process.
		if _, err := ctx.Provide(contracts.KeyWeChatLogin, contracts.WeChatLoginService(adapter)); err != nil {
			return fmt.Errorf("%s: provide wechat login: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyWeChat), module.Provides(contracts.KeyWeChatLogin)).
		AddRequires(module.Requires(contracts.KeyInbox), module.Requires(contracts.KeyDataDir)).
		WithName("ZeoKeel WeChat (ClawBot/iLink) adapter")
}

// openWithStore opens the adapter over storeDir and applies the module's
// resilience policy: the adapter must never brick the product, and a corrupt
// token file must never be overwritten. So a store that cannot be opened, or a
// corrupt token.json, degrades to a disconnected adapter (the corrupt file is
// quarantined as token.json.corrupt-<unix>) and is reported through the logger.
func openWithStore(inboxService contracts.InboxService, cfg Config, storeDir string, ctx cordis.Context) (*Adapter, error) {
	logf := func(format string, args ...any) {
		if logger, ok := ctx.Get(contracts.KeyLogger); ok && logger != nil {
			logger.Printf(format, args...)
		}
	}

	store, err := OpenTokenStore(storeDir)
	if err != nil {
		logf("wechat: token store unavailable (%v); login state will not survive a restart", err)
		return NewAdapter(inboxService, cfg), nil
	}
	adapter, err := OpenAdapter(inboxService, cfg, store)
	if err == nil {
		return adapter, nil
	}
	if !errors.Is(err, ErrTokenStoreCorrupt) {
		return nil, err
	}
	quarantined := fmt.Sprintf("%s.corrupt-%d", TokenFileName, time.Now().Unix())
	quarantinePath := filepath.Join(storeDir, quarantined)
	if renameErr := os.Rename(store.Path(), quarantinePath); renameErr != nil {
		logf("wechat: corrupt token file could not be quarantined (%v); starting disconnected", renameErr)
	} else {
		logf("wechat: corrupt token file moved to %s; a new QR login is required", quarantinePath)
	}
	clean, retryErr := OpenAdapter(inboxService, cfg, store)
	if retryErr != nil {
		logf("wechat: token store still unusable (%v); login state will not be persisted", retryErr)
		return NewAdapter(inboxService, cfg), nil
	}
	return clean, nil
}
