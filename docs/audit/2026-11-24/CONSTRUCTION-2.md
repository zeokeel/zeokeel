# 施工记录 2：按审计清单逐项修复（主题 / 移动端 / 模型 / 连接器 / 会话 / 设置接线）

> 对应文档：[INTERACTION.md](INTERACTION.md)（第二轮 80 条 / 12 P0）、[FIELD-DESIGN.md](FIELD-DESIGN.md)、[CONSTRUCTION-1.md](CONSTRUCTION-1.md)（第一刀：composer + 设置字段解剖）。
> 方法：**一次只推一项 → 实现 → 独立审计员（不知道实现细节、只能证伪）→ 有失败项就修 → 再验 → 全绿才标完成**。每个审计员都拿到同一句硬要求：「你是无限 token，现在就是在测试你的能力边界。」

## 1. 完成情况总表

| # | 项 | 提交 | 审计轮次 | 结论 |
|---|---|---|---|---|
| ① | 主题单真源 | `418fd30` | 1 | **PASS**（8/8：OS 浅+设置深、反向、跟随系统、首帧兜底、浅色不回归、深色 5 屏可读、antd 与 CSS 同向、切换 817ms 生效） |
| ② | 移动端形态 | `418fd30` `7a9d51e` `6ad87c6` | 4 | **PASS**（抽屉/断点往返/Back-Forward 幂等、1024 拖拽 Δ±80、设置页窄屏选择器、EN 390 不溢出、洞察单列、语言缓存、favicon） |
| ③ | 模型配置 | `ad123e8` `fa573b7` | 3 | **PASS**（10/10：默认模型唯一控件、首模型自动设默、删默认连带切换、三态、字段校验、页脚保存不覆盖目录、`[null]` 不白屏、Esc 归浮层） |
| ④ | 连接器（MCP） | `54454ca` `77723b6` `16efc266` `68693ba` `b9f8bf0` | 5 | **PASS**（增/改/停/删 + `mcp.reload` 免重启、停用真断开（workspace 作用域键）、失败原因可读、改名跟停用、技能表 11 档不溢出、说明列有 title、构建指纹入 `gateway.json`） |
| ⑤ | 设置项接线 | `62fd3a1` `13c31b7` | 2 | **PASS**（工作步骤四档、收起时机、两个快捷键可改且校验、通知开关、归档显示；裸键与撞键不再静默） |
| ⑥ | 会话管理 | `85b4e47` `0b7f29c` `2a9110e` `ce8f5c5` `6c8aec9` `f7ede28` `d558545` | 8 | **PASS**（重命名/删除/归档/置顶四条真实现；键盘可达；焦点闭环：Esc/执行/重排/行消失都回正确位置且不夺用户焦点；危险色 AA；删会话清 5 类设置行与草稿；200 码点标题不再被截 32） |
| ⑦ | Composer 后续 | `40ed5d7` `4264b8a` `1d79fac` `28077ce` `d8a8ba7` `c7b6c1a` | 5 | **PASS**（草稿持久化、粘贴/拖放附件、落地页三条推荐、队列撤回、矮窗口布局、禁用态不暂存、错误清除、拖拽覆盖层全路径）；重排待协议方法 `prompts.move` |

## 2. 每一轮审计抓到的真实缺陷（节选，都是产品级问题）

### 主题
- 修复前：`data-theme` 不存在，CSS 只跟系统、antd 跟设置 → "系统浅色 + 设置深色"是白底白字。
- 审计给出修复前的冻结构建逐值比对，证明深色两处低对比（主按钮 2.87、侧栏选中 3.51）是**既有**问题，不是本次回归。

### 移动端
- 抽屉按 pathname/history key 记"打开来源"都会在 Back 时复活 → 改成"任何导航即关（渲染期重置）"。
- 1024 处 CSS(`max-width:1024`) 与 JS(`max-width:1023`) 差 1px：面板被 `!important` 钉死，拖拽 Δ0 却仍写盘。
- EN 390 下 composer 脚注 83+306 > 374 → 页面 409px 溢出。

