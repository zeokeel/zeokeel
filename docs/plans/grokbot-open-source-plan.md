# Grok Bot 开源实现：整体规划与改造方案

> 状态：**规划稿（待用户拍板 §11 的六条）**，未改任何代码。
> 目标读者：要动手改 `internal/contracts`、`internal/gateway`、`internal/modules/*`、`web/` 的人。
> 对标对象：xAI Grok Bot（Team Bots / Bot 1.0，见 §1 的引用），不是逐条复刻，而是把它的**组织模型**装进我们已有的**运行时机制**。

---

## 0. 决策摘要（已与你确认）

| 维度 | 决定 | 含义 |
|---|---|---|
| 定位 | **单机个人版优先**，团队共享做第二期 | 无登录、无多租户；先跑通"一个人 + 多个岗位 Bot + 任务认领" |
| 执行面 | **全功能 agent，不做桌面/屏幕**；浏览器用**本机 Chrome/Edge + CDP** | 保留文件、命令、MCP、技能、网络；浏览器是"本机真实浏览器"，不是云电脑 |
| 记忆 | **岗位记忆 + 团队共享 + 审核队列** | 抄 Grok Bot 最核心的差异化，长在我们已有的 memory 三轨上 |
| 触发 | **定时 + Webhook + 外部事件连接器**（Slack/GitHub/邮件），并保留 offer-first 主动性 | routine 绑"岗位"，不绑"某次会话" |
| 编排 | **人只与 Lead 交互；Lead 派活、建队友、发工具、发权限**；所有信息汇总到 Lead | 单一编排者，队友不直接面对人 |
| 账号 | 单用户、无登录 | 权限只分"用户 / Lead / 队友"，靠工具授予 + 审批 + 沙箱兜底 |
| 浏览器 | **默认可见窗口**（已确认）；headless 仅作为设置项 | 用本机 Chrome/Edge + CDP，随时能看、能人工接手 |
| 连接器 | **两类分开**：① **工作入口**（微信 ClawBot / 钉钉 / 飞书 / 邮件）— 能把活递进来、把结果递回去；② **MCP 能力连接器**（已有，**全量补全**） | A 类产出 `InboxItem` 进收件箱；B 类产出 `Tool` 进工具面，二者不混用 |
| 微信接入 | **个人号走官方 ClawBot（iLink 协议）**，不是第三方 hook | 扫码登录、24h 续连、约 7 条/5 分钟限速、仅文本（媒体需 CDN 加密上传） |
| 移动办公 | **通知路由表 + 绑定用户**：审批/完成/决策可以推到微信/钉钉/飞书/邮件上，并在那里回复 | 电脑挂着就行，人在手机上下决定 |
| 记忆终审 | **不对称**（已确认）：规则类记忆（"永远先问我"）需你确认；事实类记忆（"表在 Drive"）Lead 直接生效、可回滚 | 规则改变行为，事实只补上下文 |
| 预算 | **token 为主、时长为辅**（已确认）；超限 `blocked` 交 Lead 决策 | 不自动追加、不静默降级 |
| 认领 | **混合模型（已确认）**：Lead 指派为主；开放池**默认关闭**，只有 Lead 显式 `open` 的任务可自领 | 详见 §6.3 |
| 验收 | **全部由 Lead 自审（已确认）**：任务质量不阻塞你；但**动作级审批不放宽**——写盘/对外发送/花钱/删数据仍由人回答 | 详见 §6.3 第 10 条与 §6.6 |
| 里程碑 | **M0 与 M3 并行推进（已确认）** | 任务闭环与 CDP 浏览器两条线同时开工，用子代理分头做 |

**一句话产品定义**：一个本机常驻的开源 Grok Bot——你只跟 Lead 说话，Lead 有一支可配置的队友队伍、一块共享任务板、一个外部事件收件箱，以及一套"谁在什么时候可以动手"的权限规则。

---

## 1. 对标拆解：Grok Bot 的 12 个能力面

| # | 能力面 | Grok Bot 的做法 | 对我们的意义 |
|---|---|---|---|
| 1 | **Bot 实体** | 有名字、岗位、自己的会话、随时间积累的工作上下文；可复制成模板分享 | 队友要从"一次 spawn"升级为"一个可配置岗位" |
| 2 | **能力四件套** | Context（文件/指令/技能）+ Plugins（SaaS）+ Credentials（Vault）+ Memories | 我们的对应物：workspace/技能/MCP/凭据库/记忆——缺凭据库 |
| 3 | **记忆三层** | 全 Bot 共享的少量事实 / 每 Bot 私有（规则+工作日志）/ 团队共享（改动需审核） | 现有三轨（session/project/key）只覆盖前两层的一半 |
| 4 | **routine** | skill 定义"怎么做"，routine 定义"何时跑"（cron / Slack / GitHub / Linear / Sentry / 邮件 / webhook），历史有界（20 条） | 我们只有 cron 式 automation，且 prompt 直接投会话 |
| 5 | **主动性** | offer-first：先提议"这个我来"，你点头才做；不做事先动作 | 队友需要"提案"这一等公民 |
| 6 | **审批** | Auto Review 默认开 + 自定义 Ask first 规则；共享聊天里审批卡只由"被回答的人"处理 | 我们有 approvals + 审批面清单，缺"按任务/按队友"的策略 |
| 7 | **共享与角色** | Team Bot 有 Owner / Manager / Collaborator；团队记忆与技能全员共享，学习需 reviewer | 第二期再做，但数据模型现在就要留位 |
| 8 | **工作区** | 一台云电脑按**账号**分配，所有 Bot 共享 `/workspace` 与登录态；每 Bot 只分"屏幕"；用户之间硬件隔离 | 单机必须**反过来**：按目录/root 隔离，不共享凭据 |
| 9 | **产物库** | 会话卡片 + 每 Bot 的 Library（Pages/Media/Links/Files，可 Pin/分享） | 我们只有 present/filedrop 雏形，无库、无生命周期 |
| 10 | **技能** | 把一次成功任务存成 skill（六段式：何时用/输入/步骤/校验/返回/审批边界），可发布给团队 | 已有 skills 存储与编辑，缺"从这次任务沉淀"的入口 |
| 11 | **连接器** | Slack/Teams/会议/Gmail/Outlook/1Password/浏览器 | 分两类做：**工作入口**（微信/钉钉/飞书/邮件，见 §6.4）+ **MCP 能力连接器**（已有，补全见 §7.5） |
| 12 | **会话模型** | 话题 × 人 × Bot；事件**不**新建会话；routine 结果回到创建它的会话 | 这条现在的实现基本正确，需把"事件进收件箱"补上 |

---

## 2. 现状盘点（有 / 半有 / 没有）

