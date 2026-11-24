# 输入框附件（粘贴/拖放）+ 落地页空态推荐 —— 独立验收报告

- 验收对象：`40ed5d7 feat(web): paste and drop attach files; an empty landing offers three starts`（其后仅 `560fff0 docs: …`）
- 验收方式：真网关（`serve`）+ 真 Chromium（Playwright `/data/tavo/tools/e2e/chromium-1243/chrome-linux64/chrome`），所有数字来自 `page.evaluate` / CDP / 网络与 DOM 原文，不采信实现者说法
- 环境：主实例 `:9802`（`data-comp`，`chat.model=acceptance-tool`）、自建干净实例 `:9814`（`data-c2clean`）、自建慢速实例 `:9816`（`data-c2slow`，`chat.model=acceptance-slow`）；`fakemodel` `:9803/:9815/:9817`
- 截图：`.cache/ui-audit/shots/audit-composer-*.png`（46 张）；原始日志 `.cache/ui-audit/c2-t*.log`；脚本 `.cache/ui-audit/c2-t*.mjs`

## 结论

**✅✅ 最终：第五轮（定点）复验（提交 `c7b6c1a`，构建 `f479ded6`，实例 `:9824`/`:9826`）见 §11 —— **R4-1 PASS**：h=520 有卡时 481→1440 共 12 档全部 `display:none`、可见但不可点元素 0；无卡时 12 档全部 `flex` 且三条可点（1440×520 保持可用）；`:has()` 在 HeadlessChrome/153 支持、规则随卡片出现/消失即时切换、退化行为已记录。快查（R3-1 首帧、深色 sticky、R2-1、R2-5、队列撤回、F1）全 PASS。本项无已知未闭环缺陷，可收口。**

**第四轮（定点）复验（提交 `d8a8ba7`，构建 `ec1801b7`，实例 `:9820`/`:9822`）见 §10 —— R3-1 PASS（首帧 composer `300..480`、send `391..425` 在视口内、无双 composer、1440×900 无 sticky 副作用）；R3-2 点名视口全 PASS（1440×520/1280×520 可见可点、320×480 隐藏、390×844 正常、跨 560 即时切换）；深色 sticky 背景不透明 PASS；快查回归全 PASS。残留 R4-1（低—中）：短窗（h≤560）+ 审批卡 + 宽度 481–1024 时建议行换行后被审批卡盖住（可见不可点，且不可滚到），去掉审批卡后三条均可点。**

**⚠⚠ 第三轮复验（提交 `1d79fac` + `28077ce`，构建 `1ca0a432`(comp3) / `dbbc397e`(queue)，实例 `:9812`/`:9816`）见 §9 —— R2-1 / R2-5 / 队列回滚全 PASS；R2-3 机制 PASS（真 Esc 取消在 headless 下不产生事件）；R2-2 核心 PASS（建议不再有不可点元素），但 320×480 + 审批卡时 composer 首帧仍越界 69 px（滚动一次即完整）；快查回归全 PASS；队列重排未实现（协议无 `prompts.move`）。**

**⚠ 第二轮复验（修复提交 `4264b8a`、构建 `2b6c8b1b`、实例 `:9808`/`:9810`）见 §8 —— F1/F3/F5/F6 已 PASS，F2 只修落地页（线程页仍残留）、F4 在 320×480 仍不可达，回归 11 项全 PASS。**

**第一轮结论：基本通过：清单 7 项中 6 项 PASS，1 项 FAIL（第 7 项「禁用态下粘贴/拖放应被忽略」）。** 四条修复声称逐条成立（粘贴、拖放覆盖层与落地、三条推荐、与原回形针共用暂存/移除）。另发现 4 个低危缺陷 + 1 个未能在本环境证实的风险点（详见 §4）。

| # | 检查 | 结果 |
|---|---|---|
| 1 | 粘贴图片：附件出现、正文不变、`data-dragging` 不残留、`/api/blobs` 请求数 | **PASS** |
| 2 | 纯文本粘贴：文本出现在光标处、未被 preventDefault 吞掉 | **PASS** |
| 3 | 拖入覆盖层：`dragover` → `data-dragging=true` + 可见覆盖层；`dragleave` 到外部消失；子元素不闪断 | **PASS** |
| 4 | 拖放落地：多文件（图片+文本+超大）分别验证；超大文件给可读错误而非静默丢弃 | **PASS** |
| 5 | 推荐：三条文案与点击后内容一致、点击不发送、可编辑后发送、390 不溢出可点、深色可读 | **PASS** |
| 6 | 回归：回形针、删附件、带附件发送、审批链、Enter/Shift+Enter、Esc 层级、草稿持久化 | **PASS** |
| 7 | 边界：禁用（待审批）时粘贴/拖放被忽略；非文件拖拽不触发覆盖层 | **FAIL**（前半句失败、后半句通过） |

---

## 1. 版本自证（先证被测对象，再谈结论）

### 1.1 启动自检（`:9802` 与自建 `:9814`/`:9816` 同结果）

```
GET /                    → 200  457 B
GET /favicon.svg         → 200  374 B
GET /api/capabilities    → 401（无 Authorization）
GET /api/capabilities    → 200（Bearer <token>）
```

### 1.2 `bundleSha256` 复算（不是"读一个字段就相信"）

`gateway.json` 记录 `bundleSha256=3ca1c3d7924cb15ca589059c3798b99026a5d5eb37524ce58f4f79baf52d8237`、`bundleBuiltAt=2026-11-24T19:18:26Z`。

该值不是单个 JS 的哈希（我最初按单文件比得 `0c9a2b72…`，属误读）——它是 `scripts/build-web-client.sh:82-84` 的**目录指纹**：`dist/` 下除 `BUNDLE_INFO` 外全部文件，按路径 `LC_ALL=C sort -z` 后 `sha256sum` 再整体 `sha256sum`，并写进 `dist/BUNDLE_INFO`，由 `internal/gateway/webclient/embed.go:73-96` 读入、`internal/gateway/port.go:208-213` 写进 `gateway.json`。

**用 HTTP 取到的字节复算该指纹**（不是读磁盘）：

| 步骤 | 结果 |
|---|---|
| HTTP 取 `index.html` / `favicon.svg` / `assets/index-CKbNbYka.js` / `assets/index-B14LXmXm.css` / `assets/index-CKbNbYka.js.map`（5 个对外文件），再复刻不对外提供的 0 字节 `.keep` | 6 个文件，与 `BUNDLE_INFO` 的 `files 6` 一致 |
| `find . -type f ! -name BUNDLE_INFO -print0 \| sort -z \| xargs -0 sha256sum \| sha256sum` | **`3ca1c3d7924cb15ca589059c3798b99026a5d5eb37524ce58f4f79baf52d8237`** |
| 逐文件与 `internal/gateway/webclient/dist/` 比对 | `same` ×5（index.html / favicon.svg / index js / index css / js.map） |

→ **线上服务的就是这个构建**，与我本地 `dist/` 逐字节一致。

### 1.3 构建里确实包含本次修复

```
松手即添加附件          → 命中 1
每天早上帮我汇总一次未读事项 → 命中 1
附件超过 10 MB         → 命中 1
data-dragging / zk-composer__drop / now-suggestion- → 存在
```

`git status --porcelain web/src internal/gateway/webclient/dist` 为空；`HEAD=560fff0`（功能提交 `40ed5d7`，2026-11-25 03:19:22 +0800）。三个实例（9802/9814/9816）报同一 `bundleSha256`。

### 1.4 方法学限制（必须声明）

| 手段 | 用途 | 限制 |
|---|---|---|
| `Input.dispatchDragEvent`（CDP，真浏览器拖拽管线、真命中测试） | 覆盖层出现/消失/不闪断/非文件拖拽/拖到别处 | **本沙箱里 `files:[path]` 生成的 `File` 恒为 0 字节**（`{name:"c2-notes.txt",size:0}`），上传必失败 → 只能验"事件与视觉"，不能验"字节落地" |
| `DataTransfer` + 真 `File`（页面内构造，走同一 React 处理函数） | 拖放落地：多文件顺序、超大文件、10 MB 边界、HTML 混入 | 事件 `isTrusted=false`；但 `dragover/drop` 的 `preventDefault`、`dataTransfer.files` 与真实拖拽同形，且**字节是真的**（服务端 200） |
| 真剪贴板（`navigator.clipboard.write` + `Control+V`） | 粘贴图片 / 图片+HTML 混合 / 纯文本 / 贴到光标 | 无（这是真粘贴路径） |

---

## 2. 修复声称逐条证伪

### 2.1 声称 1：粘贴 —— **成立**

| 用例 | 实测 |
|---|---|
| A1 真剪贴板截图 + 正文已有"前缀" | `value` 前后都是 `前缀`（**正文未变**）；附件 `image.png` `226 B`，`img.src=blob:http://127.0.0.1:9802/…`；`data-dragging="false"`；`POST /api/blobs?name=image.png` **恰好 1 条** |
| A2 剪贴板同时有 `text/html` 与 `image/png`（模拟"从网页复制的图"） | 剪贴板类型 `[["text/html","image/png"]]`；粘贴后正文仍 `前缀`（**HTML 标记没有进正文**），附件 +1，`/api/blobs` **1 条** |
| A3 纯文本粘贴到光标处 | 正文 `前缀ABCD`，光标 `[2,2]`，`Control+V` 粘贴 `XY` → **`前缀XYABCD`**、光标 `[4,4]`（文本未被吞、位置正确） |
| A4 合成粘贴带 3 个"文件"（png/html/txt） | `defaultPrevented=true`（`handlePaste` 确实 `preventDefault`）；3 个都进附件，正文不变 |
| A5 焦点不在输入框时粘贴 | `activeElement=BODY`，`/api/blobs` **0 条**，无附件 —— 见 §4 O1 |

### 2.2 声称 2：拖放 —— **成立**（覆盖层 + 落地见 §3.3/§3.4 清单项）

| 断言 | 实测 |
|---|---|
| 拖到卡片上出现虚线覆盖层与提示 | `dragover` 后 `data-dragging="true"`，`.zk-composer__drop` 存在，`rect={264,754,1176,146}` **与卡片 rect 完全相同**，文案 `松手即添加附件`，样式 `border: dashed 1px rgb(13,68,246)`、`radius 16px`、`z-index 2`、`pointer-events none`、`display flex` |
| 松手后成为附件 | drop 后 `data-dragging="false"`、覆盖层移除、附件 chip 出现（单文件 / 4 文件 / 12 文件） |
| 拖到别处离开时消失 | 指针移到会话区 `(852,420)` → `data-dragging="false"`、覆盖层消失；移到视口外 `(-5,-5)` 同样清除 |
| 多文件一次拖入按顺序全部附加 | 4 文件一次 drop → chip 顺序 `dropped-shot.png → b-notes.txt → c-data.json → d-readme.md`，`/api/blobs` 4 条**同序**；12 文件一次 drop → `f00…f11` 严格有序、无错误 |
| 拖入子元素不闪断 | `dragover` 打到 `textarea` 与回形针按钮（更深子节点）时 `data-dragging` 保持 `true`；`MutationObserver` 全程只记录 `true@3162 → false@5032（离开卡片）→ true@6168（再进入）`，**子元素穿越期间 0 次 true→false** |

### 2.3 声称 3：落地页三条推荐 —— **成立**

