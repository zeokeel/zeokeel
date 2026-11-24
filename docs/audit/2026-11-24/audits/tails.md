# ZeoKeel Web 尾部验收（第 3 轮）：文件 tab + 自动化计划表达式

- 验收人：独立验收员（session `604fa9b4-7d86-4699-a494-bc4fe41d3585`），未改 `web/src`（本轮只读）。
- 验收时间：2026-11-25 04:31–05:00（北京时间）
- 被测改动：`ca38e19 feat(web): the file inspector gets a tab`、`7e2d58b fix(web): the schedule help described a grammar the gateway does not accept`
- **第三轮（定点）复验（最新，见文末）：lead 列的 R2-1/R2-4/R2-2/R2-3/R2-5 与 F1–F9 回归**全部 PASS**（9 个帮助示例全部真建成功、窄屏 y=87 且顶栏 tab 可点、900 为分栏、弹窗错误清空、页级错误可 ×、深链展开树、标记 8px）。仅剩 1 条**既有**（非本次引入）的视觉残留 R3-1：900–1023px 宽下 inspector 固定 320px 落在 380px 面板里，右侧留 60px 空白。第三轮总判：**PASS**。
- 结论：**整体 FAIL**（2 项改动中，① 有 4 个可复现缺陷 F1–F4（含"文件树仍然不可达"，即本次改动声称修掉的那个 P0；F5 是 F1 的同源表现）；② 的方向是错的——新的帮助文案与新的本地校验**仍然**和网关 `contracts.ParseAutomationSchedule` 不一致，且反向误拦网关自己产生的规范形式）。

---

## 0. 环境与版本自证

| 项 | 值 / 结果 |
|---|---|
| 网关 | `http://127.0.0.1:9824`（`GET /`=200、`/favicon.svg`=200、`/api/capabilities` 无授权=401） |
| 数据目录 | `/data/zeokeel/.cache/ui-audit/data-files`（干净实例，`bash startB.sh files … 9824 9825`） |
| gateway.json | `port=9824`、`product=serve`、`bundleSha256=8c3c40dcbb544bbd7b6cb3a28c76fce08448285cdb2ad20ab9d0de8b7c5b5723`、`bundleBuiltAt=2026-11-24T20:30:16Z` |
| **指纹自证** | 用 HTTP 抓下网关实际服务的 6 个文件（`index.html`、`favicon.svg`、`assets/index-DVOuCzEE.js`、`.js.map`、`index-DyGGc8pB.css`、`.keep`），按 `scripts/build-web-client.sh` 的算法（`find … ! -name BUNDLE_INFO | sort | xargs sha256sum | sha256sum`）**复算得到 `8c3c40dc…5723`，与 gateway.json 完全一致** → 本轮证据确实是对这两个提交所产出 bundle 的验证 |
| bundle 内含两处改动 | `grep` 命中 `thread.tabs.files`、`接受：@hourly、@daily、daily 09:00、…`、`五段 cron 需要 cron 前缀…`、`时间要写成 HH:MM…`、`needsCronPrefix` |
| 浏览器 | Playwright chromium（`/data/tavo/tools/e2e/chromium-1243`），真网关真数据 |
| 说明 | 验收期间工作区出现了**并发改动**（prompt-queue，`web/src/api/prompt-queue.ts` 等，HEAD 已到 `059c30d`）。这些改动都不在 `web/src/features/{inspector,automation}`、`web/src/shell/top-band.tsx`、`web/src/features/app-routes.tsx` 里，也不在被测 bundle 中，不影响本报告结论。 |

---

## 1. 清单表（逐条 PASS / FAIL）

### ① 会话「文件」tab

| # | 断言 | 结果 | 证据 |
|---|---|---|---|
| A1 | 顶栏 5 个 tab，文本与顺序 = 对话 / 轨迹 / 记忆 / 上下文 / 文件 | **PASS**（有保留，见 A1b） | `tabs_final`：`["对话","轨迹","记忆","上下文","文件"]`；`aria-selected` 与 active 类一致 |
| A1b | 冷加载立刻就是 5 个 | **观察**：先 4 个（无「上下文」），2.8s（1440）/4.5s（390）后才补上 | 时间线 `{"ms":310,"labels":"对话\|轨迹\|记忆\|文件"}` → `{"ms":2843,…"上下文"…}`；390：`ms:177` → `ms:4534`。属 `TAB_CAPABILITY` 能力位（initialize 到达）机制，非本次改动引入，但截图/首屏会看到 4 个 tab |
| A2 | 点「文件」→ `.zk-thread-split` + `.zk-inspector` 存在 | **PASS** | `split:true`、`splitClass:"ant-splitter zk-thread-split …"`、`inspector:true`（x=1060,w=380,h=813） |
| A3 | `activeTab=文件` | **PASS** | `.zk-topband` 中 active = `文件`（其余 false）；`aria-selected=true` |
| A4 | URL 为 `/c/:id/files` | **PASS** | `url:"/c/sess-1795552451867321702/files"` |
| A5 | 左侧仍是对话 | **PASS** | `.zk-thread` 与 `.zk-composer textarea` 均在；正文为用户消息 + 工具卡 |
| A6 | 点回「对话」恢复单栏 | **PASS** | `split:false`、`inspector:false`、composer 在 |
| A7 | 浏览器后退 / 前进 | **PASS** | back → `/files` + split；forward → `/c/:id` + 单栏 |
| A8 | `/c/:id/files` 直接打开（深链）+ 刷新后 tab 与 URL 一致 | **PASS** | `deeplink_files` / `deeplink_reload`：URL=/files、active=文件、split/inspector 在 |
| A9 | **右侧是检查器（文件树 / diff 说明）** | **FAIL** | 右侧不是文件树，是「开始」指南页（`hasBody:true`、`hasTree:false`、`treeNodes:0`）；且**点击检查器自己的「文件」子页签没有任何反应**（连 JS 直接 `.click()` 也无反应）→ **文件树在 UI 里不可达**，只能靠手输 `/c/:id/files/<path>` 才出现。见 §2-F1 |
| A10 | `/c/:id/files/<path>` 深链/刷新 | **部分 FAIL** | 内容正确（树 3 节点、预览正文、预览/改动分段、`view=diff` 出 unified diff）；但**顶栏把「对话」标成 active**，且此时点「对话」无效（URL 不变、分栏还在）→ 用户被困在文件视图。见 §2-F2 |
| A11 | 390 抽屉下文件面板是覆盖层还是并排 | **FAIL（两者都不是，破版）** | 390×844：`.zk-thread` 宽 **10px**、`.zk-composer textarea` 宽 **0px**、`.zk-inspector` x=31,w=359,**y=0,h=844**（盖住 y=0..87 的顶栏），无横向滚动（`scrollWidth=390`）。不是抽屉，也不是可用并排。深色 @390 复测几何完全相同（`thread w=10`、`composer w=0`、`inspector y=0,h=844`，文字仍可读 `rgba(255,255,255,.85)` on `rgb(39,39,54)`），汉堡按钮可点、抽屉能开（`ant-drawer-open=true`），但对话列本身收不回来。见 §2-F3 与截图 14/15/16/42/43 |
| A12 | 深色下检查器文字可读 | **PASS** | 8 个元素实测（按 alpha 合成算真实底色）：文件树节点 13.4:1、面包屑路径 10.46:1、预览正文 11:1、未选中页签 10.46:1、选中页签 12.14:1、分段控件 12.18:1、顶栏 tab 14.84:1、检查器容器 13.4:1（均 ≥ 4.5，最小字号 12px） |
| A13 | 改动过的文件在树上有标记（指南承诺） | **FAIL** | 浅色下展开 `src` 后树里有 `util.ts`（`/api/files/tree` 返回 `changed:true`），但 `.zk-tree-changed` 计数 **0**、`aria-label` 空（深色下同样为 0）；`<Tree>` 未传 `showIcon`，节点上的 `icon` 永远不渲染。见 §2-F4 |
| A14 | `/c/:id/files` 与 `/c/:id/guide` 的区别 | **FAIL（无实质区别）** | 两个 URL 的检查器 DOM 完全一致（都是「开始」正文，`activeSub:["开始:true","文件:false"]`），差别只有 URL 与顶栏高亮（/files→文件、/guide→对话）。`/files` 并没有"进文件页"的意思。见 §2-F5 |

