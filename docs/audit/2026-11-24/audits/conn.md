# 验收报告 · 能力页「连接器（MCP）」

> **2026-11-25 第五轮复验（构建 `b9f8bf0` / 二进制 `zeokeel-sess3`）：见 §10 —— 全绿 PASS。**
> R4-1 修好（说明列 `title` 与 API description 逐字符相等，5 档视口 × 3 行）；构建指纹闭环（`gateway.json.bundleSha256` == 用构建脚本同款算法在 `webclient/dist` 复算的摘要，且服务端 5/5 文件逐字节一致）；回归抽样全过；技能表在空/损坏态 0 残留 DOM。仅剩 1 条 P4 观察（antd Tabs 保留隐藏 pane，非残留渲染）。
>
> **2026-11-25 第四轮复验（构建 `68693ba` / bundle `53d93d927a60`）：见 §9 —— 本轮 PASS。**
> F2 在 11 档视口（1440→320）× 浅色/深色全部通过（表宽==容器、说明列>0、行高 39px、来源内层省略生效、长名有 title）；R3-2/R3-3/R3-4/R3-5 全部修好；12 项清单 + F1/F3–F8 + N1–N5 回归全绿。唯一新增问题 **R4-1（P2）**：说明列被单行截断且没有 `title`，说明文字读不全。
>
> **2026-11-25 第三轮复验（构建 `16efc266` / bundle `edc719bb7c8c`）：见 §8。**
> 本轮结论：**N1–N5 全部修复；F2 在 1440/1024/768 通过、在 390 FAIL**（说明列被压成 0px、行高 2943–4021px、表 360px 溢出 342px 容器）；12 项清单 + F1/F3/F4/F5/F6/F7/F8 回归全 PASS。判定：**FAIL（唯一阻断 R3-1 = F2@390）**，另新增 R3-2（改名停用连接器误报"没有生效"）与 R3-3/4/5 三个残留。
>
> **2026-11-25 第二轮复验（构建 8976472，bundle sha256 3f982a3517a4）：见 §7。**
> 复验结论：**F1/F3/F4/F5/F6/F7/F8 已修复；F2 仍未修复（表格反而引入横向滚动）**；
> 12 项验收清单复跑 **12 PASS / 0 FAIL**；但「修复声称⑤」（技能 tab 来源列省略 + 空态给动作）**仍未达成**（F2），另新增 N1–N5 边界缺陷（改名会丢停用态并留下孤儿名单等）。
> 本轮总体判定：**FAIL（唯一阻断项：F2）**。


**结论：FAIL**（12 项清单：9 PASS（其中 #7 带"同名连带删除"的坑）/ 3 FAIL；5 条修复声称里 ②③④部分成立、⑤不成立、④的"停用/启用"不成立；另记 8 个缺陷 F1–F8）

- 验收人：独立验收员 `audit-conn`（session `d3178d87-ead9-4788-83ed-b8c1c50a3a6b`）
- 验收时间：2026-11-24 23:48 – 2026-11-25 00:3x（北京时间）
- 方式：真实网关 + 真实 Chromium + 真实 MCP HTTP 服务器；逐条跑、抓 WS 帧原文、抓 `/api/*` 原文、留截图。**未修改 `web/src`**（只读源码定位行号）。

---

## 0. 环境与复现入口

| 项 | 值 |
|---|---|
| 网关 | `zeokeel-conn`（`.cache/bin/zeokeel-conn`），`--product serve`，`--data-dir /data/zeokeel/.cache/ui-audit/data-conn-audit`，监听 `http://127.0.0.1:9920`（端口从 `data-conn-audit/gateway.json` 读，非写死） |
| fake model | `fakemodel -addr 127.0.0.1:9921` |
| MCP 夹具 | `scripts/acceptance/mcp-http-fixture.py` 起在 9911（验收指定）、9932、9933、9934（多 URL 用例）；工具名 `mcp_http_marker`，1 个工具 |
| 浏览器 | Playwright chromium 1243（`/data/tavo/tools/e2e/chromium-1243`），1440×900@2x / 390×844@2x，zh-CN，`Asia/Shanghai` |
| 干净实例启动 | `bash .cache/ui-audit/startB.sh conn-audit /data/zeokeel/.cache/bin/zeokeel-conn 9920 9921`（脚本会重写 settings，**重启验证改用直接启动同一 data dir**，见 §2-#4） |
| 脚本 / 原始数据 | `.cache/ui-audit/findings3/{hk,wsrpc,c1..c18}.mjs` + `c1..c18.json`（每个断言的结构化原值） |
| 截图 | `.cache/ui-audit/findings3/audit-conn-*.png`（58 张，清单见 §4） |

> 说明：bash 沙箱是 `--unshare-pid`，`ps` 看不到宿主进程，进程存活一律用「端口 + `gateway.json` + 日志」判定。WS 驱动脚本 `.cache/ui-audit/findings3/wsrpc.mjs` 走产品自身协议（`/ws`：`initialize` → `ready` → 方法，帧必须以 `\n` 结尾），仅用于**取证**（写 scoped 设置键、直接 `mcp.reload`），不替代 UI 用例。

---

## 1. 清单表（12 项）

| # | 检查 | 结果 | 证据 |
|---|---|---|---|
| 1 | 连接器 tab 有添加入口；空目录有引导 + 动作 | **PASS** | 头部 `添加连接器` 主按钮可见；空态 `没有连接器。` + 空态内 `添加连接器` 按钮（`c1.json.entry`）。`audit-conn-01-empty-connectors-1440.png`。技能 tab 计数 `技能 0` / `连接器 0` |
| 2 | 成功连接：`fixture` → `http://127.0.0.1:9911/mcp` 为 `connected`、工具数≥1、WS 有 `mcp.reload` 请求+返回帧、`/api/capabilities` 有该连接器 `state=connected` | **PASS** | 行：`fixture \| http \| 已连接`，`已发现 1 个工具`，端点 `http://127.0.0.1:9911/mcp`（等宽，computed `font-family: SF Mono…/Consolas…`）。WS 原文见 §2-#2。`/api/capabilities` 原文：`{"id":"fixture","name":"fixture","transport":"http","tools":1,"status":"已连接","state":"connected"}`。`audit-conn-04-connected-fixture.png` |
| 3 | 失败连接：`http://127.0.0.1:9919/mcp` → `failed`、行内真实错误（含 `dial tcp`）、不得显示"空闲" | **PASS** | 行 `data-state="error"`，文案 `连接失败`，错误原文完整回显：`mcp: initialize: mcp: http request: Post "http://127.0.0.1:9919/mcp": dial tcp 127.0.0.1:9919: connect: connection refused`；`/api/capabilities` 同步 `state=failed` + `error` 字段。`audit-conn-05-failed-broken.png` |
| 4 | 持久化：刷新、重启网关（同 data dir）后两条连接器与状态仍正确，重启后自动连上 fixture | **PASS** | 刷新：两行保持不变（`c2.json`）。重启（kill → 同一 data dir 重新 `zeokeel-conn`，新 epoch `044ec735181244b6`）：页面两行仍为 `已连接` / `连接失败`；日志 `23:50:34 [mcp/manager] server "fixture" connected: 6 tool(s)`、`[toolkit] mcp "fixture" connected: 1 tool(s), protocol 2025-06-18`、`server "broken" connect failed: … dial tcp … refused`。`audit-conn-07-after-gateway-restart.png` |
| 5 | 停用/启用：状态"已停用"、`mcp.disabledServers` 写入、**`/api/capabilities` 对应行不再 connected**、再启用恢复、刷新保持 | **FAIL（后端未生效）** | 写入与 UI 标签都"对"（`已停用`、`mcp.disabledServers=["fixture"]`），但**连接从未断开**：同一 `mcp.reload` 后 `/api/capabilities` 仍是 `{"id":"fixture",…,"tools":1,"state":"connected"}`，日志 `23:50:57 [toolkit] mcp "fixture" connected: 1 tool(s)`。根因与反证见 §3-F1。`audit-conn-08-disabled.png` / `audit-conn-09-enabled-again.png` |
| 6 | 编辑：改名/改 URL/改超时生效；http→stdio（不存在命令）→ failed 且错误可读；改回 http 恢复 | **PASS** | 超时 7000 写入 `timeoutMs`、清 0 则删除字段；URL 改 9932 后重连成功；改名 `fixture-renamed` 后 `id` 随之变化；stdio（`npx-does-not-exist-audit`）→ `连接失败` + `mcp: start server "npx-does-not-exist-audit": exec: "npx-does-not-exist-audit": executable file not found in $PATH`；改回 http 恢复 `已连接`。stdio 行编辑回填正确（传输=stdio、命令/参数/环境变量/超时全部回填，无 URL 字段）。`c4.json`/`c18.json`、`audit-conn-10..16`、`audit-conn-53-stdio-prefill.png` |
| 7 | 删除：确认框含名称；删除后行消失、`mcp.servers` 不含、`/api/capabilities` 不含、工具列表减少 | **PASS（有同类名连带删除的坑，见 F4）** | 确认框原文 `删除连接器「race-double」？连接会立即断开。`；删除后：DOM 少一行、`mcp.servers` 少一行、`/api/capabilities` 少一条、工具总数 `4 → 2`（删掉的两条各 1 个工具）。`audit-conn-22-delete-confirm.png`、`audit-conn-23-after-delete.png`、`c11.json` |
| 8 | 校验：名称空 / `not-a-url` / URL 含空格 / stdio 无命令，都拦下且不写 settings（抓帧证明） | **PASS** | 5 个用例全部：错误文案出现（`请填写名称` / `需要以 http:// 或 https:// 开头，且不能包含空格` / `请填写要运行命令`）、弹窗不关、`settings.set` 帧 **0 条**、`mcp.reload` 帧 **0 条**、磁盘 `mcp.servers` 逐字节未变。`c5.json.cases`、`audit-conn-17-validate-*.png` |
| 9 | 与设置页一致性：`/settings/data-privacy` 的 `mcp.servers` JSON 表示是否与页面一致；反向改坏 JSON 应给可恢复提示而非崩溃/白屏 | **FAIL（前半一致，后半无提示）** | 一致：设置页文本域值与连接器页逐行一致（含 `workspaceId`/`timeoutMs`），且**仍可编辑**（`readOnly=false`），即"未被页面接管、未只读"。反向：填 `{"oops":` → 保存成功且提示 `已保存`（无 JSON 校验）；连接器页**不崩溃/不白屏**（标题、tab、添加按钮都在），但**没有任何提示**，直接显示空态 `没有连接器。`，而 `/api/capabilities` 仍返回旧连接（5 条）→ 两边矛盾；再点 `刷新` 后连接被静默丢弃（`connectors: []`）。`c9.json`、`c16.json`、`audit-conn-31..34`、`audit-conn-50-broken-json-refresh.png` |
| 10 | Esc/焦点：弹窗内 Esc 只关弹窗不离开页面；删除确认框同理；表单 Tab 顺序 名称→传输→URL→超时→按钮 | **PASS** | Tab 顺序实测：`#name` → `#transport`(combobox) → `#url` → `#timeoutMs` → `放弃更改` → `保存`（`c7.json.tabOrder`）。Esc：弹窗关闭、URL 仍是 `/capabilities?tab=connectors`、连接器 tab 仍 active、行仍在。`Popconfirm` 上 Esc：浮层关闭、行仍在、页面不动、焦点回到该行 `删除` 按钮。`audit-conn-24..27` |
| 11 | 移动端 390 + 深色：行不溢出（`scrollWidth==clientWidth`）、按钮不裁切、深色状态点/错误文字可读（≥4 个 computed 值） | **PASS** | 390：`documentElement.scrollWidth=390 == clientWidth=390`；6 行全部 `scrollWidth=clientWidth=340`；12 个动作按钮 `48×24`，无 `clippedByRow`、无文字溢出。深色（`data-theme=dark`，`body bg rgb(17,17,24)`）：状态点 success `rgb(34,197,94)` 对比 8.25:1、error `rgb(248,113,113)` 6.8:1、错误文字 `rgb(252,165,165)` 9.9:1、状态标签 `rgb(163,171,187)` 8.15:1、端点 `rgba(255,255,255,.45)` 18.8:1。浅色：错误文字 6.47:1、状态标签 6.07:1、success 点 3.3:1（图形 ≥3:1 达标）。`c8.json`、`audit-conn-28..30` |
| 12 | 并发/边界：快速连点保存（0–50ms）不产生重复行；名称含空格/中文/emoji 可用；同名重复添加应被拒绝或有明确后果说明 | **FAIL（同名重复被接受且后果隐蔽）** | 快速双击保存：只产生 **1** 行 1 条 `settings.set`（PASS）。名称 `我的 服务 🚀`：保存成功、连接成功（PASS）。同名重复：**被接受**（`mcp.servers` 出现两行同名），后果是——第二行沿用第一行的 live 状态/错误（9932 行显示 9919 的 `connection refused`）、删一行会把同名两行**一起删掉**、并在 DOM 留下按钮全部 disabled 的**僵尸行**（`settings` 已无该行，DOM 仍有，持续 ≥7s）；URL 重复则被后端静默去重 → 永久 `空闲` 且无任何解释。`c5.json`、`c6.json`、`c11.json`、`c14.json`、`c15.json`、`audit-conn-19..21`、`audit-conn-37`、`audit-conn-43..48` |

