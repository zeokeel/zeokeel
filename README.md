# ZeoKeel Agent

**ZeoKeel：纯 Go 桌面 AI Agent。mygo 原生 UI + eino 运行时，与 DSH（0.2.1-rc.2）做像素 / 交互 / 功能三重 1:1 对齐。**

> 品牌资产只使用 `logo/` 下的 SVG，不自行猜测或重绘。插件只做内置（无第三方安装/市场）。

## 硬约束

| # | 约束 |
| --- | --- |
| C1 | 纯 Go，`CGO_ENABLED=0`，不引入任何 cgo 依赖 |
| C2 | UI 只用 mygo 原生控件（`ui` 包），不用 webview / Electron / HTML |
| C3 | 单进程、单二进制、可交叉编译到 Linux / macOS / Windows |
| C4 | 测试无显示器可跑；像素与交互验收进 CI |

## 文档

- **总体规划（主文档）**：[docs/PLAN.md](docs/PLAN.md)
  - 三条对齐轴的工程定义与门禁（§1）
  - DSH 基线令牌与布局测量（§2）
  - 架构与 ADR（§4）
  - UI 规格 + **基础样式函数层**（§5）
  - 交互规格（§6）、功能对齐矩阵（§7）
  - eino 运行时设计（§8）、数据模型（§9）
  - 质量体系（§10）、里程碑与 M0 任务（§11）、风险（§12）、待拍板（§13）
- **基线截图**：[baseline/screenshots/](baseline/screenshots/)（1600×1000，逐屏）
- **基线控件清单**：[baseline/controls/](baseline/controls/)（每屏交互元素 dump）
- **基线文档**：[docs/baseline/index.md](docs/baseline/index.md)（屏幕地图 / 控件总表 / 覆盖矩阵）
- **全元素 CSS**：[docs/baseline/css.md](docs/baseline/css.md)（17 屏 computed style 汇总 + 组件样式样本）

## 当前状态

**M0–M10 完成：五屏 UI · 动画/交互 · 运行时 · 提示词层 · Agent Loop 工具全量 · 多模态图片输入 · 终端 PTY · 真实模型 E2E**：Home / Session / Plugins / Settings / Automation 全部用 mygo 原生控件实现，逐屏与 DSH 基线像素比对迭代（masked diff 0.93% / 6.40% / 3.17% / 2.31% / 1.34%）。`CGO_ENABLED=0 go build/test/vet` 全绿、`scripts/check-nocgo.sh` PASS（四目标交叉编译）。

![M1 screens](artifacts/m1-screens.png)

各屏渲染：`artifacts/screens/`，差异图（红=不同）：`artifacts/diff/`。

基础设施：`internal/state`（事件总线/快照）、`internal/store`（SQLite v1 + JSONL）、`internal/runtime`（eino ADK：工具回路/多轮历史/审批）、`internal/provider/openai`（SSE 流式 + tool calls）、`internal/session`（会话控制器）、`internal/app`（单实例/托盘/关窗隐藏 + services 接线）。

**基线文档 100% 收口，可进入 M0**：80 张屏/状态截图、53 份全元素 computed style、108 条逐控件行为 + 216 张前后对比截图；样式路线已定：原生 mygo + Go 样式函数，CSS 仅作对齐依据。尚未开始编码；`docs/PLAN.md` 是计划真源，`docs/baseline/` 是施工图。

## 字标（已定）

![A](assets/wordmark/preview-a.png)

图形标 + “ZeoKeel”文字；**系统字体栈**，17px/600，间隔 8px，不打包字体。规格见 [docs/wordmark.md](docs/wordmark.md)。logo 只用 `logo/` 资产，不重绘。
