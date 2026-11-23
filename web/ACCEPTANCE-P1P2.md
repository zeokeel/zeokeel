# P1 / P2 验收对账（docs/WEB-CLIENT.md §8.2 / §8.3）

> 2026-11-23。范围：**B1–B16（P1，16 条）+ C1–C6（P2，6 条）= 22 条**。
> P0 24 条见 [ACCEPTANCE-S3.md](ACCEPTANCE-S3.md)（全绿）。§8.4 的 M2 八类 surface 不在本轮。
>
> 状态口径：**完成** = 行为与桌面端等价且有回归；**部分** = 主要路径可用、有明确缺一半；
> **未做** = 没有实现，必须写明原因（后端无域 / 无本地化服务 / 会自造协议）。

| # | 条目 | 状态 | 证据 / 缺口 |
|---|---|---|---|
| B1 | 设置脏守卫（Discard 恢复原值） | 完成 | `settings-screen.tsx`：`ConfirmDanger` + `setValues(baseline)`；`esc-chain.test.tsx` E5、`acceptance-p1p2.test.tsx`「shows the guard instead of closing once a field changed」 |
| B2 | 设置内 Esc（脏则先守卫） | 完成 | 本轮补：`useEscClaim` 常驻，干净时 `navigate('/')`、脏时开守卫；`acceptance-p1p2.test.tsx`「closes the page when nothing was edited」 |
| B3 | 设置导航搜索定位到项 | 完成 | 本轮补：搜索同时匹配**页名与字段名**，命中字段→`/settings/<page>?focus=<row key>`，表单 `data-focused` 高亮并滚动；`acceptance-p1p2.test.tsx`「finds a field by its label…」 |
| B4 | 固定页脚唯一 primary、滚动不动 | 完成 | `.zk-settings__footer`（`border-top` + 页面内 flex 底部），每页仅「保存」一个 primary；滚动容器是 `.zk-settings__body` |
| B5 | 暗色/英文全站跟随、无硬编码 | 部分 | 暗色：`useSystemAppearance()` + `tokens.css` 暗色块，颜色字面量由 ESLint 拦。英文：自建 `src/i18n/{zh,en}.ts` + `t()`（无第三方依赖），语言取 `settings` 域的 `ui.language`（`SETTING_KEYS.uiLanguage`），缺失时按 `navigator.language`；delta 到达即换语言不刷新；`ConfigProvider locale` 跟随 `zh_CN`/`en_US`。扫描测试禁止 i18n 目录外出现 CJK（`i18n.test.tsx`，含"故意注入中文 → 红"的证明）。浏览器实测：`i18n-zh.png` / `i18n-en.png`（侧栏、顶带、composer hint、审批卡全套切换）。**缺（故记部分，而不是完成）**：验收原文是"**全站跟随**"，而自动化/能力/洞察/检查器/设置页字段标签/记忆/子会话等 10 个文件仍在转换中，英文模式下这些屏仍是中文；清单锁在 `i18n.test.tsx` 的 `PENDING_CONVERSION` 里（只减不增，转完必须删条目） |
| B6 | 会话 Tab 三页可切、有 KeyContext 才显示「上下文」 | 部分 | 对话/轨迹/记忆三页可切（`routes.ts` `THREAD_TABS`、`top-band.tsx`）。「上下文」按 `WEB-CLIENT` R6/B6 由能力门控，网关无 `context.*`、无 `KeyContext` → **按能力隐藏，属已知等价性缺口**；不发明协议 |
| B7 | 窄窗 1024/900 顶栏与发送键可见可点 | 完成 | 本轮补：`global.css` 两个断点——≤1024 侧栏 232px、检查器 320px；≤900 侧栏 200px、检查器改浮层、顶带标题省略号。**headless chromium 实测**（1024/900/460 三档，`overflowX = 0`）：900 视口下侧栏 200px、标题可见、发送键 `x 831→863` 在视口内；460 视口发送键 `x 343→375`、composer 196px 不裁切 |
| B8 | composer <480 不重叠不裁切 | 完成 | 本轮补：≤480 断点收窄 composer 内边距、hint 换行、审批按钮换行。实测 460 视口：composer `x 200 w 196`、发送键 `x 343→375` 完全在内，`overflowX = 0` |
| B9 | 工具行点击/键盘展开收起 + 运行 spinner + 完成态 | 完成 | `thought-steps.tsx`：`ThoughtChain` + `onExpand`（点/键盘均可），`status: loading/success/error`，行文案 运行中/完成/失败；运行/失败项默认展开 |
| B10 | 工具失败行 + 可展开 stderr | 完成 | 本轮补：`tool-card.tsx` 渲染 `step.error`（`role="alert"`），与卡片同级、展开可见；状态行显示「失败」 |
| B11 | 消息 `⋯` 菜单（复制/编辑/重生成） | 未做 | 无消息操作菜单组件；且「编辑/重生成」需要网关方法（`message.regenerate` 未实现，TRANSPORT §5.1 无 `message.edit`）。只做「复制」会给出不完整菜单，按「不做未请求的半截功能」暂缺 |
| B12 | 后台任务 chip → 浮层 → 停止 | 未做 | **后端无域**：`internal/gateway` 没有 jobs/tasks 域或方法（`grep 'jobs\|task\.'` 无命中）。用 mock 数据假装会违反"不发明协议" |
| B13 | 队列弹层可查看并取消 | 完成 | 已改成 antd `Popover`：收起态只有一个「N 条待发送」chip（`thread.queue.count`），点开是行列表 + 单条取消 + 空闲时「发送队首」；数据仍走 WS `prompts:*`。测试：`queue-and-breadcrumb.test.tsx`「shows the count on the chip and reveals the rows only when opened」「keeps the send-head affordance inside the popover when idle」，`prompt-queue.test.tsx` 三条回归（排队/取消/中止保留）全绿 |
| B14 | toast（带撤销）4s 消失、错误粘滞 | 完成 | 已接：`features/toasts/toasts.tsx` 订阅 WS `toast.posted {id, level, text, sessionId?, actionLabel?, actionId?}`（§6.3 形状），antd `notification` 渲染；带 action 时按钮回 `notify.action {id, actionId}`；回执 `duration: 4`、`level=error` `duration: 0` 粘滞；同 id 不堆叠。测试：`toasts.test.tsx`（渲染/回执 action/去重/error 熬过 4s 仍在）；浏览器实测（`.cache/ui-review/toast.png`）：`这一轮已完成 + 查看会话` 出现在右上 `x 872 y 24`，5s 后回执计数 0（错误级别不消失由单测覆盖） |
| B15 | 危险确认默认焦点在取消、Enter 不执行 | 完成 | `ui/confirm-danger.tsx`：取消按钮 `autoFocus`，危险动作为 danger 且非默认；`esc-chain` E5/E7 与 `ConfirmDanger` 单测覆盖 |
| B16 | `Mod+B` 折叠侧栏、展开后状态不丢 | 完成 | 本轮补：`app-shell.tsx` `useShortcut(mod+'b')` + `COLLAPSED_WIDTH`，折叠为图标栏（导航 `inlineCollapsed`、会话变圆点且当前项高亮），宽度存 `expandedWidth`；`acceptance-p1p2.test.tsx`「collapses to a rail and restores the active session」 |
| C1 | 双击「新对话」只建一个（600ms 防抖） | 完成 | 本轮补：`sessions-context.tsx` `createGuard`（600ms 内复用同一 promise）；`acceptance-p1p2.test.tsx`「treats two clicks inside 600ms as one action」 |
| C2 | 当前行 tint 比 hover 更浓 | 完成 | `tokens.css`：亮色 hover `rgba(15,17,21,.045)` vs selected `rgba(13,68,246,.09)`；暗色 `rgba(255,255,255,.06)` vs `rgba(55,231,254,.13)` — 选择态更浓且带色相 |
| C3 | 命令面板能搜到每条命令 | 完成 | `command-palette.tsx` 按标题过滤；`acceptance-p1p2.test.tsx`「reaches each command by typing its title」逐条验证（新建会话 / 前往 收件箱 / 前往 设置） |
| C4 | 文件树 ↑↓ 改当前项并出预览 | 完成 | `inspector-panel.tsx` 用 antd `Tree`（内置 ↑↓/Home/End 键盘导航）+ `onSelect` → `/c/:id/files/<path>` → 预览/`diff` 分段；`files.test.ts` 覆盖树构建与路径 |
| C5 | 面包屑「工作区根目录」回树、不关页签 | 完成 | 已在预览头加面包屑：`工作区根目录 / <path>`，根可点击 → `/c/:id/files`，面板不卸载（不关页签），替换了原来重复的「返回树」按钮。测试：`queue-and-breadcrumb.test.tsx`「navigates to the tree and keeps the inspector mounted」；浏览器实测 `{crumb:"Workspace root", path:"src/chat/composer.tsx"} → click → url=/c/s-7071d368/files, inspectorAlive=true, pathGone=true`（`.cache/ui-review/breadcrumb.png`）。**顺带修掉一个真 bug**：文件路由注册成 `*path`，react-router 只认 `*` 作 splat，导致**所有文件深链都落到 NotFound**、检查器根本进不去；改成 `*` 并加回归测试「renders the inspector for /c/:id/files/<path>」 |
| C6 | 团队/子代理入口零值常显、不显示 0 | 完成 | 已接：侧栏常显「团队」（`sidebar-nav.tsx`），入口无任何计数徽章；`/team` → `TeamScreen` 零值显示空态「还没有队友或子任务」。域未投影时形态不变，`team.*` 落地后只填数据。测试：`team-entry.test.tsx`（常显、无 0、空态、选中态）；浏览器实测（`.cache/ui-review/team-empty.png`）：侧栏 `收件箱/能力/自动化/洞察/团队/设置`，`/team` 显示「还没有队友或子任务」 |

