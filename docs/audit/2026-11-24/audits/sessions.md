# 会话管理（重命名 / 删除 / 归档 / 置顶）独立验收

- 验收对象：commit `85b4e47` 「feat: session rename / delete / archive / pin, and a switch that reads as it acts」
- 验收方式：真网关（`zeokeel-sess`，独立数据目录）+ 真 Chromium（`/data/tavo/tools/e2e/chromium-1243`），WS 帧逐帧抓取 + HTTP 原文 + 磁盘（settings.json / sqlite）核对；**未修改 `web/src` 任何文件**
- 时间：2026-11-25 00:50–01:10（北京时间）
- 结论：**不通过（有条件）** —— 四条协议的 IPC 全通，重命名/删除/归档主路径可用；但「置顶」声称的**视觉标记与排序完全没有实现**（唯一可观察变化是菜单文案翻转），**键盘无法打开行菜单**，**归档无任何 UI 撤销入口**；另有删除后 settings 行残留、200 字符上限实际被静默截断到 32 字符等 6 个次要缺陷。

---

## 0. 环境与启动自检

| 项 | 值 |
|---|---|
| 启动方式 | `bash .cache/ui-audit/startB.sh audit-sess .cache/bin/zeokeel-sess 9961 9962`（独立数据目录 `data-audit-sess`） |
| gateway.json | `{"epoch":"a9f5c640092ec849","pid":8,"port":9961,"startedAt":"2026-11-24T16:50:30Z","product":"serve","version":"dev"}` |
| 前端产物 | `web client bundle sha256 788f413d111f (6 files, built 2026-11-24T16:48:27Z from web/dist)`；`/assets/index-CaEx22gJ.js` 内含 `session.rename` 与 `shell.sessions.renameTitle`，即被测修复已进产物 |
| **启动自检** | `GET /` → **200**；`GET /favicon.svg` → **200**；`GET /api/capabilities`（无授权）→ **401** ✅ 三项全过 |
| 鉴权 | Bearer token（`/auth` cookie 流程亦可）；WS 必须先 `initialize` 再 `ready`，且每帧必须以 `\n` 结尾（实测：不带换行的帧被静默缓冲，永不响应） |

> 备注：`GET /api/sessions` 在本产品中**不存在**（返回 `{"error":{"code":-32601,"message":"unknown method: /api/sessions"}}`，404）。清单第 3 条的"`GET /api/sessions` 不含它"改用**等价的权威通道**核对：WS `session.list.snapshot` + 页面侧栏 + sqlite `sessions` 表。

---

## 1. 清单表（10 条）

| # | 验收项 | 判定 | 关键证据 |
|---|---|---|---|
| 1 | 菜单可达性（鼠标 hover / 键盘 Tab） | **鼠标 PASS / 键盘 FAIL** | hover 前 `opacity:0` → hover 后 `opacity:0.6`，点击打开；菜单项顺序 `重命名 / 置顶 / 归档 / <divider> / 删除(danger)`。键盘：行 `li` 无 tabindex（`tabIndex=-1`）、触发图标 `tabindex="-1" role="img"`；**Tab 连续 45 次从未落进行或菜单触发**（见 F2） |
| 2 | 重命名（预填 / 200 上限 / 空 / 相同 / 同步 / 刷新） | **PASS（有 2 个缺陷）** | 预填 `delta 会话`、`maxlength=200`；空/纯空格/相同名 **0 请求**；201 字符被输入框截到 200 后提交（协议侧 201 直接 `-32602`）；改名后侧栏+标题栏同步、刷新保持（跨端：另一标签页收到 `session.list.delta` 且标题栏同步）。缺陷：F5（200→32 静默截断）、F6（表情按 UTF-16 计只到 100 个） |
| 3 | 删除（确认文案 / 取消 / 确认 / 清理） | **PASS（清理项 FAIL）** | 标题 `删除「待删除会话」？`、正文 `会话与全部消息会被删除，无法撤销。`；取消：行仍在、`session.delete` 帧 **0**；确认：行消失、WS 列表不含、**sqlite sessions 1→0 / messages 4→0**。残留：`session.<id>.*` 行未清理（见 F4） |
| 4 | 删当前会话 → `/` | **PASS** | 删除 `/c/sess-…1407991041` 当行后：`location.pathname === "/"`，内容为「现在，说点什么」，`hasError=false`、无 404、无空白；再点另一会话正常跳转并显示其标题 |
| 5 | 归档 / 取消归档 / showArchived 联动 | **PASS（无 UI 撤销入口，见 F3）** | 归档后行消失、条数 9→8；`session.<id>.archived="true"`；`/settings/archive` 勾选「在侧栏显示已归档会话」+ **保存** 后写 `ui.sessions.showArchived="true"` 且侧栏**实时**重现（`settings.delta`），取消勾选后又隐藏；归档当前会话 → 回到 `/`。取消归档只能由协议调用（`archiveSession` 菜单项恒为 `归档`） |
| 6 | 置顶（标记 / 排序 / 行 / 刷新） | **FAIL** | `session.<id>.pinned="true"` 写入成功、菜单变「取消置顶」、刷新后设置行仍在——**但行 DOM 前后完全一致**（无图标、无 `data-pinned`、`svgCount` 仍为 1=省略号）、**位置不变**（置顶甲始终在 置顶乙 之后，index=1，刷新后仍 index=1）。见 F1 |
| 7 | 并发 / 竞态 | **PASS** | 两标签同刻改名：两帧都发出，服务端终值 B，两页侧栏收敛一致、零报错；删除另一页正打开的会话：该页行消失、线程变空态，无白屏/404；双击删除：一方成功、另一方弹「操作失败 / agent: unknown chat session "…"」+ 知道了，不崩 |
| 8 | 对不存在 sessionId 的四个方法 | **PASS** | 协议层：`rename / pin / archive / delete` 均返回可读错误 `{"code":-32603,"message":"agent: unknown chat session \"nope\""}`；UI 层（改名弹窗挂着时另一页删掉该会话再保存）：弹「操作失败 / agent: unknown chat session "…"」，控制台 0 error。注意文案是后端英文原文，未本地化 |
| 9 | 移动端 390 / 深色 | **PASS（可发现性与对比度有缺陷）** | 390×844（isMobile+hasTouch）：抽屉 390 宽、菜单可开（4 项）、重命名/删除弹窗 `x=8 w=374`、`scrollWidth=390` 不溢出；深色 `data-theme=dark`：菜单底 `rgb(31,31,31)`、正文 `rgba(255,255,255,.85)`、危险项 `rgb(220,68,70)`、标题 16px；对比度 12.14 / **3.89** / 16.48 / 16.48 / 4.24 / 13.31（≥4 组已录）。缺陷：触摸端触发器 `opacity:0`（见 F6）、危险项对比度 3.89（见 F7） |
| 10 | 回归（新建 / 发消息 / 审批 / 刷新 / showArchived 默认） | **PASS** | 侧栏新建 → `/c/<new>`；发消息 → 审批卡「需要审批 web_fetch … 允许一次」→ 允许 → 正文 `The fetch returned ok, so the approval chain works.`；侧栏标题自动取首条消息；刷新后标题、整段 transcript（含工具步）仍在；`ui.sessions.showArchived` 缺省（行不存在）时归档会话隐藏 |

---

## 2. 修复声称逐条证伪

| 声称 | 判定 | 说明 |
|---|---|---|
| 1) 每行有操作菜单，hover 或菜单按钮可打开 | **半真** | 鼠标完全成立（每行都有 `.ant-conversations-menu-icon`，6 行=6 触发器；鼠标 hover 显示、点击弹菜单，末行菜单 `y=596..741` 在 900 视口内）。键盘不成立（F2） |
| 2) 重命名：预填 / 200 / 空或相同不提交 / 同步 / 刷新 | **基本成立** | 5 个断言逐条命中（见清单 2）。但"200 字符上限"名不副实：网关放行 200，`agent.RenameSession` 随后 `truncateTitle` 截到 **32 字符+…** 落库（实测 100 字 → DB 33 字），用户看到的是静默丢字（F5） |
| 3) 删除：危险确认说明消息一并删 / 行消失 / 当前会话回 `/` / 取消无事 | **成立** | 4 项全过；DB 侧 sessions+4 条 messages 同删。唯一不符："磁盘 settings 无残留"——`session.<id>.pinned` 等行保留（F4） |
| 4) 归档：隐藏 / settings 行可查 / showArchived 重现 | **成立** | 见清单 5；`/settings/archive` 页面存在且开关需点「保存」才写（点了不保存不落盘，属正常草稿语义） |
| 5) 置顶：行有标记 / 排在前 / 刷新保持 | **不成立** | 标记与排序均无；只有 `session.<id>.pinned` 行与菜单文案翻转（F1） |

---

## 3. 失败项详情

### F1（P1，声称 5 完全不成立）置顶没有任何视觉标记，也不排序

- **现象**：置顶后侧栏行不变样（无 pin 图标、无底色、无 `data-pinned`），位置不变；刷新后仍不变。
- **复现**：`node sess/s6-pin.mjs`。置顶前 `labels=[置顶乙, 置顶甲, …]`、`pinnedRowBefore.html` 与置顶后 `afterPin.html` **逐字节相同**（仅 `.ant-typography` 文本 + 省略号图标，`svgCount=1`）；`pinnedIndex=1`，刷新后仍 `1`；`settingsRow="true"`，菜单项变为 `取消置顶`。
- **期望**：行首有 pin 标记/徽标，且置顶行排在未置顶行之前（刷新后保持）。
- **建议修法**：
  1. 侧栏按 `pinned` 稳定排序后再渲染：`web/src/shell/sidebar.tsx:86`（`const items = visible.map(...)` 前加 `const ordered = [...visible].sort((a,b)=> Number(!!b.pinned)-Number(!!a.pinned))`）；
  2. 行内渲染标记：`sidebar.tsx:86-89` 的 item 增加 `icon: session.pinned ? <ZkIcon name="pin" /> : undefined`（`internal/icons/svg` 已有 pin 语义图标集合，按现有生成链取用），或 `data-pinned` + CSS；
  3. 若希望排序在后端统一（多端一致），在 `internal/modules/agent/agent.go:2152` 的 `sort.SliceStable` 比较器里把 `Pinned` 作为第一排序键（当前只按 `UpdatedAt`），再让侧栏不重复排序。

### F2（P1）键盘无法打开行菜单

- **现象**：行 `li` 不可聚焦；省略号触发器 `tabindex="-1" role="img"`；Tab 走查 45 步从未进入 `.ant-conversations-item`。
- **证据**：`sess/s2-menu-rename.json` → `rowTabIndex{tabIndex:-1, tabindexAttr:null}`、`iconAfterHover{... tabIndex:"-1", role:"img"}`、`tabSawRow:false`、`tabSawMenuTrigger:false`；tab 停点只有「新建会话」按钮等。
- **期望**：Tab 到行（或行内一个可聚焦的「更多」按钮）后 Enter/Space 打开菜单，Esc 关闭（项目已有 Esc 仲裁器）。
- **建议修法**：给 `@ant-design/x` 的 `Conversations` 传**可聚焦的触发器**：`web/src/shell/sidebar.tsx:164-186` 的 `menu={(conversation) => ({ items, onClick })}` 里加 `trigger: (conversation, { originNode }) => <Button type="text" size="small" aria-label={t('shell.sessions.more')} aria-haspopup="menu" icon={originNode} />`（antd X `Item.js` 的 `renderMenuTrigger` 支持函数式 `trigger`，替换掉默认那个 `tabindex=-1` 的 `EllipsisOutlined`）；行本身如需可选焦，给 `items` 增加 `disabled:false` + 外层 `ul` 的 roving tabindex（antd X 的 `shortcutKeys={{items: ...}}` 亦可用于键盘导航）。

### F3（P2）归档后没有「取消归档」入口

- **现象**：`showArchived=true` 时归档行重新可见，但其菜单仍是 `重命名 / 置顶 / 归档 / 删除`；点「归档」= 再次 `archived:true`（幂等），永远回不来。
- **证据**：`sess/s5b-showarchived.json` → `menuOnArchived.order=["重命名","置顶","归档","<divider>","删除"]`；`archiveAgain{row:"true", visible:true}`。取消归档只能走协议（`session.archive {archived:false}`）。
- **期望**：已归档行的第三项显示「取消归档」并调用 `archiveSession(id, false)`。
- **建议修法**：`web/src/shell/sidebar.tsx:104-108`：
  ```ts
  const archived = sessions.find((item) => item.id === sessionId)?.archived === true;
  if (key === 'archive') {
    await archiveSession(sessionId, !archived);
    if (!archived && sessionId === activeSessionId) onCleared?.();
    return;
  }
  ```
  菜单 `label` 同理翻转（`archived ? 'shell.sessions.unarchive' : 'shell.sessions.archive'`，`web/src/i18n/{zh,en}.ts` 补 `shell.sessions.unarchive` = 取消归档 / Unarchive）。

