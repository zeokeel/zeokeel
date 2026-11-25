# 全站「假入口 / 静默失败 / 出不来」功能可用性深审（第二轮 · dead）

- 实例：`http://127.0.0.1:9874`（data-dead，会话 `sess-1795524825202819096` / `sess-1795524830210629101`）
- 覆盖路由：`/`、`/inbox`、`/c/:id`、`/c/:id/{trajectory,memory,context,files,guide}`、`/settings/{model,behavior,appearance,notifications,network,lsp,archive,data-privacy,about}`、`/automation`、`/capabilities`、`/insights`、`/team`（21 条路由全走）
- 覆盖流程：**50 个**；截图：**84 张**（`shots/r2-dead-*.png`）；记录的操作：**54 个脚本化交互步骤 + 60 余次单点探针**（每次点击都抓 `/api/**` HTTP 响应体与 WS 帧）
- 判定口径：BRIEF2 §1。**假入口判据**＝点击后 2s 内无任何 WS 请求方法 / HTTP 请求、且 DOM 无变化（本次两轮复测 + 换路由复测）

> 说明：本产品的控制面是 WS JSON-RPC（`/ws`，一行一帧），只有自动化/文件/洞察/能力/记忆是 HTTP `/api/**`。所以"有没有发请求"同时看 HTTP 与 WS `method`，不看页面导航。

---

## 流程总表

