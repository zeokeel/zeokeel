# ADR 0001：退役 DSH 像素对齐基建

- 日期：2026-11-05
- 状态：**已采纳（用户决策）**
- 决策者：用户；记录：Lead / parity-cleanup
- 关联：[调研总报告](../research/00-report.md)、[代码度量](../research/01-code-audit.md)、[mygo 原生原型](../research/02-mygo-native.md)、[UI/UX 评估](../research/03-ux-design.md)、[功能对齐矩阵](../FUNCTIONAL-PARITY.md)
- 后续：[ADR 0002 品牌主题基础](0002-brand-theme-foundation.md)（由品牌主题任务记录）

## 1. 背景

`/data/mygo`（ZeoKeel）此前的工程目标是「与 DSH（DeepSeek Harness，基线 0.2.1-rc.2）做**像素 / 交互 / 功能三重 1:1 对齐**」：建立 106MB 基线截图/控件/CSS 资产、`internal/pixeltest` 像素比对库、`TestScreensBaseline` / `TestScreensDarkNarrow` 像素门禁、CDP 采集脚本，并把像素预算写进 CI 与文档。

用户重新定位产品：**ZeoKeel = DSH 桌面替代/迁移器 + 独立品牌产品**。据此拍板：**功能对齐即可；视觉与交互自有（mygo 原生设计），停止 DSH 像素级对齐**。2026-11-05 完成三条并行调研（代码度量、mygo 原生原型、UI/UX 评估），结论见 `docs/research/00-report.md`。

> 口径变更（2026-11-05 起生效）：
> - 旧：「与 DSH 做像素 / 交互 / 功能三重 1:1 对齐」
> - 新：「**功能对齐 DSH；视觉与交互自有（mygo 原生设计），像素对齐已退役**」

## 2. 决策

1. **停止像素级对齐**：不再以 DSH 截图/计算样式/CSS 作为视觉验收标准，不再维护逐像素 diff 预算与白名单。
2. **功能对齐保留为活目标**：能力与行为对齐 DSH（`docs/FUNCTIONAL-PARITY.md` 继续维护；`docs/PLAN.md` §7 功能矩阵继续有效）。
3. **视觉与交互自有**：以 mygo `ui` 原生控件 + `ui.Theme` 为基础，叠加 ZeoKeel 品牌主题（`internal/brand`，见 ADR 0002）；交互按桌面平台惯例本地化（系统菜单栏、通知、文本选择、DPI、窗口状态等），不再复刻 Web 交互模型。
4. **删除 parity 专项基建**（`baseline/`、`docs/baseline/`、`artifacts/`、`scripts/cdp.mjs`、`scripts/capture/`、`internal/pixeltest/`），删除前整包归档。
5. **CI/门禁移除像素步骤**：`scripts/ci.sh` 去掉 6 次像素测试调用；`scripts/evolve/gates.sh` 去掉 `pixel-light` / `pixel-dark` 两个 step 及 masked/raw 指标。
6. **文档口径同步修正**：README、PLAN、STATUS、QUALITY 等去掉“三重 1:1”承诺与基线活链接；两份 parity 审计文档标注为历史。

## 3. 原因（基于实测证据）

1. **像素指标在现有渲染栈上不可达**：~85% 残余差异是 Chromium subpixel AA vs mygo 灰度 AA 的渲染器地板；`TestScreensBaseline` 容差放大到 48 时 session 仍有 4.5%+ 差异（[00-report §3.2](../research/00-report.md)）。
2. **门禁并没有覆盖真实 UI**：像素测试只渲染 static 假屏 `&App{Ctrl:nil}`，生产恒走 live 路径；用户真正看到的界面从未进入像素门禁（`internal/shell/screens_test.go`、`internal/app/app.go`）。
3. **指标已失去验收意义**：PLAN 目标为非文本区 ≤0.2%、SSIM ≥0.98；实际落地预算放宽到 masked home 2.0% / session 8.5% / plugins 4.5% / settings 4.2% / automation 2.2%，dark/narrow 预算=实测+3pp（放大 10–42 倍），实质退化为“布局别继续变坏”的回归检查。
4. **成本高且持续**：`baseline/` 605 文件 / 106MB、`docs/baseline/` 19 文件、`artifacts/` 185 文件；每次 CI 多跑 6 次 shell 像素测试（shell 包全量 39–41s）；DSH 发版即需独立基线更新 PR。
5. **产品定位与差异化**：作为 DSH 桌面替代/迁移器 + 独立品牌，像素克隆不产生用户价值，却持续占用 UI 维护成本并带来品牌识别风险。
6. **桌面 UX 需要自有交互**：hover-only 操作、消息文本不可选、无系统菜单栏/通知、无窗口状态、DPI/文本缩放未接线等是“Web 外观 + Web 交互模型”的系统性缺口，原生化收益高于像素拟合（[00-report §5](../research/00-report.md)、[03-ux-design](../research/03-ux-design.md)）。

