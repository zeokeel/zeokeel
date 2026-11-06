# 产品完整性审计报告（ZeoKeel）

> 审计者：product-auditor · 日期：2026-11-05 · 基线：审计开始 `7eebe47`，审计期间并发推进到 `20f1399`（行号以验证时 HEAD `20f1399` 为准；截图用 `dist/zeokeel-linux-amd64`（构建自 `7eebe47`））
> 方法：静态代码阅读 + 隔离数据目录真跑 GUI（Xvfb 1600×1000、`unshare -n` 独立 netns、`ZEOKEEL_DATA_DIR=/tmp/zk-audit-product/data`、xdotool/import 取证）+ SQLite 直查 + DSH 参考包/既有审计文档对照
> 限制：未用真实模型（成本限制，仅 dummy endpoint 验证失败态）；托盘/系统通知在 Xvfb 无系统托盘与 D-Bus，未做真机验证；未解析 DSH 会话文件内容；a11y 真机未验证。证据索引：`../../.cache/audit/product/evidence-notes.md`，截图：`../../.cache/audit/product/shots/`。

## 0. 结论摘要

以「全功能 AI Agent 桌面端工具」为目标，ZeoKeel 的**单机核心 agent 体验已成形**（会话、工具、审批、插件开关、记忆、fork/export、快捷键编辑、主题/语言切换都真实可用），但**端用户产品面仍是「能用」而非「可交付」**：首启把配置责任全丢给用户且关键错误提示会被静默丢弃；自动化失败不可见；MCP/Skills/AGENTS.md 只能手改文件；桌面生命周期（自启、全局快捷键、更新、多窗口、拖拽、安装包）几乎为空；「DSH 桌面替代/迁移器」的定位没有任何迁移实现支撑。建议在发布前补齐 8 项 P1。

| ID | 严重度 | 标题 | NEW/KNOWN | 修复量 |
| --- | --- | --- | --- | --- |
| PD-01 | P1 | 无模型首启：无引导，且 ErrNoAgent 提示在 Enter 发送路径被静默丢弃 → 空会话、零反馈、messages=0 | NEW | M |
| PD-02 | P1 | 添加模型无连通性测试、无 provider 预设、无模型清单 | NEW | S |
| PD-03 | P1 | 单实例锁 = 固定回环端口 49731；二次启动静默退出、不激活已有窗口 | NEW | S |
| PD-04 | P1 | 定位「DSH 桌面替代/迁移器」但零迁移/导入能力 | NEW | L |
| PD-05 | P1 | 设置面缺通知/隐私与数据/诊断导出/代理/更新/重置等关键页 | NEW | M |
| PD-06 | P1 | Automation 失败与历史不可见、不绑定 workspace/权限/模型、仅前台运行 | NEW | M |
| PD-07 | P1 | MCP / Skills / AGENTS.md 无管理 UI，仅手改文件 | NEW | M |
| PD-08 | P1 | 插件双界面不一致（4 vs 6）、Add plugin 死 CTA、实验开关无功能 | NEW | S |
| PD-09 | P1 | 失败回合直接展示内部错误栈（NodeRunError/node path），无建议/重试 | NEW | S |
| PD-10 | P1 | 重启不恢复上次会话；草稿仅内存，退出即丢 | NEW | M |
| PD-11..PD-22 | P1/P2/P3 | 桌面生命周期缺口、安装包/更新缺失、明文 key、Home 不可返回、i18n、workspace 校验等 12 项 | NEW(KNOWN 见 §4) | — |

## 1. 范围与方法

### 1.1 覆盖的产品面与证据来源

| 产品面 | 静态证据 | 运行证据 |
| --- | --- | --- |
| 首启/Onboarding | `internal/shell/home.go`、`internal/shell/models_ui.go`、`internal/shell/workspaces.go` | shots 01/04/06/07/08/31/37/77/84 |
| 设置完备性 | `internal/shell/settings_ui.go:340-600`、`i18n.go`、`plugin_details.go:668-720` | shots 30/31/32/33/34/35/47/48 |
| 管理界面 | `plugins_ui.go`、`memory_ui.go`、`automation_ui.go`、`workspaces.go`、`session_search.go` | shots 25/32/33/41/43/45/66/67/68/73 |
| 桌面生命周期 | `internal/app/app.go`、`cmd/zeokeel/main.go`、`packaging/`、`Makefile` | 单实例冲突实测、窗口溢出截图 01 |
| 生态与迁移 | 无迁移代码；DSH `/root/.dsh` 布局对照；`cmd/zeokeel/main.go` 无参数 | — |
| i18n / a11y | `i18n.go`（446 条双语目录 + 244 处 `tr()/menuText()` 内联对） | shots 47/49/50/51/60（英文界面里仍显示「新建会话」） |

### 1.2 用户视角能力矩阵

