package plugins_test

// Frame tests for the Capabilities page's Connectors tab: the merged card model
// (settings.mcp.servers rows + the live plugins service), the add/edit form with
// the async connection test, the details modal, the destructive remove
// confirmation and the workspace mcp.json opt-in. The old settings MCP page's
// tests are migrated here; the settings package no longer owns MCP.

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/plugins"
)

// --- fixtures ---------------------------------------------------------------

const (
	connectorFilesJSON = `[{"name":"files","url":"http://127.0.0.1:9000/mcp"}]`
	connectorTwoJSON   = `[{"name":"files","url":"http://127.0.0.1:9000/mcp"},{"name":"web","url":"https://mcp.example.com/mcp"}]`
	connectorStdioJSON = `[{"name":"srv","transport":"stdio","command":"npx","args":["-y","server-github"],"env":["TOKEN=abc"],"timeoutMs":250}]`
)

// connectorPlugins is the live plugins service fake: MCP returns the snapshot,
// Refresh records the call and swaps in refreshed.
type connectorPlugins struct {
	mu        sync.Mutex
	mcp       []contracts.MCPPlugin
	refreshed []contracts.MCPPlugin
	calls     int
}

func (f *connectorPlugins) MCP() []contracts.MCPPlugin {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.MCPPlugin(nil), f.mcp...)
}

func (f *connectorPlugins) Refresh(context.Context) []contracts.MCPPlugin {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.refreshed != nil {
		f.mcp = append([]contracts.MCPPlugin(nil), f.refreshed...)
	}
	return append([]contracts.MCPPlugin(nil), f.mcp...)
}

func (f *connectorPlugins) refreshCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// connectorSettings is the settings store the form writes through.
type connectorSettings struct {
	mu     sync.Mutex
	values map[string]string
	setErr error
	sets   []string
}

func newConnectorSettings(values map[string]string) *connectorSettings {
	store := &connectorSettings{values: map[string]string{}}
	for key, value := range values {
		store.values[key] = value
	}
	return store
}

func (s *connectorSettings) Get(key, fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}

func (s *connectorSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setErr != nil {
		return s.setErr
	}
	s.sets = append(s.sets, key)
	s.values[key] = value
	return nil
}

func (s *connectorSettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out
}

func (s *connectorSettings) get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[key]
}

// connectorWorkspace is the minimal workspace service for the opt-in hint.
type connectorWorkspace struct{ root string }

func (w *connectorWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	return []contracts.Workspace{{ID: "ws", Name: "ws", Root: w.root}}, nil
}

func (w *connectorWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	return contracts.Workspace{ID: "ws", Name: "ws", Root: w.root}, true, nil
}

func (w *connectorWorkspace) Create(context.Context, string, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}

func (w *connectorWorkspace) Open(context.Context, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}

func (w *connectorWorkspace) Delete(context.Context, string) error { return nil }

func (w *connectorWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}

func (w *connectorWorkspace) ReadFile(context.Context, string, int) ([]byte, error) {
	return nil, nil
}

