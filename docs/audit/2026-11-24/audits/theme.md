# 主题单真源修复 — 独立验收报告

**结论：PASS（8 项清单全过；另有 2 项「深色特有可读性」缺陷，经修复前构建对照证明**不是**本次修复引入的回归，按清单第 6 条归入"范围外/需另开单"；1 项首帧白闪作为残留风险记录）**

- 验收对象：`http://127.0.0.1:9880`（ZeoKeel serve，`web/dist` bundle **sha256 4f2b901580bd**，built `2026-11-24T14:22:18Z`）
- 修复前对照构建：`/data/zeokeel/.cache/bin/zeokeel-audit2` 起在 9860（bundle **sha256 b3b872b42d21**，built `2026-11-24T11:57:24Z`）
- 浏览器：Playwright chromium 1243，viewport 1440×900，locale zh-CN；每一项都是真网关 + 真浏览器，未改任何 `web/src` 文件
- 脚本与原始数据：`.cache/ui-audit/th-*.mjs`（探针）、`.cache/ui-audit/out/*.json`（逐项原始 computed 值）
- 截图：`.cache/ui-audit/shots/audit-theme-*.png`（45 张）

---

## 一、清单表

| # | 检查 | 结果 | 证据（截图 / computed 值） |
|---|---|---|---|
| 1 | OS 浅色 + 设置深色 | **PASS** | `html[data-theme]=dark`、内联 `color-scheme:dark`、`--zk-bg-base:#111118`；`body` bg `rgb(17,17,24)`（非白）；16 个选择器逐项见 §二。截图 `audit-theme-os-light-set-dark-{now,thread,insights,team,settings-model,settings-appearance,dropdown}.png`、`audit-theme-6-dark-{now,thread,insights,team,settings-model,dropdown,modal}.png` |
| 2 | OS 深色 + 设置浅色 | **PASS** | `html[data-theme]=light`；`body` bg `rgb(255,255,255)`、`.zk-sidebar` `rgb(247,248,250)`、`.ant-menu` `.ant-table` `rgb(255,255,255)`；6 屏扫描中**不透明文字无一处 <4.5**（low=0）；半透明 antd 默认次要文字/placeholder 的低对比项（1.84–3.36）与深色下同一批、浅色下更差，属 antd 默认色而非本项问题（明细 §三）。截图 `audit-theme-os-dark-set-light-*.png` |
| 3 | 跟随系统 | **PASS** | 设置「跟随系统」保存回显后：OS=light 的 context → `data-theme=light`（body 白、`--zk-bg-base:#fff`、`matchMedia` false）；OS=dark 的 context → `data-theme=dark`（body `rgb(17,17,24)`、sidebar `rgb(11,11,16)`）。同一页面 `emulateMedia({colorScheme:'dark'})` → 600ms 内 `data-theme=dark`、`--zk-bg-base:#111118`；再回 light → `data-theme=light`。截图 `audit-theme-3-system-oslight.png` / `-oslight-emulated-dark.png` / `-osdark.png`，数据 `out/item3.json` |
| 4 | 首帧不闪（媒体兜底） | **PASS**（按条目定义：兜底生效 + 属性出现；残留白闪见 §D3） | 拦住 `assets/index-*.js` 使 `data-theme` 永不写入：OS dark → `body` bg `rgb(17,17,24)`、`--zk-bg-base:#111118`、`color-scheme:dark`（整屏 `#111118`，截图 `audit-theme-4-firstpaint-dark.png`）；OS light → `body` bg `rgb(255,255,255)`、`--zk-bg-base:#fff`（`audit-theme-4-firstpaint-light.png`）。hydration 后两帧：`data-theme` 存在且 = light/dark，body bg 与 token 同步（`audit-theme-4-hydrated-{light,dark}.png`）。注：`document.documentElement` 的 computed `background-color` 两帧都是 `rgba(0,0,0,0)`（html 本身无背景规则，画布由 body 传播），故以 body/`--zk-bg-base` 取证 |
| 5 | 浅色不回归 | **PASS** | 设置浅色 + OS 浅色：`body` `rgb(255,255,255)` ✓、`.zk-sidebar` `rgb(247,248,250)` ✓、`.zk-topband__title` `rgba(0,0,0,0.88)` ✓（三项与清单期望逐字相符）；`.ant-menu`/`.ant-sender`/`.zk-approval`/`.ant-table` 全 `rgb(255,255,255)`、`.ant-table-thead th` `rgb(250,250,250)`、`.ant-btn-primary` `rgb(13,68,246)`。与修复前构建同项逐值相同（body 255,255,255 / sidebar 247,248,250 / body 文字 rgb(15,17,21)）。截图 `audit-theme-5-light-{now,thread,insights,settings}.png` |
| 6 | 深色下可用性（5 屏 + 对比度） | **PASS**（有两项深色特有低对比，经对照证明非回归，见 §D1/D2） | 5 屏截图 `audit-theme-6-dark-now.png` `/thread` `/insights` `/team` `/settings-model`（+`-dropdown`、`-modal`）；逐屏扫描（含半透明文字按 alpha 合成）：仅 2 处深色特有 <4.5:1，其余 <4.5 项为 antd 默认色，浅色下同样低（更差）。明细见 §三 |
| 7 | antd 与 CSS 明暗一致 | **PASS** | dark：`body` bg `rgb(17,17,24)` lum 0.0059 / `.ant-menu` `rgb(20,20,20)` 0.007 / `.ant-input`（effectiveBg）`rgb(20,20,20)` 0.007 / `.ant-table` 0.007 / `.ant-layout-sider` `rgb(0,21,41)` 0.007 —— 全暗；light：body 1.0 / menu 1.0 / input `rgb(255,255,255)` 1.0 —— 全亮。修复前构建同一组合是 `body rgb(255,255,255)` + `.ant-menu rgb(20,20,20)`（清单描述的"body 白、菜单黑"割裂），现已消失。`.ant-card` 全应用 0 个（见 §D5），无该面可测 |
| 8 | 切换即时生效（不刷新） | **PASS** | 设置页浅色状态 → 点「深色」radio（`data-theme` 仍 `light`，草稿不落地）→ 保存 → **817ms** 后 `body` 变 `rgb(17,17,24)`、`data-theme=dark`、`--zk-bg-base:#111118`，全程未刷新（`pageReloaded=false`）；随后 `page.reload()` 仍为 dark（`settings.json` 已落盘 `ui.theme.mode=dark`）。截图 `audit-theme-8-settings-light.png` → `-dark-after-save.png` → `-dark-after-reload.png` |

