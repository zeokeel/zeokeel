# 00 · 总报告：要不要做 ZeoKeel-Rig SDK、抛弃 eino、把能力开放给三方？

> 日期：2026-11-06 · 发起：用户（"我是否还有必要做一个 ZeoKeel-Rig 的 SDK，抛弃咱们原来老的 eino，支持这种随意组装的插件系统；也同步开放能力给三方使用，这个项目只需要做一个组装就可以随便使用了。调研一下"）
> 方式：3 条并行研究线（eino 依赖审计 / Rig SDK 设计 / 三方开放与生态）+ 本报告综合与交叉核验。
> **只读调研，不改产品代码**：`old/` 与根工程均未修改；本目录仅新增报告。
> 子报告：[01 eino 审计](01-eino-audit.md)（416 行）· [02 Rig SDK 设计](02-rig-sdk-design.md)（550 行）· [03 三方开放与生态](03-openness-ecosystem.md)（411 行，25 条外部 URL）
> 证据分级：**[实测]** 命令可复现 ＞ **[估算]** 假设已列明 ＞ **[外部引用]** 可点开 ＞ **[判断]** 标注为判断。

---

## 0. 结论速览

### 0.1 五个直接回答

| 问题 | 回答 | 关键证据 |
| --- | --- | --- |
| **Q1：有必要做 ZeoKeel-Rig SDK 吗？** | **有，但要限定"卖给谁"**：它是一个**给 Go 开发者的 headless agent 组装 SDK**——第三方 `go get` 后注册自己的 Tool/Provider/模块、写一份 assembly.json、约 20 行 main，`go build` 就得到自己的 agent。**不是**运行时插件市场/无代码平台；那层要用 MCP/ACP 协议满足。 | [02 §0/§2/§8](02-rig-sdk-design.md)；[03 §0](03-openness-ecosystem.md) |
| **Q2：抛弃 eino 值得吗？可行吗？** | **值得，且比预想轻**：eino 只被 `old/internal/runtime` 的 **20 个文件**引用（**9 生产 + 11 测试**），runtime 23 个生产文件里 **14 个（2,970 行/46%）本来就 eino-free**；耦合面 3,464 行。去掉后：go.mod require **51→31（−39%）**、链接包 **434→356（−18%）**、链接模块 **43→22**、依赖源码 **−11.7 万行**、stripped 二进制 **33.81→约 23.2–23.5 MiB（−30~31%）**、冷编译 18.2s→4.2s。代价：自建 ≈1,300–1,950 行 ReAct/流式/工具调度，替换 **22–35 人日**。 | [实测][01 §4/§5](01-eino-audit.md) |
| **Q3："项目只做组装就能随便用"成立吗？** | **对第三方 Go 开发者成立**：装配、生命周期、DI、预检、回滚、agent loop、provider 抽象、工具注册都由 SDK 提供；他们仍需写自己的密钥/模型配置、工具、system prompt、持久化选择、部署形态。**对产品级桌面体验、非 Go 开发者、终端用户"装插件"不成立**——UI/桌面宿主/运行时安装是编译期模型的边界。 | [02 §8](02-rig-sdk-design.md)；[03 §5](03-openness-ecosystem.md) |
| **Q4：能力怎么开放给三方？** | **两层承诺**：①**库生态**（Go SDK，编译期、随宿主重编译，等同 eino/LangChain 的库生态）；②**协议生态**（MCP 工具/资源、ACP agent 客户端、LSP、OpenAI 兼容 HTTP provider——可"装机即用、跨语言、可隔离"，旧工程已有 8,528 行 MCP/LSP/ACP 与 2,717 行 provider 资产）。WASM 沙箱第三阶段按需；`.so` 动态插件明确不做。 | [03 §0/§2/§5](03-openness-ecosystem.md) |
| **Q5：成本与顺序？** | **eino 替换与 SDK 抽取必须同批做**（否则两次迁移 agent core）。去重后 **v0.1.0-alpha ≈ 45–65 人日**（eino 22–35 + SDK 骨架/示例/文档增量约 15–25 + 10–20 产品 dogfood）；**到对外 v0.1 含协议生态 ≈ 85–135 人日**；完整 v1 120–180 人日（不含 UI/产品迁移）。节奏：M0 契约冻结 1 周 → 6–8 周 alpha → headless 产品用 SDK 组装跑通后发 v0.1.0。 | [01 §6](01-eino-audit.md)、[02 §6/§7](02-rig-sdk-design.md)、[03 §5](03-openness-ecosystem.md) |