### F4（P2）删除后 settings 里留下 `session.<id>.*` 残行

- **现象**：删除只清 `allowedTools` 与 `unread`，**`pinned` / `archived` 行原样保留**；本实例 65 条 `session.*` 行对应 29 个 session id，而 DB 只有 7 个会话 → 22 个孤儿 id。
- **证据**：`sess/s3-delete.json` → `settingsAfterDelete.rows` 含 `session.sess-…205374.pinned="true"`（该会话已删）、`.archived="false"`、`.allowedTools=""`、`.unread="false"`。
- **期望**：删除即清理该会话的全部 settings 行（或至少把 pinned/archived 归 false，与 unread 同策略）。
- **建议修法**：`internal/modules/agent/agent.go:2004-2050`（`DeleteSession`）在 `s.clearAllowedToolsRow(sessionID)` / `s.setSessionUnread(sessionID,false)` 旁补
  ```go
  _ = s.settings.Set(contracts.SessionPinnedSettingKey(sessionID), "false")
  _ = s.settings.Set(contracts.SessionArchivedSettingKey(sessionID), "false")
  ```
  （`SettingsService` 无 delete API，置 false 是现有约定写法）；根因是 `internal/contracts` 里缺少 settings 行删除能力，可另开一条 `settings.delete` 方法后在 `DeleteSession` 调用。

### F5（P2）「200 字符上限」实际被截到 32 字符，且无提示

- **现象**：`session.rename` 传 100 字 → 返回 `{epoch,rev}` 成功；列表/DB 里只剩 33 字（32+…）。
- **证据**：`sess/p1-frames.json`（200 字成功、201 字 `-32602`）+ 实测 100 字 → `list title` 33 字符、`db title len 33`。
- **定位**：`internal/modules/agent/agent.go:1572` `title = truncateTitle(strings.TrimSpace(title))`；`truncateTitle` 在 `agent.go:3957-3963` 用 `maxTitleRunes = 32`（`agent.go:75`，原注释写明那是「首条 prompt 派生标题」的截断长度，被 RenameSession 复用）。
- **期望**：用户显式改名不被静默截断——要么放开到 200（与 `internal/gateway/methods.go:1331` 一致），要么 UI 的 `maxLength` 改为 32 并明示上限。
- **建议修法**：`RenameSession` 改用独立上限（如 `maxTitleRunes`→`maxExplicitTitleRunes = 200`，或直接 `strings.TrimSpace` 后用 `validateSessionTitle` 的 200）；`web/src/shell/sidebar.tsx:51` 的 `<Input maxLength={200}>` 与后端最终上限对齐。

### F6（P2）移动端（触摸）触发器不可见，热区 20×20

- **现象**：390×844 + `hasTouch` 下，行内触发器 `opacity:0`（只有鼠标 hover 才到 0.6）；触摸设备没有 hover，等于「看不见但可点」的 20×20 隐形热区；点中可打开菜单（功能可用，可发现性差）。
- **证据**：`sess/s8b-mobile-dark2.json` → `touch.iconNoHover{opacity:"0", w:20, h:20, cls:"…ant-conversations-menu-icon"}`；菜单在 tap 后正常弹出（`touch.menu.text=[重命名,置顶,归档,删除]`）。
- **期望**：粗指针/触摸设备常显触发器（≥40×40 热区），或在行内提供明确的「…」按钮。
- **建议修法**：`web/src/shell/sidebar.tsx` 用 `useMediaQuery('(pointer: coarse)')`（`web/src/design/use-system-appearance.ts` 已有同类 hook）给 `Conversations` 传 `classNames={{ item: 'zk-sess-row--touch' }}`，CSS 里 `opacity:1; width/height:40px`；或按 F2 的 `trigger` 函数换成常显 `Button`。

### F7（P3）深色下「删除」菜单项/危险按钮对比度未达 AA

- **现象/数值**：深色菜单底 `rgb(31,31,31)`；危险项 `rgb(220,68,70)` → **3.89:1**；危险按钮 白字/`rgb(220,68,70)` → **4.24:1**（正文 14px，AA 要求 4.5）。正文项 12.14、弹窗标题/正文 16.48、取消按钮 13.31 均合格。
- **证据**：`sess/s8b-mobile-dark2.json` → `darkMenu`、`darkConfirm`、`darkContrast`。
- **建议修法**：深色算法下把危险色提亮（antd `theme.darkAlgorithm` + `token.colorError` 覆写，或菜单项 `style={{color: tokens.errorTextDark}}`）；`web/src/design/theme.ts` 目前只传 `colorPrimary/fontFamily`，可在深色分支加 `token: { colorError: … }`。

### F8（P2，疑似既有性能问题，非本修复引入）硬加载 `/c/<id>` 时侧栏 2.7–4.9s 才出现

- **现象**：直接 `goto /c/<id>`（或用 F5 刷新）时，侧栏会话行在 1.6/2.8/3.7/4.0/4.2/4.9 秒才出现（6 次采样：1661, 4025, 2768, 4853, 3714, 4234 ms）；期间标题栏回落为「会话」。在 `/` 下 500ms 即有列表。
- **证据**：`sess/s2e-loadtiming.json`、`sess/s2c-reload-thread.json`（同一 URL 多次重载时 3s 采样仍可能为空）。
- **影响**：清单 2「刷新后保持」在"立即截图"口径下会看到空列表；数据未丢，晚到即恢复。
- **建议**：先用 Performance 面板确认是 `session.list.snapshot` 晚到还是 ThreadScreen 首屏阻塞主线程（侧栏与 thread 共用一帧）；若是后者，把 transcript 首屏渲染让位给侧栏（`startTransition`/分段渲染）。此项与 session CRUD 无关，建议单独跟踪。

---

## 4. 协议层结论（`session.rename/delete/archive/pin`）

四条方法全部按 `{epoch, rev}` 返回，并各自推 `session.list.delta`（`changed`/`removed`），跨客户端一致。

| 调用 | 帧（原文节选） |
|---|---|
| 成功 | `{"jsonrpc":"2.0","id":5,"method":"session.rename","params":{"sessionId":"sess-1795539239598579331","title":"改名为乙"}}` → `{"jsonrpc":"2.0","id":5,"result":{"epoch":"a9f5c640092ec849","rev":2}}` |
| 删除 | `session.delete {sessionId}` → `{epoch,rev}`；广播 `{"method":"session.list.delta","params":{"baseRev":84,"rev":85,"removed":["sess-…16498130645"]}}` |
| 归档 | `session.archive {archived:true}` → `rev` + `changed[0].archived=true` |
| 置顶 | `session.pin {pinned:true}` → `rev` + `changed[0].pinned=true` |
| 201 字 | `{"error":{"code":-32602,"message":"invalid params: title: is longer than 200 characters","data":{"field":"title","reason":"..."}}}` |
| 控制字符 | `{"error":{"code":-32602,"message":"invalid params: title: contains a control character"}}` |
| 空标题（绕过 UI） | `{"error":{"code":-32603,"message":"agent: session title must not be empty"}}`（内部错误码而非 `invalid_params`，见下） |
| 不存在会话（四个方法同） | `{"code":-32603,"message":"agent: unknown chat session \"nope\""}` |
| 缺 sessionId | `{"code":-32602,"message":"invalid params: sessionId: is required"}` |

次要协议观察：
1. 空/纯空格标题在协议层落到 `-32603`（内部错误）而非 `-32602`；UI 已拦截，外部调用会看到一个"内部错误"。建议 `renameSession` 在网关侧先用 `validateSessionTitle` 的 trim 结果判空并返回 `invalid_params`。
2. 表情上限语义不一致：`len([]rune)` 计 200，输入框 `maxLength=200` 计 UTF-16 → UI 最多 100 个 😀（实测 `utf16:200, runes:100` 提交成功），协议 200 个 😀 也成功、201 个被拒。
3. 标题截断 32 字符发生在**服务端**（见 F5），不是前端省略号。

---

## 5. 证据索引

### 截图（`/data/zeokeel/.cache/ui-audit/findings3/`，71 张 `audit-sess-*.png`）

| 主题 | 文件 |
|---|---|
| 列表 / hover / 菜单 | `02`、`03`、`04`、`71` |
| 重命名弹窗 / 200 字 / 标题栏 / 刷新 | `05`、`06`、`07`、`08`、`61`、`62` |
| 删除（确认 / 取消 / 结果 / 当前会话 / 另一会话） | `12`–`19`、`60` |
| 归档（隐藏 / 设置页 / 重现 / 菜单 / 隐藏回去 / 归档当前） | `20`–`30` |
| 置顶（前 / 后 / 刷新 / 取消） | `31`–`34` |
| 并发（双页改名 / 另一页删除 / 弹窗错误 / 双击删除 / 远端归档） | `35`、`36`、`37`、`39`、`40`、`63`–`67` |
| 移动端 390（含触摸） | `41`–`45`、`49`–`52` |
| 深色 | `46`–`48`、`53`、`54` |
| 回归（审批 / 完成后 / 刷新 / transcript） | `55`–`58` |
| 英文文案 | `68`–`70` |
| 跨端标题栏同步 / 末行菜单 | `73`、`71`、`72` |

### 原始数据（`/data/zeokeel/.cache/ui-audit/sess/`）

`s2-menu-rename.json`、`s2b-reload.json`、`s2c-reload-thread.json`、`s2d.json`、`s2e-loadtiming.json`、`s3-delete.json`、`s4-delete-current.json`、`s5-archive.json`、`s5b-showarchived.json`、`s6-pin.json`、`s7-race.json`、`s7b-stale.json`、`s8-mobile-dark.json`、`s8b-mobile-dark2.json`、`s9-regress.json`、`s9b-transcript.json`、`s10-crossclient.json`、`s11-edge.json`、`s12-double.json`、`s13-en.json`、`s14-final-ui.json`、`s15-topband-sync.json`、`p1-frames.json`（协议帧全集）

复跑：`cd /data/zeokeel/.cache/ui-audit && node sess/<脚本>.mjs`（需先启动 9961 实例；脚本内 `lib.mjs` 读 `data-audit-sess/gateway.json` 的 port/token）。

### 磁盘核对

- `data-audit-sess/settings.json`：65 条 `session.*` 行 / 29 个 id，DB 仅 7 会话（F4 的孤儿证据）
- `data-audit-sess/zeokeel.db`：删除带消息的会话前后 `sessions 1→0`、`messages 4→0`

---

## 6. 需补验收点（本轮未覆盖，建议下一轮）

1. **运行中改名/删除**：`running=true` 时 rename 是否被后续落库覆盖；删除正在跑的会话是否留下 tombstones（代码有 `markSessionDeleted`，需实测 SSE 尾帧）。
2. **折叠态侧栏**（Mod+B 图标轨道）：轨道点只有 aria-label，无行菜单——是否属于可接受降级需产品确认。
3. **空状态**：删到只剩 0 个会话时侧栏空态文案与 `/` 行为。
4. **归档语义**：归档会话仍出现在 `session.list.snapshot`（`archived:true`），仅前端过滤；若"归档"应等价于"从列表域摘除"（多端/搜索/统计口径），需产品定夺。
5. **错误文案本地化**：`agent: unknown chat session "…"` 直接进弹窗（中英混排），建议网关按 `code` 返回可翻译键。
6. **200 字符标题的标题栏展示**：当前标题栏显示 33 字截断文本且无 `title` 提示，长名无法读到全称。
---

# 第二轮复验（2026-11-25 01:19–01:45）

- 被测版本：`6a3a85e`「fix: the session pin shows, the menu is reachable, the draft survives」（含 `b9f8bf0` 的 bundle 指纹）
- 实例：`startB.sh sess3 .cache/bin/zeokeel-sess3 9963 9964`（独立数据目录 `data-sess3`）
- gateway.json 自证：`{"epoch":"799cf6306304071b","port":9963,"bundleSha256":"a6f76797924ddcef2fe3b35a3d854038f747dedb448cbca7d6cd26371f88cc28","bundleBuiltAt":"2026-11-24T17:18:55Z"}`；产物 `/assets/index-DyE2-BJJ.js`（与一轮 `index-CaEx22gJ.js` 不同），bundle 内含 `dangerItemColor` 与 `#fca5a5`，说明本轮改动确实进了产物
- 启动自检：`GET /`=200、`GET /favicon.svg`=200、`GET /api/capabilities`(无授权)=401 ✅
- **总结论：部分通过** —— F1 / F3 / F4 / F5 / F6、composer 草稿、以及上一轮 10 项清单全部 PASS；**F2 键盘只过一半**；**F7 深色对比修复未生效**（token 被写进 CSS 变量，但 Dropdown 菜单项不读它）；F1 的修复引入一处**新回归 F1b**（置顶文案" · 置顶"会泄漏进改名预填/删除确认并被写进真实标题）。