| 能力 | 现状 | 代码位置 | 结论 |
|---|---|---|---|
| 队友 = 命名子会话 | 有：spawn/send/wait/interrupt/close、RunState、冷恢复 | [team.go](../../internal/contracts/team.go)、[team_coordinator.go](../../internal/modules/agent/team_coordinator.go) | ✅ 可直接用 |
| 团队纪律 | 有：只有 Lead 能 spawn，成员不再 spawn，`wait_agent` 不轮询 | [system_prompt.go](../../internal/modules/agent/system_prompt.go):283 | ✅ 基调正确 |
| 共享任务板 | 有：CAS、依赖、write scope、claim/complete/release/reopen、Lead 可 reassign、maxTasks | [task_board.go](../../internal/modules/toolkit/task_board.go)、[task_board_service.go](../../internal/modules/toolkit/task_board_service.go) | 🟡 缺 lease/心跳/证据/验收/优先级 |
| 会话与工作区 root | 有：一次 run 绑定 root，工具按 root 重建 | [workspaceroot.go](../../internal/contracts/workspaceroot.go)、`toolkit/execution_routing.go` | ✅ 单机隔离的地基 |
| 定时自动化 | 有：cron 式、提示词投递会话、历史 10 条、可覆盖 model/workspace/permission | [automation.go](../../internal/contracts/automation.go) | 🟡 只有时间触发，无事件触发 |
| 外部事件入口 | 没有 | — | ❌ 要做收件箱 + 事件网关 |
| 记忆 | 有：memoryv2 三轨（session/project/key）+ 待确认队列 | `internal/modules/memorystore`、[memoryv2.go](../../internal/contracts/memoryv2.go) | 🟡 缺"岗位/团队"两个作用域与审核视图 |
| 技能 | 有：文件系统技能、编辑器、MCP 工具 | `internal/modules/skills` | ✅ 可直接复用 |
| 审批 | 有：approvals 集合 + 审批面清单 + 三种动词 | [APPROVAL-SURFACES.md](../APPROVAL-SURFACES.md) | 🟡 缺按任务/按队友的策略层 |
| 沙箱 | 有：档位 + 策略 | `internal/modules/sandbox` | ✅ |
| 产物 | 半有：present、filedrop、filepreview | [interaction.go](../../internal/contracts/interaction.go):85 | ❌ 无 artifacts 库/生命周期 |
| 浏览器 | 没有 | — | ❌ CDP 接入（§9.3） |
| 收件箱 | UI 有 `/inbox`（5 类打断汇聚），后端无统一事件收件箱 | [WEB-CLIENT.md](../WEB-CLIENT.md) §1.4 | 🟡 复用 UI，换数据源 |
| Bot 实体 / 岗位模板 | 只有 prompt preset（一段文本） | [presets.go](../../internal/contracts/presets.go) | ❌ 要做 RoleTemplate + BotInstance |
| 预算 | 无按任务/按队友的用量与上限 | — | ❌ 要做 |

---

## 3. 差距清单（G1–G12）

每条给"最小可实现"和"验收"，避免做成空架子。

| ID | 差距 | 最小实现 | 验收 |
|---|---|---|---|
| G1 | 没有 Bot 实体 | `RoleTemplate`（岗位模板）+ `BotInstance`（实例）；Bot = 模板 + 记忆作用域 + 工具授予 + 默认模型 | 能建一个"研究 Bot"，重启后仍在，能接着上次的记忆干活 |
| G2 | 队友没有"开工前确认"的语义 | 任务加 `approval: none \| before_start \| before_send` | before_start 的任务被认领后停在 `awaiting_approval` |
| G3 | 任务板缺 lease | `claim` 返回 `leaseUntil`；`heartbeat` 续租；过期回到 `ready` 并记 `expired` 事件 | 杀掉队友进程后 N 分钟任务自动可再认领，且不会双开 |
| G4 | 任务缺证据与验收 | `complete` 必须带 `evidence[]`（命令输出/文件 hash/测试结果/截图）；`review` 状态 + `accept/reject(reason)` | 无证据的完成被拒绝；reject 后任务回到 ready 且保留历史 |
| G5 | 没有统一事件收件箱 | `InboxItem`（来源、去重键、优先级、TTL、状态）+ 批量分诊 | 同一 Slack 线程 10 条消息合并为 1 条待办 |
| G6 | routine 只绑时间 | routine 增加 `ownerBotId` + `trigger`（cron / webhook / event） | webhook 触发能落进收件箱或直接建任务（按配置） |
| G7 | 记忆没有岗位/团队作用域 | memory 增加 `scope: agent \| team`；团队写入进"建议队列"，Lead 或用户批准 | 队友学的规则只影响自己；团队规则经批准后全队可见 |
| G8 | 没有产物库 | `Artifact`（路径、hash、来源任务、可见性、过期时间）+ 会话卡片引用 | 任务完成后产物可在任务卡里打开，7 天后按策略清理 |
| G9 | 没有凭据库 | 本地加密存储 + 审批式注入（不落日志、不进上下文） | 队友永远拿不到明文；使用时走一次性注入 |
| G10 | 没有浏览器 | CDP：本机 Chrome/Edge + 每 Bot 独立 browser context | 两个队友各自登录不同账号互不串味 |
| G11 | 没有预算与配额 | 每任务 token/时长预算、每 Bot 并发上限、超限挂起 | 超预算任务停在 `blocked` 并要求 Lead 决策 |
| G12 | 没有"从任务沉淀技能"的入口 | 任务完成时提议生成 skill 草稿（人确认后入库） | 完成卡上出现"存为技能"，生成六段式草稿 |
| G13 | **没有工作入口连接器**（钉钉/飞书/微信/邮件） | 统一 `Inbound`/`Outbound` 适配器接口 + 验签/幂等/配额 + 收件箱 + 审批后回复 | 群里 @机器人 的消息能进收件箱并转成任务；回复经批准回到原线程 |
| G14 | **MCP 不够全面**（见 §7.5） | 注解→审批、工具两级白名单、prompts、超时重试（其余按需） | destructive 工具一定停审批；单 server 超时不拖住整轮 |

---

## 4. 目标架构

```
┌─────────────────────────────────────────────────────────────┐
│ L5 界面（web/）                                              │
│   Lead 会话 · 任务板 · 团队屏 · 收件箱 · 产物库 · 设置        │
├─────────────────────────────────────────────────────────────┤
│ L4 协议（internal/gateway + contracts）                       │
│   bots.* · inbox.* · routine.* · artifacts.* · team.* 扩展    │
├─────────────────────────────────────────────────────────────┤
│ L3 编排（agent 模块）                                         │
│   Lead 分诊器 · 派遣器 · 认领/租约 · 验收 · 预算 · 通知路由     │
├─────────────────────────────────────────────────────────────┤
│ L2 执行（现有）                                               │
│   子会话（session）· 工具 · 沙箱 · 审批 · 技能 · MCP · 浏览器   │
├─────────────────────────────────────────────────────────────┤
│ L1 事实源（持久化）                                           │
│   任务板 · 收件箱 · Bot/岗位配置 · 记忆 · 产物 · 事件日志       │
└─────────────────────────────────────────────────────────────┘
```

**六条架构原则**（后面所有细节都由它们推导）：

1. **单一编排者**：只有 Lead 能建任务、建队友、发工具、发权限；队友只能"提议"和"在自己被授予的范围内行动"。
2. **任务板是事实源**：谁在干什么以任务板为准，不以聊天记录为准；所有变更 CAS + 修订号。
3. **收件箱是唯一外部入口**：Slack/GitHub/邮件/webhook/定时/队友提议，全部先落收件箱，由 Lead 批量分诊；**事件不新建会话**。
4. **一切可恢复**：任务有租约、消息有收据、子会话可冷恢复、Lead 崩溃后从事实源重建视图。
5. **权限显式且可撤销**：工具与凭据都是"授予"，带范围与期限；审批只由人回答。
6. **噪音有界**：人只会被三类事件打断——审批、需要决策、任务完成/失败；其余折叠。

---

## 5. 数据模型草案

