# ZeoKeel Agent

**ZeoKeel：纯 Go 桌面 AI Agent。mygo 原生 UI + eino 运行时。功能对齐 DSH（DeepSeek Harness），视觉与交互自有（mygo 原生设计 + ZeoKeel 品牌）。**

> 品牌资产只使用 `logo/` 下的 SVG，不自行猜测或重绘。插件只做内置（无第三方安装/市场）。
>
> **2026-11-05 决策**：DSH 像素级对齐已退役；功能对齐继续作为目标。见 [ADR 0001：退役 DSH 像素对齐基建](docs/decisions/0001-retire-dsh-pixel-parity.md)。

## 硬约束

| # | 约束 |
| --- | --- |
| C1 | 纯 Go，`CGO_ENABLED=0`，不引入任何 cgo 依赖 |
| C2 | UI 只用 mygo 原生控件（`ui` 包），不用 webview / Electron / HTML |
| C3 | 单进程、单二进制、可交叉编译到 Linux / macOS / Windows |
| C4 | 测试无显示器可跑；功能与交互验收进 CI（DSH 像素/基线门禁已于 2026-11-05 退役） |

## 文档

- **总体规划（主文档）**：[docs/PLAN.md](docs/PLAN.md)
  - 对齐轴与门禁（§1；功能/交互轴有效，像素轴已退役）
  - DSH 设计令牌与布局测量（§2；历史参考，已退役）
  - 架构与 ADR（§4）
  - UI 规格 + **基础样式函数层**（§5；自有视觉设计系统）
  - 交互规格（§6）、功能对齐矩阵（§7）
  - eino 运行时设计（§8）、数据模型（§9）
  - 质量体系（§10）、里程碑与 M0 任务（§11）、风险（§12）、待拍板（§13）
- **决策记录**：[docs/decisions/](docs/decisions/)（0001 退役 DSH 像素对齐；0002 品牌主题基础）
- **功能对齐矩阵**：[docs/FUNCTIONAL-PARITY.md](docs/FUNCTIONAL-PARITY.md)（活文档；像素对齐已退役）
- **历史文档（已退役，不再维护）**：[docs/UI-PARITY-AUDIT.md](docs/UI-PARITY-AUDIT.md)、[docs/COMPONENT-PARITY.md](docs/COMPONENT-PARITY.md)、[docs/research/](docs/research/)
- **字标规格**：[docs/wordmark.md](docs/wordmark.md)

## 当前状态

**M0–M13 完成：五屏 UI · 动画/交互 · 运行时 · 提示词层 · Agent Loop 工具全量 · 多模态图片输入 · 终端 PTY · 真实模型 E2E · 附件上传 · fork/导出**：Home / Session / Plugins / Settings / Automation 全部用 mygo 原生控件实现。`CGO_ENABLED=0 go build/test/vet` 与 `scripts/check-nocgo.sh`（四目标交叉编译）为现行门禁；DSH 像素比对已退役（2026-11-05），历史基线资产已整包归档（`/data/zeokeel-backups/parity-assets-20261105.tar.gz`，回滚点 tag `pre-native-ui`）。

基础设施：`internal/state`（事件总线/快照）、`internal/store`（SQLite v1 + JSONL）、`internal/runtime`（eino ADK：工具回路/多轮历史/审批）、`internal/provider/openai`（SSE 流式 + tool calls）、`internal/session`（会话控制器）、`internal/app`（单实例/托盘/关窗隐藏 + services 接线）。

**2026-11-05 原生视觉迁移（已覆盖全部屏/区）**：

- 阶段 1（完成）：像素门禁与静态演示屏拆除，测试全部迁移 live fixture；品牌主题 `internal/brand`（logo 色板 + mygo `ui.Theme`）生效。见 [ADR 0001](docs/decisions/0001-retire-dsh-pixel-parity.md)、[ADR 0002](docs/decisions/0002-brand-theme-foundation.md)。
- 阶段 2/3（完成）：**Home、Session、Sidebar、Settings、Plugins、Automation 六屏改用 ZeoKeel 品牌原生视觉**（mygo 原生控件 + Theme 令牌，去 DSH 文案/像素魔法值）；系统菜单栏与窗口状态持久化落地（[ADR 0003](docs/decisions/0003-desktop-shell-integration.md)）。
- 阶段 4（完成）：Trajectory / 内联搜索 / todo·team·plan 面板 / 右侧栏（Files·Terminal·Start）品牌化；**Markdown 迁移到 mygo `ui/markdown`**（代码高亮/表格/任务列表/流式/文本选中复制，净减 452 行）；**系统通知**（完成/失败/待审批，[ADR 0004](docs/decisions/0004-desktop-notifications.md)）；系统偏好 TextScale/HighContrast/ReduceMotion 与 2x DPI 冒烟。
- 阶段 6（完成）：按独立设计评审修 P0——**浅色三级文字对比度** `#7E8796→#646C7A`（BgStrong 4.76:1，全表面达 WCAG AA）；**深色字标**改用 `logo-dark.svg` 派生 2x 位图（去除白边）；**破坏性操作确认**（Automation 删除 / Memory Clear all 弹层 + Danger，与其它删除一致，含计划文案中文本地化）；22 张预览重生成。未验证项见 [已知限制](docs/design/known-limitations.md)。
- 阶段 5（完成）：**工具卡结果原生化**（代码块 Copy/GFM markdown/图片加载/错误面板/40 行折叠）；**设置独立原生窗口**（Cmd+, 单例非模态，[ADR 0005](docs/decisions/0005-settings-window.md)）；**后台会话完成/失败通知**（controller hook + 去重/节流）；Sidebar a11y 树层级与键盘回归；死代码清理（-203 行）与 UI 渲染性能基准（稳态帧 0.48–1.26ms，[perf.md](docs/design/perf.md)）。
- 设计与逐屏记录：[原生视觉方向](docs/design/native-ui.md)；可复现截图：[docs/design/preview/](docs/design/preview/)（22 张浅/深色）；未验证项/框架限制：[已知限制](docs/design/known-limitations.md)。

![Session 原生视觉（浅色）](docs/design/preview/session-native-light.png)

- **v1.1 收尾（完成）**：后台"等待审批"通知、TextScale 覆盖 design 类型系统（21 处关键屏转换）、Trajectory 虚拟化（1000 事件稳态帧 22.7→3.12ms）、opt-in 视觉回归工具（`scripts/visual-check.sh` + `TestVisualRegression`）、验收 P3 清理。
- **验收（2026-11-05）**：独立验收复跑 `objective ①PASS / ②PASS / ③PASS`（[07](docs/research/07-final-acceptance.md)、[08 最终复核](docs/research/08-final-recheck.md)）；`gofmt`/`vet`/`build`/`go test ./...`（17 包）/`ci.sh --fast` 全绿。
- 下一阶段（v1.1）：发布前真机验证（IME/a11y/DPI/通知矩阵，见 [已知限制](docs/design/known-limitations.md)）、CI headless 视觉黄金冒烟、TextScale 全覆盖、Trajectory 虚拟化；观感迭代参考 [review-round4.md](docs/design/review-round4.md)。

## 字标（已定）

![A](assets/wordmark/preview-a.png)

图形标 + “ZeoKeel”文字；**系统字体栈**，17px/600，间隔 8px，不打包字体。规格见 [docs/wordmark.md](docs/wordmark.md)。logo 只用 `logo/` 资产，不重绘。
