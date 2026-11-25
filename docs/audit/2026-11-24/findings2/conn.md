# 能力页（技能 / MCP 连接器）功能可用性深审

- 覆盖流程：**19** 个；截图：**16** 张（`shots/r2-conn-*.png`）；记录的操作：**30 次交互（点击 / 输入 / 保存）+ 11 次 WS 方法探针**
- 实例：`http://127.0.0.1:9878`（data-conn，brief 指定）+ 自建 4 个可写实例做 A/B（`9879` data-conn2 healthy、`9880` data-conn3 failed、`9881` data-conn4 运行时写入+重启、`9888` data-conn5 过期 scope；9883 被占用 → 端口协商到 9888）
- 关键结论先说：**能力页是一个纯只读列表，且它读到的状态字段还是错的。** 连接器「怎么加」在 web 端**不存在**（不只是按钮缺失：协议方法、读模型语义、运行时重载三处都缺）。`docs/TRANSPORT.md §5.2.4` 里写的 **19 个**扩展方法（plugins./mcp./skills./memory.）在 `internal/gateway` 里**一个都没实现**（`contract.json` 52 个方法里 0 个扩展方法），因此这不是"UI 偷懒"，是整条链路没接线。

## 0. 计数与优先级

| 类型 | 条数 | 条目 |
|---|---|---|
| **M 缺失** | **5** | C-01 添加/编辑/删除连接器无入口、C-02 19 个协议方法全缺、C-06 技能只读、C-10 空态无下一步、C-11 无搜索/排序 |
| **F 假入口** | **0** | 能力页没有可点控件，不存在"按了没反应"；唯一"看着能点"的表头排序见 C-11 |
| **S 静默失败** | **4** | C-04 状态永远「空闲」（失败/成功都看不出来）、C-05 非法 JSON「已保存」+ 重启清空、C-09 技能警告与遮蔽无声、C-12 过期 scope 行被删 |
| **U 不可恢复** | **1** | C-15 连接器失败态无重试/无删除/无编辑 |
| **H 半接线** | **3** | C-03 写 `mcp.servers` 不生效（无 reload 触发）、C-07 `skills.dirs`/`mcp.disabled*` 写全局键但读 scoped 键、C-14 单次取数无刷新/无推送 |
| 非五类 | 2 | C-08 绝对路径撑破列宽（视觉/信息）、C-13 端口静默协商（运维陷阱） |

**最该先修的 3 条**
1. **C-02 + C-01**——把 `§5.2.4` 的方法真正实现（先 `mcp.reload`/`mcp.tools`/`mcp.server.probe`/`skills.snapshot`，并进 `initialize.capabilities`），再给连接器 tab 一个「添加连接器」。这两条不修，第 2、3 条都无处落地。
2. **C-04**——状态列语义：`'connected'` vs `"已连接"` 的比较让**连上也显示空闲、失败也显示空闲**，把 `lastError` 整条丢掉了。读模型改结构化 `{state, tools, lastError}`，UI 按 `state` 渲染。
3. **C-03 + C-05 + C-07**——设置页三处「假接线」：`mcp.servers` 写进去不生效也不提示、非法 JSON 照存、`skills.dirs`/`mcp.disabled*` 写到后端不读的键。最小止血：`settings.set` 命中这些键时 `go mcpreload.Trigger(ctx)` + 服务端 schema 校验 + 写 scoped 键；做不到就把字段从设置页摘掉，别让它看起来能用。

**"要让它真的可用"的最小清单**

| 层 | 最小项 |
|---|---|
| 协议 | `capabilities.snapshot`（skills+connectors+warnings）+ `capabilities.subscribe/delta`；`mcp.reload`、`mcp.tools`、`mcp.tool.enable`、`mcp.server.add/update/remove/enable`、`mcp.server.probe`、`mcp.auth.status/prepare/wait/cancel/revoke`；`skill.get/create/update/delete`；`initialize.capabilities` 增 `mcp`/`skills` 能力位；同步 `TRANSPORT §5.1` + `contract.json` + `TestTransport*` |
| API（网关/后端） | `settings.set` 命中 `mcp.*`/`skills.*` → 触发 `mcpreload.Trigger`（函数已在，缺调用者）+ 广播 delta；读模型状态结构化（`state/lastError`）而非本地化字符串；解析失败保留上次成功行；`Warnings()` 进读模型；scoped 行失效改 `orphaned` 不删除 |
| UI | 连接器：主按钮「添加连接器」+ 表单（name/transport/url 或 command+args+env/headers/auth/超时）+「测试连接」+ 行内启停/删除/状态+失败原因+重试 + 工具抽屉；技能：搜索、来源显示根名、行点击预览 `SKILL.md`、启停、删除、新建、目录用 scoped 键；空态带主行动；页头一个「刷新」 |
| 设置页 | `mcp.servers` 换结构化编辑器（或至少 JSON schema 校验 + 字段错误 + "需重启"提示）；`skills.dirs`/`mcp.disabled*` 改 scoped 键 |

---

## 流程总表

