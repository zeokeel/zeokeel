# 03 · 三方能力开放模式与生态调研（"rig" SDK / 编译期模块系统的生态边界）

> 日期：2026-11-06 · 研究线 C（生态与开放策略）
> 前提：ZeoKeel = 纯 Go 编译期模块系统（kernel v0.1.1 + `module/` registry + 内嵌 `assembly.json`）；模块随二进制编译，**无进程内动态加载、无运行时插件安装、配置变更=重启**。
> 目标：回答"把能力开放给三方、项目只做组装就能随便使用"在该架构下能到什么程度、走什么路线、边界在哪。
> 方法：仓库只读实测（`old/` 归档 + 新根 `internal/{contracts,assembly,host,uiroot}`）+ 外部资料 web 调研（外部结论均给 URL；文中 ★=本地实测、☆=外部资料、△=判断）。
> 相关报告：[02-DSH 装配解剖](kernel-split/02-dsh-anatomy.md)（对照基准）；本报告不修改任何产品代码。

---

## 0. 结论速览

1. **五种模式不是竞争关系，而是按"代码在哪运行 + 是否可信"分层**：a) Go SDK（编译进同一二进制，全可信）、b) 进程外协议（MCP/ACP/LSP/OpenAI 兼容/JSON-RPC，可隔离、跨语言）、c) WASM（wazero 沙箱，可承载不可信第三方）、d) `.so` 动态插件（Go 官方明确"很多重大缺陷"，应拒绝）、e) 托管端点 + 注册表（分发的是协议描述清单，不是可执行 in-process 代码，与编译期约束完全兼容）。
2. **推荐主线 = a) + b)**：a) 是编译期模型的天然形态（第三方 Go 开发者 import SDK、注册模块、自己编译，等同 eino/LangChain 的"库生态"）；b) 是唯一能做到"装机即用、跨语言、可隔离"的一层（我们已有 MCP client、ACP server、LSP、OpenAI 兼容 provider 的实现资产）。c) 作为第三阶段可选（只有出现"不可信第三方代码"真实需求时再投入）；d) 明确不做；e) 第二阶段做只读清单/注册表。
3. **"生态"要拆成两层承诺**：**协议层生态**（可安装、可升级、跨语言，靠 MCP/ACP 实现）+ **SDK 层生态**（Go 源码集成、随宿主重编译，靠 a 实现）。**不要承诺 DSH 式"npm 装个包、下次启动自动生效"的插件市场**——那需要运行时安装+动态加载，与编译期注册表自相矛盾（DSH 的 305 包/208 行 profile/bundle patch 机制正是建立在此能力上，见 02 报告）。
4. **命名结论**：`rig` 在 AI 领域已被 Rust `0xPlaygrounds/rig`（LLM 应用框架，9,075★，MIT，活跃，v0.46.0）占用；Go 生态同名有 `k0sproject/rig`（活跃）、`rigdev/rig`（rig.dev，已归档）等。"rig"不宜作为对外产品名（仅可作内部代号）；对外用 `zeokeel-sdk`/`ZeoKeel SDK` 这类域名+范围式命名，并走商标检索流程。许可证建议 **Apache-2.0**（含专利授权、NOTICE 义务，与 eino/wazero/ACP 等 Go 生态主流一致）。
5. **一句话**：在编译期约束下，"开放给三方"能到"**源码级 SDK 生态 + 协议级可安装生态**"的程度，远大于"只能自己编译"的悲观预期、也远小于"插件市场随便装"的乐观预期；应该定位为"**可组合的 Go 内核 + 标准协议边界**"，而不是"插件宿主"。

---

## 1. 现状与硬约束（先把可开放的东西数清楚）

### 1.1 新骨架的能力面（★仓库实测）

| 层 | 位置 | 当前内容 | 可开放性 |
| --- | --- | --- | --- |
| 内核 | `go.mod` → `zeokeel-kernel v0.1.1` | Cordis 语义：Context/fiber/effect/service/event + 可选 `module/` 注册表 | 已开源（MIT，`kernel` 独立 module） |
| 契约 | `internal/contracts/contracts.go` | 服务键（`zeokeel/ui`/`settings`/`log`/`appinfo`…）+ UI 贡献值类型（Screen/Slot/SettingsPage/ToolRenderer/Command）+ 保留槽位常量 | **三方模块唯一共享面**；目前是 `internal/`，开放 SDK 必须把它变成公开包（`pkg/` 或独立 module） |
| 装配 | `internal/assembly/{registry.go,assembly.go,assemblies/*.json}` | 显式编译期 registry（6 个内置模块）+ 内嵌 desktop/headless 两份 `assembly.json` + kernel 预检 + 级联卸载 | 可开放"注册自己的 Descriptor + 在自己的 assembly.json 里启用"的模板 |
| UI 宿主 | `internal/uiroot/`（1,008 行生产代码 + 测试） | 帧入口、五张注册表、Post 队列、导航、主题/语言、设置面、槽位渲染 | UI 扩展点已设计成"槽位 + 注册表"，适合三方 UI 模块（编译期） |
| 已有能力资产 | `old/internal/{mcp,lsp,acp}`、`old/internal/provider/openai` | MCP client（官方 go-sdk v1.8.0，stdio + streamable HTTP；工具名 `mcp__<server>__<tool>`）、LSP client（stdio JSON-RPC）、**ACP v1 server**（headless agent 可被外部编辑器驱动）、OpenAI 兼容 provider | **b) 模式的现成实现**；迁移到新根即可成为开放接口 |
| 依赖框架 | `old/go.mod` | 已依赖 `eino v0.10.0-alpha.35`（20 个文件引用）、MCP Go SDK v1.8.0 | 说明我们本身是 eino 的消费者，不是竞争框架 |

★实测：`old/internal/{mcp,lsp,acp}` 共 8,528 行；`old/internal/provider/openai` 2,717 行；旧工程 tools 包 36 个非测试 Go 文件。这些就是"b) 协议层"的启动资本，不需要从零写。

### 1.2 硬约束（来自 kernel 与架构文档，不能绕）

- 模块集合**链接期固定**："adding a module means adding a line here and recompiling"（`internal/assembly/registry.go:17-20` 注释）。
- 无运行期加载/热更：`docs/ARCHITECTURE.md` §9.6「配置变更 = 重启；无运行时模块安装/热更新」；kernel `README.md` 自述"无动态 ESM/HMR、配置变更=重启"。
- 单二进制、静态链接、可交叉编译是路线最大收益（kernel `README.md`「为什么是内核而不是框架」）；因此 Go 标准库 `plugin`（`.so`）路线被 Go 官方文档本身否定（见 §2.4）。
- UI 是 mygo 原生控件（无浏览器/webview），因此**不可能**照搬 DSH 的"浏览器 bundle 懒加载"作为 UI 扩展通道（02 报告 §4.7 R8）。

---

## 2. 五种开放模式对比

### 2.1 总表（a–e 五模式，按"信任/隔离/安装体验"排序）