**统计：PASS 9（#1,#2,#3,#4,#6,#7,#8,#10,#11）+ FAIL 3（#5,#9,#12）。**

> 逐条对齐"修复声称"：① 表单与校验 = 成立；② `mcp.servers` + `mcp.reload` + 免重启 + 提示 = 成立（但 `mcp.disabledServers` 是**死写**）；③ 网关派生状态/错误原文/传输/等宽端点/工具数 = 成立；④ 编辑/删除 = 成立，**停用/启用 = 不成立**；⑤ 技能 tab = **不成立**（来源列仍撑宽、空态无动作，见 F2/F3）。

---

## 2. 关键证据原文

### #2 添加成功：WS 帧（请求 + 返回，逐字）
```
send {"jsonrpc":"2.0","id":10,"method":"settings.set","params":{"key":"mcp.servers","value":"[{\"name\":\"fixture\",\"transport\":\"http\",\"url\":\"http://127.0.0.1:9911/mcp\"}]"}}
recv {"jsonrpc":"2.0","method":"settings.delta","params":{"baseRev":0,"rev":1,"rows":{"mcp.servers":"[{\"name\":\"fixture\",\"transport\":\"http\",\"url\":\"http://127.0.0.1:9911/mcp\"}]"}}}
send {"jsonrpc":"2.0","id":11,"method":"mcp.reload","params":{}}
recv {"jsonrpc":"2.0","method":"settings.delta","params":{"baseRev":1,"rev":2,"rows":{"mcp.servers":"[{\"name\":\"fixture\",\"workspaceId\":\"ws-1795535349800011203\",\"url\":\"http://127.0.0.1:9911/mcp\",\"transport\":\"http\"}]"}}}
```
（`mcp.reload` 的返回帧按 id 关联，编辑用例里抓到完整应答：）
```
send {"jsonrpc":"2.0","id":11,"method":"mcp.reload","params":{}}
recv {"jsonrpc":"2.0","id":11,"result":{"connectors":[{"id":"fixture-renamed","name":"fixture-renamed","transport":"http","tools":1,"status":"已连接","state":"connected"},{"id":"broken",…,"tools":0,"status":"连接失败: mcp: initialize: … dial tcp 127.0.0.1:9919: connect: connection refused","state":"failed","error":"mcp: initialize: …"}]}}
```

### #2 `GET /api/capabilities`（curl，带 cookie + Origin）
```json
{"skills":[],"connectors":[{"id":"fixture","name":"fixture","transport":"http","tools":1,"status":"已连接","state":"connected"}]}
```

### #5 停用（三次取证的完整链条）
1) 页面点 `停用` 后的帧：
```
send {"jsonrpc":"2.0","id":10,"method":"settings.set","params":{"key":"mcp.servers","value":"[…两行…]"}}
send {"jsonrpc":"2.0","id":11,"method":"settings.set","params":{"key":"mcp.disabledServers","value":"[\"fixture\"]"}}
send {"jsonrpc":"2.0","id":12,"method":"mcp.reload","params":{}}
```
2) 同一次 reload 后 `/api/capabilities`：`{"id":"fixture",…,"tools":1,"state":"connected"}` —— **没有变化**；日志仍是 `[toolkit] mcp "fixture" connected: 1 tool(s), protocol 2025-06-18`。
3) 反证（用协议直接写 **workspace-scoped** 键，证明后端只认它）：
```
settings.set {"key":"mcp.disabledServers.ws-1795535349800011203","value":"[\"fixture\"]"}  → result {epoch, rev:3}
mcp.reload  → result {"connectors":[{"id":"broken",…}]}          ← fixture 真的没了
settings.set {"key":"mcp.disabledServers.ws-1795535349800011203","value":"[]"}
mcp.reload  → result {"connectors":[{"id":"fixture","tools":1,"state":"connected"}, …]}  ← 又回来了
```

### #9 设置页原文
```
serversValue = "[{\"name\":\"fixture-renamed\",\"workspaceId\":\"ws-1795…\",\"url\":\"http://127.0.0.1:9911/mcp\",\"transport\":\"http\",\"timeoutMs\":5000},…]"
serversReadOnly = false        fieldLabel = "MCP 服务器"
helpText = "MCP 服务器 | JSON 数组：{name, transport, command|url}。"    （未提 timeoutMs / workspaceId）
disabledValue = "[]"
```
改坏后：`settings.set {"key":"mcp.servers","value":"[{\"name\":\"x\","}` → 页内提示 `已保存`；连接器页 `alert=null`、`empty="没有连接器。 | 添加连接器"`、`rows=0`、`/api/capabilities` 仍 5 条；点 `刷新` 后 `/api/capabilities` → `"connectors":[]`。

### #5（技能 tab）表格实测几何
```
表头宽度：名称 74px / 说明 88px / 来源 1096px   （列配置是 160 / auto / 200）
来源单元格：scrollWidth 1080 == clientWidth 1080 → ellipsis 从未触发
行高 325px（名称、说明被压成一字一行）
```
（根因：`Typography.Text ellipsis` 在 `Table` 默认 `table-layout:auto` 下无宽度约束，长路径把列撑到 1096px。）

---

## 3. 失败项（现象 / 复现 / 期望 / 建议修法）

### F1（P0，功能不成立）「停用/启用」只改本地标签，后端连接从未断开
- **现象**：点 `停用` → 行变 `已停用`（灰点）、`mcp.disabledServers=["fixture"]` 已写盘，但 `/api/capabilities` 仍 `state=connected, tools=1`；`mcp.reload` 后日志仍打印 `[toolkit] mcp "fixture" connected: 1 tool(s)`。也就是说：用户以为断开，实际**连接仍在、工具仍注册给模型**（安全语义问题），而 UI 在**谎报**状态。
- **复现**：`/capabilities?tab=connectors` → 任一 `connected` 行点 `停用` → 看 `/api/capabilities`（或看网关日志）。脚本 `.cache/ui-audit/findings3/c3-restart-toggle.mjs`、`c3b-scoped-key.mjs`。
- **根因**：写读两端键不规范一致——
  - 写：`web/src/features/capabilities/capabilities-screen.tsx:139`（`setSetting(SETTING_KEYS.mcpDisabledServers, …)`，即裸键 `mcp.disabledServers`；读同理 `:124-125`）。
  - 读：`internal/modules/toolkit/mcp_control.go:663` → `contracts.MCPDisabledServersSettingKey(workspaceID)`，有工作区时它是 `mcp.disabledServers.<workspaceId>`（`internal/contracts/plugins.go:319`）。
  - 对照：`mcp.servers` 有"eager migration"把全局行绑定到当前工作区（`internal/modules/toolkit/mcp_settings.go:178-220`），而 `mcp.disabledServers` **没有**对应迁移 → 有工作区时裸键永远被忽略。
- **期望**：停用后 `mcp.reload` 返回的 connectors 里该行消失（或 `state!=connected`）、工具从注册表撤销；启用后恢复；刷新/重启后保持。
- **建议修法（择一，推荐 a）**：
  a. 后端兼容 + 迁移：`loadMCPServers` 同时读 scoped 与裸键（并集），并把裸键内容迁移进 scoped 键后清除（与 `reconcileMCPServerRows` 对称）。这样设置页那个裸键文本域也一并被救活（见 F7）。
  b. 前端写 scoped 键：需要页面知道当前 workspace id（目前 `ConnectorSummary` 里没有；`mcp.servers` 行的 `workspaceId` 是网关回填的，可作为来源），改动面更大且设置页仍是裸键 → 仍会不一致。
- **附带**：`mcp.disabledServers` 每次保存（新增/编辑/删除）都会写一遍（多为 `"[]"`），属无意义写入，建议只在真正变化时写。

### F2（P1，声称⑤不成立）技能 tab 的「来源」列仍然撑宽表格，省略号从未生效
- **现象**：一条长路径技能的表格几何为 `名称 74px / 说明 88px / 来源 1096px`，行高 325px——名称与说明被压成一字一行，正是 round-2 C-08 的症状；`来源` 单元格 `scrollWidth==clientWidth`，省略号没触发。截图 `audit-conn-35-skills-source-ellipsis.png` 里可见来源文本一直延伸到内容区右边界之外。
- **复现**：在 `<data-dir>/workspace/skills/<很长的目录名>/SKILL.md` 放一个技能（或任何真实长路径来源），打开 `/capabilities` 技能 tab。脚本 `c10-skills.mjs`。
- **根因**：`web/src/features/capabilities/capabilities-screen.tsx:286-306`：`Table` 未设 `tableLayout="fixed"`，`source` 列 `width: 200` 在 auto 布局下压不住长单词（路径无空格），`Typography.Text ellipsis` 没有可用宽度。
- **期望**：来源列固定宽度（如 200–240）并在其中省略、`title` 悬浮显示全文；名称/说明列保持可读宽度；表格总宽不超出容器。
- **建议**：`<Table tableLayout="fixed" …>` + `source` 列用 `ellipsis={{ showTitle: true }}`（或给 `.zk-connector-path` 加 `display:block; min-width:0; overflow:hidden`，并给 `td` 设 `max-width`）；同时给 `说明` 列 `ellipsis` 兜底。

### F3（P2）技能 tab 空态没有动作
- **现象**：`没有加载技能。` 只有一个 `Empty`，无按钮（连接器 tab 的空态有 `添加连接器`）。验收要求"空态给动作"。
- **位置**：`capabilities-screen.tsx:283-285`（`<Empty description={…noSkills} />`）。
- **建议**：与连接器空态对齐，给一个动作（如 `刷新`／跳转 `设置 → 数据与隐私` 的技能目录、或"新建技能"若产品已有该能力）。

### F4（P1）同名重复添加被接受，且衍生三连：串状态 / 连带删除 / 僵尸行
- **现象**（同一份 `mcp.servers` 内两行同名）：
  1. **串状态**：`live` 是按 `name` 建 Map，后写覆盖前写 → 两行共用一条 `ConnectorSummary`。截图 `audit-conn-22-delete-confirm.png` 里，`broken` 的 9932 那行显示的是 9919 的 `connection refused`（它自己的端点是健康的 9932）。
  2. **连带删除**：`remove()` 用 `configured.filter(item => item.name !== row.name)`，删一行把同名两行一起删（确认框只说单数「删除连接器「dup-a」？」）——实测工具数 4→2（两条各 1 个工具一起消失）。
  3. **僵尸行**：删除后 DOM 残留一行（`已连接`、三个按钮全部 `disabled`），而 `mcp.servers` 已无该行；持续 ≥7s 不消失，仅刷新页面才清掉（`c14.json` 时间序列：`domCount 6 / settingsCount 5`；`c15.json.ghost.rowButtonsState` 显示 `[true,true,true]`）。根因是 `key={row.name}` 重复键（`capabilities-screen.tsx:325`），React 复用节点导致旧节点残留（含"保存中"的 disabled 快照）。
