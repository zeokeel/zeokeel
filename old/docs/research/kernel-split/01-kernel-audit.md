# 01 · 内核能力审计：zeokeel-kernel v0.1.1 能为 mygo 的 DSH 式拆分提供什么

> 研究线 A / task-1 交付物。审计对象：`github.com/zeokeel/zeokeel-kernel@v0.1.1`（只读模块缓存
> `/root/go/pkg/mod/github.com/zeokeel/zeokeel-kernel@v0.1.1`，源码 12,488 行，纯标准库）。
> 可执行 spike：`.probe/research/kernel-split/kernel/`（独立 go module，35 个顶层测试 + 8 个子测试，
> `go test ./...`、`go test -race ./...` 全绿）。**本报告没有修改 /data/mygo 的 internal/、cmd/、
> go.mod、go.sum 或 git 历史**（`git status --short` 为空；spike 位于被 gitignore 的 `.probe/`）。
>
> 证据分级：**[实测]** = 本次 spike 跑出的输出；**[估算]** = 由实测/代码线性外推；**[外部引用]** =
> 内核/DSH 仓库自带文档或源码（附 file:line）；**[判断]** = 审计者的工程判断。
> 内核结论一律给出相对内核仓库根的 `file:line`。DSH（Cordis 4.0.5）侧结论已与 02 报告对齐，
> 对齐结果见 §6.2。

---

## 0. 结论摘要（先读这 9 条）

1. **[实测] 内核底座完整可用**：Context/fiber/effect/service/event/logger 语义齐备，激活是事务
   （失败全回滚）、effect 逆序单次撤销、依赖声明驱动 Pending↔Active 自动切换；上游 233 个测试本机复跑全绿
   （内核 56 / module 21 / conformance 154 / archtest 2）。
2. **[实测] 依赖门控正是 DSH 式装配需要的解耦点**：consumer 先于 provider 加载会 Pending，provide 后
   自动 Active、撤回后自动 Pending、再 provide 又 Active；`ServiceGate` 还能动态开/关依赖方。
3. **[实测] `module/` 是可用的“编译期 loader 子集”**：typed 清单、严格 JSON assembly、8 类启动预检、
   required/optional 失败策略与整树回滚、provider-first 调度、清单运行时校验、disabled、builtin 契约、
   稳定 entry ID、`Locate`/`MissingServices` 均有测试证据。
4. **[实测 + 外部引用] 但 `module/` 明确**不做**运行时热插拔与配置热更新**：Loader 没有
   `Update/Reload/ApplyFile/WatchFile`；`entryPlugin.Activate` 忽略内核传入的 config 参数
   （`module/load.go:173-175`），所以 `Fiber.Update` 成功重启后模块工厂仍拿到 load 时的旧 typed config
   （spike 已证伪）。配置变更路径 = **重启进程**。
5. **[实测] typed config 有“两套体系、没有桥接”**：内核 `JsonConfigSchema` 支持 required/default/
   类型/deny-unknown/浅 merge；`module.Descriptor[T]` 只做 `encoding/json` + 可选 `Validate`，
   **不注入默认值、不接内核 schema**。mygo 必须自己定一套配置约定。
6. **[实测] panic 遏制边界清晰**：插件 Activate panic → fiber `Failed`（进程不死）；listener panic →
   记录后继续分发；waterfall panic → **fail-closed 重新抛出**（唯一故意的例外，宿主须在边界 recover）；
   模块 goroutine 用 `module.SafeGo/SafeCall`；fatal/OOM/死循环/跨 goroutine panic 不隔离。
7. **[实测] 内核级热更与动态可用性存在**：`Fiber.Update` 走 schema 校验后 unload→reactivate，非法
   配置被拒且不改动运行中的 fiber；`ServiceGate` 可动态撤销依赖可见性——宿主自建 DSH 式热更新/开关
   的可复用底座（module 层未封装）。
8. **[实测] 性能量级可接受**：插件激活+卸载 ≈ 62–80µs，服务查找 ≈ 360ns/op，事件分发 ≈ 350ns/op，
   单模块 `Load+Close` ≈ 214–241µs；30–50 模块冷启动 7–12ms **[估算]**。显式全量 refresh 上界
   O(F×64)、parallel async 每 listener 一 goroutine，大规模 fanout 需留意（上游自述）。
9. **[判断] 对 mygo 的结论：可以做 DSH 式“模块化装配”，不能做 DSH 式“运行时插件宿主/HMR”。**
   现成约 60%（生命周期/DI/事件/日志/装配/预检/回滚）；近似约 25%（typed config、group、诊断、热更新
   内核原语）；必须自建约 15%（loader update/disable、include/patch/YAML、ExtensionPoint、entry 级
   isolate/intercept、契约版本、UI 边界）。

---

## 1. 审计对象、环境与方法

### 1.1 对象

| 项 | 值 |
| --- | --- |
| 模块 | `github.com/zeokeel/zeokeel-kernel` |
| 版本 | `v0.1.1`（缓存 zip h1 `WPkO74p7…O7Q=`，VCS hash `882cd0a46e6e…`） |
| 语言/依赖 | Go 1.27；仅标准库（`go.mod` 无 require） |
| 包 | `cordis`（内核根包）+ 可选 `module/`（编译期注册表/启动装配）+ `conformance/`（一致性测试）+ `internal/archtest`（边界静态检查） |
| 规模/测试 | 根包+module 共 12,488 行 Go（`module/` 1,006 行实现 + 1,096 行测试）；上游测试 233 个（README.md:22 声明，本次独立复跑一致） |

### 1.2 环境与约束

- 无外网（`GOPROXY=off GOSUMDB=off`）；Go 工具链 `/usr/local/go/bin/go`（go1.27.3）。
- 内核源码为只读模块缓存：本审计只读、未改。
- spike 独立 module：`require github.com/zeokeel/zeokeel-kernel v0.1.1`，`go.sum` 两行由缓存
  `go mod download -json` 给出（无 `replace`、未联网、未跑 `go mod tidy`）；复现：
  `cd .probe/research/kernel-split/kernel && export PATH=/usr/local/go/bin:$PATH GOPROXY=off
  GOSUMDB=off GOFLAGS=-mod=mod && go test ./...`。

### 1.3 证据分级与采集方式

| 等级 | 含义 | 本报告中的采集方式 |
| --- | --- | --- |
| 实测 | 本机跑出的可复现输出 | spike 的 43 个 PASS（35 测试 + 8 子测试）、上游 233 个 PASS、benchmark 数字 |
| 外部引用 | 上游仓库文档/源码自述 | 内核 `README.md`、`docs/*.md`、`*.go` 注释与实现；DSH vendored 的 Cordis 4.0.5 源码 |
| 估算 | 由实测/代码线性外推 | 模块数×单模块装配开销、fanout 成本 |
| 判断 | 审计者工程判断 | mygo 落地建议、风险排序 |

### 1.4 复现命令与总结果

```text
$ cd .probe/research/kernel-split/kernel
$ go test ./...
ok  	probe.local/kernelaudit	0.059s            # 35 顶层 + 8 子测试全绿
$ go test -v ./...                              # 逐测试证据，关键 t.Logf 见附录 A
$ go test -race ./...
ok  	probe.local/kernelaudit	1.09s
$ go test -bench . -benchtime=300ms -count=3 -run '^$' -benchmem
BenchmarkPluginActivation-96   4135–9308  62546–80140 ns/op  2928 B/op  46 allocs/op
BenchmarkServiceLookup-96     ~1e6       359.6–360.7 ns/op    48 B/op    1 alloc/op
BenchmarkEventEmit-96         ~1e6       346.4–353.4 ns/op    24 B/op    2 allocs/op
BenchmarkModuleLoadClose-96      1464–1495 214077–240918 ns/op 10177 B/op 145 allocs/op
```