func (w *connectorWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

// --- harness ----------------------------------------------------------------

// newConnectorHarness mounts the screen with a settings store (and optional
// plugins service). KeySettings is provided after activation because the module
// reads every service lazily, before the first frame.
func newConnectorHarness(t *testing.T, service contracts.PluginsService,
	values map[string]string) (*harness, *connectorSettings) {
	t.Helper()
	h := newHarness(t, service, nil, plugins.Config{})
	store := newConnectorSettings(values)
	provide[contracts.SettingsService](t, h.ctx, contracts.KeySettings, store)
	return h, store
}

func openConnectorTab(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	tester.Frame()
	h.click(tester, "连接器")
}

// scrollConnectorTo brings label's control into the scrolled tab body before a
// click (the options disclosure and the form footer can sit below the fold).
func scrollConnectorTo(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	tester.Frame()
	if rect, ok := tester.Find(label); ok && rect.W > 0 && rect.H > 0 {
		return
	}
	for i := 0; i < 10; i++ {
		tester.Scroll(600, 400, 0, 240)
		tester.Frame()
		if rect, ok := tester.Find(label); ok && rect.W > 0 && rect.H > 0 {
			return
		}
	}
}

// fillConnectorInput replaces one input's value by its composite label
// ("名称 输入"). The inputs live in a modal, so no page scroll is involved once
// the field exists.
func fillConnectorInput(t *testing.T, h *harness, tester *ui.Tester, label, value string) {
	t.Helper()
	scrollConnectorTo(t, tester, label)
	rect, ok := tester.Find(label)
	if !ok {
		t.Fatalf("input %q not found; texts = %v", label, tester.Texts())
	}
	if rect.W <= 0 || rect.H <= 0 {
		t.Fatalf("input %q has no visible area: %+v", label, rect)
	}
	tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Frame()
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	if value != "" {
		tester.Type(value)
	}
	tester.Frame()
}

// chooseConnectorTransport opens the transport selector and chooses option.
func chooseConnectorTransport(t *testing.T, h *harness, tester *ui.Tester, option string) {
	t.Helper()
	h.click(tester, "传输 输入")
	if err := tester.Click(option); err != nil {
		t.Fatalf("choose transport %q: %v; texts = %v", option, err, tester.Texts())
	}
	tester.Frame()
}

// useConnectorTester swaps the connection-test seam for one test.
func useConnectorTester(t *testing.T, fn func(context.Context, contracts.MCPServerConfig) error) {
	t.Helper()
	previous := plugins.MCPConnectionTester
	plugins.MCPConnectionTester = fn
	t.Cleanup(func() { plugins.MCPConnectionTester = previous })
}

// storedConnectorRows decodes the written mcp.servers row.
func storedConnectorRows(t *testing.T, store *connectorSettings) []contracts.MCPServerConfig {
	t.Helper()
	rows, err := contracts.ParseMCPServersJSON(store.get(contracts.SettingMCPServers))
	if err != nil {
		t.Fatalf("stored MCP JSON is malformed: %v (%q)", err, store.get(contracts.SettingMCPServers))
	}
	return rows
}

// waitForConnectorText drives frames until an async result is visible.
func waitForConnectorText(t *testing.T, tester *ui.Tester, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		tester.Frame()
		if tester.HasText(want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q; texts = %v", want, tester.Texts())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// assertConnectorExactTextCount checks how many text nodes equal want exactly.
func assertConnectorExactTextCount(t *testing.T, tester *ui.Tester, want string, count int) {
	t.Helper()
	got := 0
	for _, text := range tester.Texts() {
		if text == want {
			got++
		}
	}
	if got != count {
		t.Fatalf("%q shows %d times, want %d; texts = %v", want, got, count, tester.Texts())
	}
}

// --- empty and degraded states ----------------------------------------------

func TestConnectorTabWithoutSettingsShowsEmptyState(t *testing.T) {
	// No settings store: the tab gives the explicit empty state and the add
	// action cannot open a form that would have nowhere to save.
	h := newHarness(t, nil, nil, plugins.Config{})
	tester := h.tester()
	h.click(tester, "连接器")

	h.assertTexts(tester, "设置存储未启用", "添加连接器")
	if err := tester.Click("添加连接器"); err == nil {
		tester.Frame()
	}
	h.assertNoTexts(tester, "名称 输入", "测试连接")
}

func TestConnectorTabWithoutPluginsStillManagesSavedConfig(t *testing.T) {
	h, _ := newConnectorHarness(t, nil, map[string]string{
		contracts.SettingMCPServers: connectorFilesJSON,
	})
	tester := h.tester()
	h.click(tester, "连接器")

	// The configuration stays manageable; only the live state is unavailable.
	h.assertTexts(tester, "连接器服务未启用", "files", "http://127.0.0.1:9000/mcp",
		"HTTP", "待重启", "启用连接器 files")
	h.assertNoTexts(tester, "设置存储未启用")
}

// TestConnectorMalformedStoredJSONIsReadOnly pins the parse-failure state: the
// error is visible, the grid draws no cards and the add action stays disabled
// (writing would silently overwrite configuration the user may need to repair).
func TestConnectorMalformedStoredJSONIsReadOnly(t *testing.T) {
	raw := `{"not":"an-array"}`
	h, store := newConnectorHarness(t, &connectorPlugins{}, map[string]string{
		contracts.SettingMCPServers: raw,
	})
	tester := h.tester()
	h.click(tester, "连接器")

	h.assertTexts(tester, "配置解析失败")
	h.assertNoTexts(tester, "MCP 连接器列表")
	if err := tester.Click("添加连接器"); err == nil {
		tester.Frame()
	}
	h.assertNoTexts(tester, "名称 输入")
	if got := store.get(contracts.SettingMCPServers); got != raw {
		t.Fatalf("malformed configuration was rewritten: %q", got)
	}
}

// --- the live cards ---------------------------------------------------------

func TestConnectorLiveCardsRender(t *testing.T) {
	service := &connectorPlugins{mcp: []contracts.MCPPlugin{
		{
			Name: "files", URL: "http://127.0.0.1:9000/mcp", ServerName: "fake-files",
			Version: "0.1.0", Protocol: "2024-11-05", Tools: 3, Status: "已连接",
		},
		{
			Name: "broken", URL: "http://127.0.0.1:9/mcp",
			Status: "连接失败: dial tcp 127.0.0.1:9: connection refused",
		},
	}}
	h, _ := newConnectorHarness(t, service, nil)
	tester := h.tester()
	h.click(tester, "连接器")

	h.assertTexts(tester,
		"2 个连接器",
		"files", "已连接", "3 个工具", "http://127.0.0.1:9000/mcp",
		"HTTP", "启用连接器 files",
		"broken", "连接失败",
		"查看报错详情", "重试", "无法连接到服务器")
	// The raw backend error stays in the tooltip/details, never in the card.
	h.assertNoTexts(tester, "dial tcp")
}

func TestConnectorEnableSwitchWritesDisabledServers(t *testing.T) {
	service := &connectorPlugins{mcp: []contracts.MCPPlugin{
		{Name: "srv", URL: "http://127.0.0.1:9/mcp", Status: "已连接", Tools: 2},
	}}
	h, store := newConnectorHarness(t, service, nil)
	tester := h.tester()
	h.click(tester, "连接器")

	h.click(tester, "启用连接器 srv")
	if got := store.get(contracts.SettingMCPDisabledServers); got != `["srv"]` {
		t.Fatalf("%s = %q, want [\"srv\"]", contracts.SettingMCPDisabledServers, got)
	}
	h.assertTexts(tester, "已停用", "已停用（重启后断开）")

	h.click(tester, "启用连接器 srv")
	if got := store.get(contracts.SettingMCPDisabledServers); got != "[]" {
		t.Fatalf("%s after enable = %q, want []", contracts.SettingMCPDisabledServers, got)
	}
}

func TestConnectorCardsMergeStoredRowsWithLiveState(t *testing.T) {
	// One stored row and one live-only assembly row: same-name live state merges
	// into the stored card instead of drawing a second card.
	service := &connectorPlugins{mcp: []contracts.MCPPlugin{
		{Name: "files", URL: "http://127.0.0.1:9000/mcp", Tools: 4, Status: "已连接"},
		{Name: "assembly", URL: "https://mcp.example.com/assembly", Tools: 1, Status: "已连接"},
	}}
	h, _ := newConnectorHarness(t, service, map[string]string{
		contracts.SettingMCPServers: connectorFilesJSON,
	})
	tester := h.tester()
	h.click(tester, "连接器")

	h.assertTexts(tester, "2 个连接器", "files", "assembly", "已连接", "4 个工具")
	assertConnectorExactTextCount(t, tester, "http://127.0.0.1:9000/mcp", 1)

	// A live-only server has details but no edit/remove: it is not in settings.
	h.click(tester, "连接器操作 assembly")
	h.assertTexts(tester, "详情")
	h.assertNoTexts(tester, "编辑", "移除")
	tester.Key(0, ui.KeyEscape)
	tester.Frame()

	h.click(tester, "连接器操作 files")
	h.assertTexts(tester, "详情", "编辑", "移除")
}

func TestConnectorRestoresDisabledAndPendingRestartRows(t *testing.T) {
	// The toolkit skips disabled servers at activation; the stored rows must
	// still show, saying why they are absent, and a stored row with no live
	// state connects after the next restart.
	h, _ := newConnectorHarness(t, &connectorPlugins{}, map[string]string{
		contracts.SettingMCPServers: `[{"name":"linear","url":"https://mcp.linear.app/mcp"},` +
			`{"name":"workspace-docs","url":"http://127.0.0.1:9010/mcp"}]`,
		contracts.SettingMCPDisabledServers: `["linear"]`,
	})
	tester := h.tester()
	h.click(tester, "连接器")

	h.assertTexts(tester, "2 个连接器", "linear", "已停用", "workspace-docs", "待重启")
	h.assertTexts(tester, "https://mcp.linear.app/mcp", "http://127.0.0.1:9010/mcp")
}

// --- add and edit ----------------------------------------------------------

func TestConnectorAddHTTPWritesSettings(t *testing.T) {
	h, store := newConnectorHarness(t, &connectorPlugins{}, nil)
	tester := h.tester()
	h.click(tester, "连接器")

	h.assertTexts(tester, "还没有配置 MCP 服务器", "添加连接器")
	h.click(tester, "添加连接器")
	h.assertTexts(tester, "添加连接器", "名称 输入", "URL 输入", "超时 (ms) 输入",
		"测试连接", "取消", "添加")

	fillConnectorInput(t, h, tester, "名称 输入", "files")
	fillConnectorInput(t, h, tester, "URL 输入", "https://mcp.example.com/mcp")
	h.click(tester, "添加")

	want := `[{"name":"files","url":"https://mcp.example.com/mcp"}]`
	if got := store.get(contracts.SettingMCPServers); got != want {
		t.Fatalf("stored MCP servers = %q, want %q", got, want)
	}
	h.assertTexts(tester, "已保存，重启生效", "files", "https://mcp.example.com/mcp",
		"待重启", "0 个工具")
	h.assertNoTexts(tester, "名称 输入")
}

func TestConnectorAddStdioWritesTransportFields(t *testing.T) {
	h, store := newConnectorHarness(t, &connectorPlugins{}, nil)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "添加连接器")

	// Switching the transport reveals the stdio subprocess fields.
	chooseConnectorTransport(t, h, tester, "STDIO")
	h.assertTexts(tester, "命令 输入", "参数 输入", "环境变量 输入")
	h.assertNoTexts(tester, "URL 输入")

	fillConnectorInput(t, h, tester, "名称 输入", "本地服务器")
	fillConnectorInput(t, h, tester, "命令 输入", "npx")
	fillConnectorInput(t, h, tester, "参数 输入", "-y server-github")
	fillConnectorInput(t, h, tester, "环境变量 输入", "TOKEN=abc")
	h.click(tester, "添加")

	rows := storedConnectorRows(t, store)
	if len(rows) != 1 {
		t.Fatalf("stored rows = %+v, want one stdio row", rows)
	}
	row := rows[0]
	if row.Name != "本地服务器" || row.Transport != "stdio" || row.Command != "npx" {
		t.Fatalf("stored row = %+v, want stdio/npx", row)
	}
	if len(row.Args) != 2 || row.Args[0] != "-y" || row.Args[1] != "server-github" {
		t.Fatalf("stored args = %v, want [-y server-github]", row.Args)
	}
	if len(row.Env) != 1 || row.Env[0] != "TOKEN=abc" {
		t.Fatalf("stored env = %v, want [TOKEN=abc]", row.Env)
	}
}

func TestConnectorEditServerInPlace(t *testing.T) {
	h, store := newConnectorHarness(t, &connectorPlugins{}, map[string]string{
		contracts.SettingMCPServers: `[{"name":"srv","url":"http://127.0.0.1:9/mcp"}]`,
	})
	tester := h.tester()
	h.click(tester, "连接器")

	h.click(tester, "连接器操作 srv")
	h.click(tester, "编辑")
	h.assertTexts(tester, "编辑连接器", "保存", "取消")

	// Editing may rename the connector; it stays one row, saved in place.
	fillConnectorInput(t, h, tester, "名称 输入", "srv-renamed")
	fillConnectorInput(t, h, tester, "URL 输入", "http://127.0.0.1:10/mcp")
	h.click(tester, "保存")

	rows := storedConnectorRows(t, store)
	if len(rows) != 1 || rows[0].Name != "srv-renamed" || rows[0].URL != "http://127.0.0.1:10/mcp" {
		t.Fatalf("stored rows = %+v, want the edited row only", rows)
	}
	h.assertTexts(tester, "已保存，重启生效", "srv-renamed")
	h.assertNoTexts(tester, "编辑连接器")
}

func TestConnectorFormRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name     string
		stored   string
		stdio    bool
		drafts   map[string]string
		wantText string
	}{
		{name: "empty name", drafts: map[string]string{"URL 输入": "https://mcp.example.com/mcp"},
			wantText: "名称不能为空"},
		{name: "empty url", drafts: map[string]string{"名称 输入": "files"},
			wantText: "URL 不能为空"},
		{name: "invalid url", drafts: map[string]string{"名称 输入": "files", "URL 输入": "ftp://mcp.example.com"},
			wantText: "URL 无效"},
		{name: "invalid timeout", drafts: map[string]string{"名称 输入": "files",
			"URL 输入": "https://mcp.example.com/mcp", "超时 (ms) 输入": "soon"},
			wantText: "超时必须是整数毫秒"},
		{name: "duplicate url", stored: connectorFilesJSON,
			drafts:   map[string]string{"名称 输入": "other", "URL 输入": "http://127.0.0.1:9000/mcp"},
			wantText: "该 URL 已存在"},
		{name: "duplicate name", stored: connectorFilesJSON,
			drafts:   map[string]string{"名称 输入": "files", "URL 输入": "https://other.example.com/mcp"},
			wantText: "该名称已存在"},
		{name: "invalid env", stdio: true,
			drafts:   map[string]string{"名称 输入": "坏环境", "命令 输入": "npx", "环境变量 输入": "TOKEN"},
			wantText: "环境变量格式无效（第 1 行）"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]string{}
			if tc.stored != "" {
				values[contracts.SettingMCPServers] = tc.stored
			}
			h, store := newConnectorHarness(t, &connectorPlugins{}, values)
			tester := h.tester()
			h.click(tester, "连接器")
			h.click(tester, "添加连接器")
			if tc.stdio {
				chooseConnectorTransport(t, h, tester, "STDIO")
			}
			for label, value := range tc.drafts {
				fillConnectorInput(t, h, tester, label, value)
			}
			before := store.get(contracts.SettingMCPServers)
			h.click(tester, "添加")

			h.assertTexts(tester, tc.wantText)
			if got := store.get(contracts.SettingMCPServers); got != before {
				t.Fatalf("invalid input wrote the store: %q → %q", before, got)
			}
		})
	}
}

