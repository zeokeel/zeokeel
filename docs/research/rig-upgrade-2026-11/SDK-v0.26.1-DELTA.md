# rig v0.24.0 → v0.26.1：SDK 增量全量清点（含细节与坑）

> 分析时间：2026-11-20 晚。对照：本仓当前 `github.com/zeokeel/zeokeel-rig v0.24.0`（今早升级）
> vs 最新 `v0.26.1`（同日 13:02Z 发布）。同时给出 v0.19.0（本仓升级前的基线）的累计口径。
> 方法：`go/ast` 逐包导出面提取（脚本 `apidump`）+ 目录/工具账本/文档 diff + 逐版本 CHANGELOG 精读，
> 并用官方扫描器 `rigmigrate` 对本仓实跑。原始数据：`.cache/logs/api-v0190.tsv`、`api-v0240.tsv`、
> `api-v0261.tsv`、`api-sqlite-v0240.tsv`、`api-sqlite-v0261.tsv`。

---

## 0. 结论：不是"只加了几个 API"，量级如下

| 指标 | v0.19.0（升级前） | v0.24.0（今早升到） | **v0.26.1（最新）** |
| --- | ---: | ---: | ---: |
| 非测试 `.go` 文件 | 191 | 325 | **336** |
| 包目录（含 internal/cmd） | 60 | 92 | **93** |
| **导出符号总数** | 1132 | 1842 | **1967** |
| 模型工具账本 `catalog.Static` | 81 | 83 | **83**（未再增） |
| `session/sqlite` 子模块导出符号 | —（尚未拆分） | 84 | **112** |

增量口径：

| 区间 | 新增导出符号 | 删除 | 净增 | 新增包 | 删除包 |
| --- | ---: | ---: | ---: | ---: | ---: |
| v0.19.0 → v0.26.1（相对我们升级前） | **+854** | −19 | **+835** | 34 | 3 |
| v0.24.0 → v0.26.1（相对当前 HEAD） | **+155** | −30 | **+125** | 3 | 2 |
| `session/sqlite` v0.24 伪版本 → v0.26.1 | **+28** | 0 | +28 | — | — |

> 注意：**v0.25/v0.26 没有新增模型可调用的工具**（账本 83 不变；`spill_read` 在 v0.24.0 就已在
> `tools/builtin/spill.go`，只是 v0.24 的 `docs/ai/tools.json` 没重新生成）。新增的是**宿主侧
> 能力与执行点**：审批记忆、长生命周期运行时、工具生命周期、隐藏路径、jobs/terminal 类型化 API、
> 非 git 撤销、溢出存储、指令渲染、SQLite 检索与迁移。

---

## 1. 版本序列（v0.24.0 之后）

| 版本 | 定位 | 一句话 |
| --- | --- | --- |
| v0.24.1 | 发布流程 | 推 tag 即发布（删掉草稿发布 workflow、tag 不再要求签名） |
| **v0.25.0** | 能力大版 | 隐藏路径一等公民、`kit.Runtime` 预备件、jobs/terminal 类型化、undo 文件后端、sqlite 检索/导入导出/改名、TurnHook、SystemPromptFunc |
| **v0.26.0** | 审批 + 运行时 + 破坏性清理 | 审批记忆/轮次去重/prompt 链、`kit.Runtime` 落地、工具激活持久化与取消激活、`secrets` 整包删除、所有 no-op API 真删、read-only 工具面裁剪 |
| v0.26.1 | 补丁 | PTC 内层调用改用当次 Run 门禁；修 `allow-always` 被当拒绝 |

新增文档：`docs/MIGRATION-v0.25.md`（154 行，破坏性变更逐条对照表）、`docs/OWNERSHIP.md`（120 行，
"能力该放 SDK 还是宿主"的三条判据 + 分域归属矩阵）、`docs/sqlite.md`、`docs/TOOLS.md`。

---

## 2. 包级变化

**v0.24.0 → v0.26.1 新增包**：`spillfile`（`rig.SpillStore` 的文件实现）、`internal/rigmigrate`
+ `cmd/rigmigrate`（旧 API 扫描器）。
**删除**：`secrets`（整包 + testdata）。

**v0.19.0 → v0.26.1 新增包（34 个，按能力域）**：
- 运行时/上下文：`events`、`instructions`、`costmeter`、`modelprice`、`session/store`、`session/sqlite`（子模块）
- 工具面：`tools/discovery`（`tool_search`）、`tools/patch`（`apply_patch`）、`tools/search`（BM25）、
  `tools/undo`、`tools/shelf`（渐进加载原语）、`tools/sandboxgrant`、`tools/sandboxstatus`