| # | 流程 | 结论 | 类型 | 证据 |
|---|---|---|---|---|
| 1 | `/capabilities` 首次加载（技能 tab） | 可用：`GET /api/capabilities 200 {"skills":[],"connectors":[]}`，渲染 `技能 0 / 连接器 0` | — | `out2/r2-conn-b.json`；`r2-conn-01-skills-empty.png` |
| 2 | 切到「连接器」tab | 可用：URL 变 `?tab=connectors`（`replace:true`，不污染历史），**无任何 /api 请求**（数据一次性加载） | — | `afterConnClickApi: []`（`out2/r2-conn-a.json`） |
| 3 | 刷新后 tab 是否保持 | 保持：重载后 `连接器 0` 仍选中 | — | `r2-conn-03-after-reload.png`；`afterReloadSelected` |
| 4 | 未知 tab 参数 `?tab=xxx` | 静默回落技能 tab，无提示（可接受） | — | `capabilities-screen.tsx:11` |
| 5 | 放一个技能到 `<workspace>/.agents/skills/<name>/SKILL.md` | 可用：无需重启，刷新页面即出现（`skills` 每次调用重扫根） | — | `GET /api/capabilities` 立刻返回该技能 |
| 6 | 技能「查看详情 / 打开 SKILL.md」 | **无入口**：行不可点、不可聚焦（`tabIndex=-1`）、无抽屉/弹窗 | **M** | 点行后 `modal:0 drawer:0`，URL 不变（`out2/r2-conn-c.json`） |
| 7 | 技能「启用 / 禁用」 | **无入口**（只有设置里一坨 `skills.disabled` JSON 数组） | **M** | `capabilities-screen.tsx:56-71` 只有 3 列 |
| 8 | 技能「删除 / 新建 / 编辑」 | **无入口**（协议 `skill.create/update/delete` 也不存在） | **M** | 探针 `-32601 unknown method: skill.create` |
| 9 | 技能搜索 / 筛选 / 排序 | **无**：无搜索框；点表头无反应（`sortable:false ariaSort:null cursor:auto`，顺序不变） | **M** | `out2/measure2.json` |
| 10 | 设置 → 数据与隐私 →「技能目录」写额外目录并保存 | **写进全局 `skills.dirs`，后端只读 `skills.dirs.<wsId>` → 永不生效**（实测同一路径：全局键 0 个技能，scoped 键立刻 1 个） | **H** | 见 C-07 |
| 11 | 连接器「添加 / 编辑 / 删除 / 重连 / 鉴权 / 看工具」 | **全部无入口**：整页只有全局「新建会话」和一个无文本的侧栏折叠图标按钮 | **M** | `buttons:["新建会话",""]`（census） |
| 12 | 在设置「MCP 服务器」JSON 里写一条**合法**行 → 保存 | 请求成功（`settings.set` rev 2→3）、`settings.json` 落盘、界面「已保存」，**连接器 tab 仍 0**、无任何提示 | **H** | `out2/probe2.log`、`out2/r2-conn-uiwrite.json` |
| 13 | 同上，写**非法** JSON → 保存 | 「已保存」，无字段校验；**重启后连接器全丢**，只有一行日志 | **S** | `r2-conn-12-invalid-json-saved.png`；日志 `[toolkit] parse mcp.servers: invalid character 'h'` |
| 14 | 真正重启进程（同一 dataDir） | **行出现并连上**：`{"id":"local-mcp","tools":2,"status":"已连接"}`，日志 `[toolkit] mcp "local-mcp" connected: 2 tool(s)` | — | 9881 实测（`out2/`；见 C-03） |
| 15 | 连接器状态列 | 健康服务器（2 工具）显示**「空闲」**；坏服务器（连接失败）也显示**「空闲」**，错误文本被丢弃 | **S/H** | `r2-conn-08/09` 两张图逐像素同构；`capabilities-screen.tsx:95-97` vs `plugins.go:211` |
| 16 | 连接器故障后「恢复」 | 无重试 / 无删除 / 无重启入口：进入失败态后**只能手工改 `settings.json`** | **U** | C-15；行上无任何操作 |
| 17 | 重复/被遮蔽技能 | 静默只留第一个根的那条；`SkillWarnings` 有接口但**没有任何出口**（读模型与 UI 都不显示） | **S** | `r2-conn-06`（`.zeokeel/skills/r2-conn-probe` 未出现、无警告） |
| 18 | 过期 workspace 的 scoped 连接器行 | 启动时**被删除并改写 `settings.json`**（`[]`），仅日志一行 | **S** | 9888 实测：`drop MCP connector "stale-scope" scoped to deleted workspace` |
| 19 | 协议层：`§5.2.4` 扩展方法 | **19 个文档化方法全部 `-32601`**；`initialize.result.capabilities` 里也没有 `mcp`/`skills` 能力位 | **M** | `out2/probe1.log`；`contract.json` 52/0 |

> **F（假入口）本轮 0 条**——原因是能力页**根本没有可点的控件**：不存在"按了没反应"的按钮，缺失全部表现为 M/H。唯一"看着能点"的表头（排序）在 antd 里是纯视觉，见流程 9。

---

## 发现