func TestConnectorSaveFailureRollsBack(t *testing.T) {
	h, store := newConnectorHarness(t, &connectorPlugins{}, nil)
	store.setErr = errors.New("disk is read-only")
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "添加连接器")
	fillConnectorInput(t, h, tester, "名称 输入", "files")
	fillConnectorInput(t, h, tester, "URL 输入", "https://mcp.example.com/mcp")
	h.click(tester, "添加")

	h.assertTexts(tester, "保存失败：disk is read-only")
	if got := store.get(contracts.SettingMCPServers); got != "" {
		t.Fatalf("failed save wrote the store: %q", got)
	}
	// The draft stays open so the user can fix or retry the save.
	h.assertTexts(tester, "名称 输入")
}

// --- async connection test --------------------------------------------------

func TestConnectorConnectionTestIsAsyncAndDoesNotSave(t *testing.T) {
	h, store := newConnectorHarness(t, &connectorPlugins{}, nil)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "添加连接器")
	fillConnectorInput(t, h, tester, "名称 输入", "probe")
	fillConnectorInput(t, h, tester, "URL 输入", "http://127.0.0.1:9/mcp")

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	useConnectorTester(t, func(context.Context, contracts.MCPServerConfig) error {
		once.Do(func() { close(started) })
		<-release
		return nil
	})

	h.click(tester, "测试连接")
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the connection test did not start")
	}
	// The frame keeps rendering while the test is in flight.
	h.assertTexts(tester, "正在测试…")
	if got := store.get(contracts.SettingMCPServers); got != "" {
		t.Fatalf("testing saved the draft: %q", got)
	}
	close(release)
	waitForConnectorText(t, tester, "连接测试通过")
	h.assertNoTexts(tester, "正在测试…")
	if got := store.get(contracts.SettingMCPServers); got != "" {
		t.Fatalf("testing saved the draft after finishing: %q", got)
	}
}