> 说明：主题切换的落地路径是「保存 → `settings.set` → settings 域 2s 轮询回声 → `useSystemAppearance` 改值 → 写 `data-theme`」，所以延迟上限 2s，实测 0.8s，非"点击即变"。这是 TRANSPORT §6.3 的既有契约（settings 无推送），不属本项缺陷。

---

## 二、§1 逐选择器 computed 值（OS 浅色 + 设置深色，`/`、`/c/:id`、`/insights`、`/team`、`/settings/*`）

| 表面 | 选择器 | computed（dark） |
|---|---|---|
| 页面底 | `body` | bg `rgb(17, 17, 24)`；color `rgb(249, 250, 251)` |
| 侧栏 | `.zk-sidebar` | bg `rgb(11, 11, 16)`；color `rgba(255,255,255,0.85)`；border-inline-end `rgba(255,255,255,0.10)` |
| 侧栏宿主 | `.ant-layout-sider` | bg `rgb(0, 21, 41)`（antd 经典暗蓝，被 `.zk-sidebar` 同尺寸全覆盖：两者 bounding box 均为 0,0,264×900，无缝隙） |
| 顶栏 | `.zk-topband` | bg `rgba(0,0,0,0)`（透明）；实际可见底 = 祖先 antd Layout `rgb(0,0,0)`；color `rgba(255,255,255,0.85)` |
| 顶栏标题 | `.zk-topband__title` | color `rgba(255, 255, 255, 0.85)` |
| 侧栏导航 | `.ant-menu` / `.ant-menu-item` | bg `rgb(20, 20, 20)`；item color `rgba(255,255,255,0.85)` |
| 用户气泡 | `.ant-bubble-content-filled` | bg `rgba(255, 255, 255, 0.12)`（合成后 `rgb(46,46,52)`，底 `#111118`；字 12.5:1）；color `rgba(255,255,255,0.85)` |
| 助手气泡 | `.ant-bubble-content-borderless` | bg 透明（底 `#111118`）；color `rgba(255,255,255,0.85)` |
| 输入框容器 | `.ant-sender` | bg `rgb(27, 27, 38)`；border `rgba(255,255,255,0.16)`；文本域 color `rgba(255,255,255,0.85)`（实际打字后实测，`audit-theme-composer-typed-dark.png`）；placeholder `rgb(163,171,187)`（对该底 7.39:1） |
| 输入框（antd） | `.ant-input` | 无底色，effectiveBg `rgb(20, 20, 20)`；color `rgba(255,255,255,0.85)` |
| 卡片 | `.ant-card` | **全应用 0 个**（9 条路由逐个计数为 0，见 §D5）；最接近的"卡片类"面：`.ant-modal` 文本 `rgba(255,255,255,0.85)` + 其内 `.ant-input` bg `rgb(20,20,20)`（`audit-theme-6-dark-modal.png`「添加模型」弹窗） |
| 下拉面板 | `.ant-select-dropdown` | bg `rgb(31, 31, 31)`；item color `rgba(255,255,255,0.85)`；选中项 bg `rgba(0,0,0,0)`（`audit-theme-6-dark-dropdown.png`） |
| 表格 | `.ant-table` / `thead th` / `td` | `rgb(20,20,20)` / `rgb(29,29,29)` / 透明（底 `rgb(20,20,20)`）；文字 `rgba(255,255,255,0.85)` |
| 审批卡 | `.zk-approval` | bg `rgb(27, 27, 38)`；color `rgba(255,255,255,0.85)` |
| 设置页 label | `label.zk-field__label` | color `rgb(249, 250, 251)`；同页 `h2.zk-settings__section-title` `rgba(255,255,255,0.85)` |
| 提示文字 | `.zk-composer__hint` | color `rgb(163, 171, 187)`（底 `rgb(17,17,24)`，对比 8.15:1） |

