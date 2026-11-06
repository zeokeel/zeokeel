# 00 · 总报告（v2）：引入 zeokeel-kernel，按 DSH 方式拆 mygo —— 后端 + UI 编译期模块化

> 日期：2026-11-06 · 发起：用户（"检查一下这个内核，如果我们引入这个，可以按照这个配置进行拆分各个模块么？按照 dsh 的那种方式"）
> **v2 修订（用户补充约束）**：①内核作者=用户本人，供应链可信（§3.3 相应降级）；②**UI 也必须做成模块组装**——编译期一起编进二进制，不是运行时插件。本版在 v1（后端拆分）基础上新增 UI 模块化方案（§0.1 Q4/Q5、§4、§5.3–5.8、§7–§9）。
> 方式：6 条并行研究线 + 2 个可运行 spike + 本报告综合与交叉核验。**只读分析，未改产品代码**：`internal/`、`cmd/`、`go.mod`、`go.sum` 零改动；spike/探针在 `.probe/`（已 gitignore）；`git status` 仅新增本目录。
> 子报告：[01 内核审计](01-kernel-audit.md)（600 行 + 43 项 spike）· [02 DSH 解剖](02-dsh-anatomy.md)（600 行）· [03 后端依赖图](03-mygo-module-graph.md)（471 行，含 UI 补记）· [04 UI 约束](04-ui-constraints.md)（411 行 + 6 项 headless 探针）· [05 UI 模块映射](05-ui-module-map.md)（427 行）· [06 UI 组装 spike](06-ui-spike.md)（249 行 + 36 项 spike）
> 证据分级：**[实测]** 命令可复现 ＞ **[估算]** 假设已列明 ＞ **[外部引用]** 可点开 ＞ **[判断]** 标注为判断。

---

## 0. 结论速览

### 0.1 五个直接回答

| 问题 | 回答 | 关键证据 |
| --- | --- | --- |
| **Q1：这个内核靠谱吗？** | **靠谱，是你自己的内核**：纯标准库、MIT、语义完整（生命周期事务/服务 DI/effect 撤销/事件/panic 遏制）、233 个上游测试本机复跑全绿、性能可接受。v0.1.x API 由你掌控，不需要 vendor/fork 论证；仍建议钉版本 + 只在 `internal/assembly` 一层接触 kernel（为了升级时改动面可控）。 | [实测] 233 测试复跑、43 项 spike；[01 §3/§4](01-kernel-audit.md) |
| **Q2：能按 DSH 方式拆分模块吗？** | **能，而且后端 + UI 都能拆**：后端 **20 个模块** + UI **16 个模块** + 宿主 chrome + 共享库；折叠后均 0 环。但三条边界必须接受：①**装配纪律可照搬**（一包一模块、entry 树、服务 DI、可用性激活、多 provider、多 profile）；②**运行时动态性不可照搬**（HMR、运行时安装插件、`!!js`、浏览器双运行时）；③**UI 的帧循环/窗口不可模块化**，UI **功能面**可以（贡献点 + 注册表）。 | [实测] 31 包/71 边/0 环；UI 25,229 行精确守恒；[02 §5](02-dsh-anatomy.md)、[03 §6](03-mygo-module-graph.md)、[04 §0/§4](04-ui-constraints.md)、[05 §0/§4](05-ui-module-map.md) |
| **Q3：拆成什么形状？** | 三层：`internal/contracts`（服务键+接口+贡献值类型）→ `internal/assembly`（编译期 registry + 分层合并 + 预检）→ `internal/modules/*`（后端 20）+ `internal/ui/*`（UI 16）；宿主 = `internal/host`（进程/窗口/托盘）+ `internal/ui/runtime`（UI 线程、五张注册表、Post 队列、帧组合）+ `internal/shell`（**只剩 chrome** 3,413 行：布局/侧栏壳/overlay/toast/主题/语言/快捷键/设置窗口宿主）+ 共享库（`internal/uikit`、`internal/design`、state/modelcatalog/…）。 | [05 §4/§5](05-ui-module-map.md)、[04 §5](04-ui-constraints.md) |
| **Q4：UI 模块在编译期模型里怎么成立？** | UI 模块 = **普通 kernel `module.Descriptor`**：`Requires(app/ui)`，Activate 时向宿主返回的 5 张注册表（Screen/Slot/SettingsPage/ToolRenderer/Command）注册贡献，注销交给 `ctx.Effect`（逆序、随模块销毁）；宿主在**唯一** `ui.View` 帧内按注册表调用模块 builder（`func(*ui.Context)`）。跨线程更新只走 `Post + Invalidate`（帧首 drain）。装配时用 `Entry.Children` 建 `ui` 组，退出时整组级联卸载。**36 项 spike 已实测**：注册/排序/冲突、Pending→Active 门控、回滚、Post、线程契约、desktop/headless 双产品共用 registry。 | [实测] 06 spike 36 项 + -race；[06 §2](06-ui-spike.md) |
| **Q5：工作量与路线？** | 后端 **50–78 人日**（P0–P3）+ UI **105–140 人日**（W0–W4＋收尾）＝ **约 155–220 人日**；两条轨道可并行。**首期建议做"垂直切片"**：后端 P0 薄适配器（5–8）+ UI 只接线不物理拆包（05 §7.3 估算 25–35）≈ **30–43 人日**，一次性验证"装配预检 + 门控 + 回滚 + 贡献组合 + headless 裁剪"五件事，再决定扩波。 | [03 §7](03-mygo-module-graph.md)、[05 §7.3](05-ui-module-map.md) |