| 断言 | 实测 |
|---|---|
| `/` 空态有三条 | `[data-testid^="now-suggestion-"]` 计数 **3**，`<button type=button>`，`cursor:pointer` |
| 文案与 i18n 一致 | `说说这个仓库的结构，以及我应该先看哪几个文件` / `检查最近的改动有没有明显问题` / `每天早上帮我汇总一次未读事项`，与 `web/src/i18n/zh.ts:55-57` **逐字符相等** |
| 点击填入输入框 | 三条分别点击，`textarea.value` **等于**对应文案（逐字符相等），`path` 仍是 `/` |
| 不自动发送 | 每次点击后 `POST /api/chat/run` 计数 **0**、WS 帧新增 **0**、非 GET 请求 **0** |
| 可直接编辑后发送 | 点击「review」→ 追加 `（已编辑）` → `value` = `检查最近的改动有没有明显问题（已编辑）` → Enter → 跳到 `/c/sess-…`，transcript 第 1 行 = 该编辑后文本，输入框清空 |
| 390 不溢出、可点 | 390×844：`scrollWidth==clientWidth==390`、无 overflow；三个按钮 `insideX/insideY=true`，`elementFromPoint(中心)` 命中按钮自身（`hitsSelf=true`）；点击后填入成功。360/375/414/320/1440 同结论 |
| 深色可读 | `data-theme=dark`、页面 bg `rgb(17,17,24)`、按钮 `color rgb(199,202,217)` / `bg rgb(27,27,38)` → **对比度 10.46:1**；深色下覆盖层文字 `rgb(60,187,250)` 亦清晰（截图 `audit-composer-dark-dragover.png`） |

### 2.4 声称 4：与原回形针共用同一套暂存/移除 —— **成立**

| 断言 | 实测 |
|---|---|
| 同一套暂存 | 三条路径产物结构完全一致：`SPAN.ant-tag.ant-tag-filled.zk-attach…`（图片内 `IMG.zk-attach__thumb`；文本内 `SPAN.zk-attach__text`）；三路径均 `POST /api/blobs?name=<文件名>`；均带关闭按钮 |
| 预览 | 图片三路径都有 `blob:` 预览；文本文件无预览（符合预期） |
| 大小 | 同一 `formatBytes`：`165 B` / `16 B` / `10.0 MB` / `230 B` 一致 |
| 移除释放 URL | 打点 `URL.createObjectURL/revokeObjectURL`：created 3（3 张图）、逐条移除后 revoked 3，**每个 `blob:` 都被 `revokeObjectURL` 精确释放，无泄漏** |

---

## 3. 清单逐条（含 7 的失败证据）

### 3.1 【1】粘贴图片 —— PASS

- 附件出现：chip `image.png` `226 B` + 预览 `blob:http://127.0.0.1:9802/…`
- 正文不变：`前缀` → `前缀`
- `data-dragging` 不残留：粘贴后 `"false"`、无 `.zk-composer__drop`
- 上传请求数：`POST /api/blobs?name=image.png` **1 条**（无重复、无第二个请求）
- 混合剪贴板（`text/html`+`image/png`）再粘一次：正文仍 `前缀`、+1 chip、**1 条** POST
- 截图 `audit-composer-paste-image.png` / `audit-composer-paste-real.png`

### 3.2 【2】纯文本粘贴 —— PASS

`前缀ABCD`（光标 2）→ 粘贴 `XY` → **`前缀XYABCD`**，光标 4；`handlePaste` 在 `files.length===0` 时直接 `return`（`web/src/chat/composer.tsx:311-317`），未 `preventDefault`，浏览器默认插入生效。

### 3.3 【3】拖入覆盖层 —— PASS

| 场景 | `data-dragging` | 覆盖层 | 备注 |
|---|---|---|---|
| `dragenter` 单发 | false | 无 | 组件只挂 `onDragOver`（`composer.tsx:319`），真实拖拽里 `dragover` 紧随其后（通常 <50 ms），不可感知；见 §4 F6 |
| `dragover`（卡片中心） | **true** | 有，rect 与卡片相同 {264,754,1176,146} | 文案 `松手即添加附件`，`dashed 1px rgb(13,68,246)`，`radius 16px`，`z=2`，`pointer-events:none` |
| `dragover`（textarea 子节点） | true | 有 | 未闪断 |
| `dragover`（回形针子节点） | true | 有 | 未闪断 |
| 移到会话区 / 移出视口 | false | 无 | `dragleave` 生效 |
| 非文件拖拽（`types=['text/plain']`，无 `Files`） | false | 无 | `drop` 也 `defaultPrevented=false`，0 次 POST，正文不变 |
| 拖到卡片外再松手 | false | 无 | 不产生附件、URL 未跳转（`navs=[]`） |

`MutationObserver` 全程只有 3 次状态变更（进入 → 离开 → 再进入）。截图 `audit-composer-dragover.png`、`audit-composer-dragleft.png`、`audit-composer-dark-dragover.png`。

### 3.4 【4】拖放落地 —— PASS

| 用例 | 结果 |
|---|---|
| 单图片 drop | 1 chip + 预览 + `POST` 1 条 200 |
| 一次 4 文件（图片/文本/json/md） | 4 chip **按传入顺序** + 4 条 POST **同序**，全部 200 |
| 一次 12 文件 | 12 chip `f00…f11` 有序，两行换行不溢出卡片，无错误 |
| 超大 20 MB + 正常 txt 一起 drop | 正常文件照样附加（`POST ?name=b-notes.txt` 200）；`huge.bin` **没有发请求**（客户端先拦）；页面出现红色 `role=alert` 文案 **`附件超过 10 MB`**（`web/src/i18n/zh.ts:684`）→ 可读错误、非静默丢弃 |
| 边界 10 MB+1 字节 | 拦下，0 次 POST，错误文案同上 |
| 边界 恰好 10 MB（10485760 B） | 接受，`POST` 200，chip `10.0 MB` |
| drop 带 `text/html` + 文件 | 正文 `草稿文字` **未被写入任何标记**，文件正常附加 |
| drop 到子元素（textarea） | 正常附加（`handleDrop` 冒泡到卡片） |
| drop 无文件（只有文本） | `types=['text/plain','text/html']`、`files=0`、`dragging=false`、0 次 POST、正文不变 |

截图 `audit-composer-dropped.png`、`audit-composer-drop-multi.png`、`audit-composer-drop-toolarge.png`、`audit-composer-many-attachments.png`。

### 3.5 【5】推荐 —— PASS

见 §2.3。补充：**英文 UI 同样成立**（`ui.language=en` 落库后）：`Explain this repository and which files I should read first` / `Review the latest changes for obvious problems` / `Summarise my unread items every morning`，390×844 下三个按钮 `inside=true`、`scrollWidth==clientWidth==390`、`overflower=[]`（无横向溢出）。截图 `audit-composer-390-clean.png`、`audit-composer-390-390.png`、`audit-composer-390-en-390.png`、`audit-composer-en-desktop.png`、`audit-composer-dark.png`。

### 3.6 【6】回归 —— PASS

| 项 | 实测 |
|---|---|
| 回形针上传 | `setInputFiles` → `POST /api/blobs?name=clip-shot.png` → chip `clip-shot.png 165 B` + 预览；文本文件无预览 |
| 删除附件 | 逐个点关闭：chip 递减 4→3→2→1→0；`revokeObjectURL` 精确释放对应 `blob:`（created 3 / revoked 3） |
| 发送带附件 | `POST /api/chat/run` 体：`{"sessionId":"sess-…","text":"三条路径的附件一起发","clientRunId":"…","attachments":["f24d2fe1…","d8d1476c…","dadfae70…"]}` —— 三条路径（粘贴/回形针/拖放）的 blob id **全部随请求发出**；松手后消息气泡显示三个文件名 + 正文；**重连后仍在**（`rows[0]` 文本 = `image.png clip-shot.png drop-notes.txt 三条路径的附件一起发`） |
| 审批链 | 待审批 → `approval-allow` → 工具执行 → `完成 The fetch returned ok, so the approval chain works.`；`拒绝` 路径同样关闭审批 |
| Enter / Shift+Enter / Ctrl+Enter | Shift+Enter → `line1\nline2`、`/api/chat/run` **0 次**；Ctrl+Enter → 1 次并跳转会话 |
| Esc 层级 | 有草稿 → 清空；斜杠菜单开 → 关菜单且保留 `/`，再 Esc 才清空；待审批 → Esc **拒绝**（审批消失、`data-disabled` false、回合结束）；运行中 → 发 `{"id":19,"method":"run.cancel","params":{"sessionId":"sess-…"}}`，回合以 `cancelled 详情` 结束、输入框回 idle、**随后再发一条仍成功** |
| 草稿持久化 | `localStorage['zk.draft.<sessionId>']="会话里的草稿"`；reload 后 value 恢复；跳到 `/` 再回来仍恢复 |

截图 `audit-composer-attach-parity.png`、`audit-composer-sent-three.png`、`audit-composer-persisted-attachments.png`、`audit-composer-esc-deny.png`、`audit-composer-esc-stop.png`、`audit-composer-draft.png`。

### 3.7 【7】边界 —— **FAIL**

**(a) 禁用（待审批）时粘贴/拖放没有被忽略 —— FAIL**

复现（`c2-t7-disabled.mjs`，会话 `sess-1795548386901964223`，等 `data-disabled="true"` 且 notice 出现后再操作）：

| 操作 | 期望 | 实测 |
|---|---|---|
| 前置状态 | 禁用 | `data-disabled="true"`、`textarea.disabled=true`、notice `本会话有一条审批等待处理：处理后才能继续发送。` |
| 输入文字 | 不进入 | **不进入**（`value=""`，正确） |
| 粘贴图片文件 | 忽略 | **被接受**：`chip=while-disabled-paste.png 8 B`、`POST /api/blobs?name=while-disabled-paste.png`、`chips 0→1` |
| 拖放文本文件 | 忽略 | **被接受**：`chip=while-disabled-drop.txt 19 B`、`POST /api/blobs?name=while-disabled-drop.txt`、`chips 1→2` |
| 解除审批后 | —— | `data-disabled="false"`，两个"禁用期间"的附件**仍留在输入框里**，一次发送即可带出 |

原因：`handlePaste` / `handleDrop` 只判 `onAttachFiles` 与 `files.length`，**不判 `disabled`**（`web/src/chat/composer.tsx:311-338`），而 `disabled` 只被写到卡片的 `data-disabled`（`composer.tsx:399`）并透传给 Sender（`:499`、`:538`）。截图 `audit-composer-disabled.png`、`audit-composer-disabled-after.png`。

**(b) 非文件（纯文本）拖拽不触发覆盖层 —— PASS**：`types=['text/plain']` → `data-dragging=false`、无覆盖层、`dragover.defaultPrevented=false`、`drop` 后 0 次 POST、正文不变（`c2-t3d` B1、`c2-t4b`）。

### 3.8 补充边界（清单外，主动加压；全部通过）

| 场景 | 实测 |
|---|---|
| 160 字符长文件名 | chip 宽度被压到 **320 px**、`ant-typography-ellipsis` 生效，不撑破卡片；`/api/blobs?name=xxxx…` 完整编码 |
| 文件名带空格 + 中文（`my report 报告 v2.md`） | 正常上传、chip 正常显示、URL 百分号编码正确 |
| 同一手势里重复同一文件 2 次 | 2 个 chip、2 次上传（不去重，符合"用户拖两次就两份"的直白语义） |
| 无扩展名 + 空 MIME（`noext`，`type:''`） | 正常附加（chip `noext 1 B`），无报错 |
| 斜杠菜单打开时粘贴文件 | 附件照常附加（1 条 POST），菜单仍开着、正文 `"/"` 不被污染 |
| 运行中拖入附件并试图发送 | 拦截且**给出可读原因**：`本轮运行中只能排队文本：请等本轮结束后再发送附件`；`/api/chat/run` **0 次**、草稿**保留**（`回合二号带附件`）、附件保留（`web/src/features/thread/thread-screen.tsx:287`） |
| 卸掉附件后运行中再发纯文本 | 走排队：`/api/chat/run` 0 次、草稿清空、界面出现 `1 条待发送` |

截图 `audit-composer-longnames.png`、`audit-composer-running-send-blocked.png`。

---

## 4. 失败项与缺陷清单

