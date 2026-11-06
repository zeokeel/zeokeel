# eino 依赖审计与替换规格（ZeoKeel-Rig 调研线 A）

> 对象：`/data/mygo`（HEAD `40368d1`；旧工程在 `old/`，独立 module `github.com/zeokeel/zeokeel/old`，可 `go list`/`go build`；新根工程无 eino）。
> 目标：量化旧工程对 `github.com/cloudwego/eino`（v0.10.0-alpha.35）的依赖，给出「抛弃 eino」的替换规格、依赖收益与分阶段成本，作为 ZeoKeel-Rig SDK 的决策证据。
> 环境：离线，`PATH=/usr/local/go/bin:$PATH GOPROXY=off GOSUMDB=off GOFLAGS=-mod=mod`；只读分析，未改产品代码、未 commit。
> 证据分级：**[A]** 本报告命令实测（脚本见附录 A，全部可复现）；**[B]** 源码/模块缓存阅读并给 `file:line`；**[C]** 推断与估算（人日、体积预测）。
> 产物：本文件 + `.probe/research/rig-sdk/`（`ea.go` 使用矩阵、`deps.py` 依赖收益、`build_probe.sh` 体积/冷编译探针、`out/` 原始数据）。

## 0. 结论摘要

**[A]** 依赖面很小、很集中：`github.com/cloudwego/eino` 只被 **20 个文件**引用，且**全部在 `old/internal/runtime`**（9 个生产文件 + 11 个测试文件）；涉及 **8 个 eino 包、68 个符号**（生产 67 个）。旧 runtime 共 23 个生产文件/6,434 行，其中 **14 个文件/2,970 行（46%）已经完全不碰 eino**（Provider/Tool/History/Gate/Compactor/Checkpoint/Approval/Sink/Review 等自有抽象）。

**[A]** eino 的代价很重：二进制探针显示，仅引用 `eino/schema` 就使 stripped 二进制从 1.44MiB 涨到 **7.64MiB（+6.20MiB）**；引用旧工程实际使用的 8 个包后为 **12.40MiB（+10.96MiB）**。旧工程 `go.mod` 的 51 条 require 中 **20 条（39%）可随 eino 删除**（直接依赖 9→8、间接依赖 42→23）；链接包 434→356（**−78，−18%**）、链接模块 43→22（**−21**）、构建清单 117→109；eino 及其依赖在二进制中贡献约 **11.0 MiB**（旧 stripped 二进制 33.81MiB 的 ~32.4%，替换自研核心约 +0.2–0.5MiB 后净收益 **~10.5MiB**）；隔离冷编译探针 18.18s→4.18s（同规模代码）。

**[B]** 可替换性好于预期：旧 runtime 已把 eino 关在**两个适配器 + 一个 ReAct 循环**里——`Provider`（自有流式接口，`provider.go:205-238`）经 `providerChatModel` 适配 eino model（`model_adapter.go:22-203`），`Tool`（自有接口，`tool.go:14-20`）经 `einoInvokableTool` 适配（`model_adapter.go:381-556`）；`compose.Graph`、`callbacks/tracing`、`flow` **完全未使用**，eino 只提供了「流式 ReAct 循环 + 消息流类型 + 两个 legacy 拓扑预构建」。持久化层不存 eino 类型（store/session 0 引用）→ **无需数据迁移**。

**[C]** 成本与结论：自建最小核心替换 eino，生产代码新增/改写约 **1,300–1,950 行**、测试迁移约 **3,600 行/173 个测试**（其中 67 个在直接引用 eino 的测试文件里）；分 P0–P3 约 **22–35 人日**；若同时完成 Rig SDK 公共 API/文档/版本化再加 **8–12 人日**，合计 **30–47 人日**。**建议抛弃 eino**（面向第三方 import 的 SDK 不应继承 alpha 依赖与 20 个额外模块）；**不建议**为旧工程做 eino adapter 过渡——新根已是 eino-free 骨架（0 引用、283 链接包、10.20MiB 二进制的 Stage 0），Rig SDK 应直接建在新核心上；adapter 仅在「旧产品必须持续发版」时作为 3–5 人日的风险缓冲。

## 1. 方法与口径

- **使用矩阵**：AST 解析 `old/**/*.go` 的 eino import 与 `pkg.Symbol` 选择子，按文件/包/符号计数（`.probe/research/rig-sdk/ea.go`，输出 `out/eino_usage.json`、`eino_files.md`、`eino_symbols.md`）。只统计 `github.com/cloudwego/eino` 及其子包；测试文件单独标记。
- **依赖收益**：`go mod graph`（444 条边）+ `go list -m all`（117 个构建清单模块）+ `go list -deps ./cmd/zeokeel`（434 个链接包）联合分析：从旧工程 31 个自有包出发做包级 BFS，删除 eino 包后「不可达」的链接包/模块即为二进制与 go.mod 可减部分（`deps.py`）。
- **体积/冷编译**：在 `.probe` 内复制 `old/go.mod/go.sum` 建立探针 module（不改产品代码），分别构建 baseline（无 eino）、schema-only、full（旧 runtime 实际使用的 8 个 eino 包），`-ldflags '-s -w'` 与发布产物一致；冷编译用独立 `GOCACHE` 计时（`build_probe.sh`），与共享缓存隔离。
- **行为契约**：读 `old/internal/runtime` 9 个耦合文件与 eino 模块缓存源码（`/root/go/pkg/mod/github.com/cloudwego/eino@v0.10.0-alpha.35`）；搜索旧代码中所有注释型「eino 行为适配」以还原隐式契约。
- **口径提示**：LOC 为物理行数（含空行/注释）；探针体积是「同进程链接增量」，与整包旧二进制并非严格线性相加（见 §5.2 说明）；未执行旧工程全量 `go test`/`go build`（只读分析，仅 `go list`/`go mod graph`/探针构建）。

## 2. 使用矩阵（核心）

### 2.1 总览

| eino 包 | 符号数 | 引用次数 | 旧功能承载 |
| --- | --- | --- | --- |
| `schema` | 28 | 85 | 消息/工具调用/流式/用量/多模态的统一中间表示 |
| `adk` | 18 | 27 | ChatModelAgent 拓扑、Runner/事件流、middleware、AgentTool、max iterations |
| `adk/prebuilt/planexecute` | 8 | 8 | legacy planexecute 模式（plan→execute→replan） |
| `adk/prebuilt/supervisor` | 2 | 2 | legacy supervisor 模式（supervisor + 子代理） |
| `components/model` | 6 | 13 | model.BaseModel/ChatModel/ToolCallingChatModel/Options |
| `components/tool` | 3 | 6 | tool.BaseTool/InvokableTool/Option |
| `components/tool/utils` | 1 | 1 | `InferTool`（deep 模式 2 个工具的结构体→JSON Schema） |
| `compose` | 2 | 5 | `ToolsNodeConfig`（工具表）与 `GetToolCallID`（审批关联） |