| 能力 | 现状 | 缺口 | 严重度 | NEW/KNOWN |
| --- | --- | --- | --- | --- |
| 首启引导（欢迎/清单/示例任务） | 无；首屏只有 hero + composer | 无引导、无示例、无「配置模型」CTA | P1 | NEW |
| 无模型时的错误反馈 | toast 代码存在但在 Enter 发送路径被丢弃 | 用户零反馈，空会话 | P1 | NEW |
| 模型/Provider 配置 | 单 OpenAI 兼容协议，名称/URL/key/model/effort 五字段 | 无 provider 预设、无连通性测试、无模型清单拉取 | P1 | NEW |
| 多模型/key 管理 | 多模型列表、默认、编辑、删除；首模型自动默认 | 删除无确认；无 per-session key 校验 | P2 | NEW |
| 语言 zh/en 切换 | 设置内切换，主要屏幕全覆盖 | 系统生成标题冻结创建语言；托盘/日期未本地化 | P2 | NEW |
| 快捷键自定义 | 13 条可编辑、持久化、可恢复默认 | 覆盖少；无冲突检测 | P2 | NEW |
| 权限默认 | Full/Workspace/Read-only/Auto-review 四档全局默认 | 无 per-workspace/per-task 权限 | P2 | NEW |
| 外观/字号 | 浅/深/跟随系统 + 会话字号 + 系统 TextScale | 无主题色/密度自定义 | P3 | KNOWN |
| 通知 | 完成/失败/待审批三类，点击聚焦会话，内容隐私安全 | 无开关/免打扰/动作按钮/通知中心 | P2 | KNOWN(部分) |
| 数据与隐私设置 | 无 | 无 key 存储说明/遥测开关/清空数据/导出全部数据 | P1 | NEW |
| 诊断/日志导出 | 仅单会话 JSONL 导出 + 右侧 Log 页 | 无应用日志/诊断包导出 | P2 | NEW |
| 关于/版本/重置 | 版本 0.1.0、构建信息、复制、配置位置 | 无更新检查、无变更日志、无许可/第三方声明、无重置 | P2 | NEW |
| Workspaces | 侧栏树、增删改名、拖拽排序、嵌套、按 workspace 归档 | 无 per-workspace 设置；root 不校验存在/可写 | P2 | NEW |
| Memory / AGENTS.md | Memory 有列表/删除/清空；AGENTS.md 仅被 prompt 读取 | AGENTS.md 无查看/编辑入口；无「是否已注入」可见性 | P1 | NEW |
| Skills | 从 `<root>/skills/<name>/SKILL.md` 加载 | 无列表/创建/校验/错误提示 | P1 | NEW |
| MCP | 读 `mcp.servers` 设置或 `<root>/mcp.json` | 无配置表单、无服务器状态/连通测试、错误不可见 | P1 | NEW |
| Plugins | 6 个内置开关 + 4 个实验开关（持久化） | 两套 UI 列表不一致；Add plugin 死路；实验项无功能 | P1 | NEW |
| Automation | 增删改/启停/立即运行/筛选/搜索 | 无执行历史/失败原因/重试；不绑定 workspace/权限/模型；仅前台运行 | P1 | NEW |
| 会话管理 | 搜索（标题+正文 LIKE）、归档、重命名、pin、fork、JSONL 导出、反馈 | 无批量操作、无导入、无跨会话导出 | P2 | KNOWN(部分) |
| 托盘/关窗到托盘 | 有托盘 Show/Quit；关窗隐藏；窗口几何持久化 | 菜单英文硬编码、仅两项；无「关闭即退出」偏好 | P2 | NEW |
| 全局快捷键/开机自启 | 无 | 完全缺失 | P1 | NEW |
| 单实例/二次启动 | 固定端口锁；二次启动只打印 stderr | 不激活已有窗口；与数据目录无关 | P1 | NEW |
| 多窗口/拖拽文件 | 仅设置独立窗口；无拖拽 | 无分屏/多会话窗口、无 OS 文件拖入 | P2 | NEW |
| 自动更新/安装包/签名 | 仅 Windows unsigned zip | 无 mac/linux 包、无安装器、无签名、无更新 | P1 | KNOWN |
| DSH 迁移/导入 | 无 | 与「迁移器」定位矛盾 | P1 | NEW |
| ACP/SDK/headless | 无（GUI 单二进制，无 CLI 参数） | 无法接入编辑器/CI/平台 | P2 | NEW |

## 2. 详细发现（按用户旅程）

### [PD-01] 无模型首启：无引导，且发送失败反馈被静默丢弃 · P1 · NEW

- **现象**：首次运行（无 workspace、无模型）输入消息回车，先弹「添加工作区」；创建后再发送 → 应用切到 Session 屏、出现一个空会话、草稿留在输入框，但**没有任何错误提示、没有消息记录**；在会话内再次 Enter/点 Send 同样毫无反应。
- **证据**：
  - 运行：`shots/04-workspace-dialog.png`（添加工作区弹窗只有名称/路径/浏览）、`shots/06-after-workspace-created.png`、`shots/07-send-no-model-toast.png`、`shots/08-send-no-model-after.png`、`shots/77-nomodel-toast-t025.png`、`84-fresh-nomodel-t04.png`（0.25–1.35s 连续截图均无 toast/错误行）；`sqlite3 .../index.sqlite "select count(*) from messages;"` → `0`（sessions=1）。
  - 代码：`home.go:323-334` 与 `session_live.go:812-826` 在 `ErrNoAgent` 时直接用 `c.Toast(...)`；而 `composer_menus.go:364-373` 的注释写明「Outside a build pass mygo drops Context toasts」，正确做法是 `queueToast` + `app.go:281 flushDeferredToast`。Composer 的 Enter 回调正属于 build 之外，提示被丢弃。
  - 界面仅有 composer 末尾灰色「未选择模型 / No model」chip（`models_ui.go:374-381`），无引导、无 CTA。
