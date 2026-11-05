# ZeoKeel UI 渲染性能基准（2026-11-05）

> 目的：给"原生视觉 + mygo 即时模式渲染"一个**可复现的性能基线**，回答两个问题：
> ① 每帧重建整个语义树（mygo 的 view 模型）在桌面尺寸下要多久、分配多少内存；
> ② `ui.Render`（无头全量渲染，测试/预览用）与真实窗口的稳态帧成本差多少，避免把测试成本误当用户体验成本。
>
> 基准确认了 EVOLUTION G10 记录的"0 benchmark"缺口；数据来自 `internal/shell/render_bench_test.go`，可重复运行。

## 1. 测量环境

| 项 | 值 |
| --- | --- |
| 日期 | 2026-11-05 |
| CPU | Intel(R) Xeon(R) Gold 5220R CPU @ 2.20GHz（2 socket × 24 core × 2 thread，共 96 逻辑核） |
| OS / 架构 | Linux / amd64（容器，无 GPU、无显示服务器） |
| Go | `go1.27.3 linux/amd64` |
| 构建 | `CGO_ENABLED=0`（纯 Go，无 cgo） |
| 渲染后端 | mygo headless（CPU 软件光栅 + pango/cairo 文本）；测试不启动窗口、不使用 GPU |
| 窗口尺寸 | 1600×1000（设计评审尺寸）与 900×600（最小窗口） |
| 固定输入 | 中文为首发语言；fixture 用真实 store/agent/controller（`newBenchApp`），Session 用固定 Markdown+工具卡快照，Automation 预置 3 个任务，Sidebar 预置 3 工作区 × 4 会话的树 |

> 注意：服务器 CPU 与桌面 CPU 的单核性能不同，绝对数字不能直接搬到用户机器；结论看**相对量级**（哪个屏最重、每帧是否在 16.7ms 预算内）与**趋势**。

## 2. 方法

两层测量，都在 `internal/shell/render_bench_test.go`：

1. **`BenchmarkRender*`（任务要求的主基准）**：每次迭代调用 `ui.Render(app.View, w, h, 1)`。
   `ui.Render` 会新建一个无头 runtime、跑完 settle 帧（最多 20 帧，直到视图不再请求动画帧）、再编码成图像。因此它包含**一次性启动成本 + 若干 settle 帧 + 图像分配**，适合跨屏对比与回归，但**不是**用户每帧实际成本。
2. **`BenchmarkWarmFrame`（补充：稳态帧成本）**：`ui.NewTester` 只建一次，循环调用 `tt.Frame()`。runtime、字体缓存、Markdown 解析缓存与元素状态都已热，每次迭代是一次真实的"重建语义树 + 布局 + 绘制"，对应窗口里输入/流式 tick 时的帧成本。

fixture 全部在计时循环外构建。每个 benchmark 记录 `ns/op`、`B/op`、`allocs/op`。

## 3. 复现命令

```sh
cd /data/mygo
export PATH=/usr/local/go/bin:$PATH
export GOMODCACHE=/data/mygo/.cache/gomod
export CGO_ENABLED=0

# 主基准（冷渲染，任务要求的命令）
go test -run '^$' -bench 'BenchmarkRender' -benchmem ./internal/shell

# 稳态帧成本（真实窗口语义）
go test -run '^$' -bench 'BenchmarkWarmFrame' -benchmem ./internal/shell

# 观察波动：重复 3 次，取中位数（perf 表用的就是 3 次中位数）
go test -run '^$' -bench 'BenchmarkRender|BenchmarkWarmFrame' -benchmem -count=3 ./internal/shell
```

## 4. 结果

### 4.1 `ui.Render`（冷渲染；3 次运行中位数）

| 屏 | 1600×1000 ns/op | 1600×1000 B/op | 1600×1000 allocs/op | 900×600 ns/op | 900×600 B/op | 900×600 allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Home | 21.70 ms | 6.99 MB | 1140 | 11.48 ms | 2.71 MB | 1104 |
| Session | 31.44 ms | 8.24 MB | 2082 | 14.62 ms | 3.84 MB | 2066 |
| Plugins | 27.67 ms | 7.37 MB | 1571 | 12.82 ms | 3.04 MB | 1560 |
| Automation | 26.18 ms | 7.15 MB | 954 | 11.54 ms | 2.91 MB | 951 |
| Settings | 27.13 ms | 7.78 MB | 1670 | 13.81 ms | 3.40 MB | 1637 |
| Sidebar（工作区树） | 24.59 ms | 7.59 MB | 2660 | 14.45 ms | 3.32 MB | 2649 |

### 4.2 稳态帧（`tt.Frame()`，热缓存；3 次运行中位数）