## R2 判定表

| # | 要求 | 判定 | 证据 |
|---|---|---|---|
| F1 | 置顶：可见变化 + 排最前；取消恢复；刷新保持；双置顶顺序合理 | **PASS（但有新回归 F1b）** | 把三者中**最后一行**「序一」置顶 → 立刻变成第一位且文案 `序一 · 置顶`（`before:[序三,序二,序一] → after:[序一·置顶,序三,序二]`）；再置顶 销A 后同置顶块顺序为 `销C·置顶, 销A·置顶, 销B`（块内保持网关 updatedAt 序）；刷新后不变；取消两者 → `销C, 销B, 销A` 原序恢复；`session.<id>.pinned` true→false。截图 `r2-01..07`、`r2-45` |
| F2 | Tab 可达每行 `session-menu`(24×24, tabindex 0, "会话操作")；Enter/Space 开；方向键选、Enter 执行、Esc 关闭且焦点回按钮；无多余设置写入 | **部分 PASS** | 每条行都有触发器（5 行 5 个；Tab 第 3 步即到；80 次 Tab 覆盖全部行的按钮，`allRowsReachable=true`）；`{w:24,h:24,tabIndex:0,aria:"会话操作",opacity:1}`；Enter/Space 打开；**打开后焦点仍留在按钮上，方向键无高亮、再按 Enter 只是把菜单关掉（0 动作）**；先按 Tab 进入菜单后 ↑↓/Enter 才工作（对目标行置顶成功，帧 `{sessionId: 目标id, pinned:true}`）；**焦点在菜单内时 Esc → 焦点落到 `BODY`，未回到按钮**；全程 `settings.set` 写入 **0**。见 F2′ |
| F3 | 已归档行菜单为「取消归档」；执行后行重现/列表一致；再归档可消失 | **PASS** | 归档后菜单 `重命名/置顶/取消归档/<divider>/删除`；点击后 `archived=false`、行仍在（showArchived=true）、菜单翻回「归档」；再归档 + showArchived=false → 消失；再打开开关 → 重现（`settings.delta` + `session.list.delta changed[0].archived` 同步）。截图 `r2-14..18`、`r2-42` |
| F4 | 删除后 `session.<id>.*` 不得残留；其它会话不受影响 | **PASS** | 受害会话先写 `pinned/archived/allowedTools/unread/model` 5 行 → 删除后 `allKeysMentioningVictim=[]`、`session.*` 总行 11→6（**真删除**而非置空）；保留会话的 `pinned=true` 完好；列表 `removed:[id]`、`hasVictim=false`。截图 `r2-19` |
| F5 | 100/198/200 原样；201 拒 | **PASS** | 列表与 DB（`length(title)`）均 100/198/200，**无 `…` 追加**；201 → `-32602 invalid params: title: is longer than 200 characters`；UI 输入 200 也存 200；标题栏 200 字（`textLen=200`，`scrollW 3200 > clientW 1144` 但页面 `scrollWidth=1440`，由省略号裁切）。截图 `r2-20/21` |
| F6 | 触摸下触发器可见、可点 | **PASS** | 390×844 `isMobile+hasTouch`：`opacity:1`、24×24、`tabIndex=0`、aria「会话操作」，每行 1 个（8 行 8 个）；无 hover 直接 tap 打开；删除确认 `x=8 w=374`，`scrollWidth=390` 不溢出。截图 `r2-22..24` |
| F7 | 深色危险菜单项/危险按钮 ≥4.5 | **FAIL（修复无效）** | 深色实测 danger item `rgb(220,68,70)` on `rgb(31,31,31)` = **3.89**；危险按钮 白字/`rgb(220,68,70)` = **4.24**；同一页面 `--ant-menu-danger-item-color` 已是 `#fca5a5`（8.68 若生效）。浅色同病：`rgb(255,77,79)` on 白 = **3.27**（变量 `#b91c1c` 未用）。见 F7′ |
| 8 | composer 草稿（切会话/刷新/发送后清空/互不串） | **PASS** | A 输入「A 的草稿」→ `localStorage['zk.draft.<A>']`；切 B 为空、输入 B 草稿；回 A 恢复；刷新后仍在；发送后 composer 空且 A 的 key 被删、B 的草稿完好；改名 + 刷新后草稿仍在。截图 `r2-27..30`、`r2-44` |
| 9 | 回归（协议 + 上一轮 10 项） | **PASS** | 四条方法均 `{epoch,rev}`（63→69）；`removed`/`changed` 差值正确；201/控制字符 `-32602`；不存在会话 `-32603 agent: unknown chat session`；带消息会话删除后 DB `sessions 1→0 / messages 4→0`；删当前会话 → `/`（TopBand「现在」，无错误）；菜单文案顺序不变；空/空格/同名改名 0 请求；取消删除 0 帧、行保留；双端并发改名收敛（B）、跨端标题栏同步；`showArchived` 设置页勾选+保存生效；新建→审批→允许→刷新 transcript 完整。截图 `r2-31..39` |

## 失败项

### F7′（P1，修复未生效）危险色 token 打在了错误的通道上

- **现象**：深色危险菜单项仍 `rgb(220,68,70)`（3.89:1），危险按钮白字/红底 4.24:1；浅色危险项 3.27:1。均低于 AA 4.5。
- **根因（实测规则原文）**：`web/src/design/theme.ts` 设的是 `components.Menu.dangerItemColor`，antd 6 只把它变成 CSS 变量 `--ant-menu-danger-item-color`（页面实测 `#fca5a5`），但下拉菜单项的颜色来自 **Dropdown 自己的规则**：
  ```
  :where(.css-pbsm9t).ant-dropdown .ant-dropdown-menu .ant-dropdown-menu-item.ant-dropdown-menu-item-danger:not(.ant-dropdown-menu-item-disabled) … { color: var(--ant-color-error); }
  ```
  即 `.ant-dropdown-menu-item-danger` 读 `--ant-color-error`，与 Menu token 无关；全站没有 `.ant-menu-item-danger`（真实 Menu）危险项，所以该 token 是**死配置**。危险按钮的红色底同样来自 `--ant-color-error`。
- **复现**：`ZK_DATA=data-sess3 node sess/r2-s6-touch-dark.mjs`（深色菜单+确认框）、`sess/r2-s6b-token-probe.mjs`（变量链）、`sess/r2-s6c-rule.mjs`（命中规则原文）、`sess/r2-s12-light.mjs`（浅色 3.27）。
- **期望**：深色 danger 文本 ≥4.5（如 `#fca5a5`=8.68），危险按钮 ≥4.5（如白字在 `#b91c1c` 上 6.47）。
- **建议修法**（二选一，须覆盖 Dropdown + Button）：
  1. 全局：`token: { colorError: dangerText[appearance] }`（深色 `#fca5a5` 会变成浅粉底、白字不可读，需同时给按钮换 `colorTextLightSolid`，不推荐直接改全局）；
  2. 定点（推荐）：`web/src/design/global.css` 增加
     ```css
     .ant-dropdown-menu-item-danger:not(.ant-dropdown-menu-item-disabled) { color: var(--zk-danger-text); }
     .ant-dropdown-menu-item-danger:not(.ant-dropdown-menu-item-disabled):hover { color: var(--zk-danger-text); }
     .ant-btn-dangerous.ant-btn-primary { background: var(--zk-danger-bg); }
     ```
     并在 `web/src/design/tokens.css` 的生成链里给 `--zk-danger-text`（亮 `#b91c1c`/暗 `#fca5a5`）与 `--zk-danger-bg`（亮 `#b91c1c`/暗 `#b91c1c`）各一个值（`web/scripts/gen-tokens.mjs` 已是颜色的唯一来源，不能手写 hex）。

### F2′（P2，部分未达标）菜单打开后不接管焦点；Esc 从菜单返回丢了焦点

- **现象**：`Enter` 打开菜单后 `document.activeElement` 仍是 `BUTTON[data-testid=session-menu]`，四个菜单项都没有 `-active`；此时 `ArrowDown` 无任何高亮，再 `Enter` 只是关闭菜单（0 动作、0 帧）。先按一次 `Tab` 把焦点送进菜单后，`ArrowDown`/`Enter` 才正常（实测对目标行发出 `session.pin`）。焦点在菜单内按 `Esc`：菜单关闭，但焦点变为 `BODY`，未回到触发按钮（要求"焦点回到按钮"）。
- **证据**：`sess/r2-s2d-precise.json`（`after-enter{active:BUTTON}` → `arrowdown-no-tab` 无高亮 → `enter-no-tab{open:false,pinDelta:0}` → `after-tab-into-menu{active:LI 重命名}` → `arrowdown-in-menu{active:LI 置顶}` → `enter-in-menu{pinDelta:1,active:BODY}` → `esc-from-menu{active:BODY}`）；`sess/r2-s2-keyboard.json`（`esc` 分支焦点在按钮时 Esc 可回按钮，`settingsWritesTotal:0`）。
- **期望**：`Enter/↓` 打开后第一项立即高亮（roving focus），`Esc`/执行后焦点回到触发按钮。
- **建议修法**：`web/src/shell/sidebar.tsx:164-222` 的 `menu` 回调里给触发器按钮加键盘处理：`onKeyDown` 拦截 `ArrowDown/Enter/Space` → 打开后 `queueMicrotask` 聚焦 `.ant-dropdown-menu-item`（或直接把 antd `Dropdown` 的 `autoFocus` 透传——`Conversations` 不暴露该 prop，可在 `menu` 里带 `autoFocus: true`，antd 6 的 Dropdown menu 支持 `autoFocus`）；关闭时用 `onOpenChange(false)` 把焦点 `triggerRef.current?.focus()` 收回。

### F1b（P1，新回归）置顶文案" · 置顶"污染改名预填、删除确认，并被写进真实标题

- **现象**：置顶行的 `label` 被改成 `标题 · 置顶`，而菜单回调把 `conversation.label` 当标题传给 `handleMenu`：
  - 重命名弹窗预填 `销C · 置顶`（应为 `销C`）；
  - 删除确认 `删除「销C · 置顶」？`；
  - **改名时把标记一起提交**：预填 `泄漏二 · 置顶` + 输入 `改` → 提交 `泄漏二 · 置顶改`，DB 与列表标题都变成 `泄漏二 · 置顶改`；取消置顶后标题残留；再置顶后行文案变成 `泄漏二 · 置顶改 · 置顶`（截图 `r2-20` 可见 `泄漏二 · 置顶改 · 置顶`）。
- **证据**：`sess/r2-s1b-leak.json`（`prefill="泄漏二 · 置顶"`、`submitted=["泄漏二 · 置顶改"]`、`storedTitle="泄漏二 · 置顶改"`、`snapshotAfterUnpin="泄漏二 · 置顶改"`）；`sess/r2-s1-pin.json`（`renamePrefillOnPinned.inputValue="销C · 置顶"`、`deleteCopyOnPinned="删除「销C · 置顶」？"`）。
- **期望**：行上的置顶标记只影响**展示**；改名/删除一律用真实标题。
- **建议修法**：`web/src/shell/sidebar.tsx:215` 不要传 `conversation.label`，改传真实标题：
  ```ts
  const session = sessions.find((item) => item.id === conversation.key);
  void handleMenu(String(info.key), String(conversation.key), session?.title ?? '');
  ```
  （顺带把 `promptRename`/`confirmDelete` 的入参语义写死为"真实标题"）；更好的做法是标记不要拼进 `label`，而是给 item 一个 `icon` 或用 `classNames` 渲染角标（`Conversations` 的 item 支持 `icon`）。

### 次要观察（不阻塞）

1. 删除会话后 `localStorage['zk.draft.<id>']` 不清（`sess/r2-s11-kbd-row.json`：删除后仍存「将被删除的草稿」）。建议删除流程顺带 `localStorage.removeItem('zk.draft.'+id)`，或在 ThreadScreen 卸载时按会话存在性清理。
2. 表情上限仍是"UI 按 UTF-16 200 / 后端按 rune 200"（UI 最多 100 个 😀）——与上一轮一致，未在本次声称范围内。
3. 浅色模式危险项 3.27 也是同样根因（F7′），修的时候两侧一起修。
4. 深色危险项 hover 色仍是 antd 的 `colorErrorHover`，覆写时需一并处理 hover/active 态。

