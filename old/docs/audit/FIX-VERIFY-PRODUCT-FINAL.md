# 产品面最终验证（Round 4 · ZeoKeel）

> 验证者：product-auditor · 日期：2026-11-06 · 基线：**HEAD `20e98d4` 干净快照**（`git archive 20e98d4 | tar -x -C /tmp/zkf/snapshot`，`go build` 生成 `/tmp/zkf/zeokeel`）
> 方法：干净快照构建 + 真跑 GUI（`xvfb-run` 1600×1000 + 隔离 `ZEOKEEL_DATA_DIR` + `unshare -n` + 精确 PID）+ 快照内定向测试 + 代码级证据 + SQLite 断言
> 证据：`.cache/audit/product-final/logs/`（测试输出）、`.cache/audit/product-final/shots/`（22 张截图）；关联：`docs/audit/PRESETS-USER-GUIDE.md`（已定稿）、`docs/audit/FIX-VERIFY-PRESETS-UI.md`
> 限制：未用真实模型（成本/环境限制）；macOS/Windows 真机未验证（仅 Linux Xvfb）；FP-16 实际问答回合与工具/审批回合未真跑（以 runtime/shell 测试 + 代码为证据）。

## 0. 结论

**Round 3 的 6 项产品能力在干净快照上均可判定 CONFIRMED（presets/FP-16/updater GUI/hotkey/automation rollback/UX-14），无 P0/P1 阻断**；用户旅程除 1 处**回归**外完整：
- **P2 回归（建议 P1 优先级修复）**：Round3 per-session agent factory 让 `Controller.HasAgent()` 恒为 true（`agentFactory != nil`），导致首启 setup 卡的「尚未配置模型 / 配置模型」行与会话空态的「添加模型」提示**不再出现**（`internal/shell/home.go:105`、`session_live.go:355` 均用 `!HasAgent()`）。发送失败时仍有明确 toast 兜底，故非阻断，但首启引导的模型步骤丢失（PD-01 修复回归）。
- P3 打磨项：hotkey 校验错误提示保留英文内层文本；自动化失败原因/导入命令为英文技术文本；About 干净构建显示 `0.1.0-dev`（发布构建需注入版本/commit）。
- 发布清单（非产品缺陷）：本次快照无更新 feed（更新页正确显示“仅手动”）；签名/安装包/自动更新仍属既定延期项。

## 1. 能力矩阵（clean snapshot @20e98d4）