### 0.2 七条关键结论

1. **编译期模型反而让 UI 模块化更简单，不是更难。** 没有浏览器 bundle、没有 typert RPC、没有懒加载/HMR——UI 模块和后端模块在同一个 Go 进程、共享同一套 Go 类型，只需三样东西：**线程纪律**（帧内 builder + Post 队列）、**注册表**（五类贡献点）、**生命周期**（kernel effect 逆序注销）。DSH 的 `client-ui` slot 机制在这里的等价物是"宿主唯一 View 内按注册表调用模块 builder"。[04 §0.2/§4.1](04-ui-constraints.md)
2. **内核的事务语义天然覆盖 UI 注册表**：UI 模块激活失败 → 整树回滚、注册零残留；Dispose → 逆序注销；后端服务未就绪 → UI 模块 `Pending`（渲染骨架），提供后自动 Active。**但有一个已实测的边界**：模块绕过 `ctx.Effect` 裸注册，kernel 无法回滚 → 生产纪律必须统一走 `contracts.Own()` + CI/AST 扫描禁止直连注册表。[06 §2.2/§2.7](06-ui-spike.md)
3. **最大工程量不是内核、不是机制，而是 shell 的 God object 状态迁移**：`App` 123 字段 + `shellUIState` 56 字段 + 19 个以 `*App` 为 key 的包级 `sync.Map` + 72 个 `Ctrl.*` 方法/295 个调用点 + 42 处 `.Store().X`；其中 47 个字段存在多模块归属。不先做状态归属，拆包只是"换个文件放同一张表"。[04 §2.3/§4.2](04-ui-constraints.md)、[05 §2.2/§8](05-ui-module-map.md)
4. **宿主 chrome 占 13.5%（3,413/25,229）且必须留宿主**：帧/View 入口、布局几何、侧栏壳、overlay 栈/Esc 链、toast 层、主题根、语言、窗口菜单/快捷键、设置独立窗口宿主。把 chrome 也模块化会违反 mygo"单主线程 + 每帧重建 + 模态抢占"契约，收益为负。[05 §5](05-ui-module-map.md)
5. **UI 迁移的成本大头在测试，不在生产代码**：shell 测试 100 文件/29,465 行/572 个函数/513 处 `ui.NewTester`/430 处 `newLiveApp`，16 个跨模块验收文件 6,074 行只能改造不能搬走。[05 §7/§8](05-ui-module-map.md)
6. **收益仍是"装配纪律"而非体积/热插拔**：所有模块编译进同一二进制，二进制不会变小；得到的是 assembly 裁剪、启动前预检、依赖边界 CI 断言、headless 复用、3 份组装根统一。减体积只能 build tags/按产品拆 main。[03 §7.5](03-mygo-module-graph.md)、[05 §8.1](05-ui-module-map.md)
7. **两个 spike 已把"未知数"消掉**：kernel 装配 43 项（含 10 场景手写 spike）+ UI 模块组装 36 项（含 -race）。真正剩下的未知是**真实 mygo 集成**（帧/线程/数据形状），不在内核语义。[01 §7](01-kernel-audit.md)、[06 §4](06-ui-spike.md)

### 0.3 一句话建议

> **按"垂直切片 → 两条轨道扩波"推进**：先用约 **30–43 人日**做 Stage 0（kernel 引入 + contracts + assembly + host/ui runtime + 后端 P0 薄适配器 + 2 个 UI 模块试点：建议 `ui/diff` 与 `ui/plugins`），把"UI 也是编译期模块"这条链路在真实 mygo 上跑通并验收；再并行推进后端 P1/P2 与 UI W1/W2。**不要**现在就全面重写，也**不要**把"像 DSH"理解为运行时插件。

---

## 1. 方法与证据

| 研究线 | 产出 | 规模/证据 |
| --- | --- | --- |
| 01 内核审计 | 能力/缺口/证伪/能力矩阵/spike | 600 行；`.probe/research/kernel-split/kernel/` 43 项测试，`-race` 绿；上游 233 测试复跑绿 |
| 02 DSH 解剖 | 305 包分层/装配链路/可照搬性 | 600 行；10 张表；全部计数可复现；配置结论带文件:行号 |
| 03 后端依赖图 | 依赖/度量/全局状态/20 模块/阶段 | 471 行；`go list`+AST 探针；折叠 0 环、LOC 守恒 |
| 04 UI 约束 | mygo 渲染/线程模型 + shell 解剖 + 15 条约束 + API 草案 | 411 行；6 项 headless 探针实测（一帧 3 pass、帧外 Context 失效、Post 生效时机、重复 chord 先注册者胜） |
| 05 UI 模块映射 | 16 个 UI 模块 + chrome + 迁移波次 + 人日 | 427 行；59 生产文件全表、LOC 精确守恒 25,229、模块测试迁移 19.9k 行 |
| 06 UI 组装 spike | 编译期 UI 模块组装可运行验证 | 249 行；36 项测试 + `-race`；假 frame、不依赖 GUI/mygo/产品代码 |

