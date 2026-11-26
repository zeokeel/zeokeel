# ZeoKeel UI/UX 重构设计稿（接力入口）

这是一套**从后端功能反推**的前端重构方案：不沿用旧前端骨架，纯从后端运行形态推导 IA 与交互。
高保真设计稿是真实渲染的静态 HTML（非 Figma），用的是可商用、可内嵌打包的 OFL 字体。

## 先看这两样
1. **[DESIGN-PROPOSAL.md](DESIGN-PROPOSAL.md)** — 完整方案：三条核心论点、反推版 IA、7 屏清单、craft 自检、落地路线。
2. **[shots/contact-sheet.png](shots/contact-sheet.png)** — 7 屏总览图。单屏在 `shots/*.png`。

## 三条论点（一句话）
- 浏览器是**自治机器的观察窗 + 控制台**，不是聊天框（后端 30s/60s 自转）。
- 后端**六类人类闸门**（批准/提问/计划/外发/返工/升级）是同一概念，合并成一条**决策队列**。
- **工作单位是任务**（带租约/证据/返工/审计），会话只是运行载体。

## 目录结构
```
DESIGN-PROPOSAL.md     方案正文（含「从截图到代码」的落地路线）
shots/*.png            7 屏截图 + contact-sheet 总览
comps/
  tokens.css           ★ 设计令牌层 = antd re-skin（颜色/字体/间距/组件皮肤）
  icons.html           1.6 描边图标族（SVG symbols）
  _ops-side.html       反推版分组侧栏（OPS/BOARD/RUN/FLOW/ROLES/REPORT 复用）
  <NAME>.full.html     可直接浏览器打开的成品页（OPS/BOARD/RUN/LAUNCH/FLOW/ROLES/REPORT）
  ops-*.html / body-*  成品页的 body 片段（配 assemble 脚本）
fonts/                 自托管 OFL 字体：Inter / 思源黑体(子集) / JetBrains Mono
assemble-ops.mjs       片段 + 侧栏 + tokens → .full.html（OPS 系列）
render.mjs             用 playwright-core 批量截图
```

## 直接查看
把任一 `comps/*.full.html` 用浏览器打开即可（字体走相对路径 `../fonts/`，已自托管，无需联网）。

## 重新渲染截图（可选）
```bash
cd docs/ui-redesign
npm_config_cache=.npm-cache npm i playwright-core          # /root/.npm 可能只读，故指向本地缓存
PLAYWRIGHT_BROWSERS_PATH=.pw-browsers npx playwright-core install chromium
node assemble-ops.mjs                                       # 重建 .full.html
PW_CHROME=$(find .pw-browsers -name chrome -o -name headless_shell | head -1) node render.mjs
```
`node_modules/ .pw-browsers/ .npm-cache/` 已在本目录 `.gitignore` 忽略，不会进仓库。

## 接力做实现时（低风险顺序，详见 DESIGN-PROPOSAL §5）
1. `tokens.css` → `web/src/design/antd-theme.ts` 的 `theme.token`（零结构改动，先换肤）
2. 重写 `web/src/shell/sidebar-nav.tsx` 分组 + 收敛双收件箱
3. 新建 `features/decisions/`：聚合 approvals/ask/plan/tasks.send/tasks.rework/notify 六域为一条队列
4. 补后端已就绪、前端零引用的缺口：`api/workflow.ts` / presets 可视化 / `api/reports.ts` / `tasks.rework.reset` 接线
5. 任务板接 `tasks.snapshot/delta`；RUN 在现有 thread 上加任务横幅 + 证据栏 + 提交验收