- **影响**：新用户第一次尝试必然失败且无解释，会判断「应用坏了」；会话库里留下空会话（`messages=0`）。
- **复现**：清空 data dir 启动 → 输入任意文本回车 → 建 workspace → 再回车；观察 5s 内无任何反馈，DB messages=0。
- **建议修复**：把两处 `c.Toast` 换成 `toastText()`（S）；首启在 Home 提供引导条：检测无模型/无 workspace 时显示 setup 卡片（打开 Models、创建默认 workspace、写入 AGENTS.md 模板）（M）。

### [PD-02] 添加模型无连通性测试 / 无 provider 预设 / 无模型清单 · P1 · NEW

- **现象**：Settings > Models 空态只给「添加模型」；弹窗字段为名称/基础 URL/API 密钥/模型/推理强度，占位符写死 DeepSeek 示例；保存即完成，无任何校验或连通性验证。
- **证据**：`shots/31-settings-models-empty.png`、`shots/37-add-model-dialog.png`；`models_ui.go:225-300`（`modelDialog` 只有 `ui.Field` 文本输入）、`confirmModel`（`models_ui.go:342-372`）只校验名称非空；无「Test」按钮、无 `/models` 探测。
- **影响**：用户必须自己知道「OpenAI 兼容」协议、base URL 规则和模型 id；填错后第一次发送才失败（且错误原文见 PD-09）。对普通用户是不可完成的任务；对 DSH 迁移者也是重复劳动。
- **建议修复**：内置常见 provider 预设（DeepSeek/OpenAI/本地 Ollama 等）+「测试连接」按钮（调 `/models` 或一次 1-token completion）+ 失败分类提示（S/M）。

### [PD-03] 单实例锁 = 固定回环端口，二次启动静默退出 · P1 · NEW

- **现象**：进程启动时 `net.Listen("127.0.0.1:49731")`，失败即判「已在运行」；与 `ZEOKEEL_DATA_DIR` 无关。第二次启动不会激活/前置已有窗口，只在 stderr 打一行 `ZeoKeel is already running` 后退出（Windows GUI 无 stderr）。
- **证据**：`internal/app/app.go:111-121`（`acquire` 硬编码端口）、`cmd/zeokeel/main.go:12-16`（只 log，无窗口唤醒）；审计期间 4 名审计员/测试脚本共用一个固定端口，多个实例因此无法启动，只能 `unshare -n` 隔离。
- **影响**：① 双击图标「没反应」是桌面应用最典型的反馈缺陷；② 本机任何进程占用 49731 都可让 ZeoKeel 完全无法启动（无回退、无提示）；③ 无法同时开两个数据目录（便携版/多账户场景）。
- **建议修复**：改用数据目录内锁文件/命名信号量（per-data-dir），二次启动通过本地 IPC/`mygo.App.Activate` 唤起已有窗口并聚焦（S）。

### [PD-04] 「DSH 桌面替代/迁移器」定位没有任何迁移实现 · P1 · NEW

- **现象**：项目多处把产品定位写成「DSH 桌面替代/迁移器」（`docs/STATUS.md:7`、`docs/PLAN.md:363`、`docs/design/native-ui.md:4`），但 ZeoKeel 没有任何 DSH 数据导入：无会话导入、无 `.dsh/.credentials.yaml` 凭据导入、无设置映射，甚至没有 Import/Migrate 菜单项。
- **证据**：DSH 数据布局实测为 `/root/.dsh/{sessions/<workspace>/*, .credentials.yaml, storages, attachments, profiles}`；ZeoKeel 仅 `~/.zeokeel/index.sqlite`。全仓 `grep -rn "migrat\|import.*dsh" internal cmd` 无实现；设置/更多菜单只有 Settings/About/Quit。迁移后用户必须重建 provider/key、丢失全部会话历史与已装技能/MCP 配置。
- **影响**：定位承诺与产品能力直接矛盾；这是「替代/迁移器」叙事的核心功能缺口（也会让评估者给出不成立结论）。
- **建议修复**：最小可行迁移：一次性 importing wizard —— 探测 `~/.dsh` → 读 `sessions/<ws>` JSONL → 转 ZeoKeel message/event → 复制 credentials（掩码确认）→ 设置映射；或明确把定位降级为「DSH 同类替代」并在 README 写清不迁移（L / 或 M 只做会话导入）。

### [PD-05] 设置面缺关键页面（通知/隐私与数据/诊断/代理/更新/重置） · P1 · NEW

