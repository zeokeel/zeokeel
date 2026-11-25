package wechat

// Activation tests: the module must open its token store under the product data
// directory, degrade to a disconnected adapter instead of bricking the product
// when the stored token is corrupt, and restore a valid login across restarts.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
)

// testLogger is a goroutine-safe contracts.Logger test double.
type testLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *testLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *testLogger) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func loadWechatModule(t *testing.T, dataDir string) (cordis.Context, *module.Loader, contracts.WeChatService, *testLogger) {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() {
		if err := root.DisposeAll(); err != nil {
			t.Errorf("DisposeAll: %v", err)
		}
	})
	inboxService := inbox.NewService(inbox.Config{MaxPerMinute: 1000})
	if _, err := root.Provide(contracts.KeyInbox, contracts.InboxService(inboxService)); err != nil {
		t.Fatalf("provide inbox: %v", err)
	}
	if _, err := root.Provide(contracts.KeyDataDir, dataDir); err != nil {
		t.Fatalf("provide data dir: %v", err)
	}
	logger := &testLogger{}
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(logger)); err != nil {
		t.Fatalf("provide logger: %v", err)
	}

	registry := module.NewRegistry()
	// The inbox is stood in at the root: this test is about the wechat store, not
	// about loading the inbox module (the assembly graph owns that).
	registry.AddBuiltin(contracts.KeyInbox).AddBuiltin(contracts.KeyDataDir).AddBuiltin(contracts.KeyLogger)
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register wechat module: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{ID: "wechat", Module: ModuleID}})
	if err != nil {
		t.Fatalf("load wechat module: %v", err)
	}
	t.Cleanup(func() {
		if err := loader.Close(); err != nil {
			t.Errorf("loader.Close: %v", err)
		}
	})
	service, ok := root.Get(contracts.KeyWeChat)
	if !ok {
		t.Fatal("contracts.KeyWeChat was not provided by the module")
	}
	return root, loader, service, logger
}

func TestModuleActivatesWithAStoreDirAndStartsDisconnected(t *testing.T) {
	dataDir := t.TempDir()
	_, loader, service, _ := loadWechatModule(t, dataDir)

	instance, ok := loader.Instance("wechat")
	if !ok || instance.State() != cordis.Active {
		t.Fatalf("wechat instance = (%v, %v), want active", ok, instance.State())
	}
	status, err := service.Status(context.Background())
	if err != nil || status.Connected {
		t.Fatalf("status = (%+v, %v), want disconnected", status, err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "wechat")); err != nil {
		t.Fatalf("wechat data dir was not prepared: %v", err)
	}
}

func TestModuleRestoresThePersistedToken(t *testing.T) {
	dataDir := t.TempDir()
	store, err := OpenTokenStore(filepath.Join(dataDir, "wechat"))
	if err != nil {
		t.Fatalf("OpenTokenStore: %v", err)
	}
	if err := store.Save("persisted-token"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, _, service, _ := loadWechatModule(t, dataDir)
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Connected {
		t.Fatalf("status = %+v, want Connected from the stored token", status)
	}
}

func TestModuleQuarantinesACorruptTokenFile(t *testing.T) {
	dataDir := t.TempDir()
	storeDir := filepath.Join(dataDir, "wechat")
	if err := os.MkdirAll(storeDir, 0o700); err != nil {
		t.Fatalf("mkdir store dir: %v", err)
	}
	corrupt := []byte(`{"token": `)
	if err := os.WriteFile(filepath.Join(storeDir, TokenFileName), corrupt, 0o600); err != nil {
		t.Fatalf("seed corrupt token: %v", err)
	}

	_, loader, service, logger := loadWechatModule(t, dataDir)
	instance, ok := loader.Instance("wechat")
	if !ok || instance.State() != cordis.Active {
		t.Fatalf("wechat instance = (%v, %v), want active despite the corrupt token", ok, instance.State())
	}
	status, err := service.Status(context.Background())
	if err != nil || status.Connected {
		t.Fatalf("status = (%+v, %v), want disconnected", status, err)
	}

	quarantined, err := filepath.Glob(filepath.Join(storeDir, TokenFileName+".corrupt-*"))
	if err != nil {
		t.Fatalf("glob quarantine: %v", err)
	}
	if len(quarantined) != 1 {
		t.Fatalf("quarantined files = %v, want one %s.corrupt-*", quarantined, TokenFileName)
	}
	moved, err := os.ReadFile(quarantined[0])
	if err != nil {
		t.Fatalf("read quarantined file: %v", err)
	}
	if string(moved) != string(corrupt) {
		t.Fatalf("quarantined content = %q, want the original bytes", moved)
	}
	if !strings.Contains(logger.joined(), "corrupt") {
		t.Fatalf("logger = %q, want a corrupt-token warning", logger.joined())
	}
	// The store is usable again: a login after quarantine persists as usual.
	store, err := OpenTokenStore(storeDir)
	if err != nil {
		t.Fatalf("OpenTokenStore after quarantine: %v", err)
	}
	if err := store.Save("fresh-token"); err != nil {
		t.Fatalf("Save after quarantine: %v", err)
	}
	if token, ok, err := store.Load(); err != nil || !ok || token != "fresh-token" {
		t.Fatalf("Load after quarantine = (%q, %v, %v)", token, ok, err)
	}
}
