# P3 遗留验收用例设计（ZeoKeel）

> 作者：product-auditor · 日期：2026-11-06 · 基线：HEAD `b92dc1e`（最终修复提交）
> 范围：5 项 P3 遗留的**验收测试设计**（本轮不写实现代码，只定义可执行验收）。
> 约定：每条给出「现状与证据 → 风险 → 测试文件/测试名 → 夹具与步骤 → 断言 → RED/GREEN 判据 → 实现接口建议 → 证据要求」。
> 所有测试都要求无显示器可跑、无真实模型依赖、`CGO_ENABLED=0`；文件路径为建议位置，实现方可在同包内微调，但测试名与断言口径不应弱化。

## 1. Automation run-now 失败应回滚空会话

**现状与证据**
- `internal/app/automation_runs.go:162` `runAutomationTask`：无可用 origin 会话时在 `:226/:228` 新建会话，`:261` `_, _ = submitInto(sessionID)`。
- `submitInto` 在 `:174-196` 已能记录 `automation_runs` 失败行（status=failed, error="submit: ..."），但**新建的会话没有删除**：Submit 失败后侧栏/workspace 留下一个空会话（与「run-now 失败」的实际效果不符，用户会看到幽灵会话）。
- 失败路径覆盖：`ErrNoAgent`（无模型）、provider 已删除、controller closed、目标 model 不可用；origin 分支（`:199-218`）不新建会话，无此问题。

**风险**：每次失败的定时任务/立即运行都会累积空会话；用户无法判断会话来源，误以为任务成功；与 PD-04 导入回滚（已修的同类问题）不一致。

**测试设计**
- 文件：`internal/app/automation_runnow_rollback_test.go`（package `app`，可用现有 `newLiveApp*`/store fixture，或最小构造 `store.Open(t.TempDir())+session.New(...)`）。
- 测试名：`TestAutomationRunNowRollsBackEmptySession`（+ 子测试 `origin_session_failure_keeps_no_new_session`、`success_case_keeps_session`）。
- 夹具/步骤：
  1. store 中创建 workspace + automation（`OriginSessionID=""`、`WorkspaceID=wsA`、`Permission="read-only"`、`Model=""`）；controller 的 agent 置 nil（触发 `ErrNoAgent`）。
  2. 记录 `QuerySessions(workspace wsA)` 数量 N。
  3. 调用 `app.runAutomationTask(ctrl, task)`（同包直接调用；或 `ctrl.RunNow` 后 `synchronize`）。
  4. 断言：`QuerySessions` 仍为 N（新建的空会话已被删除）；`ListAutomationRuns(task.ID)` 恰好 1 行且 `Status=="failed"`、`Error` 含 "submit"；被删除的 session 不再可 `GetSession`；无通知/无 team 残留（若 relevant）。
  5. 成功用例：agent stub 能成功启动 turn（或不校验 Submit 语义，用 fake controller）→ 会话保留、run 进入 running/completed。
- RED/GREEN：现状 RED（N+1，空会话仍在）；实现删除后 GREEN。判据只依赖 store API（`QuerySessions`/`GetSession`/`ListAutomationRuns`），不碰 UI。
- 实现接口建议：在 `runAutomationTask` 的新建会话分支记 `freshSession := true`；`submitInto` 返回 false 时，若 `freshSession && store.MessageCount(sessionID)==0` 则 `ctrl.DeleteSession(ctx, sessionID)`；run 行的 `session_id` 保留（历史事实）或置空并在 `Error` 中注明 "session rolled back"，二选一需在 API 文档写死。
- 证据：`go test -count=1 -run TestAutomationRunNowRollsBackEmptySession -v ./internal/app` + SQLite 查询前后 sessions 数。

## 2. skills 默认扫描路径应基于 workspace 而非进程 cwd

**现状与证据**
- app 侧默认值已是 workspace 绝对路径：`internal/app/services.go:866` `read("skills.root", "ZEOKEEL_SKILLS_ROOT", filepath.Join(root, "skills"))`。
- 但 `internal/skills/skills.go:205-240` `RootsFor(root)` 与 `:102/:210/:367` 对**相对路径**调用 `filepath.Abs` → 以**进程 cwd** 为基准解析。桌面应用由 Finder/开始菜单/脚本启动时 cwd 是任意的，于是 `skills.root="skills"`（或 `ZEOKEEL_SKILLS_ROOT=skills`）会扫描错误目录；`--from`/headless 场景同理。
- 相关：`skillsRootsAvailable`（`services.go:517-529`）、多 root 展开（FP-17，`skills.RootsFor` 对 `skills` 目录做 workspace 侧展开）、prompt 里的技能目录（`services.go:1105-1114`）。