```go
// 岗位模板：可复用、可分享（第二期做成模板市场）
type RoleTemplate struct {
    ID          string   // role-researcher
    Name        string   // 研究 Bot
    Purpose     string   // 一句话职责
    Prompt      string   // 岗位说明（六段式：何时用/输入/步骤/校验/返回/审批边界）
    Tools       []string // 允许的工具名（白名单，空=只读默认集）
    Skills      []string // 启用的技能
    Model       string   // 默认模型（空=跟随会话）
    MemoryScope string   // agent（默认）/ team（可写团队建议）
    Approval    string   // none / before_send / always
    Budget      Budget   // tokens / 时长 / 并发
}

// Bot 实例：模板 + 身份 + 私有记忆作用域 + 当前授予
type BotInstance struct {
    ID         string   // bot-1（稳定 ID，不是 session）
    RoleID     string
    Name       string
    CreatedAt  time.Time
    Tools      []Grant  // 运行期追加授权（带期限与来源任务）
    Routines   []string
    Status     string   // idle / running / paused / archived
}

type Grant struct {
    Tool      string
    Scope     string    // 例：fs.write:/data/proj 或 net:api.github.com
    Until     time.Time // 零值=直到任务结束
    Reason    string    // 哪个任务、谁批准
    Revocable bool
}

// 任务：现有 Task 的扩展（字段名与现有 taskboard 对齐，新增部分标 +）
type Task struct {
    ID, Title, Detail, Status string
    OwnerID   string   // 成员 id（"" = 无主）
    DependsOn []string
    WriteScope []string
    // + 新增
    Priority   int       // 0..3
    Kind       string    // task / proposal / incident
    Approval   string    // none / before_start / before_send
    LeaseUntil time.Time // 认领租约
    Heartbeat  time.Time
    Budget     Budget
    Used       Usage
    Evidence   []Evidence // 完成凭证
    ReviewBy   string     // 谁验收：lead / human
    ParentID   string     // 父任务（拆解树）
    SourceRef  string     // 来自哪个 inbox item / 会话消息
}

// 收件箱：外部世界与队友提议的唯一入口
type InboxItem struct {
    ID        string
    Source    string    // slack / github / email / webhook / cron / teammate
    DedupKey  string    // 来源 + 线程 + 内容 hash
    Priority  int
    Title     string
    Body      string
    Ref       string    // 原始链接/ID
    Received  time.Time
    ExpiresAt time.Time
    Status    string    // new / triaged / task / ignored / archived
    TaskIDs   []string  // 分诊结果
}

// routine：现有 AutomationTask 的扩展
type Routine struct {
    ID, Name string
    OwnerBotID string        // + 归属岗位（不再是"某个会话"）
    Trigger  Trigger         // + cron{expr,tz} / webhook{key} / event{source,filter}
    Action   string          // task（直接建任务）/ inbox（进收件箱等分诊）/ chat（回原会话）
    Prompt   string
    Approval string          // 运行前是否要人确认
    History  []RunRecord     // 保留最近 20 条
}

// 产物
type Artifact struct {
    ID, Path, Kind string   // file / image / page / report
    Hash      string
    TaskID    string
    BotID     string
    Bytes     int64
    ExpiresAt time.Time
}
```

---

## 6. 核心交互逻辑（本篇重点）

### 6.1 一个任务的全景时序

```
你 ──"把竞品定价整理成表"──▶ Lead 会话（root session）
                              │
      ① 判重 ─────────────────┤ 已有同源任务？→ 追加为子任务并告诉你
                              │
      ② 拆解 ─────────────────┤ 生成 3 个 task：抓取 / 清洗 / 出表
                              │   每个 task 带：验收标准、write scope、预算、审批
                              │
      ③ 派遣 ─────────────────┤ 指派给「研究 Bot」「数据 Bot」；或放进开放池
                              │
     ┌────────────────────────┴─────────────────────────┐
     ▼                                                  ▼
 研究 Bot（子会话）                                 数据 Bot（子会话）
 claim → lease → 开工                               claim → lease → 排队等依赖
 每 60s heartbeat                                   依赖完成后自动 ready
 需要写文件/发消息 → 审批卡（由你答）                 完成 → evidence
     │                                                  │
     └──────────────► 任务板（事实源，CAS 修订）◄───────┘
                              │
      ④ 验收 ─────────────────┤ Lead 按 ReviewBy 审核：accept / reject(reason)
      ⑤ 汇总 ─────────────────┤ 只把结果、证据、遗留问题交回你的会话
      ⑥ 沉淀 ─────────────────┤ 提议"存为技能" / 写岗位记忆 / 团队记忆建议
                              │
                              ▼
                        你看到：完成卡（结果 + 证据 + 产物 + 下一步建议）
```

**关键点**：队友之间不互相"聊天对齐"，对齐发生在**任务板上**；所有跨任务的决策回到 Lead。

### 6.2 任务状态机

| 状态 | 含义 | 谁能进入 | 允许的下一步 |
|---|---|---|---|
| `pending` | 已创建，依赖未满足 | Lead / 依赖完成自动 | `ready`、`cancelled` |
| `ready` | 可被认领 | 依赖满足 | `claimed`、`cancelled` |
| `claimed` | 有主且租约有效 | 认领者 | `running`、`ready`（释放/过期） |
| `running` | 正在执行 | 认领者 | `review`、`blocked`、`failed`、`ready`（中断） |
| `awaiting_approval` | 等待人批准（before_start） | running 中的认领者 | `running`、`cancelled` |
| `blocked` | 缺信息/缺权限/超预算 | 认领者或 Lead | `running`、`failed`、`cancelled` |
| `review` | 完成待验收 | 认领者提交证据 | `done`、`ready`（reject） |
| `done` | 验收通过 | Lead/人 | — |
| `failed` | 失败且不再重试 | 认领者/Lead | `ready`（重开） |
| `cancelled` | 取消 | Lead/人 | — |

**转移必须是 CAS + 修订号**（沿用现有 taskboard 的做法）；每次转移写一条 `task.event`（谁、何时、从什么到什么、理由、证据引用），这是事后审计的唯一依据。

### 6.3 认领模型：三个候选与推荐

你还没定的就是这一条。三个选项：

| 模型 | 规则 | 优点 | 致命问题 |
|---|---|---|---|
| A. 纯指派 | 只有 Lead 能分配，队友不能自领 | 可控、可预测 | 队友退化成执行器，Lead 成为瓶颈 |
| B. 纯自领 | 任务板一有 ready 就谁闲谁抢 | 快、并行度高 | 重复劳动、抢到不会做、无人负责验收 |
| **C. 混合（推荐）** | **Lead 指派为主；只有 Lead 显式 `open` 的任务可自领；自领先"提案"再开工** | 保留主动性又不失控 | 多一个提案环节，需要 UI 呈现 |

**推荐的 C 模型完整规则**（可直接实现）：

1. **谁能创建任务**：只有 Lead（`team_task_create` 保持现状）。队友只能 `propose`（提案），提案进 Lead 收件箱，Lead 决定是否转成 task。
2. **两种可认领的任务**：
   - `assigned`：OwnerID 已指定 → 该队友 `claim` 即获得租约；其他人 claim 返回 `not_ready/owned`。
   - `open`：OwnerID 为空且 `pool=open`（Lead 显式打开）→ 任何人可 `claim`，先到先得。
3. **认领即租约**：`claim` 返回 `leaseUntil = now + leaseTTL`（默认 10 分钟，长任务靠心跳续）。**同一任务任意时刻最多一个有效 owner**，由 CAS 保证。
4. **开工前提案（open 池专用）**：claim 后 `intentTTL`（默认 2 分钟）内必须提交一行"我打算怎么做"，否则租约自动回收，任务回 `ready` 并记 `abandoned`。这条是防"抢了不做"的关键。
5. **心跳与回收**：`heartbeat` 续租；超过 `leaseTTL` 无心跳 → 网关把任务回 `ready`，写 `expired` 事件，**但不自动重派**（避免抖动），由 Lead 在下一轮分诊里决定给谁。
6. **抢占（preempt）**：只有两种合法抢占——租约过期，或 Lead 显式 `revoke(owner, reason)`。队友之间不能互抢。
7. **释放**：认领者可 `release`（保留已产出证据为评论），任务回 `ready`。
8. **并发闸门**：每个 Bot 有 `maxConcurrent`（默认 1，重活 Bot 可调 2）；开放池同时最多 `maxOpen`（默认 3）个 open 任务，防止全队涌向同一批活。
9. **完成必须带证据**：`complete` 不带 `evidence[]` 直接拒绝（G4）。
10. **验收一律由 Lead 自审**（已确认）：`submit` 后进入 `review`，Lead 自己判定 accept/reject，不阻塞你。**但这条只放宽"质量验收"，不放宽"动作授权"**——写盘、对外发送、花钱、删数据依旧停审批且只能由人回答（§6.6）。换句话说：Lead 决定"做得好不好"，你决定"能不能做"。为防止 Lead 用验收当授权，任务处于 `review` 期间不允许发起任何新的外部动作。