- **现象**：设置只有 6 页：通用、模型、记忆、内置插件、代理预设、关于。通用页只有权限、语言、快捷键、外观、行为、高级（高级仅一个 disabled 的上传会话日志开关）。
- **证据**：`shots/30-settings-general.png`、`33/35`；`settings_ui.go:340-379`（nav 列表）、`458-585`（General 全部 rows）；`grep -rn "notification" internal/shell/settings_ui.go internal/shell/preferences.go` 无命中；无 reset/清空数据/代理/更新检查。
- **影响**：桌面 Agent 用户会预期可控制「什么时候弹通知」「我的数据在哪、怎么删」「怎么导出日志给支持」「公司代理怎么配」「怎么升级」；当前只能手改 SQLite/环境变量，产品可信度与可支持性差。
- **建议修复**：至少补 Notifications（开关/免打扰）、Data & Privacy（key 存储位置与清除、导出/清空全部数据、遥测=无声明）、Diagnostics（导出应用日志/诊断包）、Network（HTTP(S)_PROXY）、Updates（检查更新/通道/当前版本）（M）。

### [PD-06] Automation 失败与执行历史不可见，任务未绑定 workspace/权限/模型 · P1 · NEW

- **现象**：Automation 页可增删改、启停、立即运行，但任务行只显示「上次/下次」时间；触发失败不会出现在任何 UI；调度器仅在应用进程存活时按 30s 轮询。
- **证据**：运行 `shots/41-automation-empty.png`、`43-automation-new-dialog.png`（无时区/workspace/权限/模型/通知字段）；代码 `internal/app/app.go:597-605`（失败只 `log.Printf`）、`:608-615`（run now 同样只 log）、`:621-625`（30s 轮询）；`internal/automation/scheduler.go:135-215`（出错只写 `LastError` 内存字段，UI 未读取）；`store/automation.go:14-25`（只有 last_run/next_run，无 history/status/reason）；`controller.go:330-334`（`CreateSession` 用 `ensureWorkspace()`，即触发时「当前 workspace」，任务表无 workspace_id）。
- **影响**：用户无法知道定时任务是否成功/为什么失败；多 workspace 下任务会跑错目录；后台无人值守执行仍使用全局默认权限（若默认 Full Access，任务会无提示地全权执行工具）；应用关闭期间任务静默错过。
- **建议修复**：AutomationRun 表（started/finished/status/error/session_id）+ 列表显示最近运行与失败原因；任务增加 workspace/权限（建议默认 Read-only 或 Workspace Write）/模型/通知开关；明确「仅在应用运行时执行」或提供常驻/开机自启（M）。

### [PD-07] MCP / Skills / AGENTS.md 无管理界面 · P1 · NEW

- **现象**：三者都只能靠手改文件：MCP 读 `mcp.servers` 设置 JSON 或 `<workspace>/mcp.json`；Skills 从 `<root>/skills/<name>/SKILL.md` 扫描；AGENTS.md 仅被 prompt 读取。设置与插件页都没有列表/编辑/校验/错误提示（MCP 甚至在 Settings>内置插件里只有一个开关，无「配置」按钮）。
- **证据**：`internal/app/services.go:726-728`（skills 目录扫描）、`:735`（mcp 来源）、`:1411-1422`（`connectMCP` 读 mcp.json）、`:388`/`:1134`（AGENTS.md 注入）；`plugins_ui.go:15-70` + `plugin_details.go:170-220`（detail specs 只有 subagent/web_search/agent_loop/shell，没有 mcp/skills）；运行 `shots/33-settings-builtin-plugins.png`（技能/MCP 行无「配置」）。
- **影响**：MCP 是「全功能 Agent」的生态入口，普通用户无法完成配置；技能写好但格式错时只有模型侧报错，用户看不到；AGENTS.md 项目记忆无任何入口。
- **建议修复**：Settings 内新增 MCP 服务器表单（stdio/http、env、JSON 预览、连通测试、状态徽章）、Skills 列表（名称/描述/路径/校验错误/打开文件夹）、AGENTS.md 查看/编辑（Markdown 预填模板）（M）。

### [PD-08] 插件双界面不一致 + Add plugin 死 CTA + 无功能实验项 · P1 · NEW

- **现象**：独立 Plugins 页内置插件只列 4 个（子代理/网络搜索/代理循环/Shell），Settings > 内置插件列 6 个（多「技能」「MCP 服务」）；两处同一插件描述文案不同；「添加插件」按钮打开解释「没有第三方市场」的弹窗；4 个实验开关（代理团队/语音输入/自动授权审核/开发者工具）均无对应实现。
- **证据**：`shots/25-click-plugins.png`、`50-plugins-en.png`、`33-settings-builtin-plugins.png`、`45-add-plugin-dialog.png`；`plugins_ui.go:242-251`（`builtinPluginRowsLive` 仅 4 行）vs `plugins_ui.go:15-70`（`builtinPluginDefs` 6 项）；`plugin_details.go:455-474`（Add plugin 说明）；`plugin_details.go:358-370`（voice_input 明确「此版本未内置」仍可开启）。
- **影响**：同一能力两个入口不同集合，用户会认为有功能丢失；主 CTA 是死路；可开启但无效果的开关是「假控件」。
- **建议修复**：统一为一个插件页（复用 `builtinPluginDefs`），有配置的才显示 Configure；把实验项移入「实验功能」折叠区并在开启时明确 toast「该功能尚未实现」或直接隐藏（S）。

