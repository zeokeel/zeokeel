# 基线文档 · 屏幕地图

> 基线：**DSH 0.2.1-rc.2**，参考窗口 1600×1000。截图/控件 dump 由 `scripts/cdp.mjs` 采集（支持滚动扫描与行为探测）。
> 施工规则：**一个界面没有截图 + 控件清单 + 行为说明，不进入编码**。

> 进度：基础屏 14 · 滚动位 12 · 控件样本 622（视口内） · 行为记录 9

## 基础屏

| 屏 | 说明 | 截图 | 控件数 | 文档 | 状态 |
| --- | --- | --- | --- | --- | --- |
| 首页 / 新会话 | 侧栏 + 居中 composer；默认入口 | [png](../../baseline/screenshots/00-home.png) | 18 | [md](screens/00-home.md) | 🟡 已截图/待确认 |
| More 菜单 | 侧栏底部 More 打开的菜单 | [png](../../baseline/screenshots/01-more-menu.png) | 21 | [md](screens/01-more-menu.md) | 🟡 已截图/待确认 |
| 插件页 | 侧栏 Plugins 入口 | [png](../../baseline/screenshots/02-plugins.png) | 35 | [md](screens/02-plugins.md) | 🟡 已截图/待确认 |
| 自动化任务页 | 侧栏 Automation tasks 入口 | [png](../../baseline/screenshots/03-automation.png) | 17 | [md](screens/03-automation.md) | 🟡 已截图/待确认 |
| 会话页 · Chat | 打开一个已有会话 | [png](../../baseline/screenshots/04-session.png) | 250 | [md](screens/04-session.md) | 🟡 已截图/待确认 |
| 会话页 · Trajectory | 会话头 Trajectory Tab | [png](../../baseline/screenshots/05-trajectory.png) | 82 | [md](screens/05-trajectory.md) | 🟡 已截图/待确认 |
| 设置 · General | More → Settings | [png](../../baseline/screenshots/06-settings.png) | 39 | [md](screens/06-settings.md) | 🟡 已截图/待确认 |
| Composer · 权限弹层 | 点击 Full access | [png](../../baseline/screenshots/07-permission.png) | 22 | [md](screens/07-permission.md) | 🟡 已截图/待确认 |
| Composer · 模型弹层 | 点击模型选择器 | [png](../../baseline/screenshots/08-model.png) | 20 | [md](screens/08-model.md) | 🟡 已截图/待确认 |
| Composer · 模式弹层 | 点击 Standard mode | [png](../../baseline/screenshots/09-mode.png) | 22 | [md](screens/09-mode.md) | 🟡 已截图/待确认 |
| Composer · 工作区选择 | 点击工作区 chip | [png](../../baseline/screenshots/10-workspace-picker.png) | 23 | [md](screens/10-workspace-picker.md) | 🟡 已截图/待确认 |
| Composer · 添加文件/命令菜单 | 点击 + 按钮 | [png](../../baseline/screenshots/11-attach.png) | 26 | [md](screens/11-attach.md) | 🟡 已截图/待确认 |
| 反馈弹窗（采集误触，待重采） | 输入 / 时误触 Feedback；命令面板待重采 | [png](../../baseline/screenshots/12-commands.png) | 29 | [md](screens/12-commands.md) | 🟡 已截图/待确认 |
| 新会话 · 空态 | 点击侧栏 New Session | [png](../../baseline/screenshots/14-new-session.png) | 18 | [md](screens/14-new-session.md) | 🟡 已截图/待确认 |

## 滚动扫描（长列表/长会话，确保元素全部发现）