- eino 模块本身：**75 个包 / 85,804 行生产代码**（另 136,449 行测试）；旧工程只 import 8 个包，二进制中链接 **30 个 eino 包 / 49,189 行**。
- 版本 `v0.10.0-alpha.35`（发布时间 2026-09-20，[A] 模块 `.info`）；本机模块缓存另有 `v0.9.22`，可离线对比，但本报告不评估 API 迁移。
- 值得注意：**仅 `eino/schema` 一个包就带进 23 个模块/163 个包**（探针 schema-only：gonja 11 包、sonic 9 包、go-toml 5 包、x/exp、easyjson、yaml.v3、logrus…），因为 eino schema 内部引用了模板引擎与序列化栈——这是「eino 并不轻」的直接证据（[A] `go list -deps ./schema`）。
- 68 个符号中 **28 个是 schema 类型/构造器**（`Message` 单符号 146 次引用），18 个 adk 符号覆盖 Agent 生命周期；eino 的抽象面远大于旧工程实际使用面（模块 75 包 vs 用到 8 包）。

### 2.2 逐文件矩阵（生产 9 个文件）

| 文件（LOC） | 使用的 eino 包.符号（节选） | 承载功能 | 替换难度 [C] |
| --- | --- | --- | --- |
| `agent.go` (1064) | `adk.{NewChatModelAgent,ChatModelAgentConfig,ChatModelAgent,Agent,NewRunner,RunnerConfig,AgentEvent,AsyncIterator,ErrExceedMaxIterations,ToolsConfig}`, `components/model.BaseModel`, `schema.Message` | Agent 生命周期：拓扑构建/重建、turn 串行、Runner 迭代消费、事件翻译、max-iterations 优雅收尾 | **高**（core 主战场，但逻辑多为自有状态机，可平移） |
| `model_adapter.go` (556) | `model.{ChatModel,ToolCallingChatModel,BaseModel,Option,GetCommonOptions}`, `tool.{InvokableTool,Option}`, `compose.GetToolCallID`, `schema.*` | Provider↔eino 模型适配、流式 chunk→schema.Message、用量转换、工具适配（审批/自动审核/问题门/callID 关联）、工具错误转文本 | **高**（两层适配都在此，但一半逻辑是自有业务，可拆出复用） |
| `stream.go` (350) | `adk.MessageVariant`, `schema.{Message,TokenUsage,StreamReader,ConcatMessages}` | 把 eino 的 MessageVariant（含流式）翻译为 state.Token/Reasoning/ToolStart/ToolEnd/Usage 事件 | **中**（~200 行可直接改为消费 Provider Chunk） |
| `modes.go` (333) | `adk.{Agent,ChatModelAgentConfig,ChatModelAgentMiddleware,NewChatModelAgent,ToolsConfig}`, `supervisor.New`, `planexecute.*`, `model.ToolCallingChatModel`, `tool.BaseTool`, `compose.ToolsNodeConfig` | 6 种模式拓扑构建：standard/minimal/creator + legacy deep/supervisor/planexecute | **中**（standard 三态极易；supervisor/planexecute 建议删/映射） |
| `mode_deep.go` (145) | `adk.{AddSessionValue,ChatModelAgentConfig,NewChatModelAgent,ToolsConfig}`, `tool.BaseTool`, `utils.InferTool`, `compose.ToolsNodeConfig` | deep 模式的 `task`（子代理）与 `write_todos` 两个工具 + 拓扑 | **中**（工具本身自有语义；仅 `InferTool` 反射 schema 需手写替代） |
| `multimodal.go` (280) | `adk.{BaseChatModelAgentMiddleware,ChatModelAgentMiddleware,ChatModelAgentState,ModelContext}`, `schema.{MessageInputPart,MessageInputImage,MessagePartCommon,ChatMessagePartTypeImageURL,ImageURLDetail*}` | 把工具结果里的图片注入下一轮模型输入（BeforeModelRewriteState 修改 state.Messages）；ImagePart↔schema 多模态互转 | **中低**（runtime.Message.Images 已存在；只需请求构建期注入，middleware 可省） |
| `subagent.go` (465) | `adk.{NewAgentTool,ChatModelAgent,ChatModelAgentConfig,NewChatModelAgent,ToolsConfig}`, `tool.{BaseTool,InvokableTool}`, `compose.ToolsNodeConfig` | 子代理：受限工具子 ChatModelAgent、`AgentTool` 调用、深度限制、并发安全缓存 | **中**（改为直接跑子 loop 即可，eino 的 AgentTool 事件桥/interrupt 旧代码未用） |
| `history.go` (164) | `adk.MessageExtraKeySystemReminder`, `schema.{Message,SystemMessage,AssistantMessage,ToolMessage,WithToolName,ToolMessageOption}` | 存储态 Message ↔ eino schema 转换；compaction 摘要的「非首条 system」hack | **低**（转换目标可改为自有类型；hack 随 eino 消失） |
| `question_gate.go` (107) | `compose.GetToolCallID` | ask_user 阻塞问答：从 eino ctx 取 callID 关联回答 | **低**（换自有 ctx key） |

补充事实（[B]）：

- 9 个生产耦合文件共 **3,464 行**（runtime 生产代码的 54%）；其中 `model_adapter.go` 的后半段（审批 Gate/审核/问题门/callID，约 260 行）本质是旧业务，可整体搬到新 loop。
- `agent.go` 中大量代码是「自有状态机 + eino 适配注释」（35 处 eino 提及），真正依赖 eino 的只是 `NewChatModelAgent/NewRunner` 的两处构造与 `AsyncIterator/AgentEvent` 的消费（`agent.go:767-777,891-927`）。
- `stream.go` 的 eino 依赖集中在 `adk.MessageVariant` 的类型断言；事件语义（ToolStart/ToolEnd/Usage/Reasoning）全部是旧工程定义的 `state.Event`。

### 2.3 生产代码已用符号全量（按包，[A] `eino_symbols.md`）

- `adk`（18）：`AddSessionValue`、`Agent`、`AgentEvent`、`AsyncIterator`、`BaseChatModelAgentMiddleware`、`ChatModelAgent`、`ChatModelAgentConfig`、`ChatModelAgentMiddleware`、`ChatModelAgentState`、`ErrExceedMaxIterations`、`MessageExtraKeySystemReminder`、`MessageVariant`、`ModelContext`、`NewAgentTool`、`NewChatModelAgent`、`NewRunner`、`RunnerConfig`、`ToolsConfig`。
- `schema`（28）：`Assistant`、`AssistantMessage`、`ChatMessagePartTypeImageURL`、`CompletionTokensDetails`、`ConcatMessages`、`FunctionCall`、`ImageURLDetail`、`ImageURLDetailAuto`、`Message`、`MessageInputImage`、`MessageInputPart`、`MessagePartCommon`、`Pipe`、`PromptTokenDetails`、`ResponseMeta`、`RoleType`、`StreamReader`、`System`、`SystemMessage`、`TokenUsage`、`Tool`、`ToolCall`、`ToolInfo`、`ToolMessage`、`ToolMessageOption`、`User`、`UserMessage`、`WithToolName`。
- `adk/prebuilt/planexecute`（8）：`Config`、`ExecutorConfig`、`New`、`NewExecutor`、`NewPlanner`、`NewReplanner`、`PlannerConfig`、`ReplannerConfig`。
- `adk/prebuilt/supervisor`（2）：`Config`、`New`。
- `components/model`（5，生产）：`BaseModel`、`ChatModel`、`GetCommonOptions`、`Option`、`ToolCallingChatModel`。
- `components/tool`（3，生产）：`BaseTool`、`InvokableTool`、`Option`。
- `components/tool/utils`（1）：`InferTool`。
- `compose`（2）：`GetToolCallID`、`ToolsNodeConfig`。