- **复现**：同名添加两次（不同 URL）→ 删其中一个。脚本 `c14-repro2.mjs`（100% 复现）。
- **期望**：要么**拒绝**同名（明确提示"已存在同名连接器"），要么接受但把 `key` 换成稳定唯一 id（如 `workspaceId+url+index`）、`remove` 按行身份而不是按名字、live 状态按行身份匹配。
- **建议**：最小修法是前端在 `submit()` 里校验重名并报错；若产品确实要支持"同名不同端点"（后端 `mcpManagerKey` 已支持 `name#2`），则必须改成按身份操作（`key`、`remove`、`visible live map` 三处，`capabilities-screen.tsx:120-128, 223-224, 325`）。

### F5（P2）重复 URL 的添加被后端静默去重 → 永久「空闲」且无解释
- **现象**：添加一个与已有行同 URL 的连接器，行显示 `空闲`（灰黄点），页面唯一文案是通用的 `已保存并重新连接`；后端日志：`[toolkit] skip mcp.servers[3]: duplicate URL "http://127.0.0.1:9911/mcp" (assembly wins)`，该行永远不会连上。用户视角 = "加了个连接器，永远是空闲，不知道为什么"。
- **位置**：去重在 `internal/modules/toolkit/mcp_settings.go:143-153`；前端把它渲染成 `idle` 默认分支（`capabilities-screen.tsx:78-90`）。
- **建议**：前端在提交前查重 URL（提示"该端点已被 xxx 使用"）；或网关在 `mcp.reload`/`/api/capabilities` 里对该行返回 `state=failed` + `error="duplicate URL, skipped"`，让行内错误位显示原因。
- **附带文案 bug**：日志里的 `(assembly wins)` 是错的——实际胜出的是更早的 **settings 行**（`mcp_settings.go:146-150` 无条件按 source 判定），会误导排障。

### F6（P3）校验被拦下时产生未捕获的 Promise rejection
- **现象**：每次校验失败（名称空/URL 非法/stdio 无命令）都会抛一个 `pageerror`（`Object`，Playwright 记为 `pageerror: Object`），c5 里 5 次校验 = 5 条；`void submit()`（`capabilities-screen.tsx:434` `onOk={() => void submit()}`）没有 catch，`validateFields()` 的 reject 成为 unhandled rejection。功能不受影响，但开发态会弹错误覆盖层、生产会污染控制台/监控。
- **建议**：`const ok = async () => { try { await submit(); } catch { /* 校验失败由表单展示 */ } }`，或 `submit().catch(() => {})`。

### F7（P2）设置页仍保留可编辑的 `mcp.servers` 原始 JSON；写坏后连接器页无提示地"变空"
- **现象**：`/settings/data-privacy` 的 `MCP 服务器` 字段是可编辑 TextArea（`readOnly=false`），说明该行**没有被页面接管、也不是只读**——两个入口同时写同一行，且设置页**不做 JSON 校验**（保存坏 JSON 也提示 `已保存`）。写坏后连接器页既不崩也不白屏（这点达标），但**没有任何提示**，只显示空态 `没有连接器。`，而 `/api/capabilities` 此刻仍返回 5 条活连接（两边矛盾）；用户点 `刷新` 后连接被静默丢弃（`connectors: []`），必须自己想到回设置页手改 JSON 才能恢复。
- **期望**：连接器页在 `mcp.servers` 解析失败时给出可操作提示（`role="alert"`：`mcp.servers 不是合法 JSON，连接器无法加载——去 设置 → 数据与隐私 修复`），并**不要**同时把"空列表"当成正常空态；`刷新` 前也应提示解析错误而不是静默清空连接。
- **建议**：`parseConnectors` 目前 `catch { return [] }`（`capabilities-screen.tsx:48-57`）丢掉了错误；改成返回 `{rows, error}`，页面渲染 alert + 链到设置页；设置页对该键改为只读 + "在能力页管理"跳转（或保存前做 JSON.parse 校验）。

### F8（P3）应用壳的 2 个 404（与本功能无关，仅记录）
- 每次页面加载控制台固定 2 条 `Failed to load resource: 404`，实测 `/favicon.ico`、`/manifest.webmanifest`、`/apple-touch-icon.png` 均 404（网关对未知静态路径返回 404）。不影响连接器功能，但会干扰"看控制台判断有没有 bug"的验收习惯。

---

## 4. 截图清单（`/data/zeokeel/.cache/ui-audit/findings3/`）

| 文件 | 内容 |
|---|---|
| `audit-conn-01-empty-connectors-1440.png` | #1 空态 + 引导动作 |
| `audit-conn-02-empty-skills-1440.png` | 技能空态（无动作，F3） |
| `audit-conn-03-add-modal-1440.png` | 添加表单（名称/传输/地址/超时） |
| `audit-conn-04-connected-fixture.png` | #2 连接成功 + `已保存并重新连接` |
| `audit-conn-05-failed-broken.png` | #3 失败行 + `dial tcp` 原文 |
| `audit-conn-06-after-reload.png` | #4 刷新后保持 |
| `audit-conn-07-after-gateway-restart.png` | #4 重启网关后保持（新 epoch） |
| `audit-conn-08-disabled.png` / `09-enabled-again.png` | #5 停用/启用（UI 变、后端不变 → F1） |
| `audit-conn-10..16` | #6 编辑：回填 / 超时 / URL / 改名 / stdio 表单 / stdio 失败 / 改回 http |
| `audit-conn-17-validate-*`（5 张） | #8 四类校验 + stdio 无命令 |
| `audit-conn-18-unicode-name.png` | #12 中文+空格+emoji 名称 |
| `audit-conn-19-race-double-save.png` | #12 快速双击保存（无重复行） |
| `audit-conn-20/21-duplicate-name*.png` | #12 同名重复被接受（F4） |
| `audit-conn-22-delete-confirm.png` | #7/#10 确认框含名称 |
| `audit-conn-23-after-delete.png` | #7 删除后行消失 |
| `audit-conn-24-tab-order.png` / `25-after-esc-modal.png` / `26-unhandled-rejection.png` / `27-confirm-after-esc.png` | #10 Esc/焦点 + F6 |
| `audit-conn-28-mobile-390-light.png` / `29-dark-1440.png` / `30-mobile-390-dark.png` | #11 移动端 + 深色 |
| `audit-conn-31..34` | #9 设置页原文 / 写坏 / 连接器页空态 / 恢复 |
| `audit-conn-35/36-skills-*.png` | 技能来源列撑宽（F2） |
| `audit-conn-37-idle-duplicate-url.png` | F5 重复 URL → 空闲无解释 |
| `audit-conn-38..49` | 删除连带 / 复现序列 / 帧证据 / 僵尸行 |
| `audit-conn-50-broken-json-refresh.png` | F7 坏 JSON + 刷新 → 连接清空 |
| `audit-conn-52/53-stdio-*.png` | stdio 行渲染 + 编辑回填 |

---

## 5. 需补验收点（本轮无法判定或未覆盖）

1. **停用后的真实副作用**：本轮只证明了"连接仍在、工具仍注册"（`/api/capabilities` + 日志）。若产品后续修好 scoped 键，需要再验**工具确实从模型可见工具集中消失**（需要一次真实 run 的 `tool.list`/tool-call 证据，本环境只有 fakemodel，未跑）。
2. **多工作区**：`mcp.disabledServers.<wsid>` 的设计意图是工作区隔离，但连接器页没有工作区选择器。需要一条用例：两个工作区各自停用不同连接器，互不影响（本轮只有一个工作区）。
3. **OAuth / 带 headers 的连接器**：表单不暴露 headers/auth，`mcp.servers` 行的 `headers`/`auth` 字段只能靠设置页手写。编辑时 `submit()` 会保留未知字段（`{...editing}`），但**新增**路径无法表达；需要确认这是有意为之。
4. **真实 stdio MCP 服务器**（如 `npx @modelcontextprotocol/server-filesystem`）：本轮用不存在命令验证了失败路径，未验证成功路径（无网/无包）。
5. **超时语义**：`timeoutMs` 只验证了"写入/清除"，未验证"卡死的 MCP 端点会在超时后 failed"（需要一个只接受连接不回包的夹具）。
6. **`mcp.reload` 失败路径**：若 reload 抛错（例如 toolkit 未装配），页面 `persist` 会报 `screen.capabilities.saveFailed`；本轮环境无法模拟，未验。
7. **技能 tab 的"空态动作"具体形态**：产品未定义该动作应该是什么（刷新？跳转设置？），需产品确认后再验。

---

## 6. 一句话总结

连接器的**主链路（增/改/删/校验/持久化/状态派生/免重启 reload）是真做通了**，并且失败信息、移动端、深色、Esc/焦点都过关；但**「停用/启用」是假开关**（全局键写、scoped 键读，连接和工具都没断，UI 还显示"已停用"），**技能来源列根本没省略**（1096px 撑满、名称压成一字一行），**同名连接器会把状态串台、连带删除并留下僵尸行**——这三点必须在修完 F1/F2/F4 后重跑本清单（尤其 #5、#9、#12）。

---

# 7. 复验（round-2 · 新冻结构建 `8976472`）

- 构建：`8976472 fix: connector switch actually switches, and the settings that did nothing`，web bundle `sha256 3f982a3517a4`（6 files, built 2026-11-24T16:14:11Z）
- 实例：`.cache/bin/zeokeel-conn2`，`--data-dir .cache/ui-audit/data-conn2-audit`，端口 **9925**（读 `data-conn2-audit/gateway.json` 协商结果，非写死），fake model 9926，MCP 夹具 9911 / 9933
- 失败路径用**已验证关闭**的端口 `127.0.0.1:9939`（旧报告用的 9919 已被别的实例占用，故换端口并先探测确认 `connection refused`）
- 脚本：`findings3/r2a..r2n*.mjs`，原始值 `r2a..r2n.json`；截图 `findings3/audit-conn-r2-*.png`（40 张）
- 复验方式：每个用例都读 **三方**——DOM（`data-state`/文案）、`mcp.reload` 的请求/应答帧原文、`/api/capabilities` 原文；持久化类用例另外读 `settings.json` 磁盘原文并重启网关复核。**未修改 `web/src`。**

## 7.0 复验结论

| | 结果 |
|---|---|
| F1 停用/启用真开关 | **PASS**（三方一致 + 重启后仍停用 + 启用后工具数回来） |
| F2 技能来源列省略 / 不撑宽 | **FAIL**（`tableLayout` 确实是 fixed，但来源列 1103px、表格 2384px 撑出 1128px 容器并出现横向滚动；来源永不省略；长名行高 127px） |
| F3 技能空态解释文案 | **PASS**（文案到位；末句"重启网关即可被列出"与实际不符，实测无需重启） |
| F4 同名重复 / 僵尸行 | **PASS**（同名被字段校验拦下且 0 写入；自改名无误报；强制同名两行后删除不产生按钮全禁用的行）→ 但**改名会丢「已停用」并留孤儿名单**（新增 N1） |
| F5 同 URL 未生效提示 | **PASS**（明确横幅含名称与原因）→ 提示是一次性的、且与"已保存"并存（新增 N2） |
| F6 校验失败不再抛未捕获 rejection | **PASS**（3 次失败 = 0 pageerror，全程 console 0 error） |
| F7 设置页两行只读 + 指向连接器页 | **PASS**（两行 `disabled=true`、无法聚焦、帮助文案带"（在对应页面修改）"）→ 带外脏值仍无提示（残留 N3） |
| F8 favicon | **PASS**（`/favicon.svg` 200 `image/svg+xml`，首屏 0 个 4xx、0 条 console error） |
| 12 项验收清单 | **12 PASS / 0 FAIL**（技能 tab 属于「修复声称⑤」，不在 12 项内 → 仍见 F2 FAIL） |

