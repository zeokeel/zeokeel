# 移动端形态修复 —— 独立验收报告

- 验收对象：`418fd30 fix(web): one theme source, and a shell that folds at 1024`（P0-2 移动端部分）
- 用例：390×844 八屏可用性 / 输入框 / 抽屉 / 设置页 / 768-1024-1440 / 深色 / 1440 回归 / 键盘
- 验收方式：真网关 + 真 Chromium（Playwright，`/data/tavo/tools/e2e/chromium-1243`），全部数字来自 `page.evaluate` 实测，不看实现者说法
- **结论：FAIL** —— 8 项清单中 2 项失败（第 3 项"浏览器返回"、第 5 项"1024 分隔条拖拽"），另有 2 处应修的残留缺陷
- **⚠ 本节是轮 3 对 `418fd30` 的结论。轮 4 复验（新冻结构建 `7a9d51e`、新实例 :9904）见 §8：4 个修复点 + 上轮 8 项回归全部 PASS，无清单内失败项；另发现 2 个清单外新缺陷 F5（视口往返抽屉复活）、F6（英文 UI 390 输入框脚注溢出）与注意点 O1/O2/O4/O5。**
- **⚠⚠ 最新：轮 5（第三轮复验，构建 `54454ca`、实例 :9906）见 §9 —— 结论 FAIL**：F5 / F6 / O4 / O5 四个修复点 PASS，但 O2 未落地（空按分隔条仍写 `zk.sidebar.width`，代码与打包产物均无比较分支）；上两轮关键项回归全 PASS。新发现 N1（语言闪动 ~1s）、N2（favicon 404）。
- **✅ 最新：轮 6（第四轮复验，构建 `8976472`，实例 :9940）见 §10 —— 结论 PASS**：O2 / N1 / N2 / `ui.notifications` / `ui.sessions.showArchived` 五项全部生效，回归全过。新发现 N3（通知开关初始显示与行为不一致）、N4（提供的 :9926 实例只回应 /health，未在服务）、N5（会话页偶发卡在“正在连接网关”，未复现）。

## 0. 环境与被测二进制（冻结证据）

| 项 | 实测值 |
|---|---|
| BASE | `http://127.0.0.1:9888`（`data-mobile/gateway.json` port=9888, token=`NOpuNDyR…` (前 8 位), startedAt 2026-11-24T14:41:48Z） |
| 二进制 | `.cache/bin/zeokeel-mobile` 44 354 976 B, mtime 11-24 22:41 |
| 模型桩 | `fakemodel` :9898（`chat.model=acceptance-tool`，脚本化 web_fetch → 固定文本） |
| 前端产物 | `/assets/index-Ck43_pX6.js` 2 419 225 B，含 `open-sidebar` / `settings-page-picker` / `zk-main` 标记 → 线上包确实是修复后的构建 |
| 断言锚点 | `#zk-main` computed `min-width` = **0px**（390 / 1024 / 1025 / 1440 均为 0px） |
| 数据 | `target-mobile.json`：doneSession `sess-1795531328041064334`、pendingSession `sess-1795531332484301480`（**种子被我的用例部分消耗，见 §5**） |

## 1. 清单表

| # | 检查 | 结果 | 证据（数字 + 截图） |
|---|---|---|---|
| 1 | 390×844 八屏基本可用（`/`、完成态、待审批态、`/inbox`、`/settings/model`、`/settings/appearance`、`/team`、`/insights`）：无横向滚动、`#zk-main.clientWidth ≥ 380`、正文不出现一字一行 | **PASS** | 8/8 屏 `scrollWidth == clientWidth == 390`，`#zk-main.clientWidth = 390`，`#zk-main` x=0 宽=390；正文每行字符数（可见文本节点字符/行盒）10.4 / 11.4 / 10.3 / 10.2 / 9.7 / 7.0 / 5.9 / 3.4 —— 无「一字一行」的正文段落（`/insights` 的 3.4 来自表格短单元格，其排版问题另见 F3）；`audit-mobile-01…08`（`out/mob1-screens.json`） |
| 2 | 输入框可用：`.zk-composer textarea` clientWidth ≥ 300、能输入中文并发出、待审批会话 notice + 禁用 | **PASS** | textarea `clientWidth=348`（right=369 ≤ 390）；键盘输入后 value == "移动端输入一条中文消息，确认发送"；连发 5 条 Enter 全部落库（bubble 8→10、10→12、12→14、14→15、15→15 但重载后 5 条全在，见 `out/mob2e/mob2g`）；回答出现（`.ant-bubble` +2，`The fetch returned ok…`）；待审批：`.zk-composer__notice` = "本会话有一条审批等待处理：处理后才能继续发送。"、`textarea.disabled=true`、`composer-send.disabled=true`；`audit-mobile-09/10/11/37` |
| 3 | 抽屉：汉堡 → 抽屉 260–360、有 scrim、scrim/Esc 关闭；抽屉内选会话 → 导航且自动关闭；**再按浏览器返回，布局仍正常** | **FAIL** | 打开：`.ant-drawer-content-wrapper` **280px**，`.ant-drawer-mask` 覆盖 390px `rgba(0,0,0,0.45)` z=1000，抽屉面 `rgb(255,255,255)`；点 scrim (370,420) → `open=false, mask=null, sidebar w=0`；Esc → 同上；抽屉内点会话 → `/c/…480` 且 `mask=null`（自动关闭 ✓）；抽屉内点导航项「收件箱」→ `/inbox`、`open=false`、`mask=null`（`audit-mobile-40`）✓；**浏览器 Back → 回到原会话同时抽屉重新弹出：open=true, drawerW=280, mask 覆盖 390, `elementFromPoint(195,500)` 命中 `.ant-drawer`**；Esc 可再关。截图 `audit-mobile-12/13/14/15/16`、复现 `audit-mobile-34/35/36`（`out/mob3-drawer.json`、`out/mob3c-back.json`） |
| 4 | 设置页 <900：导航列隐藏、顶部 `data-testid=settings-page-picker`（含搜索）、切换有效、控件不被裁切 | **PASS** | 390：`.zk-settings__nav` **display:none / 0×0**，picker 160×32 @x16（"模型"），搜索框紧随；9 个设置页全扫（模型/行为与限额/外观/通知/网络与搜索/LSP/归档/数据与隐私/关于）：控件 7–22 个，**被裁 0 个**，最大 right=374 ≤ 390，`hOverflow=0`；picker 选"外观" → `/settings/appearance`，picker 文案同步、单列 grid `390px`；边界：**899 → picker 显示、nav 0px、单列 `899px`**；**900 → picker 不渲染、nav 220px、两列 `220px 680px`**；`audit-mobile-17/18/19/33-899/33-900/33-768/38`（`out/mob4-settings.json`、`out/mob4c-switch.json`、`out/mob9-boundary.json`） |
| 5 | 平板 768 无横向滚动且侧栏为抽屉；1024 与 1440 恢复分栏、无汉堡；**拖拽分隔条仍可用** | **FAIL** | 768：`data-narrow=true`、汉堡按钮在、`.zk-sidebar` 不在 DOM、`overflow 0`、main 768；点汉堡后抽屉 280px、mask 覆盖 768、抽屉内 `.zk-sidebar` 280（`audit-mobile-39`）；1024：`narrow=false`、无汉堡、`.zk-sidebar` 可见 232px、main 760、overflow 0；1440：sidebar 264、main 1176、无汉堡；**拖拽**（真指针抓 `.ant-splitter-bar-dragger`，+60/+80px）：1440 **264 → 344（Δ80）** ✓、1025 Δ60 ✓、1100 Δ60 ✓、**1024 232 → 232（Δ0）✗**，但 `localStorage['zk.sidebar.width']` 被写成 344/324（Splitter 收了拖拽却没生效）；`audit-mobile-20/21/23-1024/23-1440`（`out/mob5-viewports.json`、`out/mob5b-drag.json`、`out/mob9-boundary.json`） |
| 6 | 深色 + 窄屏：390 下抽屉与内容都为暗色（≥4 个选择器 computed 背景） | **PASS** | 设置"深色"并**保存**后：`data-theme=dark`、`color-scheme=dark`；实测 computed background：`body` `rgb(17,17,24)`（亮度 18）、`#zk-main` `rgb(17,17,24)`、`.zk-composer .ant-sender` `rgb(27,27,38)`、`.ant-drawer-section` `rgb(31,31,31)`（31）、`.ant-drawer-body` / `.zk-sidebar` `rgb(11,11,16)`（11）、会话行文字 `rgba(255,255,255,0.85)`、mask `rgba(0,0,0,0.45)`；`audit-mobile-24/25/26`（`out/mob6b-dark.json`） |
| 7 | 无回归：1440 下 `/`、`/c/:id`、`/settings/model` 与 `shots/final-*.png` 对比 | **PASS** | 像素差（2880×1800，同尺寸逐像素）：`/settings/model` vs `final-settings-model.png`：mean 0.74/255，>8 差异 **0.3%**、>32 **0.2%**（band 区 0.0%）→ 基本逐像素一致；`/c/:id` vs `final-thread-idle.png`：>8 **8.3%**，差异集中在 band 27.8%（标题/toast 文案不同，修复前截图带两条 toast）与会话区（数据内容不同：侧栏会话标题、正文轮次都与修复前不是同一批——差异属内容而非布局）；几何无变化：sidebar 264 / `#zk-main` 1176 / composer 1176 宽 textarea 1102 / `scrollWidth=clientWidth=1440`；`audit-mobile-30/31/32` |
| 8 | 键盘：Tab 到汉堡（可读名）→ Enter 开抽屉 → Esc 关闭且焦点回汉堡 | **PASS** | Tab#1 = 跳转链接"跳到主内容"，**Tab#2 = `data-testid=open-sidebar`，name="打开侧栏"，40×40 @(8,4)**；Enter → `open=true` drawerW 280；Esc → `open=false`，`document.activeElement` = BUTTON[data-testid=open-sidebar]（aria-label 打开侧栏，可见）；`audit-mobile-27/28/29`（`out/mob8-kbd.json`） |

## 2. 失败项详情

### F1（清单 3）抽屉在浏览器"返回"后自己重新弹出 —— P0 级观感/可用性缺陷

- **现象**：390 下打开抽屉 → 在抽屉里选另一个会话 → 抽屉按预期自动关闭、URL 变为 `/c/<新会话>`；此时按浏览器返回键，页面回到原会话，**抽屉同时重新弹出**（280px + 全屏 scrim），把内容盖住，用户必须再按一次 Esc/点 scrim 才能看到页面。
- **复现**（`mob3c-back.mjs`，稳定复现）：
  1. 390×844 打开 `/c/sess-1795531328041064334`；
  2. 点 `[data-testid="open-sidebar"]`，等 `.ant-drawer-open` 且 wrapper.x==0；
  3. 点抽屉内 `li.ant-conversations-item`（"把 README 里的安装步骤…"）→ URL `/c/sess-1795531332484301480`，`mask=null`；
  4. `page.goBack()` → URL 回 `/c/sess-1795531328041064334`，实测 `open=true, drawerW=280, maskCoverage=390, elementFromPoint(195,500)∈.ant-drawer`。
  期望：返回后抽屉保持关闭，只切换页面。
