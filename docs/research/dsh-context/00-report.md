# 00 · dsh-context 原理调研与 ZeoKeel 自研整体规划

> 日期：2026-11-14 · 状态：**调研 + 规划，未改任何产品代码**（仅新增 `docs/research/dsh-context/` 下 4 个文档）
> 调研对象：[`bowenliang123/dsh-context`](https://github.com/bowenliang123/dsh-context) v0.84.1（npm 包 `dsh-context`，Apache-2.0，DSH 第三方插件；本机 DSH 参考实现 `@deepseek-ai/dsh 0.2.1-rc.2`）
> 本仓库口径：ZeoKeel（Go 桌面 AI Agent 客户端，`zeokeel-rig v0.16.2` + `mygo v0.9.0` 原生 UI + `zeokeel-kernel` 编译期模块）
> 配套调研：`01-dsh-context-host.md`（宿主端原理，549 行）· `02-dsh-context-client.md`（客户端原理，445 行）· `03-zeokeel-baseline.md`（ZeoKeel 现状与缺口，278 行）

---

## 0. 一页结论

**dsh-context 是什么**：它把「模型每次请求到底带了什么上下文」变成可观测、可诊断、可管理的产品面——上下文洞察页、会话 Context 标签页、右栏 Context 面板、`/context` 命令、插件设置卡，外加 Fleet（Agent 家族）页。定位不是「用量条」，而是**上下文账本 + 归因 + 趋势 + 浏览器**。

**原理一句话**：它不是读 DSH 的 token-meter，而是**自带一套事件折叠引擎（fold）**——把 durable session log 折成「7 类上下文组成 + 每请求时间线 + 日账本」，注册为 DSH 的 session projection；再用「投影/持久 checkpoint/冷日志重折」三层读取 + `revision/unchanged` 增量协议 + 有界缓存/预聚合/限速回填，把切片喂给前端；官方 meter 只做对账基准（`scripts/diff-fold.ts`）。客户端只是解码与渲染，实时性来自宿主 projection 的 `detailRev`，没有自建 WebSocket。

**ZeoKeel 现状（关键缺口）**：
- 已有：会话底部的「轮/步/tok/s/上下文/窗口/缓存命中/剩余」状态行、每回合 `MessageUsage` + 一条 durable `usage` 事件、transcript/tool timeline 读模型、团队/子代理/Jobs 读模型。
- 缺：**逐模型调用**明细（rig `EventUsage.Iteration` 被丢弃）、**每次请求的上下文快照**（system prompt 分段 / tool schema / 注入来源）、**压缩与清理事件**（手动 `/compact` 不写事件，自动压缩在 rig 内不可观测）、**跨会话聚合**、**成本**。
- 因此：**这不是一个纯 UI 功能**。UI 只占工作量约 1/3，核心成本在「数据基座」：把提示词 section 化 + 补事件 + 写折叠器。

**可行性结论**：可行。dsh-context 的**数据模型、折叠算法、估算口径、读取/缓存协议可照搬**；它的 cordis/投影注册表/worker_threads/React 运行时**不可照搬也不该照搬**——ZeoKeel 是单进程 Go 客户端，可直接用结构体、goroutine、SQLite 和进程内服务替代 HTTP 路由。

**推荐路径（分三档，可独立交付）**：

| 档 | 交付 | 估算 |
|---|---|---|
| **P0 会话内上下文账本**（建议先做） | 数据基座（提示词 section 化、逐调用/快照/压缩/spill 事件）+ 折叠服务 + **会话 Context 标签页** + `/context` | **≈ 9–12 人天** |
| **P1 跨会话洞察 + 成本** | 右栏 Context 面板 + **洞察页**（新 screen）+ 跨会话聚合/投影表 + 成本（价格表）+ 设置页 | **≈ 11–15 人天** |
| **P2 深度诊断** | 注入事件时间线、文件活动、命令执行、Fleet、自动压缩可观测 | **≈ 10–16 人天** |

> 人天含帧测试/快照/i18n/designlint，按 1 名熟悉本仓库的工程师计；依据 `03-zeokeel-baseline.md` §6 细表。

**需要你拍板**（详见 §10）：范围（P0-only 还是 P0+P1）、是否允许改 `zeokeel-rig`、洞察页是否做一级侧栏入口、成本价格来源、语言范围。

---

## 1. dsh-context 是什么

### 1.1 定位与分发

- DSH（DeepSeek Harness）的第三方插件，一个 npm 包、**同一包两个运行时入口**：宿主半（`main` → Node/cordis 插件）+ 浏览器半（`exports["./client"]` → DSH Web 客户端 bundle）（`package.json`，01 §1.1）。
- 一条 `cordis.patch.yml` 插入一行宿主插件；`package.json` 的 `dsh` 字段声明 bundle patch、client 平台与注入依赖。
- 版本闸门 `engines.dsh` 覆盖 `0.2.0-rc.2 / 0.2.1-rc.2 / 0.2.2-rc.1` 三条支持线；兼容策略是「能力探测 + 缺失退化」，不按版本号分支（01 §7）。

### 1.2 它提供的用户面（5 个主入口 + 2 个附加）

| # | 入口 | 作用 |
|---|---|---|
| 1 | **上下文洞察页**（一级侧边栏面板 + 主栏整页） | 跨会话：KPI、7 天用量/成本、8 周热力图、技能/工具/命令统计、构成环、会话卡墙 |
| 2 | **会话 Context 标签页** | 单会话看板：Context Stats / Token Stats / Timing Stats / Current Context / Context Trend / Context Browser / Context Events / File Activity / 命令执行 / AgentGraph |
| 3 | **右侧 Sidebar Context 面板** | 与 #2 同一棵视图树，挂在聊天旁（可配置入口） |
| 4 | **`/context` 斜杠命令** | 居中弹窗：Current Composition + Context Browser |
| 5 | **插件设置卡片** | 入口组合、默认时间范围/粒度/趋势模式、排序、系统提示词前缀/后缀 |
| 6 | （附加）回复行「Context」跳转按钮 | 从某条回复跳到该 step 的上下文 |
| 7 | （附加）Fleet 标签页 | Agent 父子家族网络、跨 Agent 协作时间线、协作任务、事件检查 |

### 1.3 它依赖 DSH 的哪些公共扩展面

- 宿主：`sessionProjections`（硬依赖）、可选 `sessionQuery`、`connection.fetch.register`（POST 路由）、storage/settings、llm 模型目录。
- 客户端：`ctx.slots`（main/sidebar.panellist/conversation.view/sidebar.right.pane.tab/conversation.input.overlay/plugins.bundle.config）、`ctx.sidebarRightTabs`、`ctx.get('inputTriggers')`、`ctx.configForms`、`ctx.locale`。
- 这些契约在 02 §2.1 有逐条「注册 API ↔ 本地 DSH README」对照。

---

## 2. 原理 A：宿主端（只读数据面）

### 2.1 总体数据流（01 §1.3 原图的精简版）

```text
durable session log（每条 committed event）
   │
   ├─ 活会话：DSH 投影注册表对每条事件同步 apply(state, event)
   ├─ 冷会话：sessionQuery 读整段 raw events（projectionMode:'none'）
   ▼
Fold：TimelineState（surface 节点 + 7 类 sums + requests[] + events[] + fileOps/commands/spans + 边界字段）
   │
   ├─→ session projection：contextTimeline / contextActivity / contextHeaders（wire view）
   ├─→ 内存缓存（DetailShadow 8 槽 / foldCache / detail 4 条）+ 持久 projection checkpoint
   └─→ 14 条 POST 路由（detail/delta/facts/snapshot/heads/overview/backfill/commands/fleet/skills/attachment/balance/recom/version）
          │  revision + unchanged 协议、分片（parts/window）、缓存上限
          ▼
       客户端：解码 + 格式化 + 渲染（不做计算、不持有价格表）
```

### 2.2 数据源：两类，缺一不可

1. **DSH 官方投影（活会话首选，零重算）**
   - `contextTimeline`：单会话「当前组成 + 逐 step 请求记录 + 事件 + 文件操作 + 命令 + 计时」。
   - `contextActivity`：按天账本（token/成本/turn/tool/skill/命令）。
   - `contextHeaders`：每次 `request/header` 的工具清单与 token 归因（工具→来源包）。
   - 官方 kernel 投影（`sessionStats` / `tokenUsage` / `contextBreakdown` / `contextPressure`）只作**对账/兜底**：`officialParts()` 优先用 `contextBreakdown.systemTokens/toolsTokens/messageTokens`，缺失时退回自己的折叠值（01 §2.1）。
2. **durable session log 事件（重算/冷读的唯一事实源）**
   折叠器只认 `type/seq/time/data/surfaceOp`；覆盖 `request/header`、`system/message`、`request/context`、`tool/call`、`tool/result`、`assistant/message`、`step/start|end`、`approval/*`、`job/*`、`compaction/summary|prune`、`plan/mode`、`subagent/catalog`、`team/member` 等（01 §2.2 有逐族折叠动作表）。
   - 关键认知：**压缩不是删除事件**，而是「一条带 `surfaceOp:{op:'replace',startSeq,endSeq}` 的 user 消息」替换一段历史（01 §3.3）。

### 2.3 折叠器：不可变、全函数、有界

- 状态 `TimelineState` 约 50 个字段，分 5 组：surface / 分类汇总 / requests / 事件账 / 边界状态（01 §3.1）。
- `Apply(state, event)` **只拷贝被改字段**，未命中事件返回同一引用；任何异常返回原 state——折叠是全函数、坏事件不污染状态（`fold.ts:315-329,975-984`）。
- 「表面替换」：`surfaceOp.replace` 精确 splice；被移除节点进 `archived[]` 带 `goneSeq`；分类 sums 同步扣减；`compaction/*` 先暂存 `shadowedSeqs`，等下一个表面事件再落地（01 §3.3）。
- **内存上界**（可配置，`config.ts:4-13`）：1500 请求 / 300 turn / 400 事件 / 2000 表面节点 / 400 归档 / 400 文件操作 / 400 命令；裁剪写 `floor` 让前端知道「更老的要另拉」；注入类节点被钉住不裁（01 §3.5）。
- **活投影瘦身**：head 只保留最近 2 条请求、清空事件/文件/命令（跑着的命令除外）；明细靠 `DetailShadow`（8 槽 LRU + 512 缓冲 + 20s 空闲清空）按需冷折（01 §2.1、§4.1）。

### 2.4 七类组成与 token 口径

7 类：`system / tools / user / inject / skill / assistant / tool`（`shared/categories.ts:12-20`）。

归类规则（01 §3.2）：

| 类 | 规则 |
|---|---|
| system | 只算**最新存活**非空 `system/message` |
| tools | 最新 `request/header` 的 tools 整体 JSON |
| user | 非注入的 `user/message` |
| inject | `developer/message`，或 `source.kind` 非空且 ≠ user、或带 `source.form` |
| skill | `source.kind ∈ {skill-invocation, skill-catalog}`；或 tool/result 识别为 skill 时把 token 从 tool 迁来 |
| assistant | `assistant/message`（再细分 reasoning / answer / tool-call） |
| tool | `tool/result` |

token 估算（**全部标注为估计**，01 §3.4）：

```text
CHARS_PER_TOKEN=4；ROLE_OVERHEAD=4；BLOCK_OVERHEAD=4
systemTokens(text)  = ceil(len/4)+4
textTokens(text)    = ceil(len/4)+4
toolsTokens(tools[]) = ceil(len(JSON.stringify(tools))/4)+4
messageTokens(msg)  = Σ blockPrice + 4
   text/reasoning = ceil(len/4)+4；tool-call = name+arguments；图片 = DeepSeek v41 公式
图片：PATCH=14、下采样 3、上限 1024、最小 544×544、网格 h*(w+1)+2（仅 DeepSeek 路线精确）
文件：按模型可见句柄文本计价（下界）
```

- 对账：**不 import meter，而是镜像 + 差分**——`scripts/diff-fold.ts` 用真实 log 对拍插件折叠 vs DSH `contextBreakdown`；偏差 8 条明确写在 `docs/compatibility.md`（01 §3.4、§9.1）。这些偏差点就是「自研时必须做决策」的清单。
- 计费/占用的真实数字来自 provider：`prompt`（输入量）、`cacheRead`、`output`、`contextWindow`；组成环只是估算，`anchoredParts()` 会把 7 类按比例缩放到 provider 报的输入总量（01 §2.1、02 §4）。

### 2.5 读取、缓存、回填（性能与正确性的骨架）

- **三层读取**：`live`（投影 snapshot，零 I/O）→ `warm`（DSH 持久 projection checkpoint，零 I/O）→ `cold`（`sessionQuery` 读整段 raw events 后 `foldLog`）。冷读过 `makeColdReadGate`：FIFO 串行 + 堆用量 < 2/3 才放行，否则返回「暂无数据」而不是报错（01 §4.1）。
- **14 条 POST 路由**统一 `{ok,value,revision,unchanged}` 信封；客户端带 revision，命中只回 `{}` + unchanged，零解码、零 diff（01 §4.2、02 §3.2）。
- **分片**：`parts`（requests/nodes/events/files/commands/spans）+ `window`（tail）按需拉；展开才加载、懒挂载、分页。
- **缓存**：`detail` 4 条 / `DetailShadow` 8 槽 / `foldCache` 20s 空闲全清；客户端 22 条缓存策略（detail 64 个会话、6 MiB、LRU+TTL；`boundedCache` 有 maxEntries/maxBytes/maxWeight/ttl）（01 §4.3、02 §3.4）。
- **预聚合与回填**：`overview-cache.v1.json` 原子写 + 2s 防抖 + 文件身份键（path/size/mtime）判新鲜度；`backfill` 自适应切片（150ms 预算、8s 工作/2s 暂停、>30000 事件走 worker、2s→15s 退避触发、最多 10 次）（01 §4.3-4.4）。
- **端点上限**：snapshot ids 256 / heads ids 64 / 单命令 4096 字符等（01 附录 A）。

### 2.6 成本与日账本

- **两段式**：折叠期只累 `{provider, model, 峰/谷, 4 个 token 桶}`；服务期再乘价格（models.dev 价格书，1h TTL，失败 fail-open 不伪造）；DeepSeek 峰段 ×2、1 CNY=0.15 USD（01 §5.1、附录 A）。
- 概览以**日账本**为唯一账单源（400 天 / 每天 64 个名字上限）；继承种子（fork）会重置成本避免父子重复计费（01 §5.2、§9.1）。

### 2.7 兼容性策略（值得抄的一条）

不按版本号写分支，只按**字段形状**判断：先看字段在不在、类型对不对，缺能力就退化（fallback 空投影 / 单发冷折 / 不显示），绝不 throw。01 §7.2 列了 9 个「依赖 DSH 内部实现」的风险点（`deriveEventMessage`、`restore/hydrate/prepare/write`、`session_projcache` 行形状…），自研时这些都要封在适配层。

---

## 3. 原理 B：客户端（表现面）

### 3.1 入口与挂载

5 个主入口全走 DSH 槽位（02 §2.1）：`main`(keyed) + `sidebar.panellist`；`conversation.view` id=context；`ctx.sidebarRightTabs` + `sidebar.right.pane.tab`；`inputTriggers` + `conversation.input.overlay`；`configForms.whileServed` + `plugins.bundle.config`。入口按设置**动态挂载/注销**（关掉 = 注销 slot + 释放数据层，而不是 render 时 `if` 隐藏）。

### 3.2 实时性：靠宿主 projection，不靠 WebSocket

`useTimelineSource()` 读 `projection('contextTimeline')` 的 **slim head**（含 `detailRev`）→ `detailRev` 变化 → 300ms 防抖拉 `facts` 分片 → 服务端 `rev < target` 时 1s 后自动 catch-up → 展开型数据（requests/nodes/events/files/spans）按需 `ensurePart`。洞察页独立：part 懒加载 + 30s 轮询 + 250ms 合并 + backfill 退避（02 §3.3）。

### 3.3 组件与图表

- 会话看板（`contextView.tsx` 933 行）：CurrentComposition 堆叠条 → Context Trend（每请求一根柱，Step/Turn、Total/Delta、DNA、Overview、时长曲线）→ Context Browser（七类可折叠 → 元素 → 完整内容）→ 两列 Stats（Context/Token/Timing）→ 懒挂载的 Events / File Activity / Command Executions / AgentGraph（02 §4.1）。
- 洞察页（`overviewPanel.tsx` 489 行）：页头 + 6 格 KPI + 7 天用量柱 + 构成环 + 热力图/技能/工具/命令 4 tiles + 会话卡墙（02 §4.2）。
- **图表几乎全是 HTML+CSS**：堆叠条/DNA(linear-gradient)/热力图/Treemap 都是 div；仅 8 个文件含 SVG（donut/agent 边/ribbon 等），**0 canvas**（02 §6）。
- 交互：hover 联动（140ms 延迟 + pin 后 320ms 锁定）、pin step、点柱跳 transcript、点文件打开右栏、滚动位置记忆（上限 32）。

### 3.4 性能手段（可直接抄的数字）

趋势 >240 柱开窗（overscan 36、hysteresis 8）、列表 >30 行窗口化（overscan 8）、明细上限 1500/400/2000/400/400/400、折叠区懒挂载、DNA 大 session 惰性（>512 请求）、缓存 22 条策略全部有 TTL/上限、预热 900ms 去抖（02 §8）。

### 3.5 对 mygo 的可替代性（02 §9 的结论）

- **A 类 10 项**（直接可替代）：会话 tab、右栏页签、`/context` 弹窗、集合卡、donut、热力图、骨架屏、条件构建。
- **B 类 13 项**（需自绘/组合）：`ui/chart`（scales/stack/pie）+ `ui.Painter`（path/gradient）做堆叠条/趋势/ribbon/DNA；treemap/agent 网络图需移植纯函数布局；hover 联动是「手动 state + Invalidate」；i18n/设置接入。
- **C 类 3 项**（成本高）：**一级侧栏洞察页**（改壳层与信息架构）、插件设置卡片（ZeoKeel 无 bundle 配置槽）、8 语言 ×750 key 词典量。
- 关键结论：**「先把数据做成 Go 模型，UI 只展示」**——dsh-context 客户端一半代码是数据整形（wire/timelineSource/fleetModel/agentTree 合计 3000+ 行），ZeoKeel 应把等价逻辑放进后端 module，5 个入口共享同一份 model。

---

## 4. ZeoKeel 自研：目标与范围

### 4.1 目标：回答三个问题

1. **我现在带着什么？**——当前请求的上下文组成（system prompt 各段 / tool schema / 历史消息 / 注入的 memory/skill / 工具结果），带估算 token 与占比。
2. **为什么涨/掉？**——按 step/轮的趋势，压缩/清理/注入/模型切换事件标注，能定位「是哪一块把窗口撑爆/压下来的」。
3. **花了多少？**——token 分项与估算成本，跨会话/按天/按模型/按工作区聚合。

### 4.2 范围分层（独立可交付）

| 档 | 能力 | 为什么这个顺序 | 人天 |
|---|---|---|---|
| **P0** | 数据基座（section 化提示词 + `context_snapshot`/`model_call`/`compact`/`spill` 事件 + 折叠器 + `KeyContext` 契约）+ 会话 **Context 标签页**（当前组成/趋势/浏览器/统计）+ `/context` 命令 + 状态行数据源统一 | 没有基座，任何 UI 都是假的；tab 是壳层成本最低、价值最直接的入口 | 9–12 |
| **P1** | 右栏 Context 固定页 + **洞察页**（新 screen）+ 跨会话聚合（`usage_rollup` + `(type,created_at)` 索引）+ 成本（模型价格字段 + 峰谷）+ Context 设置页 | 跨会话才有「洞察」；成本依赖 P0 的 token 账本；洞察页需要设计评审 + 导航 IA 变更 | 10–14 |
| **P2** | Context Events（注入来源 diff）、File Activity、命令执行（耗时/退出码）、Fleet（复用现有 team/subagent 读模型）、自动压缩可观测（启发式标注「推断」） | 诊断深度功能，价值真实但依赖更多事件/口径决策 | 10–16 |

### 4.3 明确不做（避免「看起来完整」的装饰）

- 不做 8 语言（先 zh/en，词典随功能增量）；不模拟 DSH 的插件配置卡/插件市场。
- 不做 HTTP 路由层、projection 注册表、worker_threads、zod 契约——单进程 Go 直接进程内服务。
- P0/P1 不做 DNA 平铺、treemap、Agent 网络图、hover-scrub 这类高频重绘的自绘交互；先做**点击 pin + tooltip + 列表跳转**。
- 不新增右栏模块（右栏仍只有 session 一个多标签模块），不把 Context 做成第二个「侧栏会话列表」。

---

## 5. 目标架构（ZeoKeel 版）

### 5.1 分层与数据流

```text
┌─ internal/modules/agent（现有）──────────────────────────────────────────────┐
│ run(): systemPromptSectionsForRun → 渲染 + 采集                                │
│   appendEvent("context_snapshot", {model, window, sections[], tools[], history})│
│ handleEvent(EventUsage) → appendEvent("model_call", {iteration, tokens, …})    │
│ handleEvent(EventToolResult) → 若 IsSpilledToolResult → appendEvent("spill")   │
│ compact_now() → appendEvent("compact", {before, after, saved, replaced})       │
└───────────────┬──────────────────────────────────────────────────────────────┘
                │ durable events（sessionstore SQLite，现有 events 表）
                ▼
┌─ internal/modules/contextbook（新增后端模块）────────────────────────────────┐
│ fold.go：Apply(state, event) 纯函数 + Bounds（窗口/floor）                     │
│ cache.go：每会话 TimelineState，LRU（8 槽）+ 版本/seq 失效，后台折、UI Post   │
│ service.go：实现 contracts.ContextInspector（SessionContext / ContextItems）  │
│ （P1）insights.go：日账本/模型/工作区聚合 + revision + 投影表读写             │
└───────────────┬──────────────────────────────────────────────────────────────┘
                │ contracts（KeyContext / KeyInsights）
                ▼
┌─ UI（mygo 原生）─────────────────────────────────────────────────────────────┐
│ session 第 4 个 tab（context_pane.go）｜ /context 命令｜ ⋯ 菜单「查看上下文」 │
│ （P1）rightpanel 固定页 pageContext｜ ui/insights 新 screen｜设置页 Context     │
└──────────────────────────────────────────────────────────────────────────────┘
```

### 5.2 契约草案（示意，落地时按 `contracts` 现有风格细化）

```go
// internal/contracts/contextinspect.go（新增）
type ContextCategory string // system|tools|user|inject|skill|assistant|tool

type ContextSection struct{ ID, Label, Source string; Bytes, Tokens int; Hash string }
type ContextTool    struct{ Name, Group string; Tokens int }
type ContextRequest struct {
    Seq, Turn, Step, Iteration    int
    At                            time.Time
    Model                         string
    Prompt, Cached, Completion    int // provider 报的数
    ContextTokens, Window         int
}
type ContextEvent struct{ Kind, Detail string; At time.Time; Seq, Before, After int }
type ContextItem  struct{ Kind, Label, Detail, Ref string; Tokens int; At time.Time; Seq int }
type SessionContext struct {
    Revision  uint64
    Snapshot  struct {
        At       time.Time
        Model    string
        Sections []ContextSection
        Tools    []ContextTool
        Current  map[ContextCategory]int // 估算
        Prompt   int                     // provider 锚点
        Window   int
    }
    Requests []ContextRequest
    Events   []ContextEvent
}

type ContextInspector interface {
    // SessionContext 返回一次快照；ok=false 表示该会话无持久数据（冷态尚未折完）。
    SessionContext(sessionID string) (SessionContext, bool)
    // ContextItems 返回浏览器某一类的分页（按需加载，展开才拉）。
    ContextItems(sessionID, kind string, offset, limit int) ([]ContextItem, bool)
}
var KeyContext = cordis.NewServiceKey[ContextInspector]("zeokeel/context")

// internal/contracts/insights.go（P1）
type InsightsQuery struct{ From, To time.Time; Model, Workspace string; Limit int }
type InsightsService interface {
    Aggregate(InsightsQuery) (Insights, error) // Revision 用于 unchanged
}
var KeyInsights = cordis.NewServiceKey[InsightsService]("zeokeel/insights")
```

> 不在 contracts 里 import rig / UI 类型；UI 模块只 import contracts/uikit/design/icons/lib（AGENTS.md §1、ARCHITECTURE.md §3）。

### 5.3 新增事件（durable，JSON payload；无 schema 迁移）

| 事件 type | 写入点 | payload（要点） | 用途 |
|---|---|---|---|
| `context_snapshot` | `agent.run()` 组装完提示词、首次请求前 | `{model, window, sections:[{id,label,source,bytes,tokens,hash}], tools:[{name,group,tokens}], history:{messages,user,tool,assistant}, injected:{memory,skills,agentsMd,preset}}` | 组成树、注入来源、每轮 diff |
| `model_call` | `handleEvent(rig.EventUsage)`（`agent.go:2348-2350` 处扩展） | `{iteration, model, promptTokens, cachedTokens, completionTokens, totalTokens, contextTokens}` | 趋势/缓存命中/逐 step 账本 |
| `compact` | `compact_now.go` 结果处；自动压缩启发式（P2，标 `inferred:true`） | `{kind, before, after, saved, replaced, inferred}` | 趋势下降标记、压缩事件列表 |
| `spill` | `handleEvent(rig.EventToolResult)`，`rig.IsSpilledToolResult` | `{callId, tool, path, bytes}` | 清理事件、大结果定位 |
| （P2）`context_change` | 相邻 `context_snapshot` 的 section hash diff | `{sectionId, kind, fromHash, toHash}` | 注入时间线（AGENTS.md/memory/skill/preset 变化） |

- **配置/上下文/模型**：`run_start` 目前只记 model（`agent.go:2255`），P0 不动它，由 `context_snapshot` 覆盖。
- **兼容**：新事件类型对旧会话自然缺失；读取方必须容忍「没有 snapshot 的老会话」→ 退化为「只显示估算 + 现有状态行数字」。
- **时间**：`sessionstore` 事件自带 `created_at`；`model_call` 的耗时/工具耗时可由 `tool_call`→`tool_result` 的时间差近似（P2，标注近似）。

### 5.4 提示词 section 化（P0 的关键改动）

现在 `systemPromptForRun` 返回一个拼接字符串（`system_prompt.go:286-305`），`buildRunSystemPrompt` 在其前面用 `appendPromptSection` 追加 memory/skills，`withWorkspaceInstructions`/`withPresetInstructions` 再改 `cfg.SystemPrompt`（`system_prompt.go:374-395`）。改造为：

```go
type PromptSection struct{ ID, Source, Text string }

// systemPromptSectionsForRun 保序产出：environment → approval → mode →
// configured(+workspace+preset+memory+skills 按现有顺序) → tool guidance。
func systemPromptSectionsForRun(...) []PromptSection

// renderSystemPrompt 用 "\n\n" 连接并跳过空段；输出必须与旧实现逐字节一致。
func renderSystemPrompt([]PromptSection) string
```

**验收红线**：新增 golden 测试覆盖 `(goos, mode, prompt 配置, 工具面)` 矩阵，断言 `renderSystemPrompt(sections) == 旧 systemPromptForRun(...)`；现有 `system_prompt_test.go` 不改语义全部通过。

### 5.5 线程 / 缓存 / 一致性

- **折叠在后台**：`contextbook` 订阅 `ChatService.Subscribe`（session 变更）→ 标脏 → 合并（≥300ms）→ goroutine 折 → `UIRuntime.Post(store + Invalidate)`。**绝不在 build/帧回调里读 SQLite**（`store.go:177-179` 单连接）。
- **缓存**：每会话 `TimelineState` LRU 8 槽；`SessionContext` 小对象直接返回；`ContextItems` 分页不缓存大内容（展开才从事件/transcript 读模型取）。
- **一致性**：总量以 provider `ContextTokens` 为锚；7 类分项按估算比例分配并标注「估计」；状态行与新账本**共用同一数据源**（`ContextTokens`），避免两个数字打架。
- **失败降级**：无 snapshot / 折叠失败 / 聚合超时 → 返回 `ok=false` → UI 显示空态 + 「此会话无上下文记录」+ 重试；不抛错、不显示假 0。

---

## 6. 逐文件改动清单

### 6.1 P0（会话内账本）

| 文件 | 动作 | 内容 | 验收 |
|---|---|---|---|
| `internal/contracts/contextinspect.go` | **新增** | 上表类型 + `KeyContext`（~120 行） | `go vet`；契约单测 |
| `internal/modules/agent/system_prompt.go` | 改 | 抽 `PromptSection` + `systemPromptSectionsForRun` + `renderSystemPrompt` | golden：渲染字节不变 |
| `internal/modules/agent/context_capture.go` | **新增** | 组装快照（sections/tools/history 计数 + 估算）、写 `context_snapshot`；`model_call`/`spill`/`compact` 写入 | 事件回放单测（JSON fixture） |
| `internal/modules/agent/agent.go` | 改 | `run()` 在首次请求前采集；`handleEvent(EventUsage)` 取 `Iteration` 写 `model_call`；`EventToolResult` 判 spill | 现有 agent 测试全绿 |
| `internal/modules/agent/compact_now.go` | 改 | 手动压缩写 `compact` 事件 | 压缩测试断言事件 |
| `internal/modules/contextbook/fold.go`、`cache.go`、`service.go` | **新增** | 纯函数折叠（7 类/替换/窗口/floor）+ LRU + `ContextInspector` 实现（~500 行） | 表驱动折叠测试 + benchmark |
| `internal/assembly/registry.go` + `internal/assembly/assemblies/desktop.json` | 改 | 注册 `contextbook.Module()` | 装配测试 |
| `internal/ui/session/context_pane.go` | **新增** | 组成条 + 趋势 + 浏览器 + 统计（`ui/chart`/`ui.List`/`Outline`/`Collapsible`/`Segmented`） | 帧测试 + 快照 |
| `internal/ui/session/view.go`、`session.go` | 改 | `tabContext` 常量、tab 标签、pane switch、`/context` 命令、`Config` 文案 | 帧测试（tab/命令/Esc/空态） |
| `internal/ui/session/commands_help.go`（如适用） | 改 | `/help` 自动收录新命令 | 帧测试 |
| `.cache/ui-conformance` 快照 | 生成 | 新增 Context tab 场景（亮/暗/窄窗/空态） | `ui-snapshot` |

> 说明：`contextbook` 与 `agent` 的 write scope 分离（不同 owner）；P0 不新增 SQLite 表。

### 6.2 P1（洞察 + 成本 + 右栏 + 设置）

| 文件 | 动作 | 内容 |
|---|---|---|
| `internal/contracts/insights.go` | 新增 | 聚合查询/结果 + `KeyInsights` |
| `internal/modules/contextbook/insights.go` | 新增 | 日/模型/工作区聚合、revision、投影表读写 |
| `internal/modules/sessionstore/store.go` | 改 | `CREATE INDEX IF NOT EXISTS idx_events_type_created ON events(type, created_at)`；`usage_rollup(day,model,workspace_id,…)` 表 + upsert + 保留期清理 |
| `internal/modules/agent/usage.go` | 改 | 回合结束 upsert rollup（或 contextbook 订阅事件后写） |
| `internal/contracts/modelcatalog.go` | 改 | `InputPrice/CachedInputPrice/OutputPrice`（每 1M token，可选）+ 峰谷倍率 |
| `internal/ui/settings/modelcatalog*.go` | 改 | 价格字段编辑（模型页） |
| `internal/ui/insights/`（新模块） | 新增 | screen：KPI / 7 天用量 / 热力图 / 构成环 / 会话列表（`ui.Table`/`GridView`） |
| `internal/assembly/registry.go` + `desktop.json` + `internal/ui/sidebar/sidebar.go` | 改 | 注册 screen；`ScreenNavSpec` 加中文名/图标 |
| `internal/ui/rightpanel/rightpanel.go` + `view_context.go` | 改 | 固定页 `pageContext` + 页签 + 命令 `rightpanel.context.open` |
| 设置页（contextbook 或 settings 模块注册） | 新增 | 采集开关/保留天数/成本显示与货币/清空统计 |

### 6.3 P2（诊断深度）

| 能力 | 主要改动 |
|---|---|
| Context Events（注入时间线） | `context_snapshot` hash diff → `context_change` 事件；UI 事件列表 |
| File Activity | 复用 `tool_call` 的 `path`（`agent.go:2686-2699`）；补读/写/搜索分类与行数估算 |
| 命令执行 | `tool_call`→`tool_result` 时间差 + 结果文本 `[exit code: N]` 解析（标注近似/未结构化） |
| Fleet | 补同步 `subagent` 的 `result.Usage`（`subagent.go:189-199`）；复用 `TeamService/ChildAgentService/JobService`；UI 家族卡/时间线 |
| 自动压缩可观测 | 启发式（同回合 `ContextTokens` 显著下降 + 估算 history 超预算）标 `inferred`；或立项改 rig 发 `EventCompact` |

### 6.4 文档同步义务（与代码同 PR）

- 新增 screen / 会话 tab / 右栏固定页 / 命令 / 设置页：更新 `docs/DESIGN.md` §2 场景表与附录 A registry、`docs/AGENT-SURFACES.md`（若算 surface）、`docs/UI-CONFORMANCE.md`（图表/集合用法）。
- 新增自绘（如趋势图用 `ui/chart`+`Painter` 组合）：按 `DESIGN.md` §7.1 + `UI-COMPONENT-MIGRATION.md` + designlint 白名单登记，**不刷新 baseline**。
- 新增持久事件类型：在 `docs/ARCHITECTURE.md` 会话日志一节登记事件族；同步 `CHANGELOG.md`。

---

## 7. 里程碑与验收

### 7.1 里程碑（建议）

| 里程碑 | 内容 | 出口标准 |
|---|---|---|
| **M0 数据基座**（2.5–3.5d） | 契约 + section 化提示词（golden 不变）+ 4 类事件 + 折叠器 + 回放测试 | 事件可回放；折叠 benchmark 通过；旧测试全绿 |
| **M1 会话 Context tab**（3–4d） | 组成条 + 趋势 + 浏览器 + 统计 + 空/错/加载态 | 帧测试覆盖 tab 切换/键盘/空态；快照亮暗窄窗 |
| **M2 `/context` + 一致性**（1.5–2d） | 命令、状态行与新账本共用数据源、⋯ 菜单入口 | 命令帧测试；两处数字一致；designlint 0 新增 |
| **M3 洞察页 + 成本**（7–10d，P1） | 右栏 Context 页 + rollup/索引 + 聚合服务 + screen + 价格字段 + 设置页 | 聚合 1k 会话 < 200ms（后台）；帧测试/快照；DESIGN 场景更新 |
| **M4 诊断深度**（P2，按需） | 注入事件/文件/命令/Fleet/自动压缩 | 每项独立验收，不做「装饰面板」 |

### 7.2 验收命令与测试矩阵

```bash
gofmt -l internal cmd
go build ./...
go vet ./...
go test ./internal/modules/agent/... ./internal/modules/contextbook/... ./internal/ui/session/... -count=1
go test ./... -count=1                     # 全量（慢，CI）
go run ./internal/devtools/designlint --strict --baseline .design-baseline.json ./...
bash scripts/design-check.sh
go tool mygo vet .
go run ./internal/devtools/ui-snapshot .cache/ui-conformance
```

- **单元**：折叠表驱动（surfaceOp replace、7 类归类、窗口裁剪/floor、坏事件返回原 state）；估算口径 fixture。
- **golden**：`renderSystemPrompt(sections)` 与旧渲染逐字节一致（矩阵）。
- **集成**：写入事件 → `contextbook.SessionContext` → 与 provider `ContextTokens` 锚点一致；重启后从事件恢复。
- **帧测试**：tab 切换、`/context`、空态/加载/失败重试、键盘 Esc/Enter、窄窗布局。
- **快照**：亮/暗/窄窗/长会话/空态；人工看 PNG。
- **性能**：折叠 benchmark（5k 事件 < 30ms）、会话打开 < 100ms（2k 事件）、洞察聚合后台 < 200ms、内存每会话 < 1 MiB（LRU 8 槽）。
- **回归**：`internal/ui/uimodtest`、`internal/regression`、`scripts/verify.sh --quick`。

### 7.3 性能预算（目标，非承诺）

| 指标 | 目标 |
|---|---|
| 单会话冷折（2k 事件） | < 30ms（后台，不占帧） |
| Context tab 首次打开 | < 100ms（含折叠；超出先显示骨架） |
| 趋势图绘制 | 只画窗口内柱（>240 柱开窗）；单帧新增 < 2ms |
| 聚合查询（1k 会话） | < 200ms（后台 + 缓存；不阻塞写入） |
| 常驻内存 | Context 缓存 ≤ 8 会话×1MiB；不随会话数增长 |

---

## 8. 关键决策（含推荐）

| # | 决策 | 推荐 | 理由 |
|---|---|---|---|
| D1 | 先改 `zeokeel-rig` 还是先在 ZeoKeel 产品侧采集？ | **先产品侧**；P2 再评估 rig 变更 | 产品侧能拿到 run 的 tools/prompt/事件与时间戳；rig 只需未来补「压缩事件/请求头」；避免跨仓库升级成本 |
| D2 | 折叠器放哪？ | 新模块 `internal/modules/contextbook`（纯函数 + 服务） | 与 UI 解耦、可复用、可 benchmark；不把 500 行逻辑塞进已经很大的 agent 模块 |
| D3 | 数据传递走 HTTP/路由吗？ | **不走**，进程内 contracts 服务 | 单进程桌面；DSH 的 POST 信封是插件沙箱的产物 |
| D4 | `revision/unchanged` 还要吗？ | 要（进程内版本号） | 避免每帧重建大对象/重复序列化；UI 只在 revision 变化时重建 |
| D5 | 图表实现 | `ui/chart`（scales/stack/pie）+ `ui.Painter`（path/gradient）；不引第三方图表库 | mygo 官方件 + DESIGN token；0 canvas/SVG 依赖 |
| D6 | token 估算口径 | 复用 `rig.EstimateTextTokens`（CJK 1 字符/token，ASCII 4 字符/token）+ 工具 schema JSON + 图片 1024 | 与 rig 现有估算同源；DSH 的 4 字符/token 对 CJK 低估 |
| D7 | 估算 vs 真实 | 总量锚 provider `ContextTokens`；分项标「估计」；无 provider 数据时整块显示「—」 | 不伪造；与状态行一致 |
| D8 | 存储 | P0 不建表（读现有 events）；P1 加索引 + `usage_rollup` | 事件表是通用结构，零迁移；单连接下避免每帧扫表 |
| D9 | 成本价格来源 | 先手工配置（模型级单价 + 峰谷）；models.dev 拉取作为后续可选 | 离线优先、可审计；不把网络依赖塞进 P0 |
| D10 | 洞察页入口 | **P1 再定**；先做会话内 tab（无 IA 变更），洞察页需设计 owner 评审 | 一级侧栏是产品导航决策，不应在数据未落地时先改壳层 |

---

## 9. 风险与开放问题

| 风险 | 等级 | 说明 | 缓解 |
|---|---|---|---|
| 自动压缩不可观测 | **高** | rig 在 `prepareContext` 内压缩且不发事件（`rig/agent/agent.go:489-506`） | P2 启发式标注「推断」；或单独立项给 rig 加 `EventCompact` |
| 估算与真实差异 | 高 | 分项 token 只能估算；CJK/JSON schema/图片误差大 | 总量用 provider 锚点；UI 标注「估计」；固定 fixture 回归 |
| 重启后用量丢失 | 中高 | `MessageUsage` 只在内存（`usage.go:3-12`），restore 不回放 | Context 页从事件回放；顺带修状态行（可选） |
| SQLite 单连接 | 中高 | `SetMaxOpenConns(1)`（`store.go:177-179`），聚合会阻塞写入 | 折叠/聚合只走后台；P1 投影表；禁止 build 内 I/O |
| 两个数字打架 | 中 | 状态行（`ContextTokens`）与新账本（估算组成）口径不同 | 共用总量锚点；分项明确标注估计 |
| DESIGN 约束 | 中 | 图表多序列易写 hex/自绘；强调色预算、字号、选择态、DS-04 浏览器行不得复制消息解剖 | 先用 `ui/chart`/`Painter` 与官方集合件；designlint 不刷新 baseline；新增场景先评审 |
| 洞察页 IA 变更 | 中 | 新一级侧栏入口影响导航；卡片墙可能违反「不做装饰性面板」 | 先会话内 tab；洞察页单独立项 + 设计评审 |
| 范围膨胀 | 中 | 全量对标 10+ 面板 > 35 人天 | P0/P1/P2 收口，每档独立验收 |
| 工作区有未提交改动 | 低 | 实施前需确认 `git status` 中现有修改的归属 | 开工前先对齐，按 write scope 拆分 |

**开放问题（需要事实/决策）**：
1. 自动压缩是否值得改 rig（发事件）？需要产品判断「压缩可观测」的价值。
2. 成本是否要覆盖非 DeepSeek 模型？价格数据从哪来（手工/内置/网络）？
3. 洞察页是否要「一级入口」，还是先放设置/右栏？
4. 是否要持久化「每会话上下文账本」到磁盘（DSH 有 projection checkpoint）？P0 先内存 + 事件重放，若冷启动慢再考虑。
5. 语言范围：zh/en 起步是否可接受？

---

## 10. 需要你拍板的 5 件事

1. **范围**：先做 P0（约 2 周）还是 P0+P1（约 4–5 周）？
2. **是否允许改 `zeokeel-rig`**：默认「不改，产品侧采集」；若要「自动压缩/请求头精确可观测」，需另立 rig 变更。
3. **洞察页入口**：是否接受「P1 新增一级侧栏入口」？还是先只放会话内？
4. **成本**：P1 是否包含成本？价格来源选「手工配置」还是「内置表 + 可选联网更新」？
5. **语言**：zh/en 起步是否够？

> 你确认后，我建议从 **M0 数据基座**开工：先落契约 + section 化提示词（golden 保证零行为变化）+ 4 类事件 + 折叠器与回放测试，再进 M1 的 UI。

---

## 附录 A：调研文档索引

| 文档 | 内容 | 行数 |
|---|---|---|
| [01-dsh-context-host.md](01-dsh-context-host.md) | 宿主端：数据源/折叠/估算/路由/缓存/回填/成本/兼容性/Go 启示 | 549 |
| [02-dsh-context-client.md](02-dsh-context-client.md) | 客户端：5+2 入口/数据层/组件树/图表/性能/26 项 mygo 可替代性评级 | 445 |
| [03-zeokeel-baseline.md](03-zeokeel-baseline.md) | ZeoKeel 现状：11 项功能×缺口矩阵/数据管道/UI 落位/工作量/风险 | 278 |

## 附录 B：关键证据速查

| 结论 | 证据 |
|---|---|
| 状态行已有上下文/缓存/速度 | `internal/ui/session/view.go:3713-3800`、`:3638-3692` |
| 每回合用量（内存 + 一条事件） | `internal/modules/agent/usage.go:3-12,35-65`；`internal/contracts/chat.go:270-285` |
| 逐调用 usage 被丢弃 | `internal/modules/agent/agent.go:2348-2350`；`rig/agent.go:31-45`（Event 有 Iteration/Usage，无时间） |
| `run_start` 仅 model | `internal/modules/agent/agent.go:2255` |
| 系统提示词字符串拼接、不持久化 | `internal/modules/agent/system_prompt.go:1-18,286-305,374-395` |
| 工具 schema 可得 | `rig/tool.go:7-16`（`Schema() map[string]any`）；`rig/agent.go:15-45`（Event.ToolResult） |
| 手动压缩无事件 | `internal/modules/agent/compact_now.go:31-131` |
| 自动压缩不可观测 | `rig/agent/agent.go:489-506` |
| spill 可识别 | `rig/context.go:83-101`（`IsSpilledToolResult`） |
| 事件表/单连接/索引 | `internal/modules/sessionstore/store.go:140-148,177-179,694-805` |
| 会话 tab | `internal/ui/session/view.go:33-35,855-887` |
| 右栏固定页模型 | `internal/ui/rightpanel/rightpanel.go:50-63` |
| screen/settings/命令注册 | `internal/contracts/contracts.go:20-58,95-106,211-240`；`internal/uiroot/runtime.go:63-83` |
| 新 screen 需补侧栏标签 | `internal/ui/sidebar/sidebar.go:416,1589-1604` |
| mygo 官方图表 | `mygo@v0.9.0/docs/ui/chart.md`；`internal/design`（`DESIGN.md:173`） |
| 模块边界/线程模型 | `docs/ARCHITECTURE.md:43-51,110-120`；`AGENTS.md` 常驻规则 |

---

## 附录 C：价值评估——「对我们有多大提升」（2026-11-14 补）

### C.1 外部需求信号

- GitHub [`bowenliang123/dsh-context`](https://github.com/bowenliang123/dsh-context)：**5,088 stars / 218 forks / 3 个 open issue**（2026-11-14 GitHub API）。
- npm [`dsh-context`](https://www.npmjs.com/package/dsh-context)：**上月下载 503,258 次**（2026-10-15 ~ 11-13，npm downloads API）。
- README 称获 DSH 团队成员推荐并加星。
- 解读：这是 DSH 生态里被验证的「必装级」插件，不是小众玩具；但下载量含 CI/镜像/自动更新，**不能直接换算成 ZeoKeel 的用户需求**，只能算强信号。

### C.2 内部分层判断

| 用户/角色 | 提升 | 依据 |
|---|---|---|
| **重度 Agent 用户**（长会话、大仓库、多工具、团队/子代理） | **大（0→1）** | 现状只有「一个总数」；建成后能回答「哪块占满、为什么涨/掉、哪一步开始退化」——这是诊断能力的从无到有 |
| 普通聊天用户 | 小 | 大概率不打开 Context tab；成本/趋势对其无感 |
| 产品/团队自身 | 中-大 | 数据基座可反哺 prompt/工具/Skill/压缩策略调优；也是「可解释性」卖点与 DSH 迁移用户的对齐点 |
| 模型回答质量/速度 | **无直接提升** | 这是只读诊断层，不改 agent 行为（除非后续用数据驱动压缩策略） |

### C.3 可感知的量化（估算，需上线实测校准）

| 维度 | 现状 | 建成后 | 置信度 |
|---|---|---|---|
| 回答「为什么上下文涨/掉」 | 翻 transcript / 日志 / 猜，约 10–30 分钟 | 趋势 + 组成 + 事件，约 1–2 分钟 | 中（假设 80% 场景可定位） |
| 分项 token 归属 | 只有总量 1 个数 | 7 类 + 每 step 趋势 | 高 |
| 成本可见性 | 无（只有 token） | 按天/模型/会话估算成本 | 高（价格表准的前提下） |
| 无效 token 消耗 | 不可见 → 不行动 | 假设减少 10–20% | 低-中（需 pilot 验证） |
| 上下文类问题反馈/工单 | 靠人工复现 | 面板直接截图定位 | 中 |

### C.4 不提升的部分（诚实边界）

- 不提升模型回答质量、速度、成功率；它让问题**可见**，不替模型解决问题。
- 轻用户可能永远不打开；如果 ZeoKeel 的目标用户主要是「聊天式使用」，这项功能的用户侧 ROI 会明显下降。
- 估算口径本身有误差（CJK / JSON schema / 图片），不能当账单；必须 UI 标注「估计」并以 provider token 为锚。
- 洞察页若不克制，反而增加视觉噪音与维护成本（DESIGN 强调色/卡片墙风险）。

### C.5 ROI 与建议

| 方案 | 投入 | 收益 | 建议 |
|---|---|---|---|
| **P0-lite**（M0 基座 + 组成条/趋势，不做 Browser/洞察） | ≈ 5–7 人天 | 拿到「为什么涨」约 60% 的价值 | 只想先验证需求时选它 |
| **P0 完整**（含 Browser + `/context`） | 9–12 人天 | 会话内闭环 | **推荐起点** |
| P0 + P1 | 再加 11–15 人天 | 跨会话洞察 + 成本 | 有重度用户数据后再上 |
| 全量 P2 | 再加 10–16 人天 | 诊断深度 | 按用户反馈决定 |

**上线后 4 周的验证指标**：Context tab 周打开率（目标 >20% 周活）、长会话 `/compact` 使用率变化、每会话平均 token 成本变化、上下文类反馈/工单下降、DSH 迁移用户对「有 Context 面板」的反馈。

**一句话**：对重度用户是「从不可见到可归因」的**大提升**，对产品是**中-大**（可解释性 + 数据基座 + 生态对齐），对模型质量**无直接提升**；以 P0（9–12 人天）为起点性价比最高，P1/P2 应等 P0 的使用数据再决定。

---

> **本报告只做调研与规划，未改任何产品代码。** 仓库中 `git status` 的既有修改属于此前工作，与本次调研无关；本次新增仅 `docs/research/dsh-context/` 下 4 个文档。