## 7.1 F1–F8 逐条证据

### F1（P0）停用/启用 —— PASS

停用（一次点击的完整帧，逐字）：
```
send {"jsonrpc":"2.0","id":10,"method":"settings.set","params":{"key":"mcp.servers","value":"[…两行…]"}}
recv {"jsonrpc":"2.0","id":10,"result":{"epoch":"734d117f4f9e7a7f","rev":45}}
send {"jsonrpc":"2.0","id":11,"method":"mcp.reload","params":{"disabled":["ok1"]}}
recv {"jsonrpc":"2.0","id":11,"result":{"connectors":[{"id":"bad1",…,"state":"failed"}],"disabled":["ok1"]}}      ← ok1 从 connectors 里消失
recv {"jsonrpc":"2.0","method":"settings.delta","params":{"baseRev":45,"rev":46,"rows":{"mcp.disabledServers.ws-1795536953764906803":"[\"ok1\"]"}}}   ← 网关写在它读的那个键上
```
三方交叉核对（停用后）：
- DOM：`ok1 | http | 已停用`，`data-state="neutral"`，工具数行消失；
- `/api/capabilities`：`connectors` 只剩 `bad1`，`"disabled":["ok1"]`；
- 磁盘：`mcp.disabledServers.ws-1795536953764906803 = ["ok1"]`（**不再写裸键**，`mcp.disabledServers` 已不存在）。

**重启网关（同一 data dir，新 epoch `734d117f4f9e7a7f`）后仍停用**：
```
2026/11/25 00:17:10 [toolkit] skip disabled MCP server "fixture"
2026/11/25 00:17:10 [toolkit] skipped 1 disabled MCP server(s) (mcp.disabledServers.ws-1795536953764906803)
```
页面：`fixture | http | 已停用`；`/api/capabilities`：`"disabled":["fixture"]` 且 connectors 无 fixture。

启用后恢复：
```
send {"jsonrpc":"2.0","id":12,"method":"mcp.reload","params":{"disabled":[]}}
recv {"jsonrpc":"2.0","id":12,"result":{"connectors":[{"id":"ok1",…,"tools":1,"state":"connected"},…],"disabled":[]}}
```
DOM 回到 `已连接` + `已发现 1 个工具`；磁盘键变 `[]`；刷新页面保持。`audit-conn-r2-05/07/08`。
边界：**删除一个「已停用」的连接器会同时清掉它在停用名单里的条目**（删除后键变 `[]`），没有留下"删掉还停用"的幽灵条目。

### F2（P1）技能来源列 —— **FAIL**

`tableLayout` 计算值 = `fixed`，但实测（1440 视口，容器 1128px）：

| 指标 | 旧构建 | 新构建 | 复验判据 | 结果 |
|---|---|---|---|---|
| `table-layout` | auto | **fixed** | fixed | PASS |
| 名称列宽 | 74px | **160px** | 不再一字一行 | 改善 |
| 说明列宽 | 88px | 1121px | 可读 | PASS |
| 来源列宽 | 1096px | **1103px** | 应固定 ~200 并省略 | **FAIL** |
| 来源单元格 `scrollWidth/clientWidth` | 1080/1080 | **1087/1087** | 需要 `scroll > client` 才省略 | **FAIL（从不省略）** |
| 表格内容宽 vs 容器 | 溢出页面 | **2384 > 1128（表格内部横向滚动）** | 不撑宽 | **FAIL** |
| 长名技能行高 | 325px | **127px** | < 60px | **FAIL（短名 39px 达标，长名仍 127px）** |

看到的现象（`audit-conn-r2-09/11`）：来源列整列被推到容器右侧之外，用户必须横向滚动才看得到；名称列 160px 里 81 字符的名字仍换 5 行。

关键对照：**名字正常的技能（`short`）也一样不省略**——同一表格里它那行的来源单元格 `scrollWidth 497 == clientWidth 497`（列宽 1103px），说明问题不在"极端长名字"，而在列宽被内容撑开这个机制本身。

**根因与已验证的修法**（`capabilities-screen.tsx:296-299`）：`Table` 同时加了 `tableLayout="fixed"` 和 `scroll={{ x: 'max-content' }}`；后者让 antd 按内容量列宽，`width: 200` 与省略号双双失效。现场诊断（只改内联样式，不动产品代码）：把表格 `width:100%; min-width:auto` 后立刻变成
```
名称 160 / 说明 768 / 来源 200；来源 scrollWidth 1087 > clientWidth 184 → 出现省略；表宽 1128 == 容器
```
→ 建议：去掉 `scroll={{ x: 'max-content' }}`（或改 `scroll={{ x: false }}`），配合已有的 `tableLayout="fixed"` + `source` 列 `width:200`；若要保留横向滚动，则给 `source` 列加 `ellipsis:{showTitle:true}` 并给列指定 `width`（antd 在 `x` 模式下仍按 width 收敛）。

### F3（P2）技能空态 —— PASS（有文案小瑕）
```
没有加载技能。 | 技能来自宿主的技能目录（仓库内 .agents/skills 或数据目录下的 skills/）；新增技能后重启网关即可被列出。
```
- 有解释文案（要求达成）。仍无按钮（连接器空态有），但本轮判据是"有解释文案"。
- 文案瑕疵：末句"重启网关即可被列出"与实际不符——实测在 `<workspace>/skills/` 新建技能后 **无需重启**，`/api/capabilities` 立刻列出（`r2d` 就是在新进程内直接读到 2 个技能）。建议改成"保存后刷新本页即可"。

### F4（P1）同名重复 / 僵尸行 —— PASS
- 同名添加：字段校验拦下，文案 `已经有同名连接器`，弹窗不关，**`settings.set` 0 条、`mcp.reload` 0 条、磁盘 0 变化**。
- 自改名（name 不变）不误报：保存成功（`timeoutMs:6000` 落地），无错误。
- 改名撞别人名字：拦下，0 写入。
- 僵尸行：用协议强制写入两行同名（绕过校验，模拟脏数据）后渲染正常（`key={name}:${index}`），删掉其中一行后 DOM 与磁盘都是 1 行，残留行按钮 `[false,false,false]`（不再有"按钮全禁用的行"）。`audit-conn-r2-30`。

### F5（P2）同 URL —— PASS
添加 `dup-url → http://127.0.0.1:9911/mcp`（fixture 已占用该端点）后，页面给出：
```
「dup-url」没有生效：同名或同地址的连接器已存在，网关会跳过重复项。
```
（`role=alert` 的 `.zk-msg-error`，含连接器名与原因；行仍是 `空闲`，但不再是"永远空闲且无解释"。）

### F6（P3）未捕获 rejection —— PASS
连续 3 次「名称空 → 保存」：`pageerror` 计数 **0**；整轮所有用例 `consoleErrors: []`。

### F7（P2）设置页只读 —— PASS
```
serversDisabled: true, disabledDisabled: true, canFocus: false, 值未变
serversHelp: "连接器在「能力 → 连接器」里编辑，这里只读展示。 （在对应页面修改）"
disabledHelp: "停用名单由「能力 → 连接器」维护，这里只读展示。 （在对应页面修改）"
```
`audit-conn-r2-17`。即"第二个写者"已关闭：设置页再也不能把 `mcp.servers` 存成垃圾。

### F8（P3）favicon —— PASS
`<link rel="icon" href="/favicon.svg">`；`GET /favicon.svg → 200 image/svg+xml`；首屏 `badResponses: []`、`consoleErrors: []`（旧构建的 2 条 404 消失）。

## 7.2 12 项清单复跑

| # | 检查 | 结果 | 证据 |
|---|---|---|---|
| 1 | 入口 + 空态动作 | PASS | 头部 `添加连接器` + 空态内同名按钮（`r2a.entry`） |
| 2 | 成功连接 | PASS | 行 `已连接`、`已发现 1 个工具`；`mcp.reload` 请求/应答含 `tools:1,state:connected`；`/api/capabilities` 一致（`r2a`） |
| 3 | 失败连接 | PASS | `9939` 关闭端口 → `data-state="error"`、`连接失败`、原文 `dial tcp 127.0.0.1:9939: connect: connection refused`（`r2a`） |
| 4 | 持久化 | PASS | 刷新保持；**重启网关后**（两次独立重启，epoch `734d117f4f9e7a7f` / `9397a319a724dd4a`）`ok1` 自动重连 `connected: 1 tool(s)`、`bad1` 仍 failed、停用状态也保持（`r2b/r2c/r2o`：`[toolkit] mcp "ok1" connected: 1 tool(s), protocol 2025-06-18`） |
| 5 | 停用/启用 | **PASS** | 三方一致 + 重启后仍停用 + 启用恢复 `tools:1`（`r2b/r2c/r2n`） |
| 6 | 编辑 | PASS | 回填（含 stdio 的 command/args/env/timeout）、改 URL、改超时（4500/6000，清 0 删字段）、http→stdio 失败可读、改回 http 恢复、改名（`r2i`） |
| 7 | 删除 | PASS | 确认框 `删除连接器「ok1」？连接会立即断开。`；删除后 DOM/`mcp.servers`/`/api/capabilities` 三处消失；**工具总数 1 → 0**；无僵尸行（`r2g/r2m`） |
| 8 | 校验 5 例 | PASS | 5 例全部：错误文案 + 弹窗不关 + `settings.set` **0** + `mcp.reload` **0** + `mcp.servers`/`mcp.disabledServers` 均未变（`r2i.validation`） |
| 9 | 与设置页一致 | PASS（残留见 N3） | 表示一致；两行只读 + 指引；连接器页不再受"设置页脏值"影响（因为不可能再从 UI 产生脏值） |
| 10 | Esc / 焦点 | PASS | Tab：`#name`→`#transport`→`#url`→`#timeoutMs`→`放弃更改`→`保存`；Esc 关弹窗、URL 不变、tab 仍 active；Popconfirm Esc 关闭（`r2i/r2g`） |
| 11 | 390 + 深色 | PASS | 390：`docScroll 390 == client 390`、3 行 `340==340`、9 个按钮 48×24 无裁切；深色 `data-theme=dark`：点 success 8.25 / error 6.8 / neutral 8.15、错误文字 9.9、状态标签 8.15；浅色 6.47 / 6.07 / 4.83（`r2j`） |
| 12 | 并发/边界 | PASS（新边界见 N1/N2） | 快速双击保存 = 1 行 1 条 `settings.set`；`我的 服务 🚀` 可保存并连接；同名重复被拒 | 

## 7.3 新增缺陷（本轮复验发现）

### N1（P2）改名会静默丢掉「已停用」，并留下孤儿停用名单；新连接器可能"出生即停用"
- 复现：`disable "switchme"` → 编辑改名为 `sw2` → 保存。
  - 停用名单仍是 `["switchme"]`（`disabled` 键原样），而配置行已叫 `sw2` → **该行的停用状态凭空消失**（改名即等于启用），用户没有任何提示。
  - 名单里的 `switchme` 变成孤儿（没有对应行），且不会被回收（对比：删除行会清）。
- 后果：再新建一个名为 `switchme` 的连接器 → 行渲染为 `已停用`（`r2l2.orphanAdd`），同时弹出 **误导性** 提示 `「switchme」没有生效：同名或同地址的连接器已存在，网关会跳过重复项。`（真实原因是被孤儿停用名单命中，不是同名/同地址）。
- 位置：`web/src/features/capabilities/capabilities-screen.tsx`（`submit()` 改名时未同步 `disabledNames`；`toggle/remove` 才维护）＋ 网关只按名字存停用名单（`internal/gateway/methods.go` `reloadConnectors` 写 `MCPDisabledServersSettingKey`）。
- 建议：改名时把停用名单里的旧名替换成新名（`disabled.map(n => n === editing.name ? row.name : n)`），并在 `persist` 里一并提交；或后端按"名字+URL"身份存停用，并在写名单时剔除不存在的名字（GC）。

