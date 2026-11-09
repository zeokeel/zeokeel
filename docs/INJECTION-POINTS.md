# ZeoKeel UI 注入点与模块注册清单

> 注入点模型与保留槽位全表见 [ARCHITECTURE.md §4/§5](ARCHITECTURE.md)。本文件是「当前已接线」
> 的落地清单（模块 → 注入点 → assembly 注册），随模块迁入持续更新。
>
> 当前状态（2026-11-06）：`welcome` / `toolcards` 已接入 `internal/assembly/registry.go`
> 与 `assemblies/desktop.json`；产品级装配由 `internal/ui/uimodtest/assembly_integration_test.go`
> 覆盖（desktop Load 6 实例 Active、真实 Root.View 可见全部示范模块、Close 后贡献消失）。
> 目录约定见 `docs/ARCHITECTURE.md` §4/§5；模块只 import `internal/contracts`、
> `internal/uikit`、kernel 与 mygo，不 import 其他模块实现 / uiroot / host。

## 1. 待注册的 Module() 列表

顺序无要求：kernel 的 provider-first 调度会先把 provider 排到 consumer 前，UI 模块在
assembly 内同时要求 `zeokeel/ui` 与 `zeokeel/appinfo` 两个服务，因此 appinfo 必须与 UI
模块在**同一份 assembly** 内启用（kernel 预检拒绝 required 无 provider）。

| Module ID | Go 包 | 注册调用 | Requires（服务名） |
| --- | --- | --- | --- |
| `zeokeel/appinfo` | `internal/modules/appinfo` | `appinfo.Module()` | —（provides `zeokeel/appinfo`） |
| `zeokeel-ui/home` | `internal/ui/home` | `home.Module()` | `zeokeel/ui`, `zeokeel/ui/navigator`, `zeokeel/appinfo` |
| `zeokeel-ui/about` | `internal/ui/about` | `about.Module()` | `zeokeel/ui`, `zeokeel/appinfo` |
| `zeokeel-ui/welcome` | `internal/ui/welcome` | `welcome.Module()` | `zeokeel/ui`, `zeokeel/appinfo` |
| `zeokeel-ui/toolcards` | `internal/ui/toolcards` | `toolcards.Module()` | `zeokeel/ui` |

（`zeokeel/host/group` 是 no-op 组容器，由 assembly 侧定义。）

## 2. 注入点清单

| 模块 | 贡献 | 身份 | Order | 注入点 / 渲染方 |
| --- | --- | --- | --- | --- |
| home | Screen | `home` | 10 | 宿主 screen 区域 |
| home | Command | `home.settings` | 10 | 宿主命令面（命令面板/快捷键） |
| home | 消费槽位 | — | — | `home.hero` / `home.composer` / `home.recent`（自身 `RenderSlot`，空态兜底） |
| home | 消费 ToolRenderer | — | — | `RenderTool("demo")`（工具卡区域，空态兜底） |
| welcome | SlotItem | `welcome.hero` | 10 | `home.hero`（替换 home 默认 hero） |
| welcome | Command | `welcome.copy-version` | 20 | 宿主命令面 |
| toolcards | ToolRenderer | `toolcards.demo-card`（Tool=`demo`） | 10 | home 的 `RenderTool("demo")` |
| about | SettingsPage | `about`（Section `general`） | 90 | 宿主设置面（sidebar 页导航 + page body） |
| about | SlotItem | `about.header` | 10 | `settings.header`（设置页头，所有设置页可见） |

`docs/ARCHITECTURE.md` §5 的保留槽位由宿主渲染：`sidebar.*`、`screen.*`、
`settings.*`、`overlay.*`；本壳层没有顶栏区域（全局动作在内容顶部 Toolbar/命令面板），
模块不要注册 `topbar.*`；`home.*` 由 home 模块渲染（含默认空态），`session.*` 等后续模块接管。

## 3. assembly.json 片段（desktop 产品，UI 子组）

```json
{
  "id": "ui",
  "module": "zeokeel/host/group",
  "children": [
    {"id": "home", "module": "zeokeel-ui/home", "config": {}},
    {"id": "about", "module": "zeokeel-ui/about", "config": {"title": "关于"}},
    {"id": "welcome", "module": "zeokeel-ui/welcome", "config": {}},
    {"id": "toolcards", "module": "zeokeel-ui/toolcards", "config": {}}
  ]
}
```

headless 产品不挂 UI 子组即可（当前无 UI 模块时 `Root.Validate()` 不通过，headless 不建窗口）。

## 4. typed Config（assembly.json 的 config 字段，缺省即默认值）

| 模块 | 字段（JSON） | 默认 |
| --- | --- | --- |
| home | `title`, `greeting`, `composerPlaceholder`, `recentTitle`, `recentEmpty` | 首页 / 编译期模块化的桌面 AI agent。 / 向 ZeoKeel 提问… / 最近会话 / 还没有最近会话。 |
| about | `title` | 关于 |
| welcome | `title`, `message` | 欢迎使用 ZeoKeel / 这是一个独立编译的 UI 模块，通过注入点出现在宿主中。 |
| toolcards | `title` | 工具调用 |

## 5. 已验证的扩展语义（headless，无显示环境）

- **新增模块不改宿主/home**：`ui/welcome` 激活后立即出现在 `home.hero`，
  命令 `welcome.copy-version` 出现在宿主命令面；dispose 后全部消失、home 默认 hero 回退。
- **工具卡注入**：`ui/toolcards` 激活后 home 的 `RenderTool("demo")` 出现工具卡；dispose 后
  home 回退到"还没有渲染器"空态。
- **设置面扩展**：`ui/about` 的 `settings.header` 贡献出现在所有设置页页头；dispose 后设置面
  回空态。
- **共享 UI 库**：`internal/uikit`（`PageHeader` / `Section` / `Bullet` / `KeyValueRows` /
  `EmptyState`），全部由 mygo 原生控件组合，模块不再复制布局代码。

## 6. 测试

```sh
export PATH=/usr/local/go/bin:$PATH GOPROXY=off GOSUMDB=off GOFLAGS=-mod=mod
go test ./internal/ui/... ./internal/uikit/... ./internal/modules/... -count=1
go test -race ./internal/ui/... ./internal/uikit/... -count=1
```

| 测试文件 | 覆盖 |
| --- | --- |
| `internal/uikit/uikit_test.go` | 四个 uikit 组件的 headless 渲染、空 subtitle / nil body |
| `internal/ui/uimodtest/integration_test.go` | home 槽位化组合与第三方注入、welcome 扩展、toolcards 工具卡、about 设置面、各模块 manifest |
| `internal/ui/uimodtest/assembly_integration_test.go` | 真实 `internal/assembly.Load(desktop)` 产品装配：6 个实例全 Active、真实 Root.View 出帧看到 welcome/toolcards/about、loader.Close 后贡献消失 |

`internal/ui/uimodtest` 是 test-only 包（只有 `_test.go`，不会编进产品）。它有两层测试：
模块级（kernel plugin 逐个激活，隔离验证注入/卸载）与产品级（真实 `assembly.Load(assembly.ProductDesktop)`）。
测试栈与产品一致：kernel root + uiroot 服务 + appinfo 后端模块 → 用 `ui.NewTester(Root.View, …)`
在内存中出帧，用 `HasText/Texts` 断言用户可见文本；
