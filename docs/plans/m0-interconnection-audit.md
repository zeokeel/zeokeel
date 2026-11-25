# M0 模块互联审计（第一轮，2026-11-25）

> 目的：把"模块之间怎么连起来"当成一等公民审计。功能测试证明"这个模块自己能跑"，本文件证明"它们接在一起还能跑"。
> 关联：[整体规划](grokbot-open-source-plan.md)、[交互设计](grokbot-interaction-design.md)。

---

## 1. 商品装配链路（现状）

```
internal/assembly/registry.go          compile-time 模块清单（显式注册，无 init 注册）
        │  mustRegister(registry, inbox.Module())
        ▼
internal/assembly/assemblies/headless.json   产品装配文档（id + module + config）
        │  Entries(ProductHeadless)
        ▼
assembly.LoadWithBuildInfo(root, ProductHeadless)   内核按顺序实例化
        ▼
cordis root: 每个模块的 Provide 落到服务键（contracts.KeyX）
        ▼
消费方 ctx.Require(contracts.KeyX)      gateway / agent / toolkit
```

serve 产品**复用 headless 的 entries**（`ServeEntries()` 只覆盖 approvals 的 mode 与 hosttools 的 interactive）。所以：

> **一条模块必须同时出现在 registry.go 和 headless.json，否则它在本二进制里永远不加载。**

## 2. 本次新增的连线