### 0.2 七条关键结论

1. **"eino 依赖"远没有想象中深**：eino 只出现在 `internal/runtime` 一个包；8 个 eino 包、68 个符号；`compose.Graph`、callbacks/tracing、prebuilt/deep、interrupt/session 序列化**全部未使用**；落盘数据零 eino 类型（store/session 都不引用）→ **无数据迁移**。[实测][01 §1/§2]
2. **替换的核心不是"重写 runtime"，而是"复刻 12 条行为契约"**：流式 chunk/concat、工具错误转文本、工具默认并行、max-iterations 默认 20+三层绕行、system message 剥离 hack、图片 middleware、AgentTool 子代理、GetToolCallID 关联、拓扑热重建等；旧测试 **31 文件/8,882 行/173 Test** 可当规格，其中 eino-bound 只有 **11 文件/3,585 行/67 Test**，**117 个 Test 由 fakeProvider 驱动可原样迁移**。[实测][01 §2/§3]
3. **SDK 的最大工程前提不是 eino，而是"契约拆分"**：当前 `internal/contracts` 同时 import 了 mygo/ui——SDK 必须把 headless 契约（Provider/Tool/History/Gate/Event/Logger/Settings）与 UI 契约（Screen/Slot/UIRuntime…）拆成两份，否则 UI 框架会泄漏进 SDK 依赖树。"contracts/v1 冻结 + `internal/kernelx` 单点隔离 kernel v0.1" 是 SDK 输出的第一优先级。[02 §2.3/§4.3]
4. **仓库策略推荐三仓**：`zeokeel-kernel`（已有，MIT，零三方依赖）+ `zeokeel-rig`（新 SDK，根 module 只 require kernel）+ `zeokeel`（产品：UI/桌面宿主/产品模块）。rig 仓内用**嵌套 module** 承载可选 `ui/`（mygo）与 `store/sqlite/`。降级方案 B：先在产品仓建 `rig/` 嵌套 module 过渡。**SDK 的卖点就是"第三方信任 + 干净依赖树"**，单仓单 module 做不到。[02 §5]
5. **"开放给三方"必须拆成两层承诺**：库生态（a，编译期 SDK）+ 协议生态（b，MCP/ACP/LSP/OpenAI 兼容）是主线；WASM 第三阶段按需；`.so` 明确拒绝；注册表（e）第二阶段做只读清单。**不要承诺 DSH 式"npm 装包下次启动生效"**——那依赖运行时安装 + 动态加载 + HMR，与编译期注册表自相矛盾（DSH 的 305 包/208 行 profile 机制正建立在此之上）。[03 §0/§5]
6. **命名/许可证有硬结论**：`rig` 在 AI 领域已被 Rust `0xPlaygrounds/rig`（9,075★、MIT、活跃 v0.46.0）占用，Go 生态另有 `k0sproject/rig`（活跃）、`rigdev/rig`（归档）——**`ZeoKeel-Rig` 只作内部代号**，对外用 **ZeoKeel SDK**（`github.com/zeokeel/sdk` 一类）；许可证 **Apache-2.0 + NOTICE + DCO**（先不上 CLA）。[外部引用][03 §5]
7. **产品收益不是"变成插件市场"，是"多产品复用 + 依赖树瘦身 + 第三方库生态"**：SDK 化后 ZeoKeel 产品 = 组装 + UI；headless/CLI/第三方 Go 产品共用同一 agent core。这正好接上我们已完成的编译期模块骨架（Stage 0 已跑通：薄 main + assembly + 模块 + UI 注入点）。[判断]

### 0.3 一句话建议

> **做，但按"先瘦身、再 SDK、后生态"的顺序，且定位为 Go 开发者的 headless agent SDK。**
> 第一步（M0，约 1 周）：**拆 contracts（headless vs UI）+ 冻结 contracts/v1 边界 + 定名/许可**；
> 第二步（6–8 周，45–65 人日）：**同批完成 eino-free agent core 与 `zeokeel-rig` MVP**，产品 desktop/headless 都改成用 rig 组装（dogfood）；
> 第三步（按需）：**协议生态**（MCP 双向 → ACP → 只读注册表 → 视需求再评估 WASM）。
> **不要**先做"插件市场/运行时安装"——这条路在编译期模型内走不通，也不该承诺。