### [PD-09] 失败回合展示内部错误栈，无操作建议/重试 · P1 · NEW

- **现象**：配置了一个不可达 endpoint 后发送，Session 内显示 `Turn failed` + `[NodeRunError] runtime: provider stream: openai: chat completions: Post "http://127.0.0.1:9/v1/chat/completions": dial tcp ... node path: [node_1, ChatModel]`（红色），没有「检查设置/网络/API key」的建议、没有重试按钮、没有「打开模型设置」链接。
- **证据**：`shots/63-dummy-send-t63.png`（构建自 `7eebe47`）；HEAD `20f1399` 刚改为「错误留在该回合内」（`session_live.go`、`state/snapshot.go`），但文案仍是原始 provider 栈。
- **影响**：普通用户无法从 `NodeRunError`/`node path` 得到任何可操作信息；支持成本高。
- **建议修复**：错误分类器（网络/鉴权/限流/模型不存在/上下文超限）→ 用户可读文案 + 操作按钮（打开模型设置、重试、复制诊断信息）；技术细节收进可折叠 details（S）。

### [PD-10] 重启不恢复会话，草稿只存在内存里 · P1 · NEW

- **现象**：关闭应用（Quit）再启动总是回到 Home，不恢复上次打开/最后活跃的会话；未发送的 composer 草稿只存于 `App.drafts` 内存 map，进程退出即丢。
- **证据**：重启后截图 `shots/60-restart-session.png`（Home，无恢复）；`internal/shell/draft_scope.go:14-40`（`drafts map[string]string` 只在内存，无 store 写入）；`grep -rn "last_session\|restoreLast\|OpenLast" internal` 无命中；窗口几何有持久化（`window_state.go`）说明会话恢复不是框架限制。
- **影响**：桌面 Agent 的核心期待是「回来接着干」；当前用户每天都从 Home 重新找会话、重打草稿。
- **建议修复**：持久化 `ui.last_session` 并在启动时只读打开（不自动提交）；草稿按会话落 store（或至少 `before-quit` 落盘），启动恢复（M）。

### [PD-11] Home 不可返回（信息架构死路） · P2 · NEW

- **现象**：进入任意会话后，侧栏只有「插件 / 自动化任务」两个导航项，ZeoKeel 品牌行不可点击；没有任何回到 Home 的入口（除删除/归档当前会话，或删除工作区）。
- **证据**：`workspaces.go:166-170`（`sidebarNavItems` 仅 2 项）；`chrome.go:81-92`（品牌行无 OnClick）；全仓 `Screen = Home` 只在启动/删除会话/删除工作区路径出现（`session_search.go:82`、`menus.go:350`、`workspaces.go:721/750/1309/1350`）。
- **影响**：Home 是唯一带「新建任务引导/模型 chip/工作区 chip」的首页，用户无法自然返回；新用户建完第一个会话后再也见不到 Home。
- **建议修复**：品牌行或侧栏增加 Home/新任务入口（或把 Home 合并为新会话空态），Cmd+N 可保留（S）。

### [PD-12] 桌面生命周期大块缺失（全局键/自启/多窗口/拖拽/托盘增强） · P1 · NEW

- **现象**：只有托盘（Show/Quit）、关窗到托盘、窗口几何/最大化恢复、设置独立窗口。缺失：全局快捷键（应用未聚焦时呼出）、开机自启、多窗口（会话窗口/分屏）、OS 文件拖入、托盘快捷动作（新建会话/暂停自动化）、关闭行为偏好。
- **证据**：`internal/app/app.go:568-585`（托盘只有 Show/Quit，文案硬编码英文）、`:214-223`（关闭一律隐藏，无偏好）；`grep -rn "GlobalShortcut\|autostart\|开机自启" internal cmd` 无命中；`mygo.NewWindow` 只出现 2 次（主窗口 + 设置窗口，`app.go:180`、`settings_window.go:182`）；无任何 file-drop 处理（附件只来自 Dialog.Open）。
- **影响**：「全功能桌面 Agent」与「浏览器 AI」的差异主要在 OS 集成度；当前即使与 DSH 桌面端比也缺自启/全局唤起这类高频能力。
- **建议修复**：OS 集成批次：开机自启开关（登录项/注册表/XDG autostart）、全局快捷键（Quick Ask）、托盘菜单加 New session/Settings、可选「关闭即退出」、会话拖成独立窗口（M/L）。

### [PD-13] 安装包/签名/自动更新缺失（复核 KNOWN） · P1 · KNOWN