| 连线 | 提供方 | 消费方 | 断言（测试） |
|---|---|---|---|
| `contracts.KeyInbox` | `internal/modules/inbox`（新） | triage、未来的 gateway `inbox.*` | `internal/modules/inbox` 行为测试 16 条；`internal/assembly` 里通过 `root.Get(KeyInbox)` 真实调用 Add/去重/Triage/Get |
| `contracts.KeyTriage` | `internal/modules/triage`（新） | 未来的 Lead 派遣、gateway | `internal/modules/triage` 7 条（幂等、看板失败不脏写、无 assignee 跳过 Assign） |
| **三模块链路** inbox → triage → task board | inbox + triage + toolkit | — | 装配测试里：Add → Apply(task) → board.List 里能找到同一 id，且 inbox 行变 task |
| `contracts.KeyTaskLease` | `internal/modules/tasklease`（新） | 未来的派遣器、gateway | 10 条（抢占/心跳/证据必填/验收/打回/过期/释放/防双开） |
| **四模块链路** inbox → triage → board → lease | 上述四个模块 | — | 装配测试里：Apply 建任务 → Claim → Submit → Accept，board 最终 completed |
| `contracts.KeyApprovalAction` | approvals 模块（与 KeyApprovals 同源） | 桌面按钮 / 卡片回调 / 聊天回复 | 13 条（token 校验/过期/单次使用/绑单/索引回复/空队列） |
| `contracts.KeyNotifyRoute` | notifyroute（新，无依赖） | 渠道适配器、gateway | 8 条（默认覆盖/校验/顺序/不健康跳过/inapp 兜底/静音只压非审批/跨午夜/停用） |
| `contracts.KeyWeChat` | wechat（新，依赖 inbox） | 轮询循环（下一步挂载） | 8 条（入站归一/重投合并/全字段发送/无 token/窗口关闭/空文本/平台失败/401） |
| `contracts.KeyLead` | lead（依赖 inbox + triage + dataDir） | `lead.run`、自动化 | 5 条循环 + 4 条规则持久化 |
| `contracts.KeyDingTalk` | dingtalk（依赖 inbox） | 入站路由（待挂） | 17 条（验签/过期/非文本/合并/发送/限速型错误） |
| `contracts.KeyFeishu` | feishu（依赖 inbox） | 入站路由（待挂） | 24 条（challenge/验签/旧签名/v1 回调/合并/卡片动作/发送/token 铸造） |
| 租约持久化 | tasklease（+dataDir） | 重启恢复 | 7 条（存活/过期回收/损坏/0600/评审状态） |
| `contracts.KeyCredentials` | creds（依赖 dataDir） | `credentials.list/set/delete`、渠道适配器 | 17 条 + 19 校验子测试；网关 3 方法 + mock 覆盖 |
| `contracts.KeyEmail` | email（依赖 inbox） | 邮件渠道 | 30 条（SMTP 报文/535/IMAP 归一/Message-ID 去重/QP+base64/循环取消） |
| `contracts.KeyBrowser` | browser（新，无 requires） | 未来工具面 / 任务证据 | 46 测试（含真 ws:// 传输：握手/分片/乱序回复/事件丢弃计数/Close 幂等） |
| `contracts.KeyHeartbeat` | heartbeat（新，可选 lead/wechat/email） | `heartbeat.status/tick`、运行状态页 | 7 测试（tick/单渠道失败不中断/lead 失败记录/定时停止/禁用不动/状态） |
| `contracts.KeyTeamNotes` | teamnotes（依赖 memorystore 之后） | 团队共享记忆 + 复核 | 24 测试（真 provider 约束、复核幂等、拒绝在摘要可见） |
| 渠道报文夹具 | dingtalk/feishu/wechat testdata | 真机前最后一道闸 | 钉钉 11 + 飞书 8 + 微信（本轮）条夹具，按官方形状回放 |
| `heartbeat.configure` | gateway | 运行状态页可调 | 契约 86 方法；最小间隔 5s |
| Web 运行状态页 | `web/src/features/settings/health/**` | `/settings/health`（R27b） | 9 测试（调用顺序 read<tick<read、失败不回读、断开空态） |
| 验收即记忆 | gateway `taskReview` → teamnotes | 任务通过时写共享笔记 | 3 测试（通过写/打回不写/记忆失败不影响验收） |
| 微信扫码登录 | `wechat` + `wechat.login.start/state` | 设置页扫码接入 | 12 模块测试 + 网关 2 方法（expired 是答案不是错误） |
| Web 共享记忆页 | `web/src/features/settings/memory/**` | `/settings/memory`（R27c） | 18 测试（列表/复核态/确认驳回/失败保留行/窄屏） |
| 模型计划器装配 | `lead.PlannerFromSettings`（读 `chat.baseURL/apiKey/model`） | `lead.RunOnce` | 4 条（完整→模型/缺项→规则） |
| `heartbeat.configure` | gateway + 运行状态页控件 | 运行中改节奏/开关 | 3 条模块测试 + 5 条 UI 测试（开关/节奏/失败不翻转/在飞禁用） |
| `contracts.KeyReplyRelay` | replyrelay（inbox + approval action） | 聊天里回「同意」 | 5 条（归档/非回复不动/非渠道不解析/无待办不报错/同一到达只解析一次） |
| 渠道报文夹具（微信） | wechat testdata + fixtures | 真机前最后一道闸 | 发现 200+ret_code 当成功、data 信封静默丢弃，均已修 |
| 渠道报文夹具（邮件） | email testdata + fixtures | 真机前最后一道闸 | 发现 multipart 当原文入库、HTML-only 静默、折叠主题留 CRLF；单封坏信曾阻塞整个邮箱，已改为跳过+上报 |
| Web 渠道设置 | `web/src/features/settings/channels/**`、`web/src/api/channels.ts` | `/settings/channels`（R27a） | 12 测试；密钥不回显有断言 |
| `planner`（library） | planner（仅依赖 contracts） | 模型计划器 | 14 测试 / 21 断言（线格式/错误分层/预算先于网络/密钥永不入错误） |
| 模型计划器接入 | `lead.ModelPlanner` + `lib/leadplan` | `lead.RunOnce` | 4 条（映射 task/skip/无模型/驱动整轮） |
| 入站路由（钉钉/飞书） | gateway `inbound.go` | 平台回调 | 6 条（202/签名 401/challenge 200/204/404） |
| Web「待办收件箱」 | `web/src/features/inbox/**`、`web/src/api/inbox.ts` | `/inbox/incoming` | web 全量 477 tests 绿；旧 `/inbox` 有回归测试 |
| Web「任务详情」 | `web/src/features/task/**`、`web/src/api/task.ts` | `/t/:taskId`（R3b，无导航） | 22 测试（五段顺序/认领/证据门槛/通过打回/重试） |
| `tasks.lease` | gateway（独立 `leaseReadParams`，只读 id） | 任务详情首屏证据 | 方法表 + 契约 + mock 同步（74 方法）；屏幕首屏读证据、读失败只降级 §3 |
| `leadplan`（模型面） | leadplan（新，仅依赖 contracts） | 未来模型计划器 | 17 测试 / 45 断言（提示注入隔离、解析表） |
| `contracts.KeyWebhook` | webhook（依赖 inbox + dataDir） | `POST /api/inbound/{source}`、`webhook.sources/source.set/remove` | 10 条收件测试 + 7 条 HTTP 测试 + 6 条 source 管理测试（排序/校验/删除/重启恢复/轮换失效/0600） |
| **网关 → 服务键**（11 个方法） | inbox / triage / tasklease / approvalaction | web 客户端、Lead 分诊回合 | 一致性测试（TRANSPORT §5.1 ↔ dispatch ↔ contract.json ↔ ws-contract.ts）+ 载荷映射测试 7 条 |
| 注册 ↔ 装配一致 | registry | headless.json | `TestEveryRegisteredModuleIsLoaded`（新） |

