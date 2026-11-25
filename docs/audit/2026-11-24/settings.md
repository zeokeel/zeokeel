# 设置（9 页）与收件箱 审计

- 覆盖：`/settings/{model,behavior,appearance,notifications,network,lsp,archive,data-privacy,about}`（1440×900 各 1 张）、`/inbox`（1440 + 390）、设置导航（1440×660 滚到底）、保存栏 clean/dirty/空值、非法 JSON / 非法 URL、模型目录（空态→添加→校验错→列表→删除确认）、深色主题、英文界面（appearance/about/inbox/其余 7 页）。
- 截图：55 张（`shots/settings-*.png`）。量测脚本：`.cache/ui-audit/settings-1-pages.mjs` … `settings-12-dark.mjs`，原始数字在 `settings-1-pages.json`、`settings-3b.json`、`settings-5-en.json`、`settings-10-final.json`、`settings-12-dark.json`。
- 服务器：9860 只读基线（未改任何状态）；9874 专属实例（改字段/保存/切语言/切主题/造脏状态都在这里）。

## 结论

1. **设置页整体是「一个 Page + 九个 h1 + 一堆 div 行」，没有 antd `Form`**：主表单零校验、零错误态，非法 JSON（`{not json`）与非法 URL（`not a url`）都能「已保存」（`settings-bad-json.png`、`settings-bad-url.png`）。同一个页面里，只有「添加模型」弹窗是规范 antd Form（红框 + `请输入名称`）——两套表单语言并存。
2. **字段行的解剖与本项目自己的契约定相反**：DESIGN §「设置表单行 · `uikit.SwitchRow`」要求「标签在节左缘、开关在行尾、描述对齐节左缘」，web 实现是「标签 + 说明同一行（13px + 12px）、控件在下一行、开关在标签下方」。9 页里 4 个开关占 240px、5 个数字框占 375px，密度约为 macOS/VS Code 设置行的 1/3。
3. **命名重复到处可见**：「关于」既是分组名又是页名（导航里上下相邻各一次）；「语言」「主题」「Agent 预设」「模型」都出现 2–4 次（section 标题 + 字段 label + 页标题 + 导航项）。搜索框恰好放大了这个问题：搜「模型」命中 4 处不同层级。
4. **两个 P0 是「看起来对、其实是坏的」**：① 主题切「深色」后 antd 走 `darkAlgorithm` 但 ZK token 的深色块只挂在 `@media (prefers-color-scheme: dark)` 上，结果白底白字、整页不可读（`settings-dark-reload.png`）；② 每页底部把网关原始 key 摊给用户，`session.sess-1795522277388119007.unread` 这类会话 id 在 9 页里各出现一次（`settings-1440-about.png`）。
5. **收件箱把审批降级成一行 JSON**：标题就是 `{"url":"http://127.0.0.1:9861/health"}`，没有会话归属、没有影响面、没有「始终允许」；390 下按钮直接压住标题（`settings-inbox-390.png`）。与线程内审批卡（`来自会话：… · 用户` / `这会向外发出网络请求。` / 原始参数折叠）相比，同一张审批在两处是两个产品。

---

## 发现

### S01 切换语言后设置导航仍是旧语言，中英混排 [P1]

- 现象：设置右侧内容全部变英文（`Appearance / Language / Theme / Other settings…`），但左侧设置导航的 5 个分组名与 9 个页项仍是中文（`常用 / 界面与通知 / 连接 / 数据 / 关于`、`模型 / 行为与限额 / 外观…`）；应用侧栏（`Inbox / Capabilities` 已英文化）里的会话标题也仍中文（后者是数据，可接受）。
- 证据：`shots/settings-en-appearance.png`、`shots/settings-en-about.png`；量测 `settings-5-en.json` → `enAppearance.navGroup = ["常用","界面与通知","连接","数据","关于"]`、`enAppearance.navItems = ["模型","行为与限额",…]`，而 `enAppearance.title = "Appearance"`、`sections = ["Language","Theme","Other settings (stored by the gateway, not wired yet)"]`。
- 代码：`web/src/features/settings/settings-screen.tsx:88-116` — `navItems` 在 `useMemo(..., [pages, matches])` 里调用 `t(...)`，**依赖数组漏了 `t`**；语言切换时 `pages`/`matches` 引用不变 → memo 不重算。boot 时语言先按 zh 渲染，快照到达后翻成 en，内容区重渲染而导航保持 zh。
- 规范：DESIGN §"一屏最多 4 种字号 / 文案唯一来源"；antd 6 `Menu` 无此约束（问题在 memo 依赖）；同类问题也应查 `web/src/shell/sidebar-nav.tsx`（同屏中英混排）。
- 建议：`useMemo` 依赖加 `t`（或把 `t` 的 locale 作为 key）；并把「导航文案跟随语言」写进 `ia.test.ts` 的断言（现有 `ia.test.ts` 只测结构）。

### S02 分组名「关于」与页名「关于」重复；组标题与页项字号相同 [P1]

- 现象：导航最后一组标题是「关于」，组内页项也叫「关于」，两者上下相隔 42px 且视觉几乎同权（14px vs 14px，仅颜色 0.45 vs 0.88）；用户读到的是一份 10 行的列表，第一列「关于」不知道是标题还是可点项。
- 证据：`shots/settings-nav-bottom.png`、`shots/settings-1440-about.png`；量测 `settings-1-pages.json` → `navGroups[4] = {text:"关于", x:272, y:613, w:202, h:38}`，`navItems[8] = {text:"关于", x:276, y:655, w:194, h:40}`；`settings-5-en.json` → 组标题 `font:14px, color:rgba(0,0,0,0.45), weight:400`，页项 `font:14px`。
- 代码：`web/src/features/settings/ia.ts:99` — `const ABOUT = { key:'about', labelKey:'settings.ia.about', rank:90 }`，与 `ia.ts:322-323` 的 page `labelKey:'settings.ia.about'` **用的是同一个 MessageKey**（`web/src/i18n/zh.ts:424/478` 也都指向「关于」）。
- 规范：antd 6 `Menu` 的 `type:'group'` 语义区隔要求视觉可辨（`semantic Menu` → `item-group-title` 与 `item` 分属不同层级）；DSH 侧栏分组用 `SectionLabel()`（TypeXXXS + LabelTertiary）与导航项明确不同字号。
- 建议：① 分组改用「更多」或直接取消该组（9 页里 5 组，最后一组只有 1 页，属过度分组）；② 组标题降到 `--zk-type-xxxs`(11) + `--zk-label-tertiary` + 大写/字距，与 14px 页项拉开两档；③ 页项加 `aria-current`。

### S03 顶栏「设置」与正文 h1「模型」是两级标题，但都不承担层级职责 [P1]

- 现象：顶栏一条 14px/400 的「设置」（x=280），下方内容区一条 20px/600 的「模型」（x=508）。同屏两处标题，左缘相差 228px，中间没有面包屑或返回关系；「设置」既不是窗口标题也不是当前页，用户无法从标题判断自己在哪一层。关闭 X 在内容区右上（x=1392），离「模型」很远，也离「设置」很远。
- 证据：`shots/settings-1440-model.png`；量测 `settings-10-final.json` → `topband {x:264,w:1176,h:49,pad:"0px 16px"}`、`topbandTitle 设置 {x:280,h:48,font:14px/400}`；`contentTitle 模型 {x:508,y:65,w:154,h:28,font:20px/600}`；`searchInput {x:272,y:61,w:203,h:24}`（顶栏底 49，搜索框顶 61）。
- 代码：`web/src/features/settings/settings-screen.tsx:263-274`（内容区 h1 + 关闭按钮）；顶栏文案来自路由标题（`.zk-topband__row`），与 `ia.ts` 无关。
- 规范：DESIGN §"TypeL 20/28/600 = 设置页/对话框 H1"（只应有一个 H1）；macOS 系统设置把页名放侧栏选中项 + 窗口标题，正文不再重复 h1；VS Code 设置用「搜索 + 单列设置行」不设页 H1。
- 建议：二选一——① 顶栏改成面包屑 `设置 › 模型`（`antd Breadcrumb`），正文 h1 删掉，只留副标题行；② 顶栏留「设置」，正文 h1 保留但把关闭 X 移到顶栏右端，与滚动的 body 解耦。

