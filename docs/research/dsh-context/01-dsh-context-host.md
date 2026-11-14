# dsh-context 宿主端原理调研（ZeoKeel 自研等价能力用）

> 调研对象：`dsh-context`（npm 包 / GitHub `bowenliang123/dsh-context`），**v0.84.1**（`package.json:3`），Apache-2.0。
> 源码口径：2026-11-14 从 GitHub `main` 逐文件抓取（tarball 含演示视频过大超时，改为 raw 逐文件，共 79 个 host/shared/docs 文件，落盘 `/data/mygo/.cache/research-ctx-host/repo`）。本机 DSH 为 `@deepseek-ai/dsh 0.2.1-rc.2`。
> 引用约定：`src/host/xxx.ts:12-34` 指插件仓库内文件行号；`dsh: packages/...` 指 DSH 包内路径（本机安装树 / README 有交叉验证的才展开）。
> 证据强度：**A**=读了源码且与本地 DSH 包文档一致；**B**=读了源码但 DSH 侧只由插件文档/README 佐证；**C**=未验证（文中显式标注「未验证」）。

---

## 0. 一句话结论

dsh-context 的宿主端**不是**「调 DSH 的 meter 拿数」，而是一套**自带事件折叠引擎（fold）的只读数据面**：它把自己注册成 DSH 的 session projection（`contextTimeline`/`contextActivity`/`contextHeaders`），用**同一份 durable session log** 折出「每 step 的上下文组成 + 时间线 + 日账本」，再用 **14 条 POST 路由 + 3 层读取（活会话投影 → 持久 projection 缓存 → 冷日志重折）+ 内存/磁盘缓存 + 后台回填/worker** 把这些数据切片喂给前端。DSH 官方 meter 只被当作**对账基准**（`scripts/diff-fold.ts` 差分校验），不是数据来源。

对 ZeoKeel 的关键判断：**数据模型与折叠算法可照搬（Go 重写成本可控），运行时管道（cordis DI / projection registry / worker_threads / zod schema / React client）不可照搬也不该照搬。**

---

## 1. 插件结构：host / client / shared 三半

### 1.1 装载方式

```yaml
# cordis.patch.yml（全文 4 行）
- insert:
    - id: dsh-context
      name: dsh-context
```

- 一个 `cordis.patch.yml` 插入一行宿主插件；真正的「双半」由 `package.json` 的 `dsh` 字段声明：`dsh.bundle.patch` 指向该 patch，`dsh.client.platform: "web"` + `dsh.client.inject` 声明浏览器半及其依赖顺序（`package.json:56-74`）。
- 宿主半入口 `main: lib/index.js`，浏览器半通过 `exports["./client"]` 单独出包（`package.json:20-28`）。**同一个包、两个运行时入口**，不是两个包。
- 版本闸门：`engines.dsh: ">=0.2.0-rc.2 || >=0.2.1-rc.2 || >=0.2.2-rc.1"`（`package.json:18`；`dsh.compatibility.dshReleases` 同义声明于 `:71-77`），是「三条支持线取最低 term」的并集语义，详见 `docs/compatibility.md:9`。

### 1.2 host 半的 apply 顺序（`src/host/index.ts:40-78`）

```text
apply(ctx, config)
├─ makeColdReadGate()                      # 冷读闸门（堆水位 + FIFO 串行）
├─ createCatalogService() + createCostSource()   # npm/models.dev 目录 + 价格书
├─ detectHarnessVersion(ctx)               # 版本探测
├─ watchVersionChannel / Balance / Recom / Skills / Fleet / Attachment   # 旁路小路由
├─ if 版本 < baseline: 注册 3 个 fallback 空投影后 return   (:52-57)
├─ createToolAttribution(ctx, version)     # 工具→来源包归属
├─ watchStepIdentity(ctx)                  # 给无 id 的消息事件补 dshctx- id
├─ createDetailGate(ctx); resolveBounds(config)            # 折叠上界
├─ new DetailShadow(...)                   # 活会话影子折叠（8 槽）
├─ watchDetailChannel / DeltaChannel / FactsChannel / CommandPageChannel   # 明细类路由
├─ watchHeadsChannel(...)                  # 批量 head
├─ register(contextTimeline, contextHeaders, contextActivity)  # 三个投影单元
├─ watchTimelineRowSweep(ctx)             # 旧 stateVersion 行瘦身迁移
├─ createOverviewCache(); watchActivityBackfill(...)       # 磁盘预聚合 + 回填
├─ watchSnapshotRoute / watchOverviewRoute
└─ watchPromptInjection(ctx, config)      # 系统提示词前缀/后缀
```

要点：
- **硬依赖只有一个服务**：`inject = ['sessionProjections']`（`src/host/index.ts:37`），其余全部走可选 `ctx.inject([...])` / `ctx.get()`，缺服务即「无此能力」而不是抛错。这是它能跨 3 条 DSH 版本线的原因之一。
- 宿主半没有 UI，只有**路由 + 投影 + 缓存 + 后台任务**；所有展示计算都在宿主完成（`docs/host-client-principles.md:26-35` 明确「数据来源后端化、数据计算后端化、预先聚合」）。
- shared 半（`src/shared/*.ts`）是**纯函数 + zod 契约 + 类型**，host 与 client 共享：`estimate.ts`、`pricing.ts`、`imageTokens.ts`、`fileTokens.ts`、`categories.ts`、`types.ts`、`version.ts` 等。**没有 I/O，可直接移植。**

### 1.3 数据流总图

```text
┌──────────────────────── DSH host 进程（Node） ─────────────────────────┐
│                                                                        │
│  durable session log (v4 JSONL, 每逻辑会话一份，子会话单独文件)         │
│        │ ctx.on('session/event')             │ sessionQuery.observeSession
│        │ (活会话逐条推送)                     │ (冷会话整段读, projectionMode:none)
│        ▼                                     ▼
│  ┌──────────────┐   ┌───────────────────┐   ┌──────────────────────┐
│  │ DetailShadow │   │ sessionProjections │   │  coldRead gate       │
│  │ 8 槽·512 缓冲 │   │ contextTimeline    │   │ 堆 < 2/3 才放行       │
│  │ 20s 空闲清空  │   │ contextActivity    │   │ FIFO 串行            │
│  └──────┬───────┘   │ contextHeaders     │   └──────────┬───────────┘
│         │           └─────────┬─────────┘              │
│         │  foldLog(events)    │ snapshot(session,keys)  │ foldLog / restore+hydrate
│         ▼                     ▼                         ▼
│    TimelineState        wire view (ContextTimeline)   TimelineState
│         │                     │                         │
│         ├──────► 内存缓存：makeFoldCache(detail 4 条 / delta / facts)  
│         │                     │
│         │           ┌─────────┴──────────┐
│         │           │ sessionProjectionCache（DSH 持久 checkpoint）  │
│         │           │ cachedSnapshot(header, keys) 零 I/O           │
│         │           └─────────┬──────────┘
│         ▼                     ▼
│  /api/dsh-context/{snapshot,heads,overview,backfill,detail,delta,facts,
│                    commands,fleet,skills,attachment,balance,recom,version}
│         │   (全部 POST，connection.fetch.register，cache-control:no-store)
│         ▼
│  overview-cache.v1.json（磁盘预聚合：$DSH_HOME/storages/dsh-context/）
│  + fold worker（>30000 事件的会话，worker_threads）
└──────────────────────────────┬─────────────────────────────────────────┘
                               │ HTTP POST JSON（浏览器相对路径，不 root-absolute）
                               ▼
              client 半（React：Context 页/侧栏/Fleet/insights/`/context`）
              只做：解码、格式化、货币换算(1 CNY=0.15 USD)、渲染；无价格表
```

