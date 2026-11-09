# 02 · ZeoKeel-Rig SDK 设计：边界、三方用法、扩展点与仓库策略（调研线 B）

> 研究线 B / task-11 交付物。只读分析 /data/mygo（新 ZeoKeel 骨架 + `old/` 归档旧工程）与
> 只读内核缓存；未改任何产品代码、未 commit。并行调研线：A = `01-eino-audit.md`（eino 依赖与替换
> 规格，task-10）、C = `03-openness-ecosystem.md`（三方开放模式/命名法律，task-12）——本报告给出
> 设计决策，涉及 eino 细节与命名法律时以 01/03 为准（03 已产出并对齐；01 产出中）。
>
> 证据分级：**[实测]** 本机命令可复现 ＞ **[外部引用]** 仓库文档/源码（给 file:line 或路径）＞
> **[估算]** 假设已列明 ＞ **[判断]** 审计者工程判断。所有 spike/数字均在 §9 给复现命令。
>
> 用户原话："做一个 ZeoKeel-Rig 的 SDK，抛弃老 eino，支持随意组装的插件系统，同步开放能力给三方，
> 这个项目只需要做一个组装就可以随便使用了。"

---

## 0. 结论速览（7 个直接回答）

| # | 问题 | 回答 | 依据 |
| --- | --- | --- | --- |
| Q1 | **SDK 边界** | 三层 + 一个可选面：`zeokeel-kernel`（已有内核，保持独立）/ **`zeokeel-rig`（新 SDK：headless-first 的 contracts/v1 + agent loop + tool/provider/session/assembly/headless host）** / `zeokeel`（产品：UI/host/产品模块/产品 assembly）/ 可选面：UI 与 SQLite-store 作为 rig 仓内**独立嵌套 module**，不进 rig core。 | §2 |
| Q2 | **三方怎么用** | `go get github.com/zeokeel/zeokeel-rig` → 写 Tool/Provider/Module（kernel `module.Descriptor`）→ 编译期注册进 registry → 写 `assembly.json` → `main` 约 20 行 → `go build` 得到自己的 agent 二进制。"安装插件 = 加依赖 + 注册 + 重编译"。 | §3 |
| Q3 | **扩展点与版本化** | `contracts/v1`（Provider/Tool/History/Gate/Event/Logger/Settings）+ kernel `ServiceKey` 服务 + 键控注册表（ToolRegistry/ProviderRegistry）+ 事件流；**UI 槽位不属 SDK**（产品/可选 ui module 所有）。semver + contracts/v1 冻结策略 + `internal/kernelx` 单点隔离 kernel v0.1 演进。 | §4 |
| Q4 | **仓库策略** | 推荐 **C（三仓）**：kernel（已有）/ `zeokeel-rig`（新）/ `zeokeel`（产品）；rig 仓内用嵌套 module 承载可选 `ui/`、`store/sqlite/`；模块路径 `github.com/zeokeel/zeokeel-rig`（工作名）。对外命名/许可按 03：建议 **ZeoKeel SDK + Apache-2.0**。B（单仓多 module）作为过渡降级方案。 | §5 |
| Q5 | **MVP 范围** | contracts/v1 + eino-free agent loop + OpenAI 兼容 provider + tool registry/3 个内置工具 + assembly 预检 + headless runner + 3 个可编译示例 + 文档/测试；**不做** session 持久化/压缩/checkpoint/多智能体/MCP/LSP/ACP/UI/eino 删除（eino 靠"不迁移"自然消失）。**约 35–50 人日**。 | §6/§7 |
| Q6 | **成本与最大风险** | MVP 35–50 人日，完整 v1 120–180 人日（不含 UI）。最大风险：**API 稳定性 + SDK/产品双轨 + eino 行为等价**（旧 runtime 8.9k 行测试是规格），其次是 UI 依赖泄漏、版本碎片、`rig` 命名检索冲突（Rust 0xPlaygrounds/rig，命名法律见 03）。 | §7 |
| Q7 | **做不做/何时** | **做**，定位"给 Go 开发者的 headless agent 组装 SDK"，不是"运行时插件市场/无代码平台"。节奏：现在 1 周做 M0（契约+边界）；M1–M2 与产品 agent core 迁移**同批 dogfood**；`v0.1.0-alpha` 目标 6–8 周，`v0.1.0` 在 headless 产品用 rig 组装成功后发布。"只需组装"对 Go 开发者成立；对产品级功能/非 Go/运行时安装不成立（走 03 的 MCP/ACP）。 | §8 |

---

## 1. 事实基线（本报告的锚点）

### 1.1 新根骨架现状 [实测]

```text
$ find internal cmd -name '*.go' ! -name '*_test.go' | xargs cat | wc -l   # 2,986 行
internal/contracts   242 行（服务键/接口/贡献值类型；imports mygo/ui——UI 耦合点）
internal/uiroot    1,008 行（帧根 + 五张注册表 + Post/导航/设置面；imports mygo）
internal/host        786 行（窗口/托盘/单实例/设置/日志/信号/myo 桌面宿主）
internal/assembly    353 行（编译期 registry + 内嵌 desktop/headless.json + 预检 + 加载）
internal/modules/appinfo、internal/ui/{home,about,welcome,toolcards}、internal/uikit
$ go build ./... && go vet ./... && go test ./... -count=1       # 全绿（离线）
```

关键事实：**当前 `internal/contracts` 已经 import `github.com/egoist/mygo/ui`**（`contracts.go:5-9`），
`uiroot`/`host` 同样依赖 mygo。因此"把 contracts 直接变成 SDK 公开包"行不通——必须按 headless /
UI 拆成两份契约，否则 SDK 会把桌面 UI 框架拖进第三方依赖树。这是本设计的第一硬约束。

### 1.2 旧工程（`old/`）与 eino 事实 [实测 + 外部引用]