可见 eino 的「抽象面」远大于「使用面」：68 个符号里 28 个是 schema 类型、18 个是 adk 生命周期，真正承载业务的只有 `ChatModelAgent/Runner`、`ToolsNodeConfig`、`NewAgentTool`、两个 legacy 预构建和 `GetToolCallID`。

### 2.4 测试文件矩阵（11 个直接引用 eino 的测试）

| 测试文件（LOC） | 引用 eino 的内容 | 测试对象 | 迁移方式 |
| --- | --- | --- | --- |
| `agent_test.go` (961) | `model.ChatModel/Option`、`schema.*`（14 符号） | fake eino chat model 驱动的多轮/流式/工具/usage 核心断言 | 把 `fakeChatModel` 改为 `fakeProvider`（Provider 接口已存在，fakeProvider 在 agent_test.go:141） |
| `modes_test.go` (870) | `model.*`、`schema.*`、legacy 模式构建 | 6 模式拓扑、deep 工具、supervisor/planexecute 构建 | legacy 模式若删除，相关 ~5 个 Test 一并删/映射 |
| `max_iterations_test.go` (366) | `schema.{Assistant,FunctionCall,Message,ResponseMeta,TokenUsage,ToolCall}` | 步数上限、优雅收尾、`ErrExceedMaxIterations` | 新 loop 直接用自有计数器，断言可保留 |
| `multimodal_test.go` (316) | `schema.*`（UserMessage/ToolMessage/多模态 part） | 工具图片注入、历史多模态 | 改自有 Message.Images |
| `checkpoint_barrier_test.go` (211) | `schema.Message` | checkpoint 屏障（model/tool 两侧） | 只依赖 fake model 输出，换 Provider fake |
| `mode_acceptance_test.go` (197) | `schema.Message` | 模式验收 | 同上 |
| `subagent_test.go` (190) | `schema.Message` | 子代理调用/深度 | 同上 |
| `mode_pin_test.go` (166) | `schema.Message` | 拓扑固定（turn 内旧模式） | 同上 |
| `advisory_sink_test.go` (141) | `schema.Message` | 预算/提示注入 | 同上 |
| `subagent_rt05_test.go` (115) | `schema.Message` | 子代理并发/缓存 | 同上 |
| `removed_preset_test.go` (52) | `schema.Message` | 预设移除后的模式兜底 | 同上 |

### 2.5 明确**没有**使用的 eino 能力（不需要重建）

- **`compose.Graph`/`NewGraph`**：生产代码 0 引用；只用 `compose.ToolsNodeConfig`（作为 `adk.ToolsConfig` 的嵌入字段，3 处）和 `compose.GetToolCallID`（4 处）。
- **`callbacks`/`tracing`**：0 引用（eino 的 callback/trace 体系完全未接入）。
- **`flow`、`components/{document,embedding,indexer,retriever}`、`adk/filesystem`、`adk/prebuilt/deep`**：未 import（deep 模式是旧工程自搭，理由见 `modes.go:35-40`、`mode_deep.go:16-31`：prebuilt/deep 会引入 `doublestar`）。
- **AgenticMessage / 增强工具（EnhancedInvokableTool）/ 多模态输出（AssistantGenMultiContent）**：未用；只用了经典 `schema.Message` 的 Content/ReasoningContent/ToolCalls/UserInputMultiContent。
- **eino session 序列化/gob、interrupt/resume、AgentTool 事件桥**：旧 runtime 调 `NewAgentTool(...).InvokableRun(runCtx, payload)` 时未传任何 option（`subagent.go:158-164`），stream 事件桥、checkpoint 桥、interrupt 状态均未启用，仅同步取最终文本。
- **`utils.InferTool` + `eino-contrib/jsonschema`**：只用于 deep 模式的 2 个工具（`mode_deep.go:74,90`）；其余全部工具都把手写 JSON Schema 放在工具 Description 里（旧 `runtime.Tool` 接口只有 Name/Description/Invoke，`tool.go:14-20`）。替换后无需反射式 schema 库。

## 3. 行为契约与测试绑定

### 3.1 必须复刻的语义契约（[B]）

| # | 契约 | 旧实现证据 | eino 侧机制 | 替换风险 |
| --- | --- | --- | --- | --- |
| C1 | 流式模型：text/reasoning/tool_calls/usage 增量转成事件；空 keepalive chunk 丢弃；Done 空 chunk 收尾 | `model_adapter.go:74-150,238-256`；`stream.go:147-216` | `schema.Pipe`/`StreamReader`、`MessageVariant.IsStreaming` | 中：事件粒度/顺序是测试断言对象 |
| C2 | 工具调用以**完整调用**下发（旧 Provider 在 provider 层已聚合 delta） | `provider.go:210-238`（Chunk 注释）；`model_adapter.go:258-278` | `schema.ToolCall`/`FunctionCall` | 低：新 loop 直接用自有 ToolCall |
| C3 | 工具错误**转文本**返回模型（eino ToolsNode 遇 error 会让整轮 NodeRunError 失败） | `model_adapter.go:415-481`（注释 456-462 明确说明） | ToolsNode 失败语义 | 低：新 loop 天然可控，但需保留 `IsError` 标记 |
| C4 | 同一 assistant 消息的多工具调用**并行执行** | `modes.go:200-202` 未设 `ExecuteSequentially`；eino 默认并行（模块缓存 `compose/tool_node.go:252-255`） | ToolsNode 并发 | 中：审批并发、ToolEnd 顺序、SDK 语义需明确 |
| C5 | MaxIterations：0=不限；eino 默认对 <=0 回退 20；命中上限以「完成+提示」收尾而非失败 | `agent.go:21-39,510-519,891-927,952-1003`；ADR 0013 | `react.go:419-433` 默认 20；`react.go:34,472` ErrExceedMaxIterations | 中：旧代码已用 1e9 sentinel 绕开，新 loop 直接实现更干净 |
| C6 | 系统消息剥离：Instruction 非空时 eino 丢掉首个 system；compaction 摘要用 `Extra` 标记自救 | `history.go:44-63` | `adk` `defaultGenModelInput` | 低：新 prompt 组装可显式保留摘要 |
| C7 | 工具图片注入：工具结果中的图片在下一轮模型请求前变成 user 多模态输入 | `multimodal.go:19-30,60-126,251-280` | ChatModelAgent middleware `BeforeModelRewriteState` 改 `state.Messages` | 中低：改为请求构建期注入（旧 provider adapter 已再注入一次，`model_adapter.go:179`） |
| C8 | tool call ID 经 ctx 传给工具，用于审批/问答事件关联；无 ID 时补 uuid | `model_adapter.go:446,468,484-517`；`question_gate.go:78` | `compose.GetToolCallID`（模块缓存 `tool_node.go:1616-1627`） | 低：改为自有 ctx key |
| C9 | 子代理：受限工具子 agent、`{"request": ...}` 入参、返回最终文本、深度限制、并发时每 run 独立 child | `subagent.go:109-218` | `adk.NewAgentTool`（模块缓存 `agent_tool.go:94-132,208+`） | 中：需自建嵌套 loop 与深度/取消传播 |
| C10 | deep 模式 `write_todos` 写入 eino session value（无读取方） | `mode_deep.go:33-34,90-104` | `adk.AddSessionValue` | 低：可删除或改为进程内 map |
| C11 | 拓扑热重建：SetMode/SetModel 重建拓扑并原子切换，旧 turn 固定旧拓扑；eino ChatModelAgent 不被并发驱动（runMu） | `agent.go:195-263,531-592,757-761` | eino agent 非并发安全 | 中：新 loop 需保证同一 Agent 串行/每会话独立 |
| C12 | Runner 事件流：AsyncIterator[AgentEvent]，streaming 开启；sessionID 透传 Provider 请求 | `agent.go:767-777,891-927` | `adk.NewRunner`/`AgentEvent` | 低：自有 channel 即可 |