func TestConnectorConnectionTestLocalizesKnownCause(t *testing.T) {
	h, store := newConnectorHarness(t, &connectorPlugins{}, nil)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "添加连接器")
	useConnectorTester(t, func(context.Context, contracts.MCPServerConfig) error {
		return errors.New("dial tcp 127.0.0.1:9: connect: connection refused")
	})
	fillConnectorInput(t, h, tester, "名称 输入", "probe")
	fillConnectorInput(t, h, tester, "URL 输入", "http://127.0.0.1:9/mcp")
	h.click(tester, "测试连接")
	waitForConnectorText(t, tester, "无法连接到服务器")
	h.assertNoTexts(tester, "dial tcp")
	if got := store.get(contracts.SettingMCPServers); got != "" {
		t.Fatalf("testing saved the draft: %q", got)
	}
}

// --- details -----------------------------------------------------------------

func TestConnectorDetailsModalShowsMaskedConfigAndRawError(t *testing.T) {
	service := &connectorPlugins{mcp: []contracts.MCPPlugin{{
		Name: "srv", ServerName: "fake", Version: "1.2.3", Protocol: "2024-11-05",
		Tools:  2,
		Status: "连接失败: mcp: initialize: mcp: http request: dial tcp 127.0.0.1:9: connect: connection refused",
	}}}
	h, _ := newConnectorHarness(t, service, map[string]string{
		contracts.SettingMCPServers: connectorStdioJSON,
	})
	tester := h.tester()
	h.click(tester, "连接器")

	h.click(tester, "连接器操作 srv")
	h.click(tester, "详情")
	// The subtitle is a purpose line; the transport lives once, in the 类型 row.
	h.assertTexts(tester, "外部 MCP 服务配置与状态")
	h.assertTexts(tester, "类型", "stdio", "命令", "npx", "-y server-github",
		"环境变量", "TOKEN=*** · 值已隐藏", "超时", "250 ms",
		"服务器", "fake", "版本", "1.2.3", "协议", "2024-11-05", "工具数", "2",
		"状态", "连接失败")
	// The localized cause leads (the chain's "initialize" classifies as a
	// handshake failure); the raw backend chain stays folded until the user
	// asks for it, and the secret env value is masked.
	h.assertTexts(tester, "协议握手失败（MCP 初始化未完成）", "原始错误")
	h.assertNoTexts(tester, "dial tcp 127.0.0.1:9", "TOKEN=abc")
	h.click(tester, "原始错误")
	// Expanded: the chain is selectable and the repeated "mcp:" wrappers are
	// stripped (one honest chain, not a prefix pile).
	h.assertTexts(tester, "dial tcp 127.0.0.1:9")
	h.assertNoTexts(tester, "mcp:")
	h.click(tester, "关闭")
	h.assertNoTexts(tester, "250 ms")
}

