# ZeoKeel 预设（Preset / Persona）用户文档

> 版本：2026-11-06（FP-06）· 适用：ZeoKeel 桌面端 / headless · 相关代码：`internal/presets`、Controller preset API
> 本文是用户视角说明；实现契约见 `internal/presets/presets.go` 包注释与 `docs/audit/FIX-VERIFY-PRODUCT.md`。

## 1. 预设是什么

预设（Preset）是一个命名的 Agent 组合，可以同时改变四件事：

| 组成 | 作用 |
| --- | --- |
| `tools` | 模型可见的工具白名单；空数组/省略 = 全部已注册工具 |
| `persona` | 该会话的人设前缀（替换部署级 persona，只影响选了此预设的会话） |
| `prompt` | 附加系统提示词（紧跟在 persona 之后注入，顺序稳定） |
| `model` | 该会话的默认模型 ID；空 = 使用应用默认模型 |

三个内置预设（不可删除）：

| ID | 名称 | 行为 |
| --- | --- | --- |
| `standard` | Standard | 全部工具 + 默认人设/模型（历史行为） |
| `minimal` | Minimal | 仅 `terminal_*` 六个终端工具 + `read_image` + `working_directory` |
| `creator` | Creator | 全部工具 + 通过设置工具/技能定制 ZeoKeel 的指引 prompt |

## 2. 选择预设（UI 已可用，2026-11-06 实机复核）

- **设置页**：Settings（Cmd/Ctrl+,）→ 侧栏 **代理预设 / Agent presets**。页面列出全部预设（内置在前，用户预设按 `order, id` 排序），每行显示名称、描述、`ID`、工具数量（`全部工具` 或 `工具 N 个`）、`Model`（若有）、`order`（若设置），以及：
  - `Default` 徽标 = 新会话默认预设；
  - `用户文件` 徽标 = 来自 `<数据目录>/presets/*.json`（或覆盖了内置预设）；
  - 右侧 `设为默认 <名称>` = 把该预设设为 `agent.preset`。
  - 顶部按钮：**重新加载预设**（Reload，改完 JSON 用这个）、**打开预设目录**、**创建示例预设**（写入 `presets/reviewer.json` 模板；已存在则提示）。
- **会话级**：会话头部（对话/轨迹标签上方）的 preset chip（如 `Standard`）→ 点击弹出 `会话预设` 列表 → 选择即写入 `session.preset.<会话 id>`，下次回合同步 `session.mode.<id>`；不选时从 `agent.preset` 解析默认值。选中后有 toast「预设已设置，下一回合生效」，chip 立即更新。
- **未知/已删除预设**：自动回退 `standard`，chip 回退为 `Standard` 并显示橙色提示「预设不可用，已回退到标准模式」（不会报错、不会卡住会话打开）。
- **坏预设文件**：加载是 fail-closed 的——页面顶部显示红色横幅「预设文件被忽略：<错误>」，列表暂时回退到上一份可用注册表（通常是内置 3 项）；修好文件后点 **重新加载预设** 恢复。
- 切换后对**下一个回合**生效（当前回合不受影响）。

## 3. 用户预设文件

每个用户预设是一个 JSON 文件，放在：

```
<数据目录>/presets/<id>.json          # 默认 ~/.zeokeel/presets/
```

数据目录可用 `ZEOKEEL_DATA_DIR` 覆盖。文件名为 `*.json`；其它文件（README 等）会被忽略。一个文件一个预设。

字段：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `id` | 是 | 稳定 ID，小写字母/数字/`.`/`_`/`-`，1-64 字符；会话保存的就是它 |
| `name` | 新预设必填 / 覆盖内置可省略 | 显示名；覆盖内置时省略则继承内置名 |
| `description` | 否 | 选择器里的一行说明 |
| `tools` | 否 | 工具白名单；省略=继承/全部，`[]`=全部（显式清空） |
| `prompt` | 否 | 附加系统提示词 |
| `persona` | 否 | 该预设会话的人设前缀 |
| `model` | 否 | 默认模型 ID（须先在 Settings > Models 里添加该模型） |
| `order` | 否 | 用户预设排序（小的在前；同值按 ID） |

### 示例：新增一个代码审查员

