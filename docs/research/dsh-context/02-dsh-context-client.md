# dsh-context 客户端 UI 原理与 ZeoKeel/mygo 可替代性评估

> 调研对象：`bowenliang123/dsh-context` 的**浏览器侧**（`src/client/**`）。
> 源码快照：GitHub `main` 分支，tree `d81325ec6bab9dae9bf83e7a866ac74119504e18`（2026-11-14 抓取；raw.githubusercontent.com 在本机不可达，经 ghfast.top 镜像逐文件抓取 224 个文件，其中 `src/client` 顶层 77 项（含 `components/`、`styles/` 两目录），`i18nDe/Es/Fr/Ja/Pt/Ru` 六个大词典未抓，只核对语言清单）。
> 本地 DSH 版本：`@deepseek-ai/dsh 0.2.1-rc.2`（`/usr/local/lib/node_modules/@deepseek-ai/dsh/node_modules/@deepseek-ai/`，下文简称 `@dsh/…`）。
> mygo 依据：`mygo@v0.9.0`（模块缓存 `/data/mygo/.cache/gopath/pkg/mod/github.com/egoist/mygo@v0.9.0/docs/ui/*.md`），ZeoKeel 依据 `docs/DESIGN.md`、`docs/UI-CONFORMANCE.md`、`docs/INJECTION-POINTS.md` 与 `internal/**` 代码。
> 本文只做调研，未改任何代码；证据格式为 `文件:行号`。标注「未验证」的是没有直接读到的推断。

---

## 0. 一句话结论

dsh-context 的客户端 = **「6 类宿主扩展点、7 个注册点」+「1 个 URL 路由数据层（15 条 POST 路由，revision/unchanged 增量）+「1 套 HTML+CSS 图表（76 个组件里仅 8 个文件含 SVG、0 canvas）」**。
它没有任何自研渲染引擎：所有卡片是普通 DOM，图表是「百分比宽高的 div + CSS 渐变 + 少量 SVG 圆环/贝塞尔连线」，交互（hover 联动、pin、点击跳 transcript、右键打开右栏）全部是 React 状态 + 宿主（Host）已有的服务调用。
因此对 mygo 的结论是：**信息架构与交互可完整平移，图表用 `ui/chart` + `ui.Painter` 可做得更好，真正贵的只有「一级侧边栏全局页」和「跨 5 个入口的共享状态」这两处壳层/模块边界成本。**

---

## 1. 客户端分层

```
┌─────────────────────────────── 浏览器（dsh web client）───────────────────────────────┐
│  dsh 宿主客户端插件系统：ctx.slots / ctx.locale / ctx.layout / ctx.sidebarRightTabs /   │
│  ctx.inputTriggers / ctx.configForms / ctx.sessionProjections(经 slot props 透传)       │
│                                                                                        │
│  dsh-context client bundle（src/client/index.ts 的 apply(ctx)）                        │
│   ├─ 入口层：index.ts(编排) / insightPage / sidebar / fleetTab / command / placement    │
│   ├─ 设置层：settings.ts（内存 store）+ components/settingsCard.tsx（Plugins 页卡片）   │
│   ├─ 数据层：timelineSource.ts / overviewSource.ts / fleetModel.ts / historyPage.ts …  │
│   │        route.ts（POST 信封 + revision）+ retention.ts/boundedCache.ts（缓存上限）   │
│   ├─ 视图层：components/contextView.tsx（会话主看板，宿主 tab 与右栏共用同一棵树）      │
│   │        components/overviewPanel.tsx（上下文洞察页）                                 │
│   │        components/fleetView.tsx（Fleet 页）+ missionPulse（协作枢纽）               │
│   │        components/contextModal.tsx（/context 弹窗）                                 │
│   └─ 图表层：stackedBar / trendChart / trendDnaBars / trendOverview / donut / heatmap / │
│            browserTreemap / agentGraph（详见 §6）                                       │
└────────────────────────────────────────────────────────────────────────────────────────┘
                                        ↕ HTTP POST（同源 fetch，15 条路由）
┌──────────────────────────── 宿主 Node 侧（src/host/**）───────────────────────────────┐
│ ctx.sessionProjections.register(contextTimeline / contextHeaders / contextActivity)     │
│ watch*Channel() 注册 api/dsh-context/* 路由（detail/delta/facts/overview/fleet/…）       │
│ 折叠 worker（foldWorker）、backfill（异步回填）、pricing/balance 等                     │
└────────────────────────────────────────────────────────────────────────────────────────┘
```

证据：
- 客户端入口与全部注册点：`src/client/index.ts:70-162`；模块导出 `module.exports = { name, inject: ['slots','locale'], apply }`（`src/client/index.ts:164-168`）。
- 宿主侧：`inject = ['sessionProjections']`（`src/host/index.ts:33`）、projection 注册（`src/host/index.ts:75-77`）、各路由 `watch*Channel`（`src/host/index.ts:34-38, 78-90`）。
- 「视图树只有一份」：右栏与弹窗复用同一个 `ContextView`/`ContextBrowser` 工厂（`src/client/index.ts:90, 98`, `src/client/components/contextModal.tsx:43-48`）。

---

## 2. 五个正式入口（+2 个附加入口）

### 2.1 挂载契约总表

