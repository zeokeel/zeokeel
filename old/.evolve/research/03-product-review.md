# ZeoKeel 产品/设计自评审（03）

> 评审对象：`/data/mygo`（ZeoKeel，纯 Go 桌面 Agent；mygo 原生 UI + eino runtime；目标 DSH 0.2.1-rc.2 像素/交互/功能三重 1:1）
> 评审日期：2026-11-04 · 评审人：product-reviewer（Team 成员）
> 方法：文档主张 → 代码/测试证据对照（file:line）→ 五轴 rubric → Top10 缺口 → 人类拍板项。
> 边界：只读源码；未跑真模型 e2e；`TestScreensBaseline` 按任务许可实际执行（会重写 `artifacts/` 产物）；无 git，无法用 blame/PR 证据。
> 时点说明：评审期间工作树被并发写入（18:50–19:00 多个源文件 mtime 变化、19:01 曾出现一次 build failed，19:02 复测已恢复）；本报告行号/数字以 2026-11-04 19:00 读取内容为准，门禁结论以 18:57 的全绿运行为准；最终验收必须在所有写入者停止后由 Lead 重跑。

---

## 0. 一页结论

**结论：工程实现的质量明显高于文档所承诺的“证据闭环”。** 代码侧有 542 个测试函数、无头像素回路、a11y/动效测试，`go test/vet/gofmt/check-nocgo` 全绿；但“1:1”的门禁标准、状态真源与审计文档三者没有闭环：像素门禁只渲染 static 假屏、PLAN 定义的 SSIM/ROI/0.2% 阈值未实现、三份文档互相矛盾、审计文档中大量“待修”项已在代码中修好。

**当前评分（0–5，权重：视觉 30% / 产品 25% / 交互 20% / 文档真实性 15% / 可测试性 10%）**

| 维度 | 分数 | 一句话依据 |
| --- | --- | --- |
| 产品功能 | 3.0 | 工具/审批/自动化/插件/MCP 等主链路真实可用；但命令面板 9<15、`/compact` 是死命令、附件/图片上传缺失 |
| 交互 | 3.0 | 138 处 `ui.NewTester` 驱动、键盘/审批/队列有测试；但 composer 仍是单行输入、错误被静默吞掉、IX 用例库无同名测试 |
| 视觉 | 2.0 | static 五屏 masked diff 0.86–6.27% 且可复现；但 live UI 无像素断言、深色 9–31%、字体未应用、无几何/SSIM 断言、视图层 53–54 处样式字面量 |
| 文档真实性 | 1.0 | README/STATUS/PLAN/审计文档 4 处互相矛盾、3 份审计文档过期、基线 diff 数字与实跑不符、`baseline/VERSION` 缺失 |
| 可测试性 | 2.5 | headless 全绿、测试基数大；但无 CI、无性能基准、e2e 需凭据且无留存证据、测试会写仓库 `artifacts/` |
| **加权总分** | **2.4 / 5** | **结构优秀、证据链未闭环；不建议对外宣称“像素/交互/功能 1:1 完成”** |

**给 Lead 的一句话**：下一步最高 ROI 不是再压 0.1pp 像素，而是把“live 视图 + 门禁标准 + 文档真源”三件事做成机械可复验的闭环。

---

## 1. 文档 vs 代码抽查（10 条）

方法：每条“文档声称已完成/已实现”的条目，找到对应代码或测试的 `file:line`，裁决定义为——
**✅ 属实**（有代码+测试支撑）/ **⚠️ 部分属实**（有实现但边界未披露或指标过期）/ **❌ 过时/夸大**（文档与代码矛盾）。