- **现象**：仅 `dist/zeokeel-windows-amd64.zip`（未签名，README 让用户点 SmartScreen「仍要运行」）；无 macOS .app/dmg、无 Linux deb/AppImage/Flatpak、无代码签名、无自动更新/更新检查。
- **证据**：`Makefile` 目标只有 `windows/package-windows`；`packaging/windows/README-WINDOWS.txt` 自述 Known limits「Unsigned; no installer/auto-update yet」；`dist/` 实测仅 Windows zip；无 updater 代码。
- **影响**：发布形态不成立（macOS Gatekeeper 直接拦截未签名 app；Windows 无安装器=无开始菜单/无升级路径）。
- **建议修复**：三平台构建矩阵 + 签名（Developer ID/EV）+ 安装器（MSI/pkg/deb）+ 更新通道（Squirrel/自研 manifest）（L）。

### [PD-14] API key 明文存 SQLite，无凭据管理 · P2 · NEW

- **现象**：模型/搜索/服务商密钥以明文写入 `~/.zeokeel/index.sqlite` settings 表；无 OS keychain、无加密、无「清除凭据/重新输入」流程；隐私设置里也没有任何说明。
- **证据**：实测 `select key,value from settings where key like '%api%'` 直接读出 `model.<id>.api_key` 与 `provider.api_key` 明文（证据笔记 §2）；`store/model.go:20/28/48/434`（`api_key` 字段与 `ProviderAPIKeyKey` 同步写入）；`inspect` 工具对模型 settings 做脱敏（`tools/inspect.go:303`）说明代码知道这是敏感数据。
- **影响**：任何能读用户目录的本机进程/备份都会拿到密钥；企业用户不可接受；号被盗后无「吊销/移除」入口。
- **建议修复**：接入 keychain/Secret Service（或至少 AES-GCM + 用户口令/DPAPI 可选），设置里提供「已保存凭据」「清除」（M）。

### [PD-15] 默认窗口 1600×1000 超出常见笔记本屏幕 · P2 · NEW

- **现象**：默认窗口 1600×1000、最小 900×600；在 1280×800 屏幕上首帧右侧/底部被裁（Xvfb 实测），1366×768 笔记本同理；无「适配当前显示器」逻辑。
- **证据**：`internal/app/app.go:180-188`（Width 1600/Height 1000/MinWidth 900/MinHeight 600）；`shots/01-home-first-run.png`（1280×800 屏幕下 composer 右侧被裁）。
- **影响**：小屏用户第一次打开看到的是被裁界面。
- **建议修复**：启动时按当前显示器可用区域 clamp 到 ~90%（可用 `screen.WorkArea`/mygo 屏幕 API）（S）。

### [PD-16] i18n 收尾问题（系统标题/托盘/日期） · P2 · NEW

- **现象**：切到英文后，此前创建的系统默认会话标题仍显示「新建会话」；托盘菜单硬编码「Show ZeoKeel / Quit」；About 的 `version 0.1.0` 两种语言完全一致；时间格式统一 `2006-01-02 15:04`，未按 locale。
- **证据**：`shots/49-home-en.png`、`50-plugins-en.png`、`51-automation-en.png`、`60-restart-session.png`（英文 UI 中「新建会话」）；`app.go:570/577`（托盘英文）；`i18n.go` `about.version` 双语相同；`memory_ui.go:118`、`automation_ui.go:315-318` 固定格式。
- **影响**：英文用户看到中文残留会认为本地化不完整。
- **建议修复**：默认标题改为创建时存空、显示时本地化；托盘文案走 `T()`；日期用 `time.Format` + locale（S）。

### [PD-17] Workspace 路径不校验存在/可写 · P2 · NEW

- **现象**：「添加工作区」只校验名称与路径非空；不检查目录是否存在/可写/是否文件；创建后立即可用，直到第一次工具调用才报路径错误。
- **证据**：`internal/shell/workspaces.go:1275-1315`（`confirmWorkspace`）；`internal/store/workspace.go:21-47`（`CreateWorkspace` 只校验 name；root 任意字符串直接落库）。
- **影响**：首启 onboarding 中的 workspace 是必经步骤，错误路径会导致后续 agent 工具全部失败且错误不易归因。
- **建议修复**：创建时 `os.Stat` + 可写探测 + 不存在时提供「创建目录」选项（S）。

### [PD-18] 删除模型无二次确认 · P2 · NEW

- **现象**：Settings > Models 点「删除」立即删除（toast「模型已删除」），无确认框；而删除会话/工作区/自动化任务都有确认弹窗。
- **证据**：`models_ui.go:364-372`（`deleteModel` 直接 `Ctrl.Models().Delete`）；运行 `shots/64-delete-model-confirm.png`（点击后已删，无弹窗）。
- **影响**：误删后需重新输入 key；与产品其它破坏性操作策略不一致。
- **建议修复**：加确认（可顺带「同时删除已保存的 API key」说明）（S）。

### [PD-19] 无批量导出/备份/导入，无诊断日志导出 · P2 · NEW

- **现象**：导出只有「Download session log」（单个会话 JSONL，藏在会话头部 `...` 菜单），无「导出全部数据/备份/恢复」，无应用日志导出（stderr 只进终端/日志文件不可见）。
- **证据**：`shots/67-session-header-menu.png`；`home_live_menus.go:420`、`composer_slash.go:878`（export 只有单会话）；设置里无 import/export/backup；应用日志仅 `log.Printf` 到 stderr。
- **影响**：用户换机/迁移/报障都没有路径；与「迁移器」定位再次冲突。
- **建议修复**：Settings 加 Data 页：导出全部（zip: sqlite+AGENTS.md+skills+mcp 配置，脱敏 key）、导入恢复、打开数据目录、导出诊断包（最近日志/版本/环境）（M）。