- **根因**（读代码确认，`web/src/shell/app-shell.tsx`）：
  ```ts
  67: const [drawerFor, setDrawerFor] = useState<string | null>(null);
  68: const drawerOpen = narrow && drawerFor === location.pathname;   // ← 只用 pathname 比对
  69: const closeDrawer = () => setDrawerFor(null);
  108: <TopBand onOpenSidebar={narrow ? () => setDrawerFor(location.pathname) : undefined} />
  ```
  `drawerFor` 记录的是"在哪个 pathname 上打开过"，导航离开时**从不清理**，只是被 `drawerFor === location.pathname` 暂时掩盖；一旦 Back 回到那个 pathname，条件重新为真 → 抽屉复活（前进/后退多次同样会来回弹）。
- **建议修法**（二选一，推荐前者，仍不加 effect）：
  1. 用 `location.key` 而不是 `pathname` 作为"这一次打开"的标识 —— 历史遍历会产生新的 key：
     ```ts
     const location = useLocation();
     const [drawerKey, setDrawerKey] = useState<string | null>(null);
     const drawerOpen = narrow && drawerKey === location.key;
     // TopBand onOpenSidebar={() => setDrawerKey(location.key)}
     ```
  2. 或在所有导航入口显式关抽屉（`Sidebar onSelect`、侧栏菜单 `SidebarNav` 的 navigate、面包屑/tab…）—— 注意只改 `onSelect` 不够，顶部导航也会换 pathname，会留下同样的陷阱。

### F2（清单 5）1024px 刚好落在两套断点之间的缝隙：分隔条拖不动，却把宽度写进 localStorage

- **现象**：1024×900 下侧栏是可拖的分栏（符合"1024 恢复分栏"），但拖动 `.ant-splitter-bar-dragger` 60–80px 后侧栏宽度**一点没变**（232 → 232，Δ0）；同时 `localStorage['zk.sidebar.width']` 被写成 344/324。同样拖拽在 1025（Δ60）、1100（Δ60）、1440（Δ80）都正常。
- **复现**：`mob5b-drag.mjs` / `mob9-boundary.mjs`：viewport 1024 → 抓 `.ant-splitter-bar-dragger`（x=229, w=6）→ mouse down、move +60（分 6 步）、up → 读 `.zk-shell__sidebar-panel` 宽度与 `getComputedStyle().width`：拖前 `232px`，拖后 `232px`，`stored="324"`。
- **根因**：
  - `web/src/design/global.css:1639-1653`：
    ```css
    @media (max-width: 1024px) {
      .zk-shell__sidebar-panel { flex: 0 0 232px !important; width: 232px !important;
                                 max-width: 232px !important; min-width: 232px !important; }
    }
    ```
    `!important` 把面板钉死，Splitter 的 inline 宽度失效；
  - 而 JS 断点是 `useMediaQuery('(max-width: 1023px)')`（`web/src/shell/app-shell.tsx:65`）→ **正好 1024** 时"CSS 认为窄、JS 认为宽"，两侧规则打架；Splitter 仍然接收拖拽并在 `onResizeEnd` 落盘（`app-shell.tsx:158-163`），于是产生"拖不动但宽度被记错"。
  - 附带：`global.css:1655-1674` 的 `@media (max-width: 900px) { .zk-shell__sidebar-panel { 200px !important } }` 已经不可达（<1024 是抽屉、不渲染 panel），是死规则。
- **建议修法**：把 `1639` 的媒体查询改成 `(max-width: 1023px)` 与 JS 对齐；不要在面板上用 `width !important`（用 `flex-basis`/`max-width` 即可）；删除 `1655-1674` 的 900px 死块（含重复的 `.zk-topband__menu`，见 F4）。验收口径建议改成"1024 拖拽前后宽度差 > 0"。

## 3. 其它应修缺陷（不属于本次声称范围，但本次实测到）

### F3 `/insights` 在 390 下双列表格被压成 163px/列，表头一字一行
- 实测：`.zk-insights__tables` 是 2 列 grid（`342px` → 每列 **163px**），日期列仅 **63px**，"2026-11-18" 折成两行（单元格高 61px，而单行是 39px）；"按模型"表头"调用"列 **30px** → 竖排"调/用"（一字一行）。整页仍 `scrollWidth==clientWidth==390`（所以未触发清单 1 的三条断言），但属于可见的排版缺陷。
- 位置/建议：`web/src/design/global.css:1514` `.zk-insights__tables { grid-template-columns: minmax(0,1fr) minmax(0,1fr) }` 没有窄屏回退 → 增加
  `@media (max-width: 720px) { .zk-insights__tables { grid-template-columns: minmax(0, 1fr); } }`，并给日期单元格 `white-space: nowrap`（或把日期格式改成 `11-18`）。

### F4 `global.css` 里 `.zk-topband__menu` 被整块复制进了 `@media (max-width: 900px)`
- `global.css:181-206`（正确的基样式）与 `global.css:1675-1700`（`@media (max-width: 900px)` 内的同名同规则副本，且缩进错乱）。功能上无影响（规则相同），但会造成"改一处漏一处"的维护陷阱；建议删除 1675-1700 这段副本（它本来就是 F2 建议删除的 900px 块的一部分）。

## 4. 截图索引（`/data/zeokeel/.cache/ui-audit/shots/`）

| 文件 | 内容 |
|---|---|
| `audit-mobile-01-home.png` … `08-insights.png` | 390×844 八屏：`/`、完成态会话、待审批态会话、`/inbox`、`/settings/model`、`/settings/appearance`、`/team`、`/insights` |
| `audit-mobile-09-composer-typed.png` | 390 输入框内已键入中文（发送键由禁用变可用） |
| `audit-mobile-10-composer-answer.png` | 发送后出现回答（bubble 2→4） |
| `audit-mobile-11-pending-notice.png` | 390 待审批态：notice + textarea 禁用 + 审批卡（卡 342 宽 right=366、允许按钮 right=349 均在视口内） |
| `audit-mobile-12-drawer-open.png` | 抽屉打开（280px + scrim 0.45） |
| `audit-mobile-13-drawer-scrim-closed.png` | 点 scrim 后关闭 |
| `audit-mobile-14-drawer-esc-closed.png` | Esc 关闭 |
| `audit-mobile-15-drawer-picked.png` | 抽屉内选会话后：已导航且抽屉关闭 |
| `audit-mobile-16-after-back.png`、`audit-mobile-35-after-back-reopen.png` | **F1：浏览器返回后抽屉重新弹出** |
| `audit-mobile-34-after-pick.png`、`audit-mobile-36-after-back-esc.png` | F1 复现的相邻状态（选后关闭 / 返回后 Esc 再关） |
| `audit-mobile-17-settings-picker.png`、`-18-settings-picker-open.png`、`-19-settings-appearance-after-switch.png` | 设置页顶部选择器（含搜索）、下拉、切换后 |
| `audit-mobile-20-{768,1024,1440}-home.png`、`-21-*-thread.png` | 768 / 1024 / 1440 首页与会话 |
| `audit-mobile-23-1024-drag.png`、`-23-1440-drag.png` | **F2：1024 拖拽后宽度不变；1440 拖拽后 264→344** |
| `audit-mobile-24-dark-settings-390.png`、`-25-dark-thread-390.png`、`-26-dark-drawer-390.png` | 深色 390：设置页 / 会话 / 抽屉 |
| `audit-mobile-27-kbd-hamburger-focus.png`、`-28-kbd-drawer-open.png`、`-29-kbd-esc-focus.png` | 键盘：汉堡聚焦 / Enter 打开 / Esc 关闭且焦点回汉堡 |
| `audit-mobile-30-1440-home.png`、`-31-1440-thread.png`、`-32-1440-settings-model.png` | 1440 回归对比用（与 `final-*.png` 同尺寸 2880×1800） |
| `audit-mobile-33-settings-{768,899,900}.png` | 设置页断点：768/899 选择器、900 恢复 220px 导航列 |
| `audit-mobile-37-clean-send-answer.png` | 完成态会话干净发送后的回答 |
| `audit-mobile-39-768-drawer.png` | 768 下汉堡打开的抽屉（280px + mask 768） |
| `audit-mobile-40-drawer-nav.png` | 抽屉内点导航项「收件箱」后：已到 `/inbox` 且抽屉关闭 |
| `audit-mobile-38-settings-about-390.png` | 设置"关于"页 390 全宽表单 |

## 5. 被测数据的副作用（复查须知）

1. `doneSession sess-1795531328041064334`：验收输入框时新增 **8 条用户消息**及其回答（"移动端输入一条…"、"A/B 路径…"、"连通性测试第 1–5 条"）——发送/中文输入/回答渲染的证据也来自这里。
2. 种子里的**待审批审批被我在第 2 项用例中"允许一次"消耗掉了**：那条 `web_fetch` 审批卡当时内联渲染在完成态会话页里（"来自会话：把 README 里的安装步骤补充完整"），点"允许一次"后 `pendingSession sess-1795531332484301480` 不再是待审批态，且该会话再次发消息也不再要求审批，**无法用同一条审批恢复**。
3. 为拿到真实待审批态，我新建了 `sess-1795531601874576357` 并留了一条待审批（截图 `audit-mobile-11` 即此时刻：notice 在、textarea 禁用）。**注意约 14 分钟后该审批被网关超时清掉**（复查时已无 notice）；要再看待审批态，用"新会话 + 发一条会调 web_fetch 的消息"即时复现。
4. 主题设置最终恢复为 `跟随系统`（保存后实测 `data-theme=light`，body 白）。

## 6. 复现脚本（都在 `/data/zeokeel/.cache/ui-audit/`，输出在 `out/`）

| 脚本 | 覆盖 |
|---|---|
| `mob1-screens.mjs` | 清单 1（八屏几何 + 每行字符数）→ `out/mob1-screens.json` |
| `mob2-composer.mjs` / `mob2e-clean.mjs` / `mob2g-repeat.mjs` / `mob2d-freshpending.mjs` | 清单 2（尺寸/中文输入/发送/回答/待审批 notice+禁用，Enter 连发 5 次） |
| `mob3-drawer.mjs` / `mob3c-back.mjs` | 清单 3（打开/宽度/scrim/Esc/选会话/返回）→ `out/mob3-drawer.json` `out/mob3c-back.json` |
| `mob4c-switch.mjs` / `mob4d-sweep.mjs` / `mob9-boundary.mjs` | 清单 4（选择器切换、九个设置页扫描、899/900 边界） |
| `mob5-viewports.mjs` / `mob5b-drag.mjs` / `mob9-boundary.mjs` | 清单 5（768/1024/1440 + 分隔条拖拽 + 1025/1100 对照） |
| `mob6b-dark.mjs` | 清单 6（深色 + 390，含"必须保存后生效"的正确操作路径） |
| `mob7-regress.mjs` | 清单 7（1440 三屏重拍，供与 `final-*.png` 逐像素比对） |
| `mob8-kbd.mjs` | 清单 8（Tab/Enter/Esc/焦点回归） |