### ② 自动化「计划表达式」

真值取自**运行中的网关**（`POST /api/automation`，`enabled=true`，200=接受 / 400=拒绝），与浏览器里"是否被拦 / 文案 / 是否发 POST"逐列对比。

| 表达式 | 网关 | 前端 | 前端文案 | POST | 判定 |
|---|---|---|---|---|---|
| `@hourly` | 200 | 放行 | — | 1（200，nextRun 05:00） | **PASS** |
| `@daily` | 200 | 放行 | — | 1（200） | **PASS** |
| `cron 0 8 * * *` | 200 | 放行 | — | 1（200，存为 `cron 0 8 * * * Asia/Shanghai`） | **PASS** |
| `daily 09:00` | **400**（`time "09:00" must be HH:MM:SS…`） | 放行 | — | 1（**400**） | **FAIL**：应用不了，用户照帮助填 → 网关报错（本次改动声称修掉的正是这个） |
| `weekly mon 09:00` | **400**（`time "mon" must be HH:MM:SS…`） | 放行 | — | 1（**400**） | **FAIL** |
| `every 24h` | **400**（`every needs a whole number of seconds like "30s"`） | 放行 | — | 1（**400**） | **FAIL** |
| `after 2h` | **400**（同上） | 放行 | — | 1（**400**） | **FAIL** |
| `0 8 * * *` | **200**（合法，legacy daily，nextRun 08:00） | **拦截** | 五段 cron 需要 cron 前缀，例如 cron 0 8 * * *。 | **0** | **FAIL**：误拦合法表达式（声称"裸五段被网关拒绝"是错的，见 §2-F7） |
| `daily 9am` | 400 | 拦截 | 时间要写成 HH:MM（例如 09:30）。 | 0 | PASS（拦截行为正确；但文案指向 HH:MM，网关要 HH:MM:SS） |
| `daily 25:00` | 400 | 拦截 | 时间要写成 HH:MM（例如 09:30）。 | 0 | PASS（同上文案问题） |
| `tomorrow` | 400 | 拦截 | 这个计划表达式不合法。可用：@hourly、@daily、daily 09:00、weekly mon 09:00、every 24h、after 2h、cron 0 8 * * *。 | 0 | PASS（文案里 4 个"可用"其实不可用） |
| `every day` | 400 | 拦截 | 同 format | 0 | PASS |
| `cron 0 8 * *` | 400 | 拦截 | 同 format | 0 | PASS |
| 只填合法表达式（`@hourly`），title/prompt 空 | — | 拦截 | **只有** `请输入任务名称`、`请输入提示词` | **0** | **PASS**（无计划错误、无请求） |
| 三种提示都在 | — | — | format / clock / needsCronPrefix 三套文案均实际渲染 | — | **PASS** |
| **帮助文案与实现一致** | — | — | 见 §2-F7 矩阵 | — | **FAIL**：文案列出的 7 种里 **4 种网关拒绝**（daily 09:00、weekly mon 09:00、every 24h、after 2h），并把裸五段 cron 说成非法（实际合法）；placeholder 就是 `daily 09:00`（网关拒绝的那个） |

补充真值（越界用例，全部实测）：

| 表达式 | 网关 | 前端 | 结论 |
|---|---|---|---|
| `*/15 * * * *` | 200 | 拦截（needsCronPrefix） | 误拦 |
| `daily 09:00:00` / `daily 09:00:00 Asia/Shanghai` | 200 | 拦截（clock / clock） | 误拦 |
| `weekly 09:00:00 Asia/Shanghai mon` | 200 | 拦截（format） | 误拦 |
| `cron 0 8 * * * Asia/Shanghai` | 200（这就是网关自己存的形式） | 拦截（format） | 误拦 → 导致"编辑既有 cron 任务无法保存"，见 §2-F6 |
| `at 2026-12-01T09:30:00+08:00` | 200 | 拦截（clock） | 误拦 |
| `weekly` / `at`（裸） | 400 | 放行 → POST 400 | 误放 |
| `every 0s` / `after 0s` | 400 | 放行 → POST 400 | 误放 |
| `weekly sun 09:00` | 400 | 放行 → POST 400 | 误放 |
| `  @hourly  ` / `@HOURLY` | 200 | 放行 → 200 | 一致（大小写与首尾空白两端都容错） |
| 空 / 全空格 | — | `请输入计划表达式` / `计划表达式不能为空字符`，POST 0 | PASS |

### ③ 回归

| # | 项 | 结果 | 证据 |
|---|---|---|---|
| C1 | 自动化列表 | **PASS** | 表头 `任务/时间表/下次运行/上次运行/启用/操作`；行内 title+prompt、schedule code、nextRun、lastError 占位、按钮组（立即运行/编辑/历史/删除） |
| C2 | 运行历史弹窗 | **PASS** | 空态 `还没有运行记录。`（标题 `运行历史 · 审计 @hourly`）；有记录时 2 条 `success` + 时间 + `打开会话` 链接（`sess-1795552815613387734`） |
| C3 | 启用开关 | **PASS（主路径）** | `PATCH /api/automation/<id> {"enabled":false}` → 200，回读 `switchChecked=false`；再开 → 200、`nextRun` 回来 |
| C4 | 编辑既有任务 | **FAIL** | 打开网关规范化后的 cron 任务，schedule 预填 `cron 0 8 * * * Asia/Shanghai`，点保存 → 客户端报 `这个计划表达式不合法…`，**PATCH 一次都没发**（`calls:[]`）→ 改个标题都存不了。见 §2-F6 |
| C5 | 删除 | **PASS** | Popconfirm `删除「…」？取消/确定` → `DELETE … 200`，行数 7→6 |
| C6 | 草稿跨 tab / 刷新 | **PASS** | 输入「草稿-跨tab-保持」→ 切「文件」→ 切「轨迹」→ 回「对话」仍为原文；`reload` 后仍在；localStorage 键 `zk.draft.sess-1795552451867321702`；清空后键被删除 |
| C7 | `/files` 与 `/guide` 的区别 | **FAIL** | 同 A14：两者渲染完全一致 |
| C8 | 副作用 | **观察** | 390 下抽屉仍可用（`ant-drawer-open=true`，截图 21）；`every 30s` 任务确实按秒运行并刷新 `上次运行`；`after 30s` 运行一次后自动 `enabled=false`（符合设计） |

---

