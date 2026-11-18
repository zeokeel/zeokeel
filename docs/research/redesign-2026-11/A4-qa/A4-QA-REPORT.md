# A4 交互 QA 报告（2026-11-18，interaction-audit → QA 角色）

> **角色**：交互 QA 负责人（实现收尾阶段的验证）。本轮只写 `.cache/` 与 `docs/research/redesign-2026-11/A4-qa/`，
> **未改任何产品代码/产品测试**。改动中的红测试只记录、不修（5 位实现者正在改同一棵树）。
> **配套**：[A4-interaction-detail-audit.md](A4-interaction-detail-audit.md)（审计报告）· [A4-findings.tsv](A4-findings.tsv)（22 条发现）·
> [interaction-drive.txt](interaction-drive.txt)（QA 场景）· [check.sh](check.sh)（一键核对）。

---

## 0. 一页结论

1. **可重跑的交互 QA 套件已就绪**：**31 个用例**，跑一遍 **≈20 秒**（`go run` + 真 desktop assembly headless，机制与 `internal/devtools/ui-drive` 相同：assembly + uiroot + 脚本化 OpenAI 兼容流）。基线（HEAD `e9a0d6b`）：**17 PASS / 10 EXPECTED-FAIL / 0 FAIL**（27 例版本）；当前（2026-11-18 01:2x）：**19 PASS / 6 PASS(fixed) / 6 EXPECTED-FAIL / 0 FAIL**。
   **当前快照（01:25 复跑）**：31 例 → **19 PASS / 4 PASS(fixed) / 8 EXPECTED-FAIL / 0 FAIL**；其中 **`esc-chrome-tip-deny|stop` 在 01:10—01:25 之间由 PASS(fixed) 退回 EXPECTED-FAIL**——这是本轮最重要的回归信号（见 §2.1e）。
   **已转正 4 项**：`esc-chrome-tip-deny`、`esc-chrome-tip-stop`、`esc-modal-tip-close`（A4-I-01 两类根因）、`new-session-double-click`（A4-I-04）、`drop-element-zone`（A4-I-03）、`files-tree-return-affordance`（A4-I-10，面包屑「工作区根目录」已落地）。
2. **两条 QA 定稿已写进场景文件（"仍需拍板"= 0）**：① 禁用发送的三类旁注（只显示一条、12/18 tertiary、工具条右侧发送左边；未配置→「先配置模型」可点、审批中→「处理审批后可发送」、空草稿→不显示；禁用控件不挂 Tooltip）；② 文件页面包屑根节点「工作区根目录」+ `Label("返回工作区根目录")`，点击/Enter 回树且不关页签。对应用例：`send-note-no-model`、`send-note-approval`、`send-note-idle-empty`、`files-tree-return-affordance`。
3. **`A4-I-01` 的口径已按运行时证据收窄**（重要）：**不是"所有提示都吞第一次 Esc"**。composer（屏幕级 Esc 在焦点祖先链上）三例 PASS；失效的是**窗口级兜底**（侧栏/顶带聚焦时 Esc 拒绝/停止）与 **Modal 层**（设置内带提示控件上 Esc 关闭）。修法是**把 Esc 注册到聚焦内容的祖先链上**，不是删提示——详见审计报告 §2 A4-I-01 与 §4 风险 1。
4. **`A4-I-02` 的口径也已收窄**：**只有 `uikit.ConfirmDanger`（18 个调用点）丢焦点**（Esc 与 Enter 都丢）；设置面与命令面板实测会归还（3 例 PASS）。原因：`ConfirmDanger` 用"面板内按钮的 OnClick 清 open"关闭，没走 mygo overlay 的归还路径。
5. **`check.sh` 已按 Lead 要求分组**：**LOCKED**（六类行为必须有保护网——Esc / 焦点归还 / 双击新建 / 拖入 / 禁用原因 / 树选中，01:02 实测 **6/6 绿**）与 **WIP**（其余表面，允许红到收尾：01:02 为 `uikit`/`session`/`settings`/`uimodtest` 四个包，属文案与结构改造中间态）。构建失败时脚本以退出码 2 直接报 `BUILD FAILED`，不把"树在改"误报成回归。
6. **ui-drive 步骤与脚本都做过端到端自检**（在稳定树 `e9a0d6b` 的临时 worktree 上）：五段全跑通、结论行与退出码符合预期；`a4qa-*` 12 步在基线 4 PASS/8 FAIL，在改动中 8 PASS/4 FAIL。
7. **收尾动作**：`bash docs/research/redesign-2026-11/A4-qa/check.sh --with-drive` —— 期望 `结论：GREEN`；若仍 `GREEN-LOCKED`，说明只剩 EXPECTED-FAIL/WIP，逐条看 §2.1 与 §2.1b 的清单。

---

## 1. 怎么跑

```bash
# 一键（构建 + 交互相关帧测试 + QA 套件）——收尾验收用
bash docs/research/redesign-2026-11/A4-qa/check.sh

# 另跑 ui-drive 的交互步骤子集（palette/settings/stream/queue/approval/overlay/panel/window）
bash docs/research/redesign-2026-11/A4-qa/check.sh --with-drive

# 只跑 QA 套件（19s），可过滤
go run ./docs/research/redesign-2026-11/A4-qa/testdata/a4qa \
  --scenario docs/research/redesign-2026-11/A4-qa/interaction-drive.txt \
  --out .cache/a4-qa/inflight [--only esc-|--list]

# 单独看某条用例（示例：焦点归还）
go run .../a4qa --scenario .../interaction-drive.txt --only focus-return --out .cache/a4-qa/probe
```

- **结构（5 段）**：①构建（`go build ./...` + `go build ./internal/devtools/ui-drive`）②**LOCKED**（六类行为必须有保护网：Esc / 焦点归还 / 双击新建 / 拖入 / 禁用原因 / 树选中）③**WIP**（其余表面，允许红到收尾，仅观测）④QA 套件 ⑤可选 ui-drive（`a4qa-` 步骤 + 交互子集）。
- **退出码**：`check.sh` 0 = LOCKED 全绿且套件 0 FAIL（WIP/EXPECTED-FAIL 仍有则给 `GREEN-LOCKED`）；1 = LOCKED/套件/drive 有回归；2 = 构建失败（树在改动中，后续阶段跳过）。
- **耗时**：`check.sh`（无 drive）约 75s；`--with-drive` 约 220s（含两次 ui-drive 启动与流式等待）。
- **判定词**：`PASS` 必须通过 · `FAIL` 回归 · `EXPECTED-FAIL` A4 已知缺失、实现中（不计失败）· `PASS(fixed)` 曾经的缺失已修好（这一列就是收尾看板）。
- **产物**：`.cache/a4qa-check/`（`build.log`、`frame-*.log`、`suite.log`、`suite/results.json`、`suite/shots/*.png`）。
- **ui-drive 原生步骤（已按 Lead 授权落地）**：`internal/devtools/ui-drive/steps_a4qa.go`，12 个 `a4qa-*` 步骤，沿用 `d.step(...)` 注册方式、零新依赖：
  `a4qa-esc-composer-stop`、`a4qa-esc-chrome-tip-deny`、`a4qa-esc-modal-tip-close`、`a4qa-focus-return-dialog-esc`、`a4qa-focus-return-dialog-enter`、`a4qa-tab-order`、`a4qa-hover-vs-selected`、`a4qa-palette-reaches-all-commands`、`a4qa-new-session-double-click`、`a4qa-disabled-send-reason`、`a4qa-drop-zone`、`a4qa-files-tree-select`、`a4qa-files-tree-return`。
  跑法：`go run ./internal/devtools/ui-drive --only a4qa- --out .cache/a4-qa/drive`（`check.sh --with-drive` 已自动带上）。
  注册点在 `main.go` 的 `run()` 首行（`d.runA4QASteps()`）；已过 `go build ./internal/devtools/ui-drive` 与 `go vet`，零新依赖（只用标准库 + 既有 contracts/ui 包）。