| # | 用户看到的入口 | 注册 API（DSH 槽位/服务） | dsh-context 证据 | 宿主侧契约证据 |
|---|---|---|---|---|
| 1 | **上下文洞察页**：一级侧边栏面板行 + 主栏整页 | `ctx.slots.register({ name:'main', key:'dsh-context' })`（主栏 keyed slot）+ `ctx.slots.register({ name:'sidebar.panellist', id:'dsh-context', order:20, label })`（侧栏面板列表行） | `src/client/insightPage.ts:8-32`（`INSIGHT_PANEL_ID='dsh-context'`、`ORDER=20`、两条注册） | `@dsh/dsh-client-ui-layout/README.md:32`（`main` keyed slot + `ctx.layout.selectPanel(id)`）；`@dsh/dsh-client-ui-sidebar/README.md:38`（`sidebar.panellist` 的 id/order/label，同一 id 对应 main 的 key） |
| 2 | **会话 Context 标签页** | `ctx.slots.register({ name:'conversation.view', id:'context', order:20, label })`（会话视图标签） | `src/client/index.ts:93-99`；`CONTEXT_VIEW_ID='context'`（`src/client/dshFaces.ts:313`） | `@dsh/dsh-client-ui-conversation/README.md:85,91`（`conversation.view` 是会话级 View tab，偏好持久化、空会话不出现） |
| 3 | **右侧 Sidebar 面板（Context 页）** | `ctx.sidebarRightTabs.register({ id:'dsh-context', kind:'dsh-context', title, guide:[…] })` + `ctx.slots.register({ name:'sidebar.right.pane.tab', key:'dsh-context' })` + `…pane.tab.title` | `src/client/sidebar.ts:5-7, 17-45`（`SIDEBAR_CONTEXT_ID/KIND`、guide 入口 `order=20`、body 与 title 两个 keyed slot） | `@dsh/dsh-client-ui-sidebar-right/README.md:12,48,83-84`（tab-type 两段式注册、`ctx.sidebarRight.openTab/openResource`、`useTabInfo()` 提供 `sidebar/panel/tab`） |
| 4 | **`/context` 斜杠命令弹窗** | `ctx.get('inputTriggers').registerSource({ trigger:'/', name:'context', candidates, onPick, matchEnter })` + `ctx.slots.register({ name:'conversation.input.overlay', id:'context-modal', inject })` | `src/client/command.ts:6-40`（命令源 + `onPick`/`matchEnter` 都 `modalStoreOf(sessionId).set(true)`）；`src/client/index.ts:117-128`（overlay 槽 + `inject: { hooks:{contextModal}, warm:true }`） | `@dsh/dsh-client-ui-commands/README.md:12,28`（`/` 源的 resolved 三态：popupSelect / action / execute；`ctx.commandUi`）；`@dsh/dsh-client-ui-conversation/README.md:74,85`（`conversation.input.overlay` 是 composer 输入浮层） |
| 5 | **插件设置卡片**（Plugins 页 → bundle 页 → Configuration） | `ctx.configForms.whileServed([NS], …)` 内 `ctx.slots.register({ name:'plugins.bundle.config', key:NS })` | `src/client/index.ts:147-161`（`NS='dsh-context'`；`forms.get(NS)` 绑定设置 scope；`whileServed` 保证宿主不服务该 namespace 时整卡不出现） | `@dsh/dsh-client-ui-plugin-manager/README.md:73,131,133`（`plugins.bundle.config` 按 npm 包名 keyed；页面声明该 child slot）；`@dsh/dsh-client-ui-settings/README.md:28,34-44,64`（`ctx.configForms.get/whileServed`、共享 describe 镜像、Host 设置文档为唯一权威） |
| 6 | （附加）**回复行「Context」跳转按钮** | `ctx.slots.register({ name:'conversation.chat.assistant-actions', id:'context-jump', order:20 })` | `src/client/index.ts:67-68, 109-115`；`src/client/components/contextJump.tsx` | `@dsh/dsh-client-ui-conversation` 的 assistant 动作槽（README 未逐字列出该名为「未验证」；dsh-context 以 `entrySeated()` 幂等判重，说明同宿主可能已有占用：`src/client/dshFaces.ts:35-63`） |
| 7 | （附加）**Fleet 标签页** | 同 #2 的 `conversation.view`，`id:'fleet', order:30` | `src/client/fleetTab.ts:6, 15-21`（`FLEET_ORDER=30`，受 `settings.fleetTab()==='show'` 门控） | 同 #2 |

### 2.2 门控与动态挂载（重要设计）

五个入口**不是无条件注册**，而是由设置驱动「挂载/卸载」：

- `placement`（Context 面板入口）= `all` / `tab` / `sidebar` / `hide`，用 `watchPlacement(settings, {tab, sidebar})` 在 settings.store 变化时增量注册/注销两个入口（`src/client/placement.ts:9-14, 16-55`；调用点 `src/client/index.ts:93-99`）。
- `insightsEntry`（洞察页 Show/Hide）与 `fleetTab`（Fleet tab Show/Hide）用通用 `watchGate(store, wanted, mount)` 门控（`src/client/toggleGate.ts:3-32`；调用点 `src/client/insightPage.ts:19-22`、`src/client/fleetTab.ts:15-17`）。
- 这意味着**关闭入口 = 注销 slot = 组件与数据层一并释放**，而不是渲染时 `if` 隐藏；是它控制常驻内存的关键手段之一。

### 2.3 入口 → host API → 组件 → 数据 链路图

```mermaid
flowchart LR
  subgraph 入口
    A1[sidebar.panellist + main keyed<br/>dsh-context]
    A2[conversation.view id=context]
    A3[sidebarRightTabs + sidebar.right.pane.tab]
    A4[inputTriggers '/' + conversation.input.overlay]
    A5[plugins.bundle.config]
    A6[conversation.chat.assistant-actions]
    A7[conversation.view id=fleet]
  end
  subgraph 组件
    C1[overviewPanel.tsx<br/>KPI/7日图/热力图/环/技能·工具·命令/会话卡]
    C2[contextView.tsx host=tab<br/>Stats×3 / Current / Trend / Browser / Events / Files / Commands / AgentGraph]
    C3[contextView.tsx host=sidebar<br/>同一棵树 + 宽度上限/可见性门控]
    C4[contextModal.tsx<br/>CurrentComposition + ContextBrowser]
    C5[settingsCard.tsx<br/>13 组偏好 + prompt 前后缀]
    C6[contextJump.tsx]
    C7[fleetView.tsx + missionPulse<br/>AgentNetwork / 协作 / 任务 / 事件]
  end
  subgraph 数据
    D1[overviewSource.ts<br/>POST overview part=summary/days/tally/sessions + backfill]
    D2[timelineSource.ts DetailStore<br/>projection contextTimeline → POST facts(parts,window)]
    D3[projection: contextPressure / contextBreakdown / tokenUsage / contextHeaders]
    D4[historyPage.ts / delta / attachment / commands 分页]
    D5[settings.ts ← configForms.get('dsh-context')]
    D6[fleetModel.ts + fleetDetail.ts<br/>POST fleet 路由]
  end
  A1-->C1-->D1
  A2-->C2-->D2
  A3-->C3-->D2
  A4-->C4-->D2
  A5-->C5-->D5
  A6-->C6
  A7-->C7-->D6
  C2-->D3
  C2-->D4
  C7-->D2
```

（`C2`/`C3`/`C4` 共用 `useContextSession()`：`src/client/sessionData.ts:11-21`。）

---

## 3. 客户端数据获取与实时更新

### 3.1 路由清单（15 条，全部同源 POST）

`src/client/apiRoutes.ts:1-27`：

| 路由 | 用途 | 主要消费者 |
|---|---|---|
| `api/dsh-context/detail` | 整份会话时间线明细（旧版兼容路径） | `timelineSource.ts:147` |
| `api/dsh-context/facts` | **主路径**：按 `parts` + `window` 拉取时间线分片 | `timelineSource.ts:231`、`facts.ts`(host) |
| `api/dsh-context/delta` | 单个请求的 assembled 上下文 delta（浏览器按需展开） | `timelineSource.ts:213` |
| `api/dsh-context/overview` | 洞察页分片（summary/days/tally/sessions） | `overviewSource.ts:297` |
| `api/dsh-context/backfill` | 触发宿主异步回填历史会话聚合 | `overviewSource.ts:406` |
| `api/dsh-context/fleet` | Agent 家族：preview/history/full 三档 | `fleetDetail.ts:228,241,256` |
| `api/dsh-context/heads` | 批量 agent 头部（另：`detail` 兜底） | `agentHeads.ts:20,40` |
| `api/dsh-context/commands` | 命令执行分页 | `commandPages.ts:65` |
| `api/dsh-context/attachment` | 附件预览路径 | `attachmentSource.ts:26` |
| `api/dsh-context/skills` | Skill 目录 | `skills.ts:28` |
| `api/dsh-context/balance` | 账户余额 | `balance.ts:100` |
| `api/dsh-context/recom` | 赞助/推荐位 | `components/recomCard.tsx:38` |
| `api/dsh-context/version` | 版本/升级门控 | `versionSource.ts:51` |
| `api/dsh-context/snapshot` | overview 快照（宿主侧 `snapshotRoute`） | host |
| `api/dsh-context/facts`（同） | 见上 | — |