| # | 文档声称（出处） | 代码/测试证据 | 裁决 | 说明 |
| --- | --- | --- | --- | --- |
| 1 | `M0–M10 完成`、五屏 masked diff `0.93/6.40/3.17/2.31/1.34%`（README.md:33；PLAN.md:5） | 实跑 `go test ./internal/shell -run TestScreensBaseline -v`：masked `0.86/6.27/2.80/2.49/0.82%`，raw `2.13/8.29/4.25/2.99/2.55%`（screens_test.go:30-34、:72） | ⚠️ | “完成”大致成立；但记录的 diff 数字已过期/不可复现（settings 2.49% > 文档 2.31%），且预算实际放宽到 masked 2.0/8.5/4.5/4.2/2.2%、raw 3.5/11/6.5/5/4% |
| 2 | `基线文档 100% 收口…尚未开始编码`（README.md:41）；`M0 进行中`（STATUS.md:9） | 同一 README 上一段已写 `M0–M10 完成`（README.md:33）；STATUS 后续章节 M1–M10 全部标“完成”（STATUS.md:44、:65、:79…:247）；`internal/shell/*.go` 64 个文件、18 包全绿 | ❌ | README 自身前后矛盾；STATUS“唯一状态真源”（STATUS.md:1）内部矛盾。**典型过期文案** |
| 3 | `PLAN §11 里程碑 M0–M7`（PLAN.md:935-942）与 `状态：M0–M10 完成`（PLAN.md:5）、`STATUS M7=Agent 模式/M10=多模态`（STATUS.md:172、:247） | PLAN §11 只定义 M0–M7（M7=硬化发布：打包/签名/自动更新）；STATUS 的 M7–M10 是另一套语义；`dist/` 只有 Windows 产物，无安装包/签名/自动更新 | ❌ | 两套里程碑口径并存；按 PLAN 的 M7 出口标准看“M7 完成”不成立 |
| 4 | `五屏 UI … 逐屏与 DSH 基线像素比对`；方法含 live 实拍“逐像素 diff”（README.md:33；UI-PARITY-AUDIT.md:3；COMPONENT-PARITY.md:6） | `TestScreensBaseline` 渲染的是 `&App{Screen: c.screen}`（screens_test.go:51），无 `Ctrl`（static 假屏）；live 用例 `TestCaptureLiveSession` 只 `SavePNG`、不比较（capture_live_test.go:38-58）；真实 live 走 `session_live.go` 等另一套函数 | ❌ | **像素门禁不覆盖用户真实看到的 live UI**；“逐像素 diff live”在审计文档中构成误导 |
| 5 | `消息渲染仅纯文本、工具卡仅一种形态、首页仍静态、消息操作条缺`（COMPONENT-PARITY.md:32-34；UI-PARITY-AUDIT.md:34-35） | `internal/shell/markdown.go:1-11` + `session_live.go:264-293`（assistant 走 markdown）；`toolcards.go:58-71` 已按 bash/file/search/web/image/ask 等 6+ 变体渲染；`message_actions.go:31-70` 有 Copy/Retry/feedback；`home_live_test.go:10` 证明 live 首页 | ❌ | 审计文档是**修复前的快照**，未回写；“待修清单”已不可作为工作清单 |
| 6 | `37 个有序 section` 提示词层（STATUS.md:209；docs/prompts.md） | `internal/prompt/registry.go` 恰好 37 个 `ID:`；`services.go:487` 调 `prompt.Compose`；AGENTS.md 在 `services.go:473` 注入 | ✅ | 属实（含 EN/ZH、按工具裁剪） |
| 7 | `压缩：WindowCompactor…app 已启用`（STATUS.md:151） | `internal/app/services.go:439-440` 已接 `WindowCompactor{KeepLast:12}`/`ContextBudget:24000`；`runtime/compaction.go:20-51` | ⚠️ | runtime 属实；但用户入口 `/compact` 明确回“not available in this build”（composer_menus.go:47、:196-204）——**功能存在、命令是死路** |
| 8 | `i18n 全量 350 键`（STATUS.md:188） | `internal/shell/i18n.go:44` 的 map 实有 **362** 个键，另有 67 处 `a.tr(en,zh)` 内联双语 | ⚠️ | 数量基本属实（略少报）；但“全量”无法机械验证（内联双语、static 文案硬编码） |
| 9 | `深色/窄屏逐屏基线，预算内：home 12.54/session 22.49/plugins 4.05/settings 3.66%`（STATUS.md:191-193） | 实跑 `TestScreensDarkNarrow`：dark `home 9.26 / session 23.15 / plugins 3.14 / settings 5.03`，narrow `home 11.14 / session 31.47`；代码注释自述“They gate structural regressions, not DSH pixel parity”（baseline_dark_test.go:31-55、:73-104）；automation 无 dark/narrow 基线 | ❌ | 数字又过期；更关键：`session 23%/31%` 与 PLAN “浅色+深色同期交付、像素 1:1”不兼容，文档未把它列为未达标项 |
| 10 | `插件页移除 “+ Add plugin”`（PLAN.md:1002、:708 验收“无 Add plugin”）；`字标已选 A 已锁定`（PLAN.md:1000） | 代码保留按钮：`plugins.go:202` `Label("Add plugin")`；PLAN 同一节“仍需你拍板”又列字标与 Add plugin（PLAN.md:1007-1013）；`docs/wordmark.md:1` 已写“已定” | ❌ | 锁定决策与代码/文档三处不一致；属于“产品口径未收口”而非实现缺陷 |

**抽查小结**：10 条中 ✅1 / ⚠️3 / ❌6。过时与夸大集中在**状态类文档**与**审计类文档**；核心代码（prompt/tools/runtime/审批/自动化）抽查到的实现基本真实。这个分布说明：ZeoKeel 的短板不是“功能没做”，而是“做了但没人能机械相信”。

---

## 2. 可机械执行的评审 rubric

使用方式：每项 `PASS=1 / FAIL=0`（`human` 项不计入自动分，但必须由指定 owner 签字）；类别分 = `PASS 数 / 项数 × 5`；总分为加权和。**任何 S1 项 FAIL 视为发布门禁失败**。

### 2.1 产品功能（PF，权重 25%）