## R2 截图与脚本索引

- 截图（44 张）：`audit-sess-r2-01..07`（置顶）、`r2-08`（后缀泄漏）、`r2-09..13`、`r2-40/41`（键盘）、`r2-14..18`、`r2-42`（归档/取消归档）、`r2-19`（删除清理）、`r2-20/21`、`r2-43`（长标题）、`r2-22..24`（触摸）、`r2-25/26`（深色）、`r2-27..30`、`r2-44`（草稿）、`r2-31..39`（回归/并发）
- 脚本：`sess/r2-s1-pin.mjs`、`r2-s1b-leak.mjs`、`r2-s1c-last-to-top.mjs`、`r2-s2-keyboard.mjs`、`r2-s2b-kbd-probe.mjs`、`r2-s2c-esc.mjs`、`r2-s2d-precise.mjs`、`r2-s3-archive.mjs`、`r2-s4-delete-cleanup.mjs`、`r2-s5-titles.mjs`、`r2-s6-touch-dark.mjs`、`r2-s6b-token-probe.mjs`、`r2-s6c-rule.mjs`、`r2-s7-draft.mjs`、`r2-s8-regress-core.mjs`、`r2-s9-flow.mjs`、`r2-s10-race.mjs`、`r2-s11-kbd-row.mjs`、`r2-s12-light.mjs`、`r2-s13-unarchive-menu.mjs`、`r2-s14-extras.mjs`
- 原始数据：同目录 `r2-*.json`（含 `r2-s6c-rule.json` 的命中 CSS 规则原文、`r2-s2d-precise.json` 的逐步焦点轨迹）

---

# 第三轮复验（2026-11-25 01:39–02:05）

- 被测版本：`0b7f29c`「fix(web): the pin is an icon, the menu takes focus, danger text meets AA」+ `0e73c5c`「fix(web): the danger rule needs to outrank antd's, not match it」
- 实例：`startB.sh sess5 .cache/bin/zeokeel-sess4 9965 9968`（`data-sess5`）
- gateway.json 自证：`{"epoch":"3a0887a818ff3637","port":9965,"bundleSha256":"4fdf281d0cc5a3b10d8e11eb470c53ca2ff746c02f57907fc64da8b7890a0f63","bundleBuiltAt":"2026-11-24T17:38:33Z"}`；产物 `/assets/index-Ckfv1WQc.js` + `index-Bbs_AR-k.css`（CSS 内含 `--zk-danger-text` 与 `!important` 覆写、JS 内含 `zk-session-pin`）
- 启动自检：`GET /`=200、`GET /favicon.svg`=200、`GET /api/capabilities`(无授权)=401 ✅
- **总结论：两处修好、两处仍不达标** —— F1b 完全修复；新增项（草稿清理、码点上限）通过；F2′ 的 Enter/Space/方向键/执行/Tab 路径可用，但 **↓ 不开菜单**、**Esc 与执行后焦点落到 BODY**；F7 的**危险菜单项已达标**（深 8.68 / 浅 6.47），但**实心危险按钮仍 4.24 / 3.27**（CSS 显式排除了 `.ant-btn-primary`）。上一轮 PASS 的 F1/F3/F4/F5/F6/草稿/9 项回归全部复跑通过。

## R3 判定表

| # | 要求 | 判定 | 证据 |
|---|---|---|---|
| 1 F1b | label == `session.title`；标记是独立 `.zk-session-pin`；改名预填无"置顶"；库内标题无标记串；取消无残留；再置顶不叠加 | **PASS** | 置顶后 `label="针一号"`（`labelHasMark=false`）、`pinCount=1`、`aria/title="置顶"`、行 `title` 属性也是真实标题；改名预填 `针一号`、提交/落库 `针一号改`（无标记串）；取消置顶 `pinCount=0` 且 label 不变；再置顶 `pinCount=1`（不叠加）；标题本身含"置顶"二字（`标题里带置顶两个字`）时 label 原样、pin 图标独立计 1。截图 `r3-01..04` |
| 2 F2′ | ↓ 或 Enter → 开 + 焦点到首项；↑↓ 高亮；Enter 执行；Esc 关闭焦点回触发器；Tab 可用；无多余 settings.set | **部分 PASS** | Enter：开 + 焦点落 `LI 重命名`（`-active`）；Space 同；↑↓ 高亮移动（`重命名→取消置顶`）；Enter 执行（`pinDelta=1`，目标会话 pinned 翻转）；Tab 进入菜单可用；`settings.set` 写入 **0**。**↓ 不开菜单**（`arrowdown-opens{open:false, active:BUTTON}`）；**Esc 后 `active=BODY`**、**执行后 `active=BODY`**。见 R3-F2a / R3-F2b |
| 3 F7 | 深色（真实 `prefers-color-scheme`）危险菜单项 ≥4.5；实心 `primary danger` 按钮文字 ≥4.5；浅色同样 | **部分 PASS（按钮未达标）** | 真实深色上下文（`dataset.theme=dark`，非手改属性）：危险菜单项 `rgb(252,165,165)` on `rgb(31,31,31)` = **8.68**；**实心危险按钮 `rgb(255,255,255)` on `rgb(220,68,70)` = 4.24**。浅色：菜单项 `rgb(185,28,28)` on 白 = **6.47**；**按钮 白 on `rgb(255,77,79)` = 3.27**。菜单项修复生效（`!important` 覆写成功），按钮被 CSS 的 `:not(.ant-btn-primary)` 显式排除（实测 class：`ant-btn-primary ant-btn-dangerous ant-btn-color-dangerous ant-btn-variant-solid`）。见 R3-F7b |
| 4a | 删除会话后清 `localStorage['zk.draft.<id>']` | **PASS（本页删除）** | UI 删除前 `zk.draft.<id>="删除前写下的草稿"` → 删除后 `null`，浏览器里已无任何 `zk.draft.*` 键。远端删除（另一客户端）仍会留下本页草稿键，见存疑点 |
| 4b | 改名按码点限 200（100 emoji 可，超 200 码点截断） | **PASS（有显示不一致）** | 协议：200 个 😀（200 runes）成功，201 个 → `-32602`；UI：100 个 😀（100 cp / 200 UTF-16）提交 100 cp、落库 100；200 个 😀 通过；201 个 😀 → 提交 **200 cp**、落库 200；201 个汉字 → 提交 200。**但输入框仍显示 201 个**（`shownCodePoints=201` vs `sent=200`），见存疑点 |
| 5 回归 | F1/F3/F4/F5/F6/草稿/9 项 | **PASS** | F1 把末行「序一」置顶 → 首位（`[序三,序二,序一]→[序一,序三,序二]`）；F3 已归档菜单「取消归档」→ `archived=false`、菜单翻回、隐藏/重现一致；F4 删除清 5 条 `session.<id>.*`（8→3，无残留、保留会话 pinned 完好）；F5 100/198/200 原样 + 201 `-32602`；F6 触摸 `opacity=1`、24×24、16 行 16 触发器；草稿 A/B/刷新/发送全对；协议 `{epoch,rev}`（59→65）+ `removed` 差值、空/空格/同名 0 请求、删除确认文案、取消 0 帧、带消息删除 DB `1→0/4→0`、删当前回 `/`（`r2-s9` 复跑）、`showArchived` 设置页勾选保存生效、并发改名收敛、跨端标题栏同步、审批+刷新 transcript |

## 失败项

### R3-F2a（P2）触发器上按 ↓ 不会打开菜单

- **现象**：焦点在 `[data-testid=session-menu]` 上按 `ArrowDown`，菜单不出现、焦点不动（`{open:false, active:BUTTON.session-menu}`）；随后什么都没有发生。
- **根因**：antd `Dropdown` 的 `trigger` 是 `['click']`（`@ant-design/x` Item.js 里固定），`ArrowDown` 不产生 click；`sidebar.tsx:221-237` 的 `onKeyDown` 只对 `ArrowDown/Enter/Space` 做了「80ms 后聚焦 `.ant-dropdown-menu-item`」，**没有打开动作**——菜单没开时选择器为空，等于空转。Enter/Space 能开是因为浏览器把键转成了 click。
- **复现**：`ZK_DATA=data-sess5 node sess/r3-s2-keyboard.mjs` → `runs[0]`（`arrowdown-opens`）。
- **期望**：↓ 与 Enter/Space 等价：打开菜单并把焦点放到首项。
- **建议修法**：在 `onKeyDown` 里对 `ArrowDown` 主动开：`if (event.key === 'ArrowDown') { event.preventDefault(); event.currentTarget.click(); }`（click 会走 antd 的打开路径），再沿用现有的 setTimeout 聚焦；同时补 `aria-haspopup="menu"` 与 `aria-expanded`（当前按钮只有 `aria-label`）。

### R3-F2b（P2）Esc 关闭 / 执行动作后焦点落到 `BODY`，没有回到触发器

- **现象**：从菜单内（`active=LI 重命名`）按 `Esc`：菜单关闭，`document.activeElement` 变成 `BODY`；用 ↑↓ 选到「取消置顶」按 Enter 执行后，焦点同样是 `BODY`。claim 要求「Esc 关闭后焦点回触发器（不是 BODY）」。
- **证据**：`sess/r3-s2-keyboard.json` → `esc-returns-focus{inMenu:LI 重命名, after.active:BODY}`、`enter-executes{after.active:BODY}`；对比：焦点仍在触发器时按 Esc（鼠标开的菜单）焦点本就在按钮上。
- **建议修法**：在 `sidebar.tsx` 里保存打开的触发器 ref（`const openTrigger = useRef<HTMLButtonElement|null>(null)`，在 `onClick`/`onKeyDown` 打开时写入）。随后：
  1. 在 `useEffect` 里对 `document` 加 `keydown` 捕获监听：当 `event.key === 'Escape'` 且 `document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden)')` 存在时，等 antd 关闭（`requestAnimationFrame` 或 `setTimeout(0)`）后 `openTrigger.current?.focus()`；
  2. 菜单项 `onClick` 之后同样 `requestAnimationFrame(() => openTrigger.current?.focus())`（行重渲染后仍能找到同一按钮，因为 key 是 sessionId）。

### R3-F7b（P1）实心危险按钮对比度未达标

- **现象/数值**（真实偏好色 `prefers-color-scheme`，`dataset.theme` 自动为 dark/light）：
  - 深色菜单项 `#fca5a5` on `#1f1f1f`：L1=0.5032、L2=0.0137 → **(0.5032+0.05)/(0.0137+0.05)=8.68** ✅
  - 深色实心按钮 白字 on `#dc4446`：L1=1.0、L2=0.1979 → **1.05/0.2479=4.24** ❌
  - 浅色菜单项 `#b91c1c` on 白：L1=0.1123、L2=1.0 → **1.05/0.1623=6.47** ✅
  - 浅色实心按钮 白字 on `#ff4d4f`：L2=0.2713 → **1.05/0.3213=3.27** ❌
  （WCAG：`L=0.2126R+0.7152G+0.0722B`，`C=(L1+0.05)/(L2+0.05)`；通道线性化 `c/12.92` 或 `((c/255+0.055)/1.055)^2.4`）
- **根因**：`global.css` 的覆写是 `.ant-btn-dangerous:not(.ant-btn-primary){ color: … }`——确认框的「删 除」是**实心 primary**（实测 class `ant-btn-primary ant-btn-dangerous ant-btn-color-dangerous ant-btn-variant-solid`），被 `:not()` 排除；它的底色 `--ant-color-error` 未改，白字在 `#dc4446`/`#ff4d4f` 上分别 4.24/3.27。
- **复现**：`ZK_DATA=data-sess5 node sess/r3-s3-contrast.mjs`（深/浅两轮，含 computed 与比值）。
- **期望**：实心危险按钮文字 ≥4.5（白字配 `#b91c1c` 即 6.47）。
- **建议修法**（任选，需覆盖默认/hover/active）：
  ```css
  .ant-btn-dangerous.ant-btn-primary,
  .ant-btn-dangerous.ant-btn-primary:hover,
  .ant-btn-dangerous.ant-btn-primary:active {
    background: var(--zk-danger-solid) !important;   /* 亮/暗都用 #b91c1c */
    border-color: var(--zk-danger-solid) !important;
    color: #fff !important;
  }
  ```
  值放进 `web/src/design/tokens.css` 生成链（新增 `--zk-danger-solid: #b91c1c`，暗色可同值或 `#b91c1c`）；也可改 `components.Button.colorError/colorErrorHover`，但会波及所有危险按钮（含 text 型），需回归确认。