### 3.2 信封协议：`ok/value/revision/unchanged`

- 所有业务路由由 `postRouteEnvelope()` 统一处理：`POST` JSON → 校验 `{ ok:true }` → 读 `revision` 字符串 → 若 `unchanged===true` 返回 `{value:null, unchanged:true, revision}`，否则解码 `value`；解码失败抛 `RouteError`（`src/client/route.ts:24-74`）。
- 三种消费强度：`postRoute`（失败抛）/ `postRouteEnvelope`（要 revision）/ `postRouteQuietly`（失败吞掉，返回 null，用于非关键路径如 delta、attachment、balance：`src/client/route.ts:76-87`）。
- 服务端 `revision` + `unchanged` 让**轮询/重连时零解码、零 diff**，这是它「大 session 不卡」的第一道保证。

### 3.3 实时更新：靠宿主 projection，不靠 WebSocket

这是最容易误判的一点。客户端**没有**自建 SSE/WebSocket 通道；实时性来自宿主已有的 session projection：

1. 槽位给组件注入 `useProjection` / `useChat` 等标准 hook（`src/client/seats.ts:47-62`；`src/client/dshFaces.ts` 中 `projectionOf`/`conversationNodesOf`）。
2. `useTimelineSource()` 读 `projection('contextTimeline')`，只取 **slim head**（含 `detailRev`）（`src/client/timelineSource.ts:799-806`）。
3. `detailRev` 变化 → `store.request(headRev)` → `DetailStore` 用 300ms 防抖合并请求（`DETAIL_DEBOUNCE_MS=300`，`src/client/timelineSource.ts:255`），POST `facts`（`core()` = `['head','series']`）。
4. 服务端返回的 `rev < target` 时，1s 后自动 catch-up（`CATCH_UP_MS=1_000`，`src/client/timelineSource.ts:259, 659-676`）。
5. 展开型数据用 `ensurePart(part)` 按需拉：`requests`（Delta/DNA 模式）、`nodes`（Browser 首次挂载）、`events`（卡片滚入）、`files`/`spans`（detail 就绪后，`src/client/components/contextView.tsx:282-299`）；分片失败重试 1s ×3（`PART_RETRY_MS=1_000`×3 次指数退避，`src/client/timelineSource.ts:261-263, 611-612`）。
6. 洞察页独立：`part` 懒加载 + 30s 轮询（`POLL_MS=30_000`）+ 250ms 合并刷新（`REFRESH_COALESCE_MS=250`）；回填未完成时指数退避触发 `backfill`（2s→15s，最多 10 次）（`src/client/overviewSource.ts:12-20, 390-414`）。
7. 跨面板失效总线很薄：`invalidate(kind)` 只有 5 种 kind（`sessions|overview|fleet|heads|pricing`），实际只有 `costRefresh.ts:23` 发 `pricing`（`src/client/invalidation.ts:1-34`；订阅方 `timelineSource.ts:724-727`、`overviewSource.ts:334`、`statsContext.tsx:77`、`agentHeads.ts:74`）。
8. 预热：会话详情在 `detailRev` 稳定 900ms 后后台 warm（`WARM_DELAY_MS=900`，`src/client/prefetch.ts:6-36`）。

### 3.4 缓存与淘汰（两层，全部有上限/TTL）

`retention.ts` 用一张常量表声明 **22 个作用域化缓存策略**（`src/client/retention.ts:19-42`），常见值：

| 缓存 | 上限 | 机制 |
|---|---|---|
| `detailStores`（每 session 明细） | 64 个 / 6 MiB / weight 8 | LRU + 字节估算（`detailBytesOf` 按行数估字节，`timelineSource.ts:715-722`）+ 活动 store 不可回收 + 淘汰时 `release()` |
| `agentHeads` | 128 / 1 MiB / TTL 60s | TTL + LRU |
| `fleetPreview` / `fleetHistory` / `fleetFull` | 64·4MiB / 64·4MiB / **4**·4MiB（均 TTL 5min） | 三档，full 只留 4 个 |
| `commandHighlight` | 600 / TTL 10min | 全局 |
| `modalStores` | 32 / TTL 10min | 每 session 弹窗开关 |
| `historyContent/Header` | 128 / 12，TTL 5min | 请求内容按需取 |

`boundedCache.ts` 是通用实现：`maxEntries / maxBytes / maxWeight / minEntries / ttlMs`，Map 迭代序即 LRU（`get` 时 delete+set 触碰：`src/client/boundedCache.ts:60-73`），`trim({protect, reserveWeight})` 支持「新插入前先腾位」（`src/client/boundedCache.ts:118-140`）；到期用单个 `unref` 定时器批量过期（`src/client/boundedCache.ts:170-202`）。

### 3.5 失败降级（每一层都有明确落点）

| 层 | 行为 | 证据 |
|---|---|---|
| 路由失败 | 非关键路径 `postRouteQuietly` 静默；关键路径抛 `RouteError` | `src/client/route.ts:76-87` |
| 明细失败 | 卡片显示 `DetailNote state="failed"` + 重试按钮；首屏 `SkeletonPlot` | `src/client/components/contextView.tsx:735-736`；`components/detailNote.tsx` |
| 数据未到 | `COLD_TIMELINE` 空时间线兜底 + 骨架屏（按最终尺寸占位） | `src/client/coldTimeline.ts`、`components/cardSkeletons.tsx`（README_en.md:218 设计原则） |
| 组件崩溃 | 每棵子树包 `ErrorBoundary` | `src/client/components/errorBoundary.tsx`；包裹点 `contextView.tsx:917` 等 |
| 能力不满足 | `UpgradeGate`（宿主版本低于基线时整体替换为升级提示） | `src/client/components/upgradeGate.tsx`；`src/host/index.ts:73-78` fallback projection |
| 设置不可写 | `settings.set` 先乐观更新再 `bound.set`；被拒则回滚到真值 | `src/client/settings.ts:157-169` |

---

## 4. 组件树与信息架构

### 4.1 会话 Context 看板（`components/contextView.tsx`，933 行）

渲染顺序（同一棵树给 tab / 右栏 / 弹窗）：