- **为什么仍保留自带运行器**：声明式场景（`interaction-drive.txt`）可以一次改文案/断言、跑完 27 例只花 19s；ui-drive 步骤走它的报告体系，适合并入总驱动。两者断言同一批行为，互为对照（本轮已用它们互证：`a4qa-esc-chrome-tip-deny` 与套件 `esc-chrome-tip-*` 结论一致）。

---

## 2. 当前状态

### 2.1 QA 套件：31 例（基线 27 例 → 定稿后新增 4 例）

> 时间戳：当前一列为 **2026-11-18 01:2x**（树可编译）。改动中一列取 00:55 的 27 例快照。
> **01:05 与 01:10 复查时树曾两度不可编译**（`uikit/ask.go`、`home/composer.go`、`session/view.go` 的中间态），
> 此时 `check.sh` 以退出码 2 报 `BUILD FAILED`——那是"树在改"，不是回归。

| 用例 | 表面 | 基线 | 当前 | 说明 |
|---|---|---|---|---|
| esc-stop-composer | composer | PASS | PASS | |
| esc-composer-hover-stop | composer | PASS | PASS | |
| esc-composer-focus-stop | composer | PASS | PASS | |
| esc-chrome-tip-deny | shell | EXPECTED-FAIL | **EXPECTED-FAIL（回归，见 §2.1e）** | A4-I-01 窗口级兜底 |
| esc-chrome-tip-stop | shell | EXPECTED-FAIL | **EXPECTED-FAIL（回归，见 §2.1e）** | 同上 |
| esc-modal-tip-close | shell | EXPECTED-FAIL | **PASS(fixed)** | A4-I-01 Modal 层 |
| esc-overlay-settings / -palette / -palette-over-settings | shell | PASS | PASS | |
| esc-approval / esc-approval-idle | tools | PASS | PASS | |
| focus-return-dialog-esc | shell | EXPECTED-FAIL | **EXPECTED-FAIL** | A4-I-02（焦点 `""`） |
| focus-return-dialog-enter | shell | EXPECTED-FAIL | **EXPECTED-FAIL** | A4-I-02 |
| focus-return-settings | shell | PASS | PASS | |
| palette-escape-restores-focus / palette-command-restores-focus | palette | PASS | PASS | |
| hover-vs-selected | sidebar | PASS | PASS | |
| tab-order-shell / tab-order-composer | shell | PASS | PASS | |
| palette-all-commands | palette | PASS | PASS | 17 条命令全可达 |
| drop-element-zone | composer | EXPECTED-FAIL | **PASS(fixed)** | A4-I-03 |
| new-session-double-click | sidebar | EXPECTED-FAIL | **PASS(fixed)** | A4-I-04 |
| disabled-send-state | composer | PASS | PASS | 发送禁用态仍在 |
| **send-note-no-model**（定稿新增） | composer | — | **EXPECTED-FAIL** | A4-I-05：「先配置模型」未实现 |
| **send-note-approval**（定稿新增） | composer | — | **EXPECTED-FAIL** | A4-I-05：「处理审批后可发送」未实现 |
| **send-note-idle-empty**（定稿新增） | composer | — | PASS | 空草稿无旁注（且另两条不出现） |
| **files-tree-keyboard-select**（定稿新增） | rightpanel | — | **EXPECTED-FAIL** | A4-I-11：↑↓ 选中生效，但**选中不出预览** |
| **files-tree-enter-opens**（定稿新增） | rightpanel | — | PASS | ↑↓ 选中后 Enter 打开页签（回归保护） |
| **files-tree-select-follows-pointer** | rightpanel | EXPECTED-FAIL | **EXPECTED-FAIL** | A4-I-11：点击打开后该行不是当前项 |
| **files-tree-return-affordance** | rightpanel | EXPECTED-FAIL | **PASS(fixed)** | A4-I-10：「返回工作区根目录」已落地 |
| baseline-composer-visible | composer | PASS | PASS | |

合计（当前，01:25）：**19 PASS / 4 PASS(fixed) / 8 EXPECTED-FAIL / 0 FAIL**。
剩余 8 条 = 4 个实现任务：**A4-I-01 窗口兜底回归（×2，需回退 `OnWindowShortcut`）**、A4-I-02（×2）、A4-I-05（×2）、A4-I-11（×2）。**没有待拍板项。**

### 2.1b ui-drive `a4qa-` 步骤快照（基线 vs 改动中，同一套 12 步）

| 步骤 | 基线 `e9a0d6b` | 改动中（00:58） | 对应发现 |
|---|---|---|---|
| a4qa-esc-composer-stop | PASS | PASS | — |
| a4qa-esc-chrome-tip-deny | FAIL（pending=1） | **PASS** | A4-I-01 窗口级兜底 |
| a4qa-esc-modal-tip-close | FAIL | **PASS** | A4-I-01 Modal 层 |
| a4qa-focus-return-dialog-esc | FAIL（焦点 ""） | FAIL | A4-I-02 |
| a4qa-focus-return-dialog-enter | FAIL | FAIL | A4-I-02 |
| a4qa-tab-order | PASS | PASS | — |
| a4qa-hover-vs-selected | PASS（bias 29 vs 15） | PASS（23 vs 3） | A4-I-13 观感 |
| a4qa-palette-reaches-all-commands | PASS（17 条） | PASS（17 条） | — |
| a4qa-new-session-double-click | FAIL（1 → 3，两个新会话） | **PASS**（1 → 2） | A4-I-04 |
| a4qa-disabled-send-reason | FAIL（无任何新文案） | FAIL | A4-I-05 |
| a4qa-drop-zone | FAIL（元素未接管） | **PASS** | A4-I-03 |
| a4qa-files-tree-select | FAIL（不是当前项） | FAIL | A4-I-11 |
| a4qa-files-tree-return | FAIL（无返回入口） | FAIL | A4-I-10 |

基线 4 PASS / 8 FAIL → 改动中 8 PASS / 4 FAIL。**剩下 4 条 FAIL 就是收尾前需要实现者收口的清单**（A4-I-02 ×2、A4-I-05、A4-I-10、A4-I-11）。

### 2.1c `check.sh` 端到端自检（在稳定树 `e9a0d6b` 的临时 worktree 上跑通）

| 阶段 | 结果 |
|---|---|
| 1 构建（`go build ./...` + ui-drive） | PASS / PASS |
| 2 LOCKED 六组 | **6/6 绿** |
| 3 WIP 十一包 | 全 ok（基线没有改动中的红） |
| 4 QA 套件 | 27 例：**17 PASS / 10 EXPECTED-FAIL / 0 FAIL**，退出 1（因为 drive 有 FAIL） |
| 5 ui-drive（a4qa- + 子集） | a4qa- 8 FAIL、子集 0 FAIL |
| 结论行 | `结论：RED`（基线本来就该红——4 类缺失 + 2 类 Esc 缺失） |