inbox 的模块定位：**它是入口，不依赖任何人**（`Requires` 为空），所以它必须能先于 Lead/任务板启动。这条由 `TestDescriptorMetadata` 钉住——如果将来有人给它加 `Require(KeyTeam)`，测试会红。

## 3. 审计发现（真实，不是假设）

| # | 发现 | 性质 | 处理 |
|---|---|---|---|
| F1 | 加一个模块后，`assembly_test.go` 里 **3 处硬编码断言**同时红：headless 实例列表顺序、实例数量、hosttools 的固定下标 `instances[9]` | 顺序耦合：模块一插队，无关断言就连锁失败，容易让人误以为功能坏了 | 已按新顺序修正；**建议**后续把"按下标取实例"改成"按 id 查找"，本次未改（避免扩大改动面） |
| F2 | `TestRegistryIsExplicit` 的模块计数写死 `18`（注释还写着 20，早已陈旧） | 陈旧断言：计数一改就得手改，注释还误导 | 已改为 19 并修正注释；**建议**改成"至少包含这些模块"的白名单断言 |
| F3 | `zeokeel/modelcatalog` 注册了但不在任何装配文档里 | 我一开始以为是新发现并加了一个"注册/装配一致性"守卫，**结果仓库早就有 `TestEveryRegisteredModuleIsComposedIntoAProduct`**——重复守卫已删除；那个守卫里已写明 modelcatalog 与 host/group 是**有意不装配**并附了原因 | 保留原守卫，删除重复；本文件记下这条教训：先读测试再写测试 |
| F4 | `zeokeel/host/group`（容器模块）也不在 headless entries | 合法：它只在嵌套 group 时被引用，headless 没有 group | 带原因列入例外，不失败 |
| F5 | ~~inbox 是内存实现，重启丢~~ | ✅ 已修：`Open(dir)` 载入 `<data>/inbox/inbox.json`，每次变更原子写（tmp+rename），失败回滚内存；损坏文件**移到一边**并告警，不静默清空 | 持久化测试 6 条（重启恢复/重启后仍去重/状态恢复/损坏报错/无残留 tmp） |
| F6 | 第二次加模块又触发同一批断言（顺序列表、实例数、`instances[10]`） | 证明 F1/F2 不是偶发 | **已修根因**：改为「存在性」断言 + 按 id 查实例；计数断言删除，换成"必须包含这些模块" |
| F8 | `TestApprovalsSnapshotDeltaReplay` 在全套并发下偶发 `i/o timeout` | 测试脚手架的首帧读超时只有 5s，整包并发时不够 | ✅ 已修：`wstest` 读超时 5s → 15s（测试夹具，不是产品超时），`-count=3` 连过、全量重跑绿 |
| F9 | 全量测试时 `internal/modules/planner` 卡满 600s 超时 | 取消测试的假处理器只等测试放行、不等请求断开，`httptest.Server.Close` 被永久阻塞 | ✅ 已修：处理器 `select` 加上 `r.Context().Done()`，`-count=3` 通过 |
| F10 | 钉钉真实回调一律 `bad signature` | 官方头是 `timestamp`(毫秒) + `sign`，适配器只认 `X-DingTalk-*` 且按秒解析 | ✅ 平台报文夹具撞出并修复；别名保留，单位按量级判断，并有"毫秒头配秒签名必须失败"的反向测试 |
| F11 | 飞书加密推送被静默 200 | 签名能过、既不认 challenge 也不认事件，于是返回"成功" | ✅ 改为 `ErrFeishuBadPayload`（先验签再判形态） |
| F7 | **装配链路测出一个真 bug**：`Claim` 对已经指派/进行中的任务再次写 board，被 rig 拒绝 `team task update has no changes`，整次认领失败 | 单模块测试用的是 fake board，永远发现不了；只有真实装配链路能测出来 | 已修：先读当前任务，只写变化过的字段（状态不同才 SetStatus，owner 不同才 Assign） |