**没有**契约依赖 eino callback/trace、Graph 分支、checkpoint/resume、动态工具/中间件生态。

### 3.2 旧代码里的「eino 适配注释」清单（隐式契约证据）

[B] 生产代码中 13 个文件出现 `eino` 字样（agent.go 35 处、model_adapter.go 27、modes.go 24、mode_deep.go 12、subagent.go 11、multimodal.go 9、stream.go 9、history.go 7、sink.go 3、provider.go 3、manual_compaction.go 2、question_gate.go 2、context.go 1），以下注释直接暴露了替换时必须注意的行为：

- `agent.go:33-39`：eino 对 MaxIterations<=0 回退 20 → 旧代码传 `1e9` sentinel。
- `agent.go:240-242`：eino TypedChatModelAgent 把 per-run 状态存在 agent 上，两个 Runner.Run 并发会 race → 旧代码 `runMu` 串行。
- `agent.go:908-914`：eino 把超限包成 NodeRunError → 旧代码特判 `ErrExceedMaxIterations` 走优雅收尾。
- `history.go:52-63`：Instruction 非空时 eino 丢弃首个 system → compaction 摘要用 `MessageExtraKeySystemReminder` 打标记。
- `model_adapter.go:102-104`：eino 只在看到 EOF 后才构建下一次请求 → 软提示/预算告警要“搭下一次请求”。
- `model_adapter.go:131-139`：空 keepalive chunk 会让 eino 的 concat 收到 nil → 旧代码丢弃或补空 assistant 消息。
- `model_adapter.go:258-262`：ToolCall.Index 留 nil 让 eino 的 ConcatMessages 追加而不是合并调用。
- `model_adapter.go:456-462`：eino ToolsNode 丢弃 error 结果并整轮失败 → 旧代码把工具错误转成文本结果。
- `model_adapter.go:484-486`：eino tools node 把 tool call id 放进 ctx → 审批用 `GetToolCallID` 关联。
- `multimodal.go:19-30`：图片只在 eino agent state 内可见、不持久化。
- `mode_deep.go:16-31`：prebuilt/deep 因 filesystem/doublestar 被弃用，deep 用 ADK 原语自搭。
- `manual_compaction.go:65,81`：eino 拓扑的 per-run 状态 → 手动压缩必须与其他 turn 串行。
- `sink.go:98,129,178`：事件必须在 eino 构建下一次请求前入队（eino 请求边界语义）。

**判读**：这些注释几乎全部是「eino 实现细节泄漏到旧代码」的证据；新核心可以逐条消失（对应 §4.2 的设计点），不会留下兼容负担。

### 3.3 测试绑定量化（[A]）

| 指标 | 数值 |
| --- | --- |
| `old/internal/runtime` 测试文件 / LOC / Test 函数 | **31 / 8,882 / 173** |
| 直接 import eino 的测试文件 / LOC / Test 函数 | **11 / 3,585 / 67**（占测试 LOC 40%、Test 函数 39%） |
| 使用 eino fake model seam（`Config.einoModel`） | 45 处、11 个文件 |
| 使用自有 `fakeProvider`（可原样迁移） | 117 个 Test 函数、18 个文件 |
| 两者都不用的测试 | 9 个文件、45 个 Test 函数（纯自有逻辑） |
| 生产 `runtime.` 引用面（SDK 边界） | 9 个旧包、141 个文件、**1,465 处**引用（session 550、app 296、tools 268、shell 167、provider/openai 131、agentteam 28、mcp 15、skills 7、e2e 3） |
| runtime 公开 API（生产） | 24 个 Config 字段、16 个接口（Tool 3 方法、Provider 1、History 2、Gate 2、CheckpointStore 3 等）、39 个导出类型 |
| 持久化数据是否含 eino 类型 | **否**：store/session 0 个 eino import；落盘的是 `store.Message`/JSONL 与 `runtime.Message` 转换结果 → 无需数据迁移 |

测试迁移的**关键杠杆**：绝大多数语义测试（stream/approval/gate/usage/compaction/checkpoint）都通过 `fakeProvider` 驱动，属于「换 loop 后仍可复用」；真正需要重写的只有 11 个注入 eino fake model 的文件（把 `einoModel` seam 换成 Provider fake，约 3,585 行中的模型桩部分）。

**建议的测试迁移分批**（[C]）：

1. 第一批（P1，零改写）：9 个文件/45 个「两者都不用」的测试——纯自有逻辑，仅随包移动。
2. 第二批（P1，机械改写）：18 个文件/117 个 `fakeProvider` 测试——把 `fakeProvider` 的实现从 `<-chan Chunk` 保留原样即可；loop 换实现后断言不变。
3. 第三批（P2，需要重写模型桩）：11 个文件/67 个 eino fake-model 测试——`fakeChatModel/scriptModel` 的脚本化响应改写到 `fakeProvider`（每条脚本消息对应一个 Chunk 流），预计 2–4 人日。
4. 第四批（P3，删除）：`modes_test.go` 中 legacy supervisor/planexecute 的 ~5 个 Test 随模式决策删除/映射。

## 4. 替换规格（核心）

### 4.1 直接复用（不重建）

旧 runtime 的 14 个 eino-free 生产文件（2,970 行）已构成自有领域层，可整体保留：

