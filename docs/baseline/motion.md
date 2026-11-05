# 动效基线（Motion）

> 来源：DSH 0.2.1-rc.2 前端构建
> `/usr/local/lib/node_modules/@deepseek-ai/dsh/node_modules/@deepseek-ai/dsh-web-frontend/dist/assets/index-BxpfOGf3.css`，
> 以及 `baseline/css/*.json` 里逐元素抓取的 computed
> `transition-duration` / `transition-timing-function`。
> MyGo 侧 API：`ui.Animation`、`ui.CubicBezier`、`ui.ElementTransition`、
> `ui.Motion`、`ui.Loop`、`c.After`（mygo v0.7.4 `docs/ui/motion.md`、
> `transitions.md`、`spinner.md`）。
>
> 实现：`internal/design/motion.go`（令牌与 preset）、
> `internal/shell/motion.go`（live 动效状态：计时、流式淡入、三点）、
> `internal/shell/session_live.go`（live 会话接线）。
> 静态 shell（`App.Ctrl == nil`）不经过这些代码，像素基线不变。

## 1. 时长令牌

| 令牌 | 值 | 来源 / 证据 | 用途 |
| --- | --- | --- | --- |
| `DurationTick` | 100ms | computed `0.1s`：`._row_k7ery_26`（工具/活动行，`transition: color .1s ease`）、`._iconIdle_k7ery_78`（`opacity .1s ease`）、`O_Ebla_title`、`l_V-RG_root`、`uV2eYG_primary`（composer 主按钮） | 工具卡行悬停、状态点换色、phase 标签 |
| `DurationFast` | 120ms | computed `0.12s`：`nyYjTG_file`（文件卡 `.12s ease`）、`_tab_d62eq_37`（`color .12s`）、`_thumb_1ik0f_33`（`transform .12s`）、`bhn1Oq_searchInput` | 流式文字回落、caret/content 切换 |
| `DurationEnter` | 150ms | `_indicator_1gwo3_1{animation:_indicator-enter_1gwo3_1 .15s ease-out}`、`_tooltip-in_12mhf_1 .15s` | 工作指示器入场、Send/Stop 标签切换 |
| `DurationBase` | 160ms | `_indicator_1gwo3_1{transition:… .16s ease-out}`、`_dsh-toast-in_l6cvn_1 .16s ease-out`、`_indicator_d62eq_22{transform .16s ease}` | 指示器颜色/透明度、toast（预留） |
| `DurationSlow` | 200ms | `--ds-transition-duration`；`_modalEnter_o6lrb_1 var(--ds-transition-duration) var(--ds-ease-in-out)`；computed `0.2s cubic-bezier(0.4, 0, 0.2, 1)` 于 `._markdown_1ypvv_5 a, ._fileLink_1ypvv_59` | 消息出现、工具卡展开/收起（布局） |
| `DurationTab` | 300ms | computed `0.3s cubic-bezier(0.4, 0, 0.2, 1)`：`_tabCell_6nhg2_142`、`_emptyTabHost_6nhg2_143` | 右侧栏 tab（本改动未接线，令牌保留） |
| `DurationSpin` | 800ms | `_spinner_u7vgf_45{animation:_spin_u7vgf_45 .8s linear infinite}` | 环形 spinner 一圈 |
| `DurationPulse` | 1500ms | `_dsh-state-dot-spin_1i3xo_1 1.5s linear`、`_dsh-state-dot-dash_1i3xo_1 1.5s ease-in-out`、`_reveal-second-dot/_third-dot 1.5s step-end` | 状态点脉冲、三点显隐周期 |
| `DurationShimmer` | 1500ms | `._sweep_1rdzk_34,._highlight_1rdzk_54{animation-duration:1.5s;animation-delay:.3s;animation-timing-function:steps(48,end);animation-iteration-count:infinite}` | 会话行 shimmer |
| `ShimmerDelay` | 300ms | 同上 `animation-delay:.3s` | shimmer 起始延迟 |
| `ShimmerSteps` | 48 | 同上 `steps(48,end)` | shimmer 步进数 |