运行方式：
```bash
cd /data/zeokeel/.cache/ui-audit
export ZK_BASE=http://127.0.0.1:9888 \
       ZK_TOKEN=$(python3 -c "import json;print(json.load(open('data-mobile/gateway.json'))['token'])")
node mob3c-back.mjs      # F1
node mob5b-drag.mjs      # F2
```

## 7. 建议补充的验收点

1. **抽屉的返回/前进幂等性**：Back/Forward 多次后抽屉状态必须保持"关闭"，且再次打开仍工作（本次 F1 就是漏测这条）。
2. **断点缝隙断言**：`window.innerWidth === 1024` 时"分隔条拖拽前后宽度差 > 0"，并断言 `zk.sidebar.width` 只在拖拽真正生效时写入；两套断点（CSS `1024` vs JS `1023`）应统一。
3. **窄屏表格回退**：`/insights` 这类双列表格在 <720 应叠成单列，且日期不折行（F3）。
4. **待审批态的验收要在"会话首次运行"上做**：审批一旦被允许，同一会话不再提示；验收脚本不要顺手点"允许一次"把种子审批消费掉（本次就是踩了这条）。
5. **验收用独立数据目录**（`startB.sh <name> …`）做输入/发送类用例，避免污染种子的完成态/待审批态。
6. **深色验收必须走"改 → 保存"**：主题是设置草稿，只点单选框不保存不会翻转（这是设计如此，不是缺陷；但脚本容易误判为"深色没生效"）。
---

# 复验（轮 2）—— 新冻结构建 `7a9d51e`，新实例 :9904

- 被测构建：`7a9d51e fix(web): close the three gaps the mobile audit found`（父提交 `418fd30`）
- 实例：`http://127.0.0.1:9904`（`data-mobile/gateway.json`：port 9904、token `8DOaM0qc…`、startedAt 2026-11-24T15:10:14Z、epoch `a368d710364bab0b`）
- 二进制：`.cache/bin/zeokeel-mobile` **44 371 368 B**，mtime 11-24 23:09
- 服务端产物：`/assets/index-MbrI8NUO.js` **2 424 778 B**（含新抽屉状态字段 `seenKey`）、`/assets/index-D8W2mWPV.css` **41 614 B**
- 数据：全新库（无种子）；由我通过 `[data-testid="new-session"]` 建立，见 §8.8
- 方式：真网关 + 真 Chromium（Playwright `chromium-1243`），全部数字来自 `page.evaluate`；**逐屏、逐档、逐键位，无抽样**

## 8.1 本轮 4 个修复点（逐条证伪）

| # | 检查 | 结果 | 证据（数字 + 截图） |
|---|---|---|---|
| F1 | 抽屉 × 浏览器历史：打开→点导航/会话（自动关）→ **Back 保持关**；**Forward 保持关**；连按两次 Back、再开抽屉、再 Back 都是关；附带 Esc/遮罩仍能关、焦点回汉堡 | **PASS** | 全程抽屉 280px、mask 覆盖 390、`rgba(0,0,0,0.45)`。T1 导航（收件箱）：点击后 280→0 且 `mask=0`、url `/inbox`；Back → `/c/A` `open=false mask=0`；Forward → `/inbox` `open=false mask=0`。T2 会话点击：→ `/c/B` 关闭；Back → `/c/A` 关；Forward → `/c/B` 关。T3：Back×2 → `/inbox` 关、`open=false`；再开 → `open=true 280 mask 390`；再 Back → 关。T4 Esc → 关、`document.activeElement=BUTTON[data-testid=open-sidebar]`（focus-visible `outline solid 2px rgba(13,68,246,0.8)`）。T5 遮罩点击 → 关、焦点同样回汉堡。T6 三次开关不增加 history（7 → 7）。附加：点"当前会话"行也关闭（url 不变、`open=false`）；Back→Forward×2 仍 `open=false`。控制台在 open/nav/back/forward 全程 **0 warning / 0 error**（渲染期重置无 React 告警）。`audit-mobile-r2-01…11`、`-47/-48`（`out/r2-drawer-history.json`、`out/r2-edge2.json`） |
| F2 | 1023/1024/1025/1100/1440 五档拖 80px，侧栏宽度实际变化>0；`zk.sidebar.width` 与真实宽度一致；1023 抽屉、1024+ 分栏（无缝隙） | **PASS**（附 O2 偏差） | **1023**：`narrow=true`、汉堡在、无 `.zk-split`、`main=1023`、抽屉 280 + mask 1023、无横向滚动。**1024**：`narrow=false`、无汉堡、splitter 在、`panelW=264`（不再被 232 钉死）、`main=760`、`hOverflow=0`；拖 +80 → **264→344（Δ80）**、`stored=344`；回拖 −80 → 344→264、`stored=264`。**1025 / 1100 / 1440**：同样 Δ+80 / Δ−80、`stored` 同步（1025: 264↔344；1100: 344；1440: 344）。served CSS 中已无 `.zk-shell__sidebar-panel` 任何强制宽度规则（`grep` 计数 0），媒体查询为 `width<=1023px`。`audit-mobile-r2-31-drag-1024/1025/1100/1440`、`-32-drawer-1023`（`out/r2-drag.json`） |
| F3 | 洞察：390/768 两表堆叠单列、表头与日期不折行、无横向滚动；1440 仍两列并排 | **PASS** | **390**：grid `342px`（单列），两表 y=229 / y=579（堆叠），表头"调用"100×39 **单行**、日期 `2026-11-18` 242×39 **单行**、`.ant-table-cell` `white-space: nowrap`，表内 `scrollW==clientW==342`，页面 `scrollW==clientW==390`。**768**：grid `720px` 单列、堆叠、单行、无横向滚动（768/768）。**1440**：grid `556px 556px`，两表 x=288 / x=860 **并排**，表头与日期均单行（h=39）。`audit-mobile-r2-24-insights`、`-28-insights-390`、`-29-insights-768`、`-30-insights-1440`（`out/r2-insights.json`） |
| F4 | 汉堡 CSS 唯一：1440 无按钮；390 下 40×40、有 hover/focus 样式；无重复规则导致仅在 <900 生效 | **PASS** | 服务端 CSS（41 614 B）里 `.zk-topband__menu` 共 **3 处**：基样式、`:hover`、`:focus-visible`，**嵌套深度全部为 0（顶层）**，媒体查询内无副本。390 实测：`40×40 @(8,4)`、`display:flex`、`border-radius:8px`、`cursor:pointer`、`padding:0`、`margin-inline-start:-8px`、`aria-label=打开侧栏`；hover：背景 `rgba(0,0,0,0)` → `rgba(15,17,21,0.043)`、文字 `rgb(74,81,96)` → `rgb(15,17,21)`；Tab 焦点 `matches(':focus-visible')=true` 且 `outline solid 2px rgba(13,68,246,0.8)`。1440：`[data-testid=open-sidebar]` 不存在、`.zk-topband__menu` 计数 0，band 里只有 `H1.zk-topband__title`。`audit-mobile-r2-12/15/16`、`-36-1440-home`（`out/r2-keyboard-css.json`） |

## 8.2 上轮 8 项回归（全跑）

| # | 检查 | 结果 | 证据 |
|---|---|---|---|
| R1 | 390×844 八屏无横向滚动、`#zk-main ≥ 380`、无"一字一行" | **PASS** | 7 屏 + 待审批屏 = 8：`scrollW==clientW==390`、`main=390`、`hOverflow=0`；正文每行字符数 14.8 / 14.6 / 8.0 / 9.9 / 6.9 / 5.9 / 4.3 / 9.5（待审批）。`audit-mobile-r2-18…24`、`-39`（`out/r2-screens-390.json`、`out/r2-pending.json`） |
| R2 | 输入框 348px + 连发 + 待审批 notice/禁用 | **PASS** | textarea `clientWidth=348`、right=369；5 次连发全部成功（341 / 342 / 344 / 351 / 350 ms），每次键入值完全一致（`复验连发第 N 条：确认输入框可用`），bubble 2→4→6→8→10→12，末态 textarea 清空、发送键回到禁用；待审批屏：`notice="本会话有一条审批等待处理：处理后才能继续发送。"`（right 382）、`textarea.disabled=true`、`composer-send.disabled=true`、审批卡 342 宽 right 366、允许键 right 349。`audit-mobile-r2-17`、`-39`（`out/r2-composer.json`） |
| R3 | 抽屉 280 + mask | **PASS** | 390：280 + mask 390 `rgba(0,0,0,0.45)` z=1000；768：280 + mask 768；1023：280 + mask 1023；点击遮罩/Esc 关闭后焦点均回 `open-sidebar`。`audit-mobile-r2-13/14/41/32` |
| R4 | 设置页 picker + 9 页 0 控件被裁 | **PASS** | 390：picker `160×32 @x16`、搜索框在、`.zk-settings__nav` `display:none` `0×0`、grid 单列 `390px`；下拉 9 项、`maxRight=172`；9 个页面（模型/行为与限额/外观/通知/网络与搜索/LSP/归档/数据与隐私/关于）控件 7–22 个、**被裁 0 个**、`maxRight ≤ 374`、`hOverflow=0`。`audit-mobile-r2-25/26/27`、`-21/-22`（`out/r2-settings.json`） |
| R5 | 768 抽屉形态 / 1440 分栏形态 | **PASS** | 768：`narrow=true`、汉堡在、`.zk-sidebar` 不在首屏 DOM、无 splitter、`main=768`、`hOverflow=0`；抽屉 280 + mask 768，遮罩关闭后焦点回汉堡、布局正常。1440：无汉堡、sidebar 264、`main=1176`、`hOverflow=0`。`audit-mobile-r2-41`、`-36`（`out/r2-edge.json`、`out/r2-regress.json`） |
| R6 | 深色 + 390 | **PASS** | `data-theme=dark`、`color-scheme=dark`；实测背景：`body`/`#zk-main`/`.zk-shell` `rgb(17,17,24)`（亮度 18）、composer `rgb(27,27,38)`、`.ant-drawer-section` `rgb(31,31,31)`（31）、`.ant-drawer-body`/`.zk-sidebar` `rgb(11,11,16)`（11）、会话行文字 `rgba(255,255,255,0.85)`、mask `rgba(0,0,0,0.45)`；抽屉 280。恢复"跟随系统"后 `data-theme=light`、body 白。`audit-mobile-r2-33/34/35`（`out/r2-dark.json`） |
| R7 | 1440 无回归（对 `shots/final-*.png`） | **PASS** | 同尺寸 2880×1800 逐像素：`/settings/model` vs `final-settings-model.png`：mean 2.5/255、>8 差异 **2.8%**、>32 **1.9%**（band 区 0.0%）——差异来自 `ad123e8` 的模型页内容（默认模型行、模型服务分区），布局几何一致；`/c/:id` vs `final-thread-idle.png`：>8 **8.9%**（band 26.7%，来自标题/toast 文案与会话内容不同），几何一致。几何实测：sidebar 264、`#zk-main` 1176、band 49（设置）/87（会话）、composer 1176。`audit-mobile-r2-36/37/38`（`out/r2-regress.json`） |
| R8 | 键盘 Tab→汉堡→Enter→Esc 焦点回位 | **PASS** | Tab#1 = `跳到主内容`，**Tab#2 = `data-testid=open-sidebar`（name 打开侧栏，40×40）**；Enter → `open=true`、280、mask 390；Esc → `open=false`、`activeElement=BUTTON[open-sidebar]`（aria-label 打开侧栏）。`audit-mobile-r2-12/13/14`（`out/r2-keyboard-css.json`） |

