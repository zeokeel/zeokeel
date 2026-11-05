# `internal/shell` 重构计划（AR-04）

> 状态：**PARTIAL（已做增量模块化，整体重构 DEFERRED）** · 日期：2026-11-06 · 基线 `HEAD=20e98d4`（+ 工作树）
> 关联：[BUGS AR-04](../BUGS.md)（P2）、[AR-08](../BUGS.md)（UI 线程同步 DB）、[known-limitations §3.9](../design/known-limitations.md)
> 本文只记录现状与分批计划，**本轮不实施**；每阶段完成后回写 BUGS/STATUS。

## 1. 现状（2026-11-06 实测）

| 指标 | 数值 |
| --- | --- |
| `internal/shell` 生产代码 | **57 文件 / 22,865 行** |
| `internal/shell` 测试代码 | 89 文件 / 24,784 行（测试≈生产） |
| 全仓 Go | 149,156 行（shell 占 ~15%） |
| `App` struct 定义 | `internal/shell/app.go` **262 行**（状态 + 依赖 + 窗口/设置窗口 seam） |
| `App` 方法数 | **711**（`grep -c "func (a \*App)"` 全包合计） |
| 最大生产文件 | `workspaces.go` 1,504 · `right_sidebar.go` 1,232 · `settings_ui.go` 1,033 · `composer_slash.go` 989 · `session_live.go` 933 · `plugin_details.go` 858 · `composer_menus.go` 843 · `automation_ui.go` 794 · `models_ui.go` 763 · `session_search.go` 741 |
| 直接内部依赖 | `store`、`session`、`state`、`runtime`、`tools`、`automation`、`winproc`、`design`、`brand`、`icons`（10 个；view 文件里直接调 `store`/`Ctrl`） |
| 已模块化的新文件（wave2/3 增量） | `manage_ui.go` 439 · `settings_extra.go` 703 · `presets_ui.go` 500 · `session_errors.go` 165 · `session_open_async.go` 144 · `sessions_cache_async.go` 137 · `workspace_changes_ui.go` 91 · `draft_scope.go` 228 · `buildinfo.go` 57 · `plan_ui.go` 475 |

判断：**规模没有失控，但耦合结构未变**——新功能继续以"再开一个文件 + 加 App 方法"的方式增量落地
（`manage_ui`/`settings_extra`/`presets_ui` 都是这样进来的），而 `App` 仍是单一巨型状态对象，
view 仍直接读 `store` 并同步调 `Controller`。因此 AR-04 从"41.4k 行/120 文件"的历史统计变为
"22.9k 生产行/57 文件但 App 711 方法"，问题从**行数**转为**中心化耦合**。

## 2. 结构与风险

```
cmd → app（组装根）
        └─ shell（UI + 交互 + 业务动作 + 数据读取 混在一起）
             ├─ 直接 import store/session/state/runtime/tools/automation/winproc
             ├─ view 文件里读 store、写 Ctrl（同步，UI 线程）
             ├─ 跨屏共享状态全部挂在 App struct（262 行字段）
             └─ i18n（660 行单 map）、样式/令牌（design）复用在包内自由访问
```

风险按优先级：

1. **God object**：`App` 262 行字段 + 711 方法；任何屏的改动都能触达全局状态，回归面=整包。
2. **UI 线程同步 DB（AR-08 未修）**：`OpenSession`/`ListSessions`/`CreateSession` 在点击/快捷键处理器里同步执行；大会话打开会冻结帧。
3. **层级缺失**：view、动作、数据读取同层；无法用 import 规则约束（没有独立包边界）。
4. **大文件/长函数**：10 个 >700 行生产文件；`workspaces.go` 单文件 1,504 行混合树构建/行渲染/菜单/排序。
5. **测试成本**：24.8k 行测试直接操作 `App` 内部字段，抽取时必须保留薄包装，否则测试改写量爆炸。
6. **并行开发**：每个 wave 都在 shell 上加功能（settings 补页、presets、manage UI），重构窗口必须排在 wave 之间。

## 3. 目标分层（不实施的契约）