token 侧同时读出：`--zk-bg-base:#111118`、`--zk-bg-sidebar:#0b0b10`、`--zk-label-primary:#f9fafb`、`--zk-accent:#3cbbfa`、`--zk-on-primary:#0f1115`；`html` computed `color-scheme: dark`（内联 `style.colorScheme=dark`）。

---

## 三、§6 深色对比度扫描明细（含分类）

扫描口径：所有叶子文本节点 + 输入框 placeholder，前景色按 alpha 合成到实际背景，按 WCAG 相对亮度算比值（<24px 用 4.5:1）。

| 屏 | <4.5:1 项 | 比值 | 声明色 / 实际底 | 分类 |
|---|---|---|---|---|
| `/insights`、`/team`、`/settings/*` | 侧栏选中项文字（「洞察」「团队」「设置」「模型」「外观」） | **3.51** | `rgb(54,162,216)` on `rgb(32,70,89)` | **深色特有**；修复前构建同值为 3.51 → 非本次回归（见 §四） |
| `/team` | 主按钮「新建队友」 | **2.87** | `rgb(255,255,255)` on `rgb(54,162,216)` | **深色特有**；修复前同值 2.87 → 非本次回归；但与品牌断言冲突（`internal/brand/palette.go:235` 注释「#3CBBFA … 8.7:1 with ink text」，`OnPrimary = HexInk #0F1115`）；按品牌色算应为 **6.58**（`#0f1115` on `#36a2d8`） |
| `/team` | 「还没有活动。」 | 2.25 | `rgba(255,255,255,0.25)` on `rgb(20,20,20)` | 范围外：antd 默认 `colorTextQuaternary`；**浅色下更差 1.84** |
| `/settings/model` | 搜索框 placeholder「搜索设置页」 | 2.25 | antd placeholder | 范围外：antd 默认；浅色 1.84 |
| `/settings/model` | 「保存」「放弃更改」（禁用态） | 2.24 | `rgba(255,255,255,0.25)` | 范围外：禁用控件不受 AA 约束；浅色 1.84 |
| 全部屏 | antd 次要说明文字 `rgba(...,.45)`（如「来自网关设置的实时快照」） | 3.36(dark) / 3.36(light) | antd `colorTextDescription` | 范围外：两外观同为 3.36，与本次修复无关 |
| `/`、`/c/:id` | 输入框 placeholder | 7.39（dark）/ 6.07（light） | — | 合规（无需处理） |
| `/`、`/c/:id` | 未读徽标「1」 | 4.24 | `white` on `rgb(220,68,70)` | 范围外：浅色下同值，antd Badge 默认 |