### N2（P3）F5 的"没有生效"提示是一次性的，且与"已保存并重新连接"同时出现
- 现象：横幅出现后，下一次任何操作（甚至再点一次保存/刷新）就会被 `setError(null)` 清掉，行又回到"永远空闲"的观感；同屏还有绿色 `已保存并重新连接`，两条互相矛盾。
- 建议：把"未生效"做成行内持久标记（如行上 `data-state="skipped"` + 一行灰字原因），或至少在 `mcp.reload` 回应里给该行 `state=failed, error="duplicate endpoint, skipped"`，让错误落在行内（与 F3 的"失败行显示 provider 原文"同一套机制）。

### N3（P3）带外脏值仍会静默清空（F7 的残留面）
- 现象：用协议（绕过 UI）把 `mcp.servers` 写成 `{"oops":`，连接器页仍显示空态且**无任何 alert**；点 `刷新` 后 `/api/capabilities` 的 `connectors` 变 `[]`（连接被真的丢弃）。
- 说明：UI 已无法产生这种值（F7 只读），所以原缺陷路径已关闭；但用户手改 `settings.json` 或旧版本残留仍会踩到。
- 建议：连接器页对 `parseConnectors` 失败给出 `role=alert`（"mcp.servers 不是合法 JSON，去 设置 → 数据与隐私 查看"），并禁止在解析失败时把行列表当成"空"来渲染。

### N4（P3）每次"添加"会连续两次 `mcp.reload`
- 现象：`submit()` 里 `persist()` 触发一次 reload，紧接着为了 F5 检查又调一次 `reloadConnectors()`；一次添加产生 2 个 `mcp.reload` 请求、日志出现两轮 connect（`r2a.addFrames` 里 id 11、12）。功能正确，但每次添加都把 MCP 连接重建两次（对 stdio/远端服务器是双倍握手开销）。
- 建议：直接用 `persist()` 里那次 reload 的返回结果做"未生效"判定（把结果从 `persist` 返回），不要二次 reload。

### N5（P4）F3 文案与实际不符
- 见 §7.1-F3：文案说"重启网关即可被列出"，实测无需重启。

## 7.4 复验截图（`findings3/audit-conn-r2-*.png`）

| 文件 | 内容 |
|---|---|
| `r2-01/02` | 连接器空态、技能空态（新文案） |
| `r2-03/04` | 连接成功（1 工具）、失败（9939 原文） |
| `r2-05/06/07/08` | 停用 → 刷新保持 → **重启后仍停用** → 启用恢复 |
| `r2-09/10/11/12` | 技能表：现状（来源列 1103px、表格 2384 横向滚动）、两行对比、**诊断：去掉 max-content 后 160/768/200 + 省略生效** |
| `r2-13/14/15` | 同名被拒、自改名不误报、改名撞名被拒 |
| `r2-16` | 同 URL → `没有生效` 横幅 |
| `r2-17/18` | 设置页两行只读 + 指引；带外脏值后的空态 |
| `r2-19` | 删除后（无僵尸行） |
| `r2-20/21` | 干净首屏（0 个 4xx）；脏值全量视图 |
| `r2-22/23` | stdio 失败行、改名后 |
| `r2-24` | Esc 后仍在连接器 tab |
| `r2-25/26/27/28` | 桌面浅色、390 浅色、桌面深色、390 深色 |
| `r2-29/30` | 双击保存 + emoji 名；强制同名两行删除后无残行 |
| `r2-31..39` | unicode 连接成功、孤儿停用名、改名丢停用、删除已连接（工具 1→0）、最终态 |

## 7.5 仍需补的验收点（复验后）

1. **F2 的修法需重跑**：去掉 `scroll={{x:'max-content'}}` 后，请用**同一条夹具**（`<workspace>/skills/<81 字符目录名>/SKILL.md`）复测：来源列应 ~200px 且 `scrollWidth > clientWidth`、表格宽 == 容器、名称列 160px。
2. **改名与停用的联动**（N1）：改名后停用名单应跟随；孤儿名字应有 GC；"出生即停用"的误导文案需修正。
3. **停用的真实后果**：修好之后要验"工具确实从模型可见工具集消失"（需一次真实 run 的 tool 列表/工具调用；本环境只有 fakemodel，未跑）。
4. **多工作区隔离**：`mcp.disabledServers.<ws>` 的隔离意图仍只在一个工作区验证过；需要两个工作区各停用不同连接器互不影响。
5. **带外脏值的提示**（N3）：连接器页对 `mcp.servers` 解析失败应给 `role=alert` + 去处指引。
6. **超时语义**：只验了写入/清除，未验"卡死端点在 `timeoutMs` 后变 failed"（需要只接连接不回包的夹具）。
7. **OAuth / headers 连接器**：表单仍不暴露 `headers`/`auth`，新增路径无法表达（编辑能保留）。
8. **真实 stdio MCP 服务器成功路径**（本轮只有"命令不存在"的失败路径）。
9. **`mcp.reload` 二次调用**（N4）去掉后回归一次添加/编辑/删除。

---

# 8. 第三轮复验（构建 `16efc266` / bundle `edc719bb7c8c`）

- 二进制 `.cache/bin/zeokeel-conn3`（built 00:34）；**bundle 自证**：实例日志 `gateway: web client bundle sha256 edc719bb7c8c (6 files, built 2026-11-24T16:33:57Z from web/dist)`；另从服务端取回 `assets/index-CFPI0JPv.js` / `index-D4EAsoyE.css` 逐字符确认本轮修复**在产物里**（`zk-skills-table`×1、`connectors-broken`×1、`connectors-reset`×1、`brokenHint`×3；CSS `.zk-skills-table{width:100%}` + `.zk-skills-table table{table-layout:fixed;width:100%}`）。注意：构建时间 16:33:57Z **早于** 提交时间 16:37:17Z，所以"用时间戳对版本"不可靠，这里用内容比对。
- 实例：`startB.sh conn3-audit ... 9927 9928`，data dir `data-conn3-audit`，端口 **9927**（`gateway.json` 协商值）；MCP 夹具 9911、9933；失败端口用已验证关闭的 9939。
- 脚本 `findings3/r3a..r3j*.mjs` + `r3a..r3j.json`；截图 `findings3/audit-conn-r3-*.png`。

## 8.0 结论

| 项 | 结果 |
|---|---|
| **F2 技能表** | **PASS @1440/1024/768**（表宽==容器、来源列 `scrollWidth>clientWidth` 省略生效、名称列 160）／**FAIL @390**（说明列被压成 **0px**，行高 **2943–4021px**，表 360px 溢出 342px 容器） |
| **N1 改名** | **PASS**（`disabled` 跟随为 `["beta"]`、行仍"已停用"、新 `alpha` 不"出生即停用"、删除后无孤儿）——但**新增误报**：改名一个已停用连接器会弹"没有生效"（见 R3-2） |
| **N2 单一结论** | **PASS**（只有红色"没有生效"，无绿色"已保存"；非保存操作后仍保留；下一次保存/刷新/换页消失） |
| **N3 损坏态** | **PASS**（`role=alert` + "恢复为空列表"；`not json`/`[null]`/半损坏三种都识别；恢复后可正常添加；页面刷新不再清空活连接）→ 残留：损坏时点**「刷新」按钮**仍会 `mcp.reload` 把活连接丢掉（有告警，不静默；见 R3-3） |
| **N4 单次 reload** | **PASS**（一次添加只有 1 条 `mcp.reload`，id 10→11 连号） |
| **N5 文案** | **PASS**（不再说"重启"，"放进新技能后刷新即可被列出"与实测一致）→ 残留：文案仍写"数据目录下的 skills/"，而该目录在有工作区时**不会被扫描**（见 R3-4） |
| **回归** | 12 项清单 + F1/F3/F4/F5/F6/F7/F8 全跑：**全 PASS**（唯一失败项仍是 F2@390） |

**第三轮判定：FAIL（1 项：#390 的 F2；另 1 个新增 P2 误报 + 3 个 P3 残留）。**

## 8.1 F2 四档实测（浅色 + 深色）

| 视口 | tableLayout | 表宽 / 容器 | 名称/说明/来源 列宽 | 来源 `scroll>client` | 行高（长名 / 短名） | 判定 |
|---|---|---|---|---|---|---|
| 1440 light | fixed | **1128 / 1128** | 160 / 768 / 200 | **1087 > 184 ✓** | 127 / 61 | 省略✓ 表宽✓ 行高✗(>60) |
| 1024 light | fixed | **712 / 712** | 160 / 352 / 200 | 1087 > 184 ✓ | 127 / 83 | ✓ |
| 768 light | fixed | **720 / 720** | 160 / 360 / 200 | 1087 > 184 ✓ | 127 / 83 | ✓ |
| **390 light** | fixed | **360 / 342 ✗** | **160 / 0 / 200 ✗** | 1087 > 184 ✓ | **4021 / 2613 ✗** | **FAIL** |
| 1440 dark | fixed | 1128 / 1128 | 160 / 768 / 200 | ✓ | 127 / 61 | 同浅色 |
| **390 dark** | fixed | **360 / 342 ✗** | **160 / 0 / 200 ✗** | ✓ | **4021 / 2613 ✗** | **FAIL** |

截图：`audit-conn-r3-f2-1440-light.png`、`-1024-light`、`-768-light`、`-390-light`、`-1440-dark`、`-390-dark`。390 的那张能直接看到说明列消失、文字一列一字往下排（单行高达 4 千像素）。

### 8.1.1 最小可复现 DOM/CSS 片段（390 视口，`data-conn3-audit`）

```html
<!-- 容器 342px（390 - 2×24 内边距） -->
<div class="ant-table-wrapper zk-skills-table">        <!-- computed width: 342px -->
  <div class="ant-table-container"><div class="ant-table-content">
    <table style="table-layout: fixed;">               <!-- computed width: 360px  ← 比容器宽 18px -->
      <colgroup>
        <col style="width: 160px;">                    <!-- 名称 -->
        <col>                                          <!-- 说明：无 width → 拿到剩余 0px -->
        <col style="width: 200px;">                    <!-- 来源 -->
      </colgroup>
      <thead>
        <tr><th>名称</th><th>说明</th><th>来源</th></tr> <!-- th 实测宽度：160 / 0 / 200 -->
      </thead>
      <tbody>
        <tr class="ant-table-row">                     <!-- 实测高度 2943px（长名）/ 2613px -->
          <td style="width:160px">…</td>
          <td style="width:0">A<br>s<br>k<br>…</td>     <!-- 0px 宽 → 每行一个字符 -->
          <td style="width:200px"><span class="zk-connector-path" title="…">/data/…</span></td>
        </tr>
      </tbody>
    </table>
  </div></div>
</div>
```
```css
/* 当前实现（web/src/design/global.css:1372-1379） */
.zk-skills-table { width: 100%; }
.zk-skills-table table { table-layout: fixed; width: 100%; }
/* 列宽来自 capabilities-screen.tsx: name width:160（固定 px）、source width:200（固定 px）、description 无 width */
```
**根因**：`table-layout: fixed` 下固定列宽之和 160 + 200 = 360 > 容器 342，浏览器把"自动列"（说明）压到 0；行内文字按字符折行 → 行高 2943px。

**已验证的最小修法**（仅改内联 `<col>` 宽度做的诊断，产品代码未改）：把固定像素改成百分比
```js
cols[0].style.width = '38%';   // 名称
cols[2].style.width = '34%';   // 来源
```
结果：`表宽 342 == 容器 342`，列宽 `130 / 96 / 116`，来源 `1087 > 100`（省略生效），行高从 2943 → 413。
建议正式修法二选一：
1. 列宽用相对值（`name: '38%'`、`source: '34%'`，或 `width: 160` 只对 ≥md 生效、窄屏走百分比）；
2. 给窄屏加媒体查询，或保留 `scroll={{x: 560}}`（此时表宽会大于容器、内部横向滚动，属于另一套可接受模式，但需在验收里改口径）。
另：行高 < 60 的判据在任何视口都未达成（127px），因为**名称列本身没有省略**——建议名称列也 `ellipsis: {showTitle:true}`（或 `Typography.Text ellipsis`），否则 81 字符的名字在 160px 里必然折 5 行。截图 `audit-conn-r3-f2-390-minimal-dom.png`、`audit-conn-r3-f2-390-after-percent-patch.png`。