独立复跑上游测试（依赖已在 go.mod 中，无需拷贝源码）：

```text
$ go test -v github.com/zeokeel/zeokeel-kernel/...   # 逐包显式列出 root+module+conformance×3+archtest
PASS（kernel 56、module 21、lifecycle 67、runtime 51、services 36、archtest 2 = 233）
```

---

## 2. 内核画像：它到底提供了什么

### 2.1 五对象与两维度

内核文档自述只拥有五件事：context、service（typed 值 + 可用性检查）、fiber（一个插件一个生命周期
单元）、effect（所有权归 fiber 的副作用注册）、events（同步/异步 pub/sub + waterfall）——`doc.go:5-14`。
两个正交维度：时间可组合性（可撤销 effect）与空间可组合性（声明式依赖驱动激活）——`doc.go:16-25`。

**对 mygo 的含义 [判断]**：这五件套恰好是“模块化宿主”需要的运行时原语，但**不含 UI、IO、
进程、权限、配置来源**；这些必须留在宿主（正是 `doc.go:49-52` 与 `README.md:186-193` 的边界声明）。

### 2.2 生命周期与事务边界

- 六态机 `Pending → Loading → Active → Unloading → Disposed`（+`Failed`，`fiber.go:19-35`）。
- `ctx.Plugin(plugin, config)`（`context.go:919-974`）：创建 fiber（先 Pending），依赖已满足则当场同步
  激活并立即返回 fiber；激活失败/panic 是 **fiber 状态（Failed）而不是 error**（`fiber.go:1127-1129`），
  宿主必须读 `Fiber.State()/ErrorDetail()`（`fiber.go:1091`、`:1513-1519`）。
- 激活事务：所有注册（服务/监听器/effect/子 fiber）归本次 attempt 的 epoch；失败时
  `failActivation` 取走并按逆序 drain、移除监听器、sweep 子 fiber，最后置 Failed——`fiber.go:1325-1339`；
  若“apply 成功后依赖恰好消失”，退化为回滚到 Pending 等待下次刷新——`fiber.go:1344-1359`、`fiber.go:1194-1197`。
- 卸载：`unload` 先做 withdraw→两阶段级联（先按依赖顺序让 dependent 失活，再 drain 自己），
  最后 finishUnload——`fiber.go:1011-1067`；级联帧在 `fiber.go:1030-1048`。
- 公开生命周期：`Fiber.Dispose`（`fiber.go:1632-1654`）、`DisposeAsync`（`fiber.go:1662-1687`）、
  `Abandon`（`fiber.go:1696-1742`，无析构器的兜底）、`Restart`（`fiber.go:1748`）、
  `Update`（`fiber.go:1826-1882`）、`Context.DisposeAll`（`context.go:1253-1258`）。
- 清理顺序/幂等：effect 逆序、单次（`effect.go`、`async_effects.go`）；级联卸载由 child ownership
  effect 承担（`fiber.go:575-623`、`fiber.go:703-740`）。安全阀：`MaxPluginDepth=256`（`fiber.go:13-16`）、
  `FiberQuota`（`l2.go:59-85`）、watchdog `StalledFibers`（只观测不杀，`l2.go:104-116`）。

### 2.3 服务与依赖门控

- 类型化服务：`ServiceKey[T]`（`service.go:35-45`）+ Go 1.27 泛型方法
  `ctx.Provide/Get/Require`（`context.go:307`、`context.go:413`、`context.go:448`）。
- 依赖声明：`PluginInjector.Inject() []string`（`registry.go:95`）；`pluginKind.inject()` 做 panic
  遏制式读取（`registry.go:141-155`）。
- 门控：`dependenciesReady`/`dependenciesSatisfied`（`fiber.go:433-465`）决定 Pending→Active；
  服务变更由 `noteServiceChanged` + `RefreshDependents` 触发（`context.go:396-397`、
  `registry.go:447-466`、`registry.go:470-546`），刷新是收敛的固定点循环（有界）。
- 动态可用性：`ServiceGate`（`service.go:147-204`）与 `ProvideChecked`（`context.go:319`）让 provider
  保持注册但可临时不可见；`Accessor`（`context.go:703-737`）提供“计算属性”读 lane；作用域
  `Extend/Isolate/IsolateLabeled/Intercept/ExtendMeta`（`context.go:165-241`）让同名服务可隔离 provide，
  事件按 scope 过滤（`events.go:152-157`）。
- 限制 [判断]：inject 只有“必选服务名列表”，没有 optional/版本/能力声明（optional 只能在模块层
  用 `ctx.Get` 探测）；类型检查在运行期使用 `reflect`/类型断言，`Get` 类型不匹配静默返回 `nil,false`
  （`context.go:437-438`），需模块层预检兜底（module 已做）。

### 2.4 effect 所有权、异步与取消

- effect：`ctx.Effect(func() Effect)`（`context.go:770`）、`EffectLabeled`、`AttachEffect`；
  sync/async 共用一条有序列表，逆序单次释放；panic 首个捕获、drain 完再抛（`effect.go:122` 起）。
- 异步：`AsyncPlugin`（`registry.go:79-87`）、`ctx.EffectAsync`（`context.go:393`）、
  `AsyncDisposer.DisposeAsync/DisposeBlocking`（`async_effects.go:137-297`）；取消语义
  “被取消的 teardown 把未开始的 cleanup 放回，下次重试”（`async_effects.go:156-283`，文件头注释）。
- 取消：Go 用 `context.Context`；取消 async activation 会回滚到 Pending（`fiber.go:1270-1275`）。
  另有协作式 `Fiber.Cancel` + `ctx.Checkpoint()`（`context.go:1275-1283`）。
- 阻塞桥：可选 `BlockingExecutor`（`exec.go:17-66`，首次安装胜出，进程级单例）。
- [判断] 限制：goroutine 无法被内核抢占/杀死；“取消”只能是协作式的；parallel async 每 listener
  一个 goroutine（上游 `docs/GO-DESIGN.md:517` 自述）。

### 2.5 内核边界（不做什么）

内核根包零 IO、零第三方依赖、禁 `unsafe`（上游 `internal/archtest` 强制：本次复跑
`TestKernelBoundary` PASS）；模块加载/文件监听/权限/沙箱/定时器/UI 组合属于 embedder
（`doc.go:49-52`、`README.md:186-193`）。`module/` 是**可选宿主层**，只允许标准库 + `os`/`json`。

---

## 3. 实测结果（按 task-1 的 7 项清单）

### 3.1 插件生命周期：Root→Plugin→Active→Dispose、失败回滚、DisposeAll

| 断言 | 结果 | 证据（spike 测试） |
| --- | --- | --- |
| Root→Plugin→Active→Dispose，cleanup 逆序且只跑一次 | PASS | `TestLifecycleRootPluginActiveDispose`：cleanup order `[second first]` |
| Activate 先 provide/effect/on 再返回 error：服务撤回、cleanup 跑、listener 摘除、状态 Failed | PASS | `TestActivationFailureRollsBackEverything` |
| 插件 panic：状态 Failed 且宿主继续可用 | PASS | `TestPluginPanicIsContained` |
| DisposeAll 级联子 fiber + root，幂等 | PASS | `TestDisposeAllTree`：cleanups=2、二次 DisposeAll 无副作用 |
| 激活失败不是 error 而是 fiber 状态 | 实测 | `Plugin` 返回 nil err、`State()=Failed`、`ErrorDetail()=plugin failed: ...` |