同一条命令在**改装中的工作树**上（01:02）得到：LOCKED 6/6 绿、WIP 4 包红（uikit/session/settings/uimodtest，文案或结构改造中）、套件与 drive 因树不可编译而以 `BUILD FAILED`（退出码 2）跳过——脚本对"改动中"和"回归"是分开报的。

**稳定性**：`esc-stop-composer` 曾因"停止后运行态异步 settle"误报一次 FAIL；已改成**轮询 3s**（`expect-running false`），Esc 子集连跑 3 次、全套连跑 2 次结果一致（`expect-running true` 也改为等 5s）。

### 2.1d A4-I-11 期望与断言（可直接转给 impl-panels）

**实测现状**（01:2x，`files-tree-*` 三例）：
- ↑/↓ **能**移动树的当前项（`main.go` 行 `AccessInfo.Selected=true`）→ 已有；
- 当前项上 **Enter 会打开文件页签** → 已有（`files-tree-enter-opens` PASS，作回归保护）；
- **选中不显示预览**（选中 `main.go` 后帧里没有它的内容 `QA 预览标记 gamma`）→ **缺**；
- **指针点击打开文件后，该行不是树的当前项**（返回文件页仍 `Selected=false`）→ **缺**。

**期望（定稿）**
1. **键盘**：树获得焦点后 ↑/↓ 改变**当前项**（持久选择 = `BgSelected` + 2 DIP `Accent` 左条 + `LabelPrimary`，与侧栏树一致；`st.filesRow` 同步），**不打开页签**，预览区立即显示该文件内容；Home/End 到首/末行。
2. **指针**：点击文件行 = 打开页签（现状保留）**且**把该行设为当前项；从预览返回文件页后，该行仍带选中态，键盘从它继续。
3. **Enter/双击**：在树上打开当前项（现状 Enter 已满足）。
4. **目录行**：点击/→ 只展开折叠，不打开页签；目录可成为当前项但预览区留空并显示一行「目录」（建议文案），避免"选中了什么都没有"。
5. **零值**：面板首次打开时当前项为空（不预选第一行），↑/↓ 从第一行开始；`README.md` 与 `main.go` 不能因为排序不同而错位断言——用例已改为"第一行 = main.go"。

**断言方式（Go 帧测试，`ui.NewTester`；与 QA 场景 `files-tree-*` 等价）**
```go
openFilesPage(tester)                                  // Cmd+Alt+P 或点「文件」
tabToTree(tester)                                      // Tab 直到 Focused 的树节点（RoleTreeItem）
tester.Key(0, ui.KeyDown); tester.Frame()
if !treeRow("main.go").Selected { t.Fatal("↑↓ 没有移动当前项") }
if !tester.HasText("QA 预览标记 gamma") { t.Fatal("选中后预览没有显示该文件内容") }
tester.Key(0, ui.KeyEnter); tester.Frame()
if _, ok := tester.AccessByRole(ui.RoleTab, "main.go"); !ok { t.Fatal("Enter 没有打开页签") }
tester.Click("文件"); tester.Frame()
if !treeRow("README.md").Selected { t.Fatal("指针点击后该行不是当前项") }
// treeRow(name) = 遍历 tester.AccessAll() 取 Role==ui.RoleTreeItem && Name==name
```
移植位置建议：`internal/ui/rightpanel/files_outline_test.go`（现有 `TestFilesTreeKeyboardSemantics` 旁边），复用 `workspaceHarness` + `setDir/setFile` 的 fake，不需要真 assembly。

> `files-tree-return-affordance` 已 PASS(fixed)：impl-panels 的「返回工作区根目录」面包屑已落地，点击回树且页签保留——本条无需再动。

### 2.1e ⚠ 回归：窗口级 Esc 兜底（01:10 起）——需要 impl-shell 回退

**现象**（01:20 复跑，稳定）：焦点在侧栏/顶带时，`Esc` **既不拒绝审批也不停止流式**；composer 聚焦时一切正常（`esc-approval|esc-approval-idle` PASS）。有提示、无提示都失败 → 与 Tooltip 无关。

**定位**：`internal/uiroot/shortcuts.go:374` 现在用
```go
c.OnWindowShortcut(0, ui.KeyEscape, func() { ... r.escapeFallback() ... })
```
而 mygo 的 `OnWindowShortcut` **只在 modal 内注册**（`ui/actions.go:70-73`：`if … raw.insideModal() …`）。`bindChromeShortcuts` 在 `view(c)` 的根上下文调用，不在 modal 内 → **窗口级 Esc 兜底根本没注册**。旁证：文件顶部还留着 `_ = root` 与 `r.escapeTaken` 的实验残迹，注释写的是"subtree 注册会把 popover 的 Esc 吞掉（QA 抓到的 P0）"。

**这也修正了我早前的建议**：把兜底注册到"根 Row 的 children"确实会让它比 overlay 更深——mygo `routeKey`（`ui/input.go:1333-1357`）先查**焦点祖先链**上的普通 shortcut，**再**查 overlay，所以祖先链注册会抢走所有 popover/菜单的 Esc。impl-shell 实测的 P0 成立，我此前的"上移到祖先链"对**窗口兜底**是错的。

**建议的组合（三者取其二即可让 QA 转绿，且不产生新 P0）**
1. **兜底回退到窗口作用域**：`c.OnShortcut(0, ui.KeyEscape, r.escapeFallback)`（即 01:10 之前的写法），恢复"焦点在哪都能拒绝/停止"；它排在 overlay 之后，popover/菜单/Modal 仍先关。
2. **提示与 Esc 冲突只在这 5 个可聚焦 chrome 控件上消失**（`NoEscSteal`：不挂 Tooltip，保留 `Description` 给读屏）：侧栏「折叠/展开侧边栏」「新建会话」「设置」、顶带「快捷操作」「打开/收起右侧栏」。其余 40+ 图标按钮的提示全部保留。
3. **Modal 内**：保留 Modal 自己的 Esc；「关闭设置」这类紧邻 Esc 语义的图标按钮同样走 `NoEscSteal`（或接受对话框里第一次 Esc 只关提示）。
   （若选 1+2+3，QA 的 `esc-chrome-tip-*`、`esc-modal-tip-close` 都会一次生效；用例已改成**不要求提示一定在场**，两种修法都通过。）

**用例证据**：`esc-chrome-tip-deny`（pending 1→1）、`esc-chrome-tip-stop`（running 仍 true）；drive 步骤 `a4qa-esc-chrome-tip-deny` 同结论；无提示对照 `deny-no-tip`（临时探针）同样失败 → 证明是兜底没注册，而非提示抢键。

### 2.2 帧测试：LOCKED（必须绿）与 WIP（允许红）

**LOCKED（六类行为的保护网，2026-11-18 01:02 运行：6 组全绿）**：`internal/uiroot`（Escape/PaletteEscape/InspectorDrawerEscape/SettingsSurfaceEscape/FileDrop/Shortcuts）、`internal/regression`（EscE2E/WindowShortcuts）、`internal/uikit`（ApprovalDialogEscape/Backdrop、AskDialog、PlanReview、ConfirmDanger、ComposerEnter/Escape）、`internal/ui/session`（Esc/FindBar/ComposerEnter·Queue·Focus·Refocus/Drop/AttachmentPicker）、`internal/ui/approval`（TestEsc）、`internal/ui/rightpanel`（FilesTree/WorkspaceFileRowMenu）。