---

## 1. 方法与证据

| 研究线 | 产出 | 规模/证据 |
| --- | --- | --- |
| A eino 依赖审计 | 使用矩阵、12 条行为契约、替换规格、依赖/体积收益 | 416 行；探针 `.probe/research/rig-sdk/{ea.go,deps.py,build_probe.sh,out/}`；全部数字可复现 |
| B SDK 设计 | 三层边界、三方用法示例、扩展点/版本化、仓库策略、MVP、成本 | 550 行；含可编译设计稿示例、目录树、方案 A/B/C 对比 |
| C 开放与生态 | 五模式对比、业界参考（25 URL）、信任模型、命名/许可、路线 | 411 行；外部结论均带 URL，本地结论带 `file:line` |

**Lead 交叉核验（独立于子报告）[实测]**：
- eino 引用面：`grep -rl cloudwego/eino --include='*.go' old/internal old/cmd` = **20 文件**（其中 prod **9**、test **11**）；`old/internal/runtime` 生产 **6,434 行**、测试 **31 文件/8,882 行**；
- 依赖面：`old/go.mod` 直接 9 + 间接 42；eino 相关模块 **17/117**；新根工程每包测试 `-race` 绿；旧工程 `old/` 独立构建绿（4.5s 增量）；
- 二进制探针（子报告 A 产出，我复核了文件与数字）：baseline 1.44 MiB / schema-only 7.64 MiB / full 12.40 MiB；旧 stripped 33.81 MiB；估算去 eino 后 23.2–23.5 MiB；
- 当前骨架事实（B/C 引用基线）：12 包、2,986 行生产代码、uiroot 29 个 headless 测试（99.3% 覆盖）、desktop/headless 双产品装配已验证。

---

## 2. eino 事实与替换规格（A 摘要）

### 2.1 使用矩阵（8 个 eino 包 / 68 符号，生产 67）

| eino 包 | 符号数 | 生产用途 | 替换难度 |
| --- | --- | --- | --- |
| `schema` | 28（Message 引用 146 次） | 消息/多模态/工具调用/流式 chunk/usage/reasoning | **高**（最核心，全部自建） |
| `adk` | 18 | ChatModelAgent、Runner、middleware、ToolsConfig、子代理 NewAgentTool、session values | **高**（agent loop 拓扑） |
| `adk/prebuilt/planexecute` | 8 | plan/execute/replan 三件套 | 中（可先删，用我们自己的模式替代） |
| `adk/prebuilt/supervisor` | 2 | supervisor 多智能体 | 中（旧 agentteam/多模式已在产品层） |
| `components/model` | 6 | BaseModel/ChatModel/ToolCallingChatModel/Option | 低（已有 Provider 抽象） |
| `components/tool`（+utils.InferTool） | 3+1 | InvokableTool/BaseTool/InferTool/jsonschema | 低-中（工具注册表已有） |
| `compose` | 2 | **仅** ToolsNodeConfig + GetToolCallID | **低**（Graph 未用，可删） |
| （unused） | — | callbacks/tracing、flow、deep、interrupt、session 序列化 | 直接删 |

### 2.2 替换规格与边界

- **可复用**：runtime 23 个生产文件中 **14 文件/2,970 行已 eino-free**（Provider/Tool/History/Gate/Compactor/Checkpoint 等）——这些直接迁移，不重写。
- **需自建 ≈1,300–1,950 行**：ReAct loop（350–500）、事件翻译（200–300）、工具调度（250–350）、子代理（100–150）、deep 工具（100–150）、prompt/多模态（110–180）。
- **直接删除**：`compose.Graph`、callbacks/tracing、`prebuilt/supervisor`+`planexecute`、session values、`InferTool`+jsonschema；公共面（Provider/Tool/Message/Config）**保持不变**（旧包 9 个/141 文件/1,465 处引用不用改签名）。
- **不做 eino adapter 过渡**：新根工程已 eino-free，旧 runtime 只在 old/ 归档；adapter 只会把 eino 重新带进依赖图（成本 +3–5 人日，收益为负）。[判断，与 A 一致]

### 2.3 收益与风险

