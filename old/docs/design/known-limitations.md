# 已知限制与未验证项（v1 收口用）

> 更新时间：2026-11-06（审计修复轮 + 1.0 功能缺口统一收口）· 基线 `HEAD=3100af0`（收口轮见 [FIX-REPORT-CLOSURE](../audit/FIX-REPORT-CLOSURE.md)）；审计报告见 `docs/audit/`，缺陷状态清单见 [docs/BUGS.md](../BUGS.md)。
> 目的：把"没有做到/没有条件验证"的事说清楚，避免把目标达成的部分与已知缺口混在一起。发布前必须完成 §1 的真机验证；其余为后续排期。

## 1. 真机能力未验证（本环境无显示器）

| 项 | 现状 | 需要的验证 |
| --- | --- | --- |
| 中文/日文/韩文 IME | composer 用 mygo 原生 TextArea，理论上支持组合输入；未真机验证候选窗位置/组合期重排 | macOS/Windows/Linux 各一次 IME 冒烟（中文输入、删除、换行、候选窗跟随） |
| 屏幕阅读器 | a11y 树结构/名称/层级已用 headless 反射测试覆盖（Sidebar RoleTree/TreeItem、无名节点审计、键盘回归） | VoiceOver（macOS）、Narrator（Windows）、Orca（Linux）人工复核朗读顺序与操作 |
| 高 DPI/多显示器 | `ui.Render(...,scale=2)` 冒烟（3200×2000 非空、关键控件存在）；未真机 1.25x/1.5x/2x 切换 | 各平台 DPI 切换与跨屏拖动，确认无 1px 漂移/模糊 |
| 系统通知服务 | 通知状态机与 Notifier 注入测试覆盖；真实 mygo.NewNotification 只在源码级接入 | macOS 打包后通知权限、Windows balloon、Linux notification service 实机 |
| 窗口管理 | 窗口状态持久化、设置独立窗口均有单测与 headless 行为测试；原生窗口菜单栏已于 2026-11-05 退役（[ADR 0006](../../docs/decisions/0006-retire-native-menu-bar.md)） | 真机窗口恢复、双窗口焦点、无菜单栏下的快捷键/剪贴板/Quit 路径 |
| Windows shell 方言 | Windows 构建只暴露 `pwsh`（PowerShell）、POSIX 构建只暴露 `bash`；系统提示含显式运行环境段与平台化工具描述（[ADR 0009](../../docs/decisions/0009-platform-shell-stacks.md)）；所有子进程（bash/pwsh/terminal/jobs/git/MCP/UI 终端）启动时设置 `CREATE_NO_WINDOW`+`HideWindow`（[ADR 0011](../../docs/decisions/0011-windows-hidden-console.md)），不再弹 cmd 窗。本环境仅有 `GOOS=windows` 编译/vet/纯函数单测，未真机运行 | Windows 真机：pwsh 解析（pwsh→powershell 回退）、中文输出/路径、terminal 默认 pwsh、plan/只读模式下工具集、缺失 PowerShell 时的错误提示、命令执行时不再出现 cmd 窗口 |
| 全局快捷键（Windows/Linux X11/Wayland/macOS） | Windows：RegisterHotKey+消息泵；Linux/X11：purego dlopen libX11 + fake-Xlib 单测；**Wayland：纯 Go D-Bus + xdg-desktop-portal `GlobalShortcuts`（CreateSession/BindShortcuts/Activated/Session.Close，fake portal 测试通过；真实 portal 未验）**；macOS：Carbon/purego dlopen（compile-only，`carbon_mapping` 纯映射单测）；本环境无 macOS/显示器/Wayland 会话 | Windows 真机：注册成功、被占用（ErrOccupied）、设置变更即生效、退出释放；Linux X11 真机：真实 grab/冲突、键位映射、退出 ungrab、按住自动重复（debounce 待做）；**Wayland 真机：GNOME/KDE portal 同意对话框、Activated 投递、shortcut id 持久化、session 释放**；macOS 真机：HIToolbox 符号/回调 ABI/主线程事件循环、F21–F24 无标准键码、无 AppKit target 时降级 ErrUnsupported |
| LSP 工具（语言服务器） | stdio JSON-RPC client + fake LSP server 测试（definition/references/implementation/hover、UTF-16 坐标、输出上限、超时 kill）；`lsp.servers`/`.zeokeel/lsp.json` 安全门；未真机 gopls | 真机：gopls/clangd/pyright 等真实 server 的 initialize 兼容、大仓库 hover/引用延迟、server 崩溃隔离；冷启动无连接池（每次调用重启）需评估 |
| 会话引用 / 思考行 UI | canonical mention + 注入行折叠 disclosure + ReasoningRow 行为均有 headless Tester 测试；真实 IME/滚动/选中交互未真机 | 真机：长上下文滚动下引用折叠行展开、思考行 expand/collapse 动画与焦点、与 markdown 链接/选择交互不冲突 |
| 自动更新（GUI/CLI） | 引擎+CLI+GUI 下载/安装均有单测/httptest；app seam `DownloadAndInstallUpdate`/`applyPendingUpdate` 已交付；无真实 feed/签名 | 真机：真实托管 feed 的检查/下载/校验/**重启应用完成安装**、失败回滚（`.old-*` 备份）、无 feed 时提示 |
| ACP 客户端互操作 | `zeokeel acp` stdio e2e + fake provider；无真实编辑器 | Zed/Neovim 等 ACP 客户端：initialize、session/new（cwd）、流式 update、cancel、EOF/SIGINT 干净退出 |
| 预设（Presets） | catalog/UI/测试就绪；无真机视觉与交互验证 | 真机：预设页选择/覆盖生效（工具白名单/人设），用户 JSON 覆盖与错误提示 |


## 2. 框架/平台限制（已记录策略，未强改）

- **滚动条常显**：mygo v0.7.4 只有 hover overlay（6 DIP），无"始终显示"全局偏好；当前策略为保留 overlay + HighContrast 下加粗加深（`docs/design/native-ui.md` §12.3）。Windows 常显滚动条需框架能力或自绘，列为后续。
- **TextScale 覆盖**：v1.1 起系统文字缩放覆盖基座主题字号、会话正文/行高，以及 `design.Tokens` 品牌 type scale（`Tokens.Font/Text/TextMuted` 的 size/line 同比例缩放，`t.Font(design.TypeX)` 自动生效）；Home/Session/Settings 另有 21 处显式字号/`settingsText` 调用改为 TypeRole helper（chip 高度等同比放大），1.5x 关键屏已测不裁切。剩余：`internal/shell` 内仍有约 170 处显式 `.FontSize(...)`（随迭代变化；复核命令 `grep -rn "\.FontSize(" internal/shell --include=*.go | grep -v _test | wc -l`）与 `settingsText/nativeText` 等 helper 内的固定档位不随 TextScale 缩放——多数是图标/像素微调或自定义行高，其余屏幕（Sidebar/右栏/面板等）的批量转换留待 v1.2（详见 native-ui.md §12.5）。
- **HighContrast**：在品牌主题上做对比度增强（边框/次要文字向主文字混合、Focus 不透明、滚动条加深），不是专门的高对比主题，需真机/无障碍用户评估。
- **动画**：ReduceMotion 通过 `c.DisableAnimations()` 全局关闭；mygo 框架内建 transition 遵循，个别自绘动画（流式淡入）显式检查。

## 2.1 产品决定明确不做（不是能力缺口）

以下能力是**用户/产品决定明确不做**，不再作为"待实现/后续"跟踪，也不应出现在 parity 期望清单里：

- **PTC 代码运行时**（Programmatic Tool Calling / JS 沙箱）：2026-11-06 用户决策不做。原因：引入 JS/Node 运行时违反"纯 Go、CGO_ENABLED=0、单二进制"硬约束；PTC 模式与入口已从代码/UI 移除（ADR 0005 内置插件边界同样适用于此）。
- **语音输入**（DSH 实验能力）：2026-11-06 用户决策不做，UI 不提供入口。
- 相关历史记录保留在 `docs/history/` 与审计报告中，仅作追溯；若代码/文档仍出现 PTC/voice 入口，以本轮删除结果为准（代码删除由同批 worker 执行）。

## 3. 桌面交互遗留

- **通知**：当前/后台会话的 turn 完成/失败与**审批/提问等待**均已通知（controller hook）；通知 ID 替换、动作按钮、"退出应用后点击通知"未做；跨会话总量节流为简化策略（同会话完成 5s 冷却，审批不节流）。
- **设置窗口**：所有窗口均无菜单栏（原生菜单栏 2026-11-05 退役，[ADR 0006](../../docs/decisions/0006-retire-native-menu-bar.md)）；窗口间几何联动、大页滚动位置持久化未做；overlay 作为无显示器/创建失败的回退保留（行为略有差异）。
- **计划模式**：2026-11-05 起对齐 DSH（[ADR 0008](../../docs/decisions/0008-plan-mode-soft-alignment.md)）：入口为 `/plan` 命令 + composer 计划徽章（`/plan` 开、`/plan off` 或点徽章关、`/plan <消息>` 开并提交该消息）；计划模式是**软引导**（`plan:policy` prompt + `present` "Plan" + 审阅卡），**不再拦截任何工具**——需要强制只读规划的必须同时选择 composer 的「只读」权限（plan 与权限正交、可叠加）。**已实现**：独立 `exit_plan_mode` 工具（`internal/tools/exit_plan_mode.go`）、审阅卡 Keep planning（带反馈，`internal/shell/plan_ui.go` 的 `keepPlanning`）、pending 态徽章（`plan_ui.go`）；Home 草稿会话的 `/plan` 与 Home composer 打开 `/` 面板后的键盘焦点均已修复（后者见 native-ui.md §19.4/§19.5）。仍未做：计划审阅卡在真实窗口中的多显示器/窄窗视觉矩阵（真机项，见 §1）。
- **Composer 斜杠命令 / `@` 提及**（2026-11-05 对齐 DSH，native-ui.md §19.5）：
  - **已补齐（2026-11-06 统一收口）**：`/goal` 已接 Controller `GoalAction`（status/create/edit/pause/resume/complete/abandon/clear，本地执行不进模型；edit 保持同一 goal id/phase/rounds）；`/compact` 已接 runtime 手动压缩 API + 会话级**持久替换**（早期历史变 summary、事件回放、重开不回退）。见 [FIX-REPORT-CLOSURE](../audit/FIX-REPORT-CLOSURE.md)。
  - **保留的 ZeoKeel 策略**：未知 `/xxx` 行仍作为普通消息发送给模型（DSH 文档称未知行被拒绝，但其源码对未知行也走普通提交；两端口径不统一，按用户此前文档保留现有行为）。
  - **`@` 提及已补齐（2026-11-06 统一收口）**：行尾 chevron 指针下钻、`@"path with spaces"` 引号路径（含 `"`/`\` 转义与往返解析）、文件夹多级面包屑点击跳级、会话行 workspace/时间详情、跨工作区候选（同 cwd 优先）；会话引用已升级为 canonical `@[label](dsh-session:<id>)` + 受限只读快照作为第二条 user 消息注入（Chat 折叠显示）。已知差异：composer 显示明文 mention（mygo TextArea 无内联芯片）。
  - **caret 记录的框架限制**：mygo 在 composer 上方插入派生弹层时会重建编辑器元素、选区回落到文末；`recordComposerCaret` 用「draft 未变且 caret 落到文末」判定并回写原 caret（native-ui.md §19.5），真机 IME 组合期的极端 caret 行为仍需人工冒烟。
- **文本选择**：消息与工具卡中的 Markdown 内容（正文/代码）可选中并右键复制（mygo markdown）；其余 UI 文本未全量开启选择（侧栏、按钮、标题、设置说明等），桌面应用一般也不需要。
- **Trajectory**：已虚拟化（ui.List，1000 事件稳态帧 22.7ms→3.12ms，见 perf.md §8）；`FollowEnd=false` 为当前选择，若产品要"日志式跟随末尾"改一处即可。
- **工具输出**：超大工具结果 40 行封顶 + 展开，未虚拟化（当前规模可接受）；工具结果代码块语言映射为简化集合（bash/text 等）。超过 32KB 的工具结果在持久化时溢写到 workspace `.zeokeel/tool-results/`（见 §5 运行中统计），工具卡显示摘要与路径，完整内容可用编辑器打开；清理策略为每 workspace 保留最近 100 份，无 UI 内打开按钮（用文件面板/编辑器打开）。

## 3.5 已修复的评审判定项（保留记录）

- 浅色三级文字对比度（`#7E8796→#646C7A`，AA）、深色字标白边（`mark-dark@2x.png`）、破坏性操作确认、toolcard 错误面板 danger-on-tint（`DangerText #B91C1C`，5.7:1）、设置窗口重复关闭入口：均已修复并有测试/预览证据（见 STATUS 阶段 6 与 review-round3/4 附录）。

## 3.6 v1.1 已处理项（2026-11-05）

- 后台"等待审批"通知（controller `ApprovalRequestedHook`，所有会话）。
- Trajectory 虚拟化（ui.List；1000 事件稳态帧 7.3×）。
- TextScale 覆盖 design 类型系统（21 处关键屏显式字号转换；剩余约 170 处，复核命令见 §2 第 2 条）。
- opt-in 视觉回归工具（`scripts/visual-check.sh` + `TestVisualRegression`，CI 未强制）。
- P3 清理：plugins 采样色→token、settings-window 预览唯一关闭入口、known-limitations/preview README 措辞与尺寸。

## 3.7 2026-11-05 审计修复轮已处理项

- **AR-01 崩溃/日志诊断**：`cmd/zeokeel` 启动即配置日志文件（stderr+文件）、`debug.SetCrashOutput`、panic 写 crash 报告；`--version/--help` 不触碰数据目录。验证：`go test ./cmd/...`。
- **AR-02 CI/门禁**：新增 `.github/workflows/ci.yml`（本地与 CI 同用 `scripts/ci.sh`；race 覆盖 state/session/tools/runtime/store/agentteam/app，shell 单独 job `-timeout 20m`）；coverage 写 `.cache/coverage.out` 作为 artifact；`scripts/evolve/gates.sh` 的 flaky 重跑改为失败。
- **AR-03 版本注入/打包**：见 §5 构建条目；`package-linux`/`package-macos` 新增，Windows 行为不变。
- **RT-01/RT-06/RT-08**：workspace MCP 默认不执行（§5）、无整体超时 + 流空闲/重试设置（§5）。
- **迁移定位（PD-04）**：产品文案由"DSH 桌面替代/迁移器"降级为"DSH 同类替代"；最小导入器已交付（技术预览）：`internal/importer` 读取 DSH `sessions/**/session.v4.jsonl[.zstd]` 与 credentials **键名**（值永不读取），写 ZeoKeel store；`zeokeel import [--from] [--dry-run] [--json] [--max]` 已接 CLI（退出码 0/1/2，`--json` 输出 report）。
**导入范围（先说清楚）**：只导入**文本对话**（用户/assistant 文本与 reasoning）；工具调用/工具结果以合成文本行计数、不落 tool_calls；图片/附件/profile/storage/trajectory/usage 不导入；subagent 子会话默认跳过（`--include-subagents` 可含）；凭据只列键名，需在设置里重填 API key。
**导入质量（wave2 importer 已补一部分）**：① DSH 注入上下文过滤 ✅（`SkipSystemInjected`，source.kind != user 不再变用户气泡）；② assistant step 过滤 ✅（`SkipStepAssistant`，无可见文本的 step 消息不落库）；③ 幂等 ✅（`--manifest` 默认 `<数据目录>/dsh-import-manifest.json`，按源文件 hash/size/mtime 跳过；`--no-manifest` 可退回全量重导）；④ 图片统计 ✅（`images_dropped`）；⑤ skip 原因分解 ✅（`skips` 按 subagent/system_injected/step_assistant/unsupported_version/idempotent/...）；⑥ header version 守卫 ✅（未来版本显式跳过）。**2026-11-06 统一收口已补齐**：① app 运行中导入检测（默认拒绝 + `--force`，dry-run 不探测）；② 图片/附件对象迁移（`attachments/v1/objects|file-objects`/内联 base64 → store 附件行 + MessageID 关联；25MiB 帽/缺/超大/损坏计数跳过；`Lstat` 拒绝 symlink；失败会话补偿回滚）；③ manifest v2 旧版 key 一次性迁移不重导、损坏从 `.bak` 恢复或隔离重建、`>v2` fail-closed。**仍不导入**：工具调用/工具结果/轨迹/usage（messages 表无 tool_call 列，强行合成会破坏后续模型调用——设计保持不变）；损坏 manifest 重建可能重导一次（备份恢复成功则不会）。UI 入口为设置→数据与隐私→「从 DSH 导入」**命令弹窗**（展示/复制 `zeokeel import` 命令并提示先退出应用；**非应用内执行**）；独立复验见 FIX-VERIFY-IMPORT2 / FIX-VERIFY-WAVE23。

## 3.8 wave 1/2 独立验证残余（2026-11-05）

独立验证（`docs/audit/FIX-VERIFY-BACKEND.md`、`FIX-VERIFY-UI.md`、`FIX-VERIFY-PRODUCT.md`）确认 wave1 P0/P1 主体成立，以下残余仍在 BUGS.md 跟踪：

- **RT-01 三个残留**：workspace MCP 被忽略时的提示文案可能误导；Creator 的 `settings_set` 通道曾写 `mcp.*`（已加写白名单，需回归保持）；MCP 启动日志 args 可能含 secret（已 redact，需回归保持）。
- ~~**UX-04**：设置窗打开/移动后主窗口 popover 残留不重绘~~ → **✅ CONFIRMED FIXED（2026-11-06，Linux/Xvfb 无 WM）**：fix-security 真跑“popover→Ctrl+,→移窗→Escape”复现序列，popover 立即消失无残影（证据 `.cache/audit/fix-round4-ux04/*.png`）；根因澄清：mygo `holdFrame` 只在 `host.occluded()` 时挂帧，Linux GTK surface 未实现 `OccludableSurface`，不是残影原因；现有重绘链路（关闭 popover 状态 + `refreshWindows` + `Invalidate`）已正确。真实 WM/compositor 下仍建议 spot check。
- **版本显示（干净开发构建 = `0.1.0-dev`）**：About 已统一为 `shell.BuildVersionString()` 单一来源（硬编码 `version 0.1.0` 与 `pluginProductVersion` 已删）；**干净开发构建**（`go build`/`go run`，未经 ldflags）显示 `0.1.0-dev`（`cmd/zeokeel/version.go` 默认值），**发布构建**由 `scripts/build.sh` 用 `-ldflags "-s -w -X main.version/commit/buildTime"` 注入 git tag 版本/commit/构建时间（`make version` 可预览；`ZEOKEEL_VERSION` 可覆盖）。
- **PD-02b**：无 key 的本地 provider（Ollama 预设）可保存但发送被判"未配置"。
- **PD-04 导入质量 5 项**：注入上下文过滤、step 聚合、幂等、图片统计、UI 入口（见 §3.7 与 `FIX-VERIFY-PRODUCT.md` §2.6）。

## 3.9 wave 2 残余与延期（2026-11-05）

- **FP-16 ask_user 仍非阻塞**：回答作为下一轮用户消息（`internal/tools/ask_user.go:91` 注释：工具内阻塞会死锁回合）。阻塞式需要 interrupt/resume 语义，DEFERRED。
- **FP-11 checkpoint 屏障恢复**：屏障可写入、fail-closed 成立；但没有恢复 consumer——`requested`/`tool_pending` checkpoint 不被读取，恢复仍走 History/tool_calls（FIX-VERIFY-CORE2）。PARTIAL。
- **RT-10/逐 token 持久化**：已改为批量写（`eventBatchMaxBytes=8KB`、`eventBatchMaxDelay=200ms`，`internal/session/event_batch.go`）——**崩溃恢复窗口 ≤8KB/200ms** 的流事件，非流事件强制 flush；snapshot 每 token O(n) 拷贝（AR-22）与 events 保留策略仍待后续。
- **FP-17 skills 多 root**：当前仍单 root（`skills.root` 或 workspace `skills/`），多 root/项目级技能目录未落地。
- **UX-04 设置窗重绘**：已 CONFIRMED FIXED（Linux/Xvfb 无 WM，见 §3.8）；真实 WM/compositor 下 spot check 即可。
- **About 版本**：已统一为 `shell.BuildVersionString()` 单一来源（删除硬编码 `version 0.1.0` 与 `pluginProductVersion`）；预览已按新文案重生成。
- **RT-04 / AR-04 / AR-09 / AR-22 大项**：per-session Agent、shell 分层、schema v2、快照优化 DEFERRED（理由见 FIX-PLAN §7）。
- **自动更新 / Windows 安装器**：引擎 `internal/updater`（14 契约测试，含崩溃恢复）+ `zeokeel update` CLI（check/download/install/--json）+ 设置页「检查更新 / 下载并安装」（下载→SHA-256 校验→`ScheduleInstall`→重启安装；`app/update_install.go` 提供 apply-on-startup 与 replacer）已交付（GUI 安装经 shell `SetInstallUpdateHook` 路由到 app seam `DownloadAndInstallUpdate`，启动时 `applyPendingUpdate` 应用暂存更新，**重启应用完成安装**）；**托管 update feed 与代码签名/公证为外部依赖（DEFERRED，AR-03）**，无真实 feed/签名链路的真机安装验证；Windows 安装器仅为计划与 Inno/NSIS 草案（`packaging/windows/INSTALLER-PLAN.md`，未编译/未签名）；`release.yml` 发布未签名便携包。
- **全局快捷键（globalhotkey）**：**Windows** 已实现（`RegisterHotKey` + 消息泵）；**Linux/X11** 已实现（`x11_linux.go`：purego dlopen 系统 `libX11.so.6`，无 cgo、无新模块依赖；`XGrabKey` 四组 lock-mask 变体；fake-Xlib 单测 + opt-in 真 display smoke）；设置项 `global_hotkey` + app `syncGlobalHotkey` 接线。**未在真机验证注册/占用/冲突场景**（Windows 与 X11 均只有单测/交叉编译；X11 真 display smoke 需 `ZEOKEEL_X11_TEST=1`）。**macOS**：`globalhotkey_darwin.go` 用 purego dlopen `Carbon.framework`/HIToolbox 的 `RegisterEventHotKey`（纯 Go、无 cgo），`carbon_mapping.go` 映射层 5 个跨主机单测，**darwin amd64/arm64 交叉编译 + vet 通过，但 runtime 未在 macOS 真机验证**；注意点：① Carbon hot-key API 自 macOS 10.8 弃用但仍随系统提供，需真机验证 HIToolbox 符号与回调 ABI；② 注册必须在 AppKit 主线程/事件 target 上（当前 app 启动与设置路径满足；其他调用者需自行 marshal），无 target 时优雅降级 `ErrUnsupported`；③ F21–F24 在 macOS 无标准键码；④ Linux 按住热键可能自动重复触发，待 `XkbSetDetectableAutoRepeat`/debounce 后续处理。**Wayland（org.freedesktop.portal.GlobalShortcuts，需 D-Bus client）DEFERRED**；无 `$DISPLAY`/XWayland 不可用 → `ErrUnsupported` 降级并在 UI 提示。
- **ACP v1（编辑器/外部客户端）**：最小面已交付（initialize/session/new/prompt/cancel + session/update；stdio；`zeokeel acp`）。**Non-goals（DEFERRED）**：session/load|list、authenticate、client fs/terminal 方法、image prompt、MCP server 启动、session/request_permission（headless 自动 deny）；未与真实编辑器做互操作验证，只有 stdio e2e 与 fake-provider 测试。
- **AR-04 shell 重构**：已增量模块化（`manage_ui`/`settings_extra`/`presets_ui`/`session_*_async` 等新文件），但 `App` 仍是 God object（262 行字段/711 方法）、view 直连 store（AR-08）；整体分层重构 **DEFERRED**，分批计划见 [`shell-refactor-plan.md`](shell-refactor-plan.md)（P0–P4）。
- **AR-09/AR-07 数据库 schema v2（已落地）**：`SchemaVersion=2`，有序迁移步骤（每步单事务 + `schema_migrations` 日志，失败回滚到上一步）；v2 是 `messages.ordinal` 回填 + 索引（AR-07）。升级前用 `VACUUM INTO` 在 `<data dir>/backups/` 写一致性快照（`index-v<from>-to-v<to>-<UTC 时间戳>.sqlite`，0600；**备份失败则迁移失败 = fail-closed**）。**旧版二进制打开 v2 库会以 `ErrSchemaTooNew` 拒绝启动**（提示升级 ZeoKeel），即**不支持 downgrade**——回退到旧版需用 `backups/` 快照手工恢复；该目录可手动清理，建议保留最近一份。
- **预览金标**：22 张已按 wave1+wave2 UI 重生成（含设置窗 900×840）并回归 PASS（14 屏/1.9s）。

## 4. 视觉回归策略（v1.1：opt-in 回归工具）

- DSH 像素基线门禁已退役（ADR 0001），**不再与外部截图做像素比对**；下列回归比对的是 ZeoKeel 自有品牌预览，不是 DSH 基线。
- 现有 22 张品牌预览图（`docs/design/preview/`）由 opt-in 测试生成（`ZEOKEEL_UPDATE_PREVIEWS=1`），普通测试/CI 不写工作树。
- **v1.1 新增 opt-in 视觉回归工具**（不恢复 DSH 像素门禁，不引入 flaky CI）：
  - `sh scripts/visual-check.sh`：无头重生成预览 → ImageMagick 对比 `docs/design/preview/` 金标 → 逐屏输出差异像素/%/diff 图（`/tmp/zeokeel-visual/`），超过阈值（默认 2%，`--threshold`/`ZEOKEEL_VISUAL_THRESHOLD` 可配）退出码 1；`--update` 显式刷新金标（人工 review 后提交）；跑完自动还原工作树。约 12s。
  - `ZEOKEEL_VISUAL=1 go test -run TestVisualRegression ./internal/shell`：进程内 opt-in 测试，覆盖 Home/Sidebar/Settings/Plugins/Automation/Trajectory/Session 浅深两态；默认 skip（普通 `go test` 与 CI 不受影响）；时钟/日期文本按渲染位置掩码；阈值/像素容差/产物目录可配。约 2s。
  - 已知漂移源与排除：字体栈/CJK fallback 随机器不同（金标与生成机器的字体环境绑定）；`session/automation/trajectory` 预览含墙钟/下次运行时间，脚本默认跳过这三组（`--all` 可看），进程内测试则用文本矩形掩码。因此**不做 CI 硬门禁**；换机器/换字体/大改视觉后先 `--update` 重建金标并人工 review。
  - 工具卡预览含异步图片加载，单次约 0.006% 抖动（阈值内）；空态/错误态/加载态/窄窗截图仍未全部纳入预览集，由相应行为测试覆盖。

## 5. 功能对齐口径

- 与 DSH 的"功能对齐"是**行为与能力对齐**，不是逐像素/逐交互复刻；DSH 版本升级后不做视觉同步，功能差异以 `docs/FUNCTIONAL-PARITY.md` 为准。
- **平台与 shell 工具**（[ADR 0009](../../docs/decisions/0009-platform-shell-stacks.md)）：shell 栈按平台门控（Windows 仅 `pwsh`、POSIX 仅 `bash`，对齐 DSH「每台机器一套 shell 栈」）；POSIX 构建不再暴露 `pwsh` 工具、Windows 构建不再暴露 `bash`。未集成 Git Bash/WSL（如需要可后续用自定义 shell 栈/profile 扩展）。Windows 终端为管道回退（PTY 仅 Unix），默认 shell 优先探测 `pwsh`、缺失回退 `%COMSPEC%`/cmd.exe；`terminal_signal` 描述仍沿用 POSIX 措辞（低风险）。Windows 子进程控制台已隐藏（[ADR 0011](../../docs/decisions/0011-windows-hidden-console.md)）。
- **审批范围**（[ADR 0010](../../docs/decisions/0010-approval-scope.md)）：Workspace Write 默认只审批能逃逸工作区沙箱/有工作区外副作用的工具（shell/terminal/job/worktree/web/MCP）；workspace 内的文件写由沙箱边界保证、不再逐次审批；`str_replace_editor view` 等只读调用永不审批；Auto review EXP 保留更宽的模型复核范围。需要逐次文件审批可把 `approval.tools` 设回旧列表或 `*`。
- **workspace MCP 配置默认不执行（RT-01 安全修复）**：仓库里的 `<workspace root>/mcp.json` 是**不可信数据**，默认忽略——克隆一个仓库不会执行它声明的 MCP server 进程。需要信任该文件时显式设 `mcp.workspace_config=allow`（仅精确值 `allow`，大小写不敏感；`on/true/1` 等一律 fail-closed）或环境变量 `ZEOKEEL_MCP_WORKSPACE_CONFIG=allow`；`mcp.servers` 设置非空时优先于 workspace 文件。被忽略的 workspace 文件会在日志里记录原因（`mcp.workspace_config` 每次构建 agent 时读取，改设置后下次重建生效）。源码与测试：`internal/app/services.go`（`workspaceMCPConfigAllowed`/`connectMCP`）。
- **模型超时与重试（RT-06/RT-08 已修复，对齐 DSH）**：默认不设整体请求超时；流式读取的空闲上限 `streamIdleTimeoutMs` 默认 **300s**（与 DSH 相同），可用 `provider.stream_idle_timeout_seconds` / `ZEOKEEL_PROVIDER_STREAM_IDLE_TIMEOUT_SECONDS` 调整；等待响应头 60s。可选整体上限 `provider.timeout_seconds`（默认 0=不限）。
  瞬时失败按 DSH normal mode 重试：最多 **5** 次，指数退避 **500ms–10s**、**10%** 抖动；可重试 408/425/429/5xx、传输错误、空闲超时、空响应（EMPTY_RESPONSE）与无可见输出的意外断开，并遵守 `Retry-After`。可用 `provider.retry_max_retries`（0=关闭）、`provider.retry_initial_ms`、`provider.retry_max_ms` 调整。**已输出可见内容后的失败不重试**（避免重复输出；DSH 在 agent step 边界重跑并丢弃失败分片，ZeoKeel 的流式 UI 无法撤回已显示文本），因此流中途失败仍需重发。
- **运行中统计/中断状态**（[ADR 0014](../../docs/decisions/0014-usage-lifecycle-contract.md)）：Usage 为会话累计并实时下发；Stop/失败/崩溃/重开有终态 Reason 与工具收尾。逐轮终态标记已持久化（`turn_end` 事件 + `Snapshot.TurnMarks`，Chat 按 user 消息锚点在每个历史回合末尾显示「已停止/已中断/本轮失败」，重开回放，旧会话回退为只显示最后回合）。软预算 advisory（默认 25/50/100 steps、200K/500K tokens，只提醒不硬停）可由 `plugin.agent_loop.budget_warn_steps` / `budget_warn_tokens` 配置（空=默认、0/off=关闭）。大工具结果（>32KB，含 CJK）在 session 持久化时落盘到 workspace `.zeokeel/tool-results/<call>.txt`，持久化/下一轮 history 只发摘要+路径+前 2KB，保留最近 100 份（写失败回退原文；默认 workspace 无 root 时同样回退）。仍存在：取消瞬间 trailing usage chunk 的修复只覆盖已收到 usage 样本的调用（无样本不计数）；footer 全 0 隐藏时保留 24 DIP 行高；spill 只影响会话持久化/下一轮，不改变当前轮模型已收到的完整结果。
- 构建产物统一 `dist/`（[ADR 0012](../../docs/decisions/0012-build-output-layout.md)）：仓库根/包目录不保留二进制；`make build` / `make package-windows` / `make package-linux` / `make package-macos` 固定命名，`make clean` 只清构建产物。
- 版本与诊断（AR-01/AR-03）：`scripts/build.sh` 用 `-ldflags "-s -w -X main.version/commit/buildTime"` 注入版本并剥离符号（约 -32% 体积），`zeokeel --version` 输出三项；日志写 `<DataDir>/logs/app-YYYYMMDD.log`（8MiB 轮转、跨天换文件），panic 写 `logs/crash-*.txt`，并用 `debug.SetCrashOutput` 捕获致命崩溃。
- 纯 Go/no-cgo 边界：插件只内置、无第三方插件市场/运行时；无内核级沙箱（策略 + 审批 + 审计）。这些是产品级既定边界，不随 UI 改造改变。
- 打包：Windows zip、Linux tar.gz（amd64/arm64）、macOS unsigned `.app` zip 由 `scripts/build.sh` 生成；发布流水线 `.github/workflows/release.yml` + `make release` 已就绪；**代码签名/公证与托管 update feed 仍缺**（需证书与发布账号，见 BUGS AR-03）；自动更新引擎+CLI+设置页检查已交付；构建与打包脚本不写仓库根目录。
- **2026-11-06 统一收口轮**：功能缺口（workspace inline diff、`/goal`、`/compact` 持久压缩、`@` 细节、会话引用、崩溃中断收尾、ACP 完整面、LSP、Wayland portal、导入器收尾、Chat 思考展示）已补齐，契约/差异/未验项见 [FIX-REPORT-CLOSURE](../audit/FIX-REPORT-CLOSURE.md)；发布仍受真机矩阵、签名/公证与托管 feed（外部条件）约束。

## 6. 后续建议优先级

1. 真机 IME/屏幕阅读器/DPI/通知矩阵（发布前必须；§1）。
2. 通知动作按钮/退出后点击（后台审批通知已完成）。
3. 视觉回归：opt-in 工具已提供（`scripts/visual-check.sh` + `TestVisualRegression`，§4）；CI 强制化需先解决跨机器字体/DPI/时钟矩阵，暂列后续。
4. TextScale 全量覆盖（固定 type scale 收敛到 Theme）。
5. 工具输出虚拟化（Trajectory 已完成；若真实超长输出需要）。