## 2. 失败项（现象 / 复现 / 期望 / 建议修法）

### F1【严重】文件树仍然不可达：检查器自己的「文件」子页签是死链（P0 未修）

- **现象**：点顶栏「文件」后，右侧检查器停在「开始」指南页（正文：`检查器用来查看 agent 改了什么：左边是工作区树，右边是文件内容或逐文件 diff。` / `选择「文件」页开始；改动过的文件在树上有标记。`）。点检查器里的「文件」子页签**完全不响应**：URL 停在 `/c/:id/files`，DOM 不变（`hasTree:false`），连 `element.click()` 也无反应（按钮 w=42,h=27，`disabled=false`，命中测试=`文件`）。
- **复现**：`/c/sess-…/files`（或先从 `/guide` 点「文件」子页签）→ 再点「文件」子页签 → 观察右侧仍是「开始」正文。
- **期望**：点「文件」（顶栏或检查器内）就能看到工作区树；`/guide` 才是指南页。
- **根因**：`guide` 的判定是"没有选中文件且没有 view 参数"，而不是"当前是不是 /guide 路由"：
  - `web/src/features/inspector/inspector-panel.tsx:121` `const guide = relative === '' && !searchParams.get('view');`
  - `web/src/features/inspector/inspector-panel.tsx:140`（子页签）`onClick={() => sessionId && navigate(\`/c/${sessionId}/files\`)}` —— 在 `/files` 上等于导航到同一个 URL，`guide` 依旧为 true。
- **建议修法**：把"是不是指南"绑定到路由而不是"有没有选文件"。例如 `const guide = location.pathname.endsWith('/guide')`（`useLocation`）并在 `/files` 无选中时渲染树 + `选择一个文件查看内容。`；或子页签导航时带上显式参数（`navigate('/c/:id/files?browse=1')`）并让判定读它。

### F2【严重】文件路径 URL 下顶栏高亮错 + 点「对话」无效（用户被困）

- **现象**：`/c/:id/files/src/util.ts` 上顶栏 active 是**对话**（`aria-selected=true`），「文件」是 false；此时点「对话」URL 不变、分栏仍在（`before===after`）。1440 与 390 均可复现。
- **复现**：深链 `/c/:id/files/src/util.ts` → 看顶栏 → 点「对话」。
- **期望**：active 应是「文件」；点「对话」回到 `/c/:id`。
- **根因**：用 `endsWith` 判断 tab（`/files/src/util.ts` 不以 `/files` 结尾 → 落到默认 `conversation`；而 antd Tabs 点"已 active 的 tab"不触发 `onChange`，所以点击被吞）：
  - `web/src/shell/top-band.tsx:31-34`（`location.pathname.endsWith(tab.suffix)`）
  - `web/src/shell/top-band.tsx:68-72`（`onChange` 只在 key 变化时触发）
- **建议修法**：改成前缀/段匹配，例如 `const rest = location.pathname.slice(`/c/${sessionId}`.length); const active = THREAD_TABS.find(t => t.suffix === '' ? rest === '' : rest === t.suffix || rest.startsWith(t.suffix + '/'));`。

### F3【严重】390 宽下文件分栏破版（不是覆盖层也不是可用并排）