> DSH 构建里没有 1.6s：shimmer 与状态点的周期实测为 **1.5s**
> （样式表里 `grep '1\.[0-9]s'` 只出现 1.5s）。这里按实测值编码，
> 避免“文档与源码各说一套”。

## 2. 缓动令牌

| 令牌 | 值 | 来源 / 证据 |
| --- | --- | --- |
| `EaseStandard` | `cubic-bezier(0.4, 0, 0.2, 1)` | DSH 的 `--ds-ease-in-out`；CSS 用它做 `_modalEnter` 入场与 file link box-shadow，file link 的 computed `transition-timing-function` 正是该曲线，基线内同值共 328 处 |
| `EaseCSS` | `cubic-bezier(0.25, 0.1, 0.25, 1)` | CSS 关键字 `ease`；computed `0.1s ease` 的活动行、composer 主按钮 |
| `EaseOut` | `cubic-bezier(0, 0, 0.58, 1)` | CSS `ease-out`；`_indicator-enter`、`_dsh-toast-in`、`_dockHintIn`、`_dockScrimIn` |
| `EaseInOut` | `cubic-bezier(0.42, 0, 0.58, 1)` | CSS `ease-in-out`；`_dsh-state-dot-dash` |
| `Pulse` | `ui.Bounce(EaseInOut)` | MyGo 的往返缓动，配 `DurationPulse` 做状态点呼吸 |

## 3. DSH 动效 → 实现