---

## 2. 数据源：它到底读什么

### 2.1 DSH 投影（首选数据源，活会话零重算）

DSH 的 `dsh-session-projection` 契约：**域插件注册 projection unit，注册表对每条 committed event 同步 `apply(state,event)`，wire view 经 schema 校验、`Object.is` 门控后推给载体；`snapshot(session)` 返回 `{asOfSeq, values}`**（本地 `@deepseek-ai/dsh-session-projection/README.md`：Define a projection unit / Drive and checkpoint flow）。dsh-context 完全按此契约注册三个单元（`src/host/index.ts:69-71`）：

| projection key | 定义文件 | 语义 | 对 ZeoKeel 的价值 |
|---|---|---|---|
| `contextTimeline` | `src/host/timeline.ts:380-397` | 每会话的「当前上下文组成 + 逐 step 请求记录 + 事件 + 文件操作 + 命令 + 计时」 | ★★★ 核心 |
| `contextActivity` | `src/host/activity.ts` | 按天账本：token/成本/turn/tool/skill/命令的日聚合 | ★★★ insights 页核心 |
| `contextHeaders` | `src/host/headers.ts` | 每次 `request/header` 的工具清单与 token 归因（工具→来源包） | ★★ |

- `contextTimeline` 的**活投影状态是「瘦身版」**：`apply` 用 `headBounds = {...bounds, headOnly:true}`（`timeline.ts:386,394`），而 `headOnly` 下 `trimHeadState` 只保留最近 2 条请求、清空 events/agents/fileOps/spans、commands 只留 running 的 16 条（`foldState.ts:368-376`）。细节数据由 detail/facts/delta 路由**另起冷折或影子折**补，避免每条事件都维护全量状态。
- `wire.view` 有 `summary` 标记（`timeline.ts:392` `summary:false`）：DSH `0.2.2` 起列表行会据此**排除** `contextPressure`/`contextBreakdown` 等 kernel 键（`docs/compatibility.md:57`），所以 dsh-context 声明 `summary:false` 以在列表行也带上自己的值；旧注册表忽略该标记，行为兼容。
- **它同时消费 DSH kernel 的投影做对账**：facts 路由的 `KERNEL_KEYS = ['sessionStats','tokenUsage','contextBreakdown']`（`src/host/facts.ts:27`），把官方统计与自己的折叠结果并排提供；`contextBreakdown` 字段就是 DSH token-meter 的三段 `systemTokens/toolsTokens/messageTokens`（`src/shared/types.ts:377-381`；口径见本地 `dsh-token-meter/README.md`：只统计存活 system prompt + 其余可见节点 + 最新 request/header 工具，固定启发式，不是计费值）。客户端用 `officialParts()` 做**官方口径优先、折叠口径兜底**的组成环（`src/shared/categories.ts:32-60`：`breakdown?.systemTokens ?? current.system`，message 总量按折叠比例分配到 5 类）。

### 2.2 session log 事件（重算/冷读的唯一事实源）

折叠器只认 `type/seq/time/data/surfaceOp` 五元组（`src/host/foldState.ts:141-147`），`applyTimeline` 的 switch 覆盖的事件族（`src/host/fold.ts:340-973`）：

| 事件 | 折叠动作 | 证据 |
|---|---|---|
| `request/header` | 工具 schema 总 token（`JSON.stringify(tools)/4+4`）、model/provider/reasoningEffort/maxTokens；model 变化推 `model` 事件 | `fold.ts:341-368` |
| `system/message` | 推入 `systems`（上限 8）、`systemTokens`=最新非空；带 `surfaceOp` 时移除区间 | `fold.ts:369-385`、`:55-68` |
| `request/context` | `contextWindow`/model/provider（用于占用率分母） | `fold.ts:386-399` |
| `tool/call` | 记 callId→{name,start,argsRaw}；命令类工具额外生成命令记录 | `fold.ts:400-432` |
| `job/registered` / `job/settled` | 后台任务起止/状态，补命令记录的 start/end/exit | `fold.ts:433-471` |
| `tool/ptc-dispatch-start` / `tool/ptc-dispatch` | PTC 子调用成对折叠（无 tool/call+result），transport 父调用不重复计时 | `fold.ts:472-575` |
| `assistant/attempt` | 首个流式 token 时间（TTFT 终点锚） | `fold.ts:576-584` |
| `step/start` / `step/end` | 步边界、wall/active/idle 计时、请求 activeMs 回填、时间段切片 | `fold.ts:585-656` |
| `approval/asked` / `approval/decided` | 审批等待窗口（计入 idle）；`ask_user_question` 结果同 | `fold.ts:657-676,740-742` |
| `user/message` / `developer/message` | 表面节点 + 注入事件记录（`developer` 一律算注入，不算人类输入） | `fold.ts:683-726` |
| `tool/result` | 表面节点；错误取 `message.isError`；`skill` 结果改归 skill 类；命令结果回填退出码 | `fold.ts:727-837` |
| `assistant/message` | **每次请求快照**：`total = system+tools+user+inject+skill+assistant+tool`，另记 prompt(usage 输入总量)/cacheRead/output，写入 `requests`，累计成本 | `fold.ts:838-934` |
| `session/end-seed`（inherited） | fork 继承种子：重置 cost 与生成计数，避免父/子重复计费 | `fold.ts:935-943` |
| `compaction/summary` / `compaction/prune` | 事件入账，并暂存 `shadowedSeqs` + `shadowedTokenCount`，等**下一个表面事件**应用替换 | `fold.ts:952-971` |
| `plan/mode`、`subagent/catalog`、`team/member` | 模式事件；agent 别名表（父子树/标签） | `fold.ts:677-682,944-951` |

**实时 vs 重算**的分工：
- **活会话**：`session/event` 逐条 advance（`detailShadow.ts:81-84,99-114`），投影注册表本身也在每条 committed event 上紧驱动（DSH 契约）。**零重复读日志**。
- **冷会话**：`sessionQuery.observeSession(id,{projectionMode:'none'})` 读整段日志 → `foldLog(events,bounds)`（`detailShadow.ts:41-60`）或切片恢复（backfill）。`projectionMode:'none'` 表示**不要 DSH 再算投影**，只要 raw events，避免双重折叠。
- 视图中的 `parts`/`officialParts` 混口时，一律标注「估计」：`current.total` 是启发式总量，`prompt` 是 provider 报的输入量（`foldWire.ts:154` 的 `last.prompt`）。

---

## 3. 折叠算法（核心）

### 3.1 状态模型

`TimelineState`（`foldState.ts:155-212`，约 50 个字段，按职责分 5 组）：