- **现象**（390×844，实测几何）：`.zk-thread` **10px**、`.zk-composer` 16px、`textarea` **0px**、`.zk-inspector` x=31/w=359/**y=0,h=844**（越过 y=87 的顶栏），`documentElement.scrollWidth=390`（没有横向滚动可救）；截图 15/16 显示对话列只剩一条被裁的「对话」字样，检查器的「开始/文件」页签压在顶栏上。
- **复现**：视口 390×844 → `/c/:id/files/src/util.ts`。
- **期望**：窄屏只显示一个面板（检查器全宽 + 明确的"返回对话"），或走 Drawer；不应把对话列压到 10px。
- **根因**：`Splitter` 两栏 `min={360}` / `min={280}` 无响应式分支：
  - `web/src/features/app-routes.tsx:33-42`
  - `web/src/design/global.css:1678-1680`（`.zk-thread-split{height:100%}`，无媒体查询）
- **建议修法**：用 `useMediaQuery('(max-width: 768px)')`（已有 hook：`web/src/design/use-system-appearance.ts:59`）在窄屏渲染单栏；或给 `.zk-thread-split` 加 `@media` 规则把第二栏改成 `position:absolute; inset:0` 的覆盖层并在顶部给返回按钮。

### F4【中】"改动过的文件在树上有标记" 从不渲染

- **现象**：`/api/files/tree` 返回 `src/util.ts` `changed:true`；展开 `src` 后树里能看到 `util.ts`，但 `.zk-tree-changed` 计数 **0**（浅色/深色都一样）。
- **根因**：节点上挂了 `icon`，但 `<Tree>` 没有 `showIcon`，antd 不渲染自定义图标：
  - `web/src/features/inspector/inspector-panel.tsx:38-40`（`icon: <span className="zk-tree-changed" …/>`）
  - `web/src/features/inspector/inspector-panel.tsx:164-172`（`<Tree …>` 缺 `showIcon`）
- **建议修法**：`<Tree showIcon …>`（并确认 `aria-label={t('inspector.changed')}` 落在可读元素上），或改用 `title` 内的可见徽标而不是 `icon`。

### F5【中】`/c/:id/files` 与 `/c/:id/guide` 没有行为区别

- 与 F1 同源：两条路由渲染同一个「开始」正文，差别只有 URL 和顶栏高亮（`/files`→文件、`/guide`→对话）。"文件"入口没有"进文件页"的语义，用户无法从 `/guide` 理解两者差异。
- 修法同 F1（路由决定内容），另外建议 `/guide` 不要高亮「对话」而是有自己的标题，或顶栏对这两条子路由都不高亮。

### F6【严重】自动化：任何一次写入失败都会清空整张任务列表（且红条不消失）

- **现象**：一次失败的保存（例如 `daily 09:00`）之后，页面主体只剩一行英文 `automation create failed: 400 Bad Request`，**表格整体消失**（`tableExists:0`、`rows:0`，截图 34/35 可见左侧 7 条任务全没了）；关掉弹窗也不恢复，只有刷新才好。开关失败同理：`automation update failed: 400 Bad Request` + 表消失（截图 36）。
- **复现**：新建任务填 `daily 09:00` → 保存（网关 400）→ 关弹窗观察列表。
- **期望**：错误提示与列表并存（错误应是 `Alert`/`message` + 字段级错误），列表保留；错误在成功重载或重新打开弹窗时清除。
- **根因**：渲染是三元而非叠加，且失败路径只 `setError` 不重载：
  - `web/src/features/automation/automation-screen.tsx:148-159`（`{error ? <div className="zk-msg-error">{error}</div> : tasks === null ? … : <Table/>}`）
  - `web/src/features/automation/automation-screen.tsx:115`（`submit` 失败只 `setError`，不 `reload`、不清 error）
- **建议修法**：把错误横幅移到三元之外（`<>{error && <Alert/>}{tasks ? <Table/> : …}</>`）；`openCreate/openEdit` 时 `setError(null)`；把网关 400 的 schedule 错误用 `form.setFields([{name:'schedule', errors:[msg]}])` 落到字段上（并翻译，别显示 `400 Bad Request` 原文）。

### F7【严重·根因】自动化语法"镜像"与网关不一致：误拦 6 条合法写法（5 类形态）、误放 9 条非法写法（5 类形态）

网关真值（`internal/contracts/automation.go`）与前端镜像（`web/src/features/automation/schedule-hint.ts`）逐条对比：

| 类别 | 网关接受 | 网关拒绝 |
|---|---|---|
| 前端**接受** | ✅ 交集：`@hourly`、`@daily`/`@midnight`、`every <N>s`、`after <N>s`、`cron <5 段>` | ❌ **误放**：`daily`（裸）、`daily HH:MM`、`weekly`（裸）、`weekly <周几> [HH:MM]`、`at`（裸）、`at HH:MM`、`every/after <N>m|h|d`、`every 0s`、`after 0s` |
| 前端**拦截** | ❌ **误拦**：裸五段 cron（`0 8 * * *`）、`*/N * * * *`、`daily HH:MM:SS[.fff] [zone]`、`weekly HH:MM:SS[.fff] [zone] <1..7>`、`at <RFC3339>`、`cron <5 段> <zone>`（= 网关自己的规范形式） | ✅ 正确拦（`tomorrow`、`every day`、`cron 0 8 * *` 等） |

- **关键反例（最容易被用户撞到）**：网关把任何 cron 任务规范化为 `cron 0 8 * * * Asia/Shanghai`；打开该任务的「编辑」→ 保存 → 客户端判"非法"，**一次 PATCH 都不发**（截图 29/30）。也就是"用网关自己的数据回来编辑"是做不到的。
- **根因（文到行）**：
  - `web/src/features/automation/schedule-hint.ts:9` `DURATION = /^\d+(s|m|h|d)$/` —— 网关只接受秒：`contracts.ParseAutomationSchedule` → `parseAutomationDelay`（`internal/contracts/automation.go:667-676`，"needs a whole number of seconds like 30s"）。
  - `schedule-hint.ts:10` `HHMM = /^\d{1,2}:\d{2}$/` + `:15-19` —— 网关要 `HH:MM:SS[.fff]`：`parseAutomationClock`（`automation.go:546-567`，len(parts)!=3 即报错）。
  - `schedule-hint.ts:37-41`（weekly"周几在前"）—— 网关是 `weekly <HH:MM:SS> [zone] <ISO 周几>`：`parseAutomationWeeklySelector`（`automation.go:428-445`）。
  - `schedule-hint.ts:45-47`（cron 只允许 5 段）—— 网关允许 5 段 + 可选 zone（共 7 个 token）：`parseAutomationCronSelector`（`automation.go:463-471`）。
  - `schedule-hint.ts:48-51`（裸五段 → `needsCronPrefix`）—— **网关明确接受裸五段**：`automation.go:311-312`（文档）、`:369-371`（分发）、`:491-532`（`parseAutomationBareCron`，`M H * * *` 解析为 daily，`*/N * * * *` 解析为 every N minutes）。实测 `POST /api/automation {"schedule":"0 8 * * *","enabled":true}` → **200**，`nextRun=2026-11-25T08:00:00+08:00`。
- **建议修法**（两条路，任选其一但别自造第二套语法）：
  1. **去掉镜像**：表单只做"非空 + 形态粗检"，合法性交给网关——把 400 的 message 落到 `schedule` 字段（见 F6 建议），错误文案本地化；这样永不失配。
  2. **要本地校验就生成**：由 `internal/gateway/contract.json`（`gen:ws-contract` 同一机制）导出一份可接受形态清单，`schedule-hint.ts` 变生成物，并加一条 Go 侧表驱动测试（`automation.go` 的 Parse 结果 ⇄ 清单）防漂移。至少要按上表把 6 类合法形式放行、4 类非法形式拦下，并把 `HH:MM` 文案改成 `HH:MM:SS`。

### F8【中】帮助文案本身仍然与网关不一致（这条正是 `7e2d58b` 声称修掉的）

- **原文（实际渲染，`.ant-form-item-extra`）**：
  > `接受：@hourly、@daily、daily 09:00、weekly mon 09:00、every 24h、after 2h、cron 0 8 * * *。裸五段 cron 需要在前面加 cron。`
  > 英文（`web/src/i18n/en.ts:358`）：`Accepted: @hourly, @daily, daily 09:00, weekly mon 09:00, every 24h, after 2h, cron 0 8 * * *. A bare five-field cron needs the cron prefix.`
- **实测**：7 种里 **4 种网关 400**（daily 09:00 / weekly mon 09:00 / every 24h / after 2h），第 8 句"裸五段需要 cron 前缀"是**假的**（网关 200 接受）。
- **漏列但合法**：`at <RFC3339>`、`*/N * * * *`、`daily HH:MM:SS`、`weekly <HH:MM:SS> <1..7>`、`cron … <zone>`、`@midnight`（后者前端接受）。placeholder 也是非法值：`web/src/features/automation/automation-screen.tsx:280` `placeholder="daily 09:00"`。
- **建议修法**：文案按 `internal/contracts/automation.go:307-327` 的注释逐条抄（`*/N * * * *`、`M H * * *`、`@hourly/@daily/@midnight`、`every <N>s`、`after <N>s`、`at <RFC3339>`、`daily HH:MM:SS[.fff] [zone]`、`weekly HH:MM:SS[.fff] [zone] <1..7>`、`cron <5 段> [zone]`），placeholder 改 `cron 0 8 * * *` 或 `daily 09:00:00`；加一条 vitest 把文案里的每个示例过一遍镜像（F7 修好后这条测试才有意义）。

---

## 3. 附带观察（不计入失败，但建议记一笔）

1. **`上下文` tab 迟到**：冷加载 0.3s 只有 4 个 tab，2.8–4.5s 后第 5 个才出现（能力位来自 initialize）。首屏/截图与"五个 tab"的验收口径会打架，建议 tab 条在 initialize 到达前不要渲染（或骨架），避免跳动。
2. **错误文案未本地化**：网关 400 在页面上显示为 `automation create failed: 400 Bad Request`（`describeError` 兜底），中文界面里是英文 + HTTP 码。
3. **Esc 关历史弹窗**：一次实测（焦点在触发按钮上）Esc 无效；重测（焦点在弹窗内）Esc 有效。属 antd 焦点相关行为，不判失败，但值得在历史弹窗上加 `keyboard`/主动聚焦以确保稳定。
4. **禁用态可存非法计划**：`POST /api/automation {schedule:"tomorrow", enabled:false}` → 200 存下（网关只在启用/计算 nextRun 时解析）。之后开关打开 → 400（前端已正确回滚，但按 F6 整张表会被清空）。建议网关在 Create/Patch 时始终校验 schedule，或前端在保存成功后回读并提示。
5. **`数据目录 workspace` 由验收员填充**（`README.md`/`src/app.ts`/`src/util.ts` + 一个未提交改动），只为让检查器有内容；未改 `web/src`。

---

## 4. 截图（`/data/zeokeel/.cache/ui-audit/findings3/shots/`）

| 文件 | 内容 |
|---|---|
| `audit-tails-01-thread-5tabs.png` | 1440 会话顶栏 5 个 tab（对话/轨迹/记忆/上下文/文件） |
| `audit-tails-02-files-split.png` | 点「文件」后：左对话 + 右检查器（停在「开始」指南，非文件树） |
| `audit-tails-05-back-conversation.png` | 点回「对话」恢复单栏 |
| `audit-tails-06-history-back-files.png` | 浏览器后退回到 `/files`（split + 检查器） |
| `audit-tails-07-deeplink-reload.png` | 深链 `/c/:id/files` 刷新后 tab/URL 一致 |
| `audit-tails-08-guide-route.png` / `09-files-tree.png` | `/guide` 与 `/files` 渲染完全相同（F5 证据） |
| `audit-tails-12-deeplink-file-reload.png` | `/files/src/util.ts` 刷新后：树 + 预览 + 顶栏错标「对话」 |
| `audit-tails-13-draft-after-reload.png` | 草稿刷新后仍在 |
| `audit-tails-14/15/16-390-*.png` | 390 破版：对话列 10px、检查器覆盖顶栏、无横向滚动 |
| `audit-tails-17/18/19-dark-*.png` | 深色下文件预览、diff、指南（对比度 ≥10:1） |
| `audit-tails-20/21-390-*.png` | 390 普通会话（全宽正常）+ 抽屉可用 |
| `audit-tails-22/23-*.png` | 自动化页 + 新建弹窗（含帮助文案） |
| `audit-tails-24-blocked-bare-cron.png` | `0 8 * * *` 被拦（"需要 cron 前缀"）—— 而网关接受它 |
| `audit-tails-25-only-schedule-required-errors.png` | 只填合法表达式：仅两个必填错误 |
| `audit-tails-26/27/28/28b-*.png` | 列表 / 开关 / 历史空态 / 历史有记录 |
| `audit-tails-29-edit-canonical-prefill.png`、`30-edit-save-canonical.png` | 编辑网关规范化的 cron 任务 → 判非法、PATCH 未发 |
| `audit-tails-32/33-*.png` | 删除确认 / 删除完成 |
| `audit-tails-34/35-*.png` | 一次 400 后整张列表被清空（关弹窗后仍空） |
| `audit-tails-36/37-*.png` | 禁用态脏计划：开关失败清空列表 / 编辑被拦 |
| `audit-tails-38-tab-trap-1440.png` | 文件路径 URL 下顶栏标「对话」、点击无效 |
| `audit-tails-39-tree-expanded.png` | 展开 `src` 后树有 util.ts，但「已改动」标记缺省 |
| `audit-tails-40-boundary-last.png`、`41-subtab-noop.png` | 边界用例末态 / 子页签点击无效 |
| `audit-tails-42-dark390-files.png`、`43-dark390-drawer.png` | 深色 + 390：同样破版（检查器盖住顶栏、对话列 10px），抽屉可开 |

## 5. 证据与复现脚本

全部在 `/data/zeokeel/.cache/ui-audit/findings3/work/`（Node + Playwright，真网关 9824）：

- `run-files.mjs` → `files.json`：五 tab、点击、后退前进、深链、刷新
- `run-files2.mjs` → `files2.json`：检查器树/预览/diff、`/files` vs `/guide`、草稿持久化
- `run-mobile-dark.mjs` → `mobile-dark.json`、`run-mobile2.mjs` → `mobile2.json`：390 几何与 tab 时间线
- `run-final.mjs` → `final.json`：深色对比度（alpha 合成）、文件 URL 的 tab 陷阱
- `run-auto.mjs` → `auto.json`：13 个表达式逐条（拦截/文案/POST/状态）+ 帮助文案原文 + 只填计划的情况
- `run-auto2/3/4.mjs` → `auto2/3/4.json`：列表、开关、历史、编辑、删除、列表被错误清空
- `run-boundary.mjs` → `boundary.json`：12 个边界输入
- `probe_schedules.sh` + `specs-claim.txt`：不经 UI 的网关真值矩阵（`enabled=true`）
- 复算 bundle 指纹：`work/bundle/`（6 个 HTTP 抓取文件 → `8c3c40dc…5723` 与 gateway.json 一致）
---

# 第二轮复验

- 时间：2026-11-25 05:13–05:35（北京时间）
- 被测提交：`59bb436 fix(web,gateway): the file inspector is reachable, and the gateway owns the schedule grammar`
- 实例：`bash startB.sh tails2 /data/zeokeel/.cache/bin/zeokeel-tails2 9840 9841`，数据目录 `data-tails2`（全新）
- 验收员仍然只读 `web/src`（`git status web/` 干净）

## 0. 版本自证

| 项 | 值 |
|---|---|
| 网关 | `http://127.0.0.1:9840`；`GET /`=200、`/favicon.svg`=200、`/api/capabilities` 无授权=401 |
| gateway.json | `bundleSha256=7410a7335c0e3da21307d1bdc78dc986dff0f3086b85145174e2f221d98c205f`、`bundleBuiltAt=2026-11-24T21:13:25Z` |
| **HTTP 复算** | 抓 `index.html`/`favicon.svg`/`index-CNGM3wNl.js`(+`.map`)/`index-D5a9G2sF.css`/`.keep` 共 6 个文件，按 `build-web-client.sh` 算法复算 = **`7410a733…205f`，与 gateway.json 完全一致** |
| bundle 内容 | 命中 `返回对话`、`示例：@hourly、…、at 2026-12-01T09:30:00+08:00。校验由网关完成，写错时弹窗里会给出原因。`；`scheduleProblem` 逻辑已不在（只剩一条未使用 i18n 文案） |
| 工作区 | 由验收员铺了 `README.md`、`src/main.ts`、`src/util.ts`（未提交改动）、`src/deep/nested/file.ts`（为 F2 深路径） |

