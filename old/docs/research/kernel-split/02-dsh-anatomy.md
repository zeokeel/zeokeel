# 02 · DSH 装配方式解剖：305 个包、分层 patch 与插件三要素

> 日期：2026-11-06 · 研究线 B（对照基准，不涉及 /data/mygo 产品代码修改）
> 对象：本机安装产物 `@deepseek-ai/dsh` **0.2.1-rc.2**（CLI 包，`package.json` 实测）及其运行中的 `web` profile。
> 方法：**只读**遍历安装目录 + 解析 YAML + 读随包 `src/`（vendor 包源码随包发布）+ 交叉验证包 README。全部计数命令列于 §6，全部配置结论带 `文件:行号`。
> 证据等级：★实测（可复现命令）＞ ☆代码/文档引用（文件+行号）＞ 判断（显式标注）。
> 本文不使用 `--dump-config`（会重写运行中 profile 的 `cordis.yml`，虽然内容相同，仍选择零写入路径），组合结果由本地复刻 `applyEntryPatches` 得到，语义与 `cordis-plugin-include/src/index.ts:57-127` 逐行对齐。

路径缩写（后文所有 `文件:行` 均相对以下根）：

| 缩写 | 绝对路径 |
| --- | --- |
| `$DSH` | `/usr/local/lib/node_modules/@deepseek-ai/dsh` |
| `$PKGS` | `$DSH/node_modules/@deepseek-ai` |
| `$PROFILE` | `/root/.dsh/profiles/web` |

---

## 0. 结论速览

1. **305 个包不是"305 个模块"**：18 个是组合包（bundle，只含 `cordis.patch.yml` 装配清单），78 个是"双面插件"（host 半边 + `dsh.client` 浏览器半边），其余是 host 侧服务/能力/工具/命令/基础库。真正被一个 profile 装配起来的行，在 `web` 上是 **200 个顶层行 + 8 个组内子行 = 208 行，引用 198 个唯一包 + 1 个 `cordis:group` 内建**（§3.9，实测）。
2. **"DSH 方式"的实质是两层分离**：① 进程级 Loader 条目树 = 由「bundle patch 层 + 用户 patch 层 + home patch 层 + `--patch` 覆盖层」合成的 YAML 行列表；② 会话级 Agent preset = 挂在 preset 行 `config.plugins` 里的子树，由 `dsh-agent-preset`（Group 插件）按 session 安装。两层都是"行（row）"，都走同一套插件契约（§3.10、§4.4）。
3. **行（row）= `{id, name, config, disabled, inject, group, isolate, insert}`**，`id` 是稳定别名、可与包名解耦；patch 只做两件事：`insert` 追加、按 `id` **整字段覆盖**（`config` 整块替换不合并，`disabled` 同理）；`!!js` 是运行时用 `new Function('ctx','expr', with(ctx){eval(expr)})` 求值的表达式（§3.4、§4.7）。
4. **插件三要素实测成立**：`apply(ctx,config)` / `static inject` / `static Config`（Schemastery）在 host 工具包里是三个导出（`dsh-tool-bash/lib/index.js:223-233,340`），在服务包里是 `class extends Service` + `static inject` + `static Config` + `super(ctx,"goals")`（`dsh-goal/lib/index.js:493,587-592`）；1.0.6 的 Loader 允许省略 `apply`，用 `name`/`inject`/`Config`/`export default` 亦可。
5. **对 Go 目标环境的硬判断（详见 §5）**：**可照搬**的是"一模块一包 + 描述符/服务契约 + 声明式 entry 树 + 服务可用性驱动激活 + 能力接缝/多 provider + 多 profile 复用组合"；**可近似**的是"分层 patch + 稳定 id + 类型化远程契约（Typert）"；**不可照搬**的是"动态 ESM import + `!!js` 运行时求值 + 文件监听 HMR + 浏览器懒加载双运行时 + npm 运行时安装"。DSH 的模块化收益里，约一半来自"分层组合纪律"，与 JS 运行时无关；另一半（热更、动态加载、浏览器插件市场）属于其运行时选型，Go 单二进制路线**结构上做不到**，不应在目标里假装保留。
6. 本报告不弱化硬约束：`zeokeel-kernel` 的 `module/` 层已明确"无动态 ESM/HMR、配置变更=重启进程"（kernel README:191-192、docs/COMPILE-TIME-REGISTRY.md:331-343），本文 §5 的判断以此为前提，而不是把 DSH 的能力清单原样搬到 Go。

---

## 1. 观测对象与量级

| 指标 | 实测值 | 复现命令（§6-A1..A9） |
| --- | --- | --- |
| CLI 包版本 | `@deepseek-ai/dsh` 0.2.1-rc.2，bin=`lib/bin.js` | A1 |
| `@deepseek-ai/*` 包目录数 | **305**（顶层、无嵌套重复，去重后仍 305） | A2 |
| 其中 0.2.1-rc.2 版本 | 294；另有 vendor 系 11 个（cordis 4.0.5、loader 1.0.6、include 1.0.10、group 1.0.5、timer 1.1.7、cosmokit 1.8.6、schemastery 3.18.5、node-addon-system 0.1.2×2、libreoffice-kit 0.1.5×2） | A3 |
| 安装体积 | `node_modules` 852MB；`@deepseek-ai` 566MB（其中 `libreoffice-kit-wasm` 146MB、`dsh-experimental-inspector` 26MB） | A4 |
| 一方 JS 代码量 | 936 个非 client 脚本 **881,610 行 / 33.4MB**；97 个 client 脚本 **379,545 行 / 25.8MB**；2206 个 `.d.ts` 116,772 行；599 个 README 80,202 行 | A5 |
| 源码域 | 58 个（`packages/*` 49 + native/system 1 + apps/web 1 + vendor 7；按 `repository.directory` 的 `a/b` 前缀统计） | A6 |
| 声明 `dsh.client` 的包 | **78** | A7 |
| 声明 `dsh.bundle` 的包 | **18** | A8 |
| 声明 `peerDependencies["@deepseek-ai/cordis"]` 的包 | **297 / 305**（例外 8 个：cordis 本身、cosmokit、schemastery、dsh-web-frontend、libreoffice-kit(-wasm)、node-addon-system(-linux-x64)） | A9 |

**关键事实**：安装产物是"构建后 JS + 类型声明 + README 双语 + locale"，`lib/index.js` 保留 region 注释与 JSDoc、不是压缩文件（抽查 `dsh-goal/lib/index.js:1-30`、`dsh-tool-bash/lib/index.js:1-40`），因此本文可按行号引用行为而不只是类型声明。

---

## 2. 305 个包的分层解剖

### 2.1 先按"发布形态"分四类（互斥，合计 305）

判定依据全部来自 `package.json`：`dsh.bundle`（是否组合包）、`dsh.client`（是否双面）、`repository.directory`（是否 vendor）。

| 形态 | 数量 | 判据 | 代表包 | 说明 |
| --- | --- | --- | --- | --- |
| vendor / 内核 | 7 | `src` 以 `vendor/` 开头 | `@deepseek-ai/cordis`、`cordis-plugin-{loader,include,group,timer}`、`cosmokit`、`schemastery` | 上游 Cordis 语义的 vendored 副本，**不含 DSH 产品逻辑** |
| bundle / 组合包 | 18 | `package.json` 有 `dsh.bundle.patch` | `dsh-base`、`dsh-web-app`、`dsh-headless`、`dsh-acp-app`、`dsh-sdk-app`、`dsh-sdk-minimal`、12 个 `dsh-experimental-*-bundle/profile` | 只有 patch 清单（+可选 glue 入口），不是"功能插件" |
| 双面插件 | 78 | `package.json` 有 `dsh.client`（`platform:"web"`） | `dsh-client-ui-*`（54）、`dsh-api-*`（10）、`dsh-client-*` 内核 8、`dsh-experimental-inspector` 等 | host 行 + `./client` 浏览器 bundle；node 半与 browser 半同包 |
| 纯 host 侧 | 202 | 其余 | `dsh-tool-*`、`dsh-session*`、`dsh-llm*`、`dsh-fs*`、`dsh-host-*` 等 | 只在 Node 进程内运行 |

> 交叉验证：`grep -l '"client"'` 得到 78、`grep -l '"bundle"'` 得到 94（字符串误配），必须用 `jq 'select(.dsh.client)'`；**计数命令要看字段而不是 grep 字符串**（§6-A7/A8）。

### 2.2 再按"运行时角色"分 13 类（互斥，合计 305）

判定顺序 = 表内顺序，先命中先归类；"core/capability/util" 用 `repository.directory` 的域白名单，另有两个显式例外（脚注）。这条分类是本文后续讨论"能不能照搬"的基本单位。