- `provider.go`（Message/ToolCall/ImagePart/Usage/Request/Chunk/Provider，238 行）——**新核心的模型契约就是它**；
- `tool.go`（Tool/Registry，91 行）+ `approval.go`（Gate/ApprovalQueue，234）+ `review.go`（ToolReviewer，292）——工具执行与审批/自动审核；
- `compaction.go`/`compaction_llm.go`/`manual_compaction.go`（331+304+152）——压缩只依赖 `Provider` 与 `Message`；
- `checkpoint.go`/`checkpoint_recovery.go`（204+174）——turn 边界 checkpoint；
- `sink.go`（301，事件串行化）、`settings.go`（464）、`advisory_sink.go`（87）、`turn_context.go`（68）、`context.go`（30）。

这也意味着替换 eino **不影响** ContextBudget/Compaction/Checkpoint/审批/自动审核/事件总线的现有行为。

### 4.2 自建最小核心（新增/改写）

| 组件 | 规格 | 约 LOC [C] |
| --- | --- | --- |
| 消息/流类型 | 直接用现有 `runtime.Message/ToolCall/Usage/ToolSpec/Chunk`；`Provider.Stream` 返回 `<-chan Chunk`（本身就是流式）。**不需要**再定义一层 schema/StreamReader/Pipe | 0（已有） |
| ChatModel 接口 | 现有 `Provider` 即最小模型接口；若 SDK 需要 generate 便捷方法，加一个 `Generate` 包装（内部收流） | 30–60 |
| Tool 接口 | 保留 `Tool{Name,Description,Invoke}`；schema 沿用「Description 内嵌 JSON schema」约定；如需结构化参数，新增可选 `Params() json.RawMessage` 而非引入 jsonschema 库 | 20–40 |
| ReAct 主循环 | `for { request = buildPrompt(instruction, history, reminders); stream provider; emit Token/Reasoning; if no tool calls → finish; parallel execute tools (callID ctx); append tool messages; iteration guard }`；取消/错误/优雅上限复用 `completeMaxIterations` 逻辑 | 350–500 |
| 事件翻译 | 把 Chunk 流翻译为 `state.Token/Reasoning/ToolStart/ToolEnd/Usage/Status`（改写现 `stream.go`，去掉 MessageVariant） | 200–300 |
| 工具调度 | 并行执行 + callID ctx + 审批 Gate/自动审核/QuestionGate + 工具错误转文本 + `ToolStart/ToolEnd` 顺序保证 | 250–350 |
| 子代理 | `RunSubagent` 直接驱动子 loop：受限工具、深度、delegated approval fail-closed、并发独立 child；无需 AgentTool 事件桥 | 100–150 |
| deep 模式工具 | `task`/`write_todos` 两个 Tool（描述内嵌 schema）；todos 写进程内 map 或直接丢弃（旧代码无读取方） | 100–150 |
| prompt 组装 | 自有 GenModelInput：instruction(host) + compaction 摘要（作为非首条 system 保留）+ history + user 消息；移除 `MessageExtraKeySystemReminder` hack | 60–100 |
| 多模态注入 | 请求构建期把工具结果图片并入 user 消息（复用 `multimodal.go` 的注入逻辑，删 middleware） | 50–80 |
| 模式/拓扑 | standard/minimal/creator 三态保留（只是工具可见性）；deep=standard+2 工具；legacy supervisor 建议映射为 standard+subagent 工具；planexecute 建议删除（见 §4.3） | 100–200 |
| **合计** | | **1,300–1,950** |

**新核心的验收/等价性测试策略**（[C]）：

- 事件级等价：对同一 `fakeProvider` 脚本，新 loop 产出的 `state.Event` 序列与旧实现逐项对比（Token/Reasoning/ToolStart/ToolEnd/Usage/Status 的顺序与载荷），作为 P1 的硬门禁。
- 流式边界：专门构造「空 chunk/Done chunk/同 chunk 多 toolcall/usage 只在末 chunk」等脚本，锁定 C1/C2。
- 工具并发：同一 assistant 消息并发调用 N 个工具（含需要审批/拒绝/超时），断言审批事件可并发、ToolEnd 集合完整、历史顺序稳定（C4/C8）。
- 步数上限：0=不限 / 1–1000 显式上限 / 命中上限优雅收尾三条路径（C5，对照 ADR 0013 的验收）。
- 多模态：工具结果图片注入后下一轮 request 的 `Message.Images` 与历史不持久化合成消息（C7）。
- 子代理：深度 1..N、受限工具、delegated approval fail-closed、取消传播（C9/C11）。

**设计要点**（对应 §3.1/§3.2 的隐式契约）：

- 流式不需要 `StreamReader`/`Pipe`：旧 `Provider.Stream` 已经是 `<-chan Chunk`，loop 直接消费并转发事件，C1 的“空 keepalive/Done 收尾”规则可原样保留在 loop 里。
- 不需要 `ConcatMessages`：loop 自己累积 text/reasoning/usage/toolcalls；历史记录使用自有 `Message`（旧代码已在 stream.go:205-211 用 ConcatMessages，仅为拿到合并后的 eino Message，可去掉）。
- 工具 callID 不再走 eino ctx：新 loop 在调用每个工具前 `ctx = WithToolCallID(ctx, id)`，`compose.GetToolCallID` 的 4 处调用替换为自有 helper（`model_adapter.go:446,468,499` + `question_gate.go:78`）。
- parallel tools：显式实现「同消息多调用并行、结果按调用顺序写回历史」（C4）；审批仍在各 goroutine 内阻塞，事件经既有 `eventSink` 串行化（复用 sink.go 语义）。
- max iterations：loop 内 `maxIterations<=0` 表示不限（与旧产品默认一致，ADR 0013），命中显式上限时复用旧 `completeMaxIterations` 的收尾（tool pending 关闭、提示、history/checkpoint 保存、idle）。

### 4.3 直接删除、不重建

- `compose.Graph`/编排图、`flow`、callbacks/tracing（**从未使用**）；
- `adk/prebuilt/deep`（从未 import；deep 自搭）；
- `adk/prebuilt/supervisor`/`planexecute`：legacy 模式别名，`Modes()` 不展示（`modes.go:96-102`），仅 `modes_test.go` 绑定；建议 **删除这两个模式**（旧会话若含该 mode 字符串，映射到 standard/deep 并记录一次迁移提示），如需保留语义，自建 plan-execute 循环约 250–350 行；
- `utils.InferTool` + `eino-contrib/jsonschema`（仅 deep 2 个工具；改手写 schema）；
- eino session values + gob 序列化 + interrupt/resume + AgentTool 事件桥（旧代码未用）；
- `schema.Pipe`/`StreamReader`/`ConcatMessages`/多模态 part 类型（自有类型替代，loop 直接累积）。

### 4.4 新旧契约对照表（SDK 面）