## 4. 尚未接的线（明确空白，不许假装完成）

| 缺口 | 说明 | 下一步 |
|---|---|---|
| ~~inbox → Lead 分诊器~~ | ✅ 已接：`triage.Apply` 把收件箱项变成任务板任务（先建任务再标记，失败不脏写；重复分诊不重复建） | 已交付 |
| ~~Lead 的"思考"层~~ | ✅ 已交付**可替换的计划器**：`Planner` 接口 + 确定性 `RulePlanner`（源/关键词/指派人/前缀/忽略），循环保证有界、失败不中断、不回炉；模型版计划器以后替换即可 | 已交付（模型版待接） |
| ~~任务板租约/心跳/证据/验收~~ | ✅ 已交付：`tasklease`（TTL/心跳/证据必填/review/accept/reject/expire） | 已交付 |
| ~~审批 action + 文字回复~~ | ✅ 已交付：`IssueToken` / `Apply` / `ApplyReply`，三条入口最终都落到 `Resolve` | 已交付 |
| 渠道适配器 | 🟡 四个渠道的内核都有了：webhook（+HTTP+source 管理）、微信（轮询/发送/token 持久化/轮询循环/保活）、钉钉（验签+收+发）、飞书（challenge+验签+收+卡片动作提取+发）；邮件未开始。**只有 webhook 挂了 HTTP 路由** | 下一步：钉钉/飞书入站路由与卡片回调、凭据存储、真机验证 |
| 渠道真机验证 | 微信/钉钉/飞书都只对**假服务**跑过（httptest），真实平台的报文、签名实现与限速未验证 | 已知风险 | 上线前每个渠道真机跑一遍（回调回放 + 收发 + 限速） |
| ~~钉钉/飞书 HTTP 路由~~ | ✅ 已交付：`/api/inbound/dingtalk`、`/api/inbound/feishu`（challenge 200、无关事件 204、错误 401/400/503） | 已交付 |
| 凭据仍未接到适配器 | creds 库有了，但三个适配器仍从 Config 读 `json:"-"` 字段；没人调用 `KeyCredentials` | 缺口 | 适配器启动时 `Get(kind,id)` + 热更新；UI 表单 |
| 卡片动作未接审批 | 飞书 `CardAction` 提取了 approvalId/decision，但审批 action 需要一次性 token；卡片 value 里还没带 token | 缺口 | 发卡时把 token 放进 `action.value`，回调走 `approval.action` |
| 模型计划器装配注入 | 零件齐：`planner.New(...)` + `lead.ModelPlanner`；装配层还没读 `chat.baseURL/apiKey/model` 设置去构造 | 小缺口 | 装配/自动化里按设置构造；无配置则保持规则计划器 |
| ~~钉钉/飞书未读凭据库~~ | ✅ 已交付：两个适配器从凭据库补齐空字段，显式配置仍然优先 | 已交付 |
| ~~扫码登录 UI~~ | ✅ 已交付：渠道设置页「扫码接入」块（2s 轮询、10 次上限、过期可重启、卸载清表）；无二维码依赖，先显示可复制链接 | 已交付（真二维码需批准一个依赖） |
| 浏览器工具审批 | browser_open/title/screenshot 已注册为工具，默认走审批门（沙箱只管文件不管网络） | 已定：保持默认询问；若要放过 title 需改 APPROVAL-SURFACES 机器清单 |
| ~~网关协议 `inbox.*` / 租约 / 审批动作~~ | ✅ 9 个方法已实现并同步 TRANSPORT §5.1.13、contract.json、ws-contract.ts | 已交付 |
| ~~`notify.route.*`（主/备渠道）~~ | ✅ 已交付：router 服务 + `notify.route.snapshot/upsert` + capability `notifyroute` | 已交付 |
| UI | 未接；效果图只是原型 | 协议稳定后一轮做 |

## 4b. 本轮互联判断（审批层）

