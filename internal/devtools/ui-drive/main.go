// Command ui-drive is a headless interaction driver for ZeoKeel: it boots the
// real desktop assembly (scripted OpenAI-compatible model server, settings,
// workspace, data dir) and drives every surface the way a user would — clicks,
// typing, keyboard shortcuts, dialogs, inspectors, window sizes — recording a
// screenshot and a PASS/FAIL verdict for each step.
//
// It is a development tool: it links the product UI, not a special test build.
// The output is .cache/fix-drive/drive-report.md (feature matrix, failures,
// screenshot index) plus one PNG per step under .cache/fix-drive/shots/.
//
// Usage:
//
//	go run ./internal/devtools/ui-drive                 # full baseline run
//	go run ./internal/devtools/ui-drive --quick         # skip streaming/approval waits
//	go run ./internal/devtools/ui-drive --out DIR       # output directory
//	go run ./internal/devtools/ui-drive --only a,b      # run step-id prefixes
//
// Running it a second time in the same --out directory produces a comparison
// section against the previous run (baseline vs. fixed).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	// The framework's own zh-Hans catalog: the snapshot/driver mirrors
	// the product's language into mygo (host does the same import), so link
	// the same translations or framework chrome would stay English.
	_ "github.com/egoist/mygo/locales/zh-Hans"
	cordis "github.com/zeokeel/zeokeel-kernel"
	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/assembly"
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// stepStatus is the verdict of one driven interaction.
const (
	statusPass    = "PASS"
	statusFail    = "FAIL"
	statusWarn    = "WARN"
	statusBlocked = "BLOCKED"
)

// stepResult is one driven interaction and its evidence, also persisted to
// results.json so a later run can diff against this one.
type stepResult struct {
	ID         string   `json:"id"`
	Category   string   `json:"category"`
	Name       string   `json:"name"`
	Expected   string   `json:"expected,omitempty"`
	Status     string   `json:"status"`
	Details    []string `json:"details,omitempty"`
	Shots      []string `json:"shots,omitempty"`
	Texts      []string `json:"texts,omitempty"` // frame texts at failure (bounded)
	DurationMS int64    `json:"durationMs"`
}

// loaderCloser is the part of the kernel module loader the driver closes.
type loaderCloser interface{ Close() error }

// drive is one booted desktop product plus the driver state.
type drive struct {
	outDir   string
	shotsDir string
	quick    bool

	tester *ui.Tester
	uiRoot *uiroot.Root
	root   cordis.Context
	loader loaderCloser
	server *scriptServer

	dataDir           string
	workspaceRoot     string
	sessionID         string
	originalSessionID string

	results    []*stepResult
	prev       map[string]*stepResult
	onlyFilter []string

	w, h int

	// localeTag is the mygo framework locale last pushed into the Tester: the
	// real desktop host mirrors ui.language into mygo.App.SetLocale, so the
	// headless driver must do the same or the framework's built-in chrome
	// (markdown Copy, divider names) would not follow the product language.
	localeTag string
}

func main() {
	outDir := flag.String("out", ".cache/fix-drive", "output directory for shots and the report")
	quick := flag.Bool("quick", false, "skip waits that need the scripted stream/approval flow")
	only := flag.String("only", "", "comma-separated step-id prefixes to run (debugging)")
	flag.Parse()

	d, err := newDrive(*outDir, *quick)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ui-drive: boot: %v\n", err)
		os.Exit(1)
	}
	defer d.close()

	d.onlyFilter = splitFilter(*only)
	d.loadPrevious()
	d.run(*only)
	d.writeReport()
}

// newDrive creates the output directories and boots the desktop product.
func newDrive(outDir string, quick bool) (*drive, error) {
	shotsDir := filepath.Join(outDir, "shots")
	if err := os.MkdirAll(shotsDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", shotsDir, err)
	}
	dataDir, err := os.MkdirTemp("", "zeokeel-drive")
	if err != nil {
		return nil, fmt.Errorf("temp data dir: %w", err)
	}
	d := &drive{outDir: outDir, shotsDir: shotsDir, quick: quick, dataDir: dataDir, w: 1280, h: 800}
	d.onlyFilter = nil
	if err := d.boot(); err != nil {
		_ = os.RemoveAll(dataDir)
		return nil, err
	}
	if err := d.seed(); err != nil {
		d.close()
		return nil, fmt.Errorf("seed: %w", err)
	}
	return d, nil
}