```
ContextViewBody(props: { sessionId, useProjection, useChat, host:'sidebar'? })
├─ CurrentComposition              当前上下文：6 色堆叠条 + reserve 斜纹 + 图例                (:639)
├─ lc-card[data-lc-trend] Context Trend                                                (:663)
│   ├─ 工具栏：Step/Turn、Total/Delta、DNA、Adaptive、Overview、时长曲线开关
│   ├─ TrendOverview（整段 ribbon，无横向滚动） 或 TrendChart（每请求一根 14px 柱，可横滚）
│   ├─ DetailNote / SkeletonPlot（失败/加载）
│   └─ RequestDetail（pin 后展开：实际 prompt/output/cache vs 估算）                    (:771)
├─ ContextBrowser（七类可折叠 → 元素行 → 实际内容；Live/历史 step）                    (:788)
├─ 右侧两列：
│   ├─ StatsContext（Turns/Steps/Answers/Skills/Human inputs/工具调用/花费/Agent 家族）  (:822)
│   │   └─ PluginInfo + RecomCard                                                      (:832-833)
│   ├─ StatsTokens（八类 token 拆分 + cache hit 环形）                                  (:838)
│   └─ StatsTiming（模型调用/工具/开销时间 + 环形）                                     (:841)
├─ 折叠后（below fold，懒挂载）：Context Events (:855) + File Activity (:886)            (:278)
├─ Command Executions（懒挂载 + 分页）                                                  (:898)
└─ AgentGraph（Fleet 家族卡，窄栏自动折行）                                              (:907)
```

关键交互与状态（全部在 `ContextViewBody` 局部 state，不落全局）：
- `selectedSeq`（pin 的 step）/`hoveredSeq`（hover 联动）/`hoverTurn`/`focusSeq`/`jumpSeq`/`hoverCat`/`focusCat`（`contextView.tsx:136-164`）。hover 有 140ms 延迟 + 320ms 的「pin 后锁定」（`HOVER_DELAY_MS=140`、`HOVER_LOCK_MS=320`，`:96-97, 177-209`），DNA 模式延迟 220ms。
- **图表 → Browser 的联动**：hover 柱子把该 step 的 assembled 上下文预览到 Browser；点击 pin 并打开对应分类（`:338-350`）。
- **Stats → 深处的跳转**：`onJump()` 把 turns/steps 滚动到 Trend、read/write/search/image 设 `filterFocus`、skills/answers/user 设 `catFocus` 并滚动到 Browser（`:321-337`）；`onToolPick` 打开 Browser 的 tools 分类（`:346-350`）。
- **家族跳转**：没有 Fleet tab 时回落到右栏 / 切 tab（`:342-345`，`activateViewTab`/`openContextSidebar`）。
- **DNA 与 Adaptive**：`dna` 开关 + `delta` 模式自动开 adaptive（`:141-158`）；大 session 时 DNA 惰性（`DNA_LAZY_MIN=512`，`:98`）。
- **滚动位置记忆**：`viewScroll` Map（上限 32，`retention.ts:37`）在会话离开时记录、回来恢复（`:360-381`）。
- 右栏模式额外做：`IntersectionObserver` 判定面板可见性是 DNA 是否允许、以及列宽上限 `browserCap` 的计算（`:211-220, 301-320`）。

### 4.2 上下文洞察页（`components/overviewPanel.tsx`，489 行）

```
lc-ov-pagehead  ← 页标题 + 设置齿轮（openPluginSettings）+ 折叠 + 日期范围 Preset
├─ KPI band（6 格）：会话数 / billed tokens / 估算成本 / cache hit / 工具调用 / 活跃时长   (:274-306)
├─ OverviewUsage（最近 7 天 tokens+成本双柱）                                             (:308)
├─ OverviewTokens（构成环）+ StatsTiming（家族生命周期时间）                              (:309-313)
├─ Tiles（4 列 @container 自适应）：Heatmap(8 周热力图) / OverviewSkills / Tool calls / Command executions (:314-360)
└─ 会话列表：OverviewCard 卡墙（构成环、tokens、turns、成本、workspace 面包屑；排序 recent/tokens/context；搜索；按 workspace 分组；点击 openSession）(:360-489)
```

- 工具栏状态：`range`(today/7d/30d)、`sort`、`coverage`(all/active/archived)、`metric`(sessions/tokens)、`pick`（按 skill/tool/cmd 过滤会话集合）、`day`（点热力图选一天）、搜索框（`:44-51` 常量 + 组件内 state）。**点范围 Preset 会写回设置**，其它开关只影响当前视图（README_en.md:227）。
- 数据分片：`useOverviewSummary/useOverviewDays/useOverviewTally/useOverviewPage` 分别对应 `overview` 路由的 part；`foldStateOf(coverage)` 驱动 backfill 退避（`src/client/overviewSource.ts:390-414`）。

### 4.3 Fleet 页（`components/fleetView.tsx` 199 行 + `missionPulse.tsx` 820 行）

- 结构：summary band → **Agent Network**（`agentGraph.tsx`）→ **Cross-Agent Collaboration**（`missionPulse`）→ **Teamwork Tasks** → **Event Inspection**（README_en.md:185-209）。
- 数据：`useFleetModel()`（家族树/团队，FLEET_ROUTE 的 preview/history 档）+ `useFleetDetails()`（full 档，按 `rootStamp`/`updatedAt` 失效，`fleetView.tsx:66-79`）；`agentTeam` projection 直读优先（`:53`）。
- 写作用域：`DnaBand`/`layoutForest` 等布局计算在 `agentTree.ts`（651 行，纯函数）；渲染与窗口化在组件。

### 4.4 `/context` 弹窗（`components/contextModal.tsx`，182 行）

- `ContextModalGate`：只有 `useContextModal(s=>s)` 为真才挂 body（`contextModal.tsx:42-49`），`warm:true` 时预热（`useSessionDetailWarm`）。
- body 用 `createPortal` 挂到 `document.body`，背景遮罩的左右边界**对齐 composer 的 dock 测量**（`measureDock` + `MutationObserver` 跟随 frame 宽度变化），Esc 关闭（`:100-135`）。
- 内容极简：标题 + CurrentComposition + ContextBrowser（`:128-170`）；关闭时回滚因 `/context` 被消费的草稿 token（`takePendingConsume` + `sessions.scope(id).bail(...)`，`:101-109`）。

### 4.5 设置卡（`components/settingsCard.tsx`，282 行）

13 个偏好行 + 2 个自由文本（`settingsCard.tsx:140-260`）：

| 组 | 字段 | 形式 | 默认 |
|---|---|---|---|
| 入口 | `defaultPlacement` | 4 选 1（all/tab/sidebar/hide） | `all` |
| 入口 | `insightsEntry` / `fleetTab` | Show/Hide | `show` / `show` |
| 视图 | `defaultOverviewRange` | today/7d/30d | `30d` |
| 视图 | `defaultGranularity` | step/turn | `step` |
| 视图 | `defaultTrendMode` | total/delta | `total` |
| 视图 | `defaultDurationCurve` | Show/Hide | `show` |
| 视图 | `overviewAutoSteps` | 100/200/300/off | `100` |
| 视图 | `defaultDeltaBase` | step/turn | `step` |
| 排序 | `defaultToolSort` / `defaultFileSort` | size/count/name；count/lines/latest/path/name | `count` / `lines` |
| 提示词 | `promptPrefix` / `promptSuffix` | 自由文本（清空按钮） | `''` |

