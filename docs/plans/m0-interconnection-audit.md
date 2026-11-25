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
| 注册 ↔ 装配一致 | registry | headless.json | `TestEveryRegisteredModuleIsLoaded`（新） |

inbox 的模块定位：**它是入口，不依赖任何人**（`Requires` 为空），所以它必须能先于 Lead/任务板启动。这条由 `TestDescriptorMetadata` 钉住——如果将来有人给它加 `Require(KeyTeam)`，测试会红。

## 3. 审计发现（真实，不是假设）

| # | 发现 | 性质 | 处理 |
|---|---|---|---|
| F1 | 加一个模块后，`assembly_test.go` 里 **3 处硬编码断言**同时红：headless 实例列表顺序、实例数量、hosttools 的固定下标 `instances[9]` | 顺序耦合：模块一插队，无关断言就连锁失败，容易让人误以为功能坏了 | 已按新顺序修正；**建议**后续把"按下标取实例"改成"按 id 查找"，本次未改（避免扩大改动面） |
| F2 | `TestRegistryIsExplicit` 的模块计数写死 `18`（注释还写着 20，早已陈旧） | 陈旧断言：计数一改就得手改，注释还误导 | 已改为 19 并修正注释；**建议**改成"至少包含这些模块"的白名单断言 |
| F3 | `zeokeel/modelcatalog` 注册了但不在任何装配文档里 | 我一开始以为是新发现并加了一个"注册/装配一致性"守卫，**结果仓库早就有 `TestEveryRegisteredModuleIsComposedIntoAProduct`**——重复守卫已删除；那个守卫里已写明 modelcatalog 与 host/group 是**有意不装配**并附了原因 | 保留原守卫，删除重复；本文件记下这条教训：先读测试再写测试 |
| F4 | `zeokeel/host/group`（容器模块）也不在 headless entries | 合法：它只在嵌套 group 时被引用，headless 没有 group | 带原因列入例外，不失败 |
| F5 | inbox 是内存实现（slice 1），重启丢 ⚠️ | 已知限制 | 下一片：接 `KeyDataDir` 做 JSONL/SQLite 持久化，契约不变 |
| F6 | 第二次加模块又触发同一批断言（顺序列表、实例数、`instances[10]`） | 证明 F1/F2 不是偶发 | **已修根因**：改为「存在性」断言 + 按 id 查实例；计数断言删除，换成"必须包含这些模块" |
| F8 | `internal/gateway` 的 `TestApprovalsSnapshotDeltaReplay` 在整套测试并发时偶发 `i/o timeout`（7s） | 高负载下的时序 flaky，与本次改动无关（单独 `-count=3` 连续通过，全量重跑通过） | 记录在案；后续给该测试加宽松超时或改成事件驱动等待，不掩盖 |
| F7 | **装配链路测出一个真 bug**：`Claim` 对已经指派/进行中的任务再次写 board，被 rig 拒绝 `team task update has no changes`，整次认领失败 | 单模块测试用的是 fake board，永远发现不了；只有真实装配链路能测出来 | 已修：先读当前任务，只写变化过的字段（状态不同才 SetStatus，owner 不同才 Assign） |

## 4. 尚未接的线（明确空白，不许假装完成）

| 缺口 | 说明 | 下一步 |
|---|---|---|
| ~~inbox → Lead 分诊器~~ | ✅ 已接：`triage.Apply` 把收件箱项变成任务板任务（先建任务再标记，失败不脏写；重复分诊不重复建） | 已交付 |
| Lead 的"思考"层（谁来决定 Action/Task） | triage 只执行决定，不做判断；决定来自 Lead 模型 | M0-2b：Lead 分诊回合（agent 侧） |
| ~~任务板租约/心跳/证据/验收~~ | ✅ 已交付：`tasklease`（TTL/心跳/证据必填/review/accept/reject/expire） | 已交付 |
| ~~审批 action + 文字回复~~ | ✅ 已交付：`IssueToken` / `Apply` / `ApplyReply`，三条入口最终都落到 `Resolve` | 已交付 |
| 渠道适配器 | 无（微信/钉钉/飞书/邮件都还没代码） | M0-5，先 webhook，再微信 iLink |
| 网关协议 `inbox.*` / `notify.route.*` | 未加，TRANSPORT 未改 | 与 M0-2 同批（协议先行） |
| UI | 未接；效果图只是原型 | 协议稳定后一轮做 |

## 4b. 本轮互联判断（审批层）

- **一个语义**：token、按钮、卡片回调、聊天回复全部落到 `Approvals.Resolve`；没有第二条写路径。
- **复用而非重写**：`ApplyReply` 调 `ParseReply`，测试 `TestApplyReplyRoundTripsThroughTheParser` 盯着，防止词表被复制成两份。
- **token 绑定审批单**：签名覆盖 `payload|approvalID`，把 A 单的 token 用到 B 单会被判无效（有测试）。
- **重启失效**：secret 每次启动随机生成，重启后旧 token 全失效——这是**有意的**（宁可让用户重发一次，也不让旧链接长期有效）；代价写进 §4 已知限制。

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
scripts/verify.sh --quick（approval 后）    PASS 5/5，63s
                                            （首次跑时 gateway 的
                                            TestApprovalsSnapshotDeltaReplay 以
                                            i/o timeout 失败；单独 -count=3 连过、
                                            全量重跑也过 → 判为高负载下的 flaky，
                                            非本次改动引入，记在 F8）
```

提交：`ed3d628`（设计文档）→ `b9f2aff`（inbox + 审批解析 + 装配守卫，1205 行新增）。