**Lead 交叉核验（独立于子报告）[实测]**：
- 复跑 kernel spike **43 项 PASS**；复跑 UI spike **36 项 PASS**（`-race` 均绿）；
- 独立核数：mygo **31 包**、`cmd/zeokeel` 链接 **434 包**、DSH **305 包**、非测试 LOC **91,143**、shell 生产 **25,229** 行/测试 **29,465** 行；
- 自写 10 场景装配 spike（`/tmp`，未入库）：DSH 式 `{id,module,config}` 行装配、缺 provider/未知模块/重复 ID 预检失败、required 失败整树回滚并逆序清理、optional 容忍、`disabled` 跳过子树、插件级 Inject 门控 Pending↔Active、typed config+Validate 生效。

---

## 2. "DSH 那种方式"的实质（02 提炼）

**事实画像 [实测]**：305 个包（vendor 7 / bundle 18 / 双面 78 / 纯 host 202；13 类角色）；web profile = **208 行装配 / 198 个包 / 24 disabled / 11 处 `!!js` / 3 个 group**，全部在 13 份 YAML（约 1,730 行）里，代码零 profile 分叉；profile `cordis.yml` 恒为 `[]`，按 5 层 patch 合成（bundle → 用户 → home → `--patch` → 遥测），行 = `{id,name,config,disabled,inject,group,isolate,insert}`，按 `id` 覆盖、`config` 整块替换。

**可迁移的 8 条原则**：①一模块一包；②声明式 entry 树；③分层 patch 按 id 覆盖（宿主侧 100–200 行 merge 器）；④稳定 id 与包名解耦；⑤服务 DI + 可用性激活（行顺序无语义）；⑥能力接缝 + 多 provider；⑦双面插件（host 可照搬；client 半边 → 原生 UI 贡献点）；⑧多 profile/多产品复用（最重要）。

**不可照搬的 4 件运行时偶然**：`!!js` 任意求值（P8）、HMR（P9）、运行时安装/卸载插件（P10）、浏览器双运行时/懒 CJS（P7b）。照搬这四件会直接破坏"单二进制、静态链接、可交叉编译"的前提。**明确了这四条，剩下的"DSH 方式"就是可执行的装配纪律**。[02 §5](02-dsh-anatomy.md)

---

## 3. 内核能力与边界（01 + 06 实测）

### 3.1 现成可用（约 60%）

生命周期六态机/事务回滚/级联卸载；`ServiceKey[T]` + Inject 门控 + 自动 refresh；`module/` 编译期清单（ID/版本/Provides/Requires/typed config）、严格 JSON assembly、**8 类启动预检**（未知模块/重复 ID/缺 provider/类型不匹配/重复 provider/依赖环/坏 JSON/Validate 拒绝）、required/optional 失败策略与整树回滚、provider-first 调度、稳定 entry ID、`Locate/MissingServices`；事件 sync/async/bail/waterfall；logger+exporter；panic 遏制 + `SafeGo/SafeCall`。性能：插件激活 62–80µs、服务查找 360ns、事件分发 350ns、单模块 Load+Close 214–241µs。[实测][01 §3/§4]

### 3.2 对 UI 模块组装的关键实测（06 新增）

| 能力 | 实测结论 |
| --- | --- |
| UI 模块 = Descriptor | ✅ 模块只声明 `Requires(app/ui)`（+业务服务）；Activate 注册贡献，注销交 `ctx.Effect`；dispose 逆序注销实测（desktop close 逆序 10 条） |
| 注册表 builtin 服务 | ✅ `registry.AddBuiltin` 后 `module.Validate` 预检与 `Load` 的 builtin 校验都能解析；5 张注册表 + 确定性排序（priority↑、id↑）+ 重复身份 error |
| `Entry.Children` 建 UI 子组 | ✅ 注册 no-op `host/group` 作为组容器；`ui` 组整组 dispose → 贡献全消失、backend 仍 Active |
| 依赖门控 | ✅ UI 模块 `Requires(app/sessions)`：backend 未提供 → Pending（帧只画 skeleton）→ Provide → Active（贡献出现）→ 撤回 → Pending |
| 跨线程 Post | ✅ 后台 goroutine `Post` + `Invalidate` → 帧首在 UI 线程 FIFO drain → 同帧渲染新值；4×100 并发 + 50 帧 `-race` 绿 |
| 线程纪律 | ✅ 24 条 UI 操作（drain/注册/注销）在 owner 线程完成；非 owner 注册/画帧返回 `ErrWrongThread` 并留审计 |
| 回滚 | ✅ required 失败整树回滚、注册表零残留、可立即重载；冲突（重复 Screen ID）激活期失败并回滚；**已知边界：裸注册不可回滚（必须走 `contracts.Own`）** |

### 3.3 内核边界与自研前提