> 回答你"外部通知到底谁来派活"的疑问：**外部事件永远先落收件箱，由 Lead 分诊**（§6.4）；队友的"主动认领"只发生在 Lead **已经开放**的池子里。这样既不会漏事，也不会出现"队友看到一个邮件就自己冲出去干活"。

### 6.4 连接器：两类，不要混为一谈

| 类别 | 是什么 | 例子 | 接到哪里 | 安全要求 |
|---|---|---|---|---|
| **A. 工作入口连接器** | 外面的人/系统**把活递进来**，并且我们**要把结果递回去** | 微信 chatbot、钉钉机器人、飞书机器人、邮件 | 入站 → 收件箱 → Lead 分诊；出站 → 审批后回到原线程 | 验签、白名单、防重放、幂等键、速率限制 |
| **B. 能力连接器（MCP）** | **给队友长手**：查数据、调 API、用工具 | 已有 MCP（stdio / HTTP） | 直接进队友的工具面 | OAuth/凭据、每 workspace 作用域、输出上限、工具白名单 |

两条线的产物完全不同：**A 产出 `InboxItem`，B 产出 `Tool`**。A 类永远不能直接触发队友执行；B 类只受工具授予与审批约束。

#### A. 工作入口连接器（本项目的重点）

```
微信 / 钉钉 / 飞书 / 邮件
   │ ① 平台回调（webhook 或长连接）→ 验签 + 时间窗 + 幂等键
   ▼
connector 适配器（每平台一个，统一接口）
   │ ② 规范化：sender / channel / thread / text / attachments / mentions / replyTo
   ▼
inbox（单一队列；去重键 = platform + channel + thread + hash）
   │ ③ Lead 分诊：建任务 / 追加到现有任务 / 归档忽略 / 起草回复
   ▼
任务板 → 队友执行 → 产物（Artifact）
   │ ④ 出站：回复草稿 → 动作级审批（人点头）→ 平台 API 发回原线程
   ▼
原会话（同一 thread，不新开会话）
```

平台差异（按官方文档核对，2026-11-25）：**每个平台都有"只能发"和"能收能批"两套接入方式，别混用**。

| 平台 | 只能发（单向通知） | 能收能批（双向） | 官方限制 |
|---|---|---|---|
| 微信 | — | 官方 ClawBot 插件（iLink）：`getupdates` 长轮询收、`sendmessage` 发 | 插件能力声明**只支持单聊**（不能进群）；**但单聊能收消息，因此能接活** |
| 钉钉 | 群自定义机器人 webhook | **企业内部应用机器人 + Stream 长连接**：收消息、事件订阅、互动卡片回调 | 发消息 **200 次/分钟/会话**；卡片回调要 `callbackType=STREAM` |
| 飞书 | 群自定义机器人 webhook（能发卡片，但**按钮只能跳链接、不能回调，也不能收消息**） | **应用机器人 + 长连接**（SDK 建 WebSocket，服务器需能出网） | 自定义机器人 **100 次/分钟、5 次/秒**、body ≤ 20KB；旧版"消息卡片回传交互"**不支持长连接** |
| 邮件 | SMTP 发信 | IMAP（IDLE 推送）收信 | 无卡片；审批只能靠正文关键词或深链 |

**四条硬结论**：
1. **"群里 @ 机器人"只有应用机器人能做**（钉钉 Stream / 飞书长连接），自定义 webhook 机器人只能发、不能收。
2. **微信只能单聊**，所以"群里 @ 微信机器人"这个场景不存在；微信群里的活只能靠钉钉/飞书接。
3. **只要能收消息就能接活**：微信单聊、钉钉/飞书应用机器人、邮件——入站全部归一化后进 `inbox`，**一律先给 Lead**，来源不决定谁来处理。
4. **只有 Lead 对人说话**：队友永远不能直接给人发消息；任何对外回复都由 Lead 起草、经动作级审批后由连接器发出。

#### 微信 ClawBot 适配器（特殊，单独说明）

微信这一条不是"回调"，是**长轮询 + 24 小时会话**，实现要点：

1. **登录**：扫码（终端/UI 出二维码链接，手机微信打开确认）；**每次扫码 Bot ID 会变**，适配器要把它当成可变身份，不能写进任务或记忆当主键。官方插件声明**只支持单聊**，群里用不了。
2. **收**：`getupdates` 长轮询（服务端 hold 约 35s），天然符合"长连接优先"；断线后指数退避重连。
3. **发**：`sendmessage` 必须给全 `from_user_id / to_user_id / client_id / message_type / message_state / context_token / item_list` 与 `base_info.channel_version`；**`context_token` 只能用当前这条消息的**。缺字段时平台 HTTP 200 但**不投递**——所以发送后要做"我发出去了吗"的对账（下一轮 getupdates 里看不到回显就当失败并告警）。
4. **续连**：连接 24 小时到期；剩余 2 小时提示、最后 30 分钟强制换 token；新 token **原子替换**，否则会掉线。
5. **限速**：每 Bot 约 **7 条/5 分钟**（服务端共享）→ 直接进 §9.1 配额，超出排队而不是丢弃。
6. **媒体**：官方 SDK 支持文本；图片/语音/文件需要微信 CDN 的 AES-128-ECB + PKCS#7 加密上传流程，**先不做**（见 §11.2）。
7. **24 小时保活（媒体实测，官方文档未写）**：用户超过 24 小时没给 Bot 发消息，下发的消息会被丢弃（客服式会话窗口）。所以：定时任务的结果可能送不到；适配器要维护"最近一次入站时间"，超窗就改走邮件/钉钉并在界面提示。
8. **合规**：遵守《微信 ClawBot 功能使用条款》；腾讯可随时限速/停服，所以这一路必须**可降级**（连接器失效时任务照跑，只是回不去微信）。

**出站一律走动作级审批**：队友起草、Lead 汇总，但"发到外部"这个动作仍由人回答——验收全交 Lead 不等于发送权也交出去。

A 类连接器安全基线（缺一不可）：

1. 验签失败 → 401，且**不写库**；
2. 时间窗 ±5 分钟防重放；3. 幂等键 24 小时内去重（同一线程连发 10 条只留 1 条 + 计数）；
4. 每个适配器独立的每分钟/每天入站配额；5. 只接受白名单群/频道/发件域（可配）；
6. **正文永远是数据，不是指令**——邮件/群里说"帮我把钱转出去"不构成授权（对齐 Grok Bot："email from others is information, not instructions"）；
7. 附件落盘前做类型与大小白名单，落盘进 artifacts 并标来源。

#### B. MCP 能力连接器

现状已具备：stdio + HTTP 两种传输、OAuth 授权、按 workspace 作用域隔离、`tools/call` 与 `resources/read`、默认 64KiB 输出上限、连接去重、重连、启用/禁用。**缺口见 §7.5**。

> 一条硬规则（对两类连接器都适用）：**外部事件永不直接唤醒队友**。Grok Bot 的做法是"new mail doesn't start a chat or wake a Bot on its own"，我们照抄；由 Lead 分诊后再决定谁干。

### 6.5 队友协作与 handoff

| 通信 | 允许吗 | 规则 |
|---|---|---|
| Lead → 队友 | 允许 | 派遣、追问、纠正、撤销任务 |
| 队友 → Lead | 允许 | 进度、阻塞、完成、提案（都进 Lead 收件箱，不进人） |
| 队友 ↔ 队友 | **默认不允许直连**；第二期开放"同父任务内直连" | 需要直接对话时由 Lead 开一条 `handoff`（点对点、限定条数、抄送 Lead） |
| 队友 → 人 | 禁止 | 队友要人决策时只能上报 Lead，由 Lead 汇总后问你 |