- 存储契约：`storage`（聚合契约）、`storagetest`、`spillfile`、`memory`（wiki 契约）
- provider：`provider/local`、`provider/ollama`、`provider/lmstudio`
- 内部：`internal/{memwiki,fsguard,gitsnap,diffpreview,seccomp,rigmigrate}`
- CLI：`cmd/rigdoctor`、`cmd/rigmigrate`

**删除（v0.19.0 → v0.26.1）**：`tools/report`、`tools/settings`、`docs/zh`。

---

## 3. v0.24.0 → v0.26.1 新增导出面（按包，共 155）

| 包 | 新增数 | 内容 |
| --- | ---: | --- |
| `kit` | 29 | `Runtime`/`RuntimeConfig`/`NewRuntime`/`NewAgent`/`Close`、`AddRoot`/`RemoveRoot`、`AddMCPClient`/`RemoveMCPClient`、`DeriveRunner`、`Agent.{Jobs,Terminals,Tool,Owner,SessionApprovals,ForgetApprovals,DeactivateTools}`、`NewEventSink`/`EventSink`、`NewMemoryApprovals`/`NewMemoryActivations`、`PromptSection`、`ErrRuntimeClosed`/`ErrMCPClientNotFound` |
| `tools/jobs` | 24 | 类型化宿主面：`Job`/`JobView`/`JobSnapshot`/`BackgroundJobs`/`Operation`/`OperationSpec`/`ProcessSpec`/`Outcome`/`Status`/`Kind`/`Source`/`JobScope`/`ScopeResolver`/`WithScopeResolver`、`Manager.Snapshot`、`Manager.ToolsFor(owner)`、`KindBash`/`KindPwsh`/`KindPTYSend`、`Status{Running,Stopping,Completed,Failed,Killed}` 常量 |
| 根包 `rig` | 22 | `TurnHook`（BeforeRun/AfterRun/BeforeTool/AfterTool）、`SystemPromptFunc` 之外的 `DiscoverableTool`/`IsToolDiscoverable`、`ToolActivationStore`、`ToolDeactivator`、`ApprovalStore`/`ApprovalRule`/`ApprovalScopeSession/Global`、`WithSpillScope`/`SpillScope`、`ErrNoActivationRecord` |
| `gate` | 15 | 审批记忆：`WithApprovalMemory`/`WithApprovalSessionID`/`WithApprovalLogger`、`ApprovalTarget`/`CanonicalArgs`、`RoundMemo`/`NewRoundMemo`、`WithoutToolMemory`、`Prompt`/`PromptChain`、`ApprovalKind{Tool,Command,Path}` |
| `sandbox` | 13 | `WithHidden`/`Hidden`/`HiddenPatterns`/`GlobMatch`、`WithScratchDir`/`ScratchDir`/`ScratchDirError`/`ScratchDirMode`、`CheckConfinedCommand`、`WritableRootsFor`、`RemoveWritableRoot` + `ErrWritableRoot{NotFound,IsPrimary}` |
| `tools/undo` | 9 | `Backend` 接口（Take/Revert/Changes/Prune/Turns）、`NewFileTracker`/`FileOptions`/`DefaultFileMaxBytes`（非 git 后端） |
| `spillfile` | 6 | `New`/`Store.{Save,Load,GC,Dir}`：`rig.SpillStore` 的文件实现 |
| `tools/terminal` | 5 | `Scope`/`ScopeResolver`/`WithScopeResolver`、`Manager.ToolsFor(owner)`/`ToolsWithListFor(owner)` |
| `tools/presets` | 5 | `ForMode(mode string, confined bool, names)`、`ModeFilter`、`Mode{ReadOnly,WorkspaceWrite,DangerFullAccess}` |
| `tools/catalog` | 5 | `Required()`、`OptionalByName`/`OptionalNames`/`Optional`/`OptionalTool`：工具对宿主能力的**依赖声明** |
| `instructions` | 4 | `WithRepoFileNames`（自定义指令文件名）、`Set.RenderWith`/`RenderOptions`/`DefaultTrustNotice`（untrusted 围栏） |
| `tools/mcp` | 3 | `SanitizeName`、`NormalizePrefix`、`MigrateLegacyNames`（旧名迁移：可推 server 的自动改，推不出的给 `needsUser`） |
| `events` | 2 | `KindToolDeactivated` + payload 别名 |
| `agent` | 1 | `Agent.DeactivateTools` |
| `tools/search` | 1 | `WithHiddenFunc`（隐藏路径接入自遍历检索） |
| `tools/skill` | 1 | `ToolsWithRoot`（技能根跟随工作区，不再固定 cwd） |