| # | 角色 | 数量 | 代表包 | 判据（可复现） |
| --- | --- | --- | --- | --- |
| 1 | vendor（Cordis 内核/loader 生态） | **7** | `cordis`、`cordis-plugin-loader` | `src` 前缀 `vendor/` |
| 2 | bundle / profile 组合包 | **18** | `dsh-base`、`dsh-web-app` | `dsh.bundle` 存在 |
| 3 | typert 类型面 | **3** | `dsh-typert-registry`、`dsh-typert-loader`、`dsh-typert-protocol` | 名称 `dsh-typert-*` |
| 4 | client-ui（浏览器 UI） | **57** | `dsh-client-ui-chat`、`dsh-client-ui-sidebar` | 名称 `dsh-client-ui-*` 或 `dsh-experimental-client-ui-*` |
| 5 | client-core（浏览器内核/传输） | **8** | `dsh-client-modules`、`dsh-client-connection`、`dsh-client-hmr` | 名称 `dsh-client-*`（未被 4 收走） |
| 6 | api（Remote 端点，双面） | **10** | `dsh-api-gateway`、`dsh-api-session-controller` | 名称 `dsh-api-*` |
| 7 | host（宿主端点/服务面） | **9** | `dsh-host-webserver`、`dsh-host-frontend-static` | 名称 `dsh-host-*` |
| 8 | tool（模型面工具） | **25** | `dsh-tool-bash`、`dsh-tool-fs`、`dsh-tool-subagent` | 名称 `dsh-tool-*` |
| 9 | command（slash 命令） | **3** | `dsh-command-goal`、`dsh-command-compact` | 名称 `dsh-command-*` |
| 10 | experimental（非 bundle 实验插件） | **9** | `dsh-experimental-agent-team`、`dsh-experimental-speech-to-text` | 名称 `dsh-experimental-*`（未被 2/4/8 收走） |
| 11 | core 服务（引擎/领域） | **93** | `dsh-agent`、`dsh-session`、`dsh-tools`、`dsh-llm`、`dsh-agent-preset` | 域 ∈ {core,session,session-query,context,llm,preset,interaction,storage,spill,compaction,goal,plan,settings,feedback,boot,guard,identity,telemetry,workspace,extensions,deliverables,acp,sdk,ptc-runtime,workflow,subagent}¹ |
| 12 | capability provider（可替换接缝） | **45** | `dsh-fs-local`、`dsh-bash-sandbox`、`dsh-llm-deepseek`、`dsh-storage-json` | 域 ∈ {fs,shell,sandbox,subprocess,web,mcp,credentials,attachment,jobs,schedule,skill,terminal,document,wasm,system,entry}² |
| 13 | util 基础库 | **18** | `dsh-atomic-write`、`dsh-package-manifest`、`dsh-home-paths` | 域 = `util` |
| | **合计** | **305** | | 与 A2 一致 |

¹ 例外：`dsh-session-title-all-prompts-llm`（实验域）计入 core；² 例外：`dsh-skill-badge`（实验域）计入 capability。其余全部由域白名单决定；脚本见 §6-A10，输出 13 行计数之和必须 = 305，否则说明安装产物变了。

**这张表本身就是对"DSH 怎么拆模块"的第一个回答**：模块不是按"前端功能页"拆的，而是按**契约位置**拆：
- `core` 定义**服务接口与领域模型**（agent/session/tools/llm/preset…）；
- `capability` 是**同一服务接口的多个实现**（本地 fs vs 沙箱 fs、bash vs pwsh、deepseek vs pi-ai 模型适配器、json vs sqlite 存储）；
- `tool` 是**模型可见的薄适配层**（每个 tool 包平均仅几十 KB 的 JS，依赖 `ctx.tools` 注册）；
- `client-ui` 是**浏览器贡献点**（slot 注册），`api`/`host` 是**传输与端点**，`typert` 是**自动生成的 host↔client 类型面**。

### 2.3 按源码域看（58 个域，验证"单职责"）

| 源码域（`packages/<domain>`） | 包数 | 主导角色 | 源码域 | 包数 | 主导角色 |
| --- | --- | --- | --- | --- | --- |
| client | 61 | client-ui/client-core | fs | 7 | capability+tool |
| experimental | 29 | bundle(12)+插件 | context | 7 | core（提示词上下文） |
| session | 21 | core（会话日志/投影/标题） | boot | 6 | core（loader/hmr/plugin-manager） |
| util | 18 | util | bundle | 6 | bundle（base/web/headless/acp/sdk） |
| api | 10 | api（双面） | credentials | 5 | capability |
| shell | 10 | capability+tool | compaction | 5 | core |
| llm | 9 | core+capability | interaction | 5 | core（命令/审批/提问） |
| host | 9 | host | skill | 5 | capability+tool |
| core | 8 | core（agent/tools/session/scope…） | subagent | 5 | core（注册表+provider） |
| 其余 36 个域 | 小计 62（web5、extensions/goal/sandbox 各 4，另 32 个域各 1–3） | web/mcp/storage/spill/sandbox/… | | | |

合计 305。**单包职责极窄**：例如 `dsh-tool-todo` 仅一个工具（`dsh-base/cordis.patch.yml:437-441` 一行装配），`dsh-session-format-v0-to-v1` 只是会话日志格式迁移（`dsh.sessionFormatMigration`，4 个包声明该字段）。这是"一个功能一个包"的直接后果：305 个包 / 一个 web 组合 198 个包，粒度可以细到"一个迁移函数"。

### 2.4 双面板的标记系统（host / client / typert 三面）

一个"双面插件"的 `package.json` 同时给出三个面的元数据：

```jsonc
// $PKGS/dsh-client-ui-goal/package.json:29-41
"dsh": { "client": {
  "inject": ["@deepseek-ai/dsh-api-remotes", "...dsh-client-ui-renderer", "...dsh-client-ui-session"],
  "platform": "web"
}}
```

```jsonc
// $PKGS/dsh-api-gateway/package.json:39-46
"dsh": { "client": { "inject": [...], "platform": "web", "immediately": true } }
```

- `dsh.client.platform` 目前只有 `web`；`immediately:true` 表示启动即物化（不是首次使用时），`inject` 是**浏览器侧服务依赖**（与 host 侧 `inject` 不是同一张图）。
- `exports` 约定：`./client`（浏览器 bundle，75 个包有 `lib/client.js`）、`./typert`（host 面生成产物，29 个包）、`./remote`（浏览器面生成产物，29 个包）、`./types`（跨面包共享的纯类型出口）。
- `dsh.client.external` 列出"基座模块之外的请求"（如 react 之外的内部包），由组合阶段校验提供方与环（`dsh-client-modules/README.zh.md:34`、`lib/index.js:417-444`）。
- 判据验证：`exports` 含 `./client` 的包 89 个 > `dsh.client` 78 个——多出的 11 个是"只被程序化组合使用"的 client 面（如 `dsh-typert-registry` 有 node/browser 双 bundle），不参与浏览器 roster。

### 2.5 单个包的标准解剖（"模块长什么样"）

对 305 个包做文件存在性统计（§6-A11）：

| 文件/目录 | 包数 | 含义 |
| --- | --- | --- |
| `lib/index.js` | 301 | host 入口（4 个例外：`dsh-web-frontend`、`libreoffice-kit-wasm`、`node-addon-system-linux-x64`、`schemastery` 是纯资源/原生） |
| `lib/types/**/*.d.ts` | 301 | 类型出口（TS 严格声明，含 `apply/inject/Config` 签名） |
| `README.md` + `README.zh.md` + `README.i18n.yaml` | 296/295 | 每包双语文档 + 元数据头（`kind: package-reference`），**文档与代码同包发布** |
| `locale/*.json` | 214 | 模型/UI 文案（i18n 是插件的一部分，不是中心化词条表） |
| `LICENSE` | 303 | 独立许可 |
| `lib/client.js` | 75 | 浏览器 bundle（`window.__ModuleLoader__.load({id,factory})` 懒 CJS 工厂） |
| `lib/typert.host.js` + `lib/typert.remote-client.js` | 29 + 29 | **生成的** host 反射/浏览器远程客户端（文件头 `/* Generated by @deepseek-ai/dsh-typert-generator ... do not edit. */`，`dsh-goal/lib/typert.host.js:1`） |
| `src/` | 8 | 只有 vendor 包随包发布源码（对本文是"可读上游实现"的福利） |

典型小包 = `package.json` + `lib/index.js`（1 个插件导出）+ `lib/types/*.d.ts` + README×3 + locale。**模块边界由 package 边界强制**：跨包只能走 `exports` 声明的入口；源码里大量 `/* @module @deepseek-ai/<pkg> */` 头部注释（如 `dsh-tool-bash/lib/index.js:1-8`）把文件与包身份绑定。

### 2.6 规模画像（给"拆分收益"取参照系）

- 一个 web profile 装配 208 行、引用 198 个包 = **平均每包 1.05 行**；这些事实集中在 13 层 patch 文件（1730 行 YAML，含用户层 35 行）里，代码中没有 `if (featureEnabled)` 式分叉——**配置量 ≈ 代码量的 0.2%** 是"声明式组合"的核心数字。
- 305 个包里 **78 个有浏览器半边**（25.6%）；75 个包各带 `lib/client.js`，client bundle 合计 12MB（含分块脚本 25.8MB）；对 Go 单二进制 + 原生 UI 目标而言，这 78 个包的角色需要重新映射（§5-P7）。

---

## 3. 装配链路：profile → bundles → patch 层 → 行

### 3.1 profile 目录只有四个文件

`$PROFILE` 实测（`ls -la $PROFILE`）：

| 文件 | 行数 | 作用 | 关键内容 |
| --- | --- | --- | --- |
| `package.json` | 20 | **profile manifest**：`dsh.profile.bundles` 有序列表 | 9 个 bundle（L7-17：base、web-app、7 个 experimental） |
| `cordis.yml` | 4 | **空根条目列表** `[]`（L4） | 注释明确："树由 patch 组装；改 `cordis.patch.yml` 别改这里" |
| `cordis.patch.yml` | 35 | **用户 patch 层** | 5 行：3 个 UI 配置 + `agent-default-model` + `permission` 预设（L4-35） |
| `pnpm-workspace.yaml` | 5 | profile 是独立 pnpm 项目 | `nodeLinker: hoisted`、`autoInstallPeers: false` |