### C-01 连接器 tab 没有任何写入口 [P0]（M）
- 操作：`/capabilities?tab=connectors`（空态与有数据两种）逐一清点可交互元素；尝试点空白、点行、点表头、Tab 遍历。
- 网络：**整页生命周期内只有 `GET /api/capabilities` 一次**（`out2/r2-conn-b.json` 全量请求表：`/capabilities`、`/assets/index-*.js`、`/assets/index-*.css`、`/api/capabilities`，无其它）。点行/表头后 API 调用数 = 0。
- DOM/截图：可点元素只有 2 个——跳转链接 `#zk-main` 与全局「新建会话」；表格 4 列（名称/传输/工具数/状态）无操作列；行 `cursor:auto`、`tabIndex=-1`。见 `r2-conn-07-connectors-empty.png`、`r2-conn-08-connectors-healthy.png`。
- 代码：`web/src/features/capabilities/capabilities-screen.tsx:74-103`（连接器 tab 只有 4 列 `Table`，`pagination={false}`，无 `rowSelection`/`onRow`/`actions`）；`web/src/api/read-models.ts:74-77` 是**唯一**能力 API，且只有 `GET`。
- 对照（DSH）：`dsh-mcp-manager` 把服务器集合做成 `static Config = z.object({ servers: z.dict(...).volatile() })`（`src/index.ts:196-198`），Settings 直接渲染成表单；`ctx.remote.mcpManager` 提供 `watch/restart/probe`（`src/index.ts:313,347,363`）；`dsh-client-ui-plugin-manager` 的「添加插件」是主按钮 + 独立箭头 + 安装对话框（README.zh.md:50-59）。
- 修法：连接器 tab 页头放**唯一** primary 按钮「添加连接器」（AGENTS.md §3.7 强调色预算：一屏 ≤1 primary）；行操作 = 启停开关 + 删除（`ConfirmDanger`）+ 工具抽屉。

### C-02 `§5.2.4` 的 19 个扩展方法在网关里一个都没有 [P0]（M，根因）
- 操作：登录取 cookie 后开 `/ws`，`initialize` → `ready`，逐一调用扩展方法（自建探测程序 `main.go`，见「复现」）。
- 网络（WS 帧原文）：
  ```
  POST /auth -> 204
  GET  /ws   -> 101 Switching Protocols
  plugins.snapshot   {"error":{"code":-32601,"message":"unknown method: plugins.snapshot"}}
  plugins.refresh    -32601 · mcp.reload -32601 · mcp.tools -32601 · mcp.tool.enable -32601
  mcp.auth.status    -32601 · skills.snapshot -32601 · skill.get -32601
  skill.create       -32601 · skill.delete -32601
  settings.snapshot  {"result":{"epoch":"...","rev":2,"rows":{...}}}   ← 只有 settings.* 是真的
  ```
- 代码：`internal/gateway/methods.go:213-376` 的 `methodTable()`（52 个方法）里没有任何 `mcp.*`/`skills.*`/`skill.*`/`plugins.*`/`memory.*`；`dispatch` 未命中即 `errMethodNotFound`（`:445-467`）。机器证据：`internal/gateway/contract.json` 的 `params` 有 52 个键、扩展方法 **0** 个；`capabilities.static` 16 项、`conditional` 6 项，**没有** `mcp`/`skills`。而 `docs/TRANSPORT.md:860-879` 明确列了 `plugins.snapshot/refresh`、`mcp.reload/tools/tool.enable/auth.status|prepare|wait|cancel|revoke`、`skills.snapshot`、`skill.get/create/update/delete`、`memory.list/get/write/delete`。
- 影响链：客户端拿不到方法 → 不可能做「重载 / 工具清单 / 逐工具启停 / OAuth」；`initialize.capabilities` 没有能力位 → 客户端连"这个功能存在吗"都无法判断（只能猜，或按 AGENTS 所说"试错后再禁用"）。
- 对照（DSH）：mcp-manager 的 Remote 面 `watch`（stream，推完整列表+状态）、`restart(name)`、`probe(name)`，写配置走 Settings 的 ConfigEditor 而不是管理服务本身（README.zh.md「Remote 操作」）。DSH 明确把"写"和"观测"分开：`watch` 是流，`probe` 不动线上实例。
- 修法：先实现 4 个就能让页面"活"起来：`capabilities.snapshot`（或复用 `plugins.snapshot`+`skills.snapshot`）、`mcp.reload`、`mcp.tools`、`mcp.probe`；同时把 `mcp`/`skills` 加进 `gatewayCapabilities()` 的 conditional 列表，客户端才敢渲染入口。

### C-03 设置里的「MCP 服务器」是唯一写入口，写完**不生效**（无重载路径，`mcpreload.Trigger` 是死代码）[P0]（H）
- 操作（浏览器，9878）：设置 → 数据与隐私 →「MCP 服务器」textarea 填 `[{"name":"ui-mcp","transport":"http","url":"http://127.0.0.1:9899/mcp"}]` → 保存 → 再进 `/capabilities?tab=connectors`。
- 网络：保存走 WS `settings.set`（`settings.snapshot` rev 2→3 可证），`data-conn/settings.json` 变为该行；随后 `GET /api/capabilities` 仍 `{"skills":[...],"connectors":[]}`（1.5s 后复测同）。
- DOM/截图：设置页 `.zk-notice` = 「已保存」，保存按钮回到 disabled；连接器 tab 仍是「连接器 0 / 没有连接器。」（`r2-conn-15`、`r2-conn-16`）。**没有任何提示说"需要重启"**。
- 代码（根因三处）：
  1. 读模型是**激活期快照**：`internal/gateway/read_models.go:62-75` 读 `contracts.KeyPlugins` → `pluginsService.MCP()`（`internal/modules/toolkit/plugins.go:36-41`，注释即 "snapshots the entries built at activation"），没有订阅、没有重读。
  2. 合并只发生在 `newMCPController(...).Load()`（`internal/modules/toolkit/toolkit.go:277-292`）→ `loadMCPServers`（`internal/modules/toolkit/mcp_control.go:634-671`）→ `mergeMCPServerConfigs`（`mcp_settings.go:168-171`）。
  3. 热重载的**唯一**触发者是 `internal/lib/mcpreload/mcpreload.go:21-31` 的 `Trigger()`——全仓 `grep "mcpreload\."` 结果：**0 个调用者**；`mcp.reload` 又没实现（C-02）。因此 `Reload`（`mcp_control.go:91-93`）永远没人调。