### F1【中低，清单 7 直接失败】禁用态下粘贴/拖放仍上传并暂存附件

- **现象**：审批待处理、输入框已禁用（`data-disabled=true`、`textarea.disabled=true`、notice 明说"处理后才能继续发送"）时，粘贴图片文件或拖入文件仍会 `POST /api/blobs` 并把 chip 塞进禁用中的输入框；解除审批后这些附件还在，可能被下一次发送误带出去。
- **复现**：`c2-t7-disabled.mjs`（`SESS=<待审批会话>`）：等 `[data-testid="composer"]` 的 `data-disabled=true` → 对 `textarea` 派发带 `File` 的 `paste` → chips 0→1 且网络出现 `POST /api/blobs`；对卡片派发 `dragover+drop` → chips 1→2。
- **期望**：禁用时两条附加路径都应静默忽略（与键盘输入一致），或明确提示"处理完审批后可添加附件"。
- **建议修法**：在 `web/src/chat/composer.tsx:311`（`handlePaste`）与 `:332`（`handleDrop`）开头各加 `if (disabled) return;` —— 只改 `composer.tsx:363` 的 `attach` 分支没用，它只决定回形针按钮是否渲染，拦不住 drop/paste；`web/src/features/thread/thread-screen.tsx:481` 也可顺手写 `disabled ? undefined : (files) => void attachFiles(files)` 做双保险。

### F2【低】附件错误提示常驻不消失（成功后仍显示"附件超过 10 MB"）

- **现象**：一次超大文件后，红色 `role=alert` 文案 `附件超过 10 MB` 一直留在输入框上方；后续正常粘贴/拖放成功也不清除，直到发送或刷新。
- **复现**：`c2-t4-drop.mjs` D3（20 MB 被拒）→ D5（恰好 10 MB 成功、HTTP 200）→ 报错仍在；`c2-t12b-bigpaste.mjs`：11.24 MB 粘贴被拒 → 随后 `blob.bin` 成功附加，`errors` 仍为 `["附件超过 10 MB"]`。
- **期望**：任一附件成功后清空上一次的附件错误。
- **建议修法**：`web/src/features/now/now-screen.tsx:113-125` 的成功分支加 `setError(null)`；`web/src/features/thread/thread-screen.tsx:132-143` 的成功分支加 `setAttachError(null)`（现在只有队列路径 `:296` 会清）。

### F3【低】点击推荐会覆盖已有草稿，且焦点不进输入框

- **现象**：输入框已有一半草稿时点推荐，草稿被**静默替换**（无确认、无"还原"）；点击后 `activeElement` 仍是按钮，直接敲键盘没有任何字进入输入框（须再点一次输入框）。
- **复现**：`c2-t15-overwrite-dock.mjs`：`fill('我自己写了一半的草稿')` → 点 `now-suggestion-review` → `value='检查最近的改动有没有明显问题'`、`focus='now-suggestion-review'`；`c2-t5c-clean.mjs`：点击后 `keyboard.type('ZZ')`，`value` 不变。
- **期望**：空输入时才整段填入；已有草稿时不覆盖（或提供撤销）；点击后把焦点交给输入框并把光标放到末尾。
- **建议修法**：`web/src/features/now/now-screen.tsx:77` `onClick={() => setDraft(...)}` 改为"仅当 `draft.trim()===''` 时填入"，并调用 composer 的 focus（`composer.tsx` 已暴露 `senderRef.current?.focus?.()`，可提一个 `focusComposer()` 或通过 ref 转发）。

### F4【低】320×640 + 有待审批卡片时，第三条推荐被审批卡遮住且不可点

- **现象**：窄/矮屏上空态欢迎区与 `ApprovalDock` 抢空间：`320×640` 下第三条推荐 `每天早上帮我汇总一次未读事项` 的命中元素是 `SECTION.zk-approval`，`elementFromPoint` 判 `hitsSelf=false`；滚到底也没用（`scrollTop=scrollHeight` 后仍被遮），Playwright 点击 4 s 超时。375×667 / 390×844 无此问题。
- **复现**：`c2-t15-overwrite-dock.mjs`（先在 9802 造一条待审批，再用 320×640 打开 `/`）、`c2-t15b-scrollhit.mjs`。
- **期望**：矮屏下推荐行与审批卡不重叠（推荐区预留 dock 高度，或 dock 收起为一行摘要）。
- **建议修法**：`web/src/design/global.css:693`（`.zk-now__welcome` 只给了 140px 底部预留）——把预留改成"composer + dock 实际高度"（用 CSS 变量或 `flex` 布局让 dock 参与占位，而不是浮在欢迎区上）；或对 `max-height:700px` 用更紧凑的推荐行（单行横向滚动）。

### F5【低，未能在浏览器证实】没有全局 drop 护栏，文件拖到输入框以外可能被浏览器默认打开

- **现象（代码级）**：全仓 `dragover/drop` 监听只在 `web/src/chat/composer.tsx:405-408`，没有任何 `window` 级 `preventDefault` 兜底；真实 OS 把文件拖到会话区/侧边栏后松手，Chromium 的默认动作是**导航到 `file://`**（等于把 SPA 打开成本地文件）。
- **说明**：本沙箱 CDP 拖放不触发浏览器默认动作（命中测试里 `navs=[]`、URL 未变），所以**我没能观测到实际跳转**，按"需补验收点"处理，不敢定为 FAIL。
- **建议修法**：在应用壳（`web/src/shell/app-shell.tsx` 或 `main.tsx`）挂一对 `window.addEventListener('dragover'|'drop', e => { if (e.dataTransfer?.types.includes('Files')) e.preventDefault(); })`，把"卡片外松手"变成"什么都不发生"。

### F6【信息】覆盖层只在 `dragover` 出现；`dragCancel` 不清除

- `dragenter` 单发不显示覆盖层（只挂 `onDragOver`，`composer.tsx:319`）。真实拖拽 `dragover` 紧跟 `dragenter`，不可感知；仅当拖动源在卡片上**静止不动**时才可能看到 1 帧空窗。
- CDP `dragCancel` 后覆盖层仍在（`c2-t16-cancel.mjs`：`C1_in=true → C2_after_dragCancel=true → C5_left=false`）。真实 Chromium 取消拖拽会给当前目标发 `dragleave`（我已用"移出卡片/移出视口"覆盖该路径并清除成功），故判为**兜底缺失**而非必现缺陷；建议同时听 `window` 的 `dragleave`/`dragend` 保险。

### O1【观察】焦点不在输入框时粘贴图片会被丢弃

`activeElement=BODY` 时 `Control+V`：`/api/blobs` 0 条、无附件（粘贴事件落在 `body`，卡片的 `onPaste` 不参与）。若产品希望"在会话页任意位置 Ctrl+V 都能贴图"，需要把 paste 监听到文档级；否则至少给一次 toast 提示。

---

## 5. 需补验收点（本环境无法闭环）

| # | 缺口 | 建议怎么补 |
|---|---|---|
| 1 | **真实 OS 文件拖放的字节**：CDP `Input.dispatchDragEvent` 在本沙箱只产出 0 字节 `File` | 用真桌面（或 `xdotool`/GTK 拖放源 + 非 headless Chromium）从文件管理器拖拽；或换用能注入真实 `DataTransfer` 字节的驱动 |
| 2 | **卡片外松手是否导航**（F5） | 同上，用真 OS 拖放观测 URL 变化 |
| 3 | 附件在上传中的过渡态（大文件慢网、取消/重试） | 用带延迟的上传代理，观测 chip 是否显示"上传中"、能否取消 |
| 4 | 多标签页 / 断线重连期间的暂存附件 | `attachments` 只在内存，重连（epoch 变化）后是否丢失、是否需要提示 |
| 5 | 禁用态/运行中附件策略的产品决策（F1、§3.7）| 明确"能否在不能发送时先暂存"，再决定改代码还是改文档 |
| 6 | 320 宽 + 审批卡的布局（F4）、推荐行在 280/短横屏 | 补 320×480、375×500、横屏（844×390）三档 |
| 7 | 输入框 attach 的 `accept="image/*,text/*,application/pdf,application/json"` 与粘贴/拖放不限类型的不一致 | 明确是否要统一（现在 `.bin` 只能走粘贴/拖放，选不到） |

---

## 6. 复现脚本与原始日志（都在 `.cache/ui-audit/`）

| 脚本 | 覆盖 | 日志 |
|---|---|---|
| `c2-clip-test.mjs` / `c2-t1-paste.mjs` | 真剪贴板粘贴、混合剪贴板、光标处文本粘贴、合成多文件、失焦粘贴 | `c2-t1.log` |
| `c2-t3c-overlay.mjs` / `c2-t3d-boundary.mjs` / `c2-t16-cancel.mjs` | 覆盖层生命周期、子元素不闪断、离开清除、非文件拖拽、dragCancel | `c2-t3c.log`、`c2-t3d.log` |
| `c2-t4-drop.mjs` / `c2-t4b-nofiles.mjs` | 单/多/12 文件 drop、超大与 10 MB 边界、HTML 混入、无文件 drop | `c2-t4-drop.log` |
| `c2-t5-suggest.mjs` / `c2-t5b-mobile.mjs` / `c2-t5c-clean.mjs` / `c2-t14-i18n-dark.mjs` / `c2-t14b-en.mjs` | 推荐点击/不发送/编辑发送、390/375/360/320/414、深色、英文、遮挡 | `c2-t5.log`、`c2-t5b.log` |
| `c2-t6-thread.mjs` / `c2-t7-disabled.mjs` | 线程页、禁用态粘贴/拖放（F1） | `c2-t6.log`、`c2-t7.log` |
| `c2-t8-attach.mjs` / `c2-t9-send.mjs` / `c2-t10b-persist.mjs` | 三路径暂存一致性、revokeObjectURL、run 请求体、重连后持久化 | `c2-t8.log`、`c2-t9.log` |
| `c2-t11-keys.mjs` / `c2-t11b-draft.mjs` | Esc 三层、Shift/Ctrl+Enter、草稿持久化 | `c2-t11.log` |
| `c2-t12-more.mjs` / `c2-t12b-bigpaste.mjs` | 连续粘贴、12 文件、11.24 MB 粘贴、二进制文件粘贴 | `c2-t12.log` |
| `c2-t13b-stop.mjs` | 慢速模型下 Esc 停止（`run.cancel`） | — |
| `c2-t15-overwrite-dock.mjs` / `c2-t15b-scrollhit.mjs` | 草稿被覆盖（F3）、审批卡遮挡（F4） | — |
| `c2-t17-edge.mjs` / `c2-t18b-running2.mjs` | 长名/重复/空 MIME/菜单开着粘贴、运行中带附件发送被拦 | — |

## 7. 截图清单

`audit-composer-` 开头，共 46 张，全部在 `.cache/ui-audit/shots/`：

- 粘贴：`paste-image` / `paste-real` / `paste-toolarge`
- 拖放：`dragover` / `dragleft` / `dropped` / `drop-multi` / `drop-toolarge` / `many-attachments` / `dark-dragover`
- 附件与发送：`attach-parity` / `sent-attachment` / `sent-three` / `turns-done` / `attachment-persisted` / `persisted-attachments` / `reload-persisted` / `running-attach`
- 其它边界：`longnames` / `running-send-blocked`
- 推荐：`landing` / `suggestion-filled` / `suggestion-sent` / `390-clean` / `320-clean` / `390-390` / `390-360` / `390-320` / `390-desktop` / `390-en` / `390-en-390` / `390-en-1440` / `en-desktop` / `dark`
- 审批/禁用/边界：`thread-pending` / `thread-approved` / `disabled` / `disabled-after` / `esc-deny` / `esc-stop` / `slow-running` / `draft` / `dock-320x640` / `dock-320-click` / `dock-375x667` / `dock-390x844`
---

# 第二轮复验（构建 `2b6c8b1b`，提交 `4264b8a`，2026-11-25 03:45 +0800）

