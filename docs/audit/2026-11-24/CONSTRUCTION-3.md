# 施工记录 3：会话循环（回复 / 思考 / 工具 / 时序 / 组件）

> 范围：`docs/audit/2026-11-24/INTERACTION.md` 之后的第三阶段，只做会话主循环。
> 方法没变：先由独立审计员用真网关 + 真 Chromium 证伪，再修，再验；每轮换人或换包。

## 0. 夹具先行

`internal/devtools/fakemodel` 原来只有 `plain / tool / slow` 三个场景，且 **`text()` 用
`strings.Fields` 拼词，把每个换行都抹成空格**——所有 markdown 夹具到浏览器里都塌成一行，
两轮审计在这份失真数据上跑过。现在：

- `splitKeepingLines` 保留空白；
- 场景扩到 `think / thinktool / thinktwice / rich / long / fence / multi / fail / longcmd`，
  覆盖「推理→工具→回答」「两轮推理中间夹工具」「两次调用」「工具失败」「300 字无空格命令」
  「未闭合围栏」。

## 1. 五路审计各抓到了什么

| 方向 | 关键失败 | 修法 |
|---|---|---|
| 回复渲染 | 表格无表头/无边框、引用块只有缩进、h2 是 UA 的 21px、行内码 11.9px（库的 `--code-inline-text:.85em` 没被覆盖）、代码块无语言标签/无复制键、链接 `#1677ff` 4.10:1 且点击导航离开、14 列宽表 2053px 高、阅读列 433↔1128 跳变 | 排版变量写在 `.x-markdown` 本身（一次修 7 个值）；表格/引用/标题按 token 重写；宽表横向滚动、`align` 生效；`header={lang?undefined:false}` + 主题化 `highlightProps`；链接 `_blank`+`noopener`；68ch 量度 |
| 思考链 | `durationMs` 在快照映射里被丢、链用了不存在的 `desc`、流式思考无 CSS（换行塌陷、3.36:1）、**推理期间工具步骤被早返回吞掉**、Think 箭头是死控件、链标题不可聚焦 | `description` + 轮数；`.zk-thinking-text` pre-wrap/13px/8:1；工具步骤与 Think 同屏；箭头可折叠；标题变真 `<button aria-expanded>`；`reasoningSegments` + `textOffset` 逐轮交错 |
| 工具回显 | 运行期无目标段（实时 `input` 是 JSON 字符串）、审批卡不说访问哪里、失败红框显示枚举 `network`、拒绝显示 rig 英文句、长输出被 slice(24)、300 字命令把卡片撑到 2552px | `asRecord` 解字符串；审批 `url/query` 目标；失败文本优先真实输出；`denied` 语义 + 中性提示；内滚 + `min-width:0` |
| 整轮时序 | 落地页回车后 220–584ms 无反馈、停止显示红色 `cancelled`、22 次停止只有 5 次显示「已停止」、运行中出现两个停止键、创建期停止键是空操作、上传中能发出导致附件丢失、队列面板 Esc 抢占停止 | 创建提示（9.6–46ms）、`errorKind=cancelled` → 已停止、refresh 拒绝把 aborted 降级、唯一停止键、上传态 + 发送守卫、队列面板 overlay 级 Esc |
| 组件选型 | 13 处 `SectionRow` 用 div 而非 label/value 语义、附件无上传态、三套"进行中"表达、`Bubble.List`/`Actions`/`Prompts` 反向建议 | `<dl>`、上传 spinner + `aria-busy`、统一 spinner；**不换** `Bubble.List`（无虚拟化）/`Actions`（0 个真按钮）/`Prompts`（不可键盘） |

## 2. 最后几轮修的，都是"改一处、漏一处"

- **两份白名单**：快照映射和差值映射是两份独立代码，`errorKind`、`reasoningSegments` 各漏过一次。
- **停止文案的竞态**：`run.cancel` 与 `session.snapshot` 同 tick 发出，快照读到 cancel 落库之前 →
  已停止活 23ms 被覆盖。refresh 现在拒绝把刚中止的最后一行动降级为 `end_turn`。
- **成功卡上的红框**：修"失败文本"时忘了门控 `status==='error'`，所有成功卡都把输出当错误播报了一遍。
- **取消被当成完成**：`ok = status != "error"` 把 `cancelled` 算成功，客户端两处再压回 `ok`。

## 2.5 收尾时补的两笔

- **代码块配色改成算出来的**：`oneLight` 的 number 是 3.72:1，换 stock 浅色主题反而更差（2.50/2.60）。
  新增 `syntax-theme.ts`：把主题里**每个** token 颜色按实际 surface 沿色相二分校正到 ≥4.5，
  `syntax-theme.test.ts` 逐 token 度量（亮/暗两套、彩色比例 >1/3、surface 字面量必须等于 `tokens.css`）。
  两次栽在"换了主题没量"，所以现在有不量就红的单测。
- **停止的取消加了兜底**：为了"先上屏"，cancel 被推到下一帧（实测 12/12 ≤33ms，中位 13.8ms，
  服务端 23/23 cancelled）；但后台标签页可能不产帧，所以 rAF 与 `setTimeout(100)` 赛跑、先到先执行。

## 2.6 终验数字（bundle `f7dab3a0…`）

| 面 | 结果 |
|---|---|
| 代码块几何 | 首字距边框 14px、只有 1 层边框、字号行高 13/20（工具卡与栅栏、亮与暗四种组合一致） |
| token 对比度 | 浅色 min 4.5047（number）/ 深色 min 4.5204（comment），全量 0 个 <4.5；校正后仍 62% 彩色 |
| 停止首帧 | 两次实测 +12ms / +13ms 即首个绘制帧，中间无缺帧；服务端 `tool_result status=cancelled`、`run_end error=cancelled` |
| 停止（全量） | 12/12 ≤33ms（中位 13.8ms），服务端 23/23 cancelled |

## 3. 已知限制（写在这里，不假装没有）

- 同一轮里两次**完全相同**的 `web_fetch` 只弹一次审批。已实测确认这是"一次决定、作用于同一轮里的同签名调用"，
  不是漏问：批准一次 → 两个调用都执行；**拒绝一次 → 两个调用都被拒绝**（`status=denied`、两张卡都显示
  「已拒绝」，且服务端只收到一次 `approval.resolve`）。就 UI 而言决定与结果是一致的；是否应该逐个确认属于
  产品/治理口径，不是渲染缺陷。
- 刷新后流不重连：能看到运行态、能停止，但正文不再增长（需要 `run.attach` 这类协议补口）。
- 审批解决后的 toast 会多留 3–6s（通知超时，非阻塞）。
- `<Welcome>` 把标题渲染成 `h4`，页面 h1 到 h4 跳级（组件固定行为，未替换）。