（默认值全集：`src/client/settings.ts:92`；白名单校验：`settings.ts:71-89`。）

---

## 5. 视觉与布局约束

### 5.1 用到的 DSH token / 度量

- 全部颜色走 DSH alias 变量：`--dsw-alias-bg-layer-1/2`、`--dsw-alias-border-l1/l2`、`--dsw-alias-label-primary/secondary`、`--dsw-alias-interactive-bg-hover`、`--dsw-alias-state-error-primary`（`src/client/styles/base.css:22, 40, 60, 98`）。
- 卡片：`background: bg-layer-1; border: 1px border-l2; border-radius: 10px; padding: 14px 16px; margin-bottom: 14px`；浅色主题加大到 12px + 阴影（`styles/base.css:22-23`）。
- 控件：分段控件圆角 6px、段 5px、`font-size: 11px`、padding 3×8（`styles/base.css:34-40`）；搜索框 6px（`:49`）。
- 正文 13px、脚注 12px、微标签 11px（`styles/base.css:19, 60, 104`）。
- 布局：CSS Grid + **容器查询**（`@container lc-ov (max-width: 932/700/640/560/400px)`，`styles/overview.css:21-103`），4 列 tiles → 2 列 → 1 列；trend 区两列（`lc-col`）在侧栏 host 下收成单列。
- 阴影：`--lc-shadow-card`、`--dsw-shadow-lv3`；动画：`animate-lc-stacked-in`/`animate-lc-bar-in`/`animate-lc-donut-in`，全部带 `motion-reduce:animate-none`（如 `stackedBar.tsx:163`、`donut.tsx:58`、`trendDnaBars.tsx:5`）。

### 5.2 mygo/design 对应关系

| dsh-context | mygo/design | 说明 |
|---|---|---|
| `bg-layer-1` 卡片底 | `design.Panel` / `S1` 表面（`docs/DESIGN.md` §S0–S4） | 一一对应 |
| `border-l2` | `t.BorderL2` | 同名语义 |
| `label-primary/secondary` | `t.TextPrimary` / `t.TextSecondary` | |
| `state-error-primary` | `t.Error` / `design.SemanticError` | ZeoKeel 规则：error 必须用语义色 |
| 卡片 10/12px 圆角 | `design.RadiusMD(10)` / `RadiusLG(12)`（`DESIGN.md` §度量档位） | 只能引 token |
| 14×16 padding、14 间距 | `design.Space*` | |
| 13px 正文 / 12px 元数据 / 11px 微标签 | `TypeBody(14)` 偏低一档；ZeoKeel 现行规范正文 14、元数据 12、11 仅微标签（`AGENTS.md` 规则 12） | **不能照搬 13px 正文** |
| `@container` 响应式 | `ui.Split` 宽度 + 断点判断（`Row/Box` 重排） | mygo 无 CSS 容器查询，需在 build 里按宽度分支 |
| Hover / pin / 联动 | 官方 `Hovered`/`Selected` 状态 + 模块 state | 可做，但要手动管 repaint |
| `overflow-x` 横向滚动趋势图 | `ui.Scroll`/`List` 横滚（`docs/ui/scroll.md`） | 可行 |
| 点击开右栏文件预览 | `rightpanel.PreviewFile`（`contracts.KeyFilePreview`） | ZeoKeel 已有等价能力 |

---

## 6. 图表实现方式（关键：几乎全是 CSS）

| 图表 | 实现 | 证据 | 备注 |
|---|---|---|---|
| **堆叠条**（Current / 每步构成 / reserve 斜纹 / free window） | `<div>` 列表，`width: pct%`、`backgroundColor` 内联；reserve 遮罩用绝对定位 div + CSS 斜纹类；hover 高亮靠 class | `components/stackedBar.tsx:147-211` | 最小带宽 + 重新归一分摊（`:57-70`）；段数 ≤256 才播放入场动画（`SEGMENT_ANIMATION_MAX=256`，`:9`） |
| **趋势柱状图**（每请求一根） | 固定 `BAR_W=14`、`BAR_GAP=2` 的 div 柱，横向滚动容器 + **只渲染窗口内柱子**；hover/pin/事件标记（`✂`）都是 DOM | `components/trendWindow.ts:1-5`、`components/trendChart.tsx:1-30, 110-200` | 窗口：≤240 根全渲染；否则 `overscan 36` + `hysteresis 8`（`trendWindow.ts:11-36`） |
| **Overview ribbon**（整段压缩、无横向滚动） | 单块 SVG（宽度自适应，内部 path/rect 逐段） | `components/trendOverview.tsx:329` 起（SVG 片段，未逐行核） | 与 DNA 模式互斥 |
| **DNA 平铺** | 单个 `<div>`，背景 `linear-gradient(to top, color from+px, …)` 由每 band 的像素区间生成；2px 网格 snap；≤64 stop；hover 命中用 `clientY` 反算 | `components/trendDnaBars.tsx:71-110`（total）、`:123-166`（delta）、`DNA_SNAP_PX=2` `:33`、`DNA_MAX_STOPS=64` `:8` | 零 DOM 子元素，纯渐变 → 大 session 也便宜 |
| **时间环 / Donut** | **SVG** `<circle r=15.9155>` + `stroke-dasharray/dashoffset`（周长≈100，直接当百分比用） | `components/donut.tsx:52-68` | 环心文字用 DOM 绝对居中 |
| **热力图** | `div` 网格（8 周 × 7 天 = 56 个 cell），5 档 class `lc-heat-0..4`，每格一个 `<button>` | `components/heatmap.tsx:54-127`（`gridOf` :24-43，分档 :45-52） | 未来日期空槽；点选/取消点选 |
| **Treemap**（Context Browser 地图视图） | `squarify` 纯函数算矩形（`treemap.ts:1-163`）→ 绝对定位 div 块；仅图标是 SVG | `components/browserTreemap.tsx:1-60`（`MapIcon` :22-27），`GAP/FRAME/PAD` 常量 :31-45 | 文本省略阈值一整套（`LABEL_MIN_W` 等） |
| **Agent 网络图** | **SVG 画边**（三次贝塞尔 `M…C…`）+ 绝对定位 HTML 卡片节点；布局由 `layoutForest` 纯函数算 | `components/agentGraph.tsx:73-78`（`linkPath`）、`:264-290`（`<svg><path>`）；布局 `agentTree.ts`（`layoutForest` 等，651 行） | 悬浮点亮整条链、点击选中、运行中边脉冲（`animate-lc-agent-flow`）、窄栏折行不横滚 |
| 趋势时长曲线 | SVG | `components/trendDurationCurve.tsx`（含 `<svg`） | 可关 |