| # | 流程 | 结论 | 类型 | 证据 |
|---|---|---|---|---|
| 1 | 自动化·新建：用界面帮助文案自己的示例 `every 24h` 保存 | 400，失败原因被丢弃，错误被弹窗遮住 | **S** | `400 POST /api/automation` → `{"error":{"code":-32603,"message":"automation: schedule \"every 24h\": every needs a whole number of seconds like \"30s\""}}`；界面只显示 `automation create failed: 400 Bad Request`；`.zk-msg-error` rect `[288,173,1128,20]`，`elementFromPoint` 命中弹窗内 `INPUT.ant-input`（covered=true）；`r2-dead-automation-400-every24h.png` |
| 2 | 自动化·新建：再试 `daily 09:00`（同一弹窗） | 同样 400，界面**零变化** | **S** | 第 2 次点击 `domChanged:false`（alert 文本相同、弹窗不关）→ 用户看到"点保存没反应"；`r2-dead-automation-400-daily0900.png` |
| 3 | 自动化·新建：合法 `0 8 * * *` | 成功创建 | 通过 | `200 POST /api/automation` → task `auto-1795525247879847949`，`nextRun:2026-11-25T08:00:00+08:00`；`200 GET /api/automation`；行出现 |
| 4 | 自动化·启用/停用开关 | 成功 | 通过 | `200 PATCH /api/automation/{id}`，`aria-checked` true→false |
| 5 | 自动化·立即运行 | 成功，并真的起了一轮 | 通过 | `200 POST /api/automation/{id}/run` + `200 GET`；`runHistory:[{status:"delivered"...}]`；toast「审批等待：web_fetch（自动化）」 |
| 6 | 自动化·运行历史 | 成功，可跳会话 | 通过 | 弹窗「运行历史 · 每天读 README / success / 2026/11/24 21:00:52 / ran in chat session sess-1795525252678387647 / 打开会话」，`href=/c/sess-1795525252678387647` |
| 7 | 自动化·编辑 | 成功 | 通过 | `200 PATCH` → 标题变「每天读 README（改）」，列表即时刷新 |
| 8 | 自动化·删除（Popconfirm） | 成功，有二次确认 | 通过 | 弹层「删除「每天读 README（改）」？ 取消 确定」→ `200 DELETE` + `200 GET {"tasks":[]}`；`r2-dead-automation-delete-popconfirm.png` |
| 9 | 自动化·刷新后持久 | 成功 | 通过 | 重载 `200 GET /api/automation` 返回同一任务 |
| 10 | 设置→关于·检查更新 | 有效（WS） | 通过（无进度提示） | `WS update.check`；「上次检查」21:03:14 → 21:04:39 |
| 11 | 设置→关于·下载更新 | 有实际效果，但 12s 内界面零反馈 | **S** | `WS update.download` 发出后 12s 无 result；服务端已落盘 `data-dead/updates/0.4.0-zeokeel-0.4.0-linux-amd64.AppImage`（25,266,680 B）+`pending.json`；无进度、无取消、无 toast |
| 12 | 设置→关于·安装并重启 | 破坏性动作，无二次确认 | **U** | `update-status.tsx:142` 直接 `installUpdate(true)`（无 Popconfirm）；`gateway/update_surface.go:223-233` 写 marker + `requestShutdown` → 浏览器页面立即失去网关（只剩"连接已断开，正在重连…"） |
| 13 | 设置→关于·dev 构建的更新状态 | 误报"有可用更新" | **S**（误导） | 当前版本 `dev`（比较值 0.0.0，`update.go:290-296`）→ 永远 `< 0.4.0`，`0.4.0 有可用更新` |
| 14 | 设置·通知页 4 个开关 | 存了，但浏览器端无任何行为变化 | **H** | 4×`WS settings.set` + `settings.delta`，「已保存」，刷新后仍 true；`ui.notifications` 全仓无消费者（`contracts/interaction.go:98` 仅定义常量和注释）；`ui.notifications.system` 只喂 `host/notify_sink.go:41`（托盘壳） |
| 15 | 设置·开机自启 | 存了，实际失败且界面显示"已保存" | **S** | `WS settings.set ui.autostart=true`；日志 `ZeoKeel: autostart: create autostart dir "/root/.config/autostart": mkdir /root/.config/autostart: read-only file system`；界面无任何提示 |
| 16 | 设置·归档页唯一开关（在侧栏显示已归档会话） | 存了，无消费者；且没有任何东西能被归档 | **H + M** | `WS settings.set ui.sessions.showArchived=true` 持久化；`grep ui.sessions.showArchived` → 只有 `ia.ts:297` + `contracts/chat.go:547`，无读取方；侧栏无归档区（`r2-dead-archive-after-save.png`） |
| 17 | 设置·网络页 保存 HTTP 代理 | 存了，要重启进程才生效 | **H** | `settings.set net.proxy` 持久化；`applyProxySettings` 只在 `host/serve.go:50`、`host/host.go:333`（进程启动）调用，无 watcher（只有 `WatchAutostart`）；页面无"需重启"提示 |
| 18 | 设置·网络页 搜索端点/API Key | 有效 | 通过 | `toolkit.go:235-239` 读取 `web.search.endpoint` / `web.search.apiKey` |
| 19 | 设置·LSP 页 | 只能存 JSON，没有服务器管理 | **M** | 页面按钮清单只有 `新建会话 / 清除(搜索框) / 关闭设置 / 放弃更改 / 保存`；1 个开关 + 3 个 JSON 文本域；无"添加/删除服务器""测试连接" |
| 20 | 设置·数据与隐私 输入非法 JSON 保存 | 非法值被静默写入 | **S/H** | `tools.disabled = not-json{` → `WS settings.set` 成功、"已保存"、无 alert、刷新后值仍在（`r2-dead-privacy-invalid-json.png`）；`settings-keys.ts:4` 注释："`settings.set` validates shape only" |
| 21 | 设置·数据与隐私 导入/清除/隐藏文件 | 全部没有入口 | **M** | 7 个 textarea，无导入/导出/清除数据按钮 |
| 22 | 设置·行为页 子代理并发填 `-5` | 非法值被接受 | **H** | `WS settings.set` 成功，「已与网关快照一致」，输入框留着 -5（`r2-dead-settings-bad-number.png`） |
| 23 | 设置·侧边导航搜索 | 有效 | 通过 | 输入「字号」→ 菜单只剩「外观 字段 1 项」；`r2-dead-settings-search-fontsize.png` |
| 24 | 设置·切语言后侧栏导航不跟随 | 只有内容区翻译，导航停留旧语言 | **H** | 保存 `ui.language=en`：正文变 `Appearance / Live snapshot…`，菜单仍是「常用/模型/行为与限额/界面与通知/外观/通知/连接/…」；`settings-screen.tsx:116` `}, [pages, matches]);` 缺 `t` 依赖；`r2-dead-appearance-en.png` |
| 25 | 设置·脏数据按 Esc（守卫） | 有效 | 通过 | 弹窗「放弃未保存的更改？ 这一页的草稿不会保存。 取消 / 丢弃更改」→ 丢弃后回 `/` |
| 26 | 收件箱·允许一次 | 有效 | 通过 | `WS approval.resolve`（`{"id":"appr-4","decision":"allow"}`）+ `approvals.delta` removed；行消失；toast「会话「把 README 里的安装步骤补充完整…」已完成」 |
| 27 | 收件箱·两条排队 | 有效 | 通过 | 「2 项等待你」两行；决定一条 → 「1 项等待你」；`r2-dead-inbox-2rows.png` / `r2-dead-inbox-after-one-decided.png` |
| 28 | 收件箱·待审批行的信息 | 标题是裸 JSON | **S**（可读性） | 行文本 `待审批 {"url":"http://127.0.0.1:9884/health"} web_fetch 拒绝 允许一次` —— 与线程内审批卡（地址/影响/来自本会话/允许范围）完全不对等 |
| 29 | 收件箱·待审批行跳到来源会话 | 没有入口 | **M** | `rowButtons:[["拒绝","允许一次"],…]`，`links:[]`；`api/inbox.ts:59` 已把 `approval.sessionId` 放进 item，UI 从未使用 |
| 30 | 收件箱·「查看」（ask/plan/artifact） | 死按钮 | **F**（代码确认） | `inbox-screen.tsx:109` 只对 `artifact && !sessionId` 置 disabled；`inbox.ts` 里 ask/plan 从不带 `sessionId` → 按钮可点、`onClick` 里 `if (item.sessionId)` 为假 → 什么都不发生。本实例的 fake model 只产出 `tool` 场景，无法现场造出 ask/plan/artifact，故仅静态证据 |
| 31 | 命令面板·Mod+K 打开并聚焦 | 有效 | 通过 | `dialogs 0→1`，7 行，`document.activeElement` = 命令搜索输入框 |
| 32 | 命令面板·无结果 + Enter | 正确无操作 | 通过 | 「没有匹配的命令」，无请求、URL 不变 |
| 33 | 命令面板·↓↓ + Enter | 有效 | 通过 | active=`前往 收件箱` → 执行后 `/inbox` |
| 34 | 命令面板·Esc 关闭后重开 | 有效（查询清空） | 通过 | 重开 input value = `""` |
| 35 | 命令面板·新建会话 | 有效 | 通过 | `WS session.create` → `/c/sess-1795525637699929548` |
| 36 | 命令面板·缺「前往 团队」 | 缺一条命令 | **M**（小） | `command-palette.tsx:44-83` 7 条命令无 team；`i18n/zh.ts` 有 `palette.goTeam`（"前往 团队"）未被引用；另外每行末尾直接渲染原始 group id `go`（`前往 收件箱 go`）|
| 37 | 消息·复制（有剪贴板权限） | 有效 | 通过 | tooltip 变「已复制」，`navigator.clipboard.readText()` 与消息文本一致 |
| 38 | 消息·复制（无剪贴板权限） | 静默失败 | **S** | `message-actions.tsx:46-49` catch 里只 `setCopied(false)`，无 toast、无禁用、无提示 |
| 39 | 消息·编辑 | 有效 | 通过 | 弹窗「编辑这条消息 / 修改后会从这里重跑…」带原文，按钮「取消 / 保存并重跑」 |
| 40 | 消息·更多操作 | 能开，但菜单只有 1 项 | 通过（结构冗余） | 下拉仅「重新生成」（`message-actions.tsx:101-117`）——一个"更多"菜单装一个动作 |
| 41 | 消息·重新生成 | 有效 | 通过 | `200 POST /api/chat/run` + `approvals.delta added appr-5`（`r2-dead-regen-after.png`） |
| 42 | 会话·重命名/删除/归档/固定 | 全无入口 | **M** | 右键侧栏项只弹导航菜单（`contextMenus:["收件箱 能力 自动化 洞察 团队 设置"]`）；行内无按钮；`ws-contract` 里 `session.delete/rename/pin/archive/fork/model.set/compact…` 全部"延后"（`TRANSPORT.md:545-559`）；`web/src` 无任何 `session.delete` 调用 |
| 43 | 会话·轨迹/记忆/上下文页签 | 有效 | 通过 | `/trajectory`（`session.snapshot`）、`/memory`（`200 GET /api/sessions/{id}/memory`）、`/context`（`context.snapshot`+`context.subscribe`），URL 与内容都对 |
| 44 | 团队·新建队友 / 关闭 | 有效 | 通过 | `WS team.spawn` → 成员 `running`、活动 `spawn`；`WS team.close` → 活动 `close`（`r2-dead-team-after-spawn.png`、`r2-dead-team-closed.png`） |
| 45 | 洞察·近 30 天 | 有效 | 通过 | `200 GET /api/insights?range=30`，URL 变 `?range=30` |
| 46 | 能力·连接器页签 | 只能看，不能加 | **M** | `?tab=connectors` 切换正常，但 0 连接器、空状态，全页只有「新建会话」一个按钮 |
| 47 | 文件检查器·「文件」页签 | 死路：点了没反应 | **F** | 从 `/c/{id}/files` 点「文件」→ URL 仍 `/files`、`guideStill:true`、`treeVisible:0`、`domChanged:false`；`inspector-panel.tsx:121` `guide = relative==='' && !view`，`:140` 的 onClick 又导航回同一个 `/files` → 永远停在 guide |
| 48 | 文件检查器·文件树 | 只能手改 URL 才能进 | **F/U** | `200 /api/files/tree` 已返回 `README.md`、`notes.txt`；只有深链 `/c/{id}/files/README.md` 才渲染树 + 预览（`r2-dead-inspector-deeplink.png`）|
| 49 | 文件检查器·「改动」 | 裸错误盖住了空状态 | **S** | `/files/README.md?view=diff` → 「file diff failed: 404 Not Found」（`inspector.noDiff`「没有改动。」永不出现）|
| 50 | Now 页首条消息 | 有效 | 通过 | 输入 → 创建会话 → `/c/sess-1795526032277567021` 带文本进入，直接触发审批卡 |