### S04 三列宽度与内距不成体系（264 / 220 / 956，label 行 908 vs 控件 720 vs 90）[P1]

- 现象：应用侧栏 264 → 设置导航 220（内距 12/8，右 1px 分隔线）→ 内容列 956（页头内距 16/24/8、body 8/24/24、页脚 8/24）。导航右边框到内容文字之间只有 24px，而导航自身的左右内距是 8/8 —— 分隔线两侧的呼吸不一致。更明显的是字段内部：label 行宽 **908**、控件 `max-width: 720`、数字控件 **90**，三种宽度在同一列里叠着，右侧留白参差 188px。
- 证据：`shots/settings-1440-appearance.png`、`shots/settings-detail-language-row.png`；量测 `settings-1-pages.json` → `nav {x:264,w:220}`、`page {x:484,w:956}`、`bodyPad "8px 24px 24px"`、`headerPad "16px 24px 8px"`、`footerPad "8px 24px"`、`navBorder {1px rgba(15,17,21,0.09)}`、`nav {padding:"12px 8px"}`；`settings-10-final.json` → `labelRowW.w = 908`、`contentX.w = 908`、`.zk-field__control maxWidth 720`。
- 代码：`web/src/design/global.css:827-835`（nav）、`:843-850`（header）、`:851-860`（body）、`:875-884`（footer）、`:896-900`（label-row 无 max-width）、`:913-918`（control max-width 720）。
- 规范：DESIGN §9.4「控件高度只用 28/32/36/40/44」的同一条纪律要求「一套间距阶梯」；DSH `FontSizeRow.module.css` 用 `padding: 16px 0` + 行内 `gap: 8` 的单一节奏，不给 label 行和控件两套宽度。
- 建议：给 `.zk-field` 加统一的 `--settings-row-max: 720px`，label 行与控件都用它（label 行 `max-width` 由容器继承）；导航左右内距改 12/12，让分隔线两侧对称。

### S05 搜索框与多个按钮是 24px 高，不在 28/32/36/40/44 阶梯上 [P2]

- 现象：设置导航搜索框 `size="small"` → 高 24；模型行的「设为默认/编辑/删除」、收件箱的「拒绝/允许一次」也都是 24（`ant-btn-sm`）。同一屏里 24 与 32 两种控件高度混用。
- 证据：量测 `settings-10-final.json` → `searchInput {h:24}`、`smallBtns [{t:"",h:24,size:"ant-btn-sm"},{t:"添加模型",h:32},{t:"放弃更改",h:32},{t:"保存",h:32}]`；`settings-8-inbox2.json` → 收件箱按钮 `{h:24}`；截图 `shots/settings-detail-model-section.png`。
- 代码：`web/src/features/settings/settings-screen.tsx:121-129`（`size="small"`）、`web/src/features/settings/model-catalog.tsx:145-159`（`size="small"`）、`web/src/features/inbox/inbox-screen.tsx:139-144`（`size="small"`）。
- 规范：DESIGN §9.4「控件高度只用 28 / 32 / 36 / 40 / 44（AGENTS.md §3.13）」。
- 建议：统一 `size="middle"`（32）或给 antd 主题加 `controlHeightSM: 28`（`zkThemeConfig` 里已有 `token`，加一项即可）。

### S06 字段 label 与 section 标题重复（语言/语言、主题/主题、Agent 预设/Agent 预设），「模型」出现 4 次 [P1]

- 现象：外观页第一组标题「语言」，紧接着字段 label 又是「语言」，后面跟 help「界面语言；缺失时跟随浏览器。」→ 视觉读作「语言 语言 界面语言；缺失时跟随浏览器。」；主题组同理「主题 主题 浏览器端随系统外观…」。模型页 section「Agent 预设」+ label「Agent 预设」+ help 连成一句「Agent 预设 JSON：预设列表…」。「模型」同时出现在：导航项、页 h1、section「模型服务」、字段 label。
- 证据：`shots/settings-detail-language-row.png`（一张图就能看到双 label）、`shots/settings-1440-appearance.png`、`shots/settings-1440-model.png`；量测 `settings-1-pages.json` → `appearance.sections = ["语言","主题",…]` 且 `fields[0].label = "语言"`、`fields[1].label "主题"`；`model.fields[0].label = "Agent 预设"` 而 `model.sections[0..2] = ["模型目录","Agent 预设","模型服务"]`。
- 代码：`web/src/features/settings/ia.ts:182`（`titleKey:'settings.ia.language'`）+ `:186`（`radio(K.uiLanguage, 'settings.ia.language', …)`）；`:196` + `:200`（theme 同构）；`:119-120`（section `settings.ia.agentPresets` + field 同 key）；`:105-135`（模型页三处「模型」）。
- 规范：DESIGN §"设置/表单 → `Form/Field/Fieldset`"（Fieldset 负责分组标题，Field 负责 label，两者不得同文）；antd 6 `Form.Item` 的 `label` 与 `extra` 是「标题 + 说明」两段，不应重复；macOS 系统设置的分组名（如「外观」）与行名（如「外观」下的「浅色/深色」）永不同文。
- 建议：① 单字段 section 直接去掉 section 标题（外观页的「语言」「主题」两组各只有 1 个主字段）；② 或保留 section、把字段 label 改成更具体的主语（「界面语言」「配色方案」），并让 `ia.test.ts` 加一条「section.titleKey ≠ 任一 field.labelKey」断言。

### S07 label 与 help 同行，13px vs 12px、同字重，只靠颜色区分 [P1]

- 现象：每一行的结构是 `[label 13px #000 .88][help 12px #5a6371]` 在同一基线上，控件在下一行。label 与 help 只差 1px 与一个颜色，读起来是一条被空格切碎的长句（S06 的「双 label」正是因此更像口吃）。英文下同样：「Theme  The browser follows the system appearance; the gateway stores what you pick.」
- 证据：`shots/settings-detail-language-row.png`、`shots/settings-detail-en-dataprivacy.png`；量测 `settings-1-pages.json` → `labelFont {13px, weight 400}`、`helpFont {12px, rgb(90,99,113)}`、`helpSameRow: true`（9 页所有带 help 的字段）；`settings-10-final.json` → `sectionTitleFont 14px/500`、`fieldLabelFont 13px/400`。
- 代码：`web/src/design/global.css:896-911`（`align-items: baseline` + label 13 / help 12）；label 的字重取 `--zk-type-xs-weight: 400`（`tokens.css:49`），与 help 同重。
- 规范：DSH `FontSizeRow.module.css` `.rowText { flex-direction: column; gap: 4px }`、`.title {14/22/400 label-primary}`、`.desc {12/18/400 label-tertiary}`——标题与说明**竖向两行**、字号差 2、说明进 tertiary；DESIGN §"TypeXS 13 = 表单标签" 但未要求与说明同行。
- 建议：`.zk-field__label-row` 改 `flex-direction: column; gap: 2px`，label 提为 `--zk-type-body`(14)/500 `--zk-label-primary`，help 保持 12 并 `margin-top: 2px`；同时把 help 用 `aria-describedby` 挂到控件（见 S11）。

### S08 输入宽度三套：720 / 90 / 内容驱动；数字框恒定 90px [P1]