```
shell/kit      纯渲染原语 + 布局/样式 helper（无 store/Ctrl 依赖）
shell/text     i18n 目录、格式化、面包屑/树排序等纯函数（可单测）
shell/model    read model：sessions/workspaces/messages/usage 的缓存与异步刷新（唯一读 store 的地方）
shell/actions  命令层：所有 Ctrl.* 调用的唯一入口 + 异步命令队列（UI 线程只 enqueue）
shell/screens  home / session / settings / plugins / automation / workspace 各自成包（只依赖 kit/model/actions）
shell/app      生命周期、窗口/tray/settings-window seam、组装（不承载业务状态）
```

依赖方向固定：`screens → actions → model → kit/text`；**screens 不再 import store/session**；
`app` 只做组装。可加两条自动门禁：`go list -deps` 断言 + "生产文件 >800 行 fail" lint。

## 4. 分批计划（每阶段独立可交付，保持测试绿）

| 阶段 | 内容 | 工作量 | 验收 |
| --- | --- | --- | --- |
| **P0**（S，1–2d） | 抽出无状态纯函数：树构建/排序/面包屑/格式化/i18n 目录加载 → `shell/kit`、`shell/text`；App 只保留薄调用 | S | 无行为变化；shell 测试全绿；`git diff` 纯移动 |
| **P1**（M，3–5d） | `shell/model` read model：session/workspace 列表与消息快照缓存 + 异步刷新；view 不再直接 `Ctrl.ListSessions/OpenSession`（先机械包装，再切异步） | M | AR-08 至少一半路径移出 UI 线程；慢 store 注入测试不阻塞帧；打开大会话不再冻结（headless 帧时间断言） |
| **P2**（M，3–5d） | `shell/actions` 命令层 + 异步命令队列：`Ctrl.*` 全部经 actions；UI 回调只 enqueue + 显示 pending 态 | M | 无 view 直接调用 Ctrl（grep 门禁）；错误/回滚语义不变；现有交互测试全绿 |
| **P3**（L，1–2w） | 拆分大屏：`workspaces.go` → workspace tree/list/menu/ordering；`right_sidebar.go` → panel per tab；`settings_ui.go`+`settings_extra.go` 合并为 settings 包；screen 包化 | L | 无生产文件 >800 行；import 边界测试通过；预览金标重生成 |
| **P4**（S，1d） | `app.go` 拆分生命周期/窗口/托盘/roster；`shell/app` 只做组装 | S | 行为不变；`App` 方法 < 300 |
| **收尾** | 删除兼容包装；BUGS AR-04 → DONE；known-limitations 去掉 PARTIAL 标注 | S | 全量门禁 + 独立验证 |

## 5. 顺带解决 / 明确不做的

- **顺带解决**：AR-08（UI 线程 DB，P1/P2）、AR-21/22 中的大函数拆分（P3）、`internal/shell` 测试组织（P3）。
- **不在本计划内**：mygo 框架出帧机制（UX-04 残留）、schema/持久化（AR-07/09 已完成）、
  per-session Agent（RT-04，已由后端 wave2 落地——shell 只消费）。
- **接口需求**：P1/P2 需要 `app` 暴露稳定的命令接口（`Controller` 的异步包装不需要改 internal 其他包；
  若需要新增 `app` 级 API，先走"跨范围信息约定"）。

## 6. 风险与缓解

| 风险 | 缓解 |
| --- | --- |
| 24.8k 行测试绑在 App 内部 | 抽取时保留同名薄方法转发；一次只移一个域；每阶段只跑定向测试 + 全量门禁 |
| wave 并行开发冲突 | 每阶段用独立分支/worktree；明确写范围；与 fix-frontend 排期错开 |
| 行为回归（聚焦/滚动/异步顺序） | 每阶段保留现有交互测试 + 增加 frame-time/pending 态断言；预览金标随 UI 相关阶段重生成 |
| L 项被无限推迟 | 先做 P0/P1（S/M，收益最大：文件变小 + UI 线程解耦），P2/P3 视发布节奏排期 |

## 7. 跟踪

- BUGS `AR-04` 保持 **PARTIAL**（已增量模块化），P1 完成前不标 DONE；P0 完成即可把
  "God-package 风险"描述从"未动"改为"已开始分层"。
- 每阶段结束更新本文 §1 指标表 + BUGS/STATUS；不实施前本文即是 DEFERRED 的书面计划。