- 实测对照（把"重启是不是就够"一次问清）：
  - 9882/data-conn2：`settings.json` **预置**行 → 启动即 `{"name":"r2-fake","tools":2,"status":"已连接"}`，日志 `[mcp/manager] server "r2-fake" connected: 7 tool(s)`、`[toolkit] mcp "r2-fake" connected: 2 tool(s), protocol 2025-06-18`。
  - 9881/data-conn4：**运行时** `settings.set` 写入 → `/api/capabilities` 仍 0；**真重启**后 → `{"id":"local-mcp","tools":2,"status":"已连接"}`，且 `settings.json` 被 `reconcileMCPServerRows` 自动补上 `workspaceId`（`mcp_settings.go:201-207`）。
  - 结论：**后端能连（协议走通 initialize → notifications/initialized → tools/list，见 fixture 日志），缺的只有「不重启就重载」+ 入口**。
- 对照（DSH）：配置变化即对账，无需重启——"每次提交的配置变更都会对账编队：新的启用条目挂载，被移除的条目卸载，`enabled:false` 卸载实例但保留条目，其余任何字段变化只重建那一个实例"（`dsh-mcp-manager/README.zh.md`「管理配置的服务器」）。
- 修法：`settings.set` 命中 `mcp.servers` / `mcp.disabled*` / `skills.dirs*` 时 `go mcpreload.Trigger(ctx)`（函数已存在，接上即可），成功后推 `capabilities.delta`；在此之前，UI 的保存提示必须改成"需要重启网关才生效"——现在说的是「已保存」。

### C-04 状态列永远显示「空闲」：前端比较 `'connected'`，后端给的是本地化中文 [P0]（S/H）
- 操作：两台实例对打——9879 的服务器**连上了**（`tools:2`）、9880 的服务器**连不上**（`connect: connection refused`）。
- 网络：
  - 9879 → `connectors:[{"id":"r2-fake","transport":"http","tools":2,"status":"已连接"}]`
  - 9880 → `connectors:[{"id":"r2-dead","transport":"http","tools":0,"status":"连接失败: mcp: initialize: mcp: http request: Post \"http://127.0.0.1:9898/mcp\": dial tcp 127.0.0.1:9898: connect: connection refused"}]`
- DOM/截图：两台的「状态」单元格**都是「空闲」**，颜色 `rgba(0,0,0,0.45)`（secondary），无 tooltip、无错误文本（`title:""`）；`r2-conn-08-connectors-healthy.png` 与 `r2-conn-09-connectors-failed.png` 除名称外完全一致。
- 代码：`capabilities-screen.tsx:95-97` `value === 'connected' ? t(connected) : t(idle)`；后端 `internal/modules/toolkit/plugins.go:210-213` `statusConnected = "已连接"`、`statusConnectFailed = "连接失败: "`（硬编码中文，不经 i18n）。
- 对照（DSH）：状态是**结构化联合类型**而不是字符串：`McpManagedServerStatus = McpConnectionStatus | {state:'disabled'} | {state:'failed', reason:'load', lastError} | {state:'failed', reason:'cleanup', result}`（`dsh-mcp-manager/src/types.ts:17-21`），列表视图 `{name, configured, status}`（`:23-31`）；UI 按 `state` 渲染，失败必带 `lastError`。
- 修法（一行能止血，但根要改协议）：读模型改成 `{state, tools, lastError}`；UI `switch(state)`。止血版：把比较改成 `value.startsWith('已连接')` 是不够的——英文界面同样错，必须走结构化。

### C-05 非法 JSON 被「已保存」接受；重启后连接器**全丢**，只有日志知道 [P0]（S）
- 操作：同一 textarea 填 `this is not json at all {{{` → 保存 → 重启网关。
- 网络/DOM：保存后 `.zk-notice` = 「已保存」，保存按钮 disabled（说明前端认为写成功）；`settings.json` 里确实是这串垃圾。重启用 `/api/capabilities` → `connectors:[]`。
- 日志（唯一痕迹）：`21:07:43 [toolkit] parse mcp.servers: invalid character 'h' in literal true (expecting 'r')`。
- 代码：`web/src/features/settings/settings-screen.tsx:239-254`（`save()` 直接 `setSetting`，无任何校验）；`internal/modules/toolkit/mcp_settings.go:46-50`（解析失败 → `return merged`，即**丢弃全部**用户连接器）。
- 对照（DSH）：写入处校验并点名字段——"坏条目——缺 `command`、带跨传输字段、模板写错，或使用了插件已注册的名字——在写入处被拒绝，因此文档绝不会存下管理器无法执行的服务器"；实现是 `validateEntries(config.servers.get())`（`dsh-mcp-manager/src/index.ts:222`）。
- 修法：`settings.set` 对 `mcp.servers` 做服务端 schema 校验（返回 `-32602 invalid params` + 字段名），或前端改成结构化表单；解析失败时**保留原行**而不是清空（`mergeMCPServers` 的 `return merged` 应改为保留上次成功解析的行 + 报错）。