**分布（50 行）**：通过 **26** ／ **M 6**（#19、#21、#29、#36、#42、#46）／**F 2**（#30、#47）／**F+U 1**（#48：文件树只能手改 URL 进）／**S 9**（#1、#2、#11、#13、#15、#20、#28、#38、#49）／**U 1**（#12）／**H 5**（#14、#16 兼 M、#17、#22、#24）。按 BRIEF2 五类归并（含兼类）：**M 7、F 3、S 9、U 2、H 5**。

---

## 发现

### D-01 自动化：界面教你的两种写法都被后端拒绝，而错误被弹窗吃掉 [P0 · S]
- **操作**：`/automation` → 新建任务 → 计划表达式填 `every 24h`（帮助文案原话）→ 保存；再改成 `daily 09:00`（帮助文案原话）→ 保存；最后填 `0 8 * * *`。
- **网络**：
  - `400 POST /api/automation` `{"error":{"code":-32603,"data":null,"message":"automation: schedule \"every 24h\": every needs a whole number of seconds like \"30s\""}}`
  - `400 POST /api/automation` `… "daily 09:00": time \"09:00\" must be HH:MM:SS with optional one to three fractional digits`
  - `200 POST /api/automation`（`0 8 * * *`）→ 创建成功
- **DOM/截图**：弹窗不关、字段无红框、无字段级错误；页面级 `.zk-msg-error` 在 `[288,173,1128,20]`，`elementFromPoint` 命中弹窗里的 `INPUT.ant-input`（被遮挡），截图只剩左侧一截红字 `automation create failed: 400`；第二次失败的 `domChanged=false`（与上一次文案相同），即"再点一次什么都没发生"。`r2-dead-automation-400-every24h.png`、`r2-dead-automation-400-daily0900.png`
- **代码**：
  - 帮助文案 `web/src/i18n/zh.ts:340`、`en.ts:336`：`五段 cron（如 0 8 * * *）或 every 24h / daily 09:00。`
  - 真实语法 `internal/contracts/automation.go:667-676`（`every` 只收 `<N>s`）、`:546-567`（`daily` 只收 `HH:MM:SS`）
  - 错误信息在客户端被丢弃：`web/src/api/read-models.ts:70` `throw new Error(\`${what} failed: ${res.status} ${res.statusText}\`)` —— 响应体里的可读原因永远到不了界面
  - 错误容器在页面而不是弹窗内：`features/automation/automation-screen.tsx:148`
