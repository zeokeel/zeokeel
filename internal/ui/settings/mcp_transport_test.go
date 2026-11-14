package settings_test

// TDD tests (RED first) for the MCP page's transport support: HTTP keeps the
// URL form, stdio adds command/args/env, the list shows a transport badge and
// the stored JSON uses contracts.MCPServerConfig.

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

const mcpTransportJSON = `[{"name":"http-srv","url":"http://127.0.0.1:9000/mcp"},{"name":"stdio-srv","transport":"stdio","command":"npx","args":["-y","server-github"],"env":["TOKEN=abc"]}]`

// selectStdioTransport switches the add form to the stdio transport.
func selectStdioTransport(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	h.click(tester, "HTTP")
	h.click(tester, "STDIO")
}

// parseStoredMCPRows decodes the stored setting for assertions.
func parseStoredMCPRows(t *testing.T, store *fakeSettings) []contracts.MCPServerConfig {
	t.Helper()
	rows, err := contracts.ParseMCPServersJSON(store.get(contracts.SettingMCPServers))
	if err != nil {
		t.Fatalf("stored MCP JSON is malformed: %v", err)
	}
	return rows
}

// --- tests ------------------------------------------------------------------

func TestMCPTransportListShowsTypeAndAddress(t *testing.T) {
	store := newFakeSettings(map[string]string{contracts.SettingMCPServers: mcpTransportJSON})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)

	h.assertTexts(tester, "http-srv", "http://127.0.0.1:9000/mcp", "HTTP",
		"stdio-srv", "stdio", "npx", "-y server-github")
	if got := store.get(contracts.SettingMCPServers); got != mcpTransportJSON {
		t.Fatalf("loading rewrote the stored JSON: %s", got)
	}
}

func TestMCPAddStdioServerWritesTransportFields(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)

	selectStdioTransport(t, h, tester)
	h.assertTexts(tester, "命令", "参数", "环境变量")

	clearAndType(t, h, tester, "名称 输入", "本地服务器")
	clearAndType(t, h, tester, "命令 输入", "npx")
	clearAndType(t, h, tester, "参数 输入", "-y server-github")
	clearAndType(t, h, tester, "环境变量 输入", "TOKEN=abc")
	h.click(tester, "添加")

	rows := parseStoredMCPRows(t, store)
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
	h.assertTexts(tester, "已保存")
}

func TestMCPTransportSwitchShowsMatchingFields(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)
	h.assertTexts(tester, "URL 输入")

	selectStdioTransport(t, h, tester)
	h.assertTexts(tester, "命令 输入", "参数 输入", "环境变量 输入")
	h.assertNoTexts(tester, "URL 输入")

	// Switch back to HTTP: the URL field returns and the stdio fields hide.
	h.click(tester, "STDIO")
	h.click(tester, "HTTP")
	h.assertTexts(tester, "URL 输入")
	h.assertNoTexts(tester, "命令 输入")
}

func TestMCPHTTPAddStillWorks(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)

	typeMCPForm(t, h, tester, "files", "https://mcp.example.com/mcp")
	h.click(tester, "添加")
	rows := parseStoredMCPRows(t, store)
	if len(rows) != 1 || rows[0].Transport == "stdio" || rows[0].URL != "https://mcp.example.com/mcp" {
		t.Fatalf("stored rows = %+v, want one HTTP row", rows)
	}
}

func TestMCPTransportValidationDoesNotWrite(t *testing.T) {
	cases := []struct {
		name     string
		stored   string
		drafts   [4]string // name, command, args, env
		wantText string
	}{
		{
			name:     "empty command",
			drafts:   [4]string{"本地", "", "-y srv", ""},
			wantText: "命令不能为空",
		},
		{
			name:     "NUL command",
			drafts:   [4]string{"本地", "bad\x00cmd", "", ""},
			wantText: "命令不能包含 NUL",
		},
		{
			name:     "duplicate stdio",
			stored:   `[{"name":"existing","transport":"stdio","command":"npx","args":["-y","srv"]}]`,
			drafts:   [4]string{"另一个", "npx", "-y srv", ""},
			wantText: "该服务器已存在",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]string{}
			if tc.stored != "" {
				values[contracts.SettingMCPServers] = tc.stored
			}
			store := newFakeSettings(values)
			h := newHarness(t, &fakeChat{}, store, settings.Config{})
			tester := h.tester()
			tester.Frame()
			openMCPPage(t, h, tester)
			before := store.get(contracts.SettingMCPServers)

			selectStdioTransport(t, h, tester)
			clearAndType(t, h, tester, "名称 输入", tc.drafts[0])
			clearAndType(t, h, tester, "命令 输入", tc.drafts[1])
			clearAndType(t, h, tester, "参数 输入", tc.drafts[2])
			clearAndType(t, h, tester, "环境变量 输入", tc.drafts[3])
			h.click(tester, "添加")

			h.assertTexts(tester, tc.wantText)
			if got := store.get(contracts.SettingMCPServers); got != before {
				t.Fatalf("invalid stdio input wrote the store: %q → %q", before, got)
			}
		})
	}
}