- 现象：文本与 textarea 一律 720 宽（`max-width: 720` + antd 100%）；数字框恒定 90（antd `InputNumber` 默认宽度），里面输入区只有 66；radio 组宽度随文案 128/224/226/292/294 不等；密码框 678（内含眼睛图标）。同一列里 4 种右边界。
- 证据：`shots/settings-1440-behavior.png`（5 个 90px 小方块）、`shots/settings-detail-number-stack.png`、`shots/settings-1440-model.png`；量测 `settings-1-pages.json` → `chat.contextBudget input w=66`、`chat.apiKey w=678`、`chat.model w=720`、`ui.language w=128`、`ui.theme.mode w=224`、`ui.workSteps.mode w=292`、`agent.permission w=294`；`settings-3b.json` → `fontSizeField.wrapW "90px"`、`fontSizeField.input {w:66,h:30}`。
- 代码：`web/src/features/settings/settings-screen.tsx:444-451`（`InputNumber` 未设 `style`/宽度）；`:425-434`（`Input.Password` 内部图标挤压）；无 `formatter/parser`、无 `addonAfter`。
- 规范：DESIGN §"`NumberInput`/`DecimalInput`：所有数字设置必须官方输入 + 默认开关"；DESIGN §9.4 的「一套阶梯」；macOS 系统设置里数值行与文本行的控件右边界一致。
- 建议：给 `.zk-field__control` 内所有控件统一宽度：文本/textarea `100%`、数字 `InputNumber style={{width:120}}` 且 `addonAfter` 放单位（tokens / 秒 / KiB / px）；radio 组保持内容宽但统一左对齐基线。

### S09 数字字段缺单位、缺默认值、缺范围（`上下文预算 (tokens)` / `对话字号`）[P1]

- 现象：「上下文预算（tokens）」把单位塞进 label，值框是空的且没有任何范围提示；「对话字号」既无单位（px？）也无默认值展示；5 个并发/超时字段同理。空值保存后是 `""`，用户不知道「空」= 用内置默认，还是 = 0。
- 证据：`shots/settings-1440-behavior.png`、`shots/settings-1440-appearance.png`；量测 `settings-11-a11y.json` → `number {min:null, max:null, required:null, unit:""}`、`cb {min:"", max:"", step:"1", value:"", wrap:90}`。
- 代码：`web/src/features/settings/settings-screen.tsx:444-451`（`InputNumber` 无 `min`/`max`/`addonAfter`）；`ia.ts:130`、`:164-168`、`:209` 的 `kind:'number'` 均无 `defaultValue`（`SettingsField.defaultValue` 只有 `bool()` 用，`ia.ts:80-86`）。
- 规范：DSH `FontSizeRow` 给数值配 `px` 单位标签（`.unit`）并在 `stepper` 里显示当前值；antd 6 `Form.Item` 的 `extra` 天然放「默认 X / 范围 A–B」。
- 建议：`InputNumber` 加 `addonAfter={t('unit.tokens')}` 等、`min/max` 与 `placeholder={defaultValue}`；`SettingsField` 增 `unitKey?: MessageKey` 与 `defaultValue` 一并渲染到 help。

### S10 开关行解剖违反自家契约：开关在标签下方而非行尾，4 个布尔占 240px [P1]

- 现象：通知页 4 个开关各自「label 一行 + 开关一行」，每行 ~60px；外观页「偏好深色」、LSP「启用语言服务器」、归档「在侧栏显示已归档会话」同样。整页留白巨大，扫描成本高。
- 证据：`shots/settings-1440-notifications.png`、`shots/settings-detail-switch-row.png`、`shots/settings-1440-archive.png`；量测 `settings-1-pages.json` → notifications 4 个字段输入 `{w:44,h:22}`（`BUTTON.ant-switch`）且 y = 202/262/322/382（间距 60），页内除 4 行外无他物。
- 代码：`web/src/features/settings/settings-screen.tsx:452-459`（switch 落在 `.zk-field__control` 即 label 的下一行）；`.zk-field { flex-direction: column }`（`global.css:890-895`）。
- 规范：**DESIGN.md 自己的契约**——"设置表单行 | `uikit.SwitchRow` | 布尔设置行统一解剖：**标签在节左缘、开关在行尾**、描述对齐节左缘（替代 macOS 式右对齐标签列，跨页开关 x 一致）"；DSH `AppearanceRow` 也是标题左、控件右。
- 建议：`switch` 类型走独立行组件：`.zk-field--switch { flex-direction: row; justify-content: space-between; min-height: 44px }`（DESIGN 的 `--zk-row-double-height: 44px` 正好可用），help 放第二行左侧。

### S11 单选组的 `<label for>` 指向不存在的 id；radiogroup 无名字；help 无 aria-describedby [P1]

- 现象：5 个 radio 字段（语言、主题、权限模式、工作步骤、收起时机）都有 `<label for="zk-field-ui.language">`，但页面上**没有任何元素**的 id 等于它——`Radio.Group` 不接受 `id`，`label htmlFor` 成了孤儿；`.ant-radio-group` 有 `role=radiogroup` 但 `aria-label` 为空 → 读屏只念「单选按钮组」；所有字段的 help 文案都没有 `aria-describedby` → 「界面语言；缺失时跟随浏览器。」只存在于视觉层。
- 证据：量测 `settings-11-a11y.json` → `radio {labelFor:"zk-field-ui.language", targetExists:false, groupRole:"radiogroup", groupAria:null, radios:[{id:"", ariaLabelledby:null, ariaDescribedby:null}]}`；`text {ariaDescribedby:null, helpId:""}`；`number {ariaDescribedby:null}`。对比 `switchA11y {labelFor:"zk-field-ui.theme.dark", id:"zk-field-ui.theme.dark"}` —— 只有 switch 的关联是通的。
- 代码：`web/src/features/settings/settings-screen.tsx:407-414`（label htmlFor 对所有 kind 一律生成）、`:460-472`（`Radio.Group` 未传 `aria-label`/`aria-labelledby`）、`:411-413`（help `<span>` 无 id）。
- 规范：DESIGN §"所有表单字段必须用 `Field`（label 与 a11y 绑定）"；antd 6 `Form.Item` 的 `htmlFor` 会自动接到控件，且 `tooltip/extra` 参与 `aria-describedby`；`semantic Form` 明确 `label` 是独立语义节点。
- 建议：不要再手写 `htmlFor`——主表单换成 `antd Form` + `Form.Item`（label/extra/校验一次到位）；若坚持自绘，radio 分支改用 `aria-labelledby={labelId}`，help `<span id={helpId}>` 并给控件 `aria-describedby={helpId}`。

### S12 JSON 文本域用比例字体、禁止手动 resize、无格式提示 [P2]

- 现象：7 个 JSON 字段（行为页 3、LSP 2、数据与隐私 7、模型页 1）都是同一种 textarea：比例字体、`resize: none`、`autoSize {minRows:2,maxRows:8}`、高 54。要手改 JSON 时既不能拉高，也没有等宽对齐，看不出嵌套错位；而「其它设置」里的 key 反而用了 monospace 14。
- 证据：量测 `settings-11-a11y.json` → `textarea {resize:"none", rows:2, height:54, overflowY:"hidden"}`；`settings-10-final.json` → `unmappedFont {14px, monospace}`；截图 `shots/settings-1440-data-privacy.png`。
- 代码：`web/src/features/settings/settings-screen.tsx:435-443`（`Input.TextArea` 无 `style={{fontFamily:...}}`、无 `onResize`）。
- 规范：DESIGN §"`NumberInput` 不许自由文本校验" 同源思路——结构化数据给结构化编辑；antd 6 `Input.TextArea` 支持 `autoSize` 与受控高度，两者可并用。
- 建议：JSON 字段加 `font-family: var(--zk-font-mono)` + `minRows: 4` + 允许 `resize: vertical`；长期方案是把 `presets.agents` / `mcp.servers` / `lsp.servers` 做成与「模型目录」同级的列表编辑器（弹窗内 antd Form 已证明可行）。