**统计**：`src/client/components/**` 共 76 个文件，含 `<svg` 仅 8 个（`agentGraph / browserTreemap / contextJump / donut / pluginInfo / pulseTimeline / trendDurationCurve / trendOverview`），`<canvas>` / `getContext` **0 处**。即：**dsh-context 没有图表库**，全部手写 DOM/CSS + 极少量 SVG。

---

## 7. 多语言与设置持久化

### 7.1 i18n

- 注册：`ctx.locale.register(NS, DICTS)`（`NS='dsh-context'`，`src/client/index.ts:72-75`），`t = ctx.locale.bind(NS)`。
- 8 种语言：`de/en/es/fr/ja/pt/ru/zh`（`src/client/i18n.ts:10-12`）；英文词典 **749 个 key**（`src/client/i18nEn.ts` 计数 749），`zh` 为 key 基准类型（`i18nZh.ts` 导出 `DictKey`，`i18n.ts:12`）。
- 语言切换后重渲染：`ViewKit.useLocaleTick()` = `useSyncExternalStore(locale.subscribe, () => locale.getLocale().active)`（`src/client/viewkit.ts:23-42`）；每个 memo 组件首行调用 `kit.useLocaleTick()`（如 `trendChart.tsx:110`），避免 memo 卡住旧语言。
- 所有 slot 注册都带 `locale: NS`（`index.ts:95,112,124,156`；`sidebar.ts:39`），让宿主在语言变化时重建标签。
- 服务端下发文本（分类名/事件标签）走 `catLabel`/`eventLabel`（`viewkit.ts:13-17`）。

### 7.2 设置持久化

- 宿主权威：`ctx.configForms.get('dsh-context')` 返回 `{ getSnapshot, subscribe, set }` 作用域；`whileServed(['dsh-context'])` 保证宿主没服务该 namespace 时卡片与设置都不出现（`src/client/settings.ts:9-18`；`index.ts:147-161`）。
- 客户端 `createContextSettings()`：手写 store（`subscribe/getSnapshot`）+ 11 个 `defaultXxx()` 读取器；`attach(scope)` 做首同步并订阅；`set(field, value)` **先乐观发布**再 `bound.set`，失败时重新 `sync` 并把被拒字段回落到默认（`src/client/settings.ts:129-170`）。
- 校验：`prefsOf()` 逐字段白名单收窄，非法值丢弃而不是采信（`settings.ts:71-89`）。
- 宿主侧：设置文档由 DSH settings 服务持久化，浏览器只保留一份 describe 镜像，`settings/document-updated` 与 `connection/reset` 时刷新（`@dsh/dsh-client-ui-settings/README.md:28,34-36,64`）。
- **页面级开关不写设置**：只有洞察页的 range Preset 写回（README_en.md:227）；其余 toggle 是视图内 state（`contextView.tsx:139-165`）。

---

## 8. 性能与体量控制（源码中的实际数字）

| 手段 | 数字/切点 | 证据 |
|---|---|---|
| 数据分片 `parts` | `requests/nodes/events/files/commands/spans` 各自按 `tail` 窗口拉 | `timelineSource.ts:17-33`（`FactPart`/`FactWindow`） |
| 明细上限 | steps 1500 · turns 300 · events 400 · surface nodes 2000 · archive 400 · file ops 400 · commands 400 | README_en.md:233；host `bounds` 配置 |
| 趋势柱窗口化 | >240 根才开窗；overscan 36、hysteresis 8；DOM 只留窗口 + 两侧 pad | `trendWindow.ts:1-36` |
| 列表窗口化 | `ROW_WINDOW_OVERSCAN=8`；`useSurfaceWindow` 默认 `estimate 32px / minCount 30 / overscan 240px / seed 12`；`useMeasuredWindow` >30 行才启用 | `useRowWindow.ts:12, 68-71, 128-136, 585-600` |
| 使用窗口化的视图 | ContextBrowser、CommandPanel、FileCard、PulseEvents/PulseTimeline、MissionPulse | `browser.tsx:283,338`、`commandPanel.tsx:186`、`fileCard.tsx:117`、`pulseEvents.tsx:162`、`pulseTimeline.tsx:138`、`missionPulse.tsx:233` |
| 折叠区懒挂载 | `useLazyMount()`：below-fold / Browser / Commands 挂载后才 `ensurePart` | `contextView.tsx:278-299`；`components/useLazyMount.tsx` |
| 大 session 降级 | DNA 惰性（>512 请求）、Adaptive 自动开、auto-overview 阈值 100/200/300/off | `contextView.tsx:98, 146-158`；`settings.ts:83` |
| SVG 节点上限 | AgentGraph 布局窗口 `GRAPH_OVERSCAN=320`、seed 60 | `agentGraph.tsx:57-59` |
| 缓存 | 见 §3.4（22 条策略，全部有 ttl/上限） | `retention.ts:19-42` |
| 预热/重连 | warm 900ms 去抖；projection rev 驱动、无重连全量 | `prefetch.ts:6-36`；`timelineSource.ts:659-701` |
| 滚动位置 | `viewScroll` 每 session 记录，上限 32 | `retention.ts:37`；`contextView.tsx:360-381` |

---

## 9. ZeoKeel / mygo 可替代性评级

评级口径：
- **A 直接可替代**：官方控件/原语现成，按 DESIGN.md 直接搭，不需要新增登记。
- **B 需自绘/组合**：mygo 有原语或画布，但需要写组合/自定义绘制，按 §7.2 登记（`Draw` 与 `ui/chart` 是官方 API，不算「新组件」）。
- **C 成本高**：需要跨模块/壳层改动，或与 ZeoKeel 现有信息架构冲突。