```json
{
  "id": "reviewer",
  "name": "Code reviewer",
  "description": "Reviews diffs and reports findings without editing files.",
  "tools": ["read_file", "bash"],
  "persona": "You are a strict code reviewer. Report only verified issues, cite files and lines, and never edit files unless explicitly asked.",
  "prompt": "Prefer diffs and short findings over prose. Run the smallest command that proves a claim.",
  "order": 10
}
```

`tools` 里的名字必须是**当前构建实际注册的工具名**（写错会在加载时报错，而不是静默失效）。完整例子见 `internal/presets/testdata/reviewer.json` 与 `testdata/doc-writer.json`。

### 覆盖内置预设（field-wise merge）

用内置 ID 写同名文件即可覆盖内置预设，**只需写想改的字段**：

```json
{ "id": "minimal", "tools": ["read_file", "bash"] }
```

结果：仍叫 “Minimal”、描述/提示词沿用内置，但工具白名单被替换为 `read_file,bash`。规则：

- 文件里出现的字段覆盖内置值；**没出现的字段继承内置值**（包括 `name`、`description`、`prompt`、`persona`、`model`）。
- 校验在**合并后**执行：覆盖文件不必重复内置的 `name`；但改成新 ID（没有同名内置）时必须同时写 `id` 和 `name`。
- `"tools": []` = 显式清空白名单，即“全部工具”；与“省略 tools（继承内置白名单）”不同。

## 4. 校验与故障排查

加载是 **fail-closed** 的：任一用户文件不合法，本次加载整体失败，上一份可用预设继续生效（不会出现半套配置）。设置页会显示文件路径 + 原因。

| 现象 | 含义与处理 |
| --- | --- |
| `invalid preset JSON` | 文件不是合法 JSON 对象；修 JSON 后点“重新加载” |
| `duplicate preset id` | 两个用户文件声明了同一 ID；删掉/改名其中一个 |
| `unknown tool in preset allowlist` | `tools` 里有当前构建不存在的工具名；改用实际工具名或删除该项 |
| `invalid preset` | 缺 `id`/`name`（新预设）、ID 非法（大写/空格/中文）或 tools 项为空 |
| 会话显示“preset not found; falling back to standard” | 该会话选择的预设文件被删或改名；重新选择或恢复文件 |

修改文件后无需重启：设置 → 代理预设 → **重新加载预设**（Reload）；新文件立即可见。

## 5. Persona / Prompt 注入语义（给自定义预设作者）

- `persona` 注入为 `preset:persona-prefix` 段，顺序等同 DSH `DEPLOYMENT_PERSONA_PREFIX`（0）：它**替换**该会话的部署级人设前缀，不影响其它会话。
- `prompt` 注入为 `preset:instructions` 段（顺序 = persona 之后一位），用于行为约束（例如“先读最小验证命令”）。
- 两者都为空时，该预设不贡献任何提示词段（继承全局提示词）。
- persona/prompt 文本按用户原文注入，不做翻译；可中英混写。

## 6. 与 model 的交互

`model` 只是“该预设会话的默认模型”：会话若在 picker 里显式选过模型，则以会话选择为准；模型 ID 不存在时回退应用默认并记录警告。内置 `standard/minimal/creator` 不绑定模型。

## 7. 当前接线状态与已知限制（2026-11-06）

- **已接线并实机验证**：数据层（`internal/presets`）、Controller API（`Presets/ReloadPresets/SetSessionPreset/SessionPresetID/ResolvePreset/SessionPresetModel`）、Agent 构建（工具白名单过滤、persona/prompt 注入、会话默认模型）、Settings > 代理预设页、会话 header preset picker。真跑复核见 `docs/audit/FIX-VERIFY-PRESETS-UI.md`。
- **已知限制（修复跟踪中）**：
  1. 用户预设 `tools` 中写错工具名时，当前构建不会在加载时报错，而是可能退化为“全部工具”（fail-open）；在修复前请只填写实际存在的工具名（设置页会显示工具数量，可用于核对）。
  2. 坏文件时的红色警告由设置页自身检查展示；命令行/headless 使用方暂不能从 Controller 直接读取加载错误。
- 覆盖内置预设时 `name` 可省略（继承内置名），这是 2026-11-06 修复后的行为；更早版本会要求写 `name`。
- 本文档由 `docs/audit/FIX-VERIFY-PRESETS-UI.md`（UI 可用性复核）引用。
