# 审计修复状态跟踪（Lead 维护，实时）

> **wave 1 已完成并提交 `1da4f12`（119 文件）**：P0 + 大部分 P1（安全/权限/数据/运行时可靠性/UI-UX/产品/发布/文档）。详细修复清单与验证证据见 [`FIX-REPORT.md`](FIX-REPORT.md) §1–§5；产品侧状态真源见 `docs/BUGS.md`。
> wave 2 待办见 [`FIX-REPORT.md`](FIX-REPORT.md) §6；本文件保留 wave 1 前的计划式跟踪（历史参考）。
> 基线：`HEAD=3100af0`（2026-11-05 22:18）。修复计划：`FIX-PLAN.md`。最终交付：`FIX-REPORT.md`。
> 状态：⏳ 进行中 / ✅ FIXED（代码+测试） / 🧪 待独立验证 / ⚠️ PARTIAL / ⛔ DEFERRED（写明理由）

## P0

| ID | 问题 | 负责人 | 状态 | 证据 |
| --- | --- | --- | --- | --- |
| RT-01 | workspace mcp.json 无审批执行任意命令 | fix-security | ✅ 已修复（待独立验证） | services.go connectMCP 默认 off + `mcp.workspace_config=allow`；`mcp_workspace_gate_test.go`（默认不执行/allow 执行/注册表端到端）；Lead 已跑测试 PASS |

## P1 — 后端/运行时/安全

| ID | 问题 | 负责人 | 状态 | 证据 |
| --- | --- | --- | --- | --- |
| RT-02 | submit 不按会话权限重建 Agent | fix-security | ⏳ | |
| RT-03 | Read Only 仍注册持久副作用工具 | fix-security | ⏳ | |
| RT-04 | 单 Agent 全局串行 / 审批阻塞前台 | fix-security（方案） | ⏳ | |
| RT-05 | Team 成员无 turnContext | fix-runtime | ⏳ | |
| RT-06 | 模型流 60s 总超时 | 并发提交 3111614 | ✅ 已修复 | commit |
| RT-07 | spill 跟随 symlink 写出工作区 | fix-security | ⏳ | |
| RT-08 | 无重试/退避 | 并发提交 6bd0720 | ✅ 已修复 | retry.go |
| RT-11/PD-14 | DB/key 文件权限 + 明文 key | fix-security | ⏳ | |
| RT-12/PD-03 | 单实例固定端口、二次启动无反馈 | fix-security | 🚧 进行中 | |
| RT-09 | @daily 按 UTC | fix-runtime | ✅ 已修复（待独立验证） | scheduler 本地时区 + 2 用例；`go test ./internal/automation ./internal/tools -run TestSchedule` |
| RT-10/AR-06 | 逐 token 持久化 | fix-runtime | ✅ 已修复（待独立验证） | store.AppendEvents 批量事务 + session/event_batch.go（8KB/200ms 或边界 flush）；新增 8 个测试；AR-06 snapshot O(n²) 部分延期 |
| RT-13 | 终态事件可丢 / early 无界 | fix-security | ⏳ | |
| RT-14 | auto-review 用 workspace AGENTS.md 当授权证据 | fix-security | 🚧 进行中 | |
| RT-15 | env scrub 范围 | fix-security | 🚧 进行中 | |
| FP-08 | write_file 非原子/无 read-before-write | fix-security | ⏳ | |
| FP-13 | MCP 仅 stdio | fix-security | ⏳ | |
| FP-19 | web_fetch 代理不一致 | fix-security | ⏳ | |
| FP-18 | 无 headless CLI | fix-security + fix-platform | ⏳ | |

## P1 — UI/UX

| ID | 问题 | 负责人 | 状态 | 证据 |
| --- | --- | --- | --- | --- |
| UX-01/UX-03/PD-01 | 键盘 Enter 发送静默 + 空会话 | fix-frontend | ✅ 已修复（待独立验证） | toastDeferred/queueToast + rollbackHomeSession；send_feedback_test.go 5 用例；Lead 已读 diff |
| UX-02 | 会话对话无空态 | fix-frontend | ✅ 已修复（待独立验证） | session_live.go transcriptEmpty + 无模型 CTA；2 测试 |
| PD-02 | 模型配置无预设/连通性测试 | fix-frontend | ✅ 已修复（待独立验证） | models_ui.go Provider 预设 + 异步 /models 测试连接；2 测试 |
| PD-09 | 失败回合原始错误栈 | fix-frontend | 🚧 进行中 | |
| PD-11/PD-17/PD-18 | Home 入口/工作区校验/删除确认 | fix-frontend | 🚧 PD-18 ✅ 删除确认；其余进行中 | |
| UX-04/05/06/07/08/09/10/11/13/14 | UI/UX P2 打磨 | fix-frontend | ⏳ | |

## P1 — 产品面/平台

| ID | 问题 | 负责人 | 状态 | 证据 |
| --- | --- | --- | --- | --- |
| PD-04 | 无 DSH 迁移 | 待定（Lead 决策：最小导入或改定位） | ⏳ | |
| PD-05 | 设置缺通知/数据/诊断/代理/更新/重置 | fix-frontend | ⏳ | |
| PD-06 | Automation 无历史/失败/绑定 | fix-security + fix-frontend | ⏳ | |
| PD-07 | MCP/Skills/AGENTS.md 无管理 UI | fix-frontend（后端接口 fix-security） | ⏳ | |
| PD-08 | 插件双界面不一致/空功能开关 | fix-frontend | ⏳ | |
| PD-10 | 重启不恢复会话/草稿丢 | fix-frontend（后端 store 键协调） | ⏳ | |
| PD-12 | 桌面生命周期缺失 | fix-frontend（部分）/延后 | ⏳ | |
| PD-13 | 安装包/签名/更新缺失 | fix-platform（部分：linux/mac 脚本） | ⏳ | |
| AR-01 | 无崩溃诊断 | fix-platform | ⏳ | |
| AR-02 | CI 只是本地脚本 | fix-platform | ⏳ | |
| AR-03 | 发布链路/版本未注入 | fix-platform | ⏳ | |
| AR-16/AR-12/13/14/15/19 | 文档真源/BUGS.md/漂移 | fix-platform | ⏳ | |
| AR-18/PD-15 | 窗口默认尺寸/恢复越界 | fix-security | ⏳ | |

## P2 — 架构/数据/功能（本轮尽力）

| 组 | 项 | 负责人 | 状态 |
| --- | --- | --- | --- |
| 架构 | AR-04 shell 分层、AR-05 双快照、AR-07 消息序号、AR-08 UI 线程 DB、AR-09 schema 迁移框架、AR-20/21/22 | 待排（部分可在修复中顺手） | ⏳ |
| 功能 | FP-01 workflow、FP-02 PTC、FP-03 异步 subagent、FP-04 goal、FP-05 changes、FP-06 preset、FP-07 schedule、FP-09 FTS5、FP-10 token/图片、FP-11 checkpoint、FP-12 provider、FP-14 附件、FP-15 jobs/team、FP-16 ask_user、FP-17 skills | fix-runtime/fix-security | ⏳ |
| 产品 | PD-19/20/21/22 | 待排 | ⏳ |

## L 项（明确延期，写入 known-limitations/roadmap）

workflow 完整 JS 引擎、PTC Node 运行时、ACP/SDK 完整协议、三平台签名/公证/自动更新基建、shell 整体重构、schema v2 大迁移、真机 IME/a11y/DPI/通知矩阵。
