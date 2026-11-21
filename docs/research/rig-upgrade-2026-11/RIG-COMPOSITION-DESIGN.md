# rig 组合层设计：kit 作为 agent 编译器，产品作为薄壳

> 状态：设计稿（2026-11-21）；P1（阶段钩子 + kit 透传）与 P2（Spec/Build/注册表/LoadFile + ExtraTools + 模块能力/生命周期）已落地；P3a 产品 hostbundle 缝已落地；P3b 双装配 parity 已跑通（同 spec 经 direct/kit 事件序列一致），但**完整产品运行**经 kit 路径时发现发现态（session active set）由产品自己持有、kit 的 discovery 默认行为不同，故产品运行路径仍走 direct，待把发现态迁到 rig ActivationStore 后再切。
> 目标读者：维护 rig/kit 与 ZeoKeel 装配层的人。

## 0. 目标与非目标

**目标**

1. rig 作为 SDK 被载入；宿主用一小段组合代码（或一份配置文件）构造 agent。
2. kit 从"构造器"升级为"编译器"：`Spec → 解析 → 校验 → 默认值 → 装配 → 预检 → 冻结`。
3. 四类注入缝：**值策略**、**能力接口**、**阶段钩子**、**宿主模块**；任何一层都能由宿主替换/追加。
4. 当前产品变成薄壳：UI + 业务语义 + 宿主能力适配器，不再自己拼 agent。

**非目标**

- 不做运行期热插拔插件（Go 接口的能力边界必须编译期存在）。
- 不引入脚本/表达式语言；配置文件只描述数据与引用名。
- 不让 kit 接管 UI、团队命名、ACP 宿主入口等产品语义。
- 不做 per-token 钩子；流式增量仍然只走事件。

## 1. 现状（事实基线）