## 存疑 / 次要

1. **输入框显示与提交不一致**：去掉 `maxLength` 后 `onChange` 只截 `draft`，不改输入框本身的值——粘 201 个字符时框里显示 201、提交/落库 200（`r3-s4-draft-emoji.json` 的 `cjk201.inputCodePoints=201, sent=200`；`r3-s5b-ui201` 显示 201、发送 200）。建议 onChange 里同步回写被截断的值（受控或 `event.target.value = sliced`），做到"所见即所存"。
2. **远端删除不清本页草稿键**：UI 删除会清（已验证），但另一客户端删除后本页 `zk.draft.<id>` 仍在（`r3-s4` 的 `draftAfterRemoteDelete="远端删除的草稿"`）。建议在 `SessionsProvider` 收到 `session.list.delta.removed` 时 `localStorage.removeItem('zk.draft.'+id)`。
3. claim 里的「101 个 emoji 应被截到 200 码点」按码点语义并不需要截断（101 个 emoji = 101 码点）；实测 101 个 emoji 原样通过、201 个才截到 200——按"码点上限 200"理解是正确行为，此处只记录，不算失败。
4. 菜单按钮仍缺 `aria-haspopup="menu"` / `aria-expanded`（无障碍惯例，非本次 claim）。

## R3 截图与脚本索引

- 截图（16 张）：`audit-sess-r3-01..04`（F1b）、`r3-05/06/07`（F2′ 的 ↓/Enter/Esc）、`r3-08-{dark,light}-menu`、`r3-09-{dark,light}-confirm`（F7）、`r3-10`（草稿清理）、`r3-11..14`（emoji/CJK 边界）
- 脚本：`sess/r3-s1-pin.mjs`、`r3-s2-keyboard.mjs`、`r3-s3-contrast.mjs`、`r3-s4-draft-emoji.mjs`、`r3-s5-emoji-boundary.mjs`、`r3-s5b-ui201.mjs`、`r3-s6c-pin-word.mjs`、`r3-s6b.mjs`；回归复跑：`r2-s3-archive`、`r2-s4-delete-cleanup`、`r2-s5-titles`、`r2-s7-draft`、`r2-s1c-last-to-top`、`r2-s8-regress-core`、`r2-s9-flow`、`r2-s10-race`、`r2-s6-touch-dark`（`ZK_DATA=data-sess5`）
- 数据：`sess/r3-*.json`；上一轮原始数据已备份在 `sess/r2-backup/`

---

# 第四轮复验（2026-11-25 01:51–02:12）

- 被测版本：`2a9110e`「fix(web): ArrowDown opens the row menu, focus comes back, danger buttons are AA」
- 实例：`startB.sh sess6 .cache/bin/zeokeel-sess6 9969 9975`（`data-sess6`）
- gateway.json 自证：`{"epoch":"c6faad14c38a5e42","port":9969,"bundleSha256":"7f2dbb7606885459bf1e22a9f7c2df514b0cde0aa4371973a1b7b729d5133e59","bundleBuiltAt":"2026-11-24T17:50:00Z"}`；产物 `index-B1FX6DEs.js` + `index-D-szANgW.css`（含 `aria-haspopup`、`.ant-btn-dangerous.ant-btn-primary` 覆写）
- 启动自检：`GET /`=200、`/favicon.svg`=200、`/api/capabilities`(无授权)=401 ✅
- 过程说明：首启实例的 fake model 端口 9972 被其它进程占用（`bind: address already in use`），审批类回归因此失败；换 **9975** 重起（bundleSha256 不变）后**全部复跑**，下列数字来自重起后的实例。
- **总结论：三处修好，F2b 仍不成立（且比上一轮多出两条）**
  - ✅ F2a：↓ / Enter / Space 都能开菜单并把焦点放到首项；↑ 不开；`aria-haspopup="menu"` 有了。
  - ❌ F2b：焦点确实"回到了一个 `session-menu` 按钮"，但**回到的是列表最后一行**，不是打开菜单的那一行；**删除**在键盘下根本执行不了（确认框被同一次 Enter 立刻点掉）；**重命名**取消/保存后焦点落在 `BODY`。
  - ✅ F7b：深色实心危险按钮 9.9:1、浅色 6.47:1，hover/active 同值；菜单项仍 8.68 / 6.47。
  - ✅ 所见=所存：控制输入框后 201 码点在框内只剩 200、提交/落库 200。
  - ✅ 回归（F1b / F4 / F5 / F6 / 草稿 / 协议 / 删当前 / 归档往返 / 审批）全绿。

## R4 判定表

| # | 要求 | 判定 | 证据 |
|---|---|---|---|
| 1 F2a | ↓ 和 Enter/Space 开菜单 + 聚焦首项；↑ 不开；`aria-haspopup`；`aria-expanded` 核对 | **PASS** | `↓{open:true, active:LI 重命名, 首项 -active}`、`Enter` 同、`Space` 同、`↑{open:false, active:BUTTON}`；按钮 `aria-haspopup="menu"`、`aria-label="会话操作"`；**`aria-expanded` 不存在**（antd 只加 `ant-dropdown-open` class）→ 存疑 1；`settings.set` 写入 **0**。截图 `r4-01/02` |
| 2 F2b | Esc 后焦点回触发器；执行四项后回触发器；Tab 出去再回来可用；深色/移动端同样 | **FAIL** | 见 R4-F2c / R4-F2d / R4-F2e：焦点回到**最后一行**的触发器（5 行、8 行、19/22 行、深色、移动抽屉均复现）；删除确认框被同一次 Enter 点掉（3/3 次点击目标都是「放弃更改」）；重命名取消/保存后焦点 `BODY` |
| 3 F7b | 真实深色实心危险按钮 ≥4.5；浅色 ≥4.5；hover/active 同值；复测菜单项 | **PASS** | 深色 `color=rgb(17,17,24)` on `bg=rgb(252,165,165)` → **9.9**；浅色 白 on `rgb(185,28,28)` → **6.47**；hover/active 分别同为 9.9 / 6.47；菜单项深 8.68、浅 6.47。`:active` 用 CDP `CSS.forcePseudoState` 强制测量。截图 `r4-06/07-{dark,light}` |
| 4 | 201 码点显示 200/落库 200；200 emoji；清空后取消/确认合理 | **PASS** | 201 汉字 → 框内 **200**、再补打一字符仍 200、提交/DB 200；200 个 😀（400 UTF-16）→ 框内 200 cp、提交/DB 200；清空 + 保存 → 0 请求、弹窗关、标题不变；清空 + 放弃更改 → 0 请求。截图 `r4-08/09` |
| 5 回归 | F1b、F4、F5、F6、草稿、协议、删当前、归档往返 | **PASS** | 见「回归证据」小节 |

## 失败项

### R4-F2c（P1）焦点回到"某一行的"触发器，但不是打开菜单的那一行

- **现象**：任意一行（首行/中间行/末行）打开菜单，按 Esc 或执行「置顶/归档」后，焦点都落到**列表最后一行的** `session-menu`；随后按 Enter 打开的是最后一行那位的菜单。
- **证据**：
  - `sess/r4-s2-focus-return.json`（5 行）：`esc-row-0{打开 焦点五 → after: rowIndex 4 键位一}`、`esc-row-2{键位三 → rowIndex 4}`、`esc-row-4{键位一 → rowIndex 4}`；`execute-returns-focus{在 键位三 上置顶 → after: rowIndex 4 键位一}`（置顶本身作用正确，`pinFrames` 的 sessionId 正确）。
  - `sess/r4-s6-focus-consequence.json`（19 行）：鼠标在第 1 行「开关归档R2」开菜单 → Esc 后 `active=rowIndex 18 焦点四`；接着按 Enter 打开的菜单 `y≈698`，最近的是第 16 行「移动焦点」——**键盘用户会操作到另一条会话**。
  - `sess/r4-s5-dark-mobile.json`：深色 1440（8 行，开第 1 行）→ Esc 后 `rowIndex 7`；移动 390 抽屉（8 行，开第 0 行）→ Esc 后 `rowIndex 7`。
  - `sess/r4-s7b-four-actions.json`（22 行）：`pin → rowIndex 21`、`archive → rowIndex 21`。
- **根因**：`web/src/shell/sidebar.tsx:245-247`
  ```tsx
  ref={(node) => { if (node) lastTrigger.current = node; }}
  ```
  内联 ref 回调对**每一行**都执行（每次渲染还会前一次 null 再赋值），最后执行的是最后一行的按钮；打开菜单时没有记录 `event.currentTarget`。
- **影响**：动作本身正确，但焦点回错位后，紧接着的键盘操作会打开/执行**另一条会话**的动作——误删/误归档风险。
- **建议修法**：把记录点从"渲染"移到"打开"这一刻：
  ```tsx
  onClick={(event) => { lastTrigger.current = event.currentTarget; }}
  onKeyDown={(event) => {
    if (['ArrowDown', 'Enter', ' '].includes(event.key)) lastTrigger.current = event.currentTarget;
    …原有逻辑…
  }}
  ```
  并删掉 `ref` 回调。若担心列表重排后节点失效，可记 `data-session-id`，关闭时用 `document.querySelector('[data-session-id="…"] [data-testid="session-menu"]')` 重新取（按 key 必定能找到）。

### R4-F2d（P1）键盘选「删除」：确认框被同一次 Enter 立刻点掉，键盘用户无法删除

- **现象**：在菜单里用 Enter 选中「删除」→ 确认框**闪一下就消失**，焦点落到 `BODY`，没有发 `session.delete`，行还在。
- **证据**：
  - `sess/r4-s11-delete-trace.json` 的事件时间线：`t=654 keydown LI「删除」` → `t=682 click BUTTON「放弃更改」` → `t=688 keyup` → `t=918 modal-removed`；
  - `sess/r4-s12-delete-repeat.json` 连做 3 次：`clickedByEnter` 全部是 `放弃更改`、`modals=0`、`deleteFrames=0`、`rowGone=false`、`focus=BODY`；
  - 鼠标路径正常（`r4-s9-delete-debug.json`：鼠标点「删除」→ 确认框停留、可取消/确认）。
- **根因**：菜单项上的 Enter `keydown` 没有被 `preventDefault()`；同一次按键的默认动作（"点击当前聚焦的按钮"）落在 antd 确认框打开后**自动聚焦的那个按钮**上（当前是「放弃更改」），于是刚打开的确认框被立刻取消。若 antd 将来把初始焦点放到「删 除」，同样的路径就会**未经确认地真删**。
- **建议修法**（任一即可，推荐 2+3）：
  1. 菜单项激活时阻止默认：在 `document` 捕获阶段监听 `keydown`，若 `event.key === 'Enter'` 且目标在 `.ant-dropdown-menu-item` 内 → `event.preventDefault()`；
  2. 把开框推迟一帧：`await new Promise((r) => setTimeout(r, 0))` 后再 `modal.confirm(...)`，让这枚 Enter 的默认动作先结束；
  3. 危险确认框显式 `autoFocusButton: 'cancel'`（antd `Modal.confirm` 支持），既消灭"Enter 直接确认"的风险，也让默认焦点落在安全按钮上。

### R4-F2e（P2）重命名弹窗取消/保存后焦点落在 `BODY`

- **现象**：菜单里 Enter 选「重命名」→ 弹窗正常（预填正确）→ 点「放弃更改」或「保存」→ `document.activeElement` 是 `BODY`，不是打开菜单的触发器。
- **证据**：`sess/r4-s13-rename-focus.json`：`afterCancel{tag:BODY}`、`afterSave{tag:BODY}`（保存后行已改名 `改名焦点R4-改`，动作本身正确）。
- **期望**：弹窗关闭后把焦点还给同一行的触发器（与 Esc 路径一致）。
- **建议修法**：`modal.confirm({ …, afterClose: () => lastTrigger.current?.focus() })`（`afterClose` 在 antd 5/6 均可用）；配合 R4-F2c 的修复，`lastTrigger` 才是正确的那个。

## 回归证据（本轮复跑）