> 复验对象：`4264b8a fix(web): a blocked composer stages nothing, and the landing keeps your draft`（F1–F6 六项修复）
> 独立实例：`:9808`（`data-c2b`，`chat.model=acceptance-tool`）+ `:9810`（`data-c2bslow`，`chat.model=acceptance-slow`），`fakemodel` `:9809/:9811`
> 截图：`.cache/ui-audit/shots/audit-composer-r2-*.png`（18 张）；日志 `c2r2-*.log`；脚本 `c2r2-*.mjs`

## 8.0 结论（第二轮）

**6 项修复中 4 项 PASS、2 项未完全闭环**：F1 / F3 / F5 / F6 PASS；**F2 只修了落地页，线程页仍残留**（与提交信息"thread and landing"不符）；**F4 在 320×480 仍不可达**（提交信息与实现者自测只覆盖到 320×640）。回归 11 项全 PASS（含我第一轮的两处"失败"其实是量测方式问题，已逐条澄清）。

| # | 项 | 结果 | 一句话证据 |
|---|---|---|---|
| F1 | 禁用态不暂存附件；运行中不误伤 | **PASS** | 禁用态 paste/drop：chips 0、`/api/blobs` 0、`data-dragging` 恒 false；解除审批后 chips 仍 0；重新启用后 paste 立刻 1 条 POST + chip；慢速运行中（`data-disabled=false`）paste/drop 各 1 条 POST + chip |
| F2 | 错误提示在成功后被清除 | **落地页 PASS / 线程页 FAIL** | 落地页：20 MB 被拒 → `附件超过 10 MB`；随后 8 B 文本成功 → `errors=[]`。线程页：同样序列成功后 `errors=["other","附件超过 10 MB"]`（`web/src/features/thread/thread-screen.tsx:132-143` 未加清除） |
| F3 | 空=填入、非空=追加、焦点进输入框、连点两次都追加 | **PASS** | 空：value==文案、sel[14,14]；非空：`我自己的草稿\n说说…`、sel[29,29]；连点：两行都追加、sel[59,59]；点击后直接敲 `X` 落在光标处（sel[60,60]）；`POST /api/chat/run` 0 |
| F4 | 320×640 / 320×480 / 375×500 + 审批卡时三条推荐可达；1440 不变 | **部分 FAIL** | 320×640：静止时第三条 `hitsSelf=false`，但滚动 welcome 后 s=25‥110 可达、三次真实点击全 ok；375×500：可达（s=55‥100）；360×560/390/1440：静止即 `hitsSelf=true`；**320×480：s=0‥130 全扫无一可行位置，三次点击全 `TimeoutError`** |
| F5 | 卡片外丢文件不导航、app 可用、随后丢进卡片仍能附加 | **PASS** | 真 CDP 拖到会话区松手：`dragover/drop` 均 `defaultPrevented=true`、`navs=[]`、URL 不变、三条推荐仍在；非文件拖拽在 body 上 `prevented=false`（守卫不越界）；随后丢进卡片 → chip + 1 条 POST；再真拖入卡片 → 覆盖层照常出现 |
| F6 | 单发 dragenter 显示覆盖层；dragleave 到外清除；子元素不闪断 | **PASS** | 单发 `dragenter`（合成 + 真 CDP 各一次）：`data-dragging=true`、覆盖层 rect == 卡片 rect、文案 `松手即添加附件`、`dashed 1px rgb(13,68,246)`；`dragleave`→外部清除；子节点（textarea/回形针）穿越保持 true，`MutationObserver` 只有进入/离开两次翻转 |
| 回归 | 粘贴图片/文本、多文件顺序、20 MB 拦截、推荐不自动发送、回形针、删除释放 URL、带附件发送、Esc 四层、草稿持久化 | **PASS** | 见 §8.3 |

## 8.1 版本自证（第二轮）

```
GET / = 200 | GET /favicon.svg = 200 | GET /api/capabilities(无授权) = 401
gateway.json: bundleSha256=2b6c8b1bd53c7ee892ecd79bacba77ce1daebd8e25772d3564510a3ddceccb4a, bundleBuiltAt=2026-11-24T19:44:25Z
```

用 **HTTP 取到的字节**复算目录指纹（`dist/` 除 `BUNDLE_INFO` 外 6 个文件，`sort -z`→`sha256sum`→`sha256sum`）得到
**`2b6c8b1bd53c7ee892ecd79bacba77ce1daebd8e25772d3564510a3ddceccb4a`**，与 `gateway.json` 逐字符相同；5 个服务端文件与 `internal/gateway/webclient/dist/` 逐文件 `same`。
产物已按内容改名：`assets/index-kLMi84LF.js`（2 454 496 B）、`assets/index-DRPloYwv.css`（47 334 B）、`index-kLMi84LF.js.map`；bundle 内命中新代码标记 `focusRequest`。

## 8.2 逐条证伪

### F1 —— PASS（三面都验了：禁用不暂存 / 启用正常 / 运行中不误伤）

| 场景 | 实测 |
|---|---|
| 前置：审批待处理 | `data-disabled="true"`、`textarea.disabled=true` |
| 禁用 + 粘贴带文件 | `chips=[]`、`POST /api/blobs` **0 条**、`paste.defaultPrevented=false`（handler 直接 return） |
| 禁用 + dragenter/dragover | `data-dragging="false"`、无 `.zk-composer__drop`（`handleDragOver` 也提前返回） |
| 禁用 + drop | `chips=[]`、0 条上传、`drop.defaultPrevented=true`（**window 守卫**兜住了浏览器默认行为，所以禁用态拖放也不会导航） |
| 解除审批后 | `chips=[]`、0 条上传（第一轮的"事后冒出来"不再出现） |
| 重新启用 + 粘贴 | 立刻 `POST /api/blobs?name=r2-enabled-paste.png`、chip `…9 B`（没有过度拦截） |
| **运行中**（`:9810` slow，`data-disabled=false`） | 粘贴 → 1 条 POST + chip `r2-running-paste.txt 13 B`；拖放 → 1 条 POST + chip `r2-running-drop.txt 12 B`；Esc 停止后 chips 仍在（符合"排队等下一条"的既有设计） |

截图 `audit-composer-r2-disabled.png`、`-disabled-after.png`、`-running-attach.png`。

### F2 —— 落地页 PASS，**线程页 FAIL**

| 页面 | 序列 | 实测 |
|---|---|---|
| 落地页 `/` | drop 20 MB → 再 drop 小文件 | 第一次：`errors=["附件超过 10 MB"]`、chips 0、0 条 POST；第二次：chip `small-ok.txt 8 B`、1 条 POST 200、**`errors=[]`** ✅ |
| 线程页 `/c/sess-…` | 同上 | 第一次：`errors=[…,"附件超过 10 MB"]`、0 条 POST；第二次：chip `thread-small.txt 2 B`、1 条 POST 200、**`errors` 里 `附件超过 10 MB` 仍在** ❌ |

原因（代码级）：`4264b8a` 只改了 `web/src/features/now/now-screen.tsx:125`（成功分支 `setError(null)`），**`web/src/features/thread/thread-screen.tsx:132-143` 的 `attachFiles` 成功分支没有 `setAttachError(null)`**（全文件仅 `:287`、`:296` 会写/清它）。提交信息里 "thread and landing" 与实际改动不符。
建议修法：在 `thread-screen.tsx:136`（`setAttachments(...)` 之前）加 `setAttachError(null);`。

（注：线程页 `errors` 里的 `"other"` 来自 `.zk-tool-card` 内一个 `role=alert` 的工具错误节点，是我的用例"拒绝审批"产生的，与附件无关。）

### F3 —— PASS

| 场景 | 实测 |
|---|---|
| 空草稿点「review」 | `value="检查最近的改动有没有明显问题"`（== 文案）、`sel=[14,14]`、`focus=composer-input`（textarea） |
| 非空草稿 `我自己的草稿` 点「explain」 | `value="我自己的草稿\n说说这个仓库的结构，以及我应该先看哪几个文件"`、`sel=[29,29]`、焦点在文本框（**未覆盖**） |
| 连点「automate」再点「review」 | 两条都追加在后（4 行），`sel=[59,59]` |
| 点击后直接敲键盘 | `…问题X`，`sel=[60,60]` —— 焦点是真在输入框里，不是仅 `activeElement` 假象 |
| 点击是否发送 | `POST /api/chat/run` **0** |

截图 `audit-composer-r2-suggest-append.png`。

### F4 —— 320×640 / 375×500 可达，**320×480 不可达**

`.zk-now__welcome` 现在 `overflow-y:auto; min-height:0`（`global.css:698-703`），滚动即可换位置。逐视口扫 `scrollTop`（步长 5–10 px）记录"是否有某个滚动位置能让该按钮的命中元素是自己"：

| 视口 | 静止时 hitsSelf | 可达的 scrollTop（explain / review / automate） | 三次真实点击 |
|---|---|---|---|
| 320×640 | true / true / **false**（`SPAN.zk-approval__id`） | 0–10 / 0–65 / **25–110** | ok ok ok（草稿 3 行） |
| 375×500 | true / **false** / **false** | 0 / 10–55 / **55–100** | ok ok ok |
| 360×560 | true / true / true | 0–10 / 0–65 / 5–110 | — |
| 390×844 | true / true / true | 无需滚动（welcome 不可滚） | ok ok ok |
| 1440×900 | true / true / true | 无需滚动 | ok ok ok |
| **320×480** | **false / false / false** | **空 / 空 / 空**（s=0‥130 全扫） | **三个都 TimeoutError(4s)** |

320×480 的几何（`c2r2-f4-diag.mjs`）：topband `y=0 h=49`；`.zk-now__welcome` `y=65 h=140`；`section.zk-approval` **`y=65 h=284`**（与欢迎列同起点、整段盖住）；composer `y=361 h=188`（`bottom=549`，**已超出 480 视口**）。也就是说：矮到 480 时审批卡把欢迎列完全吃掉，滚动列再滚也没用 —— "让欢迎列可滚动"只解决了 320×640 这一档。

建议修法（二选一或都做）：① 该断点下把 `ApprovalDock` 折叠成一行摘要（点击展开），给欢迎列让出 ≥120 px；② 让推荐行 **吸底**（`position: sticky; bottom: 0`）或改成单行横向滚动（`overflow-x:auto`），与审批卡不争同一块空间；③ 顺手处理 `320×480` 下输入框被裁 69 px 的问题（composer `bottom=549 > 480`）。

截图 `audit-composer-r2-dock-{320x640,320x480,375x500,390x844,1440x900}.png`、`-320x480-sweep.png`。

### F5 —— PASS

| 检查 | 实测 |
|---|---|
| 合成 drop 在会话区（卡片外） | `dragover.defaultPrevented=true`、`drop.defaultPrevented=true`（window 守卫）、chips 0、`/api/blobs` 0、URL 不变 |
| 守卫是否越界（非文件拖拽） | body 上 `dragover` 的 `types=['text/plain']`、`prevented=false`（没有把普通文本拖拽也吃掉） |
| 守卫是否误伤卡片内 drop | 同一页随后丢进卡片：`drop.defaultPrevented=true`、chip `inside-drop.txt 6 B`、1 条 POST |
| **真 CDP 拖拽**在卡片外松手 | `dragover/drop` 在 `window` 冒泡阶段读到 `defaultPrevented=true`、`navs=[]`、`location` 不变、页面仍可用（3 条推荐在、textarea 在） |
| 之后真拖入卡片 | `data-dragging=true`、覆盖层出现 |

注：浏览器"文件拖到页面→打开 file://"这件事只在 `drop` 的默认动作未被取消时发生，所以 `drop.defaultPrevented=true` 就是"不会导航"的直接证据；我没有依赖 CDP 是否真的执行导航（本沙箱不执行）。

截图 `audit-composer-r2-f5-guard.png`。