注意两点：
1. **根配置永远是空的**。`dsh` 每次启动都重写 `cordis.yml`（`$DSH/lib/profile-boot-C14i-8IN.js:216-222`，注释解释了原因：Loader 会把组合后的树写回该文件，若不重写，下次启动会重复插入每个 bundle 行）。空根 + patch 是"唯一真源"的设计。
2. **profile 的 `dependencies` 可以为空**（`$PROFILE/package.json:4`），bundle 包从安装产物解析（两级锚点：先 installation，再 profile 目录；`dsh-app-boot/lib/types/profile.d.ts` 模块注释）。用户装第三方 bundle 时才走 pnpm 往 profile 里装。

### 3.2 bundle 声明：`dsh.bundle.patch` 是字符串或有序文件列表

```jsonc
// $PKGS/dsh-base/package.json:31-34
"dsh": { "bundle": { "patch": "./cordis.patch.yml" } }
```

```jsonc
// $PKGS/dsh-web-app/package.json:46-54
"dsh": { "bundle": { "patch": [
  "./cordis.patch.yml", "./presets/standard.patch.yml", "./presets/ptc.patch.yml",
  "./presets/minimal.patch.yml", "./presets/cordis.patch.yml" ] } }
```

`bundlePatchFiles()` 校验声明形状、`bundlePatchPaths()` 解析为绝对路径、按序拼接（`dsh-app-boot/README.zh.md`「Profile」节；实现 `lib/index.js:1015-1055`）。**一个 bundle 可以贡献多个 patch 文件**，web-app 用它把"主表面"和"4 个 agent preset 声明"拆开，便于 profile 只覆盖 preset 行。

本 web profile 的 13 个 patch 文件（12 个 bundle patch + 1 个用户 patch）实测：

| 层（`dsh.profile.bundles` 顺序） | patch 文件数 | YAML 行数 | 顶层条目 | 新增行 | 其中 `disabled:true` | 说明 |
| --- | --- | --- | --- | --- | --- | --- |
| 1. `dsh-base` | 1 | 529 | 1 | **96** | 1 | 共享核心：一个 insert 铺满整个 host 面 |
| 2. `dsh-web-app` | 6 | 589+128+134+78+136=1065 | 32 | **94**（主体 90 + 4 个 preset 声明） | 23 | 浏览器表面：3 处配置重述 + 2 个 insert + 23 个禁用 + 4 个 preset |
| 3. `experimental-agent-team-profile` | 1 | 32 | 5 | 3 | 4 | 先禁用 4 个内置 subagent 工具行，再插 Team 工具 |
| 4. `experimental-auto-review` | 1 | 3 | 1 | 1 | 0 | 单行插入 |
| 5. `experimental-tool-worktree` | 1 | 5 | 1 | 2 | 0 | 插 2 行 |
| 6. `experimental-session-search` | 1 | 14 | 1 | 1（组，内含 2 子行） | 0 | 独立 isolate 的组 |
| 7. `experimental-terminal-bundle` | 1 | 19 | 1 | 1（组，内含 4 子行） | 0 | 独立 isolate 的组，含平台 `!!js` |
| 8. `experimental-session-titles-bundle` | 1 | 12 | 2 | 1 | 1 | 先禁用 base 的标题行，再插替代实现 |
| 9. `experimental-ralph-bundle` | 1 | 16 | 1 | 1（组，内含 2 子行） | 0 | 独立 isolate 的组 |
| 用户层 `$PROFILE/cordis.patch.yml` | 1 | 35 | 5 | 0 | 0 | 只做 `config` 覆盖（3 UI + 模型 + 权限预设） |
| **合计** | **13** | **1730** | **50** | **200** | **24（生效值）** | 见 §3.9 最终树 |

### 3.3 合成顺序（七步，代码级）

`readProfilePatches()`（`dsh-app-boot/lib/index.js:1127-1145`）逐行给出顺序，`mountRootInclude()`（同文件 4125-4145）把它们一次性交给根 `cordis:include`：

```
空根 cordis.yml=[]
  └─ + bundle 层（按 package.json dsh.profile.bundles 顺序，每个 bundle 的多个 patch 文件按声明顺序）
  └─ + profile 用户层  cordis.patch.yml
  └─ + home 用户层     $DSH_HOME/cordis.patch.yml（本机不存在；它优先级更高，"机器局部偏好"）
  └─ + --patch overlay（argv 顺序，可重复）
  └─ + 遥测开关 patch（DSH_TELEMETRY_DISABLED 非空 ⇒ 把 session-telemetry-otel 行置 disabled:true）
= 一个 patches 数组 → Include(builtin) 挂载时 applyEntryPatches([], patches) → 最终 entry 列表
```

证据链：
- 注释文档：`$DSH/lib/profile-boot-C14i-8IN.js:76-79`（"bundle layers in `dsh.profile.bundles` order, the profile's own `cordis.patch.yml`, `--patch` overlays, the telemetry switch"）、`224-235`（`composeProfile` 同义）。
- 实现：`dsh-app-boot/lib/index.js:1118-1124`（bundle patches 先）、`1127-1145`（+ 用户层 + home 层 + overlays + 遥测）。
- 挂载：`lib/index.js:4125-4145` `ctx.loader.builtins.include = Include`、`builtins.group = Group`，根 include 的 `config: { path: cordis.yml, patches }`。
- 引导：`lib/index.js:4507-4550` `boot()` = `new Context()` → `ctx.plugin(Loader)` → `prepare(hostCtx)` → `mountRootInclude(...)` → `loader.await()` → `auditStartupEntries(ctx)`。
- CLI 侧：`$DSH/lib/profile-boot-C14i-8IN.js:255+` `runProfile()` 里 `boot(...)` 前注入 `profileContext`、`PluginPackages`（runtime resolution）、`cmdline`。

**"layer 顺序"决定覆盖关系**：同一 `id` 的行，后层整字段覆盖前层；`insert` 的行在插入那一刻被索引，因此**同一层或后层可以继续 patch 刚插入的行**（`cordis-plugin-include/src/index.ts:79-93`，注释明确"a layer must be able to configure or disable a row an earlier layer inserted"）。

### 3.4 行（row）结构与 patch 语义（本文最重要的一张表）

`EntryOptions` 定义于 `cordis-plugin-loader/src/config/entry.ts:10-23`；`PatchOptions` 定义于 `cordis-plugin-include/src/index.ts:130-140`。

| 字段 | 类型 | 语义 | 证据 |
| --- | --- | --- | --- |
| `id` | string | 树内**稳定标识**；也是 patch 的唯一寻址键。同层/跨层可重复（后写赢） | entry.ts:11-12 |
| `name` | string | 模块说明符：包名、`@scope/pkg/subpath`、相对/绝对路径、file URL、`cordis:group` / `cordis:include` 内建 | tree.ts:111-118（`cordis:` 前缀 → `loader.builtins`）；全部 18 个 bundle 共 6 个 subpath 名，web 组合内生效 5 个（§3.5） |
| `config` | any | 传给插件 `Config`（Schemastery）并在 `apply` 前校验/填充默认值 | loader「config」生命周期；`dsh-tool-bash/lib/index.js:340` `apply(ctx, config = {})` |
| `disabled` | bool \| `!!js` | **覆盖**该行启用状态；`!!js` 在激活前用 Loader context 求值；**父行 disabled 会传播给子行** | entry.ts:66-79 `get disabled()`、84-93 `disabledOf()` |
| `inject` | string[] \| map | **行级覆盖**插件级 inject：附加/替换该行所需服务（用于按服务可用性延迟激活） | loader README `Entry Options`；web-app 的 `webserver`/`web-runtime`/`connection` 用它等 `webStartup`（`$PKGS/dsh-web-app/cordis.patch.yml:179-181,197-199,234-236`） |
| `group` | bool | 该行是**组**，其 `config` 是子行列表；组本身不加载插件 | entry.ts:17-18；`cordis:group` 内建（group.ts:75-77） |
| `isolate` | map | 组内**服务命名空间隔离**（如 `isolate: { terminals: true }`），同名服务互不冲突 | `$PKGS/dsh-experimental-terminal-bundle/cordis.patch.yml:5-8` |
| `insert` | EntryOptions[] | patch 专用：**追加**行。无 `id` ⇒ 追加到根；有 `id` ⇒ 追加到该组行的 `config` 子列表；目标不存在/不是组 ⇒ warn 并跳过 | include/src/index.ts:79-93 |
| 其余键 | any | 覆盖行的顶层字段（`config` 是其中之一） | include/src/index.ts:120-123 |