### 3.1 `session/sqlite` 子模块（+28，且 v0.25.0 起**有 tag**）

| 类别 | 新增 |
| --- | --- |
| 元数据 | `UpdateMeta` + `MetaPatch`（改名、换工作区；nil=不改、空串=清空） |
| 检索 | `SearchFTS`/`SearchSubstring`/`SearchMode`、`BackfillSearchText`/`BackfillSearchIndex`、`FTS5Available`/`SearchIndexReady` |
| 导入导出 | `ImportRows`/`SessionRows`/`ImportReport`、`ExportExtras`（events/costs/checkpoint） |
| 成本 | `SessionCosts`/`CostBySession`/`CostByWorkspace`/`CostEntry.WorkspaceID`/`RunID` |
| 事件/检查点/统计 | `SessionEvents`/`SessionCheckpoint`/`Stats`/`SessionStats` |
| 消息维护 | `DeleteMessages`/`TruncateMessages`/`DeleteCheckpoint` |

---

## 4. 破坏性删除与签名变更（30 + 6）

**删除（编译器会挡）**

| 区域 | 删除 | 替代 |
| --- | --- | --- |
| 脱敏 | `secrets` 整包（22 条模式、`Redactor`、`RedactForExport`、流式 `Writer`）、`agent.NewWithRedactor`、`agent.RedactMessages`、`kit.Config.Redactor`、`RunnerOptions.Redactor` | **无**：宿主导出/展示前自己清洗 |
| 保护路径 | `sandbox.WithProtectedPaths`/`AddProtectedPaths`/`ProtectedPaths`/`WriteDenyPaths`/`WithProtectMissingPaths`/`ProtectMissingPaths`/`Workspace{NonGit,}MetadataPaths`、`osrunner.Profile.{Protected,MissingProtected}`、`ErrMissingProtected`、`kit.{DisableProtection,ProtectPaths,ProtectWorkspaceMetadata,HomeSecretPaths}`、`full.AllOptions.ProtectWorkspaceMetadata` | `WritableRoot.ReadOnlySubpaths`（`WithWritableRoot(root, readOnly...)`/`ProtectGitMetadata`） |
| 拒绝 glob | `sandbox.WithDeniedGlobs`、`Policy.DeniedGlobs` | **`sandbox.WithHidden`**（参与判定） |
| 授权校验 | `sandbox.ErrGrantProtected`、`ValidateWritableGrantPath` 少一个 `protected` 参数 | 把目录排除在可写面外 |

**签名变更（能编译但要改语义）**：`tools/jobs.Manager.{StartOperation,StartProcess,PendingNotices}`
（`background.*` → `jobs.*` 类型）、`tools/terminal.WithBackgroundJobs(manager jobs.BackgroundJobs)`、
`tools/presets.ForMode`（`sandbox.Mode` → `string` + `confined bool`，且 `tools/presets` 不再 import `sandbox`）。

---

## 5. 能力域详解（细节）

### 5.1 审批：从"进程内一轮"变成可持久 + 可去重 + 可组合

- `rig.ApprovalStore`（Load/Save）+ `rig.ApprovalRule{Kind,Key,Scope,SessionID,DecidedAt}`；
  `kit.NewMemoryApprovals()` 提供内存实现；`kit.Config.ApprovalScope` = `session`（默认）/`global`。
- **只免提问、不放宽围栏**：被记住的调用照样过沙箱策略（read-only 下记住的命令仍被拒）；
  `Approval: never` 档不挂记忆；store 读写失败只记日志。
- `gate.RoundMemo`：同一轮内"工具 + 规范化参数"相同的并行调用只问一次（`gate.CanonicalArgs` 是
  参数规范化的唯一实现，`gate.ApprovalTarget` 是目标判定的唯一实现）。
- `gate.Prompt`/`PromptChain` + `kit.Config.ApprovalPromptChain`：审批文案按序组合
  （如 `gate.DiffPrompt` + `gate.SandboxGrantPrompt`）。
- 修复（v0.26.1）：PTC `run_code` 内层调用改用**当次 Run** 的门禁；`allow-always` 不再被误判为拒绝。