- **F1b**（`r3-s1-pin`）：置顶 `label="针一号"`、`labelHasMark=false`、`pinCount=1`、`aria=置顶`；改名预填 `针一号`、提交/落库 `针一号改`；取消置顶 `pinCount=0` 无残留；再置顶仍 1 个。
- **F4**（`r2-s4-delete-cleanup`）：受害者 5 条 `session.<id>.*` 全清（`victimKeys={}`，`session.*` 9→4），保留会话 `pinned=true` 完好；`zk.draft.<id>` 删除后为 `null`（`r3-s4`）。
- **F5**（`r2-s5-titles`）：100 / 198 / 200 列表与 DB 一致、无 `…`；201 → `-32602`；UI 200 也存 200。
- **F6**（`r2-s6-touch-dark`）：触摸 `opacity=1`、24×24、每行一个；`aria="会话操作"`。
- **草稿**（`r2-s7-draft`）：A→B→A 保留、刷新保留、发送清空且 B 不受影响。
- **协议**（`r2-s8-regress-core`）：`{epoch,rev}` 8→14；`removed` 差值；空/空格/同名 0 请求；201/控制字符 `-32602`；未知会话 `-32603`；带消息删除 DB `1→0 / 4→0`。
- **删当前**（`r2-s9-flow`）：URL 变 `/`、无错误。
- **归档往返**（`r2-s3-archive`）：`archived true→false`、菜单「取消归档」↔「归档」、隐藏/重现、`showArchived` 设置页生效。
- **审批链**（`r2-s9-flow`）：`web_fetch → 允许一次 → The fetch returned ok`，刷新后 transcript 完整（`http://127.0.0.1:9975/health`）。

## 存疑 / 次要

1. **`aria-expanded` 缺失**：antd 6 不写这个属性（只加 `ant-dropdown-open` class），触发器上为 `null`。claim 是"antd 提供则核对"，故不算失败；若要更完整的 a11y，可用受控 state 补 `aria-expanded={open}`。
2. **菜单内 Tab 会关闭菜单**：Tab 一次到下一行触发器（菜单关闭），Shift+Tab 回来按 Enter 可重新开。功能可用，但不是"菜单内 Tab 循环"的 ARIA 惯例。
3. **远端删除仍不清本页草稿键**（前几轮存疑，本轮 claim 未要求）：另一客户端删除后本页 `zk.draft.<id>` 仍在。
4. 首启实例的 fake model 端口冲突属环境问题（9972 被占），不是产品缺陷；换 9975 后所有审批类回归通过。

## R4 截图与脚本索引

- 截图（30 张）：`audit-sess-r4-01..02`（↓/Space 开+首项聚焦）、`r4-03/04/05`（Esc/执行/Tab）、`r4-06/07-{dark,light}`（危险色）、`r4-08/09`（受控 201、200 emoji）、`r4-10/11`（深色/移动 Esc）、`r4-12`（焦点错位后果）、`r4-13/14-action-*`（四动作两版）、`r4-15/16`（删除焦点）、`r4-17/18`（删除键盘/鼠标对照）、`r4-19`（键盘删除 150ms）、`r4-20`（事件追踪）、`r4-21`（三次重复）、`r4-22/23`（重命名焦点）
- 脚本：`sess/r4-s1-kbd-open.mjs`、`r4-s2-focus-return.mjs`、`r4-s3-danger.mjs`、`r4-s4-controlled.mjs`、`r4-s5-dark-mobile.mjs`、`r4-s6-focus-consequence.mjs`、`r4-s7b-four-actions.mjs`、`r4-s8-delete-focus.mjs`、`r4-s9-delete-debug.mjs`、`r4-s10-delete-kbd2.mjs`、`r4-s11-delete-trace.mjs`、`r4-s12-delete-repeat.mjs`、`r4-s13-rename-focus.mjs`、`r4-clean.mjs`
- 数据：`sess/r4-*.json`；历史备份 `sess/r2-backup/`、`sess/r3-backup/`

---

# 第五轮复验（2026-11-25 02:15–02:35）

- 被测版本：`ce8f5c5`「fix(web): focus returns to the row that opened the menu, and Enter cannot confirm itself」
- 实例：`startB.sh sess8 .cache/bin/zeokeel-sess7 9980 9981`（`data-sess8`）
- gateway.json 自证：`{"epoch":"5a1786105bb4fd9f","port":9980,"bundleSha256":"ab2589e05a1f17c3a806c4345c242b7178738989ce5e2a73b88337239ad5fdbd","bundleBuiltAt":"2026-11-24T18:13:39Z"}`；产物 `index-B-LR0ad6.js`（含 `autoFocusButton` / `afterClose` / `shell.sessions.cancel`）+ `index-D-szANgW.css`
- 启动自检：`GET /`=200、`/favicon.svg`=200、`/api/capabilities`(无授权)=401 ✅
- **总结论：五项全绿，F2c 只剩一个窄口子** —— Esc 焦点回行（5 行 / 19 行 / 深色 / 390 抽屉）、键盘删除全流程、重命名三种关闭方式、焦点闭环、F2a / F7b / 所见=所存 / 回归全部通过；**唯一未达标：在非首行执行「置顶」（行被重排到第 0 位）后，焦点落到 `BODY`，没有跟着那一行走**（同一动作在"行本来就在第一位/归档不重排"时焦点是正确的）。

## R5 判定表

| # | 要求 | 判定 | 证据 |
|---|---|---|---|
| 1 F2c | 5 行与 19 行，第 0 / 中间 / 最后行：Esc 与执行动作后焦点回到**同一行**；深色 1440 与 390 抽屉 | **PASS（Esc）/ 部分（执行动作）** | Esc：5 行 `打开 0/2/4 → after rowIndex 0/2/4`（label 全部对上）；19 行 `打开 0/9/18 → after 0/9/18`；深色 1440 打开第 1 行 → `rowIndex 1 探针三R5`；390 抽屉打开第 0 行 → `rowIndex 0 压D3`。执行：`归档`/`取消归档`（不重排）→ `rowIndex 6 归档执行R5` ✅；`置顶` 在首行（不重排）→ `rowIndex 0` ✅；**`置顶` 在第 6 行（重排到第 0）→ `BODY`** ❌（R5-F2f） |
| 2 F2d | 键盘选「删除」：确认框出现、行数不变、焦点在「取消」；再 Enter 应取消；Tab 到「删除」再 Enter 才删；3 次一致 | **PASS** | 3/3 次：`before rows 45/44/43` → 确认框出现且 `rows` 不变、`active=取 消`、按钮 `[取 消, 删 除]`；再按 Enter → `rows` 不变、**无新增 `session.delete` 帧**、焦点回到该行触发器（`rowIndex 7/6/5` 分别对应 键盘删A/B/C）；重开菜单 → Enter → Tab（焦点到「删 除」）→ Enter → `session.delete` 帧 +1、行消失。截图 `r5-05-delete-*` |
| 3 F2e | 重命名保存/取消/Esc 后焦点回触发器；删除确认取消后同样 | **PASS** | 重命名：预填 `改名焦点R5`、按钮 `[取 消, 保 存]`；`save → rowIndex 5 改名焦点R5-改`、`cancel-click → rowIndex 5`、`esc → rowIndex 5`（弹窗已关）；删除确认取消 → 焦点回该行触发器（见 F2d 的 `afterSecondEnter`） |
| 4 焦点闭环 | 打开 → Esc 回触发器 → 立刻 Enter 再打开，菜单出现在**同一行** | **PASS** | 第 6 行「压D3」：`openedAt menuY=643 / nearest 压D3(y=608,d=5)` → Esc `rowIndex 6 压D3` → Enter `reopened menuY=643 / nearest 压D3(d=5)`。截图 `r5-07` |
| 5 回归 | F2a / F7b / 所见=所存 / F1b / F4 / F5 / F6 / 草稿 / 协议 / 删当前 / 归档往返 | **PASS** | 见「回归证据」 |
| 6 深浅色 + 390 | 上述键盘流程在深色与 390 抽屉同样可用 | **PASS** | 深色 1440：`↓` 开 + 首项聚焦；Esc → `rowIndex 1`；删除确认 `active=取 消`、危险按钮 `rgb(17,17,24)` on `rgb(252,165,165)`；取消 → 焦点回 `rowIndex 1`。390 抽屉：`↓` 开；Esc → `rowIndex 0`；删除确认 `x=8 w=374`、`scrollWidth=390`、`active=取 消`；取消 → 焦点回 `rowIndex 0` |

## 失败项

### R5-F2f（P2）执行「置顶」导致列表重排后，焦点落 `BODY`

- **现象**：在第 6 行打开菜单 → ↑↓ 选中「置顶」→ Enter → 置顶成功（`session.pin` 帧里的 sessionId 正确）、行移到第 0 位，但焦点是 `BODY`，从 120ms 到 2000ms 采样均为 `BODY`（`r5-s2b`、`r5-s5` 两次独立复现）。
- **取消置顶同样复现**：两行置顶（丙、甲）时在第 1 行「取消置甲」执行取消置顶 → 该行离开置顶块、位置变化，焦点同样 `BODY`（`sess/r5-s8-unpin-move.json`：`openedIndex 1、highlighted 取消置顶、after{BODY}`）。
- **对照**（同一动作在不重排时正确）：置顶一个**本来就在第 0 行**的会话 → 焦点 `rowIndex 0 执行焦点R5`（`r5-s2`）；`归档`/`取消归档`（顺序不变）→ `rowIndex 6 归档执行R5`（`r5-s6`）。
- **根因（实测）**：列表重排时行的 DOM 节点被**重新创建**，`lastTrigger.current` 变成游离节点，`focus()` 静默失败。节点重建的探针证据：给 42 行的 `<li>`/按钮都打 `data-rowprobe`，置顶后只剩 **34 行**带探针、第 0 行（被移动的那个会话）`rowProbe=null`——被移动的 8 行是重建的（`r5-s2e-probe3.json`）。另外当前代码在 `onClick` 里 `void handleMenu(...)` 之后**立刻** `requestAnimationFrame(focus)`，而置顶是异步请求，聚焦发生在列表重排之前。
- **影响**：键盘用户置顶后"丢了位置"，下一次 Tab/Enter 从页面顶部开始，而不是停在那条会话。
- **建议修法**：不要再依赖 DOM 引用，改为"按会话 id 在动作完成后重新查找并聚焦"：
  1. 给触发器加 `data-session-id={session.id}`；
  2. `onClick`/`onKeyDown` 只记 `lastSessionId.current = session.id`；
  3. 菜单动作返回 Promise 后（`void handleMenu(...).then(() => requestAnimationFrame(() => { document.querySelector(\`[data-session-id="${lastSessionId.current}"] [data-testid="session-menu"]\`)?.focus(); }))`）再聚焦；模态框路径继续用 `afterClose` 里的同一查询。
  4. 删除/归档导致行消失时，聚焦应退化到"当前会话的触发器或列表首行"，避免落在 `BODY`。

## 回归证据（本轮复跑）

- **F2a**（`r4-s1-kbd-open`）：`↓/Enter/Space → open:true, active=重命名`；`↑ → open:false`；`aria-haspopup="menu"`；`settings.set` 0。
- **F7b**（`r4-s3-danger`）：深色 `9.9 / 9.9 / 9.9`（默认/hover/active），浅色 `6.47 / 6.47 / 6.47`；菜单项 深 8.68、浅 6.47。
- **所见=所存**（`r4-s4-controlled`）：201 汉字 → 框内 200、补打仍 200、提交/DB 200；200 emoji → 200 cp；清空后保存/取消各 0 请求。
- **F1b**（`r3-s1-pin`）：预填 `针一号`、提交/落库 `针一号改`、`pinCount=1`、取消后 0、再置顶仍 1。
- **F4**（`r2-s4-delete-cleanup` + `r3-s4`）：受害者 `session.<id>.*` 全清（16→11 行）、保留会话 pinned 完好；`zk.draft.<id>` 删除后 `null`。
- **F5**（`r2-s5-titles`）：100/198/200 原样（列表与 DB）、201 `-32602`。
- **F6**（`r2-s6-touch-dark`）：触摸 `opacity=1`、24×24、`aria="会话操作"`。
- **草稿**（`r2-s7-draft`）：A→B→A 保留、刷新保留、发送清空且 B 不受影响。
- **协议**（`r2-s8-regress-core`）：`{epoch,rev}` 157→163；空/空格/同名 0 请求；201/控制字符 `-32602`；未知会话 `-32603`；带消息删除 DB `1→0 / 4→0`。
- **删当前**（`r2-s9-flow`）：URL `/`、无错误。
- **归档往返**（`r2-s3-archive`）：`true→false`、菜单「取消归档」↔「归档」、隐藏/重现。
- **删除行消失时序**（`r5-s7-delete-timing`）：确认后 **200ms** 内行消失（`r2-s8` 那次 `rowGone:false` 是该脚本在 1.4s 内读取的时序假象，已用本项复核）。
- **审批/刷新**（`r2-s9-flow`）：`web_fetch → 允许一次 → The fetch returned ok`，刷新 transcript 完整。