### S13 枚举字段用自由文本，非法值被后端静默归一化 [P1]

- 现象：「对话模式」与「Agent 模式」是两个纯文本输入框，用户必须手打内部 token。后端 `chatMode()` 对未知值**静默回落 `standard`**、`agentModePlan()` 只认 `plan`——打错一个字母，保存成功、界面显示你打的值、实际行为没变。
- 证据：`shots/settings-1440-model.png`（「对话模式」空文本框）、`shots/settings-1440-behavior.png`（「Agent 模式」空文本框）；代码 `internal/modules/agent/plan_mode.go:179-193`（`chatMode` switch：`minimal|creator|standard`，`default: return contracts.ChatModeStandard`）、`:195-202`（`agentModePlan` 只匹配 `plan`）。
- 代码（客户端）：`web/src/features/settings/ia.ts:131`（`text(K.chatMode, 'settings.ia.chatMode')`）、`:157`（`text(K.agentMode, …)`）。
- 规范：DESIGN §"`Checkbox`/`Switch`/`Radio`/`RadioGroup`/`Segmented` … 互斥选项用 Radio/Segmented"；"`NumberInput` … 不许自由文本校验" 同理；antd 6 `Select` 是最小惊奇的枚举控件。
- 建议：改成 `radio(K.chatMode, …)` 三项（标准/精简/创作）与 `radio(K.agentMode, …)` 两项（普通/计划）；`ia.ts` 已有 `radio()` 工厂与 `options` 结构，只需补文案 key。若要保留自由文本，至少在 help 里列出合法值并做客户端白名单校验。

### S14 主表单零校验：非法 JSON、非法 URL 都能「已保存」，无 validateStatus/help [P1]

- 现象：把「沙箱可写目录」填成 `{not json` 点保存 → 绿色「已保存」、页脚「已与网关快照一致」、输入框边框保持 `rgb(217,217,217)`、`aria-invalid` 为 null；把「HTTP 代理」填成 `not a url` → 同样「已保存」。用户以为存好了，后端解析时才炸（或永不炸，值一直坏在磁盘上）。
- 证据：`shots/settings-bad-json.png`、`shots/settings-bad-url.png`；量测 `settings-3b.json` → `badJson {notice:"已保存", ariaInvalid:null, fieldHasErrorClass:false, textareaBorder:"rgb(217,217,217)", footer:"已与网关快照一致"}`、`badUrl {notice:"已保存", ariaInvalid:null}`。
- 代码：`web/src/features/settings/settings-screen.tsx:239-254`（`save()` 直接逐个 `setSetting`，无本地校验）、`:385-476`（`FieldRow` 是裸 div，没有任何 `Form.Item` 包裹）。
- 规范：antd 6 `Form.Item` 的 `validateStatus` / `help`（或 `rules`）是官方错误态：红边框 + 红色说明，`Form` 的 `scrollToFirstError` 负责定位；同一页的「添加模型」弹窗就是正确示范（`settings-model-add-errors.png`：`请输入名称`、边框 `rgb(255,77,79)`）。
- 建议：主表单换 `antd Form`（`layout="vertical"`），JSON 字段加 `rules=[{ validator: tryJSON }]`、URL 字段加 `type:'url'`、数字加 `type:'number'`；失败时 `validateStatus="error"` + `help` 并把保存按钮挡在 `form.validateFields()` 之后。

### S15 字段顺序与同义字段分散：外观页 9 项单列、热键被拆成 3 段、通知页 4 开关单列 [P2]

- 现象：外观页 9 个字段在一个 section 里单列排到底，其中「全局热键 / 命令面板快捷键 / 折叠侧栏快捷键」三个同类项被排在「工作步骤展示 / 收起时机」之后；「主题」与「偏好深色」语义重叠却分列两行（两个不同 row key）。通知页把「开机自启」「关闭时最小化到托盘」放进标题为「分级」的 section——这两项与通知等级无关。
- 证据：`shots/settings-1440-appearance.png`、`shots/settings-1440-notifications.png`；量测 `settings-1-pages.json` → appearance `fields` 顺序：`ui.language, ui.theme.mode, ui.theme.dark, ui.chatFontSize, ui.workSteps.mode, ui.workSteps.collapse, ui.globalHotkey, ui.shortcut.commandPalette, ui.shortcut.toggleSidebar`；notifications `sections=["分级"]`，字段含 `ui.autostart`、`ui.closeToTray`。
- 代码：`web/src/features/settings/ia.ts:195-233`（外观单 section 9 字段）、`:241-251`（通知：`titleKey:'settings.ia.verbosity'` → zh 文案「分级」，见 `zh.ts:426`）。
- 规范：DESIGN §"多字段子组用 `Fieldset`"；macOS 系统设置把「外观/强调色/滚动条」分在「外观」组下的独立小节。
- 建议：外观页拆 3 个 section（外观 / 文字与步骤 / 快捷键）；「偏好深色」与「主题=深色」合并（`ui.theme.dark` 是 legacy，`use-system-appearance.ts:8-12` 的注释自己也说它没被消费）；通知页 section 改名「通知」并把托盘两项移到「外壳行为」或网关设置页。

### S16 每页底部把网关原始 key（含会话 id）直接摊给用户 [P0]

- 现象：9 个设置页的末尾都有一节「其它设置（网关有、界面未接线）」，下面是等宽字体的 raw row：`session.sess-1795522277388119007.unread` → `false`、`session.sess-1795522282587895103.unread` → `false`。用户在此页没有任何操作能做，却被展示内部 key 与自己会话的 id；9 页各重复一次，且这一节的标题本身在向用户承认「界面没接线」。
- 证据：`shots/settings-1440-about.png`（3 行）、`shots/settings-1440-notifications.png`、`shots/settings-1440-archive.png`、`shots/settings-en-about.png`；量测 `settings-1-pages.json` → 9 个页面 `pageEmpty` 均以 `其它设置（网关有、界面未接线）session.sess-…` 结尾；`unmapped {x:508,y:899,w:908,h:77}`（模型页正好落在首屏折叠线下，其它页在首屏内）。
- 代码：`web/src/features/settings/settings-screen.tsx:313-334`（`extraRows` 无条件渲染 + `<code>{key}</code>`）、`:148`（`extraRows={unmappedRows(rows)}`）、`ia.ts:386-395`（`unmappedRows` 把 `session.*` 一并算进来）；文案 `zh.ts:194`。
- 规范：DESIGN §"不做未请求的功能 / 不把内部结构暴露给用户"（§3 反模式：给用户看实现细节）；`docs/WEB-CLIENT.md §9` 同源；隐私角度：`session.<id>.unread` 含用户会话标识，属不该出现在只读页面的数据。
- 建议：① 默认隐藏；仅在 `?debug=1` 或开发者设置下渲染；② 过滤掉 `session.*`（会话未读是客户端状态，不该进 settings 汇总）；③ 真要做「界面未接线」报告，应放在「关于/诊断」页的一个折叠区（`Collapse`），并说明「这些设置只能通过配置文件修改」。

### S17 切「深色」主题后设置内容区白底白字，整页不可读 [P0]