深色下去掉对比度仍**可读**的表面（已截图确认）：审批卡、用户/助手气泡、输入框、下拉面板、表格、弹窗、设置页 label —— 未见白底黑字残留或"隐形文字"。

---

## 四、与修复前构建的对照（证明"这些低对比不是本次修复造成的"）

用冻结的旧构建 `zeokeel-audit2`（bundle `b3b872b42d21`，11:57Z）在 9860 起独立实例，同一 data 目录只放 `ui.theme.mode`：

| 场景 | 修复前（9860） | 修复后（9880） |
|---|---|---|
| OS 浅色 + 设置**深色** | `data-theme` **不存在**；`body rgb(255,255,255)` + `.ant-menu rgb(20,20,20)` + `.ant-table rgb(20,20,20)`（白页面配黑表格的割裂，即 SYS-01）；截图 `audit-theme-prefix-darksetting-oslight.png` | `data-theme=dark`；`body rgb(17,17,24)` + `.ant-menu rgb(20,20,20)` → 一致 |
| OS 深色 | token 走媒体查询变暗，antd 也暗 | `data-theme=dark`，一致 |
| 设置**浅色** + OS 浅色 | antd 仍**恒为暗**（`.ant-menu/.ant-table rgb(20,20,20)`，主题设置完全没被消费） | antd 随 appearance 变浅（`rgb(255,255,255)`）—— 这是修复本身，不是回归 |
| 侧栏选中项 / 主按钮 | `rgb(54,162,216)` on `rgb(32,70,89)` = 3.51；白 on `rgb(54,162,216)` = 2.87 | **完全相同**（3.51 / 2.87） |

结论：§D1/§D2 两个深色低对比项在修复前就存在（属 antd darkAlgorithm 的派生色 + 默认 `colorTextLightSolid`），本次修复既没引入也没改善；修复前更严重的问题（CSS/antd 割裂、主题设置不被消费）已被本次修复消除。

---

## 五、缺陷 / 残留（均不影响本项 PASS 判定，但应记录）

### D1（非回归，建议另开单）深色主按钮白字 2.87:1
- 现象：深色下 `.ant-btn-primary` 底 `rgb(54,162,216)`、字 `rgb(255,255,255)`，对比度 2.87（AA 要求 4.5）。`/team`「新建队友」最直观（截图 `audit-theme-6-dark-team.png`）。
- 复现：设置→外观→主题=深色 → `/team`。
- 期望：深色 accent 上的文字用品牌 `OnPrimary`（`#0F1115`）→ 6.58:1（`internal/brand/palette.go:235-239` 明确写「8.7:1 with ink text」）。
- 建议修法：`web/src/design/theme.ts:16-19` 的 `token` 里补 `colorTextLightSolid: onPrimary[appearance]`（并让 `web/scripts/gen-tokens.mjs` 导出 `onPrimary`，取自 `internal/brand/palette.go` 的 `OnPrimary`，禁止手写 hex）。
- 另注：antd darkAlgorithm 把种子 `#3cbbfa` 派生成实测 `rgb(54,162,216)`（≠ 品牌值），若要严格"单真源"，还需显式覆盖相关派生 token（或接受 antd 派生并在 token 里同步该值）。

### D2（非回归，建议另开单）侧栏选中项 3.51:1
- 现象：`.ant-menu-item-selected` 文字 `rgb(54,162,216)` on 底 `rgb(32,70,89)` = 3.51；浅色下同一项为品牌蓝 on 淡蓝底，通过。
- 期望：深色选中项文字 ≥4.5。若用品牌 `--zk-accent #3cbbfa` 于该底为 **4.65**（刚过），或把 `itemSelectedBg` 压到 `--zk-bg-selected` 一档。
- 建议修法：`web/src/design/theme.ts:13-21` 加 `components: { Menu: { itemSelectedColor: accent[appearance], itemSelectedBg: … } }`（配 `--zk-bg-selected` 语义），或改造 antd 的 `colorPrimaryBg` 派生。