| 组 | 字段 | 说明 |
|---|---|---|
| 表面 | `surface: SurfaceNode[]`（seq/time/cat/tokens + reasoning/answerTokens/callTokens/imgs/tool/skill/text/form/agent/err） | 当前模型可见面，**有序**，替换即 splice |
| 分类汇总 | `sums: {user,inject,skill,assistant,tool}`、`systemTokens`、`systems[]`、`toolsTokens` | 增量维护，增删同步加减 |
| 请求序列 | `requests: SlimRequestRecord[]`（time/seq/total/turn/step/prompt/activeMs/cacheRead/output/system/tools/user/...） | 每个 `assistant/message` 一条 |
| 事件账 | `events: ContextEventRecord[]`（inject/model/mode/compaction/prune）、`archived[]`、`fileOps[]`、`commands[]`、`spans[]`、`timing`、`cost` | 有上界与 floor |
| 边界/待决 | `stepStart`、`stepWaits`、`stepApprovals`、`stepSpans`、`stepBeats`、`callNames`、`pendingCode*`、`pendingShadowedSeqs`、`agents`、`detailRev` | 跨事件配对 |

**不可变折叠**：`ensure(keys)` 只拷贝将被改动的键（`fold.ts:315-329`），未命中事件的返回**同一引用**——这正好满足 DSH registry 的 `Object.is` 门控（引用不变 = 零下游工作）。整个函数 `try/catch` 包裹，任何异常都**返回原状态**（`fold.ts:975-984`），保证「折叠是全函数、坏事件不污染状态」。

### 3.2 7 类组成与归类规则

分类只有 5 个 message 类 + system + tools = **7 类**（`CATS`，`src/shared/categories.ts:12-20`；zod 枚举同序 `timeline.ts:150-162`）：

| # | key | 归入规则 | 证据 |
|---|---|---|---|
| 1 | `system` | 只取**最新存活**非空 `system/message` 的估算；`systems` 保留最近 8 条但只有最后非空计入 | `fold.ts:55-68,369-385` |
| 2 | `tools` | 最新 `request/header` 的 `header.tools` 整体 JSON 估算 | `fold.ts:341-348`；`pricing.ts:4-8` |
| 3 | `user` | 默认兜底：非注入的 `user/message` | `fold.ts:41-49,683-726` |
| 4 | `inject` | `developer/message`；或 `source.kind` 非空且 ≠ `'user'`；或 `source.form` 存在（`isInjection`） | `fold.ts:44,47`；`pricing.ts:158-162` |
| 5 | `skill` | `source.kind ∈ {'skill-invocation','skill-catalog'}`；或 `tool/result` 的工具名/源识别出 skill 时**把已计入 tool 的 token 迁到 skill** | `fold.ts:46,815-832` |
| 6 | `assistant` | `assistant/message` | `fold.ts:42` |
| 7 | `tool` | `tool/result` | `fold.ts:43` |

对账要点（与 DSH token-meter 的差异，`docs/compatibility.md:63-64`）：
- DSH `contextBreakdown.messageTokens` 把**被替换掉的 system prompt 也算进 message**；dsh-context 只算最新存活 system（`systemTokens`），历史 system 不计入 composition 总量。
- DSH 按「表面位置归位」；dsh-context 按 `source.kind` 判注入，因此 `developer/message` 归 inject 而非 user（明确写在 `compatibility.md:26`）。
- 表面替换（compaction）后，dsh-context 的 `archived` 保留被替换节点并带 `goneSeq`（`fold.ts:127-130`），archive 窗口按「最老请求 seq」裁剪（`foldState.ts:214-240`）。

### 3.3 表面替换（compaction 的真实形状）

DSH 的压缩结果不是删除事件，而是「一个 `user/message` 带 `surfaceOp: {op:'replace',startSeq,endSeq}`」替换一段历史（本地 `dsh-compaction/README.md`：The surface contract）。dsh-context 的处理：

```text
fold(event):
  if event.surfaceOp is replace(start,end):
      if pendingShadowedSeqs (由 compaction/summary|prune 暂存):
          removed = removeSurfaceSeqs(pendingShadowedSeqs)   # 按 seq 精确删除
          // 把 shadowedTokenCount 修正为「实际移除 token - 新节点 token」，
          // 并写进那场 compaction 事件的 tokens/grossTokens
      else:
          si = first index with seq == start
          ei = first index with seq == end
          removed = surface.splice(si, ei-si+1, newNode)
      for r in removed: sums[r.cat] -= r.tokens; 反向扣减 assistant 细分
      sums[newCat] += newNode.tokens
```

证据：`fold.ts:242-288`（区间替换）、`:952-971`（shadowedSeqs 暂存）、`:147-165`（`removeSurfaceSeqs` 同步扣 sums 并归档）、`:132-136`（assistant 细分反向扣减）。

### 3.4 token 估算口径（`src/shared/estimate.ts` + `src/host/pricing.ts`）

```text
CHARS_PER_TOKEN = 4; ROLE_OVERHEAD = 4; BLOCK_OVERHEAD = 4     # estimate.ts:1-3

systemTokens(text)        = ceil(len(text)/4) + 4                # estimate.ts:5-8
systemContent(blocks)     = ceil(Σ len(text blocks)/4) + 4
                            （非 text block 回退 JSON.stringify 长度）   # estimate.ts:14-32
textTokens(text)          = ceil(len/4) + 4                      # estimate.ts:10-12
toolsTokens(tools[])      = ceil(len(JSON.stringify(tools))/4) + 4   # pricing.ts:4-8
messageTokens(msg)        = Σ blockPrice + 4                     # pricing.ts:82-88
  text/reasoning          = ceil(len/4) + 4
  tool-call               = ceil(len(name)/4) + ceil(len(arguments)/4) + 4
  image                   = estimateImageTokens(w,h) 或 JSON 结构性价格 + 4
  其它/非对象 block        = 4 + ceil(len(JSON.stringify(block))/4)
assistant 细分（仅展示用）  = reasoning/answer(text)/call(tool-call) 三类之和   # pricing.ts:70-80
```

- **图片**不是 4 字符启发式，而是照搬 DeepSeek 官方 v41 图片换算：PATCH=14、下采样 3、**上限 1024 token**、**最小 544×544 像素**、网格 `h*(w+1)+2`（`src/shared/imageTokens.ts:1-5,18-20`），带固定点迭代求稳定网格（`:95-110`）。无有限宽高则退回结构价格（`pricing.ts:54-61`）。
- **文件附件**按「模型可见句柄文本」计价：`File "<name>" (<bytes> bytes, sha256:<8位>) was uploaded, but ...`（`src/shared/fileTokens.ts:6-13`），与 DSH `fileHandleText` 同思路，但插件端**拿不到可解析路径**，因此是下界（`compatibility.md:47`）。
- **与官方 meter 的一致性如何保证**：不 import，而是**镜像 + 差分**。`compatibility.md:41-50` 给出逐站点对照表（estimate/pricing/fileTokens/occupancy/reserve 各自镜像 dsh 哪个文件），`scripts/diff-fold.ts`（121 行）用 bun 把插件折叠与 DSH token-meter 自己的 `contextBreakdown` 在真实 log 上对拍，退出码 0 即一致；表面顺序/替换位置另有 host 测试双向折叠断言（`compatibility.md:147`）。
- **必须视为估计**：`docs/compatibility.md:55-64` 列了 8 条偏差限制——thinking 三分桶、无 span 的 block、slim 列表行、工具并发 busy 并集、计数口径（steps=折叠到的 assistant message 数，而 DSH `sessionStats` 数 `step/end`）、idle 切分阈值 2 分钟、日账本 400 天保留、继承种子成本重置。**这些偏差点本身就是「自研时必须做决策」的清单。**

### 3.5 折叠上界与窗口（防止长会话爆内存）

`DEFAULT_BOUNDS`（`config.ts:4-13`，可由插件配置覆盖）：