**覆盖语义的三条精确规则**（include/src/index.ts:105-127，逐行复刻即可验证）：
1. 非 `insert` patch 必须有 `id`；找不到目标 ⇒ `warn('patch: entry %C not found')` 并**跳过**（不报错，不创建）；
2. 若 patch 带 `name` 且与目标 `name` 不同 ⇒ `warn('name mismatch ...')` 并**跳过**（防止 id 拼错时误改别的行）；用户 patch 里写 `name` 就是为了这个防呆（`$PROFILE/cordis.patch.yml:4-5` 等）；
3. 覆盖 = `for (const [key,value] of overrides) target[key] = value`——**逐顶层字段整体替换**：`config` 整块替换、`disabled` 整体替换，不存在深合并。这就是 base/web-app header 反复强调"a patch replaces the targeted row's whole `config`"的原因（`$PKGS/dsh-base/cordis.patch.yml:6-10`、`$PKGS/dsh-web-app/cordis.patch.yml:5-6`）。

**由此产生一条可验证的工程纪律**：patch 层里**不要**只写"想改的字段"，必须**重述该行完整 config**（否则被替换后丢默认值）；反过来，bundle 作者把"按 mode 变化的值"全部下沉到 mode bundle，base 只保留 neutral 默认（base header:6-10）。一个真实例子：
- base 的 `session-query-sqlite` 给 `openAt: never`（`dsh-base/cordis.patch.yml:157-161`）；
- web-app 重述整块 `{path: ':memory:', openAt: never}`（`dsh-web-app/cordis.patch.yml:27-30`）；
- 若某部署要开全文搜索，在后层写 `config: {path: '<durable>', openAt: first-search}`——**必须重述 path**。

### 3.5 `id` 命名：稳定别名，与包名解耦

- `id` 是**人类可读的稳定名**（kebab-case，如 `ui-conversation`、`agent-default-model`、`tool-bash`），不等于包名：`ui-permission` → `@deepseek-ai/dsh-client-ui-permission-presets`（`dsh-web-app/cordis.patch.yml:418-419`）、`approval` → `dsh-user-approval`、`permission` → `dsh-permission-presets`。好处：包可以改名/换实现，只要 id 不变，用户 patch 与 HMR 寻址不受影响。
- **同一包可被多行挂载**（不同 id/config），web 组合实测 6 组：`dsh-agent-preset`×4（standard/ptc/minimal/cordis 四个 preset 声明）、`dsh-tool-subagent`×2（`tool-subagent` spawn / `tool-subagent-fork` fork）、`dsh-session-query-sqlite`×2（base 行 + 实验组内）、`dsh-terminal-bash`×2（组内 bash/pwsh 两种 `shellDialect`）、`dsh-workflow-ptc`×2、`cordis:group`×3。**"一行 = 一个实例"**，因此同包多实例是正常装配手法，而不是复制包。
- **子路径入口**（6 个声明、web 组合内生效 5 个）：`dsh-plugin-manager/tools`（工具行，base 默认 disabled）、`dsh-tool-subagent-control/list-agents`、`dsh-tool-cordis/host`、`dsh-tool-subagent/model-selection-settings`、`dsh-web-app/startup`、`dsh-headless/startup`。用途：**同一包暴露多个独立插件入口**（如 `web-app` 包既有 `web-runtime` 主行，又有 `startup` 提供 `webStartup` 服务）。
- 组行（`group:true`）的 `config` 是**子 entry 列表**，子行有自己的 id（在组内寻址），组内 `isolate` 决定服务命名空间。web 组合 3 个组共 8 个子行（§3.9）。

### 3.6 `disabled` 的三种形态与真实用法

1. **`disabled: true`**（静态禁用，仍保留行）：web 组合生效 **24 行**。最主要用途是"**平面迁移开关**"：web-app 把 base 里属于"agent 平面"的 22 个行禁用、改由每个 session 的 preset 挂载（`dsh-web-app/cordis.patch.yml:460-476` 的长注释解释了"为什么禁用而不是删除"——"a row absent from a surface overlay would silently reappear the day someone reorders the composition"）。这是可读性极强的一条设计原则：**用 disabled 表达"本 surface 不在此平面挂载"，而不是让行消失**。
2. **`disabled: false`**（显式启用）：用于把下层禁用行重新打开（例如 profile patch 覆盖 base 的 `disabled:true`；`dsh-hmr/README.zh.md:29-35` 的示例 `- id: hmr\n disabled: false`）。
3. **`disabled: !!js "<表达式>"`**（运行时条件）：web 组合生效 **11 处**（顶层 9 + 组内 2），全部是"环境/进程上下文"条件，实测表达式只有四种形态：
   - 平台：`process.platform === 'win32'` / `!== 'win32'`（bash/pwsh 互斥：`dsh-base/cordis.patch.yml:251,257,283,287`；组内 `dsh-experimental-terminal-bundle/cordis.patch.yml:12,15`）；
   - 宿主能力：`!ctx.get('profileContext')`——只有 profile 启动器才有的服务（plugin-manager/hmr/config-editor/settings：`dsh-base/cordis.patch.yml:22,30,107,111`）；
   - surface 判断：`ctx.get('profileContext')?.name !== 'desktop'`（web 专属/desktop 专属行：`dsh-web-app/cordis.patch.yml:47,59,298`）；
   - 配置值插值（非 disabled）：`!!js dshHomePath('sessions')`、`!!js process.env.DSH_PERMISSION_MODE ?? 'workspace-write'`（`dsh-base/cordis.patch.yml:139`、`244`；`dsh-app-boot/lib/index.js:4552` 注入 `dshHomePath`）。
4. **一个可直接观察的覆盖效应**：base 的 `tool-bash` 行（L283）原本 `disabled: !!js process.platform === 'win32'`，web-app 用 `disabled: true`（L480）覆盖它 → 最终树里该行是**静态 true**，`!!js` 消失。所以最终树 11 处 `!!js` < web 组合 13 个 patch 文件里的 25 处 `disabled: !!js`（全部 18 个 bundle 共 32 处，§6-A12），差值主要被上层静态覆盖或位于 preset 子树。

`!!js` 的实现是硬约束（§4.7）：`cordis-plugin-loader/src/config/utils.ts:3-10`：

```ts
export const evaluate = new Function('ctx', 'expr', `
  with (ctx) { return eval(expr) }
`)
```

配合 `interpolate()`（同文件 15-23）在**行激活时**递归替换 config 里的 `{__jsExpr}` 节点；YAML 侧由 `cordis-plugin-include` 的自定义 tag 解析（`cordis-plugin-include/src/index.ts:9-23`）。**这要求运行时具备完整 JS 求值器**，且表达式可以访问 `ctx`（服务图）、`process`、profile 内注入的 helper（`dshHomePath`）。

### 3.7 按服务可用性激活：行顺序没有加载语义

base patch 头部写得很直白（`$PKGS/dsh-base/cordis.patch.yml:12-13`）：

> "Row order carries no load semantics (activation is service-availability driven); the grouping is for readers."

机制（Cordis core）：插件声明 `inject`（静态服务依赖）→ fiber 进入 **PENDING**，等所需服务全部可用才激活；服务被替换/撤走时自动回 PENDING/卸载（`cordis/src/fiber.ts:147-156` FiberState、`cordis/src/registry.ts:160-190`）。因此"provider 后到"也能激活，**装配顺序由依赖图而不是文件顺序决定**。行级 `inject` 是这一机制在配置层的暴露：`webserver` 行 `inject: [webStartup]`，其 `config.host: !!js ctx.webStartup.host ?? '127.0.0.1'` 只有在 `web-startup` 服务就绪后才求值（`dsh-web-app/cordis.patch.yml:8-13`）。启动审计兜底：`boot()` 在 `loader.await()` 后调 `auditStartupEntries()`（`dsh-app-boot/lib/index.js:4464-4478`），只对**全局 required 白名单**（`agent-loop`,`webserver`,`web-runtime`,`modules`,`connection`,`headless-runner`,`acp`,`sdk-jsonrpc-server`，`lib/index.js:4267-4278`）缺失/未激活抛 `StartupError`，其余只 warn；**required 是 id 白名单而非行元数据**——缺失/disabled 的 required 不报错，只有"存在但激活失败"才致命。

### 3.8 第二层装配：Agent preset（会话级子插件树）

进程级 208 行之外，还有一层**每 session 装配**的子树。`dsh-web-app` 的 4 个 preset 文件各自插入一个 `dsh-agent-preset` 行，`config.plugins` 是一份**完整子 entry 列表**（standard/ptc/minimal/cordis 四种 agent 形态）：

```yaml
# $PKGS/dsh-web-app/presets/standard.patch.yml:1-9
- insert:
    - id: preset-standard
      name: '@deepseek-ai/dsh-agent-preset'
      config:
        id: standard
        order: 1
        plugins:
          - id: persona
            name: '@deepseek-ai/dsh-persona'
          # … 工具/上下文/规划组 …
```

实现上，`dsh-agent-preset` 是一个**既是 Group 又是 Service 的插件**（`$PKGS/dsh-agent-preset/lib/index.js:7-26`）：

| 要素 | 代码 | 作用 |
| --- | --- | --- |
| `static [EntryGroup.key] = true` | :12 | 告诉 Loader"本行 `config.plugins` 是子行列表，勿提前插值 `!!js`"（父行 config 保持字面量，子行激活时各自求值） |
| `static inject = ["agentPresets"]` | :10 | 依赖 preset 注册表服务 |
| `async *[Service.init]()` | :24-26 | 服务初始化时 `ctx.agentPresets.register(this.config)`，注册失败自动回滚 |
| `static Config` | :13-19 | `{id, name, description, order, plugins}` 的 Schemastery schema |