| 旧 runtime 契约（保留） | eino 中对应物（删除） | 处置 |
| --- | --- | --- |
| `Provider.Stream(ctx, Request) (<-chan Chunk, error)` | `model.BaseModel/ChatModel/ToolCallingChatModel` | 保留 Provider；删除 model 包依赖 |
| `Tool{Name,Description,Invoke}` | `tool.BaseTool/InvokableTool` + `utils.InferTool` | 保留 Tool；参数 schema 走 Description 或可选 `Params()` |
| `Message{Role,Content,Reasoning,ToolCalls,Images,ToolCallID,ToolName}` | `schema.Message` 及其多模态 part | 保留自有 Message；无需 schema 转换层 |
| `Request{Tools []ToolSpec}` | `adk.ToolsConfig`/`compose.ToolsNodeConfig` | 自有 ToolSpec 直接下发 Provider |
| `state.Token/Reasoning/ToolStart/ToolEnd/Usage` 事件 | `adk.MessageVariant/AgentEvent/AsyncIterator` | 保留事件；loop 直接产出 |
| `Gate/QuestionGate/ToolReviewer` | eino 无对应（旧业务） | 保留，loop 内调用 |
| `History/Compactor/CheckpointStore/CheckpointLoader` | eino 无对应（旧业务） | 原样保留（14 个 free 文件） |
| `SubagentRunner` | `adk.NewAgentTool` | 保留接口；实现改为子 loop |
| Config.Mode 六值 | 拓扑构造器 | 收窄为 standard/minimal/creator/deep（legacy 映射） |

### 4.5 替换的边界与约束

- **必须**：保持旧 `Config`/`Agent`/`Provider`/`Tool`/事件类型的语义（旧包 app/session/shell/tools 有 1,465 处引用，签名破坏会波及 141 个文件）。
- **建议**：SDK 只暴露 `Provider/Tool/History/Gate/Compactor/CheckpointStore/QuestionGate/Message/Event/Agent/Config`；`Message` 等类型从 `internal/runtime` 提到公共包，旧包通过类型别名（`type Message = rig.Message`）过渡。
- **禁止**：把 eino 类型留在公共 API；旧代码已做到（`einoModel` seam 故意 unexported，`agent.go:41-43`），新核心不要倒退。

## 5. 依赖收益（核心）

### 5.1 go.mod 与构建图（[A]，`deps.py`）

| 指标 | 现状 | 去 eino 后 | 变化 |
| --- | --- | --- | --- |
| require 条目 | 51（9 直接 + 42 间接） | 31（8 直接 + 23 间接） | **−20（−39%）** |
| 构建清单模块（`go list -m all`） | 117 | 109 | −8 |
| 二进制链接包 | 434 | 356 | **−78（−18%）** |
| 二进制链接模块 | 43 | 22 | **−21** |
| 依赖源码行（生产） | — | — | 移除 **116,996 行**（其中 eino 模块链接部分 49,189 行） |

可整体删除的 **20 条 require**（全部因 eino 才进入构建图；[A] `dep_reduction.json`）：

`cloudwego/eino`、`eino-contrib/jsonschema`、`bytedance/sonic`、`bytedance/gopkg`、`nikolalohinski/gonja`、`slongfield/pyfmt`、`pelletier/go-toml/v2`、`wk8/go-ordered-map/v2`、`goph/emperror`、`json-iterator/go`、`modern-go/concurrent`、`modern-go/reflect2`、`mailru/easyjson`、`bahlo/generic-list-go`、`buger/jsonparser`、`yargevad/filepathx`、`sirupsen/logrus`、`pkg/errors`、`golang.org/x/exp`、`gopkg.in/yaml.v3`。

（`bytedance/sonic/loader`、`cloudwego/base64x` 虽在旧 `go.mod` 间接区，但不在「链接包」归因里；`go mod tidy` 会按最终 import 图一并清理，量级为 1–3 条，未计入上表保守值。）

二进制内被移除的 21 个模块（链接包数，[A]）：

| 模块 | 包数 | 模块 | 包数 |
| --- | --- | --- | --- |
| `cloudwego/eino` | 30 | `nikolalohinski/gonja` | 11 |
| `bytedance/sonic` | 9 | `pelletier/go-toml/v2` | 5 |
| `mailru/easyjson` | 2 | `golang.org/x/exp` | 2 |
| `bahlo/generic-list-go` | 1 | `buger/jsonparser` | 1 |
| `bytedance/gopkg` | 1 | `eino-contrib/jsonschema` | 1 |
| `goph/emperror` | 1 | `json-iterator/go` | 1 |
| `modern-go/concurrent` | 1 | `modern-go/reflect2` | 1 |
| `pkg/errors` | 1 | `sirupsen/logrus` | 1 |
| `slongfield/pyfmt` | 1 | `wk8/go-ordered-map/v2` | 1 |
| `yargevad/filepathx` | 1 | `gopkg.in/yaml.v3` | 1 |
| （5 个标准库包） | 5 | | |

**不受影响**（不能算进 eino 收益）：`modernc.org/sqlite`、`egoist/mygo`、`creack/pty`、`modelcontextprotocol/go-sdk`、`golang.org/x/{sys,image}`、`google/uuid`、`ebitengine/purego`、`golang.org/x/text`、`go-text/typesetting`——与任务提示一致。

### 5.2 二进制体积（[A] 探针，stripped，`-ldflags '-s -w'`）

| 探针 | 大小 | 相对 baseline |
| --- | --- | --- |
| baseline（仅 fmt） | 1,511,584 B（1.44 MiB） | — |
| schema-only（`eino/schema`） | 8,011,936 B（7.64 MiB） | **+6.50 MiB** |
| full（旧 runtime 实际 8 个包） | 13,000,864 B（12.40 MiB） | **+10.96 MiB** |
| 旧工程 linux stripped 产物（参考） | 35,451,143 B（33.81 MiB） | — |
| 新根 eino-free 骨架二进制（参考，非功能等价） | 10,690,823 B（10.20 MiB），283 链接包 | — |

- 探针方法：把 `old/go.mod/go.sum` 复制为 `.probe` 内 module（`module probe`），三个 main 包分别引用 0 / 1 / 8 个 eino 包；后两者引用旧 runtime 实际用到的构造器/配置类型以强制链接；`CGO_ENABLED=0`、`GOOS=linux`，与发布产物同 flag。
- **为什么 schema-only 也这么重**：`eino/schema` 内部 import `bytedance/sonic`、`nikolalohinski/gonja`、`eino-contrib/jsonschema`、`uuid` 及 eino 内部序列化包（[A] `go list -deps ./schema` 显示 163 包/23 模块）——即使用最小的消息类型，也要背上模板引擎与 JSON 栈。
- **[A] 冷编译**（独立 GOCACHE，同 probe）：full **18.18s** vs baseline **4.18s**（+14.0s，4.35×）。
- [C] 映射到全仓：旧 `cmd/zeokeel` 冷编译需编译 434 包，其中 78 包是 eino 链（18%），估算冷构建时间可降 **15–25%**（mygo/sqlite 等仍是主要成本）。
- [C] 体积换算：旧 stripped 33.81MiB − eino 链 10.96MiB + 新核心 0.2–0.5MiB ≈ **23.2–23.5MiB（−30%~−31%）**；若只删 eino 不重写 runtime（不可运行），理论上限 −32%。