### 5.2 `kit.Runtime`：长生命周期宿主的共享层

- 进程级共享：provider（含 `http.Client`/连接池）、`*jobs.Manager`、`*terminal.Manager`、
  按工作区缓存的记忆 wiki、spill store，以及 `Searcher`/`SessionLog`/`Skills`/`TeamTasks`/`Scheduler`/
  `Reviewer`/`Runner`/`MCPClients`/`Models`。
- 每个会话/子代理只重建 `agent.Agent`、history、gate 绑定、`sandbox.Policy`、工具实例、activations、undo。
- 运行期变更：`AddRoot`/`RemoveRoot`（撤销对已存在 Agent **立即生效**，并对同 `Config` 新建的 Agent **持久**；
  主根不可撤销）、`AddMCPClient`/`RemoveMCPClient`（新 Agent 生效，已有 Agent 只收事件）、
  `DeriveRunner`（派生更严的 runner，不动共享 runner）。
- 生命周期：`Close` 幂等；关闭后 `NewAgent`/`Run` 返回 `kit.ErrRuntimeClosed`。

### 5.3 工具生命周期：激活可持久、可取消、可隐藏

- `rig.ToolActivationStore`（+`AgentSpec.ActivationStore`、`kit.Config.Activations`、`kit.NewMemoryActivations()`）
  把 `tool_search` 的激活结果按会话落盘，首次使用时惰性加载。
- **契约破坏性变更**：`Load` 返回 `rig.ErrNoActivationRecord` = 没有记录；**空切片 = 有记录且为空**；
  记录是**权威全集**（不再与 `InitialActiveTools` 求并集），否则"取消激活"会在重启后复活。
- `DeactivateTools`（`rig.ToolDeactivator`）：`tool_search`/`memory`/`skill` 不可移除（all-or-nothing）。
- `rig.DiscoverableTool`：`Discoverable() == false` 的工具不进 `tool_search` 清单，但仍可被宿主激活。
- `events.KindToolDeactivated` 事件。

### 5.4 隐藏路径（我们今早自建能力的官方版）

- `sandbox.WithHidden(patterns...)` + `Policy.Hidden`/`HiddenPatterns` + 导出 `sandbox.GlobMatch`。
- **参与判定**：`ResolvePath`/`CheckPath`/`CheckWritablePath`/`DeniedPath` 都看它 →
  `read_file`/`list_dir`/`glob`/`grep`/`search` 的结果与写入全拦；`search` 另有
  `search.WithHiddenFunc`（v0.25.0 修过"隐藏后 search 仍吐内容"、`@.env` mentions 泄露存在性）。
- 匹配语义：相对所属根的 slash 路径；不带 `/` 的模式按**基名**匹配任意深度；`**` 跨段、`*`/`?` 段内；
  大小写折叠随平台。
- **与 v0.19 拒绝 glob 的差异**：不再有"命中目录的子树也拒绝"（`**/.git` 不隐 `.git/config`）。
- `kit.Config.HiddenPaths` 是零配置入口；`sandbox.Status`/`Preflight` 不含"保护路径"概念。

### 5.5 jobs / terminal：宿主不再解析人读文本

- `tools/jobs` 从"包内 background 类型"提升为公共类型：`Job`/`JobView`/`JobSnapshot`/`BackgroundJobs`/
  `Operation`/`OperationSpec`/`ProcessSpec`/`Outcome`/`Status`/`Kind`/`Source`/`JobScope`/`ScopeResolver`、
  `Manager.Snapshot()`、`Manager.ToolsFor(owner)`、`WithScopeResolver`。
- `rig.ToolResult.Data`：job 五个工具都填结构化数据（`job_output`: `output_offset`/`next_offset`/
  `output_truncated`；`job_list`: `{"jobs":[...]}`）。
- 一致性修复：`job_*` 的文本状态与 `Data` 改为**同一次快照**（此前"文本 running / Data exited"矛盾）。
- `tools/terminal` 同理：`Scope`/`ScopeResolver`、`Manager.ToolsFor/ToolsWithListFor(owner)`，PTY 句柄可跨工具集共享
  （`full.AllOptions.BackgroundTerminals`）。
- `job_start`/`terminal_open` 与 `bash` 同口径：read-only + 有 runner 时允许（仍经 `Runner.Confine`）。

### 5.6 撤销：非 git 工作区也能用 + 回合号跨进程