## 8.3 结论

**PASS** —— 本轮 4 个修复点（抽屉历史 / 1024 拖拽 / 洞察窄屏 / 汉堡 CSS 唯一）全部证伪失败，即修复生效；上轮 8 项回归全部通过，无回归。**本轮清单无失败项。**

但复验中发现了 **2 个新缺陷（都不在本轮清单内）**：
1. **F5**（P2，同族）：抽屉在"窄→宽→窄"视口往返后会自行重新打开（§8.4）——若把"抽屉只在用户操作时打开"也算作抽屉修复的验收口径，这一点应记 FAIL；
2. **F6**（P2，英文 UI 专有）：`ui.language=en` 时 390 会话页 `scrollWidth=409 > 390` 横向溢出，元凶是输入框脚注 `.zk-composer__meters`（nowrap 306px）+ `.zk-composer__foot{flex-wrap:nowrap}`（§8.7）。中文下 390 无溢出。

## 8.4 新发现与仍需留意的点

### F5（P2，新）抽屉在视口往返后自行复活
- **现象**：390 打开抽屉 → 把视口改成 1440（抽屉卸载、正常分栏）→ 改回 390 → **抽屉又自己打开了**（`open=true`、280px、遮罩覆盖正文），用户没点任何东西。
- **复现**（`r2-12-resurrect.mjs`，两次确定性复现）：
  1. 390×844 打开 `/c/<A>`，点汉堡打开抽屉；
  2. `page.setViewportSize({width:1440,height:900})`，等 1.3s：`narrow=false`、`.ant-drawer` 从 DOM 消失、sidebar 264；
  3. `page.setViewportSize({width:390,height:844})`，等 1.5s：`narrow=true`、**`open=true`、drawerW=280、`elementFromPoint(195,500)` 命中 `.ant-drawer`**。
  对照：先关抽屉再往返 → 保持关闭；导航后再往返 → 保持关闭（说明只有"未清理的 openedFor"会复活）。
- **根因**：`web/src/shell/app-shell.tsx:71-80` 的渲染期重置只比较 `location.key`（`if (drawer.seenKey !== location.key) setDrawer(...)`），没有把 `narrow` 纳入；宽屏时 Drawer 组件被卸载，但 `drawer.openedFor` 仍等于当前 `location.key`，回到窄屏时 `drawerOpen = narrow && openedFor === location.key` 重新为真。
- **建议修法**：把断点也放进同一个重置键，例如
  ```ts
  const [drawer, setDrawer] = useState({ openedFor: null, seenKey: location.key, seenNarrow: narrow });
  if (drawer.seenKey !== location.key || drawer.seenNarrow !== narrow) {
    setDrawer({ openedFor: null, seenKey: location.key, seenNarrow: narrow });
  }
  ```
  （仍是渲染期重置，不引入 effect。）
- **严重度**：P2 —— 需要"抽屉打开时跨越 1024 断点缩放/旋转"才触发；平板横竖屏或桌面窗口缩放会遇到。
- **补充**：768→1440→768 同样复活（`open=true/dw=280`，实测），所以不是 390 特有，任何窄→宽→窄都算。

**边界矩阵**（`r2-14-f5-matrix.mjs`，每格为 `open` 值，起止均为 390）：

| 场景 | 390 开抽屉 | 过渡 | 回到 390 | 结果 |
|---|---|---|---|---|
| 从未打开过 | — | →1440 | `open=false` | 正常 |
| 同侧缩放（390→1023→390） | `true/280` | 1023 仍 narrow | `true/280` | 正常（不该关） |
| 同侧缩放（390→768→390） | `true/280` | 768 仍 narrow | `true/280` | 正常 |
| **跨断点（390→1440→390）** | `true/280` | 1440 抽屉卸载 | **`true/280`** | **F5：复活** |
| 先遮罩关闭再跨断点 | `true/280` → 关闭 | →1440 | `false` | 正常（`openedFor` 已清） |
| 多次跨断点（390→1440→1024→390） | `true/280` | 两次都不 narrow | **`true/280`** | **F5：复活** |

结论：只要"打开过且未手动关闭"，跨过一次窄/宽断点再回来就会复活；与 location.key 无关（未导航），只与 `narrow` 变化有关。

### O1（注意，不是缺陷）有等待审批时 Esc 会"拒绝审批"，不会关抽屉
- 实测（`out/r2-edge2.json`）：390 待审批会话 → 打开抽屉（280、遮罩覆盖）→ 按 **Esc** → **抽屉仍开（open=true、280）**，而那条审批被拒绝（`notice` 消失、`textarea.disabled` 变 false、`允许一次` 按钮消失）；随后点遮罩，抽屉正常关闭且焦点回汉堡。
- 这是 `APPROVAL-SURFACES` 里 L4「Esc=拒绝，从任何地方都能拒绝最老的请求」的既定语义，优先级高于"Esc 关抽屉"。**因此"Esc 关抽屉"只在没有等待审批时成立**；验收脚本与帮助文案最好写清这一点（否则会被误判成抽屉回归——我第一遍就踩了）。

### O2（偏差）零位移的"按下-抬起"仍会写 `zk.sidebar.width`
- 实测：1024 / 1025 / 1100 / 1440 下，在 `.ant-splitter-bar-dragger` 上按下-抬起**不移动**，侧栏宽度不变（264→264），但 `localStorage['zk.sidebar.width']` 被写成 `"264"`。真实拖拽时写入值与可见宽度完全一致（344/264）。
- 与提交信息 "the drag writes the stored width only when it moved" 字面不符：`app-shell.tsx:158-163` 的 `onResizeEnd` 只判断 `if (width)`，没有与上一次宽度比较。危害极小（写入的是当前真实宽度），若要严格符合描述，加一句 `if (Math.round(width) === lastWidthRef.current) return;` 即可。

### O3（观察）`/`（现在）页在无会话时仍是完整可用形态
- 390 下 `/` 显示"现在，说点什么" + 输入框 + 待审批卡（若有），`main=390`、无横向滚动；这是设计如此，记录备查。

## 8.5 截图索引（`shots/`，`audit-mobile-r2-*`，共 66 张）

| 文件 | 内容 |
|---|---|
| `r2-seed-a.png` / `r2-seed-b-pending.png` | 建会话 A（完成态）/ B（待审批，后来被 O1 试验拒绝） |
| `r2-01-nav-closed` … `r2-03-nav-forward` | 导航点击关闭 → Back → Forward（均关闭） |
| `r2-04-session-picked` … `r2-06-session-forward` | 会话点击关闭 → Back → Forward（均关闭） |
| `r2-07-back-twice` / `r2-08-reopened-after-back` / `r2-09-back-after-reopen` | 连按两次 Back、再开抽屉、再 Back |
| `r2-10-esc-focus` / `r2-11-scrim-closed` | Esc / 遮罩关闭 + 焦点回汉堡 |
| `r2-12-kbd-hamburger` / `r2-13-kbd-drawer-open` / `r2-14-kbd-esc-focus` | 键盘 Tab → Enter → Esc |
| `r2-15-hamburger-hover` / `r2-16-hamburger-focus-ring` | 汉堡 hover / focus-visible |
| `r2-17-composer-answers` | 连发 5 条后的会话 |
| `r2-18-home` … `r2-24-insights` | 390 七屏（首页/完成态会话/收件箱/设置模型/设置外观/团队/洞察） |
| `r2-25-settings-picker` / `r2-26-settings-dropdown` / `r2-27-settings-last` | 设置页选择器、下拉 9 项、切换后 |
| `r2-28-insights-390` / `r2-29-insights-768` / `r2-30-insights-1440` | 洞察：堆叠 / 堆叠 / 两列 |
| `r2-31-drag-{1024,1025,1100,1440}` | 各档拖拽后（侧栏变宽） |
| `r2-32-drawer-1023` | 1023 是抽屉（280 + mask 1023） |
| `r2-33/34/35-dark-*` | 深色 390：设置 / 会话 / 抽屉 |
| `r2-36/37/38-1440-*` | 1440 首页 / 会话 / 设置模型（回归对比用） |
| `r2-39-pending-thread` / `r2-40-pending-inbox` | 390 待审批屏 + 收件箱 1 项 |
| `r2-41-768-drawer` | 768 抽屉（280 + mask 768，收件箱角标 1） |
| `r2-42-roundtrip-1440` / `r2-43-roundtrip-back-390` | **F5**：宽屏时抽屉消失 → 回窄屏后复活 |
| `r2-44-resurrect-1` / `r2-45-resurrect-2` | **F5** 第二次、第三次往返仍复活 |
| `r2-46-control-roundtrip` | 对照组：先关闭再往返 → 保持关闭 |
| `r2-47-click-active-row` / `r2-48-fwd-twice` | 点当前会话行即关闭 / Forward×2 仍关闭 |
| `r2-49-esc-with-pending` | **O1**：有等待审批时 Esc 拒绝审批、抽屉不关 |
| `r2-50-en-home` … `r2-56-en-insights` | 英文 UI × 390 七屏（其中 `r2-51-en-thread` 就是 **F6** 的 409/390） |
| `r2-57-en-settings-dropdown` | 英文设置页下拉（9 项，maxRight 172） |
| `r2-58-en-thread-overflow` / `r2-59-en-overflow-390` | **F6** 溢出证据 |
| `r2-60-zh-long-meters-390` | 中文对照：12 轮下脚注仍 390/390（不溢出） |
| `r2-61-en-drawer-390` | 英文抽屉内部几何正常（页级溢出仍来自 F6） |

## 8.6 复现脚本（`/data/zeokeel/.cache/ui-audit/`）