**风险**：技能静默不加载或加载到当前 shell 目录下的同名目录（供应链/误配置风险）；用户难以归因（skill 工具报 not found）。

**测试设计**
- 文件：`internal/skills/roots_test.go`（package `skills`）。
- 测试名：`TestRootsForRelativeRootUsesBaseDirNotProcessCwd`、`TestRootsForAbsoluteRootUnchanged`、`TestRootsForDefaultLayoutUnchanged`；app 级 `internal/app/skills_root_workspace_test.go::TestAgentSkillsRootIndependentOfProcessCwd`。
- 夹具/步骤（单元）：
  1. `ws := t.TempDir()`；创建 `ws/skills/reviewer/SKILL.md`；另建 `other := t.TempDir()` 并在其中放同名假 skill。
  2. `t.Chdir(other)`（Go 1.27 支持）；调用新契约 `RootsFor("skills", RootsOptions{BaseDir: ws})`（或 `ResolveRoot(ws, "skills")`）。
  3. 断言返回第一条为 `ws/skills`（clean/abs），且包含 `ws/.zeokeel/skills`、`~/.zeokeel/skills`（按 FP-17 布局）；不包含 `other/skills`。
  4. 绝对路径 `RootsFor("/abs/skills", …)` 与现状等价；默认（相对值空？）行为不变。
- 夹具/步骤（app 集成）：
  1. store + workspace `/tmp/ws`；`t.Chdir` 到无关目录。
  2. `buildAgentWithOptions(st, ws, ...)`；断言 registry 的 `skill` 工具扫描到 `ws/skills/reviewer`（或 `skills.LoadTolerant(ws/skills)` 名字含 reviewer）；改 cwd 后结果不变。
- RED/GREEN：现状 RED（相对 root 跟随 cwd）；实现后 GREEN。判据用路径字符串（`filepath.Clean`）与 skill 名称，不依赖 UI。
- 实现接口建议：给 `skills` 包增加 `RootsOptions{BaseDir string}`（或 `RootsForAt(base, root)`）；app 调用处传 workspace root 作为 base；相对 `skills.root` 只允许相对 workspace，禁止 `..` 逃逸（clean 后 prefix 校验）。
- 证据：`go test -count=1 -v ./internal/skills ./internal/app -run 'TestRootsFor|TestAgentSkillsRoot'`；再加一次手工 printf 路径日志（实现自证）。

## 3. RT-07 tool-result spill symlink TOCTOU 加固

**现状与证据**
- `internal/session/tool_spill.go:36-105`：`safeSpillDir` 逐段 `os.Lstat` 拒绝 symlink/非目录，随后 `os.OpenFile(path, O_WRONLY|O_CREATE|O_EXCL, 0600)`（`:53`）保证已存在文件/symlink 不被截断。
- 残余 TOCTOU：`Lstat(dir)` 与 `OpenFile(path)` 之间，目录组件可被替换为指向 workspace 外的 symlink；`O_EXCL` 只保护最终文件已存在的情形，**不阻止路径中的目录组件被 symlink 跟随**。`os.Mkdir` 后、`Lstat` 前的窗口同理。
- 当前测试覆盖：预先存在的 symlink 目录会被拒绝（Lstat 检查），但**无法覆盖并发替换**（竞态窗口）。

**风险**：工作区是不可信输入（克隆仓库可提交 `.zeokeel/tool-results` symlink），若攻击者/构建脚本在窗口内替换目录，32KB+ 工具结果会被写进 `~/.ssh` 等外部目录（P0 级路径逃逸的残留面）。

**测试设计**
- 文件：`internal/session/tool_spill_toctou_test.go`（package `session`，需白盒）。
- 测试名：`TestSpillToolResultRejectsSymlinkSwapBetweenCheckAndOpen`、`TestSpillToolResultNoEscapeOnDirectorySwap`。
- 夹具/步骤：
  1. `root := t.TempDir()`；`outside := t.TempDir()`；payload := `strings.Repeat("x", toolResultSpillBytes+1024)`。
  2. **测试缝**：实现方新增 unexported hook（如 `var spillBeforeOpenHook func(dir string)`，仅在测试中赋值，生产路径 nil 时零开销；或 `Options.Seams`）。hook 内把 `root/.zeokeel/tool-results` 重命名为 `.bak` 并 `os.Symlink(outside, root/.zeokeel/tool-results)`。
  3. 调用 `spillToolResultFile(root, "call-1", payload)`。
  4. 断言：`ok==false` 且 `err != nil`（fail-closed，回退到内联完整结果）；`outside` 目录下没有任何文件（`os.ReadDir` 空）；`root` 下除 `.bak` 外无新文件；再次正常调用（无 hook）成功且 `filepath.EvalSymlinks(path)` 的 prefix 在 root 内。
  5. 子测试：Mkdir 前 swap、最终文件换成 symlink、目录组件换成普通文件 → 全部 fail-closed。