**WIP（实现中的表面，01:02 运行：4 个包红，允许到收尾）**

| 包 | 结果 | 说明 |
|---|---|---|
| `internal/uikit` | ❌ | `TestSegmentedControlChosenSegmentLook`（分段控件观感改造中） |
| `internal/ui/session` | ❌ | `TestQueueStopKeepsBackendInbox`、`TestQueueRemoveOneAndClearAll`、`TestQueueBackendErrors`（队列文案/结构改造中） |
| `internal/uiroot` | ✅ | Palette / Escape / Inspector / Narrow / Shortcut / Settings / Shell / Rail / Focus 全绿 |
| `internal/ui/approval` | ❌ | 英文文案 / 自定义标签 / 待审批计数 3 例（文案改造中） |
| `internal/ui/interaction` | ❌ | ask/plan 8 例（选项/页脚结构改造中） |
| `internal/ui/rightpanel` | ✅ | 文件树 / 行菜单 / 页签 全绿 |
| `internal/ui/toasts` | ✅ | 全绿 |
| `internal/regression` | ✅ | 窗口级 Esc E2E / 窗口快捷方式 全绿 |
| `internal/ui/uimodtest` | ❌ | `TestPaletteKeyboardPathEndToEnd`、`TestDialogSafetyAndButtonOrder`（E2E 断言随文案/结构变化） |

> 这些红是**改动中的中间态**（与 5 位实现者的文案/结构改造一一对应），按任务要求只记录不修；收尾时 `check.sh` 必须 `结论：GREEN`。
> 另外：**01:05 复查时树再次不可编译**（`uikit/ask.go` 的 `askOtherChoice`、`home/composer.go` 的 `SendTooltip`、`session/view.go` 的 `buildUserMessageMeta` 参数），此时 `check.sh` 以退出码 2 结束并在 LOCKED 之前打印 `BUILD FAILED`——这是"改动中"的正常态，不是回归。

---

## 3. 行为 ↔ 用例 ↔ 现有测试：谁锁住了什么

| 行为 | QA 用例 | 现有自动化（已锁） |
|---|---|---|
| composer 内 Esc 一次停止（含提示在场） | `esc-stop-composer`、`esc-composer-hover-stop`、`esc-composer-focus-stop` | `TestComposerEscapeInterruptsAfterMenus`、`TestEscCancelsRunningTurnMenuFirst` |
| 浮层内 Esc 只关自己（设置/面板/面板浮于设置） | `esc-overlay-settings`、`esc-overlay-palette`、`esc-palette-over-settings` | `TestSettingsSurfaceEscapeClose`、`TestPaletteEscapeClosesOnFirstPress`、`TestPaletteKeepsChosenRowVisibleAfterReopen` |
| 审批 Esc=拒绝、不打断本轮（含空闲跨会话） | `esc-approval`、`esc-approval-idle` | `TestEscDeniesPendingApprovalBeforeInterrupting`、`TestEscFallbackDeniesFromChromeFocus`、`TestEscE2EIdleCrossSessionDenies` |
| 窗口级兜底（侧栏聚焦）**提示在场时** | `esc-chrome-tip-deny`、`esc-chrome-tip-stop` | **缺**（现有兜底测试无提示在场变量） |
| Modal 层 Esc **提示在场时** | `esc-modal-tip-close`（已修） | **缺** |
| 危险确认关闭后焦点归还 | `focus-return-dialog-esc`、`focus-return-dialog-enter` | **缺**（`TestConfirmDangerDefaultsToCancel` 只测 Enter 语义） |
| 设置/命令面板关闭后焦点归还 | `focus-return-settings`、`palette-*-restores-focus` | `TestInspectorDrawerEscapeOnceAndRestoresFocus`（同类，非同一路径） |
| hover 与 selected 可区分 | `hover-vs-selected`（像素：选中比 hover 更浓的品牌 tint） | **缺**（此前只有 token 值，无像素断言） |
| Tab 顺序（侧栏→顶带→内容→composer） | `tab-order-shell`、`tab-order-composer` | **缺**（各控件的 Tab 可达性有测试，整窗顺序没有） |
| 命令面板覆盖全部动作 | `palette-all-commands`（按标题逐条检索） | `TestPalette*` 覆盖过滤/选中/滚动，无"全量可达"断言 |
| 外部拖入由元素级 drop zone 接管（高亮前提） | `drop-element-zone`（已修） | **缺** |
| 双击「新对话/新建会话」只建一个 | `new-session-double-click` | **缺** |
| 禁用发送有可读原因 | `disabled-send-reason` | **缺**（`TestComposer*` 不覆盖禁用解释） |
| 检查器文件页：点行成当前项 / 可返回列表 | `files-tree-select-follows-click`、`files-tree-return-affordance` | `TestFilesTreeKeyboardSemantics`（键盘路径）、`TestFilesTreeLargeDirectoryReachesBeyondCap`；鼠标路径与返回入口**缺** |

---

## 4. 还缺测试的行为（按表面，收尾后建议补）

| 表面 | 缺口 | 建议测试 |
|---|---|---|
| 壳层 | 窗口级 Esc 在"提示浮起"时的行为（当前 guraded 两例） | 把 `esc-chrome-tip-deny/stop` 固化成 `internal/uiroot` 帧测试（真实 approvals fake + 焦点在侧栏图标按钮） |
| 壳层 | 全窗 Tab 顺序（含分隔条是否应可聚焦、右栏 dock 后的顺序） | `tab-order-shell` 的断言搬进 `uiroot` 测试，加一条"右栏打开后顺序" |
| 对话框 | ConfirmDanger 关闭后的焦点（Esc/Enter） | `focus-return-dialog-*` 搬进 `uikit`（`ReturnTo` 落地后） |
| 会话 | 双击新建/创建型动作幂等 | `new-session-double-click` 搬进 `sidebar` 测试 |
| 会话 | 禁用的发送有可见原因 | `disabled-send-reason` 搬进 `session`（文案定稿后） |
| 右栏 | 文件页鼠标路径的选中与返回入口 | 两条 `files-tree-*` 搬进 `rightpanel` |
| 侧栏 | hover/selected 视觉区分（像素） | 把 `hover-vs-selected` 的采样逻辑搬成快照断言（亮/暗各一） |
| 全局 | ReduceMotion 下的动效关闭 | `design/motion.go` 的 `TransitionColors` 在 `ReduceMotion` 下返回负时长：补一条 `uikit` 测试断言"立刻到位" |

---

## 5. 给 Lead 的一页验收清单（按表面）

收尾时逐项打勾；括号里是证据来源（`QA:` = 套件用例，`test:` = 帧测试，`drive:` = ui-drive 步骤）。