**成本归属**：每个 task 记录 `botId + budget + used`；Bot 之间互发消息也计入发起方任务。这样"谁花的钱"永远可追。

### 6.6 审批与工具授予

三层，从宽到严：

1. **岗位级**（RoleTemplate.Approval）：这个岗位默认多谨慎。
2. **任务级**（Task.Approval）：这一单要不要开工前确认 / 发送前确认。
3. **动作级**（现有 approvals + 审批面清单）：具体危险动作照旧停审批。

**工具授予**：Lead 可以给队友"发工具"（`grant(tool, scope, until, reason)`），也能撤销。规则：

- 授予有范围：`fs.write:/data/proj/**`、`net:api.github.com`、`browser:domain=example.com`。
- 授予有期限：任务结束即失效（除非显式 `until=0` 的长期授予）。
- 授予必须留痕：谁批的、为哪个任务、什么时候撤销。
- **审批权不下放**：队友永远不能批准自己的动作，也不能批准别人的；审批只由人回答（第二期才有 Manager 角色）。

### 6.7 记忆写入与审核

```
回合结束
  ├─ 岗位记忆（agent scope）：队友自己写，立即生效，只影响自己
  ├─ 团队记忆（team scope）：写成"建议"，进审核队列
  │     └─ Lead 初审（是否符合团队约定） → 你终审（可选） → 生效
  └─ 项目记忆（project scope）：沿用现有 project 轨
```

- 记忆条目必须带**来源**（哪个任务、哪次对话）和**时间**，可审计、可回滚。
- 禁止写入：凭据、卡号、一次性验证码（这些进凭据库）。
- 冲突策略：同一 key 的新旧值并存显示，由审核人择一，不静默覆盖。

### 6.8 失败、中断与恢复

| 场景 | 行为 |
|---|---|
| 队友进程被杀 | 租约到期 → 任务回 `ready` + `expired` 事件；已产出证据保留 |
| Lead 进程崩溃 | 重启后从任务板/收件箱重建视图；`running` 的任务若租约过期按上一条处理 |
| 浏览器断连 | 任务转 `blocked(reason=browser_lost)`，自动重连一次；失败则等人 |
| 预算耗尽 | 转 `blocked(budget)`；Lead 决定追加还是收尾 |
| 审批超时 | 保持 `awaiting_approval`，UI 上计数；不自动放行 |
| 依赖任务失败 | 下游保持 `pending` 并标 `upstream_failed`；Lead 决定重开或取消整棵树 |
| 外部事件重复 | 去重键合并，不重复建任务 |
| 用户撤销 | Lead `cancel` 整棵子树；正在跑的队友收到 interrupt，产出转为"部分结果" |

### 6.9 通知分层（人只被三类事打断）

| 级别 | 内容 | 呈现 |
|---|---|---|
| 打断 | 审批卡、必须人来决策、任务完成/失败 | 会话内卡片 + 系统通知（现有 notifier） |
| 汇总 | 队友进度、事件分诊结果 | Lead 会话里的折叠块，不推送 |
| 静默 | 心跳、租约、重试 | 只在团队屏的任务卡里可见 |

### 6.10 UI 呈现（复用现有路由）

| 面 | 路由 | 需要改什么 |
|---|---|---|
| Lead 会话 | `/c/:id` | 完成卡增加"结果 / 证据 / 产物 / 下一步 / 存为技能" |
| 团队屏 | `/team` | 队员列表 + 任务板（含租约倒计时、证据、验收按钮） |
| 收件箱 | `/inbox` | 数据源从"5 类打断"扩为"打断 + 外部事件待分诊" |
| 产物库 | `/c/:id/files` 旁新增 Tab | Artifact 列表、过期标记、Pin |
| 岗位配置 | `/settings/bots`（新） | 模板 CRUD、工具授予、预算、审批策略 |

---

### 6.11 远程审批与通知路由（"电脑挂着，人在外面"）

目标闭环：**在微信/钉钉/飞书里发任务 → Lead 分诊派活 → 队友干到需要授权 → 审批推到你手机 → 你回一个短码 → 电脑把事做完 → 结果回到同一个会话**。

#### 6.11.1 用户绑定

- 每个连接器绑定**一个 owner 身份**（`connector_user_id` ↔ 本机唯一用户）；只有这个人的回复能构成审批。
- 群里其他人的"同意/可以/approve"一律当**数据**，不是授权；不认识的发件人直接进收件箱忽略。
- 一期单用户：**一个连接器只能绑一个 owner**，多绑要从 UI 显式解绑再绑。

#### 6.11.2 通知路由表

配置项 `notify.route`（设置页可视化，存 settings）：

| 类别 | 含义 | 默认渠道 | 可选项 |
|---|---|---|---|
| `approval` | 等审批（必须即时） | 你绑定的主渠道 + 站内 + 系统通知 | 微信 / 钉钉 / 飞书 / 邮件 / 站内 |
| `needs_decision` | Lead 需要你拍板 | 主渠道 | 同上 |
| `task_done` / `task_failed` | 任务完成/失败 | 主渠道（可关） | 同上 |
| `inbox_digest` | 待分诊事件汇总 | 主渠道，每天一次或攒够 5 条 | 同上 |
| `daily_summary` | 一天干了什么 | 邮件（可选） | 同上 |

字段：`category`、`channel`、`target`（连接器里的用户/群）、`quietHours`（免打扰）、`escalation`（无回应 N 分钟后换渠道）、`enabled`。

**主渠道 + 备用渠道（分开配）**：

| 方向 | 配置 | 语义 |
|---|---|---|
| 入站（接活） | `inbound.primary` + `inbound.fallback[]` | 你从哪个渠道发消息就算接活；主渠道不可用（微信 24h 窗口、掉线）时用备用。**所有入站都进 Lead，不分渠道。** |
| 出站（通知/回复） | `outbound.primary` + `outbound.fallback[]` | 先走主渠道；失败/超窗/限速按顺序降级，并显示「这条是用哪个渠道送到你的」。 |
| 审批 | `approval.channel` + 深链 | UI 按钮为准；IM 只负责把链接或卡片送到你手里。 |

#### 6.11.3 审批：两个等价入口——点按钮，或回一句「同意」

审批就是一句话的事：**发一段文字告诉你「要做什么、后果是什么」，你回「同意」或「不同意」**。界面上另有按钮，等价。

| 入口 | 你看到 | 你做什么 |
|---|---|---|
| 微信 / 钉钉 / 飞书（文字） | 一段话：要做什么 + 后果 + 「回复同意或不同意」 | 回「同意」/「不同意」 |
| 钉钉 / 飞书（应用机器人卡片） | 互动卡片三按钮 | 点按钮 |
| 桌面 Web / 手机 Web | `/inbox` 三按钮卡 | 点按钮 |

**文字回复的解析规则**（简单但不含糊）：

1. 该会话**只有一条待批**时，「同意 / 不同意」直接生效——这是绝大多数情况。
2. 有多条待批时，它先回「现在有 3 条：1 发报价单 2 删旧文件 3 发周报，回数字」。
3. 词表窄：允许＝同意/可以/发吧/行；拒绝＝不同意/不行/别发/拒绝；始终允许＝以后都同意。其余一律回「没看懂，回 1 允许 / 2 拒绝」。
4. 只认你本人：引用/转发的「同意」不算；群里别人的「同意」不算。
5. 30 秒内可回「改主意」撤回（动作还没执行时）。

两条入口都写进同一条 `approval.event` 审计：谁、在哪个入口、什么时候批的。