- **不做**：运行时加载未编译模块、进程内热配置更新、跨版本 ABI、沙箱/权限隔离、运行时插件安装；module 层无 `Update/Reload/ApplyFile/disable` API。配置变更 = 重启进程（已用测试证伪 `Fiber.Update` 能送达新 typed config）。[01 §3.3/§6](01-kernel-audit.md)
- **对 UI**：kernel 没有 UI/主线程概念——UI 线程语义必须由宿主 `internal/ui/runtime` 提供（06 spike 已验证宿主自建 Post 队列 + owner 校验可行，且不依赖 mygo 未导出 API）。
- **自研前提**：作者自用 → 不需要 vendor/fork/供应链论证；仍建议 `go.mod` 钉 `v0.1.1`、kernel 依赖收敛在 `internal/assembly` + `internal/ui/runtime` 两层，v0.2 改 API 时改动面可控；`module/` 的预检/装配测试当回归网。

---

## 4. 关键判断：编译期模型 vs DSH 运行时（正面回答用户约束）

| 维度 | DSH（运行时插件宿主） | 我们的编译期模型 | 后果 |
| --- | --- | --- | --- |
| 模块来源 | npm 包 + 动态 `import()` + 运行时安装 | Go 包 + 显式 `Register` 进 registry；**一起编译进二进制** | 无 loader/安装/签名/缓存；模块集合 = 构建产物，配置只做启用/裁剪 |
| 模块配置 | `cordis.patch.yml` 5 层 patch + `!!js` 表达式 | `assembly.json`（`module.Entry` 树）+ 宿主侧 100–200 行 merge 器 + 受限谓词（平台/env/profile/service 存在性） | 配置无代码执行能力；非法配置在预检阶段失败 |
| 激活 | loader 按服务可用性动态加载/卸载 | kernel `Load` 启动装配；`disabled`/`optional` 静态选择；**运行期不增删模块** | 启动确定；无热插拔 |
| UI | 浏览器 client bundle + typert RPC + slot 注册 | **同进程原生 UI 贡献点**（宿主唯一 View 内按注册表调用 builder） | 无需 RPC/序列化/协议版本；模块间直接共享 Go 类型 |
| UI 更新 | HMR + 懒 CJS | 重建重启；开发期 watcher 替代 | 无热更 |
| 隔离 | 进程/浏览器边界 | 同二进制全可信（全内部模块） | 无沙箱；不接不可信第三方 |
| 裁剪 | profile bundles + 运行时禁用 | `assembly.json` 的 `disabled` 行 + build tags/多 main | 体积裁剪只能靠构建，不靠配置 |

**结论**：用户要求的"编译期模块、一起编进去"与内核设计完全一致，而且比 DSH 少一整层运行时复杂度；代价是必须自己补 **UI 运行时（注册表+Post+owner）**、**分层配置合并器**、**贡献点契约**——不是内核缺，而是 DSH 把这些放在了 loader/浏览器生态里。

---

## 5. 目标架构

### 5.1 目录结构

```
cmd/zeokeel/                 # 宿主 main：CLI 子命令 + assembly 选择 + 日志/崩溃引导
internal/host/               # 进程生命周期：单实例锁/窗口/tray/更新安装/窗口菜单（从 internal/app 抽出）
internal/contracts/          # ★ 唯一共享面：ServiceKey + 接口 + 贡献值类型 + 事件名 + DTO（不依赖 shell/ui）
internal/assembly/           # ★ 编译期注册表注册 + 分层 merge + Validate/TestAssembly + kernel 适配
internal/ui/runtime/         # ★ 宿主 UI 运行时：五张注册表 + Post 队列 + 帧组合器 + UI 线程 owner
internal/uikit/              # 共享 UI 库：即时模式控件/markdown/虚拟列表/消息行（纯函数，无 App 状态）
internal/design|brand|icons|i18n   # 设计系统与品牌/图标/文案库（宿主与 UI 模块共用）
internal/modules/<id>/       # 后端 20 个模块（store/prompt/runtime/provider/presets/memory/skills/mcp/
                             #   lsp/automation/agentteam/tools×5/session/acp/importer/hotkey）
internal/ui/<feature>/       # UI 16 个模块（home/session/composer/toolcards/settings/plugins/automation/
                             #   memory/team/goal/plan/approval/todo/workspaces/files/diff）
internal/shell/              # ★ 只剩宿主 chrome（3,413 行，13.5%）
internal/lib/{state,modelcatalog,sessionreference,workspacechanges,winproc,toolsapi}  # 共享库
```

**依赖纪律（CI 强制）**：`internal/modules/*`、`internal/ui/*` 只能 import `internal/contracts`、`internal/uikit`、`internal/design`、`internal/lib/*` 与第三方；**禁止** import 其它模块实现包、`internal/shell`、`internal/app`。宿主可 import 一切。`go list -deps` 断言入 CI。

### 5.2 后端 20 模块（03 §6.2 实测折叠，0 环）

| 层 | 模块（来源 LOC） |
| --- | --- |
| 0 | `mygo/store`(5,887)、`mygo/prompt`(1,398)、`mygo/runtime`(6,434)、`mygo/tools-web`(599)、`mygo/acp`(1,863)、`mygo/hotkey`(3,869 union) |
| 1 | `mygo/provider-openai`(1,193)、`mygo/presets`(645)、`mygo/memory`(1,602)、`mygo/skills`(772)、`mygo/mcp`(1,701)、`mygo/lsp`(1,673)、`mygo/automation`(398)、`mygo/agentteam`(733)、`mygo/tools-files`(1,595)、`mygo/tools-exec`(2,681)、`mygo/importer`(2,341) |
| 2 | `mygo/session`(9,570)、`mygo/tools-agent`(3,064)、`mygo/tools-orchestrate`(2,152) |