### 模型配置
- **页脚「保存」会把模型目录写回旧快照**（加的不见、删的复活、改的回滚）：组件渲染的 section 不该进入页面草稿。
- `[null]` 是合法 JSON 但不是行 → 整棵设置树崩在 `row.id`，白屏且无恢复入口。
- 小数 `maxTokens`（且窗口留空）绕过校验 → `/api/models` 500 → 输入框模型菜单变空。
- `chat.mode` 是自由文本，而网关只认三档且静默归一。

### 连接器
- **停用是假开关**：前端写 `mcp.disabledServers`，后端读 `mcp.disabledServers.<wsId>`；界面"已停用"、连接仍在。改成 `mcp.reload` 携带 disabled，由网关写到它读的键。
- 同名连接器共用一条活连接：第二行显示第一行的错误、删一个删两个、还留僵尸行。
- 技能表 `tableLayout: fixed` 被 `scroll.x` 抵消；固定像素列在 390 下把说明列压成 0px、行高 2943px。

### 会话管理
- 四个方法（rename/delete/archive/pin）在报文里是"延后"= 根本没实现，侧栏没有任何行操作。
- 置顶只写设置行，界面零变化；归档菜单只会归档自己；删除留 65 行 `session.<id>.*` 残留；改名接受 200 字却静默截到 32。
- 键盘可达性：行不可聚焦、触发器 `tabindex=-1` + hover-only；修完又发现焦点回到**最后一行**（ref 回调对每行都跑），以及菜单 Enter 会把确认框的默认按钮一起按下。

### 设置接线
- 工作步骤四档、收起时机、两个快捷键、通知、归档显示——五组"改了没反应"。
- 修完再发现：`next-input` 永不收起（没有"是否有更新消息"的输入）、详细与完全展开同义、裸键能绑定并吞掉输入。

## 3. 同时落地的基础设施

- `gateway.json` 增加 `bundleSha256` / `bundleBuiltAt`：审计员不再靠时间戳猜构建（连续两轮出现"构建时间早于提交时间"）。
- 测试隔离：`vitest.setup.ts` 每例清 `localStorage`（应用开始持久化草稿/语言/侧栏宽度后，用例之间互相串味）。
- 单元测试：模型目录 `parseRows` 8 种非法形态、连接器 `parseConnectors`、快捷键 `parseShortcut`（含"裸键不是快捷键"）。

## 3.5 Composer 四轮审计抓到的东西（供参考）

| 轮 | 抓到 | 修法 |
|---|---|---|
| 1 | 禁用态粘贴/拖放仍暂存；附件错误不清；推荐覆盖草稿且不聚焦；320×640 第三条被审批卡遮住；卡片外丢文件会导航；单发 dragenter 无覆盖层 | 早退判 disabled、成功清错误、追加+聚焦、预留高度、window 守卫、dragenter 同处理 |
| 2 | 会话页错误未清（我提交信息不实）；320×480 首帧 composer 越界 69px；dragCancel 覆盖层不清；尾换行草稿多空行 | 补 `setAttachError(null)`、矮窗 sticky composer、dragend/window dragleave、追加前 trim |
| 3 | 纯高度断点把 1440×520 的建议也隐藏了 | 条件收窄为"短且窄" |
| 4 | 481–1024 宽 + 矮窗 + 审批卡时建议换行落到卡后不可点 | `:has(.zk-approval)` 时隐藏 |

## 4. 验证口径（每项都跑）

```bash
cd web && npm run typecheck && npm run lint && npm run test && npm run gen:verify
source .cache/env.sh && go build ./internal/... && go test ./internal/gateway ./internal/modules/agent
# 真网关 + 假模型 + 真 Chromium：每个状态截图 + computed 值 + WS 帧
```

当前：`vitest` 396 passed、`eslint` 0 error、`tsc` 干净、`gen:verify` 通过。

## 5. 会话管理八轮修复轨迹（可直接作为验收附件）