## 1. 逐条结论

| # | 断言（lead 给的证伪点） | 结果 | 证据（实测） |
|---|---|---|---|
| F1 | 从顶栏「文件」**一步**进树；`/files` 与 `/guide` 内容不同；子页签可来回切 | **PASS** | 点「文件」后：`url=/c/:id/files`、`hasTree=true`、`treeNodes=3`、`treeTexts=["README.md","src"]`、`hasGuideBody=false`、`activeSub=文件:true`、`preview="选择一个文件查看内容。"`；点「开始」→ `/guide` + 指南正文；再点「文件」→ 回到树（桌面与 390 各验证一次） |
| F2 | `/files/<深路径>` 刷新 → activeTab=文件；点「对话」回 `/c/:id`（含 390） | **PASS** | `/files/src/deep/nested/file.ts`：`activeTop=["文件"]`；点「对话」→ `/c/:id`、`split=false`、composer 在；`goBack` 回到文件页且 active 仍是文件；`reload` 后同；390 下 `activeTop=["文件"]` |
| F3 | 390/375/320 单栏 + 返回、composer 不为 0、无横向滚动、深色同、1440 仍分栏 | **PASS（主路径）/ 见 R2-1** | 390/375/320：`narrow=true`、`thread=null`、`composer=null`、`overflowX=false`、`back` 存在（w=334/320/295）；点返回 → `/c/:id`、composer 348/333/278px、thread 390/375/320px；深色 390/375/320 同为窄屏单栏（`inspector x=30/w=345、x=0/w=320`，`composer=false`，`overflowX=false`，预览文字 `rgb(249,250,251)` 可读）；1440：split=true、inspector 380px@x=1060、composer 在 |
| F4 | `changed:true` 的文件出现 `.zk-tree-changed` | **PASS** | 展开 `src` 后 `marks=1`、`aria-label="已改动"`；深色同上（`rgb(245,158,11)`，6×6px 圆点） |
| F6/F7 | 六种合法形式全部不被前端拦（该 200 的都 200） | **PASS** | `0 8 * * *`、`*/15 * * * *`、`daily 09:00:00`、`weekly 09:00:00 1`、`cron 0 8 * * * Asia/Shanghai`、`at 2026-12-01T09:30:00+08:00`：每条 `POST 1 次 → 200`，列表逐条 +1，无红条、无弹窗错误 |
| F6/F7 | `daily 9am`、`tomorrow` 由网关拒绝、弹窗显示原因、不发第二次 | **PASS（附 R2-2）** | 两者均 `POST 1 → 400`；弹窗保持打开并在框内显示 `[data-testid=automation-form-error]`：`automation create failed: automation: schedule "daily 9am": time "9am" must be HH:MM:SS with optional one to three fractional digits`；列表行数 6→6 不变；再点一次保存仍只发 **1** 次 POST |
| F8 | 编辑网关自己存的任务 → 原样保存成功 | **PASS** | 打开 `cron 0 8 * * * Asia/Shanghai` 任务：预填一致；只改标题 → `PATCH … 200`，请求体 `{"title":"R2 改个标题",…,"schedule":"cron 0 8 * * * Asia/Shanghai","enabled":true}` |
| F9 | 写入失败后列表仍在；关弹窗后错误可清除；describeError 显示网关原文 | **部分 PASS** | 列表：PATCH 400 后 `rows=6, tableExists=1`（不再清空）；页级红条内容已是网关原句 `automation update failed: automation: schedule "tomorrow": want a five-field cron expression, …`；但「关弹窗后错误可清除」**未做到**：`formError` 不清（R2-2）、页级红条也不随成功开关清除（R2-3） |
| 回归 | 列表 / 历史 / 开关 / 删除 / 草稿 / 上轮 PASS 项 | **PASS** | 开关 `PATCH 200` + 回读 `aria-checked=false`；历史 `运行历史 · …` + 空态；删除 `DELETE 200`，6→5；草稿 `R2 草稿` 在切「文件」、切回、刷新后都在（`zk.draft.sess-…`）；轨迹/记忆/上下文 tab 的 active 与 URL 一致 |