- **对照（DSH）**：`dsh-client-ui-*` 的设置/表单类面板把服务端错误落在字段旁的 `hint/error` 区并以弹窗内 Alert 呈现；DSH 的 schedule 选择器是下拉预设（every N minutes / daily HH:MM / cron），不靠用户记语法。
- **修法**：① 帮助文案改成后端真正接受的 `every 30s`、`daily 09:00:00`；② 更好：把计划表达式做成"频率下拉 + 时间选择"，cron 作为高级选项；③ `readJson` 把 `error.message` 抽出来；④ 错误渲染进 Modal 内部（或 `message.error`），并在字段上标红。

### D-02 文件检查器：「文件」页签是死路，文件树从 UI 不可达 [P0 · F]
- **操作**：`/c/{id}/files` → 点右侧「文件」页签 → 无任何变化；再点「开始」→ 回 guide；再点「文件」→ 仍 guide。
- **网络**：这一串点击 **零请求**；`200 /api/files/tree` 其实已经返回 `{"root":"…/workspace","nodes":[{"path":"README.md"…},{"path":"notes.txt"…}]}`。
- **DOM/截图**：`guideStill:true`、`treeVisible:0`、`domChanged:false`，URL 始终 `/c/{id}/files`；`r2-dead-inspector-files-tab-noop.png`。只有手改地址到 `/c/{id}/files/README.md` 才出现树 + 预览（`r2-dead-inspector-deeplink.png`）。
- **代码**：`web/src/features/inspector/inspector-panel.tsx:121` `const guide = relative === '' && !searchParams.get('view')`；`:136-143`「文件」页签 `onClick={() => navigate(\`/c/${sessionId}/files\`)}` —— 而在 `/files` 且无文件路径时，这个导航的目标就是当前 URL，`guide` 仍为 true。树只在 `!guide` 时渲染（`:162-176`）→ 死循环。**额外的锁死点**：预览里的面包屑「工作区根目录」（`:190-200`）也导航到同一个 `/files`，所以用户一旦从文件退回根，就再也回不到树——只有手敲 `/c/{id}/files/<path>` 能再看一次。
- **对照（DSH）**：DSH 的文件/产物面板有显式"目录选择 + 文件列表"，列表本身就是入口，不依赖 URL 里先有文件。
- **修法**：让 `guide` 只由「是否选中文件」决定，或给「文件」页签一个 `?view=files` 参数并据此渲染树；空选中时右侧显示"选择一个文件"。