| 维度 | 数字 [实测/估算] |
| --- | --- |
| go.mod require | 51 → **31**（−20，−39%；direct 9→8，indirect 42→23） |
| 构建清单 / 链接包 / 链接模块 | 117→109 / **434→356（−78）** / 43→22（−21） |
| 依赖源码 | **−116,996 行** |
| stripped 二进制 | 33.81 MiB → **约 23.2–23.5 MiB（−30~31%）** |
| 冷编译 | 18.18s → **4.18s** |
| 替换人日 | **22–35**（P0 3–5 / P1 8–12 / P2 6–10 / P3 5–8）；含 SDK 公共 API 包装 **+8–12** |

**风险**：①测试迁移与**流式事件顺序等价性**（eino-bound 11 文件/3,585 行/67 Test，旧测试是唯一规格）；②工具默认并行/审批并发语义；③max-iterations 三层旧逻辑；④44 处 eino 行为适配注释需逐条转成我们自己的明确契约。**缓解**：先写 12 条行为契约的 acceptance 测试（以旧 173 Test 为输入），再删 eino；分 P0–P3 小步切换。

---

## 3. ZeoKeel-Rig SDK 设计（B 摘要）

### 3.1 三层 + 一个可选面

```
zeokeel（产品）        cmd · host(桌面窗口/托盘) · uiroot · UI 模块 · 产品 assemblies
zeokeel-rig（SDK）     contracts/v1 · agent(eino-free loop) · provider · tool · session ·
                       assembly · host(headless runner) · examples · internal/kernelx
   ├─ 可选嵌套 module: ui/（mygo 桌面运行时）
   └─ 可选嵌套 module: store/sqlite/（持久化）
zeokeel-kernel（已有）  cordis 内核 + module/ 编译期注册表（独立 module、零三方依赖）
```

- **硬规则**：rig 根 module **只 require kernel**；CI 断言 `go list -deps | grep -E 'mygo|eino|sqlite'` 为空；`contracts/v1` 不出现 `*ui.Context`/`BuildFunc`/槽位类型；产品可 import rig，rig 永不 import 产品。
- **当前最大机械工作**：拆 `internal/contracts` 为 headless（rig）与 UI（产品）两份——现在 UI 契约与 headless 契约混在一个包。[02 §2.3]

### 3.2 三方使用姿势（"只需组装"的具体形态）

```go
package main

import (
    "github.com/zeokeel/sdk"              // 工作名 zeokeel-rig
    "github.com/zeokeel/sdk/provider/openai"
    "github.com/zeokeel/sdk/tool/builtin"
    mymod "example.com/my-agent/mymodule"
)

func main() {
    reg := sdk.NewRegistry()
    sdk.MustRegister(reg, appinfo.Module())
    sdk.MustRegister(reg, openai.Module(openai.Config{BaseURL: "...", APIKeyEnv: "MY_KEY"}))
    sdk.MustRegister(reg, builtin.FilesModule())
    sdk.MustRegister(reg, mymod.Module())      // 第三方自己的模块
    sdk.Run(sdk.Options{Product: "my-agent", Assembly: "assembly.json"})
}
```

- **安装 = 加依赖 + 注册 + 重编译**；配置变更 = 重启；无运行时安装。
- **产品裁剪**：硬裁剪 build tags/多 main，软裁剪 assembly.json 的 `disabled`。
- **第三方仍需写**：provider 密钥/模型配置、自己的工具或挑选内置、system prompt/策略、持久化选择、审批策略、部署形态（CLI/HTTP/ACP）。

### 3.3 仓库策略与命名

| 方案 | 结论 |
| --- | --- |
| A 单仓单 module | ❌ SDK 无独立 semver，依赖树受产品牵制，品牌弱 |
| B 单仓多 module（go.work） | ✅ 可作过渡（`rig/` 嵌套 module，`rig/vX.Y.Z` tag） |
| **C 三仓（推荐）** | **kernel（已有）/ zeokeel-rig（新）/ zeokeel（产品）**；开发期父目录 `go.work` 三仓联调；一次定 import 路径 `github.com/zeokeel/zeokeel-rig`（工作名） |

- **命名**：`rig` 只作内部代号（Rust `0xPlaygrounds/rig` 同领域冲突）；对外 **ZeoKeel SDK**（`github.com/zeokeel/sdk` 一类）。首次发布前定名，预发布期改 module path 成本可忽略。
- **许可**：Apache-2.0 + NOTICE + DCO；kernel/mygo MIT 兼容，示例可 MIT；产品仓补 LICENSE。

