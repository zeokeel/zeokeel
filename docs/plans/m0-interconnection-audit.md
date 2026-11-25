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
| `contracts.KeyInbox` | `internal/modules/inbox`（新） | 未来的 Lead 分诊器、gateway `inbox.*` | `internal/modules/inbox` 行为测试 15 条；`internal/assembly` 里通过 `root.Get(KeyInbox)` 真实调用 Add/去重/Triage |
| 注册 ↔ 装配一致 | registry | headless.json | `TestEveryRegisteredModuleIsLoaded`（新） |

inbox 的模块定位：**它是入口，不依赖任何人**（`Requires` 为空），所以它必须能先于 Lead/任务板启动。这条由 `TestDescriptorMetadata` 钉住——如果将来有人给它加 `Require(KeyTeam)`，测试会红。

## 3. 审计发现（真实，不是假设）

| # | 发现 | 性质 | 处理 |
|---|---|---|---|
| F1 | 加一个模块后，`assembly_test.go` 里 **3 处硬编码断言**同时红：headless 实例列表顺序、实例数量、hosttools 的固定下标 `instances[9]` | 顺序耦合：模块一插队，无关断言就连锁失败，容易让人误以为功能坏了 | 已按新顺序修正；**建议**后续把"按下标取实例"改成"按 id 查找"，本次未改（避免扩大改动面） |
| F2 | `TestRegistryIsExplicit` 的模块计数写死 `18`（注释还写着 20，早已陈旧） | 陈旧断言：计数一改就得手改，注释还误导 | 已改为 19 并修正注释；**建议**改成"至少包含这些模块"的白名单断言 |
| F3 | `zeokeel/modelcatalog` **注册了但不在任何装配文档里**，且当前无消费者 → 运行时永不加载 | 真·死模块：注册 ≠ 加载 | 在守卫测试里列为**带原因的已知缺口**，写入本文件；要么补进装配，要么删注册 |
| F4 | `zeokeel/host/group`（容器模块）也不在 headless entries | 合法：它只在嵌套 group 时被引用，headless 没有 group | 带原因列入例外，不失败 |
| F5 | inbox 是内存实现（slice 1），重启丢 ⚠️ | 已知限制 | 下一片：接 `KeyDataDir` 做 JSONL/SQLite 持久化，契约不变 |

## 4. 尚未接的线（明确空白，不许假装完成）

| 缺口 | 说明 | 下一步 |
|---|---|---|
| inbox → Lead 分诊器 | 现在只有存与改状态，没有"Lead 读收件箱 → 拆任务 → 派活" | M0-2：triage loop + 任务板写入 |
| 任务板租约/心跳/证据/验收 | 现状只有 CAS/依赖/认领，没有 lease、没有 evidence、没有 review | M0-3 |
| 审批 `approval.action` + 文字回复接线 | 解析器（`approvals/reply.go`）已绿，但还没接到审批单上 | M0-4：审批单 token + action + 回复路由 |
| 渠道适配器 | 无（微信/钉钉/飞书/邮件都还没代码） | M0-5，先 webhook，再微信 iLink |
| 网关协议 `inbox.*` / `notify.route.*` | 未加，TRANSPORT 未改 | 与 M0-2 同批（协议先行） |
| UI | 未接；效果图只是原型 | 协议稳定后一轮做 |

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
```