## 汇总

**完成 16 / 部分 4 / 未做 2**（22 条）

> B5 由"完成"改判"部分"：验收原文要求"**全站跟随**"，而 10 个文件（自动化/能力/洞察/检查器/设置页标签/记忆/子会话/`api/settings.ts`/`api/inbox.ts`/`tools/registry.tsx`）English 模式下仍是中文。基础设施与主路径已完成、且有扫描闸门防回归，但"全站"未达成——账面按事实记，不按进度记。

未做的 2 条与原因（都等 Go 轨的域投影，不由 web 侧发明协议）：
- **B11 消息菜单**：`message.regenerate` 在 TRANSPORT §5.1 状态为"延后"，且没有编辑方法；只做复制等于半截菜单。
- **B12 后台任务 chip**：`contracts/tasks.go` 有 5 个类型，但网关未投影 `tasks` 域 → 未投影，不是不存在。

部分的 2 条：
- **B6** 三 Tab 完成、「上下文」按能力隐藏（已知等价性缺口，需 Go 侧 `context.*`）；

## 附：本轮为补齐而改的代码

- `features/settings/settings-screen.tsx`：B2（Esc 常驻 claim）+ B3（字段级搜索 → `?focus=` → 高亮滚动）
- `shell/app-shell.tsx` / `shell/sidebar.tsx` / `shell/sidebar-nav.tsx`：B16（Mod+B 折叠、宽度记忆、当前会话圆点）
- `features/sessions/sessions-context.tsx`：C1（600ms 创建防抖）
- `chat/tool-card.tsx`：B10（失败调用展示 error 输出）
- `design/global.css`：B7/B8（1024/900/480 断点）、B16（图标栏）、B3（定位高亮）
- `features/routes.ts`：顶部注释改成现状、删掉 11 条过期 `deferral`（只留 R6）