| DSH 动效 | 时长 / 缓动 | ZeoKeel 实现 | 证据（选择器 / computed 值） |
| --- | --- | --- | --- |
| 弹层淡入（mask + dialog） | 200ms `cubic-bezier(.4,0,.2,1)` | `design.AnimSlow`；消息与布局过渡共用同一条曲线 | `@keyframes _modalEnter_o6lrb_1{0%{opacity:0}to{opacity:1}}`，`animation:_modalEnter_o6lrb_1 var(--ds-transition-duration) var(--ds-ease-in-out)` |
| 工作指示器入场 | 150ms `ease-out`，仅 opacity 0→1 | `design.IndicatorTransition()`（Enter 淡入 + 2 DIP 上移，这是我们加的极轻位移） | `_indicator_1gwo3_1{animation:_indicator-enter_1gwo3_1 .15s ease-out}` |
| 工作指示器三点逐个出现 | 1.5s `step-end`，第二点在 33.33%、第三点在 66.66% | `design.DotsAt(elapsed)` + 每 500ms `c.After` 唤醒一次视图（不跑 60fps） | `_reveal-second-dot_1gwo3_1{0%,33.32%{opacity:0}33.33%,to{opacity:1}}`、`_reveal-third-dot_1gwo3_1`、`_secondDot/_thirdDot{animation:… 1.5s step-end infinite}` |
| 状态点旋转 + dash | 1.5s `linear` / 1.5s `ease-in-out` | running 工具状态点：`Loop("pulse", DurationPulse, Pulse)` → `PulseOpacity`（透明度呼吸），换色走 `.1s ease` | `_dsh-state-dot-spin_1i3xo_1 1.5s linear infinite`、`_dsh-state-dot-dash_1i3xo_1 1.5s ease-in-out infinite`、`_iconIdle_k7ery_78{transition:opacity .1s ease}` |
| 环形 spinner 旋转 | 800ms `linear infinite` | `ui.Spinner(c).Size(14,14)`（MyGo 原生、只重绘不重建视图，12 辐条 / 0.9s 一圈） | `_spinner_u7vgf_45{width:20px;height:20px;border:2px solid …;animation:_spin_u7vgf_45 .8s linear infinite}` + `:after` 的 72° conic 弧 |
| 会话行 shimmer | 1.5s `steps(48,end)`，延迟 300ms，无限循环 | `design.ShimmerPhase(elapsed)` 编码步进相位（令牌已备，本轮未画扫光） | `_dsh-row-shimmer-sweep_1rdzk_1{0%{transform:translate(-100%)}66.6667%,to{transform:translate(100%)}}`、`animation-duration:1.5s;…steps(48,end)` |
| 工具/活动行悬停与状态 | 100ms `ease` | `design.ColorTransition()`（100ms / `EaseCSS`），作用于工具行、状态点、phase 标签 | computed `0.1s ease` 于 `_row_k7ery_26`、`O_Ebla_title`、`_iconIdle_k7ery_78` |
| 工具卡展开 / 收起 | 200ms `cubic-bezier(.4,0,.2,1)`（映射） | `design.CollapseTransition()`：detail 以 `Motion{Collapse:true}` 长高/收起；卡片 `design.LayoutTransition()` | DSH 工具行本身只有 `.1s ease` 的颜色/透明度过渡（`_row_k7ery_26`、`_chevronHover_k7ery_84`），展开由组件条件渲染、CSS 未单列时长。这里用同页标准 `--ds-transition-duration` / `--ds-ease-in-out`（computed `0.2s cubic-bezier(0.4,0,0.2,1)`）作为映射，属**有意延展** |
| 文件卡 / 内容切换 | 120ms `ease` | `design.AnimFast`：`ContentTransition` 用于 caret、"Deep diving…" 文本、Send/Stop 标签 | computed `0.12s ease` 于 `nyYjTG_file`；`_tab_d62eq_37{transition:color .12s ease}` |
| 右侧栏 tab 滑入 | 300ms `cubic-bezier(.4,0,.2,1)` | `design.DurationTab`（令牌保留；右栏不在本改动范围） | computed `0.3s cubic-bezier(0.4, 0, 0.2, 1)` 于 `_tabCell_6nhg2_142` |
| Toast 入场 / 自动淡出 | 160ms `ease-out`；hold 3s 后 1s `ease` | `design.DurationBase` / `AnimBase`（令牌保留，本轮未接 toast） | `_dsh-toast-in_l6cvn_1 .16s ease-out,_dsh-toast-fade_l6cvn_1 1s ease var(--dsh-toast-hold, 3s)` |
| 流式助手文字出现 | DSH 无整段淡入；本实现 120ms `ease-out` | `design.StreamOpacity`：文字增长时透明度 `0.65→1`，位移 `2→0 DIP`；静止后严格为 1 / 0 | 见下方“有意差异”；目标帧与静态帧逐像素一致（`TestMotionLiveSettlesToRestFrame`） |
| 新消息出现 | 200ms `cubic-bezier(.4,0,.2,1)`（映射） | `design.MessageTransition()`：Enter 淡入 + 上移 6 DIP + 位置过渡 | DSH 的消息块没有 CSS 入场动画；时长/曲线取同页标准 `--ds-transition-duration` / `--ds-ease-in-out`，属**有意延展** |
| Send ↔ Stop 状态切换 | 150ms `ease-out`（内容） + 100ms `ease`（颜色） | `design.liveRunButton`：`ContentTransition` 换字 + `ColorTransition` 换色；running 时 `Stop` 调 `Ctrl.Stop()` | 颜色取 `uV2eYG_primary` 的 computed `0.1s ease`；文字切换取指示器入场同族的 `.15s ease-out`，属映射 |
| 工具提示 | 150ms `var(--ds-ease-in-out)` | `design.AnimEnter`（令牌保留） | `_tooltip-in_12mhf_1 .15s var(--ds-ease-in-out)` |

## 4. 有意差异

1. **Spinner 字形**：DSH 是 20px 圆环 + 72° conic 品牌弧；MyGo 的
   `ui.Spinner` 是 12 辐条。两者都是“未知时长”语义、都只重绘不重建视图。
   我们在 28px 指示器行里用 14px 的 `ui.Spinner`，并保留 DSH 的 800ms
   `DurationSpin` 令牌；不自己画 conic 弧是为了避免逐帧重建视图。
2. **状态点**：DSH 旋转 SVG 圆弧并做 `stroke-dasharray/dashoffset`
   动画；MyGo 的 CGO-free 路径绘制没有 dash 动画原语，因此用同周期
   （1.5s）的透明度呼吸替代，语义（运行中/完成/失败颜色）保持一致。
