# ZeoKeel 质量体系（防止“永远修不完”）

> **2026-11-05 更新**：DSH 像素对齐已退役（[ADR 0001](decisions/0001-retire-dsh-pixel-parity.md)）；像素预算/DSH 基线门禁从质量体系中移除，视觉改按 ZeoKeel 自有设计系统验收。本文件为现行质量口径。

## 为什么之前修不完（像素对齐时代的历史教训）

之前的循环标准是“验证代理找不到任何问题”。这个标准**不可收敛**：代理总能提出更细的 UI 差异（字体 1px、hover 状态、DSH 没实现的特性），于是每轮都有“新问题”，永远达不到 clean。

正确做法：**用可执行规格 + CI 门禁定义完成度**，而不是让代理主观找问题。

## 定义“完成”

1. 功能规格：`docs/FUNCTIONAL-PARITY.md` 的能力矩阵，每项有**可执行验收**（E2E 测试/命令），未实现项显式标记 `not planned`。
2. 回归规格：`scripts/ci.sh` 必须全绿；任何改动不得降低现有测试覆盖率与门禁标准。
3. 已知缺陷：只允许存在于 `docs/BUGS.md`（带严重级、复现命令、回归测试链接）；不允许口头“已知”。
4. 视觉：以自有设计系统的组件样本/关键屏**自身回归** + 设计令牌断言为门禁，不再追“代理觉得像不像”，也不再与 DSH 截图做像素比对（2026-11-05 退役）。

## 流程（每个改动）

1. **先写失败测试**（TDD）：功能测试/回归测试，确认当前失败。
2. 最小实现，单一职责，尽量单文件单一写入者。
3. `sh scripts/ci.sh` 全绿。
4. 独立 review（人或代理）只检查：契约、边界、并发、错误路径、回归风险。
5. 合并；测试保留；更新 `docs/BUGS.md` 状态。
6. 禁止“为了过验证而改断言”，除非行为规格本身变更并记录。

## CI 门禁（scripts/ci.sh）

- gofmt / go vet / go build / go test（-count=1）
- 零 cgo 四目标交叉编译
- 生产工具注册表可调用
- 无头渲染 smoke + 交互用例（DSH 像素基线/深色窄屏基线已退役，2026-11-05）
- 覆盖率摘要；race 检测（阻断级；无 cgo 工具链时跳过）

## 测试金字塔（当前已有基础上补）

| 层 | 内容 | 现状 |
|---|---|---|
| 单元 | parser/markdown/cron/path/session 状态机 | 大量已有 |
| 组件 | shell ui.NewTester、provider httptest、runtime fake model | 大量已有 |
| 集成 | store+session+controller、app services、MCP stdio | 已有 |
| E2E | 真实模型对话/工具/识图（-tags e2e；需 key） | 已有脚本 |
| 属性/模糊 | markdown、cron、JSON args、路径穿越 | **待补** |
| 并发 | -race + 并发压测（terminal/jobs/team/session） | 部分已有 |

## Bug 处理纪律

- 每个 bug：复现测试 → 根因 → 修复 → 回归测试；同类问题批量排查（修类，不修例）。
- 严重级：Critical（数据丢失/崩溃/安全问题）、Major（功能不可用/错误结果）、Minor（视觉/文案）。
- Critical/Major 为 0 才可发布；Minor 可在 BUGS.md 排期。

## 现实目标

“无 bug”不可能；可达成的是：**规格内无已知 Critical/Major、CI 全绿、回归测试覆盖所有修复、残余风险有记录**。