| 字段 | 默认 | 作用 |
|---|---|---|
| `maxRequestSteps` | 1500 | `requests` 上限（超出丢最老） |
| `maxKeptTurns` | 300 | 按 turn 裁剪 requests 的上限 |
| `maxEvents` | 400 | 事件账上限 |
| `maxNodes` | 2000 | 表面节点窗口（`surfaceWindowOf`） |
| `maxArchiveNodes` | 400 | 被替换节点归档窗口 |
| `maxFileOps` / `maxCommands` | 400 / 400 | 文件操作/命令记录上限（带 floor 供增量拉取） |
| `maxCommandChars` | 4096 | 单条命令行截断 |

- 窗口裁剪**不丢 floor**：裁剪时写 `fileOpsFloor`/`commandsFloor`/`surfaceFloor`/`archiveFloor`（`foldState.ts:294-349`），前端据此知道「更老的数据要另拉」而不是当作没有。
- **注入类节点被钉住**：`surfaceWindowOf` 在溢出时把 `inject`/`skill` 节点全部保留（`foldState.ts:242-264`），因为它们影响后续所有 step 的上下文；只裁普通消息。
- 其它硬上限：`SYSTEM_NODES_MAX=8`、`STEP_BEATS_MAX=1024`、`PENDING_CODE_{OPS,CMDS,STARTS}_MAX=200`、`COMMAND_CALLS_MAX=1024`、`TIMING_SPANS_MAX=2000`、`SKILL_NAMES_MAX=500`、`TALLY_NAMES_MAX=128`（单名 120 字符）（`fold.ts:55,70-74,297`；`foldState.ts:351,378-384`）。

---

## 4. 增量、缓存与路由

### 4.1 三层读取模型（一切读取的路由骨架）

```text
read(id, keys):
  1) live:   sessions.get(id) 存在 → sessionProjections.snapshot(session, keys)   # 零 I/O、同步
  2) warm:   sessionProjectionCache.cachedSnapshot(header, keys)                   # 持久 checkpoint，零 I/O
  3) cold:   coldReads.admit(() => sessionQuery.observeSession(id, {projectionMode:'none'}))
             → foldLog(events, bounds)                                             # 受堆水位闸门 + FIFO
```

证据：`headsRoute.ts:61-83,137-160`（三级依次回退）；`snapshotRoute.ts:126-129`（只有 live/cached 两级，冷读交给 backfill）；`detail.ts:17` 的 `ColdPayload` 缓存（4 条）与 `detailShadow`（8 槽）。
**冷读闸门** `makeColdReadGate`：FIFO 串行，V8 堆用量必须 < 上限的 **2/3** 才放行，否则返回 `undefined`（调用方走「暂无数据」路径而非报错）（`coldRead.ts:12-38`）。这是全局背压，防止一次打开大量会话把宿主打爆。

### 4.2 路由清单（14 条，全部 POST + JSON）

`connection.fetch.register({path, methods:['POST'], requestBody:'buffered', fetch})`，统一 `cache-control: no-store`，错误形状 `{ok:false,error:{code,message}}`（`connection.ts:22-36`；`postRoute.ts:27-48`）。

| 路由 | 请求要点 | 响应要点 | 缓存 |
|---|---|---|---|
| `/api/dsh-context/snapshot` | `{ids?≤256, revision?}` | `{ok,value:{id:{contextTimeline,contextActivity}},revision}`；`revision` 相同且该 key 已服务过 → `{value:{},unchanged:true}` | 服务端记忆最后 revision+key（`snapshotRoute.ts:82-148`） |
| `/api/dsh-context/heads` | `{ids≤64}` | 每个 id 的 head（三级读取回退），带 `asOfSeq` | 无独立缓存，依赖 projection cache |
| `/api/dsh-context/overview` | `{part: summary\|days\|page\|tally, revision?, scope?, sort?, page?, tally?, pick?}` | 预聚合 KPI/热力图/12 卡/榜单；同 revision → `unchanged` | `overviewCache`（磁盘）+ 内存 rows + served key；materialize 锁 |
| `/api/dsh-context/backfill` | 无体 | 触发一轮回填，返回 `{coverage:{complete,remaining,failed,running,total,covered,unsupported}}` | 幂等，按 corpus 签名跳过 |
| `/api/dsh-context/detail` | `{id, seq?/parts?}` | `ContextTimelineDetail = {rev,head,requests,events,nodes,archive,fileOps,commands,spans,...}` | `foldCache`（4 条）+ `DetailShadow` |
| `/api/dsh-context/delta` | `{seq,baseSeq}` | 两个 cut 之间的上下文增量（`shared/delta.ts` + `shared/increment.ts` + `shared/assemble.ts`） | 独立 `foldCache`，gate.live=false |
| `/api/dsh-context/facts` | `{ids, parts?}` | 内核投影对账（`sessionStats`/`tokenUsage`/`contextBreakdown`）+ 自己的 head 部分 | `foldCache` + kernel 投影 |
| `/api/dsh-context/commands` | 分页 | 命令执行记录 | 页面缓存 |
| `/api/dsh-context/fleet` | `{id, mode?}` / `{batch:{ids≤16,mode,rootId?}}` / `{summary}` | agent 家族：root 汇总、成员卡、批量 | 按 root `header.createdAt` 生命周期的 bounded counts 缓存 |
| `/api/dsh-context/skills` | `{cwd,agentPreset?}` | 技能目录（≤500），冷读也过闸门 | 无 |
| `/api/dsh-context/attachment` | 附件 id | 附件元数据/内容 | 冷读闸门 |
| `/api/dsh-context/balance` | — | DeepSeek 余额（账号优先，API key 兜底） | 无 |
| `/api/dsh-context/recom` | — | 推荐/文档卡 | 无 |
| `/api/dsh-context/version` | — | npm 最新版 + 是否可升级（`{ready,failed,checkedAt,current,updateAvailable,latest}`） | catalog 1h TTL |

### 4.3 缓存族与容量模型

| 缓存 | 键 | 上限/淘汰 | 证据 |
|---|---|---|---|
| `foldCache`（detail/delta/facts 各自实例） | 会话 id / revision | `maxEntries`，可选 `maxBytes`；**空闲 20s 全清**；`get` 时 LRU 重排；`ctx.effect` 卸载即清 | `foldCache.ts:18-68` |
| `DetailShadow` | 会话 id → 活折状态 + 未折缓冲 | **8 槽 LRU**；缓冲 **512** 条，溢出即整槽丢弃（下回重新冷折）；20s 空闲清空；`session/disposed` 即删 | `detailShadow.ts:9-13,62-98,116-184` |
| `overviewCache`（磁盘） | 会话 id → `{HostSession, identity:{path,sizeBytes,mtimeMs}}` | 单文件 `overview-cache.v1.json`；原子写（tmp+rename）；**2s 防抖**；stat 并发 16 / capture 4；load 时逐条 `fs.stat` 验证 identity | `overviewCache.ts:8-16,198-353` |
| overview 内存态 | revision + rows + served map | rows 空闲 20s 释放；`served` 只记「最后 revision + 请求 key」用于 `unchanged` | `overview.ts:228,444-455`；`compat:119` |
| 投影持久 checkpoint（DSH 提供） | 会话 id → `{key:{ver,seq,val}}` | DSH 的 `writeEveryEvents`/`writeIntervalMs` 节流；三类强制写（创建/turn end/dispose） | 本地 `dsh-session-projection-cache/README.md` |
| 价格书 / 版本目录 | 进程级 | 1h TTL；single-flight；失败 30s backoff | `catalog.ts:15`；`compat:116` |