| ID | 检查项 | 判定方法（命令/文件检查） | 通过标准 | 证据采集 | 程度 |
| --- | --- | --- | --- | --- | --- |
| PF-01 | PLAN §7 功能矩阵逐行有实现+验收 | `awk '/^### 7.1/,/^### 7.3/' docs/PLAN.md` 抽行；每行 `grep -rn "<能力关键词>" internal/` 找代码 + `grep -rn "<能力>" internal --include=*_test.go` 找测试 | P0 行 100% 有代码+测试；P1 ≥90%；P2 有降级说明 | 脚本输出“矩阵行→证据文件”CSV | auto |
| PF-02 | 生产工具注册表可调用 | `CGO_ENABLED=0 go test ./internal/app -run TestProductionToolsInvocable -count=1 -v` | PASS；注册表 ≥40 工具；must-list 全部成功（tools_invoke_test.go:73） | 测试输出 + 工具数 | auto |
| PF-03 | 命令面板与基线命令目录一致且无死命令 | 解析 `composer_menus.go:34-48` 的 `slashCommands`；对每条检查 `runSlashCommand` 分支是否真的执行动作；与 `baseline/controls/68-session-commands.json` 的条目比对 | 命令数 = 基线目录；每条命令有真实动作；不得出现 “not available in this build” | 命令清单 diff + 测试 | auto |
| PF-04 | 审批/提问/交付闭环 | `go test ./internal/shell -run 'TestApproval\|Test.*Question\|Test.*Artifact' -v`；检查 `defaultApprovalTools`（services.go:35）与 gate 接线 | 允许/拒绝/始终允许/取消均有测试；提问可 resume；present 卡可见 | 测试输出 | auto |
| PF-05 | 插件开关→下一轮工具集生效 | `go test ./internal/app -run 'TestPluginToggles' -v`；核对每个 `plugin.<name>.enabled` 都有注册/移除分支 | 每个 UI 开关都有对应 gate；关闭后工具从 registry 消失 | 测试 + registry dump | auto |
| PF-06 | 自动化任务到点执行 | `go test ./internal/automation ./internal/app -run 'TestCron\|TestScheduler' -v`；核对 cron 子集与 UI 帮助一致（`automation/cron.go:1-15`） | 解析/下次执行/触发建会话测试通过 | 测试输出 | auto |
| PF-07 | 模型/权限/模式/语言设置跨回合生效并持久化 | `go test ./internal/session ./internal/shell -run 'TestSettings\|TestModel\|TestMode\|TestLanguage' -v`；检查 `SetSetting` 键 | 设置落库、重建 agent、下一回合生效 | 测试 + store 行 | auto |
| PF-08 | 功能降级清单可被用户看到 | 对照 PLAN §7.3 8 项，检查 UI/About/插件页是否有对应披露；人工判断文案 | 每项降级至少有 1 处产品内可见说明 | 截图 + 文案清单 | human |

### 2.2 交互（IX，权重 20%）

| ID | 检查项 | 判定方法（命令/文件检查） | 通过标准 | 证据采集 | 程度 |
| --- | --- | --- | --- | --- | --- |
| IX-01 | 交互用例库可追溯 | `grep -rn 'IX-' internal --include=*_test.go \| wc -l`（PLAN §6.9 要求“编号即测试名”） | ≥18 个 IX-xx 测试名；0 个缺失用例 | 计数 + 缺失列表 | auto |
| IX-02 | 键盘可达与焦点 | `go test ./internal/shell -run 'Test.*TabReachability' -v`；检查 `ui.Tester.Key(ui.KeyTab)` 用法（a11y_test.go:9-38） | 五屏 Tab 可达；无“Tab 永远到不了”的焦点项 | 测试输出 | auto |
| IX-03 | 快捷键集合与基线一致 | 从 `baseline/screenshots/73-shortcuts.png`/controls 提取列表，与 `shortcuts.go:19-21`、`chrome.go:33-41` 的 `OnShortcut` 比对 | 高频项全覆盖；冲突键为 0 | 两列 diff 表 | auto+human |
| IX-04 | 流式/停止/排队/重试行为 | `go test ./internal/session ./internal/shell -run 'Test.*Stop\|Test.*Queue\|Test.*Retry' -v` | 停止保留部分输出；队列只保留 1 条；重试不覆盖旧消息 | 测试输出 | auto |
| IX-05 | Composer 输入模型 | 检查 live composer 是否用多行控件：`grep -n 'TextArea\|TextInput' internal/shell/session_live.go`；键盘断言 Shift+Enter 换行 | 多行 + 自动增高 + Shift+Enter 换行 + Enter 发送（以基线为准） | AST/UI 树断言 | auto |
| IX-06 | 弹层/菜单焦点与 Esc 回归 | `go test ./internal/shell -run 'Test.*Esc\|Test.*Focus\|Test.*Menu' -v` | Esc 关闭且焦点回原位；打开时背景不可误点 | 测试输出 | auto |
| IX-07 | 错误/空态/离线反馈不吞 | 注入 `ErrNoAgent`/无工作区/工具失败，检查是否出现 toast/引导卡；`grep -n 'if err := a.Ctrl.Submit' -A3 internal/shell/session_live.go` | 用户可感知错误；不得静默 return | 交互测试 + 截图 | auto+human |
| IX-08 | 无障碍播报与对比度 | `go test ./internal/shell -run 'TestDarkContrast\|Test.*Reachability' -v`；`tt.Announcements` 断言 | 关键动作有播报；对比度 ≥4.5:1 | 测试输出 | auto |
| IX-09 | 动效时长在容差内 | `go test ./internal/shell -run 'TestMotion' -v`（12 个虚拟时钟测试）；对照 `design/motion.go` token 注释 | 断言 ±50ms；ReduceMotion 生效 | 测试输出 | auto |