// --- removal -----------------------------------------------------------------

func TestConnectorRemoveConfirmation(t *testing.T) {
	h, store := newConnectorHarness(t, &connectorPlugins{}, map[string]string{
		contracts.SettingMCPServers: connectorTwoJSON,
	})
	tester := h.tester()
	h.click(tester, "连接器")

	h.click(tester, "连接器操作 files")
	h.click(tester, "移除")
	h.assertTexts(tester, "移除 MCP 连接器", "确认移除「files」？此操作无法撤销。",
		"移除连接器", "取消")

	// Esc cancels the destructive action: nothing is written, the card stays.
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if got := store.get(contracts.SettingMCPServers); got != connectorTwoJSON {
		t.Fatalf("Esc wrote the store: %q", got)
	}
	h.assertTexts(tester, "files")

	// Confirming the dialog drops the row and writes the rest back.
	h.click(tester, "连接器操作 files")
	h.click(tester, "移除")
	h.click(tester, "移除连接器")
	want := `[{"name":"web","url":"https://mcp.example.com/mcp"}]`
	if got := store.get(contracts.SettingMCPServers); got != want {
		t.Fatalf("stored MCP servers = %q, want %q", got, want)
	}
	h.assertTexts(tester, "已保存，重启生效", "web")
	h.assertNoTexts(tester, "files")
}