## 存疑 / 次要

1. 删除/归档使**行消失**时（`showArchived=false`），焦点落 `BODY` 是"行已不存在"的必然结果；建议退化到首行或当前会话触发器，而不是 `BODY`（与 R5-F2f 同一处修）。
2. 删除确认框的取消文案已从「放弃更改」改为「取消」（合理）；相关旧脚本需同步选择器（本轮已适配 `取\s*消`）。
3. `aria-expanded` 仍未提供（antd 不写）；菜单内 Tab 仍会关闭菜单（现状记录）。

## R5 截图与脚本索引

- 截图（12 张）：`audit-sess-r5-01/02`（5 行、19 行 Esc 焦点）、`r5-03/04`（执行置顶焦点，含失败态）、`r5-05-delete-{键盘删A,B,C}`（键盘删除三次）、`r5-06`（重命名焦点）、`r5-07`（焦点闭环）、`r5-08`（深色删除框）、`r5-09`（390 删除框）、`r5-10`（归档执行焦点）
- 脚本：`sess/r5-s1-focus.mjs`、`r5-s8-unpin-move.mjs`、`r5-s2-execute-focus.mjs`、`r5-s2b-execute-move.mjs`、`r5-s2c-node-probe.mjs`、`r5-s2d-probe2.mjs`、`r5-s2e-probe3.mjs`、`r5-s3-delete-keyboard.mjs`、`r5-s4-rename-focus.mjs`、`r5-s5-loop-dark-mobile.mjs`、`r5-s6-execute-archive.mjs`、`r5-s7-delete-timing.mjs`；回归复跑：`r4-s1-kbd-open`、`r4-s3-danger`、`r4-s4-controlled`、`r3-s1-pin`、`r3-s4-draft-emoji`、`r2-s3-archive`、`r2-s4-delete-cleanup`、`r2-s5-titles`、`r2-s7-draft`、`r2-s1c-last-to-top`、`r2-s8-regress-core`、`r2-s9-flow`、`r2-s6-touch-dark`（`ZK_DATA=data-sess8`）
- 数据：`sess/r5-*.json`；历史备份 `sess/r2-backup/`、`r3-backup/`、`r4-backup/`

---

# 第六轮复验（2026-11-25 02:42–03:00）

- 被测版本：`6c8aec9`「fix(web): keep focus on the row when the action re-orders the list」
- 实例：`startB.sh sess12 .cache/bin/zeokeel-sess10 9983 9984`（`data-sess12`）
- gateway.json 自证：`{"epoch":"1539ab45c6e4b3fa","port":9983,"bundleSha256":"9f9e5319ce722ea74f8416cf0b2796eaeb70de161530a87156f59d14fc3af71d","bundleBuiltAt":"2026-11-24T18:40:06Z"}`；产物 `index-DUeuDv_l.js`（含 `data-session-id`）+ `index-JOrtKam5.css`
- 启动自检：`GET /`=200、`/favicon.svg`=200、`/api/capabilities`(无授权)=401 ✅
- **总结论：F2f 仍未达标，且原因是一个可一行修掉的**选择器错误**：回焦永远落在"列表第一行"，只有当被操作的会话恰好落到第 0 位时才看起来正确（lead 的三行用例正属于这种巧合）。其余（不抢焦点/行消失退化/回归）通过。

## R6 判定表

| # | 要求 | 判定 | 证据 |
|---|---|---|---|
| 1 F2f | 置顶 / 取消置顶（会重排）后焦点落在**被操作会话**的触发器；5 行与 19 行、深浅色、390 抽屉；连续两次 | **FAIL** | 被操作会话落到第 0 位时"碰巧对"（`pin-last`：焦点 rowIndex 0 = 该行）；**一旦不落在第 0 位就是错的**：`unpin` 后「序甲」回到 index 2，焦点却在 **rowIndex 0 序丙**；「旧会话X」置顶后落在 index 1，焦点在 **rowIndex 0 新会话Y**；连续三次置顶（体焦点三/二/一，分别落到 0/1/2）焦点**一直停在 row 0 体焦点三**；深色下「六甲」置顶落到 index 3，焦点在 row 0「六十九7」；390 抽屉同样落在 row 0 的另一行。根因见 R6-F2f |
| 2 不抢焦点 | 动作后把焦点移到输入框/别的会话，1 秒内不得被抢回 | **部分 PASS** | 输入框：置顶后立刻点输入框并输入 `abc`，1.5s 后 `activeElement=TEXTAREA`、值 `abc` ✅；**点"别的会话"行后焦点被拉回侧栏某个触发器**（`r6-s5`：点击「抢焦甲」后 400ms/1600ms 焦点都在「新会话Y」的 `session-menu` 上）——这是同一个选择器 bug 的连带症状（回焦循环始终没找到目标，2 秒内反复把焦点放回第一行） |
| 3 行消失 | 删除 / 归档隐藏后焦点退化到某行触发器而非 BODY | **PASS** | 键盘删除「删除行六」→ 行消失、焦点在 row 0「焦点甲六」的触发器；`showArchived=false` 归档「归档行六」→ 行消失、焦点同样退到 row 0 触发器（都不是 BODY） |
| 4 回归 | Esc 回焦、确认框「取消」默认、执行动作后回焦、F2a/F2d/F2e/F7b 快查；菜单内 Tab 现状 | **PASS（执行动作后回焦仅在落到 row 0 时成立）** | Esc：深/浅色都回到 rowIndex 2 的触发器；删除确认 `active=取 消`、按钮 `[取 消, 删 除]`、Enter 取消后行数不变并回焦 row 0；重命名保存后回焦「末甲改」row 0；F2a：`↓` 开后 `active=重命名`、`↑` 不开；F7b：深色菜单项 8.68、实心危险按钮 9.9（`rgb(17,17,24)` on `rgb(252,165,165)`），浅色 6.47 / 6.47；菜单内 Tab：第 1 次 Tab 仍停在「重命名」，第 2 次 Tab 关闭菜单（现状记录） |

## 失败项

### R6-F2f（P1）回焦选择器写错：永远聚焦"列表第一行"，不是被操作的那一行

- **根因（一行）**：`web/src/shell/sidebar.tsx` 的 `focusSessionTrigger`
  ```tsx
  const selector = sessionId
    ? `[data-session-id="${CSS.escape(sessionId)}"] [data-testid="session-menu"]`   // ← 后代选择器
    : null;
  const target = (selector ? document.querySelector(selector) : null)
    ?? document.querySelector('[data-testid="session-menu"]');                       // ← 永远命中第一行
  ```
  但 `data-session-id` 是写在**按钮自身**上的（`<button data-testid="session-menu" data-session-id={...}>`），所以后代选择器**永远匹配 0 个**，代码每次都走 `??` 回落分支 → 聚焦列表第一个触发器。
- **选择器实证**（`sess/r6-s2-selector-probe.json`，任一按钮）：
  - `[data-session-id="sess-…"] [data-testid="session-menu"]` → **0 个匹配**
  - `[data-session-id="sess-…"][data-testid="session-menu"]` → **1 个匹配**
  - `sampleAttrs = ["type","class","aria-label","data-testid","data-session-id","aria-haspopup"]`（属性就在按钮上）
- **为什么 lead 的用例看起来是对的**：三行里对最后一行置顶，被置顶的行**正好落到第 0 位**，与"回落第一行"重合，所以 `rowIndex 0 = 被置顶行`。本轮独立复现同一现象（`r6-s3`：`pin-last 三甲 → rowIndex 0 三甲`），但只要动作后目标不在第 0 位就立即暴露：
  - `r6-s8`（干净三行 序甲/序乙/序丙）：`pin 序甲(最后一行) → rowIndex 0 序甲 ✅`；`unpin 序甲 → 序甲 回到 index 2，焦点却在 row 0 序丙 ❌`（120–2600ms 采样 7 次全部错）；
  - `r6-s4`：`新会话Y` 已置顶时置顶较早的 `旧会话X` → X 落在 index 1，焦点在 row 0 `新会话Y` ❌；
  - `r6-s9`：连续三次置顶 `体焦点三/二/一`（分别落到 0/1/2），焦点**三次都停在 row 0 体焦点三** ❌；
  - 深色 1440：置顶 `六甲` 后它落在 index 3，焦点在 row 0 `六十九7` ❌；390 抽屉：置顶 `六乙` 后焦点在 row 0 `六十九7` ❌。
- **连带影响**：因为循环始终"没达到目标"，它会在 2 秒内每 80ms 把焦点放回第一行——于是"动作后点了别的会话"也会被拉回侧栏（`r6-s5` 的 click 场景）。
- **次要脆弱点**：`r6-s7` 里出现过一次动作后 1500ms 仍是 BODY 的情况——`attempt` 首次运行时焦点还停在即将卸载的菜单项（`LI`）上，命中"用户自己移动了焦点"的早退分支，随后下拉关闭、焦点落 BODY，循环已退出。建议把"焦点在 `.ant-dropdown` 内"也视为自己的临时状态继续重试。
- **建议修法**（改完这两点即可全绿）：
  ```tsx
  // 1) 同元素选择器
  const selector = sessionId
    ? `[data-testid="session-menu"][data-session-id="${CSS.escape(sessionId)}"]`
    : null;
  // 2) 早退条件排除我们自己的临时表面
  const active = document.activeElement;
  if (active instanceof HTMLElement && active !== document.body && active.isConnected) {
    if (active.closest('.ant-dropdown')) { /* 继续重试 */ }
    else if (!active.matches('[data-testid="session-menu"]')) return;
    else if (sessionId && active.getAttribute('data-session-id') === sessionId) return;
  }
  ```
  或者更省事：把 `data-session-id` 加在**行容器**（`li` 由 antd 生成，可改用 `item.className`/`data` 透传）上，保留现有后代选择器。

## 通过项证据

- **不抢焦点（输入框）**：置顶后立刻 `click` 输入框并 `type('abc')`，1500ms 后 `activeElement=TEXTAREA`、`composer="abc"`（`r6-s5`）。
- **行消失退化**：删除 → row 0 触发器；归档隐藏 → row 0 触发器（`r6-s6`）。
- **Esc 回焦**：深/浅色均 `rowIndex 2`（`r6-s10`）。
- **确认框默认取消**：`active=取 消`、按钮 `[取 消, 删 除]`；Enter 取消后行数 33→33、`session.delete` 帧 0、焦点回触发器（`r6-s7`）。
- **重命名回焦**：保存后 `rowIndex 0 末甲改`（`r6-s7`）。
- **F2a**：`↓` 打开且 `active=重命名`；`↑` 不打开（`r6-s7`）。
- **F7b**：深色菜单项 **8.68**、实心危险按钮 **9.9**；浅色 **6.47 / 6.47**（`r6-s10`，含 computed 值）。
- **菜单内 Tab 现状**：第 1 次 Tab 仍停在首项，第 2 次 Tab 关闭菜单并落到某个触发器（`r6-s10` 记录）。

## R6 截图与脚本索引

- 截图（16 张）：`audit-sess-r6-01..06`（5 行/19 行/深色/390 的置顶回焦）、`r6-07`（反例）、`r6-08`（置顶落到 index 1）、`r6-09/10`（不抢焦点）、`r6-11/12`（行消失退化）、`r6-13/14`（pin/unpin 时间线）、`r6-15-{dark,light}`（F7b/Esc/确认框快查）
- 脚本：`sess/r6-s1-f2f.mjs`、`r6-s2-selector-probe.mjs`、`r6-s3-counterexample.mjs`、`r6-s4-pin-below.mjs`、`r6-s5-no-steal.mjs`、`r6-s6-steal-tab-disappear.mjs`、`r6-s7-regress.mjs`、`r6-s8-timeline.mjs`、`r6-s9-body-flake.mjs`、`r6-s10-quick.mjs`
- 数据：`sess/r6-*.json`

---

# 第七轮复验（2026-11-25 02:51–03:15）—— 定点复验