## 2. 新增 / 遗留失败项

### R2-1【中】窄屏检查器是「右侧对齐的固定覆盖层」：挡住顶栏 tab、留死区、900px 处两套断点打架

- **现象（实测几何）**：
  - 390：inspector `position:fixed; z-index:40`，`x=31, w=359(=92vw), y=0, h=844`；375 为 `x=30,w=345`；320 为 `x=0,w=320`（**全宽，5 个 tab 全部被盖**）。顶栏 5 个 tab 在 `x=16..298 / y=48`，命中测试：`对话`→`.ant-tabs-tab-btn`（可点），`轨迹/记忆/上下文/文件`→**`.zk-inspector__tabs`**（被面板盖住）。**实测点这三个 tab 均 3s 超时**，用原始坐标 `mouse.click(90,67)` 也无任何跳转 → 在文件页上无法用顶栏切到轨迹/记忆/上下文，只能先「返回对话」。
  - 899：`narrow=true`，inspector 只有 `420px`（`min(420px,92vw)`）贴右，左侧 **479px 全空白**（截图 16）。
  - 900：JS 断点是 `max-width: 899px` → 走分栏，CSS 断点是 `max-width: 900px` → inspector 变 fixed；结果 thread 520px 渲染出来、右栏 380px 空着、inspector 浮在 `480..900` 上，**把 composer 右缘压掉约 40px**（截图 15）。
- **复现**：390/899/900 宽，打开 `/c/:id/files`，看几何 + 点顶栏 tab。
- **期望**：窄屏面板应占满宽度、从顶栏下方开始（`top: <topband 高度>`）或做成抽屉；断点在 JS 与 CSS 必须是同一个值（899 或 900，二者取一），避免"分栏 + 浮层"叠加。
- **根因**：`web/src/design/global.css:2010-2021`（`@media (max-width: 900px) .zk-inspector{position:fixed;top:0;right:0;width:min(420px,92vw)}`，早于本提交）+ `web/src/features/app-routes.tsx:33`（`useMediaQuery('(max-width: 899px)')`）。
- **建议修法**：把窄屏分支与 CSS 合并为一处：`width:100%; inset-block-start: var(--zk-topband-h, 87px)`（或把面板渲染进布局流），并把 CSS 媒体查询改成 `max-width: 899px`；同时补一条 vitest/RTL 断言"窄屏下 `.zk-inspector` 不与 `.zk-topband` 相交、`.zk-topband .ant-tabs-tab` 不被遮挡"。

### R2-2【中】弹窗内的错误不清：关掉再新建，上一条英文报错还在

- **现象**：`daily 9am` 保存失败 → 框内出现报错 → 关闭再点「新建任务」，**同一条英文报错仍在**（`reopen_after_daily_9am.formError` 有值）。
- **复现**：新建 → 填 `daily 9am` → 保存（400）→ ✕ → 再点「新建任务」。
- **期望**：新弹窗是干净表单，无上次报错。
- **根因**：`web/src/features/automation/automation-screen.tsx:90`（`openCreate`）与 `:96`（`openEdit`）只 `setFieldsValue`，没有 `setFormError(null)`（该 state 定义在 `:44`，只在 `submit` 的 `:114` 清）。
- **建议修法**：在 `openCreate`/`openEdit` 里加 `setFormError(null)`（或改用 `form.setFields([{name:'schedule',errors:[msg]}])`，把原因挂到字段上，随表单重置一起消失）。

### R2-3【小】页级错误条没有关闭按钮，成功的开关操作也不清它

- **现象**：非法存量任务（`tomorrow`, 禁用）开开关 → PATCH 400 → 列表保住、出现红条（内容已是网关原句）；接着把另一条合法任务开关**成功**切换一次，红条**仍在**（`after_successful_toggle.pageBanner` 仍非空）。
- **期望**：错误条可关闭，或在任何成功写入后消失。
- **根因**：`web/src/features/automation/automation-screen.tsx:64-88`（`toggle` 成功路径不回写错误，也不 `reload`）；渲染处 `:156` 的红条没有关闭入口。
- **建议修法**：`toggle` 成功后 `setError(null)`；红条加 `closable`（antd `Alert` 有）。

### R2-4【中】帮助文案仍列 4 个网关拒绝的示例（含 placeholder）

- 实际渲染（`screen.automation.field.scheduleHelp`）：`示例：@hourly、@daily、daily 09:00、weekly mon 09:00、every 24h、after 2h、cron 0 8 * * *、0 8 * * *、at 2026-12-01T09:30:00+08:00。校验由网关完成，写错时弹窗里会给出原因。`；placeholder 仍是 `daily 09:00`。
- 在本实例逐条打网关：`daily 09:00` → 400、`weekly mon 09:00` → 400、`every 24h` → 400、`after 2h` → 400（其余 5 条 200）。4/9 的例子是照抄就会失败的值。
- **建议修法**：把示例换成 `daily 09:00:00`、`weekly 09:00:00 1`、`every 30s`、`after 30s`；placeholder 改 `cron 0 8 * * *` 或 `daily 09:00:00`。参考 `internal/contracts/automation.go:307-327` 的注释原文。
- 位置：`web/src/i18n/zh.ts:367` / `web/src/i18n/en.ts`（同 key）、`web/src/features/automation/automation-screen.tsx:289`。