| 维度 | 事实 |
| --- | --- |
| 形态 | `old/` 是独立 Go module（`github.com/zeokeel/zeokeel/old`），仅作历史设计参考；归档已从仓库移除 |
| agent core | `old/internal/runtime`：23 个非测试文件 / **6,434 行**；31 个测试文件 / 8,882 行（共 15,316） |
| eino | `cloudwego/eino v0.10.0-alpha.35`；**9 个非测试文件** import（agent/modes/mode_deep/model_adapter/stream/multimodal/history/subagent/question_gate），11 个测试文件；用到 8 个 eino 包：`schema`×16、`adk`×7、`compose`×5、`components/model`×5、`components/tool`×4、`tool/utils`×1、`adk/prebuilt/supervisor`×1、`adk/prebuilt/planexecute`×1 |
| 公开接口 | **eino-free 的已是多数**：`Provider`（provider.go:236）、`Tool`（tool.go:14）、`History`（history.go:22）、`Gate`（approval.go:31）、`Compactor`（compaction.go:21）、`QuestionGate`（question_gate.go:25）；eino 泄漏点集中在 `Agent` 内部（`agent.go:13-15`、`model_adapter.go:11-14`、`modes.go:9-14`、`stream.go:12-13` 等） |
| 其他后端包 | `provider/openai` 1,193 行（非测试）、`tools` 12,007 行、`session` 8,840 行（`Controller` 115 个导出方法）、`store` 5,887 行、`state` 1,345 行（10 种事件） |
| 依赖图/内核 | `old`：`go list -m all` = **117 modules**；rig core 目标 = kernel（v0.1.1，**0 require**，上游 233 测试全绿）+ 标准库，第三方 `go.sum` 近乎为空 |

### 1.3 已有结论复用（不重复论证）

00 报告已定：编译期模型收益是**装配纪律**（预检/回滚/依赖边界/多产品复用），不是热插拔或体积；
不可照搬 DSH 的运行时安装/HMR/`!!js`；UI 编译期模块化但帧循环归宿主。本报告只回答"如何以 SDK
形态开放给第三方"，不重复内核/UI 可拆性论证。

---

## 2. SDK 边界：三层 + 一个可选面（Q1）

### 2.1 分层

```text
┌──────────────────────────────────────────────────────────────────────┐
│ zeokeel（产品仓）                                                     │
│  cmd/zeokeel · internal/host(desktop 窗口/托盘) · internal/uiroot      │
│  internal/contracts/ui · internal/ui/* · internal/modules/*(产品模块)  │
│  assemblies/desktop.json · headless.json                             │
├──────────────────────────────────────────────────────────────────────┤
│ zeokeel-rig（SDK 仓，headless-first）                                 │
│  contracts/v1 · agent(eino-free loop) · provider + provider/openai    │
│  tool + tool/builtin · session · assembly · host(headless runner)     │
│  modules/appinfo · examples/* · internal/kernelx                      │
│  ┌ 可选嵌套 module（独立 go.mod、独立 tag）────────────────────────┐  │
│  │ ui/            mygo UI runtime + slots（第三方桌面产品选用）     │  │
│  │ store/sqlite/  SQLite History/Store（第三方持久化选用）          │  │
│  └──────────────────────────────────────────────────────────────────┘  │
├──────────────────────────────────────────────────────────────────────┤
│ zeokeel-kernel（已有独立仓/模块）                                      │
│  cordis（Context/Fiber/Effect/Service/Event/Logger）+ module/（预检/装配）│
└──────────────────────────────────────────────────────────────────────┘
```

### 2.2 包含 / 不包含（边界表）

| 能力 | 进 rig？ | 理由 |
| --- | --- | --- |
| kernel（context/fiber/effect/service/event/logger + module 预检） | **作为依赖，不复制** | 已是独立 module（0 三方依赖），rig 通过 `internal/kernelx` 单点包装；第三方不必直接 import kernel（可 import） |
| contracts/v1（Provider/Tool/Message/Event/History/Gate/Logger/Settings/键） | ✅ core | 稳定契约 = SDK 的立身之本；只允许 kernel 别名 + 标准库 |
| agent loop（模型↔工具循环、流式、取消、最大迭代、审批） | ✅ core | "组装即用"的必需品；**eino-free 重写**（旧 eino 不迁移） |
| provider 注册表 + OpenAI 兼容流式实现 | ✅ core（stdlib net/http） | 第三方开箱能连主流模型；其他 provider 以模块/子包扩展 |
| tool 契约 + 注册表 + 最小内置工具（读文件/列目录/受审批 shell） | ✅ core（三个以内） | 提供最小可用面；大量工具放 `rig/tool/*` 或产品模块，避免 SDK 变"全家桶" |
| History/Session 模型 + 内存实现 | ✅ core（接口 + memory） | agent 跨轮对话最小闭环；持久化可选 |
| Store（SQLite） | ❌ rig core，✅ 可选嵌套 module | modernc.org/sqlite 重依赖；第三方可能自带 DB |
| assembly（registry + assembly.json + 预检 + typed config） | ✅ core | kernel `module/` 的注册/装配帮助层；产品只写自己的 registry/assemblies |
| headless host runner（信号/日志/settings/退出） | ✅ core | 三方 main 20 行即可跑；桌面 host 留产品 |
| appinfo（build identity 服务） | ✅ core 小模块 | 通用；产品品牌配置由 assembly config 注入 |
| UI（mygo、uiroot、uikit、design、槽位/注册表） | ❌ core，✅ 可选 `ui/` module | UI 框架是产品/桌面选择；SDK headless-first，绝不 import mygo |
| 桌面进程能力（窗口/托盘/单实例/热键/更新器） | ❌ | 平台/产品相关；V1 不做跨平台桌面 SDK |
| MCP/LSP/ACP、memory/skills/presets/agentteam/automation | ❌ core（v0.2+ 可选 contrib modules） | 产品级能力；第三方按需引入，避免核心依赖树膨胀 |
| eino adapter / 运行时插件安装、热更、沙箱 | ❌（非目标；adapter 如确需独立成 module） | 用户目标抛弃 eino；编译期模型硬约束（00 报告 §4）；adapter 会把 eino 带进依赖图 |