[实测] 关键日志：`rollback: cleanups=1 service-withdrawn=true listeners-removed=true state=Failed`；
`DisposeAll: fibers=0 cleanups=2 idempotent=true`。

**对 mygo 的影响 [判断]**：启动/关闭语义可直接承担“模块实例”的启动事务与关闭序列；
但宿主必须区分“Plugin 返回 error（创建失败）”与“状态 Failed（激活失败）”，module 层已经封装
（见 §3.3.4）。

### 3.2 ServiceKey Provide/Inject 依赖门控（Pending↔Active↔Pending）

| 步骤 | 实测结果 |
| --- | --- |
| consumer（Inject=`probe/db`）先加载 | `state=pending activations=0` |
| root.Provide(dbKey) | `state=active activations=1`（自动刷新，无需手动 Refresh） |
| provider disposer.Dispose() | `state=pending`（依赖方自动卸载，仍在注册表里等待） |
| 再次 Provide | `state=active activations=2` |
| ServiceGate / 服务类型 | gate 关闭→打开→再关闭 = `Pending → Active → Pending`（provider 未撤回）；`Get[T]` 类型不匹配静默 `nil,false`（需上层预检） |

证据：`TestInjectGatePendingThenActiveThenWithdraw`（`lifecycle_test.go`）、
`TestServiceGateDynamicAvailability`。内核实现：`context.go:307-399`（Provide 注册 effect 所有权）、
`context.go:619-634`（withdraw 后记脏）、`registry.go:470-546`（dependents 收敛刷新）、
`fiber.go:433-465`（就绪判定）。

**对 mygo 的影响 [判断]**：只要模块之间通过 `ServiceKey` 表达契约，装配顺序不再需要人工排序
（module 层还额外做了 provider-first 调度让 optional 依赖也确定）；这是拆分的最大收益点。
代价是：所有跨模块边界必须收敛为“共享 contract 包里的 ServiceKey + 接口”，不能继续用
`internal/app` 里的具体指针互相 import——这是 mygo 拆分的主要重构工作量，而不是内核成本。

### 3.3 module/ 注册表层（清单、assembly、预检、失败策略、回滚）

#### 3.3.1 清单与注册

- `Descriptor[T]`：ID/Name/Version/Provides/Requires/Decode/Validate/Factory——`module/module.go:79-106`；
  `Define` 链式 `AddProvides/AddRequires/AddOptionalRequires/WithValidate/WithVersion`——
  `module/module.go:111-155`。
- 类型化契约：`Provides/Requires/RequiresOptional(key)` 用 `reflect.TypeFor[T]()` 把 `ServiceKey[T]`
  翻成清单项——`module/module.go:41-77`。注册表是**显式值**（非包级 init 单例）：`NewRegistry`
  （`:278-288`）、`Register`（`:297-310`，重复/非法描述符 error）、`AddBuiltin`（`:315-320`，默认
  声明 logger 契约）、`Modules/Module/Has/Builtins`（`:322-372`）。
- [实测] `TestModuleRegistryManifests`：模块数 10、`probe/db` provides/requires/version 正确、
  重复注册报错、空 ID/nil factory 报错、builtin 含 logger。

#### 3.3.2 assembly.json（LoadFile/ParseConfig）

- `FileConfig{Version, Entries}` / `Entry{ID, Module, Config, Disabled, Optional, Children}`——
  `module/config.go:24-27`、`module/validate.go:18-36`。
- `ParseConfig` 是**严格 JSON**（`DisallowUnknownFields`），接受 `{"version":1,"entries":[…]}` 或裸数组、
  version 仅 0/1——`module/config.go:33-55`；`LoadConfigFile`（`:58-64`）；`LoadFile`（`:72-78`）。
  [实测] `TestModuleAssemblyJSONParseAndLoad`：未知字段/version 9/空文档全拒绝；嵌套树加载顺序
  `[database app api sidecar]`，typed config `{dsn:"postgres://assembly",pool:5}` 送达工厂；
  `Locate(fiber)` 回映 entry ID；`Close` 全部 Disposed 且幂等。

#### 3.3.3 预检矩阵（8 类，全部实测 PASS）

`Registry.Validate(entries)`（`module/validate.go:113-235`）在创建任何 fiber 之前一次性收集所有问题
（`:229-232`）。[实测] `TestModulePreflightValidationMatrix` 的 8 个子测试：

| # | 场景 | 实测预检错误（摘） |
| --- | --- | --- |
| 1 | 未知模块 | `unknown module "no/such-module" (is its package registered in this host?)` |
| 2 | 重复实例 ID | `duplicate instance ID "dup"` |
| 3 | 缺 provider | `requires service "probe/db", but no enabled entry in the assembly provides it` |
| 4 | 类型不匹配 | `service "probe/db" has incompatible contracts: ... requires string ... provides *kernelaudit.db` |
| 5 | 重复 provider | `service "probe/db" is provided both by entry "db1" and entry "db2"` |
| 6 | required 依赖环 | `required-dependency cycle: ca -> cb -> ca` |
| 7 | 坏配置（JSON 类型） | `invalid config ... cannot unmarshal string into ... pool of type int` |
| 8 | 坏配置（typed Validate） | `config for module "probe/db" rejected: dsn is required` |

另有 `TestModulePreflightErrorCreatesNoFibers`：预检失败时 **0 个 fiber、0 个模块工厂被执行**。
[实测] 台账：循环检测在 `module/validate.go:343-397`；provider 唯一性 `:168-187`；必选/可选
依赖与可赋值性 `:189-224`；collect-all 返回 `*module.ValidationError`（`:38-46`）。

**对 mygo 的影响 [判断]**：这 8 类正是装配期最容易“静默半启动”的错误，module 层把它们提前到
启动前并给出可读诊断。mygo 可以在 CI 里对每个产品的 assembly 跑一次 `Validate`（上游也建议
“产品装配测试模板”，`docs/COMPILE-TIME-REGISTRY.md:§5.3`）。

#### 3.3.4 启动事务与失败策略

- `Load`（`module/load.go:95-138`）：Validate（零 fiber）→ builtin 校验（`:143-155`）→
  按 provider-first schedule 创建（`:113-132`）→ `WhenIdleContext` + 逐实例 `AwaitSettled`（`:267-276`）
  → 状态复核（`:278-318`）→ 失败则 `disposeAll()` 逆序回滚（`:441-447`）。
- required 实例 Failed/Pending → `*LoadError` + 全树回滚；optional 实例失败/永久 Pending →
  保留实例 + `Warnings()`；`Entry.Optional` 语义见 `module/validate.go:29-33`。
- [实测]：
  - `TestModuleRequiredFailureRollsBackWholeTree`：required 失败 → `LoadError`、模块 fiber 残留 0、
    已激活模块的 cleanup 也执行（cleanups=1）、`Registry().FiberCount()==0`。
  - `TestModuleOptionalFailureIsKeptAsWarning`：optional 失败保留 Failed + 警告，required 模块照常 Active。
  - `TestModuleRequiredConsumerOfFailedOptionalProviderFailsAssembly`：可选 provider 失败导致 **required**
    consumer 永久 Pending → 整体失败回滚（不产生“半可用系统”）。
  - `TestModuleFactoryPanicIsContained`：模块工厂 panic 被内核遏制，`LoadError` 含 panic 详情，全树回滚。
  - `TestModuleManifestLieIsCaughtAtRuntime`：清单声明 provide 但工厂没 provide → `LoadError`
    （运行时清单校验 `module/load.go:344-365`）。
  - `TestModuleDisabledEntryIsSkippedWithWarning`：`disabled` entry 及其子树跳过并 warning。