- `undo.Backend`（Take/Revert/Changes/Prune/Turns）+ `NewFileTracker(root, FileOptions)`：
  非 git 仓库把改动/新增文件镜像到 `Dir/turns/<turn>/files/<rel>` + `manifest.json`（权限位），
  默认 4 MiB 上限（超限只记 warning、不改动）。
- 回合号持久化在 `Dir/state.json`（原子写），多实例取 `max(内存, 磁盘)`。
- 安全细节：回退一律 `Lstat` 判定类型、写走"先删 + `O_EXCL` 创建"、符号链接/目录/设备文件只 warning，
  只读（0444）文件可回退并恢复权限位，文件被换成目录时给准确原因且不删目录内容。
- `kit.Config.UndoDir`：非 git + 设置后挂 `FileTracker`；git 仓库仍优先 `GitUndo`。

### 5.7 溢出：可读回的句柄 + 按作用域回收

- `spillfile.New(dir)` 实现 `rig.SpillStore`（文件后端）：`Save` 原子写、`Load` 拒路径穿越、
  `GC` 只回收同作用域；`rig.WithSpillScope`/`SpillScope` 把作用域打在 ctx 上。
- 修复：`session/sqlite` 的 `GC(keep)` 以前是**全局**裁剪，会话 A 的回收让会话 B 的 `ref:` 失效
  （模型工具结果永久丢失）；现在按作用域，空作用域只回收空作用域（老数据留桶、不再被会话回收清掉）。
- 删会话连带清它的溢出与成本行（否则无界增长/孤儿成本）。

### 5.8 指令与提示词

- `instructions.WithRepoFileNames`：仓库/祖先级指令文件名可配置（`AGENTS.md` 永远在，传入名追加）。
- `instructions.RenderWith` + `RenderOptions{Trust, Notice}` + `DefaultTrustNotice`：整份指令包进
  `<workspace_instructions trust="untrusted">` 围栏；修了"正文含闭合标签时提前闭合"的逃逸
  （大小写不敏感 + 允许空白的变体一并中和）。
- `rig.AgentSpec.Hooks`（`TurnHook`）：回合边界四个钩子（BeforeRun/AfterRun/BeforeTool/AfterTool）；
  `AfterRun` **一定**被调用（成功/失败/取消/BeforeRun 拒绝，零值 `RunResult`），其 panic 被隔离；
  `BeforeRun`/`BeforeTool` 的 panic 照旧上抛；子 agent **不继承**父的 `Hooks` 与 `SystemPromptFunc`。
- `rig.AgentSpec.SystemPromptFunc`：按运行计算系统提示词（日期/git 分支/权限档不再冻结在构造期；
  同轮多次模型调用复用同一值，前缀稳定）。

### 5.9 装配与工具面

- `tools/presets.ForMode(mode, confined, names)` / `ModeFilter`：**权限档裁剪工具面**。read-only 下
  `kit` 不再注册纯写工具（`write_file`/`edit_file`/`apply_patch`/`str_replace_editor`）与 `undo`；
  `bash`/`pwsh`/`run_code` 只在挂 runner 时保留 → 零配置 read-only 从 40/16 变 **35 注册 / 11 active**。
- `tools/catalog` 新增依赖声明：`Required()`、`OptionalByName`/`OptionalNames`/`Optional`（哪些工具需要
  `Searcher`/`Presenter`/`SpillStore`/`MCPClients`… 宿主可提前知道"接了才出现"）。
- `tools/mcp.SanitizeName`/`NormalizePrefix`/`MigrateLegacyNames`：清洗规则只有一份；旧工具名（含
  `mcp_list_resources` 一族）可机器迁移，推不出 server 的进 `needsUser`。
- MCP 工具不再被**名字级预设**过滤（此前 `personal` 预设会把宿主接进来的 MCP 工具静默裁掉，已修）。
- `apply_patch` 没有策略就**不可用**（`patch.WithPolicy`，缺了每次调用直接报错、什么都不写）。
- `present` 恢复两种形态：文件（`rig.FilePresenter`）与内容产物（`title`+`body` → `rig.Presenter`）；
  `present_artifact` 仍不提供。
- `tools/skill.ToolsWithRoot`：技能默认根跟随工作区（以前在 daemon/GUI 里 `cwd` 与工作区无关时
  静默跳过 `<Root>/.zeokeel/skills`）。

### 5.10 持久化（`session/sqlite`，子模块）