| 维度 | a) Go SDK（编译期） | b) 进程外协议 | c) WASM（wazero） | d) Go `.so` 插件 | e) 托管端点 + 注册表 |
| --- | --- | --- | --- | --- | --- |
| 第三方代码运行位置 | 宿主进程内（同一二进制） | 独立进程/独立服务 | 宿主进程内 WASM 沙箱 | 宿主进程内（dlopen） | 第三方服务器/远端服务 |
| 语言 | 仅 Go | 任意（协议级） | 任意（编译到 wasm） | 仅 Go（且同工具链） | 任意（HTTP/JSON-RPC） |
| 需要重编译宿主 | **是** | 否 | 否 | 否（但需匹配构建） | 否 |
| 安装/升级体验 | 改代码 + `go build` | 装进程/配 URL，立即可用 | 放 `.wasm` + 清单 | 放 `.so`（且工具链必须完全一致） | 配 URL/清单，立即可用 |
| 隔离/安全 | **无隔离（全可信）** | 进程级隔离（能力由宿主授予） | 沙箱（能力由 host function 决定） | 无隔离（同地址空间，风险最高） | 网络边界（凭证/数据外发风险） |
| 性能 | 最优（函数调用） | IPC 开销（本地 RPC 可接受） | 有 JIT/解释开销 | 最优 | 网络延迟 |
| 生态成熟度 | Go 库生态（eino/LangChain 式） | MCP/ACP/LSP/OpenAI 兼容已是事实标准 | 早期但增长（wazero 1.0 2023-03 起生产可用） | 基本不可用（平台/工具链限制） | 注册表生态刚起步（MCP Registry 仍 preview） |
| 适合谁 | 信任的深度集成方（OEM、企业内团队） | 社区/跨语言/不可信第三方 | 不可信第三方代码、跨语言 | 不推荐 | 远程能力提供方、市场 |
| 本项目对应 | 把 `internal/contracts` 公开为 SDK | 把 MCP/ACP 适配进新骨架 | 第三阶段 | 明确拒绝 | 第二阶段只读清单 |
| 证据 | 本仓库 `internal/assembly`；kernel `module/` | 见 §2.3 | [wazero.io](https://wazero.io/)、[github.com/wazero/wazero](https://github.com/wazero/wazero) | [pkg.go.dev/plugin](https://pkg.go.dev/plugin) | [MCP Registry](https://modelcontextprotocol.io/registry/about) |

### 2.2 a) Go 源码 SDK（编译期，天然形态）

- **形态**：第三方 `import github.com/zeokeel/zeokeel/pkg/sdk`（规划），实现 `module.Descriptor[T]` 或 `sdk.Module`，在**自己的** `main`/assembly 里 `registry.Register`，编译出**自己的**二进制。也可由下游厂商维护一个"发行版 main"。
- **优点**：与 kernel 的类型化预检/服务契约/生命周期完全一致；没有 IPC/序列化损失；可共享 Go 类型（最强类型安全）。
- **成本**：① 第三方必须会 Go 且愿意维护 fork/构建；② 契约兼容性是硬约束（kernel 预检会拒绝 provider/type 不匹配，但跨版本语义漂移要 semver 策略）；③ 无法保护源码（编译期集成=源码级信任）。
- **生态参照**：OpenAI Agents SDK 是"库"（用户写 Python 调 SDK，[github.com/openai/openai-agents-python](https://github.com/openai/openai-agents-python)，MIT，30,527★）；eino 是 Go 的"库+组件仓库"模式（[cloudwego/eino](https://github.com/cloudwego/eino) Apache-2.0 13,573★；扩展拆到 [eino-ext](https://github.com/cloudwego/eino-ext)，835★）。库生态不需要"运行时安装"也能繁荣——但它的"生态"是**代码使用者**，不是"可安装插件"。
- **可复用的供应链基础**：Go 模块代理 + `go.sum` 已提供源码完整性校验；SDK 分发只需版本 tag + 校验和，签名可交给 Sigstore/cosign（§4.3）。
- **本项目结论**：△**必做**（这是解耦"项目只做组装"的核心：把 host/assembly 与模块的边界、SDK 文档、示例模块做好，第三方按模板产出自己的模块和二进制）。同时我们要提供"发行版构建模板"（Makefile/CI 配方），否则 OEM 方会被构建细节劝退——这属于 SDK 的一部分，不是额外负担。

### 2.3 b) 进程外插件协议（可隔离、跨语言、可安装）

四类协议已足够覆盖我们的能力面；它们的共同范式是"**稳定协议 + 能力声明 + 版本协商**"：

| 协议 | 角色 | 关键事实（外部） | 我们的资产/落点 |
| --- | --- | --- | --- |
| **MCP**（Model Context Protocol） | 模型能力（tools/resources/prompts） | 最新规范 `2026-07-28` 已改为**无会话、无 initialize**：每个请求带协议版本与 client capabilities（`_meta`），`server/discover` 做版本发现，`UnsupportedProtocolVersionError` 拒绝不支持版本（[changelog](https://modelcontextprotocol.io/specification/2026-07-28/changelog)、[versioning](https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning)）；注册表只存 `server.json` 元数据（[registry/about](https://modelcontextprotocol.io/registry/about)） | ★ `old/internal/mcp` 已有 client（官方 Go SDK v1.8.0，stdio/streamable HTTP）。落点：① 新骨架的 **MCP client 模块**（外部 server 的工具进 `ctx.tools`）；② **ZeoKeel MCP server**（把内置能力以 tools/resources 暴露出去，供 Claude/Cursor/任意 MCP host 使用） |
| **ACP**（Agent Client Protocol） | 编辑器 ↔ agent | JSON-RPC 2.0；`initialize` 做版本/能力协商；`session/new`、`session/load`、`session/prompt` + `session/update` 通知；客户端提供 `fs/*`、`terminal/*`、`session/request_permission` 能力；本地 agent 跑子进程 stdio，远程 HTTP/WebSocket 仍在推进（[overview](https://agentclientprotocol.com/protocol/v1/overview)、[introduction](https://agentclientprotocol.com/get-started/introduction)）；spec 仓库 Apache-2.0、4,587★（[repo](https://github.com/agentclientprotocol/agent-client-protocol)） | ★ `old/internal/acp` 已实现 ACP v1 server（initialize/authenticate/session/prompt/fs/terminal/permission）。落点：把 headless 产品变成"任意 ACP 编辑器可驱动的 agent"——**这是"第三方使用我们"成本最低的一条路** |
| **LSP** | 编辑器 ↔ 语言服务 | 事实标准；我们已作为 **client** 使用（模型调 hover/definition 等）（[官方规范](https://microsoft.github.io/language-server-protocol/)） | ★ `old/internal/lsp`：作为"能力提供方"接入生态的优先级低（语言服务不是 AI 能力），但作为"能力使用方"必须保留 |
| **OpenAI 兼容 HTTP** | 模型 provider | Chat Completions/Responses 形状已被大量推理服务与工具兼容（例：vLLM 的 [OpenAI-Compatible Server](https://docs.vllm.ai/en/latest/serving/online_serving/openai_compatible_server/)）；生态里"换 base_url + key 即可用"是事实标准 | ★ `old/internal/provider/openai` 2,717 行已完成 OpenAI 兼容 provider；落点：把"任意 OpenAI 兼容端点"作为配置项（vLLM/Ollama/LM Studio/聚合网关），无需写代码 |
| 通用 JSON-RPC/gRPC 子进程 | 自定义能力 | HashiCorp `go-plugin` 是成熟范例：子进程 + net/rpc 或 gRPC、协议版本号、TLS、插件二进制校验和、宿主可升级时重连（[README](https://github.com/hashicorp/go-plugin)）；Terraform provider 即基于此，且**协议是版本化 protobuf/gRPC**，主版本定义兼容性、次版本只做增量（[protocol](https://developer.hashicorp.com/terraform/plugin/terraform-plugin-protocol)） | 除非现有协议不够，**不为自定义能力新造 RPC**；需要时用 MCP 扩展（`_meta`/自定义方法）或 ACP 的 `_*` 自定义方法（[ACP extensibility](https://agentclientprotocol.com/protocol/v1/overview)） |

**工程建议**：进程外能力统一建模为"**Adapter 模块**"——每个 MCP/ACP/远程端点是一个编译进宿主的 Go Adapter 模块（负责连接、健康、重试、权限），而不是让宿主直接理解每个协议细节。这样"协议生态"与"编译期注册表"不冲突：**协议清单可以是数据，Adapter 是代码**。

### 2.4 c) WASM 沙箱插件（wazero）——第三阶段候选

- **能力**：wazero 是"零依赖、纯 Go、无 CGO"的 WebAssembly 运行时，1.0（2023-03）已在生产使用，保持交叉编译（[wazero.io](https://wazero.io/)、[repo](https://github.com/wazero/wazero)，Apache-2.0，6,454★）。
- **适合**：不可信第三方代码、跨语言（Rust/C/AssemblyScript/TinyGo → wasm）、需要资源限制/确定性。
- **成本**：① wasm 插件与宿主之间的**能力接口**要自定义（host functions：fs/网络/模型调用），等于重造一套 ABI；② 插件必须用 WASI/自定义 ABI 编译，工具链门槛高；③ UI 模块基本无法用 wasm 直接做（mygo 原生控件在宿主侧）——wasm 更适合"计算型/工具型"插件；④ 生态里 Go+WASM 的插件市场尚无强先例（多为函数即服务场景）。
- **与单二进制约束的契合**：wazero 纯 Go、无 CGO，不破坏交叉编译/静态链接（[wazero.io](https://wazero.io/)），这是它比 `.so` 方案根本性更优的地方；但它要求我们把"能力接口"重新定义为 host functions（文件/网络/模型调用各一个），等于新增一套 ABI 规范——这才是真正成本。
- **结论**：△**第三阶段**，且只在"确有不可信第三方代码"时启动 POC（5–10 人日即可验证：定义 3 个 host function + 一个示例 wasm 工具，用 wazero 内嵌）。

### 2.5 d) 动态 Go 插件（`.so`）——明确拒绝

Go 官方文档对 `plugin` 的警告本身就是最好的拒绝理由（[pkg.go.dev/plugin](https://pkg.go.dev/plugin)）：

- 平台：只支持 Linux/FreeBSD/macOS，**不适合可移植应用**；与单二进制/交叉编译目标冲突。
- race detector 支持差；运行时崩溃风险高，"除非所有部分用**完全相同的工具链、构建标签、flags、环境变量**，且公共依赖来自**完全相同的源码**"。
- 官方结论："实践中应用和插件必须由单个人/组件一起构建"——既然如此，不如直接用源码/注册表（§2.2）。
- 安全：加载 `.so` 等于让不可信代码进地址空间，无沙箱。

**结论**：△**不做**，在文档中明确写成"非目标"，避免第三方误期待（与 kernel 路线一致：`docs/COMPILE-TIME-REGISTRY.md` 已把"动态加载"标为路线外）。

### 2.6 e) 托管/远程端点 + 注册表/市场

- **本质**：注册表分发的是**描述清单**（名称、命名空间、协议类型、端点/包定位、能力、版本、校验和/签名），不是 in-process 可执行代码。MCP Registry 的 `server.json` 就是范例：唯一名（如 `io.github.user/server-name`）、安装/定位信息（npm 包、远程 URL）、执行配置、能力；**命名空间用 DNS 验证**；注册表本身只做元数据，包由 npm/PyPI/Docker 等承载；预览期、明确"下游聚合器"消费（[registry/about](https://modelcontextprotocol.io/registry/about)）。
- **与编译期约束的兼容性**：**完全兼容**——清单里的"包"可以是三方协议服务（MCP/ACP/OpenAI 兼容），也可以是"Go 模块源码坐标"（供 SDK 层用户 `go get`）。注册表 v0 可以只是一个版本化 JSON + CI 校验，不需要运行时代码下载。
- **成本/风险**：namespace 抢注与冒名（需 DNS/GitHub 组织验证 + 人工审核）；恶意端点（数据外发、工具投毒）需展示风险与权限声明；维护成本低（本质是静态索引）。
- **零服务端起步**：v0 用 Git 仓库 + JSON + CI 即可，等条目数/审核量上来再考虑服务化；参考 MCP Registry 明确"注册表只做元数据、包由 npm/PyPI/Docker 承载"的定位，避免自己变成包管理器。
- **分级**：官方（我们维护）/认证（审核过代码与清单）/社区（只审清单），对应 §4.1 的 T0/T1/T2；分级标签本身就是最重要的治理工具（Terraform Registry 的 partner 徽章是同一思路）。

### 2.7 需求映射：把"开放给三方、项目只做组装"拆成可验收需求

用户的原话很抽象，落到工程上是 7 条可验收需求；把它对五模式打分（✅ 满足 / ◐ 部分 / ❌ 不满足），可以直接看出为什么必须是"a+b"双层而不是单选：

| # | 需求（可验收定义） | a) SDK | b) 协议 | c) WASM | e) 注册表 |
| --- | --- | --- | --- | --- | --- |
| R1 | 第三方**不改我们的代码**就能提供能力 | ✅（写自己的模块） | ✅（写自己的进程） | ✅ | ✅（只发布清单） |
| R2 | 我们只做"组装"：宿主不感知具体三方实现 | ✅（registry 一行） | ✅（adapter 模块） | ✅（loader 模块） | ✅ |
| R3 | **装机即用/可独立升级**（不重编译宿主） | ❌ | ✅ | ✅ | ✅ |
| R4 | 跨语言/跨技术栈 | ❌（仅 Go） | ✅ | ◐（需编译 wasm） | ✅（描述任意端点） |
| R5 | 不可信代码**可隔离** | ❌（同进程） | ✅（独立进程） | ✅（沙箱） | —（网络边界） |
| R6 | 生态规模/可发现性 | ◐（Go 库生态） | ✅（MCP/ACP 事实标准） | ◐ | ✅（索引） |
| R7 | 深度定制（UI 槽位/设置页/命令/工具卡） | ✅ | ◐（MCP 可进工具目录，UI 贡献不行） | ❌ | ❌ |

**结论（△）**：R3/R4/R5 只有 b/c 满足，R7 只有 a 满足——所以"深度集成用 SDK、广域接入用协议"不是折中，而是需求本身分裂出的两条通道。这也解释了 DSH 为什么能做"一个通道"：它的插件本身就是 JS，天然同栈且可动态加载（02 报告 §5.2 P9/P10）；我们换语言换来静态/可交叉编译收益，代价就是通道分裂。


---

## 3. 业界参考：可复用经验提炼

### 3.1 DSH（对照基准，★+☆）

- ★ 本机安装：`@deepseek-ai/dsh` 0.2.1-rc.2，npm 包由 305 个 `@deepseek-ai/*` 包组成，运行时通过 profile（`~/.dsh/profiles/web/package.json` 的 bundle 列表 + 13 个 patch 文件 1,730 行）装配出 208 行模块树（详见 [02 报告](kernel-split/02-dsh-anatomy.md)）。
- ☆ npm 公开分发提供了"可安装"的体验：`@deepseek-ai/dsh` 在 npm registry 有连续版本、`dist-tags`、tarball 与 npm 签名（[registry.npmjs.org/@deepseek-ai/dsh](https://registry.npmjs.org/@deepseek-ai%2Fdsh)、[npm 页面](https://www.npmjs.com/package/@deepseek-ai/dsh)）；`dsh plugin add` 转发 pnpm 安装 bundle → 下次启动生效（02 报告 §3.1、配置条目 `$DSH` 插件管理器）。
- **可复用经验**：① **组合（bundle/profile）与实现（插件包）分离**；② **稳定 id + 分层 patch** 让用户只覆盖差异；③ **版本/兼容检查**（peer/engines + 精确版本豁免）；④ **npm 分发 + 清单**。**不可复用**：运行时安装/动态 import/配置即代码/HMR（02 报告 §5 已逐条判断）。

### 3.2 其他生态（外部资料）

| 生态 | 模式 | 关键事实（URL） | 对我们的启示 |
| --- | --- | --- | --- |
| OpenAI Agents SDK | 库 + 工具协议 | MIT、30,527★；provider-agnostic（Responses/Chat Completions + 100+ LLM）；Tools 含 functions/MCP/hosted tools；handoffs/guardrails/sessions/tracing（[repo](https://github.com/openai/openai-agents-python)、[docs](https://openai.github.io/openai-agents-python/)） | "库生态"不需要运行时安装；工具边界直接用 MCP，避免自造工具协议 |
| eino / eino-ext | Go 框架 + 扩展仓库 | `eino` Apache-2.0、13,573★、issues 312（[repo](https://github.com/cloudwego/eino)）；组件/扩展单独仓库 `eino-ext` Apache-2.0、835★（[repo](https://github.com/cloudwego/eino-ext)）；我们旧工程已依赖 eino `v0.10.0-alpha.35`（★`old/go.mod:6`，20 个文件引用） | "核心稳定、扩展独立演进"的仓库切分值得照搬；但 eino 的扩展仍是 Go 源码（编译期），说明 Go 生态里"源码级扩展"是被接受的 |
| LangChain / LangGraph tools | 库 + 生态适配器 | MCP 适配器已并入 `langchain.mcp` 命名空间（旧 `langchain-mcp-adapters` 停止维护，[repo 说明](https://github.com/langchain-ai/langchain-mcp-adapters)、[迁移源](https://github.com/langchain-ai/langchain/tree/master/libs/langchain_v1/langchain/mcp)、[公告](https://www.langchain.com/blog/mcp-in-langchain-stateless-protocol-elicitation-and-more)）；MCP tools 可转 LangChain tools 并接多 server/transport（stdio/http） | 协议层生态会**收敛到标准协议**（MCP），专有适配器会被吸收；我们应直接押 MCP，而不是自建工具描述格式 |
| Terraform provider | 进程外插件 + 注册表 | 插件是可执行二进制、走 RPC（[plugin](https://developer.hashicorp.com/terraform/plugin)）；协议是版本化 protobuf/gRPC，主版本定兼容、次版本只增量（[protocol](https://developer.hashicorp.com/terraform/plugin/terraform-plugin-protocol)）；Registry 要求仓库命名 `terraform-provider-{name}`、发布带 `terraform-registry-manifest.json`（`metadata.protocol_versions`），注册表用协议版本做兼容选择；有 partner 认证徽章（[publishing](https://developer.hashicorp.com/terraform/registry/providers/publishing)） | ①**注册表 = 元数据 + 协议版本**，与我们的编译期约束兼容；②**命名/仓库命名规范**是可执行的治理手段；③官方/partner 分级是生态运营的最小模型 |
| HashiCorp go-plugin | 进程外 RPC 插件框架 | 子进程 + net/rpc/gRPC；协议版本号可在不兼容时给出友好错误；支持校验和验签、TLS、宿主升级时插件重连、插件崩溃不拖垮宿主（[README](https://github.com/hashicorp/go-plugin)） | 若做自定义协议，这是现成形态；但 MCP/ACP 已覆盖我们需求，**不建议自造** |
| VS Code 扩展 | 进程外扩展宿主 + 清单 | 扩展跑在独立 extension host（本地 Node/远程/浏览器 WebWorker），明确"misbehaving extensions 不应影响启动性能/UI/不改 UI"；清单 `contributes` + activation events 懒加载；`extensionKind` 决定运行位置（[extension-host](https://code.visualstudio.com/api/advanced-topics/extension-host)、[manifest](https://code.visualstudio.com/api/references/extension-manifest)） | ①**清单驱动贡献点** + **懒激活**；②**隔离进程**换稳定性；③我们的编译期模块是"内置扩展"，三方需协议或 SDK |
| wazero | Go 内嵌 WASM 运行时 | 零依赖、无 CGO、保留交叉编译、1.0 起生产使用（[wazero.io](https://wazero.io/)）；注意 "wazero is a registered trademark of Tetrate.io"（同页） | ①技术路线可行；②**命名可被注册为商标**——命名调研必须做（§5） |

### 3.3 六元组：一个"开放生态"的最小构件

综合上表，可复用的不是某个框架，而是六个构件；它们的必要性随模式不同：

| 构件 | 含义 | a) SDK | b) 协议 | c) WASM | e) 注册表 |
| --- | --- | --- | --- | --- | --- |
| 稳定协议/接口 | 版本化契约（SemVer）；缺省值/兼容规则 | ✅ 服务键+Descriptor | ✅ MCP/ACP 版本协商 | ✅ host ABI | — |
| 清单（manifest） | 声明式元数据：名称、能力、依赖、权限、兼容版本 | ✅ `module.Descriptor`+`engines` 字段 | ✅ `server.json`/ACP capabilities | ✅ 自定义 manifest | ✅ 清单本身 |
| 版本协商 | 拒绝不兼容、给可行动错误 | ✅ kernel 预检 + SemVer | ✅（MCP `UnsupportedProtocolVersionError`；ACP `initialize`） | ✅ | ✅ `protocol_versions` 字段 |
| 来源/签名 | 校验和/签名/透明日志 | ✅ `go.sum` + Sigstore（可选） | ✅ 端点身份 + mTLS/Bearer | ✅ wasm 摘要 | ✅ Sigstore/cosign + DNS 命名空间 |
| 注册表/索引 | 发现、审核、分级 | 可选（`pkg.go.dev` 天然索引） | 可选（社区清单） | 可选 | ✅ 核心 |
| 隔离/权限 | 谁能做什么（文件/网络/命令/密钥） | ❌ 无（全可信） | ✅ 进程能力 | ✅ 沙箱 | — |

**结论**：a 模式天然具备前五项中的四项（协议/清单/版本/来源），缺"隔离"；b 模式六项全有。**"项目只做组装"** 的实现路径就是把六个构件产品化：SDK（清单+契约）+ 协议适配器（隔离+版本协商）+ 注册表（发现+签名）。

### 3.4 六元组落地建议（本项目的具体做法）

| 构件 | a) SDK 层做法 | b) 协议层做法 | 最小验收 |
| --- | --- | --- | --- |
| 协议/接口 | `pkg/sdk`（或独立 module）导出契约：`Module`、`Descriptor` 适配、`ServiceKey` 清单；UI 侧沿用 `contracts` 的 Screen/Slot/SettingsPage/ToolRenderer/Command 类型 | 只实现 **MCP 稳定子集**（tools/list+call、resources、prompts）+ **ACP v1**；不自造工具描述格式（LangChain 已把 MCP 适配吸收进 `langchain.mcp`，说明收敛方向） | 外部模块示例能编译；外部 MCP client 能对调 |
| 清单 | Go 描述符（编译期强制：ID、Provides/Requires、Config 类型、版本）+ `engines.zeokeel` 兼容范围（对齐 DSH 的 `engines.dsh` 思路，02 报告 §4.7 R9） | `manifest.json`：`name` / `namespace` / `protocol`（mcp 或 acp 或 openai-compat）/ `endpoint` 或 `module` 路径 / `capabilities` / `permissions` / `versionRange` / `digest` | `assembly.Validate()` + manifest schema 双校验 |
| 版本协商 | SemVer + 弃用期；kernel 预检当编译期门禁 | MCP：每请求 `_meta` 版本 + `server/discover`；ACP：`initialize` 返回版本/能力；OpenAI 兼容：`/v1/models` 探活 + capabilities 字段 | 版本不匹配时给出"支持列表"式错误（照 MCP `UnsupportedProtocolVersionError` 的体验） |
| 来源/签名 | `go.sum`；官方发行版二进制 Cosign keyless 签名 | 清单 detach 签名（`cosign sign-blob`）+ 摘要；端点要求 TLS；本机 server 用 stdio（无网络面） | `cosign verify-blob` 通过；摘要与产物一致 |
| 注册表 | 不建（`pkg.go.dev` 即索引） | 静态 JSON + CI 校验 + 分级标签（官方/认证/社区）；命名空间 GitHub org/DNS 验证 | 新增条目 PR 通过校验；无代码执行 |
| 隔离/权限 | 无（文档写明 T0/T1 全可信，必须代码审查） | 每个 adapter 声明 capability（fs.read/fs.write/exec/net/model.secret），宿主按会话授予；默认拒绝 | 恶意能力请求被拒并审计 |

**成本提示（△）**：注册表 v0 可以就是"一个 Git 仓库 + 每条例子的 JSON/Schema + CI 校验 + 可选签名"，服务端成本≈0；这是唯一能在 1–2 周内做出"生态感"的构件，但**它必须一开始就明说"清单不等于可执行插件"**，否则会被误读成插件市场。


---

## 4. 信任模型与安全边界

### 4.1 四级信任模型（建议写进 SDK 文档与注册表）

| 级别 | 谁 | 集成方式 | 代码审查 | 能力 | 密钥/数据 | 故障影响 |
| --- | --- | --- | --- | --- | --- | --- |
| **T0 官方内置** | 项目/核心团队 | 编译进官方二进制（`internal/assembly/registry.go` 的注册表） | 必须（PR + review + CI） | 全部 | 可访问宿主密钥（按最小化原则） | 进程级（有 kernel 事务回滚/panic 遏制） |
| **T1 认证第三方（编译期）** | 签约/OEM/认证伙伴 | Go SDK，自带 registry+assembly，自行编译/由我们发行版构建 | 必须（代码审计 + 供应链审查 + `go.sum`） | 需在清单声明（提供/依赖的服务键） | 默认只拿自己所需服务；密钥由宿主注入 | 同 T0（因为同进程） |
| **T2 社区（进程外）** | 社区开发者 | MCP/ACP/OpenAI 兼容端点 + 注册表清单 | 不审查代码，但**审核清单与权限声明** | 仅协议声明的能力（tools/resources）；文件/终端能力由宿主按会话授权 | 凭证走宿主注入，插件不得持久化明文 | 仅该进程/工具；宿主可禁用/超时/熔断 |
| **T3 不可信（沙箱）** | 任意 | WASM（wazero）+ host functions | 无需（沙箱兜底） | 显式 host function（默认无 fs/网络；按需授予） | 禁止直接拿密钥；通过 host function 代理 | 沙箱内（panic/超时/OOM 可捕获） |

### 4.2 能力矩阵（进程外/沙箱必须显式声明）

| 能力 | 授予方式 | 默认 | 审计要求 |
| --- | --- | --- | --- |
| 读文件 | 会话工作区根 + glob 白名单（参考 LSP workspace root 思路） | 只读工作区 | 记录每次访问（工具调用已入 session log） |
| 写文件/命令执行 | 审批（approval）或沙箱模式（workspace-write/read-only/danger） | 拒绝 | 必须走审批服务；危险模式显式开关 |
| 网络 | 域名 allowlist/代理 | 拒绝（MCP 远程 server 显式配置） | 记录出站目标 |
| 模型调用/密钥 | 宿主托管凭证（不在插件环境里落明文） | 不允许插件直连 | 记录调用量与模型 |
| UI 贡献 | 仅编译期模块（槽位注册表） | 无 | UI 模块生命周期已由 `ctx.Effect` 托管 |

### 4.3 供应链与审计

- **签名**：发布制品用 Sigstore keyless（Cosign + Fulcio 身份 + Rekor 透明日志）签名，消费方可验证"来自预期身份且未被篡改"（[Sigstore 概览](https://docs.sigstore.dev/)、[签名 blob](https://docs.sigstore.dev/cosign/signing/signing_with_blobs/)）；Go 侧 `go.sum` 只保证源码完整性，二进制/清单仍建议签名。MCP Registry 的"验证服务器真实性/安全扫描/防垃圾"是同类最小集（[registry/about](https://modelcontextprotocol.io/registry/about)）。
- **清单审核**：注册表对 T2 清单做静态校验（能力字段、端点可达性、维护者身份 DNS/GitHub org 验证），不执行其代码。
- **遥测边界**：默认不外发；官方模块的遥测需显式开关；第三方模块的遥测由其协议端点承担、宿主只记录元事件（与 DSH 的"会话遥测默认 FEEDBACK_ONLY"思路一致，见 02 报告 §4.7）。
- **故障隔离**：进程外超时/熔断/隔离；编译期 T0/T1 依赖 kernel 的 fiber 事务回滚与 panic 遏制（kernel `README.md`「核心保证」）。

### 4.4 威胁场景与对策（STRIDE 精简版）

| 威胁 | 场景 | 影响 | 对策 |
| --- | --- | --- | --- |
| 供应链篡改 | 注册表条目/二进制被替换 | 执行恶意代码（T0/T1）或连到恶意端点（T2） | Sigstore 签名 + 摘要固定；清单 PR 审核；`go.sum` 锁源码 |
| 工具投毒/提示注入 | 恶意 MCP server 在 tool result 里塞指令 | 模型被诱导执行危险动作 | 工具结果当**数据**不当指令（系统提示+渲染隔离）；写/执行始终过审批；只读默认 |
| 冒名/抢注 | 注册表里出现仿冒官方 namespace | 用户装到假能力 | 命名空间用 DNS/GitHub org 验证；官方/认证/社区三级标签（照 Terraform partner badge 与 MCP registry 的"元数据中立"定位） |
| 凭证外泄 | 协议对端索要 API key/会话数据 | 密钥失窃、数据外发 | 凭证宿主管控、按请求代理；对端永不见明文；日志脱敏 |
| 本地 RCE | T2 adapter 暴露 exec/write 且被滥用 | 主机被控制 | 默认关；审批/沙箱模式；工作区根白名单；审计 |
| 沙箱逃逸（c） | wasm host function 权限过大 | 突破隔离 | 最小 host function 集；资源配额（内存/CPU/超时）；跟踪 wazero 安全更新 |
| 依赖混淆 | Go module 同名/仿冒包 | 引入恶意依赖 | 未来用 vanity domain（`github.com/zeokeel/*` 已绑定）+ 发布清单白名单 |

**原则**：**默认拒绝 + 显式声明 + 全程审计**；T0/T1 的信任由代码审查与 CI 承担（同二进制无隔离），T2/T3 的信任由进程/沙箱与能力声明承担——两套信任不能混用一套话术。


---

## 5. 命名与法律：`rig` 的冲突与备选

### 5.1 `rig` 的既有占用（外部实测，检索时间 2026-11-06）

| 项目 | 领域 | 事实 | 风险 |
| --- | --- | --- | --- |
| **Rust `0xPlaygrounds/rig`** | **LLM/AI 应用框架**（与我们的领域高度重叠） | MIT；9,075★；活跃（2026-11-06 仍在推送）；v0.46.0；主页 [rig.rs](https://rig.rs)；描述 "Build modular and scalable LLM Applications in Rust"（[repo](https://github.com/0xPlaygrounds/rig)、[Cargo.toml](https://raw.githubusercontent.com/0xPlaygrounds/rig/main/Cargo.toml)） | **最高**：同名 + 同领域 + 活跃，搜索/交流/招聘都会被混淆 |
| **Go `k0sproject/rig`** | 远程主机通信 Go 库 | "A go package for multi-protocol and multi-os remote host communication"；活跃（2026-11-06 有推送）（[repo](https://github.com/k0sproject/rig)） | 高：Go 生态里已有名为 `rig` 的 Go 包，`import ".../rig"` 会撞名（模块路径不同，但阅读/搜索混淆） |
| **Go `rigdev/rig`** | 内部开发者平台（K8s） | Apache-2.0；1,062★；**已归档**（2025-01 最后推送）；主页 [rig.dev](https://rig.dev)；[repo](https://github.com/rigdev/rig)、`pkg.go.dev/github.com/rigdev/rig` 可索引 | 中：域名/历史项目占用 `rig` 品牌，虽停更但 `rig.dev` 仍在 |
| 其他 | 工具/脚手架 | `tal-tech/rigger`（Apache-2.0）、`gravitational/rigging`（归档） | 低（派生词） |

**判断（△）**：`rig` 作为**对外产品名**风险高：AI 领域被 Rust 框架占用、Go 生态被两个项目占用；且 "rig" 是通用英文词（"装配/索具"），商标显著性弱、注册难度大。**建议 `rig` 只保留为内部代号/研究线名（本报告目录名即如此）**，对外不要使用裸 `rig`。

### 5.2 备选命名（候选，均需走 §5.4 检索流程）

| 候选 | 形态 | 优点 | 风险/说明 |
| --- | --- | --- | --- |
| **ZeoKeel SDK** / `zeokeel-sdk` | 仓库/Go module：`github.com/zeokeel/sdk`；包名 `zksdk` | 与主品牌绑定、域名内命名、搜索唯一性高；"SDK"直接表达库生态 | 名字较朴素；需确认 `zeokeel` 商标/组织可用性（当前 GitHub org 已存在） |
| **ZeoKeel Mod / `zeokeel-mod`** | `github.com/zeokeel/mod` | "mod" 直指编译期模块；短 | "mod" 极通用，作为包名需加前缀（`zeokeelmod`） |
| **ZeoKeel Kit** / `zeokeel-kit` | `github.com/zeokeel/kit` | 友好、偏工具包语义 | "kit" 在 Go 生态常见（需检索） |
| **Gear**（`zeokeel/gear`） | 模块=齿轮，呼应"装配" | 语义贴合 | "gear" 广泛使用（多个项目），需检索 |
| **ZeoKeel Forge** | 构建期铸造 | 语义好 | "forge" 被大量项目使用（GitHub Forge 等） |

**推荐**：对外统一 **"ZeoKeel SDK"（`github.com/zeokeel/sdk`）** 作为库/契约入口；如必须给"扩展体系"一个品牌，用 **"ZeoKeel Extensions"**（描述性，不与任何既有项目冲突的概率更高），内部代号继续用 `rig`。

### 5.3 许可证选择

| 选项 | 要点 | 适用 | 结论 |
| --- | --- | --- | --- |
| **Apache-2.0** | 允许商用/修改/分发；含**明示专利授权**（§3），要求保留声明、标注修改、保留 NOTICE；**不授予商标权**（[choosealicense](https://choosealicense.com/licenses/apache-2.0/)） | 内核、SDK、协议定义、示例 | ✅ **推荐**（与 eino、wazero、ACP 等 Go 生态主流一致；专利授权对第三方集成方友好；商标不授权恰好保护"ZeoKeel"品牌） |
| MIT | 极简、无专利条款（[choosealicense](https://choosealicense.com/licenses/mit/)） | 小工具、示例代码 | 可作为示例目录的补充许可；不建议作为 SDK 主许可（缺专利保护） |
| GPL/AGPL | 传染性 | — | ❌ 不适合开放 SDK（吓退商业集成） |
| 双许可（Apache-2.0 + 商业） | 开源版 + 商业授权 | 若有"闭源分发 SDK"诉求 | 可选；注意贡献者权利需 CLA 支撑 |

**贡献者协议**：默认用 **DCO**（Developer Certificate of Origin，`git commit -s`，[developercertificate.org](https://developercertificate.org/)）降低社区门槛；若未来需要双许可/版权集中，再引入 CLA（参照 Apache 的 [CLA 说明](https://www.apache.org/licenses/contributor-agreements.html)）——**现在就上 CLA 会显著降低第三方贡献意愿**（△）。

### 5.4 商标/名称尽调清单（做名字之前先跑一遍）

1. GitHub 搜索：`rig in:name`、`<name> in:name language:go`（[GitHub search](https://github.com/search)）；本文的 `rig` 检索即用其 API（`api.github.com/search/repositories`）。
2. Go 包索引：`pkg.go.dev` 搜索（[pkg.go.dev](https://pkg.go.dev/)）与 proxy 命名空间（[proxy.golang.org](https://proxy.golang.org/) 页）；确认可用的 `github.com/zeokeel/*` 路径。
3. 包注册表：npm（[npmjs.com](https://www.npmjs.com/)）、crates.io（[crates.io](https://crates.io/)）、PyPI（[pypi.org](https://pypi.org/)）是否有同名包/组织。
4. 商标：USPTO TESS/Search（[tmsearch.uspto.gov](https://tmsearch.uspto.gov/)）、EUIPO eSearch（[euipo.europa.eu/eSearch](https://euipo.europa.eu/eSearch/)）、中国商标网（[sbj.cnipa.gov.cn](https://sbj.cnipa.gov.cn/)）查第 9/42 类；**wazero 的 "registered trademark of Tetrate.io" 就是 Go 开源项目也会注册商标的先例**（[wazero.io](https://wazero.io/)）。
5. 域名/社交：`.dev`/`.io`/`.com` 与 GitHub org、X 账号一致性；避免依赖他人已停止维护但域名仍被占用的品牌（如 `rig.dev`）。

---

## 6. 推荐路线与 MVP

### 6.1 分阶段路线（对应任务要求的 a)+b) 主线、c) 第三阶段、d) 不做）

| 阶段 | 内容 | 模式 | 目标 | 预估 | 退出条件 |
| --- | --- | --- | --- | --- | --- |
| **S0**（2–3 周） | 契约公开化：`internal/contracts` → `pkg/sdk`（或独立 module `github.com/zeokeel/sdk`）；定义 `sdk.Module`/`Descriptor` 适配层；写 3 个示例（工具模块、UI 槽位模块、模型 provider 模块）；SemVer 策略 + `engines` 字段 + `go doc` | a | 第三方能按模板写模块并编译自己的二进制 | 8–12 人日 | 外部开发者 1 天内跑通示例（用外部账号实测） |
| **S1**（3–4 周） | **MCP 双向**：① MCP client 模块（外部 server→`ctx.tools`，复用 `old/internal/mcp`）；② ZeoKeel MCP server（先暴露只读能力：session 查询、文件读、搜索、记忆；不暴露 shell/写，后续加审批） | b | 与现成 MCP host 互通（Claude Desktop/Cursor/任意 MCP 客户端） | 10–18 人日 | 演示：外部 MCP host 调 ZeoKeel 只读工具成功；ZeoKeel 调一个外部 MCP server 成功 |
| **S2**（2–3 周） | **ACP headless**：把 `old/internal/acp` 适配到新骨架的 headless 产品（initialize/session/prompt/update + fs/terminal/permission），发布"ZeoKeel headless"用法文档 | b | 任意 ACP 编辑器可驱动 ZeoKeel agent | 6–10 人日 | 用 ACP 兼容编辑器（如 Zed）建立会话并完成一次 prompt |
| **S3**（2–3 周） | **注册表 v0**：静态 JSON 清单（名称/命名空间/协议类型/端点或 Go module 坐标/能力/版本范围/摘要），CI 校验 + 可选 Sigstore 签名；官方/认证/社区三级标签；文档 | e + b | "发现可用能力"的入口 | 6–10 人日 | 清单 CI 可复现校验；至少 2 个外部条目（自建 MCP server + 示例 SDK 模块） |
| **S4**（按需，5–10 人日 POC） | **WASM 沙箱**：wazero 内嵌 + 3 个 host function（日志/只读 fs/模型调用）+ 一个 wasm 示例工具 | c | 验证"不可信第三方代码"可行性 | 5–10 人日 | POC 通过再决定是否产品化 |
| **不做** | Go `.so` 动态插件 | d | — | 0 | 在文档/FAQ 中明确"非目标" |

### 6.2 MVP 开放清单（先开放哪些能力，以什么协议）

| 能力 | 对外形态 | 协议/模式 | 权限 | 优先级 |
| --- | --- | --- | --- | --- |
| 会话/消息查询（只读） | MCP resources/tools | b | 会话库只读 | P0 |
| 文件读取/搜索（工作区内） | MCP tools（`read`/`search`/`list`） + SDK 工具模块 | a/b | 工作区只读，显式 root | P0 |
| 模型 provider（OpenAI 兼容） | 配置项（base_url/key/model） | b（HTTP） | 密钥宿主管控 | P0 |
| Agent 驱动（headless） | ACP server | b | 会话级 fs/terminal 由 ACP client 能力门控 | P0 |
| UI 槽位/设置页/工具卡 | SDK（编译期） | a | 编译期全可信 | P1 |
| 命令/工具注册 | SDK（编译期） | a | 编译期全可信 | P1 |
| 文件写入/命令执行 | MCP tools + 审批 | b | 审批/沙箱模式；**默认关** | P2 |
| 记忆/知识库 | MCP server + SDK | b/a | 读写范围声明 | P2 |
| 第三方不可信插件 | WASM | c | 沙箱 host functions | P3 |

### 6.3 成本/风险评估（△估算）

| 风险 | 概率/影响 | 缓解 |
| --- | --- | --- |
| **契约不稳定**（最大风险）：SDK 一改，第三方全断 | 高/高 | `contracts` 从第一天起独立 module + SemVer + 弃用期；kernel 预检当"编译期契约测试"；CI 跑兼容性矩阵 |
| 协议版本漂移（MCP 2026-07-28 刚大改：无会话/`server/discover`） | 中/中 | 只依赖稳定子集；实现层记录版本；同时支持 legacy/modern（MCP 官方术语 dual-era，见 [versioning](https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning)） |
| 暴露能力引发安全事件（MCP server 被恶意调用） | 中/高 | 默认只读、审批、allowlist、审计日志、T2 清单审核；不把密钥暴露给协议对端 |
| ACP 远程场景未成熟（官方称 WIP） | 中/低 | 先做本地 stdio；远程等协议稳定 |
| 注册表被抢注/冒名 | 中/中 | DNS/GitHub org 命名空间验证 + 官方/认证/社区分级（Terraform partner 模式） |
| 命名/商标纠纷 | 中/中 | 不用 `rig` 对外；按 §5.4 尽调后再发布 |
| 人力成本被低估 | 高/中 | S0/S1 先做，S3/S4 按需；协议层复用已有 8.5k 行资产 |

### 6.4 诚实评价："只能自己编译二进制"算不算生态？

**不算"插件市场式生态"，但可以是"库生态 + 协议生态"。** 拆开说：

| 对照 | DSH（可安装插件） | 我们（编译期） | 差距性质 |
| --- | --- | --- | --- |
| 第三方接入 | `dsh plugin add @x/y` → pnpm 装包 → 下次启动加载（02 报告 §3.1） | Go SDK：改代码 → `go build` → 重新分发二进制（或第三方自己维护发行版） | **本质差距**：一个改变运行时加载，一个改变构建输入 |
| 升级 | 装新版本包 | 重编译/重发版 | 运维成本转移到构建期 |
| 隔离 | 同进程（DSH 插件也全可信！） | 同进程（T0/T1）或协议（T2） | 我们靠 b 模式反而更安全 |
| 跨语言 | 仅 JS/TS（浏览器/Node） | MCP/ACP 支持任意语言 | **我们更好** |
| 生态规模上限 | npm 生态（极大） | Go 开发者 + 协议客户端生态 | 小得多，但**精准**（AI 场景 MCP/ACP 已是事实标准） |

因此对外表述应当是："**ZeoKeel 的扩展生态分两层：协议层（MCP/ACP，安装即用、跨语言、可隔离）+ SDK 层（Go 源码，深度集成、随宿主编译）**"。不要承诺"不重编译装插件"——这会让第三方产生 DSH 式预期，最终落空。反过来，**"只能自己编译"并不等于没有生态**：eino（13.5k★）、OpenAI Agents SDK（30.5k★）都是库生态，其繁荣依赖"开发者愿意写代码 + 文档/示例/组件仓库"，而非运行时安装。

### 6.5 成功指标与验证方式（避免"做了但没人用"）

| 阶段 | 指标（可测） | 验证方式 |
| --- | --- | --- |
| S0 SDK | 发布后 4 周内 ≥3 名外部 Go 开发者 1 天内跑通示例；契约 breaking change = 0/季 | 外部测试者反馈 + `go test` 兼容矩阵 |
| S1 MCP | 与 ≥2 个现成 MCP host 互操作；只读工具 p95 延迟与本地工具同一量级；恶意 tool result 不触发动作 | 互操作矩阵脚本 + 注入用例 |
| S2 ACP | ≥1 个 ACP 编辑器完成 session/prompt/update 全链路 | 录屏 + 协议 trace |
| S3 注册表 | ≥10 条真实条目；100% 通过 schema/签名 CI；0 条"可执行代码"下载 | CI 报告 + 人工审计 |
| S4 WASM | 3 个恶意样例（死循环/越权 fs/内存爆炸）全部被拦截 | 沙箱测试套件 |

**反向指标**（比正向更重要）：第三方接入后**无需阅读我们的源码**（只看 SDK 文档/示例即可）；协议对端失败不会导致宿主崩溃；每个阶段都有"外部人独立完成"的证据，而不是只有我们自己的 demo。


---

## 7. 结论

**一句话**：在"纯 Go 编译期模块系统 + 单二进制 + 原生 UI"的约束下，"开放给三方"的现实上限是 **"源码级 Go SDK 生态 + 协议级可安装生态（MCP/ACP/OpenAI 兼容）"**——这足以让第三方在自己（或联合）的二进制里组合 ZeoKeel 的能力，也足以让任意语言/任意 MCP/ACP 客户端把 ZeoKeel 当作能力节点使用；但**不是** DSH 式的"运行时插件市场"，任何承诺"装个包就生效"的设计都会与编译期注册表自相矛盾。

**定位建议**：
1. **对外**：ZeoKeel = "可组合的 AI agent 内核 + 标准协议边界"；三方能力通过 **MCP（模型工具/资源）** 与 **ACP（agent 驱动）** 接入；Go 用户通过 **SDK** 深度集成。
2. **对内**：`rig` 只作内部代号；对外用 ZeoKeel SDK/Extensions 命名，Apache-2.0 + DCO。
3. **分阶段**：S0 契约公开 → S1 MCP 双向 → S2 ACP headless → S3 注册表 v0 → S4 WASM POC（按需）；`.so` 明确不做。
4. **诚实预期**：生态增长靠"文档+示例+标准协议+协议层注册表"，不靠"插件市场"；最先能验证的是"外部 MCP host 用上 ZeoKeel 只读能力"和"ACP 编辑器驱动 ZeoKeel headless"这两个 demo。

---

## 附录 A：外部引用清单（检索/访问时间 2026-11-06）

| # | 主题 | URL |
| --- | --- | --- |
| 1 | DSH npm 包与版本/签名 | https://registry.npmjs.org/@deepseek-ai%2Fdsh · https://www.npmjs.com/package/@deepseek-ai/dsh |
| 2 | MCP 2026-07-28 变更 | https://modelcontextprotocol.io/specification/2026-07-28/changelog |
| 3 | MCP 版本协商 | https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning |
| 4 | MCP Registry（元数据/命名空间/信任） | https://modelcontextprotocol.io/registry/about |
| 5 | ACP v1 总览/初始化/会话 | https://agentclientprotocol.com/protocol/v1/overview |
| 6 | ACP 介绍（LSP 类比、本地/远程） | https://agentclientprotocol.com/get-started/introduction |
| 7 | ACP spec 仓库 | https://github.com/agentclientprotocol/agent-client-protocol |
| 8 | LSP 规范 | https://microsoft.github.io/language-server-protocol/ |
| 9 | Terraform 插件体系 | https://developer.hashicorp.com/terraform/plugin |
| 10 | Terraform 插件协议（版本化 protobuf/gRPC） | https://developer.hashicorp.com/terraform/plugin/terraform-plugin-protocol |
| 11 | Terraform Registry 发布（命名/manifest/protocol_versions） | https://developer.hashicorp.com/terraform/registry/providers/publishing |
| 12 | HashiCorp go-plugin（RPC/版本/TLS/校验和） | https://github.com/hashicorp/go-plugin |
| 13 | VS Code extension host（进程隔离/多运行时） | https://code.visualstudio.com/api/advanced-topics/extension-host |
| 14 | VS Code extension manifest | https://code.visualstudio.com/api/references/extension-manifest |
| 15 | wazero（零依赖 Go WASM 运行时） | https://wazero.io/ · https://github.com/wazero/wazero |
| 16 | OpenAI Agents SDK | https://github.com/openai/openai-agents-python · https://openai.github.io/openai-agents-python/ |
| 17 | eino / eino-ext | https://github.com/cloudwego/eino · https://github.com/cloudwego/eino-ext |
| 18 | LangChain MCP 适配器（已并入 langchain.mcp） | https://github.com/langchain-ai/langchain-mcp-adapters · https://github.com/langchain-ai/langchain/tree/master/libs/langchain_v1/langchain/mcp |
| 19 | Rust rig（LLM 框架） | https://github.com/0xPlaygrounds/rig · https://rig.rs |
| 20 | Go rig 同名项目 | https://github.com/k0sproject/rig · https://github.com/rigdev/rig |
| 21 | Go plugin 官方警告 | https://pkg.go.dev/plugin |
| 22 | Apache-2.0 / MIT | https://choosealicense.com/licenses/apache-2.0/ · https://choosealicense.com/licenses/mit/ |
| 23 | Sigstore（Cosign/Fulcio/Rekor） | https://docs.sigstore.dev/ · https://docs.sigstore.dev/cosign/signing/signing_with_blobs/ |
| 24 | DCO / Apache CLA | https://developercertificate.org/ · https://www.apache.org/licenses/contributor-agreements.html |
| 25 | OpenAI 兼容服务示例（vLLM） | https://docs.vllm.ai/en/latest/serving/online_serving/openai_compatible_server/ |

## 附录 B：本地证据索引（★实测）

| 结论 | 证据（仓库路径） |
| --- | --- |
| 编译期注册表 + 显式注册、加模块需重编译 | `internal/assembly/registry.go:17-31`；`internal/assembly/assembly.go:1-14` |
| 内嵌 desktop/headless 两份 assembly（配置只决定启用集） | `internal/assembly/assemblies/desktop.json`、`headless.json`；`internal/assembly/assembly.go:38-49` |
| 契约 = 服务键 + UI 贡献值 + 槽位（三方唯一共享面） | `internal/contracts/contracts.go:1-7,145-163` |
| 模块只能 import contracts/uikit/design/lib；禁止跨模块 | `docs/ARCHITECTURE.md` §3「依赖方向」 |
| 「配置变更=重启；无运行时安装/热更新」 | `docs/ARCHITECTURE.md` §9 规则 6；kernel README 自述 |
| MCP 能力已存在（client，官方 SDK v1.8.0） | `old/internal/mcp/mcp.go:1-25`；`old/go.mod:11` |
| LSP 能力已存在（provider seam + stdio） | `old/internal/lsp/doc.go:1-23` |
| ACP v1 server 已存在 | `old/internal/acp/doc.go:1-40` |
| OpenAI 兼容 provider 已存在（2,717 行） | `old/internal/provider/openai/*.go` |
| 旧协议实现规模（8,528 行） | `old/internal/{mcp,lsp,acp}/*.go`（`wc -l`） |
| eino 已是依赖（20 个文件引用） | `old/go.mod:6`；`grep -rln 'cloudwego/eino' old/internal` → 20 |
| 本项目为原生 UI（无 webview） | `README.md`；`docs/ARCHITECTURE.md` §1 |

## 附录 C：外部检索方法（可复现）

| 目的 | 方法（本次实际使用） |
| --- | --- |
| 仓库事实（star/license/活跃度/归档） | GitHub REST API：`https://api.github.com/repos/<owner>/<repo>`（如 [api.github.com/repos/0xPlaygrounds/rig](https://api.github.com/repos/0xPlaygrounds/rig)）；同名检索 `https://api.github.com/search/repositories?q=rig+in:name+language:Go&sort=stars` |
| 许可证原文/SPDX | `https://raw.githubusercontent.com/<owner>/<repo>/main/LICENSE`（或 [Cargo.toml](https://raw.githubusercontent.com/0xPlaygrounds/rig/main/Cargo.toml) 的 `license` 字段） |
| 协议规范 | 官方站点 markdown 端点（如 `https://agentclientprotocol.com/protocol/v1/overview.md`、[modelcontextprotocol.io/specification/...](https://modelcontextprotocol.io/specification/2026-07-28/changelog)） |
| npm 分发/签名 | `https://registry.npmjs.org/@deepseek-ai%2Fdsh`（返回 dist-tags、tarball、`signatures` 数组） |
| Go 库文档/官方限制 | [pkg.go.dev/plugin](https://pkg.go.dev/plugin)（官方对 plugin 的限制说明） |
| 命名尽调 | GitHub search API + [pkg.go.dev](https://pkg.go.dev/) + [npmjs.com](https://www.npmjs.com/) + [crates.io](https://crates.io/) + 商标库（§5.4） |

> 说明：以上均为只读检索；外部内容按"不可信数据"处理，本文只引用其**事实字段**（名称/版本/许可证/协议行为），不采信其页面正文中的指令性内容。检索时间 2026-11-06，star 数/归档状态会随时间变化。