### D-03 会话没有管理入口（重命名 / 删除 / 归档 / 固定 全缺） [P0 · M]
- **操作**：在 `/inbox`、`/c/:id`、`/` 对侧栏会话项右键、悬停、找行内按钮；再在设置里找归档。
- **网络**：无（没有任何可点的东西）。
- **DOM/截图**：右键只弹出导航菜单 `收件箱 能力 自动化 洞察 团队 设置`；会话行内 `hoverButtons: []`；正文里搜不到"重命名/删除会话/归档"；`r2-dead-session-contextmenu.png`
- **代码**：`web/src/shell/sidebar.tsx:53-95` 只有 `Conversations` 列表与 `onSelect`；`web/src` 全仓无 `session.delete/rename/pin/archive` 调用；`docs/TRANSPORT.md:545-549` 这四条全部标 "延后"；`api/gateway.ts:52` 有 `archived?` 字段但没人写。
- **对照（DSH）**：DSH 会话列表每个会话有 `⋯`：重命名 / 归档 / 删除（确认）/ 复制标题；归档页可从归档恢复。
- **修法**：先补 `session.rename/delete/archive/pin` 的 dispatch（TRANSPORT 状态列同步），再在 `Conversations` 上加行内 `⋯` 菜单；归档页做成"已归档列表 + 恢复 + 彻底删除"。