- **v0.25.0 起有 tag**（v0.25.0/v0.26.0/v0.26.1）；schema **core v13**。
- 检索：`rig_messages.search_text` 解码列（正文+推理+工具名+tool_call 参数 + `tool_call_id` + image URL）
  + FTS5（`tokenize='trigram'`，`bm25` 排序）+ CJK 列（unigram + 相邻 bigram，解决 trigram 对 2 字词零命中）
  + `SearchQuery.Mode`/`Offset` 翻页；老库升级自动回填（分批 500 行），**没有 FTS5 的构建降级为 substring**
  （`FTS5Available()` 可查），不 panic。
- 元数据：`UpdateMeta`/`MetaPatch`（改名/换工作区）+ `title_cleared` 标记列（显式清空标题不再被自动标题恢复）。
- 导入导出：`ImportRows`/`SessionRows`（幂等、冲突定位到条号、按 ordinal 对账）、`ExportOptions` 带
  events/costs/checkpoints、`CompareSessions` 纳入标题——往返不丢标题。
- 记忆按工作区分区（`Options.Workspace`，同名页可在两个工作区并存）；成本新增 workspace/run 维度。
- 自愈：段重放只做幂等语句、补 `ALTER TABLE ADD COLUMN`（`rig_messages` 被手工删掉不再让 `Open` 永久失败）、
  删掉用不上的前导通配索引。

---

## 6. 对 ZeoKeel 的映射

| rig 新能力 | 我们能否用 | 动作 |
| --- | --- | --- |
| `sandbox.WithHidden` | **能，且应当** | 删掉 `internal/modules/toolkit/denied_glob_gate.go`（529 行）+ 其测试（379 行），`newPolicy` 换 option；注意子树语义差异 |
| `rig.ToolResult.Data` + jobs 类型化 API | 能 | 退役 `toolkit/jobs_service.go` 的文本解析/截断；`Manager.Snapshot()` 直接给 UI |
| `rig.TurnHook` | 能 | 回合快照/事件双写/成本记账从"Run 外层包装"搬进钩子 |
| `SystemPromptFunc` | 能 | 日期/git 分支/权限档按运行重算（现在是构造期冻结） |
| `spillfile.New` + `SpillStore` | 能 | 替掉自建 spill 目录解析；`spill_read` 工具因此可用（现在只有文件路径） |
| `session/sqlite` v0.26.1 | 能，但仍要评估 | `UpdateMeta`/检索/导入导出补齐了三批（D9）的 3 个反对意见；剩：抬 `modernc.org/sqlite` 到 v1.62.0、SQLite→SQLite 迁移自写 |
| `mcp.MigrateLegacyNames` | 能 | 把出厂/用户 `mcp.disabledTools` 里的旧名一次迁移 |
| `cmd/rigmigrate` 扫描器 | 能 | 升级 SOP 加一步；对本仓实跑结果见下 |
| `kit.Runtime` / 审批记忆 / 激活持久化 | **不能**（我们不用 `kit`） | 若将来改用 `kit` 再评估；当前自研审批队列/会话管理继续保留 |
| `presets.ForMode` | 不能（不用 kit） | 我们的"权限档 → 工具面"裁剪在 `toolkit` 自己实现 |
| `tools/search`/`patch`/`undo`/`shelf` | 能（新能力，不是删自研） | 需产品评审：`undo` 会牵动审批卡与快照 |

### 6.1 `rigmigrate` 对本仓的实跑结果

```
$ go run github.com/zeokeel/zeokeel-rig/cmd/rigmigrate -C /data/mygo
internal/modules/toolkit/denied_glob_gate.go:82      sandbox.Policy.DeniedGlobs → WithHidden/HiddenPatterns
internal/modules/toolkit/denied_glob_gate_test.go:99 sandbox.WithDeniedGlobs → WithHidden
internal/modules/toolkit/denied_glob_gate_test.go:358 sandbox.WithDeniedGlobs → WithHidden
internal/modules/toolkit/toolkit.go:545              sandbox.WithDeniedGlobs → WithHidden
（另有 7 条命中在 audit/upstream/rig 的旧版 vendored 副本里，非本仓代码）
rigmigrate: 11 removed API use(s) found
```

即：**本仓真正的移植面只有 denied-globs 这一处**（另外 `secrets`/保护路径/`presets` 我们都没引用），
与临时升到 v0.26.1 编译的结果（2 个编译错误）一致。

---

## 7. 升级到 v0.26.1 的建议顺序