// boot assembles the same desktop product the process host assembles (mirrors
// internal/devtools/ui-snapshot/main.go).
func (d *drive) boot() error {
	d.server = newScriptServer()
	root := cordis.Root()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	settings := &memSettings{values: map[string]string{
		contracts.SettingsChatModel:   "drive-model",
		contracts.SettingsChatBaseURL: d.server.URL(),
		// A demo MCP server that fails fast: the plugins page shows a
		// diagnostic row whose details expand.
		contracts.SettingMCPServers: `[{"name":"demo-mcp","url":"http://127.0.0.1:1/mcp"}]`,
	}}
	provide(root, contracts.KeyDataDir, d.dataDir)
	provide(root, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(root, contracts.KeyNavigator, uiRoot.Navigator())
	provide(root, contracts.KeyTheme, uiRoot.Theme())
	provide(root, contracts.KeyLocale, uiRoot.Locale())
	provide(root, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide(root, contracts.KeyFileDrop, contracts.FileDropService(uiRoot.FileDrop()))
	provide(root, contracts.KeyUserInteraction, contracts.UserInteraction(uiRoot.UserInteraction()))
	provide(root, contracts.KeyArtifactPresenter, contracts.ArtifactPresenter(uiRoot.ArtifactPresenter()))
	provide(root, contracts.KeyPlanReviewer, contracts.PlanReviewer(uiRoot.PlanReviewer()))
	provide(root, contracts.KeyGoalTracker, contracts.GoalTracker(uiRoot.GoalTracker()))
	provide(root, contracts.KeyNotifier, contracts.Notifier(uiRoot.Notifier()))
	provide(root, contracts.KeySettings, contracts.SettingsService(settings))
	provide(root, contracts.KeyLogger, contracts.Logger(discardLogger{}))
	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductDesktop, assembly.BuildInfo{Version: "0.1.0"})
	if err != nil {
		return fmt.Errorf("load desktop assembly: %w", err)
	}
	d.root = root
	d.uiRoot = uiRoot
	d.loader = loader
	d.tester = ui.NewTester(uiRoot.View, d.w, d.h)
	d.tester.Frame()
	return nil
}

// close tears the product down.
func (d *drive) close() {
	if d.loader != nil {
		_ = d.loader.Close()
	}
	if d.root != (cordis.Context{}) {
		_ = d.root.DisposeAll()
	}
	if d.server != nil {
		d.server.Close()
	}
	if d.dataDir != "" {
		_ = os.RemoveAll(d.dataDir)
	}
	if d.workspaceRoot != "" {
		_ = os.RemoveAll(d.workspaceRoot)
	}
}

// seed creates the durable state the screens need: one conversation, a
// workspace with files and git changes, an automation task, memories and tasks.
func (d *drive) seed() error {
	chat := d.chat()
	sessionID, err := chat.NewSession("驱动会话")
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	d.sessionID = sessionID
	d.originalSessionID = sessionID
	if err := d.uiRoot.Navigator().Push("session"); err != nil {
		return fmt.Errorf("push session: %w", err)
	}
	d.tester.Frame()

	// A workspace with a clean tree, one uncommitted edit and one untracked
	// file: the Files/Terminal/Changes inspector panes all get real content.
	workspaceRoot, err := os.MkdirTemp("", "zeokeel-drive-workspace")
	if err != nil {
		return fmt.Errorf("workspace dir: %w", err)
	}
	d.workspaceRoot = workspaceRoot
	if err := os.WriteFile(filepath.Join(workspaceRoot, "README.md"),
		[]byte("# 驱动工作区\n\n这是 ui-drive 创建的工作区。\n\n- 目录浏览\n- 文本预览\n- 变更 diff\n"), 0o600); err != nil {
		return fmt.Errorf("write README: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "internal"), 0o700); err != nil {
		return fmt.Errorf("mkdir internal: %w", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "internal", "main.go"),
		[]byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		return fmt.Errorf("write main.go: %w", err)
	}
	runGit := func(args ...string) {
		command := exec.Command("git", append([]string{"-C", workspaceRoot}, args...)...)
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=ui-drive", "GIT_AUTHOR_EMAIL=drive@zeokeel.local",
			"GIT_COMMITTER_NAME=ui-drive", "GIT_COMMITTER_EMAIL=drive@zeokeel.local")
		_ = command.Run()
	}
	runGit("init")
	runGit("add", ".")
	runGit("commit", "-m", "initial")
	_ = os.WriteFile(filepath.Join(workspaceRoot, "README.md"),
		[]byte("# 驱动工作区\n\n这是 ui-drive 创建的工作区。\n\n- 目录浏览\n- 文本预览\n- 变更 diff\n- 已修改\n"), 0o600)
	_ = os.WriteFile(filepath.Join(workspaceRoot, "notes.txt"), []byte("临时笔记\n"), 0o600)

	if workspaces, err := d.root.Require(contracts.KeyWorkspace); err == nil {
		if _, createErr := workspaces.Create(context.Background(), "drive-workspace", workspaceRoot); createErr != nil {
			fmt.Printf("ui-drive: seed workspace: %v (continuing)\n", createErr)
		}
	} else {
		fmt.Printf("ui-drive: seed workspace service missing: %v (continuing)\n", err)
	}

	if automation, err := d.root.Require(contracts.KeyAutomation); err == nil {
		_, _ = automation.Create(context.Background(), contracts.AutomationTask{
			Name: "每日构建检查", Prompt: "检查构建是否通过并总结", Schedule: "*/30 * * * *", Enabled: true,
		})
	}
	if memory, err := d.root.Require(contracts.KeyMemory); err == nil {
		_, _ = memory.Add(context.Background(), rig.Memory{Text: "偏好：界面使用中文，回答尽量简洁", Scope: "global"})
		_, _ = memory.Add(context.Background(), rig.Memory{Text: "项目根目录是 /data/mygo", Scope: "workspace"})
	}
	if board, err := d.root.Require(contracts.KeyTaskBoard); err == nil {
		first, _ := board.Create(context.Background(), "发布前检查", "确认文档与截图")
		if first.ID != "" {
			_, _ = board.SetStatus(context.Background(), first.ID, contracts.TaskStatusInProgress)
		}
		_, _ = board.Create(context.Background(), "编写发布说明", "")
	}
	d.tester.Frame()
	return nil
}

// chat requires the chat service.
func (d *drive) chat() contracts.ChatService {
	chat, err := d.root.Require(contracts.KeyChat)
	if err != nil {
		panic(fmt.Sprintf("require chat: %v", err))
	}
	return chat
}

// run executes every step group in product order. The --only filter (stored on
// the drive) narrows which steps run.
func (d *drive) run(string) {
	d.runShellSteps()
	d.runSettingsSteps()
	d.runArchiveSteps()
	d.runPaletteSteps()
	d.runSessionSteps()
	d.runHSessionSteps()
	d.runHNarrowSteps()
	d.runOverlaySteps()
	d.runPanelSteps()
	d.runTeamSteps()
	d.runJobsSteps()
	d.runThemeLanguageSteps()
	d.runSidebarSteps()
	d.runWindowSizeSteps()
	d.runLandingSteps()
	d.runToastSteps()
}

// sanitizeUI returns the transient UI state to a known baseline before each
// step, so a failed step cannot poison the next one: native menus close, the
// palette closes, a leftover run is cancelled and the chat mode is normal.
func (d *drive) sanitizeUI() {
	if d.tester == nil {
		return
	}
	d.tester.CloseMenu()
	d.key(0, ui.KeyEscape)
	d.drain(2)
	if d.has("搜索命令") {
		d.key(0, ui.KeyEscape)
		d.drain(2)
	}
	chat := d.chat()
	if active := chat.ActiveSession(); active != "" {
		if session, ok := chat.Session(active); ok && session.Running {
			_ = chat.Cancel(active)
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				d.frame()
				if current, ok := chat.Session(active); ok && !current.Running {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	}
	if store, ok := d.root.Get(contracts.KeySettings); ok && store != nil {
		_ = store.Set(contracts.SettingAgentMode, contracts.AgentModeNormal)
		_ = store.Set(contracts.SettingChatMode, contracts.ChatModeStandard)
	}
	// Move the headless clock past any toast TTL and running animation the
	// previous step raised, so its screenshots only show the current state.
	d.tester.Advance(6 * time.Second)
	d.drain(2)
}

// step is the driver's single step runner: fn records assertions through the
// check handle; the verdict and its screenshot/text evidence land in results.
func (d *drive) step(id, category, name, expected string, fn func(c *check)) {
	if !d.wantedStep(id) {
		return
	}
	c := &check{d: d, id: id, category: category, name: name, expected: expected, started: time.Now()}
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				c.failf("panic: %v", recovered)
			}
		}()
		d.sanitizeUI()
		fn(c)
	}()
	c.finish()
	d.results = append(d.results, c.result())
}