- provider-first 调度：`buildSchedule`（`module/validate.go:245-320`）把 parent→child、provider→consumer
  （**含 optional 边**）做确定性拓扑排序；[实测] `TestModuleScheduleProviderFirst`：
  配置顺序 `[api, db]` → 计划顺序 `[db, api]`；可选边 `[opt-consumer, opt-provider]` → `[opt-provider, opt-consumer]`。
- 稳定入口：`Loader.Instance/Instances/Locate/Close/MissingServices/Warnings/Closed`——
  `module/load.go:368-439`；entry ID 是稳定身份，内核 fiber ID 不稳定（`module/load.go:25-27`）。

#### 3.3.5 运行时校验 / 反应式卸载 / 内置契约

- [实测] `TestModuleProviderWithdrawalDeactivatesDependent`：启动后 dispose provider 的 fiber →
  dependent 自动回 Pending，`Loader.MissingServices("api") == ["probe/db"]`。
- [实测] `TestModuleBuiltinContracts`：内核 logger 作为 builtin 契约直接满足模块 required；
  `Registry.AddBuiltin` 可声明宿主已提供契约；声明了但宿主没提供 → `Load` 报错
  “declared but not available”（`module/load.go:143-155`）。
- [实测] `TestModuleMultipleInstancesOfSameModule`：同一 module ID 以不同 entry ID 实例化两次可行
  （只要不冲突 provide 同名服务）。

#### 3.3.6 module 层**没有**的东西（关键缺口）

| 缺口 | 证据 |
| --- | --- |
| 无 `Update/Reload/ApplyFile/WatchFile`；配置变更=重启 | Loader 方法面 `module/load.go:368-439`；`README.md:113`、`README.md:191`；设计决策 `docs/COMPILE-TIME-REGISTRY.md:265-300` |
| 内核 `Fiber.Update` 无法给模块送达新 typed config | `entryPlugin.Activate` 忽略 config 参数、回放 load 时 decoded：`module/load.go:161-175`；**[实测]** `TestModuleFiberUpdateCannotDeliverTypedConfig`（activations=2 但 config 仍为 `original`） |
| 无运行期 enable/disable（`Entry.Disabled` 只在启动预检生效） | `module/validate.go:142-145`；Loader 无对应方法 |
| 无 include/多文件/YAML/TOML/patch、无 entry 级 isolate/intercept | `docs/COMPILE-TIME-REGISTRY.md:338`；`module/config.go` 仅严格 JSON；`entryPlugin.Activate` 直接用父 ctx 的 `ctx.Plugin`（`module/load.go:200`），assembly 无该字段 |
| 无配置默认值/迁移/版本兼容规则 | decode 仅 `json.Unmarshal`（`module/module.go:205-224`）；`Version` 只做诊断字段（`:91-92`） |
| 无 health/readiness API（只有 Warnings/MissingServices） | `module/load.go:399-439` |

### 3.4 typed config：内核 schema 与模块 typed config 是“两套体系”

**内核层** `JsonConfigSchema`（`config.go:151-322`）：
- 类型只有 5 种 JSON kind（`config.go:16-29`）；`Required/DefaultValue/Property/DenyUnknown`
  链式构建（`:167-210`）；
- `Validate` 注入默认值、报缺 required、检查声明类型、可选 deny-unknown（`:227-295`）；
- `Merge` 是浅合并（高层按 key 覆盖，默认值不覆盖显式值）（`:300-322`）；
- 通过 `PluginConfigSchema` 接口接入插件激活（`registry.go:100`、`registry.go:157-170`），
  校验 panic 被遏制（`config.go:117-138`），失败是 `CodeValidation` 的 fiber Failed。
- [实测] `TestJsonConfigSchemaDefaultsRequiredAndTypes/Merge`、`TestPluginConfigSchemaFailureIsFiberState`
  （invalid config → Failed + `CodeValidation`，插件 body 0 次执行）。

**模块层** `Descriptor[T]`（`module/module.go:205-224`）：只做 `encoding/json` round-trip 解码到 T，
再调用可选 `Validate` hook（`module/validate.go:147-155`）；**不做默认值注入**（Go zero value 兜底）、
**不接内核 ConfigSchema**、不产生 JSON Schema。实测：配置缺省字段时工厂拿到 zero value，预检只抓
JSON 类型错 + 自定义 Validate，内核 schema 的 defaults/required 在 module 层不可用。
- [实测] `TestFiberUpdateHotConfig`（内核）：`Fiber.Update` 走 schema 校验后 unload→reactivate，
  `mode/retries` 更新成功（activations=2），非法 update 返回 `CodeValidation` 且运行态不变。

**对 mygo 的影响 [判断]**：不要同时用两套。建议 mygo 统一走“**typed struct + Validate hook**”
（module 路线，编译期类型最强，也最符合 Go），把“默认值/必填”收敛到 `Validate` 或 `Default()`
约定；内核 `JsonConfigSchema` 只用于少数“插件式/动态配置”的宿主层（例如 UI 设置的热更新），
不要指望它替模块层做默认值。

### 3.5 panic 遏制、异步、事件/waterfall、logger、SafeGo/SafeCall、取消

| 能力 | 实测证据 | 内核实现 file:line | 边界/例外 |
| --- | --- | --- | --- |
| 插件 Activate panic → Failed，进程存活 | `TestPluginPanicIsContained` | `fiber.go:1165-1182` `callContained`、`:1325-1339` | 仅同 goroutine |
| 事件 listener panic → 记录后继续分发 | `TestListenerPanicDoesNotAbortDispatch` | `events.go:221-241` | 其余 listener 不受影响 |
| waterfall middleware panic → **fail-closed 重新抛出** | `TestWaterfallOrderTerminalAndFailClosed` | `waterfall.go:52-65`、`:99-115` | 内核唯一故意例外，宿主在调用边界 recover |
| 事件顺序/prepend/bail/async 双 lane | `TestEventsOrderPrependBailAndAsyncLanes` | `events.go:94-125`、`:245-356`、`context.go:1178-1232` | sync lane 不会调用 async listener |
| waterfall 嵌套顺序 + Terminal 短路 + Remaining/Args | `TestWaterfallOrderTerminalAndFailClosed` | `waterfall.go:34-75` | fail-closed 已注明 |
| async plugin + EffectAsync + DisposeAsync 等待 cleanup | `TestAsyncPluginEffectAndDisposeAsync` | `context.go:393`、`async_effects.go:137-297` | DisposeAsync 取消时 teardown 仍完成（A-9） |
| 取消 async 激活 → 回滚 Pending，可重试 | `TestAsyncActivationCancellation` | `fiber.go:1270-1275`、`:1232-1240` | 取消=context 取消，通常需协作 |
| Fiber.Cancel + Checkpoint 协作取消 | `TestCooperativeCancellation` | `context.go:1275-1283` | 仅轮询点可感知 |
| SafeGo/SafeCall panic 遏制 + PanicReport | `TestSafeGoAndSafeCall` | `module/safe.go:35-90` | 模块自建裸 goroutine 仍会拖垮进程 |
| logger 门面/等级/ring buffer/exporter | `TestLoggerFacadeAndExporter` | `logger.go:88-101`、`:125-146`、`:271-288`；`context.go:1371-1395` | buffer 默认 1000（`DefaultBufferSize`） |
| 模块工厂 panic 被 loader 变成 LoadError | `TestModuleFactoryPanicIsContained` | `fiber.go:1165-1182` + `module/load.go:194-228` | 同上 |
| effect cleanup panic 策略：drain 完再抛首个 | 上游 conformance 复跑 PASS | `effect.go:122` 起；`fiber.go:1648-1652` | Dispose 调用方需 recover |