- RED/GREEN：现状 RED（hook 场景会写到 outside）；GREEN 判据 = 写入发生前完成"打开后验证"或"逐段 nofollow 打开"。
- 实现接口建议（任选其一，需在 known-limitations 记录平台差异）：
  a) unix：用 `syscall.Openat` 从 `root` fd 逐段 `O_NOFOLLOW|O_DIRECTORY`，最终 `O_CREAT|O_EXCL|O_NOFOLLOW` 打开（Linux/macOS）；windows 回退 b。
  b) 跨平台：`OpenFile` 后 `f.Stat()` 与 `os.Lstat` 各段比对 `dev+ino`（捕获前后不一致则关闭、删除、返回错误）；并 `filepath.EvalSymlinks` 校验 prefix。
- 证据：`go test -count=1 -run 'TestSpillToolResult.*(Swap|Escape)' -v ./internal/session`；测试内断言 outside 目录为空 + 返回 err。

## 4. 旧 manifest 迁移兼容（importer）

**现状与证据**
- `internal/importer/manifest.go`：`manifestFormatVersion = 1`（`:28`）；`loadManifest`（`:149-176`）对 `Version != 1` 直接 `ErrManifest` fail-closed；`manifestKey`（`:74-90`）现用「DSH home + 相对 sessions 路径」作为 key（commit e9b5d7e 修复了真实 DSH `session-<uuid>` 目录的 key 碰撞）。
- 升级路径问题：wave2 中间构建（`84c5b1f`）写出的 manifest 使用旧的碰撞 key（`home|session.v4.jsonl.zstd`）且 `dsh_session_id` 记录的是文件名；这些 manifest 在升级后**不会报错**（仍是 version 1），但 key 与新算法不匹配 → 第二次导入会把已导入会话再导一遍（重复）。此外更早的 manifest 可能没有 `source_mtime_unix_nano`/`version` 字段。
- 现测试只覆盖「新 key 写→读幂等」（`TestImportManifestIdempotent`）与「损坏/未来版本 fail-closed」，无旧 key 迁移用例。

**风险**：升级后首次重复导入造成会话重复、manifest 继续膨胀；用户误以为「幂等」失效；若简单地把旧版本判为 fail-closed，则升级用户的下一次导入直接报错且无修复路径。

**测试设计**
- 文件：`internal/importer/manifest_migration_test.go`（package `importer`）。
- 测试名：`TestLoadLegacyManifestMigratesKeys`、`TestLegacyManifestDeduplicatesAfterMigration`、`TestManifestUnknownFutureVersionFailsClosed`、`TestManifestMissingVersionSkipsOrMigrates`。
- 夹具：
  1. DSH fixture：`<home>/sessions/--proj--/session-<uuid>/session.v4.jsonl`（1-2 个会话）+ 对应 ZeoKeel store。
  2. 旧 manifest JSON：`{"version":1,"sessions":{"<home>|session.v4.jsonl.zstd":{dsh_session_id:"session.v4.jsonl.zstd",source_file:"<home>/sessions/--proj--/session-<uuid>/session.v4.jsonl.zstd",source_size:<n>,sha256:"<hash>",zeokeel_session_id:"..."}}}`（sha/size 与 fixture 文件实际一致）。
- 步骤/断言：
  1. 首次 `Import`（store 空）→ 会话导入 1 个；manifest 被重写：key 变为 `home|--proj--/session-<uuid>/session.v4.jsonl.zstd`（或设计文档写死的等价规范），并产生一条 warning「migrated legacy manifest」；旧 key 不再出现。
  2. 第二次 `Import` → `sessions_imported==0`、`skips.idempotent==1`、store session 数不变（关键断言：迁移后仍幂等）。
  3. `version:2` 的 manifest → `ErrManifest`，错误消息含版本与「use --no-manifest to re-import」引导；dry-run 与真实导入都不写坏文件。
  4. 缺失 `version` 但结构可识别（有 `source_file`/`sha256`）→ 迁移后同上；结构不可识别 → fail-closed `ErrManifest`。
- RED/GREEN：现状 RED（旧 key 未被识别 → 第二次导入重复）；实现迁移后 GREEN。
- 实现接口建议：`loadManifest` 返回 `(manifest, migrated bool, err)`；迁移规则：对每个 entry，若 `SourceFile` 位于 `sessionsRoot` 下则用新 `manifestKey` 重算 key；否则保留并标注 warning；`save()` 只在非 dry-run 时写回（保持 dry-run 只读语义）。
- 证据：`go test -count=1 -v ./internal/importer -run Test.*Manifest`；两次运行前后 sqlite `select count(*) from sessions` 不变 + manifest 内容 key 形状 diff。