1. `WithDeniedGlobs` → `WithHidden`（+ 决定是否保留"子树也隐藏"的薄包装）。
2. 删 `denied_glob_gate.go` / `denied_glob_gate_test.go`，撤掉 `rebind.go`/`lsp.go` 的包装字段。
3. `denied_glob_gate_test.go` 的行为用例改成"策略级"断言（`policy.Hidden` + 工具结果），保留行为级回归。
4. `jobs_service.go` 换 `ToolResult.Data`（可与 3 并行）。
5. 全量验证：`gofmt` / `go build ./...` / `go vet ./...` / `go test ./...` / `scripts/design-check.sh` /
   `designlint --strict` / `go tool mygo vet .`。
6. 单独排期（不阻塞升级）：`TurnHook`、`SystemPromptFunc`、`spillfile`、`session/sqlite`。

## 8. 限制

- 中间版本（v0.24.1/v0.25.0/v0.26.0）只读了 CHANGELOG + 新增文档 + v0.26.1 源码现状；未逐版编译。
- 导出口径是 `go/ast` 的**声明级**导出面（含 const/var/接口方法），不含结构体字段级的增删；
  `session/sqlite` 的 77 → 84 → 112 中，v0.24 之前的部分（`v0.19` 时代还没有该子模块）按伪版本计。
- `audit/upstream/rig` 是旧版 vendored 副本，`rigmigrate` 的 7 条命中属噪声。
---

## 9. 落地记录（2026-11-20 晚）

本报告第 7 节的建议已执行，最终仓库状态：

- `go.mod`：`github.com/zeokeel/zeokeel-rig v0.26.1`。
- **删除**：`internal/modules/toolkit/denied_glob_gate.go`（529 行）与 `denied_glob_gate_test.go`（379 行）；
  `rebind.go` 与 `lsp.go` 里的 gate 包装字段一并撤回。
- **替换**：`newPolicy` 由 `sandbox.WithDeniedGlobs` 改为 `sandbox.WithHidden(hiddenPatterns(cfg.DeniedGlobs)...)`；
  `hiddenPatterns`（`denied_globs.go`）只做模式加宽（目录条目 + 子树、基名模式补 `**/<p>/**`），
  匹配仍由 `sandbox.GlobMatch`/`Policy.Hidden` 负责。
- **行为契约**：`toolkit/hidden_paths_test.go`（TDD：先红后绿）覆盖子树、基名任意深度、绝对路径、
  `dir/**` 与 `dir/` 两种写法、工作目录跟随、默认空列表、写入拒绝且不落盘、加宽规则的幂等/去重。
- **保留的产品语义**：read-only 档仍禁止执行命令——rig v0.26 允许"read-only + OS runner"执行，
  产品在该组合下不把 runner 交给 `bash`/`pwsh`（`rebind.go`），原文案与拒绝不变。
- 验证：`gofmt` 空、`go build ./...`、`go vet ./...`、`go test ./...`（51 包）全绿、
  `scripts/design-check.sh` PASS、`designlint --strict` 0 new、`go tool mygo vet .` 通过。

---

## 10. 第二批落地：沙箱相关自研代码换 SDK（2026-11-20 深夜）

按"SDK 更好的就接线、自研的删掉"继续，本轮换掉两处：

| 自研 | 换成 | 证据 |
| --- | --- | --- |
| `agent.sandboxShellAutoAllowed`（runner 可用 + 名字是 bash/pwsh 就免问，27 行 + 其单测） | SDK `gate.AllowSandboxedCommands`（shell 类 + runner 自报 enforcement≠none + 策略非 danger-full-access） | 自研判据没有看策略模式、也没看 runner 自报的强制完整度；Landlock 只报 partial、无沙箱宿主若用未实现的 runner 会被当成 partial 而误免问 |
| `contracts.ReadOnlyAutoAllowedTools` 的手写 26 项白名单 | `gate.SandboxReadOnlyTools` 派生 + 两张显式增量表（removals/addition） | 手写表缺 SDK 新增的 `job_wait`/`terminal_read`/`terminal_list`/`search`/`inspect`/`sandbox_status`/`spill_read`/`tool_search`，且每上新工具都要人工补 |