### R2-5【小】深链不会在树里展开/露出被选中的文件

- **现象**：`/files/src/deep/nested/file.ts` 右侧预览正确，但左侧树仍是折叠态（`nodes=["README.md","src"]`、`expanded=0`、`selectedVisible=0`）——看不到"我正在看哪个文件"，也没法从树里往上点。
- 期望：按选中路径展开祖先并滚动到选中项（antd `Tree` 的 `expandedKeys`/`autoExpandParent` 或 `scrollTo`）。
- 位置：`web/src/features/inspector/inspector-panel.tsx:178-190`（`<Tree … selectedKeys={[selected]}>` 未给 `expandedKeys`）。
- 顺带：「已改动」标记是 6×6px 圆点（`rgb(245,158,11)`），信息能读到但偏小。

### R2-6【观察】`上下文` tab 仍迟到

- 冷加载先 4 个 tab，2.8–4.5s 后第 5 个才出现（`deep_path_state.tabs` 只有 4 个，1.2s 后同页变 5 个）。机制是 `TAB_CAPABILITY` 等 initialize，不属本轮改动，但"五个 tab"的验收口径建议改成"initialize 后 5 个"，或让 tab 条在能力位到达前不渲染。

## 3. 第二轮截图（`/data/zeokeel/.cache/ui-audit/findings3/shots/`）

| 文件 | 内容 |
|---|---|
| `audit-tails-r2-01-thread.png` | 会话五 tab |
| `audit-tails-r2-02-files-tree-direct.png` | **点「文件」一步进树**（F1 修好） |
| `audit-tails-r2-03-subtab-roundtrip.png` | 开始↔文件 子页签来回切 |
| `audit-tails-r2-04-changed-marker.png` | 展开后「已改动」标记出现（F4） |
| `audit-tails-r2-05-tree-select.png` | 树里选中 util.ts → 预览 |
| `audit-tails-r2-06-deep-path.png` / `07-…-back.png` / `08-…-reload.png` | 深路径 active=文件、点「对话」回会话、刷新仍一致（F2） |
| `audit-tails-r2-09-files-reload.png` | `/files` 深链 + 刷新 |
| `audit-tails-r2-10-narrow{390,375,320}-files.png` / `11-…-back.png` | 窄屏单栏 + 返回 + 回会话后 composer 正常 |
| `audit-tails-r2-12-boundary{899,900}.png` | **899 右侧面板留 479px 空白 / 900 分栏与浮层叠加**（R2-1） |
| `audit-tails-r2-13-dark390.png` | 深色 390 窄屏 |
| `audit-tails-r2-14-deeplink-tree-reveal.png` | 深链不展开树（R2-5） |
| `audit-tails-r2-15-overlay900.png` / `16-overlay899.png` / `17-overlay390.png` / `18-390-tab-tap.png` | 覆盖层几何与顶栏被遮（R2-1） |
| `audit-tails-r2-20-help.png` | 新的帮助文案 + placeholder |
| `audit-tails-r2-21-all-pass-created.png` | 六种合法形式全部创建成功 |
| `audit-tails-r2-22-refused-daily-9am.png` / `…-tomorrow.png` | 网关拒绝、弹窗内显示原因、列表仍在 |
| `audit-tails-r2-23-reopen-stale-error.png` | **关闭再新建，旧报错还在**（R2-2） |
| `audit-tails-r2-24-edit-canonical-saved.png` | 编辑网关规范化的 cron 任务保存成功（F8） |
| `audit-tails-r2-25-history.png` / `26-delete.png` | 历史 / 删除回归 |
| `audit-tails-r2-27-toggle-fail-list-kept.png` / `28-banner-after-success.png` | 失败后列表保住 / 成功开关后红条仍在（R2-3） |
| `audit-tails-r2-29-draft.png` | 草稿跨 tab + 刷新 |
| `audit-tails-r2-30-390-roundtrip.png` / `31-dark-marker.png` | 390 子页签往返 / 深色标记 |
| `audit-tails-r2-32-dark375.png` / `-dark320.png` | 深色 375 / 320 窄屏单栏 |

## 4. 第二轮脚本与原始数据

`/data/zeokeel/.cache/ui-audit/findings3/work/`：

- `r2-files.mjs` → `r2-files.json`（F1/F2/F4 + 深链 + 刷新 + 历史）
- `r2-narrow.mjs` → `r2-narrow.json`（390/375/320/899/900、返回、深色、深链露出）
- `r2-chain.mjs` / `r2-overlay.mjs` / `r2-tap.mjs` → 覆盖层祖先链、命中测试、390 tab 点击
- `r2-auto.mjs` → `r2-auto.json`（六种合法形式、两种拒绝、二次提交、F8、开关/历史/删除）
- `r2-final.mjs` → `r2-final.json`（帮助示例的网关真值、失败后列表、红条清除、草稿、tab 匹配）
- `r2-extra.mjs`（390 往返、深色标记、文件 tab no-op + 面包屑）
- `bundle2/`：第二轮 bundle 的 6 个 HTTP 文件（复算 `7410a733…205f`）

---

# 第三轮（定点）复验

- 时间：2026-11-25 05:26–05:45（北京时间）
- 被测提交：`d9f15c8 fix(web): the narrow inspector is a column, and every help example is real`
- 实例：`bash startB.sh tails3b /data/zeokeel/.cache/bin/zeokeel-tails3 9850 9851`（数据目录 `data-tails3b`，干净）
  - 说明：`data-tails3` 已被 lead 的实例占用（另一实例在 `:9842`）。两者 `bundleSha256` **同为 `5864bc94609158fde95ccb19e4da683f524cb84efbbe70ea02a80e3dbea4d352`**，即同一份构建；本轮用我自己的干净实例取证。

## 0. 版本自证

| 项 | 值 |
|---|---|
| 网关 | `http://127.0.0.1:9850`；`GET /`=200、`/favicon.svg`=200、`/api/capabilities` 无授权=401 |
| gateway.json | `bundleSha256=5864bc94…d352`、`bundleBuiltAt=2026-11-24T21:25:24Z` |
| **HTTP 复算** | 抓 `index.html`/`favicon.svg`/`index-B3ySySnS.js`(+`.map`)/`index-D263UQ73.css`/`.keep` 6 个文件复算 = **`5864bc94…d352`**，与 gateway.json 一致 |
| 内容抽查 | 帮助文案已是 `示例：…daily 09:00:00、weekly 09:00:00 1、every 86400s、after 120s…`；`关闭错误提示` 与 `.zk-msg-error__dismiss` 均在新 bundle 里 |

## 1. 定点清单结论