| 位置 | 控件数 | scrollTop / max | 截图 |
| --- | --- | --- | --- |
| 02-plugins-s0 | 35 | 138/138 | [png](../../baseline/screenshots/02-plugins-s0.png) |
| 02-plugins-s1 | 37 | None/None | [png](../../baseline/screenshots/02-plugins-s1.png) |
| 02-plugins-s2 | 37 | None/None | [png](../../baseline/screenshots/02-plugins-s2.png) |
| 04-session-s0 | 110 | 785/1929 | [png](../../baseline/screenshots/04-session-s0.png) |
| 04-session-s1 | 195 | 1570/1929 | [png](../../baseline/screenshots/04-session-s1.png) |
| 04-session-s2 | 233 | 1929/1929 | [png](../../baseline/screenshots/04-session-s2.png) |
| 04-session-s3 | 267 | None/None | [png](../../baseline/screenshots/04-session-s3.png) |
| 04-session | 250 | None/None | [png](../../baseline/screenshots/04-session.png) |
| 06-settings-s0 | 285 | 255/255 | [png](../../baseline/screenshots/06-settings-s0.png) |
| 06-settings-s1 | 286 | None/None | [png](../../baseline/screenshots/06-settings-s1.png) |
| 06-settings | 39 | None/None | [png](../../baseline/screenshots/06-settings.png) |
| 14-new-session | 18 | None/None | [png](../../baseline/screenshots/14-new-session.png) |

## 行为探测（首页控件 → 点击前后对比）

| 控件 | URL 变化 | 弹窗 | 页面变化 | 结论 |
| --- | --- | --- | --- | --- |
| New session |  | 0→0 | 否 | 无可见变化（需进一步确认） |
| Collapse sidebar |  | 0→0 | 是 | 展开/折叠或切换视图 |
| Plugins |  | 0→0 | 是 | 展开/折叠或切换视图 |
| Automation tasks |  | 0→0 | 是 | 展开/折叠或切换视图 |
| Search sessions |  | 0→0 | 是 | 展开/折叠或切换视图 |
| View options |  | 0→0 | 是 | 展开/折叠或切换视图 |
| Add workspace |  | 0→1 | 是 | 打开弹层/菜单 |
| More |  | 0→0 | 是 | 展开/折叠或切换视图 |
| mygo |  | 0→0 | 是 | 展开/折叠或切换视图 |

> 说明：破坏性控件与用户配置开关（如插件开关、Sign out、Feedback 提交）本轮未点击；确认后再补测，避免改动你的实例配置。

## 尚未采集

- 设置子页：Models / Built-in plugins / Agent presets / Keyboard shortcuts / About
- 插件页：全部 Experimental 行 + More + Add plugin 移除后的对照（品牌替换后重采）
- 会话页：streaming、工具卡片展开、审批卡片、提问卡片、错误态、右侧边栏（Files/Terminal/Browser）
- 自动化任务：新建/编辑弹层、空态
- 命令面板（/）、@ 引用、附件上传、拖拽
- 深色主题全屏、窄屏（1200×800 / 900×600）
- 品牌替换对照（ZeoKeel logo + 字标 A/B）

## 隔离实例采集（第二轮，2026-11-04）

> 探测实例：`DSH_HOME=/data/mygo/.probe/home`，`127.0.0.1:8099`，已复制 workspace 与会话投影缓存；**不影响你正在用的实例**。

| 屏 / 状态 | 截图 | 控件 dump |
| --- | --- | --- |
| 设置 · General（含滚动） | [png](../../baseline/screenshots/55-settings-general.png) | [json](../../baseline/controls/55-settings-general.json) |
| 设置 · Models | [png](../../baseline/screenshots/56-settings-models.png) | [json](../../baseline/controls/56-settings-models.json) |
| 设置 · Built-in plugins | [png](../../baseline/screenshots/57-settings-builtin-plugins.png) | [json](../../baseline/controls/57-settings-builtin-plugins.json) |
| 设置 · Agent presets | [png](../../baseline/screenshots/58-settings-agent-presets.png) | [json](../../baseline/controls/58-settings-agent-presets.json) |
| 插件页（Built-in + Experimental） | [png](../../baseline/screenshots/59-plugins-page.png) | [json](../../baseline/controls/59-plugins-page.json) |
| 自动化任务 | [png](../../baseline/screenshots/60-automation.png) | [json](../../baseline/controls/60-automation.json) |
| 会话 · Chat（含滚动） | [png](../../baseline/screenshots/61-session-chat.png) | [json](../../baseline/controls/61-session-chat.json) |
| 会话 · Trajectory（含滚动） | [png](../../baseline/screenshots/62-session-trajectory.png) | [json](../../baseline/controls/62-session-trajectory.json) |
| 工具卡片展开 | [png](../../baseline/screenshots/63-toolcard-open.png) | — |
| 会话 · More actions 菜单 | [png](../../baseline/screenshots/64-session-more-menu.png) | [json](../../baseline/controls/64-session-more-menu.json) |
| 右侧边栏（Workspace files / New terminal） | [png](../../baseline/screenshots/65-right-sidebar.png) | [json](../../baseline/controls/65-right-sidebar.json) |
| 会话 · 权限弹层 | [png](../../baseline/screenshots/66-session-permission.png) | — |
| 会话 · 模型弹层 | [png](../../baseline/screenshots/67-session-model.png) | — |
| 会话 · 命令面板（/） | [png](../../baseline/screenshots/68-session-commands.png) | [json](../../baseline/controls/68-session-commands.json) |
| 深色会话 | [png](../../baseline/screenshots/69-session-dark.png) | [json](../../baseline/controls/69-session-dark.json) |
| 深色 · 窄屏 1200×800 | [png](../../baseline/screenshots/70-session-dark-narrow.png) | — |
| 发送中（streaming/等待） | [png](../../baseline/screenshots/71-streaming.png) | [json](../../baseline/controls/71-streaming.json) |
| 回合失败态（TRANSPORT） | [png](../../baseline/screenshots/72-turn-failed.png) | [json](../../baseline/controls/72-turn-failed.json) |