### F6 —— PASS

| 检查 | 实测 |
|---|---|
| 单发 `dragenter`（合成） | `defaultPrevented=true`、`data-dragging=true`、覆盖层 rect `{264,754,1176,146}` == 卡片、文案 `松手即添加附件`、`dashed 1px rgb(13,68,246)` |
| 单发 `dragenter`（**真 CDP，只发 dragEnter 不发 dragover**） | 同上 —— 修复前这里是 false/无覆盖层 |
| `dragleave` 到卡片外 | `false` + 覆盖层移除 |
| 拖入子元素（textarea / 回形针） | 保持 `true`；`MutationObserver` 全程只有 `true→false→true`（分别来自 A 进入、B 离开、C 再进入），**子元素穿越期间 0 次翻转** |
| 线程页真拖拽 enter→孩子→回卡片→离开 | `flicker=["true","false"]`（一次进一次出） |
| `dragCancel` | 仍不清除（与第一轮同，属无 `dragend`/window-`dragleave` 兜底；真实 Esc 取消会给 `dragleave`，未能在此沙箱证伪）；**卡片外 `drop` 现在会清除**（守卫 `if (type==='drop') setDragging(false)`，实测 K4：true→false） |

截图 `audit-composer-r2-dragenter.png`、`-dragenter-child.png`、`-thread-drag.png`。

## 8.3 回归（第二轮实测，全 PASS）

| 项 | 实测 |
|---|---|
| 真剪贴板贴图 | 正文 `前缀` 不变、chip `image.png 151 B`、`POST /api/blobs` 1 条、`data-dragging=false` |
| 纯文本粘贴到光标 | `ABCD`（光标 2）→ `ABXYCD` |
| 一次 4 文件 drop 顺序 | chips 与 POST 均为 `r2-a.txt → r2-b.json → r2-c.md → r2-d.csv` |
| 20 MB 拦截 | 0 条 POST、红字 `附件超过 10 MB` |
| 推荐不自动发送 | 点击后 `POST /api/chat/run` 0 |
| 回形针 | `setInputFiles(favicon.svg)` → chip `favicon.svg 374 B` |
| 删除释放 URL | `createObjectURL` 1 次 / 删除后 `revokeObjectURL` 同一条（精确配对） |
| 带附件发送 | `POST /api/chat/run` 体含 `attachments:["a37532ea…","44d89d06…","90b9d1be…","a1a33cd8…"]`（4 个 blob id 全带上） |
| 审批链 / Esc 拒绝 | 本会话审批：`approvals 1 → 0`、`data-disabled true → false`、回合以"已完成"收尾 |
| Esc 四层 | 有焦点时 Esc 清草稿（`要清掉的草稿` → ``）；斜杠菜单 Esc 关菜单保留 `/`（6 个候选）；运行中 Esc → `{"id":20,"method":"run.cancel","params":{"sessionId":"sess-…"}}`，回合 `cancelled`，随后再发一条成功 |
| 草稿持久化 | `zk.draft.<sessionId>="R2 草稿持久化"`，reload 后 `value` 恢复 |

## 8.4 本轮残留 / 需注意

| 编号 | 级别 | 内容 |
|---|---|---|
| R2-1 | **中**（F2 未闭环） | 线程页附件错误不清除：`web/src/features/thread/thread-screen.tsx:132-143` 缺 `setAttachError(null)`；提交信息声称已修但只有 now-screen 改了 |
| R2-2 | **中**（F4 未闭环） | `320×480` 下审批卡（`y=65 h=284`）完全覆盖欢迎列（`y=65 h=140`），滚动无法救；三次点击全超时。同时 `320×480` 下 composer `bottom=549 > vh=480` 被裁 69 px |
| R2-3 | 低（信息） | `dragCancel` 仍不清覆盖层；建议补 `window` 的 `dragend`/`dragleave` 兜底 |
| R2-5 | 低（观感） | 推荐追加在「草稿已以换行结尾」时会多出一个空行（`第一行\n` + 推荐 → `第一行\n\n检查…`）；纯空白草稿则按「空」处理直接填入。建议追加前判断 `current.endsWith('\n')`，已换行就不再补 |
| R2-4 | 环境 | 我第一次用 `startB.sh comp2` 时，`:9806/:9807` 上已有另一个 `comp2` 实例（`data-comp2/gateway.json` pid/epoch 均不同），启动被拒并**顺带改写了 `data-comp2/settings.json` 的 `chat.baseURL` 为 9809**；我已立即改回 `http://127.0.0.1:9807/v1`，`:9806` 全程 200 可访问。建议并行复验时各自用不同 `<name>`（我最终用 `c2b`/`c2bslow`） |

## 8.5 第二轮脚本与截图

| 脚本 | 覆盖 |
|---|---|
| `c2r2-f1-disabled.mjs` | F1：禁用态 paste/drop/解除后/再启用（`:9808`） |
| `c2r2-f1-running.mjs` | F1：慢速运行中 paste+drop（`:9810`） |
| `c2r2-f2f3.mjs` | F3 全部 + F2 落地页 |
| `c2r2-f2thread.mjs` | F2 线程页（暴露未修） |
| `c2r2-f4-dock.mjs` / `c2r2-f4-diag.mjs` / `c2r2-f4-sweep.mjs` | F4 五档视口 + 320×480 几何与滚动全扫 |
| `c2r2-f5-guard.mjs` | F5 合成 + 真 CDP 卡片外 drop、非文件拖拽、卡片内 drop |
| `c2r2-f6-dragenter.mjs` | F6 单发 dragenter（合成 + 真 CDP）、leave、子元素、MutationObserver |
| `c2r2-cancel-dropout.mjs` | dragCancel 与"卡片外 drop 清覆盖层" |
| `c2r2-regress.mjs` / `c2r2-esc-deny.mjs` / `c2r2-esc-diag.mjs` / `c2r2-stop.mjs` / `c2r2-thread-drag.mjs` | 回归 11 项 + Esc 四层 + 线程页拖拽 |

截图 18 张：`audit-composer-r2-{disabled,disabled-after,running-attach,suggest-append,f2-cleared,f2-thread,dock-320x640,dock-320x480,dock-320x480-sweep,dock-375x500,dock-390x844,dock-1440x900,f5-guard,dragenter,dragenter-child,thread-drag,esc,regress}.png`。

---

# 第三轮复验（提交 `1d79fac` + `28077ce`；构建 `1ca0a432`（comp3）/ `dbbc397e`（queue））

> 复验对象：① `1d79fac fix(web): clear the attach error in the thread too; no unreachable suggestions`（R2-1/R2-2/R2-3/R2-5）；② `28077ce feat(web): cancelling a queued prompt gives the text back`（队列回滚，新能力）
> 实例：`:9812`（`data-c2c`，`zeokeel-comp3`，`chat.model=acceptance-tool`）+ `:9816`（`data-q3`，`zeokeel-queue`，`chat.model=acceptance-slow`，用 `startC.sh` 第 5 参数指定）
> 截图：`.cache/ui-audit/shots/audit-composer-r3-*.png`（27 张）；脚本 `c2r3-*.mjs`

## 9.0 结论（第三轮）

**R2-1 / R2-5 / 队列回滚 全部 PASS；R2-3 机制 PASS（真 Esc 取消在本沙箱无法产生任何事件，见 §9.4）；R2-2 核心 PASS（不再有"可见但不可点"的建议），但"composer 首帧完整在视口内"这条在 320×480 + 审批卡时仍不成立（差 69 px，滚动一次即可，发送按钮被裁 6 px）。快查回归全 PASS。**

| # | 项 | 结果 | 一句话证据 |
|---|---|---|---|
| R2-1 | 会话页 20 MB 拒 → 8 B 成功 → 错误消失（反向也要重现） | **PASS** | 会话页：`["附件超过 10 MB"]` → `[]` → 再 20 MB → `["附件超过 10 MB"]` → 再小文件 → `[]`；落地页同序同结果；最新构建（`dbbc397e`）复测同结果 |
| R2-2 | 320×480 / 320×640 / 375×500 + 审批卡：无"可见但不可点"元素、composer 完整在视口、无横向滚动；1440/390 不受影响 | **部分 PASS** | 建议：320×480/375×500/1440×520 下 `display:none`（无不可点元素）✓；320×640 静止时第三条被审批卡压住但滚动 s=10‥110 可达且三次点击 ok ✓；390/1440/1024×600 三条 `hitsSelf=true` ✓；无横向滚动 ✓；**但 320×480 首帧 composer `y=361..549`（越界 69 px），`send` 按钮 `452..486` 被裁 6 px，`hint` 完全在屏外；thread 可滚 500/431，滚到底后 `y=292..480` 完整（与实现者数字一致，说明对方量的是滚动后状态）** |
| R2-3 | 真 CDP 拖拽中途取消 → 覆盖层消失；拖出窗口同样；再拖入仍显示 | **机制 PASS / 真 Esc 无法观测** | CDP `dragCancel` 在页面里**不派发任何事件**（`window.__ev` 只有 dragenter/dragover）；新增的两个兜底经合成验证：`window.dragend` → `data-dragging=false`、`window.dragleave(relatedTarget=null)` → false；**真拖出窗口（`(-10,-10)`）→ `dragleave(relatedTarget=null)` 到达 → false**，再拖入 → true |
| R2-5 | 草稿以换行结尾 → 点推荐不出现空行 | **PASS** | `"第一行\n"` → `"第一行\n检查最近的改动有没有明显问题"`；`"第一行  \n \n"` → 同上（尾部空白被裁）；`"a\nb"` → `"a\nb\n检查…"`（内部换行保留）；纯空白/空 → 直接填入；焦点始终 `composer-input` |
| 队列 | 取消队列项 → 文本回到输入框、焦点、不发送、不丢字；刷新后队列空 + 草稿在 | **PASS** | 见 §9.5：空/非空/尾换行草稿、非队首、双击、空队列、撤回后立刻发送、刷新持久化全部实测 |
| 重排 | 队列排序 | **未实现（需协议变更，非缺陷）** | 契约里只有 `prompts.snapshot/enqueue/remove/subscribe/unsubscribe`（`docs/TRANSPORT.md:561-565`、`internal/gateway/contract.json`），**无 `prompts.move`**；队列行内只有「取消」一个按钮 |
| 快查回归 | F1/F3/F5/F6、贴图/拖放/回形针/带附件发送 | **PASS** | 见 §9.6 |

## 9.1 版本自证（两个构建都用 HTTP 字节复算）

| 实例 | 二进制 | `bundleSha256`（`gateway.json`） | 用 HTTP 字节复算 | 产物 |
|---|---|---|---|---|
| `:9812` | `zeokeel-comp3`（44 574 416 B，03:57） | `1ca0a432d9eaab18e1aeb6604b5d7e3b2881969df9f64d1e013aa5c1dcc4fbbf` | **一致** | `assets/index-Bfq6-Ifx.js`、`index-DYowItI_.css` |
| `:9816` | `zeokeel-queue`（44 578 512 B，04:01） | `dbbc397e803c4e8c09c52a44838cb46934036d87eacf4b281fdf64e70867ec60` | **一致** | `assets/index-C42cBh3N.js`、`index-DYowItI_.css` |

两次都按 `scripts/build-web-client.sh:82-84` 的目录指纹（`dist/` 除 `BUNDLE_INFO` 外 6 个文件 → `sort -z` → `sha256sum` → `sha256sum`）用**服务的字节**重算，与 `gateway.json` 逐字符相同；`BUNDLE_INFO` 的 `builtAt` 分别为 `19:57:13Z` / `20:01:04Z`。
产物内含本轮修复标记：CSS 有 `@media(max-height:560px){.zk-now__suggestions{display:none}…}`，JS 有 `trimEnd`（R2-5 的 `replace(/\s+$/,'')` 被压缩成 `trimEnd()`）与 `dragend`（R2-3 兜底）、`zk-queue__row`。`git status --porcelain web/src internal/gateway/webclient/dist` 为空。