### C-06 技能 tab 同样只读：无详情 / 无启停 / 无删除 / 无新增 [P1]（M）
- 操作：技能 tab 两行数据下点行、Tab 遍历、找任何按钮。
- 网络：点行后 0 请求、无 modal/drawer；`skill.get/create/update/delete` 全部 `-32601`。
- DOM：`buttons:["新建会话",""]`（后者是无文本的侧栏折叠图标按钮）；行不可聚焦；`capabilities-screen.tsx:56-71` 三列无操作。
- 对照（DSH）：`dsh-client-ui-skill` 的 `/name` 引用"点击已知 skill 会在右侧栏打开提供方给出的 `SKILL.md` 路径"（README.zh.md「source 提供什么」），候选来自 `skills/list` Remote；ZeoKeel 侧 `skill.get` + 文件预览能力都有（`/api/files/*`），只是没接。
- 修法：行点击 → 右检查器/Markdown 预览 `SKILL.md`；操作列 = 启停（写 `skills.disabled`）+ 删除（`ConfirmDanger`）；页头 + 「新建技能」。

### C-07 设置「技能目录」写的是全局 `skills.dirs`，后端读的是 `skills.dirs.<wsId>` → 永无效果 [P1]（H）
- 操作（9881，workspace 已打开）：① `settings.set skills.dirs=/…/extraskills` → `/api/capabilities` 技能 **0**；② `settings.set skills.dirs.ws-1795525422968987426=同路径` → **立刻**出现 `dir-skill`（同一个 SKILL.md）。
- 代码：写端 `web/src/features/settings/ia.ts`（`json(K.skillsDirs, 'settings.ia.skillCatalog')`，键见 `web/src/design/settings-keys.ts:74`）= 常量 `skills.dirs`；读端 `internal/modules/skills/skills.go:308` `s.settings.Get(contracts.SkillsDirsSettingKey(workspaceID), "")`，而 `contracts.SkillsDirsSettingKey` 在有 workspace 时返回 `skills.dirs.<id>`（`internal/contracts/skills.go:37-44`）。
- 截图：`r2-conn-10-settings-ia-tools.png`（字段「技能目录 / JSON 字符串数组。」，我 probe 写入的全局值被正常显示 → 用户看不出它没被读）。
- 同类问题：`mcp.disabledServers` / `mcp.disabledTools` 也写全局键，读端用 `MCPDisabledServersSettingKey(workspaceID)`（`internal/contracts/plugins.go:318-326`、`mcp_control.go:663-665`）→ 在默认"有 workspace"的形态下**开关永不生效**。
- 修法：设置页按当前 workspace 写 scoped 键（`settings.snapshot` 里有 `workspaces.json` 的 `currentId`，客户端已知道）；或后端 `Get` 时两者都读（scoped 优先）。

### C-08 技能「来源」渲染绝对路径，撑破列宽（980px 下说明列被压成一字一行）[P1]（—，视觉/信息，不属五类）
- 操作：1440px 与 980px 各截一次；读取排名/列宽/行高。
- DOM：`来源` 列声明 `width:120`，实际单元格 **453px**（tag 宽 418–437px，无省略、无 `title`）；980px 视口下表格宽 668px，说明列被挤到约 1 字/行，行高 61px→ 实际视觉约 450px，tag 溢出到表格右边线之外。
- 代码：`capabilities-screen.tsx:64-69`（`render: value => <Tag>{value}</Tag>`）+ `internal/gateway/read_models.go:86-92`（`skillDirectory` = `filepath.Dir(path)` 绝对路径）。
- 截图：`r2-conn-06-skills-with-rows.png`（1440）、`r2-conn-17-skills-980.png`（980）。
- 修法：返回根名（`workspace` / `user` / `extra:dir`）而不是全路径，或 `Typography.Text ellipsis={{tooltip}}` + `max-width`；`说明` 列本来就是后端截断到 200 字的摘要（rig `skills.go:260`），别让它承担布局。

### C-09 技能发现警告无出口，重复技能静默丢弃 [P1]（S）
- 操作：同名技能同时放 `workspace/skills/` 与 `workspace/.zeokeel/skills/`。
- 网络：`/api/capabilities` 只返回第一条，**无 warnings 字段**；`contracts.SkillWarnings`（`internal/contracts/skills.go:26-29`）从未被 `handleCapabilities` 调用（`read_models.go:50-60` 只调 `List`）。
- 影响：用户以为技能生效，其实被另一个根的旧版本遮蔽；扫描上限（`MaxListedSkills=500`）被命中时同样无声。
- 修法：读模型加 `warnings: string[]`（`SkillsService` 已实现 `Warnings()`），技能 tab 顶部用 `Alert type="warning"`。

### C-10 空态不告诉你下一步 [P2]（M）
- 操作：干净实例打开两个 tab。
- DOM：`没有连接器。` / `没有加载技能。`（antd `Empty` 默认插画 + 一句否定句）。页头说明「装了什么技能、连了哪些 MCP 服务器。」，与"怎么装"无关。
- 代码：`capabilities-screen.tsx:54,79`；i18n `screen.capabilities.noConnectors`。
- 对照（DSH）：插件页空列表仍带「添加插件」主按钮与「发现」标签页（`dsh-client-ui-plugin-manager/README.zh.md:48-59`、`dsh-client-ui-plugin-market`「没有索引源时，该标签页显示空状态，其中的**添加索引源**按钮…」）。
- 修法：空态 = 一句"还没有连接器"+ primary「添加连接器」+ 次按钮「从模板添加 / 查看文档」。