#### 6.11.4 消息归属：所有入站都归 Lead，只有 Lead 对人说话

| 规则 | 说明 |
|---|---|
| 入站统一 | 微信单聊、钉钉/飞书、邮件、webhook、定时、队友提案——全部归一化进 `inbox`，**先给 Lead** |
| 只有 Lead 对人说话 | 队友不直接接触渠道；对外回复由 Lead 起草 → 审批 → 连接器发出 |
| 主/备渠道 | 入站、出站各一套主渠道 + 备用；切换不影响历史消息归属 |

#### 6.11.5 手机端：就是电脑端那一套，内网直连

不做"简化版移动页"，也不讨论隧道：

- 手机浏览器打开 **同一个 Web**（内网 IP + 端口），看到的是**全功能模块**：现在、等我、在跑、会话、任务详情、设置，一个不少。
- 布局按视口自适应（窄屏单列、底部标签栏、按钮加大到拇指可达）。
- 网络前提：**默认同一个内网**，所以只需要把监听地址从 `127.0.0.1` 改成局域网地址（并保留访问口令）。这是对 TRANSPORT N1 的明确修改，记在决策里。
- 微信里的通知只需要一句话提醒"有东西等你"，你回「同意」即可，不必点链接。

#### 6.11.6 单机挂机的前提

- 系统电源策略：禁止自动休眠（或至少不休眠磁盘/网络）；笔记本合盖行为要显式提示。
- 断网恢复：连接器自动重连（微信换 token、钉钉/飞书长连接退避重连）。
- 重启恢复：审批单、通知队列、收件箱全部持久化；重启后未决审批仍在。
- 配额提示：微信限速低，审批高峰要排队；UI 显示「还有 N 条待发」。
- 微信保活：超过 20 小时无入站就预警，超过 24 小时自动把出站改到备用渠道。

## 7. 产品改造清单

### 7.1 契约层（`internal/contracts`）

| 文件 | 改动 |
|---|---|
| `bot.go`（新） | `RoleTemplate`、`BotInstance`、`Grant`、`BotService`、`KeyBots` |
| `inbox.go`（新） | `InboxItem`、`InboxService`（List/Triage/Archive）、`KeyInbox` |
| `taskboard` 相关 | `Task` 增字段：Priority/Kind/Approval/Lease/Heartbeat/Budget/Evidence/ReviewBy/ParentID/SourceRef；新方法 `Claim`（返回租约）、`Heartbeat`、`Release`、`Submit`（带证据）、`Accept/Reject`、`OpenPool/ClosePool`、`Propose` |
| `automation.go` | `Routine.OwnerBotID`、`Trigger`（cron/webhook/event）、`Action`（task/inbox/chat）、历史保留 20 条 |
| `memoryv2.go` | 增加 `scope: agent \| team` 与"建议队列"状态 |
| `artifact.go`（新） | `Artifact`、`ArtifactService`、`KeyArtifacts` |
| `browser.go`（新） | `BrowserSession`（profile、contextId、当前 URL、截图引用）、`KeyBrowser` |

### 7.2 网关层（`internal/gateway`）

新增/扩展的传输方法（**先改 TRANSPORT §5.1 状态列，再加 dispatch 与 `TestTransport*`**，这是硬规则）：

| 方法 | 入参 | 出参 | 实现状态 |
|---|---|---|---|
| `bots.snapshot` | — | `{templates:[RoleTemplate], bots:[BotInstance]}` | 待做 |
| `bots.upsert` / `bots.remove` | 模板/实例 | `{ok}` | 待做 |
| `bots.grant` / `bots.revoke` | `{botId, tool, scope, until}` | `{ok}` | 待做 |
| `inbox.snapshot` | `{status?}` | `{items:[InboxItem]}` | 待做 |
| `inbox.triage` | `{ids[], action, taskIds[]}` | `{ok, tasks}` | 待做 |
| `tasks.claim` | `{id}` | `{task, leaseUntil}` | 扩展现有 |
| `tasks.heartbeat` | `{id}` | `{leaseUntil}` | 待做 |
| `tasks.submit` | `{id, evidence[]}` | `{task}` | 待做 |
| `tasks.review` | `{id, verdict, reason?}` | `{task}` | 待做 |
| `tasks.pool` | `{id, open}` | `{ok}` | 待做 |
| `routine.snapshot/upsert/run` | 见 §5 | `{routines}` | 扩展现有 automation |
| `artifacts.list` | `{sessionId?}` | `{items}` | 待做 |
| `browser.snapshot` | `{sessionId}` | `{tabs, url, shot}` | 待做 |
| `notify.route.snapshot` / `notify.route.upsert` | `{routes[]}` | `{routes[]}` | 待做 |
| `connector.bind` / `connector.unbind` | `{connector, userId}` | `{ok, bound}` | 待做 |
| `approval.action`（内部） | `{approvalId, action, token, channel, messageId}` | `{ok}` | 待做（由连接器适配器调用，不暴露给浏览器） |

### 7.3 模块层（`internal/modules`）

| 模块 | 改动 |
|---|---|
| `agent` | 新增 Lead 分诊器（读收件箱 → 建任务 → 派遣）、派遣器（选 Bot、发预算、发工具）、验收器；系统提示词加"你只能通过任务板派活"的纪律 |
| `toolkit` | 任务板：租约/心跳/证据/验收/池；新增 `browser` 工具组（CDP）；新增 `artifacts` 工具组 |
| 新模块 `botstore` | 模板/实例/授予的持久化（目录：`<data>/bots/`） |
| 新模块 `inbox` | 收件箱持久化 + 去重索引 + 配额 |
| 新模块 `connectors` | 三类接口：`Inbound`（验签/长轮询→规范化→收件箱）、`Outbound`（审批后回复原线程）、`Registry`（实例、凭据引用、状态页）；首批：钉钉、飞书、邮件、**微信 ClawBot（iLink，含 24h 续连与发送对账）** |
| 新模块 `events` | 平台无关的事件框架：幂等键、配额、重放窗口、事件总线（不绑定任何具体平台） |
| `automation` | routine 归属 Bot、触发器抽象、历史 20 条 |
| `notifier`（扩展） | 通知路由表、按类别选渠道、免打扰、升级重试 |
| `memorystore` | agent/team 作用域 + 建议队列 |
| `approvals` | 任务级审批策略、审批归属；**短码/一次性 token、过期、远程动作入口、审计事件** |
| `skills` | "从任务沉淀"生成六段式草稿 |

### 7.4 Web 层（`web/`）

- `/settings/bots`：岗位模板与 Bot 实例；工具授予用 antd `Table` + `Modal`。
- `/settings/notifications`：新增**路由表**（类别 × 渠道 × 绑定用户 × 免打扰 × 升级），以及「远程允许始终允许」开关。
- `/settings/connectors`（新）：每个连接器的状态卡（在线/断线/剩余连接时间/限速余量）、启停、入站白名单、**出站审批策略**；微信额外显示二维码与「重新连接」。
- `/team`：任务卡增加租约倒计时、证据列表、accept/reject 按钮（**禁止把 `running` 画成 `active`**，沿用现有状态语义规则）。
- `/inbox`：新增"待分诊"分组；事件组折叠。
- 完成卡：结果/证据/产物/下一步/存为技能。
- 所有颜色字号走 `tokens.css`，不手写 fetch/WebSocket。

### 7.5 MCP 能力补全清单（G14）

现状盘点（`internal/modules/toolkit/mcp_*.go`）：stdio/HTTP 传输、OAuth、按 workspace 隔离、tools + resources、64KiB 输出上限、重连与去重、启停。还不够全面的地方，按优先级：