## 5. keyless provider scheme 校验

**现状与证据**
- `internal/app/services.go:500-515` `keylessLocalProvider(baseURL string) bool`：`url.Parse` + `Hostname()` 白名单（localhost/docker.internal）+ `net.ParseIP` 的 loopback/private/link-local；`:960` 的 agent 构建处 `if apiKey == "" && !keylessLocalProvider(baseURL) { return nil, nil }`。
- 缺口：**不校验 scheme**。`ftp://localhost/x`、`gopher://localhost/`、`ws://127.0.0.1/` 会被判为 keyless 本机 provider，agent 被构建，随后 provider 请求以错误形式失败（误导为网络问题）；`file://` 因 Host 为空被拒（OK）。也不校验端口、userinfo、IPv4-mapped IPv6 等边界。
- 需求：只有 `http`/`https` + 本机/私网地址才允许空 key；其他 scheme/公网地址必须要求 key，并在**构建期**给出可操作错误（而不是运行时网络失败）。

**测试设计**
- 文件：`internal/app/keyless_provider_test.go`（package `app`）。
- 测试名：`TestKeylessProviderSchemeValidation`（表驱动）、`TestBuildAgentRequiresKeyForPublicProvider`、`TestBuildAgentAllowsKeylessLocalProvider`。
- 表驱动用例（want = allowed）：
  - allowed：`http://localhost:11434/v1`、`http://127.0.0.1:8080`、`http://[::1]:11434/v1`、`https://localhost/v1`、`http://192.168.1.5:11434`、`http://10.0.0.2:11434`、`http://host.docker.internal:11434/v1`、`https://[::ffff:127.0.0.1]:443/v1`（mapped loopback）。
  - rejected：``（空）、`localhost:11434/v1`（无 scheme）、`ftp://localhost/x`、`file:///etc/passwd`、`gopher://localhost/`、`ws://127.0.0.1/`、`javascript:alert(1)`、`http://user:pass@localhost/`（userinfo 混淆）、`http://localhost.evil.com/`、`http://2130706433/`、`https://api.example.com/v1`、`http://localhost:99999/`（非法端口）。
- 集成断言：
  1. `buildAgentWithOptions` + `provider.base_url=ftp://localhost/x` + 空 key → 返回错误（非 nil,nil），错误含 "unsupported provider scheme" / "key required" 等稳定片段；
  2. 公网 https + 空 key → 错误提示去 Settings > Models 填 key（与 PD-02 的 UI 联动）；
  3. `http://localhost:11434/v1` + 空 key → bundle 非 nil（当前行为保持）。
- RED/GREEN：scheme 用例现在 RED（ftp 被判 allowed）；GREEN 判据 = 只有 http/https 通过。
- 实现接口建议：把 `keylessLocalProvider` 改为 `allowKeylessProvider(baseURL string) (bool, error)`：先 `url.Parse` + scheme ∈ {http,https} + Host 非空 + 无 userinfo + 端口合法，再做本机/私网判定；构建处把 error 归类为「用户可操作配置错误」（可复用 session_errors 分类的 auth/config 类）。
- 证据：`go test -count=1 -run 'TestKeylessProvider|TestBuildAgentRequiresKey' -v ./internal/app`；表格用例逐项输出。

## 6. 公共验收要求（5 项通用）

- **RED 证据**：每项在实现前先提交测试并记录失败输出（测试名 + 首个失败断言），实现后同测试转 GREEN；不允许先实现后补测。
- **无外部依赖**：不连真实模型/网络；symlink 用例在 Windows 上 `t.Skip`（并记录平台差异）；cwd 用例用 `t.Chdir` 恢复。
- **并行安全**：测试不得修改真实 `~/.zeokeel`；全部 `t.TempDir()` + 注入 data dir。
- **回归门禁**：每项实现后至少跑对应包 + `go build ./... && go vet ./...`；涉及 app 的再跑 `go test -count=1 ./internal/app ./internal/session ./internal/skills ./internal/importer`。
- **文档同步**：修复后把「已知限制/剩余 P3」从 `docs/BUGS.md` 移除并指向测试名；`docs/design/known-limitations.md` 只保留真机相关未验证项。
- **排期建议**：②（skills cwd）与 ⑤（keyless scheme）为 S，①（自动化回滚）S/M，④（manifest 迁移）M，③（spill TOCTOU）M/L（需平台分支与安全评审）。