### [PD-20] 无 ACP / SDK / headless 集成形态 · P2 · NEW

- **现象**：`cmd/zeokeel/main.go` 无任何 CLI 参数，进程只能以 GUI 形态运行；无 JSON-RPC/ACP server、无 headless 执行模式、无 SDK。DSH 侧存在 `dsh-acp`、`dsh-acp-app`、`dsh-sdk-jsonrpc-server`、`dsh-sdk-protocol` 等包。
- **证据**：`cmd/zeokeel/main.go` 全文（仅 `app.Run()`）；`grep -rn "acp\|jsonrpc\|headless" internal cmd` 只在注释/测试中出现。
- **影响**：无法被编辑器、CI、企业平台集成；「全功能桌面 Agent」生态位缺失。
- **建议修复**：先提供 headless 一次性执行 CLI（`zeokeel run --prompt ... --json`）+ 本地 IPC；再评估 ACP（M/L）。

### [PD-21] 通知不可配置、无动作按钮（KNOWN 部分复核） · P2 · NEW+KNOWN

- **现象**：完成/失败/待审批通知固定开启（能通知则通知），设置里无开关/免打扰/仅审批；通知点击聚焦会话（正面），但没有「允许/拒绝」动作按钮，退出应用后点击无意义。
- **证据**：`internal/app/notify.go:403-430`（三类文案）、`:443-462`（不支持则仅 log 降级）、`:515-525`（点击聚焦）；`grep -rn "notification" settings_ui.go` 无命中；`known-limitations.md` §3 已记录动作按钮未做（KNOWN）。
- **影响**：专注/演示场景无法静音；审批仍需回到应用操作。
- **建议修复**：通知设置（总开关/分类开关/免打扰时段）+ 审批通知动作按钮（跟随 KNOWN 计划）（M）。

### [PD-22] 文案/命名一致性 & 产品级 a11y 未验收 · P3 · NEW+KNOWN

- **现象**：① 「打开配置文件」实际打开「配置与数据库」对话框（`plugin_details.go:587-660`），命名不符；② 自动化空态「在会话中创建的任务会显示在这里」与本页「新建」按钮矛盾；③ 会话标题/自动标题无长度 UI 约束证据；④ a11y 仅有 headless 反射测试，known-limitations §1 明确真机 IME/屏幕阅读器/DPI/通知未验证。
- **证据**：`shots/33/41`；`known-limitations.md` §1；`i18n.go` 相关 key。
- **影响**：可信度与无障碍合规风险。
- **建议修复**：文案清理 + 真机 a11y/IME/DPI 冒烟纳入发布清单（S）。

## 3. 已验证良好的方面

1. **核心会话体验完整**：真实模型流式、工具审批卡片、中断/停止、失败回合边界、turn 终态标记都有产品级呈现（源码 + 既有 e2e；本轮验证了失败回合 UI）。
2. **语言切换真实可用**：zh/en 覆盖 Home/Session/Plugins/Automation/Settings/About 全部主要屏（`shots/47/50/51` 对比），446 条双语目录 + 244 处内联双语对；设置保存即时生效。
3. **快捷键编辑是「真」的**：13 条绑定可改、持久化到 `keybinding.<id>`、可恢复默认，且列表与注册表同源（`shortcuts.go:160-290`）。
4. **通知内容隐私友好**：通知只写事件类型（不含 prompt/工具参数/输出/会话标题），点击聚焦会话（`notify.go:403-430/515`）。
5. **会话管理扎实**：标题+正文搜索、归档视图、pin/rename/delete、拖拽排序、fork 复制附件字节、JSONL 导出含附件内容。
6. **桌面基础体验正确**：关窗到托盘、窗口几何/最大化持久化、设置独立窗口、单实例（虽实现粗糙但存在）、系统主题/TextScale/HighContrast/ReduceMotion 跟随、无遥测。

## 4. 已知问题复核（KNOWN 状态）

| 已知项（来源） | 现状 | 结论 |
| --- | --- | --- |
| 三平台安装包/签名/自动更新缺失（brief §7、FUNCTIONAL-PARITY C） | 仍只有 Windows unsigned zip，无更新代码 | 仍成立 → PD-13 |
| 通知动作按钮/退出后通知点击未做（known-limitations §3） | 仍只有整条通知点击聚焦 | 仍成立 → PD-21 |
| 通知/系统通知真机未验证（known-limitations §1） | 本环境无 D-Bus/托盘，无法验证 | 未验证（§5） |
| 插件仅内置、无第三方市场（产品边界） | 成立且有诚实说明弹窗，但 CTA 仍叫「添加插件」 | 边界成立，UI 需收口 → PD-08 |
| 无内核级沙箱（既定边界） | 成立；与产品面关系：Automation 无人值守仍用默认权限，需产品层约束 | 见 PD-06 |
| Plan 模式为软引导、无 exit_plan_mode（known-limitations §3） | 仍成立（不在本维度展开） | 仍成立 |
| TextScale 仍有约 169 处固定字号（known-limitations §2） | 代码中仍有大量显式 FontSize | 仍成立 |
| IME/屏幕阅读器/高 DPI 未真机验证（known-limitations §1） | 本轮同样无法验证 | 未验证（§5） |
| Goal round driver/`/goal`、`/compact` 未做（brief §7） | 仍成立（产品面：进阶自动化能力缺口） | 仍成立 |