## 9.2 R2-1（会话页附件错误清除）—— PASS

| 页面 | 步骤 | `[role=alert]`（已排除 `.zk-tool-card` 内的工具错误） | chips |
|---|---|---|---|
| 落地页 | drop 20 MB | `["附件超过 10 MB"]` | 0 |
| | 再 drop 8 B | **`[]`** | `land-small.txt 2 B` |
| | 再 drop 20 MB | `["附件超过 10 MB"]` | 1 |
| | 再 drop 8 B | **`[]`** | 2 |
| 会话页 | drop 20 MB | `["附件超过 10 MB"]` | 0 |
| | 再 drop 8 B | **`[]`** | `thread-small.txt 2 B` |
| | 再 drop 20 MB | `["附件超过 10 MB"]` | 1 |
| | 再 drop 8 B | **`[]`** | 2 |
| 最新构建 `dbbc397e` 会话页复测 | 20 MB → 8 B | `["附件超过 10 MB"]` → **`[]`** | `latest-small.txt 2 B` |

即"成功清错"与"失败重现"都成立（反向不会把错误吞掉）。截图 `audit-composer-r3-thread-errors.png`、`-landing-errors.png`。

## 9.3 R2-2（窄屏 + 审批卡）—— 核心 PASS，"composer 首帧完整"未达标

`.zk-now__welcome` 保留 `overflow-y:auto`，新增 `@media (max-height: 560px)`：建议行 `display:none`、`.zk-thread{overflow-y:auto}`、欢迎列 `height:auto`。

| 视口（均有待审批卡） | 建议行 | 命中/可达 | 点击 | composer（首帧） | 横向滚动 |
|---|---|---|---|---|---|
| 320×480 | `display:none`（0×0） | 不存在 | 跳过（无元素） | **`y=361..549`，越界 69 px**；textarea `384..438` 可见可输入（实测输入 "hi" 成功）；`send 452..486` 被裁 6 px；`hint 511..529` 全在屏外；thread `500/431` 可滚，滚到底后 `292..480` 完整 | 无 |
| 320×640 | 渲染 | 静止 `[true,true,false]`；滚动后 explain 0–10 / review 0–65 / **automate 10–110** 可达 | 三个都 ok（草稿 3 行） | `452..640` 完整 | 无 |
| 375×500 | `display:none` | 不存在 | 跳过 | `354..500` 完整 | 无 |
| 390×844 | 渲染 | 三条 `hitsSelf=true` | ok ok ok | `698..844` 完整 | 无 |
| 1024×600 | 渲染 | 三条 `hitsSelf=true` | ok ok ok | `454..600` 完整 | 无 |
| 1440×900 | 渲染 | 三条 `hitsSelf=true` | ok ok ok | `754..900` 完整 | 无 |
| 1440×520（短桌面窗，补测） | `display:none` | 不存在 | 跳过 | `374..520` 完整 | 无 |

**结论**：R2-2 的目标（"不存在可见但不可点的元素" + 无横向滚动 + 1440/390 不受影响）达成；**但"composer 完整在视口内"只在滚动后成立**：320×480 + 审批卡（高 284 px）时 composer 底部越界 69 px，发送按钮被裁 6 px、提示行全在屏外。实现者给的 `top 292 / bottom 480` 是 `scrollTop=69` 的读数（我复现到同样的 292/480）。

建议（供下一轮，不阻塞收口）：
- 让审批卡在该断点参与占位（或折叠成一行摘要），使 `composer.bottom ≤ vh` 在 `scrollTop=0` 就成立；
- 或在出现审批卡时把 `.zk-thread` 自动滚到底（`scrollIntoView` composer），避免"要再滑一下才能点发送"。

**另一个取舍**（信息）：断点只看高度，所以 **375×500 与 1440×520 这类"矮但常见"的窗口也丢了三条建议**。桌面用户把窗口压矮（或开着一堆面板）时会看不到空态起步建议；可接受，但建议下一轮考虑 `max-height` 与 `max-width` 的组合，或给 3 条建议做单行横向滚动而不是整块隐藏。

截图 `audit-composer-r3-{320x480,320x480-withdock,320x480-detail,320x480-nodock,320x640,375x500,390x844,1024x600,1440x900,1440x520}.png`。

## 9.4 R2-3（拖拽取消）—— 机制 PASS，真 Esc 现象无法在本环境产生

| 检查 | 实测 |
|---|---|
| 真 CDP `dragEnter` → `dragCancel` | 覆盖层**仍在**（`data-dragging=true`），且页面**一个事件都没收到**：`window.__ev` 只有 `dragenter`/`dragover`，没有 `dragleave`/`dragend`/`drop` → CDP 的 dragCancel 不是真浏览器取消，页面无从响应（既不能证明修好，也不能证明没修好） |
| Playwright `keyboard.press('Escape')` / CDP `rawKeyDown Escape`（拖拽进行中） | 同样**不产生任何 DOM 事件**（事件表不变） |
| 真拖出窗口（`dragOver(-10,-10)`） | 收到 `dragleave(relatedTarget=null)` → **`data-dragging=false`、覆盖层消失** ✅ |
| 再拖入 | `data-dragging=true`、覆盖层出现 ✅ |
| 新增兜底（合成事件直测处理函数） | `window.dispatchEvent(new DragEvent('dragend'))` → **false** ✅；`window.dispatchEvent(new DragEvent('dragleave',{relatedTarget:null}))` → **false** ✅；卡片内 `dragleave` 带页面内 `relatedTarget`（页面内移动）→ 仍按"离开卡片"清除（既有语义） |

结论：实现补的两条兜底（`dragend`、window `dragleave` with `relatedTarget===null`）都生效，且"拖出窗口"这条真路径已用真 CDP 验到；真实浏览器 Esc 取消会派发 `dragend`（源）+ `dragleave`（目标），因此该路径在原理上已覆盖。**本沙箱无法产生"真 Esc 取消"的事件序列，故这一条只能到"机制 + 相邻真路径"，不能给"真 Esc 端到端"的证据。**

## 9.5 队列回滚（`28077ce`）—— PASS

流程：新建会话 → 发 #1（`acceptance-slow`，约 12 s）→ 运行中再发 #2（进队列，chip `1 条待发送`）→ 点 chip 打开 popover（标题 `待发送队列`）→ 点行内取消。

| 用例 | 输入状态 | 取消后 | 其他 |
|---|---|---|---|
| 空草稿 | `draft=""` | `value="排队中的第二条"`、`focus=composer-input`、光标 `[7,7]` | 队列行 0、chip 消失、**`POST /api/chat/run` 0 条** |
| 刷新 | —— | 队列空（无 chip）、`value="排队中的第二条"`、`localStorage["zk.draft.<sid>"]="排队中的第二条"` | 草稿持久化成立 |
| 非空草稿 | `draft="已有草稿A"` | `value="已有草稿A\n队列条目A"`（追加、不覆盖） | 0 条新 run |
| 草稿以换行结尾 | `draft="草稿B\n"` | `value="草稿B\n队列条目X"`（**无空行**，`/\n\n/` 不匹配） | 0 条新 run |
| 非队首取消 | 队列 `#1条目Y1 / #2条目Y2`，取消第 2 行 | `value="条目Y2"`，队列剩 `#1条目Y1` | 0 条新 run |
| 双击同一取消（旧 id） | 队列 `#1条目Y1`，`clickCount:2` | `value="条目Y2\n条目Y1"`（只还原一次，无重复、无异常） | 0 条新 run（`if (!item) return` 生效） |
| 撤回后立刻发送 | `value="条目Y2\n条目Y1"`，Enter | `POST /api/chat/run` 体 `{"text":"条目Y2\n条目Y1",…}`，发送成功、输入框清空 | 1 条 run |
| 纯空白草稿 | `draft="   "` | `value="带空格的条目"`（按"空"处理直接填入；注意条目本身在入队时已被 composer 的 `trim()` 裁掉尾部空格） | 焦点在输入框 |
| 队列为空 | 无 chip | 无 `.zk-queue__row`、无取消按钮 | 0 条请求 |

**补充事实（信息，不是缺陷）**：一轮结束后，队列会被网关**自动消费**（无需客户端发 `chat.run`：实测 `newRuns=0` 但 transcript 出现第 2 轮回答、状态栏 `2 轮`）。所以"撤回"的时间窗 = 该轮结束之前；队列已消费后自然无项可撤。

**未实现（需协议变更）**：队列重排。协议方法表只有 `prompts.snapshot / enqueue / remove / subscribe / unsubscribe`（`docs/TRANSPORT.md:561-565`；`internal/gateway/contract.json` 同集合），**没有 `prompts.move`**；`web/src/features/thread/prompt-queue-panel.tsx` 的行内只有「#序号 + 文本 + 取消」和一个「发送队首」页脚，没有任何拖拽/上移/下移控件。按"未实现/需协议变更"记录，不计缺陷。

截图 `audit-composer-r3-queue-{open,cancelled,append,newline,trailing-nl,nonhead,second,send}.png`。

## 9.6 快查回归（`dbbc397e` 构建）—— PASS

| 项 | 实测 |
|---|---|
| 贴图（真剪贴板） | 正文 `前缀` 不变、chip `image.png 128 B`、`POST /api/blobs` 1 条 |
| 一次 3 文件 drop | chips 与 POST 均 `g-a.txt → g-b.txt → g-c.txt` |
| F6 单发 `dragenter` | `data-dragging=true` + 覆盖层；`dragleave` 到外部 → false |
| F5 卡片外 drop | `dragover/drop` 均 `defaultPrevented=true`、URL 不变、无导航、无附件 |
| F3 推荐追加 | `落地草稿\n检查最近的改动有没有明显问题`、`focus=composer-input`、光标 19 |
| 回形针 + 删除 | `favicon.svg 374 B`；`createObjectURL` 与 `revokeObjectURL` 同一条配对 |
| 带附件发送 | `POST /api/chat/run` 体含 4 个 `attachments` id（1 图 + 3 文本） |
| F1 禁用不暂存（`:9812`/comp3） | 待审批时 paste → chips 0、0 条上传；dragenter `data-dragging=false`；drop 被 window 守卫 `preventDefault`、chips 0；解除后仍有 0；启用后 paste 立刻 1 条上传 |
| R2-1 最新构建复测 | 会话页 `["附件超过 10 MB"]` → `[]` |

截图 `audit-composer-r3-{f1-disabled,sweep,regress,dragenter,latest}.png`。

## 9.7 本轮残留

| 编号 | 级别 | 内容 |
|---|---|---|
| R3-1 | 低（观感/一步操作） | 320×480 + 审批卡时 composer 首帧越界 69 px（`send` 被裁 6 px、`hint` 在屏外），需滚一次；`.zk-thread` 已可滚，故不阻塞。建议见 §9.3 |
| R3-2 | 低（取舍） | `max-height:560px` 一刀切隐藏建议 → 1440×520 / 375×500 这类矮窗口也丢建议；建议按宽度组合或改横向滚动 |
| R3-3 | 信息 | 真 Esc 取消在 headless/CDP 下不产生任何事件，无法端到端取证（机制已补，相邻真路径已验） |
| R3-4 | 信息 | 队列在一轮结束后由网关自动消费，撤回窗口仅限该轮未结束时 |

## 9.8 第三轮脚本与截图

