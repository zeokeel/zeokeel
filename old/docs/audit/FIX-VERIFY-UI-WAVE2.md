# wave 2 UI/产品面修复独立复核（FIX-VERIFY-UI-WAVE2）

> 复核者：verifier-ui · 日期：2026-11-06 00:05–00:45 · 基线 `HEAD = 84c5b1f`（工作树干净，wave2 提交）
> 自建二进制：`-ldflags version=0.2.0-w2final commit=84c5b1f`，sha256 `c5b77199…`；Xvfb **:98 1600×1000x24**（无 WM）+ `unshare -n` + `ZEOKEEL_DATA_DIR=/tmp/zk-w2-verify/data`；xdotool/import；sqlite3 直查；截图 32 张：`.cache/audit/verify-ui-w2/shots/`。
> 限制：未用真实模型/真实 DSH 数据执行导入 CLI；导出/重置/删除全部会话的确认弹层未逐一点开；Automation 绑定的下拉选项未逐项点选；AGENTS.md 编辑弹窗未驱动（需选中工作区后）。

## 0. 结论摘要

- 10 组新界面复核：**CONFIRMED 9 · PARTIAL 1 · REFUTED 0**。wave2 产品面真实落地，与 FIX-REPORT 声明一致；wave1 回归抽查正常。
- **全部关键界面有真跑证据**：PD-05 六页设置（含功能读写）、PD-06 Automation 绑定/历史/失败原因（真跑 run-now 失败入 `automation_runs` 并在 UI 显示）、PD-07 MCP JSON 校验/技能扫描、PD-10 重启恢复会话+草稿、About/诊断版本单一来源、setup 卡、DSH 导入入口。
- **唯一 PARTIAL**：PD-07 的 AGENTS.md 编辑弹窗（页面 section 存在，未选中工作区时显示「请先选择工作区」；编辑/保存流程未驱动）。
- 未发现 P0/P1 回归；UX-04 仍按上一轮结论 PARTIAL（代码注释已自认 mygo frame-hold 限制并登记 docs/BUGS.md，本轮未复测）。
- 新观察（P3 级）：Automation「立即运行」在提交失败时仍创建空会话（侧栏出现 0 消息的「W2 task」会话），与 UX-01 空会话污染同类，建议失败回滚或标注。

## 1. 判定矩阵

| 项 | 声称 | 判定 | 复核证据 | 备注 |
| --- | --- | --- | --- | --- |
| PD-05 设置补页 | 通知/数据与隐私/诊断/网络/桌面/更新 | **CONFIRMED** | 实机截图 04–09：通知（回合通知/聚焦时静默两个开关；关「聚焦时静默」后 DB `notification.suppress_focused=false` 持久化）、数据与隐私（数据目录/打开数据目录/API 密钥本地说明/从 DSH 导入/导出全部数据/重置设置/删除全部会话）、诊断（版本/build/运行时/日志路径/复制诊断信息/导出日志）、网络（代理地址；填 `http://127.0.0.1:7891` 后 DB `network.proxy` 落库）、桌面（关闭窗口时=隐藏；登录时启动开关）、更新（当前版本/更新通道=仅手动/检查更新） | 设置窗 900×840 中 14 个 nav 项全部可见无裁切 |
| PD-06 Automation | 绑定/最新运行/失败原因/历史/前台提示 | **CONFIRMED** | 24：页头「自动化任务仅在 ZeoKeel 前台运行时执行。」；25：新建表单含 **工作区/权限/模型** 三个绑定下拉（默认工作区/默认权限/默认模型）；26：列表行显示「每天 00:00 · 下次 … · 尚未运行」+ 立即运行/历史/停用/编辑/删除；27/28：点立即运行（无模型）→ 日志 `no agent configured`；`automation_runs` 表 1 行；列表行变「失败 · 刚刚 / submit: session: no agent configured」，历史展开显示「失败 · 刚刚 · 完成于 刚刚 / …」+「打开会话」 | 默认选项为空=按运行时当前值（设计）；显式绑定选项未逐项点选 |
| PD-07 管理 UI | MCP JSON 编辑/校验、Skills 扫描/错误标记、AGENTS.md 编辑 | **PARTIAL** | MCP：15 页面（JSON 编辑器+保存服务+工作区 mcp.json 忽略说明）；17 输入非法 JSON → 状态「JSON 无效，请在下方修正」+ 红色错误 + toast「MCP 服务未保存：…」，DB 无 `mcp.servers`（拒绝落库）。Skills：18 根目录输入/扫描路径/「根目录下未找到 SKILL.md」空态/AGENTS.md section（未选工作区时「请先选择工作区」） | AGENTS.md 编辑弹窗与 Skills 校验错误样例未驱动（需选中工作区+建 SKILL.md 样本），仅确认入口存在 |
| PD-10 恢复 | last_session + draft 持久化并启动恢复 | **CONFIRMED** | 建会话→输入 `restore me please`（不发送）→ DB 出现 `ui.last_session=<id>`、`draft.<id>=restore me please`；More→退出（干净退出）；同 data dir 重启 → 直接回到该会话、composer 回填草稿、`messages=0`（**未自动提交**，21/23 截图） | 500ms debounce 代码为 `session_restore.go` |
| About/版本 | 单一来源 | **CONFIRMED** | 12：只显示 `0.2.0-w2final (commit 84c5b1f, built …)`，无旧「version 0.1.0」行；诊断页同源（06） | 上次 PARTIAL 已修复 |
| context.budget_tokens 行 | 设置行 | **CONFIRMED** | 30：通用>高级「上下文预算 200000（压缩前的近似 token 上限…）」 | 落库键名见 `settings_ui.go:622-627` |
| 首启 setup 卡 | 无模型/无工作区引导 | **CONFIRMED** | 01：「完成 ZeoKeel 的初始设置」+ 两行状态（还没有工作区…；尚未配置模型…）+「创建工作区」「配置模型」按钮；点创建工作区成功弹对话框（20） | |
| PD-04 导入入口 | 「从 DSH 导入」可见 | **CONFIRMED** | 05 数据与隐私行「从 DSH 导入 / 用 zeokeel import 命令迁移…」+ 19 弹窗：`zeokeel import --dry-run --json`（预览）与 `zeokeel import` 两条命令+复制按钮+默认目录说明 | CLI 未执行（UI 入口验证） |
| wave1 回归抽查 | UX-01/02、PD-02/09/18 正常 | **CONFIRMED（抽查）** | 32：会话 composer 键盘 Enter 无模型 → toast「尚未配置 Agent。请在 设置 > 模型 中添加模型。」+ 草稿保留 + 空态无模型 CTA（UX-02）仍在；PD-02/09/18 上轮已真跑 CONFIRMED（FIX-VERIFY-UI），本轮未回归 | |
| UX-04 | （上轮 PARTIAL） | 维持 **PARTIAL** | 本轮未复测；`settings_window.go` 注释已自认「state 清理+重绘正确，但 mygo 帧保持可能残留」，追踪 docs/BUGS.md | 建议 master 保持 OPEN 或降 P3 已知限制 |