**对 mygo 的影响 [判断]**：kernel 的 panic 边界可以替换/包住 mygo 现有 crash handler 的一部分
（cmd/zeokeel/main.go 已有进程级 recover + crash report），但**不能替代**：fatal/OOM/死循环/跨
goroutine panic 仍需进程 supervisor；模块自建 goroutine 必须强制 `module.SafeGo`（CI 可用 go/ast
扫描 `go` 语句，上游建议见 `docs/COMPILE-TIME-REGISTRY.md §6.2`）。

### 3.6 性能 / 并发 sanity

| 基准 | 结果（3 次） | 解读 |
| --- | --- | --- |
| Plugin 激活+Dispose | 62.5–80.1 µs/op，2928 B，46 allocs | 每模块启动成本 ~0.06–0.08ms **[估算]** |
| 服务查找 `ctx.Get` | 359.6–360.7 ns/op，48 B，1 alloc | 热路径可接受 |
| 事件分发 `Emit` 1 listener | 346.4–353.4 ns/op，24 B，2 allocs | 每事件固定开销 ~0.35µs |
| module Load+Close（1 模块） | 214–241 µs/op，10.2 KB，145 allocs | 含 WhenIdle/AwaitSettled/预检/回滚路径 |
| 30–50 模块冷启动（线性外推） | ≈ 7–12 ms **[估算]** | 不含模块自身 IO（DB/网络） |
| 大规模依赖 fanout | 显式全量 refresh 最坏 O(F×64)、每 listener 一 goroutine（parallel async）——上游自述 **[外部引用]** | `docs/GO-DESIGN.md:511`、`:517` |

[判断] 对桌面应用（模块数几十、启动一次）性能不是风险；风险在“运行时频繁热更新/大量事件”
场景下的 refresh 与 goroutine 开销——这正好也是 module 层刻意不做热更新的原因之一。

---

## 4. 能力矩阵：Cordis 4.0.5 特性 × Go 内核 × 对 mygo 拆分的影响

> Cordis 4.0.5 证据来自 DSH vendored 源码 `@deepseek-ai/dsh/node_modules/@deepseek-ai/`
> （cordis@4.0.5、loader@1.0.6、include@1.0.10、group@1.0.5）。状态：✅支持 / 🟡近似 / ❌缺失 / N/A；
> DSH 侧细节与 02 报告对齐结果见 §6.2。

| # | Cordis 4.0.5 特性 | Go 内核 v0.1.1 | 对 mygo 拆分的影响 | 证据 |
| --- | --- | --- | --- | --- |
| 1 | Context 树 / isolate / intercept / extend | ✅ `Extend/Isolate/Intercept/ExtendMeta` | 模块作用域隔离可用；但 assembly 不能按 entry 配置，需代码 | `context.go:165-241` |
| 2 | Plugin/Fiber 生命周期 + effect 逆序撤销 | ✅ 六态机、事务回滚、级联卸载 | 模块实例的启动/关闭底座 | `fiber.go:19-35`、`:1130-1313`、`:1011-1067` |
| 3 | 服务 DI + inject 门控（空间可组合性） | ✅ `PluginInjector` + 自动 refresh | 拆分的核心收益：装配顺序解耦 | `registry.go:95`、`:470-546`、`fiber.go:433-465` |
| 4 | 服务可用性 check / 动态开关 | ✅ `ServiceGate`/`ProvideChecked` | 功能开关可先落在服务层 | `service.go:147-204`、`context.go:319` |
| 5 | 事件 sync/async/bail/parallel/serial + scope | ✅ 双 lane + owner 回收 | 可替代/桥接 `internal/state` broker 的进程内事件 | `events.go:94-125`、`:245-356` |
| 6 | waterfall 中间件（Next/Terminal） | ✅（panic fail-closed） | 审批/工具调用拦截链可实现 | `waterfall.go:34-160` |
| 7 | Logger + exporter + ring buffer | ✅ 内置 logger 契约 + slog exporter | 与 mygo 文件日志桥接，别重复造 | `logger.go:125-385`、`context.go:1371-1395` |
| 8 | schema 库（schemastery）等价物 | 🟡 最小 JSON object schema（5 类型/默认值/required/denyUnknown/浅 merge） | 复杂配置校验需自建；无类型生成/transform/union | `config.go:151-322` |
| 9 | 模块 typed config（编译期类型） | 🟡 `encoding/json` + `Validate`；无默认值、不接内核 schema | mygo 需统一配置约定（建议 typed struct + Validate） | `module/module.go:205-224`、`module/validate.go:147-155` |
| 10 | 编译期模块注册表 + typed 清单 | ✅ Descriptor/Provides/Requires/Register | mygo 每个产品一个 assembly main | `module/module.go:79-113`、`:278-310` |
| 11 | 启动预检（缺失/重复/类型/环/坏配置） | ✅ 8 类场景实测 | 装配错误左移到启动前/CI | `module/validate.go:113-235`、`:343-397` |
| 12 | 启动配置树 + 失败策略 + 回滚 | ✅ 严格 JSON、嵌套 entry、required/optional、整树回滚 | 产品配置 + 启动确定性 | `module/config.go:33-78`、`module/load.go:95-138` |
| 13 | group / include（多文件、YAML、拆分） | 🟡 仅 `Entry.Children` 嵌套；无 include/YAML/多文件 | DSH 式多文件配置需宿主自建解析，再喂 `[]Entry` | `module/validate.go:18-36`、`docs/COMPILE-TIME-REGISTRY.md:338` |
| 14 | 运行时 enable/disable 热插拔（loader entry disabled 动态切换） | ❌ Loader 无 update/disable API；内核可 Dispose+重新 Plugin 但需宿主自管实例表/依赖闭包 | 运行时开关需宿主自建（或重启进程）；服务级开关可用 `ServiceGate` 近似 | Loader 方法面 `module/load.go:368-439`；DSH `cordis-plugin-loader/src/config/entry.ts:118` |
| 15 | 配置热更新（fiber.update / loader.update / volatile commit） | 🟡 内核 `Fiber.Update` 可用且实测；module 适配器无法送达新 typed config；无 volatile | mygo 若要“不停服改配置”，需自建 reloader（dispose+re-Load）或重启 | `fiber.go:1826-1882`；`module/load.go:173-175`；DSH `cordis/src/fiber.ts:736`、loader `entry.ts:145-191` |
| 16 | service proxy / callable / mixin（ctx 属性与可调用服务） | 🟡 `Accessor` 计算属性 + `ctx.Get`；callable/mixin 无等价（可用函数类型服务值近似） | 低-中：写法差异，不影响能力；跨模块接口需显式 ServiceKey | `context.go:703-737`；`README.md:193`；DSH `cordis/src/service.ts:18,37,50`、`reflect.ts:65-69` |
| 17 | 装饰器（@Inject 等） | ❌ Go 无装饰器；接口 + Descriptor 替代 | 中低：method 级 inject 需改写成插件级 Inject | `README.md:193`；DSH `cordis/src/registry.ts:37` |
| 20 | loader patch 层（applyEntryPatches / `loader/patch-context`） | ❌ assembly 无 patch 概念 | 若 mygo 需要“预设 patch/覆盖配置”，需自建（纯数据可行） | DSH `cordis-plugin-include/src/index.ts:57,129`；`cordis-plugin-loader/src/index.ts:41` |
| 21 | HMR / volatile config（ESM 热替换、不重启提交配置） | N/A / ❌ 静态链接 + 无热更新 | 开发期必须 rebuild；volatile 仅在热配置路线下才有意义 | `README.md:192`；`docs/COMPILE-TIME-REGISTRY.md:§1.1`；DSH loader `entry.ts:145-191` |
| 23 | ExtensionPoint/Contribution（命令/工具/路由集合贡献） | ❌ 内核与 module 层都没有；上游文档建议自建 P0 | **mygo 的 tools/MCP/skills/commands 需要这个**，工作量大头之一 | `docs/COMPILE-TIME-REGISTRY.md` I9 |
| 24 | loader 运行时树 update/remove/await/locate | 🟡 `Instances/Locate/Close/Warnings/MissingServices` + kernel `WhenIdle`；无 update/remove | 宿主自建实例管理 | `module/load.go:368-439`、`registry.go:799-830` |
| 25 | 只读诊断（PendingOn/Providers） | 🟡 `Loader.MissingServices` 计算；内核无诊断 API | 运维排障需补 | `module/load.go:428-439`；`docs/COMPILE-TIME-REGISTRY.md` I7 |
| 26 | L2 配额 / watchdog / 崩溃能力自述 | ✅ `FiberQuota`/`StalledFibers`/`IsolationCapabilities` | 可观测性基础，但不是安全边界 | `l2.go:22-116` |
| 27 | 进程外隔离 / WASM / 沙箱 / 权限 | ❌ 明确不做（同进程全可信） | 若接第三方不可信插件 → 另设计进程隔离，内核不解决 | `README.md:186-193`、`docs/COMPILE-TIME-REGISTRY.md §6.3` |
| 28 | 定时器/节流工具（plugin-timer） | ❌ 内核无 IO/timer；effect 可表达取消 | 小工具包，宿主自建 | `doc.go:49-52`（内核边界） |