### 3.4 MVP 范围与成本

- **v0.1.0-alpha 必须包含**：contracts/v1（Provider/Tool/History/Gate/Event/Logger/Settings + 注册表 + agent 事件流）、eino-free loop（流式/tool-call/取消/迭代/审批）、OpenAI 兼容 provider、Tool registry + ≤3 内置工具、assembly 预检/回滚、headless runner、3 个可编译示例、文档/CI。
- **明确不做**：session 持久化/压缩/checkpoint/多智能体/MCP/LSP/ACP/UI/eino adapter（进 v0.2+ 或产品仓）。
- **成本**：MVP **35–50 人日**（与 eino 替换重叠，去重后见 §5）；完整 v1 **120–180 人日**（不含 UI）。
- **Go/No-Go 闸门**：M0 结束前定不清 Provider/Tool/Event 三件契约 → 暂停；M2 后 headless 产品无法用 rig 组装出可运行 agent → 回退方案 B 收缩范围。

---

## 4. 三方开放与生态（C 摘要）

### 4.1 五种模式（按"代码在哪运行 + 是否可信"分层）

| 模式 | 适用 | 结论 |
| --- | --- | --- |
| a) Go 源码 SDK（编译期、全可信、随宿主重编译） | Go 开发者、内部多产品 | ✅ **主线**（等同库生态） |
| b) 进程外协议：MCP / ACP / LSP / OpenAI 兼容 / JSON-RPC 子进程 | 装机即用、跨语言、可隔离 | ✅ **主线**（旧工程已有 8,528 行 MCP/LSP/ACP、2,717 行 provider 资产） |
| c) WASM（wazero） | 不可信第三方、跨语言 | ⭕ 第三阶段按需（需自造 host ABI） |
| d) `.so` Go plugin | — | ❌ 明确拒绝（平台/工具链/崩溃限制，官方文档否定） |
| e) 托管端点 + 注册表（只分发清单/协议描述） | 分发与发现 | ⭕ 第二阶段只读注册表 v0 |

### 4.2 信任模型与开放承诺

- **T0 官方内置**（编译期、代码审计）/ **T1 认证第三方**（编译期、审计+签名）/ **T2 社区进程外**（清单+权限声明+隔离）/ **T3 WASM 沙箱**（不可信）。
- **默认拒绝 + 显式声明 + 全程审计**；密钥宿主管控；工具结果当数据不当指令；来源签名用 Sigstore（注册表阶段）。
- **诚实评价**：编译期生态 = **库生态 + 协议生态**；不是 DSH 式插件市场。对外必须两层承诺，不要拿 DSH 的"npm 装包即用"做对标宣传。

### 4.3 命名与许可（外部证据）

