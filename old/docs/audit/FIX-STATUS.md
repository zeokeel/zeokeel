# 审计修复状态跟踪（Lead 维护）

> **wave 1 已提交 `1da4f12` + `da12d44`**：P0 + 大部分 P1 完成，全量门禁绿；修复清单/验证见 [`FIX-REPORT.md`](FIX-REPORT.md) §1–§5。
> **wave 2 进行中**：产品面（PD-05/06/07/10/12）、功能缺口（FP-19/04/07/16/15/17/01/03/05/14）、构建信息/import CLI、预览金标、独立验证（FIX-VERIFY-CORE2/UI/PRODUCT）。分工与接口契约见 [`FIX-PLAN.md`](FIX-PLAN.md) 末尾「Wave 2 计划」。
> 产品侧状态真源：`docs/BUGS.md`（wave 2 完成后由 fix-platform 同步）。
> 最终交付：`docs/audit/FIX-REPORT.md` 定稿 + 全量门禁 + 独立验证报告。

## wave 1（DONE）
P0 RT-01；P1 安全/权限 RT-02/03/05/07/11/12/13/14/15；可靠性 RT-09/10、RT-06/08 并发修复复核；功能 FP-08/09/11/13/18；UI/UX UX-01/02/03/04/05/06(半)/08/09/10/11/13 + PD-01/02/03/08/09/11/17/18；工程 AR-01/02/03(部分)/16/18；文档真源 BUGS.md/STATUS/README/PLAN/PARITY/known-limitations；打包 linux/mac；headless CLI。详见 FIX-REPORT。

## wave 2（进行中）
- 产品面：PD-05 设置补页、PD-06 Automation 历史/失败/绑定、PD-07 MCP/Skills/AGENTS 管理、PD-10 会话/草稿恢复、PD-12 桌面生命周期（自启/关闭行为/托盘）、PD-04 导入器 CLI/入口、About 版本、context budget 设置、首启引导卡。
- 功能：FP-19 代理、FP-04 goal、FP-07 schedule 投回、FP-16 ask_user 阻塞、FP-15 job 通知、FP-17 skills 多 root、FP-01 workflow parallel、FP-03 subagent 异步、FP-05 workspace changes/diff、FP-14 附件路径语义。
- 验证：FIX-VERIFY-CORE2 / FIX-VERIFY-UI / FIX-VERIFY-PRODUCT；预览金标重生成+视觉回归。
- 提交：wave 2 全量门禁绿后 Lead 统一 commit，并更新 FIX-REPORT 定稿。

## 延期（DEFERRED，理由见 FIX-REPORT §7）
workflow 完整 JS 引擎、PTC Node 运行时、ACP/SDK、三平台签名/公证/自动更新、RT-04 per-session Agent（方案已记录）、AR-04/AR-22 大重构、真机 IME/a11y/DPI 矩阵。