### 2.3 视觉（VIS，权重 30%）

| ID | 检查项 | 判定方法（命令/文件检查） | 通过标准 | 证据采集 | 程度 |
| --- | --- | --- | --- | --- | --- |
| VIS-01 | static 五屏像素门禁 | `SCREEN=<name> go test ./internal/shell -run TestScreensBaseline -count=1 -v` | 每屏 masked ≤ 预算（screens_test.go:30-34）且 raw ≤ 预算 | `artifacts/screens/*.png`、`artifacts/diff/*.png`、日志 | auto |
| VIS-02 | **live 视图像素门禁** | `grep -n 'Compare\|pixeltest' internal/shell/capture_live_test.go`——当前只 SavePNG；要求新增 live 断言 | live（fake controller）每屏/每状态都有基线 diff 与预算 | live 渲染 + diff 图 | auto（当前 FAIL） |
| VIS-03 | 门禁标准符合 PLAN §1.2/§5.9 | 检查 `pixeltest` 是否实现 ROI 文本/非文本分类、SSIM、1px 几何断言：`grep -n 'SSIM\|Rect(' internal/pixeltest/*.go` | 非文本区 ≤0.2%/屏；文本区 SSIM ≥0.98；几何 ≤1px；白名单带 owner+到期日 | 门禁实现 + 报告 | auto（当前 FAIL） |
| VIS-04 | 设计系统单一来源 | `grep -rn 'ui\.Hex(\|ui\.RGBA(' internal/shell --include=*.go \| grep -v _test \| wc -l` 应为 0；`grep -c '^func (t \*Tokens)' internal/design/components.go` 覆盖 PLAN §5.4 映射表 | 视图层 0 字面量；组件函数覆盖映射表 ≥90%；有 AST lint 脚本 | 计数 + lint 输出 | auto（当前 54 处，FAIL） |
| VIS-05 | 渲染环境确定/可复现 | 在测试日志记录：`go version`、`fc-match <stack>`、mygo/eino 版本、DPR；重跑 diff 波动 ≤0.1pp | 文档记录的数字 = 当前实跑（±0.1pp）；跨机器差异有解释 | 环境指纹 + 两次 diff | auto |
| VIS-06 | 深色/窄屏对齐 | `go test ./internal/shell -run TestScreensDarkNarrow -v`；要求 automation 也有 dark/narrow 基线 | 每屏 dark/narrow 有基线且 ≤ 明示预算；无“结构回归 only”豁免 | dark/narrow 渲染 + diff | auto（当前 automation 缺基线） |
| VIS-07 | 品牌资产与字标 | 对照 `docs/wordmark.md` 检查 `app.go:242` 旁品牌行渲染；`grep -rn 'deepseek' internal/shell --include=*.go` 仅模型名残留 | 字标规格一致；无品牌残留 | 截图 + grep | auto+human |
| VIS-08 | 组件级样式 golden | 检查 `internal/design/*_test.go` 是否有浅/深两套组件样本 golden（PLAN §5.4 规则 5） | 每个组件函数 1 张 golden × 2 主题 | 测试输出 | auto |

### 2.4 文档真实性（DOC，权重 15%）

| ID | 检查项 | 判定方法（命令/文件检查） | 通过标准 | 证据采集 | 程度 |
| --- | --- | --- | --- | --- | --- |
| DOC-01 | 状态单一真源 | 只允许 `docs/STATUS.md` 含进度状态；`grep -rn '完成\|进行中\|尚未开始\|M0–M10' README.md docs/PLAN.md docs/*.md` 中 README/PLAN 只引 STATUS 链接 | 0 处非 STATUS 状态断言；STATUS 自身无自相矛盾小节 | grep 报告 | auto（当前 FAIL） |
| DOC-02 | 声明-证据可追溯 | 抽取文档中含“已实现/完成/✅/PASS/已修复”的行，每行必须能落到 file:line 或测试名；抽样 ≥10 行 | 100% 可追溯；不可追溯行删除或降级为“计划” | 声明→证据 CSV | auto+human |
| DOC-03 | 审计文档时效 | 对 `docs/UI-PARITY-AUDIT.md`/`COMPONENT-PARITY.md` 每一条 `❌/待修` 在代码中复现失败 | 0 条“已修复却仍列待修”；文档带“最后核对 commit/时间” | 逐条复核表 | auto |
| DOC-04 | 数字可复现 | 文档记录的 diff/测试数/键数，与命令实跑比对（如 `go test ... -v`、`grep -c`） | 差异 ≤0.1pp 或 ±2%；超差即更新文档 | 命令输出 vs 文档 | auto |
| DOC-05 | 锁定决策与代码一致 | `grep -n '已锁定' -A20 docs/PLAN.md` 逐条对 code（Add plugin、字标、主题默认、插件范围、样式路线） | 0 冲突；冲突必须人类重拍 | 决策对照表 | auto+human |
| DOC-06 | 基线可复现链路 | 检查 `baseline/VERSION`、`baseline/tokens.json`、`baseline/geometry.json` 是否存在；`internal/design/tokens.go` 是否生成 | 文件存在且生成器可重跑；`scripts/pixel-gate.sh` 存在 | 文件清单 + 脚本 | auto（当前缺失） |
| DOC-07 | 链接/资产完整性 | markdown 链接解析脚本（相对路径存在性、PNG 存在）；例：`docs/wordmark.md:17` 的 `assets/wordmark/preview-a.png`（实际文件在仓库根 `assets/`，docs 相对路径断链） | 0 断链 | 断链列表 | auto |