| # | 缺口 | 影响 | 建议 |
|---|---|---|---|
| 1 | **工具注解未映射审批**（readOnly / destructive / idempotent） | 只能按工具名猜危险度 | 注解 → 审批策略与沙箱档位；destructive 一律审批 |
| 2 | **每服务器工具白名单**（现在粒度是"整台服务器开/关"） | 一个 server 里混着读写工具时只能全给 | 服务器 + 工具两级白名单 |
| 3 | **prompts/list / get** 未接 | 只能当工具用，无法用对方预设的工作流 | 接上并映射成技能候选 |
| 4 | **resources 作为上下文附件** | 资源只能被主动读，不能拖进会话 | 资源 → `@` 引用 → 上下文注入 |
| 5 | **分页（cursor）** | 大 catalog 只拿第一页 | 统一翻页封装 |
| 6 | **超时/重试/退避** | 慢 server 拖住整轮 | 每 server 超时 + 有限重试 |
| 7 | **进度与取消**（notifications/progress、$/cancel） | 长工具无法中途看进度或取消 | 映射到任务卡与 interrupt |
| 8 | **结构化输出**（outputSchema） | 结果只能当文本渲染 | 输出 schema → 工具卡字段 |
| 9 | **sampling / elicitation** | 服务端不能反向提问或借模型 | 明确不支持并给出诚实错误（不要假装） |
| 10 | **健康度与用量** | 不知道哪个 server 慢/失败多 | 每 server 成功率、P95、错误分类进 insights |
| 11 | **一键安装/目录** | 手填 command/URL 门槛高 | 内置常用 MCP 目录（可离线内置 JSON） |

已确认**全量补全**：1/2/6 是"不补就会出安全问题或卡死"的，进批次 A；3/4/5/8 进批次 B；7/9/10/11 进批次 C。

---

## 8. 里程碑

> 已确认：**M0（任务闭环）与 M3（CDP 浏览器）并行推进**。两线的交点是 `Artifact`（截图/下载产物）——浏览器产出的证据要能被任务板的 `evidence[]` 直接引用，所以 M0 先把 `Artifact` 的契约定好，M3 只做生产者。

| 里程碑 | 范围 | 验收（可执行） | 明确不做 |
|---|---|---|---|
| **M0 地基**（2 周） | G1 部分（Bot 实体字段）+ G3/G4（租约/证据/验收）+ G5 最小收件箱（webhook 一种来源）+ 协议与 UI 最小闭环 | ① 你发一个任务，Lead 拆 2 个任务派给两个队友；② 杀掉一个队友进程，10 分钟后任务回到 ready 且历史里能看到 `expired`；③ 无证据的完成被拒；④ 断网重连后视图一致 | 不做记忆共享、不做浏览器、不做外部连接器 |
| **M1 记忆** | G7（agent/team 作用域 + 建议队列 + 审核 UI） | 队友写的规则只影响自己；团队建议经你批准后另一队友可见 | 不做自动遗忘策略 |
| **M2 触发** | G6（routine 绑 Bot）+ 连接器框架（`Inbound`/`Outbound`/`Registry` + 收件箱 + 幂等/验签），用 webhook 做参考实现 | 一组 webhook 事件在 1 分钟内被聚合成 1 个任务；同一幂等键发 10 次只落 1 条 | 不接任何真实平台 |
| **M2.5 MCP 补全（全量）** | G14 的全部 11 项，分三批：**批次 A** 注解→审批、两级白名单、超时重试；**批次 B** prompts、resources 进上下文、分页、结构化输出；**批次 C** 进度取消、健康度与用量、目录 | destructive 注解的工具一定停审批；单 server 超时不拖住整轮；prompts 能变成技能候选 | 不做 sampling/elicitation（明确报不支持，不假装） |
| **M3 浏览器** | G10（CDP：启动/接管/profile 隔离/截图证据） | 两个 Bot 各自登录不同站点账号互不影响；截图进任务证据 | 不做桌面操作 |
| **M4 连接器** | **钉钉 + 飞书**（长连接优先）→ **邮件** → **微信 ClawBot（iLink）**；出站回复 + 凭据库 G9 | 钉钉/飞书群里 @机器人 的消息 1 分钟内进收件箱；微信扫码后能收发文本且 24h 续连不断线；发送失败可检测；凭据不出现在任何日志/上下文 | 不做微信媒体消息、不做个人号第三方 hook |
| **M5 浏览器收尾** | CDP 接管、多 context、下载拦截、截图证据链 | 见 M3 验收 + 人工接手不丢状态 | 不做云电脑 |
| **M6 共享（第二期）** | Team Bot、Owner/Manager/Collaborator、共享记忆审核人、预算归属 | 两个"用户"共用同一个 Bot 且记忆互不可见 | 不做多租户托管 |

---

## 9. 单机工程约束

### 9.1 默认限额（写进配置，不要硬编码）

| 项 | 默认 | 理由 |
|---|---|---|
| 并发 run | 2（可调 1–4） | 单机模型并发与 CPU 都有限 |
| 每 Bot 并发任务 | 1 | 避免同一岗位自我竞争 |
| 开放池同时开张 | 3 | 防止全队涌向一批活 |
| 租约 TTL / 心跳 | 10 分钟 / 60 秒 | 长任务靠心跳续 |
| 提案 TTL | 2 分钟 | 防"抢了不做" |
| 任务板上限 | 256（沿用） | 超限明确报错，不复用 id |
| 收件箱 | 每来源 60 条/分钟、2000 条/天，超出折叠摘要 | 防止 webhook 风暴 |
| 每会话 inbox 呈现 | 200 条，超出摘要 | UI 不爆 |
| routine | 每 Bot 50 条、全局 300 次/时、单条 200 次/时、历史 20 条 | 直接对齐 Grok Bot 的阀门 |
| 每 Bot 私有目录 | 512MB 软限 | 超限提示并列出大文件 |
| 产物 | 7 天 GC（可按任务标记保留） | 单机磁盘有限 |
| 浏览器 context | 最多 4 个并发 | 本机内存 |
| 连接器入站 | 每适配器 60 条/分钟、2000 条/天；正文 64KB 截断；附件 25MB | 防平台风暴与超大附件 |
| 连接器出站 | 每适配器 30 条/分钟，且全部经审批 | 防刷屏与误发 |
| 邮件轮询 | 1 分钟/次（IDLE 优先） | 单机 CPU 与连接数 |
| 微信 ClawBot | 发送 ≤ 7 条/5 分钟（平台硬限），超出排队；连接 24h 到期前 2h 预警 | 平台限速，不可协商 |
| 审批短码 | 30 分钟过期；一次性 token；同一审批单最多 3 次尝试 | 防重放与暴力猜码 |
| 审批升级 | 15 分钟换渠道、30 分钟标"卡住" | 移动办公的响应预期 |

### 9.2 隔离（单机的正确做法）

- **共享检出**：仓库根，多会话共享，靠 write scope 协调（不是锁）。
- **run 的根**：session 绑定 root（已有 `WithWorkspaceRoot`），队友继承父级 root，禁止跨 root。
- **每 Bot 私有目录**：`<data>/agents/<botId>/`：草稿、日志、下载、浏览器 profile。**必须做**，否则队友之间互相污染。
- **凭据**：绝不共享。Grok Bot 敢共享是因为用户之间硬件隔离；我们共用一个文件系统，凭据必须走加密库 + 一次性注入。

### 9.3 本机浏览器（CDP）方案

- 启动：受管进程拉起本机 Chrome/Edge，`--remote-debugging-port=<随机>`，`--user-data-dir=<data>/browser/<botId>`。
- 隔离：每个 Bot 一个 `BrowserContext`（CDP `Target.createBrowserContext`），cookie/storage 互不共享；同一 Bot 多标签页共享登录态（这是特性）。
- 证据：关键步骤截图 → artifacts；页面 URL/标题 → 任务证据。
- 审批：跨域导航、上传文件、提交表单、下载可执行文件 → 停审批（沿用审批面清单的语义）。
- 断连：一次自动重连，失败转 `blocked(browser_lost)`。
- headless 可选但默认**可见**（你说了用本机浏览器；可见窗口也方便你接管）。