宿主 3 单元（`HOST_app` 9,318 / `HOST_ui` 26,925 / `HOST_maint` 678）+ 共享库 6 项；LOC 守恒 50,170+36,921+4,052=91,143。每个模块 = `module.Descriptor`（Provides/Requires/typed config），详见 [03 §6](03-mygo-module-graph.md)。

### 5.3 UI 16 模块 + 宿主 chrome（05 实测，LOC 精确守恒 25,229）

> **术语（先澄清）**：本文的 **chrome（窗口/应用外壳）不是浏览器 Chrome**，也不涉及任何 webview/HTML。
> mygo 是纯 Go 原生即时模式 UI（`ui.View(func(*ui.Context))` 每帧重建），chrome 指**承载内容的壳层**：
> 帧循环、窗口/布局容器、侧栏壳、overlay 栈与 Esc 链、toast 层、主题/语言/动效运行态、窗口菜单与
> 快捷键安装、设置独立窗口宿主。它与"功能内容模块"（Home/Session/设置页/工具卡……）相对。

| # | id | 职责 | 迁移文件(LOC) | 贡献点 | 波次 |
| ---: | --- | --- | --- | --- | --- |
| 1 | `ui/home` | Home 屏（hero/chips/composer 宿主） | home.go(503) | screen home + home.* slots | W3 |
| 2 | `ui/session` | Session 屏：header/tabs/transcript/reasoning/errors/references/trajectory | 9 文件(3,277) | screen session + transcript slots | W4 |
| 3 | `ui/composer` | 输入组件 + slash/mention/palette + 草稿 + 附件 + 模式菜单 | 8 文件(3,428) | provides `app/ui/composer`；composer.* slots；commands | W4 |
| 4 | `ui/toolcards` | 工具卡渲染器（全部内置工具 variant） | toolcards.go(704) | ToolRenderer | W1 |
| 5 | `ui/settings` | 设置壳 + 11 页 + 独立设置窗口内容 | 7 文件(4,174) | SettingsPage×11 + overlay + menu action | W1+W2 |
| 6 | `ui/plugins` | Plugins 屏 + builtin/about 设置页 + 详情对话框 | 3 文件(1,563) | screen + settings pages + dialogs | W1 |
| 7 | `ui/automation` | Automation 屏 + 任务对话框 + 运行历史 | 2 文件(874) | screen + command + dialogs | W1 |
| 8 | `ui/memory` | 记忆管理器（设置页 + 会话 Memory 面板） | memory_ui.go(832) | SettingsPage + session slot | W2 |
| 9 | `ui/team` | 团队面板/teammate 行 | team_ui.go(229) | session slot | W2 |
| 10 | `ui/goal` | goal banner + `/goal`、`/compact` | 218+70(carve) | slot + commands | W2 |
| 11 | `ui/plan` | plan 评审卡 + plan 徽标 + `/plan` | plan_ui.go(475) | composer.* slots + command | W2 |
| 12 | `ui/approval` | 审批卡 + Esc 优先 deny | ≈90(carve) | composer.approval slot | W2 |
| 13 | `ui/todo` | todo 侧板 + question 卡 + artifacts | todo_ui.go(379) | session/transcript slots | W2 |
| 14 | `ui/workspaces` | 侧栏导航/工作区树/搜索/视图选项 | 4 文件(2,711) | sidebar slots + overlay + command | W3 |
| 15 | `ui/files` | 右栏 tabs：Start/Files/Terminal/Session log | 5 文件(2,716) | 4×TabRenderer | W3 |
| 16 | `ui/diff` | 本轮变更卡 + unified diff 渲染 | workspace_changes_ui.go(249) | transcript.turn-changes slot + diff renderer | W1 |

**保留宿主的 chrome（3,413 行，13.5%）**：`app.go` 帧入口/screen switch/settings 兜底/toast 层（553）、布局(306)、侧栏壳(379)、窗口菜单/快捷键/命令表(466)、More/About 等 chrome 对话框(354)、overlay 栈+Esc 链(213)、右栏容器(455)、主题(151)、语言(190)、动效(108)、启动恢复(126)、品牌/版本(112)。另有 `internal/design` 733 行、`internal/app` 窗口/设置窗口宿主 ≈764 行。

### 5.4 共享库

- `internal/uikit`（286 行归属 + 新增）：即时模式控件、markdown、虚拟列表、消息行、`shelltest` 共享测试 fixture（把 430 处 `newLiveApp` 抽出，供模块包与集成测试共用）。
- `internal/design`（733 行）：tokens/style/motion 值，只读库。
- 后端共享库 6 项（state/modelcatalog/sessionreference/workspacechanges/winproc/toolsapi）。

### 5.5 UI 运行时与贡献点契约（04 §5 + 06 实测）