## 4. 删除清单（已归档，2026-11-05）

| 路径 | 内容 | 删除前数量/大小 | 归档核验 |
| --- | --- | --- | --- |
| `baseline/` | screenshots 80 / controls 74 / css 53 / behavior 397 / README | 605 文件，约 106MB | 归档 612 项 |
| `docs/baseline/` | index / coverage / css / motion / dark-narrow / screens/* | 19 文件，约 116KB | 归档 21 项 |
| `artifacts/` | screens / diff / diff-dark / diff-narrow / live / verify / overlay / probe 等生成物 | 185 文件，约 8.9MB | 归档 198 项 |
| `scripts/cdp.mjs` | CDP 采集脚本（截图/控件/CSS/行为） | 1 文件，约 26KB | 归档 1 项 |
| `scripts/capture/` | 25 份采集 dump | 25 文件，约 108KB | 归档 26 项 |
| `internal/pixeltest/` | diff.go / diffimg.go / diff_test.go | 3 文件，约 16KB | 归档 4 项（含目录项） |
| **合计** | 整数包 | — | **862 项** |

配套删除的 parity 专项测试（`TestScreensBaseline` / `TestScreensDarkNarrow` / icon/live 截图等）由并行任务在同一决策下清理，记录见 `docs/research/04-parity-test-inventory.md`。

## 5. 归档位置与回滚

- **归档包**：`/data/zeokeel-backups/parity-assets-20261105.tar.gz`（约 48MB，862 项，2026-11-05 打包）。
- **核验**：`tar tzf /data/zeokeel-backups/parity-assets-20261105.tar.gz | grep '^baseline/screenshots/00-home.png'`。
- **整体回滚**：仓库已打 tag `pre-native-ui`（提交 `b974d92 snapshot: pre-native-UI migration (DSH pixel parity era)`），回滚命令：
  ```sh
  git checkout pre-native-ui        # 回到像素对齐时代（只读查看或建分支）
  git checkout master -- .           # 或按需取回单文件/目录（当前分支 master）
  ```
- **局部取回**：归档包内路径为仓库相对路径，在仓库根解压即可：
  ```sh
  tar xzf /data/zeokeel-backups/parity-assets-20261105.tar.gz baseline/screenshots/00-home.png
  tar xzf /data/zeokeel-backups/parity-assets-20261105.tar.gz docs/baseline/
  ```
- **注意**：`baseline/`、`artifacts/` 已被 `.gitignore` 忽略（防止再次误提交）；从归档取回仅作**只读历史参考**，不得重新引入为验收门禁。

## 6. CI/门禁与文档变更

- `scripts/ci.sh`：删除 `pixel baselines (light)`（5 次）与 `pixel baselines (dark/narrow)`（1 次）共 6 次像素调用；保留 gofmt / vet / build / `go test ./...` / no-cgo / 工具可调用 / 覆盖率 / race。
- `scripts/evolve/gates.sh`：删除 `pixel-light`、`pixel-dark` 两个 step、`ARTIFACTS_DIR`、像素 metrics 正则与 JSON/Markdown 指标输出；保留 fmt/vet/build/test/nocgo 与 flaky 重跑语义。
- 文档：
  - `README.md`：口径改为“功能对齐 DSH + 自有视觉/交互”；删除基线截图/CSS/控件链接与 `artifacts/` 图片引用。
  - `docs/PLAN.md`：§0/§1.2/§1.3/§2/§4.6/§4.7/§5/§5.9/§10/§11/§12/§13/附录 A–D 中与像素/基线/1:1 相关的段落改为“历史参考/已退役”，并记录本条 2026-11-05 决策；eino 运行时等有效内容不改。
  - `docs/STATUS.md`：顶部加入 2026-11-05 方向变更说明；历史里程碑保留并标注像素门禁已退役。
  - `docs/QUALITY.md`：门禁定义移除像素预算。
  - `docs/UI-PARITY-AUDIT.md`、`docs/COMPONENT-PARITY.md`：头部标注“历史文档，已退役 2026-11-05，不再维护”。
  - `docs/EVOLUTION.md`：顶部加一行状态说明（门禁机制以 `scripts/ci.sh` / `scripts/evolve/gates.sh` 现行为准）。

## 7. 有意保留的引用（不是遗留 bug）

- `docs/research/00～03`：调研历史快照，保留原始语境与 `baseline/` 路径引用（不再能点击，属历史证据）。
- `docs/FUNCTIONAL-PARITY.md`：**活文档**，功能对齐仍是目标。
- `internal/shell`、`cmd/zeokeel/main.go` 等生产代码注释中仍可能提到 `baseline/`/基线文档历史路径（这些文件不在本任务写范围）；仅注释性质，不影响构建，下一阶段随原生化替换清理。
- `.gitignore` 中 `/baseline/`、`/artifacts/` 忽略规则保留，防误提交。
- 历史文档里指向 `docs/baseline/` 的旧路径按历史说明处理，不再作为活链接。

## 8. 品牌资产盘点（只读，供下一阶段）

### 8.1 现有资产

| 资产 | 路径 | 说明 |
| --- | --- | --- |
| 图形标（浅色） | `logo/logo.svg` | 1254×1254，圆角矩形白底 + 蓝/青/紫渐变图形标 |
| 图形标（深色） | `logo/logo-dark.svg` | 同上，仅容器底色改为 `#000000` |
| 字标规格 | `docs/wordmark.md` | 图形标 + “ZeoKeel”文字；系统字体栈、17px/600、间隔 8px；浅色文字 `#0F1115`、深色 `#F9FAFB` |
| 预览图 | `assets/wordmark/preview-a.png`、`preview-b.png`、`base-b.png`、`agent-badge.png`、`mark-48.png` | 字标候选与 48px 图形标预览（非最终 UI 资源） |
| 预览脚本 | `scripts/render-wordmark.sh` | 用 ImageMagick 生成上述 PNG（仅预览字体，实现走系统字体栈） |
| 品牌主题（建设中） | `internal/brand` | mygo `ui.Theme` 品牌覆盖，见 ADR 0002 |

### 8.2 品牌色板（从 `logo/logo.svg` 提取）

| 类别 | 色值 | 说明 |
| --- | --- | --- |
| 主蓝 | `#0946EF`、`#0D44F6` | 图形标左侧/中部主色，建议作为品牌主色基准 |
| 靛蓝/紫 | `#1D32FD`、`#3935FB`、`#755BFD` | 渐变过渡色 |
| 青/天蓝 | `#37E7FE`、`#2ECCFB`、`#3CBBFA` | 渐变高光与点缀 |
| 浅蓝/高光 | `#CFE5FF`、`#9BB7FA`、`#A2BBFE`、`#F0F8FB` | 图形标高光与浅色层次 |
| 容器背景 | `#FFFFFF`（浅色）/ `#000000`（深色） | 图形标圆角矩形底 |
| 字标文字 | `#0F1115`（浅色）/ `#F9FAFB`（深色） | `docs/wordmark.md` |
| 界面中性参考 | `#F9FAFB`、`#F1F3F5`、`#DADBDC` | 现有预览脚本中的浅色中性色 |

### 8.3 下一步品牌方向（不在本条决策中设计新 logo）

1. **品牌主题**：以 mygo `LightTheme()` / `DarkTheme()` 为底，覆盖 Accent / AccentHover / Selection / Focus / Radius / Spacing / Font（系统字体栈）；具体值由 ADR 0002 记录。Accent 建议取品牌主蓝（`#0946EF`/`#0D44F6` 族）的单一值，渐变只用于 logo 与局部装饰。
2. **字标**：严格按 `docs/wordmark.md` 实现（图形标 24×24 + 文字），不重绘、不改色、不打包字体；当前 `assets/wordmark/*.png` 仅作历史预览，不作为运行时资源。
3. **图标**：沿用已建立的 `internal/icons`（Lucide 等价图形，ISC）或评估 mygo 原生图标；不再按 DSH 截图逐枚对照重绘。
4. **交互原生化**：系统菜单栏、系统通知、消息文本选择、窗口尺寸/位置持久化、DPI/TextScale/HighContrast——优先于任何视觉打磨（详见 [00-report §7 Step 1](../research/00-report.md)）。
5. **视觉验收**：改为“自有设计系统组件样本 + 关键屏自身回归（不对比 DSH）”；不再引入外部基线截图。
6. **待拍板**（下一阶段）：品牌主色最终值（单值 vs 强调渐变）、字标最终版、是否保留部分 DSH 视觉语言（当前按“自有视觉”执行）、目标平台顺序。
