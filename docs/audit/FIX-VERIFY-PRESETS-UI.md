# 预设（Preset/Persona）UI 可用性复核

> 复核者：product-auditor · 日期：2026-11-06 · 基线：HEAD `8e2ba5b` + Round3 工作树（presets Controller/agent 接线 + uiux-auditor 的 shell preset UI，均在工作树中、未提交）
> 方法：真跑 GUI（Xvfb 1600×1000、隔离 `ZEOKEEL_DATA_DIR`、`unshare -n`、精确 PID；二进制由工作树 `go build -o /tmp/zkp/zeokeel ./cmd/zeokeel` 构建 @01:27）+ 代码阅读 + SQLite 断言 + 代码级探针
> 关联：`docs/audit/PRESETS-USER-GUIDE.md`（用户文档）、`internal/presets`（7/7 测试绿）、Controller API `internal/session/controller.go:4460-4560`
> 截图：`.cache/audit/product-presets/shots/`（50 张中 20 张用于本报告）；探针：`.cache/audit/product-presets/probe-failopen.txt`

## 0. 结论

**Settings > Agent presets 页与会话 preset picker 可用性：CONFIRMED（可发布为技术预览）**。5 个主场景全部通过真跑验证：列表/覆盖标记/tools 数/Model、创建示例+Reload、坏文件 fail-closed 警告、picker 选择持久化、未知预设回退 warning、默认预设对新会话生效；与用户文档 §2/§3 一致（本次已同步文档）。

**残余（不阻塞 UI 上线，需修复）**：
1. **P2（建议 P1）集成 fail-open**：用户 preset 的非空工具 allowlist 若全部拼错/不存在，`internal/app/services.go filterRegistryByPreset` 会**保留全部工具**（探针见 §3.1）。UI 无法感知（Controller Load 不带 KnownTools，`ErrUnknownTool` 在 app 路径不会触发）。
2. **P3**：Controller 的 preset load 错误不暴露（UI 靠每帧自行 `presets.Load` 显示坏文件 warning，可用但非契约）；建议 `Controller.PresetLoadError()/PresetWarnings()`。
3. **P3 polish**：fail-closed 状态下页面用“用户文件”badge 判断文件存在，但行数据来自 builtins-only 回退（坏文件场景 Minimal 显示“用户文件 + 工具 8 个”），语义略混；默认标记文案硬编码英文 `Default`（中文 UI 混排）。

## 1. 前置：presets 核心与 P3 修复（已完成）

| 项 | 证据 |
| --- | --- |
| `internal/presets` GREEN 7/7（5 验收 + 示例 + 覆盖继承） | `go test -count=1 -v ./internal/presets` |
| **P3 修复**：覆盖内置预设可省略 `name`（先 merge 后校验；覆盖中声明的 `tools` 才做成员校验） | `internal/presets/presets.go` `Load` override 分支；`TestBuiltinOverrideInheritsMissingFields`；UI 证据 `shots/40/42`（Minimal 行显示内置名 + 工具 2 个） |
| Controller API | `Presets/ReloadPresets/SetSessionPreset/SessionPresetID/ResolvePreset/SessionPresetModel`；`preset_api_test.go` 4 测试绿 |
| 用户文档 | `docs/audit/PRESETS-USER-GUIDE.md`（§2/§7 已按实际 UI 更新） |

## 2. UI 复核矩阵（真跑，2026-11-06 01:27–01:32）

种子：`reviewer.json`（2 tools、persona+prompt、order 10）、`doc-writer.json`（3 tools、order 20）、`minimal.json`= `{"id":"minimal","tools":["read_file","bash"]}`（无 name 覆盖）。

| # | 场景 | 判定 | 证据（截图/DB） |
| --- | --- | --- | --- |
| UI-1 | Settings > 代理预设列表：3 builtin + reviewer/doc-writer；tools 数/Model/order/描述 | **CONFIRMED** | `shots/10-presets-page.png`、`40-presets-page-live.png`：Standard=全部工具、Minimal=工具 2 个、Creator=全部工具、Code reviewer=工具 2 个·order 10、Documentation writer=工具 3 个·order 20 |
| UI-2 | 覆盖内置无 name（P3 回归） | **CONFIRMED** | `shots/40/42`：Minimal 行名称为内置 “Minimal”、`用户文件` badge、`工具 2 个`（若 P3 未修则 Load 失败、页面只剩 3 builtins） |
| UI-3 | 坏 JSON fail-closed | **CONFIRMED** | `shots/31-broken-json-warning.png`：红色 banner `预设文件被忽略：presets: invalid preset JSON: …/presets/broken.json: unexpected end of JSON input`；列表回退 builtins；“用户文件”badge 语义略混（见 §4-3） |
| UI-4a | 创建示例预设（干净目录） | **CONFIRMED** | `shots/51-example-created.png` + `ls $DATA/presets`：创建 `reviewer.json`（模板）；已存在时 toast `示例预设已存在`（`shots/42`） |
| UI-4b | Reload | **CONFIRMED** | `shots/52-after-reload-reviewer.png`：toast `预设已重新加载`，新创建的 reviewer 立即出现在列表（Reviewer / 用户文件 / 工具 6 个 / order 10） |
| UI-5 | picker 当前态 + 列表 | **CONFIRMED** | `shots/12-preset-picker-open.png`、`53-picker-with-example.png`：header chip 显示当前预设；popover `会话预设` 列出全部 preset（name+description+选中 ✓，有 persona 的显示 `人设` tag） |
| UI-6 | picker 选择持久化 | **CONFIRMED** | `shots/20-picker-reviewer-selected.png`（选择 Creator 后 chip 更新 + toast `预设已设置，下一回合生效`）；DB：`session.preset.<sid>=creator` 且 legacy `session.mode.<sid>=creator` 同步 |
| UI-7 | 未知/已删预设回退 | **CONFIRMED** | `shots/30-ghost-fallback-header.png`：DB 写 `session.preset.<sid>=ghost` 后重启，chip 回退 `Standard` + 橙色 `预设不可用，已回退到标准模式`，会话仍可打开 |
| UI-8 | 用户文档一致性 | **CONFIRMED** | 文档 §2 已更新为实际 UI：设置→代理预设、按钮名（重新加载预设/打开预设目录/创建示例预设）、toast/警告文案 |
| UI-9 | 默认预设对新会话生效 | **CONFIRMED** | `shots/42-default-minimal.png`：Minimal 行出现 `Default` badge + toast `默认预设：minimal`；`settings.agent.preset=minimal`；`shots/44-new-session-default-minimal.png`：新会话 chip=Minimal、模式=极简模式 |
| UI-10 | 窄窗 900×600 | **PARTIAL** | `shots/54-narrow-window-header.png`：主窗 900×600 下 header preset chip 仍可见不裁切；Settings 独立窗口（900×840）未做窄窗重排验证 |
| UI-11 | 打开预设目录 | **未验证** | headless Xvfb 无 `xdg-open`，无法验证系统文件管理器；代码走 `pluginOpener` 与既有“打开配置目录”同一路径 |