| 项 | 判定 | 证据 |
| --- | --- | --- |
| **presets 全链路**（设置页/覆盖内置/新增/Default/坏文件 fail-closed/Reload/示例/会话 picker/持久化/回退/窄窗） | **CONFIRMED** | 截图 `02-presets.png`、`14-picker-final.png`、`15-toast-placement.png`；`internal/presets` 7/7、`internal/session` preset_api 4/4、`internal/shell` preset 测试全绿；`FIX-VERIFY-PRESETS-UI.md` 矩阵（含 `默认` 本地化、`覆盖内置` badge、`上一份可用列表`、picker persona/prompt 摘要） |
| **FP-16 ask_user 阻塞 + 问题卡** | **CONFIRMED（代码+测试）** | runtime：`TestAskUserBlocksWithinTurn`、`TestQuestionGateTimeoutDegradesToPending`、`TestQuestionGateCancelStopsTurn` PASS；shell：`TestQuestionCardAnswerFlow`、`TestQuestionCardOptionButtonsAnswer`、`TestQuestionCardEmptyAnswerIsRefused` PASS（日志 `logs/runtime-question-tests.txt`、`logs/shell-question-tests.txt`）；未做真实模型直播回合（限制） |
| **updater GUI** | **CONFIRMED** | 截图 `03-updates.png`、`10-updates-check.png`：当前版本/更新通道（仅手动）/检查按钮；点击后状态行「此版本未配置更新源，因此仅支持手动更新。」；测试 `TestUpdatesCheckRunsAndShowsResult`、`TestUpdatesCheckErrors` PASS + app `TestCheckForUpdateWiring` PASS；CLI `zeokeel update` 由平台组另行 e2e |
| **global hotkey** | **CONFIRMED（带 P3）** | 截图 `04-desktop.png`、`11-hotkey-invalid.png`：桌面页有全局快捷键输入 + Linux 平台说明（仅 Windows 生效、值保存供后续）；非法值内联报错且不落库（`settings` 无 `ui.global_hotkey`）；测试 `TestDesktopGlobalHotkeyRow`、app `TestGlobalHotkeySettingWiring`/`TestGlobalHotkeyUnsupportedPlatformDegrades` PASS。P3：错误行「快捷键无效：globalhotkey: invalid hotkey: … needs at least one modifier and a key」英文内层未本地化 |
| **automation 回滚 + 历史/失败可见** | **CONFIRMED** | 截图 `13-automation-history.png`、`16-automation-runs-expanded.png`：前台运行提示、任务行（上次/下次/状态）、`历史` 展开显示 completed（绿）与 failed（红）+ 失败原因；app `TestRunAutomationTaskRollsBackEmptySessionOnSubmitFailure`、`TestRunAutomationTaskRecordsFailure`、`TestRunAutomationTaskDeliversToOriginSession`、`TestRunAutomationTaskCreatesSessionWhenOriginMissing` PASS。P3：失败原因直接显示英文 `no agent configured` |
| **UX-14 toast 不遮挡** | **CONFIRMED** | 截图 `15-toast-placement.png`（toast 在顶部条带，composer/header 均不遮挡）；测试 `TestToastDoesNotOverlapComposer`、`TestSettingsToastSitsInTopBand`、`TestToastDoesNotInterceptPopupPointer` PASS |
| **DSH 导入入口** | **CONFIRMED（命令式入口）** | 截图 `12-import-command-dialog.png`：设置 → 数据与隐私 → 从 DSH 导入 → 弹窗给出 `zeokeel import --dry-run --json` / `zeokeel import` + 复制按钮 + `--from` 说明；不是应用内执行（README 表述“设置页 UI 入口仍待做”已过时，建议同步为“命令入口”） |
| **删除回归（PTC/语音）** | **CONFIRMED** | 插件页仅 3 个实验项（代理团队/自动授权审核/开发者工具，均标 `未实现`），无语音输入；模式/预设菜单无 PTC（`retired_features_absence_test.go`、`removed_preset_test.go` PASS） |
| **数据与隐私/导出/重置** | **CONFIRMED（页面级）** | 截图 `05-data-privacy.png`：数据目录、打开、API key 本地说明、从 DSH 导入、导出全部数据（zip 不含 key）、重置设置、删除全部会话（含确认弹窗，测试覆盖） |
| **关于/版本** | **PARTIAL** | 截图 `06-about.png`：页面正常；但干净快照构建显示 `0.1.0-dev`（无 ldflags），发布构建需按 README 注入 version/commit/buildTime，否则用户看到开发版本号 |

## 2. 用户旅程终检（断点/文案记录）

| 步骤 | 结果 | 备注 |
| --- | --- | --- |
| ① 首启 Home setup 卡 | **断点（P2 回归）** | 干净 data dir（无 model/workspace）实际只显示「创建工作区」行；「尚未配置模型 / 配置模型」行不出现。根因：`HasAgent()` 在 Round3 后等价于“有 agent 或 **有 agentFactory**”，而 app 启动即安装 per-session factory（`internal/app/app.go:319 installSessionAgentFactory`；`controller.go:3285-3288`），故 `home.go:105 needModel := !a.Ctrl.HasAgent()` 恒 false。建议改用「是否已配置可用模型/provider key」判定（如 `Ctrl.DefaultModelID() != ""` 或新增 `Ctrl.HasConfiguredModel()`），并补一条 **production-wiring**（带 factory、无 key）fixture 的测试（现有 `TestHomeSetupCardGuidesFirstRun` 用无 factory 的 controller，因此未发现）。 |
| ② 创建会话 | 通过 | 「新建会话」正常；空态无断点。同一根因：`session_live.go:355` 的空态“未配置模型/添加模型”提示也被跳过；发送失败 toast 仍在（PD-01 兜底） |
| ③ 配置模型 | 通过 | Settings > 模型 → 添加模型（provider 预设/测试连接，前轮已复验；快照代码未变） |
| ④ 工具/审批回合 | 未真跑（限制） | 以 runtime/shell 既有测试与 Round2/3 验证为证据；建议发布前真机/真实模型 1 个短回合冒烟 |
| ⑤ 预设切换 | 通过 | picker 列表/摘要/选中持久化/回退/默认预设均 CONFIRMED（§1 presets） |
| ⑥ 问题卡回答 | 通过（代码+测试） | FP-16 阻塞语义与卡片交互测试 PASS；未直播 |
| ⑦ 自动化历史 | 通过 | `历史` 展开失败原因可见（截图 16） |
| ⑧ 更新检查 | 通过 | 无 feed → 明确“仅手动更新”而非报错（截图 10） |
| ⑨ DSH 导入入口 | 可用（命令式） | 弹窗命令+复制（截图 12）；如需应用内一键导入属后续增强 |
| ⑩ 关于/数据与隐私 | 通过（版本号见 P3） | 见 §1 |