### 2.3 当前 `internal/*` → rig 公开包迁移表

| 当前（/data/mygo） | 去向 | 动作/备注 |
| --- | --- | --- |
| `internal/contracts`（242 行，import mygo/ui） | **拆分**：headless 部分 → `zeokeel-rig/contracts/v1`；UI 贡献类型（BuildFunc/Screen/Slot/SettingsPage/ToolRenderer/Command/UIRuntime/Navigator/Theme/Locale）→ 产品 `internal/contracts/ui`（未来可随 `rig/ui` module 公共化） | 最大的机械工作；拆完 SDK 侧零 mygo |
| `internal/assembly`（353 行） | 通用部分 → `rig/assembly`（registry 帮助、内嵌 JSON 读取、预检、buildinfo overlay、builtin 声明帮助）；产品 `internal/assembly` 只留**产品 registry + 产品 assemblies + UI builtin 声明** | UI builtin 的服务声明留在产品 |
| `internal/host`（786 行） | 通用部分 → `rig/host`（Settings 文件存储、Logger、termination signals、headless runner）；桌面部分（mygo 窗口/托盘/菜单/退出）留产品 | `runHeadless` 的 45 行现成模式（host.go:254-297）即 `rig/host.Runner` 原型 |
| `internal/modules/appinfo` | `rig/modules/appinfo` | 纯服务模块，无 UI/平台依赖 |
| `internal/uiroot`、`internal/uikit`、`internal/design`、`internal/ui/*` | **产品保留**；`uiroot` 未来可整包上移到可选 `rig/ui` module | 现阶段不移动，避免把 mygo 拖进 rig |
| `old/internal/runtime`（eino） | `rig/agent` + `contracts/v1` 的行为规格来源 | **不迁移代码，按 31 个测试文件的语义重写**（eino 内部实现全部丢弃） |
| `old/internal/provider/openai`（1,193 行） | `rig/provider/openai` | 迁移时去掉旧 runtime 依赖，只保留 Provider 流式实现 + 重试 |
| `old/internal/tools`（12,007 行） | `rig/tool` 契约 + `rig/tool/{fs,exec,...}` 分批 + 产品专属工具留产品 | 先做最小 3 个内置工具；MCP/LSP/agent 工具后置 |
| `old/internal/session`（8,840 行 / Controller 115 方法）、`store`（5,887 行） | session：rig 定义读模型/控制接口，产品控制器留产品模块；store：`contracts/v1` 接口 + 可选 `store/sqlite` module | 接口化是迁移门槛（00 报告 T1）；SQLite 不进 rig core |
| `old/internal/state`（1,345 行 / 10 事件） | `contracts/v1.Event` 封闭集 + rig/agent 事件流 | 只保留 headless 需要的 6–8 种 |
| `old/internal/{mcp,lsp,acp,memory,skills,presets,modelcatalog,prompt,automation,agentteam}` | 产品模块，或 v0.2+ 可选 rig contrib modules | 不在 MVP |

### 2.4 headless-first 与 UI 依赖隔离（硬规则）

1. `zeokeel-rig` 根 module 的 `go.mod` **只 require kernel**；任何 mygo / sqlite / eino 依赖必须放
   嵌套 module 或产品仓。
2. CI 断言：`go list -deps ./... | grep -E 'mygo|eino|sqlite'` 必须为空（rig 根 module）。
3. `contracts/v1` 不出现 `*ui.Context`、`BuildFunc`、槽位类型；UI 的一切都在 `ui/` 或产品。
4. 产品可以 import rig + ui module；rig 永远不 import 产品（单向）。第三方 headless 的 `go mod graph`
   里应只有 kernel + 标准库——这是对 eino 系框架最硬的差异化。

---

## 3. 三方使用姿势（Q2）

### 3.1 心智模型：安装 = 加依赖 + 注册 + 重编译

| 步骤 | 三方要做的事 | DSH 对应物 |
| --- | --- | --- |
| 安装 | `go get github.com/zeokeel/zeokeel-rig@v0.1.0`；自定义模块放自己仓库 | npm 安装 + profile 目录 |
| 注册 | 在 `main`/`assembly.go` 里 `rig.Register(registry, mymodule.Module())`（不要 init() 隐式注册） | bundle patch 里的 `name` 行 |
| 配置 | 写 `assembly.json`（entry 树：id/module/config/children/disabled） | `cordis.patch.yml` 行 + profile/bundle |
| 构建 | `go build` → 得到自己的 agent 二进制 | 无构建（运行时加载） |
| 更新 | 改依赖/配置 → 重新编译（配置也可只重启） | 热重载/HMR |

**Go 编译期模型下没有"运行时安装插件"**：第三方扩展的两种形态是 ①自己的 Go 模块编译进二进制；
②通过 MCP/ACP 等**进程外协议**接入（见 03 报告）。这不是 SDK 缺陷，是单二进制/可交叉编译/可审计的
代价；文档必须把这句话放在首页，避免第三方按 DSH 预期使用。

### 3.2 示例 A：最小 agent（设计稿 API；`main` + assembly 约 20 行）

```go
// main.go —— 第三方 agent 产品的全部启动代码
package main

import (
	"context"
	"log"

	"github.com/zeokeel/zeokeel-rig"
)

func main() {
	app, err := rig.New(rig.Options{
		Assembly: "assembly.json", // 模块树：enabled/disabled/config
		// Registry 省略 = rig.DefaultRegistry()（agent + openai provider + 内置工具 + appinfo）
	})
	if err != nil {
		log.Fatal(err) // 预检失败：未知模块/缺 provider/依赖环/坏配置一次报全，0 个 fiber 创建
	}
	if err := app.Run(context.Background()); err != nil { // 阻塞到 SIGINT/SIGTERM，逆序卸载
		log.Fatal(err)
	}
}
```