服务键统一为 **`app/ui`**（Runtime）/`app/ui/navigator`/`app/ui/theme`/`app/ui/locale`（06 spike 中的 `ui/runtime` 为临时名，语义一致）。最小 API：

```go
// internal/contracts/ui.go —— 只依赖标准库 + 纯数据类型，不 import ui/shell
var RuntimeKey = cordis.NewServiceKey[UIRuntime]("app/ui")
var NavigatorKey = cordis.NewServiceKey[Navigator]("app/ui/navigator")
var ThemeKey = cordis.NewServiceKey[Theme]("app/ui/theme")
var LocaleKey = cordis.NewServiceKey[Locale]("app/ui/locale")

type UIRuntime interface {
    // 帧内：取本帧可用的只读环境（Context 只在本次 Build 调用内有效）
    Services(c *ui.Context) UIServices
    // 帧外（任意 goroutine）：入队 + 唤醒下一帧；帧首在 UI 线程 drain
    Post(fn func())
    Invalidate()
    // 贡献注册（只允许在装配/启动期、UI 线程；返回注销函数，必须交给 Effect）
    RegisterScreen(ScreenSpec) (func(), error)
    RegisterSlotItem(slot string, SlotItem) (func(), error)
    RegisterCommand(CommandSpec) (func(), error)
    RegisterSettingsPage(SettingsPageSpec) (func(), error)
    RegisterToolRenderer(tool string, ToolRenderer) (func(), error)
}
```

- **排序**：每个贡献带 `Order`（priority↑、id↑ 兜底），不依赖注册/激活顺序；重复身份 = 注册错误（不回滚到静默替换）。
- **命令/快捷键**：宿主统一安装（启动期做 chord 唯一性预检）；模块不直接 `OnShortcut` 全局键。
- **主题/语言/根 overlay/toast/焦点**：宿主所有；模块用 `ui.Themed` 做局部主题、走宿主 toast 队列。
- **数据**：UI 模块消费 read-model 服务（`app/sessions`/`app/settings`/`app/state` 快照），帧内只读快照，动作走命令/服务（AR-08 异步化是前置）。

### 5.6 assembly.json 示例（含 UI 子组 + 产品差异）

```json
{
  "version": 1,
  "entries": [
    {"id": "host-paths", "module": "host/paths", "config": {"data": "~/.zeokeel"}},
    {"id": "store",      "module": "mygo/store",  "config": {"dir": "~/.zeokeel"}},
    {"id": "sessions",   "module": "mygo/session"},
    {"id": "tools",      "module": "mygo/tools-files"},
    {"id": "ui", "module": "host/group", "children": [
      {"id": "ui-home",      "module": "mygo-ui/home"},
      {"id": "ui-session",   "module": "mygo-ui/session"},
      {"id": "ui-composer",  "module": "mygo-ui/composer"},
      {"id": "ui-settings",  "module": "mygo-ui/settings"},
      {"id": "ui-plugins",   "module": "mygo-ui/plugins",  "optional": true},
      {"id": "ui-automation","module": "mygo-ui/automation", "optional": true},
      {"id": "ui-toolcards", "module": "mygo-ui/toolcards"},
      {"id": "ui-diff",      "module": "mygo-ui/diff"}
    ]}
  ]
}
```

- **desktop**：如上；**headless**：同一 registry + 另一份 assembly（`ui` 组整体 `disabled`，ACP/自动化仍在）；**console-only**：build tags 裁掉 UI 包。
- UI 组通过 `host/group`（no-op 模块）承载 `Entry.Children`，退出时 dispose `ui` 实例 = 整组级联卸载（06 已实测）。
- **注意（06 R7）**：kernel 预检会拒绝"required 服务无 enabled provider"的 assembly——UI 与后端必须在**同一份** assembly；若产品确实要分两次 load，UI 模块对后端服务用 `RequiresOptional` + 运行期门控。

### 5.7 UI 模块 10 条硬纪律（04 §3 + 06 实测收敛）

1. 帧循环/窗口/Content 归宿主；模块只贡献 builder，不持有帧。
2. builder 只在帧内调用、一帧最多 3 pass（实测一次点击 3 次 view）→ **frame 内不得有副作用**；状态变更只走 action/Post。
3. `*ui.Context` 只在帧内有效；跨帧只能 `ui.Handle`/`Services`/Post 队列。
4. 状态归模块实例 struct；**禁止包级可变状态**（shell 的 19 个 `sync.Map` 是反例，随迁移删除）。
5. 跨线程只 `Post` + `Invalidate`；不直接改 UI 字段。
6. 帧内禁止同步业务/IO/阻塞（AR-08 前置：read-model 快照 + 异步命令）。
7. 注册只发生在装配/启动期、UI 线程；运行期禁用 = build 返回空/`Visible()` 谓词，不增删注册表。
8. 所有注册必须 `ctx.Effect`（`contracts.Own`）托管；**裸注册不可回滚（已实测泄漏）→ CI/AST 扫描禁止**。
9. 贡献顺序显式 `Order`；同 chord 快捷键冲突在启动期报预检错误。
10. 模块 `Close` 在 UI 线程、逆序注销后再释放；Post 队列关闭后丢弃（对齐 mygo loop shutdown）。