// --- workspace mcp.json opt-in ----------------------------------------------

func TestConnectorWorkspaceOptInWritesSetting(t *testing.T) {
	root := t.TempDir()
	h, store := newConnectorHarness(t, &connectorPlugins{}, nil)
	provide[contracts.WorkspaceService](t, h.ctx, contracts.KeyWorkspace, &connectorWorkspace{root: root})
	tester := h.tester()
	h.click(tester, "连接器")

	h.assertTexts(tester, "连接器选项")
	h.click(tester, "连接器选项")
	h.assertTexts(tester, "读取工作区 mcp.json")
	h.assertTexts(tester, filepath.Join(root, "mcp.json"))

	if got := store.get(contracts.SettingMCPWorkspaceOptIn); got != "" {
		t.Fatalf("default opt-in = %q, want unset/off", got)
	}
	h.click(tester, "启用工作区 mcp.json")
	if got := store.get(contracts.SettingMCPWorkspaceOptIn); got != "true" {
		t.Fatalf("opt-in after enable = %q, want true", got)
	}
	h.click(tester, "启用工作区 mcp.json")
	if got := store.get(contracts.SettingMCPWorkspaceOptIn); got != "false" {
		t.Fatalf("opt-in after disable = %q, want false", got)
	}
}

// --- refresh and i18n --------------------------------------------------------