| 脚本 | 覆盖 | 输出 |
|---|---|---|
| `r2-seed.mjs` | 在全新库上建会话 A（完成态）/ B（待审批） | `out/r2-seed.json` |
| `r2-1b-drawer-history.mjs` | 修复点 1（T1–T6，含前置"无等待审批"断言） | `out/r2-drawer-history.json` |
| `r2-2-keyboard-css.mjs` | 键盘 + 汉堡 CSS 唯一性 / hover / focus-visible / 1440 缺失 | `out/r2-keyboard-css.json` |
| `r2-3-composer.mjs` | 输入框 348 + 连发 5 条 | `out/r2-composer.json` |
| `r2-4-screens.mjs` | 390 七屏几何 | `out/r2-screens-390.json` |
| `r2-5-settings.mjs` | 设置 picker + 9 页扫描 | `out/r2-settings.json` |
| `r2-6-insights.mjs` | 洞察 390/768/1440 堆叠与折行 | `out/r2-insights.json` |
| `r2-7-drag.mjs` | 修复点 2（1023/1024/1025/1100/1440 + 空拖写盘） | `out/r2-drag.json` |
| `r2-8-dark.mjs` | 深色 + 390 | `out/r2-dark.json` |
| `r2-9-regress.mjs` | 1440 回归截图 | `out/r2-regress.json` |
| `r2-10-pending.mjs` | 待审批态（最后执行，避免被 Esc 消费） | `out/r2-pending.json` |
| `r2-11-edge.mjs` | 768 形态 / 控制台洁净 / 视口往返 | `out/r2-edge.json` |
| `r2-12-resurrect.mjs` | **F5** 复现（含对照组） | `out/r2-resurrect.json` |
| `r2-13-final-edges.mjs` | 点当前会话行 / Forward×2 / **O1** Esc 与审批仲裁 | `out/r2-edge2.json` |

运行：
```bash
cd /data/zeokeel/.cache/ui-audit
export ZK_BASE=http://127.0.0.1:9904 \
       ZK_TOKEN=$(python3 -c "import json;print(json.load(open('data-mobile/gateway.json'))['token'])")
node r2-12-resurrect.mjs   # F5
node r2-7-drag.mjs         # 五档拖拽
```

## 8.7 附加覆盖（我自己加的）：英文 UI × 390 —— 发现 F6

本轮清单只要求中文。为了"边界"我把 `ui.language` 切到英文又跑了一遍 390 七屏（`r2-15-en.mjs`）：

| 屏 | scrollW / clientW | main | 结论 |
|---|---|---|---|
| `/` | 390 / 390 | 390 | 正常 |
| `/c/:id` | **409 / 390** | 390 | **横向溢出 19px（F6）** |
| `/inbox` | 390 / 390 | 390 | 正常 |
| `/settings/model` | 390 / 390 | 390 | 正常 |
| `/settings/appearance` | 390 / 390 | 390 | 正常 |
| `/team` | 390 / 390 | 390 | 正常 |
| `/insights` | 390 / 390 | 390 | 正常（表名单列 + nowrap 同样生效） |

设置页下拉在英文下同样正常：9 项、`maxRight=172`。

### F6（P2，新）英文 UI 下 390 会话页横向溢出 19px（输入框脚注不换行、状态条不缩）
- **现象**：`ui.language=en`、390×844 的 `/c/:id` 上 `document.documentElement.scrollWidth=409 > clientWidth=390`，页面可横向滚动；中文下同一会话是 390/390。
- **定位**（`r2-16-en-overflow.mjs`，逐元素扫描 + computed 样式）：
  - 溢出元素是 `.zk-composer__meters`（内容 `.zk-composer-meters`）：`x=103 w=306 right=409`，`white-space: nowrap`、`min-width: auto`、`flex: 0 1 auto`。
  - 父级 `.zk-composer__foot`：`display:flex; flex-wrap:nowrap; width=374`（x 8…382）。
  - 英文：hint 被压到 **83px**（`Enter or Cmd/Ctrl+Enter to send · Shift+Enter for a new line · / for commands`）+ 状态条 **306px** = 389 > 374 → 溢出 15px，页面多出 19px（含边距）。
  - 中文对照：hint 105px + 状态条 257px = 362 ≤ 374（不溢出）；即使会话涨到 12 轮（状态条 272px）也不溢出——只有英文的宽词把 min-content 顶出去。
- **影响**：英文用户（或任何宽字符语言）在手机上会话页可左右拖动 19px，正文跟着抖；轮次变多后更糟——会话涨到 **12 轮**时英文页 `scrollW=424`（溢出 34px，状态条 "12 turns 12 steps · 300 tok · cache 0% · context 0%" 更宽），同一会话中文仍是 390/390。抽屉打开时页面同样可横向滚（424）。
- 附：英文下抽屉**内部**没问题——9 个导航/会话项 `right ≤ 270 < 280`、无内部溢出（`audit-mobile-r2-61-en-drawer-390`）。
- **建议修法**（`web/src/design/global.css`）：
  ```css
  .zk-composer__foot { flex-wrap: wrap; }          /* 或仅在 <480 时 wrap */
  .zk-composer__meters { margin-inline-start: auto; white-space: nowrap; min-width: 0; }
  @media (max-width: 480px) { .zk-composer__foot { flex-wrap: wrap; } .zk-composer__meters { margin-inline-start: 0; } }
  ```
  或者让状态条在窄屏换行（`white-space: normal`）／把 hint 在 <480 隐藏。

### O4 / O5（i18n 残留，英文 UI 下才可见，P3）
- **O4**：切到英文后 `document.documentElement.lang` 仍是 **`zh-CN`**（实测 EN UI 里读到 `zh-CN`），屏幕阅读器会用中文语音读英文页。
- **O5**：跳过链接文案硬编码中文——`web/src/shell/app-shell.tsx:134`（`<a className="zk-skip-link" href="#zk-main">跳到主内容</a>`），英文 UI 下第一个可聚焦元素仍是中文；`r2-15` 的英文页 `innerText` 首词即"跳到主内容"。
- 二者都不在本轮范围，但既然声称双语，建议顺手改成 `t()` + 跟随 `ui.language` 设 `<html lang>`。

## 8.8 复验后的数据状态（复查须知）

| 会话 | id | 状态 |
|---|---|---|
| 甲（完成态） | `sess-1795533141601190937` | 1 条种子消息 + 5 条"复验连发"（共 6 轮），其审批已批准 |
| 乙 | `sess-1795533150150969185` | 原为待审批；**被第一次 Esc 试验按 L4 拒绝**，现为普通会话 |
| 丙 | `sess-1795533625024717083` | 为拍待审批屏而建；其审批**在 O1 试验中被 Esc 拒绝** |

- 现状：**无等待审批**（`/inbox` 为空）。要再看待审批态：新会话 + 发一条会调 `web_fetch` 的消息，**不要按 Esc**（Esc 会拒绝它）。
- 主题已恢复"跟随系统"（`data-theme=light`）。`localStorage` 只在各自浏览器 context 内，未跨用例污染。

---

# 第三轮复验（轮 4）—— 冻结构建 `54454ca`（含移动端修复 `6ad87c6`），新实例 :9906

- 被测构建：HEAD `54454ca`（gateway `mcp.reload`）+ 上一提交 `6ad87c6 fix(web): drawer resets on a breakpoint flip; EN foot no longer overflows`
- 实例：`http://127.0.0.1:9906`（`data-mobile/gateway.json`：epoch `fc359e033d3acba4`、startedAt 2026-11-24T15:36:32Z、token `TkuLbl4D…`）
- 二进制：`.cache/bin/zeokeel-mobile` **44 382 184 B**，mtime 11-24 23:36
- 服务端产物：`/assets/index-Br_frsuU.js` **2 424 946 B**（含 `seenNarrow`、`shell.skipToContent`）、`/assets/index-DRyGikmN.css` **41 701 B**
- 数据：全新库（无种子）。我建了 甲（**12 轮**完成态）、乙（用于抽屉/输入）、丙（待审批，最后建、**不批准也不按 Esc**）
- 方式：真网关 + 真 Chromium，全部数字来自 `page.evaluate`；**五档宽度 × 两语言 × 边界值，不抽样**
- 构建完整性：工作区有**未提交**改动（`web/src/features/toasts/toasts.tsx`、`web/src/shell/sidebar.tsx`），不在 23:36 的冻结二进制里；本节所有结论针对**冻结二进制与其服务端产物**，不描述工作区未构建的改动。

## 9.1 本轮 5 个修复点（逐条证伪）

| # | 检查 | 结果 | 证据（数字 + 截图） |
|---|---|---|---|
| F5 | 抽屉在断点往返后不得复活 | **PASS** | ① 390 开抽屉（280/遮罩覆盖）→ 1440（`.ant-drawer` 卸载、sidebar 264、无汉堡）→ 回 390：**`open=false, drawerW=0, mask=0, covers=false`**；② 768→1440→768：`open=false`；③ 1024（分栏 264/760）→1000（抽屉可开 280）→1024（卸载）→1000：`open=false`；④ 开抽屉→1440→在宽屏点会话"乙"（url 变 `/c/B`）→回 390：`open=false`；⑤ 320→1440→320：`open=false`。对照：从未打开→往返仍关；先 Esc 关闭→往返仍关；**同侧缩放 390→1023→390 仍保持打开（280）**；多次穿越（1440→1024→390）`open=false`。`audit-mobile-r3-01…05`、`-38/-39`（`out/r3-f5.json`、`out/r3-extras.json`） |
| F6 | 英文 390 无横向溢出；foot 的 hint/usage 换行或截断而不是撑破；320/768 同验 | **PASS** | **390 EN**：`scrollW==clientW==390`（修复前 409）；`.zk-composer__foot` `flex-wrap:wrap`，hint 换行成 2 行（h=36, y=766），usage 落到第二行 `x=61…right=382`（321px，未截断）；**320 EN**：`320/320`，hint 2 行，usage 第二行 `clientW=304 < scrollW=321` → **截断（`overflow:hidden` + `text-overflow:ellipsis`，截图可见省略号）**；**768 EN**：`768/768`，hint（x24…434）与 usage（x423…744）同一行；**ZH** 320/390/768 全部无溢出、usage 272px 完整。12 轮会话（`12 turns 12 steps · 300 tok · cache 0% · context 0%`）全程 `hOverflow=0`。`audit-mobile-r3-07/08/09`（`out/r3-f6.json`） |
| O2 | 按下-抬起不移动时 `zk.sidebar.width` 不应变化；移动后拖回应正确写回 | **FAIL** | 1024/1100/1440 三档、每档新 context：先 `localStorage.removeItem('zk.sidebar.width')` → 重载（`stored=null, panelW=264`）→ **在 `.ant-splitter-bar-dragger` 上按下-抬起不移动 → `stored="264"`（panelW 仍 264）**；空等 4 秒的对照里 `stored` 保持 `null`（所以写入确实由这次按下-抬起触发）；真实拖 +80 → `344/344`，拖回 −80 → `264/264`（这两步正确）。根因见 §9.2。`audit-mobile-r3-06-o2-nomove-{1024,1100,1440}`（`out/r3-o2.json`） |
| O4 | `html[lang]` 跟随界面语言，刷新后仍正确 | **PASS** | 切英文 → `document.documentElement.lang === "en"`；切回中文 → `"zh-CN"`；各自刷新后终态仍正确（en / zh-CN），且 skip 文案与 `lang` 始终一致（不会出现"英文内容标 zh-CN"）。**注意**：若浏览器语言与已保存设置不一致，首屏约 1 秒是浏览器语言（见 N1）。`audit-mobile-r3-14/15`（`out/r3-flash.json`） |
| O5 | skip link 文案双语；Tab 一次聚焦；Enter 跳到 `#zk-main` | **PASS** | EN：`Skip to main content`（151×30）；ZH：`跳到主内容`（94×30）；未聚焦时 `y=-32`（移出视口）、Tab 后 `y=8` 回到视口且 `document.activeElement.classList='zk-skip-link'`（focus 环 `solid 2px rgba(13,68,246,0.8)`）；Enter 后 `location.hash="#zk-main"`（EN/ZH 均验证）。Tab#1 即它、Tab#2 是汉堡。`audit-mobile-r3-10…13`（`out/r3-lang-skip.json`） |