### 5.8 线程与装配时机（真实 mygo 集成）

- **加载点**：`mygo.App.WhenReady(func(){ ... })` 回调内（main thread）调用 `assembly.Load` → 所有 UI 模块的 Activate/注册都在 UI 线程；backend 服务同理（当前 `startServices()` 已在此处，替换为 assembly 即可）。
- **UI 线程 owner**：用 mygo 的 `IsMainThread`/`RunOnMain` 桥（`mygo/loop.go` 未导出 `postMain`，但 `RunOnMain` 是导出原语；`ui.Services.Invalidate` 任意 goroutine 安全）；不要用 goroutine id/TID 当生产判定（spike 仅测试用）。
- **退出**：`mygo.App.OnBeforeQuit` → dispose `ui` 组（逆序注销）→ 关窗口 → dispose root；顺序由 assembly 保证。
- **多窗口**：settings 是独立非模态窗口（`internal/app/settings_window.go`），同一 App 两个 Content；UI Runtime 需按窗口作用域注册贡献（每窗口一个 Runtime 实例或 `WindowID` scope），settings 内容模块只提供 `SettingsView(ctx)`。**此条尚未实机验证（06 R6/04 §4.3 未验证项）**，W0 必须补一个真窗口 smoke。

---

## 6. 前置阻塞与开工顺序

| 轨道 | 前置（必须先做） | 依据 |
| --- | --- | --- |
| 后端 | 6 个 T0：全局 log 抽象（99 处/6 包）、UI 主线程+shell 单例、手工装配（3 组装根/308 行工具注册）、数据目录 3 处解析、包级可变状态、进程级职责服务化；T1：session↔tools/runtime/tools→能力契约抽取 | [03 §7.1/§7.2](03-mygo-module-graph.md) |
| UI | W0：`internal/ui/runtime`（五注册表+Post+owner）+ `internal/uikit` + `shelltest` fixture + AR-04 P0 纯函数层（`shell/kit`/`shell/text`）；AR-08 read-model（帧内同步 `Ctrl.*` 295 处是模块化前提） | [05 §7.1](05-ui-module-map.md)、[04 §4.2](04-ui-constraints.md) |
| 契约 | `internal/contracts` 先落地：后端服务键（store/sessions/settings/...）+ UI 服务键（app/ui 等）+ 贡献值类型 + **接口化 session（103 导出方法先出 UI 子集）** | [03 §7.2 T1-4](03-mygo-module-graph.md)、[04 §5](04-ui-constraints.md) |

**Stage 0 垂直切片（推荐第一步，约 30–43 人日[估算]）**：kernel 依赖 + contracts + assembly + host/ui runtime + 后端 P0 薄适配器（`importer`/`tools-web` 等低风险叶子）+ 2 个 UI 模块试点（`ui/diff` 249 行/0 个 `Ctrl` 调用，`ui/plugins` 1,563 行、数据源单一）。验收 5 件事：①启动前预检能报出缺失/环/坏配置；②服务门控（UI 等 backend、Pending→Active）；③required 失败整树回滚、无注册残留；④UI 贡献组合顺序 + dispose 后消失；⑤headless/desktop 同一 registry 两份 assembly，渲染结果不同。

---

## 7. 工作量、阶段与并行

| 轨道 | 阶段 | 内容 | 人日[估算] |
| --- | --- | --- | --- |
| 后端 | P0 骨架 | 依赖 + host builtins + assembly + 薄适配器 + 双组装根 | 5–8 |
| 后端 | P1 服务化 | logger/paths/settings 收敛、runtime 契约、工具注册服务化 | 15–25 |
| 后端 | P2 模块化 | tools 拆 5 子包、12–16 模块迁入、session 接口化 | 20–30 |
| 后端 | P3 配置装配 | typed config+Validate 全覆盖、settings 桥接、平台 build tags、CI 预检 | 10–15 |
| | **后端小计** | | **50–78** |
| UI | W0 宿主骨架 | `internal/ui/runtime` + uikit + shelltest + AR-04 P0 | 12–16 |
| UI | W1 低耦合叶子 | 设置通用页/plugins/automation/toolcards/diff | 18–24 |
| UI | W2 功能 slot | memory/team/goal/plan/approval/todo | 18–24 |
| UI | W3 导航/侧栏 | workspaces/files/home | 21–28 |
| UI | W4 会话/输入 | session/composer（+ 读模型接口冻结） | 28–36 |
| UI | 收尾 | 跨模块验收测试 6,074 行、golden/a11y、性能基准、CI 门禁 | 8–12 |
| | **UI 小计** | | **105–140** |
| | **总程序** | 两轨可并行；UI 首期可只接线不拆包（25–35）先拿装配纪律收益 | **约 155–220** |

> 假设：参与人熟悉 shell/mygo UI（不熟 +20–30%）；不含视觉重做/新功能；桌面三平台真机验收另计（+15–20%）[判断]。

---

## 8. 风险登记（修订版）