于是：**preset 行本身在进程级树里只挂载一次**（4 个），真正的 agent 子插件树在创建 session 时由注册表按 `config.plugins` 实例化（可能多个 session 共享同一份声明、各自一份实例）。这解释了 web-app 为什么把 22 个 agent 平面行在 host 平面禁用：工具不再进程级单例，而是"每个 session 一棵 preset 子树"。**"进程平面 / 会话平面"两级组合是 DSH 模块化最容易被忽略的一半**，也是 Go 侧最能直接借鉴的部分（§5-P12）。

### 3.9 最终组合结果（web profile，实测）

用本地复刻的 `applyEntryPatches`（与 include 源码逐行对齐）对 13 个 patch 文件求值，结果：

| 指标 | 值 |
| --- | --- |
| 最终行数 | **208**（顶层 200 + 组内子行 8） |
| 唯一行 id | 208 |
| 唯一 `name` | 199（**198 个包** + 1 个 `cordis:group` 内建） |
| `disabled: true` 生效行 | 24 |
| `disabled: !!js` 生效行 | 11（顶层 9 + 组内 2） |
| 行级 `inject` 覆盖 | 3（`webserver`/`web-runtime`/`connection` 等 `webStartup`） |
| 组（`group:true`） | 3（session-search、terminal、ralph），子行合计 8 |
| 子路径 `name` 行 | 5 |
| 同包多实例 | 5 组（agent-preset×4、tool-subagent×2、session-query-sqlite×2、terminal-bash×2、workflow-ptc×2） |
| `config` 被用户层覆盖的行 | 5（3 UI + agent-default-model + permission） |

**验证**：把最终树的 id 集合与 `base insert` 的 96 个 id 对齐，所有 base 行都在；web-app 的 23 个 disable 全部命中（否则会打 warn）。这说明"分层 patch + 覆盖"在真实安装上是自洽的。

### 3.10 客户端装配：`dsh.client` roster → `window.__DSH_BOOT__`

host 侧 `dsh-client-modules` 的 Node 半边扫描 Loader 条目，把每个 `dsh.client` 声明变成浏览器启动图（`$PKGS/dsh-client-modules/lib/index.js:417-513`、`720-758`；README.zh.md:12,34,69）：

1. 按 Loader entry 名解析包 manifest，读 `dsh.client`（platform 必须 `web`），解析 `exports["./client"]` 对应文件（缺 bundle 直接抛错），按 `external` 依赖做拓扑排序（`orderByModuleGraph`，:422-444）生成 `WebBootEntry[]`；
2. 组成 combo URL（`/plugins/??pkg&…&rev=…`）并注入 `<head>`：`__ModuleLoader__` 注册队列 → application combo preload → bootstrap combo script → `window.__DSH_BOOT__` 图（`bootInjections`，:460-513）；
3. 浏览器半边的 `ClientModuleSystem` 按"seed 静态模块表 → 已物化模块 → 图行 → 已注册 factory"四级解析（`$PKGS/dsh-client-modules/lib/client.js:25-31`），bundle 只注册 `factory`，首次 import 才执行模块体（懒 CJS）；HMR 下 bundle 带 `rev`、Host 带 release 身份，页面只应用"同 release"的图帧（README.zh.md:47-60）。

**这就是 78 个双面包的"client 半边如何到达"**：不是静态打包进 shell，而是 host 扫描 + HTTP 分发 + 浏览器懒加载。对"单二进制内嵌原生 UI"目标，这一整套（HTTP `/plugins`、combo、懒 CJS、release 帧）都**不适用**。

---

## 4. 插件形态与运行时/加载语义

### 4.1 Cordis 插件契约总览（Loader 1.0.6）

一个插件（host 侧）可以是四种形态之一（loader README），真实包中都能找到：

| 形态 | 形态定义 | 真实例子 | 证据 |
| --- | --- | --- | --- |
| 函数插件 | `export function apply(ctx, config)` + 可选 `export const name/inject/Config` | `dsh-tool-bash` | `lib/index.js:340`（apply）、:731（`export { Config, apply, inject, name }`） |
| 类插件（普通） | `class X { static inject; static Config; constructor(ctx, config) }` + `export default` | `dsh-agent-preset` | `lib/index.js:7-26` |
| 服务插件 | `class X extends Service`，构造里 `super(ctx, '<服务名>')` | `dsh-goal` | `lib/index.js:493,587-592` |
| 组插件 | `class X extends EntryGroup` 或 `static [EntryGroup.key]=true`，`config` 是子行列表 | `dsh-agent-preset`、`cordis:group` | `cordis-plugin-loader/src/config/group.ts:6,75-77` |

三个通用要素：
- **`apply` / 构造器** = 插件体（在 fiber 生命周期内运行，注册的一切自动随 fiber 卸载）；
- **`inject`** = 必需服务列表（数组或 name→config map；类上 `static inject`，或行级 `inject` 覆盖）；服务可用性驱动激活；
- **`Config`** = Schemastery schema（`@deepseek-ai/schemastery`，独立 vendored 包）：Loader 在激活前 `resolveConfig`（校验 + 默认值 + 类型转换），`apply` 收到已校验的 config。

计数佐证（§6-A13）：`lib/index.js` 里含 `static inject` 的包 58 个；含 `static Config`/`const Config = z` 的 130 个；`package.json` 引用 schemastery 的 140 个。**不是每个包都是"插件"**——util/类型面/资源包不注册插件，但这三类要素覆盖了所有会在 Loader 里出现行的包。

### 4.2 host 工具包：`dsh-tool-bash`

```js
// $PKGS/dsh-tool-bash/lib/index.js:223-233
const name = "tool-bash";
const inject = ["tools", "shell", "systemPrompt", "shellEnv", "workingDirectory"];
const Config = z.object({
  enableRunInBackground: z.boolean().default(true),
  promoteOnTimeout: z.boolean().default(true)
});
```

```js
// $PKGS/dsh-tool-bash/lib/index.js:340-…，注册工具；:720-730 处理"可选能力"
function apply(ctx, config = {}) { … ctx.tools.register(bashTool(...)) … }
ctx.inject(["jobs"], (jobCtx) => { /* jobs 服务可用时升级为后台任务版本 */ });
```

要点：
- `inject` 里的 5 个服务是**硬依赖**（缺任一则该行 PENDING，启动审计只 warn）；
- `jobs` 是**可选能力**：不在 `static inject` 里，而是 `ctx.inject(["jobs"], cb)` 动态挂一个子 fiber——"可选依赖不拖累主插件"，这是 §3.7 之外的第二套"按服务可用性激活"用法；
- `Config` 的两个默认值在 patch 行**不写**的情况下生效（base 的 `tool-bash` 行没有 config，`dsh-base/cordis.patch.yml:281-283`）；
- 行级 `inject` 也可以覆盖（web-app 的 `webserver` 行即为示例）。

### 4.3 host 服务包：`dsh-goal`

```js
// $PKGS/dsh-goal/lib/index.js:493 / 587-592
return class GoalService extends _classSuper {
  static inject = ["agents", "sessionProjections"];
  static Config = z.object({ defaultMaxGoalRounds: z.number().default(256) });
  constructor(ctx, config = {}) { super(ctx, "goals"); … ctx.sessionProjections.register(goalProjectionDefinition); … }
}
// :906
export { … GoalService as default, … }
```

要点：
- `extends Service` + `super(ctx,"goals")` ⇒ 构造即注册 `ctx.goals` 服务，注册随 fiber 撤销（`cordis/src/service.ts:11-60`）；
- 服务名（`goals`）与包名（`dsh-goal`）解耦，**其他包只通过服务名依赖**（`dsh-tool-goal` 依赖 `ctx.goals`，`dsh-command-goal` 依赖命令服务）；
- `static Config` 由 Loader 校验后传入（base 的 `goal` 行没有 config，`dsh-base/cordis.patch.yml:323-324`）；
- 领域逻辑（投影 schema/事件 fold）在同包内按模块组织（`lib/index.js:343-449`），**服务的"领域定义"与"服务注册"同包**——这是 core 服务的典型切法。

### 4.4 组/服务混合体：`dsh-agent-preset`

已在 §3.8 展开。它证明了一个第四形态：**同一插件既参与进程级树（作为组行），又在 init 时注册运行期服务**。

### 4.5 client-ui 包：`dsh-client-ui-goal`

```js
// $PKGS/dsh-client-ui-goal/lib/client.js:1-6
window.__ModuleLoader__.load({
  id: "@deepseek-ai/dsh-client-ui-goal",
  factory: (require) => { var module = { exports: {} }; var exports = module.exports; … }
});
```

- `package.json:29-41` 的 `dsh.client.inject` 列出**浏览器服务依赖**（remotes/session-controller/renderer/session/chat/conversation/locale）——也就是说 client 半边有自己的 Cordis 图和自己的 DI；
- 浏览器 bundle 不是 ESM 模块，而是"注册 factory + require 注入"的懒 CJS（`dsh-client-modules/lib/client.js:20-31`）；
- UI 通过 **slot 注册**（导入 `dsh-client-ui-primitives` 等）把自己的组件挂进布局，没有中心化路由表。

### 4.6 Typert：生成的 host↔client 类型面

29 个包有"三面"：`./typert`（host 反射 manifest）、`./remote`（浏览器远程客户端），全部由生成器生成：