### 采集经验（写进脚本，避免重复踩坑）

- **Preview Notice 弹窗**：每次冷启动会挡住界面；坐标点 `Continue`（1010,684）后所有操作才生效。
- **打开会话**：点侧栏会话**标题文字**（约 x=85），点行尾空白只会出 tooltip，不会进入会话。
- **会话定位**：Chat `(323,62)`、Trajectory `(406,62)`、More actions `(1538,25)`、右侧栏 `(1574,25)`、发送 `(1350,945)`。
- **滚动截图**：`scrollscan` 会优先在弹窗内找滚动容器；Chat 与 Trajectory 均已滚到底。
- **模型网络**：隔离实例发送消息返回 `TRANSPORT`（DeepSeek Messages transport failed），已记录**失败态**；成功 streaming/工具/审批态需要在探测实例配置可达的模型网关后补采。

### 行为探测进度

- `baseline/behavior/iso-home.jsonl` / `iso-plugins.jsonl` / `iso-automation.jsonl` / `iso-settings.jsonl` 已产出（菜单展开、弹窗、导航、侧栏折叠等）。
- **已知缺陷**：SPA 下 `probescan` 不会在每次探测前重置界面，连续点击会串屏；下一轮改成“每个控件前重新导航/恢复 + 截图前后对比”，才能做到控件级 100% 行为确认。

## 第三轮：弹层 / 菜单 / 全元素 CSS（2026-11-04）

| 屏 / 状态 | 截图 | CSS |
| --- | --- | --- |
| 快捷键 / 设置弹窗 | [png](../../baseline/screenshots/73-shortcuts.png) | [css](../../baseline/css/17-shortcuts.json) |
| 插件配置 · Subagent | [png](../../baseline/screenshots/74-plugin-subagent.png) | [css](../../baseline/css/18-plugin-subagent.json) |
| 自动化任务 · 新建（待重采） | [png](../../baseline/screenshots/75-automation-new.png) | [css](../../baseline/css/19-automation-new.json) |
| `@` 引用 / 文件选择 | [png](../../baseline/screenshots/76-mention.png) | [css](../../baseline/css/20-mention.json) |
| `+` 附件 / 命令菜单 | [png](../../baseline/screenshots/77-attach.png) | [css](../../baseline/css/21-attach.json) |
| 搜索会话 | [png](../../baseline/screenshots/78-search-sessions.png) | [css](../../baseline/css/22-search-sessions.json) |
| 视图选项（WorkSpace / Workspace Tree / 排序 / 归档） | [png](../../baseline/screenshots/79-view-options.png) | [css](../../baseline/css/23-view-options.json) |
| 添加工作区（目录选择器） | [png](../../baseline/screenshots/80-add-workspace.png) | [css](../../baseline/css/24-add-workspace.json) |

