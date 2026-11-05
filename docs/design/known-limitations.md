# 已知限制与未验证项（v1 收口用）

> 更新时间：2026-11-05 · 对应提交：见 git log（原生视觉迁移完成后）
> 目的：把"没有做到/没有条件验证"的事说清楚，避免把目标达成的部分与已知缺口混在一起。这些项不阻塞"UI 品牌原生化 + 桌面交互本地化"的收口，但发布前需要真机验证或后续排期。

## 1. 真机能力未验证（本环境无显示器）

| 项 | 现状 | 需要的验证 |
| --- | --- | --- |
| 中文/日文/韩文 IME | composer 用 mygo 原生 TextArea，理论上支持组合输入；未真机验证候选窗位置/组合期重排 | macOS/Windows/Linux 各一次 IME 冒烟（中文输入、删除、换行、候选窗跟随） |
| 屏幕阅读器 | a11y 树结构/名称/层级已用 headless 反射测试覆盖（Sidebar RoleTree/TreeItem、无名节点审计、键盘回归） | VoiceOver（macOS）、Narrator（Windows）、Orca（Linux）人工复核朗读顺序与操作 |
| 高 DPI/多显示器 | `ui.Render(...,scale=2)` 冒烟（3200×2000 非空、关键控件存在）；未真机 1.25x/1.5x/2x 切换 | 各平台 DPI 切换与跨屏拖动，确认无 1px 漂移/模糊 |
| 系统通知服务 | 通知状态机与 Notifier 注入测试覆盖；真实 mygo.NewNotification 只在源码级接入 | macOS 打包后通知权限、Windows balloon、Linux notification service 实机 |
| 窗口管理 | 窗口状态持久化/设置独立窗口/菜单栏均有单测与 headless 行为测试 | 真机窗口恢复、双窗口焦点、macOS 菜单栏/Windows 窗口菜单 |

## 2. 框架/平台限制（已记录策略，未强改）

- **滚动条常显**：mygo v0.7.4 只有 hover overlay（6 DIP），无"始终显示"全局偏好；当前策略为保留 overlay + HighContrast 下加粗加深（`docs/design/native-ui.md` §12.3）。Windows 常显滚动条需框架能力或自绘，列为后续。
- **TextScale 覆盖**：v1.1 起系统文字缩放覆盖基座主题字号、会话正文/行高，以及 `design.Tokens` 品牌 type scale（`Tokens.Font/Text/TextMuted` 的 size/line 同比例缩放，`t.Font(design.TypeX)` 自动生效）；Home/Session/Settings 另有 21 处显式字号/`settingsText` 调用改为 TypeRole helper（chip 高度等同比放大），1.5x 关键屏已测不裁切。剩余：全仓仍有 169 处显式 `.FontSize(...)`（转换前 177）与 `settingsText/nativeText` 等 helper 内的固定档位不随 TextScale 缩放——多数是图标/像素微调或自定义行高，其余屏幕（Sidebar/右栏/面板等）的批量转换留待 v1.2（详见 native-ui.md §12.5）。
- **HighContrast**：在品牌主题上做对比度增强（边框/次要文字向主文字混合、Focus 不透明、滚动条加深），不是专门的高对比主题，需真机/无障碍用户评估。
- **动画**：ReduceMotion 通过 `c.DisableAnimations()` 全局关闭；mygo 框架内建 transition 遵循，个别自绘动画（流式淡入）显式检查。

## 3. 桌面交互遗留

- **通知**：当前/后台会话的 turn 完成/失败与**审批/提问等待**均已通知（controller hook）；通知 ID 替换、动作按钮、"退出应用后点击通知"未做；跨会话总量节流为简化策略（同会话完成 5s 冷却，审批不节流）。
- **设置窗口**：Windows/Linux 设置窗口无菜单栏；窗口间几何联动、大页滚动位置持久化未做；overlay 作为无显示器/创建失败的回退保留（行为略有差异）。
- **文本选择**：消息与工具卡中的 Markdown 内容（正文/代码）可选中并右键复制（mygo markdown）；其余 UI 文本未全量开启选择（侧栏、按钮、标题、设置说明等），桌面应用一般也不需要。
- **Trajectory**：已虚拟化（ui.List，1000 事件稳态帧 22.7ms→3.12ms，见 perf.md §8）；`FollowEnd=false` 为当前选择，若产品要"日志式跟随末尾"改一处即可。
- **工具输出**：超大工具结果 40 行封顶 + 展开，未虚拟化（当前规模可接受）；工具结果代码块语言映射为简化集合（bash/text 等）。

## 3.5 已修复的评审判定项（保留记录）

- 浅色三级文字对比度（`#7E8796→#646C7A`，AA）、深色字标白边（`mark-dark@2x.png`）、破坏性操作确认、toolcard 错误面板 danger-on-tint（`DangerText #B91C1C`，5.7:1）、设置窗口重复关闭入口：均已修复并有测试/预览证据（见 STATUS 阶段 6 与 review-round3/4 附录）。

## 3.6 v1.1 已处理项（2026-11-05）

- 后台"等待审批"通知（controller `ApprovalRequestedHook`，所有会话）。
- Trajectory 虚拟化（ui.List；1000 事件稳态帧 7.3×）。
- TextScale 覆盖 design 类型系统（21 处关键屏显式字号转换；剩余约 169 处留 v1.2）。
- opt-in 视觉回归工具（`scripts/visual-check.sh` + `TestVisualRegression`，CI 未强制）。
- P3 清理：plugins 采样色→token、settings-window 预览唯一关闭入口、known-limitations/preview README 措辞与尺寸。

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
- 纯 Go/no-cgo 边界：插件只内置、无第三方插件市场/运行时；无内核级沙箱（策略 + 审批 + 审计）；Windows 终端为管道回退（PTY 仅 Unix）。这些是产品级既定边界，不随 UI 改造改变。
- 打包/签名/自动更新未在本轮重新验证（`dist/` 与交叉编译脚本另见 CI）。

## 6. 后续建议优先级

1. 真机 IME/屏幕阅读器/DPI/通知矩阵（发布前必须）。
2. 通知动作按钮/退出后点击（后台审批通知已完成）。
3. 视觉回归：opt-in 工具已提供（`scripts/visual-check.sh` + `TestVisualRegression`，§4）；CI 强制化需先解决跨机器字体/DPI/时钟矩阵，暂列后续。
4. TextScale 全量覆盖（固定 type scale 收敛到 Theme）。
5. 工具输出虚拟化（Trajectory 已完成；若真实超长输出需要）。