| # | 断言 | 结果 | 证据（实测） |
|---|---|---|---|
| R2-1 | 320/375/390/899：inspector 从顶栏下方开始、不覆盖顶栏、两个子页签可点、无横向滚动 | **PASS** | 四档全部：`position:static`、`insp.y=87`（= `band.bottom=87`）、`inspAboveBand=false`、`coversBandTabs=false`、`overflowX=false`；`开始/文件` 的命中测试都是自己的 `BUTTON.zk-inspector__tab`；点「开始」→ `/guide`（`guideBody=true`）、点「文件」→ `/files`（tree=true）；**顶栏 `轨迹` 点击成功跳到 `/trajectory`**（上一轮在 390 是 3s 超时） |
| R2-1 | 900 必须是分栏且不与浮层叠加 | **PASS** | `narrow=false`、`thread={x:0,y:87,w:520}`、`insp={x:520,y:87}`、`position:static`、composer 520px 完整可见；顶栏 `轨迹` 可点 → `/trajectory` |
| R2-1 | 1024 / 1440 未回归 | **PASS** | 1024：thread 380 + inspector 380@x=644（右缘正好 1024）；1440：thread 796 + inspector 380@x=1060；两者 `inspAboveBand=false`、无溢出 |
| R2-1 | 深色同 | **PASS** | 390 深色：`y=87`、`static`、无溢出、返回键 365px；900 深色：`inspRight=840`、文本 `rgba(255,255,255,.85)` / `rgb(199,202,217)` 可读 |
| R2-4 | 帮助文案里**每个**示例都能建（9 个） | **PASS** | 文案解析出 9 条：`@hourly`、`@daily`、`daily 09:00:00`、`weekly 09:00:00 1`、`every 86400s`、`after 120s`、`cron 0 8 * * *`、`0 8 * * *`、`at 2026-12-01T09:30:00+08:00`；逐条 UI 建任务，每条 `POST 1 → 200`，行数 0→1→…→9，无一条报错；placeholder = `daily 09:00:00`（网关接受） |
| R2-2 | 失败后关弹窗再新建不得带旧报错 | **PASS** | 新建填 `daily 9am` → 400，框内显示网关原句；✕ → 再点「新建任务」：`formError=null`。编辑路径同样：改 `tomorrow` → 400 → ✕ → 再编辑：`formError=null` |
| R2-3 | 页级错误可点「×」清除；开关成功后自动消失 | **PASS** | 脏计划（`tomorrow`, 禁用）开开关 → PATCH 400 → 红条 = 网关原句 + `×`（`dismiss=true`）；点 `×` → `pageBanner=null`；再制造一次失败 → 把合法任务开关**成功**切换 → `afterSuccess.pageBanner=null` |
| R2-5 | 深链打开后文件在树中可见；标记 8px | **PASS** | `/files/src/deep/nested/file.ts`：树节点 `["README.md","src","deep","nested","file.ts","main.ts","util.ts"]`、`expanded=3`、`selected=file.ts`；`.zk-tree-changed` = **8×8px**（`getComputedStyle().width = 8px`，`aria-label="已改动"`） |
| 回归 | F1 / F2 / F8 / 历史 / 删除 / 草稿 | **PASS** | 点「文件」→ `tree=true, guide=false, nodes=3, active=文件`；`/files/src/main.ts` → `active=文件`，点「对话」→ `/c/:id` 且 `split=0`；编辑 `cron 0 8 * * * Asia/Shanghai` → `PATCH 200`、`formError=null`；历史弹窗正常；删除 10→9（DELETE 200）；草稿 `R3 草稿` 在切 tab 与刷新后都在 |

## 2. 剩余（既有）残留，不阻塞

### R3-1【小·既有】900–1023px 下 inspector 固定 320px，落在 380px 面板里 → 右侧留 60px 空白

- **现象**：`900/950/1023` 三档：左栏 520/570/643、右栏面板 380，但 `.zk-inspector` 只有 320px，右缘分别停在 840/890/963；`elementFromPoint(vw-5, 200)` = `DIV.ant-splitter-panel`（那块区域既不是面板内容也不是阴影，是空白条）。1024 起恢复正常（380/380，`gapRight=0`）。深色下同样存在。
- **来源**：不是本轮引入。`web/src/design/global.css:2004-2009` 的 `@media (max-width: 1023px) { .zk-inspector { width: 320px; min-width: 320px } }` 来自 `8fe3f4a`；`59bb436` 里它在 ≤900 被覆盖层规则盖掉，所以只有 901–1023 能看到；`d9f15c8` 把 900 改成真分栏后，900 也落进这段。
- **复现**：视口 900×800 → `/c/:id/files` → 看右栏右缘（截图 `audit-tails-r3-05-gap900.png`）。
- **期望**：面板自己已经定宽（`Splitter.Panel`），inspector 应 `width:100%`。
- **建议修法**：删掉或收窄该规则（例如只在 ≤899 生效，或改 `max-width: 320px`），一行改动；`d9f15c8` 的 ≤899 规则已设 `width:100%`，把 1023 这条删掉即可。

### R3-2【观察】`上下文` tab 仍是"先 4 后 5"

- 本轮 390 计时：`139ms → 4 个 tab`、`295ms → 5 个`（比第二轮的 2.8–4.5s 快很多，属 initialize 到达的竞态）。建议验收口径写成"initialize 后 5 个"，不阻塞。

## 3. 第三轮截图

| 文件 | 内容 |
|---|---|
| `audit-tails-r3-00-thread.png` | 第三轮会话（五 tab） |
| `audit-tails-r3-01-narrow{320,375,390,899}.png` | 窄屏整宽列、从 y=87 开始 |
| `audit-tails-r3-02-narrow*-band.png` | 窄屏点顶栏 `轨迹` 成功跳转 |
| `audit-tails-r3-03-wide{900,1024,1440}.png` | 900 分栏（thread 520 + inspector 520..）+ 1024/1440 正常 |
| `audit-tails-r3-04-dark390.png` / `-22-dark900.png` | 深色窄屏 / 深色 900 |
| `audit-tails-r3-05-gap{900,950,1023,1024}.png` | **R3-1：900–1023 右侧 60px 空白**（1024 正常） |
| `audit-tails-r3-10-help.png` | 新帮助文案 + placeholder `daily 09:00:00` |
| `audit-tails-r3-11-all-examples-created.png` | 9 个示例全部创建成功（9 行） |
| `audit-tails-r3-12-refusal-in-dialog.png` / `13-reopen-clean.png` | 弹窗内网关原句 / 关掉重开已清空（R2-2） |
| `audit-tails-r3-14-toggle-failure-banner.png` / `15-after-dismiss.png` / `16-auto-cleared.png` | 页级错误带 × / 点 × 清楚 / 成功开关后自动消失（R2-3） |
| `audit-tails-r3-17-deeplink-expanded.png` / `18-marker8.png` | 深链展开树 / 标记 8px（R2-5） |
| `audit-tails-r3-19-edit-canonical.png` | 编辑网关规范化 cron 保存成功（F8） |
| `audit-tails-r3-20-wide900-nogap-overlay.png` / `21-390-back.png` | 900 顶栏可点 / 390 返回后 composer 348px |

## 4. 第三轮脚本与原始数据

`/data/zeokeel/.cache/ui-audit/findings3/work/`：

- `r3-session.mjs`（建会话）
- `r3-layout.mjs` → `r3-layout.json`（320/375/390/899 窄屏几何与命中、900/1024/1440、深色）
- `r3-gap.mjs`（900/950/1023/1024 的 60px 空白取证）
- `r3-auto.mjs` → `r3-auto.json`（9 个帮助示例逐个建、R2-2、R2-3、R2-5、F1/F2/F8/历史/删除）
- `r3-extra.mjs`（900 顶栏点击、390 返回、tab 4→5 计时、深色 900）
- `r3-draft.mjs`（草稿跨 tab + 刷新）
- `bundle3/`：第三轮 bundle 的 6 个 HTTP 文件（复算 `5864bc94…d352`）