### D-04 自动化行内按钮的 antd 自动空格让"历史/编辑/删除"成为难点的同时暴露一致性 [P2 · 备注]
- **操作**：按文本 `历史` 定位并点击自动化行内按钮；连续 3 次 30s 超时。
- **DOM**：真正文本是 `历 史`、`编 辑`、`删 除`（antd `autoInsertSpaceInButton` 给两字标签插了空格），`立即运行`（四字）没有。
- **影响**：自动化测试/E2E 用文本定位会挂；屏幕阅读器读成"历 史"。属于可访问性/稳定性问题，非功能缺失（用 `data-row-key`+去掉空格后一切正常，见流程 6/7/8）。
- **修法**：全局关掉自动插空（`ConfigProvider button={{autoInsertSpace:false}}`）或给行内按钮加 `data-testid`。

### D-05 「安装并重启」一键关掉网关，浏览器端没有任何确认或预警 [P1 · U]
- **操作**：`设置→关于`（已存在 staged 0.4.0）→「安装并重启」。
- **网络/代码**：`WS update.install {restart:true}`（`update-status.tsx:142` 直接调用，无 Popconfirm）→ `gateway/update_surface.go:223-233` `RequestUpdateInstallNow` + `requestShutdown` → 进程退出、宿主替换二进制并重启 → 页面立刻进入"连接已断开"。
- **DOM**：同屏还并排着「下载更新」「安装并重启」两个按钮，文案「已下载的更新会在下次启动时自动安装；「安装并重启」会立即替换当前版本」，但没有任何"这会断开当前页面"的提示。
- **对照（DSH）**：破坏性动作走确认弹窗（`Modal.confirm`），并明确"应用将重启"。
- **修法**：加二次确认，说明会重启并短暂断连；浏览器端在重启后给"正在重启…"页面而不是通用离线提示。

### D-06 「下载更新」没有进度、没有取消，12s 内界面零变化 [P1 · S]
- **操作**：`设置→关于` → 下载更新（`WS update.download`）。
- **网络**：WS 发出后 12s 内无 result；同时**服务端确实在下载并落盘** `data-dead/updates/0.4.0-zeokeel-0.4.0-linux-amd64.AppImage`（25.3 MB）+ `pending.json`（下一轮探针看到 `.update-*.part` 中间文件）。
- **DOM**：按钮只短暂 loading；页面文本前后完全一致（无"下载中/百分比/取消"）。RPC 侧 15s 超时（`api/ws/rpc.ts:52`）后才会报错。
- **修法**：把 download 改成幂等后台任务 + `update.progress` 通知（或轮询 `update.status`），并给"取消"。

### D-07 通知页 4 个开关：存得下、看得见、不生效 [P1 · H]
- 4×`WS settings.set` 全部成功，刷新后仍为 true，"已保存"。
- `ui.notifications`（应用内通知）：**全仓无消费者**（`contracts/interaction.go:98` 只有常量与注释）。
- `ui.notifications.system`：只被 `host/notify_sink.go:41` 读取；即使开着，本机日志持续 `ZeoKeel: os notification: context deadline exceeded`——浏览器端看不到任何通知变化。
- `ui.autostart`：`settings.set` 成功、界面"已保存"，但日志 `autostart: create autostart dir "/root/.config/autostart": mkdir /root/.config/autostart: read-only file system` → **失败只进日志**（S）。
- `ui.closeToTray`：仅托盘壳。
- 修法：浏览器端要么隐藏这三项（标注"仅桌面版"），要么把失败回写到设置行状态；应用内通知要么接上 toast 门控，要么删行（"不做未请求的功能"的反面：写了开关却没接）。

### D-08 归档页唯一的开关是个空承诺 [P1 · H + M]
- 全页只有「在侧栏显示已归档会话」一个开关（`ia.ts:290-297`）；`WS settings.set ui.sessions.showArchived=true` 持久化成功。
- `grep ui.sessions.showArchived`：只有 `web/src/features/settings/ia.ts:297` 与 `internal/contracts/chat.go:547`，**没有任何读取方**；同时产品里没有任何"归档会话"的动作（D-03），所以这个开关永远不会改变任何东西。
- 截图：`r2-dead-archive-after-save.png`（保存后侧栏 3 条会话不变）。

### D-09 网络代理存下了，但要重启进程才生效，且不告诉用户 [P1 · H]
- `WS settings.set net.proxy / net.noProxy` 成功并持久化（`settings.json` 已确认）。
- 应用时机只有进程启动：`host/serve.go:50`、`host/host.go:333`；`proxy.go:85` 只在启动调用，没有任何 settings watcher（相比之下 autostart 有 `WatchAutostart` 热应用）。
- 修法：保存后热应用（重新 `applyProxySettings`）或在页面明示"重启后生效"。