```js
// $PKGS/dsh-goal/lib/typert.host.js:1-2（生成文件头）
/* Generated by @deepseek-ai/dsh-typert-generator from FaceModel — do not edit. */
import { z } from 'zod'
// :590-591
package: '@deepseek-ai/dsh-goal',
face: 'host',
```

- 生成物内含：zod schema（方法参数/返回值，含 `.describe()` 文档）、JSON Schema、方法描述符、`TYPERT` manifest；
- `dsh-typert-loader` 扫描每个 Loader 条目的包，若 `exports["./typert"]` 存在则导入并 `ctx.typert.register()`，卸载自动撤销（`$PKGS/dsh-typert-loader/lib/index.js:40-44,79-88`）；浏览器侧 `./remote` 产物通过同一注册表拿到 schema，把 typed 调用发到 gateway（`dsh-api-gateway` + `dsh-api-remotes`）。
- **意义**：host↔client 边界不是手写协议文档，而是**从类型模型生成**的 schema + 描述符；Go 侧可用同样思路（从 Go 接口/结构体生成 JSON schema 与客户端），但不需要 zod/浏览器运行时。

### 4.7 运行时/加载语义：哪些是"必须有的运行时能力"

| # | 运行时能力 | DSH 的实现与证据 | 出现在哪些包 |
| --- | --- | --- | --- |
| R1 | **动态模块解析与 import** | Loader `EntryTree.import`：`cordis:` 内建查表，否则 `internal.import(name, baseUrl)`（`cordis-plugin-loader/src/config/tree.ts:111-130`） | 所有行 |
| R2 | **YAML 方言 + 自定义 tag** | `entryListSchema = JSON_SCHEMA.extend(JsExpr)`（`cordis-plugin-include/src/index.ts:9-23`） | 全部 patch 文件 |
| R3 | **运行时 JS 求值** | `new Function('ctx','expr', with(ctx){eval(expr)})`（`utils.ts:3-10`）；行激活时 `interpolate` config（loader `index.ts:104-113`） | 11 行 `!!js` / web 组合 |
| R4 | **插件可卸载/可替换** | fiber 生命周期 + effect 逆序撤销 + 服务随 fiber 撤销（`cordis/src/fiber.ts`、`service.ts`） | 所有插件 |
| R5 | **服务依赖注入/反应式激活** | `static inject` / `ctx.inject` / PENDING 状态（`cordis/src/registry.ts:160-190`） | 297 个包的 peer + 行级 inject |
| R6 | **文件监听 + 模块热替换** | `dsh-hmr`：chokidar 5 监听 profile manifest、用户 patch、home patch、已加载模块文件；逐出 ESM/CJS 缓存并重启受影响条目（`dsh-hmr/package.json` deps；`README.zh.md:27,49,63`） | base 的 `hmr` 行（默认启用，root=[] 仅配置监听；源码监听 opt-in） |
| R7 | **配置写回** | Loader `EntryTree.write()`/`internal/update` 把运行中树写回 `cordis.yml`（所以启动器每次重写空根，`$DSH/lib/profile-boot-C14i-8IN.js:216-222` 注释） | 配置编辑器/plugin-manager |
| R8 | **双运行时（Node host + 浏览器 client）** | 78 个 `dsh.client` 包；host 扫描 + HTTP 分发（combo）+ 浏览器懒 CJS + `__DSH_BOOT__`（`dsh-client-modules/lib/index.js:460-513`） | client-ui/client-core/api |
| R9 | **包安装与版本兼容检查** | profile 是 pnpm 项目；`dsh plugin add/remove` 转发 pnpm；启动前按 `engines.dsh`/peerDependencies 做 semver 兼容检查，`compatibility.json` 存精确版本豁免（`dsh-app-boot/README.zh.md`「Profile」；`$DSH/lib/plugin-BiveYA7h.js:1-9`） | 全部 bundle/插件 |
| R10 | **生成物管线** | Typert 生成器产出 `./typert`/`./remote`；client bundle 由 tsdown 构建为 `lib/client.js`（缺失即激活失败，`dsh-client-modules/README.zh.md`「构建要求」） | 29 个 typert 包、75 个 client bundle |

**这 10 条里，R1/R3/R6/R8 是 JS/浏览器运行时特有的；R2/R7/R9/R10 与运行时选型强相关；R4/R5 是 Cordis 语义、与语言无关。** §5 的可行性判断就是按这个划分做的。

---

## 5. 「DSH 方式」原则 × 目标环境：可照搬性逐条判断

### 5.1 判断前提（目标环境的硬事实，不弱化）

- **纯 Go + 编译期注册表**：`zeokeel-kernel/module` 提供 `module.Descriptor[T]`（ID/Provides/Requires/typed config/Factory）与 `module.Entry{ID, Module, Config, Disabled, Optional, Children}` 的 JSON 启动配置；预检（未知模块/重复 ID/缺 provider/类型不匹配/依赖环/坏配置）+ required/optional 失败策略 + 回滚（`/root/go/pkg/mod/github.com/zeokeel/zeokeel-kernel@v0.1.1/module/module.go:1-30`、`module/validate.go:13-38`）。
- **明确的非目标**（kernel 自述，不是本文的推断）："无进程内热配置更新，配置变更=重启"、"模块加载器是最小子集：无动态 ESM/HMR、无 include/group 完整语义"（kernel `README.md:191-192`）；`docs/COMPILE-TIME-REGISTRY.md:42-56` 的能力表把"运行期新增未编译进二进制的模块""热更新""不可信插件沙箱"全部标为 ❌。
- **单二进制内嵌原生 UI**：仓库实测 158 个 Go 文件 import `github.com/egoist/mygo/ui`；`go.mod` 当前**未**引入 zeokeel-kernel（0 处引用）——本文判断的是"引入后能照搬什么"，不假设已引入。
- **无动态 import / 无 HMR**：Go 标准库 `plugin` 只支持部分平台（Linux/FreeBSD/macOS）、与静态链接/交叉编译冲突，且 kernel 路线（含 `archtest` 禁 `unsafe`）明确不走这条路。

### 5.2 逐条判断表（DSH 原则 → Go 目标环境）

定义：**可照搬** = 语义与成本都可接受，Go 侧有直接对应物；**可近似** = 语义可保留大部分，但需要自建少量机制或接受行为差异；**不可照搬** = 结构上做不到，或做出来会违背单二进制/静态链接/无动态加载的前提。