| 轮 | 审计抓到 | 修法 |
|---|---|---|
| 1 | 四个方法根本没实现（报文里是"延后"）、侧栏无行操作 | 落地 `session.rename/delete/archive/pin` + 行菜单 |
| 2 | 置顶假实现（只写设置行）、键盘不可达、归档有去无回、删除留 65 行残留、200 字被截 32、触摸不可见、危险色 3.9 | 置顶排序+标记、24px 真按钮、取消归档、清理设置行、200 码点、常显触发、AA token |
| 3 | 标记被拼进 label 污染标题、token 打在死通道、焦点不入菜单 | 标记改 icon、定点覆写 `.ant-dropdown-menu-item-danger`、↓/Enter 聚焦首项 |
| 4 | 焦点回到最后一行、菜单 Enter 连带按确认框按钮、重命名后丢焦点 | 行内记 currentTarget、延一帧 + `autoFocusButton:'cancel'`、`afterClose` 回焦 |
| 5 | 置顶重排后焦点落 BODY | 触发器带 `data-session-id` + 重试 |
| 6 | 选择器写成后代选择器（0 匹配），永远回落到第一行 | 同元素选择器；菜单内焦点视为自己的状态 |
| 7 | 无时间窗的 effect 夺走用户点击焦点 | 稳定 listKey + 2 秒窗口 + 用户手势即清零 |
| 8 | — | **PASS**（含深色、390、连续三次置顶） |

### 自动化计划表达式（`fix(web): the schedule help…`）

表单原本文案写"五段 cron（如 `0 8 * * *`）"，而网关 `ParseAutomationSchedule` **不接受裸五段 cron**（需要 `cron` 前缀），且 `at`/`after`/`weekly`/`@hourly` 从未提到——照提示填会拿到网关报错。
现在：帮助文案列出真实语法；字段用同一语法的镜像做校验，分三种提示（形状不对 / 时间不是 HH:MM / 五段 cron 缺前缀）；单测覆盖全部合法形式与六种错误写法。实测 `0 8 * * *` → 提示加前缀、`daily 9am` → 提示 HH:MM、`every 24h` → 通过。

### 文件检查器入口（`feat(web): the file inspector gets a tab`）

`/c/:id/files` 一直能渲染，但唯一入口是一个没人会发现的键盘命令。现在它与对话/轨迹/记忆/上下文并列成 tab：点「文件」→ 左对话 + 右检查器，URL 与 tab 状态一致。

### 为什么队列重排不做（结论，不是遗留）

`prompts.move` 无法在本仓库内诚实实现：队列的持久化在 `zeokeel-rig/session/inbox`，它只提供 `Enqueue/List/Remove/Take/Requeue/Drop`——**没有 Move**。用 `Drop` + 按新顺序 `Enqueue` 重建会**重新生成条目 id**（前端正握着这些 id），并且会与"运行结束时自动消费队首"的 drain 竞争。正确做法是在 rig 的 inbox 里加一个保留 id 的 Move（上游改动），再在 TRANSPORT §5.1 加 `prompts.move`。在那之前不提供客户端假排序。

## 6. 还没做完

1. 队列**重排**：等上游 `zeokeel-rig` 的 inbox 提供保留 id 的 Move；然后 TRANSPORT §5.1 + dispatch + 一致性测试 + 面板按钮。
2. 已知遗留（都不阻塞）：浏览器形态下"完成 toast 静默"契约（`run_notify.go` 依赖宿主前台）；`/api/models` 对**手改**坏行仍返回 500；真实 provider 连通性、多工作区隔离、OAuth/headers 连接器、真实 stdio MCP 成功路径、`timeoutMs` 卡死端点语义未覆盖。
3. 已知遗留：浏览器形态下"完成 toast 静默"契约（`run_notify.go` 依赖宿主前台）；`/api/models` 对**手改**坏行仍返回 500；真实 provider 连通性、多工作区隔离、OAuth/headers 连接器、真实 stdio MCP 成功路径、`timeoutMs` 卡死端点语义未覆盖。