## 9.2 O2 失败详情（本轮唯一失败项）

- **现象**：在分隔条把手（`.ant-splitter-bar-dragger`）上按下再抬起、完全不动，侧栏宽度不变（264），但 `localStorage['zk.sidebar.width']` 被写入 `"264"`（原本不存在）。三档（1024/1100/1440）全部复现。
- **复现**（`r3-2-o2.mjs`）：
  1. 1024/1100/1440 新建 context，打开 `/c/<A>`；
  2. `localStorage.removeItem('zk.sidebar.width')` → `reload()` → 读：`stored=null, panelW=264`；
  3. 在 `.ant-splitter-bar-dragger`（x=panelW，w=6）上 `mouse.move → down → 350ms → up`，不做任何移动；
  4. 800ms 后读：`stored="264"`（panelW 仍 264）。
  - 对照：什么都不做等 4 秒 → `stored` 仍为 `null`；真实拖拽 +80 → `stored="344"`、拖回 −80 → `stored="264"`；1px 移动 → `stored="265"`。
- **根因（事实层面）**：`web/src/shell/app-shell.tsx:163-169` 的 `onResizeEnd` 仍然是无条件写入：
  ```ts
  onResizeEnd={(sizes) => {
    const width = sizes[0];
    if (width && typeof localStorage !== 'undefined') {
      localStorage.setItem(SIDEBAR_WIDTH_KEY, String(Math.round(width)));   // ← 没有和上一次宽度比较
      if (!collapsed) setExpandedWidth(Math.round(width));
    }
  }}
  ```
  打包产物里同样是 `onResizeEnd:e=>{let t=e[0];t&&typeof localStorage<"u"&&(localStorage.setItem(X9,String(Math.round(t))),…)}`，没有任何比较分支。也就是说 **`6ad87c6` 的提交信息声称的 "it now compares first" 没有落地**（该提交对 `app-shell.tsx` 只有抽屉 `seenNarrow` 与 skip link 两处改动，见 `git show 6ad87c6 -- web/src/shell/app-shell.tsx`，共 60 行、无 `onResizeEnd` hunk）。
- **影响**：写入的值等于当前真实宽度，用户不可见；但（a）与要求"不应变化"不符；（b）`zk.sidebar.width` 会在用户只是点了一下把手时被凭空创建（此前若用户从未拖过，键本应不存在）。
- **建议修法**（任一）：
  ```ts
  const lastWidth = useRef<number | null>(null);
  onResizeEnd={(sizes) => {
    const width = Math.round(sizes[0] ?? 0);
    if (!width || width === lastWidth.current) return;   // 没有任何变化就不写
    lastWidth.current = width;
    localStorage.setItem(SIDEBAR_WIDTH_KEY, String(width));
    if (!collapsed) setExpandedWidth(width);
  }}
  ```
  或在 pointerdown 时记录起始宽度，`onResizeEnd` 里与起始宽度比较后再写。验收口径建议保留"清空键 → 空按一次 → 键仍为 null"。

## 9.3 回归（上两轮关键项全跑）

| # | 检查 | 结果 | 证据 |
|---|---|---|---|
| R1 | 390 八屏无横向滚动（EN 7 屏 + ZH 7 屏 + 待审批屏） | **PASS** | EN：home/thread/inbox/settings-model/settings-appearance/team/insights 全部 `scrollW==clientW==390`、`main=390`（perLine 20.8/18/22/16.8/16.1/15.5/6.3）；ZH 同 7 屏同样 390/390（14.8/16.1/8/10/7/5.9/4.4）；待审批屏 390/390、main 390、perLine 9.9。`audit-mobile-r3-23-*`、`-24-*`、`-36`（`out/r3-screens.json`、`out/r3-pending.json`） |
| R2 | 输入框 348 + 连发 | **PASS** | textarea `clientWidth=348`、right=369；5 次连发全成功（343/340/335/350/352 ms），bubble 2→4→6→8→10→12，末态清空+发送禁用；页面 390/390。`audit-mobile-r3-22`（`out/r3-composer.json`） |
| R3 | 抽屉 280 + mask + Esc/遮罩关闭 + 焦点回汉堡 | **PASS** | 打开 280、mask 390 `rgba(0,0,0,0.45)`、覆盖正文；Esc → `open=false`、焦点 `BUTTON[open-sidebar]`（"Open sidebar"）；遮罩点击 → 同样关闭且焦点回汉堡。`audit-mobile-r3-16/17/18` |
| R4 | 键盘 Tab→汉堡→Enter→Esc | **PASS** | Tab#1 = skip link（focus 环），Tab#2 = 汉堡（40×40，"Open sidebar"）→ Enter 打开 280 → Esc 关闭且焦点回汉堡（`data-testid=open-sidebar`）。`audit-mobile-r3-19/20` |
| R5 | Back/Forward 幂等（导航 + 会话） | **PASS** | 导航→`/inbox` 关；Back→`/c A` 关；Forward→`/inbox` 关；Back×2→`/c A`、`/` 均关；抽屉内点会话→`/c B` 关；Back→`/c A` 关；Forward→`/c B` 关（`open=false` 全程）。`audit-mobile-r3-21`（`out/r3-drawer-kbd.json`） |
| R6 | 设置 picker + 9 页 0 控件被裁 | **PASS** | picker `160×32 @x16`、`nav display:none`、grid 单列 `390px`、搜索框在；下拉 9 项 `maxRight=172`；9 页控件 7–22 个、**被裁 0**、`maxRight ≤ 374`、`hOverflow=0`。`audit-mobile-r3-25/26/27`（`out/r3-screens.json`） |
| R7 | 768 / 1023 / 1024 / 1025 / 1100 / 1440 形态 + 拖拽 | **PASS** | 768、1023：`narrow=true`、抽屉 280 + mask（768/1023）；1024/1025/1100/1440：`narrow=false`、无汉堡、panel 264、main 760/761/836/1176，拖 +80 → 344（Δ80）、`stored=344`，拖回 −80 → 264、`stored=264`；全程 `hOverflow=0`。`audit-mobile-r3-28-*`、`-29-*`（`out/r3-viewports.json`） |
| R8 | 深色 + 390 | **PASS** | `data-theme=dark`、`color-scheme=dark`；body/main `rgb(17,17,24)`（亮度 18）、composer `rgb(27,27,38)`、drawer section `rgb(31,31,31)`（31）、drawer body/sidebar `rgb(11,11,16)`（11）、文字 `rgba(255,255,255,0.85)`、mask `.45`、抽屉 280、无溢出；恢复"跟随系统"后 light。`audit-mobile-r3-33/34/35`（`out/r3-dark.json`） |
| R9 | 1440 无回归（对 `final-*.png`） | **PASS** | 2880×1800 逐像素：`/settings/model` mean 2.5/255、>8 **2.8%**（内容差异来自模型页改版，band 区 0.0%）；`/c/:id` >8 **8.8%**（band 26.7% 为标题/toast 与会话内容不同，body 7.2%）；几何一致：sidebar 264、main 1176、无溢出。`audit-mobile-r3-30/31/32`（`out/r3-viewports.json`） |
| R10 | 待审批态 | **PASS** | 390：`notice="本会话有一条审批等待处理：处理后才能继续发送。"`（right 382）、`textarea.disabled=true`、`send.disabled=true`（settle 后测）、审批卡 342 宽 right 366、允许键 right 349；页面 390/390；`/inbox` 显示"1 项等待你"、无溢出。`audit-mobile-r3-36/37/40`（`out/r3-pending.json`、`out/r3-extras.json`） |

## 9.4 结论

**FAIL** —— 5 个修复点里 **4 个 PASS（F5 / F6 / O4 / O5），1 个 FAIL（O2）**；上两轮关键项回归全部 PASS，无回归。

失败项就是 **O2 未落地**：代码与打包产物都没有"先比较再写"的分支，空按一次分隔条仍会把当前宽度写进 `zk.sidebar.width`（1024/1100/1440 三档复现，且 4 秒空等对照证明写入由该次按下-抬起触发）。

## 9.5 新发现 / 仍需留意

### N1（P3，新）浏览器语言与已保存语言不一致时，首屏约 1 秒是浏览器语言
- 实测（`r3-12-extras.mjs`，刷新后每 100ms 采样一次 `html[lang]` + skip 文案）：
  - 浏览器 `zh-CN` + 设置 `zh`：**无闪动**（359ms 起就是 `zh-CN`）；
  - 浏览器 `en-US` + 设置 `zh`：346ms 时 `lang=en / "Skip to main content"` → **1345ms 才切成 `zh-CN / 跳到主内容`**；
  - 反向同理（浏览器 `zh-CN` + 设置 `en`）：408ms 时 `zh-CN / 跳到主内容` → 1281ms 切 `en`。
- 原因：`i18n/index.tsx` 的初始 locale 取 `navigator.language`，等 settings 快照到了才用 `ui.language` 覆盖（设计如此，但会有一次可见的语言闪动，中文用户用英文界面会先看到中文）。
- 影响：P3。`lang` 与渲染语言始终一致（不会出现"英文内容标 zh-CN"），只是首帧语言不同。要消除可在首帧用 `localStorage` 缓存上次语言，或让 `ui.language` 随 HTML 首屏注入（`window.__ZK_SETTINGS__`）。

### N2（P3，新）页面没有 favicon，浏览器自动请求 `/favicon.ico` → 404
- `index.html` 只有 `<link rel="stylesheet">`，没有 `<link rel="icon">`；`curl /favicon.ico` → **404**；每个页面加载控制台都有一条 `Failed to load resource: 404`（Playwright 的 `page.on('response')` 捕获不到，属浏览器层请求）。
- 影响：仅控制台噪音 + 标签页无图标。建议加 `rel="icon"` 或在网关对 `/favicon.ico` 返回 204。

### N3（观察）390 下会话页 Tab 行溢出成"···"
- 320/390 下 `Conversation / Trajectory / Memory / Context` 会用 antd Tabs 的 `···` 溢出菜单收起（截图 `r3-07` 320 处可见 "Conversa… / Trajectory / Memo… / ···"）。这是 antd 的既定行为、功能可用（下拉可选），记录备查，不算缺陷。