- 现象：外观页选「深色」→ 保存（提示「已保存」）→ 刷新后：应用侧栏与设置导航变深，但内容区仍是白底，而所有文字（页标题、section 标题、字段 label、radio 文案、页脚按钮文字）变成 `rgba(255,255,255,0.85)` → 白底白字、完全不可读；radio 只剩黑点；页脚两个按钮变成两个空白白框。只有 help 文案（仍用浅色 token `rgb(90,99,113)`）和 placeholder 可见。
- 证据：`shots/settings-dark-reload.png`、`shots/settings-theme-dark-saved.png`、`shots/settings-dark-detail.png`；量测 `settings-12-dark.json` → `htmlTheme:""`（**根元素没有写任何 data-theme**）、`titleColor rgba(255,255,255,0.85)` + `titleBgChain {el:"zk-content", bg:"rgb(255,255,255)"}`（对比度 ≈ 1:1）、`sectionTitleColor`/`labelColor` 同为白 85%、`bodyBg2` 透明、`footerBg rgb(255,255,255)`、`rootVarBg "#fff"`、`inputBg {bg:rgb(20,20,20), color:white}`（输入框反而已是深色 —— 两套主题在打架）。
- 代码：`web/src/design/tokens.css:75` — 深色 token 块只挂在 `@media (prefers-color-scheme: dark)` 上，**没有 `[data-theme="dark"]` 选择器**；`web/src/design/theme.ts:15` — `algorithm: appearance === 'dark' ? theme.darkAlgorithm : theme.defaultAlgorithm` 会跟用户选择走。于是「用户选深色 + 系统是浅色」⇒ antd 走暗算法、ZK token 保持浅色。
- 规范：`use-system-appearance.ts:7-9` 自己写着这条不变量："§9.1: the CSS token block and antd's algorithm must agree, so both read this one value" —— 实现没做到（CSS 读不到 React 值）；DESIGN §9.4「表面遵循 S0–S4 ramp」。
- 建议：`AppShell` 在 appearance 变化时写 `document.documentElement.dataset.theme = appearance`；`tokens.css` 深色块从 `@media (prefers-color-scheme: dark)` 改成 `:root[data-theme="dark"], @media … :root:not([data-theme="light"])` 双选择器；并在 e2e 里加一条「切深色后 `.zk-screen__title` 与其背景对比度 ≥ 4.5」的断言。

### S18 关于页版本语义自相矛盾：当前版本 dev / 最新 0.4.0 /「有可用更新」[P1]

- 现象：`当前版本 dev`，下一行 `最新版本 0.4.0 [有可用更新]`，紧跟一个 primary 的「下载更新」。仓库 HEAD 就是 0.4.0（BRIEF 也写了），用户看到「dev」无法判断自己比 0.4.0 新还是旧，却被提示可以更新。
- 证据：`shots/settings-1440-about.png`、`shots/settings-en-about.png`；量测 `settings-10-final.json` → `dd = ["dev","0.4.0 有可用更新","2026/11/24 19:59:29"]`；`settings-5-en.json` → `enAbout.dd = ["dev","0.4.0 update available","2026/11/24 20:11:23"]`。
- 代码：`web/src/features/settings/update-status.tsx:78-106`（`currentVersion || unknown`、`available → Tag processing「有可用更新」`、否则 `Tag「当前版本」`）。
- 规范：DESIGN §2「状态语义不可降级：error 用语义色 + 文案」的反面——此处是「无法判断的状态用了一个确定的标签」；DSH/VS Code 的更新面板在版本不可比时显示「无法判断/开发构建」而不是「有可用更新」。
- 建议：后端把 `dev` 构建设为 `+dev`/`-dirty` 语义（或提供 commit 短 SHA），前端在 `currentVersion` 非 semver 时把「有可用更新」换成中性 `Tag`「开发构建」并禁用「下载更新」，同时给一句「当前为源码构建，自动更新仅对安装包生效」。

### S19 只读页（关于）仍显示保存栏，且该页有 2 个 primary 按钮 [P1]

- 现象：关于页没有任何可编辑字段，底部仍然渲染「已与网关快照一致 / 放弃更改(禁用) / 保存(禁用)」；同时页面中部有 primary「下载更新」。一屏 2 个 primary（`≤1` 规则），而唯一真正的主操作（检查更新）是 default 按钮。
- 证据：`shots/settings-1440-about.png`、`shots/settings-detail-footer.png`；量测 `settings-10-final.json` → `about.primaries = [{t:"下载更新",disabled:false},{t:"保 存",disabled:true}]`、`footer {x:484,y:851,w:956,h:49}`、`footerStyle {position:"static", zIndex:"auto", bg:"rgb(255,255,255)"}`。
- 代码：`web/src/features/settings/settings-screen.tsx:337-355`（页脚无条件渲染）；`:345-353`（primary 保存）；`update-status.tsx:129-137`（primary 下载）。
- 规范：DESIGN §"设置页脚 | `uikit.PageFooter` | 顶部 Divider + 右对齐（危险/次要在前、唯一 primary 最右）"——「唯一 primary」；`§9.4 一屏 ≤1 个 primary 按钮`。
- 建议：`page.sections.every(s => s.fields.length === 0)` 时不渲染 footer（关于页整页只读）；「下载更新」降为 default，把 primary 留给未来真正的安装动作（「立即安装并重启」已有独立按钮）。

### S20 发布说明是英文标签 + 裸 URL，装在输入框样式的盒子里 [P1]

- 现象：`Full Changelog: https://github.com/zeokeel/zeokeel/compare/v0.3.9...v0.4.0` —— 英文标签 + 裸长 URL 单行，被一个 1px 边框 + 8 圆角 + 白底的盒子包住。在中文界面里它看起来像一个只读输入框（用户会去点、会去选中复制），实际是不可编辑的 Markdown 渲染。
- 证据：`shots/settings-detail-about-version.png`、`shots/settings-en-about.png`；量测 `settings-10-final.json` → `notesStyle {border:"1px solid rgba(15,17,21,0.15)", borderRadius:"8px", background:"rgb(255,255,255)", fontSize:"13px"}`、`notesInner = <p><strong>Full Changelog</strong>: <a href="...">https://github.com/…compare/v0.3.9...v0.4.0</a></p>`。
- 代码：`web/src/features/settings/update-status.tsx:153-158`（`MarkdownBody` 直接放进 `.zk-update__notes`）；`web/src/design/global.css:1551-1560`（`.zk-update__notes` 的边框 + `max-height:320px; overflow-y:auto`）。
- 规范：antd 6 `Alert`/`Typography` 的只读区块不使用 input 式描边；DESIGN §"设置块用 `t.Panel()`（标题 + Divider + body）"——容器应表达「段落」而非「输入框」；英文标签属上游 release body，客户端应本地化前缀或改为「版本说明 · 完整变更日志」链接行。
- 建议：去掉 `.zk-update__notes` 的边框与圆角，改 `background: var(--zk-bg-raised)` + `padding: 12px 0`；把裸 URL 包成「在 GitHub 查看完整变更」的行内链接（`target="_blank"`），并在链接前加本地化前缀。

### S21 日期格式 `2026/11/24 19:59:29` [P2]

- 现象：检查时间用斜杠 + 秒 + 无时区（`toLocaleString()` 的默认形态）；与仓库里其它时间显示（相对时间/ISO）不一致，且秒对用户无意义。
- 证据：`shots/settings-1440-about.png`、`shots/settings-en-about.png`（英文界面同样 `2026/11/24 20:11:23`，中英都不变）；代码 `web/src/features/settings/update-status.tsx:74` — `new Date(status.checkedAt).toLocaleString()`。
- 规范：DESIGN §"时间/计数用 XXS" 同一处纪律要求时间格式统一；DSH 会话列表用相对时间（`3 分钟前`）。
- 建议：抽一个 `formatRelativeOrDateTime(ts, locale)`：<24h 用相对时间（`19 分钟前`），更早用 `2026-11-24 19:59`；`<time dateTime>` + `title` 给绝对时间。

### S22 模型目录列表零样式：disc 圆点 + 40px 缩进 + 按钮紧跟文字；空目录无空态 [P1]