### 2.5 可测试性（TST，权重 10%）

| ID | 检查项 | 判定方法（命令/文件检查） | 通过标准 | 证据采集 | 程度 |
| --- | --- | --- | --- | --- | --- |
| TST-01 | 全量门禁 | `CGO_ENABLED=0 go test ./... -count=1 && go vet ./... && test -z "$(gofmt -l ./cmd ./internal)" && sh scripts/check-nocgo.sh` | 18 包全绿；vet/gofmt 干净；四目标交叉编译 PASS | 命令输出 | auto |
| TST-02 | 无显示器可跑 | `env -u DISPLAY go test ./internal/shell -count=1`；确认未依赖 Xvfb | PASS 且不启动 X | 命令输出 | auto |
| TST-03 | 测试金字塔达标/映射 | 统计：`grep -rh '^func Test' internal --include=*_test.go \| wc -l`、`grep -rh 't.Run(' \| wc -l`、`grep -rn 'NewTester' \| wc -l`；与 PLAN §10.1 目标（300/100/30/50）比对 | 单元 ≥300、视图 ≥100、交互 E2E ≥30、运行时 ≥50 | 统计表 | auto |
| TST-04 | e2e 可复现且有证据 | `go vet -tags e2e ./...`；`sh scripts/e2e-tools.sh`（凭据自动跳过要显式 exit 2）；要求提交脱敏 e2e 结果摘要 | tag 可编译；有凭据时 PASS；无凭据时 CI 不红但可见；仓库有最近一次证据 | 脚本输出 + 证据文件 | auto+human |
| TST-05 | 性能预算 | `grep -rn 'func Benchmark' internal --include=*_test.go`；要求启动/首帧/滚动/渲染基准 | PLAN §10.3 每项有 benchmark 且达预算 | benchmark 输出 | auto（当前 0 个，FAIL） |
| TST-06 | 测试不污染仓库 | 在干净树跑 `go test ./internal/shell` 后 `ls artifacts` 是否被改写；确认 `ARTIFACTS` 可指向临时目录 | 默认写临时目录或 CI 显式隔离；工作树无副作用 | 前后 mtime 对比 | auto |
| TST-07 | 门禁灵敏度（反向测试） | 人为移动一个容器/改一个令牌，确认 `TestScreensBaseline`/`TestScreensDarkNarrow` 必红；恢复后必绿 | 反向变更 100% 被抓；误报率 0 | 两次运行记录 | auto |
| TST-08 | CI 存在且等价本地门禁 | 检查 `.github/workflows` 或等效配置；核对 job 覆盖 TST-01/02/04/06/07 | 有 CI；本地命令与 CI 一致 | CI 配置 | auto（当前无 CI） |

### 2.6 一键 runbook（Agent 可直接执行）

```sh
cd /data/mygo
export PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0

# 1) 基础门禁
go test ./... -count=1
go vet ./...
test -z "$(gofmt -l ./cmd ./internal)"
sh scripts/check-nocgo.sh

# 2) 视觉信号（产物写 /tmp，避免污染仓库）
ARTIFACTS=/tmp/zk-pixel go test ./internal/shell -run TestScreensBaseline -count=1 -v
ARTIFACTS=/tmp/zk-dark  go test ./internal/shell -run TestScreensDarkNarrow -count=1 -v

# 3) rubric 机械探针
go test ./internal/app -run TestProductionToolsInvocable -count=1 -v
grep -c 'ui\.Hex(\|ui\.RGBA(' internal/shell/*.go | grep -v ':0'      # VIS-04，期望无输出
grep -rn 'IX-' internal --include=*_test.go | wc -l                    # IX-01，期望 ≥18
grep -c 'ID:' internal/prompt/registry.go                              # 期望 37
grep -rn 'func Benchmark' internal --include=*_test.go | wc -l         # TST-05，期望 >0
ls baseline/VERSION baseline/tokens.json baseline/geometry.json 2>&1   # DOC-06
grep -rn 'M0 进行中\|尚未开始编码' README.md docs/*.md                  # DOC-01，期望无
grep -rn 'Add plugin' internal/shell/plugins.go docs/PLAN.md           # DOC-05 冲突面
```