---

## 5. 若 mygo 用 module/ 做 DSH 式装配：现成 / 近似 / 必须自建

### 5.1 现成可用（可直接用于 mygo 拆分）

1. **模块实例生命周期**：Root→Plugin→Active→Dispose、失败回滚、级联卸载、DisposeAll——§3.1。
2. **契约化 DI**：`ServiceKey[T]` + Inject 门控 + 自动刷新，装配顺序无关——§3.2。
3. **compile-time registry + typed 清单**：模块 ID、版本、provides/requires、typed config 工厂——§3.3.1。
4. **assembly.json + 启动预检 + 失败策略 + 回滚**：8 类预检、required/optional、provider-first 调度——§3.3.3/4。
5. **事件总线 + waterfall + logger + schema**，以及 **panic 遏制 + SafeGo/SafeCall**：模块级故障不
   拖垮宿主（同 goroutine 内）——§3.5。
6. **运行期反应式卸载**（provider 退出 → dependents 自动 Pending、`MissingServices` 可解释）与
   **稳定身份/定位**（entry ID vs fiber ID、`Locate/Instances`）——§3.3.4/5。

### 5.2 近似（需要 mygo 侧适配/薄封装）

| 项 | 差距 | 适配方式 |
| --- | --- | --- |
| typed config | module 层只有 JSON decode + Validate，无默认值/无 schema | 统一约定：struct + `Validate` + `Default()`；或给 `Descriptor` 加 `WithDefaults` hook（宿主 helper） |
| 配置热更新 | 内核有 `Fiber.Update`，module 适配器不能送 typed config | 宿主写“单实例 reloader”：`Dispose` 旧 fiber + 新 `Load` 新 entry（entry ID 稳定）；或产品层接受重启 |
| group / 嵌套 | 只有 `Entry.Children`；无 group 语义、无 include | 宿主把多文件/预设解析成 `[]module.Entry` 再 `Load`；可先做 JSON merge |
| 运行时 enable/disable + 诊断/健康 | Loader 无 disable API，只有 Warnings/MissingServices | 自建实例表（disabled=dispose fiber；enable=重新 Load 子树）；Loader 外再包 Health/Ready 快照（`Instances()`+`State()`） |
| 每 entry isolate/intercept | entryPlugin 直接用父 ctx | 宿主在 `Descriptor.Factory` 内部自行 `ctx.Isolate(...)`/`Intercept(...)`；或给 module 加可选 hook（改动上游） |
| logger 桥接 | `LoggerService` 与 mygo `log`/文件日志是两套 | 写 `LogExporter` 适配器把 kernel 日志转发到 mygo 现有文件日志/诊断页 |
| config 默认值 | 内核 schema 才有 defaults | 模块 `Validate` 前先做默认值填充；或在 Descriptor 上加 `WithDefaults` |
| 契约版本 | `Version` 只是诊断字符串 | 建 `contracts/v1` 包 + 命名规范；预检只做类型可赋值，需宿主加规则 |

### 5.3 必须宿主自建（内核/module 层不做或明确不做）

1. **运行时插件树管理（DSH loader 等价物）**：entry 的 update/remove/disable/enable、await 完成、
   partial dispose、配置 patch、volatile commit（若需要）——module 层没有，需 mygo 自建（复杂度最高）。
2. **include / YAML / 多文件配置 / patch 层**：module 只吃严格 JSON `[]Entry`；DSH 的
   `plugin-include`（YAML + `!!js` 表达式 + patch list）需宿主重写（纯数据层可行）。
3. **ExtensionPoint[T]/Contribution[T]**：命令、工具、MCP server、skills、prompt 片段的
   “有序贡献 + 所有权 + 优先级 + 冲突检测”；上游文档把它列为 P0 建议自建
   （`docs/COMPILE-TIME-REGISTRY.md` I9）——**这是 mygo 拆分真正的工作量大头**。
4. **每产品 assembly 工程化 + 契约版本策略**：build tags/多 main、assembly CI 校验、`--dump-config`
   等价 CLI；`ServiceKey`+接口放共享 contract 包（如 `internal/contracts`）、按 major 版本目录化、
   跨版本配置迁移（当前都没有）。
5. **热更新/热开关（若产品确实需要）**：见 5.2，建议先不做；DSH 式“改配置不重启”在编译期路线
   等于自建 loader update + 模块 Update 契约，成本高（上游 §4.2 的取舍理由可直接复用）。
6. **UI/主线程边界**：内核零 UI、异步 listener 在任意 goroutine；mygo（mygo UI 框架）要求
   UI 更新走 UI 线程。需要宿主层 `RunOnUI` 桥 + 约定“kernel 模块不得直接操作 UI”。
7. **进程级隔离（如需第三方不可信插件）与观测性**：内核不做隔离，L1 进程分组是另一条路线；
   指标/追踪/健康检查、Pending 原因聚合、装配失败报告落盘需宿主转接。

### 5.4 映射到 mygo 现有包的建议 [判断]

mygo 目前是单体（`internal/app` 约 20,385 行，`cmd/zeokeel/main.go` 250 行做进程生命周期），
`internal/app/app.go` 手工接线 `session.Controller`、`state.Broker/Projector`、`store`、
`tools`、`updater`、`shell` 等。按内核范式可这样映射（**只建议，未实施**）：