- **一个语义**：token、按钮、卡片回调、聊天回复全部落到 `Approvals.Resolve`；没有第二条写路径。
- **复用而非重写**：`ApplyReply` 调 `ParseReply`，测试 `TestApplyReplyRoundTripsThroughTheParser` 盯着，防止词表被复制成两份。
- **token 绑定审批单**：签名覆盖 `payload|approvalID`，把 A 单的 token 用到 B 单会被判无效（有测试）。
- **重启失效**：secret 每次启动随机生成，重启后旧 token 全失效——这是**有意的**（宁可让用户重发一次，也不让旧链接长期有效）；代价写进 §4 已知限制。
- **能力门控暂缺**：新方法可用，但 `serverCapabilities` 还没加 `inbox/triage/tasklease/approvalaction`（要连带改 contract.json 的 static 列表和 web 生成物）。目前客户端只能"调用后看 `-32004`"，不优雅；列为下一步。
- **生成链是互联的一部分**：改 dispatch 必须同步 4 处——TRANSPORT §5.1、`contractParams`、`contract.json`（`go run ./gencontract`，**要在包目录里跑**，否则会写到仓库根）、`web/src/design/ws-contract.ts`（`npm run gen:ws-contract`）。漏一处，一致性测试或 `gen:verify` 会红。

## 4c. 并行工作流（2026-11-25，多智能体）

用户要求用多个智能体并行推进整个规划。做法与结果：

| 工作流 | 智能体 | 写作用域 | 结果 |
|---|---|---|---|
| 钉钉适配器 | `dingtalk` | `internal/modules/dingtalk/**`、`internal/contracts/dingtalk.go` | 17 测试绿 |
| 飞书适配器 | `feishu` | `internal/modules/feishu/**`、`internal/contracts/feishu.go` | 24 测试绿 |
| 微信登录态/轮询 | `wechat` | `internal/modules/wechat/**` | 35 测试绿（含 9 条原有） |
| 租约与规则持久化 | `persist` | `internal/modules/tasklease/**`、`internal/modules/lead/**` | 27 测试绿 |

规则：**每个智能体只写自己的包**，装配/网关/文档由我串行集成——因为那三处正是之前踩过的"四处同步"热点。
集成时我补了：registry + headless 条目 + 两个计数锁定清单 + dingtalk/feishu 的装配断言；随后 `verify.sh --quick` 全绿。

## 4d. M5（MCP 连接器）的边界，写清楚

规划里的 M5 在本仓库**大部分已经存在**，本轮的工作是把它接到产品面上，而不是另写一套：

| 已有（产品既有） | 证据 |
|---|---|
| 连接器目录与状态 | `toolkit` 提供 `KeyPlugins`（`plugins.MCP()`）与 `KeyMCPToolCatalog`；`GET /api/capabilities` 就是它的投影 |
| 工具命名与发现 | 工具名 `mcp__<server>__<tool>`；`internal/modules/agent/discovery.go` 的 `TailPrefixes` 含 `mcp__` |
| 审批面 | `mcp__*` **不在** 自动放行清单（`approval_surface_test.go` 断言 `WorkspaceAutoAllowed("mcp__anything") == false`） |

| 本轮新增 | 位置 |
|---|---|
| 任务页可见"能做什么"：技能 + MCP 连接器状态 + 不可用原因（网关原话） | `web/src/features/task/ExecutionMeans.tsx` |
| 连接器失败不再静默：状态与错误文案一起展示 | 同上（`state=failed` 时显示 gateway error） |

**仍缺**：把"某个连接器的某个工具"直接绑到某个任务上作为计划步骤（任务 → 工具的白名单）。这属于执行策略，不是连接器接入；在规划里属于 M3/M5 交界，留待后续。

**聊天回复审批的测试边界**：headless 装配把 approvals 配成 `mode: auto`（请求自动通过，永远没有 pending），
所以在整装配里跑不通"回同意→批准"这条链。现在的证据是分层的：`approvals.ApplyReply` 自己有解析测试、
`replyrelay` 模块有"归档/不动/不解析机器来源"的行为测试、装配断言只证明服务已连上。
真机/手动模式下的完整链路仍待验证（记在这里，而不是假装跑通）。

## 5. 互联审计的固定动作（每加一个模块都做）