- 现象：模型目录有数据时渲染成浏览器默认的 `<ul>`：每行一个实心圆点、整体右缩 40px、行内按钮（设为默认/编辑/删除）直接贴在元信息文字后面而不是行尾。空目录时只有标题「模型目录」+ 一个「添加模型」按钮，没有任何空态文案。
- 证据：`shots/settings-model-catalog-populated.png`、`shots/settings-model-empty-catalog.png`、`shots/settings-detail-model-section.png`；量测 `settings-4-catalog.json` → `ulPad "0px 0px 0px 40px"`、`ulMargin "14px 0px"`、`listStyle "disc"`、`rowDisplay "list-item"`、`rowPad "0px"`、`rowBg transparent`、`rowBorder "0px none"`、`marker "normal"`、`mainRect {x:548,w:868}`（被 40px 缩进推到 548，而 section 正文在 508）。
- 代码：`web/src/features/settings/model-catalog.tsx:111-166`（`className="zk-model-list"` / `"zk-model-row"` / `"zk-model-row__main"`），但 `web/src/design/global.css` 里**完全没有 `zk-model-*` 的规则**（`grep zk-model web/src/design/*.css` 无命中）→ 只有浏览器默认样式。
- 规范：DESIGN §"`List` + `ListRow`：所有集合的唯一选择（…设置列表…）；Key 稳定、Label 供 a11y"；空态用 antd `Empty`（`image={Empty.PRESENTED_IMAGE_SIMPLE}`，收件箱已这么用）。
- 建议：补 `.zk-model-list { list-style:none; padding:0; margin:0; display:flex; flex-direction:column; gap:1px }`、`.zk-model-row { display:flex; justify-content:space-between; align-items:center; padding:12px 0; border-bottom:1px solid var(--zk-border-l1) }`（行尾放动作）；空目录渲染 `Empty` + 「还没有模型，添加一个」文案。

### S23 模型删除：确认键非危险色、删默认模型不清 `chat.model`、同 id 可重复且一次删掉全部同 id [P1]

- 现象：① 删除确认气泡是「删除「本地假模型」？[取消][确定]」，确定键是**普通蓝色 primary**，不是 danger；② 添加模型时不校验 id 唯一性，同一个 id 可以加两遍（我实测列表出现两行完全相同的 `acceptance-plain`）；③ 删除其中一条 → **两行一起消失**（`remove` 按 id 过滤），一次点击删掉 2 条；④ 删掉当前默认模型时，`chat.model` 仍指向已消失的 id，页面无任何提示。
- 证据：`shots/settings-model-delete-confirm.png`、`shots/settings-model-catalog-populated.png`（两行同 id）、`shots/settings-model-after-delete.png`；量测 `settings-5-en.json` → `pop.btns = [{t:"取 消",danger:false},{t:"确 定",primary:true,danger:false}]`、`afterDelete {rows:0}`（删 1 次清空 2 行）。
- 代码：`web/src/features/settings/model-catalog.tsx:99-101`（`remove` = `filter(item => item.id !== row.id)`，无「是否为当前默认」检查）、`:92-94`（新增时 `[...catalog, row]`，无 id 去重）、`:152-159`（`Popconfirm` 未传 `okButtonProps={{danger:true}}`）、`:103-109`（`makeDefault` 只写 `chat.model`，不写回 catalog）。
- 规范：antd 6 `Popconfirm` 的破坏性动作应 `okType="danger"`/`okButtonProps={{danger:true}}`；DESIGN §"红色元素 ≤2" 的前提是红色只用于真破坏动作（这里反倒用蓝）；§"不做未请求的功能" 反面对应的是「真的破坏要说得清」。
- 建议：`Popconfirm okButtonProps={{ danger:true }}` + 描述写清影响（「这是当前默认模型，删除后将回落到第一个模型」）；新增/编辑时校验 id 唯一；删除当前默认模型时同时把 `chat.model` 置空或指向首项并给 `notice`。

### S24 模型行每行一个红色按钮，3 行就超「一屏 ≤2 处红色」[P2]

- 现象：每个模型行都有一个 danger 描边的「删除」。3 个模型 = 3 处红色，且红色出现在同一屏的同一列。
- 证据：`shots/settings-model-catalog-populated.png`；代码 `web/src/features/settings/model-catalog.tsx:156-158`（`<Button size="small" danger>`）。
- 规范：DESIGN §9.4「一屏 ≤2 个 Accent 填充块、≤1 个 primary 按钮、≤2 处红色」（AGENTS.md §3.10）；DESIGN §"管理列表平铺行 + Divider + 行尾 ⋯"——危险动作应收进行尾 `⋯` 菜单。
- 建议：行尾改 `Dropdown`（`⋯`：编辑 / 设为默认 / 删除）；删除项在菜单里用 `danger: true`，行内只留中性按钮。

### S25 归档页只有 1 个开关；破坏性操作（删除归档/清除数据）在 9 页里完全缺失且无二次确认 [P1]

- 现象：页面叫「归档」，section 叫「会话」，唯一内容是「在侧栏显示已归档会话」开关——这是显示偏好，不是归档管理。用户在设置里找不到「删除归档会话 / 清空归档 / 清除所有数据」，也看不到任何危险区（danger zone）。同样的缺口在「数据与隐私」页（见 S26）。
- 证据：`shots/settings-1440-archive.png`（整页只有一个开关 + 大空白）；量测 `settings-1-pages.json` → `archive.sections = ["会话","其它设置…"]`，`fields = [ui.sessions.showArchived]`（1 个）。
- 代码：`web/src/features/settings/ia.ts:289-300`（归档页仅 `bool(K.sessionsShowArchived, …)`）；全仓 `ConfirmDanger` 只在 `settings-screen.tsx:357`（放弃更改）与 `thread/edit-message-dialog.tsx:34` 使用，设置里没有任何删除类确认。
- 规范：BRIEF §5 判定口径「破坏性操作必须有二次确认与危险色」；DESIGN §"`t.Panel()` 每个设置块" + 「危险/次要在前、唯一 primary 最右」的页脚契约暗示危险动作存在且需着色。
- 建议：归档页补「管理已归档会话」入口（列表 + 单条恢复/删除）与「清空归档」——用 `ConfirmDanger`（已存在，`web/src/ui/confirm-danger.tsx`）+ danger 文字按钮；若产品决定不做，至少把页名改成「会话显示」以免承诺不存在的管理能力。

### S26 「数据与隐私」页全是 JSON 文本域，没有任何隐私项 [P1]

- 现象：页面标题「数据与隐私」，内容只有 section「工具与技能」下的 7 个同形文本框（禁用的工具/技能/技能目录/MCP 服务器/禁用的 MCP 服务器/禁用的 MCP 工具/工作区级 MCP），全部要手写 JSON。没有数据保留、没有遥测、没有导出/清除本地数据。7 行长得一模一样，只能靠 label 区分，且其中 5 个的 help 都是同一句「JSON 字符串数组。」
- 证据：`shots/settings-1440-data-privacy.png`、`shots/settings-detail-en-dataprivacy.png`；量测 `settings-1-pages.json` → `data-privacy.fields` 7 项全部 `TEXTAREA w=720 h=54`；`settings-5-en.json` → `enPrivacy.helps = ["JSON array of strings." ×5, "JSON array: {name, transport, command|url}.", "1 lets a workspace's own MCP config apply."]`。
- 代码：`web/src/features/settings/ia.ts:301-320`（7 个 `json(...)`）、`:313`（`titleKey:'settings.ia.toolsAndSkills'`）；文案 `zh.ts:409-416`。
- 规范：DESIGN §"`Form`/`Field`/`Fieldset` + 官方输入"；DESIGN §"能力/扩展管理页…用户明确要求卡片布局"（工具/技能/MCP 本质是管理列表，不是 JSON 设置）；antd X `Tag`/`Select mode="tags"` 适合「禁用列表」。
- 建议：把 7 个 JSON 字段换成 `Select mode="tags"`（禁用工具/技能/MCP 名）与列表编辑器（MCP 服务器 ← 模型目录同款弹窗）；页面拆成「工具与技能」与真正的「数据与隐私」（导出/清除/保留策略），或改名为「工具与技能」。