**revision 协议**（省流量的关键）：概览 revision = corpus 签名 + workspace 签名 + 回填覆盖 + 价格书版本 + fold epoch（`overview.ts:444`）；请求带同 revision → 只回 `{revision, unchanged:true}`（`:450-452`），**不做行工作、不序列化、不压缩**。snapshot 路由同理（`snapshotRoute.ts:109-118`）。

### 4.4 冷启动与历史回填（backfill）

`watchActivityBackfill`（`backfill.ts:265-589`）是一个**限速、可中断、自恢复**的后台任务：

```text
pass():
  records = sessionQuery.listSessions()                       # 复用 5s 的 corpus 列表
  if coverage.complete and signature 未变: return             # 签名=id+createdAt+cwd+live 排序哈希
  for each record:
     live?            → covered++（活会话不需要回填）
     已在 ownCache?   → covered++
     servesRows()?    → covered++   # projection cache 的 asOfSeq 与 stat().eventCount 对齐
     else 冷折：
        coldReads.admit:
          events = persistence.open(read).read(0)             # 整段日志
          if events > 30000 && stat 可用 && ownCache 存在:
               worker.pool.fold(config, events)                # worker_threads，2000 事件/块
               → 只写 ownCache（DSH 投影缓存保持冷）
          elif events > 2000 && registry.restore/hydrate/prepare/write 都在:
               切片：restore(rows, events[0..end]) 逐片推进，
               size ← nextSliceSize(size, cost, 150ms 预算, 下限 128)
               → 末尾 prepare+hydrate+cache.write 一次回写
          else:
               cache.coldSnapshot(header, inheritedEventCount, events)   # 单发
  每折一个会话后 rest(costMs)：sleep(max(25ms,costMs))；累计 8s 工作后暂停 2s
  堆水位高 → admit 返回 undefined → 暂停整轮，剩余会话「下次活跃时再折」
```

关键常量：`MIN_REST_MS=25`、`SLICE_EVENTS=2000`、`MIN_SLICE_EVENTS=128`、`BLOCK_BUDGET_MS=150`、`WORKER_FOLD_MIN_EVENTS=30000`、`ROUND_WORK_BUDGET_MS=8000`、`ROUND_PAUSE_MS=2000`（`backfill.ts:59-70`）。覆盖率字段 7 个（`:288-296`）。回填失败按 `SessionFormatUnsupported` 与其它错误分开计数（`:249-255,519-527`），**旧格式会话跳过且不修改源文件**（`:553-555`）。

### 4.5 迁移/清扫

- `TIMELINE_STATE_VERSION = 42`（`timeline.ts:378`）：投影状态形状或语义一改就 bump，DSH 缓存按 `ver` 拒绝旧行。
- `watchTimelineRowSweep`（`sweep.ts:36-77`）在启动时把**旧版 v36 的胖状态行**用 `deriveHeadState` 压成 v37 的 head-only 形状（保留 2 条请求、清空 events/archived/fileOps/spans），直接操作 `storageDomain('session_projcache').table('sessions')`。这是**唯一直接改 DSH 缓存存储的地方**，也最贴近 DSH 内部实现（风险点，见 §7）。
- `deriveHeadState` 本身是「从任意 JSON 恢复瘦状态」的防御式解码器（`foldState.ts:61-106`）：字段缺失/类型错 → 返回 `undefined`（拒绝该行）而不是部分恢复。

---

## 5. 衍生统计

### 5.1 cost / pricing