1. `registry.go` 加一行 + `headless.json` 加一条（顺序写进 §2 表）。
2. 跑 `TestEveryRegisteredModuleIsLoaded` —— 防止"注册了但没加载"。
3. 在 `TestHeadlessAssemblyLoadsAndUnwinds` 里加 3 行：`root.Get(KeyX)` + 一次真实调用 + 一次失败路径。
4. `gofmt -l` / `go vet` / `go test -race` 三个包：新模块、调用方、assembly。
5. 把"连线/断言/已知缺口"更新到本文件 §2 和 §4。

## 6. 本轮验证记录

```text
go test ./internal/modules/inbox/           ok（15 条行为测试）
go test ./internal/modules/approvals/       ok（6 条解析测试）
go test ./internal/assembly/                ok（含新守卫 + inbox 装配内调用）
go test ./internal/contracts/               ok
go test -race ./internal/modules/inbox/ ./internal/modules/approvals/   ok
go vet ./internal/modules/inbox/ ./internal/modules/approvals/ ./internal/assembly/ ./internal/contracts/   ok
gofmt -l 改动文件                            空

scripts/verify.sh --quick                   PASS（gofmt / build / vet /
                                            协议一致性 / 全量 go test 59s，
                                            5 passed 0 failed）
scripts/verify.sh --quick（triage 后）       PASS 5/5，63s
                                            （中间有一次重跑才绿，记在案）
scripts/verify.sh --quick（tasklease 后）   PASS 5/5，67s
scripts/verify.sh --quick（持久化后）    PASS 5/5，63s
scripts/verify.sh --quick（notifyroute 后）PASS 5/5，63s；web gen:verify / tsc 通过
scripts/verify.sh --quick（webhook 后）  PASS 5/5，66s
scripts/verify.sh --quick（inbound 后）  PASS 5/5，64s
scripts/verify.sh --quick（source 管理后）PASS 5/5，63s
scripts/verify.sh --quick（lead 后）    PASS 5/5，66s；web gen:verify / tsc 通过
scripts/verify.sh --quick（wechat 后）  PASS 5/5，67s
scripts/verify.sh --quick（并行四流集成后）PASS 5/5，66s
scripts/verify.sh --quick（creds 集成后）PASS 5/5，68s
web: typecheck / lint(25 既有 warning) / test 453 / gen:verify 全绿
web（任务详情后）: typecheck / lint(25) / test 477 全绿
web（lease 首屏后）: typecheck / lint(25) / test 484 / gen:verify 全绿
web（团队板后）: typecheck / lint(25) / test 501 全绿
web（渠道设置后）: typecheck / lint(25) / test 514 全绿
scripts/verify.sh --quick（浏览器/计划器/渠道设置后）PASS 5/5，67s
scripts/verify.sh --quick（heartbeat/CDP ws 后）PASS 5/5，69s
scripts/verify.sh --quick（teamnotes 后）PASS 5/5，75s
web（运行状态页后）: typecheck / lint(25) / test 534 全绿
web（共享记忆页后）: typecheck / lint(25) / test 551 全绿
scripts/verify.sh --quick（验收即记忆/扫码登录后）PASS 5/5，67s
scripts/verify.sh --quick（凭据/浏览器工具后）PASS 5/5，70s
scripts/verify.sh --quick（复核/配置/夹具后）PASS 5/5，69s
web（复核 UI 后）: typecheck / lint(25) / test 575 全绿
web（循环控件后）: typecheck / lint(25) / test 580 全绿
web（扫码 UI 后）: typecheck / lint(25) / test 560 全绿
scripts/verify.sh --quick（邮件/计划器/凭据/卡片后）PASS 5/5，64s
scripts/verify.sh --quick（网关方法后）PASS 5/5，63s；web `gen:verify` 全绿、`tsc --noEmit` 通过
scripts/verify.sh --quick（approval 后）    PASS 5/5（一次 flaky 后重跑绿）
                                            （首次跑时 gateway 的
                                            TestApprovalsSnapshotDeltaReplay 以
                                            i/o timeout 失败；单独 -count=3 连过、
                                            全量重跑也过 → 判为高负载下的 flaky，
                                            非本次改动引入，记在 F8）
```

提交：`ed3d628`（设计文档）→ `b9f2aff`（inbox + 审批解析 + 装配守卫，1205 行新增）。