| # | DSH 原则（证据） | 判断 | Go 侧落点 / 理由 | 需要新增的东西 |
| --- | --- | --- | --- | --- |
| P1 | **一模块一包、单职责、独立版本边界**（305 包 / 58 域；每包 README+类型+license） | **可照搬** | 一个 Go 包 = 一个模块，导出 `module.Descriptor[T]`；`go.mod`/包路径即版本与边界。粒度可照 DSH：core 服务包、capability 实现包、tool 包、命令包分开 | 无（kernel 已支持）；需把"包 = 插件出口"定为纪律 |
| P2 | **声明式 entry 树**（`cordis.yml=[]` + 200 行组合） | **可照搬** | kernel `module.Entry` JSON 启动配置（`LoadFile`）；比 YAML 少表达式，更严格（严格 JSON、未知字段拒绝） | 无（已有）；建议生成一份分 profile 的 assembly JSON 模板 |
| P3 | **分层 patch：bundle 层 + 用户层 + home 层 + `--patch` overlay，按 id 覆盖** | **可近似** | kernel **没有** patch 层概念（`module` 只接受最终 entry 数组）。可在宿主侧实现"layers → merge → []Entry"：把 DSH 的 `applyEntryPatches` 语义（insert 追加、按 id 整字段覆盖、name 防呆、not-found warn）在 Go 写约 100-200 行，**在 `Load` 之前**完成；需要为 runtime 校验保留"目标不存在即告警" | patch 合并器 + 层来源诊断（warn 级）；`!!js` 不可用（见 P8） |
| P4 | **稳定 `id` 与包名解耦**（`ui-permission`→`client-ui-permission-presets`；用户 patch 带 `name` 防呆） | **可照搬** | `module.Entry.ID` 已是稳定实例 ID、`Module` 是模块 ID；在 patch 合并器里加 `name` 校验即可 | patch 合并器的 name-mismatch 检查 |
| P5 | **服务依赖注入 + 服务可用性驱动激活（行顺序无语义）** | **可照搬** | kernel 已有：`Plugin.Inject()` 门控、`Pending` 状态、服务变化触发 refresh、`WhenIdle` 静默、required/optional 策略（kernel README「核心保证」；`docs/COMPILE-TIME-REGISTRY.md` §2 表） | 无；`inject` 行级覆盖需宿主在 Load 前合入 Entry（kernel Entry 无 inject 字段，可在 adapter 里把行级覆盖写进 Descriptor 包装） |
| P6 | **能力接缝 vs 具体 provider**（`dsh-fs` 接口 + `dsh-fs-local`/`-sandbox`；bash 三实现） | **可照搬** | `cordis.ServiceKey[T]` + Go interface 作契约；每个 provider 是独立包/Descriptor；`Provides/Requires` 做类型化预检（kernel 已有，且比 DSH 的字符串服务名更强） | 无；建议建 `contracts/` 包集中放 `ServiceKey` |
| P7 | **双面插件（host 行 + client bundle）+ Typert 生成类型面** | **host 可照搬、client 不可照搬** | host 半边 = 普通 Go 模块；client 半边对应"UI 贡献点"：在原生 UI 进程内用**服务/注册表**（可自建 slot 注册表，或直接用 kernel Service）让各 UI 模块注册视图，**不经过浏览器**。Typert 的"从类型生成 wire schema"可照搬到桌面 RPC/子进程边界，但"生成浏览器 bundle"没有对应物 | UI 贡献点契约（Startup/Update/Paint 或业务级 slot）；若未来有 Electron/webview 再谈浏览器半边 |
| P8 | **配置即代码：`!!js` 表达式 + 平台/环境/profile 条件**（11 处运行时表达式） | **不可照搬；可近似** | Go 无法 `eval` JS。替代方案三选一：① 启动配置里用**受限表达式**（如 CEL/Starlark，或自研 `$if`/`$env` 节点）只覆盖 DSH 实际用到的四类（平台、env、profile 名、service 存在性）；② 把条件下沉为**模块工厂内的默认值推导**（配置里只写数据）；③ 每个平台/产品编不同 assembly JSON（build 时生成）。**注意**：无论哪种，都不能让配置具备任意代码执行能力——这也是单二进制路线的安全收益 | 受限条件语法（建议只做 5-6 个谓词）+ 文档；不要引入通用脚本引擎 |
| P9 | **动态加载 + HMR（`dsh-hmr`：chokidar 监听、ESM 逐出、条目重启）** | **不可照搬** | 静态二进制内没有"重新 import 一个 Go 包"的语义。可以做的近似：① 配置/assembly JSON 变更后**提示重启**（kernel 的既定设计）；② 开发模式用外部进程 watcher（`go run` 重建重启）替代 HMR，体验接近 DSH 的"改完刷新"但**不是语义热更**；③ 长稳场景若真需要"不停服改配置"，那是 kernel 明确排除的独立课题（需要 Update/Snapshot/Restore 契约），不要在模块拆分时顺手承诺 | 无（明确不做）；可在 CLI 里加"配置已变，重启生效"提示 |
| P10 | **运行时安装/卸载插件（npm + pnpm + profile 目录 + 插件市场）** | **不可照搬** | 编译期注册表的定义就是"模块集合构建时固定"；"接入新插件"= 把 Go adapter 包加进宿主并**重新构建**（kernel 文档原话）。可近似：① `assembly.json` 里 **按已注册模块**启用/禁用（DSH 的 `disabled:true` 机制可照搬）；② 发布多产品二进制（不同 `main`/build tags 带不同模块集）；③ "插件市场"只能作为"编译期可选模块清单 + 构建服务"，不是运行时安装 | 可配置启停（已有）；产品化需要构建矩阵/发行说明模板 |
| P11 | **一套安装、多 profile 复用同一组合**（web/headless/acp/sdk/sdk-minimal 由同 launcher 启动） | **可照搬（高价值）** | 同一 Go 二进制用 `--profile`/`assembly.json` 选择 entry 集；kernel 的多宿主隔离已支持多 registry/多 root。DSH 的 web=base+web-app、headless=base+headless 的"核心 bundle + 表面 bundle"结构可直接映射 | 每个产品的 assembly JSON + CI 里跑 `Validate`（kernel 预检）当配置测试 |
| P12 | **两级平面：进程级树 + 会话级 preset 子树（scope/isolate）** | **可近似** | kernel 有 `Context.Isolate/IsolateLabeled` 与 child fiber；但 `module.Entry` 没有 isolate/preset 概念，需要宿主层：为每个 agent/session `root.Extend()`/`Isolate` 建子 context，按 preset 声明装载一组模块。工作量中等，语义可达 | agent-preset 等价物：preset 声明格式 + per-session 装载器；注意服务单例（如 jobs/subagent 注册表）必须留在进程平面（DSH 的 host-plane 论证见 `dsh-web-app/cordis.patch.yml:460-476`） |
| P13 | **自动生成类型面（Typert）+ 兼容性预检（engines/peer semver + 精确版本豁免）** | **可近似** | 生成部分：从 Go interface/struct 生成 JSON Schema（或直接用 `encoding/json` + 手写 schema）；预检部分：kernel 已有"服务契约类型/依赖环/重复 provider"预检，但**没有 semver 兼容检查**——可在 `Load` 前按 assembly 里声明的模块版本做 range 校验（几十行）。无需 compatibility.json 豁免机制（无运行时安装） | schema 生成脚本（可选）；版本范围校验器（可选） |

### 5.3 哪些属于"DSH 方式的实质"，哪些属于其运行时偶然

| 分类 | 内容 | 对 Go 的含义 |
| --- | --- | --- |
| **实质（与语言无关的组合纪律）** | 一模块一包；声明式 entry 树；分层 patch + 稳定 id；服务 DI + 可用性驱动激活；能力接缝/多 provider；平面划分（进程 vs session）；用 disabled 表达平面迁移；bundle（组合）与插件（实现）分离 | 全部可迁移（P1/P2/P4/P5/P6/P11/P12），是本次拆分的**主要收益来源** |
| **交易成本（可近似，需自建）** | 分层 patch 合并器；受限配置条件；会话级 preset 装载；类型面生成；半自动兼容检查 | P3/P8/P12/P13，工作量可控（估计数百行 Go，不含生成器） |
| **运行时偶然（不可照搬）** | ESM 动态 import；`!!js` 任意求值；chokidar HMR/模块逐出；profile `cordis.yml` 写回；浏览器 bundle 分发/懒 CJS/release 帧；npm/pnpm 运行时安装；zod/typert 浏览器产物 | P7(client)/P8/P9/P10；照搬它们会直接破坏"单二进制、静态链接、可交叉编译"的前提 |

### 5.4 三条不能弱化的硬结论

1. **"运行时安装第三方 DSH 插件"不能照搬**。编译期注册表与它自相矛盾，不是工程难度问题。对外表述必须改成"模块集随构建固定，配置可启停"。
2. **"配置里写 JS 表达式"不能照搬**。可行的近似是受限谓词（平台/env/profile/service 存在性），且应作为**显式的小语言**设计、带校验与文档；引入通用脚本引擎会把单二进制的确定性与安全收益重新丢掉。
3. **"热更"不能照搬**。可以做到"配置变更提示重启"和"开发模式 watcher 重启"，但不能承诺 DSH 那种"改模块文件不重启生效"。如果产品目标必须包含不停服热更，应单独立项评估（kernel 已把它列为独立课题），不要夹带在模块拆分里。

### 5.5 建议的照搬顺序（若引入 zeokeel-kernel 后要动手）

| 优先级 | 行动 | 依据 | 验收 |
| --- | --- | --- | --- |
| P0 | 定义"包 = 插件出口"规范：每包导出 `Descriptor[T]`，服务契约集中 `contracts/`；用 kernel `Validate` 做 CI 门禁 | P1/P5/P6 | 一个 `assembly.json` 可 `Load`，预检在 CI 里跑 |
| P0 | 把现有装配逻辑抽成 `assembly/` 包：layers → merge → `[]module.Entry`（含 id/name 防呆、not-found warn、disabled 继承） | P2/P3/P4 | 两份分层配置合成后行数/禁用数可断言（对齐本文 §3.9 的度量方法） |
| P1 | 划分进程平面 vs 会话平面；会话平面用 `Context.Isolate` + 子 fiber 装载 preset | P12 | 两个 session 各自工具目录/规划模式互不串扰 |
| P1 | 为每类 capability 定义接口 + 多实现（fs/终端/模型/存储），用 `Provides/Requires` 预检 | P6 | "缺 provider"在启动前报错，而不是运行期 nil |
| P2 | 受限配置条件小语言（只做平台/env/profile/service 存在性）；配置变更提示重启 | P8/P9 | 无 `eval`；非法条件在预检报错 |
| P2 | 多产品 assembly（main/build tags）+ 构建矩阵文档 | P10/P11 | 同一内核构建出 ≥2 个产品 entry 集 |

---

## 6. 复现命令与证据索引

### 6.A 计数命令（在 `$PKGS` 下执行；全部实测）

统一前置：`export DSH=/usr/local/lib/node_modules/@deepseek-ai/dsh; export PKGS=$DSH/node_modules/@deepseek-ai; export PROFILE=/root/.dsh/profiles/web`

| 编号 | 命令 | 期望输出 |
| --- | --- | --- |
| A1 | `jq -r '.version' $DSH/package.json` | `0.2.1-rc.2` |
| A2 | `ls -d $PKGS/*/ \| wc -l` | `305` |
| A3 | `jq -r '.version' $PKGS/*/package.json \| sort \| uniq -c \| sort -rn` | `294 0.2.1-rc.2` + 11 个 vendor 版本 |
| A4 | `du -sh $DSH/node_modules $PKGS` | `852M` / `566M` |
| A5 | 见 §6.C 脚本 `code-volume.mjs` | `936/881610/33.4MB` 等 |
| A6 | `jq -r '.repository.directory' $PKGS/*/package.json \| cut -d/ -f1-2 \| sort -u \| wc -l` | `58` |
| A7 | `jq -r 'select(.dsh.client) \| .name' $PKGS/*/package.json \| wc -l` | `78` |
| A8 | `jq -r 'select(.dsh.bundle) \| .name' $PKGS/*/package.json \| wc -l` | `18` |
| A9 | `jq -r 'select(.peerDependencies["@deepseek-ai/cordis"]) \| .name' $PKGS/*/package.json \| wc -l` | `297` |
| A10 | 见 §6.C 脚本 `classify.mjs` | 13 行角色计数，和 = 305 |
| A11 | 见 §6.C 脚本 `anatomy.mjs` | 301/301/296/295/214/75/29 等 |
| A12 | `grep -rn 'disabled: !!js' $PKGS/dsh-*/cordis.patch.yml $PKGS/dsh-web-app/presets/*.patch.yml \| wc -l` | `32`（全部 18 个 bundle）；仅 web 组合 13 文件为 25；最终树生效 11 |
| A13 | `grep -l 'static inject' $PKGS/*/lib/index.js \| wc -l`；同法统计 `static Config\|const Config = z`、`schemastery` 引用 | `58` / `130` / `140` |
| A14 | `find $PKGS -maxdepth 3 -path '*/lib/client.js' \| wc -l`；`find $PKGS -maxdepth 3 -name 'typert.host.js' \| wc -l` | `75` / `29` |
| A15 | `wc -l $PROFILE/*.yml $PROFILE/*.json $PROFILE/*.yaml`；各 bundle patch `wc -l` | 见 §3.1/§3.2 |

