# ZeoKeel 原生视觉预览（2026-11-05）

全部截图由各屏的 headless 预览测试生成（大多数为 `ui.Render(..., 1600, 1000, 1)`；独立的 Settings window 为 `900×720`，见下表），live fixture，中文默认，可复现：

```sh
export PATH=/usr/local/go/bin:$PATH
# 普通测试/CI 只渲染到临时目录，不写工作树；要刷新以下快照时显式开开关：
ZEOKEEL_UPDATE_PREVIEWS=1 go test -count=1 ./internal/shell -run 'TestRender.*NativePreview'
```

| 屏 | 浅色 | 深色 |
| --- | --- | --- |
| Home | [home-native-light.png](home-native-light.png) | [home-native-dark.png](home-native-dark.png) |
| Session | [session-native-light.png](session-native-light.png) | [session-native-dark.png](session-native-dark.png) |
| Sidebar | [sidebar-native-light.png](sidebar-native-light.png) | [sidebar-native-dark.png](sidebar-native-dark.png) |
| Settings | [settings-native-light.png](settings-native-light.png) | [settings-native-dark.png](settings-native-dark.png) |
| Plugins | [plugins-native-light.png](plugins-native-light.png) | [plugins-native-dark.png](plugins-native-dark.png) |
| Automation | [automation-native-light.png](automation-native-light.png) | [automation-native-dark.png](automation-native-dark.png) |
| Trajectory | [trajectory-native-light.png](trajectory-native-light.png) | [trajectory-native-dark.png](trajectory-native-dark.png) |
| Right sidebar（Start） | [right-sidebar-native-light.png](right-sidebar-native-light.png) | [right-sidebar-native-dark.png](right-sidebar-native-dark.png) |
| Right sidebar（Files） | [right-sidebar-files-light.png](right-sidebar-files-light.png) | [right-sidebar-files-dark.png](right-sidebar-files-dark.png) |
| Tool cards | [toolcard-native-light.png](toolcard-native-light.png) | [toolcard-native-dark.png](toolcard-native-dark.png) |
| Settings window（900×720） | [settings-window-native-light.png](settings-window-native-light.png) | [settings-window-native-dark.png](settings-window-native-dark.png) |

设计口径见 [docs/design/native-ui.md](../native-ui.md)；品牌色板与主题见 [ADR 0002](../../decisions/0002-brand-theme-foundation.md)。

## 视觉回归（opt-in，v1.1）

这些 PNG 同时是品牌视觉的 golden。仓库提供两种**同机可复现**的回归检查；它们比对的是 ZeoKeel 自有品牌预览，不是已退役的 DSH 像素基线（ADR 0001），也**不进 CI 硬门禁**（跨机器字体/DPI/时钟会漂移，见 [known-limitations §4](../known-limitations.md)）。

### 1) 脚本：`sh scripts/visual-check.sh`

无头重生成全部预览 → 用 ImageMagick 与金标逐张比较，输出差异像素数、百分比与差异图：

```sh
sh scripts/visual-check.sh                     # 默认阈值 2%，FAIL 时 exit 1
sh scripts/visual-check.sh --threshold 3       # 阈值 3%
sh scripts/visual-check.sh --fuzz 1            # 容忍每像素 1% 色差
sh scripts/visual-check.sh --all               # 连时钟屏一起比（默认跳过）
sh scripts/visual-check.sh --update            # 显式刷新金标（人工 review 后提交）
```

- 渲染会临时覆盖 `docs/design/preview/`，脚本退出时自动还原工作树；`--update` 是唯一保留新图的模式。
- 差异图与 got 图输出到 `/tmp/zeokeel-visual/diff/`（`--out`/`ZEOKEEL_VISUAL_DIFF_DIR` 可改）。
- 默认跳过三组已知含时钟/绝对时间的预览：`session-native-*`（消息脚注 HH:MM）、`automation-native-*`（下次/上次运行时间）、`trajectory-native-*`（事件 HH:MM:SS）；`--all` 可查看它们的漂移。
- 本机全量跑约 12s（22 张渲染 + 比较）。

### 2) 测试：`TestVisualRegression`（默认 skip）

```sh
ZEOKEEL_VISUAL=1 go test -count=1 -run TestVisualRegression ./internal/shell
```

- 覆盖 Home / Sidebar / Settings / Plugins / Automation / Trajectory / Session 的浅色与深色，对照本目录 PNG；约 2s。
- 时钟/日期文本按新鲜渲染中的文本矩形掩码（Session 脚注、Automation 下次运行、Trajectory 事件时间）；普通 `go test ./internal/shell` 跳过本测试，不拖慢 CI。
- 阈值：`ZEOKEEL_VISUAL_THRESHOLD`（默认 0.02=2%；≥1 视为百分数）、`ZEOKEEL_VISUAL_PIXEL_TOL`（每通道容差，默认 0）、`ZEOKEEL_VISUAL_DIFF_DIR`（默认 `/tmp/zeokeel-visual`）；失败时写 got/diff 图。
- 已知残余抖动：工具卡异步图片加载约 0.006%（阈值内）。

### 金标与环境

- 金标与**生成机器的字体环境**绑定（本次生成环境含 NotoSansCJK / DroidSansFallbackFull / Noto Mono 字体栈；本机 CJK fallback 渲染）。换机器、安装/更换系统字体后，先 `--update` 重建金标并人工 review 差异，再提交。
- 修改任何屏的视觉（颜色/间距/文案）后：跑 `--update`（或 `ZEOKEEL_UPDATE_PREVIEWS=1 go test ...`）刷新 PNG → review 截图 → 提交 PNG 与代码同一 commit；opt-in 回归用于阻止"无意的视觉漂移"，不用于外部产品对齐。
