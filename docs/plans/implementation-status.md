# 规划 → 实现 对照表（诚实版）

> 目的：回答"规划做完了吗"。本表只写**代码里能找到的东西**；没做的写"未做"，不要用"部分支持"含糊过去。
> 更新方式：每轮结束前改这一张表，行数不变。

图例：✅ 已实现且有测试 · 🟡 有实现但验收未达 · ⬜ 未做

## 一、差距清单（G1–G14）

| ID | 差距 | 状态 | 证据 / 位置 | 还差什么 |
|---|---|---|---|---|
| G1 | Bot 实体（模板+实例） | ✅ | `bot` 模块 + `bots.*`（含模板编辑）+ 派单到 Bot + `/settings/bots`；**工具绑定在门禁处真的执行**（`session.bindtask` → 不在绑定内的工具被拒，理由含任务与允许集） | 多用户角色（Owner/Manager/Collaborator）属 M6 第二期 |
| G2 | 队友开工前确认（三态齐） | ✅ | `none` / `before_start` / `before_send`（消息挂起，`tasks.send.pending` 可见原文，`tasks.send.approve` 才发且只发一次）+ 任务页「待发送消息」块 | 无推送域（需轮询） |
| G3 | 任务板租约 | ✅ | `internal/modules/tasklease`（claim/heartbeat/expire，过期回收） | — |
| G4 | 证据与验收 | ✅ | `tasklease`（无证据拒收）+ `tasks.review` + 验收写团队笔记 | — |
| G5 | 统一收件箱 | ✅ | `internal/modules/inbox`（去重/配额/TTL/持久化）+ `inbox.dispatch/triage` | — |
| G6 | routine 触发（cron/webhook/event） | 🟡 | `automation` 定时 + `webhook` 入站 + `heartbeat` 自转 + **内部事件 → 收件箱**（`raiseInboxEvent`，按 dedupe key 去重；预算停摆已接） | routine 没绑 Bot；事件源目前只有预算停摆一处，其余事件（审批等待、任务过期）未接 |
| G7 | 记忆作用域（agent/team + 建议队列） | 🟡 | `teamnotes`（团队笔记 + 人工复核）+ memoryvfs 作用域 | 没有独立的"agent 私有作用域"与建议队列；复核是"通过/驳回"而非队列 |
| G8 | 产物库（Artifact） | 🟡 | 证据 `kind=file/screenshot` + blobs 存储 | 没有独立产物实体（hash/过期/可见性/任务反向引用） |
| G9 | 凭据库 | ✅ | `internal/modules/creds`（0600、读路径无字段）+ 适配器读取 | — |
| G10 | 浏览器（CDP，每 Bot 独立 context） | 🟡 | `internal/modules/browser`（真 ws 传输 + 工具） | 没做每 Bot 独立 browser context / profile 隔离 |
| G11 | 预算与配额（token/时长/并发） | 🟡 | `tasks.budget.*` + 真实计量 + 停摆通知 + 任务页预算块 + 每 Bot 并发 1 | 跨任务/按人配额未做（M6 归属） |
| G12 | 从任务沉淀技能 | ✅ | `skilldraft` + 验收自动提议 + `skilldrafts.*`（含 `install` 写入真实 SKILL.md）+ `/skills/drafts` 采纳页 | 重名明确失败（不覆盖），由人改名后重试 |
| G13 | 工作入口连接器 | ✅ | **指令入口 = Web + 微信**（+ `webhook`/`email` 入站）；`dingtalk`/`feishu` 降级为**只出站**（`Send`+`Status`，接口里没有 `Receive`，入站路由与卡片审批已删）+ `channeldispatch` 出站 | 真平台凭据下的**出站**送达仍待验证 |
| G14 | MCP 全量（注解→审批、两级白名单…） | 🟡 | 产品既有 toolkit/MCP 基建；`mcp__*` 走审批门；`tasks.tools` 任务级白名单**且已执行** | 注解→审批映射、prompts/resources、分页、结构化输出、进度取消未做 |

## 二、里程碑（M0–M6）

| 里程碑 | 状态 | 说明 |
|---|---|---|
| M0 地基 | ✅ | 收件箱→分诊→租约/证据→审批→路由，三层证据：单元/装配/实机（`scripts/verify-live` 12/12） |
| M1 记忆 | ✅ | 团队笔记 + 复核 + 验收自动沉淀 + agent 私有作用域 + 建议队列（协议 + `/settings/memory` 上的待确认区，采纳/驳回/提议） | — |
| M2 触发 | 🟡 | webhook 入站 + heartbeat 自转 + automation 定时 + **事件→收件箱**通用通道（去重） | 事件源只接了预算停摆；routine 未绑 Bot |
| M2.5 MCP 补全 | 🟡 | 批次 A 部分落地：工具白名单已执行、**单次调用超时隔离**（默认 60s，超时后服务器仍可用）、注解说明写进 APPROVAL-SURFACES | 注解→审批的运行时读取、prompts/resources、分页、结构化输出、进度取消未做 |
| M3 浏览器 | 🟡 | CDP 真连接 + 三个工具 + **每 Bot 独立 browser context**（独立 cookie jar；空 profile 拒绝；并发首开只建一个；关闭释放） | 真浏览器需验证上下文命令经 WS 传输可用与大窗口行为；CDP 上下文不跨浏览器重启（需独立 user-data-dir，属 M5 收尾） |
| M4 连接器 | 🟡 | 入站（webhook/微信/邮件）+ 四类渠道出站 + 凭据 + 扫码登录协议 + **邮件按声明的字符集解码**（GBK/Big5/Shift_JIS 等）；钉钉/飞书只出站（入站已下线）；**真机验证只做了本地能做的部分** |
| M5 浏览器收尾 | ⬜ | 未开工（依赖 M3 的隔离） |
| M6 共享（多用户） | 🟡 | `users` 模块 + `Allowed` 在入口执行 + `users.*` 协议 + `/settings/users` + `user` 私有记忆作用域 + 任务 `requestedBy` 归属（派单记录、跨重启保留、`tasks.requestedby.get` 可读、任务页显示） | 预算未按人归属；渠道身份未绑定；**无登录**（角色是策略不是认证） |

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
| `/settings/users` 成员与角色 | ✅ |
| 390px 窄屏 | ✅（四屏实测无横向滚动） |

## 四、验证手段

| 手段 | 命令 | 覆盖 |
|---|---|---|
| 单元 + 模块 | `scripts/verify.sh --quick` | 全仓 go test / vet / gofmt / 协议一致性 |
| 装配链路 | `go test ./internal/assembly/` | 服务键、注册、`TestM0ChainEndToEnd` |
| 实机协议 | `scripts/verify-live/run.sh` | 真 serve + WS/HTTP：12 项，含整条 M0 链 |
| Web | `cd web && npm run typecheck && npm run lint && npm run test` | 603 条，含 B5 与契约一致性 |
| 真平台 | `docs/plans/channel-verification.md` | **需要真实凭据，尚未完成** |