func TestConnectorRefreshUpdatesCards(t *testing.T) {
	service := &connectorPlugins{
		mcp:       []contracts.MCPPlugin{{Name: "old", Status: "已连接", Tools: 1}},
		refreshed: []contracts.MCPPlugin{{Name: "new", Status: "已连接", Tools: 2}},
	}
	h, _ := newConnectorHarness(t, service, nil)
	tester := h.tester()
	h.click(tester, "连接器")
	h.assertTexts(tester, "old")

	h.click(tester, "刷新连接")
	waitForConnectorText(t, tester, "new")
	h.assertTexts(tester, "已刷新 1 个 MCP 连接器", "2 个工具")
	h.assertNoTexts(tester, "old")
}

func TestConnectorEnglishCopy(t *testing.T) {
	service := &connectorPlugins{mcp: []contracts.MCPPlugin{
		{Name: "files", URL: "http://127.0.0.1:9000/mcp", Tools: 2, Status: "已连接"},
	}}
	h, _ := newConnectorHarness(t, service, map[string]string{
		contracts.SettingMCPServers: connectorFilesJSON,
	})
	tester := h.tester()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.click(tester, "Connectors")

	h.assertTexts(tester, "1 connector", "Add connector", "Reconnect",
		"Connected", "Enable connector files", "Connector actions files",
		"Connector options")
	h.assertNoTexts(tester, "添加连接器", "已连接", "连接器选项")

	// The form and the remove confirmation are localized too.
	h.click(tester, "Connector actions files")
	h.assertTexts(tester, "Details", "Edit", "Remove")
	h.click(tester, "Edit")
	h.assertTexts(tester, "Edit connector", "Name input", "URL input", "Test connection",
		"Cancel", "Save")
	h.click(tester, "Cancel")

	h.click(tester, "Connector actions files")
	h.click(tester, "Remove")
	h.assertTexts(tester, "Remove MCP connector", "Remove connector", "Cancel")
}

// TestConnectorEnglishEmptyState covers the degraded no-plugin copy in English.
func TestConnectorEnglishEmptyState(t *testing.T) {
	h, _ := newConnectorHarness(t, nil, map[string]string{
		contracts.SettingMCPServers: connectorFilesJSON,
	})
	tester := h.tester()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.click(tester, "Connectors")
	h.assertTexts(tester, "Connector service not enabled",
		"Restart needed", "saved configuration only")
}
