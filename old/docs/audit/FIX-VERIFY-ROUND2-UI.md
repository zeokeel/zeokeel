# Round 2 UI 独立验证（FIX-VERIFY-ROUND2-UI）

> 验证者：verifier-ui · 日期：2026-11-06 01:17–02:00 · 基线 `HEAD = 8e2ba5b`（工作树有他人未提交的 2 个 acp 文件；为纯净起见用 `git archive HEAD` 到 /tmp 构建）
> 自建二进制：`version=0.2.0-r2verify commit=8e2ba5b` sha256 `ef4b6898…`（从纯 HEAD 源码树构建）；Xvfb **:98 1600×1000x24** + `unshare -n` + `ZEOKEEL_DATA_DIR=/tmp/zk-r2/data`；xdotool/import；sqlite3。
> 截图：`.cache/audit/verify-round2-ui/shots/`（17 张）；FP-05 用**真实 store 注入事件**（helper 源码 `/tmp/zk-r2-src/audithelper/main.go`，未改仓库代码）后由 app 自身 replay 渲染，验证的是持久化/回放契约。
> 限制：未用真实模型（FP-05 为注入事件回放，非模型回合产生）；FP-16 后端阻塞 seam 仍未实现（见 §3），问题卡无法在真实回合中出现。

## 0. 结论摘要

| 项 | 判定 | 说明 |
| --- | --- | --- |
| FP-05 工作区变更卡：渲染/点击打开/重开回放/无变更不渲染 | **CONFIRMED** | 重启后 replay 出「工作区变更 3 个文件变更」卡（M/A/D + 中文状态）；点击行 → 右栏 Files 预览真实文件内容；无 changes 的新会话不渲染卡片 |
| UX-14 toast：Settings 顶部带 | **CONFIRMED** | 设置窗保存 toast「已保存」出现在**设置窗口顶部带**，不遮挡表单行；代码 `app.go:392-419` Top+AlignCenter+`PassThrough()` |
| UX-14 toast：主窗不遮 composer | **REFUTED（真 bug）** | Session 屏 1600×1000：toast 仍为 mygo 默认底部（y≈940-985），压住 composer 工具栏行；900×600 同样压住 composer 底部（`04/05`）。主窗没有走自绘 viewport（仅 SettingsView 调用），默认 toast 也无 PassThrough |
| FP-16 问题卡 chips/空回答拒绝 | **PARTIAL（后端未接线）** | UI 已实现（`todo_ui.go:142-230`：问题卡、选项 chips 点击直答、空/空白回答本地拒绝「请输入回答后再发送。」）；但后端阻塞 seam 仍是 RED（`TDD-RED-EVIDENCE.md §1`: runtime.Config 无 QuestionGate，ask_user fire-and-forget），真实回合不会产生 pending question → 无法真跑驱动 |
| 删除回归（模式 3 项 / 插件无语音） | **CONFIRMED** | `15` 模式菜单仅 标准/极简/创造；`16` 插件页 6 内置 + 3 实验，无「语音输入」 |
| 指针穿透（主窗） | **PARTIAL/未验证** | Settings 窗 toast 代码 `PassThrough()`、位置在顶部空白带（无下方可交互元素可点证）；主窗默认 toast 无 PassThrough（代码路径如此），且盖住 composer 工具栏 |

## 1. FP-05 复现细节（CONFIRMED）

1. UI 建 workspace `R2 WS` + 新会话（session id `95fda064…`），退出 app；
2. helper：`store.Open(dataDir)` → `AppendMessage` user/assistant 两条 + `AppendEvent(type="workspace/changes", payload={turn_id,changes:[{path,status}×3]})` + `status idle`；
3. 重启 app（自恢复 last_session）→ `11-fp05-card-replay.png`：卡标题「工作区变更  3 个文件变更」，行：
   `M internal/parser/parser.go 修改` / `A docs/README.md 新增` / `D legacy/old.go 删除`；