- `rig.AgentSpec` 已有 20+ 字段，且已有 `TurnHook`（`BeforeRun/AfterRun/BeforeTool/AfterTool`，[agent.go:223](../../../agent.go#L223)）；这是"循环内部"的现有缝。
- `kit.Config` 77 个字段，其中约 28 个是接口/指针类型；**没有 JSON tag、没有 Load/Build**（[kit/kit.go:141](/data/zeokeel-rig/kit/kit.go#L141)）。
- kit **不暴露** `TurnHook`；`SystemPromptFunc` 只在 agent 层，kit 的 `RunRequest` 只有 `SessionID/Input/Model/MaxIterations/Gate/Emit`。
- `Config.Tools` 非 nil 时完全替换工具集，并关闭懒发现/零配置体检/SandboxGrants/MCP 拆分/内置工具沙箱包装（[kit/kit.go:1415](/data/zeokeel-rig/kit/kit.go#L1415)）；运行时只能 `ActivateTools/DeactivateTools`，不能 AddTool。
- 产品：UI 约 47.8k 行、后端模块约 27k 行；其中 `agent` 13.6k、`toolkit` 7.6k 是最大的两块。

## 2. 分层

```
L4 宿主壳（产品 UI / TUI / server loop）
      │ 组合：kit.Spec（Go 或 JSON）+ 注册插件
L3 kit.Build —— 编译器/链接器
      │ 解析引用 → 校验 → 默认值 → 装配 AgentSpec → Preflight → Freeze
L2 rig agent loop —— 执行器 + 阶段链
      │ prompt / request / response / tool / turn / compaction
L1 能力实现（内置 + 宿主模块）
      │ Provider/History/Gate/Memory/Runner/... + Tool middleware
L0 原语（Message/Tool/Gate/Event/ContentPart）
```

## 3. 四类注入缝

### 3.1 值策略（可序列化）

model、roots、sandbox mode、approval preset、discovery、prompt 文本/区段、context budget、cost limit、spill、undo、checkpoint 等。它们只影响默认装配，不携带代码。

### 3.2 能力接口（编译期实现）

现有 `kit.Config` 已经覆盖大部分：`Provider`、`History`、`Gate`、`Asker`、`Approvals`、`Memory`、`Skills`、`Runner`、`Jobs`、`Terminals`、`MCPClients`、`Searcher`、`SessionLog`、`Presenter`、`Scheduler`、`Reviewer`、`Goals/GoalRevisions`、`PlanController`、`Storage/Aggregate`、`Undo`、`Compactor`、`Activations`、`SandboxRunner/Policy`、`Meter`。
配置文件只写**引用名 + 参数**，实现由注册表解析。

### 3.3 阶段钩子（需要小改 agent 层）

在现有 `TurnHook` 之外扩展**按运行**的只读/可改阶段，避免一个巨型接口：

```go
type PromptStage interface {
	// Prompt 在系统提示词拼装完成后、哈希/缓存之前调用（可改文本）。
	Prompt(ctx context.Context, draft *PromptDraft) error
}
type RequestStage interface {
	// Request 在每次模型请求前调用（可改 messages/tools/model）。
	Request(ctx context.Context, req *ModelRequest) error
}
type ResponseStage interface {
	// Response 在流结束、消息写入历史之前调用（只读或可改回复）。
	Response(ctx context.Context, resp *ModelResponse) error
}
type ToolStage interface {
	BeforeTool(ctx context.Context, call *ToolCall) error // 审批前
	AfterTool(ctx context.Context, call ToolCall, result *ToolResult) error // 历史/事件前
}
type TurnStage interface {
	TurnStart(ctx context.Context, turn *TurnInfo) error
	TurnEnd(ctx context.Context, summary *TurnSummary) error
}
type CompactionStage interface {
	BeforeCompact(ctx context.Context, history []Message) error
	AfterCompact(ctx context.Context, before, after int) error
}
```

- 每个接口只有一个（或一对）方法，宿主只实现关心的阶段。
- `Order() int` 决定顺序；同序按注册顺序；冲突显式报错。
- **只读阶段**接收值拷贝或只读视图，不能改；**可改阶段**写回后由 agent 重新计算哈希。
- 失败语义：返回 error 中止本回合；`rig.ErrSkipStage` 表示跳过后续同阶段；panic 被隔离并记录。
- 并发：`TurnHook` 实例会被并发 Run 共享；阶段改为 **per-run 工厂**（`StageFactory() Stage`）或明确要求实现自己加锁。推荐工厂，避免宿主踩坑。

### 3.4 宿主模块（把"自研模块"打包）

模块是能力的组合，而不是一个新的 DI 容器：

```go
type Module interface {
	Name() string
	Order() int
	Requires() []string
	Provide() Provide // Tools、PromptSections、Stages、Capabilities
	Init(ctx context.Context, host *Host) error
	Close(ctx context.Context) error
}
```

- `Provide` 里的工具/区段/阶段按名字进入编译计划；同名冲突报错（显式 `Override` 才允许）。
- 模块之间通过 `Requires` 排序；循环依赖在编译期拒绝。
- `Close` 逆序调用，由 `Agent.Shutdown` 统一收尾。
- 模块可以来自产品业务（团队、自动化、洞察、导入器），也可以来自第三方。

### 3.5 工具中间件

```go
type ToolMiddleware func(next rig.Tool) rig.Tool
```

用于日志、脱敏、缓存、限流、指标；在工具解析后包一层，审批仍在 agent 层（审批看到的是最终参数）。顺序：`middleware(最内层=真实工具)`，执行前由 agent 问 Gate。

## 4. 编译器设计

### 4.1 Spec

```go
type Spec struct {
	Version      int
	Provider     ProviderSpec
	Model        string
	Prompt       PromptSpec       // system + sections + instructions + environment
	Tools        ToolsSpec        // preset + names/custom + discovery + initial active
	Policy       PolicySpec       // roots/mode/network/hidden paths
	Approvals    ApprovalSpec
	Capabilities map[string]json.RawMessage // 引用名 → 参数
	Stages       []StageRef                 // 名字 + order + 参数
	Modules      []ModuleRef                // 名字 + 参数
	Extensions   map[string]json.RawMessage // 预留
}
```

### 4.2 注册表

```go
type Factory[T any] func(json.RawMessage) (T, error)

func RegisterProvider(name string, f Factory[rig.Provider])
func RegisterTool(name string, f Factory[rig.Tool])
func RegisterCapability(name string, f Factory[any]) // 或按槽位分类型
func RegisterStage(name string, f Factory[Stage])
func RegisterModule(name string, f Factory[Module])
```

注册发生在进程 init（插件包 blank import）。配置文件只写名字，**不执行任意代码**。

### 4.3 编译阶段

1. **Resolve**：按名字解析模块/阶段/能力；未知名字报错（列出可用名）。
2. **Validate**：preset/mode/approval 冲突、必需能力、模块依赖、阶段顺序、JSON 严格解码（未知字段拒绝）。
3. **Default**：填 kit 既有默认值（personal preset、read-only 免问等），但**不覆盖显式值**。
4. **Assemble**：生成 `rig.AgentSpec`（tools、prompt、policy、hooks/stages、capabilities、spill、checkpoint、recovery）。
5. **Preflight**：沙箱可用性、缺失能力、模型上下文、approval 是否有通道；输出 `Diagnostics`（error/warning）。
6. **Freeze**：冻结计划；`Run` 热路径不再查注册表、不做反射。

`kit.New(Config)` 保留为 `Build` 的语法糖（Go 直接构造的快捷方式），避免破坏现有宿主。

### 4.4 配置文件示例

```json
{
  "version": 1,
  "provider": {"kind": "openai", "baseURLEnv": "ZEOKEEL_BASE_URL", "apiKeyEnv": "ZEOKEEL_API_KEY"},
  "model": "deepseek-chat",
  "prompt": {
    "system": "You are ZeoKeel.",
    "sections": [{"name": "product", "body": "回答要简洁。"}]
  },
  "tools": {"preset": "personal", "discovery": true, "modules": ["team"]},
  "policy": {"roots": ["."], "mode": "workspace-write"},
  "approvals": {"preset": "ask", "asker": "desktop"},
  "capabilities": {"memory": {"kind": "wiki", "dir": "~/.zeokeel/memory"}},
  "modules": [{"name": "team", "order": 10}, {"name": "automation", "order": 20}],
  "stages": [{"name": "audit", "order": 100, "params": {"log": true}}]
}
```

## 5. 产品薄壳的目标形态

保留在产品：

| 产品模块 | 归属 |
|---|---|
| `internal/ui/*`、`uiroot`、`uikit`、`design` | UI 壳（不动） |
| `workspace` 注册表、`settings`、`modelcatalog` | 产品业务/设置 |
| 团队命名/图标/团队 UI、`automation`、`insights`、`importer`、`update`、`appinfo` | 产品业务 |
| ACP 入口（`cmd` 与 `acp` 的产品映射） | 产品宿主入口；传输已用 rig `acp` |
| `agent` 的模型覆盖、产品 prompt 区段、事件桥、团队 runner | 变成 `hostbundle` 模块 |

下移/已在 rig：

| 能力 | rig 位置 |
|---|---|
| agent 循环、prompt、gate、恢复、spill、checkpoint | `agent`、`prompt`、`gate`、`session/*` |
| 工具集/发现/沙箱/审批/子会话/ACP 传输 | `tools/*`、`sandbox`、`session/children`、`acp` |
| 会话存储 | `session/sqlstore`（产品只开库/DSN/Close） |

新增产品包：`internal/hostbundle`——把现有 contracts 服务适配成 kit Host 接口，并注册为模块；`cmd/zeokeel` 只做 `kit.Build(spec)` + UI 启动。

## 6. 迁移阶段与门禁

| 阶段 | 内容 | 门禁 |
|---|---|---|
| P1 | rig：阶段链（先复用 `TurnHook`，再加 prompt/request/response/turn/compaction）；kit 透传 `Hooks` | rig 全量 + 阶段顺序/取消/panic 测试 |
| P2 | rig：`Spec`/`Build`/注册表/`LoadFile`；`kit.New` 变语法糖 | 编译期错误、未知字段、默认值测试 |
| P3 | 产品：`hostbundle` + 双装配（kit 与现有直接装配并存），比较事件/结果 | 产品全量 + ACP/会话/团队回归 |
| P4 | 切换 `cmd` 到 kit；删除重复装配；agent 模块缩成 host 模块 | 全量 verify（含 race/smoke） |
| P5 | 示例：配置文件启动、TUI、server loop | 示例可运行 + 文档 |

## 7. 风险与约束

1. **Prompt 缓存**：只有 PromptStage 可改文本，且必须在哈希/请求前；任何字节变化都会打掉 provider 前缀缓存——文档必须写明。
2. **事件顺序**：明确每个阶段相对 `Emit` 的位置（例如 ResponseStage 在最终文本事件之前还是之后）；补顺序测试。
3. **审批/中间件顺序**：审批在 agent 层、看到最终参数；工具中间件在工具内部，不能绕过 Gate。
4. **并发**：阶段用 per-run 工厂或要求实现自行加锁；禁止在阶段里跑长任务（阻塞回合）。
5. **安全**：插件编译期注册，配置不能执行代码；未知字段/未知名字报错；配置版本化。
6. **性能**：阶段只在回合/工具/模型边界调用，不进入 token 流；编译期一次性解析。
7. **不做的事**：不引入反射式 DI、不允许多个注册表、不允许运行期改已冻结计划。

## 8. 第一个可交付切片

最小闭环（不改产品 UI）：

1. rig：`kit.Config.Hooks []rig.TurnHook` 透传 + `PromptStage`（先只读）。
2. rig：`kit.Build(Spec)` + `LoadFile` + `RegisterModule/Stage/Tool`。
3. 示例：`examples/composed` 用一份 JSON 配置 + 一个自定义工具模块跑一轮（offline provider fake）。
4. 测试：配置解析、模块依赖、阶段顺序、事件顺序、未知字段/名字错误。

这个切片完成后，产品薄壳的迁移才有稳定接口；在此之前不要动产品装配层。
## 9. “super agent”与业务工具的归属判定

“super agent”有两层含义，归属不同：

1. **宽能力单 agent**（工具多、可长跑）：不需要 SDK 新特性；SDK 提供工具/发现/激活/审批/预算/压缩等机制，产品决定**给它哪些工具、什么提示词、什么权限**。
2. **多 agent 编排者**（spawn/管理子 agent、共享任务板）：**机制在 SDK**（`rig.SubagentRunner`、`agent.NewRunner`、`session/children`、`rig.TeamTaskManager`、fork 历史、结构化输出、深度/并发/预算、取消/Stop）；**策略在产品**（团队角色与命名、谁可以 spawn 谁、路由/规划提示词、配额、审批策略、团队 UI）。

### 9.1 五条判定

| 判定 | 放 rig/kit | 放产品 |
|---|---|---|
| 宿主无关？另一个 TUI/server/第三方 app 会原样复用吗 | ✅ | |
| 是机制（怎么跑/怎么委派/怎么记账）还是策略（给谁/给多少/什么角色） | 机制 | 策略 |
| 依赖外部 SaaS/厂商 SDK/组织 schema（钉钉、CRM、工单、OA） | | ✅ |
| 持有凭证/租户/组织数据/合规语义 | | ✅ |
| 需要在 rig 离线测试里可复现、无网络/无厂商依赖 | 纯接口/假实现 | 真实客户端 |

### 9.2 示例

- **钉钉**：纯业务工具 → 产品（或独立插件模块），实现 `rig.Tool`；OAuth/tenant/secret 由产品设置与宿主管理；**不把钉钉 SDK 放进 rig**（会把所有宿主的依赖与攻击面一起放大）。若第二个宿主也要用，抽成组织内独立 Go module/插件，而不是 rig core。
- **团队/子 agent**：`runner`、`children`、`team task board` 的**接口**在 SDK；团队角色、命名、UI、任务拆解策略在产品。
- **记忆/技能/MCP**：存储与协议机制在 SDK（已有）；具体记忆内容、技能库、MCP server 配置在产品。
- **审批/沙箱**：判定与执行机制在 SDK；审批文案、危险操作清单、组织策略在产品。

结论：super agent 不是“一个更大的 agent 对象”，而是 **SDK 的编排机制 + 产品的一组能力模块 + 产品策略**。产品薄壳通过模块注册把钉钉这类业务工具注入，通过实现/配置 Runner 表达团队策略；rig 不吸收业务。