| # | 风险 | 严重度 | 缓解 |
| --- | --- | --- | --- |
| R1 | **God object 状态迁移**（123+56 字段、19 个包级 Map、47 个多归属字段、72 个 Ctrl 方法/295 调用点） | 高 | W0 定义模块上下文接口（不收 `*App`）；逐模块搬字段进模块 struct；`App` 只留宿主字段（目标 <40）；迁移期保留同名薄方法委托 |
| R2 | **UI 测试迁移**（100 文件/29,465 行/513 NewTester/430 newLiveApp/278 `.Screen=`） | 高 | 先 `shelltest` 共享 fixture；模块包测试用 fake frame；shell 保留兼容 facade；先迁 2 个低耦合屏验证改写量 |
| R3 | **真实帧/线程集成**：假 frame 只证明组合；一帧 3 pass、Context 帧内有效、Handle/焦点时序 | 高 | W0 用 `ui.NewTester` 做真实 headless 断言（已用于 92 文件）；owner 用 `IsMainThread`；贡献 ID 映射 `c.Key` 防 key 冲突 |
| R4 | **注册纪律**：裸注册不可回滚（06 已复现泄漏） | 中 | 统一 `contracts.Own`；AST/CI 扫描禁止直连注册表；每波带"unregister 后零残留"测试 |
| R5 | **多窗口 settings**（同 App 两 Content、无窗口 ID、toast 单槽） | 中 | 每窗口 Runtime/scope；settings 页选中态留宿主；W0 补真窗口 smoke |
| R6 | **快捷键/焦点冲突**（每帧注册、先注册者胜、modal 抑制） | 中 | 启动期 chord 唯一性预检；命令由宿主统一安装；焦点单点持有（Composer 服务） |
| R7 | **预检拒绝跨 assembly 依赖**（required 无 provider） | 中 | UI 与后端同一 assembly；或 `RequiresOptional` + 运行期门控；两阶段 load 明确文档 |
| R8 | **kernel v0.1 API 演进**（作者自研，无供应链风险） | 低-中 | 钉版本；依赖收敛到 assembly/ui-runtime 两层；预检+spike 当回归网 |
| R9 | 编译期模块不减小体积、无热插拔 | 低（已明确非目标） | 写进产品预期；裁剪靠 build tags/多 main |

**明确非目标**：运行时安装/卸载插件、HMR、`!!js` 式配置表达式、浏览器/webview UI、跨进程/不可信插件沙箱、缩小 51.8MB 二进制。

---

## 9. 建议与非目标

**三阶段推进**：

1. **Stage 0（约 30–43 人日）**：垂直切片（§6）→ 拿到"装配统一 + 启动预检 + 回滚 + UI 贡献组合 + headless 裁剪"五件确定性收益；此时所有业务代码仍可保持原样（薄适配器），**可退出**。
2. **Stage 1（两条轨道并行）**：后端 P1/P2（服务化 + 工具/能力模块）＋ UI W1/W2（低耦合叶子 → 功能 slot）；每波按 05 §7.2 的"注册→渲染→卸载→golden 一致"验收。
3. **Stage 2（收口）**：后端 P3 + UI W3/W4 + 收尾（跨模块测试/golden/性能/CI 门禁）；再评估是否用 build tags 做产品裁剪。

**决策清单（只要以下成立就值得做）**：①已有/计划第二产品形态（headless/ACP/console/OEM）共用同一内核；②接受"配置=重启"；③愿意投入 ≥155 人日与测试迁移；④目标是装配纪律/裁剪/边界断言（不是运行时插件/缩体积）。若④实际是"热插拔/缩体积"，则本方向不成立。

---

## 10. 证据索引与复现

| 结论 | 子报告 | spike/探针 |
| --- | --- | --- |
| 内核能力/证伪/性能 | [01](01-kernel-audit.md) | `.probe/research/kernel-split/kernel/`（43 测试，`go test ./...`、`-race`） |
| DSH 305 包/装配链路/可照搬性 | [02](02-dsh-anatomy.md) | 计数命令见 02 §6.A |
| 后端依赖图/20 模块/阶段 | [03](03-mygo-module-graph.md) | `.probe/research/kernel-split/mygo/`（graph.go/collapse.py/out） |
| UI 帧/线程约束 + shell 解剖 | [04](04-ui-constraints.md) | `/tmp/probe-ui/main.go`（6 探针，headless） |
| UI 16 模块/宿主 chrome/波次 | [05](05-ui-module-map.md) | `.probe/research/kernel-split/ui-map/`（inventory/funcs/state/coupling） |
| UI 模块编译期组装 | [06](06-ui-spike.md) | `.probe/research/kernel-split/ui/spike/`（36 测试，`-race`） |

**复现命令**：

```bash
export PATH=/usr/local/go/bin:$PATH GOPROXY=off GOSUMDB=off GOFLAGS=-mod=mod
cd .probe/research/kernel-split/kernel     && go test ./... -count=1      # 43 PASS
cd .probe/research/kernel-split/ui/spike   && go test ./... -count=1      # 36 PASS
cd /data/mygo && go list ./... | wc -l                                     # 31
cd /data/mygo && find internal cmd -name '*.go' ! -name '*_test.go' | xargs wc -l | tail -1   # 91,143
ls -d /usr/local/lib/node_modules/@deepseek-ai/dsh/node_modules/@deepseek-ai/*/ | wc -l        # 305
```