### 8.1.2 视口扫描：退化从 ~500px 就开始了

同一份技能夹具（长名 + 长路径），只改视口宽度：

| 视口 | 容器 | 表宽 | 名称/说明/来源 | 长名行高 | 判定 |
|---|---|---|---|---|---|
| 1440 | 1128 | 1128 | 160 / 768 / 200 | 127 | 可读 |
| 1024 | 712 | 712 | 160 / 352 / 200 | 127 | 可读 |
| 768 | 720 | 720 | 160 / 360 / 200 | 127 | 可读 |
| 600 | 552 | 552 | 160 / 192 / 200 | **171** | 已明显变高 |
| 560 | 512 | 512 | 160 / 152 / 200 | **237** | 退化 |
| 520 | 472 | 472 | 160 / 112 / 200 | **325** | 退化 |
| 500 | 452 | 452 | 160 / 92 / 200 | **413** | 退化 |
| 480 | 432 | 432 | 160 / **72** / 200 | **545** | 退化 |
| 420 | 372 | 372 | 160 / **12** / 200 | **2943** | 严重 |
| 411 | 363 | 363 | 160 / 3 / 200 | 2943 | 严重 |
| 405 | 357 | **360** | 160 / **0** / 200 | 2943 | 表宽溢出容器 |
| 390 | 342 | **360** | 160 / **0** / 200 | 2943–4021 | **FAIL** |
| 360 | 312 | **360** | 160 / **0** / 200 | 2943 | FAIL |
| 320 | 272 | **360** | 160 / **0** / 200 | 2943 | FAIL |

阈值：`说明列宽 = 视口宽 − 48（页面内边距） − 160（名称） − 200（来源）`；视口 < 408 时说明列为 0 且表格开始比容器宽；视口 < 768 起说明列就一路变窄（600px 已让行高 171px，500px 时 413px）。截图 `audit-conn-r3-f2-w600 / w560 / w520 / w500 / w480 / w420 / w411 / w410 / w405 / w400 / w390 / w360 / w320.png`。

## 8.2 N1–N5 复验证据

### N1 改名 —— PASS（+ 1 个新增误报）
```
停用 alpha → /api/capabilities {"connectors":[],"disabled":["alpha"]}
改名为 beta → settings.delta {"mcp.disabledServers.ws-…":"[\"beta\"]"}
             /api/capabilities {"connectors":[],"disabled":["beta"]}    ← 跟上了，不再是 ["alpha"]
             行：beta | http | 已停用                                      ← 仍是停用态
新建 alpha（9933）→ 行：alpha | http | 已连接；banner {error:null, notice:"已保存并重新连接"}   ← 不再"出生即停用"、无误导提示
删除 alpha → settings.delta 的 mcp.servers 已不含 alpha；disabled 仍 ["beta"]（beta 是真实行，不是孤儿）
删除 beta（停用行）→ disabled → "[]"                                     ← 无孤儿
```
**R3-2（P2，新问题）** 改名一个**已停用**的连接器时，页面弹出
`「beta」没有生效：同名或同地址的连接器已存在，网关会跳过重复项。`
—— 这是误报：beta 正是按用户意愿被停用的，`mcp.reload` 的 connectors 里当然没有它。`submit()` 里的"未生效"判定（`capabilities-screen.tsx` 的 `if (result && !result.connectors.some(...))`）**没有排除 `result.disabled` 里的名字**。
建议：`if (result && !result.disabled.includes(row.name) && !result.connectors.some(...))`。截图 `audit-conn-r3-n1-renamed-disabled.png`。

### N2 单一结论 —— PASS
- 同 URL 添加后：`errorCount=1, noticeCount=0`（只有红色"没有生效"）。
- 再做一次非保存操作（打开弹窗→Esc）：错误仍在。
- 下一次保存/刷新/换页：消失。
截图 `audit-conn-r3-n2-dup-url.png`。

### N3 损坏态 —— PASS（+ 残留）
```
mcp.servers = not json
  → [data-testid=connectors-broken] role="alert"
     "连接器配置不是合法 JSON，已停止渲染。 / 可以恢复为空列表后重新添加；原始内容不会自动丢弃。 / [恢复为空列表]"
  → 此刻 /api/capabilities 仍 connectors: live1@1:connected, live2@1:connected（页面刷新不清空活连接 ✓）
mcp.servers = [null]            → 同样显示损坏态 ✓
mcp.servers = [{keepme…}, null] → 损坏态 + keepme 那一行仍渲染（不崩）✓
点「恢复为空列表」 → mcp.servers="[]"、disabled=[]、告警消失、空态出现 → 再添加 afterreset → 已连接 ✓
```
**R3-3（P3）** 损坏态下点「刷新」按钮仍会发 `mcp.reload`：`/api/capabilities.connectors` 从 `[live1@1, live2@1]` 变成 `[]`（工具连接真的被丢弃）。因为告警同时可见，不算"静默"，但建议损坏态下禁用「刷新」或先要求恢复。截图 `audit-conn-r3-n3-*.png`。

### N4 单次 reload —— PASS
```
一次添加的帧：settings.set(id=10) → mcp.reload(id=11)，reloadCount=1，id 连号（10→11）
```

### N5 文案 —— PASS（+ 残留）
空态文案（技能为 0 时实测）：
`没有加载技能。 / 技能来自宿主的技能目录（仓库内 .agents/skills 或数据目录下的 skills/）；放进新技能后刷新即可被列出。`
（`/重启/` 命中 = false ✓）。**R3-4（P3）**：后半句"数据目录下的 skills/"在有工作区时**不会被扫描**——本轮实测：`<dataDir>/skills/datadir-skill` 未出现在列表里，而 `<workspace>/skills/*` 和 `<workspace>/.agents/skills/*` 都在（`listed 2 skills from 3 roots`；随后 3 个技能全部列出）。建议文案改为"工作区内的 skills/、.agents/skills、.zeokeel/skills"。

### 技能到底从哪加载（回答 lead 的问题）
`internal/modules/skills/skills.go:262-278`：**打开工作区时 `Roots()` 直接 return 工作区根下的三个目录**，全局根（settings 的 dirs、dataDir/skills、`$ZEOKEEL_SKILLS`、`~/.zeokeel/skills`）**不再扫描**。所以：
- ✅ 可用：`<dataDir>/workspace/skills/<name>/SKILL.md`（产品约定）
- ✅ 可用：`<dataDir>/workspace/.agents/skills/<name>/SKILL.md`（DSH 约定）
- ✅ 可用：`<dataDir>/workspace/.zeokeel/skills/<name>/SKILL.md`（rig 约定）
- ❌ 不生效：`<dataDir>/skills/<name>/SKILL.md`（只有**没有**工作区时才扫；本轮实测未被列出）
- 无需重启：放进目录后刷新页面/重新请求 `GET /api/capabilities` 即可列出（日志 `skills: listed 2 skills from 3 roots`）。

## 8.3 12 项清单 + 旧缺陷回归（全跑）

| # | 检查 | 结果 | 证据 |
|---|---|---|---|
| 1 | 入口/空态 | PASS | 头部 `添加连接器`；空态含同名按钮 |
| 2 | 成功连接 | PASS | `fixture 已连接` + `已发现 1 个工具`；`/api/capabilities state=connected`；仅 1 次 reload |
| 3 | 失败连接 | PASS | `9939` → `连接失败` + `dial tcp … refused` 原文 |
| 4 | 持久化 | PASS | 刷新 + **重启网关**（epoch `cdad5e3723df9da8`）后：ok-conn 已连接、bad-conn 失败、off-conn 已停用；启用后 ok/off 都 `tools:1` |
| 5 | 停用/启用 | PASS | `disabled:["fixture"]` + connectors 无它；启用回 `fixture@1:connected` |
| 6 | 编辑 | PASS | 回填正确；http→stdio 失败可读（`executable file not found in $PATH`）；改回 http 恢复 |
| 7 | 删除 | PASS | 确认框含名称；三处消失；工具总数 **1 → 0**；无僵尸行 |
| 8 | 校验 5 例 | PASS | 5 例全部：错误文案 + `settings.set` 0 + `mcp.reload` 0 + 磁盘未变 |
| 9 | 设置页 | PASS | 两行 `disabled=true` + "（在对应页面修改）"；损坏态有告警与恢复动作 |
| 10 | Esc/焦点 | PASS | Tab `name→transport→url→timeout→放弃更改→保存`；Esc 只关弹窗，仍在 `?tab=connectors` |
| 11 | 390/深色 | PASS | 390：`390==390`、3 行 `340==340`、0 个按钮裁切；深色 success 8.25 / error 6.8 / neutral 8.15 / 错误文字 9.9 / 状态标签 8.15 |
| 12 | 并发/边界 | PASS | 双击保存 = 1 行 1 条 set；`我的 服务 🚀` 可用；同名被拒（0 写入、弹窗不关） |
| F1 | 停用/启用三方一致 | PASS | `mcp.reload {disabled:[…]}` → recv `disabled` 与 `/api/capabilities.disabled` 与 DOM 一致 |
| F3 | 技能空态文案 | PASS | 见 N5 |
| F4 | 同名/僵尸行 | PASS | 同名拦截；同名注入后删除无禁用按钮残行 |
| F5 | 同 URL 提示 | PASS | 见 N2 |
| F6 | rejection | PASS | 全程 `consoleErrors: []`、`pageerror: 0` |
| F7 | 设置页只读 | PASS | 见 #9 |
| F8 | favicon | PASS | `/favicon.svg → 200 image/svg+xml`；首屏 `badResponses: []` |

## 8.4 第三轮新增/残留问题

| 编号 | 级别 | 现象 | 建议 |
|---|---|---|---|
| **R3-1** | **P1** | 390 视口技能表：说明列 0px、行高 2943–4021px、表 360 > 容器 342（固定列宽 160+200 超容器） | 列宽改百分比 / 窄屏媒体查询 / 保留 `scroll.x` 并接受内部滚动 |
| **R3-2** | P2 | 改名一个"已停用"连接器 → 误报"「新名」没有生效：同名或同地址…" | notApplied 判定排除 `result.disabled` |
| **R3-3** | P3 | `mcp.servers` 损坏时点「刷新」仍 `mcp.reload`，活连接被丢弃（有告警不静默） | 损坏态禁用刷新，或先恢复再 reload |
| **R3-4** | P3 | 技能空态文案把"数据目录下的 skills/"列为来源，但有工作区时该目录不被扫描 | 文案改为工作区三目录 |
| **R3-5** | P4 | 名称列无省略：81 字符名字在 160px 里折 5 行（行高 127px，未达"<60"） | 名称列 `ellipsis`/两行截断 |

## 8.5 第三轮截图（`findings3/audit-conn-r3-*.png`）

`-f2-1440-light`、`-f2-1024-light`、`-f2-768-light`、`-f2-390-light`（损坏画面）、`-f2-1440-dark`、`-f2-390-dark`、`-f2-390-minimal-dom`、`-f2-390-after-percent-patch`（诊断修复后）、`-n1-renamed-disabled`（误报横幅）、`-n1-new-alpha`、`-n1-after-deletes`、`-n2-dup-url`、`-n3-not-json`、`-n3-not-json-after-refresh`、`-n3-partial`、`-n3-after-reset`、`-n3-add-after-reset`、`-n5-skills-empty`、`-reg-add/-failed/-disabled/-stdio/-delete/-settings/-390-light/-1440-dark/-390-dark`、`-trio-1440/-390-light/-390-dark`、`-after-restart`。

## 8.6 仍需补的验收点（第三轮后）