```jsonc
// assembly.json —— 产品 = 一棵 entry 树（kernel module.FileConfig 格式；正式文件须去掉注释）
{
  "version": 1,
  "entries": [
    {"id": "core",   "module": "rig/appinfo", "config": {"name": "Acme Agent", "version": "0.1.0"}},
    {"id": "agent",  "module": "rig/agent",   "config": {"model": "deepseek-v4.1-flash", "max_iterations": 0}},
    {"id": "model",  "module": "rig/provider/openai", "config": {"base_url": "https://api.deepseek.com", "api_key_env": "DEEPSEEK_API_KEY"}},
    {"id": "tools",  "module": "rig/tools/builtin", "config": {"workdir": "."}}
  ]
}
```

### 3.3 示例 B：自定义 Module + Tool + Provider（设计稿 API；三方扩展三条主路径）

```go
// weather/module.go —— 自定义模块：注册一个工具，注册/注销由 kernel effect 托管
package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-kernel/module"
)

type Config struct {
	City string `json:"city"`
	Unit string `json:"unit"` // "c" | "f"
}

func Module() *module.Descriptor[Config] {
	return module.Define("acme/weather", func(ctx cordis.Context, cfg Config) error {
		if cfg.City == "" {
			return errors.New("weather: city is required")
		}
		return rig.AddTool(ctx, weatherTool{cfg: cfg}) // 内部 ctx.Effect 注销；重复工具名报错
	}).
		AddRequires(module.Requires(rig.KeyToolRegistry)).
		WithValidate(func(cfg Config) error {
			if cfg.Unit != "" && cfg.Unit != "c" && cfg.Unit != "f" {
				return fmt.Errorf("weather: unit must be c or f")
			}
			return nil
		})
}

type weatherTool struct{ cfg Config }

func (weatherTool) Name() string        { return "weather" }
func (weatherTool) Description() string { return "Look up current weather for a city" }
func (weatherTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`)
}
func (t weatherTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{Content: "sunny 26°C"}, nil // 演示；真实实现走 HTTP
}
```

```go
// echo/provider.go —— 自定义 Provider（实现 contracts/v1.Provider，stdlib 即可）
package echo

type Provider struct{}