## 3. 代码级集成问题（需 fix-security 跟进）

### 3.1 非空 allowlist 零匹配 → fail-open（P2，建议按 P1 修）

- 探针（`/data/mygo/.cache/probe-presets.go`，输出 `probe-failopen.txt`）：user preset `tools:["read_fiel"]`（拼错）经 `presets.Load(Options{UserDir: dir})` 正常加载；`VisibleTools("typo", [read_file,bash,write_file])` 返回 `[]`, `err=nil`。
- `internal/app/services.go:1843-1856` `filterRegistryByPreset`：`if err != nil || len(visible)==0 { return reg }` → **保留全部工具**。
- 根因：`Controller.Presets()` 的 Load 不传 KnownTools（`internal/session/controller.go:359`），`ErrUnknownTool` 不会触发；`VisibleTools` 又把“空 allowlist（=全部）”与“非空 allowlist 零匹配”都表达为空结果。
- 影响：用户写错工具名时，预设本应“受限”，实际获得**全部工具**（fail-open；与安全/产品意图相反），且 UI 无任何提示。
- 建议修复：`presets.VisibleTools` 明确区分两态（新增返回值或 `Preset.Tools` 判断）；`filterRegistryByPreset` 对“非空 allowlist + 零匹配”返回空 registry 并通过 `ResolvePreset` warning/UI 提示；或 app 构建时用实际 tool names 重新校验 preset（Load with KnownTools / 单独 `ValidateKnownTools`）。补测试：`internal/app` 断言 typo allowlist 不放大；`internal/presets` 断言零匹配返回空集。

### 3.2 Controller 不暴露 preset load 错误（P3）

- `controller.go:359-368`：坏文件时 fallback builtins-only，但 error 未保存；当前 UI 通过每帧 `presets.Load` 自检（`presets_ui.go:presetLoadError`）显示 banner——工作但非契约，且 Controller 使用方（CLI/headless/ACP）拿不到原因。
- 建议：`Controller.PresetLoadError() error` 或 `PresetWarnings() []string`；保留 fail-closed 行为。

## 4. 可用性小问题（P3 polish，可后续）

1. **默认标记 i18n**：`presets_ui.go` presetRow 中 `ui.Text(c, "Default")` 硬编码英文；中文 UI 显示 “Default”。建议 `a.T("settings.default")` 或 `pluginLocalized("Default","默认")`。
2. **fail-closed 列表语义**：坏文件时列表来自 builtins-only 回退，但行内 `用户文件` badge 按“文件存在”判断（`presetFilePath`），出现「Minimal 用户文件 + 工具 8 个（内置值）」的矛盾组合；建议 banner 存在时抑制 user-file badge 或标注“last good registry”。
3. **picker 中的 ID/Model 摘要**：popover 仅显示 description + persona tag；`persona`/`prompt` 全文摘要按契约应可见（当前仅 tag），用户选择前无法预览人设内容（非阻塞，建议 hover/tooltip 或选中后 header 摘要）。
4. **示例模板文件名**：`reviewer.json` 与用户已有系统/示例同名时只会提示已存在（设计合理，但建议改 `example-reviewer.json` 避免与真实 reviewer 混淆）。
5. **打开预设目录**：Windows/macOS 未真机验证（headless 跳过）。

## 5. 结论与建议

- **可以按“技术预览”发布 presets UI**：核心路径（列表→创建示例→Reload→picker 选择→持久化→回退→默认预设）全部真跑通过，与用户文档一致；无 P0/P1 UI 阻断。
- **发布前建议修**（1 项）：§3.1 fail-open allowlist——用户预设工具名写错会静默放大权限，属于安全语义问题，建议本轮修掉或至少在 Settings 页显示 warning。
- **后续**（P3）：§3.2 Controller warnings API、§4 polish。
- 文档交付：`docs/audit/PRESETS-USER-GUIDE.md`（建议 FIX-REPORT/README 各加一行引用）；本报告作为 UI 复核证据。