1. **F2@390 修完后重跑四档**（判据：表宽==容器、说明列 > 0、来源 `scroll>client`、行高 < 60 或明确改口径）。
2. **R3-2 误报**：改名停用连接器不应弹"没有生效"。
3. **技能加载路径文档化**：把"工作区内三目录、dataDir/skills 仅无工作区时生效"写进用户文档（以及空态文案）。
4. **损坏态与 reload 的相互作用**（R3-3）：损坏时不应允许把活连接丢掉。
5. 前两轮遗留仍未覆盖：停用后"工具是否真的从模型可见集消失"（需真实 run）、多工作区隔离、OAuth/headers 连接器、真实 stdio MCP 成功路径、`timeoutMs` 对卡死端点的语义。

---

# 9. 第四轮复验（构建 `68693ba` / bundle `53d93d927a60`）

- 二进制 `.cache/bin/zeokeel-conn4`；**bundle 自证**：实例日志 `web client bundle sha256 53d93d927a60 (6 files, built 2026-11-24T16:51:41Z)`，并从服务端取回 `assets/index-CU2KtFXd.js` / `index-tVImSUCu.css` 逐字符确认本轮改动在产物里：`38%`×2、`34%`×3、`技能来自工作区根下`×1，CSS `.zk-skills-table td,.zk-skills-table th{text-overflow:ellipsis;white-space:nowrap;overflow:hidden}`。（提交时间 16:52:33Z 又晚于构建 16:51:41Z，仍以内容比对为准。）
- 实例：`startB.sh conn4-audit ... 9930 9931`，data dir `data-conn4-audit`，端口 **9930**；MCP 夹具 9911；失败端口 9939（已验证关闭）。
- 技能夹具（三档）：`<dataDir>/workspace/skills/a-skill-name-that-is-exactly-eighty-one-characters-long-for-the-round-four-audit-x`（**82 字符名 / 146 字符绝对路径**）、`…/a-skill-directory-name-that-is-deliberately-absurdly-long-…-one-line`（**168 字符名 / 232 字符路径**）、`…/skills/short`（5 字符名，对照），外加 `<workspace>/.zeokeel/skills/rig-skill` 验证第三个根。
- 脚本 `findings3/r4a..r4f*.mjs` + `r4a..r4f.json`；截图 `findings3/audit-conn-r4-*.png`。

## 9.0 结论：**PASS**（F2 全档通过，R3-2/R3-3/R3-4/R3-5 全部修好，回归全绿）

| 项 | 结果 |
|---|---|
| **F2 全视口** | **PASS**：11 档（1440/1280/1024/900/768/600/560/480/390/360/320）× 浅色 + 4 档深色，**每档 `table.width == 容器宽`、说明列 76–316px（>0）、行高恒 39px（<60）、来源内层 `span.clientWidth < scrollWidth`（省略生效）**；两种夹具（82 字符 / 168 字符名 + 232 字符路径）都测 |
| **R3-2 改名不再误报** | **PASS**：停用 → 改名 → **无"没有生效"**、仍是"已停用"；连改两次名、改 URL 都不误报；真正的同 URL 场景仍会报（F5 未被削弱） |
| **R3-3 损坏态禁用刷新** | **PASS**：`mcp.servers` 坏值时 `刷新` 按钮 `disabled=true`，强点也不触发 reload（`/api/capabilities` 前后一致，活连接不丢）；`恢复为空列表` 后按钮可用、连接可重建 |
| **R3-4 空态文案** | **PASS**：`技能来自工作区根下的 skills/、.agents/skills、.zeokeel/skills（没有打开工作区时用数据目录下的 skills/）`；实测三个工作区根都被扫（4 个技能，含 `.zeokeel/skills/rig-skill`），`dataDir/skills` 在有工作区时不扫 |
| **R3-5 长名单行省略** | **PASS**：82 字符与 168 字符名在 1440/768/390/320 都是单行（行高 39px）、`cell.clientWidth < scrollWidth`、`title` 为全名 |
| **回归** | 12 项清单 + F1/F3–F8 + N1–N5 **全 PASS**（详见 §9.3） |
| **新增残留** | **R4-1（P2）**：说明列现在也是"单行 + 省略号"，但**没有 `title`**，被截断的说明文字无处可读（1440 只显示约 40% 内容，390 只显示约 10 个字符） |

## 9.1 F2 全视口实测（浅色）

判据改为查**内层 span**（`Typography.Text`）：`clientWidth < scrollWidth` 且 `text-overflow: ellipsis`；不再用 `cell.scrollWidth > clientWidth`。

| 视口 | 容器=表宽 | 名称/说明/来源 | 行高（3 行） | 来源内层 cw<sw | 名称 title | 判定 |
|---|---|---|---|---|---|---|
| 1440 | **1128 == 1128** | 429 / 316 / 384 | 39 / 39 / 39 | 368<1671, 368<1051, 368<497 | ✓ | PASS |
| 1280 | **968 == 968** | 368 / 271 / 329 | 39 | 313<1671… | ✓ | PASS |
| 1024 | **712 == 712** | 271 / 199 / 242 | 39 | 226<1671… | ✓ | PASS |
| 900 | **852 == 852** | 324 / 239 / 290 | 39 | 274<1671… | ✓ | PASS |
| 768 | **720 == 720** | 274 / 202 / 245 | 39 | 229<1671… | ✓ | PASS |
| 600 | **552 == 552** | 210 / 155 / 188 | 39 | 172<1671… | ✓ | PASS |
| 560 | **512 == 512** | 195 / 143 / 174 | 39 | 158<1671… | ✓ | PASS |
| 480 | **432 == 432** | 164 / 121 / 147 | 39 | 131<1671… | ✓ | PASS |
| 390 | **342 == 342** | 130 / 96 / 116 | 39 | 100<1671… | ✓ | PASS |
| 360 | **312 == 312** | 119 / 87 / 106 | 39 | 90<1671… | ✓ | PASS |
| 320 | **272 == 272** | 103 / 76 / 92 | 39 | 76<1671… | ✓ | PASS |

深色：1440（1128==1128）、768（720==720）、390（342==342）、320（272==272），列宽与行高同浅色，全部 PASS。
`document.scrollWidth == clientWidth` 在每一档都成立（页面无横向滚动）。截图 `audit-conn-r4-f2-w{1440,1280,1024,900,768,600,560,480,390,360,320}.png`、`audit-conn-r4-f2-dark-w{1440,768,390,320}.png`。

> 与上一轮对比：r3 的 390 是 `说明列 0px / 行高 2943px / 表 360>342`；本轮 390 是 `说明 96px / 行高 39px / 表 342==342`。修复成立。

## 9.2 R3-2 ~ R3-5 证据

### R3-2 改名不误报 —— PASS
```
停用 alpha → disabled ["alpha"]，行：alpha | 已停用
改名 alpha→beta → banner {error:null, notice:"已保存并重新连接"}；行 beta | 已停用；disabled ["beta"]；/api/capabilities.disabled ["beta"]
再改 beta→gamma → 同上，仍无 error
改 URL（gamma→9933，仍停用）→ banner {error:null}；servers 里 URL 已更新；行仍 已停用
启用 gamma 后用同 URL 新建 dup → banner {error:"「dup」没有生效：同名或同地址的连接器已存在，网关会跳过重复项。", notice:null}   ← 真场景仍能报，未被一并静音
```
截图 `audit-conn-r4-r3-2-renamed-disabled.png`、`-url-changed-disabled.png`、`-genuine-notapplied.png`。

### R3-3 损坏态禁用刷新 —— PASS
```
mcp.servers = "not json"
  → 告警：连接器配置不是合法 JSON，已停止渲染。/ 可以恢复为空列表后重新添加；原始内容不会自动丢弃。/ [恢复为空列表]
  → 刷新按钮 disabled=true
  → 强制点一次：/api/capabilities 前后完全一致（changed=false），活连接没被丢掉
点「恢复为空列表」→ 告警消失、刷新按钮 enabled、mcp.servers="[]"
点刷新 → 正常；再添加 rebuilt(9911) → rebuilt@1:connected（连接可重建）
```
截图 `audit-conn-r4-r3-3-broken-refresh-disabled.png`、`-after-reset.png`。

### R3-4 空态文案与真实扫描目录一致 —— PASS
空态原文（把 `workspace/skills` 临时移走后测得）：
`没有加载技能。 / 技能来自工作区根下的 skills/、.agents/skills、.zeokeel/skills（没有打开工作区时用数据目录下的 skills/）；放进新技能后刷新即可被列出。`（`/重启/` 命中 false）
实证：恢复后 `skills: 4`，来源分别是 `workspace/skills/*`（3 个）与 `workspace/.zeokeel/skills/rig-skill`（1 个）；`<dataDir>/skills/*` 在有工作区时仍未出现在列表（与文案一致）。截图 `audit-conn-r4-r3-4-skills-empty-hint.png`。

### R3-5 长名单行省略 + title —— PASS (1440/768/390/320)
```
82 字符名：rowH 39，cell cw<sw（429<515 @1440；274<515 @768；130<515 @390），title = 全名
168 字符名：rowH 39，cell cw<sw（429<1049 @1440；…；103<1049 @320），title = 全名
short：rowH 39，不溢出（429==429），title = "short"
```
截图 `audit-conn-r4-r3-5-name-ellipsis-{1440,768,390,320}.png`。

## 9.3 回归（12 项 + F1/F3–F8 + N1–N5）

| # | 检查 | 结果 | 证据 |
|---|---|---|---|
| 1 | 入口/空态动作 | PASS | `添加连接器` + 空态内按钮；`技能 4 / 连接器 0` |
| 2 | 成功连接 | PASS | `fixture 已连接` + 1 工具 + `/api/capabilities connected`；`reloadCount=1`（N4 仍成立：settings.set id10 → mcp.reload id11） |
| 3 | 失败连接 | PASS | `broken 连接失败` + `dial tcp 127.0.0.1:9939: connect: connection refused` |
| 4 | 持久化 | PASS | 重启（epoch `5667a0da70f6590f`）后：ok-conn 已连接（日志 `connected: 1 tool(s), protocol 2025-06-18`）、bad-conn failed、off-conn 已停用 |
| 5 | 停用/启用（F1） | PASS | `disabled:["fixture"]` + connectors 无它 + 磁盘 scoped 键；启用回 `fixture@1:connected` |
| 6 | 编辑 | PASS | 回填正确；超时 4200 落盘；http→stdio 失败可读；改回 http 恢复 |
| 7 | 删除 | PASS | 确认框含名称；三处消失；工具 **1 → 0**；无僵尸行 |
| 8 | 校验 5 例 | PASS | 每例：错误文案 + `settings.set` 0 + `mcp.reload` 0 + 磁盘未变 |
| 9 | 设置页只读 | PASS | 两行 `disabled=true` + "（在对应页面修改）" |
| 10 | Esc/焦点 | PASS | Tab `name→transport→url→timeout→放弃更改→保存`；Esc 只关弹窗，URL/tab 不变 |
| 11 | 390/深色 | PASS | 390：`390==390`、3 行 `340==340`、0 裁切；深色 success 8.25 / error 6.8 / neutral 8.15 / 错误文字 9.9 / 状态标签 8.15（浅色 3.3 / 4.83 / 6.07 / 6.47 / 6.07） |
| 12 | 并发/边界 | PASS | 双击保存 = 1 行（`count=1`）；`我的 服务 🚀` 可用；同名被拒（0 帧、弹窗不关） |
| F3 | 技能空态 | PASS | 见 R3-4 |
| F4 | 同名/僵尸 | PASS | 拦截 + 无禁用按钮残行 |
| F5 | 同 URL 提示 | PASS | 见 R3-2 末段 |
| F6 | rejection | PASS | 全程 `pageerror 0`、`consoleErrors []` |
| F7 | 设置页只读 | PASS | 见 #9 |
| F8 | favicon | PASS | `/favicon.svg 200 image/svg+xml`；首屏 `badResponses []` |
| N1 | 改名带停用 | PASS | 见 R3-2 |
| N2 | 单一结论 | PASS | 同 URL 时 `notice=null`，只有红色结论 |
| N3 | 损坏态 | PASS | 见 R3-3 |
| N4 | 单次 reload | PASS | 见 #2 |
| N5 | 文案 | PASS | 见 R3-4 |