| # | UI 元素 / 交互 | 替代方案 | 评级 | 依据与成本 |
|---|---|---|---|---|
| 1 | 全局「上下文洞察页」（主栏整页） | 新增 screen（`contracts.Navigator`）+ 侧栏入口行 + 页面模块 | **C** | ZeoKeel 已有 screen 注册与 navigator（`internal/uiroot/navigator.go:5-47`），但侧栏是一级会话/工作区导航，新增并列入口要改壳层与 DESIGN 入口规格；页面本身用 `uikit.PageHeader`+`Card` 可中等成本 |
| 2 | 会话「Context」标签页 | `ui.TabsBase` 增加一个 tab → 一个 view 函数 | **A** | session 已有官方 tabs 架构（`internal/ui/session/view.go:31, 862`）；新增 tab 主要成本是内容 |
| 3 | 右栏「Context」页签 | `internal/ui/rightpanel` 的 view tabs 增加一页 | **A/B** | 右栏已是多标签官方 `TabsBase` 实现（`internal/ui/rightpanel/view.go:96,133`；`docs/INJECTION-POINTS.md:118`「右栏仅 session.panel.right 一个模块（多标签）」） |
| 4 | `/context` 弹窗 | `contracts.RegisterCommand`（slash 命令）+ `ui.Modal` | **A** | ZeoKeel 已有 slash 命令注册与 modal 弹窗范式（`internal/contracts/contracts.go:227`；`internal/ui/session/command_dialogs.go:81,144,199`、`commands_help.go:102`） |
| 5 | 插件设置卡片 | 设置页中的一个新 section（无插件系统） | **B/C** | ZeoKeel 设置页是固定页面 + SidebarItem 分组（`internal/uiroot/frame.go:1294-1317`）；没有「按 bundle 动态挂载配置卡」的插件配置槽，需要新增设置分组，且要定谁拥有（session/设置模块） |
| 6 | 回复行「Context」跳转按钮 | 消息元信息行内一个 `uikit.IconButton`/文本按钮 | **B** | 消息元信息行是既有富行站点（`docs/DESIGN.md` §7.1 登记），加一个动作按钮需在该行实现里加，并保持「一屏强调色预算」 |
| 7 | Context Stats / Token Stats / Timing Stats 卡 | `Card`/`uikit.Section` + `ui.Table`/`Row` + 文本 | **A** | 纯排版 + 数字格式化；官方 `Text`/`Badge`/`Row` 足够 |
| 8 | 当前上下文堆叠条（含斜纹 reserve） | `ui/chart` 的 stack + `Draw` 画斜纹；或用 `Row` 百分比宽度 | **A/B** | `chart` 有 scales/stacking（`mygo@v0.9.0/docs/ui/chart.md:1-40,223`）；斜纹用 `ui.Painter` 的 `StrokeDashed`/clip（`docs/ui/drawing.md:1-20`） |
| 9 | 趋势柱状图（横向滚动 + hover/pin + 事件标记） | `ui/chart.NewBand`/`NewLinear` + `ui.Scroll`；或自绘 `Draw` 在单个 box 里画柱 | **A/B** | `chart` 提供 band/linear/time scales、rects、tooltip（`chart.md:1-40,130-166`）；pin 状态用模块 state；横向滚动用 `ui.Scroll`/`List`（`docs/ui/scroll.md`）。mygo 无 DOM 量级节点，反而**更容易**：ZeoKeel 可只画窗口内柱子，滚动用 `Scroll` |
| 10 | Overview ribbon（整段压缩） | 一个 `ui.Box` + `Draw` 里按像素画分段矩形 | **A/B** | `ui.Painter.Fill`/`Path`（`drawing.md:1-20`）；无 SVG 依赖 |
| 11 | DNA 平铺（像素级渐变） | `ui.Painter` 按 band 区间 `Fill` 矩形；命中用鼠标 y 反算（与源码同构） | **B** | `Painter` 支持 rect/path/gradient（`drawing.md:5-9`）；无 DOM，天然没有 256 段动画上限问题；需自己实现 hover 命中与滚动容器内定位 |
| 12 | Donut 环 | `ui.Painter.Path`/`StrokePath` 画弧，或 `chart` 的 pie/donut | **A** | `chart.md:241`（Pies and donuts）；`drawing.md` 路径 |
| 13 | 8 周热力图（56 格 + 5 档） | `Row`/`Box` 网格 + `Draw` 或 `ui.GridView` | **A** | 纯方块 + hover/tooltip；`ui.Tooltip`/`Hovered`（`docs/ui/tooltip.md`） |
| 14 | Treemap（squarify） | 移植 `treemap.ts` 纯函数 + `Painter` 画矩形 | **B** | 数学是纯函数可直接移植；绘制用 `Draw`；文本省略规则需复刻 |
| 15 | Agent 网络图（节点卡 + 贝塞尔边 + 运行脉冲） | `Draw` 画边 + 官方卡/`Box` 节点；布局移植 `layoutForest` | **B** | `Painter.StrokePath`/`Path`（`drawing.md:1-20`）画三次贝塞尔；悬停点亮整链是纯状态；脉冲动画用 mygo 动画/重绘（需确认帧循环支持，未验证） |
| 16 | Context Browser（七类可折叠 + 逐元素展开 + 分页/窗口化） | `ui.Outline`/`ui.List` + `Collapsible` + `SearchField`；内容区用 `markdown`/`editor` 只读 | **A/B** | 官方集合件齐（`docs/UI-CONFORMANCE.md:17-21`）；`ListState.ScrollTo(row)` 支持跳转（`mygo@v0.9.0/docs/ui/list.md:258`）；细节多但都是既有控件 |
| 17 | 事件/文件/命令三个列表卡（筛选 chips + 搜索 + 行展开） | `ui.List` + `ListState`（Key/Selected/Filter）+ `Segmented`/`Tabs` 做 chips | **A** | 集合必须用官方 List（`AGENTS.md` 规则 6）；筛选 chips 用 `ui.Segmented`（`docs/ui/segmented.md`） |
| 18 | 会话卡墙（洞察页） | `ui.GridView` 或 `Row` 折行 + `Card` | **A/B** | `ui.GridView` 存在（`docs/ui/grid-view.md`）；虚拟化看 `GridView` 是否带窗口化（未验证） |
| 19 | hover 联动（图 ↔ 浏览器 ↔ 统计） | 模块实例 state + `Invalidate()` | **B** | mygo 没有 React 的响应式；需要「一个 state 结构 + 手动 Invalidate」，并注意 hover 高频重绘的性能（用户已能接受即时 tooltip） |
| 20 | 点击图柱跳 transcript / 打开右栏文件 | `ListState.ScrollTo(row, ui.Center)` + `rightpanel.PreviewFile` | **B** | 跳转：`list.md:258` 有 ScrollTo；但需要 seq→row 的映射（transcript 是虚拟化 List，需按 message id 查找，未验证现有 API 支持「按 key 滚动」） |
| 21 | 拖拽/DNA scrub/横向滚动图 | `ui.Scroll` + 鼠标坐标；`Split` 拖拽已有 | **B** | mygo 有鼠标事件与滚动（`docs/ui/scroll.md`）；无现成「图表 scrub」原语，需自写 |
| 22 | 骨架屏按最终尺寸占位 | `uikit` 空态 + `Spinner`/占位 Box | **A** | ZeoKeel 有 `uikit` 空态/错误行组合 |
| 23 | 8 语言 i18n | ZeoKeel 既有 locale 机制（`internal/uiroot/locale.go`）+ 词典 | **B/C** | 若要做 8 语言，词典量约 750 key ×8；ZeoKeel 现有 locale 是壳层资源，需要确认多语言覆盖策略（未验证） |
| 24 | 设置持久化（host settings + 乐观更新） | `contracts` 里 host settings 服务（ZeoKeel 已有 settings 持久化） | **B** | ZeoKeel 设置已有 host 持久化与页面（`internal/uiroot/frame.go` 设置面）；需要把「视图偏好」接到现有 settings 而不是新建一套 |
| 25 | 卸载即释放（按设置挂载/注销） | 模块内按 config 变化重建 builder 贡献 | **A** | ZeoKeel 模块以 `func(c *ui.Context)` 贡献，天然可条件构建；但「注销 slot」概念不同，需在 build 内分支 |
| 26 | 增量刷新（revision/unchanged） | ZeoKeel 服务契约里加 revision/unchanged 语义 | **B** | 属服务层设计，不是 UI 能力；对 Go 侧 RPC 成本低，但要统一契约 |