### 6.B 文件:行号证据索引（核心引用）

| 结论 | 证据 |
| --- | --- |
| profile 的 9 个 bundle 及顺序 | `$PROFILE/package.json:7-17` |
| 空根配置与"别改它"注释 | `$PROFILE/cordis.yml:1-4` |
| 用户 patch 的 5 行覆盖 | `$PROFILE/cordis.patch.yml:4-35` |
| bundle 声明形状（单文件/文件列表） | `$PKGS/dsh-base/package.json:31-34`；`$PKGS/dsh-web-app/package.json:46-54` |
| 合成顺序（bundle→用户→home→overlay→遥测） | `$DSH/lib/profile-boot-C14i-8IN.js:76-79,224-235`；`$DSH/node_modules/@deepseek-ai/dsh-app-boot/lib/index.js:1118-1145` |
| 根 Include 挂载 + 内建 group/include | `dsh-app-boot/lib/index.js:4125-4145` |
| boot 序列 + 启动审计 + required 白名单 | `dsh-app-boot/lib/index.js:4270-4278,4464-4478,4507-4550` |
| patch 行结构与 apply 语义 | `$PKGS/cordis-plugin-loader/src/config/entry.ts:10-23`；`$PKGS/cordis-plugin-include/src/index.ts:57-140` |
| `disabled` + `!!js` 求值 | `cordis-plugin-loader/src/config/entry.ts:66-93`；`src/config/utils.ts:3-27` |
| YAML `!!js` tag 方言 | `cordis-plugin-include/src/index.ts:9-23` |
| 动态 import / `cordis:` 内建 | `cordis-plugin-loader/src/config/tree.ts:111-130`；`src/index.ts:87` |
| 行级 inject 等 `webStartup` | `$PKGS/dsh-web-app/cordis.patch.yml:8-13,179-181,197-199,234-236` |
| 行顺序无加载语义 | `$PKGS/dsh-base/cordis.patch.yml:12-13` |
| 平台/上下文 `!!js` 行 | `dsh-base/cordis.patch.yml:22,30,107,111,251,257,283,287`；`dsh-web-app/cordis.patch.yml:47,59,298` |
| config 整块替换纪律 | `dsh-base/cordis.patch.yml:1-10`；`dsh-web-app/cordis.patch.yml:1-6` |
| preset（会话级子树）声明 | `$PKGS/dsh-web-app/presets/standard.patch.yml:1-9`；`$PKGS/dsh-agent-preset/lib/index.js:7-26` |
| 组 + isolate | `$PKGS/dsh-experimental-terminal-bundle/cordis.patch.yml:1-19`；`dsh-experimental-session-search/cordis.patch.yml:5-14` |
| 服务插件三要素 | `$PKGS/dsh-goal/lib/index.js:493,587-592,906`；`$PKGS/cordis/src/service.ts:11-60` |
| 工具插件三要素 | `$PKGS/dsh-tool-bash/lib/index.js:223-233,340,720-730,731` |
| 可选能力（jobs）动态挂载 | `$PKGS/dsh-tool-bash/lib/index.js:720-730` |
| client 双面声明 | `$PKGS/dsh-client-ui-goal/package.json:29-41`；`$PKGS/dsh-api-gateway/package.json:39-46` |
| 浏览器插件注册 | `$PKGS/dsh-client-ui-goal/lib/client.js:1-6`；`$PKGS/dsh-client-modules/lib/client.js:20-31` |
| 客户端启动图/`__DSH_BOOT__` | `$PKGS/dsh-client-modules/lib/index.js:417-513,720-758`；`README.zh.md:12,34,69` |
| Typert 生成面 + 注册 | `$PKGS/dsh-goal/lib/typert.host.js:1-2,590-591`；`$PKGS/dsh-typert-loader/lib/index.js:39-44,79-88` |
| HMR（监听/逐出/patch 热载） | `$PKGS/dsh-hmr/README.zh.md:27,49,63`；`$PKGS/dsh-hmr/package.json`（chokidar 5） |
| 内核能力/边界（Go 目标环境） | `zeokeel-kernel@v0.1.1/README.md:91-108,191-192`；`module/validate.go:13-38`；`docs/COMPILE-TIME-REGISTRY.md:42-56,331-343` |
| 本仓库原生 UI 事实 | `grep -rl 'github.com/egoist/mygo/ui' internal cmd \| wc -l` → 158；`go.mod` 无 zeokeel-kernel |

### 6.C 复合脚本（与 §2/§6.A 配套；因篇幅未内联完整代码，按 §2.2/§2.5 规则约 10–20 行 Node 可重建）

- `code-volume.mjs`（A5）：递归每个包 `lib/`，统计非 client `.js` / client*.js 的行数与字节，以及 `.d.ts`、README 行数。
- `classify.mjs`（A10）：按 §2.2 的 13 条规则（先命中先归类；core/cap 用源码域白名单 + 两个显式例外 `dsh-session-title-all-prompts-llm→core`、`dsh-skill-badge→cap`）输出 13 行计数并断言总和 = 305；若出现 `UNCLASSIFIED` 说明安装产物新增了包，需更新白名单。
- `anatomy.mjs`（A11）：统计每包的标准文件存在性（§2.5 表）。
- `compose.mjs`（§3.9 数据）：读 `$PROFILE/package.json` → 按 `dsh.bundle.patch` 解析 patch（`js-yaml` + 自定义 `tag:yaml.org,2002:js`）→ 复刻 `applyEntryPatches` → 追加用户层 → 输出行数/唯一 id/唯一 name/disabled/组/子路径/多实例。**只读，不写 profile**。

### 6.D 证据不足与未验证项（诚实边界）

| 项 | 状态 | 原因/替代 |
| --- | --- | --- |
| 实跑 `dsh --dump-config` 交叉验证 | **未做** | 会重写运行中 profile 的 `cordis.yml`（内容相同但零写入原则）；已用源码级复刻替代，且 §3.9 计数与各层文件独立吻合 |
| 实际触发 HMR（改模块文件看热替换） | **未做** | 需改动安装产物（禁止）；结论来自源码/README（`dsh-hmr`）与机制分析 |
| desktop 专用行（`profileContext.name==='desktop'`）的实际启用 | **未验证** | 本机只有 web profile；`desktopPlatform`、`ui-sidebar-browser` 等行的行为按表达式推断 |
| 78 个双面包的运行时依赖图完整性 | **部分验证** | 只抽查 `client-modules`/`client-ui-goal`/`api-gateway`；未逐一验证 78 个 `dsh.client.inject` 的 provider；外部 URL 未访问，全部结论来自本机安装产物 |
| Go 侧 `module` 包对"行级 isolate/preset"的改造量 | **估算** | kernel 文档列为 P1（entry 级 isolate/intercept 未实现，`docs/COMPILE-TIME-REGISTRY.md:342-343`）；本文给的是方向与量级，未做原型 |


## 7. 给 Go 拆分的三条可直接执行结论（供综合报告引用）

1. **把"DSH 方式"翻译成 Go 时，先照搬装配模型、不要照搬运行时**：entry 树 + 服务依赖 + 多 provider 接缝 + 分层 patch 已经解释了 DSH 80% 的模块化收益（§5.2 的 P1/P2/P4/P5/P6/P11/P12），且 kernel 的 `module` 层与 `Inject`/`ServiceKey` 已覆盖大半；真正缺的只有"宿主侧 patch 合并器"和"会话级 preset 装载器"两件小工程。
2. **208 行装配事实 > 代码里的 feature flag**：web 组合 208 行装配全部集中在 13 个 YAML 文件（1730 行），代码里没有 `if (profile === 'web')` 式的分叉；Go 侧应把产品差异继续放在 assembly JSON + 分层合并器里，而不是散落到模块工厂的 `if cfg.Mode`。
3. **不要为了"像 DSH"而保留运行时动态性**：`!!js`、HMR、浏览器 bundle、pnpm 运行时安装这四件事一旦引入，就等于放弃纯 Go 编译期注册表路线的前提（静态、可交叉编译、单二进制、可审计）。把它们明确列为**非目标**，比做一半近似更符合本项目的约束；若产品确实需要"不停服热更/第三方运行时插件"，那应作为独立路线重新评估，而不是模块拆分的隐含承诺。