3. **Shimmer**：DSH 用 `mask-image` 扫过渐变。MyGo 无 mask，令牌与
   `ShimmerPhase` 步进已经编码，但本轮指示器用三点显隐表达进行中；
   shimmer 留给后续行内高亮接入。
4. **流式文字**：真实 DSH 是追加 token（浏览器里没有整段淡入）。为了让
   新 token 不是硬切、同时静止帧与静态渲染完全一致，加了 120ms、
   0.65→1 的轻淡入。结束时透明度为 1.0、位移为 0，和没有动画的渲染
   逐像素一致（测试覆盖）。
5. **1.5s 而非 1.6s**：motion token 的常见近似值是 1.6s，但 DSH 构建里
   shimmer 与状态点的周期实测为 1.5s（样式表里只有 1.5s）；按实测编码，
   三点显隐与状态点同相位。
6. **computed timing function 的分组**：DSH 把 `0.18s` 用在侧栏搜索/
   header actions，`0.14s` 用在 dock 提示；它们属于侧栏/平台 chrome，
   本改动不接线，故未列令牌（值已记录于本节证据列）。
7. **消息入场 / 工具卡展开**：DSH 的 CSS 没有给这两处单独的时长与
   曲线（工具行只有 `.1s ease` 的颜色/透明度过渡）。这里映射同页标准
   `--ds-transition-duration`（200ms）与 `--ds-ease-in-out`
   （`cubic-bezier(0.4,0,0.2,1)`），并在表格的“证据”列标注为映射。

## 5. 测试（虚拟时钟，无 sleep）

`internal/shell/motion_test.go`，全部用 `ui.NewTester` + `tt.Advance`：

| 测试 | 断言 |
| --- | --- |
| `TestMotionTokens` | 10 个时长 + steps=48 与文档逐项相等；四条曲线端点 0/1、单调；`EaseStandard` 不是 `ui.EaseOut` |
| `TestMotionHelpers` | `Progress`、`OpacityAt`、`HeightAt`、`ScaleAt`、`RiseAt`、`StreamOpacity`、`DotsAt`、`PulseOpacity`、`ShimmerPhase` 的起止值 |
| `TestMotionStreamFadeStartsAndSettles` | 首帧静止；增长时从 0.65 开始；120ms 后精确 1；再次增长重新开始 |
| `TestMotionRunningClock` | “Xm Ys” 计时随 PhaseRunning 开始/结束/下一轮重置 |
| `TestMotionWorkingIndicator` | running 时 spinner + “Deep diving for 0m 0s.”；500ms 后第二点；idle 后消失且 `FramePending()==false` |
| `TestMotionLiveSettlesToRestFrame` | 新消息/流式结束后，live 的消息/工具区域与“从未动画”的同状态渲染逐像素相等（composer 光标随时钟闪动，比较时排除） |
| `TestMotionStreamingFadeFrames` | 增长后开始请求帧；`4×DurationFast` 后停止 |
| `TestMotionStreamReduceMotion` | “减少动态”下文字照常增长、不再请求动画帧 |
| `TestMotionStreamIdleHasNoFrames` | 静止会话不再请求帧 |
| `TestMotionToolCardExpandCollapse` | 点击展开开始动画、结束后显示 args/result；再点收起、结束后消失 |
| `TestMotionToolDotPulse` | running 时持续请求帧；完成后停止 |
| `TestMotionRunButtonState` | idle=Send、running=Stop，切换动画后停在新标签 |

门禁命令：

```sh
export PATH=/usr/local/go/bin:$PATH GOCACHE=/data/mygo/.cache/go-build \
  GOMODCACHE=/data/mygo/.cache/gomod GOSUMDB=off CGO_ENABLED=0
go build ./...
go test -count=1 ./internal/shell -run 'TestMotion|TestScreensBaseline' -v
gofmt -l internal/design internal/shell
go vet ./internal/design ./internal/shell
```

静态基线（`TestScreensBaseline`）不经过 `sessionLive`，diff 与改动前一致：
home 1.00%、session 6.48%、plugins 3.20%、settings 2.38%、automation 1.39%
（masked 同值）。