---

## 10. 风险与反模式

| 风险 | 表现 | 对策 |
|---|---|---|
| 任务风暴 | 队友互相发现"还有活"，无限拆解 | 只允许 Lead 建任务；子任务深度上限（默认 2 层）；每任务子任务上限（默认 5） |
| 抢了不做 | 自领后消失 | 提案 TTL + 租约回收 + `abandoned` 记录 |
| 重复劳动 | 两个 Bot 做同一件事 | 去重键 + write scope 重叠警告 + 池容量 |
| 成本失控 | 队友互发消息、宽触发 | 预算、互发计费、触发窄匹配、routine 阀门 |
| 越权 | 队友顺手删库/发邮件 | 工具授予 + 审批面 + 沙箱 + 凭据不落上下文 |
| 记忆污染 | 一条错规则传全队 | 团队记忆必须审核；带来源可回滚 |
| 单机资源打爆 | 4 个浏览器 + 4 个 run | 并发上限 + 每 Bot 目录配额 + 产物 GC |
| UI 谎言 | 把 `running` 画成 `active`、把过期租约画成在跑 | 沿用现有"状态语义不可降级"规则；租约倒计时必须真实 |

---

## 11. 决策记录

> 状态：**全部锁定（2026-11-25）**。后续改动不走"随口改"，走本文件 §11.3 的变更流程。

### 11.1 已拍板

| # | 决定 | 结论 |
|---|---|---|
| 1 | 产品形态 | 单机个人版优先，团队共享第二期 |
| 2 | 执行面 | 全功能 agent，不做桌面/屏幕；浏览器用本机 Chrome/Edge + CDP |
| 3 | 浏览器可见性 | **默认可见窗口**，headless 只是设置项 |
| 4 | 连接器 | **两类分开**：A 工作入口（微信/钉钉/飞书/邮件），B 能力连接器（MCP） |
| 5 | 微信接入 | **官方 ClawBot 插件（iLink 协议，个人号）**，不做第三方 hook |
| 5b | 微信能力边界 | **纯文本、无卡片无按钮，但双向可回复**：审批用**对话式**（回「同意 / 不同意」，或回短码）；卡片按钮只在钉钉/飞书 |
| 5c | 平台接入方式 | **通知用"自定义机器人/webhook"，接活与审批必须用"应用机器人"**（钉钉 Stream、飞书长连接）；微信只有一套（ClawBot），且**仅单聊** |
| 5d | 消息归属 | **所有渠道的入站消息一律先给 Lead**；只有 Lead 能对人说话，队友不直接接触会话渠道 |
| 5e | 审批入口 | **两个等价入口**：界面按钮 / 聊天里回「同意·不同意」；文字回复在单条待批时直接生效，多条先回编号 |
| 5f | 渠道选择 | 入站、出站各配**主渠道 + 备用渠道**，可随时切换 |
| 5g | 手机形态 | **内网直连的同一套全功能 Web**（响应式），不做简化移动页、不引入隧道；监听改为局域网地址 |
| 6 | 微信实现方式 | **直接实现 iLink 客户端**（腾讯官方包 `@tencent-weixin/openclaw-weixin`），不桥接已部署的 OpenClaw |
| 7 | 微信媒体 | **一期只做文本**；图片/语音/文件（CDN 加密上传）不做 |
| 8 | 钉钉/飞书 | **长连接优先**，HTTP 回调作为备选 |
| 9 | QQ | 同构适配器，**排在钉钉/飞书/邮件之后**，M4 之后再说 |
| 10 | 出站审批 | **按连接器配默认策略**；第一个"草稿自动发"给**钉钉私聊**，群消息与邮件保持每条必审 |
| 11 | 通知路由 | 每类别一个默认渠道；`approval` 默认 = **绑定主渠道 + 站内 + 系统通知**三路 |
| 12 | 审批升级 | 15 分钟无回应换渠道；30 分钟标"卡住"并在汇总置顶 |
| 13 | 远程"始终允许" | **默认关闭**；开启后 24 小时只能远程下发一次 |
| 14 | 记忆 | 岗位 + 团队共享 + 审核；**规则类需你确认，事实类 Lead 直接生效可回滚** |
| 15 | 预算 | token 为主、时长为辅；超限 `blocked` 交 Lead 决策 |
| 16 | 认领 | Lead 指派为主；开放池默认关闭，Lead 显式 open |
| 17 | 验收 | 全部由 Lead 自审；动作级审批（写盘/外发/花钱/删除）仍归人 |
| 18 | MCP | **全量补全**，批次 A（注解→审批、两级白名单、超时重试）→ B（prompts、resources 进上下文、分页、结构化输出）→ C（进度取消、健康度、目录） |
| 19 | 里程碑 | M0（任务闭环）与 M3（CDP 浏览器）并行 |
| 20 | 移动办公 | **通知路由表 + 绑定用户**：审批/完成/决策推到手机 IM，短码或按钮回复，电脑挂着即可 |

### 11.2 默认参数（不需要你逐条确认，先按这个跑，可调）

| 项 | 值 |
|---|---|
| 并发 run / 每 Bot 并发 | 2 / 1 |
| 开放池同时开张 | 3 |
| 租约 TTL / 心跳 | 10 分钟 / 60 秒 |
| 提案 TTL | 2 分钟 |
| 审批短码有效期 / 尝试次数 | 30 分钟 / 3 次 |
| 微信发送 | ≤ 7 条/5 分钟，超出排队 |
| 任务板 / routine | 256 / 每 Bot 50 条、300 次/时全局 |
| 收件箱 | 每来源 60 条/分钟、2000 条/天，超出折叠摘要 |
| 产物 GC | 7 天（可标保留） |
| 浏览器 context | 4 个并发 |

### 11.3 变更流程

1. 任何与 §11.1 冲突的想法，先在本文件加一条"变更提案"（编号、理由、影响面、回滚办法）。
2. 你确认后改表，再改代码；**不允许代码先行、文档后补**。
3. 涉及协议（`bots.* / inbox.* / notify.route.* / connector.*`）的变更，按 AGENTS.md 同时更新 TRANSPORT §5.1 状态列与 `TestTransport*`。

> 底线不变：**越权的是动作，不是判断**。Lead 可以自己判断好坏（验收全交它），但任何改变外部世界的动作仍要人点头——包括在手机上点头。

---

## 12. 附录：M0 的可执行验收脚本清单

1. 建两个岗位模板（研究 / 数据），各建一个 Bot 实例。
2. 你发"整理竞品定价表" → Lead 生成 2 个任务（抓取、出表），依赖关系正确。
3. 研究 Bot 认领 → 租约 10 分钟 → 60 秒心跳 → 完成任务带证据（URL 列表 + 截图）。
4. 数据 Bot 在依赖未完成时 claim 被拒（`not_ready`）。
5. 手动 `kill` 研究 Bot 进程 → 10 分钟后任务回 `ready`，历史有 `expired`。
6. 数据 Bot 完成但**不带证据** → 被拒。
7. `ReviewBy=human` 的任务在你点通过前保持 `review`。
8. 重启整个进程 → 任务板、收件箱、Bot 列表、租约状态全部一致。
9. `/team` 上租约倒计时与真实 `leaseUntil` 一致（不美化）。
10. 一条 webhook 在同一线程连发 10 次 → 收件箱只有 1 条（计数 10）。
11. 一个未验签的入站请求 → 401 且收件箱条数不变（不写库）。
12. 一条 MCP 工具带 `destructive` 注解 → 调用前必停审批；同一 server 的只读工具不停。
13. 超预算任务停在 `blocked`，UI 上能看到已用 token 与上限，Lead 追加预算后才继续。