func TestMCPTransportSaveErrorRollsBack(t *testing.T) {
	store := newFakeSettings(nil)
	store.setErr = errors.New("disk is read-only")
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)

	selectStdioTransport(t, h, tester)
	clearAndType(t, h, tester, "名称 输入", "本地")
	clearAndType(t, h, tester, "命令 输入", "npx")
	h.click(tester, "添加")
	h.assertTexts(tester, "保存失败：disk is read-only")
	if got := store.get(contracts.SettingMCPServers); got != "" {
		t.Fatalf("failed save wrote the store: %q", got)
	}
}

func TestMCPTransportEnglishCopy(t *testing.T) {
	// The list copy with transport badges and stdio addresses.
	store := newFakeSettings(map[string]string{contracts.SettingMCPServers: mcpTransportJSON})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.click(tester, "MCP")
	h.assertTexts(tester, "HTTP", "stdio", "npx -y server-github")

	// The transport-aware add form: a fresh (empty) list keeps the selector
	// unambiguous in the frame.
	empty := newFakeSettings(nil)
	h2 := newHarness(t, &fakeChat{}, empty, settings.Config{})
	tester2 := h2.tester()
	tester2.Frame()
	h2.uiRoot.Locale().SetLanguage("en")
	tester2.Frame()
	h2.click(tester2, "MCP")
	selectStdioTransport(t, h2, tester2)
	h2.assertTexts(tester2, "Command", "Arguments", "Environment")
}

// TestMCPEnvRowsKeepSpacesAndSplitOnNewlines pins E-16: the environment draft is
// one KEY=VALUE per line, so a value with a space stays whole and each line is a
// separate entry (the old strings.Fields split tore "TOKEN=a b" apart).
func TestMCPEnvRowsKeepSpacesAndSplitOnNewlines(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)
	selectStdioTransport(t, h, tester)

	clearAndType(t, h, tester, "名称 输入", "本地服务器")
	clearAndType(t, h, tester, "命令 输入", "npx")
	h.focusInput(tester, "环境变量 输入")
	tester.Type("TOKEN=a b")
	tester.Key(0, ui.KeyEnter)
	tester.Type("FOO=bar")
	tester.Frame()
	h.click(tester, "添加")

	rows := parseStoredMCPRows(t, store)
	if len(rows) != 1 {
		t.Fatalf("stored rows = %+v, want one row", rows)
	}
	env := rows[0].Env
	if len(env) != 2 || env[0] != "TOKEN=a b" || env[1] != "FOO=bar" {
		t.Fatalf("stored env = %v, want [TOKEN=a b FOO=bar]", env)
	}
}

// TestMCPEnvInvalidRowShowsFieldErrorAndSavesNothing pins the per-line
// KEY=VALUE validation: a malformed row is reported next to the field and the
// store is left untouched.
func TestMCPEnvInvalidRowShowsFieldErrorAndSavesNothing(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)
	selectStdioTransport(t, h, tester)

	clearAndType(t, h, tester, "名称 输入", "坏环境")
	clearAndType(t, h, tester, "命令 输入", "npx")
	clearAndType(t, h, tester, "环境变量 输入", "TOKEN")
	h.click(tester, "添加")

	h.assertTexts(tester, "环境变量格式无效（第 1 行）", "KEY=VALUE")
	if got := store.get(contracts.SettingMCPServers); got != "" {
		t.Fatalf("invalid env row wrote the store: %q", got)
	}
}