## 3. 残余问题（报 lead；本轮不改代码）

**P2（建议按 P1 排期修复，唯一回归）**
1. 首启 setup 卡与会话空态的「模型」引导因 `HasAgent()` 语义变化而消失（§2 ①/②）。影响：新用户建完 workspace 后看不到“还差模型”的显式 CTA；可恢复（发送时 toast「尚未配置 Agent…」）。修复 S。
   - 附带：`HasAgent()` 自身命名/语义已不准确（有 factory≠有可用模型），建议改名或新增 `HasReadyAgent()/HasConfiguredModel()` 并在所有 UI 判定处使用。
   - 回归测试：用 app 真实 wiring（factory 已安装且无 key）构造 shell App，断言 setup 卡出现「配置模型」、会话空态出现「添加模型」。

**P3（打磨，不阻塞）**
2. hotkey 校验错误内层英文（`globalhotkey: invalid hotkey: …`）未本地化；建议错误分类或仅显示首句中文提示。
3. 自动化失败原因直接显示英文技术文本（`no agent configured`）；建议复用 `session_errors` 分类或加“查看详情”。
4. 关于页/更新页在无 ldflags 构建显示 `0.1.0-dev`；发布流程需注入版本（README 已有 `-ldflags` 说明）。
5. README/文档口径：DSH 导入“设置页入口”现为命令弹窗；`docs/BUGS.md`/README 相关表述建议同步（lead 决定）。
6. 预设页 `覆盖内置` 与 `用户文件` badge 语义已在最终 UI 区分；用户指南已同步（`PRESETS-USER-GUIDE.md` §2）。

**发布清单（非缺陷）**：更新 feed 未配置（更新页行为正确）；代码签名/三平台安装包/自动更新服务端仍属 DEFERRED（见 FIX-REPORT）；macOS/Windows 真机未验证。

## 4. 建议给 README / FIX-REPORT 的引用文案

**README（功能段落后加一句）**
> **预设（Preset / Persona）**：内置 `standard`/`minimal`/`creator`，可在 Settings → 代理预设 管理与按会话切换；用户预设为 `<data dir>/presets/<id>.json`（工具白名单/persona/额外提示词/默认模型；覆盖内置只需写差异字段）。用法与示例见 [docs/audit/PRESETS-USER-GUIDE.md](docs/audit/PRESETS-USER-GUIDE.md)。

**FIX-REPORT（验证小节）**
> **产品面最终验证（Round 4，HEAD `20e98d4` 干净快照）**：presets/FP-16/updater GUI/hotkey/automation 回滚/UX-14 均 CONFIRMED；用户旅程 10 步终检，发现 1 个 P2 回归（首启 setup 卡「配置模型」引导因 `HasAgent()` 与 per-session factory 语义变化而消失）+ 若干 P3 文案/版本注入项。证据与矩阵见 [docs/audit/FIX-VERIFY-PRODUCT-FINAL.md](docs/audit/FIX-VERIFY-PRODUCT-FINAL.md)（截图 `.cache/audit/product-final/shots/`）；预设用法见 [docs/audit/PRESETS-USER-GUIDE.md](docs/audit/PRESETS-USER-GUIDE.md)。

## 5. 证据索引

- 测试日志：`.cache/audit/product-final/logs/` — `snapshot-unit-tests.txt`（presets/updater/globalhotkey）、`shell-feature-tests.txt`（updates/hotkey/toast/presets）、`shell-question-tests.txt`（FP-16 卡片）、`app-feature-tests.txt`（automation 回滚/投递/失败、hotkey、updater）、`runtime-question-tests.txt`（ask_user 阻塞/超时/取消）
- 截图：`.cache/audit/product-final/shots/` — `01-home`、`02-presets`、`03/10-updates`、`04/11-desktop+hotkey`、`05-data-privacy`、`06-about`、`07-plugins`、`12-import-command`、`13/16-automation`、`14-picker`、`15-toast`、`20-fresh-setup-card`（P2 回归证据）、`21/22-setup-model-cta`
- 用户文档：`docs/audit/PRESETS-USER-GUIDE.md`（124 行，已按最终 UI 定稿）