### 2.7 评分规则

- 每个 `auto` 项：`PASS=1/FAIL=0`；`auto+human` 项需 auto PASS 且 human 签字；纯 human 项只记录结论。
- 类别分 = `PASS/项数 × 5`；总分 = `0.25·PF + 0.20·IX + 0.30·VIS + 0.15·DOC + 0.10·TST`。
- 发布门禁：**S1 项（见 §3）必须 PASS**；否则无论总分多少都不得宣称“1:1 对齐完成”。
- 每轮评审固定产出：`runs/<date>/`（命令日志、diff 图、rubric CSV）+ 更新 `docs/STATUS.md`（唯一状态真源）。

---

## 3. 当前 Top10 缺口（按 rubric 评分与严重度排序）

严重度定义：**S1** 证据链/门禁失效，会导致错误对外结论或无法验收；**S2** 用户可感知的核心能力/体验缺口；**S3** 一致性与卫生问题。

| # | 严重度 | 缺口 | 证据（file:line / 命令输出） | 影响 | 建议修法（1–2 句） | Rubric |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | **S1** | **像素门禁只覆盖 static 假屏，live UI 无像素断言** | `TestScreensBaseline` 渲染 `&App{Screen:…}`（screens_test.go:51）；live 走 `session_live.go`；`TestCaptureLiveSession` 只 `SavePNG`（capture_live_test.go:38-58）；而 UI-PARITY-AUDIT.md:3 声称 live“逐像素 diff” | 1:1 像素证据不覆盖用户实际界面；live 回归可无声漂移；对 Lead/用户构成误导 | 给 fake controller 的 live 屏建立 golden diff 与预算，纳入同一门禁；或统一 static/live 为单轨渲染 | VIS-02 |
| 2 | **S1** | **PLAN 的“1:1”视觉门禁标准未实现** | PLAN 要求文本区 SSIM≥0.98、非文本≤0.2%/屏、几何≤1px（PLAN.md:49、:607）；实际 `pixeltest` 只有整屏容差 12 的像素比例（diff.go:18-42），预算 masked 2.0–8.5%、raw 3.5–11%（screens_test.go:30-34）；`grep SSIM internal` 为 0 | “1:1”不可判定；当前数字只能证明“结构没大动”，不能证明对齐 | 实现 ROI 文本/非文本分层 + SSIM + 盒子坐标断言；白名单必须带 owner 与到期日并进 CI | VIS-03 |
| 3 | **S1** | **文档状态多源冲突 + 审计文档过期** | README:33（M0–M10 完成）vs README:41（尚未开始编码）vs STATUS:9（M0 进行中）；PLAN:5（M0–M10）vs PLAN §11（M0–M7）；UI-PARITY-AUDIT:34-35/COMPONENT-PARITY:32-34 的待修项已在 markdown.go、toolcards.go、message_actions.go、home_live_test.go 实现 | 外部评审与未来 Agent 无法信任文档；自进化循环会基于错误现状做决策 | 以 STATUS 为唯一状态源（README/PLAN 只链接），删/改历史小节；审计文档加“最后核对时间+命令”并回写已修项 | DOC-01/03 |
| 4 | **S1** | **设计系统未落地：样式字面量散落视图层、组件函数不及 PLAN** | `grep ui.Hex/ui.RGBA internal/shell` 非测试文件共 **54 处**（19:00 复测 53 处；session.go 13、settings.go 11、home.go 7、plugins.go 5…）；`design/components.go` 只有 **12** 个组件函数（:36-137），PLAN §5.4 映射表要求 ToolCard/Prose/Input/SendButton/Popover/Pill/StatusDot/Toast/…；`scripts/lint-style-literals.sh` 不存在（PLAN.md:531、:901-903） | 改令牌不能全局生效；视觉一致性靠人肉；static/live 两套样式持续漂移 | 补齐语义组件函数、把 54 处字面量收进 design、加 AST lint 与 golden；完成后 VIS-04 才能 PASS | VIS-04 |
| 5 | **S2** | **基线锁定/生成链路缺失，diff 数字不可复现** | `baseline/VERSION`、`baseline/tokens.json`、`baseline/geometry.json` 均不存在（`ls baseline` 只有 behavior/controls/css/screenshots/README.md），而 PLAN.md:53 与 baseline/README 要求提交；`internal/design/tokens.go:1-8` 为手写常量非生成；无 CI；渲染依赖宿主 fontconfig（本机无 Segoe UI/PingFang，回退 DejaVu/Noto） | DSH 版本未锁定、令牌不可重生成；文档数字与实跑不一致（见抽查 #1/#9）；换机器结果漂移无法归因 | 补 VERSION/tokens/geometry 与生成器；测试日志记录 fontconfig/Go/mygo 指纹；把 pixel-gate.sh 做成 CI 可跑脚本 | DOC-06/VIS-05 |
| 6 | **S2** | **深色/窄屏远未 1:1，但被计入“完成”** | 实跑：dark `session 23.15%`、narrow `session 31.47%`、home 9.26/11.14；automation 无 dark/narrow 基线；代码注释自述“只 gate 结构回归，不是 DSH pixel parity”（baseline_dark_test.go:31-55）；STATUS:191-193 标为完成 | PLAN 锁定“浅色+深色同期交付”，当前深色实际是“主题不崩”而非对齐；对外宣称 1:1 有风险 | 在相同会话状态下重采 DSH dark/narrow，预算收紧到 light 的 ≤1.5×；或明确降级为 P1 并从“完成”中移出 | VIS-06 |
| 7 | **S2** | **核心输入体验缺口：单行 composer、无附件/图片上传、usage footer 无 context%** | live composer 用 `ui.TextInput`（session_live.go:349-352）；PLAN 要求多行自适应/Shift+Enter（PLAN.md:456、:617）；`+` 菜单只有 `@` 与 `/`（composer_menus.go:376-393），无文件/图片上传；`liveUsageText` 只有 turns/steps/out/Completed/tok/s/cache，无 context%（home_live_menus.go:403-430） | DSH 核心输入体验未对齐；长 prompt 无法换行编辑；用户图片无法进入多模态（仅工具图片可用） | 换 `ui.TextArea` + 自动增高 + Shift+Enter；`+` 增加文件/图片选择并写入 `Message.Images`；footer 补 context% | IX-05/PF-01 |
| 8 | **S2** | **命令面板仅 9 条（验收要求 15 条），`/compact` 是死命令** | `slashCommands` 9 条（composer_menus.go:34-48）；PLAN §7.1 验收“命令面板 15 条”（PLAN.md:710）；`/compact` 明确回不可用（composer_menus.go:196-204），但 `runtime.WindowCompactor` 已存在并接线（compaction.go:45-51；services.go:439-440） | 用户可见能力与文档/DSH 不一致；“压缩”入口有名无实，且与 STATUS“app 已启用”矛盾 | 给 Controller 暴露 Compact API 并把 `/compact` 接上；把命令目录与基线逐条对齐（15 条）后再更新 PLAN 验收 | PF-03 |
| 9 | **S2** | **错误/空态反馈被静默吞掉；always-allow 作用域不符文档** | 无模型/无 agent 时 `Ctrl.Submit` 同步返回错误，shell 直接 `return`，无 toast/引导（session_live.go:495-498；home.go:269 用 `_ =`）；PLAN §6.7 要求“未配置模型→引导卡片+发送提示”；`AllowAlways` 规则仅内存、跨会话全局（controller.go:1304-1318；runtime/approval.go 的 rules map） | 用户点发送“没反应”；审批免打扰不符合 PLAN §6.3“按会话/工作区记忆”；重启后规则丢失 | 统一 Submit 错误→用户可见文案与“去设置”入口；把 always-allow 落到会话/工作区设置并持久化 | IX-07/PF-04 |
| 10 | **S2/S3** | **锁定决策与产品口径冲突 + static 兜底渲染假数据** | PLAN.md:1002 锁定“插件页移除 + Add plugin”、§7.1:708 验收“无 Add plugin”，但 `plugins.go:202` 保留按钮；PLAN.md:1007-1013 又把字标/Add plugin 列为“仍需拍板”，而 docs/wordmark.md:1 已定；static 屏渲染 `10.1M`、`19.7M tok · Cache hit 99%`、`12:26`、`DeepSeek-V4.1-Flash` 等假数据（session.go:373、:491、:517-519）；PLAN §6.7/§8.5 明确“不造假数据/不伪造缓存统计” | 产品口径反复，用户/评审看到与决策不符的界面；无 services 时可能把演示数据当真实数据 | 由人类拍板 Add plugin 最终形态并同步 PLAN/代码；static 假数据只在测试构建/带“DEMO”标注时出现 | DOC-05/VIS-01 |