### C-11 无搜索 / 筛选 / 排序 / 分页；表头视觉上像可排序实则不可 [P2]（M）
- 证据：`out2/measure2.json`（三列表头 `sortable:false, ariaSort:null, cursor:auto`；点击后行顺序不变）；`pagination={false}`（`capabilities-screen.tsx:59,84`）。
- 对照（DSH）：插件页行列表超过 10 行带搜索输入（按标题/描述/行 id/模块名），「发现」有搜索+标签+排序+刷新（`README.zh.md:85`、`plugin-market` README）。
- 修法：技能/连接器各加一个输入框（本地过滤即可），行数 >20 再上分页。

### C-12 过期 workspace 的 scoped 连接器行被**删除**（数据丢失，仅日志）[P2]（S）
- 操作：`settings.json` 预置 `{"name":"stale-scope","workspaceId":"ws-does-not-exist",...}` → 启动。
- 网络/文件：`/api/capabilities` → `connectors:[]`；`settings.json` 的 `mcp.servers` 被改写为 `[]`；日志 `[toolkit] drop MCP connector "stale-scope" scoped to deleted workspace "ws-does-not-exist"`。
- 代码：`internal/modules/toolkit/mcp_settings.go:194-208`。
- 影响：跨实例/复制数据目录、或工作区被删后，用户配置**不可恢复地消失**，UI 从不提示。这一条直接解释了"手工加行、重启后仍是 0"的一类现场：若 `workspaceId` 抄错（例如抄了另一个数据目录的 id），行会被判为"已删工作区"而清掉。
- 修法：不删除——`state: 'orphaned'` 保留并让 UI 显示"该连接器属于已删除的工作区，是否迁移到当前工作区？"。

### C-13 端口协商静默换端口：所谓"重启没变化"极易是连到了旧进程 [P2]（—，运维/可观测）
- 实测：`--port 9883`（被别的进程占用）→ 网关**自动**监听 `9888` 并改写 `gateway.json`（`{"port":9888,"epoch":"d4cd38efbbcef438"}`）；`internal/gateway/port.go:20-27,portTries=10`（连续试 10 个端口，再让内核挑）。
- 另一个陷阱：同一 dataDir 二次启动 → 打印一行 `ZeoKeel: another instance is already running at http://127.0.0.1:9879` 并 **exit 0**（看起来"重启成功"）。
- relevance：lead 的"写入后重启仍 0、日志无 MCP 行" 的两个可验证解释——(a) 重启命令被 lock 挡下、旧进程仍在服务（浏览器还是旧快照）；(b) 实际起在新端口、而人看的是旧端口页面。判据：`gateway.json` 的 `epoch`/`pid`/`startedAt` 是否变化、日志是否有新的 `gateway: listening on …`、以及 `logs/zeokeel.log` 里是否出现 `[mcp/manager] server … connected` 或 `[toolkit] parse mcp.servers`。
- 建议：serve 启动时若请求端口被占，日志与 `gateway.json` 之外再往 UI 推一条"端口已变更"通知；`--port` 冲突时默认失败而非静默换端口（或加 `--strict-port`）。

### C-14 页面只在挂载时取一次数据，无刷新入口、无推送 [P2]（H，半接线）
- 证据：`useEffect(..., [])` 单次 `loadCapabilities()`（`capabilities-screen.tsx:15-28`）；WS 上没有任何 capabilities 域（`contract.json` 无该域）。
- 影响：用户按文档把技能目录放好、或改了设置，页面不会自己变——必须 F5。技能侧至少还能靠 F5 看到（每次 `List` 重扫根），连接器侧连 F5 也没用（C-03）。

### C-15 连接器进入「失败」态后没有任何出口 [P0]（U）
- 操作：9880 实例（`mcp.servers` 指向一个没人监听的端口）打开连接器 tab，尝试：点行、找刷新/重试/删除/编辑、Tab 遍历、右键。
- 网络：0 请求；`/api/capabilities` 里那一行的 `status` 是 `"连接失败: …connection refused"`，但 UI 只渲染「空闲」（C-04），**失败本身都看不见**。
- DOM/截图：行上无操作列、无抽屉、无弹窗；`r2-conn-09-connectors-failed.png` 与健康态截图无法区分。
- 代码：`capabilities-screen.tsx:81-102`（无 `actions`/`onRow`）；即使有，`mcp.reload`/`mcp.server.remove` 也不存在（C-02）。
- 对照（DSH）：`restart(name)` 用于"重连预算耗尽"后恢复，`probe(name)` 可在启用前测试；失败带 `lastError`，removal 与 `enabled:false` 是两条明确路径（`dsh-mcp-manager` README「观察与恢复」）。
- 修法：状态列给「重试」（调 `mcp.server.probe` + `restart`）、行尾给「删除」；失败原因必须可读（不要丢 `lastError`）。

---

## 「用户想接一个 MCP server」需要做什么（逐步标注哪一步不存在）

以当前 master（cd29938）为准，唯一可行路径与每一步的真实状态：

