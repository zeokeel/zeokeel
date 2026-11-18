# v4 闸门结果 · 按 owner 分派（2026-11-18 01:15）

> 命令：`A3-qa/measure.sh .cache/ui-conformance-v4 --baseline .cache/ui-conformance --strict`
> 快照：`.cache/ui-conformance-v4/`（head `e9a0d6b`，118 张，01:07 生成）
> 结果：**PASS 50 · FAIL 1 · WARN 3 · SKIP 0**（`--strict` 退出码 1）
> 机读：`.cache/ui-conformance-qa/ui-conformance-v4.tsv`

## 0. 一句话结论

**只有 1 条硬 FAIL，在 impl-shell**（窄窗顶带尾簇被检查器裁切）；其余 3 条 WARN 里 2 条是信息项、1 条是场景清单变化。token 层（接缝/圆角/字号/ramp/强调色）在 v4 已全部达标——**A3 的三条 P0/P1 结构问题只剩这一条**。

---

## 1. FAIL（1 条）

| # | owner | 位置 | 实测 | 期望 | 证据 |
|---|---|---|---|---|---|
| F-1 | **impl-shell** | `narrow/narrow-session-inspector.png` · 顶带尾簇 `x≈686..700, y=10..40`；检查器接缝 `x=700` | 接缝左侧 40px 最小亮度 **80**（有墨贴到接缝）；裁剪图可见面板切换图标（▯）被接缝**裁掉右半**，紧贴「检查器」标题 | 最小亮度 ≥150（顶部 84px 带内，最后 40px 无墨） | `.cache/ui-conformance-audit/crops/v4-narrow-hdr.png`（4×） |

**改法**：顶带尾部簇（⋯/🔍/面板切换）在 `windowW − SidebarRail(64) − InspectorMinWidth(260)` 不足时收进 `⋯` 溢出菜单（或按 `design.HeaderOverflowMin` 规则），保证簇的右缘距接缝 ≥ `Space2(8)`。
**注意**：这与你已知的两条（窄窗 toast 叠检查器、窄窗 chip 退化）**不是同一条**：这条是 band 尾簇本身被裁，即便 toast/chip 修好它也还在。三条同源（窄窗顶带宽度预算），建议一次改完。

---

## 2. WARN（3 条）

| # | owner | 位置 | 实测 | 判定 |
|---|---|---|---|---|
| W-1 | **impl-shell**（场景） | `.cache/ui-conformance-v4/manifest.txt` | `p2-artifact-history.png` **消失**（v1 有、v4 无） | 非视觉缺陷，但涉及「产物历史」入口：若是有意合并/改名，请确认入口仍在（AGENTS §17）；否则恢复场景 |
| W-2 | impl-settings（信息项） | `p2-model-settings.png` | 列表文本行距中位 **24px / 5 段** | 信息项，无需修；行归属看 W-4 |
| W-3 | impl-settings（信息项） | `p2-automation.png` | 列表文本行距中位 **26px / 7 段** | 同上 |

> W-2/W-3 是脚本的"文本行距采样"，不是行高判据（多行列表的"段"是文本行）。**不阻塞**。

---

## 3. 抽查补充（脚本看不到、但我已核的 2 条，请一并派掉）