## 9.4 第四轮新增残留

### R4-1（P2）说明列现在被截断且没有 `title`，说明文字读不全
- 现象：`.zk-skills-table td{white-space:nowrap;overflow:hidden;text-overflow:ellipsis}` 对**所有**单元格生效，说明列本身没有 `ellipsis: {showTitle:true}`，所以被截断的说明既没有 `title`、也没有 tooltip：
  - 1440：说明列 316px，`Skill B — 200+ char directory name so the ab…`（约 40% 内容可见，`descClipped=true`、`descTitle=null`）；
  - 390：说明列 96px，只剩 `Skill B — …` 约 10 个字符；
  - 168 字符名那行的说明同样被截断。
- 影响：为了满足"行高 < 60"把说明压成单行，代价是说明文字不可读（此前是可换行读全的）。名称列/来源列都有 `title` 兜底，唯独说明没有。
- 建议（两行改动，任一）：① 给说明列加 `ellipsis: { showTitle: true }`（antd 会写 `title` 并有原生提示），或 ② 允许说明最多 2 行 `-webkit-line-clamp: 2` + `title`，其余情况仍省略。
- 判据建议：把"说明列可读性"也写成断言（`descTitle != null` 或 `descClipped === false`）。

## 9.5 第四轮截图（`findings3/audit-conn-r4-*.png`）

- F2：`f2-w{1440,1280,1024,900,768,600,560,480,390,360,320}`、`f2-dark-w{1440,768,390,320}`
- R3-2：`r3-2-renamed-disabled`、`r3-2-url-changed-disabled`、`r3-2-genuine-notapplied`
- R3-3：`r3-3-broken-refresh-disabled`、`r3-3-after-reset`
- R3-4：`r3-4-skills-empty-hint`
- R3-5：`r3-5-name-ellipsis-{1440,768,390,320}`
- 回归：`reg-empty`、`reg-add`、`reg-delete`、`reg-settings`、`reg-390-light`、`reg-390-dark`、`reg-1440-dark`
- 重启：`after-restart`

## 9.6 仍需补的验收点（第四轮后）

1. **R4-1**：说明列的 `title`/两行截断（P2，唯一新问题）。
2. 长期未覆盖（沿用前几轮）：停用后"工具是否真的从模型可见工具集消失"（需真实 run，本环境只有 fakemodel）、多工作区隔离、OAuth/headers 连接器的新增路径、真实 stdio MCP 成功路径、`timeoutMs` 对卡死端点的语义。
3. 建议把本轮新增的断言固化进自动化：`table.width == wrapper.width`、`说明列 > 0`、`rowHeight < 60`、`source span cw < sw`、`name/desc title != null`、`刷新 disabled when broken`。

---

# 10. 第五轮复验（构建 `b9f8bf0` / 二进制 `zeokeel-sess3`）

聚焦复验：R4-1、构建指纹、回归抽样、技能表残留 DOM。实例 `startB.sh sess3-audit ... 9935 9936`，data dir `data-sess3-audit`，端口 **9935**；MCP 夹具 9911，失败端口 9939。脚本 `r5a..r5d*.mjs` + `r5a..r5d.json`；截图 `audit-conn-r5-*.png`。

## 10.0 结论：**PASS（全绿）**

| # | 检查 | 结果 |
|---|---|---|
| 1 | R4-1 说明列可读 | **PASS**：1440/1024/768/390/320 五档、三行技能，**说明单元格 `title` 与 `/api/capabilities` 的完整 description 逐字符相等**；行高恒 39px；名称/来源 `title` 仍在（名称=全名，来源=232/146/69 字符路径） |
| 2 | 构建指纹可核对 | **PASS**：`gateway.json.bundleSha256` 与我用**构建脚本同款算法**在 `internal/gateway/webclient/dist` 上重算的摘要一致；且 `GET /` 引用的两个 asset、index.html、favicon.svg、js.map **逐字节等于**该目录（5/5 sha256 MATCH） |
| 3 | 回归抽样 | **PASS**：F2 五档（表宽==容器、行高 39、无横向溢出）；停用→改名不误报；`[null]` 损坏态 `刷新` disabled 且活连接不动；390 无溢出、0 裁切；console 0 error、0 个 4xx |
| 4 | 技能表残留 DOM | **PASS**：技能 0 时 `.zk-skills-table` / `.ant-table` / `tbody tr` 均为 **0**，只有 Empty + 新文案；`mcp.servers` 损坏时连接器页同样 0 表格 + 告警；恢复后**恰好 1 个**表格（不叠加） |

## 10.1 R4-1：说明列 `title`（五档 × 三行）

| 视口 | 容器=表宽 | 说明列宽 | 行高 | 说明 `title` == API description | 名称 title | 来源 title 长度 |
|---|---|---|---|---|---|---|
| 1440 | 1128==1128 | 316 | 39 | ✅ ×3（165/75/51 字符） | ✅ | 232 / 146 / 69 |
| 1024 | 712==712 | 199 | 39 | ✅ ×3 | ✅ | 同上 |
| 768 | 720==720 | 202 | 39 | ✅ ×3 | ✅ | 同上 |
| 390 | 342==342 | 96 | 39 | ✅ ×3（`descClipped=true`，`title` 兜底） | ✅ | 同上 |
| 320 | 272==272 | 76 | 39 | ✅ ×3 | ✅ | 同上 |

取到的实际 `title` 原文（1440，第一行）：
`Skill B — a deliberately long description so the description column has to ellipsize at every viewport width, while the hover title still exposes the whole sentence.`
—— 与 `/api/capabilities` 里该技能的 `description` **完全相同**（脚本按名字前缀配对后逐条 `==` 比较，三行三档全 True）。
截图 `audit-conn-r5-r41-desc-title-{1440,1024,768,390,320}.png`。

## 10.2 构建指纹：我用的核对办法

1. **读指纹**：`gateway.json` →
   `bundleSha256: a6f76797924ddcef2fe3b35a3d854038f747dedb448cbca7d6cd26371f88cc28`、`bundleBuiltAt: 2026-11-24T17:18:55Z`；日志 `gateway: web client bundle sha256 a6f76797924d (6 files, built 2026-11-24T17:18:55Z from web/dist)`。
2. **复刻算法**：读 `scripts/build-web-client.sh:82-84`，摘要 = 对拷入目录里除 `BUNDLE_INFO` 之外的所有文件，`LC_ALL=C sort -z` 排序后逐行 `sha256sum`，再整体 `sha256sum`。我在 `internal/gateway/webclient/dist` 上原样跑了一遍：
   ```bash
   cd internal/gateway/webclient/dist
   find . -type f ! -name BUNDLE_INFO -print0 | LC_ALL=C sort -z | xargs -0 sha256sum | sha256sum | cut -d' ' -f1
   # a6f76797924ddcef2fe3b35a3d854038f747dedb448cbca7d6cd26371f88cc28   ← 与 gateway.json 完全一致
   ```
   （该目录 6 个文件：`index.html`、`favicon.svg`、`assets/index-DyE2-BJJ.js`、`assets/index-DyE2-BJJ.js.map`、`assets/index-sXXcVwAQ.css`、`.keep`。）
3. **证明"服务的就是这个指纹描述的产物"**：从网关逐个下载并比 sha256，5/5 与目录内文件一致：
   ```
   MATCH index.html                            aa674813…
   MATCH favicon.svg                           77800278…
   MATCH assets/index-DyE2-BJJ.js              dccbd8a1…
   MATCH assets/index-sXXcVwAQ.css             7cdd7e8d…
   MATCH assets/index-DyE2-BJJ.js.map          4b848f6d…
   ```
4. **asset 名一致**：`GET /` 返回 `<script src="/assets/index-DyE2-BJJ.js">`、`<link href="/assets/index-sXXcVwAQ.css">`、`<link rel="icon" href="/favicon.svg">`，正是摘要覆盖的那三个文件；`/favicon.svg → 200 image/svg+xml`。

> 结论：现在"这个二进制服务的是哪个前端"可以在**不比对构建时间**的情况下自证：`gateway.json.bundleSha256` ↔ 产物文件内容 ↔ index 引用的 asset 名，三者闭环。建议把它写进验收脚本（一条命令即可复算）。

## 10.3 回归抽样

```
停用 off-conn → {rows: off-conn|已停用, banner:{error:null}}
改名 off-conn→off-renamed → {row: off-renamed|已停用, banner:{error:null, notice:"已保存并重新连接"},
                             disabled:"[\"off-renamed\"]", /api/capabilities.disabled:["off-renamed"]}   ← 无误报、名单跟随
mcp.servers="[null]" → 告警横幅存在、刷新按钮 disabled=true、
                       活连接保持 ok-conn:connected / bad-conn:failed（未被清空）
390 light/dark → document 390==390；连接器行 340==340（2 行）；0 个按钮裁切；console 0 error、4xx 0
```
F2 五档数值见 §10.1（表宽==容器、说明列 76–316、行高 39）。截图 `audit-conn-r5-rename-disabled-no-false-error.png`、`-broken-refresh-disabled.png`、`-390-light.png`、`-390-dark.png`。

## 10.4 技能表残留 DOM（空列表 / 损坏值 / 恢复）

| 状态 | `.zk-skills-table` | `.ant-table` | `tbody tr` | 可见表格 | 其他 |
|---|---|---|---|---|---|
| 技能 = 0（把 `workspace/skills` 移走） | **0** | **0** | **0** | 0 | 只剩 Empty + 新文案（`没有加载技能。 / 技能来自工作区根下的 skills/…`） |
| `mcp.servers = "not json"`（连接器 tab） | **0** | **0** | **0** | 0 | 损坏告警存在；隐藏的技能 pane 里也没有旧表格 |
| 恢复技能（3 个）后回到技能 tab | **1** | **1** | 3 | 1 | 表宽 1128 == 容器 1128，无叠加 |

截图 `audit-conn-r5-skills-empty-no-table.png`、`-broken-no-stale-table.png`、`-skills-restored-one-table.png`。

## 10.5 观察（非失败）

- **O1（P4）**：antd `Tabs` 默认会把**已访问过**的非激活 pane 保留在 DOM（`offsetParent === null`，不可见）。所以"切到连接器后 `.zk-skills-table` 仍在 DOM 里 1 个"属于 antd 默认行为，不是残留渲染（空态时该 pane 内 0 表格）。若要求"不可见即不在 DOM"，给 `Tabs` 加 `destroyOnHidden` 即可。本轮判据（不残留旧表格）已满足。
- 说明列的省略现在有 `title` 兜底，但它是**原生 title**（悬停约 1 秒后出现浏览器提示），与名称/来源一致；若产品希望统一成 antd Tooltip，可给三列统一 `ellipsis: { showTitle: true }`。

## 10.6 第五轮截图

`audit-conn-r5-r41-desc-title-{1440,1024,768,390,320}.png`、`-rename-disabled-no-false-error`、`-broken-refresh-disabled`、`-390-light`、`-390-dark`、`-skills-empty-no-table`、`-broken-no-stale-table`、`-skills-restored-one-table`。

## 10.7 仍建议补的验收（历史遗留，非本轮阻断）

1. 停用后"工具是否真的从模型可见工具集消失"（需真实 run；本环境只有 fakemodel）。
2. 多工作区隔离（`mcp.disabledServers.<ws>`）。
3. OAuth / headers 连接器的新增路径；真实 stdio MCP 服务器成功路径；`timeoutMs` 对卡死端点的语义。
4. 把本轮断言固化进自动化：`desc title == API description`、`bundleSha256 == 复算摘要`、`broken → 刷新 disabled`、`空态无 .ant-table`。