**壳层 / 键盘**
- [ ] `Esc` 在 composer 内一次停止，不受提示影响 — QA:`esc-composer-*`（3 例）
- [ ] `Esc` 从侧栏/顶带（提示在场）仍拒绝审批、停止本轮 — QA:`esc-chrome-tip-deny|stop`（**当前红**）
- [ ] `Esc` 在设置内（提示在场）关闭设置 — QA:`esc-modal-tip-close`（**已转正**）+ test:`TestSettingsSurfaceEscapeClose`
- [ ] 面板 / 面板浮于设置 / 设置叠加时的层级 — QA:`esc-overlay-*`、`esc-palette-over-settings`
- [ ] Tab 顺序：侧栏 → 顶带 → 内容 → composer（→ 右栏） — QA:`tab-order-*`
- [ ] 分隔条可聚焦但读屏名本地化 — test:`TestSidebarResizable`（建议补一条无障碍名断言）

**焦点**
- [ ] ConfirmDanger 关闭后焦点回到触发控件（Esc 与 Enter） — QA:`focus-return-dialog-*`（**当前红**）
- [ ] 设置面 / 命令面板关闭后焦点归还 — QA:`focus-return-settings`、`palette-*-restores-focus`（绿）
- [ ] 检查器 drawer/toggle 焦点归还 — test:`TestInspectorDrawerEscapeOnceAndRestoresFocus`

**悬浮 / 选中 / 禁用**
- [ ] hover 有 80ms 过渡、hover≠selected（像素） — QA:`hover-vs-selected` + test:`TestIconButtonHoverFill...`
- [ ] 行内动作 hover/focus 显形、键盘可达、≥24 命中区 — test:`TestIconButtonTargetSizes`、sidebar 行测试
- [ ] 禁用发送的旁注按定稿只显示一条（未配置→「先配置模型」可点；审批中→「处理审批后可发送」；空草稿→不显示；禁用控件不挂 Tooltip） — QA:`send-note-no-model|approval|idle-empty`（前两条**当前红**、第三条绿）

**输入 / 流式 / 长任务**
- [ ] Enter/Shift+Enter/Cmd+Enter；运行中 Enter 排队 — test:`TestComposerEnterSends`、`TestComposerQueueDuringRun`
- [ ] 停止有 `Stopping` 中间态、不重复触发 — test:`TestRunStatusStop`（+ QA 停止 3 例）
- [ ] 跟随滚动 / 上滚暂停 / 回到最新 — test:`TestSessionThreadFollowAndBackToLatest`、`TestChatScrollFollowsStreamingEnd`
- [ ] 审批内联卡无 Default、Esc=拒绝、常显入口 — test:`TestApprovalInline*`、QA:`esc-approval*`

**命令面板 / 菜单 / 对话框**
- [ ] 命令面板覆盖全部动作 — QA:`palette-all-commands`
- [ ] 面板 Home/End/PageUp/PageDown、多词查询（A4-I-07，未实现） — test:`TestPaletteHomeEnd`（建议按 QA 断言补）
- [ ] 菜单键盘（↑↓/Home/End/字母/←→/Enter/Esc）与危险项 — 框架保证 + test:`TestWorkspaceRowMenuOpensAndDeletes`
- [ ] 危险确认取消为 Default、危险非 Default — test:`TestConfirmDangerDefaultsToCancel`

**工具栏 / 右栏 / 拖放**
- [ ] 工具栏单 Tab stop、←/→ 内部移动 — QA:`tab-order-composer`（+ test:`TestComposerToolbarGroups`）
- [ ] 拖入文件有元素级落点（高亮前提） — QA:`drop-element-zone`（**已转正**）
- [ ] 文件页：面包屑「返回工作区根目录」回树且不关页签 — QA:`files-tree-return-affordance`（**已转正**）
- [ ] 文件页：↑↓ 改当前项并出预览、Enter 打开、指针点击后仍是当前项 — QA:`files-tree-keyboard-select|enter-opens|select-follows-pointer`（Enter 绿；预览与指针选中**当前红**，期望见 §2.1d）
- [ ] 页签关闭/多标签/⌘W（未实现） — test:`TestTabs*`（建议补 ⌘W）

**通知 / 全局**
- [ ] toast 右上 2 条、悬停暂停、Tab 可达、Esc 关闭 — test:`TestNativeToastAppearsAndExpires`、toasts 全套
- [ ] 复制反馈统一（图标切换 + 跨屏 toast） — QA 未覆盖反馈语汇，建议补一条像素/图标断言
- [ ] ReduceMotion 全关 — `design/motion.go` + 建议补 `uikit` 断言

**门禁**
- [ ] `bash docs/research/redesign-2026-11/A4-qa/check.sh` 退出码 0
- [ ] 套件 EXPECTED-FAIL 清零（当前 6 条：A4-I-02 ×2、A4-I-05 ×2、A4-I-11 ×2；**没有待拍板项**）
- [ ] `go test ./internal/ui/... ./internal/uiroot ./internal/design ./internal/uikit` 全绿（收尾时）

---

## 6. 注意与限制

1. **文案是场景的一部分**：case 里的界面文案（如「新对话」「停止」「关闭设置」）以 zh 为准；本轮已经踩到一次改名（侧栏「新对话」→「新建会话」）。套件提供 `click-any a|b`、`expect-focus-any`、`tab-to-any` 三种别名写法；**改名请同步 `interaction-drive.txt`**，否则用例会报"找不到控件"（错误信息里带当前 texts，便于定位）。
2. **`disabled-send-reason` 的断言串**用了建议文案「后可发送」。文案定稿后请改这一行（或改成实现里的实际句子），否则会一直 EXPECTED-FAIL。
3. **`files-tree-return-affordance`** 断言"名含「文件列表」的控件"。若最终文案不同（如「返回文件树」），同样改这一行。
4. **不要用 `-count=1` 之外的随机性**：套件内不使用 `time.Sleep` 等拍脑袋等待（流式用"等 Running 状态 + 超时"），基线 5 次重复同一用例结果一致；但 `palette-all-commands` 与流式用例受机器负载影响会有几百毫秒波动，不影响判定。
5. **运行环境**：需要 `/tmp/zeokeel-tmp`（脚本自己 `mkdir -p`）、`GOPATH/GOCACHE` 指向 `.cache/`；`go run` 需要产品可编译（构建失败时 `check.sh` 直接退出码 2）。
6. **产物不进 git**：套件、日志、截图都在 `.cache/`；`docs/.../A4-qa/` 只有场景、脚本、运行器源码与本报告。
7. **本报告的数字是快照**：实现者仍在改；打勾前重跑 `check.sh`。我按 Lead 要求不做最终结论，等「收尾」指令后再跑一遍并给出结论。
8. **基线数字怎么复现**：本报告"基线"一列来自 `git worktree add --detach .cache/a4qa-base HEAD`（HEAD `e9a0d6b`）里跑同一套场景；验完已删除该 worktree。需要再取基线时按同样命令建一个，把 `docs/.../A4-qa/` 拷进去再跑即可（不影响主工作区）。
---

## 7. 收尾结论（**第二轮 2026-11-18 02:55**，`check.sh --with-drive`）

**结论行：`GREEN`（3 条已知限制）**（退出码 0）。必须绿项全过、**0 FAIL、0 EXPECTED-FAIL**；3 条已知限制见 §7.5；6 条旧 ui-drive 步骤待迁移文案断言（不计）。

> 第一轮（02:12）结论是 `GREEN-LOCKED`：19 PASS / 7 PASS(fixed) / 5 EXPECTED-FAIL / 0 FAIL。第二轮把其中 3 条缺口推进到 PASS(fixed)、2 条焦点归还转已知限制、1 条（无模型旁注）转 QA 装配限制，于是 EXPECTED-FAIL 清零。

