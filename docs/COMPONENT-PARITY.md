# 组件级一一对应审计（DSH vs ZeoKeel）

## 方法（全部实测，非猜测）

- DSH 侧：`baseline/css/*.json`（每屏元素 tag/class/rect/计算样式）、`baseline/controls/*.json`（可交互控件）、`baseline/screenshots/*.png`；源码对照 `dsh-client-ui-*` bundle（Rows.module.css 类名、client.js 结构）。
- ZeoKeel 侧：`internal/design/components.go` + `internal/shell/*.go` 的 Go 组件；无头实拍 `artifacts/live/home.png`、`session.png`（`TestCaptureLiveSession`，真实 Controller）；像素基线 `TestScreensBaseline`。
- 元素对应原则：DSH CSS module 类 → 我们的 Go 函数/样式；几何（x/y/w/h/padding/gap/radius/font/line-height）逐项比对。

## 侧栏（本轮已按 DSH 精确几何修）

| DSH 组件类 | DSH 几何/样式 | ZeoKeel 组件 | 状态 |
|---|---|---|---|
| `hHd-Xa_sidebarCol` | 280×1000 | `design.Sidebar()` + `App.sidebar` | ✅ |
| `hHd-Xa_logoRow` | x12 y6 256×60 | brand Row `Height(60) Margin(6,12)` | ✅ |
| `hHd-Xa_brand` | 216×24 fs13.3 | mark+wordmark+折叠按钮 | ⚠️ 字标为文字（DSH 为 SVG 字标） |
| `hHd-Xa_newSession` | x14 y70 252×38 r12 border1 fs14 lh22 | `design.PrimaryButton` + icon/label | ✅（几何对齐） |
| `hHd-Xa_panelList` | y120 gap4；`panelRow` 252×36 r12 fs14 lh22 | `App.navRow` ×2 | ✅ |
| `bhn1Oq_sectionHeader` | y206 260×36；label x16 fs14 lh20 tertiary | Workspaces header Row | ✅（高度/字号已改 14/36） |
| `YDXeBa_projectRow` | 256×34 r12 gap6 padL8 fs14 | `workspaceNode` row | ✅（选中/悬停 r12） |
| `YDXeBa_sessionRow` | 256×32 fs14 lh20；time fs10 lh16 | `workspaceSessionRow` | ⚠️ 当前行常显操作；DSH 仅 hover |
| `bhn1Oq_addWorkspace` | 行高约 34，+ 文本 tertiary | `addWorkspaceRow` | ✅（34/14/tertiary） |
| `hHd-Xa_footArea` / `yHnPSG_trigger` | footer 256×40；按钮 260×32 pad6 r12 | `moreFooter` | ✅（改为小胶囊 hover） |

## 主区组件映射

| DSH | ZeoKeel | 状态 |
|---|---|---|
| `pXSMma_heroWorkspaceRow`（工作区/模式 chip 28 高，gap2） | `homeWorkspaceRow` + `homeChip` | ✅ / 悬停态待补 |
| `uV2eYG_composer`（868 宽、r20、双层 shadow、底部工具栏） | `homeComposer`/`liveComposerArea` + `design.Composer` | ⚠️ 输入框高度/内边距与阴影值需逐像素核对 |
| 权限/模型/模式 chip（trigger 28 高，fs13/500） | `homePermissionChip`/`modelBadge`/mode popover | ⚠️ 字号/间距接近；DSH 有 chevron 旋转与打开态 |
| `iWlSmW_trigger`（权限菜单按钮） | 权限 popover 菜单项 | ⚠️ 菜单项行高/快捷键提示未对齐 |
| 消息文本 `_text_1rdzk_17`（正文 fs14，行高继承；markdown 标题/列表/代码） | `liveMessage` 纯 `ui.Text` | ❌ 缺 markdown 渲染/文件引用卡片 |
| 工具行 `_row_k7ery_26` / `ztWv_callRow` / `lcKema_*` / `HWmORq_*` / `CY-8Ka_*` / `o3BgMG_*`（多种工具卡变体：命令/读图/文件/提问/搜索） | `toolCard` 单一卡片 + 摘要行 | ❌ 仅一种形态，DSH 至少 6 种内容组件 |
| `xzv4MW_actions/timeStart`（消息操作条、时间、重试/复制/反馈） | `messageActions`（本轮已加 Copy/Retry/Feedback） | ⚠️ 布局/图标/时间格式未对齐 |
| `EvIC1a_flowItem`（消息流容器，845 宽） | 消息 Column（无固定宽度） | ⚠️ 宽度/间距 |
| `_root_1rdzk_1`（markdown span 基线） | `ui.Text` 基线 | ⚠️ 字体/行高不同（见怪异原因） |
| Composer 圆形发送按钮（32，accent，↑） | `liveRunButton` / home send | ✅ |
| 右侧栏（Files/Terminal 头部控件） | `rightSidebar` + chrome 控件 | ⚠️ Split 分隔条/Fullscreen 过渡动画待对齐 |

## 仍然“怪异”的根因（按影响排序）

1. **字体栈**：DSH 用 `-apple-system, "Segoe UI", "PingFang SC", "Microsoft YaHei"…`，字标/正文有专门 letter-spacing/size；我们大多数文本未显式设置 Font（用 mygo 默认），字号对但字面宽度/行高观感不同。
2. **品牌字标**：DSH 是 SVG 字标（deepseek harness）；我们用文字 “ZeoKeel” + mark，形状天然不同（品牌允许，但视觉差异明显）。
3. **图标库**：我们用 Lucide 近似图标，DSH 是自有 16px 图标集（folder、chevron、sliders、plus 形状不同）。
4. **工具卡/消息**：DSH 为每种工具/内容做专门组件（命令、文件、读图、提问、搜索、编辑），我们统一成一种卡片 + 摘要行。
5. **交互态**：DSH 行操作仅 hover 显示（workspace +/⋯、session ⋯），我们选中行常显；chip 打开态、hover 背景、focus ring 样式未逐项对齐。
6. **时间格式**：DSH session 行 "3min/31min"（10px），我们现在部分行空白或 "now"。
7. **子像素/抗锯齿**：原生 mygo 灰度 AA vs Chromium LCD；像素 diff 地板无法消除。

## 结论

结构与主要控件已能一一对应（侧栏 12 个组件、首页 10 个、会话 20+）；**未能一一对应的是内容渲染层组件**（markdown、6 种工具卡、消息操作条布局）与视觉细节（字体、图标、hover 态）。下一步按此表逐项替换/补组件，而不是继续调间距。