| 步 | 用户要做的 | 现状 | 缺什么 |
|---|---|---|---|
| 1 | 打开「能力 → 连接器」，点「添加」 | **✗ 不存在**：整页无按钮（C-01） | UI |
| 2 | 退而求其次：设置 → 数据与隐私 → 找到「MCP 服务器」 | **存在但不可发现**：藏在「数据与隐私」页底部「工具与技能」段，标签只写「JSON 数组：{name, transport, command\|url}。」，无链接/无引导 | UI（可发现性） |
| 3 | 手拼 JSON（name/transport/url 或 command+args+env、headers、auth、timeout） | **无表单**：纯 textarea，无 schema、无示例、无必填提示、无「测试连接」（协议也没有 `probe`） | UI + API 封装 + 协议 |
| 4 | 保存 | **能保存**（`settings.set` 成功，「已保存」）——但**没有任何校验**，也不告诉你需重启（C-03/C-05） | 校验 + 诚实提示 |
| 5 | 回连接器 tab 看它出现 | **✗ 不出现**（读模型是激活期快照，无 reload） | API（reload）+ 协议（`mcp.reload` / `capabilities.delta`） |
| 6 | 看它连上没、可用工具、是否要鉴权 | **✗ 无入口**（`mcp.tools` / `mcp.auth.*` 全是 -32601；状态列还是错的） | 协议 + UI |
| 7 | 唯一真正生效的办法：**杀掉进程、真重启** | **✓ 可行**：重启后行出现并连上（实测 `tools:2 / 已连接`），但页面显示「空闲」，且配置里若 `workspaceId` 失效会被删掉 | 需要的是"不用重启" |
| 8 | 出错时修/删/重试 | **✗ 无出口**（C-15，U） | UI |

一句话：**缺的是「UI + API 封装 + 协议」三层都缺**，不是只缺按钮。后端能力（连接、握手、工具注册、OAuth 运行时）全部健在——`internal/modules/toolkit` 的 `mcpController`、`MCPAuthService`、`MCPToolCatalog` 都已 `Provide`（`toolkit.go:277-333`），只是没有一条从浏览器到它们的"可写 + 可观测"通道。

---

## 与 DSH 的差距（该功能应该长什么样）

### DSH 里"添加一个 MCP / 插件"的完整交互链

**A. 加一个 MCP server（设置面）**
1. 入口：侧栏「插件」→ 该组合包/行的配置页（`plugins.bundle.config` / `plugins.row.config` slot），或 Settings 里以插件命名空间（示例 `mcp`）渲染的配置页；**不是**原始 JSON 文本框。
2. 表单来自 `static Config = z.object({ servers: z.dict(ServerEntrySchema).default({}).volatile() })`（`dsh-mcp-manager/src/index.ts:196-198`）；字段：`transport`（stdio｜streamable-http 必填）、`command/args/env/cwd` 或 `url/headers`、`enabled`（默认 true，false = 卸载实例保留条目）、`startupTimeoutMs`(30s)、`toolCallTimeoutMs/closeTimeoutMs/reconnect`。
3. 写入时校验：`validateEntries`（`:222`）逐传输规则 + 点名字段拒绝坏条目；跨传输字段、模板写错、名字与已注册冲突都被拒 → "文档绝不会存下管理器无法执行的服务器"。
4. 保存：ConfigEditor 持久化 + revision 检查（`form.mutate(operations, expectedRevision)`），Loader 更新 volatile config，**不重新挂载管理器**。
5. 对账：新增挂载、删除卸载、`enabled:false` 卸载保留、其余字段变更只重建该实例（单条串行操作队列，避免交错卸载/挂载）。
6. 观测：`ctx.remote.mcpManager.watch(signal)`（`@Remote({mode:'stream'})`）订阅即推全量列表，之后每次状态/条目变化再推一次（合并未读变化）；另有 `restart(name)`（重置重连预算）与 `probe(name)`（不动线上实例，可在启用前测）。
7. 密钥：`${credential:REF}` 模板 → 缺失时状态 `waiting-credentials` 并在凭据更新后自动重试；日志/工具失败的解析值 `[redacted]`。
8. 插件可 `ctx.mcpManager.register(name, entry)` 自注册服务器，随插件生命周期装卸；名字冲突在注册与设置写入两处都拒绝。

**B. 加一个插件（组合包）——可执行的"添加向导"范本**
「添加插件」（主按钮 + 箭头菜单）→ 输入包名/Git 地址/压缩包/本地绝对路径 → Enter → Host `pluginManager.inspect(spec)` 先回答"这个 spec 指向什么"并逐项拒绝（已在列表 / 所有注册表都没有 / 路径无包 / 包无组合包 patch / pnpm 会拒绝）→ 安装对话框 `idle → checking → starting → running → done|failed`（可取消、可查看 pnpm 输出、失败按归因给**重试/换源/允许构建脚本并重试**）→ 完成画面给「立即启用」。刷新失败保留缓存并 toast；权限不足时入口整体不渲染（只在 Peer 可调 `pluginManager/listBundles` 时出现）。

### ZeoKeel 应该长什么样（可执行建议）

**协议（先补这层，否则前端没法写）**
- 读：`capabilities.snapshot` → `{skills:[{id,name,description,source,root,enabled,shadowedBy?}], connectors:[{id,name,transport,url?,command?,enabled,state,tools,lastError?}], warnings:[], roots:[]}`；`capabilities.subscribe` + `capabilities.delta`（域推送，客户端先 snapshot 再吃 delta，符合 AGENTS §5.3）。
- 写：`mcp.server.add|update|remove`（结构化入参，服务端 schema 校验）、`mcp.server.enable`、`mcp.server.probe`（= DSH `probe`）、`mcp.reload`、`mcp.tools`、`mcp.tool.enable`、`mcp.auth.status|prepare|wait|cancel|revoke`；技能侧 `skill.get/create/update/delete`、`skill.dir.add|remove`。
- `initialize.capabilities` 增加 `mcp`、`skills`（+ conditional 时按 `KeyPlugins`/`KeySkills` 存在与否，与现有 `KeyTeam` 同款机制）——客户端据此决定是否渲染入口。
- 落 `docs/TRANSPORT.md §5.1` 状态列 + `contract.json` + `TestTransport*`/`TestContractIsUpToDate`（现在这三处正好都缺）。