| 闸门 | 结果 |
|---|---|
| 1 构建（`go build ./...` + ui-drive） | PASS / PASS |
| 2 LOCKED 六类行为（Esc / 焦点归还 / 双击新建 / 拖入 / 禁用原因 / 树选中） | **6 组全绿 / 0 红** |
| 3 WIP 十一包（uikit、session、approval、interaction、settings、toasts、rightpanel、sidebar、plugins、uiroot、uimodtest） | **0 包红** |
| 4 QA 套件（**32 例**） | **20 PASS / 9 PASS(fixed) / 0 EXPECTED-FAIL / 3 KNOWN-LIMIT / 0 FAIL** |
| 5a ui-drive `a4qa-`（15 步） | 13 PASS / 0 FAIL（2 步已知限制，不计） |
| 5b ui-drive 旧交互子集（40 步，非本套件契约） | 34 PASS / 6 FAIL（**文案改造后未迁移**，不计入结论） |

**已转正 9 项**：`esc-chrome-tip-deny`、`esc-chrome-tip-stop`、`esc-modal-tip-close`（A4-I-01 两类根因）、`drop-element-zone`（A4-I-03）、`new-session-double-click`（A4-I-04）、`send-note-approval`（A4-I-05 审批旁注）、`files-tree-keyboard-select`（A4-I-11 选中出预览）、`files-tree-select-follows-pointer`（A4-I-11 指针路径）、`files-tree-return-affordance`（A4-I-10）。

### 7.1 剩余 5 条 EXPECTED-FAIL：一句话原因 + 归属

| 用例 | 一句话原因 | 归属 |
|---|---|---|
| `focus-return-dialog-esc` | 危险确认关闭后 `Focused()==""`：`state.deleteTrigger` 从未被赋值（`sidebar.go:131` 只有清零、没有设置），`onClose` 的 `runtime.Post(trigger.Focus())` 永远不执行 | `internal/ui/sidebar`（sidebar 写集） |
| `focus-return-dialog-enter` | 同上（Enter 走取消按钮的默认路径，关闭后同样无焦点主） | 同上 |
| `send-note-no-model` | 「先配置模型」旁注未实现（composer 只有 hint 行与禁用的发送） | `internal/uikit/composer.go`（impl-tools）+ `internal/ui/session/view.go`（impl-session） |
| `send-note-approval` | 「处理审批后可发送」旁注未实现 | 同上 |
| `files-tree-keyboard-select` | ↑↓ 已能移动当前项，但**选中不出预览**（帧里没有该文件内容） | `internal/ui/rightpanel/view.go`（impl-panels） |

### 7.2 `focus-return-dialog-*` 的根因（**不是** headless 焦点组限制）

三处叠加，证据齐：

1. **触发句柄从未绑定**：`internal/ui/sidebar/sidebar.go:131` 声明 `deleteTrigger *ui.Handle`，全文只有 `:2471` 把它清零；`sessionMenuHandle()`/`groupMenuHandle()`（`:259-282`）是死代码，没有任何调用点。`buildDangerConfirm(..., state.deleteTrigger, ...)`（`:2446`）因此永远收到 `nil`，`:2470` 的归还分支不执行。
2. **`uikit.IconButtonOptions` 没有 Bind/Handle 字段**，所以 ⋯ 按钮即使想绑也绑不上：需要在 `internal/uikit/iconbutton.go` 的 options 里加 `Handle *ui.Handle` 并在构建后 `.Bind(o.Handle)`（一行），或在调用点 `more = more.Bind(state.sessionMenuHandle(session.ID))`。
3. **更根本：隐藏控件不能被聚焦**。⋯ 在 `!revealed` 时被 `Opacity(0).IgnoreInput(true)`（`:2316-2320`）；探针实测：危险确认关闭后 `会话操作：QA 会话` **根本不在 accessibility 树里**（同一帧里行本身 `Role=List focusable=true`）。`focusReturn`（`:133-137` 注释"The ⋯ is a hover-only control, and a hidden control cannot take the focus"）正是为这一刻设计的，但它只有"到焦点就清空"（`:2323-2326`），**从来没有被设置过**。

最小修法（3 处，约 6 行）：
```go
// 1) 绑定：构建 ⋯ 时
if h := state.sessionMenuHandle(session.ID); h != nil { more = more.Bind(h) }
// 2) 菜单的「删除」项：打开确认前
state.deleteTrigger = state.sessionMenuHandle(session.ID)
state.focusReturn = session.ID   // 让这一行在对话框关闭的那一帧保持 revealed
// 3) revealed 计算加上
revealed := current || row.Hovered() || row.FocusWithin() || state.focusReturn == session.ID
```
（若想更省事：把焦点归还目标改成**行本身**——行始终可聚焦，1 行改动，但偏离"归还触发器"的规格。）

### 7.3 旧 ui-drive 交互子集的 6 条 FAIL：**测试未迁移**（不是回归）

文案改造后这些步骤还在断言旧文案，全部有当前文案可对：

| 步骤 | 断言找的旧文案 | 现在的实现 |
|---|---|---|
| `queue-popover-cancel` | 「取消排队」 | 逐条「取消第 N 条排队消息」+「发送队列中的消息」+「清空全部」（`uikit/runstatus.go:512-540`） |
| `overlay-approval` | 「本会话内始终允许」「允许」 | 「拒绝」「允许一次」「更多」（范围菜单，「仅此一次/按所选范围允许」） |
| `approval-safe-default` | 点「拒绝」的 label rect + 「本会话内始终允许」 | 拒绝其实可点（我用探针 `click-deny` 验证 PASS）；Enter 不决议这条本身已通过 |
| `overlay-ask` | 点选项即应答 | 现在是**单选 + 提交**（Esc 关闭仍正常：新步骤 `a4qa-ask-esc` PASS，Ask 返回"用户取消"） |
| `overlay-plan` | 「点击批准」静态提示 | 提示文案已改；按钮序正常（取消计划 x=699 < 批准 x=882） |
| `overlay-plan-feedback` | 「Enter = 继续规划」 | 反馈提示文案已改 |

迁移位置：`internal/devtools/ui-drive/steps_overlays.go`（及队列步骤）；建议照 `steps_a4qa.go` 的"文案容忍"写法（`clickAny`/`findAny`）。

### 7.4 分类合计

- **回归：0**（LOCKED 全绿、套件 0 FAIL；A4-I-01 的窗口级兜底在 01:45 已经修好并保持）。
- **框架限制：0**（`focus-return-dialog-*` 已定位为产品侧未接线 + 隐藏控件不可聚焦，不是 mygo 焦点组/headless 限制）。
- **测试未迁移：6**（§7.3，旧 ui-drive 步骤）。
- **产品缺口：3**（A4-I-02 焦点归还、A4-I-05 禁用旁注、A4-I-11 选中预览），对应 5 条 EXPECTED-FAIL。

快照：`.cache/ui-conformance-final/` 已生成，`p2-artifact-history.png` 出图正常（1512×944，文件页空态 + 任务卡 + 审批等待 toast 计数 ×2）。


### 7.6 第二轮收尾（2026-11-18 02:50）：三条缺口验证 + 分类

Lead 本轮修的三条，我逐条验证：