| 脚本 | 覆盖 |
|---|---|
| `c2r3-r1-error.mjs` | R2-1：落地页 + 会话页，正反两个方向 |
| `c2r3-r2-layout.mjs` / `c2r3-r2-480.mjs` / `c2r3-480-dock2.mjs` / `c2r3-sendbtn.mjs` | R2-2：7 档视口 × 建议可达性 × composer 几何 |
| `c2r3-r3-cancel.mjs` / `c2r3-r3-esc-drag.mjs` | R2-3：dragCancel / 真拖出窗口 / dragend / window dragleave |
| `c2r3-r5-newline.mjs` | R2-5：5 种草稿结尾 |
| `c2r3-queue1.mjs` / `c2r3-queue2.mjs` / `c2r3-queue3.mjs` / `c2r3-queue4.mjs` / `c2r3-queue5.mjs` | 队列回滚：基础/追加/尾换行/非队首/双击/空队列/撤回后发送/刷新/自动消费/空白草稿 |
| `c2r3-sweep.mjs` / `c2r3-sweep2.mjs` / `c2r3-regress2.mjs` / `c2r3-final.mjs` / `c2r3-final-check.mjs` | 快查回归 + 最新构建 R2-1 |

截图 27 张（`audit-composer-r3-*`），全部在 `.cache/ui-audit/shots/`。

---

# 第四轮（定点）复验（提交 `d8a8ba7`，构建 `ec1801b7`）

> 复验对象：`d8a8ba7 fix(web): keep the composer on screen in a short window, and stop hiding the suggestions on merely short ones`（R3-1 / R3-2）
> 实例：`:9820`（`data-c2d`，`zeokeel-comp4`，`chat.model=acceptance-tool`）+ `:9822`（`data-c2ds`，`zeokeel-comp4` + `acceptance-slow`，用于队列/Esc 停止）
> 截图：`.cache/ui-audit/shots/audit-composer-r4-*.png`（20 张）；脚本 `c2r4-*.mjs`

## 10.0 结论（第四轮）

**R3-1 PASS（首帧发送按钮就在视口内，滚动不出现双 composer，1440×900 无 sticky 副作用）；R3-2 在你点名的视口全部 PASS（1440×520 / 1280×520 建议可见可点、320×480 隐藏、390×844 正常、跨 560 即时切换），但在"短窗 + 审批卡 + 建议换行"的中等宽度带（≈481–1024 px）里仍有"可见但被审批卡盖住"的残留。深色 sticky 背景 PASS。快查回归全 PASS。**

| # | 项 | 结果 | 一句话证据 |
|---|---|---|---|
| R3-1 | 320×480 / 320×640 / 375×500 + 审批卡：**首帧** `composer-send` 在视口内；可滚且滚到底无双 composer；1440×900 无怪象 | **PASS** | 320×480：composer `300..480`（`position:sticky`、`z=1`、`bg=rgb(255,255,255)`），`send 391..425`，`inViewport=true`、命中测试命中按钮自身；thread `474/431` 可滚，滚到底 composer 仍 `300..480`、`[data-testid=composer]` 与 `.zk-composer` 各 **1 个**；320×640 `452..640`（relative）；375×500 `362..500`（sticky，send `423..457`）；1440×900 `754..900`（`position=relative`，媒体查询未生效）；四档 `overflowX=false` |
| R3-2 | 1440×520 / 1280×520 建议可见且可点；320×480 隐藏；390×844 正常；跨 560 即时切换 | **PASS（点名视口）+ 残留 §10.3** | 1440×520 与 1280×520：`display:flex`、三条 `hitsSelf=true`、真实点击 ok、composer sticky `382..520`、send 命中；320×480 与 480×520：`display:none`（0×0）；390×844：三条 `hitsSelf=true`、点击 ok；实时 `setViewportSize` 跨界：320×{480→600→560→561} = none/sticky→flex/relative→none/sticky→flex/relative，1440×{520→600→559} = flex/sticky→flex/relative→flex/sticky（**无刷新即时切换**） |
| 深色 sticky | 背景不得透出下方内容 | **PASS** | 320×480 与 1440×520（sticky 生效）：`.zk-composer` `background-color=rgb(17,17,24)` = `--zk-bg-base`（`#111118`）= body 背景、**alpha=1 不透明**、`z-index=1`；320×480 下审批卡与 composer **重叠 31 px**（dock `65..331` vs composer `300..480`），但 composer 顶/中/底三处 `elementFromPoint` 都命中 composer 自身（`DIV.zk-composer` / 工具条），滚动后再探仍是 composer；截图无穿透。1440×900（媒体查询未生效）为 `relative` + 透明背景，本来就没有内容压在它下面 |
| 快查回归 | R2-1 / R2-5 / 队列撤回 / F1 / 贴图 + drop / Esc 四层 | **PASS** | 见 §10.4 |

## 10.1 版本自证（第四轮）

```
GET / = 200 | /favicon.svg = 200 | /api/capabilities(无授权) = 401
gateway.json(:9820 与 :9822 相同): bundleSha256=ec1801b73e120a30340cfab0a68563944f34926c314a6b64649fcfc9636f0c1d, bundleBuiltAt=2026-11-24T20:10:11Z
```

用 **HTTP 取到的字节**复算目录指纹（`dist/` 除 `BUNDLE_INFO` 外 6 个文件）得
**`ec1801b73e120a30340cfab0a68563944f34926c314a6b64649fcfc9636f0c1d`**（逐字符相同）；产物 `assets/index-CHSwfU0z.js`、`index-CkW2O26Z.css`；压缩后的 CSS 里可见本轮两条规则原文：

```css
@media (height<=560px){.zk-thread{overflow-y:auto}
  .zk-composer{z-index:1;padding-bottom:var(--zk-space-2);background:var(--zk-bg-base);position:sticky;bottom:0}
  .zk-now__welcome{height:auto;padding-bottom:var(--zk-space-4)}}
@media (height<=560px) and (width<=480px){.zk-now__suggestions{display:none}}
```

## 10.2 R3-1（首帧在视口内 / 无双 composer / 无 sticky 副作用）—— PASS

| 视口（有待审批卡） | composer | `send` | 命中 | 滚动后 | 节点数 | 横向滚动 |
|---|---|---|---|---|---|---|
| 320×480 | `sticky` `300..480`（whole） | `391..425` ✅ | `hitEl=BUTTON.ant-btn…`（就是发送按钮） | `scrollTop=43`，composer 仍 `300..480` | 1 / 1 | 无 |
| 320×640 | `relative` `452..640` | `543..577` ✅ | ✅ | 不可滚（`591/591`） | 1 / 1 | 无 |
| 375×500 | `sticky` `362..500` | `423..457` ✅ | ✅ | 不可滚（`451/451`） | 1 / 1 | 无 |
| 1440×900 | `relative` `754..900`（媒体查询未生效） | `815..849` ✅ | ✅ | 不可滚 | 1 / 1 | 无 |

"滚到底不出现双 composer"：`[data-testid="composer"]` 与 `.zk-composer` 在首帧与滚到底两种状态下都恰好 1 个（sticky 只是同一个节点固定，不是复制）。1440×900 的 `position=relative / z=auto`、审批卡 `573..742` 位于 composer 之上方，没有重叠或空隙。

截图 `audit-composer-r4-first-{320x480,320x640,375x500,1440x900}.png`、`-320x480-scrolled.png`。

## 10.3 R3-2（宽而矮不再隐藏；短且窄才隐藏）—— 点名视口 PASS，中等宽度带残留

| 视口 | 建议行 | 三条可达 | 真实点击 | composer |
|---|---|---|---|---|
| 1440×520 | `display:flex` | `[true,true,true]` | ok（填入 `说说这个仓库…`） | `sticky 382..520` |
| 1280×520 | `display:flex` | `[true,true,true]` | ok | `sticky 382..520` |
| 320×480 | `display:none` | 不存在（0×0） | 跳过 | `sticky 300..480` |
| 480×520（边界内） | `display:none` | 不存在 | 跳过 | `sticky 382..520` |
| 390×844 | `display:flex` | `[true,true,true]` | ok | `relative 698..844` |
| 1440×900 | `display:flex` | `[true,true,true]` | ok | `relative 754..900` |

实时跨 560 边界（同一页面 `setViewportSize`，无刷新）：

```
320×480  → none   / sticky    | 320×600 → flex / relative | 320×560 → none / sticky | 320×561 → flex / relative
1440×520 → flex   / sticky    | 1440×600 → flex / relative | 1440×559 → flex / sticky
```

**残留 R4-1（低—中，短窗 + 审批卡 + 中等宽度）**：高度 ≤560 且宽度在 `481…~1024` 时，规则把建议显示出来，但审批卡仍压在欢迎区上；当建议**换行到第二行**时，第二行（或整排）落在审批卡后面 → **可见但点不到**，且 `.zk-now__welcome` / `.zk-thread` 都不可滚，无法"滚到"：

| 宽度（h=520，有审批卡） | 建议 | 静止可达 | 任何滚动位置可达 |
|---|---|---|---|
| 320 / 400 / 440 / 460 / 480 | none | — | — |
| **481 / 500 / 520 / 540** | flex | `[false,false,false]`（命中 `SECTION.zk-approval` / `SPAN.zk-approval__origin`） | 无（欢迎列不可滚） |
| 560 / 600 / 640 | flex | `[true,false,false]` | 仅 #1 |
| 700 / 800 | flex | `[true,true,false]` | 仅 #1#2 |
| 900 | flex | `[true,true,true]`（窄壳侧栏收起，一行放得下） | 全部 |
| **1024** | flex | `[true,true,false]` —— 第三块（`automate`）换到第二行，`y=205..243`，命中 `DIV.zk-approval__header`；thread `471/471` 不可滚 | 无 |
| 1280 / 1440 | flex | `[true,true,true]` | 全部 |

1024×520 的几何：`.zk-now__welcome` `65..259`、建议行 `159..243`（`explain/review` 第一行 `159..197`，`automate` 第二行 `205..243`）、审批卡 **`201..370`** → 第二行整段在卡后。把待审批卡全部处理掉后复测：dock 消失，三条 `[true,true,true]` 且点击 ok（证明遮挡源就是审批卡）。

建议修法（任选，均不改协议）：① 短窗下让审批卡参与正常流（给欢迎列 `padding-bottom` = 卡片高度，或把 dock 放到欢迎列之后），从根上消除重叠；② 短窗（≤560）**且存在审批卡**时隐藏建议（与 320×480 一致），而不是按宽度判断；③ 建议行强制单行 + 横向滚动（`flex-wrap:nowrap; overflow-x:auto`），避免换行到第二行正好落在卡后。

截图 `audit-composer-r4-{1440x520,1280x520,320x480,480x520,390x844,1440x900,481x520,1024x520-rest,1024x520-scrolled,1024x520-nodock}.png`。

## 10.4 深色 sticky 背景 —— PASS

| 视口 | `--zk-bg-base` | body bg | `.zk-composer` bg / pos / z | 卡片与 composer 重叠 | 探针（composer 顶/中/底） |
|---|---|---|---|---|---|
| 320×480（dark） | `#111118` | `rgb(17,17,24)` | `rgb(17,17,24)` / `sticky` / `1` | **31 px**（dock `65..331`，composer `300..480`） | `DIV.zk-composer` / `DIV.ant-select…` / `DIV.zk-composer` —— 均为 composer 自身，没有审批卡文字穿透 |
| 1440×520（dark） | `#111118` | `rgb(17,17,24)` | `rgb(17,17,24)` / `sticky` / `1` | 0 px（dock `201..370`，composer `382..520`） | 同上 |
| 1440×900（dark） | `#111118` | `rgb(17,17,24)` | `rgba(0,0,0,0)` / `relative` / `auto` | 0 px | 媒体查询未生效，透明背景正常（无内容压在其下） |

`background-color` 的 alpha = 1（不透明）且与页面底色同值，因此 31 px 的重叠被 composer 不透明背景完全挡住；截图 `audit-composer-r4-dark-{320x480,1440x520,1440x900}.png` 肉眼确认无穿透（审批卡的"拒绝/允许一次"按钮在 composer 之上方完整可见）。

## 10.5 快查回归（`ec1801b7`）