| 屏 | 1600×1000 ns/op | 1600×1000 B/op | 1600×1000 allocs/op | 900×600 ns/op | 900×600 B/op | 900×600 allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Home | 0.649 ms | 21.4 KB | 403 | 0.603 ms | 21.4 KB | 403 |
| Session | 1.085 ms | 33.4 KB | 642 | 0.997 ms | 33.4 KB | 644 |
| Plugins | 0.796 ms | 27.6 KB | 581 | 0.814 ms | 27.6 KB | 581 |
| Automation | 0.481 ms | 18.4 KB | 294 | 0.482 ms | 18.4 KB | 294 |
| Settings | 0.831 ms | 32.8 KB | 518 | 0.790 ms | 32.4 KB | 512 |
| Sidebar（工作区树） | 1.259 ms | 65.2 KB | 1099 | 1.264 ms | 65.2 KB | 1099 |

### 4.3 冷渲染 vs 稳态帧

- `ui.Render` 比稳态帧贵 **15–30 倍**（Home 21.7ms vs 0.65ms，Session 31.4ms vs 1.09ms）。差额来自无头 runtime 冷启动、字体/解析/布局缓存未热、最多 20 帧 settle 与整幅图像分配——都是一次性成本，**不是每帧成本**。
- 900×600 的 `ui.Render` 约减半像素量但只降到 45–55%（固定成本占大头）；稳态帧在 900×600 也只降 5–10%（元素数量不变，成本在树/文本而非像素）。

## 5. 观察与结论

1. **稳态帧预算充裕**：最重的 Sidebar 工作区树 1.26ms、Session 1.09ms，均 < 16.7ms（60fps）一个数量级余量。mygo 的即时模式重建（每帧重跑 view 函数）在桌面规模下不是瓶颈，**不需要为此引入快照/增量渲染重构**。
2. **相对成本排序**：Sidebar 树（1.26ms）> Session（1.09ms）> Settings（0.83ms）> Plugins（0.80ms）> Home（0.65ms）> Automation（0.48ms）。成本随**语义元素数量**走（Sidebar 每行有图标/时间/按钮；Session 有 Markdown 块 + 工具卡 + composer + usage）。
3. **分配集中在元素构造**：稳态帧 294–1099 次 allocs、18–65KB/帧。Session 比 Home 多约 240 allocs/帧，对应 Markdown 视图与工具卡；Sidebar 树多约 700 allocs/帧，对应每行三个子元素。属"每帧重建树"模型的预期成本，GC 压力低（65KB/帧 × 60fps ≈ 3.9MB/s，可接受）。
4. **测试/预览成本需正确解读**：`ui.Render` 的 21–31ms 说明跑一次无头截图测试≈数十毫秒，适合做预览/回归但不宜在循环里逐 token 调用；Session 预览测试目前单张图一次，成本可接受。
5. **无明显低垂果实**：没有单帧 >16.7ms 的屏；当前不建议为性能改动视觉/结构。若后续要做流式高频重排或多会话并排，优先用 CPU profile 定位（`go test -bench BenchmarkWarmFrame -cpuprofile`），再考虑把稳定子树缓存或降频重建。
6. **基线用途**：把 4.2 表作为回归基线；若某次改动使任一屏稳态帧超过基线 1.5×，应视为性能回归并在 CI/评审中说明。

## 6. 局限

- 无 GPU/真实窗口：mygo 窗口后端与 headless 共享布局/绘制路径，但窗口合成、字体 hinting 与真实 DPI 缩放可能不同；本基准测的是应用侧成本。
- 服务器 CPU（Xeon 5220R）单核频率低于主流桌面 CPU，绝对数字偏保守。
- `GOMAXPROCS=96`（96 逻辑核）下测；用户桌面核数更少，单帧渲染是单线程为主，数字大致可平移，但 GC 并行度不同。
- 数据为 3 次运行中位数；`ui.Render` 在 900×600 Session 上波动较大（11.7–16.0ms），对比时建议重跑取中位数。
- 基准在 HEAD `b3ff65f`（markdown 迁移完成后）测得；后续视觉改动应重跑并更新本文件。

## 7. 后续（可选）

1. 在 Lead 的全包门禁里增加一条非阻断的性能记录：`go test -run '^$' -bench 'BenchmarkWarmFrame' -benchmem ./internal/shell`，把结果贴到发布检查单（不设硬阈值，先积累趋势）。
2. 流式高峰期可选做一次 profile，确认 Markdown/工具卡是否是热路径；若是，优先复用解析结果（mygo 已内置增量解析）而不是改渲染结构。
3. 真实窗口（X11/Wayland/Windows）上的帧时间与 DPI 缩放另做一轮真机测量，补齐 headless 与窗口后端的差值。