**如何读这组数字**（[C] 谨慎换算）：

- 探针 full 的 12.40MiB 是「eino 8 包 + 其链接依赖」的可执行代码增量；旧整包 33.81MiB 还包含 mygo(25 包)、sqlite(3 包)、x/image、pty、MCP SDK 等——两者不是可加可比，但「eino 链 ≈ 11MiB、占整包约 1/3」的量级结论成立。
- schema-only 6.50MiB 说明：即使未来 SDK 只想用 eino 的消息类型（不跑 eino agent），也要背上模板/JSON/序列化栈；这正是自建 Message 类型的理由。
- 旧工程未 stripped 二进制约 49.4MiB（51,828,236B），发布用 `-s -w` 后 33.81MiB；新核心无 cgo、无反射代码生成，预计新增 <0.5MiB。

### 5.3 供应链收益

- 构建图少 8 个模块（go.mod 少 20 条 require）、二进制少 21 个模块/78 个包，其中包含以「重」著称的 `bytedance/sonic`（含汇编/加载器）、`nikolalohinski/gonja`（模板引擎 11 包）、`go-toml/v2`（5 包）、`go-ordered-map`、`logrus`、`pkg/errors`、`yaml.v3`——这些在旧工程里**只被 eino 链路使用**（[A] 依赖归因；旧工程其余包 0 引用）。
- eino 版本为 **alpha**（v0.10.0-alpha.35，2026-09-20；本机还有 v0.9.22）：旧代码已为其写过 20+ 处适配注释（§3.2），SDK 依赖它意味着第三方用户被动继承 alpha 锁定；去掉 eino 同时消除 `old/docs/audit` AR-10 记录的 sonic/alpha 风险。
- 许可：eino Apache-2.0（模块 `LICENSE-APACHE`）；去依赖后许可清单减少 21 个模块；无 cgo、无新增系统依赖。
- 可复现：`.probe/research/rig-sdk/out/{mod_graph_old.txt,modules_old.txt,linked_pkg_imports.txt,dep_reduction.json,probe_sizes.txt}`。

## 6. 成本、阶段与人日（[C]）

### 6.1 分阶段

| 阶段 | 内容 | 人日 |
| --- | --- | --- |
| **P0 契约** | 冻结自有核心接口（Provider/Tool/Message/Event/History/Compactor/Checkpoint/Gate）与公共包布局；把 14 个 eino-free 文件+现有测试先跑通；建 Provider fake 等价测试脚手架；确定 SDK 的公共类型清单 | 3–5 |
| **P1 主循环** | ReAct loop + 流式事件 + 工具并行调度 + 审批/自动审核/问题门 + max-iterations 优雅收尾 + 多模态注入；迁移 117 个 fakeProvider 驱动测试；冻结事件顺序契约 | 8–12 |
| **P2 特性迁移** | 子代理嵌套 loop、deep 模式两工具、compaction/checkpoint 接线、history 转换去 eino hack；迁移 67 个 eino fake-model 测试；删 middleware/hack | 6–10 |
| **P3 收尾/删除** | legacy supervisor/planexecute 决策与迁移、删除 20 个依赖与 eino adapters、门禁（gofmt/vet/test/benchmark）、文档与迁移说明 | 5–8 |
| **小计（替换 eino）** | | **22–35** |
| （可选）Rig SDK 包装 | 公共 module 拆分、API 稳定/语义版本、示例、CI、文档 | +8–12 |
| （可选）eino adapter 过渡 | 双后端开关 + build tag + 一致性测试（仅当旧产品必须持续发版） | +3–5 |

### 6.2 风险

| 风险 | 级别 | 说明/缓解 |
| --- | --- | --- |
| 测试迁移面 | 高 | 3,585 行/67 个测试直接引用 eino 类型；但 117 个测试已用 fakeProvider，先 P1 迁 Provider 面再啃 fake-model 面 |
| 流式事件粒度/顺序 | 中 | C1/C12 断言多；用现有 173 测试做等价回归，先冻结「事件契约」再换实现 |
| 工具并行与审批并发 | 中 | C4 默认并行是 eino 隐式行为；新 loop 显式声明（建议保持并行），并补并发审批测试 |
| max iterations 语义 | 中 | C5 旧逻辑有 3 层绕行（sentinel、ErrExceedMaxIterations、优雅收尾）；新 loop 一次实现 0=不限+可选上限 |
| 多模态/图片注入 | 中低 | C7 旧双路径（middleware + provider 再注入），新 loop 单路径即可；注意历史里不要持久化合成消息 |
| 子代理取消/深度/审批 | 中 | C9；旧 AgentTool 的事件桥/interrupt 未用，替换后反而简单 |
| legacy supervisor/planexecute | 低 | 仅 legacy 别名+1 个测试文件；建议删除或映射，需产品确认旧会话 mode 兼容 |
| 隐藏依赖（json schema） | 低 | 仅 deep 2 个工具用 InferTool；改手写 schema |
| eino alpha 升级/回退 | — | 去 eino 后风险消失；保留 eino 则需跟踪 alpha API |
| 体积收益不及预期 | 低 | 探针已隔离量化（+10.96MiB），且替换核心为自有纯 Go 代码（无新增第三方） |

### 6.3 与「保留 eino」的对比

- **保留**：0 人日；但 SDK 的第三方用户被迫接受 eino alpha 依赖（模型/工具抽象、20 个额外模块、sonic/gonja 等），且新根工程已经选择 eino-free（README 硬约束/§0）；两套栈长期并存维护成本高。
- **替换**：22–35 人日（+SDK 包装 30–47）；获得 eino-free 的独立 SDK、−30% 二进制、少 20 条 require、可长期稳定的自有 API。
- **折中（不推荐）**：SDK 公共 API 用自有类型 + 内部 eino 实现（adapter 过渡），可先发 SDK 后再替换；代价是 +3–5 人日与一套 adapter 维护，且第三方仍会链接 eino（体积/供应链收益拿不到）。

## 7. 结论：值不值得、最小边界、过渡方案

**值得（面向 Rig SDK）**。理由：

1. 依赖面集中——20 文件/9 生产文件全部在 `internal/runtime`，其中 46% 已 eino-free；替换不是「换血」而是「重写 loop + 两个 adapter」。
2. 收益确定且大——−20 条 require、−21 模块、−78 链接包、−10.96MiB（−31%）二进制、同规模冷编译 −14.0s。
3. 旧 runtime 已有自有的 `Provider/Tool/Message/Usage` 领域模型与 `History/Compactor/Checkpoint/Gate` 接口，落盘数据也不含 eino 类型，**无需数据迁移**。
4. 对第三方 SDK 而言，绑定 `cloudwego/eino v0.10.0-alpha` 是明显的采用障碍；去 eino 后 SDK 无第三方运行时依赖（除可选 provider/tool 适配包）。