### S27 通知页 section 标题「分级」不成立 [P2]

- 现象：section 标题「分级」（grading），下面 4 项却是「应用内通知 / 系统通知 / 开机自启（托盘壳）/ 关闭时最小化到托盘（托盘壳）」。后两项与通知等级无关，应属「外壳/启动」；而「分级」这个词也没告诉用户这是「通知渠道」。
- 证据：`shots/settings-1440-notifications.png`；量测 `settings-1-pages.json` → `notifications.sections = ["分级"]`，字段 4 个；文案 `web/src/i18n/zh.ts:426` `'settings.ia.verbosity': '分级'`。
- 代码：`web/src/features/settings/ia.ts:243-251`。
- 规范：DESIGN §"分组标题表达的是对象，不是形容词"（Fieldset 语义）；macOS 系统设置「通知」页只放通知，登录项在「通用 → 登录项」。
- 建议：section 改「通知渠道」，把 `ui.autostart` / `ui.closeToTray` 移到新 section「启动与托盘」（或网关设置页）。

### S28 收件箱行标题是原始 JSON 参数，与线程内审批卡信息严重不一致 [P0]

- 现象：收件箱里那条待审批，标题是 `{"url":"http://127.0.0.1:9861/health"}`（原始参数 JSON），第二行是 `web_fetch`（工具 id）。同一个审批在会话里显示为：`需要审批 · 工具 web_fetch · 来自会话：把 README 里的安装步骤补充完整… · 用户` + 影响面「这会向外发出网络请求。」+ 目标 URL 块 + 「原始参数」折叠 + `Esc 拒绝 · Cmd/Ctrl+Enter 允许` + 「仅此一次/始终允许」下拉 + 拒绝/允许一次。收件箱既没有影响面、没有目标、没有会话归属、也没有「始终允许」。
- 证据：`shots/settings-inbox-1440.png`（收件箱）、`shots/settings-en-inbox.png`、`shots/settings-model-catalog-populated.png`（同一时刻会话内的审批卡，可见「来自会话：…」「这会向外发出网络请求。」）；量测 `settings-8-inbox2.json` → `title:"待审批{"url":"http://127.0.0.1:9884/health"}"`、`detail:"web_fetch"`。
- 代码：`web/src/api/inbox.ts:66-76`（`title: approval.summary || approval.tool`，`detail: approval.tool`）→ `web/src/features/inbox/inbox-screen.tsx:83-93`（原样渲染）；对照 `web/src/features/approvals/approval-card.tsx:34-48`（`approvalRisk`/`approvalTarget` 生成影响面与目标）、`:57-75`（会话来源）、`:108-118`（影响面句）、`:74-96`（原始参数 `Collapse`）、`:126-135`（scope `Select`）。
- 规范：BRIEF §5「P0：信息错位到误导 / 安全审查被削弱」；`docs/APPROVAL-SURFACES.md` 的「审批动词不中性化 + 影响面可见」；antd 6 `List`/`Descriptions` 表达「标题 + 元信息」。
- 建议：收件箱行复用 `approval-card.tsx` 的纯函数（`approvalRisk` / `approvalTarget`）生成：① 主标题 = 工具人话名 + 目标（`web_fetch → 127.0.0.1:9861/health`）；② 次行 = 影响面句 + 会话标题；③ 动作区给「仅此一次/始终允许」下拉 + 拒绝/允许，与卡片同构；原始参数收进 `Popover`/`Collapse`。

### S29 收件箱页内距 0：容器/列表/行全贴屏幕左缘，行通栏 1176 [P0]

- 现象：`.zk-inbox` 没有 padding，列表与行从 x=264（应用侧栏右边框）0 间隙开始，右端到 x=1440 屏幕边缘——一张审批卡横跨 1176px，左右都贴着容器边界，与 `/team`（`.zk-screen` 24px 内距）完全不同。计数行也在 x=264，而顶栏标题「收件箱」在 x=280。
- 证据：`shots/settings-inbox-1440.png`；量测 `settings-2-inbox.json` → `inbox {x:264,w:1176}`、`inboxPad "0px"`、`list {x:264,w:1176}`、`row {x:264,y:87,w:1176,h:64}`、`rowStyle {padding:"8px", borderRadius:"8px"}`、`buttons [{x:1311},{x:1359}]`（动作贴到屏幕右侧 24px 内）。
- 代码：`web/src/design/global.css:716-720`（`.zk-inbox` 只有 `display:flex; gap`，无 padding）；对照 `.zk-screen` 有 `padding: var(--zk-space-6)`（`global.css:786-790`）；页面根是 `web/src/features/inbox/inbox-screen.tsx:63`。
- 规范：BRIEF §3 已知基线（"`/inbox` 的 `.zk-inbox` 是 0"）；DESIGN §"页面内容统一 page padding"；macOS/VS Code 的设置与列表页都有统一左右边距。
- 建议：`.zk-inbox { padding: 16px 24px 24px }`（与 `.zk-screen` 的 24 对齐），或包一层 `.zk-screen`；列表限宽 `max-width: 880px`，让审批行读起来像一张卡而不是一条横幅。

### S30 390 视口下审批按钮压住标题，URL 被裁 —— 审批信息不可读 [P0]

- 现象：390×844 时侧栏仍占 200，收件箱内容列只有 126px：行内图标 + 标题 + 两个按钮挤在一行，「拒 绝 / 允许一次」两个按钮**叠在**标题 JSON 上（截图里 `{"url":"…` 只露出半截，被按钮遮住），行高被撑到 86。用户看不到自己正在批准什么。
- 证据：`shots/settings-inbox-390.png`（截图里按钮覆盖 `{"url":` 之后的内容）、`shots/settings-390-appearance.png`（设置页内容列完全在视口外）；量测 `settings-2-inbox.json` → `mob {scrollW:390, sidebar:{w:200}, inbox:{x:200,w:126}, rows:[{x:200,y:87,w:126,h:86}]}`。
- 代码：`web/src/design/global.css:745-753`（`.zk-inbox__row` 是 `display:flex` 不换行，动作区 `flex-wrap` 只在 `.zk-inbox__actions` 内部）、`:772-778`（`justify-content:flex-end` 无 shrink 策略）；`inbox-screen.tsx:78-123`（行结构：icon/body/actions 三列固定横排）。
- 规范：BRIEF §5「P0：移动端不可用 / 安全审查被削弱」；DESIGN §"响应式：窄视口列转行"（收件箱未被纳入）。
- 建议：`@media (max-width: 767px)` 下 `.zk-inbox__row { flex-wrap: wrap }`，`.zk-inbox__actions { width:100%; justify-content:flex-start }`；同时给壳层加移动端抽屉（跨区域建议，见末节）。

### S31 收件箱行没有会话归属；ask/plan/artifact 的「查看」是无 sessionId 的死按钮 [P1]