func (Provider) Stream(ctx context.Context, req rig.Request) (<-chan rig.Chunk, error) {
	ch := make(chan rig.Chunk, 2)
	ch <- rig.Chunk{Text: "echo: ", Done: false}
	ch <- rig.Chunk{Text: req.Messages[len(req.Messages)-1].Content, Done: true}
	close(ch) // 契约：Done/Err 后必须 close；ctx 取消必须尽快关闭
	return ch, nil
}
```

```go
// main.go —— 注册自定义模块/Provider 后再 New
registry := rig.DefaultRegistry()
rig.Register(registry, weather.Module())                            // 模块
rig.RegisterProvider(registry, "acme/echo", "echo-*", echo.Provider{}) // provider 键 + 模型前缀
app, err := rig.New(rig.Options{Assembly: "assembly.json", Registry: registry})
```

### 3.4 示例 C：多产品裁剪（同一 SDK，不同组装）

```text
products/
├── assistant/        main.go + assembly.json（agent + openai + 内置工具）
├── acp-server/       main.go + assembly.json（agent + rig/acp 模块，v0.2）
└── minimal/          main.go + assembly.json（agent + echo provider，无网络，CI 用）
# 硬裁剪用 build tags/多 main（把不需要的模块包排除出编译）；软裁剪用 assembly.json 的 disabled
```

### 3.5 与 DSH profile/bundle 的对照 [外部引用]

| DSH | rig（Go 编译期） | 说明 |
| --- | --- | --- |
| profile `cordis.yml = []` + 5 层 patch 合成 | **产品 `assembly.json`（一棵树）+ 可选宿主侧 layers→merge 合并器（100–200 行）** | merge 器在 `rig/assembly` 可选提供：按 id 覆盖、`insert` 追加、name 防呆、not-found warn；**无 `!!js` 表达式** |
| bundle（npm 组合包） | **Go module + `rig.Register` 列表** | bundle = 一个导出若干 Descriptor 的 Go 包；版本即 go.mod |
| 运行时 npm 安装插件 | `go get` + 重编译 | 无运行时安装；多产品=多 main/build tags |
| `disabled: !!js` 动态判断 | 静态 `disabled` + 宿主在加载前用受限谓词（平台/env）过滤 entry 树 | 不允许配置执行代码 |
| HMR | 重建重启；开发期 watcher | 非目标 |

---

## 4. 扩展点与版本化（Q3）

### 4.1 稳定契约清单（`contracts/v1`）

| 契约 | 内容（设计稿） | 来源/理由 |
| --- | --- | --- |
| `Provider` | `Stream(ctx, Request) (<-chan Chunk, error)`；Request/Message/ToolCall/ToolSpec/Usage/Chunk | 旧 `provider.go:236` 已是 eino-free；直接升格为公共契约 |
| `Tool` | `Name/Description/Parameters() json.RawMessage/Invoke(ctx, ToolCall) (ToolResult, error)` | 旧 `tool.go:14`；补 `Parameters`（旧 ToolSpec 在 provider.go:184） |
| `History` | `Messages/Append`（memory 实现入 core；SQLite 走可选 module） | 旧 `history.go:22` |
| `Gate`（审批） | `RequireApproval/ Wait`；默认策略在装配配置中声明（如 exec/network 默认需批准） | 旧 `approval.go:31`；默认安全是 SDK 底线 |
| `Compactor` | v0.2（接口先占位，MVP 不实现） | 旧 `compaction.go:21` |
| `Event` | 封闭集：Token/Reasoning/ToolStart/ToolEnd/ApprovalRequest/TurnDone/Error（由旧 state 10 种事件裁剪） | 旧 `state/event.go`；closed-set 保证可演进 |
| `Logger`/`Settings` + `ToolRegistry`/`ProviderRegistry` | 最小接口；注册表是 kernel `ServiceKey` 服务，模块 Activate 时注册、`ctx.Effect` 托管注销 | 从现 contracts headless 部分迁移；与 kernel 回滚语义对齐 |
| `Agent` | `Run(ctx, sessionID, input) (<-chan Event, error)` / `Cancel(sessionID)` | 旧 `Agent.Run`（agent.go:668）语义；消费者=CLI/ACP/UI session 模块 |

**不进 contracts/v1**：UI 类型（BuildFunc/ScreenSpec/SlotItem/SettingsPageSpec/ToolRenderer/CommandSpec/
UIRuntime/Navigator/Theme/Locale）——它们是"桌面产品"的扩展面，放产品 `internal/contracts/ui` 或可选
`rig/ui` module（第三方桌面应用才 import）。**UI 槽位是否属 SDK？答案：不属于 headless SDK**，
与 00 报告"UI 帧循环归宿主"一致；若未来做桌面 SDK，把 ui module 升为公开契约即可，不破坏 headless。

### 4.2 扩展机制（四层，按稳定性排序）

1. **服务（kernel `ServiceKey` + Inject 门控）**：模块间协作的正道；第三方定义自己的
   `ServiceKey[T]`（或引用 rig 的键），kernel 预检保证类型/唯一性，服务撤回自动停用依赖方。
2. **键控注册表**：`ToolRegistry`（name 唯一）、`ProviderRegistry`（模型名前缀匹配，确定性）——
   由 rig core 模块 provide，第三方模块 Require 后在 Activate 注册；重复/冲突=激活失败并回滚。
3. **事件流**：`Agent.Run` 返回 `<-chan Event`（owner 消费）；跨模块观测用 kernel 事件/waterfall
   （`rig/agent/*`）作为 v0.2 扩展点；MVP 先保证 owner 流。
4. **装配配置**：typed config + `WithValidate`；默认值由模块 `Validate`/`Default()` 约定（01 结论）。

### 4.3 版本化与 kernel v0.1 演进隔离

- **contracts/v1 冻结策略**：v1 内只允许"新增字段/新增可选接口/新增错误码"；破坏性变更开
  `contracts/v2`（双版本并存 + 适配层）。DTO 在边界复制，不直接暴露 kernel 内部类型。
- **semver**：rig `v0.x` 期间允许破坏但必须 CHANGELOG + 迁移说明；`v0.3` 起 API 面收敛；
  `v1.0` 条件 = ≥2 个外部使用方 + headless 产品以 rig 组装稳定跑 ≥2 个版本周期。
- **kernel 隔离**：`rig/internal/kernelx` 是**唯一** import `zeokeel-kernel` 的包：
  `type Context = cordis.Context`、`type ServiceKey[T] = cordis.ServiceKey[T]`、`Define/Provides/
  Requires` 包装、`Loader` 包装。kernel 升级只改 kernelx + 跑 rig 全量测试（含移植自 kernel-split
  spike 的装配回归）；rig 的公开签名不直接出现 kernel 版本细节（类型别名保持兼容）。
- **兼容矩阵**：`COMPATIBILITY.md`（rig vX ↔ kernel >=vA,<vB；产品版本矩阵在 product 仓）；
  CI 跑 kernel 最低/最高支持版本两档。
- **eino adapter**：默认**不做**。如第三方确要复用 eino 模型，单独发布
  `zeokeel-rig-eino-adapter` module（它 require eino，不污染 rig core 依赖图）。迁移期 eino 只存在于
  `old/`（归档）或产品 `internal/einobridge`（如果确实需要双跑），不进 SDK。

### 4.4 与 eino 替换的衔接（分阶段）

| 阶段 | 做什么 | 退出判据 |
| --- | --- | --- |
| P0（与 M0 并行） | 从旧 runtime 的 **eino-free 接口**抽 contracts/v1（Provider/Tool/History/Gate/Event）；定义行为规格：把 31 个旧测试文件/8,882 行按能力列成 parity matrix（流式拼接、tool_call 增量、reasoning、多模态、取消、最大迭代、审批、compaction/checkpoint 标注 v0.2） | contracts/v1 评审通过；parity matrix 覆盖旧测试 100% 文件名 |
| P1（MVP） | `rig/agent` 原生 loop：流式 + tool-call 循环 + 取消 + 最大迭代 + Gate；用 fake provider 把 parity matrix 里 MVP 子集全部写成 rig 测试 | MVP parity 子集全绿；`go list -deps` 无 eino |
| P2（v0.2） | 迁移 session/store/prompt/tools 分批；compaction/checkpoint/multimodal/reasoning 作为可选 agent 能力或模块；旧测试逐批移植为规格测试 | 旧能力矩阵全部有 rig 对应用例；产品 headless 用 rig 跑通 |
| P3（收口） | 产品 agent core 完全切 rig；`old/` 归档删除或永久冻结；依赖图从 117 modules 收缩到 kernel+rig+mygo+sqlite（产品） | `go mod why eino` 在仓库无结果 |

关键判断 [判断]：**旧 eino 运行时不是"要移植的资产"，而是"要提取规格、丢弃实现的参考"**。
旧 runtime 的公开接口本来就是 eino-free 的（Provider/Tool/History/Gate/Compactor），eino 只藏在
Agent 实现内部（agent.go/model_adapter.go/modes.go/...）。因此"抛弃 eino"≈"用旧测试当规格重写
Agent 内部"，不需要在 SDK 里保留 eino adapter；这也把"eino 替换"和"SDK 抽取"合并成一次工作，
避免双轨叠加。

---

## 5. 仓库与模块策略（Q4）

### 5.1 候选方案对比

| 维度 | A 单仓单 module（rig 作为产品子包） | B 单仓多 module（app + rig，go.work） | C 三仓（推荐） |
| --- | --- | --- | --- |
| 三方 import 路径 | `github.com/zeokeel/zeokeel/rig` | `github.com/zeokeel/zeokeel/rig` | **`github.com/zeokeel/zeokeel-rig`** |
| SDK 独立 semver | ❌ 与产品同一 tag | ✅ 嵌套 module `rig/v0.x` | ✅ 独立仓 tag |
| 依赖树隔离 | ⚠️ rig 不 import mygo 也要跟着产品 go.mod 走（graph 有 mygo 需求） | ✅ rig 自己的 go.mod | ✅ 最干净（带信任收益） |
| 同仓原子改动 | ✅ 最方便 | ✅ 方便 | ⚠️ 跨仓要 go.work/两连提交 |
| CI/发布复杂度 | 低 | 中（多 module tags） | 中（三套 CI，可模板化） |
| pkg.go.dev/品牌 | 弱（挂在产品 module 下） | 中（子 module 可见） | ✅ 独立页面、README、issue |
| 迁移成本 | 后期拆 = 破坏 import | 后期拆 = 破坏 import | 一次到位 |
| 适合阶段 | 一次性产品 | 早期联调过渡 | **SDK 定位 + 长期** |

**推荐：C（三仓）**——`zeokeel-kernel`（已有）/ `zeokeel-rig`（新）/ `zeokeel`（产品）。
理由排序：① SDK 的核心卖点是"第三方信任 + 零依赖树"，独立 module 是最低要求（A 做不到干净隔离，
B 可以但 import 路径和品牌都挂在产品仓）；② 用户既定命名就是 `zeokeel-rig`，一次定路径避免未来
破坏性迁移；③ kernel 已是独立仓，团队已有三仓运维经验；④ 开发期用父目录 `go.work` 把三仓放一起，
跨仓改动不是阻塞项。**B 作为降级方案**：若前 4 周不想开新仓，可先在产品仓内建 `rig/` 嵌套 module
（独立 go.mod + `rig/vX.Y.Z` tag）开发，API 稳定后再拆到独立仓——此时 import 路径会从
`.../zeokeel/rig` 变为 `.../zeokeel-rig`，对早期用户（自己）可接受。

### 5.2 rig 仓内布局（根 module + 两个可选嵌套 module）

```text
zeokeel-rig/                          # module github.com/zeokeel/zeokeel-rig（tag v0.x.y）
├── go.mod                            # require github.com/zeokeel/zeokeel-kernel v0.1.1（唯一依赖）
├── LICENSE  README.md  COMPATIBILITY.md  CHANGELOG.md
├── rig.go                            # 公共门面：New/Options/Register/DefaultRegistry/Run
├── contracts/v1/                     # package v1：稳定契约（只 import kernelx + stdlib）
├── internal/kernelx/                 # 唯一 import kernel 的地方（别名/包装/升级隔离）
├── agent/                            # eino-free loop（stream/tool-loop/approval/cancel/iterations）
├── provider/                         # Registry + openai/（OpenAI-compatible streaming）
├── tool/                             # Registry + builtin/（read_file/list_dir/exec）
├── session/                          # History 接口的内存实现
├── assembly/                         # registry 帮助、assembly.json 加载、预检、可选 layers merge
├── host/                             # headless runner：signals/logging/settings/graceful exit
├── modules/appinfo/                  # build identity 服务模块
├── examples/{minimal,custom-tool,custom-provider,multi-module}/
├── ui/          go.mod               # 可选嵌套 module：.../zeokeel-rig/ui（tag ui/v0.x.y，requires mygo）
└── store/sqlite/go.mod               # 可选嵌套 module：.../zeokeel-rig/store/sqlite（tag store/sqlite/v0.x.y）
```

产品仓（`/data/mygo`）保持不变的三层：`cmd` → `internal/host`（desktop）→ `internal/assembly`
（产品 registry + assemblies），UI 全部留产品；`internal/contracts` 拆分为 headless（引用 rig）与
UI（产品）两份。**产品是 C 方案的第一个消费者（dogfood）**，这是 SDK 不过度设计的保险。

### 5.3 命名与法律（与 03 报告对齐）

- **工作名**：任务书给定 `github.com/zeokeel/zeokeel-rig`（package `rig`）；Go 按完整 import path 解析，
  与任何名为 `rig` 的包不冲突，§5.2 仓布局按此路径设计。
- **对外命名风险 [外部引用]**：03 §5.1 实测 `rig` 在 AI 领域已被 Rust `0xPlaygrounds/rig` 占用，Go 生态
  另有 `k0sproject/rig`（活跃）、`rigdev/rig`（归档）——同名+同领域，检索/品牌风险高。03 建议 `rig`
  只作内部代号，对外用 **ZeoKeel SDK**（`github.com/zeokeel/sdk`）。因此**首次发布前必须定名**：若采用
  03 建议只改 module path/import 前缀（预发布期成本可忽略），本报告其余内容不依赖字面名。
- **许可证 [外部引用]**：03 §5.3 推荐 **Apache-2.0**（明示专利授权、eino/ACP/wazero 生态主流、不授权
  商标恰好保护 ZeoKeel 品牌）+ NOTICE + DCO；kernel/mygo 的 MIT 与之兼容，示例代码可 MIT。产品仓当前
  无 LICENSE，发布前补。两仓各自 `SECURITY.md`、`CONTRIBUTING.md`（v0.1 后补）。

### 5.4 发布流程与兼容矩阵

1. **顺序/预发布**：kernel tag（很少变）→ rig CI 绿 → rig tag `v0.x.y`（可选 module 另打
   `ui/v0.x.y`、`store/sqlite/v0.x.y`）→ 产品 bump → 产品 CI（desktop/headless 装配 + Xvfb smoke）→
   产品 tag；里程碑用 `v0.1.0-alpha.N`（只保证示例可编译，不保证 API）。
2. **rig CI 门禁**（发布前必须全绿）：build/vet/test -race；`go list -deps` 依赖守卫（无
   mygo/eino/sqlite）；examples 全部 `go build`；`go doc -all` 导出 API 快照 diff（golden，人工审）；
   assembly 预检/回滚回归（移植 kernel-split spike）；`go mod tidy` diff；最小版本矩阵
   （kernel 最低/最新）。
3. **兼容矩阵**：`COMPATIBILITY.md` 表（rig 版本 | kernel 范围 | 产品版本 | 备注）；产品 `go.mod`
   钉 rig 精确版本；升级 rig = 单独 PR + 跑全量装配/UI 回归。
4. **离线开发/发布 smoke**：父目录 `go.work` 使用三仓（不入库）；CI 用 GOPROXY 缓存/私有 proxy；
   发布 smoke = 干净临时 module `go get <rig>@version && go build`。

---

## 6. Rig MVP 范围（Q5）

### 6.1 第一个版本必须包含（"组装即用"的定义）

| 交付 | 验收标准 |
| --- | --- |
| `contracts/v1` | Provider/Tool/History/Gate/Event/Logger/Settings + 键；无 mygo/eino import；api 快照入 CI |
| `agent` eino-free loop | 流式文本/tool-call 循环/取消/最大迭代/Gate；fake provider 单元测试 + -race |
| `provider/openai` | OpenAI 兼容流式（stdlib net/http）+ 重试；离线用 httptest 测；真实 API 手测 |
| `tool` registry + 3 内置工具 | read_file/list_dir/exec（exec 默认需审批）；重复名激活失败回滚 |
| `session` + memory History | 多轮上下文；接口可替换；SQLite 留可选 module |
| `assembly` + `rig.New/Run` | registry 显式注册、assembly.json 严格解析、预检 8 类、required 失败整树回滚、headless 优雅退出 |
| 3 个可编译示例 | minimal（echo provider，CI 无网可跑）/ custom-tool / custom-provider；示例进 CI build |
| 文档/测试/发布 | README（含"编译期模型非运行时插件"声明）、QUICKSTART/EXTENDING/ASSEMBLY/COMPATIBILITY；覆盖率≥75%、-race、依赖守卫、API 快照、examples build、离线可复现；`v0.1.0-alpha.1` tag + pkg.go.dev + LICENSE/CHANGELOG |

### 6.2 明确不做（进 v0.2+ 或永不做）

- 不做：session SQLite 持久化（可选 module）、compaction/checkpoint、多模态/附件、reasoning 展示
  策略、modes/子智能体/agentteam、MCP/LSP/ACP 客户端、memory/skills/presets/modelcatalog/prompt 管理、
  web search、UI（桌面）、eino adapter、运行时插件安装/热更、沙箱/权限隔离、代码生成器（`rig new`
  可 v0.2）。
- 不做 kernel 内改动（rig 不 fork kernel；升级走 kernelx）。

### 6.3 里程碑

```text
M0（~1 周，3–5）  rig 仓骨架 + kernelx + contracts/v1 草案 + CI + README/声明
M1（3 周，10–14） agent loop（eino-free）+ fake provider 测试 + parity matrix MVP 子集
M2（2–3 周，8–11）     provider/openai + tool registry/3 工具 + session/memory + assembly
M3（2–3 周，8–11）     headless runner + 3 示例 + 文档 + 发布门禁 + v0.1.0-alpha.1
M4（1–2 周，6–9）      硬化：race/覆盖率/API 快照/兼容矩阵/安全默认 + 产品 headless dogfood 接入
                                   ── MVP 合计约 35–50 人日（[估算]，假设见 §7.1）
```

---

## 7. 成本与风险（Q6）

### 7.1 人日估算

[估算] 假设：1 名熟悉该仓库的 Go 工程师、离线工具链、旧测试当规格、不新增功能、不含 UI、
不含 01/03 调研。

| 项 | 范围 | 人日 |
| --- | --- | --- |
| M0 仓/契约/kernelx/CI | 3–5 | 3–5 |
| M1 agent loop + parity 测试 | 10–14 | 10–14 |
| M2/M3 provider+tool+session+assembly，headless runner+examples+docs+发布门禁 | 16–22 | 16–22 |
| M4 硬化 + dogfood 接入 | 6–9 | 6–9 |
| **MVP 合计** | | **35–50** |
| 完整 v1（+ session/store/压缩/checkpoint/多模态/多 provider/MCP/LSP/ACP/memory/skills/工具全量） | 85–130 | **120–180** |
| 对照：00 报告 mygo 后端模块化估算 | 50–78 | 且 rig 抽取与模块迁移可重叠，非简单相加 |

依据：旧代码非测试规模 runtime 6,434 / provider 1,193 / tools 12,007 / session 8,840 / store 5,887
行；旧 runtime 测试 8,882 行可作规格；kernel 装配/预检能力已实测，不需要重写 loader。最大不确定项
是 **agent loop 行为等价**（审批/流式/取消/工具增量），M1 用 parity matrix 尽早暴露。

### 7.2 风险登记

| # | 风险 | 严重度 | 缓解 |
| --- | --- | --- | --- |
| R1 | **API 稳定性**（v0.x 破坏 + 早期用户流失） | 高 | contracts/v1 冻结策略、v0.3 后 additive-only、CHANGELOG/迁移指南、examples+API 快照进 CI |
| R2 | **SDK/产品双轨维护**（rig 与产品各自演化→长期两张皮） | 高 | 产品从 M4 起 dogfood：headless 产品先用 rig 组装；rig 不复制产品逻辑；单一 core 实现 |
| R3 | **eino 行为等价**（流式增量/tool_call 拼接/审批/取消/compaction 语义） | 高 | 旧 31 个测试文件当规格；parity matrix 逐项；old/ 冻结不删直到矩阵全绿；不引入 eino adapter 进 core |
| R4 | **UI 依赖泄漏**（rig 根 module 沾 mygo → headless/第三方膨胀） | 中 | `ui/` 独立嵌套 module；CI `go list -deps` 守卫；contracts 拆分评审项 |
| R5 | **版本碎片 + 依赖树污染**（kernel/rig/product 三版本交叉；被迫拉入 eino/sqlite） | 中 | COMPATIBILITY 矩阵 + rig 只暴露 kernelx + CI 双内核版本；rig core 零三方依赖、重型能力嵌套 module |
| R6 | **命名/品牌**（Rust 0xPlaygrounds/rig；裸名 "Rig"） | 中 | 完整路径 + README 免责声明 + 全名对外；法律结论见 03 |
| R7 | **生态预期错配**（第三方以为能"装插件"→失望） | 高 | 首页写死"编译期组装，无运行时安装"；对比 DSH；把 MCP/ACP 指为进程外扩展路线（03） |
| R8 | **安全默认 + 单人维护**（exec/网络工具滥用；三仓 CI/文档/示例退化） | 高 | exec/网络默认需 Gate 审批、无沙箱如实声明、SECURITY.md（03）；模板化 CI、示例当测试、发布 checklist + 里程碑闸门 |

---

## 8. 结论（Q7）

1. **做，但定位要准确**：ZeoKeel-Rig = **给 Go 开发者的 headless agent 组装 SDK**（"自己写 20 行
   main + 自己的 tool/provider/module，就能得到自己的 agent"），不是"运行时插件市场/无代码平台"。
   后者的期待请用 03 报告的 MCP/ACP 协议层满足。
2. **时机**：现在（M0，约 1 周）先锁定边界与契约；M1–M3 与产品 agent core 迁入**同批完成**
   （旧 eino 不迁移，用旧测试当规格→一次工作同时完成"抛弃 eino"与"SDK 抽出"）；MVP 目标 6–8 周
   （35–50 人日），`v0.1.0` 在 headless 产品以 rig 组装跑通后发布。
3. **"项目只需要做组装"的诚实回答**：
   - 对**第三方 Go 开发者**：成立——装配、生命周期、DI、预检、回滚、agent loop、provider 抽象、
     工具注册都由 rig 提供；他们仍需写：自己的 provider 配置/密钥、自己的工具（或挑选内置）、
     system prompt/策略、会话持久化选择、审批策略、部署形态（CLI/HTTP/ACP）。
   - 对**产品级桌面体验/非 Go 开发者/运行时安装**：不成立——UI、桌面宿主、MCP/LSP/ACP、memory/
     skills/presets 等仍是产品仓代码；这是编译期模型的边界（00 报告已论证）。
4. **对 mygo 产品的连带收益**：rig 抽取 = 后端 SDK 化 + eino 淘汰的同一工程；产品 UI/宿主留
   `zeokeel`，kernel 保持独立；三仓让依赖树干净、semver 独立，跨仓联调用 go.work 消化。
5. **Go/No-Go 闸门**：M0 后评审 contracts/v1（1 周内定不清 Provider/Tool/Event 三件则暂停）；
   M2 后若 headless 产品无法用 rig 组装出可运行 agent，则回退 B（单仓多 module）收缩范围。

---

## 9. 复现命令与证据索引

```bash
export PATH=/usr/local/go/bin:$PATH GOPROXY=off GOSUMDB=off GOFLAGS=-mod=mod
cd /data/mygo
find internal cmd -name '*.go' ! -name '*_test.go' | xargs cat | wc -l     # 2,986
go build ./... && go vet ./... && go test ./... -count=1                    # 全绿（离线）
grep -rln 'github.com/egoist/mygo' internal/contracts internal/uiroot | sort   # UI 耦合点
cd old
grep -rln 'cloudwego/eino' internal --include='*.go' | grep -v _test | wc -l   # 9
grep -rln 'cloudwego/eino' internal --include='*.go' | grep _test | wc -l      # 11
go list -m all | wc -l                                                        # 117
cat $(ls internal/runtime/*.go | grep -v _test) | wc -l                       # 6,434（测试 8,882）
```

| 结论 | 证据 |
| --- | --- |
| 内核能力/边界/性能 | `docs/research/kernel-split/01-kernel-audit.md`（600 行 + 43 项 spike）[外部引用] |
| DSH 装配模型/可照搬性 | `docs/research/kernel-split/02-dsh-anatomy.md` §3.4/§5 [外部引用] |
| mygo 现状与模块化阻塞、工作量 | `docs/research/kernel-split/00-report.md` / `03-mygo-module-graph.md` [外部引用] |
| eino 使用矩阵/替换边界；三方开放模式/命名法律/MCP·ACP | `docs/research/rig-sdk/01-eino-audit.md`、`03-openness-ecosystem.md`（task-10/12 并行产出，细节以其为准） |
| 旧 runtime 公开接口 eino-free | `old/internal/runtime/{provider,tool,history,approval,compaction}.go`；eino 集中在 agent/modes/model_adapter/stream/multimodal/history 辅助函数 |
| 新根 UI 耦合点 | `internal/contracts/contracts.go:5-9`（import mygo/ui）；`internal/uiroot/uiroot.go:22`；`internal/host/host.go`（desktop 半） |
| headless host 可抽模式 | `internal/host/host.go:254-297`（runHeadless 45 行） |
| kernel 零依赖 | `/root/go/pkg/mod/github.com/zeokeel/zeokeel-kernel@v0.1.1/go.mod`（无 require） |

---

*报告结束。设计中的示例代码为设计稿 API（标注 [判断]），kernel/module 调用形态来自 v0.1.1 实测
（01 报告）；落地时以 rig 仓 `examples/` 的编译测试为准。未修改产品代码，无 commit。*