| # | owner | 位置 | 实测 | 建议 |
|---|---|---|---|---|
| S-1 | **impl-session** | `p1-chat.png` 发送按钮 `x≈1320..1352, y≈816..848` | 圆底 `#D5D8DC`、箭头 `#F0F1F4` → **约 1.15:1**，禁用态几乎只剩一个浅圆 | 箭头改 `LabelTertiary`(#5A6371) on `BgStrong`(#F2F4F7) ≈ 5.9:1；或保留浅圆但加 1px `BorderL2`（≥3:1）。空草稿时用户要能看见"发送在哪" |
| S-2 | **impl-tools** | `p2-approval-diff.png` diff 块 `x=31..930, y=185..345` | 整块 `#EFF9F2`（success 6% 洗底），**所有行同色**，看不出增删行的行级区分（此图 +9/−0 全为新增，无法证明混合 diff 的行为） | 请确认：块底回 `BgBase` 白，仅 `+` 行 success 6%、`−` 行 danger 6%；补一张含 context 行的审批 diff 快照供复核 |

---

## 4. 分派汇总（按 owner）

| owner | 待办 | 计数 |
|---|---|---|
| **impl-shell** | F-1 窄窗顶带尾簇被裁（**唯一 FAIL**）；W-1 确认 `p2-artifact-history` 场景；[已知] toast 叠检查器、chip 退化 | 1 FAIL + 1 确认 + 2 已知 |
| **impl-session** | S-1 禁用发送对比 1.15:1 | 1 |
| **impl-tools** | S-2 diff 行级染色不可证（补图/确认） | 1 |
| **impl-settings** | 无待修；W-2/W-3 为信息项 | 0 |
| **impl-panels** | 无待修 | 0 |

---

## 5. 已验收的修复（抽查确认，请勿回退）

| 项 | A3 编号 | v4 实测 |
|---|---|---|
| 侧栏接缝单线 | VD-03 | `x=280` 单条 `#E9EAEA`（暗 `#292930`），宽 1 |
| 检查器/右栏接缝 | VD-04 | `x=1189` 单条 `#E9EAEA` |
| 设置导航接缝 | VD-04 | `x=535`（1024 下 `x=291`）单条 `#E2E3E5` = L2 over `#F7F8FA` |
| 顶带 | — | 高 **85**（48 + 36 + 边框），底边 `#E2E3E5` |
| 圆角阶梯 | VD-25/26 | composer **r≈16**、卡片 **r≈12**、palette **r≈10**（建议对齐 12，非阻塞） |
| 导航图标去蓝 | VD-05 | 图标墨色 `#4B5261`（=`LabelSecondary`），与折叠 rail 一致 |
| 选中态 | 规则 11 | `#E2E8F9`（royal 9%），无满蓝 |
| toast 不挡正文 | VD-02 | 移到右下（`p1-chat` 右下角） |
| 审批卡 | VD-08 | 中性卡 + 橙色 2px 左 rail + `BgStrong` 内嵌块；无大色块 |
| 轨迹页 | VD-17 | 改为列表行 + 1px 分隔线，无「按钮脊柱」 |
| 右栏文件头 | VD-16 | `README.md` 只出现 1 次（tab），文件头改「工作区根目录」路径行 |
| 列表分隔 | VD-15 | 模型列表行间 1px 分隔 |
| 状态条 | VD-10 | 改为 `42K/128K` 计量条 + 「详情」按钮，不再是 11px 六指标句 |
| 强调色预算 | 规则 10 | 抽查 20 屏 accent 填充块全部 ≤2，无超预算 |
| 暗色 ramp | — | nav `#0B0B10` < page `#111118`（4 屏） |

---

## 6. 本次脚本变更（CONFIG + 探针）

| 变更 | 原因 |
|---|---|
| `BAND_H 72 → 84` | 顶带改 48 + tabs 36 |
| 窄窗 clear 探针 `band_h 42 → 84` | 同上，整条带内不留墨 |
| 接缝期望色改为「L2 合成在左/右邻面任一即匹配」 | 设置导航缝由 nav 侧绘制 → `#E2E3E5`（L2 over `#F7F8FA`），旧口径误报 Δ7 |
| 探针按 v4 重定位 | 设置面 `x=316`、设置导航缝 `x=535/291`、内容块采样带上移避开 toast、圆角探针改 composer/能力卡/palette/设置面 |
| 圆角探针去掉 approval/landing 两张卡 | 角部被 2px rail / 阴影遮挡，测量不可靠 → 改人工（该组件半径由能力卡 r12 + composer r16 已证明） |

**已知限制不变**：hover/pressed/focus 不在静态图里；模糊语义项仍需人判；探针坐标与画布绑定（换尺寸需改 `PROBES`）。

---

## 7. 下一步

等你说「收尾」，我在 v4 上做完整人工评审：逐张 `read_image`（重点 `narrow/*`、`*-dark`、`p1-chat`、`p1-approval`、`p2-context`、`p2-automation`、`p2-file-preview`、`p2-ask`），把 A3-QA-CHECKLIST 的 40 项逐条打勾 → 出 `A3-qa/verdict.md`。
若你要先派修，**只需按 §4 派 4 条（F-1 最优先）**，其余可等完整评审。