- 全元素 CSS 已覆盖 **24 个屏/状态**（`baseline/css/*.json`，43 个 computed 属性/元素），汇总 [docs/baseline/css.md](css.md)。
- 逐控件行为探测升级为 **probescan3**：每个控件前重新导航/关闭弹窗（干净状态），点击前后各截图 + 记录 DOM/URL/弹窗变化；正在跑 home / plugins，之后继续 settings / session。
- 仍未完成：Shortcuts 快捷键列表（需确认入口）、自动化 New、插件其它配置行、右栏 Terminal/Files Tab、成功 streaming（模型网络阻塞）。

## 第四轮：逐控件全覆盖探测（重置式）

- **首页**：`probescan3` 覆盖全部可用控件（每个控件前重新进入首页），每控件前后各 1 张截图 + DOM/URL/弹窗差异。
- **插件页**：覆盖全部 24 个可交互控件（Built-in 4 项、Experimental 全部开关、刷新、Add plugin 下拉、About plugins 等），到 `probescan3: no unprobed controls left`。
- 行为日志：`baseline/behavior/probe3-home.jsonl`、`probe3-plugins.jsonl`；前后截图在 `baseline/behavior/shots3/`。
- 关键发现：`+ Add plugin` 实际是下拉菜单（含 "Choose how to add a plugin"），ZeoKeel 将按决策**移除**该入口；Experimental 开关可独立切换并有对应 aria-label（`Enable Agent Teams` 等）。

## 第五轮：成功流式 + 控件级全覆盖

**主实例成功对话（新建测试会话，未改动既有会话）**
- 新建会话 → 发送「请只回复 OK 两个字」→ 成功返回 `OK`（Completed in 1s，Usage 12.1K tok）
- 截图：`90-main-new-session`、`91-main-streaming`、`92-main-final`、`93-main-trajectory`
- CSS：`baseline/css/32-main-streaming.json`、`33-main-final.json`、`34-main-trajectory.json`
- 轨迹页包含 Duration/Turns/Calls/Input/Model/Tools/SYSTEM 等完整字段

**重置式逐控件探测（probescan3）已覆盖**

| 屏 | 覆盖控件数 | 行为日志 | 前后截图 |
| --- | --- | --- | --- |
| 首页 | 23（全部） | `probe3-home.jsonl` | `shots3/home-*` |
| 插件页 | 24（全部，跑到 no unprobed controls） | `probe3-plugins.jsonl` | `shots3/plugins-*` |
| 设置 | 35 | `probe3-settings.jsonl` | `shots3/settings-*` |
| 会话 | 26（跑到 no unprobed controls） | `probe3-session.jsonl` | `shots3/session-*` |

合计 **108 个控件、216 张前后对比截图**；覆盖菜单、弹窗、开关、导航、侧栏折叠、右栏、分屏、全屏等。

**补充弹层**
- 快捷键弹窗（含 Ctrl 列表）：`81-shortcuts-dialog` + CSS 25
- 插件配置：Web search / Agent loop / Shell：`83-85` + CSS 27-29
- 自动化任务弹层：`94-automation-dialog`（待确认是否成功打开）
- 右栏：`95-right-sidebar-home`、`96-right-files`、`97-right-terminal`（Files/Terminal Tab）

## 收口（100% 覆盖）

| 维度 | 结果 |
| --- | --- |
| 截图 | 80 张（`baseline/screenshots/`） |
| 全元素 CSS | 53 份（`baseline/css/`，43 个属性/元素） |
| 逐控件行为 | 108 条（`probe3-*.jsonl`） |
| 前后对比截图 | 216 张（`behavior/shots3/`） |

**最后补齐的两项**
- 右栏 Terminal：`110-right-terminal`（bash / root@dsh:/data…，CSS 53）——点击右栏卡片中心 (1241,507) 打开
- 自动化任务创建：实际行为是**回到新会话由 Agent 创建任务**（自动化页文案 “Tasks created in your sessions…”），已按此记录（`106-automation-dialog` 即点击后的新会话态），不是弹窗

**100% 判定**
- 首页 / 插件页 / 设置 / 会话：控件级 100%（重置式探测，无剩余控件）
- 设置四子页、插件四类配置、快捷键、搜索、视图选项、添加工作区、右栏三态（Start/Files/Terminal）、深色、窄屏：均有截图 + CSS
- 唯一例外（非 UI 可构造）：审批卡片、提问卡片、语音输入弹窗由 Agent 运行态触发；入口与样式已覆盖，触发态需接入可用模型网关后补（不影响界面实现）