**后端**
- `settings.set` 命中 `mcp.*`/`skills.*` 后 `go mcpreload.Trigger(ctx)`（函数已在，接上调用者）；`mcp.reload` 走同一实现并广播 delta。
- 读模型改结构化状态：`{state: 'connected'|'connecting'|'failed'|'disabled'|'auth_required', tools, lastError}`，不要本地化字符串（C-04）。
- 解析失败**不清空**用户行（C-05）；scoped 行失效不删除，改 `orphaned` + UI 引导迁移（C-12）；`Warnings()` 进读模型（C-09）。

**UI（能力页）**
- 页头：一个 primary「添加连接器」+ 一个次级「刷新」（≤1 primary/屏，AGENTS §3.7）。
- 连接器表：名称 / 传输 / 工具数 / **状态（语义色 + 失败原因一行 + 「重试」）** / 操作（启停 `Switch`、删除 `ConfirmDanger`）；行点击 → 抽屉（工具列表 + 每工具 `Switch`、URL/命令、鉴权状态与「授权」按钮）。
- 添加表单：name（`[A-Za-z0-9_-]{1,32}`，重复即时报错）、transport 单选（http / stdio）、http → url + headers 键值对；stdio → command + args + env；auth（none/bearer/oauth2）；超时；底部「测试连接」（调 `mcp.server.probe`，显示 resolve→connect→tools/list 三段结果）。
- 技能表：搜索框、名称/说明/来源/启用；行点击 → `SKILL.md` 预览（复用右检查器 `/api/files/*`）；操作列启停 + 删除；页头「新建技能」+「技能目录」用 scoped 键写（C-07）。
- 空态：一句 + 「添加连接器」+「从模板添加」；技能空态："还没有技能 —— 打开技能目录 / 新建技能"。
- 尺寸参考（DSH 实测值，可直接用）：页面内边距 20px；标题 20px/28px；标签页控件总高 34px、选中块与外框留 2px；卡片 min-height 72px、图标后间距 14px；页头与列表 32px gap。

---

## 附：环境、复现与限制

**实例清单（我起的都在 `data-conn*`，可随时杀）**
| 端口 | dataDir | 用途 | token/说明 |
|---|---|---|---|
| 9878 | `data-conn` | brief 指定实例；技能 2 条（含被遮蔽的重复 + 长描述）、`mcp.servers` 被 UI 写入 `ui-mcp` | 见 `data-conn/gateway.json` |
| 9879 | `data-conn2` | **healthy 连接器**（`tools:2`） | `data-conn2/gateway.json` |
| 9880 | `data-conn3` | **失败连接器**（connection refused） | `data-conn3/gateway.json` |
| 9881 | `data-conn4` | 运行时写入 → 重启生效；现被写入非法 JSON（重启后 connectors 0） | `data-conn4/gateway.json` |
| 9888 | `data-conn5` | 过期 workspaceId 行被删除的复现（请求 9883，被占用后协商到 9888） | `data-conn5/gateway.json` |
| 9899 | — | 自写最小 MCP streamable-HTTP fixture（`out2/fakemcp.py`，支持 initialize/notifications/initialized/tools/list/tools/call，2 个工具） | 日志 `out2/fakemcp.log` |

**复现命令**
```bash
# 1) 控制面探针（自建，stdlib only）：打印每个扩展方法的 -32601
cd /data/zeokeel/.cache/probe2conn
GO111MODULE=off go run main.go -addr 127.0.0.1:9878 -token <token>
# 2) 写 mcp.servers 并对比 /api/capabilities（写入前/后/1.5s 后）
GO111MODULE=off go run main.go -addr 127.0.0.1:9881 -token <token> \
  -set '{"key":"mcp.servers","value":"[{\"name\":\"local-mcp\",\"transport\":\"http\",\"url\":\"http://127.0.0.1:9899/\"}]"}'
# 3) MCP fixture
python3 /data/zeokeel/.cache/ui-audit/out2/fakemcp.py 9899 /…/fakemcp.log
```

**原始证据文件**：`out2/r2-conn-b.json`（全量请求 + DOM）、`out2/r2-conn-a.json`（tab/刷新/点行）、`out2/r2-conn-c.json`（四态 census）、`out2/r2-conn-uiwrite.json`（UI 写入 vs 连接器 0）、`out2/measure2.json`（排序/溢出）、`out2/probe1.log`、`out2/probe2.log`、`out2/fakemcp.log`、`data-conn*/logs/zeokeel.log`。

**限制**
- `AGENTS.md` 说原生 `plugins` 屏（5,031 行，含 1,286 行连接器编辑器）保存在 tag `pre-s4`，但**本 checkout 没有该 tag**（`git tag` 无 pre-s4）——所以无法引用原生编辑器的字段清单来对齐，建议实现时先把它找回来。
- 本轮不碰产品代码；所有实例与 fixture 均在 `.cache/ui-audit/` 下。
- `web/src` 断言"能力页"除只读外无隐藏入口：已用 DOM 全量枚举（`buttons/role=button/a[href]`）+ 键盘 Tab + 点击行验证。