- 现象：数据层 `InboxItem` 有 `sessionId`/`sessionTitle` 两个字段，执行层只填了 approval 的 `sessionId`，`sessionTitle` **从未被赋值**，界面也从不渲染它。于是收件箱里一条审批不告诉你它来自哪个会话——而会话内审批卡明确写着「来自会话：把 README 里的安装步骤补充完整…」。ask/plan/artifact 项没有 `sessionId`，「查看」按钮恒为 disabled，只靠 `title` 提示「该产物暂无预览位置」。
- 证据：`shots/settings-inbox-1440.png`（行内只有 tag + JSON + 工具名）、`shots/settings-inbox-9874.png`；量测 `settings-8-inbox2.json` → `sessionShown:false`。
- 代码：`web/src/api/inbox.ts:15-30`（定义了 `sessionTitle?: string`）、`:66-76`（approval 只传 `sessionId`，不传 `sessionTitle`）、`:79-96`（ask/plan/artifact 均无 sessionId）、`web/src/features/inbox/inbox-screen.tsx:106-117`（无 sessionId 就 disabled）。
- 规范：DESIGN §"每行必须回答三件事：这是什么 / 从哪来 / 我能做什么"（同源规则见 §"会话行的前置位"）；`approval-card.tsx:69-80` 已有 `来自会话` 的成句文案可复用（`t('approval.fromSession', {title})`）。
- 建议：`buildInboxItems` 把 approvals-context 里的会话标题带进来（context 已有会话列表），行内渲染「来自 · <会话标题>」并让整行可点进会话；ask/plan 项在没有跳转目标时不要渲染「查看」按钮，改为不可交互的说明文字。

### S32 空态文案重复两次；计数行与标题左缘不一致 [P1]

- 现象：空收件箱时，左上角一行「没有等你处理的事」，正中 antd `Empty` 又写一次「没有等你处理的事」——同一句话一屏两遍。非空时计数行「1 项等待你」贴在顶栏下沿（y=49，0 内距）、左缘 x=264，而顶栏标题「收件箱」在 x=280，两者差 16px。
- 证据：`shots/settings-en-inbox.png`（`Nothing is waiting for you` 出现两次）、`shots/settings-inbox-cold.png`；量测 `settings-6-inbox.mjs` 输出 `inboxCold {count:"没有等你处理的事", emptyText:"没有等你处理的事"}`；`settings-2-inbox.json` → `header {x:264,y:49,w:1176,h:22}`，`topband` 标题 x=280（BRIEF §3 已记录）。
- 代码：`web/src/features/inbox/inbox-screen.tsx:62-75`（计数行与 `Empty description` 用同一个 key `inbox.empty`）。
- 规范：antd 6 `Empty` 的 `description` 就是唯一文案位；DESIGN §"空态 = 一句引导 + 1 个操作，不放大插画"。
- 建议：空态时隐藏计数行（或计数行只在 `items.length>0` 渲染）；`Empty` 的 description 换成带引导的句子（「等待你处理的事会出现在这里」），并补 1 个操作。

### S33 收件箱动作按钮 24px、缺「始终允许」、单项占满 1176×64 [P2]

- 现象：拒绝/允许一次是 `ant-btn-sm`（24px 高，不在 28/32/36/40/44 阶梯），没有 scope 选择（只能「一次」），也没有键盘提示（会话卡片有 `Esc 拒绝 · Cmd/Ctrl+Enter 允许`）。单条待办时，一张 1176×64 的横条 + 下方整屏空白。
- 证据：`shots/settings-inbox-1440.png`；量测 `settings-8-inbox2.json` → `btns [{t:"拒 绝",w:48,h:24},{t:"允许一次",w:72,h:24}]`；`settings-2-inbox.json` → `row {w:1176,h:64}`。
- 代码：`web/src/features/inbox/inbox-screen.tsx:135-146`（`ApprovalActions` 只有两个小按钮，无 `Select`）、`:76-125`（列表无 max-width）。
- 规范：DESIGN §9.4 控件高度阶梯；`docs/WEB-CLIENT.md A9`「审批无默认动作为原则」——但「始终允许」是允许动词的一种，缺失会让用户在收件箱里被迫逐个点。
- 建议：与 `approval-card.tsx` 共用 scope `Select` + 两个 32px 按钮；列表限宽（见 S29），并加「还有 N 项」计数（卡片已有 `approval.morePending`）。

### S34 英文界面下这些页面：无溢出，但中英混排与「标题=label」重复依旧 [P1]

- 现象：英文下 9 页都不溢出（`scrollW = 1440`）、长标签**不换行**（所有 label 高度 20），可读性靠 label 行还有 908 宽兜底；但：① 导航仍中文（=S01）；② 章节标题与字段 label 仍重复（`Language` / `Language`、`Theme` / `Theme`）；③ 关于页仍是英文 `Full Changelog` + 裸 URL；④ 「Disable tools / Disable skills / Skill catalog」7 行 help 全是同一句 `JSON array of strings.`。
- 证据：`shots/settings-en-appearance.png`、`shots/settings-en-about.png`、`shots/settings-en-data-privacy.png`、`shots/settings-en-behavior.png`、`shots/settings-en-notifications.png`、`shots/settings-en-network.png`、`shots/settings-en-lsp.png`、`shots/settings-en-archive.png`、`shots/settings-en-model.png`；量测 `settings-5-en.json` → `enAppearance {docOverflow:false, scrollW:1440}`、`labels` 全部 `h:"20.4219px"`（单行）；`settings-9-detail.json` → 最大 label 行 `ui.closeToTray "Minimize to tray on close (tray shell)" 207px`、换行标签 0 个。
- 代码：同 S01 / S06；help 重复见 `web/src/i18n/en.ts` 的 `settings.ia.help.jsonStringArray`。
- 建议：先修 S01（memo 依赖）与 S06（去重 label），英文换行风险本身不需要改（908 宽 + 720 控件已足够），但若把 label 行收窄到 720（S04 的建议）需复测 `Minimize to tray on close (tray shell)` + help 的组合宽度。

---

## 跨区域共性问题（给 lead 汇总用）

1. **「看起来对、其实不做」**：主题 `深色` 选了不生效且渲染成白底白字（S17）；`ui.theme.dark` 是 legacy 但仍在页面上（S15）；`chat.mode`/`agent.mode` 手打 token 打错静默回落（S13）；非法 JSON 存成功（S14）。同一根因：**没有单一的状态校验层**，控件只负责把字符串塞进 gateway。
2. **antd 6 的官方件被半途放弃**：主表单是裸 div（无 `Form.Item`/`validateStatus`/`aria`），但同一个页面的「添加模型」弹窗是标准 `Form`；列表是裸 `<ul>`（模型目录）而收件箱用了 `Empty`。建议把「字段行 = `Form.Item`」「集合 = `List`」写成可 lint 的规则。
3. **密度与行解剖**：设置行「label+help 同行、控件在下、开关在标签下」导致 9 页平均密度极低（4 开关 240px、5 数字 375px），而 DESIGN 自己的 `SwitchRow` 契约是「标签左、控件右、44px 行高」。这是**跨设置/收件箱/自动化**的共同形态问题，值得统一成一个 `SettingsRow` 组件（text / number / switch / radio 四种变体）。
4. **两套 token 系统打架**：`tokens.css` 的深色块只挂 `@media (prefers-color-scheme: dark)`，而 antd 主题跟随用户选择 → 只要用户的选择与 OS 不一致，ZK token 与 antd token 就会一半深一半浅（S17）。这不是设置页专属，任何用 `var(--zk-*)` 的地方都会中招。
5. **内部实现泄漏到用户界面**：「其它设置（网关有、界面未接线）」把 `session.<id>.unread` 摊 9 遍（S16）；模型行的 `acceptance-plain · not-a-url-at-all · 密钥未设置` 是内部三元组；收件箱把工具参数 JSON 当标题（S28）。建议统一一条规则：**用户界面不出现 row key / session id / 原始 JSON**，诊断信息只进「关于 → 诊断」折叠区。
6. **左上缘的 16px 漂移**：`/inbox` 计数行 x=264、顶栏标题 x=280、设置正文 x=508；shell 的 `--zk-topband` padding 16 与正文 24 不一致（BRIEF §3 已记录），设置在内容列里又用 24。三处左缘不一致属 shell 级，建议一次收口。