## 2. 功能读写验证（DB 直查）

- `notification.suppress_focused`: 由 true → false（点开关后即时落库）。
- `network.proxy`: `http://127.0.0.1:7891`（网络页输入回车后落库）。
- `ui.last_session` / `draft.<session>`: 存在，重启后 UI 恢复一致。
- `automation_runs`: run-now 失败后 1 行，UI 显示失败原因原文；`automations` 新字段 workspace_id/permission/model 存在（默认空=运行时回退）。
- MCP 非法 JSON：`mcp.servers` 未写入（校验失败拒绝保存）。

## 3. 发现的问题/观察

1. **P3 观察（新）**：Automation「立即运行」以无模型状态触发时，仍先创建会话再提交失败 → 侧栏留下 0 消息会话「W2 task」（32 中可见，与 DSH 一致性/UX-01 空会话污染同类）。建议：失败时不保留会话，或行内注明「已创建会话（失败）」。
2. **P3 观察**：全新数据目录下 Skills「扫描路径」显示 `/data/mygo/skills`（=进程工作目录下 skills），对从任意目录启动的桌面应用可能不符合直觉；建议默认显示数据目录或明确说明相对路径语义。
3. 其余未见新缺陷：设置 14 项 nav 在 840 高度内完整；各页文案/布局一致；无截断（独立窗口直抓）。
4. UX-04（上轮 PARTIAL）：代码已记录为 mygo frame-hold 已知限制，若 master 接受为 known-limitation 则可关闭，否则保持 OPEN。

## 4. 未验证 / 限制

1. 数据与隐私的「导出全部数据 / 重置设置 / 删除全部会话」未点开确认弹层与产物 zip 校验（只读截图）。
2. DSH 导入 CLI 未真跑（未用真实 `/root/.dsh` 做 dry-run 导入）；仅验证 UI 入口命令文案。
3. Automation 工作区/权限/模型的显式绑定选项未逐项点选；仅验证下拉存在与默认值。
4. AGENTS.md 编辑/保存、Skills 校验错误标记未驱动（PARTIAL 原因）。
5. Notifications 只验证开关持久化，未验证真实系统通知行为（Xvfb 无通知服务）。
6. 真实模型/真实代理流量未验证（代理仅落库）。

## 5. 建议 master

- **接受**：PD-05/06/07（AGENTS 编辑部分除外）/10、About 单一来源、context budget、setup 卡、PD-04 入口、桌面/更新页 → 均可在 FIX-REPORT 标记 CONFIRMED。
- **保留**：PD-07 的 AGENTS.md 编辑流程 PARTIAL（建议补一次「选中工作区→编辑 AGENTS.md→保存→读回」真跑）；UX-04 按上述决定。
- 新增 P3 观察 2 条可进 docs/BUGS.md，不必阻塞 wave2 提交。