| mygo 现状 | 拆分后角色 | 契约建议 |
| --- | --- | --- |
| `internal/state`（Broker/Projector/Snapshot） | 进程内事件 + 状态服务 | `state.Broker` 作为 ServiceKey 服务，或事件名迁移到 kernel events（注意 Projector 单写者，别拆） |
| `internal/session.Controller` | 核心服务模块 | `ServiceKey[*session.Controller]`；依赖 store/provider/tools |
| `internal/store`（SQLite） | 基础模块（provider-first 调度优先启动） | `ServiceKey[Store]`；module descriptor + 迁移钩子（宿主自建） |
| `internal/provider/*` | 服务模块（多实例） | 每 provider 一个 service key 或一个 registry service；注意同名服务冲突预检 |
| `internal/runtime`（eino ADK） | 服务模块 | 依赖 session/store/provider |
| `internal/tools`/`mcp`/`skills`/`presets` | **贡献点（不是服务）** | 需先自建 `ExtensionPoint[T]`，否则退化成 `internal/app` 手工注册表 |
| `internal/shell`、`internal/brand`、`internal/design`、`internal/i18n` | UI/宿主层，不做 kernel 模块 | 保持 mygo UI 框架；kernel 模块通过 contrib point 提供数据 |
| `internal/updater`、`internal/globalhotkey`、`internal/winproc` | 宿主能力（OS/IO） | 保持宿主；对模块暴露接口 |
| `internal/app/app.go` 的接线 | **产品 main / assembly** | 每个产品（standard/creator/minimal）一个 assembly + build tags |

**建议分阶段路线 [判断]**：① P0 契约层：`internal/contracts`（ServiceKey + 接口 + 事件名），不改行为；
② P0 装配骨架：`cmd/zeokeel` 双路径（现有 wiring 保留、kernel 装配可开关），先把 3–5 个叶子模块
（store/session/provider/state）搬进 module 装配，验证启动/关闭/回滚；③ P0 ExtensionPoint：命令/工具/
MCP/skills 统一贡献点（mygo 特有最大缺口）；④ P1 预检 CI + typed config 约定 + 装配失败报告 + health
视图；⑤ P2 热更新/热开关：仅在明确“不停服改配置/开关”需求时做，优先“重启/重建实例”。

### 5.5 结论一句话

**用 module/ 做 mygo 的“模块化装配”成立且划算；把它当成 DSH 的 loader 原样迁移不成立。**
mygo 需要补的不是内核语义，而是 loader 工程层（include/patch/update/disable/health/contribution），
以及“模块契约包 + 每产品 assembly”这两件组织性工作。

---

## 6. 风险、未决问题与 02 报告对齐

### 6.1 风险（按严重度）

1. **目标—机制错配（最高）[判断]**：若 mygo 的真实目标是“运行时装上未编译进二进制的插件/HMR”，
   编译期注册表路线天然做不到（上游 `docs/COMPILE-TIME-REGISTRY.md:§1.1/§5.2` 也这么写）。
   必须先确认：mygo 要的是 **按配置组合已编译模块**，还是 **runtime plugin host**。
2. **module/ 是 v0.1.x 原型级 API（高）**：没有 Update/Reload；`Loader` 方法面很窄；
   1,006 行实现/1,096 行测试，生产硬化（错误类型、示例、迁移策略）仍是上游 P0
   （`docs/GO-DESIGN.md:519`）。mygo 应把它当“可替换的薄层”，不要写进业务契约。
3. **内核热更新与模块配置不闭环（高）[实测]**：`Fiber.Update` 成功但模块工厂拿旧 typed config
   （§3.3.6）。mygo 若做配置热更会踩这个坑；正确做法是宿主 dispose+re-Load 或等上游加 hook。
4. **两套 config 体系 + 文档漂移（中）**：内核 `JsonConfigSchema` 与 module typed struct 各自演化，
   mygo 必须选一套；`docs/COMPILE-TIME-REGISTRY.md:17` 仍把“reload”列为已验证能力而 `§4.2`（:265）
   说已移除，`docs/GO-DESIGN.md:509` 的 P0 待办与 v0.1.1 代码（`fiber.go:2056`/`:2081` 已 reindex）
   不一致——采用前按代码为准复核清单。
6. **panic 边界只到同 goroutine（中）**：第三方库回调、模块自建 goroutine、fatal/OOM/死循环
   都不在 kernel 保护内（`docs/COMPILE-TIME-REGISTRY.md §6.2`）；需模块契约 + CI 扫描 + 进程 supervisor。
7. **UI 线程约束（中）[判断]**：kernel 异步/事件在任意 goroutine 上跑，mygo UI 单线程；
   需要明确“kernel 模块不碰 UI、通过贡献点/队列回 UI 线程”的纪律，否则会出现 dev 期随机崩溃。
8. **性能 fanout（低-中）**：显式全量 refresh 上界 O(F×64)、parallel async 每 listener 一 goroutine
   （`docs/GO-DESIGN.md:511/517`）；模块数几十没问题，高频热更/海量事件需自测。
9. **单进程全可信（中）[判断]**：模块无沙箱/权限，工具审批是产品语义不是隔离边界；接入第三方代码
   仍需供应链审查/进程隔离。10. **离线/版本可得性（低）**：依赖 Go 1.27 + 本机模块缓存，发布需确认
   v0.1.1 的 tag/供应链审计（本次未验证网络侧）。

### 6.2 与 02 报告（DSH 侧）的对齐结果与遗留问题

02 报告 `docs/research/kernel-split/02-dsh-anatomy.md`（612 行）已产出，本报告矩阵与其对齐：

- **装配模型一致**：02 §5.2 的 P1/P2/P4/P5/P6/P11/P12（一模块一包、声明式 entry 树、稳定 id、
  服务可用性驱动激活、能力接缝、多产品复用、两级平面）与本报告“现成/近似”清单逐条对应；
  kernel 的 Inject 门控被 02 明确列为“可照搬”（P5）。
- **patch 层是宿主侧 ~100–200 行小工程**（02 §5.2 P3），不是内核能力：与本报告“include/patch 需自建”
  一致；`!!js` 任意求值不可照搬（P8），建议只做平台/env/profile/service 存在性谓词。
- **HMR/运行时安装明确非目标**（02 P9/P10、§5.4 硬结论 1/3）：与本报告“编译期路线不能做 runtime
  plugin host”一致；对外表述应改为“模块集随构建固定，配置可启停”。
- **两级平面（进程级树 + 会话级 preset 子树）**是最值得借鉴的 DSH 设计（02 §3.8/P12）：
  Go 侧需宿主实现 per-session 装载器 + `Isolate`/子 fiber（kernel 已提供原语）。
- **行级 inject 覆盖**：DSH 支持（02 §3.4），kernel `module.Entry` 无该字段
  （`module/validate.go:18-36`），宿主需在 adapter 包装里合入——本报告矩阵未单列，按 02 P5 结论处理。
- **仍未对齐/未验证**：DSH 运行时 enable/disable 的真实产品用法、volatile config 使用频率、双面包
  服务依赖完整性（02 §6.D 同列未验证）；不影响“内核能力=底座、loader 工程=宿主自建”的主结论。

---

## 7. 附录 A：spike 测试清单与关键输出

### 7.1 测试 → task-1 清单映射

