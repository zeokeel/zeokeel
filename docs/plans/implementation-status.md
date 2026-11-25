# 规划 → 实现 对照表（诚实版）

> 目的：回答"规划做完了吗"。本表只写**代码里能找到的东西**；没做的写"未做"，不要用"部分支持"含糊过去。
> 更新方式：每轮结束前改这一张表，行数不变。

图例：✅ 已实现且有测试 · 🟡 有实现但验收未达 · ⬜ 未做

## 一、差距清单（G1–G14）

| ID | 差距 | 状态 | 证据 / 位置 | 还差什么 |
|---|---|---|---|---|
| G1 | Bot 实体（模板+实例） | ✅ | `bot` 模块 + `bots.*`（含模板编辑）+ 派单到 Bot + `/settings/bots`；**工具绑定在门禁处真的执行**（`session.bindtask` → 不在绑定内的工具被拒，理由含任务与允许集） | 多用户角色（Owner/Manager/Collaborator）属 M6 第二期 |
| G2 | 队友开工前确认 | 🟡 | `tasklease`：`tasks.approval.set/get` + `awaiting_approval` 状态 + `tasks.approve` / `tasks.reject-start`（策略随任务持久化，未知值 fail-closed） | `before_send` 未做；无推送域（UI 需轮询）；审批通过/拒绝尚未发通知 |
| G3 | 任务板租约 | ✅ | `internal/modules/tasklease`（claim/heartbeat/expire，过期回收） | — |
| G4 | 证据与验收 | ✅ | `tasklease`（无证据拒收）+ `tasks.review` + 验收写团队笔记 | — |
| G5 | 统一收件箱 | ✅ | `internal/modules/inbox`（去重/配额/TTL/持久化）+ `inbox.dispatch/triage` | — |
| G6 | routine 触发（cron/webhook/event） | 🟡 | `automation`（定时任务）+ `webhook` 入站 + `heartbeat` 自转 | routine 没绑 Bot，事件触发只有 webhook 一种 |
| G7 | 记忆作用域（agent/team + 建议队列） | 🟡 | `teamnotes`（团队笔记 + 人工复核）+ memoryvfs 作用域 | 没有独立的"agent 私有作用域"与建议队列；复核是"通过/驳回"而非队列 |
| G8 | 产物库（Artifact） | 🟡 | 证据 `kind=file/screenshot` + blobs 存储 | 没有独立产物实体（hash/过期/可见性/任务反向引用） |
| G9 | 凭据库 | ✅ | `internal/modules/creds`（0600、读路径无字段）+ 适配器读取 | — |
| G10 | 浏览器（CDP，每 Bot 独立 context） | 🟡 | `internal/modules/browser`（真 ws 传输 + 工具） | 没做每 Bot 独立 browser context / profile 隔离 |
| G11 | 预算与配额（token/时长/并发） | 🟡 | `tasks.budget.*` + 真实计量 + 停摆通知 + 任务页预算块 + **每 Bot 并发 1**（派单时检查在飞租约并拒绝，理由点名占用任务） | 配额（跨任务合计）未做 |
| G12 | 从任务沉淀技能 | ✅ | `skilldraft` + 验收自动提议 + `skilldrafts.*`（含 `install` 写入真实 SKILL.md）+ `/skills/drafts` 采纳页 | 重名明确失败（不覆盖），由人改名后重试 |
| G13 | 工作入口连接器 | ✅ | `webhook`/`wechat`/`dingtalk`/`feishu`/`email` + `channeldispatch` 出站 + 飞书卡片审批 | 真平台凭据下的端到端仍待验证 |
| G14 | MCP 全量（注解→审批、两级白名单…） | 🟡 | 产品既有 toolkit/MCP 基建；`mcp__*` 走审批门；`tasks.tools` 任务级白名单**且已执行** | 注解→审批映射、prompts/resources、分页、结构化输出、进度取消未做 |

## 二、里程碑（M0–M6）

| 里程碑 | 状态 | 说明 |
|---|---|---|
| M0 地基 | ✅ | 收件箱→分诊→租约/证据→审批→路由，三层证据：单元/装配/实机（`scripts/verify-live` 12/12） |
| M1 记忆 | 🟡 | 团队笔记 + 复核 + 验收自动沉淀 + **agent 私有作用域（独立 memory 类型，团队板天然看不到）**；缺建议队列 |
| M2 触发 | 🟡 | webhook 入站 + heartbeat 自转 + automation 定时；缺"事件→收件箱"通用通道 |
| M2.5 MCP 补全 | 🟡 | 批次 A 部分落地：工具白名单已执行、**单次调用超时隔离**（默认 60s，超时后服务器仍可用）、注解说明写进 APPROVAL-SURFACES | 注解→审批的运行时读取、prompts/resources、分页、结构化输出、进度取消未做 |
| M3 浏览器 | 🟡 | CDP 真连接 + 三个工具；缺每 Bot 独立 context、下载拦截 |
| M4 连接器 | 🟡 | 四类渠道 + 出站 + 凭据 + 扫码登录协议 + **邮件按声明的字符集解码**（GBK/Big5/Shift_JIS 等）；**真机验证只做了本地能做的部分** |
| M5 浏览器收尾 | ⬜ | 未开工（依赖 M3 的隔离） |
| M6 共享（多用户） | ⬜ | 未开工 |

## 三、交互面（docs/design/grokbot-interaction-design.md）

| 面 | 状态 |
|---|---|
| `/inbox/incoming` 待办收件箱 | ✅ |
| `/t/:taskId` 任务详情五段 + 执行手段 + 工具绑定 | ✅ |
| `/team/board` 团队看板 | ✅ |
| `/settings/channels` 渠道凭据/Webhook/路由/扫码 | ✅ |
| `/settings/health` 自转/浏览器/投递测试 | ✅ |
| `/settings/memory` 共享记忆 + 复核 | ✅ |
| `/skills/drafts` 技能草稿 | ✅ |
| `/settings/bots` 机器人 | ✅ |
| 390px 窄屏 | ✅（四屏实测无横向滚动） |

## 四、验证手段

| 手段 | 命令 | 覆盖 |
|---|---|---|
| 单元 + 模块 | `scripts/verify.sh --quick` | 全仓 go test / vet / gofmt / 协议一致性 |
| 装配链路 | `go test ./internal/assembly/` | 服务键、注册、`TestM0ChainEndToEnd`、飞书链路 |
| 实机协议 | `scripts/verify-live/run.sh` | 真 serve + WS/HTTP：12 项，含整条 M0 链 |
| Web | `cd web && npm run typecheck && npm run lint && npm run test` | 603 条，含 B5 与契约一致性 |
| 真平台 | `docs/plans/channel-verification.md` | **需要真实凭据，尚未完成** |