**最小可行边界**：只实现 **standard/minimal/creator + deep + 子代理 + 流式工具循环 + 审批/自动审核/问题门 + compaction/checkpoint（复用）**；`Provider` 作为模型抽象、`Tool` 作为工具抽象、自有 `Message/Event` 作为数据契约。**不重建**：Graph、callbacks/tracing、flow、document/embedding、prebuilt deep、eino session/gob/interrupt、jsonschema 反射；supervisor/planexecute 删除或映射（legacy、无 UI 入口）。

**eino adapter 过渡可行但不必做**：旧 runtime 的 eino 面被限制在 9 个文件、2 个适配器 + 1 个循环，做「新核心 + eino 适配器」双后端在技术上完全可行（+3–5 人日），但仅当「旧产品必须边发版边迁移」时需要；**新根工程已 0 引用 eino**（38 文件/6,065 行 Stage 0、283 链接包、10.20MiB 二进制），Rig SDK 直接从最小核心起步的边际成本最低，建议不保留 eino adapter。

**判断矩阵**：

| 场景 | 建议 | 理由 |
| --- | --- | --- |
| Rig SDK 面向第三方 / 长期产品 | **抛弃 eino** | 收益大、依赖集中、自有契约已就绪；预算 22–35 人日（含包装 30–47） |
| 仅维护旧归档产品、不再发版 | 不替换 | 旧工程归档即可；新根天然无 eino |
| 旧产品需边发版边迁移 | 可做 eino adapter 过渡（+3–5d） | 双后端 + 一致性测试；但会牺牲体积/供应链收益直到收尾 |
| 想快速出 SDK 占位 | 先自定义 API、内部 eino 实现 | 不推荐：第三方仍链接 eino，等于把问题推给用户 |

## 附录 A：可复现命令与脚本

```bash
# A.0 环境
export PATH=/usr/local/go/bin:$PATH GOPROXY=off GOSUMDB=off GOFLAGS=-mod=mod

# A.1 使用矩阵（AST：eino 包/符号/文件，输出 out/eino_{usage.json, files.md, symbols.md}）
cd /data/mygo && go run .probe/research/rig-sdk/ea.go -root old -out .probe/research/rig-sdk/out
# 结果：20 files (prod 9 / test 11), 8 packages, 68 symbols

# A.2 依赖/包归因（先落原始数据，再分析）
cd old
go mod graph > ../.probe/research/rig-sdk/out/mod_graph_old.txt            # 444 edges
go list -m all > ../.probe/research/rig-sdk/out/modules_old.txt             # 117 modules
go list -deps -f '{{.ImportPath}}|{{join .Imports ","}}|{{if .Module}}{{.Module.Path}}@{{.Module.Version}}{{end}}|{{.Dir}}' \
  ./cmd/zeokeel > ../.probe/research/rig-sdk/out/linked_pkg_imports.txt     # 434 lines
cd /data/mygo && python3 .probe/research/rig-sdk/deps.py
# 结果：linked 434→356（−78），modules 43→22，go.mod require 51→31，源码 −116,996 行

# A.3 体积与冷编译探针（独立 module 复制 old/go.mod+go.sum，不改产品代码）
sh .probe/research/rig-sdk/build_probe.sh
# baseline 1,511,584 B / schema 8,011,936 B / full 13,000,864 B
# COLD_FULL_MS=18180, COLD_BASE_MS=4177

# A.4 关键复核
grep -rln 'github.com/cloudwego/eino' old --include='*.go' | wc -l        # 20
grep -rn 'cloudwego/eino' . --include='*.go' --exclude-dir=old --exclude-dir=.probe | wc -l  # 0（根工程无 eino）
cd old/internal/runtime && ls *_test.go | wc -l && cat *_test.go | wc -l   # 31 / 8882
grep -cE '^func Test' *_test.go | awk -F: '{s+=$2} END{print s}'          # 173
```

## 附录 B：使用矩阵原始数据与探针产物

- `.probe/research/rig-sdk/out/eino_files.md`：20 个文件的 eino 包/符号清单（本报告 §2.2/§2.4 的原始表）。
- `.probe/research/rig-sdk/out/eino_symbols.md`：68 个符号的引用数/生产/测试文件数。
- `.probe/research/rig-sdk/out/eino_usage.json`：逐文件逐符号（含行号）。
- `.probe/research/rig-sdk/out/dep_reduction.json`：去 eino 后可删 require 与保留直接依赖。
- `.probe/research/rig-sdk/out/probe_sizes.txt`、`linked_pkg_imports.txt`、`modules_old.txt`、`mod_graph_old.txt`：体积与依赖分析原始输入。

## 附录 C：20 个引用文件的符号全量（[A] `eino_files.md` 摘要）

- 生产：`agent.go` — adk 18 符号 + model.BaseModel + schema.Message；`model_adapter.go` — model 6/13、tool 3/6、compose.GetToolCallID、schema 28 符号；`stream.go` — adk.MessageVariant + schema 5 符号；`modes.go` — adk 5 + supervisor 2 + planexecute 8 + model/tool/compose；`mode_deep.go` — adk 4 + tool.BaseTool + utils.InferTool + compose；`multimodal.go` — adk 4 + schema 10；`subagent.go` — adk 5 + tool 2 + compose；`history.go` — adk 1 + schema 7；`question_gate.go` — compose 1。
- 测试：`agent_test.go`（model.ChatModel/Option + schema 14）；`modes_test.go`（model 4 + schema 8）；`max_iterations_test.go`（schema 6）；`multimodal_test.go`（schema 8）；`checkpoint_barrier_test.go` / `mode_acceptance_test.go` / `subagent_test.go` / `mode_pin_test.go` / `advisory_sink_test.go` / `subagent_rt05_test.go` / `removed_preset_test.go`（均为 schema.Message 单符号）。

## 附录 D：参考

- eino 源码（只读模块缓存）：`/root/go/pkg/mod/github.com/cloudwego/eino@v0.10.0-alpha.35/{schema,adk,components,compose}`；关键语义：`adk/react.go:34,419-433,472`（max iterations）、`adk/chatmodel.go:530-610`（ChatModelAgentConfig/默认 GenInput）、`compose/tool_node.go:227-266,1616-1627`（工具节点/并行/GetToolCallID）、`adk/agent_tool.go:94-132,208+`（AgentTool）。
- 旧工程：`old/go.mod`（eino v0.10.0-alpha.35）、`old/internal/runtime/{provider,tool,agent,modes,model_adapter,stream,subagent,multimodal,history,question_gate,mode_deep}.go`、`old/docs/decisions/0013-agent-step-limit.md`（eino max iterations 事故与决策）、`old/README.md`（“mygo 原生 UI + eino 运行时”的现状描述）。
- 本仓相关调研：`docs/research/kernel-split/03-mygo-module-graph.md`（旧工程依赖图与模块划分）、`docs/research/kernel-split/01-kernel-audit.md`（kernel 审计）。
- 已知局限：未执行旧工程全量 `go test`/`go build`（只读分析，仅 `go list`/`go mod graph`/探针构建）；探针体积为「同进程链接增量」，不含 eino 测试依赖，也不等价于整包旧二进制线性换算；人日为 [C] 估算，未含评审/联调/CI 改造。