4. 点击第一行 → `12`：右栏切到 Files tab，面包屑 `R2 WS / parser.go`；文件不存在时显示「无法打开文件…no such file or directory」（预期）；把文件真实写到工作区后再点 → `13`：`纯文本预览 · 32 字节` + 内容 → **点击打开 CONFIRMED**；
5. 新建会话（无 changes 事件）→ `14`：空态、无卡片 → **无变更不渲染 CONFIRMED**；
6. 上述 `11` 本身就是「进程重启后从 store 回放事件」的截图 → **reopen replay CONFIRMED**。
代码：`internal/shell/workspace_changes_ui.go:22-90`（渲染/点击→`openFilePreview`）、`session_live.go:272`（transcript 后渲染）、事件持久化 `internal/session/events.go` `EventWorkspaceChanges="workspace/changes"`。

## 2. UX-14 复核细节（主窗 REFUTED，Settings CONFIRMED）

- 触发：Session composer 输入 `/clear` → toast「已清空输入框草稿」。
- `04-main-toast-session-1600.png`：toast 位于 y≈940-985（底部居中），composer 卡片 y≈855-965、工具栏 y≈939 → **toast 覆盖 composer 工具栏**（与「不遮 composer」宣称相反）。
- `05-main-toast-session-900x600.png`：900×600 下 toast 压住 composer 卡片底部/左侧工具按钮。
- `09-settings-toast-root.png`：设置窗内切「聚焦时静默」→「已保存」toast 在设置窗**顶部带**（win 坐标 y≈3-55、居中），不遮挡下方表单；符合代码 `settings_ui.go:296` + `app.go:408-419`（`Top()` + `PassThrough()`）。
- 代码事实：`toastViewport` 只在 `SettingsView` 调用，主窗 `View` 不调用（`8e2ba5b` 的 diff 明确注释保留 mygo 默认放置）；因此主窗 toast 既非顶部带、也无 pass-through。**结论：TDD-RED-EVIDENCE「主窗口默认 toast 不遮 composer」与真跑不符**（其 3 个测试可能只断言了 settings 侧/或不含 Session 屏底部布局）；建议回修：主窗也接自绘 viewport（顶部带 + PassThrough），或把默认 toast 的 bottom 偏移抬到 composer 之上。

## 3. FP-16 问题卡（PARTIAL）

- UI 代码已具备：`internal/shell/todo_ui.go:142-194` 问题卡（问题文本/context/建议答案 chips/输入框/发送）；`:219-230` chips 点击直接 `AnswerQuestion`；`:200-218` 空/纯空白回答本地拒绝并显示「请输入回答后再发送。」。
- 但后端阻塞 seam 未实现：`docs/audit/TDD-RED-EVIDENCE.md §1` 明确 `go test -tags tdd_red -run TestAskUser ./internal/runtime` 仍 FAIL（Config 无 QuestionGate；ask_user 立即返回 pending 结束回合）；`FIX-REPORT` 将 FP-16 列为 ⏸ DEFERRED。
- 因此**真跑无法让问题卡出现**（需要真实回合中 ask_user 阻塞），chips 提交/空回答拒绝的运行时行为未独立验证，仅代码级确认。建议 master 维持 DEFERRED，并将「UI 已就绪、等后端 gate」写入状态。

## 4. 删除回归（CONFIRMED）

`15-mode-menu-3.png`：会话 header 模式菜单仅 标准模式（勾选）/极简模式/创造模式；`16-plugins-no-voice.png`：插件页内置 6 项、实验 3 项（代理团队/自动授权审核/开发者工具），无「语音输入」。与 Round1 `FIX-VERIFY-REMOVAL-UI.md` 一致，8e2ba5b 未回潮。

## 5. 未验证 / 限制

1. FP-05 为**注入事件回放**验证（真实 store/format + app replay），未用真实模型跑「工具写文件→回合 diff」链路；diff 生成（`internal/workspacechanges`）本身未在本报告复验（属 core 侧）。
2. FP-16 运行时不可达（见 §3）。
3. 主窗 toast「指针穿透」无法点证（toast 下方无可交互控件）；仅证明其**位置覆盖 composer**且无 PassThrough 代码路径。Settings 窗 pass-through 为代码+位置证据。
4. 无真实窗口管理器（Xvfb 无 WM）——toast 位置/遮挡为应用自绘，环境不影响本结论。
5. 未覆盖 Round2 其他 UI（更新页/预设/ACP 等），非本任务范围。