- 被测版本：`f7ede28`「fix(web): the focus selector was a descendant of itself」
- 实例：`startB.sh sess13 .cache/bin/zeokeel-sess11 9985 9986`（`data-sess13`）
- gateway.json 自证：`{"epoch":"bace8596b8efcf74","port":9985,"bundleSha256":"6fae7c7e0b4a45011cbcd133e1dfcf6eb2b92e6e22e9b650d2d35edb6d5feeb9","bundleBuiltAt":"2026-11-24T18:49:59Z"}`；产物 `index-BxbjUMKE.js` 内含同元素选择器 `[data-testid="session-menu"][data-session-id`
- 启动自检：`GET /`=200、`/favicon.svg`=200、`/api/capabilities`(无授权)=401 ✅
- **总结论：F2f 的核心修复通过（置顶落到 index≥1 / 取消置顶 / 深色 / 390 / 连续三次全部命中被操作行）；但"不抢焦点"里的"点别的会话"仍不成立** —— 点击另一个会话后焦点会被拉回上一次操作的触发器（mousedown 时确实是 BODY，mouseup/导航后被拉回），并且**任何一次列表增量**（哪怕别的客户端新建会话）都会把焦点从 BODY 拉回侧栏。

## R7 判定表

| # | 要求 | 判定 | 证据 |
|---|---|---|---|
| 1 | 置顶落到 **index ≥ 1** → 焦点是被操作行 | **PASS** | 4 行（定点丁@0 → 定点甲@1）✅；干净三行（零丙@0、零乙@1、零甲@2；三次连续置顶各自命中）✅；深色 1440（暗乙@0 → 暗甲@**1**）✅；390 抽屉（移乙@0 → 移甲@**1**）✅ |
| 2 | **取消置顶**（回中/尾部）→ 焦点是它自己 | **PASS** | 4 行：定点甲从 index 1 取消置顶 → 落在 index **3**，焦点 `定点甲` ✅；深色：暗甲落在 index **5**（其它未置顶会话更新），焦点 `暗甲` ✅；390：移甲落在 index **3**，焦点 `移甲` ✅ |
| 3 | 深色 1440 与 390 抽屉各一次 | **PASS** | 见上（`r7-s2c-dark-mobile.json`） |
| 4 | 连续三次置顶（终位 0/1/2）聚焦各自行 | **PASS** | 干净三行：`零丙@0 / 零乙@1 / 零甲@2`，三次 `pass=true`（`r7-s1b-trio012.json`） |
| 5 | 动作后点别的会话/输入框，1.5s 内不得被抢回 | **FAIL（别的会话）/ PASS（输入框）** | 输入框：置顶后点输入框输入 `xyz`，1.5s 后 `activeElement=TEXTAREA`、值 `xyz` ✅。别的会话：mousedown 时焦点确实变成 `BODY`，**mouseup（导航触发的重渲染）后焦点跳回上一次操作的触发器**，200ms–3s 采样均在侧栏（`r7-s7-probe`）；即使动作已过去 2.6s 再点也会被拉回；另外"点空白处让焦点落在 BODY"之后，**别的客户端新建会话**造成的一次列表增量也会把焦点拉回侧栏（`r7-s8-delta-steal`）。见 R7-F2g |
| 6 | 快查：Esc 回焦、删除确认「取消」默认、行消失退化 | **PASS** | Esc：深/浅都回到 rowIndex 2 的触发器；删除确认 `active=取 消`、按钮 `[取 消, 删 除]`、直接 Enter → 行数不变、`session.delete` 帧 0、焦点回该行；Tab 到「删 除」再 Enter → 行消失，焦点退化到 row 0 的触发器（非 BODY）；`showArchived=false` 归档 → 行消失、焦点同样退到 row 0 触发器；F7b 深色菜单项 8.68 / 实心危险按钮 9.9，浅色 6.47 / 6.47；菜单内 Tab：第 1 次仍停在首项，第 2 次关闭（现状） |

## 失败项

### R7-F2g（P2）`useEffect` 依赖用了每次渲染都新建的数组、且没有时间窗 → 任何重渲染都可能把焦点夺回侧栏

- **现象**：上一次操作后焦点落在触发器上；用户点击另一个会话（mousedown → `BODY` → mouseup/导航重渲染）后，焦点跳回**上一次操作那个会话**的触发器（不是刚点的行）；即使先点空白处让焦点落在 `BODY`、再等别的客户端新建一个会话（一次列表增量），焦点同样被拉回侧栏。
- **证据**：
  - `sess/r7-s7-probe.json`：`settled=BUTTON(session-menu, 探二)` → `after-mousedown=BODY` → `after-mouseup=BUTTON(探二)` → `after-1s=BUTTON(探二)`；
  - `sess/r7-s8-delta-steal.json`：`settled=BUTTON` → 点空白 `BODY` → 另一客户端 `session.create` 后 `BODY → BUTTON(三角二)`；
  - `sess/r7-s4-isolated.json` / `r7-s5-late-click.json`：动作后 60ms、2.6s 再点别的会话，焦点都在上一次操作的触发器上。
- **根因**：`web/src/shell/sidebar.tsx`
  ```tsx
  const visible = showArchived ? sessions : sessions.filter((session) => !session.archived); // 每次渲染新数组
  useEffect(() => {
    if (!lastSessionId.current) return;
    if (document.activeElement instanceof HTMLElement && document.activeElement !== document.body) return;
    focusSessionTrigger(lastSessionId.current);
  }, [visible]);   // ← 依赖每次渲染都变，等于"每次渲染都跑"
  ```
  它只想在"列表因重排/删行吃掉焦点"时补一次，但：① 依赖是不稳定数组 → 导航、任何列表增量引起的重渲染都会执行；② 只看 `activeElement === body`，不判断"这次失焦是不是我们造成的"，也没有时间窗；③ `lastSessionId` 永不失效，几天后仍然有效。
- **期望**：只有"我们自己的动作导致的失焦"才回焦，且应在一个短窗口内；用户主动把焦点移走（点击/导航）后不得再抢。
- **建议修法**（两处小改）：
  ```tsx
  // 1) 稳定依赖：会话 id/标志拼成的签名，而不是每次渲染的数组
  const listKey = sessions.map((s) => `${s.id}:${s.pinned ? 1 : 0}:${s.archived ? 1 : 0}`).join('|');
  // 2) 只在动作后的短窗口内、且用户没有主动操作时补焦
  const pendingFocusUntil = useRef(0);
  const markPendingFocus = (id: string) => { lastSessionId.current = id; pendingFocusUntil.current = Date.now() + 2000; };
  useEffect(() => { if (Date.now() < pendingFocusUntil.current) focusSessionTrigger(lastSessionId.current); }, [listKey]);
  // 3) 任何 pointerdown / keydown（用户主动）立即清掉 pendingFocus
  ```
  菜单项 `onClick`、触发器 `onClick/onKeyDown` 里用 `markPendingFocus(...)` 代替直接赋 `lastSessionId`；`focusSessionTrigger` 的循环也以 `pendingFocusUntil` 为界。这样"点别的会话"（pointerdown 清 pending）与"外部增量"（超出窗口/未标记）都不会再抢焦点。

## 通过项与复跑说明

- **F2f 核心**：`r7-s1-light`（4 行：index 1 / tail 3 / 连续三次）、`r7-s1b-trio012`（0/1/2 三连）、`r7-s2c-dark-mobile`（深色 & 390：index 1 与 tail）全部 `pass=true`，被操作行与焦点 `data-session-id` 一一对应。
- **快查**：`r7-s3-quick`（Esc / 删除默认取消 / Tab 到删除 / 行消失退化）、`r7-s4-isolated`（归档消失退化）、`r6-s10-quick` 复跑（深/浅 F7b、Esc、默认取消、菜单 Tab 现状）。
- 说明：`r7-s2b` 初次运行因我自己的脚本把"会话 id"当标题传进 DOM 查询而失败（非产品问题），已重写为 `r7-s2c` 并按 label/id 分离。
- 数据：`sess/r7-*.json`；截图 `audit-sess-r7-01..07`。

---

# 第八轮复验（2026-11-25 03:04–03:30）—— 最后一轮定点

- 被测版本：`d558545`「fix(web): a restore never outlives the user's own gesture」
- 实例：`startB.sh sess14 .cache/bin/zeokeel-sess12 9987 9988`（`data-sess14`）
- gateway.json 自证：`{"epoch":"ff172701283060de","port":9987,"bundleSha256":"352d6bca1790d7d76ec428b380686c8a5542a95f4c59f38224631f9eb1c9927b","bundleBuiltAt":"2026-11-24T19:01:24Z"}`；产物 `index-CEhx5wRm.js`（含手势取消逻辑的字符串 `ant-dropdown, [data-testid="session-menu"]`）
- 启动自检：`GET /`=200、`/favicon.svg`=200、`/api/capabilities`(无授权)=401 ✅
- **总结论：通过** —— F2g 在真实操作节奏下不再夺焦（≥100ms 点击 5/5 为 `BODY`；点空白、外部列表增量后也保持 `BODY`），回焦本身仍准确（置顶落 0/1、取消置顶落中部/尾部），深色与 390 抽屉同样；Esc / 删除默认「取消」/ 行消失退化 / F2a / F7b 均不变。**唯一残留是一段约 30–60ms 的合成竞态窗口**（见 R8-caveat），人类操作不可达，记为待选优化。

## R8 判定表

| # | 要求 | 判定 | 证据 |
|---|---|---|---|
| 1 | F2g：动作后立刻点别的会话/空白/外部增量，焦点不得回侧栏 | **PASS** | 置顶后点另一个会话：延迟 100/100/100/400/1000ms 五次全部 `activeElement=BODY`（`r8-s8-rate.json`，5/5）；点页面空白：`BODY`，1.5s 后仍 `BODY`，随后**另一客户端新建会话**的列表增量来了也仍 `BODY`（`r8-s2-details.json`）；浅色 `r8-s1` 同结论 |
| 2 | 回焦仍要工作：置顶落 index≥1、取消置顶回中/尾 | **PASS** | 终乙→**0**、终甲→**1**（4 行用例：定点丁 0 → 定点甲 **1**）、取消置顶 → 终甲 **3** / 深甲 **5** / 摸甲 **4**，焦点 `data-session-id` 均为被操作行（`r8-s1`、`r8-s3`、`r7-s1b` 三连 0/1/2） |
| 3 | 深色 1440 与 390 抽屉各一次 | **PASS** | 深色：深乙@0、深甲@**1**、取消置顶后深甲@**5**；390：摸乙@0、摸甲@**1**、取消置顶后摸甲@**4**；深色下"点别的会话"（延迟 ≥100ms）焦点为 `BODY`（`r8-s8-rate` 即在 dark 上下文跑的 5/5） |
| 4 | 快查：Esc、删除默认取消、行消失退化、F2a/F7b | **PASS** | Esc → 打开行 rowIndex 3「细二」原行；删除确认 `active=取 消`、按钮 `[取 消, 删 除]`、Enter → 行数不变、`session.delete` 帧 0；Tab 到「删 除」→ Enter → 行消失、焦点退化到 row 0 触发器（非 BODY）；F2a：`↓` 开+首项「重命名」、`↑` 不开、`Enter` 开+首项；F7b：深 菜单项 **8.68** / 实心按钮 **9.9**，浅 **6.47 / 6.47**；菜单内 Tab 现状：第 1 次停留、第 2 次关闭 |

## 残留（P3，非阻塞）：约 30–60ms 的合成竞态

- 现象：用脚本在动作后 **30ms / 60ms** 就去点另一个会话（4/4 次），焦点会被拉回上一次操作的触发器；把延迟放到 **100ms 及以上**就全部是 `BODY`（5/5）。人类从"按 Enter 选中菜单项"到"鼠标点另一行"至少要 100ms 以上，所以实际不可达。
- 证据：`sess/r8-s8-rate.json`（30/30/60/60ms → `BUTTON.session-menu`；100/100/100/400/1000ms → `BODY`）；`sess/r8-s6-trace2.json` 的事件时间线（pointerdown 743ms → click 744ms → 764ms 触发器再次 `focusin`），与 antd 下拉关闭时的回焦时序一致。
- 若想彻底消除：在菜单动作后 120ms 内屏蔽下拉的关闭回焦（例如动作后先 `document.activeElement.blur()`，或给触发器加 `data-suppress-focus` 并在 `focusin` 时检查），也可直接接受。

## 本轮证据索引

- 截图：`audit-sess-r8-01-restore.png`、`r8-02-click-away.png`、`r8-03-blank-delta.png`、`r8-04-{dark,mobile}.png`
- 脚本：`sess/r8-s1-final.mjs`、`r8-s2-details.mjs`、`r8-s3-dark-mobile.mjs`、`r8-s4-dark-trace.mjs`、`r8-s5-dark-clean.mjs`、`r8-s6-trace2.mjs`、`r8-s7-whofocus.mjs`、`r8-s8-rate.mjs`、`r8-s9-f2a.mjs`（+ `r6-s10-quick` 复跑）
- 数据：`sess/r8-*.json`