func (d *drive) wantedStep(id string) bool {
	if d.onlyFilter == nil {
		return true
	}
	for _, prefix := range d.onlyFilter {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}

func splitFilter(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// check is the assertion handle of one step.
type check struct {
	d        *drive
	id       string
	category string
	name     string
	expected string
	started  time.Time

	details []string
	failed  bool
	warned  bool
	skipped string
	shots   []string
	texts   []string
}

func (c *check) result() *stepResult {
	status := statusPass
	switch {
	case c.failed:
		status = statusFail
	case c.skipped != "":
		status = statusBlocked
	case c.warned:
		status = statusWarn
	}
	res := &stepResult{
		ID:         c.id,
		Category:   c.category,
		Name:       c.name,
		Expected:   c.expected,
		Status:     status,
		Details:    append([]string(nil), c.details...),
		Shots:      append([]string(nil), c.shots...),
		DurationMS: time.Since(c.started).Milliseconds(),
	}
	if status == statusFail {
		res.Texts = c.d.textsExcerpt()
	}
	return res
}

// note records an observation shown in the report.
func (c *check) note(format string, args ...any) {
	c.details = append(c.details, fmt.Sprintf(format, args...))
}

// failf marks the step failed with a reason.
func (c *check) failf(format string, args ...any) {
	c.failed = true
	c.details = append(c.details, "FAIL: "+fmt.Sprintf(format, args...))
}

// warnf marks the step as a warning (the interaction worked but not as the
// design says it should).
func (c *check) warnf(format string, args ...any) {
	c.warned = true
	c.details = append(c.details, "WARN: "+fmt.Sprintf(format, args...))
}

// require is a hard assertion: it fails the step when cond is false.
func (c *check) require(cond bool, format string, args ...any) bool {
	if !cond {
		c.failf(format, args...)
	}
	return cond
}

// shot writes a PNG of the current frame into shots/<name>.png.
func (c *check) shot(name string) {
	if name == "" {
		name = c.id
	}
	path := filepath.Join(c.d.shotsDir, name+".png")
	if err := writePNG(path, c.d.tester.Image()); err != nil {
		c.failf("write screenshot %s: %v", path, err)
		return
	}
	c.shots = append(c.shots, filepath.ToSlash(path))
}

// finish records a failure screenshot and the frame texts when the step did not
// pass.
func (c *check) finish() {
	if c.failed && c.id != "" {
		c.shot("fail-" + c.id)
	}
	if c.failed && len(c.texts) == 0 {
		c.texts = c.d.textsExcerpt()
	}
}

// textsExcerpt returns the frame labels bounded for the report.
func (d *drive) textsExcerpt() []string {
	texts := d.tester.Texts()
	if len(texts) > 160 {
		texts = texts[:160]
	}
	out := make([]string, 0, len(texts))
	for _, text := range texts {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		out = append(out, text)
	}
	return out
}

// writePNG writes one frame image to path.
func writePNG(path string, img *image.RGBA) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func provide[T any](root cordis.Context, key cordis.ServiceKey[T], value T) {
	if _, err := root.Provide(key, value); err != nil {
		panic(fmt.Sprintf("provide %s: %v", key.Name(), err))
	}
}

// memSettings is the in-memory settings service the desktop product reads.
type memSettings struct{ values map[string]string }

func (s *memSettings) Get(key, fallback string) string {
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}
func (s *memSettings) Set(key, value string) error { s.values[key] = value; return nil }
func (s *memSettings) All() map[string]string      { return s.values }

// discardLogger silences module logging in the driver.
type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// loadPrevious reads the results of an earlier run in the same output directory
// so the report can show baseline→current transitions.
func (d *drive) loadPrevious() {
	path := filepath.Join(d.outDir, "results.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var previous []*stepResult
	if err := json.Unmarshal(raw, &previous); err != nil {
		return
	}
	d.prev = make(map[string]*stepResult, len(previous))
	for _, result := range previous {
		d.prev[result.ID] = result
	}
}

// writeReport writes results.json and drive-report.md.
func (d *drive) writeReport() {
	resultsPath := filepath.Join(d.outDir, "results.json")
	raw, _ := json.MarshalIndent(d.results, "", "  ")
	if err := os.WriteFile(resultsPath, raw, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "ui-drive: write results.json: %v\n", err)
	}
	report := d.renderReport()
	reportPath := filepath.Join(d.outDir, "drive-report.md")
	if err := os.WriteFile(reportPath, []byte(report), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "ui-drive: write report: %v\n", err)
	}
	fmt.Printf("\nui-drive: report %s\nui-drive: results %s\n", reportPath, resultsPath)
}

// runInfo snapshots the repository state the run was made against.
type runInfo struct {
	head      string
	dirty     string
	startedAt string
}

func gitValue(args ...string) string {
	command := exec.Command("git", args...)
	output, err := command.Output()
	if err != nil {
		return "?"
	}
	return strings.TrimSpace(string(output))
}

// renderReport builds the markdown report.
func (d *drive) renderReport() string {
	var builder strings.Builder
	info := runInfo{
		head:      gitValue("rev-parse", "--short", "HEAD"),
		dirty:     gitValue("status", "--short"),
		startedAt: time.Now().Format(time.RFC3339),
	}
	counts := map[string]int{}
	for _, result := range d.results {
		counts[result.Status]++
	}
	builder.WriteString("# ui-drive 交互驱动报告\n\n")
	fmt.Fprintf(&builder, "- 生成时间：%s\n", info.startedAt)
	fmt.Fprintf(&builder, "- 仓库：`/data/mygo` @ `%s`\n", info.head)
	fmt.Fprintf(&builder, "- 窗口：%dx%d；模式：%s\n", d.w, d.h, map[bool]string{true: "quick（跳过流式/审批等待）", false: "full"}[d.quick])
	fmt.Fprintf(&builder, "- 驱动工具：`internal/devtools/ui-drive`（headless desktop assembly + scripted model server）\n")
	if info.dirty != "" {
		fmt.Fprintf(&builder, "- 工作区未提交改动（本次运行时的中间态，结论按此快照）：\n\n```\n%s\n```\n", info.dirty)
	}
	builder.WriteString("\n## 汇总\n\n")
	fmt.Fprintf(&builder, "| PASS | FAIL | WARN | BLOCKED | 总计 |\n|---:|---:|---:|---:|---:|\n")
	fmt.Fprintf(&builder, "| %d | %d | %d | %d | %d |\n\n", counts[statusPass], counts[statusFail], counts[statusWarn], counts[statusBlocked], len(d.results))

	if len(d.prev) > 0 {
		builder.WriteString("## 与上一轮对比（上一轮 → 本轮）\n\n")
		changed := 0
		builder.WriteString("| 步骤 | 上轮 | 本轮 |\n|---|---|---|\n| --- | --- | --- |\n")
		for _, result := range d.results {
			before, ok := d.prev[result.ID]
			if !ok {
				continue
			}
			if before.Status != result.Status {
				changed++
				fmt.Fprintf(&builder, "| %s `%s` | %s | %s |\n", result.Name, result.ID, before.Status, result.Status)
			}
		}
		if changed == 0 {
			builder.WriteString("| （共有的步骤无状态变化） | - | - |\n")
		}
		builder.WriteString("\n")
		// Steps that did not exist in the previous run (new coverage).
		added := make([]*stepResult, 0, 8)
		for _, result := range d.results {
			if _, ok := d.prev[result.ID]; !ok {
				added = append(added, result)
			}
		}
		previousPass := 0
		previousFail := 0
		for _, before := range d.prev {
			switch before.Status {
			case statusPass:
				previousPass++
			case statusFail:
				previousFail++
			}
		}
		fmt.Fprintf(&builder, "上一轮共有 %d 步（PASS %d / FAIL %d）；本轮新增 %d 步覆盖：\n\n",
			len(d.prev), previousPass, previousFail, len(added))
		for _, result := range added {
			fmt.Fprintf(&builder, "- `%s` %s（%s）\n", result.ID, result.Name, result.Status)
		}
		if len(added) == 0 {
			builder.WriteString("（无新增步骤）\n")
		}
		// Steps that existed in the previous run but not in this one (renamed
		// or replaced coverage).
		current := make(map[string]bool, len(d.results))
		for _, result := range d.results {
			current[result.ID] = true
		}
		removed := make([]*stepResult, 0, 8)
		for id, before := range d.prev {
			if !current[id] {
				removed = append(removed, before)
			}
		}
		if len(removed) > 0 {
			sort.Slice(removed, func(left, right int) bool { return removed[left].ID < removed[right].ID })
			builder.WriteString("\n本轮不再存在的上轮步骤（已替换/重命名）：\n\n")
			for _, before := range removed {
				fmt.Fprintf(&builder, "- `%s` %s（上一轮 %s）\n", before.ID, before.Name, before.Status)
			}
		}
		builder.WriteString("\n")
	}

	builder.WriteString("## 功能矩阵\n\n")
	builder.WriteString("| # | 步骤 | 功能 | 类别 | 状态 | 期望 | 证据/备注 | 截图 |\n")
	builder.WriteString("|---|---|---|---|---|---|---|---|\n")
	for index, result := range d.results {
		detail := strings.Join(result.Details, "；")
		detail = strings.ReplaceAll(detail, "|", "\\|")
		if len(detail) > 300 {
			detail = detail[:300] + "…"
		}
		shot := ""
		if len(result.Shots) > 0 {
			names := make([]string, 0, len(result.Shots))
			for _, path := range result.Shots {
				names = append(names, "`"+filepath.Base(path)+"`")
			}
			shot = strings.Join(names, " ")
		}
		fmt.Fprintf(&builder, "| %d | `%s` | %s | %s | **%s** | %s | %s | %s |\n",
			index+1, result.ID, result.Name, result.Category, result.Status, escapeCell(result.Expected), detail, shot)
	}

	builder.WriteString("\n## 失败详情\n\n")
	failed := 0
	for _, result := range d.results {
		if result.Status != statusFail {
			continue
		}
		failed++
		fmt.Fprintf(&builder, "### %d. `%s` %s\n\n", failed, result.ID, result.Name)
		fmt.Fprintf(&builder, "- 类别：%s\n- 期望：%s\n- 实际：\n", result.Category, result.Expected)
		for _, detail := range result.Details {
			fmt.Fprintf(&builder, "  - %s\n", detail)
		}
		if len(result.Shots) > 0 {
			fmt.Fprintf(&builder, "- 截图：%s\n", strings.Join(result.Shots, "、"))
		}
		if len(result.Texts) > 0 {
			text := strings.Join(result.Texts, " | ")
			if len(text) > 1600 {
				text = text[:1600] + "…"
			}
			fmt.Fprintf(&builder, "- Tester.Texts() 摘要：\n\n```\n%s\n```\n", text)
		}
		builder.WriteString("\n")
	}
	if failed == 0 {
		builder.WriteString("（无失败步骤）\n\n")
	}

	builder.WriteString("## 警告与阻塞\n\n")
	problems := 0
	for _, result := range d.results {
		if result.Status != statusWarn && result.Status != statusBlocked {
			continue
		}
		problems++
		fmt.Fprintf(&builder, "- **%s** `%s` %s：%s\n", result.Status, result.ID, result.Name, strings.Join(result.Details, "；"))
	}
	if problems == 0 {
		builder.WriteString("（无）\n")
	}

	builder.WriteString("\n## 根因归属\n\n")
	byCategory := map[string][]string{}
	for _, result := range d.results {
		if result.Status != statusFail && result.Status != statusWarn {
			continue
		}
		byCategory[result.Category] = append(byCategory[result.Category], "`"+result.ID+"`")
	}
	categories := make([]string, 0, len(byCategory))
	for category := range byCategory {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	builder.WriteString("| 根因归属 | 问题步骤 |\n|---|---|\n")
	for _, category := range categories {
		fmt.Fprintf(&builder, "| %s | %s |\n", category, strings.Join(byCategory[category], "、"))
	}
	if len(categories) == 0 {
		builder.WriteString("| - | 本轮无失败/警告 |\n")
	}

	builder.WriteString("\n## 截图索引\n\n")
	for _, result := range d.results {
		for _, shot := range result.Shots {
			relative := strings.TrimPrefix(filepath.ToSlash(shot), filepath.ToSlash(d.outDir)+"/")
			fmt.Fprintf(&builder, "- `%s` — %s（%s）\n", relative, result.Name, result.Status)
		}
	}
	builder.WriteString("\n## 复验命令\n\n```bash\n")
	builder.WriteString("cd /data/mygo\nexport PATH=/usr/local/go/bin:$PATH GOPROXY=off GOSUMDB=off GOFLAGS=-mod=mod GOCACHE=/data/mygo/.cache/go-build\n")
	builder.WriteString("go run ./internal/devtools/ui-drive\n")
	builder.WriteString("```\n")
	return builder.String()
}

func escapeCell(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "|", "\\|"), "\n", " ")
}