**次级观察（未入 Top10，建议进 backlog）**
- PLAN §9.2 声称 SQLite 有 `approvals/usage/skills/schedules/goals` 表与 FTS5（PLAN.md:851-853），实际 schema 只有 7 张业务表（schema.go:14-95，automation 替代 schedules），无 FTS；文档应标注“events/settings 承载”而非虚构表。
- About 版本硬编码在 i18n `version 0.1.0`（i18n.go:45），无 ldflags 注入；发版不可追溯。
- `scripts/pixel-gate.sh`、`scripts/lint-style-literals.sh`、`scripts/cross-build.sh` 在 PLAN §10.2 列为 CI 必跑（PLAN.md:901-903），实际 `scripts/` 无此三文件。
- 测试会改写仓库 `artifacts/`（本次 `TestScreensBaseline`/`TestCaptureLiveSession` 重写了 `artifacts/screens|diff|live`），CI 必须用 `ARTIFACTS=/tmp` 隔离。
- `go.mod` 声明 `go 1.27.3`，依赖 sonic 提示版本不支持并回退 `encoding/json`（构建警告），升级/钉版策略未写入文档。

---

## 4. 设计层：必须由人类拍板的点

> Agent 自评审可以给证据和建议，但以下属于**品味/路线/对外承诺**，不应由 Agent 代决。