| 项 | 实测 |
|---|---|
| R2-1 | 会话页 20 MB → `["附件超过 10 MB"]`；再 8 B → **`[]`** + chip |
| R2-5 | `"第一行\n"` + 点「review」→ `"第一行\n检查最近的改动有没有明显问题"`（无空行） |
| 队列撤回（`:9822`） | chip `1 条待发送` → 行 `#1排队中的这条` → 取消 → chip 消失、`value="排队中的这条"`、`focus=composer-input`、**新 `chat.run` 0 条** |
| 队列后立刻发送 | `POST /api/chat/run` 体 `{"text":"排队中的这条",…}` ✅ |
| Esc 停止本轮（`:9822`） | `{"id":21,"method":"run.cancel","params":{"sessionId":"sess-…"}}`，回合结束 |
| F1 禁用不暂存 | 待审批态：paste → 附件 0、上传 0；`dragenter` → `data-dragging=false`；`drop` → 附件 0、上传 0 |
| 贴图 / drop | 正文 `前缀` 不变 + chip `image.png 123 B` + 1 条上传；3 文件 drop 顺序 `r4-a → r4-b → r4-c`（chips 与 POST 同序） |
| Esc 四层 | 拒审批：`approvals 1 → 0`、`disabled true → false`、会话"已完成"；清草稿：`要清掉的` → `""`；斜杠菜单：6 个候选、Esc 关菜单保留 `/`；停止本轮见上行 |

截图 `audit-composer-r4-{regress,queue}.png`。

## 10.6 第四轮脚本与截图

| 脚本 | 覆盖 |
|---|---|
| `c2r4-r31.mjs` | R3-1：四档视口首帧/滚动后 composer 与 send、双 composer 检查 |
| `c2r4-r32.mjs` | R3-2：七档视口建议可见/可达/点击 + 实时跨 560 边界 |
| `c2r4-width-sweep.mjs` / `c2r4-1024.mjs` / `c2r4-nodock1024.mjs` | 残留 R4-1 的宽度带、1024×520 遮挡几何、去掉审批卡后的对照 |
| `c2r4-dark2.mjs` / `c2r4-dark3.mjs` | 深色 sticky 背景不透明度与穿透探针 |
| `c2r4-regress.mjs` / `c2r4-esc2.mjs` / `c2r4-queue.mjs` | 快查回归、Esc 四层、队列撤回与停止本轮 |

截图 20 张（`audit-composer-r4-*`），全部在 `.cache/ui-audit/shots/`。

---

# 第五轮（定点）复验（提交 `c7b6c1a`，构建 `f479ded6`）

> 复验对象：`c7b6c1a fix(web): a short window with an approval card drops the suggestions`（R4-1）
> 实例：`:9824`（`data-c2e`，`zeokeel-comp5`，`chat.model=acceptance-tool`）+ `:9826`（`data-c2es`，`zeokeel-comp5` + `acceptance-slow`）
> 截图：`.cache/ui-audit/shots/audit-composer-r5-*.png`（10 张）；脚本 `c2r5-*.mjs`

## 11.0 结论（第五轮）

**R4-1 PASS（有卡的短窗：12 档宽度全部隐藏建议，可见但不可点元素计数 0；无卡的短窗：12 档全部可见且三条可点，1440×520 保持可用）；`:has()` 在目标 Chromium 可用、规则确实带进产物、并且随卡片出现/消失即时切换；退化路径已实测并记录。快查（R3-1 / 深色 sticky / R2-1 / R2-5 / 队列撤回 / F1）全 PASS。**

| # | 项 | 结果 | 一句话证据 |
|---|---|---|---|
| 1 | R4-1：h=520 **有卡**，481→1440 全宽度扫描 | **PASS** | 12/12 档 `display:none`、`hasMatch=true`、**可见但不可点元素 0**（481/500/520/540/560/640/700/800/900/1024/1280/1440） |
| 2 | 无卡同宽度扫描，建议可见且三条可点 | **PASS** | 12/12 档 `display:flex`、`hits=[true,true,true]`、真实点击 ok、`hasMatch=false`；**1440×520 明确可用** |
| 3 | `:has()` 可用性 + 退化行为 | **PASS（含退化说明）** | `CSS.supports('selector(:has(*))')=true`（HeadlessChrome/153）；产物 CSS 内含 `@media (height<=560px){.zk-thread:has(.zk-approval) .zk-now__suggestions{display:none}}`；`document.querySelector('.zk-thread:has(.zk-approval)')` 在有卡时命中、无卡时为空；**退化实测**（强制 `display:flex!important` 模拟不认 `:has()` 的浏览器）：1024×520 有卡 → 建议显示且 `automate` 被 `DIV.zk-approval__header` 盖住 = 1 个不可点 → 即 R4-1 会复现（可接受，已在 §11.4 记录） |
| 4 | 快查：R3-1 / 深色 / R2-1 / R2-5 / 队列 / F1 | **PASS** | 见 §11.5 |

## 11.1 版本自证（第五轮）

```
GET / = 200 | /favicon.svg = 200 | /api/capabilities(无授权) = 401
gateway.json(:9824 与 :9826 相同): bundleSha256=f479ded6368611b303640db0f9bfe704f5a26e2e9262625f76a6077bf7f9490a
                                  bundleBuiltAt=2026-11-24T20:19:16Z
```

用 **HTTP 取到的字节**复算目录指纹（`dist/` 除 `BUNDLE_INFO` 外 6 个文件 → `sort -z` → `sha256sum` → `sha256sum`）=
**`f479ded6368611b303640db0f9bfe704f5a26e2e9262625f76a6077bf7f9490a`**，与 `gateway.json` 逐字符相同；产物 `assets/index-Btm91pZS.js`、`index-DyGGc8pB.css`。压缩 CSS 中本轮规则原文：

```css
@media (height<=560px){.zk-thread:has(.zk-approval) .zk-now__suggestions{display:none}}
```

## 11.2 R4-1 全宽度扫描（h=520）

**有卡**（先在会话里触发审批，回到 `/`；dock 是全局的，`.zk-thread:has(.zk-approval)` 命中）：

| 宽度 | 建议行 | 三条命中 | 可见但不可点 |
|---|---|---|---|
| 481 / 500 / 520 / 540 / 560 / 640 / 700 / 800 / 900 / 1024 / 1280 / 1440 | 全部 `display:none`（0×0） | `[false,false,false]`（元素未渲染） | **0** |

**无卡**（同一页面，把审批处理掉之后）：

| 宽度 | 建议行 | 三条命中 | 真实点击 | `hasMatch` |
|---|---|---|---|---|
| 481 / 500 / 520 / 540 / 560 / 640 / 700 / 800 / 900 / 1024 / 1280 / 1440 | 全部 `display:flex` | `[true,true,true]` | `ok`（填入 `说说这个仓库…`） | `false` |

**动态性**（1024×520，同一页面不刷新）：有卡 → `hasMatch=true`、`display:none`、`[false,false,false]`；按一次 Esc 处理掉最后一张卡 → `hasMatch=false`、`display:flex`、`[true,true,true]`、点击 ok（草稿 `每天早上帮我汇总一次未读事项`）。规则随 DOM 即时生效，不需要重载。

截图 `audit-composer-r5-{481?…}`→ 实际为 `-1440x520-nocard.png`、`-1024x520-card.png`、`-1440x520-card.png`、`-after-resolve.png`、`-dynamic-1024x520.png`。

## 11.3 `:has()` 可用性与退化—— PASS（含说明）

| 检查 | 实测 |
|---|---|
| 浏览器支持 | `CSS.supports('selector(:has(*))')` = **true**（`HeadlessChrome/153.0.0.0`） |
| 产物中的规则 | 见 §11.1，压缩后仍完整保留（`zk-thread:has(.zk-approval)`） |
| 结构前提 | 审批卡确实是 `.zk-thread` 的后代（有卡时 `document.querySelector('.zk-thread:has(.zk-approval)')` 命中，无卡时为 `null`）—— 规则不是"写了但永远不匹配" |
| **退化行为** | 注入 `display:flex!important` 模拟"浏览器不认 `:has()`（整条规则被 CSS 解析器丢弃）"：1024×520 + 有卡 → 建议显示、`automate` 的命中元素是 `DIV.zk-approval__header`、不可点元素 **1**。即退化 = 回到第四轮的 R4-1 现象（"可见不可点"），**不会崩、不会报错**；这是可接受的降级，因为 `:has()` 已在 Chrome/Edge 105+、Safari 15.4+、Firefox 121+ 支持 |

## 11.4 快查回归（`f479ded6`）

| 项 | 实测 |
|---|---|
| R3-1 首帧（320×480 + 有卡） | composer `sticky 300..480`（whole）、`[data-testid=composer-send]` `391..425` 且命中自身、`.zk-composer` 节点 1 个、`display:none` 建议、`overflowX=false` |
| 深色 sticky 不穿透（320×480） | `background-color=rgb(17,17,24)` = `--zk-bg-base #111118` = body 底、**alpha=1**、`z=1`；审批卡与 composer 重叠 31 px，composer 顶/中探针均为 composer 自身 |
| R2-5 | `"甲\n"` + 点「review」→ `"甲\n检查最近的改动有没有明显问题"`（无空行） |
| R2-1 | 会话页 20 MB → `["附件超过 10 MB"]`；再 8 B → **`[]`** |
| 队列撤回（`:9826`） | chip `1 条待发送` → 行 `#1排队中的这条` → 取消 → chip 消失、`value="排队中的这条"`、`focus=composer-input`、新 `chat.run` **0** 条 |
| F1 禁用不暂存 | 待审批态：paste → 附件 0、上传 0；`dragenter` → `data-dragging=false`；`drop` → 附件 0、上传 0 |

截图 `audit-composer-r5-{320x480-card,dark-320x480,regress,queue}.png`。

## 11.5 第五轮脚本与截图

| 脚本 | 覆盖 |
|---|---|
| `c2r5-has.mjs` | `:has()` 支持与结构前提、有卡/无卡 12 档宽度扫描、退化（强制显示）复现 |
| `c2r5-quick2.mjs` | R3-1 首帧、深色 sticky、R2-5、F1、R2-1 |
| `c2r5-dynamic.mjs` | 卡片消失后建议即时回来（1024×520） |
| `c2r5-queue.mjs` | 队列撤回（`:9826` 慢模型） |

截图 10 张（`audit-composer-r5-*`），全部在 `.cache/ui-audit/shots/`。

---

## 附：五轮累计结论

| 轮次 | 提交 / 构建 | 结论 |
|---|---|---|
| 1 | `40ed5d7` / `3ca1c3d7` | 清单 6 PASS / 1 FAIL（禁用态不拦附件）+ 5 个低危 |
| 2 | `4264b8a` / `2b6c8b1b` | F1/F3/F5/F6 PASS；F2 线程页残留、F4 320×480 不可达 |
| 3 | `1d79fac` + `28077ce` / `1ca0a432`、`dbbc397e` | R2-1/R2-5/队列回滚 PASS；R2-2 核心 PASS（composer 首帧越界 69 px）；R2-3 机制 PASS（真 Esc 不可观测） |
| 4 | `d8a8ba7` / `ec1801b7` | R3-1 PASS、R3-2 点名视口 PASS、深色 PASS；残留 R4-1（短窗 + 卡 + 中等宽度可见不可点） |
| 5 | `c7b6c1a` / `f479ded6` | **R4-1 PASS**（有卡 12/12 档零不可点、无卡 12/12 档全可点）、`:has()` 可用且随卡即时切换、退化已记录；快查全 PASS |

**收口状态：本项（输入框附件粘贴/拖放 + 落地页空态推荐 + 队列撤回 + 短窗布局）在第五轮构建上无已知未闭环缺陷；剩余两项不在本项范围（队列重排需协议 `prompts.move`；`:has()` 不支持浏览器的降级为"建议显示但可能被审批卡遮挡"）。**