- `rig` 冲突：Rust [0xPlaygrounds/rig](https://github.com/0xPlaygrounds/rig)（9,075★、MIT、活跃 v0.46.0，同为 LLM 领域）、Go [k0sproject/rig](https://github.com/k0sproject/rig)（活跃）、rigdev/rig（已归档）。**`ZeoKeel-Rig` 只做内部代号**；对外 ZeoKeel SDK；发布前走 GitHub/pkg.go.dev/npm/crates.io/USPTO/EUIPO 尽调。
- 许可证 **Apache-2.0**（明示专利授权，对齐 eino/wazero/ACP 生态）+ NOTICE + DCO；商标由 ZeoKeel 品牌保留。

### 4.4 生态路线（S0–S4，约 35–60 人日）

| 阶段 | 内容 | 人日 |
| --- | --- | --- |
| S0 | 契约公开（`pkg/sdk` + 3 个示例 + 文档站骨架 + 版本门禁） | 8–12 |
| S1 | MCP 双向（client + 只读 server）——工具生态立刻可用 | 10–18 |
| S2 | ACP headless（旧代码 8.5k 行可复用）——编辑器生态 | 6–10 |
| S3 | 注册表 v0（Git+JSON+CI，可选 Sigstore） | 6–10 |
| S4 | WASM POC（按需） | 5–10 |

---

## 5. 合并路线图与成本（Lead 综合）

### 5.1 关键判断：eino 替换与 SDK 抽取必须同批

A 与 B 的工作在 **agent core** 上高度重叠：B 的 MVP（loop/provider/tool/contracts）与 A 的替换规格（同 1,300–1,950 行自建）是同一批代码。**分开做 = 迁移两次**；同批做 = 一次重写同时拿到"去 eino + SDK 公共 API"。

### 5.2 成本表（[估算]，去重后）

| 工作包 | 人日 | 说明 |
| --- | --- | --- |
| M0 契约与边界 | **4–7** | 拆 contracts（headless vs UI）、冻结 contracts/v1、定名/许可、三仓骨架、CI 门禁 |
| M1 eino-free agent core | 12–19 | ReAct loop/流式/工具调度/子代理/deep/多模态（A 的 P1+P2 主体） |
| M2 Rig SDK 骨架 | 10–16 | provider/tool/session/assembly/headless host/examples/文档（与 M1 同批，增量） |
| M3 测试迁移与等价性 | 8–12 | 旧 173 Test/8,882 行当规格；117 个 fakeProvider 驱动 Test 原样迁移 |
| 产品 dogfood（desktop/headless 改用 rig 组装） | 10–20 | 产品只是"组装 + UI"，同时验证 SDK 不过度设计 |
| **v0.1.0-alpha 小计** | **45–65** | ≈ 单人 3 个月 / 双人 1.5 个月（全职） |
| 协议生态 S0–S3 | 30–50 | 可与 v0.1 并行/之后；MCP 优先 |
| **对外 v0.1 合计** | **约 85–135** | 含发布/文档/兼容矩阵 |
| 完整 v1（session 持久化/压缩/checkpoint/多智能体/MCP/LSP/ACP 进 SDK） | 120–180 | B 的完整口径（不含 UI/产品迁移） |

> 参考基线：当前产品重组 Stage 0 已完成（12 包/2,986 行骨架、29 个 uiroot 测试、双产品装配通过），SDK 化的起点比从零好得多。

### 5.3 建议的阶段顺序

```
现在        提交/收尾当前重组（old/ + 新骨架）
M0（1 周）  拆 contracts → contracts/v1 冻结；定名（ZeoKeel SDK）+ Apache-2.0；
            建 zeokeel-rig 仓骨架 + go.work；CI 依赖守卫
M1–M3（6–8 周）
            同批：eino-free agent core + Rig MVP + 旧测试当规格 + 3 示例 + 文档
            产品 desktop/headless 改成 rig 组装（dogfood）
            → v0.1.0-alpha（只保证示例可编译，不保证 API）
v0.1.0      headless 产品用 rig 组装跑通 + API 冻结评审后发布
之后        S1 MCP 双向 → S2 ACP → S3 注册表 v0 → 视需求 S4 WASM
```

---

## 6. 风险与反方意见

| # | 风险/反对意见 | 严重度 | 缓解 |
| --- | --- | --- | --- |
| R1 | **API 稳定性**：SDK 一改，第三方全断；contracts/v1 现在是"从产品内部长出来的"，还不是为外部设计的 | 高 | M0 冻结 Provider/Tool/Event 三件；`contracts/v1` + semver + 弃用期；`internal/kernelx` 隔离 kernel v0.1 演进；发布前 dogfood |
| R2 | **eino 行为等价**：流式事件顺序、工具并行、审批并发、max-iterations 三层逻辑 | 高 | 旧 173 Test/8,882 行当 acceptance 规格；先写 12 条行为契约测试；分 P0–P3 切换 |
| R3 | **双轨维护**：SDK 与产品两套代码/两套节奏 | 中 | 产品是 SDK 第一消费者（dogfood）；产品只保留 UI/host/产品模块，尽量不再复制 core |
| R4 | **命名/品牌**：Rig 与 Rust 0xPlaygrounds/rig 同领域撞车 | 中 | 对外 ZeoKeel SDK，rig 只内部代号；发布前尽调/商标检索 |
| R5 | **生态预期错配**：对外宣传"插件生态"但用户期待 npm 式安装 | 中 | 明确两层承诺（库生态+协议生态）；MCP/ACP 作为"可安装"故事；文档写清"安装=加依赖+重编译" |
| R6 | **单人/小团队带宽**：eino 重写 + SDK + 生态运营 + 产品迁移叠加 | 中-高 | 分阶段；先 M0 + MVP，生态 S1-S4 按需；不承诺时间表外的兼容性 |
| R7 | **UI 依赖泄漏**：现有 contracts 已 import mygo；拆不干净会把 mygo 拖进 SDK | 中 | M0 先拆；CI `go list -deps` 守卫（B §2.4） |
| R8 | **"库生态"算不算生态**：第三方仍要自己编译 | 低（定位问题） | 诚实定位为"Go 开发者 SDK"，不对比 DSH；用 MCP/ACP 补"可安装"体验 |

**反方意见（如果暂缓）**：如果近期目标只有"把当前产品迁移完 + 去掉 eino"，那么**只做 A（去 eino）即可**（22–35 人日），SDK 抽取可以推迟到产品迁移稳定后再做；SDK 化会显著增加 API 冻结与双轨维护成本。这个方案的风险是"以后再拆 SDK 会破坏 import 路径/契约"——B 的降级方案（单仓多 module 过渡）可以缓解。

---

## 7. 建议决策

| 方案 | 内容 | 成本 | 适用 |
| --- | --- | --- | --- |
| **A. 只去 eino** | 自建 agent core，产品继续单仓；不对外 SDK | 22–35 人日 | 只关心依赖瘦身/稳定，不做三方生态 |
| **B. 做 SDK（推荐）** | A + 三仓拆分 + contracts/v1 + headless SDK + 示例/文档 + 产品 dogfood；协议生态随后 | **45–65 人日到 alpha，85–135 到对外 v0.1** | 目标：Go 开发者生态 + 多产品复用 + 依赖树干净 |
| **C. 只做协议生态** | 不做 SDK，只把 MCP/ACP/OpenAI provider 做成一等公民 | 30–50 人日（S1–S2） | 目标只是"能力开放"、不承诺 Go SDK |

**推荐 B**，理由：①我们已有编译期模块骨架与 UI 注入点（Stage 0 已跑通），SDK 化的增量成本被压低；②eino 替换与 SDK 抽取同批可省一次迁移；③三仓 + contracts/v1 让"第三方信任"成立；④产品本身就是第一个消费者（dogfood），不会过度设计。

**决策清单（全部为"是"才启动 B）**：
1. 是否有**至少一个**真实外部 Go 用户/内部第二产品（headless/CLI/OEM）需要复用 agent core？
2. 是否接受"安装 = 加依赖 + 重新编译"（不含运行时安装/热更）？
3. 是否愿意为 API 稳定性投入（contracts/v1 冻结 + semver + 兼容矩阵 + 文档）？
4. 是否接受 85–135 人日到对外 v0.1（并承担双轨维护）？
> 若 1 为否 → 只做 A；若 2 为否 → 只做 C（协议生态）；若 3/4 为否 → 推迟。

**若启动 B，第一周（M0）任务**：①拆 `internal/contracts`（headless → `contracts/v1` 草案；UI 留产品）；②起草 12 条 agent 行为契约（来自 A 报告）；③定名/许可（ZeoKeel SDK + Apache-2.0 + DCO）；④建 `zeokeel-rig` 仓骨架 + `go.work` + CI 依赖守卫；⑤写 3 个可编译示例（最小 agent/自定义 tool/自定义 provider）。

---

## 8. 证据索引与复现

| 结论 | 子报告 | 证据 |
| --- | --- | --- |
| eino 使用矩阵/行为契约/替换规格/收益 | [01](01-eino-audit.md) | `.probe/research/rig-sdk/out/{eino_files.md,eino_symbols.md,eino_usage.json,dep_reduction.json}`；`build_probe.sh` |
| SDK 分层/三方用法/仓库策略/MVP | [02](02-rig-sdk-design.md) | 仓库现有 `internal/{contracts,assembly,host}`；old/ 迁移表 |
| 五模式/信任/命名/生态路线 | [03](03-openness-ecosystem.md) | 25 条外部 URL（MCP/ACP/Terraform/VS Code/wazero/Rust rig 等） |
| 当前骨架基线 | `docs/ARCHITECTURE.md`、`docs/INJECTION-POINTS.md` | — |

**复现命令**：

```bash
export PATH=/usr/local/go/bin:$PATH GOPROXY=off GOSUMDB=off GOFLAGS=-mod=mod
cd old && go build ./...                                    # 归档旧工程可构建
cd /data/mygo && go list -deps ./cmd/zeokeel | wc -l        # 新骨架链接包数
grep -rl cloudwego/eino --include='*.go' old/internal old/cmd | wc -l   # 20
cd old/internal/runtime && ls *_test.go | wc -l             # 31
cd /data/mygo && cat .probe/research/rig-sdk/out/dep_reduction.json
```