## 9.6 截图索引（`shots/`，`audit-mobile-r3-*`，共 61 张）

| 文件 | 内容 |
|---|---|
| `r3-seed-a` / `r3-seed-b` | 建 甲（12 轮）/ 乙 会话后 |
| `r3-01-f5-390-1440-390` … `r3-05-f5-multi-cross` | F5：四类往返 + 多次穿越（均关） |
| `r3-06-o2-nomove-{1024,1100,1440}` | **O2 失败**：空按后 `stored` 被写入 264（panelW 不变） |
| `r3-07-f6-en-thread-{320,390}` / `r3-08-f6-en-home-320` / `r3-09-f6-zh-thread-390` | F6：EN 320（截断省略号）/ EN 390（换行）/ ZH 390 |
| `r3-10…13-*-skiplink-*` | O5：EN/ZH skip link 聚焦与跳转 |
| `r3-14/15-*-after-reload` | O4：刷新后语言终态 |
| `r3-16/17/18-drawer-*` | 抽屉打开 / Esc 关闭 / 遮罩关闭 |
| `r3-19/20-kbd-*` | 键盘汉堡聚焦 / Esc 焦点回位 |
| `r3-21-history` | Back/Forward 幂等 |
| `r3-22-composer` | 连发 5 条后 |
| `r3-23-*`（EN 7 屏）、`r3-24-*`（ZH 7 屏） | 390 八屏（含待审批屏 `-36`） |
| `r3-25/26/27-settings-*` | 设置 picker / 下拉 9 项 / 末页 |
| `r3-28-drag-{1024,1025,1100,1440}`、`r3-29-drawer-{768,1023}` | 各档拖拽与抽屉形态 |
| `r3-30/31/32-1440-*` | 1440 回归对比用（2880×1800） |
| `r3-33/34/35-dark-*` | 深色 390（设置/会话/抽屉） |
| `r3-36/37/40-pending-*` | 待审批会话 / 收件箱 / settle 后的控件 |
| `r3-38/39-320-*` | 320 抽屉 + 往返后关闭 |

## 9.7 复现脚本（`/data/zeokeel/.cache/ui-audit/`）

| 脚本 | 覆盖 | 输出 |
|---|---|---|
| `r3-seed.mjs` | 在空库建 甲（12 轮）/ 乙 | `out/r3-seed.json` |
| `r3-1-f5.mjs` | F5 八种往返/对照 | `out/r3-f5.json` |
| `r3-2-o2.mjs` | **O2（含清键 + 空按 + 拖动 + 拖回 + 预置键）** | `out/r3-o2.json` |
| `r3-3-f6.mjs` | F6（320/390/768 × EN/ZH） | `out/r3-f6.json` |
| `r3-4-lang-skip.mjs` | O4/O5 | `out/r3-lang-skip.json` |
| `r3-5-flash.mjs` | N1 语言闪动时间线 | `out/r3-flash.json` |
| `r3-6-drawer-kbd.mjs` | 抽屉/键盘/历史回归 | `out/r3-drawer-kbd.json` |
| `r3-7-composer.mjs` | 输入框连发 + 404 资源 | `out/r3-composer.json` |
| `r3-8-screens.mjs` | 390 八屏（EN+ZH）+ 设置 9 页 | `out/r3-screens.json` |
| `r3-9-viewports.mjs` | 六档宽度 + 拖拽 + 1440 截图 | `out/r3-viewports.json` |
| `r3-10-dark.mjs` | 深色 + 390 | `out/r3-dark.json` |
| `r3-11-pending.mjs` | 待审批态（最后执行，绝不 Esc/批准） | `out/r3-pending.json` |
| `r3-12-extras.mjs` | 320 往返、语言闪动对照、待审批控件 | `out/r3-extras.json` |

## 9.8 复验后的数据状态

| 会话 | id | 状态 |
|---|---|---|
| 甲（12 轮） | `sess-1795534802150041531` | 12 轮完成态，`12 turns 12 steps · 300 tok` |
| 乙 | `sess-1795534912730020298` | 1 轮种子 + 5 条"r3 send N"（输入回归用） |
| 丙（待审批） | `sess-1795535736807964213` | **仍在等待审批**（未批准、未拒绝），`/inbox` 显示"1 项等待你" |

- 界面语言：**`zh`**（本轮结束时在中文，原始 seed 是 `en`；如需还原成 `en`：设置→外观→English→保存）
- 主题：`跟随系统`（`data-theme=light`）
- 注意：**不要对丙按 Esc**——按 `APPROVAL-SURFACES` L4，Esc 会"拒绝最老审批"，把它消费掉（上一轮就是这么丢的）。

---

# 第四轮复验（轮 5）—— 冻结构建 `8976472`，实例 :9940

- 被测构建：HEAD `8976472 fix: connector switch actually switches, and the settings that did nothing`（其中包含本轮四条：O2 `lastWidth` 比较、N1 `zk.locale` 缓存、N2 favicon、`ui.notifications` / `ui.sessions.showArchived` 接线）
- **提供的实例不可用（重要）**：`data-mobile2/gateway.json` 指向 `:9926`，但该端口**只回答 `/health`**：`/` = 404、`/api/capabilities` = 404、`/favicon.svg` = 404（真 Chromium 打开显示 `404 page not found`），其配置的模型端点 `:9927` 根本不在监听；日志/`gateway.json` 是 00:15:24 写下的（`epoch 58267c131c11980a`），说明网关没能在该端口存活（或端口被别的服务占住）。
- 因此我用**同一冻结构建**自起等价实例：`.cache/bin/zeokeel-conn2`（44 448 856 B，mtime 00:14；其内嵌 bundle `sha256 3f982a3517a4 (6 files, built 2026-11-24T16:14:11Z from web/dist)` —— 与 `data-mobile2` 日志里记录的是**同一个 bundle 哈希**）→ 新数据目录 `data-mobile-r4`，端口 **:9940**（模型桩 :9941），token `6vhYzaWF…`
- 服务端产物：`/assets/index-Due1ibGs.js` **2 439 955 B**、`/assets/index-BM1ILexG.css` **43 296 B**、`/favicon.svg` **374 B**；`index.html` 含 `<link rel="icon" type="image/svg+xml" href="/favicon.svg">`
- 代码核对：`app-shell.tsx` 的 `lastWidth` 比较、`i18n/index.tsx` 的 `zk.locale` 缓存、`toasts.tsx` 的 `enabledRef` 闸门、`sidebar.tsx` 的 archived 过滤都在源码与打包产物里（打包产物片段：`onResizeEnd:e=>{let t=Math.round(e[0]??0);t&&t!==v.current&&(v.current=t,...)}`）
- 方式：真网关 + 真 Chromium，全部数字来自 `page.evaluate`；**三档宽度 × 四类步骤 + 两语言 × 八屏 + 边界值，不抽样**

## 10.1 本轮 5 项逐条证伪

| # | 检查 | 结果 | 证据（数字 + 截图） |
|---|---|---|---|
| **O2** | 清空 `zk.sidebar.width` → 重载 → 分隔条按下-抬起不移动：键必须仍为空；真拖 +80 写入新值；拖回写回旧值；1024/1100/1440 三档；再测 1px 与快速连拖 | **PASS** | 三档完全一致（`out/r4-o2.json`）：清空后 `stored=null, panelW=264` → **空按后 `stored=null`（仍是 null）、panelW 仍 264** → 1px 移动 → `265/265` → 拖 +80 → `345/345` → 拖回 −80 → `265/265` → 快速连拖三次（每次 +20，间隔 120ms）：1024/1100 记录为 `285 → 285（这一次净没动，值未变）→ 305`，1440 为 `285 → 305 → 325`，**每一次 `stored` 都等于当时的 `panelW`（没有出现"宽度没动却写盘"）**；预置 `stored=300` 后空按 → 仍 `300`（未被改写）。`audit-mobile-r4-08-o2-nomove-{1024,1100,1440}` |
| **N1** | 浏览器 en-US + 设置中文：先访问一次建缓存，再 reload 计时；`lang` 与 skip 文案一致；反向同样 | **PASS** | 方向一（en-US 浏览器 + zh 设置）：缓存 `zk.locale=zh`，reload 后**首次采样（704ms）就是 `lang=zh-CN / 跳到主内容`，全程 0 次翻转**；方向二（zh-CN 浏览器 + en 设置）：`lang=en / Skip to main content`，463ms 首采样，**0 次翻转**。冷启动（无缓存）保留一次性代价：1068ms 首帧 `en` → 1290ms 切 `zh-CN`（首帧后 222ms），随后写入缓存。`audit-mobile-r4-10/11`（`out/r4-n1n2.json`） |
| **N2** | `/favicon.svg` 200 且页面有 `<link rel=icon>`；控制台无 404 | **PASS** | `link[rel=icon]` 存在（href `/favicon.svg`，type `image/svg+xml`）；页内 `fetch('/favicon.svg')` → **200, `image/svg+xml`, 374 B, 根元素 `<svg`**；同一页面运行期间 **consoleErrors=[]、badResponses=[]**（上一轮那条 favicon 404 消失）。`audit-mobile-r4-09-favicon`（`out/r4-n1n2.json`） |
| **A** | `ui.notifications`：关掉应用内通知→保存→触发 toast→不得出现；打开→恢复 | **PASS** | 用"审批等待"toast 做对照（`approvalQueued` 必然发）：**ON（row=`true`）**：run 在 300ms 时弹出 1 条 `审批等待：web_fetch`；**OFF（row=`false`）**：同样把 run 停到审批（`notice` 出现、`允许一次` 在），轮询 ~18s **toast 数始终 0**。注：`run_notify.go` 的"完成"通知在"完成的就是当前活跃会话"时会静默，且浏览器客户端从不调用 `foreground.set`，所以完成 toast 不能当对照（我实测 WS 帧里确实没有 `toast.posted`）。`audit-mobile-r4-14/15`（`out/r4-notif4.json`） |
| **B** | `ui.sessions.showArchived`：开/关应改变列表条数（造 archived 会话） | **PASS** | 造法：`settings.set session.<乙>.archived=true` + 触发一次会话事件让读模型重建（`session.list.snapshot` 随后返回 `archived:true`）。1024 侧栏：默认（row 缺失）**1 条（仅甲，乙被隐藏）** → 开关 ON（row `true`）**2 条（甲+乙）** → OFF（row `false`）**1 条**；390 抽屉同样 1 → 2 条。`audit-mobile-r4-01/02/07`（`out/r4-wiring.json`） |

## 10.2 回归（上几轮关键项，全跑）