配套改动：
- 新增 `contracts.ToolPolicy`（`CurrentPolicy() *sandbox.Policy`）+ `KeyToolPolicy`，由 toolkit 的绑定组暴露"当前工作区那次构建用的策略"（`rootScoped.policy`），agent 门禁把它连同 runner 交给 SDK。此前 agent 根本不知道策略，所以只能看 `Available()`。
- `internal/modules/sandbox` 的 fail-closed runner 实现 `sandbox.Reporter`：`RunnerEnforcement() = EnforcementNone`。SDK 把"没实现 Reporter"的 runner 按 partial 处理（=可免问），所以这一条必须显式。
- 产品否决保留并前置：只读预设拒绝执行命令（rig v0.26 在有 runner 时允许只读档执行）、`never` 档拒绝、plan/minimal/creator 白名单、会话级权限覆盖。

TDD：先写 `agent/sandbox_gate_test.go` 的 4 个红用例（enforcement=none / 无策略 / 空策略 / danger-full-access 必须回到审批；只读策略必须直接拒绝不弹窗）与 `contracts/permissions_sdk_test.go`（SDK 派生 + 增量表钉死）、`sandbox/sandbox_test.go` 的 reporter 用例，再实现，全部转绿。

### 第三批：DSH goal 三件套交给 SDK

`internal/modules/hosttools/tools.go` 的注释里写着"Rig v0.16.2 只提供没有 context 的 goal 选项，所以产品自研三件套"——那是 2026-11 中旬的事实。rig v0.25 的 `tools/hosttools.Options.GoalRevision` 已经是 ctx-carrying 的 `GoalRevisionStore`，而产品的 `internal/hosttools/goalrevision.go` 恰好实现了同一个接口。于是：

- `rigHostOptions` 传 `GoalRevision: goalStore`，`rigHostToolNames` 加入 `get_goal`/`create_goal`/`update_goal`；
- 删除 `internal/hosttools/goaltools.go`（343 行）与 `goaltools_test.go`（173 行）；
- 保留 `goalrevision.go`（直接人类回合、轮次下限、阻塞原因等产品语义都在 store 里，不在工具里）；
- 既有模块级行为测试（`internal/modules/hosttools/goal_dsh_test.go`：create/get/update/pause/resume/complete/blocked/256 轮默认/直接人类回合）全绿，工具名/schema/结果 JSON 不变。

剩余可换项（按收益降序，均未做）：`jobs` 的 `ToolResult.Data`（可退役 `jobs_service.go` 的文本解析）、`spillfile` + `SpillStore`（替掉自建 spill 目录解析并让 `spill_read` 可用）、`session/sqlite`（三批 D9）、`TurnHook`/`SystemPromptFunc`、`sandbox.Preflight` 驱动的设置页状态文案、`require_escalated` 逃生口。

## 11. 个人客户端口径：把"我们多加的安全规则"删掉（2026-11-21 凌晨）

产品定位是**个人 AI harness 客户端**（单用户、自己的机器、自己付钱），所以这一轮只做一件事：凡是产品自己加的、SDK 没要求的限制，删掉。

| 删掉的自研限制 | 现在的行为（SDK 口径） |
| --- | --- |
| `defaultCommands` 命令名白名单（50 条） | `sandbox.WithAnyCommandAllowed(true)`：审批通过的命令直接跑；`toolkit.Config.CommandAllowlist` 仍可显式收窄 |
| `AllowShellOperators` 默认关闭（管道/`&&`/重定向被拒） | 默认 `WithAllowShellOperators()`：普通开发命令不再因语法被拒；配置字段保留但忽略 |
| "只读档禁止执行命令"（产品额外规则 + 扣住 runner） | SDK 语义：只读 + runner 时命令照跑（写被 runner 围栏），没有 runner 时策略拒绝 |
| fail-closed `unavailableRunner`（无系统沙箱时所有命令失败） | `Runner()` 返回 nil：命令以宿主权限运行，只受应用层策略与审批约束；设置页如实说明"仅应用层校验" |
| 我们从未接线的两个 SDK 沙箱工具 | 已注册：`sandbox_status`（只读、自动免问）与 `request_sandbox_access`（申请一个目录的会话级可写授权，审批门禁决定） |
| DSH goal 三件套（343 行自研） | `tools/hosttools.Options.GoalRevision` + 我们的 `GoalRevisionStore` 适配器，自研工具与其测试删除 |

验证：`go test ./...`（51 包）全绿、`design-check`/`designlint --strict`/`mygo vet` 通过；新增测试 `toolkit/policy_test.go`（任意命令 + 全语法默认、显式白名单仍可收窄）、`toolkit/sandbox_tools_test.go`（status 报告模式与根、grant 后出现在 grants 里）、`agent/sandbox_gate_test.go`（只读 + runner 免问、只读无 runner 询问）。