| 声称的修复 | 我的验证 | 结果 |
|---|---|---|
| ① 待审批时 toast 栈上移（`toastanchor.go`，同时修好 uimodtest `TestDialogSafetyAndButtonOrder`） | 新增几何断言 `toast-clears-approval-actions`：运行中 + 待审批 → toast 底边严格在「允许一次」「拒绝」之上 | **PASS**（截图 `drive-a4qa/shots/a4qa-send-note-approval.png`：旁注在发送左侧，toast 不再压按钮） |
| ② 禁用发送旁注（「处理审批后可发送」） | `send-note-approval` 套件 + `a4qa-send-note-approval` 驱动步骤 | **PASS(fixed)** / PASS |
| ③ 文件树键盘选中出预览 | 修掉我自己用例里的两个前提错误（行序不是 README 优先；`.git` 是目录）后，`files-tree-keyboard-select` 断言「预览跟着当前项走」 | **PASS(fixed)**（驱动步骤 `a4qa-files-tree-select` PASS，选中 `.git` 时预览显示「目录」） |

> ③ 的教训值得记一笔：**"第一行是哪个文件" 不是契约**（工作区顺序由目录扫描决定），断言要写成"当前项 → 预览内容"的映射，而不是钉死某一行。驱动步骤的分类启发式（点号开头 = 文件）也已修正为"点号开头 = 隐藏目录"。

`focus-return-dialog-*` 按 Lead 要求转**已知限制**（§7.5 A），不计回归；`send-note-no-model` 转**QA 装配限制**（§7.5 B，产品分支由 `firstrun_test.go:176-216` 锁定）。

### 7.5 已知限制（3 条，不计回归；第二轮收尾）

#### A. `focus-return-dialog-esc` / `focus-return-dialog-enter` —— 全量装配里焦点请求不落地

- **接线是完整的**（可逐行复核）：`internal/ui/sidebar/sidebar.go:2208` 在菜单「删除」被选中时记 `state.deleteTrigger`；`:2314` `.Bind(state.sessionMenuHandle(session.ID))`；`:2315-2322` `returnFocus` → `revealed = true` 且跳过 `Opacity(0).IgnoreInput(true)`；`:2325-2331` 同一帧 `more.Focus(ui.FocusOptions{PreventScroll: true})`；`:2452-2461` `deleteWasOpen` 记账（覆盖 Modal 自己改 open 标志的那一帧）；`:2497-2501` 关闭时写 `focusReturn`。
- **包级严格断言已过**（我复跑）：`internal/ui/sidebar/focus_return_test.go:17 TestSessionDeleteConfirmReturnsFocusToTheRowMenu`；`internal/uikit/confirm_focus_test.go:39/70/148`（Enter / Esc / 回菜单按钮）。`go test ./internal/ui/sidebar -run TestSessionDeleteConfirmReturnsFocusToTheRowMenu` → PASS；`./internal/uikit -run TestConfirmDangerReturnsFocus` → PASS。
- **全量装配里的实测差异**：关闭后 `Focused()==""`，且 `会话操作：QA 会话` **不在 accessibility 树里**（探针 `dialog-focus2`：两次 `dump-node` 都是 "absent"，同一帧里行本身 `Role=List focusable=true`）。原因在 `:2337`：**发出焦点请求的同一帧**就把 `focusReturn` 清掉了；请求若被 Modal 的关闭时序覆盖，下一帧 ⋯ 立刻回到 `Opacity(0).IgnoreInput(true)` —— "可聚焦的控件"只存在一帧，焦点自然无处可落。
- **建议（若要真正收口）**：把清空条件从"已发请求"改成"焦点真的到了"——`if state.focusReturn == session.ID && content.FocusWithin() { state.focusReturn = "" }`（文件里原有此模式，:2335-2338 附近），或直接把归还目标改成**行本身**（行始终可聚焦，1 行改动）。
- **影响**：键盘用户按 Esc/Enter 关闭危险确认后没有焦点主，需要一次 Tab 才能继续；不阻断鼠标用户。

#### B. `send-note-no-model` —— QA 装配不可构造（产品分支已由包级测试锁定）

- 产品分支存在且正确：`internal/ui/session/view.go:3388-3391`（`!modelReady && !running && hasContent` → 「先配置模型」→ 打开模型页）。
- 但 headless 装配总会从模型目录解析出默认模型（composer 显示「gpt-4o-mini（默认模型，尚未配置连接）」），`chat.Config().Model` 非空 → `modelReady = true`；"完全没有模型" 的状态在本装配里**不可构造**。
- 行为由包级测试锁定并通过：`internal/ui/session/firstrun_test.go:176-216`（断言「先配置模型」出现、点击打开模型设置、Enter 不发送）。
- **影响**：无产品缺口；本套件的「空草稿无旁注」「审批待处理旁注」两条已绿。

#### C. 旧 ui-drive 交互子集 6 条（见 §7.3）—— 测试未迁移

文案改造后仍断言旧文案；已降为观测项，不计入结论。

---

## 8. 逐控件清点 + 动态面补驱动（2026-11-18 03:50）

用户要求「每个按钮、每个点击、每个悬浮都要检查，尤其动态部分」。本节是**清点**（不是抽查）+ 五类动态面的驱动补全。

### 8.1 清点交付

| 文件 | 内容 |
|---|---|
| [A4-control-inventory.md](A4-control-inventory.md) | 按屏分组的总量、覆盖率、五类动态面结果、待覆盖清单与下一轮建议 |
| [A4-control-inventory.tsv](A4-control-inventory.tsv) | **400 行**逐构造点明细：屏 / 类型 / 控件 / 位置(file:line) / 点击 / hover / focus ring / disabled / Esc / Tab 可达 / 结果 |

**总量**：400 个交互构造点 = 358 个可点控件 + 40 个浮层容器（Modal/Overlay/Popover）。已有驱动或 Tab 走查证据 **144**（40%），待覆盖 **214**，驱动中判定的产品 FAIL **0**（第二批补驱动后从 135 升到 144：MCP STDIO/鉴权、工具失败行、LSP、记忆、数据与隐私、三屏 hover 像素）。

**判读**：覆盖率的分子只算「有驱动步骤断言过该调用点」；侧栏/壳层数字偏低是**静态标签提取**的局限（Label 在闭包里），Tab 走查已经逐个见过它们（见文档 §1 注）。

### 8.2 新增驱动（13 步，全部 PASS）

| 步骤 | 覆盖 |
|---|---|
| `a4qa-tabwalk-session` / `-landing` / `-automation` / `-insights` / `-capabilities` / `-settings` | 六屏 Tab 序：焦点每步移动、可达控件数达阈值、循环闭合（真实 Tab 走查，输出完整的焦点名序列） |
| `a4qa-stream-partial` | 流式中：部分文本可见、`停止` 在、输入后 **Enter 变排队**（不打断本轮）、Esc 收尾 |
| `a4qa-tool-row-toggle` | 工具行完成态；**点击行**展开详情、再点收起 |
| `a4qa-mcp-add-http` | 添加连接器：表单齐全 → 空 URL 测试有反馈 → 不可达 URL 测试给出失败原因 → 添加入列表；**重复 URL 被拒（「该 URL 已存在」）** |
| `a4qa-mcp-edit-toggle-remove` | 卡片菜单→编辑→保存、启停开关（已停用可见）、移除确认（取消→仍在；确认→消失） |
| `a4qa-skill-create-edit-view-delete` | 新建技能（名称/描述/内容）→ 列表 → 查看内容显示正文 → 行菜单（复制路径/禁用/编辑/删除）→ 禁用标记 → 删除需确认 |
| `a4qa-settings-inputs` | 模型页系统提示词输入+保存、行为页运行限额输入、通知开关翻转并复位 |
| `a4qa-settings-restore-defaults` | 「恢复默认」→ 确认框 → 取消后快捷键值不变 |