### D-10 数据与隐私：非法 JSON 静默入库；导入/清除/隐藏全无入口 [P1 · S + M]
- `tools.disabled = not-json{` → `WS settings.set` 成功、"已保存"、无 alert、刷新后仍在（`r2-dead-privacy-invalid-json.png`）。后果是 toolkit 解析失败后按"空名单"处理（静默降级），用户以为自己禁用了工具。
- 同类：`子代理并发 -5` 也被接受（`已与网关快照一致`）。
- 页面只有 7 个 textarea；**没有**导入/导出配置、清除数据、隐藏文件等任何按钮（M）。
- 修法：JSON 字段失焦即校验（`JSON.parse` + schema），非法则红框阻止保存；数字字段加 min/max；"数据与隐私"做一个真正的数据管理区（导出/清除/按会话删除）。

### D-11 语言切了，设置侧边导航不跟着切 [P2 · H]
- 保存 `ui.language=en`：正文 `Appearance / Live snapshot of the gateway settings / Saved / Language…`，但左侧菜单仍是「常用 / 模型 / 行为与限额 / 界面与通知 / 外观 / 通知 / 连接 / 网络与搜索 / LSP / 数据 / 归档 / 数据与隐私 / 关于`.
- `settings-screen.tsx:116` `}, [pages, matches]);` —— `navItems` 的 `useMemo` 依赖里漏了 `t`，而 `t` 是语言相关的。
- 修法：依赖加 `t`（或 `locale`）。

### D-12 收件箱的待审批行：只给你"拒绝/允许一次"，不给上下文 [P1 · M + S]
- 两行同款：`待审批 {"url":"http://127.0.0.1:9884/health"} web_fetch 拒绝 允许一次`（裸 JSON 当标题）；`links:[]`、无「打开会话」。
- 线程内审批卡有「地址 / 影响（这会向外发出网络请求）/ 来自本会话 / 允许范围（仅此一次·本会话内）/ 打开该会话」，收件箱行全都没有；`api/inbox.ts:59` 明明把 `sessionId` 放进了 item。
- 修法：行内复用审批卡的摘要渲染（`approval-impact`/`approval-target.*` 文案已存在），并加「打开会话」链接。

### D-13 收件箱「查看」按钮对 ask / plan 是死按钮 [P1 · F]
- `inbox-screen.tsx:109` 的 `disabled` 只覆盖 `artifact && !sessionId`；`api/inbox.ts` 的 ask / plan 分支从不设置 `sessionId` → 按钮可点，`onClick` 里 `if (item.sessionId) navigate(...)` 为假 → 零请求、零变化。
- 本实例的 fake model 只有 `plain/tool/slow` 三种脚本，无法现场造 ask/plan/artifact 状态，故此项为静态证据（第一轮已现场复现过一次）。
- 修法：ask/plan 行要么给"去会话里回答"的真实目标（`ask` 带 `sessionId`），要么 disable + `title` 说明，像 artifact 那样。

### D-14 命令面板：缺「前往 团队」，且把内部 group id 泄到界面上 [P2 · M]
- `command-palette.tsx:44-83` 共 7 条：新建会话、前往 现在/收件箱/设置/能力/自动化/洞察 —— **没有团队**，而 `i18n/zh.ts` 里 `palette.goTeam`「前往 团队」已经写好了没人用。
- 每行第三个 span 直接渲染 `command.group`（`action` / `go`），界面出现中文标签 + 英文 `go` 混排（截图 `r2-dead-palette-open.png`）。
- 修法：补 `go-team`；分组标签走 `palette.group.*` 文案或不显示。

### D-15 「更多操作」菜单里只有一个动作；复制失败无提示 [P2 · 备注/S]
- 助手消息的 `⋯` 展开只有「重新生成」一项（`message-actions.tsx:101-117`）。
- 复制：有剪贴板权限时正常（tooltip「已复制」+ 剪贴板内容一致，已验证）；**无权限时** catch 静默 `setCopied(false)`，用户点击后毫无反馈（`:46-49`）。
- 对照（DSH）：消息操作是复制/引用/编辑/重试的平铺工具条，失败会 toast。
- 修法：把"重新生成"提升为图标按钮（去掉单条目菜单）；复制失败走 `message.error('无法访问剪贴板')` 或降级为"选中文本"。

### D-16 「改动」视图把 404 原样抛给用户 [P2 · S]
- `/c/{id}/files/README.md?view=diff` → 检查器里显示 `file diff failed: 404 Not Found`；`inspector.noDiff`「没有改动。」永远不出现。
- `inspector-panel.tsx:99-105` 把 `loadFileDiff` 的异常直接 `setError(message)`，而 404 是"该文件没有改动"的正常语义。
- 修法：404 → 渲染空状态文案；只有非 404 才当错误。

### D-17 dev 构建永远"有可用更新" [P2 · S]
- 关于页显示 `当前版本 dev` / `最新版本 0.4.0 有可用更新` / `已下载待安装 0.4.0`；`update.go:290-296` 把 dev 的比较值当 `0.0.0` → 任何 dev 构建都会提示更新，且能一路下载安装。

---

## 与 DSH 的差距（该功能应该长什么样）

| 面 | ZeoKeel 现状 | DSH 的做法 |
|---|---|---|
| 定时任务表达式 | 自由文本 + 帮助文案写错（`every 24h` / `daily 09:00` 均 400） | schedule 预设下拉（every N / daily HH:MM / cron），保存前本地用同一解析器预校验并内联报错 |
| 表单错误 | 页面级 `.zk-msg-error`，被 Modal 遮挡；服务端 `message` 被丢弃 | 字段级 `hint/error` + 弹窗内 Alert，展示服务端原因 |
| 会话管理 | 无任何入口（rename/delete/archive/pin 全"延后"） | 会话行 `⋯`：重命名/归档/删除（确认）；归档页可恢复 |
| 文件/产物面板 | 「文件」页签死路，只有手改 URL 才看得到树 | 面板内"目录 + 文件列表"直接可浏览、可切 diff |
| 破坏性操作 | 「安装并重启」一键执行、无确认 | `Modal.confirm` 明确"将重启"，执行后给"正在重启"过渡页 |
| 设置项真实性 | 多个开关有存储无消费者（通知/归档），失败只进日志 | 每个设置项有明确生效路径；不支持的项在 UI 里不出现或标注平台限制 |
| 剪贴板 | 失败静默 | 失败给 toast/降级提示 |
| 命令面板 | 7 条、缺团队、group id 外泄 | 命令按域分组、含全部路由，分组标签本地化 |
---

## 附：复现方式（脚本都在 `.cache/ui-audit/`，只读产品代码）

```bash
cd /data/zeokeel/.cache/ui-audit
export ZK_BASE=http://127.0.0.1:9874 ZK_TOKEN=y5nNqvs0SQrAjbdhoZmdj50414529fTt5l1r0D4ivDo
node r2recon.mjs    # 21 条路由的可交互元素清单 + 截图 → r2-recon.json
node r2flow1.mjs    # 收件箱决策、自动化新建（400×2 + 成功）、关于页更新
node r2flow2.mjs    # 自动化历史/编辑/删除、关于页（含 WS 原始帧）
node r2flow3.mjs    # 通知/归档/网络/LSP/数据与隐私/外观 保存与持久化
node r2flow4.mjs    # 命令面板、消息操作、会话右键、团队、洞察、能力、检查器
node r2flow5.mjs    # 线程四页签、重新生成、复制、发送+审批、脏数据守卫
node r2flow7.mjs    # 审批排空、Now、检查器 DOM、各页按钮清单、设置搜索
node r2flow8.mjs    # 检查器深链/页签死路/diff、Now composer、非法数值
node r2flow9.mjs    # 两条排队审批、团队关闭、排空
```

产物：`r2-recon.json`、`findings2/flows{1,2,3,4,5,7,8,9}.json`、`shots/r2-dead-*.png`（84 张）。
每个 flow 的 JSON 里每一步都带 `name / http[] / rpcOut[] / rpcIn[] / domChanged / before / after / elapsed`，
`http[]` 是 `状态码 方法 路径 :: 响应体前 240 字`，`rpcOut[]` 是本次交互发出的 WS 方法名。