## 8. Trajectory 虚拟化前后（v1.1，2026-11-05）

> 背景：[known-limitations](known-limitations.md) 记录 Trajectory 未虚拟化；v1.1 把事件列表从 `ui.Scroll` + 一次性构建全部行改为 mygo `ui.List` 虚拟列表（只构建可视行）。本节记录同一宿主机、同一 fixture 的前后对比。

### 8.1 夹具与方法

- **夹具**：`newBenchApp(b, Session, nil)`（真实 store/agent/controller + 一个空会话），再向会话追加 **1000 条合成事件**（交替 `tool_start`/`status`，首尾各一条带唯一 payload 的行），并把 Session 切到 Trajectory Tab。1000 行是旧实现每帧都要全量构建的数量，也是虚拟化收益最直接的规模。
- **旧实现**：HEAD `2d8537f` 的 `trajectory.go`（`ui.Scroll` + `for _, entry := range entries` 全量构建）。为了与工作区隔离，旧数字在 `git archive HEAD` 出来的干净副本里跑（只替换了 `render_bench_test.go` 加入同一 Trajectory 基准）。
- **新实现**：当前工作区（`ui.List`，`ListState.Key` = 事件序号）。
- 数字为 **3 次运行的中位数**，同一容器、同一 `CGO_ENABLED=0`、无 GPU；其他屏/其他队友的未提交改动可能带来 ±10% 噪声，结论看量级。

```sh
export PATH=/usr/local/go/bin:$PATH GOMODCACHE=/data/mygo/.cache/gomod CGO_ENABLED=0

# 冷渲染（含一次性 startupsettle；与 §4.1 同口径）
go test -run '^$' -bench 'BenchmarkRenderTrajectory$' -benchmem -count=3 ./internal/shell

# 稳态帧（与 §4.2 同口径）
go test -run '^$' -bench 'BenchmarkWarmFrame/Trajectory' -benchmem -count=3 ./internal/shell
```

### 8.2 结果（1000 事件 trace）

| 指标 | 旧：Scroll 全量构建（2d8537f） | 新：`ui.List` 虚拟化 | 变化 |
| --- | ---: | ---: | ---: |
| `ui.Render` 1600×1000 | 128.8 ms · 47.7 MB · 47,653 allocs | **42.6 ms · 10.3 MB · 22,386 allocs** | 时间 −67%，内存 −78%，allocs −53% |
| `ui.Render` 900×600 | 104.6 ms · 43.3 MB · 47,638 allocs | **26.4 ms · 5.3 MB · 21,930 allocs** | 时间 −75%，内存 −88%，allocs −54% |
| 稳态帧 1600×1000 | 22.7 ms · 1.07 MB · 17,451 allocs | **3.12 ms · 687 KB · 10,695 allocs** | 时间 −86%（约 7.3×），allocs −39% |
| 稳态帧 900×600 | 17.8 ms · 1.07 MB · 17,451 allocs | **2.66 ms · 681 KB · 10,573 allocs** | 时间 −85%（约 6.7×），allocs −39% |

### 8.3 解释

- **为什么差这么多**：旧实现每帧构造 1000 个事件行、每行 10+ 个元素（时间文本、色点、标签、详情、caret 等），行数与语义树负载线性相关；新实现每帧只构造视口内的行（1600×1000 约 18–20 行，+2 overscan），列表其余部分只参与高度估算与锚点换算，1000 行与 20 行的帧成本同量级。
- **剩余成本**：新稳态帧的 ~10.7k allocs 主要来自侧栏/会话 header/composer 等固定部分，而不是事件行；List 自身的测量/锚点计算是轻量算术。
- **行为不回退**：虚拟化保持时间轨、事件色点/标签、×count pill、raw 展开块、点击/Enter/Space、a11y Label（行成为 List 项）与滚动语义；展开状态按事件 id（`Seq`）保存在 `liveUIState.expandedRaw`，追加事件不会丢展开或跳动。见 `trajectory_test.go` 的 `TestTrajectoryVirtualized*`。
- **取舍**：`FollowEnd=false`（打开在最早事件、不随流式新事件自动跟随），与旧 Scroll 行为一致；若产品希望 trace 像日志一样跟随末尾，改这一个字段即可。

### 8.4 备注

- 1000 事件仍远高于日常会话（当前会话规模下旧实现已达标）；虚拟化主要为长会话/大 trace 留余量，不改变短 trace 的视觉。
- 旧数字在干净的 `2d8537f` 副本测得，用于排除工作区未提交改动的影响；复测时建议同样用 `git archive HEAD` 避免共享工作区干扰。