1. **像素验收阈值**：是否接受“AA 地板”并把 session 6.27% masked / 深色 23–31% 写成豁免？SSIM/ROI 白名单的 owner 与到期日谁签？（影响：对外能否说“1:1”）
2. **字体策略**：继续系统字体栈（跨平台/字体缺失导致不可复现）还是打包 OFL 字体（体积 + 授权）？这决定像素门禁能否稳定。
3. **static/live 双轨**：把 static 假屏退役、统一到 live 单轨（工程量大、风险高），还是给 live 另建 golden（成本低但双维护）？这是架构品味决策。
4. **深色/窄屏是否 v1 交付**：PLAN 锁定“浅色+深色同期交付”，当前仅“结构不崩”。要么投入重采对齐，要么明确 P1 降级并改对外口径。
5. **Add plugin 最终形态**：移除 / 置灰 / 保留并弹“仅内置”政策（代码现状）。PLAN 锁定“移除”，基线与代码都有按钮——必须拍板并同步三处。
6. **字标收口**：`docs/wordmark.md`/README 已定方案 A，PLAN §13 仍列为待拍板；确认后删除 PLAN 待拍板项，并决定是否需要包装/安装包品牌资产。
7. **里程碑口径**：以 PLAN §11 的 M0–M7（含硬化发布）还是 STATUS 的 M0–M10 作为“v1 完成”的定义？建议重编一套并冻结。
8. **降级清单的对外披露**：voice input/auto review 仅保存偏好、Windows 无 PTY、无第三方插件——是否在 About/插件页显式展示“不可用原因”（当前仅在 More 弹层有部分文案）。
9. **真实模型 E2E 的凭据/CI 策略**：是否允许 CI 出网、secret 如何托管、证据如何脱敏留存。没有这个决策，`真实 E2E PASS` 永远是不可复核的断言。
10. **目标平台与基线冻结**：Windows 原生标题栏 vs DSH Web 客户端差异（UI-PARITY-AUDIT.md:53）；1:1 像素以哪个平台/窗口框为准，需要产品与设计确认。

---

## 5. 附录：本次执行记录（可复核）

环境：`go1.27.3 linux/amd64`、`CGO_ENABLED=0`、无 `.git`、宿主 fontconfig 143 字体（无 Segoe UI/PingFang）、`xvfb-run` 存在但未启动 GUI。

| 命令 | 结果 |
| --- | --- |
| `go test ./... -count=1` | **PASS**（18 包全绿；shell 18.3s、tools 5.8s、mcp 0.8s；sonic 回退 `encoding/json` 警告） |
| `go vet ./...`；`gofmt -l ./cmd ./internal` | **PASS**（0 行输出） |
| `go vet -tags e2e ./...` | PASS（e2e 代码可编译，未联网运行） |
| `sh scripts/check-nocgo.sh` | **PASS (zero cgo)**：linux/amd64、linux/arm64、darwin/arm64、windows/amd64 |
| `ARTIFACTS=/tmp/pixel-a go test ./internal/shell -run TestScreensBaseline -v` | PASS；masked `home 0.86 / session 6.27 / plugins 2.80 / settings 2.49 / automation 0.82%`；raw `2.13/8.29/4.25/2.99/2.55%`；两次运行数字一致 |
| `ARTIFACTS=/tmp/pixel-dark go test ./internal/shell -run TestScreensDarkNarrow -v` | PASS；dark `9.26/23.15/3.14/5.03%`，narrow `11.14/31.47%`；automation 无基线 |
| `go test ./internal/app -run TestProductionToolsInvocable -v` | PASS（注册表断言 ≥40 工具） |
| 计数 | `func Test` 共 542（shell 165）；`ui.NewTester` 调用点 138；`t.Run` 70；无 `func Benchmark`；无 `.github` CI 配置 |
| 文档矛盾探针 | `grep 'M0 进行中\|尚未开始编码'` 命中 STATUS.md:9、README.md:41；`grep 'Add plugin'` 命中 plugins.go:202 与 PLAN.md:1002/1013 |

**副作用声明**：按任务许可运行 `TestScreensBaseline` / 全量 `go test`，会重新生成 `artifacts/screens/`、`artifacts/diff/`、`artifacts/live/`、`artifacts/screens-dark|screens-narrow|diff-dark|diff-narrow/` 的 PNG（仅测试产物，未改源码）。后续 CI 应按 TST-06 用 `ARTIFACTS=/tmp` 隔离。

**未执行**：真模型 e2e（无凭据/避免出网）、GUI 启动冒烟（避免残留窗口进程）、i18n 全量人工走查（human 项）。

---

### 结论（给下一轮 Agent 的 3 句话）

1. 先把 **live 视图像素门禁 + PLAN 阈值实现** 补上，否则所有“1:1”结论都不可证伪。
2. 再把 **STATUS 单一真源 + 审计文档回写** 做完，否则自进化循环会继承错误现状。
3. 最后才继续压静态像素/补交互；**S1 未闭环前，不应把 M0–M10 写成“完成”**。