`ui-drive --only a4qa-` → **34 PASS / 3 WARN / 0 FAIL**（WARN = §7.5 A 的两条已知限制 + 工具失败行没有失败标记这条状态语义问题）。

### 8.3 本轮发现（按 owner，**未改产品代码**）

| # | 现象 | 证据 | 归属 |
|---|---|---|---|
| 1 | **设置页输入框的可访问名后缀不统一**：连接器表单是「URL 输入 / 名称 输入」，设置页是裸「代理地址 / 系统提示词」——同一个 `inputLabel` 辅助函数在两处行为不同，键盘/读屏与自动化都更难找 | `a4qa-settings-inputs` 里「代理地址 输入」找不到，只能退回裸标签 | impl-settings（+ impl-tools 的 `uikit` 表单辅助） |
| 2 | **脚本化工具调用后本轮 25s 仍未结束**（工具行渲染与展开正常，`bash` 类工具轮次能结束） | `a4qa-tool-row-toggle` 的 note：`回合在 25s 内未完成（running=true）` | 待确认：可能是 `team_task_create` 空参数路径，或驱动脚本模式的第二轮请求未触发（impl-session / 测试基建） |
| 3 | 设置 Tab 序只有 9–11 个焦点站（同一页的多个输入/开关不在 Tab 链上，需要用 ←/→ 或点击进入） | `a4qa-tabwalk-settings` note 的焦点名序列 | 观察项，非缺陷（表单内的焦点组策略），但**键盘用户切页后要按一次 Tab 才能到第一个字段** |

### 8.4 未覆盖的部分（诚实边界）

225 个构造点仍无驱动断言，主要是：闭包里的变量标签（59）、共享组合（`uikit` 的 42 个内部件，被使用方重复计入）、设置剩余页（LSP 新增服务器、记忆增删、归档恢复、数据与隐私的清除确认）、MCP 的 STDIO 与鉴权分支、工具三态的**失败**分支（需要 `server.go` 增加一个「工具返回错误」脚本模式——不在我的写集）、逐控件 hover 像素证据（机制已在 `a4qa-hover-vs-selected` 跑通，尚未铺开）。

### 8.5 第二批补驱动（2026-11-18 04:20）：MCP 分支 / 工具失败态 / 设置剩余页 / hover 像素

| 步骤 | 断言 | 结果 |
|---|---|---|
| `a4qa-mcp-stdio-form` | 传输切 STDIO → 出现「STDIO 进程/命令/参数/环境变量」且 **URL 消失**；空命令提交被拒（对话框留在原地）；填 `echo` 后保存出现在列表；跑完自动移除 | PASS |
| `a4qa-mcp-auth-branches` | 认证下拉：**Bearer 令牌** → 出现令牌字段+说明；**OAuth 2.1** → 高级选项展开后有「作用域/客户端 ID（可选）/授权服务器（可选）」；关闭不保存 | PASS |
| `a4qa-tool-fail-state` | 在**新建的空会话**里跑 `bash` 必失败命令（`ls /nonexistent-qa-path`）→ 展开后可见 stderr（`ls: cannot access …`、`[exit code: 2]`）；请求数 2（轮次正常结束） | **WARN**（stderr 可展开=是；行上无失败标记，见下表 #1） |
| `a4qa-settings-lsp-add-remove` | LSP：空表单点添加 → 「名称和命令不能为空」；填 `qa-lsp`/`gopls` 后成为列表行（用行节点断言，不被表单同名文本骗过）；服务器操作→移除后行消失 | PASS |
| `a4qa-settings-memory-add-delete` | 记忆：添加「QA 记忆条目」→ 列表出现；记忆操作→删除记忆→**取消后仍在**；再删→确认后消失 | PASS |
| `a4qa-settings-data-clear-guard` | 数据与隐私：「清除全部会话」→ 危险确认（含数量与「无法撤销」）+ 取消 → 会话数不变 | PASS |
| `a4qa-hover-session` | 悬浮像素：`附件` Δ=10、`打开右侧栏` Δ=18（`发送` 在空草稿时 Δ=0，属禁用态正常） | PASS |
| `a4qa-hover-sidebar` | `新建会话` Δ=10、`设置` Δ=10 | PASS |
| `a4qa-hover-capabilities` | `新建技能` Δ=10、`刷新技能` Δ=10 | PASS |

#### 本轮新发现（按 owner）

| # | 现象 | 证据 | 归属 |
|---|---|---|---|
| 1 | **工具以非零退出码结束，行上仍是成功态**：`bash` 返回 `[exit code: 2]` + stderr，但折叠行只有「过程 · 1 个工具调用」，工具行图标是 ✓（`icons.Check`，`uikit/toolrow.go:189-193` 只在 `StatusError` 时用 `AlertTriangle`）——说明后端把「命令非零退出」报成了 success | `a4qa-tool-fail-state` 截图 `shots/a4qa-tool-fail-expanded.png`；状态映射在 `internal/ui/session/agentsurface.go:1688-1696`（后台任务路径**是**按 exit code 判失败的，工具行路径没有） | impl-tools（工具行状态）+ 需确认 rig 侧 `call.Status` |
| 2 | **行内动作的可访问名重复**：LSP 每行的菜单都叫「服务器操作」、记忆每行都叫「记忆操作」，读屏与自动化无法区分是哪一行（我的步骤必须靠"离目标行最近的同名控件"才点得对） | `a4qa-settings-lsp-add-remove` / `-memory-add-delete` 需要 `clickNear`（新增辅助）才能定位 | impl-settings |
| 3 | `team_task_create {}` 后**本轮不再发第二次请求**（见 §8.5.1） | `a4qa-tool-row-toggle` 诊断：`本轮请求数=1 用时=25.0s`；对照 bash 失败路径 `本轮请求数=2` | impl-session / rig 工具结果回填 |

#### 8.5.1 `team_task_create {}` 最小复现（给 Lead 判归属）

- **脚本服务器**：`internal/devtools/ui-drive/server.go`（`httptest.NewServer`，OpenAI 兼容 SSE），模式 `scriptToolCallThenAnswer`。
- **第 1 轮响应体**（原样）：
  ```json
  {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"team_task_create","arguments":"{}"}}]},"index":0,"finish_reason":"tool_calls"}]}
  ```
- **工具与参数**：`team_task_create`，`arguments = "{}"`（空对象）。
- **观测**：该轮对脚本服务器的请求数 **= 1**（`Served()` 计数），25s 后会话仍 `Running=true`；工具行本身渲染出来了（「团队任务」），展开正常。
- **对照**：同一 harness 换 `bash` + `{"command":"ls /nonexistent-qa-path"}` → 请求数 **= 2**，本轮正常结束并给出失败输出。
- **读法**：工具调用**有**界面记录，但工具结果没有回填给模型（或工具本身未返回），于是 agent 循环停在第一次请求。不是 UI 层问题。