| # | 检查 | 结果 | 证据 |
|---|---|---|---|
| R1 | 390 八屏无横向滚动（ZH 7 + EN 7 + 待审批屏） | **PASS** | ZH：`scrollW==clientW==390`、`main=390`、hOv=0（perLine 14.8/14.6/8.0/11.5/9.0/5.9/4.6）；EN 同样 390/390（20.8/16.3/22/17.6/16.9/15.5/6.3）；待审批屏 390/390、perLine 10.1。`audit-mobile-r4-31-*`（ZH）、`-32-*`（EN）、`-36`（`out/r4-screens.json`、`out/r4-pending.json`） |
| R2 | 待审批态 | **PASS** | notice `本会话有一条审批等待处理：处理后才能继续发送。`（right 382）、`textarea.disabled=true`（clientW 348）、settle 后 `composer-send.disabled=true`（运行中为停止按钮）、审批卡 342（right 366）、允许键 right 349；`/inbox` "1 项等待你"，无溢出。`audit-mobile-r4-36/37` |
| R3 | 抽屉 280 + mask + Esc/遮罩关闭 + 焦点回汉堡 | **PASS** | 打开 280 / mask 390 `rgba(0,0,0,0.45)` / 覆盖正文；Esc → 关、`activeElement=BUTTON[open-sidebar]`；遮罩 → 关、焦点同样回汉堡。`audit-mobile-r4-16/17/18` |
| R4 | Back/Forward 幂等 | **PASS** | 导航（收件箱）→ 关；Back→`/c/甲` 关；Forward→`/inbox` 关；Back×2→`/c/甲`、`/` 关；抽屉内点会话（通知开）→ `/c/…409504` 关；Back→甲 关；Forward→该会话 关；Back×2→`/` 关。`audit-mobile-r4-19`、`-28/29`（`out/r4-motion.json`、`out/r4-pick.json`） |
| R5 | 断点往返（F5） | **PASS** | 390→1440→390、768→1440→768、320→1440→320、390→1024→390：回到窄屏后一律 `open=false, drawerW=0, covers=false`；"开抽屉→放大→在宽屏点另一个会话→缩回"同样 `open=false`。`audit-mobile-r4-20-*/21/30` |
| R6 | 键盘 Tab→汉堡→Enter→Esc | **PASS** | Tab#1 `跳到主内容`（focus 环 2px），Tab#2 汉堡 `40×40` `打开侧栏`（focus 环）→ Enter 开 280 → Esc 关且焦点回 `open-sidebar`。`audit-mobile-r4-22/23` |
| R7 | 1024 拖拽 | **PASS** | 1024：`panelW 264 → 344（Δ80）`、`stored 344`；拖回 `344 → 264（Δ−80）`、`stored 264`；`hOverflow=0`、无汉堡。`audit-mobile-r4-24-drag-1024` |
| R8 | 深色 + 390 | **PASS** | `data-theme=dark`、`color-scheme=dark`；`body/#zk-main` `rgb(17,17,24)`、composer `rgb(27,27,38)`、drawer section `rgb(31,31,31)`、drawer body/sidebar `rgb(11,11,16)`、抽屉 280、无溢出；恢复"跟随系统"后 light。`audit-mobile-r4-25/26` |
| R9 | 洞察窄屏 | **PASS** | 390：grid `342px` 单列、两表堆叠、表头"调用"100×39 **单行**、日期单行；768：`720px` 单列堆叠；1440：`556px 556px` 并排（x 288/860）；三者 `hOverflow=0`。`audit-mobile-r4-27-insights-{390,768,1440}` |
| R10 | 设置页 picker + 9 页 0 控件被裁 | **PASS** | picker `160×32 @x16`、搜索框在、`.zk-settings__nav` `display:none`、grid 单列 `390px`；下拉 9 项 `maxRight=172`；9 页控件 7–22 个、**被裁 0**、`maxRight ≤ 374`。`audit-mobile-r4-33/34/35` |

## 10.3 结论

**PASS** —— 本轮 5 项（O2 / N1 / N2 / `ui.notifications` / `ui.sessions.showArchived`）全部证伪失败，即全部生效；上几轮关键项回归全部通过，无回归。

## 10.4 新发现 / 仍需留意

### N3（P3，新）通知开关的初始显示与真实行为不一致
- 现象：`ui.notifications` **行不存在**（或值为空串）时，设置→通知页的"应用内通知"开关显示**关闭**，但 `toasts.tsx` 的闸门是 `String(rows[...] ?? 'true') !== 'false'` —— **行为是开**。
- 复现：`settings.set {key:'ui.notifications', value:''}` → 设置页开关 `ant-switch-checked=false`；随后在新会话里发一条 `tool*` 消息 → **仍弹出 `审批等待：web_fetch`**（300ms）。截图 `audit-mobile-r4-38-notif-switch-off`、`-39-notif-switch-off-toast`。
- 建议：设置页 bool 字段同样用 `?? 'true'` 的默认值，或首屏把默认值写回行（这样"看到的开关"与"真实行为"永远一致）。

### N4（环境，非产品缺陷）提供的 `data-mobile2` 实例没有在服务
- 证据：`:9926` 仅 `/health` 200；`/`、`/index.html`、`/api/capabilities`、`/favicon.svg` 全 404；真 Chromium 打开 `/` 得到 `404 page not found`；`/api/capabilities` 带 token 仍 404；其 `chat.baseURL` 指向的 `:9927` 不在监听。
- 影响：如果我按指示直接用它，会得出"整个前端 404"的错误结论。本节全部结论来自**同一冻结构建**的等价实例 `:9940`（同 bundle 哈希 `3f982a3517a4`）。建议下次起实例后自检 `/` = 200、`/favicon.svg` = 200、`/api/capabilities` = 401。

### N5（观察，未定论）会话页偶发"正在连接网关…"卡住
- 现象：00:26–00:31 之间有 2~3 次打开 `/c/:id` 时页面停在 `正在加载… / 正在连接网关…`，侧栏正常；同一时刻 `session.snapshot` 经 WS 直连正常返回。页面上观察到一个页面打开了 **12 条 `ws://…/ws`** 连接（反复重连）。
- 之后同样的操作恢复正常（同一实例、同一会话），未能稳定复现；网关日志只有一次 `listening on`（未重启）。**存疑，建议在真实网络/慢机器上再压一轮**（我怀疑是客户端首屏并发订阅 + 重连导致，但没拿到根因）。
- 我无法判断它是否与本轮的订阅/缓存改动有关（我只在这个实例上观察到），因此**记为待观察、不记为缺陷**；若后续复现，请从"页面同时开多条 WS + 首屏多路 snapshot"入手抓日志。

### 其它观察
- 归档会话在开关关闭时不出现在侧栏（预期），所以"点会话行"的测试要用未归档会话；我第一次用"乙"点不到就是因为它是 archived。
- 待审批运行期间 `composer-send` 会被"停止"按钮取代（`sendDisabled` 读不到），settle 后 `sendDisabled=true`；两种态都已记录。

## 10.5 截图索引（`shots/`，`audit-mobile-r4-*`，共 57 张）

| 文件 | 内容 |
|---|---|
| `r4-seed`、`r4-01-archived-on`、`r4-02-archived-off`、`r4-07-drawer-archived-390` | 归档会话 + `showArchived` 开/关（1024 与 390 抽屉） |
| `r4-03/04/05/06/12/13` | 通知闸门的早期尝试（完成 toast，未触发；保留为过程证据） |
| `r4-08-o2-nomove-{1024,1100,1440}` | **O2**：空按后 `stored` 仍为 null |
| `r4-09-favicon` | **N2**：图标加载 + 控制台无 404 |
| `r4-10/11-n1-*` | **N1**：两个方向的 reload 时间线 |
| `r4-14/15-approval-toast-{on,off}` | **修复点 A**：ON 有 toast / OFF 无 toast |
| `r4-16/17/18-drawer-*` | 抽屉打开 / Esc 关闭 / 遮罩关闭 |
| `r4-19-history`、`r4-28/29-picked*`、`r4-30-widen-pick-shrink` | Back/Forward、抽屉点会话、放大后点会话再缩回 |
| `r4-20-roundtrip-*`、`r4-21-open-widen-pick` | **F5 回归**：断点往返后抽屉保持关闭 |
| `r4-22/23-kbd-*` | 键盘：汉堡聚焦 / Esc 焦点回位 |
| `r4-24-drag-1024` | 1024 拖拽 264→344 |
| `r4-25/26-dark-*` | 深色 390：设置 / 抽屉 |
| `r4-27-insights-{390,768,1440}` | 洞察：堆叠 / 堆叠 / 两列 |
| `r4-31-*`（ZH 七屏）、`r4-32-*`（EN 七屏）、`r4-36-pending-thread`、`r4-37-pending-inbox` | 390 八屏 |
| `r4-33/34/35-settings-*` | 设置选择器 / 下拉 9 项 / 末页 |
| `r4-38-notif-switch-off`、`r4-39-notif-switch-off-toast` | **N3**：开关显示关但有 toast |

## 10.6 复现脚本（`/data/zeokeel/.cache/ui-audit/`）

| 脚本 | 覆盖 |
|---|---|
| `r4-lib.mjs` | 共享：WS RPC（settings/session）、抽屉、建会话、发送、开关、保存 |
| `r4-1-seed.mjs` | 建 甲/乙，归档乙（`settings.set session.<id>.archived`） |
| `r4-2-wiring.mjs` | **B**：`showArchived` 1→2→1（1024 + 390 抽屉） |
| `r4-4-o2.mjs` | **O2**：三档 × 清键/空按/1px/+80/−80/快速连拖/预置键 |
| `r4-5-n1n2.mjs` | **N1** + **N2** |
| `r4-7-notif.mjs` / `r4-8-notif-approval.mjs` | 通知闸门（完成 toast 尝试 → 审批 toast 对照） |
| `r4-9-motion.mjs` | 抽屉/历史/往返/键盘/1024 拖拽/深色/洞察 |
| `r4-10-pick.mjs` | 抽屉点会话 + 放大后点会话再缩回 |
| `r4-11-screens.mjs` | 390 八屏（ZH/EN）+ 设置 9 页 |
| `r4-12-pending.mjs` | 待审批屏（最后执行） |

## 10.7 复验后的数据状态

| 会话 | id | 状态 |
|---|---|---|
| 甲 | `sess-1795537230501663100` | 完成态，26 条消息 |
| 乙（archived） | `sess-1795537238388368621` | `archived=true`，开关关闭时侧栏隐藏 |
| 通知开/通知关 | `sess-1795538195261409504` / `sess-1795538206582339135` | 审批已拒绝（各 2 条消息） |
| 丁（待审批） | `sess-1795538587484882923` | **仍在等待审批**（保持不批准） |
| 其它 | 若干 untitled（WS 建的） | 空会话 |

- 设置行：`ui.notifications=true`、`ui.sessions.showArchived=false`、`ui.language=zh`、`chat.model=acceptance-tool`；主题 `跟随系统`（light）。
- 实例：我在 `:9940` 起的（后台任务 `bash-2a78a2682891455c`）；数据目录 `data-mobile-r4`。**要复查请用它**（或按 N4 的建议自检新实例的 `/`）。