**两段式**：折叠期只按 provider/model/**峰谷时段**累计 4 个计数桶；服务期再用价格书算钱。

```text
折叠期（foldMetering.ts:37-57）:
  period = isDeepSeekProvider(provider) && !isBeijingPeak(time) ? 'off' : 'peak'
  cost[provider][model][period] += {uncached, cacheRead, cacheWrite, output}
  isBeijingPeak = 北京时间 周一~周五 09:00-12:00 / 14:00-18:00   # foldMetering.ts:27-35

服务期（cost.ts:38-88 + shared/pricing.ts）:
  usd = Σ 4 桶 × 单价；DeepSeek 的 peak 期单价 ×2（PEAK_FACTOR=2, pricing.ts:23,504-509）
  costRead = {usd, ready, faces?, misses?}    # faces/misses 是逐 (provider,model) 命中证据，仅工具提示
```

- **价格书**：`https://models.dev/api.json`（`catalog.ts:15`），宿主拉取、1h TTL、single-flight、失败 30s backoff；**浏览器永远拿不到价格表**，只拿 `usd` 和逐模型 rate face（`compat:116-117`）。
- **解析顺序**（`docs/model-pricing-mapping.md:3-22`）：已知 provider 分支 → 精确/后缀/方言/归一化匹配 → 模型 org 段是 provider 分支 → canonical labs（唯一/多 lab + 家族提示）→ 单一承运商一致价；编码/订阅计划分支（models.dev 标 0 价）被排除。置信度 `high|medium|low`。
- **货币**：宿主只出 USD，客户端 `1 CNY = 0.15 USD` 换算（`shared/pricing.ts:21`；`compat:117`）。
- `costRead` 是**服务期 wire 字段而非折叠状态**（`cost.ts:85-89`），所以价格书变化不需要 bump `stateVersion`。
- 账本口径：`contextTimeline.cost` 是会话级（结算时计价）；概览 KPI 走 `contextActivity` 日账本；二者在峰谷边界上可能不同（`compat:61`）。

### 5.2 activity（日账本）

`contextActivity` 投影把事件折成 **按天** 的 `ActivityDay`：token/成本/turn/命令/工具/skill 计数，供 insights 页 KPI、热力图、12 卡、榜单使用。约束（`compat:61`）：保留 **400 天**；每天**最多 64 个名字**（工具/命令/skill 榜单）；命令在 dispatch 时记账（Command Executions 卡等结算）；PTC 只保留 start 才能记 dispatch。所有概览聚合一律以日账本为唯一账单源，保证「宽区间包含窄区间」。

### 5.3 fleet（agent 父子树）

- 数据源：`subagent/catalog`（`childId/childCreatedAt/mode`）+ `team/member`，**按形状解析、不按 payload 版本门控**（`compat:29`）；模式 `one-shot|continuable|external|unknown`；`continuable` 无 `label` 则丢弃。
- 路由三形状（`compat:121`）：单会话 `preview/full/history`（保持字节兼容）、`summary`（**只读 root 日志**：rootId/rootCreatedAt/detail/members/familySize/counts，附按 root `header.createdAt` 生命周期的计数缓存）、`batch`（≤16 id，共享一次冷读 admission，单 id 失败进 `missing`）。
- 前端先画 summary + 会话快照，再自动续 16-id 批次；精确家族总数用「宿主计数 + 每个子会话自己的 messagesTotal/delegations」的紧凑账本，**不依赖被保留窗口裁剪过的 payload**（`compat:121`）。

### 5.4 attribution / toolSources / skills

- `toolSources.ts`：静态 `FIRST_PARTY_SOURCES` 表把工具名映射到来源包（如 `read→@deepseek-ai/dsh-tool-fs`、`grep→dsh-tool-fs-search`），`mcp__<server>__<tool>` 前缀映射为 `mcp:<server>`；`schedule_*` 工具按检测到的版本线选 `dsh-schedule`（0.2.0）或 `dsh-tool-schedule`（0.2.1+）（`toolSources.ts:3-9,15-40`；`compat:32`）。
- `attribution.ts`：对**非已知工具**从模块 URL 向上找 12 层 `package.json` 拿包名（带目录缓存），失败给 `UNKNOWN_TOOL_SOURCE`（`attribution.ts:12-30`）。`contextHeaders` 投影把「每次请求的工具清单」与归属绑定，供浏览器工具卡展示。
- `skills` 路由：走 DSH `skillRegistry.list({cwd,scope})`，上限 500；冷读也过冷读闸门且 `projectionMode:'none'`（`skills.ts:8-19`；`compat:107`）。

### 5.5 timeline / delta / attachment / balance / recom / version

- `timeline`：`contextTimeline` 的 detail 视图（请求行、事件行、节点窗口、命令、spans），承载「逐请求趋势/事件/浏览器/命令执行」四块 UI。
- `delta`：`shared/delta.ts` + `shared/increment.ts` + `shared/assemble.ts` 做两个 seq 之间的**上下文增量**（哪类变多/变少、被谁替换），冷路径独立 `foldCache` 且 `gate.live=false`（`delta.ts:7-17`）。
- `attachment`：附件元数据/内容读取，走冷读闸门。
- `balance`：优先 `deepseekAccount.getBalance()`（`normal_wallets→toppedUp`、`bonus_wallets→granted`），否则 API key `/user/balance` 兜底（`compat:122`）。
- `recom`/`version`：推荐卡与 npm 版本检查；版本比较在宿主（`shared/version.ts`），前端只渲染；冷/失败时 `ready:false`，**不伪造版本**（`compat:116`）。

---

## 6. promptInjection（系统提示词前缀/后缀）

实现极短，但**设计位置很关键**：它不改 DSH 的 prompt 内容本身，而是向 DSH 的 `systemPrompt` 服务**注册两个 section**（`promptInjection.ts:35-53`）：

```ts
ctx.inject(['systemPrompt'], inner => {
  systemPrompt.section({
    name: 'dsh-context:prompt-prefix',
    order: systemPrompt.getSectionOrder('HARNESS_IDENTITY') - 1000,   // 排到身份段之前
    text: () => config.promptPrefix?.trim() ? config.promptPrefix : '', // 空串则整段不出现
    interpolate: false,
  })
  systemPrompt.section({
    name: 'dsh-context:prompt-suffix',
    order: systemPrompt.getSectionOrder('DEPLOYMENT_PERSONA_SUFFIX') + 1000, // 排到最后
    ...
  })
})
```

- **生效时机**：注册即生效，宿主每次组装 system prompt 时按 order 拼接；`text` 是**惰性函数**，支持 Schemastery 的 `.get()` 响应式读配置（`promptInjection.ts:14-31`），所以设置页改动下一次请求就生效，无需重载插件。
- **空值语义**：`sectionText` 在 trim 后为空时返回 `''`，按 DSH 约定空 section 不产生内容（`:26-31`）。
- **限制**（`compat:95`）：若部署方注册了 `complete` 型 system prompt section（整体替换），插件的前后缀会被丢弃——这是 DSH 的优先级语义，插件无法覆盖。
- **与 meter 的交互**：改 system prompt 会让 token-meter 的 `projectedTokens` 在下次 usage 采样前按零 delta 折叠（本地 `dsh-token-meter/README.md`：A system-prompt rewrite carries no shadow price），所以 dsh-context 的 `systemTokens` 会**立刻**反映新提示词，而占用率投影可能滞后一拍。

---

## 7. 兼容性策略（哪些是 DSH 内部实现）

`docs/compatibility.md`（152 行）是这份插件最有价值的「契约考古」文档，核心思想是**数据形状驱动、绝不按版本分支折叠**。

### 7.1 分层策略

| 层 | 策略 | 证据 |
|---|---|---|
| 准入 | 插件清单 `engines.dsh` + peers 双声明；DSH 侧 preflight 拒装；运行时再验一次，低于 floor 注册 **3 个零值 fallback 投影 + 升级提示**，绝不折叠 | `compat:13-18`；`index.ts:52-57`；`fallback.ts` |
| 探测 | 先查运行中模块树，查不到则 **fail-open**（当作支持，进入真单元）；home profile 树只作兜底证人 | `compat:17`；`version.ts`；`shared/version.ts` |
| 折叠 | **单一形状驱动折叠路径，永不按版本分支**；依赖 DSH 的 v3→v4 迁移保证，不写 legacy reader；无法转换的日志报 `SessionFormatUnsupportedError` | `compat:24-25` |
| 计量 | 镜像 DSH 的 estimator 源码（4 字符/token 同一套），用 `scripts/diff-fold.ts` 差分对拍；不 import | `compat:41-50,147` |
| 投影缓存 | 每个 unit 声明 `stateVersion`；形状变化即 bump，DSH 缓存按 ver 拒绝旧行；旧行另做一次性瘦身迁移 | `timeline.ts:378`；`sweep.ts`；`compat:98-108` |
| 客户端 | 一切能力走**可选 seam**（只有 slots/locale 硬注入），缺服务=没能力，不 throw、不挂起 fiber | `compat:70-96` |

### 7.2 明确依赖 DSH 内部实现（版本风险最高的点）

1. **`@deepseek-ai/dsh-session` 的 `deriveEventMessage()`**（`fold.ts:17` 直接 import）：把事件投影成 message。这是包内导出，但语义随 surface 实现变化。
2. **projection unit 契约细节**：`stateSchema`/`wire.viewSchema`/`stateVersion`/`wire.summary`、`Object.is` 门控、`snapshot(session, keys)` 的 `keys` 参数（`timeline.ts:389-396`；本地 `dsh-session-projection/README.md`）。
3. **切片折叠四件套**：`sessionProjections.restore/hydrate/prepare/write` + `sessions.prepare` + `cache.write`（`backfill.ts:38-45,351-364`）——这是 DSH 没有公开 README 的进阶面，插件用**能力探测**（typeof 全在才用），缺失就退化为单发冷折。
4. **投影缓存存储**：`storageDomain('session_projcache').table('sessions')`、行形状 `{rows:{key:{ver,seq,val}}}`（`sweep.ts:26-34`）——纯内部形状。
5. **`sessionPersistence.stat(id)`** 的 `{location.path, sizeBytes, eventCount}`（`overviewCache.ts:356-379`；`backfill.ts:112-131`）：缓存的身份与新鲜度全靠它；README 明确「今天没有 in-tree backend 报 eventCount」，所以实际以 presence 探测为准（`compat:106`）。
6. **`systemPrompt.getSectionOrder('HARNESS_IDENTITY'|'DEPLOYMENT_PERSONA_SUFFIX')`**（`promptInjection.ts:43,49`）：依赖 DSH 具体 section 名。
7. **`connection.fetch.register`** 的路由形状（`requestBody:'buffered'`）（`connection.ts:1-10`）。
8. **事件 payload 形状**：`request/header.header.tools/config`、`request/context.contextWindow`、`assistant/message.usage.{inputTokens,cacheReadTokens,cacheWriteTokens,outputTokens,reasoningTokens}`、`compaction/summary.shadowedSeqs|shadowedTokenCount`、`tool/ptc-dispatch*`、`job/registered|settled`、`subagent/catalog`、`team/member`、`session/end-seed.inherited`。
9. **DSH 的 estimator 源码文本**（`packages/llm/token-meter/src/estimate.ts` 等）：镜像意味着 DSH 改公式，插件要跟。

---

## 8. 可照搬 vs 不可照搬（Go 视角）

### 8.1 可照搬（数据模型 / 算法 / 协议）

| 项 | 说明 | 参考位置 |
|---|---|---|
| **Event → Fold(state) → View 三层** | 纯函数折叠 + 版本化状态 + 只读 wire view；状态可 JSON 序列化做 checkpoint | `fold.ts` / `timeline.ts` |
| **7 类组成 + 增量 sums** | 增删同步加减，删除走 splice + 归档（保留 goneSeq） | `fold.ts:147-289` |
| **表面替换（surfaceOp）模型** | 不删历史、按区间替换、被替换节点进 archive 带 floor | `fold.ts:242-288`；`foldState.ts:214-264` |
| **3.4 的估算口径** | 4 字符/token + role/block 各 4；工具 schema 整体 JSON；DeepSeek v41 图片公式；文件按句柄文本 | `estimate.ts`；`pricing.ts`；`imageTokens.ts`；`fileTokens.ts` |
| **head/detail 两形状 + floor** | 活投影只维护 head（2 请求、清空明细），明细按需冷折；裁剪写 floor | `foldState.ts:294-376` |
| **revision + unchanged 协议** | 客户端带 revision，服务端命中只回 `{}` + unchanged | `overview.ts:444-455`；`snapshotRoute.ts:109-118` |
| **三级读取回退** | live → 持久 checkpoint → 冷折（带全局限流/堆水位） | `headsRoute.ts:61-83` |
| **磁盘预聚合 + 身份键** | `{path,sizeBytes,mtimeMs}` 判定缓存是否仍新鲜；原子写、防抖、空闲释放 | `overviewCache.ts` |
| **限速回填** | 切片自适应（预算/耗时）、累计工作后暂停、覆盖率上报、按 corpus 签名跳过 | `backfill.ts` |
| **峰谷计价 + 计数桶** | 折叠期只存 4 桶×峰谷×provider×model，服务期再乘价格 | `foldMetering.ts:37-57`；`pricing.ts:504-509` |
| **shape-driven 兼容** | 不按版本分支，只按字段形状；能力探测 + 缺失退化 | `docs/compatibility.md` 全文 |

### 8.2 不可照搬（运行时/框架/前端）

| 项 | 原因 | Go 侧替代 |
|---|---|---|
| cordis 插件模型 + `ctx.inject/effect/get` 生命周期 | ZeoKeel 没有 cordis；且其实例隔离/副作用回收语义难复刻 | 显式构造 + `context.Context` 取消 + 依赖注入结构体 |
| session projection registry（eager drive、`Object.is` 门控、checkpoint） | DSH 内部契约；Go 版没有这个注册表 | 自己的「读模型管理器」：事件回调驱动 + 版本号 + 快照缓存 |
| `sessionProjections.restore/hydrate/prepare/write` 切片折叠 | 依赖 DSH 内部 API，README 无公开说明 | 自己实现「按事件块推进 + 定期落 checkpoint」；或简化为 worker 整折 |
| `storageDomain('session_projcache').table('sessions')` 行迁移 | 纯 DSH 内部存储形状 | 自己的 SQLite/JSON 表 + schema version 迁移 |
| `worker_threads` eval worker 引导自身 bundle | Node 特有 | Go goroutine + 有界 worker pool（天然更省） |
| zod schema / `wire.viewSchema` | JS 生态 | Go struct + 手写校验；用 `stateVersion int` 做兼容 |
| React client（slots/seats/zustand/shiki） | ZeoKeel UI 是 mygo 原生控件，无 DOM | mygo 组件 + 宿主提供的聚合 API；不要把计算搬到 UI |
| 镜像 DSH estimator 的「源码级对拍」 | ZeoKeel 没有 DSH token-meter 可对拍 | 定义自己的口径并**只说估计**；可用固定 fixture 做回归 |
| `deriveEventMessage()` | DSH 包内部语义 | 自己解析事件 → 消息（JSON 结构稳定，见 §2.2） |

### 8.3 对 Go 实现的具体启示

1. **先定数据模型，再写 UI**：`SurfaceNode/Category/RequestRecord/ContextEventRecord/ActivityDay/TimingTotals` 直接翻译成 Go struct（含 `json:"..."` tag），并用 `StateVersion` 常量做持久化兼容。这是成本最低、收益最大的一步。
2. **折叠器做成纯函数包**：`func Apply(state State, ev Event, bounds Bounds) State`，不改入参（或只拷贝被改字段）；坏事件返回原 state。Go 里用值拷贝 slice 的代价要评估——建议 `state.Surface` 用 copy-on-write，或给每个事件返回 `(State, bool changed)`。
3. **三层读取 + 限流**：`Get(id, keys)` 走 live cache → SQLite checkpoint → 冷折；冷折用 `semaphore.Weighted(1)`（对应 FIFO 串行）+ `runtime.ReadMemStats` 做堆水位闸门（对应 V8 2/3）。ZeoKeel 是单进程桌面客户端，可用更简单的 `singleflight` + 全局并发 2。
4. **预聚合与 revision**：概览别让 UI 每次扫全库。宿主维护 `revision = hash(corpus + coverage + pricing + epoch)`，命中返回 `unchanged`；预聚合结果落 SQLite（或 `$DSH_HOME` 等价目录的 JSON），用文件 `path+size+mtime`（或 ZeoKeel 自己的 storage 版本号）判新鲜度。
5. **token 估算必须标注「估计」**：4 字符/token 对 CJK 与 JSON schema 严重低估（DSH 自己 README 也这么说），组成环不能当账单。计费用 provider usage，`contextPressure` 用 `prompt` 字段。
6. **成本字段分两段**：折叠期只累 `{provider,model,peak/off,4 buckets}`；价格表宿主侧拉取/内置，1h TTL，失败 fail-open（`ready:false`，不伪造）。DeepSeek 峰谷 ×2 规则要落到配置而非硬编码。
7. **兼容性用「形状 + 能力探测」而不是版本号**：读取 DSH/ZeoKeel 事件时先看字段在不在、类型对不对；缺能力就退化（fallback 空投影/单发冷折/不显示），绝不 throw。这是这份插件能跨 3 条版本线的根本原因。
8. **注意 Go 的 GC 与内存**：Node 版靠「空闲 20s 全清 + 8 槽 LRU + 512 缓冲 + 堆闸门」控制常驻内存；Go 版应改为「引用计数/弱引用式 TTL 缓存 + `runtime/debug.SetMemoryLimit` + 定期 `debug.FreeOSMemory` 观测」，并用 benchmark 证明峰值与常驻不随会话数增长。
9. **前端只做解码与格式化**：`docs/host-client-principles.md` 的原则（按需/分页/滑动窗口/无前端计算/无前端价格表）对 mygo 同样成立；「展开才加载」「按 id 批量取」「未知值显示 — 而非 0」这三条尤其值得抄。

---

## 9. 限制与风险清单

### 9.1 正确性风险

- **组成是启发式**：4 字符/token；CJK、JSON schema、图片（仅 DeepSeek 路线精确）都会偏。DSH 自己也不把它当计费值。
- **system prompt 被替换时**：只算最新存活 system，历史 system 不进 composition（`compat:63`），与 DSH `messageTokens` 口径不同。
- **继承种子成本重置**：fork 会话在 `session/end-seed inherited` 处重置 cost 与生成计数（`fold.ts:935-943`），所以插件 per-session cost 与 `tokenUsage` 消费者不可比（`compat:64`）。
- **计数口径**：`steps` 数「折叠到的 assistant message」，DSH `sessionStats` 数 `step/end`；空/取消的 step 会少算（`compat:59`）。
- **image offload**：折叠读 raw durable events，已 offload 的消息保持 offload 前的价与数量直到被压缩（`compat:33`）。
- **命令/后台任务在 0.2.0 线**：没有 `job/*` 事件，退出码/耗时靠结果文本标记推断（`derived:true`）（`compat:28`）。

### 9.2 性能与内存风险

- 单会话冷折是 **O(事件数)**，大会话（>30000 事件）走 worker；否则切片（2s 轮次/8s 工作预算）仍会占用宿主。
- `DetailShadow` 每个活会话一份完整 `TimelineState`，8 槽 LRU；槽位被挤掉后重新冷折（有延迟）。
- 首屏缓存播种：**空缓存首次概览要一次全量投影行扫描 + 一次 durable stat 扫描**；基准 922MB 峰值 vs 639MB 暖态、530MB 启动、477MB 空闲清扫后（`compat:128`，1,158 会话语料）。
- 概览缓存 20s 空闲释放 + 2s 写防抖：崩溃可能丢最后 2s 的写，靠 identity 变更自愈。
- revision 只记**最后一个**请求 key（`snapshotRoute.ts:141` 用 `new Set([servedKey])`），交替两种 key 会重复下发——属于可接受的省流近似，但 Go 版可以直接做得更好。

### 9.3 版本/契约风险

见 §7.2 的 9 条。最脆的三条：`deriveEventMessage`、切片折叠四件套、`session_projcache` 行形状。
**升级策略建议**：把这些点封在适配层（interface + 能力探测），其余折叠逻辑零版本判断。

### 9.4 未验证（本次未读/未跑）

- **client 半**（约 200 个 tsx/ts）：只读了 `categories.ts`/`apiRoutes` 的用法与 compat 文档描述，未逐文件核；`src/client/boundedCache.ts`/`overviewModel.ts`/`fleetDetail.ts` 的具体窗口策略未验证。
- `overviewAggregate.ts`/`overviewScan.ts`/`overviewDecode.ts` 的聚合公式（KPI/热力图/12 卡字段）未逐行读，只按 `overview.ts` 的 part 分发与 compat 描述引用。
- `activity.ts` 的按天分桶细节（日边界、时区、去重键）、`fleet.ts` 的成员卡解析与 `headers.ts`、`delta.ts`、`attachment.ts`、`balance.ts`、`recoms.ts`、`version.ts`、`logShapes.ts` 只读了文件头/关键导出，内部算法未逐行验证。
- **未在真实 DSH 上跑**：无 npm 安装、无真实会话语料，因此「布局/请求形状」均来自源码而非运行时观测；`scripts/diff-fold.ts` 未执行（需 bun + DSH 源码 checkout）。
- 插件版本为 **0.84.1**（抓取时 main）；任务描述里「README 声称支持 0.2.0-rc.2+」已由 `docs/compatibility.md:9` 与 `package.json:18` 证实。

---

## 10. 给 ZeoKeel 的最小可行方案（建议）

**Phase 1（1-2 天，只读）**：翻译 shared 数据模型 + estimate/pricing/imageTokens/fileTokens + `Apply()` 折叠器（只做 `request/header`、`system/message`、`user/message`、`assistant/message`、`tool/call`、`tool/result`、`step/start|end`、`compaction/summary` 八族），输出 `Current` 7 类 + `Requests[]`；对 ZeoKeel 自己的会话日志跑一遍，与 UI 现有用量数字对齐。
**Phase 2（2-3 天）**：`GetTimelineDetail/GetHeads` 两层 API（ZeoKeel 进程内直接调用，不走 HTTP 也行）+ 内存 LRU（TTL 20-60s）+ 滑动窗口/floor；接 mygo 的 Context 面板（组成环 + 逐 step 趋势 + 事件列表）。
**Phase 3（可选）**：日账本预聚合 + `revision/unchanged` + SQLite 落盘；成本按价格表 + 峰谷计数桶；fleet 复用 ZeoKeel 已有 team/task 契约（不必照抄 DSH 的 subagent/catalog 事件形状）。
**不要做**：不要为「等价」去模拟 cordis projection registry、worker threads、zod 契约或 client 缓存层——那是为 DSH 插件沙箱付的成本，ZeoKeel 是自家单进程客户端，可以直接用 Go 的结构体、goroutine 和 SQLite。

---

## 附录 A：关键常量速查

| 常量 | 值 | 位置 |
|---|---|---|
| CHARS_PER_TOKEN / ROLE_OVERHEAD / BLOCK_OVERHEAD | 4 / 4 / 4 | `src/shared/estimate.ts:1-3` |
| 图片 token 上限 / 最小像素 / patch / 下采样 | 1024 / 544² / 14 / 3 | `src/shared/imageTokens.ts:1-5` |
| DeepSeek 峰谷倍率 / 汇率 | ×2 / 1 CNY=0.15 USD | `src/shared/pricing.ts:21-23` |
| 北京时间峰段 | 周一~五 09-12,14-18 | `src/host/foldMetering.ts:27-35` |
| 折叠上界 | 1500 请求 / 300 turn / 400 事件 / 2000 节点 / 400 归档 / 400 文件 / 400 命令 / 4096 字符 | `src/host/config.ts:4-13` |
| 活投影瘦身 | 2 请求 / 0 事件 / 16 运行中命令 | `src/host/foldState.ts:368-376` |
| shadow 槽/缓冲/空闲 | 8 / 512 / 20s | `src/host/detailShadow.ts:9-13,62` |
| foldCache 空闲 | 20s 全清 | `src/host/foldCache.ts:18` |
| 概览缓存文件/防抖 | `overview-cache.v1.json` / 2s | `src/host/overviewCache.ts:8-12,88-96` |
| 回填 | 25ms 最小休息 / 2000 切片 / 128 下限 / 150ms 预算 / 30000 worker 阈值 / 8s+2s 轮次 | `src/host/backfill.ts:59-70` |
| 冷读堆水位 | V8 heap < 2/3 | `src/host/coldRead.ts:12` |
| snapshot ids / heads ids | 256 / 64 | `src/host/snapshotRoute.ts:10`；`headsRoute.ts:14` |
| timeline stateVersion | 42（旧 36→37 迁移） | `src/host/timeline.ts:378`；`sweep.ts:7-9` |
| 日账本 | 400 天 / 64 名字每天 | `docs/compatibility.md:61` |
| 客户端成本重试梯度 | 1.5s / 4s / 10s | `docs/compatibility.md:116` |
| 目录 TTL | 1h（失败 30s backoff） | `docs/compatibility.md:116` |

## 附录 B：14 条路由一览

`snapshot`、`heads`、`overview`、`backfill`、`detail`、`delta`、`facts`、`commands`、`fleet`、`skills`、`attachment`、`balance`、`recom`、`version`（`grep -n "ROUTE = " src/host/*.ts`）。