## 5. 未验证 / 可疑项（明确「未验证」）

1. **真实模型端到端**：本轮未用真实 key（成本限制）；仅用 dummy endpoint 验证失败态。首启+真实模型的完整流程未验证。
2. **托盘/系统通知真机行为**：Xvfb 无系统托盘与通知服务；托盘图标、通知权限（macOS）、点击行为、退出后点击均未验证。
3. **三平台安装/签名/更新**：仅静态确认代码与产物缺失，未在 macOS/Windows 上验证 Gatekeeper/SmartScreen/升级路径。
4. **DSH 会话文件格式**：仅看 `/root/.dsh` 目录结构，未解析 JSONL/session v0-v4 格式，迁移复杂度评估为「高度可疑（L）」而非确认。
5. **a11y 产品级**：未做屏幕阅读器/键盘全流程人工验收（known-limitations 已列）。
6. **多显示器/DPI 下的窗口与设置窗口**：未验证（Xvfb 单屏）。
7. **About 页「项目仓库」链接 `https://github.com/zeokeel/zeokeel` 是否可达**：未联网验证，疑似占位。
8. **自动化跨重启的漏跑策略**：代码为「NextRun<=now 即触发一次」，关闭应用多日后的行为未在真机时钟下验证（推断为每次启动补跑一次，不重复）。

## 6. 优先级建议

### 发布前必须（P1，按投入产出排序）

1. **修首启/错误反馈闭环**（PD-01 + PD-02）：ErrNoAgent 用 `toastText` 修掉静默丢弃；Home 加 setup 引导卡；添加模型内置 provider 预设 + 测试连接。**这是新用户第一天能否用起来的分水岭**（M）。
2. **单实例与二次启动体验**（PD-03）：数据目录锁 + 激活已有窗口，移除固定端口硬依赖（S）。
3. **Automation 可信化**（PD-06）：运行历史/失败原因 + workspace/权限绑定 + 「仅前台运行」显式声明（M）。
4. **MCP/Skills/AGENTS.md 管理 UI**（PD-07）：至少 MCP 表单+连通测试、Skills/AGENTS.md 查看编辑（M）。
5. **设置面补齐 + 数据/隐私/诊断**（PD-05/PD-14/PD-19）：通知、数据与隐私（key 清除/清空数据/导出）、诊断导出、更新入口（M）。
6. **失败错误产品化**（PD-09）：错误分类 + 操作建议 + 重试（S）。
7. **会话/草稿恢复**（PD-10）：last session + draft 持久化（M）。
8. **迁移定位与能力一致**（PD-04）：要么做 DSH 会话/凭据导入，要么改掉「迁移器」定位文案（L/M）。
9. **打包与分发**（PD-13）：macOS/Linux 包 + 签名 + 自动更新/检查更新（L，需 CI 配合）。
10. **桌面生命周期批次**（PD-12）：开机自启、全局唤起、托盘快捷动作、关闭行为偏好（M/L）。

### 后续（P2/P3）

11. Home 导航入口（PD-11）、插件页统一（PD-08）、模型删除确认（PD-18）、workspace 路径校验（PD-17）。
12. 默认窗口适配小屏（PD-15）、i18n 收尾（PD-16）、文案一致性（PD-22）。
13. 批量导出/备份/恢复（PD-19）、通知设置与动作（PD-21）。
14. 多窗口/拖拽文件、ACP/SDK/headless（PD-12/PD-20）。

### 「全功能桌面 Agent」差距结论

- **能力广度**：agent 运行时侧已接近 DSH（会话/工具/审批/压缩/子代理/MCP/技能/定时），但**产品化外壳只完成了 60% 左右**：配置、错误、恢复、自动化可信度、OS 集成、分发这六条「桌面产品生命线」均有 P1 缺口。
- **是否可直接发布**：**不建议**。按当前状态可发布「技术预览/内测」，但不满足「全功能桌面 Agent」：新用户无模型时零反馈（PD-01）、自动化失败不可见（PD-06）、无安装包/更新（PD-13）、无迁移（PD-04）中任意一条都足以破坏首次体验或升级路径。
- **与 DSH 桌面端的产品差异**：ZeoKeel 在「自有视觉 + 纯 Go 单二进制」上有差异化，但在 DSH 已有能力（三平台分发、通知/托盘细节、插件/配置管理、ACP/SDK 生态）上仍是追赶者；「替代/迁移器」当前是目标而非事实，建议在 README/STATUS 中标注为 roadmap 并给出迁移时间表或明确不支持。