### D3（残留风险，清单第 4 条的边界）设置≠OS 时首帧白闪
- 现象：设置深色 + OS 浅色，冷启动（首次带 `#token`）白底共 22 帧、末白帧 t≈899ms；暖刷新（带 cookie）白底 13 帧 306→489ms、513ms 转暗，即**约 180ms（暖）/ 860ms（冷）的白底闪烁**后才变暗。OS=dark 时 208/208 帧全暗、首帧即暗（无闪）。
- 成因：媒体兜底按 OS 取值，而用户选择的值要等 WS settings 回声，故"设置≠OS"这一路必然先按 OS 画一帧。
- 期望（若"首帧不闪"要覆盖该场景）：首帧即按已保存的选择上色。
- 建议修法：`web/index.html` 加内联首帧脚本（localStorage 缓存上次 resolved appearance，先写 `document.documentElement.dataset.theme` + `style.colorScheme`，`App` 的 `useEffect` 再校正），或由网关把已存主题值注入 index.html 模板。同时可顺手把 `.cache` 里的 appearance 与 `ui.theme.mode` 对齐。

### D4（轻微不一致）pre-hydration 的 `color-scheme` 在 OS 浅色下算出 `light dark`
- 现象：拦住 JS 后 OS 浅色时 `getComputedStyle(html).colorScheme === "light dark"`（不是 `light`），OS 深色时为 `dark`。原因：`web/src/design/global.css:7-11` 的 `:root { color-scheme: light dark }` 在源码顺序上晚于 `tokens.css` 的 `:root{color-scheme:light}`，覆盖了后者。
- 影响：仅影响 hydration 前的原生控件/滚动条着色；此时页面本来就按 OS 显色，二者自洽，肉眼无差。hydration 后 `App` 写内联 `style.colorScheme` 即为精确值（实测 `dark`/`light`）。
- 建议：把兜底改成与媒体块一致（OS 深色才写 `dark`，否则 `light`），或让 tokens.css 的块排在其后，保持"tokens.css 是唯一 color-scheme 来源"。

### D5（清单与实际组件不符，需修清单）
- `.ant-card` 在全应用 **0 个实例**（`/`、`/inbox`、`/c/:id`、`/insights`、`/team`、`/capabilities`、`/automation`、`/settings/model`、`/settings/appearance` 逐路由计数，`web/src` 里也没有任何 `Card` 引用）。故"卡片"这一表面只能以 `.ant-modal`（弹窗）+ `.zk-approval`（审批卡）替代取证，不能算"没验"。

---

## 六、我还建议补的验收点

1. **首帧**：明确"设置≠OS"是否也要不闪（现在 180ms/860ms 白闪）；若要，按 §D3 修后加一条"首帧颜色 = 已存设置"的断言（可在 `page.addInitScript` 里采样 `performance.now()` 与 body bg 的序列，本次脚本 `th-item4b.mjs` 可直接复用）。
2. **系统偏好变化 × 用户显式选择**的竞态：本次验证了"跟随系统"下 `emulateMedia` 会跟随；还应验"设置为深色时把 OS 切到浅色不会把页面带浅"（settings 2s 轮询与 media 事件谁后到）。
3. **`color-scheme` 的可见后果**：深色下抓滚动条 / 原生 select / 输入光标（caret 实测 `rgb(54,162,216)`）截图，确认不是"页面暗、控件亮"。
4. **跨外观的一致性回归**：把 §二 的 16 个选择器做成生成物级快照（当前 `npm run test` 里没有这类断言），否则下次 token 改动无机器保护。
5. **多客户端/第二窗口**：手动改 `settings.json` 的 `ui.theme.mode` 是否在 2s 内同步到已打开窗口（本次实测外部改文件能被轮询看到，见 §四 的 9860 实验）。
6. **双语与窄视口**：深色下 390px 抽屉侧栏（`zk-sidebar-drawer` 用 `--zk-bg-sidebar`）与英文长文案换行未覆盖。
7. **打印 / `forced-colors: active`**：深色 token 在 Windows 高对比度与打印样式下的表现未验。