| task-1 条目 | spike 测试（全部 PASS） |
| --- | --- |
| 1 生命周期/回滚/DisposeAll + 2 ServiceKey 门控 | `TestLifecycleRootPluginActiveDispose`、`TestActivationFailureRollsBackEverything`、`TestDisposeAllTree`；`TestInjectGatePendingThenActiveThenWithdraw`、`TestServiceGateDynamicAvailability` |
| 3 module 注册表/预检/失败策略 | `TestModuleRegistryManifests`、`TestModuleAssemblyJSONParseAndLoad`、`TestModulePreflightValidationMatrix`（8 子测试）、`TestModulePreflightErrorCreatesNoFibers`、`TestModuleScheduleProviderFirst`、`TestModuleRequiredFailureRollsBackWholeTree`、`TestModuleOptionalFailureIsKeptAsWarning`、`TestModuleRequiredConsumerOfFailedOptionalProviderFailsAssembly`、`TestModuleManifestLieIsCaughtAtRuntime`、`TestModuleOptionalRequirementMissingProviderIsWarning`、`TestModuleDisabledEntryIsSkippedWithWarning`、`TestModuleProviderWithdrawalDeactivatesDependent`、`TestModuleBuiltinContracts`、`TestModuleFactoryPanicIsContained`、`TestModuleMultipleInstancesOfSameModule`、`TestModuleFiberUpdateCannotDeliverTypedConfig` |
| 4 typed config | `TestJsonConfigSchemaDefaultsRequiredAndTypes`、`TestJsonConfigSchemaMerge`、`TestPluginConfigSchemaFailureIsFiberState`、`TestConfigSchemaResolveAndIntercept`、`TestFiberUpdateHotConfig` |
| 5 panic/异步/事件/日志/取消 | `TestPluginPanicIsContained`、`TestListenerPanicDoesNotAbortDispatch`、`TestEventsOrderPrependBailAndAsyncLanes`、`TestWaterfallOrderTerminalAndFailClosed`、`TestAsyncPluginEffectAndDisposeAsync`、`TestAsyncActivationCancellation`、`TestCooperativeCancellation`、`TestSafeGoAndSafeCall`、`TestLoggerFacadeAndExporter` |
| 7 性能 sanity（可选） | `bench_test.go`：`BenchmarkPluginActivation`、`BenchmarkServiceLookup`、`BenchmarkEventEmit`、`BenchmarkModuleLoadClose` |

### 7.2 关键实测输出（节选，来自 `go test -v ./...`）

```text
lifecycle: cleanup=[second first]; rollback cleanups=1 service-withdrawn=true state=Failed;
  Pending->Active->Pending->Active (activations 2); gate open/close OK; DisposeAll idempotent
config: invalid plugin config -> state=Failed code=validation; hot update fast/3 -> batch/9
events/panic: plugin panic -> Failed; listener panic -> dispatch continues; waterfall panic re-raised;
async: cleanup awaited; cancelled activation -> Pending then retry Active; Checkpoint=ErrCancelled
module: instances=[database app api sidecar]; typed config delivered; schedules [db api]/[opt-provider opt-consumer];
  required-failure rollback fibers=0; Fiber.Update restarted fiber but typed config stayed "original"
```

### 7.3 spike module（独立、离线可解）

```text
module probe.local/kernelaudit;  go 1.27.3
require github.com/zeokeel/zeokeel-kernel v0.1.1
# go.sum（缓存元数据生成，无 replace）
github.com/zeokeel/zeokeel-kernel v0.1.1 h1:WPkO74p7bDqXl2gbnchXxknGrIPS4Yd+UAFXnZOUG7Q=
github.com/zeokeel/zeokeel-kernel v0.1.1/go.mod h1:WvJXgG5V0gCaaF1w0MX7D4cVb15qoG4SsCq1FG30hIw=
```

（两行由缓存 `go mod download -json …@v0.1.1` 输出；无 `replace`、无网络、未跑 `go mod tidy`。）

---

## 8. 附录 B：证据索引（相对内核仓库根 / 其他路径）

### 8.1 内核根包（`cordis`）

| 主题 | file:line |
| --- | --- |
| 六态机 / 激活 / 失败回滚 / 回 Pending | `fiber.go:19-35` / `1130-1209` / `1325-1339` / `1344-1359` |
| 卸载与两阶段级联 / Dispose 三兄弟 | `fiber.go:1011-1067` / `1632-1654`、`1662-1687`、`1696-1742` |
| Restart / Update（热配置）/ 依赖判定 | `fiber.go:1748-1795` / `1826-1882` / `433-484` |
| Plugin、Inject / Provide、Get、Require、撤回 | `context.go:919-974`、`1065-1083` / `307-399`、`413-455`、`619-634` |
| Extend/Isolate/Intercept/Accessor / Emit/On/Waterfall/DisposeAll | `context.go:165-241`、`703-737` / `1178-1250`、`1253-1273` |
| Logger 服务门面 exporter / 事件注册表 | `logger.go:88-146`、`344-370`；`context.go:1371-1395` / `events.go:94-125`、`221-356` |
| waterfall / ConfigSchema / 服务与 Plugin 接口族 | `waterfall.go:34-115` / `config.go:112-322` / `service.go:35-45`、`147-253`、`303-320`、`registry.go:66-170` |
| Registry / refresh / DisposeAll / Replace | `registry.go:250-295`、`470-546`、`1126-…`、`991-1058` |
| panic 基础设施、错误码 / 异步 effect | `contains.go`、`error.go:13-56`、`197-203` / `async_effects.go:137-297` |
| BlockingExecutor / 配额 / watchdog | `exec.go:17-66` / `l2.go:59-116` |

### 8.2 `module/` 与文档

| 主题 | file:line |
| --- | --- |
| Descriptor/Define/Add* / 类型擦除/JSON decode/Validate/Factory | `module/module.go:41-155` / `157-252` |
| Registry/Register/AddBuiltin | `module/module.go:263-320` |
| Entry/ValidationError/Validate / 预检三件套 | `module/validate.go:18-46`、`113-235` / `168-187`、`189-224`、`343-397` |
| provider-first 调度 | `module/validate.go:245-320` |
| Load/checkBuiltins / entryPlugin / createInstance/settle/collectIssues | `module/load.go:95-155` / `161-175` / `194-318` |
| 清单运行时校验 / 诊断 / Close / 回滚 | `module/load.go:344-365`、`368-439`、`414-423`、`441-455` |
| FileConfig/ParseConfig/LoadFile / SafeGo/SafeCall / 已知边界与对照 | `module/config.go:24-78` / `module/safe.go:35-90` / `README.md:186-193`、`docs/COMPILE-TIME-REGISTRY.md:265-338`、`docs/GO-DESIGN.md:485-521` |

### 8.3 DSH / Cordis 4.0.5（外部引用，与 02 报告对齐）

在 `@deepseek-ai/dsh/node_modules/@deepseek-ai/` 下：`cordis/src/service.ts:18,37,50`（callable）、
`reflect.ts:65-69,130-135,219-222`（proxy/mixin）、`registry.ts:37`（@Inject）、`fiber.ts:736`（update）；
loader `config/entry.ts:118,145-191`（update/volatile）、`config/tree.ts:90,97`、`index.ts:41`（patch-context）；
include `index.ts:57,129`（applyEntryPatches）；group `index.ts`（Group）。02 报告 §3.4/§6.B 有完整索引。

---

*报告结束。所有 spike 产物在 `.probe/research/kernel-split/kernel/`（`README.md` 含复现步骤），
`/data/mygo` 的受控源码文件未改动。*