**汇总**：A 类 10 项（页面排版、集合、Do nut、热力图、tabs、modal、命令、右栏 tab、骨架屏、条件构建）；B 类 13 项（chart/Draw 类图表、treemap、网络图、hover 联动、跳转、i18n、设置接入、增量协议）；C 类 3 项（洞察页一级入口、插件设置卡片、8 语言全量）。

---

## 10. 给「自研 dsh-context 等价能力」的落地建议

1. **先做数据，不做像素**：dsh-context 的客户端一半代码是数据整形（`wire.ts` 639 行、`timelineSource.ts` 817 行、`fleetModel.ts` 893 行、`agentTree.ts` 651 行）。mygo 侧应把等价逻辑放进 `internal/modules/*` 的纯 Go 数据类型 + 聚合函数，UI 只做展示；这样 5 个入口共享同一份 model，避免重复。
2. **图表一律用 `ui/chart` + `ui.Painter`，不要引入图表库**：`chart` 已提供 scales（linear/log/symlog/band/time）、axes、marks、stacking、pie/donut、selection（`mygo@v0.9.0/docs/ui/chart.md`）；`Draw` 提供 path/gradient/clip（`drawing.md`）。dsh-context 用 DOM 是因为它只有 DOM；在 mygo 里 `Painter` 更直接，且天然满足「矩形/像素级渐变」。
3. **入口优先级**：`会话 tab`/`右栏页签`/`/context 弹窗` 三个是 ZeoKeel 已有壳层能力的自然延伸（A），应优先；`洞察页一级入口` 和 `插件设置卡片` 要动壳层与设置信息架构，建议二期（且需要先问设计 owner，DESIGN.md §2 场景表与 §7 自绘流程）。
4. **交互降级要明确**：hover 联动和 scrub 在 mygo 里是「高频重绘」问题，不是「能不能」问题。建议先做点击 pin + tooltip，hover 联动放二期；transcript 跳转需要先确认 `List` 按 key 定位的 API（未验证），否则退化为「打开右栏 Browser 的该 step」。
5. **性能预算**：直接抄 dsh-context 的数字即可——趋势图 >240 柱开窗、列表 >30 行窗口化、明细 1500/400/2000/400/400/400 上限、缓存 6MiB/detail 与 LRU+TTL。mygo 的 `ui.List` 本身虚拟化（AGENTS.md「transcript 早已是 ui.List 虚拟化」），所以列表类不需要自建窗口化，只有**自绘图表**需要窗口化。
6. **i18n 不要照抄 8 语言**：dsh-context 的 750 key ×8 是插件作者的国际化义务；ZeoKeel 应先确认产品语言范围，避免一次性 6000 条词典。

---

## 11. 未解问题 / 未验证清单

1. **`conversation.view` 的精确 contract**：README 只描述「会话级 View tab、偏好持久化、空会话不出现」（`@dsh/dsh-client-ui-conversation/README.md:85,91`），未逐字给出 `id/order/label/locale` 字段；dsh-context 的使用方式（`index.ts:95-96`）是实证，但字段语义（如 `order` 的冲突规则）未验证。
2. **`conversation.chat.assistant-actions` 槽的存在与 props**：由 `JUMP_SLOT` 字面量反推（`index.ts:67`），未在本地 DSH README 中找到该 slot 的文档章节。
3. **`sidebar.panellist` 的 `label` 是否接受 i18n 函数**：dsh-context 传 `label: () => t('ov.title')`（`insightPage.ts:28`）；README 说「string or locale-aware label」（`@dsh/dsh-client-ui-sidebar/README.md:38`），实际类型未验证。
4. **`plugins.bundle.config` 的表单保存/脏检查语义**：只在 ui-plugin-manager README:73,131,133 有描述；「离开页面丢弃暂存编辑」「只有 save 写」的细节是否影响自研设置卡设计，未验证。
5. **`trendOverview.tsx` 的 ribbon 具体 SVG 结构**（是否单 path/多 path、是否用 mask）未逐行读取。
6. **ZeoKeel `ui.List` 是否支持「按 key 滚动到行」**：`ScrollTo(row, align)` 接受行号（`list.md:258`），是否有 key→row 的辅助未验证；这决定「点击趋势柱跳到 transcript」的成本。
7. **mygo 帧循环下高频 hover 重绘的实际开销**：未做基准；`docs/ui/performance.md` 有相关说明但未读。
8. **DSH `useProjection` 的 narrow 失败语义**：`projectionOf` 返回 `null` 后各卡片如何区分「还没到」与「宿主不支持」——dsh-context 用 `unsupportedOf`/`UpgradeGate` 兜底（`contextView.tsx:917`），细节未验证。

---

## 12. 证据索引（关键文件）

| 文件 | 行数 | 作用 |
|---|---|---|
| `src/client/index.ts` | 168 | 全部入口注册与生命周期编排 |
| `src/client/insightPage.ts` | 32 | 洞察页 main + panellist |
| `src/client/sidebar.ts` | 56 | 右栏 tab type + body/title 槽 |
| `src/client/placement.ts` | 55 | tab/sidebar 入口按设置挂载 |
| `src/client/fleetTab.ts` | 23 | Fleet tab 门控 |
| `src/client/command.ts` | 42 | `/context` 命令源 |
| `src/client/settings.ts` | 171 | 设置 store / 白名单 / 乐观写 |
| `src/client/timelineSource.ts` | 817 | 会话明细：projection→facts、防抖、catch-up、分片 |
| `src/client/overviewSource.ts` | 427 | 洞察页分片 + 30s 轮询 + backfill 退避 |
| `src/client/fleetModel.ts` | 893 | 家族模型（preview/history/full 三档） |
| `src/client/agentTree.ts` | 651 | 家族树与图布局纯函数 |
| `src/client/route.ts` | 87 | POST 信封 + revision/unchanged |
| `src/client/boundedCache.ts` | 220 | LRU/字节/权重/TTL 上限缓存（被 retention.ts 统一实例化） |
| `src/client/retention.ts` | 86 | 22 条缓存策略表 |
| `src/client/components/contextView.tsx` | 933 | 会话看板整棵树 |
| `src/client/components/overviewPanel.tsx` | 489 | 洞察页 |
| `src/client/components/browser.tsx` | 994 | Context Browser |
| `src/client/components/useRowWindow.ts` | 680 | 窗口化 hooks |
| `src/client/styles/base.css` | ~110 | 卡片/控件 token 用法 |
| `src/client/styles/overview.css` | — | 4→2→1 列容器查询 |
