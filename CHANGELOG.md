# Changelog

All notable changes to ZeoKeel are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/) and the project adheres to
[Semantic Versioning](https://semver.org/).

## [Unreleased]

（暂无未发布改动）

## [0.2.11] - 2026-11-10

macOS 改为在 Linux 上交叉编译，不再依赖 macOS 托管 runner。

- **三个平台全部在 `ubuntu-24.04` 上并行构建。** 产品没有 cgo，`go tool mygo build -platform
  darwin/arm64` 可以直接在 Linux 产出 `ZeoKeel.app`；发布流水线在 Linux job 里构建该 `.app` 并打成
  `zeokeel-<version>-darwin-arm64.tar.gz`，与 Windows setup、Linux AppImage 三个 job 并行。
- **签名/公证说明**：Linux 上没有 `codesign`/`hdiutil`/notarytool，macOS 包为**未签名、未公证**的
  `.app`（发布说明保留此提示）；手动首次打开可能需要在 Finder 右键「打开」或在系统设置中允许。
  应用内更新会把 tar.gz 解压覆盖当前 `.app` 并重启，沿用 host 的 archive 安装路径。
- 更新器 GitHub 资产选择：macOS 优先 dmg（旧 release），没有 dmg 时回退
  `-darwin-<arch>.tar.gz`；安装 kind 为 archive。
- 不再使用 macOS runner / 不再需要 `MACOS_CERT_*`、`APPLE_API_*` 签名 secrets 才能出包。

## [0.2.10] - 2026-11-10

发布流水线补丁（产品与 v0.2.9 相同）：

- **macOS 只用 GA arm64 runner（`macos-15`）**：不构建 Intel 版本。`macos-26`（新预览镜像）近期
  连续 15 分钟排队失败（"not acquired by Runner of type hosted"），参考 zeokeel-harness 的
  macOS job 一直使用 `macos-15` 且从未排队，切回该 GA arm64 镜像；签名/公证仍对 arm64 产物执行。
- **Linux AppImage 工具链安装加重试**：`go install appimagetool` 偶发代理查询失败，现在最多重试
  3 次并显式走 proxy.golang.org。
- v0.2.9 的 Windows/Linux 资产已发布；本版补齐 macOS dmg。
## [0.2.9] - 2026-11-10

继续修审计 P2，并修复 Windows 工具调用时弹出的 cmd 窗口。

- **Windows 控制台窗口。** 桌面版是 GUI 子系统、没有控制台；rig 的 pwsh/bash/后台任务/终端
  从无控制台进程启动时会各自新建 cmd 窗口。现在 GUI 进程启动时分配**一个隐藏控制台**，所有
  工具子进程继承它，不再逐个弹窗（从终端启动的情况保留用户自己的控制台）。这是对工具调用的
  全局修复，不需要改 rig 依赖。
- **后台任务解析更稳。** job_list 的 status/command 是自由文本，之前状态里出现 ` exit=` /
  ` duration_ms=` / ` command=` 会把后面的数值字段切错；改为从右侧锚定
  ` exit=<n> duration_ms=<n> command=` 解析，并补了自由文本测试。
- **快捷键标签随平台。** 右栏「开始」引导/命令面板在 macOS 显示 Cmd+Alt+P / Cmd+`（原来写的是
  Ctrl，和实际绑定不一致）；Linux/Windows 仍为 Ctrl。
- 顺手更新 COVERAGE / UI-CONFORMANCE / 设计 registry 中描述旧右栏团队/任务 pane 的过时内容。
- 说明：**v0.2.8 的 macOS dmg 因 GitHub macOS arm64 runner 容量连续排队失败，未随包发布**；
  Windows/Linux 安装包可用。macOS 用户可等 dmg 补传或从源码构建；已知旧版需手动安装一次。

## [0.2.8] - 2026-11-10

修复应用内更新：以前 Windows 只提示“重启后生效”但实际不升级，macOS 也没有静默安装路径；同时不再发布没用的更新归档。

- **更新改用安装包。** Windows 更新下载 `zeokeel-setup-<version>-<arch>.exe`；应用退出后由一个
  隐藏的 helper 进程（同一可执行文件，普通 argv，不经 cmd/shell 拼字符串）启动安装器完成覆盖安装。
  macOS 下载签名 `.dmg`，自动挂载、`ditto` 复制新 `.app`、原子替换当前 bundle 并重启；旧 bundle
  与安装计划保留到新版本启动后再清理；安装位置不可写或替换失败时恢复旧 bundle 并回退为打开 dmg。
  Linux 继续用 AppImage 自替换，并通过系统 shell（非临时挂载内的 helper）延迟重启。
- **立即安装，不再依赖“下次启动”。** 下载完成后按钮为「安装并重启」，host 的
  `uiroot.install-update` 命令立即执行安装；只有无此命令的嵌入环境才回退为下次启动安装。
  更新页文案不再承诺“仅重启即可升级”。
- **发布资产清理。** 不再发布 `darwin-arm64.tar.gz`、`windows-amd64.tar.gz`、`windows-arm64.tar.gz`
  这三个无用的更新归档；Release 只保留安装包/AppImage 和 `SHA256SUMS.txt`。
- **旧版本需要手动安装一次。** v0.2.7 及更早版本的更新器只认 tar.gz 归档，无法自动升级到本版；
  请手动下载安装一次 v0.2.8，之后的版本即可通过安装包自动更新。
- 顶栏长标题截断、三个 agent 入口常显（0.2.7 已有）包含在本版中。
- **对抗审计修复（独立审计 3 份报告）。** 折叠侧栏启动时丢失当前屏（标题/入口/右栏全部消失）；
  子会话里 switcher 双 owner 导致 header 构建 panic；生成文件卡片把失败/取消的 write/edit 也列成
  交付文件（且大参数截断后不出卡）——现在只收成功调用并用 `ToolCall.Path`；`OpenNative` 越权打开
  工作区外路径、工作区切换后同名文件显示旧内容、自动化运行会话靠标题前缀隐藏会误伤用户会话——
  分别改为工作区包含检查、按工作区隔离缓存、显式 `Origin` 元数据。

## [0.2.7] - 2026-11-10

0.2.6 的小补丁，修顶栏信息密度。

- **长标题截断。** 会话标题超过 DSH 面包屑宽度（约 220 DIP）后省略显示，完整标题保留在 Tooltip；
  不再把「智能体团队 / 子智能体 / 后台任务」和右上角工具挤走。
- **三个 agent 入口常显。** 「智能体团队」「子智能体 N」「N 个后台任务」只要有对应后端就始终显示，
  计数为 0 也在（例如「0 个后台任务」）；点开浮层有明确空态，不再出现入口时有时无。
- **后台任务停止后不再显示「退出码 -1」**，改为「—」。

## [0.2.6] - 2026-11-10

这个版本按 DSH 重做主框架：顶部三入口、左栏做减法、右栏改成按需多标签，并删掉旧检查器的大部分代码。

- **顶部入口**：会话标题行改为 `标题/面包屑 → 智能体团队 → 子智能体 N → N 个后台任务 → ⋯`。
  「智能体团队」打开原生浮层（成员卡 + 共享任务卡）；「子智能体 N」打开切换器，选中后**在主内容区**打开
  子会话：标题变成 `父标题 › 子智能体名` 面包屑，点父标题返回；一次性子代理只读，可继续队友可发消息。
  「后台任务」浮层显示状态/命令/用时/退出码并可停止。运行中 badge 保留在 ⋯ 之前。
- **右栏重做**：默认收起；打开后是「开始」引导（工作区文件 / 新建终端两张等宽卡片，无说明文字）+
  「文件」「终端」+ 每个打开文件一个可关闭标签；文件页含工作区树、交付区、文本/Markdown/图片预览与
  文件内折叠 diff；所有页面可滚动。旧 8 页签（概览/工具/日志/变更/团队/任务）删除，终端与文件保留。
- **生成的文件**：回合尾部新增「交付文件」「已编辑 N 个文件」卡片；每行可「在侧边栏预览」「查看改动」
  「用系统应用打开」，预览在右栏新开文件标签。正文/交付卡都不会丢掉产出的文件。
- **左栏做减法**：删除「首页」导航（启动落在会话/新对话 hero）；自动化运行会话不再混进会话列表
  （仍可在自动化页看运行历史）；侧栏保留 会话 / 自动化任务 / 插件 / 设置。
- **删除的旧代码**：右栏 8-pane 选择器与概览/工具/日志/变更/团队/任务 pane、`uikit.TeamSurface`、
  团队建人表单、旧团队/任务命令与 pane 切换快捷键、tool-card 团队跳转 footer；新增统一
  `ChildAgentService` 读模型、`JobService`、`FilePreviewer`（含系统应用打开）。diff 为 +9.5k/−12.3k（净减约 2.9k 行，含测试与文档）。

升级方式不变：下载覆盖运行，原有设置与会话保留；终端能力保留（只在右栏按需出现）。

## [0.2.5] - 2026-11-10

补上 0.2.4 因 macOS 托管 runner 排队而缺失的安装包；功能与 0.2.4 相同。

- **macOS arm64 dmg。** Developer ID 签名、`notarytool` 公证并 staple，仅支持 Apple Silicon。
- **macOS CI 镜像改为 `macos-26`**（原 `macos-15` 已偏旧、容量紧张），并延长 job 预算。

## [0.2.4] - 2026-11-10

这个版本把「工作步骤」按 DSH 的方式对齐了——运行时不再把所有读取文件/执行命令过程实时铺开
（在 v0.2.3 的安装包与无密钥更新之上发布）。

- **运行中只看一行工作步骤。** 现在默认（标准）模式下，AI 干活时只显示一行原地变化的状态：
  「正在运行命令 · npm test --watch …… 用时 8s」。不再把每个工具调用都展开到对话里，
  长任务滚屏明显减少；流式回答和需要你确认的卡片仍然照常显示。想看完整过程可以展开，
  或在设置里切换档位。
- **新增两个设置（设置 → 行为 → 工作步骤）。**
  「工作步骤展示」：简洁 / **标准（默认）** / 详细 / 完全展开；
  「工作步骤收起时机」：**回答结束后（默认）** / 下次有新消息时。
  完全展开就是旧版一直展开的行为；下次有新消息时会保留刚完成的过程，直到你发下一条消息才收起。
- 设置立即生效，原有设置与会话不受影响。

升级方式不变：下载覆盖运行，原有设置与会话保留。

验证：`./scripts/verify.sh --no-smoke` **10/10 PASS**（含全量测试与 `-race`、回归测试与界面快照）。

## [0.2.3] - 2026-11-10

这个版本把安装包和升级链路接上：

- **Windows 安装包。** `mygo build` 产出无命令行窗口的 GUI 程序和 NSIS 安装器，安装到
  当前用户目录，带开始菜单、卸载项和图标；exe 内置图标、版本信息和 DPI manifest。
- **Linux AppImage。** 发布只提供 AppImage；由 `scripts/build-appimage.sh` 从 mygo 的 Linux
  构建产物组装，图标/desktop 与程序同源。
- **macOS dmg 签名与公证（Apple Silicon）。** arm64 `.app` + dmg，Developer ID 签名、`notarytool`
  API Key 公证并 staple；CI 自动完成。
- **无密钥自动更新。** 应用直接读取 GitHub Release，按平台选择更新归档并校验
  `SHA256SUMS.txt`；设置「更新」页可检查、下载，并在下次启动安装；命令面板新增
  「软件更新」入口。更新不需要任何更新签名密钥。
- **单一 logo 源。** 新增 `logo/logo.svg` / `logo/logo-dark.svg`；所有图标（Windows exe、
  安装器、macOS icns、Linux desktop/AppImage、UI mark）都由 `scripts/gen-icons.sh` 在构建时派生。
- **发布自动化。** 打 `vX.Y.Z` 标签后 CI 自动构建三平台、生成 `SHA256SUMS.txt` 并发布
  GitHub Release（见 `docs/RELEASE.md`）。

## [0.2.2] - 2026-11-10

这个补丁版继续收拾会话阅读区：

- **同一回合的多步过程只显示一条。** 以前 AI 连续执行多个步骤（每一步一个工具调用）时，会出现好几行
  一模一样的「过程 · 思考 · 1 个工具调用」。现在同一回合的连续步骤合并成一条摘要（工具数合计），
  默认折叠，只把**最终结果**展开显示；点开摘要可以看到每一步的完整过程。
- **最终回复底部加了「复制」按钮。** 最新一条 AI 回复的正文下方有一个常显的复制按钮，点一下即可复制
  这条回复，不用再打开 ⋯ 菜单；⋯ 菜单里的「复制消息」保留，两者复制的文本一致。

升级方式不变：下载覆盖运行，原有设置与会话保留。

验证：`./scripts/verify.sh --no-smoke` **10/10 PASS**（含全量测试与 `-race`、回归测试与界面快照）。

## [0.2.1] - 2026-11-10

这个补丁版做两件界面上的小修：

- **消息的时间和操作挪到上面了。** 以前每条 AI 回复下面会单独占一行显示“时间 · 用时 · tokens · ⋯”，
  在只有过程折叠行的回复里像一行空白；现在它们直接放在消息顶部：有折叠过程时和
  「过程 · 思考 · N 个工具调用」共用一行，没有折叠时是回复上方的一条紧凑行，省掉一行高度。
- **移除了消息评分功能。** 「有帮助 / 没帮助」以及 `/反馈` 命令删除了；消息 ⋯ 菜单现在只有
  「复制消息 / 重新生成 / 用量明细」。同时删除了导出（Markdown/JSONL）和诊断报告里的评分字段，
  旧版本留下的评分设置行不再被读取或导出，不影响会话本身。

升级方式不变：下载覆盖运行，原有设置与会话保留。

验证：`./scripts/verify.sh --no-smoke` **10/10 PASS**（含全量测试与 `-race`、回归测试与界面快照）。

## [0.2.0] - 2026-11-10

这个版本做了一次大的整理：**模型管理重做**（支持任意服务商、每个模型独立连接、推理等级可以按模型配置），
同时把会话页的阅读体验和界面壳层重排了一遍——看起来更紧凑，内容区更大。

升级方式：下载对应你电脑系统的压缩包，解压后运行即可；原来的设置、会话和 API Key 都会保留。
旧版只有一条全局模型连接的安装，首次启动会自动迁移成模型列表里的一条记录。

### 三个最明显的变化

- **模型列表成了唯一的模型来源。** 设置 → 模型页现在是「模型列表 + 添加模型」：可以添加多个服务商
  （DeepSeek、OpenAI、Ollama、通义千问、Kimi、智谱、硅基流动、OpenRouter、Gemini 或自定义），
  每个模型有自己的 Base URL 和 API Key，可以测试连接、设为默认。旧版内置的默认模型已删除。
- **推理等级可以按模型单独设置。** 以前是会话里一个统一值；现在每个模型有自己的默认推理等级，
  对话里还能给「这个会话的这个模型」单独改，默认跟随模型。输入框里的菜单改成「模型 / 推理等级」
  两级，不会把模型列表和强度堆在一起。
- **会话页更紧凑、内容区更大。** 消息的时间/用时/用量收成一条紧凑信息行（用量用 K/M/B 缩写）；
  思考和工具调用合成一条可展开的「过程带」，前面有标记区分思考还是工具调用；标题和
  对话/轨迹/记忆标签提到顶层（去掉返回按钮和 Alt+←）；目标条、审批卡移到输入框上方；
  通知改到右上角；默认窗口从 1400×900 放大到 1512×944，正文阅读列从 840 加宽到 960。

### 模型与推理

- 模型页：模型列表（每模型的服务商/Base URL/API Key/上下文/思考模式/生成参数编辑）+ 添加/编辑弹窗
  （常用服务商预设、密钥继承、测试连接）+ 默认模型；显式空列表按「没有模型」处理，发送前提示去配置。
- 旧单连接自动迁移：`chat.model` / `chat.baseURL` / `chat.apiKey` 一次性写成模型列表行；
  旧的会话级思考设置只在旧会话里回落，新的选择按（会话 × 模型）保存。
- agent：每个模型按自己的连接发请求；子代理/团队各自使用配置的模型连接；没有模型时禁止发送并给出
  可操作的提示，而不是静默失败。
- 对话 composer：模型/推理等级两级菜单、模型按服务商分组；首页 composer 用同一入口配置新会话默认值。

### 会话阅读与操作

- 每条消息末尾一条右对齐紧凑信息行：`时间 · 用时 · tokens · ⋯`；⋯ 菜单提供复制消息/重新生成/
  有帮助/没帮助/用量明细，用量全部用 K/M/B 紧凑单位，不再显示一长串原始数字。
- 过程活动带：思考与非决策工具行带类型标记、动作名、状态和预览；完成且过程至少两行时折叠成
  「过程 · 思考 · N 个工具调用 · 用时」，点击展开恢复完整时间线；流式时始终展开。
- 顶层顶栏：会话标题/工作区、运行中、团队、⋯「会话操作」（复制会话/导出/归档）、对话·轨迹·记忆标签；
  没有返回按钮、没有 Alt+← 历史回退，左侧栏就是导航。

### 界面与窗口

- 目标条和审批卡固定在输入框上方：审批不再居中弹窗，改成带警示横幅和描边的内联卡
  （拒绝/本会话始终允许/允许；Esc 拒绝最早一条，不打断正在生成的回答）；在其他页面遇到审批会自动切回会话。
- 通知固定右上角、顶栏下方，最多显示两条，不遮挡输入区。
- 默认窗口 1512×944；正文阅读列 960，两侧留白更少；窄窗口仍按比例收缩。

### 这次发布怎么验证的

- `./scripts/verify.sh --no-smoke`：**10/10 PASS**（gofmt / build / vet / designlint / design-check /
  mygo vet / 全量测试 / -race / 回归 / 界面快照）。
- 全量测试 47 个包通过，`-race` 320 秒通过；回归测试 **289 PASS / 0 SKIP / 0 FAIL**；
  界面快照 **97 步 / 95 张**；设计检查 0 新增违规。
- 覆盖：模型列表/连接/迁移/思考覆盖、审批内联卡与 Esc 语义、通知位置、顶栏与窄窗/深色主题、
  过程带折叠；六个安装包仍覆盖 Windows / macOS / Linux（x86-64 与 ARM64），每个包都有 SHA256 校验。


## [0.1.4] - 2026-11-10

这个版本**不加新功能，专门修界面**：把上一轮全面检查发现的 37 个问题修好了（另有 1 个
影响很小的性能项按现状保留）。一句话总结：**更稳、不卡、不容易误操作，中文界面更完整**。

升级方式：下载对应你电脑系统的压缩包，解压后运行即可；原来的设置和会话会保留在用户
数据目录里，不会丢。不需要改任何设置。

### 三个最明显的变化

- **排队发消息失败时，窗口不会一直“空转”了。** 以前在 AI 回复过程中先排一条消息，如果
  发送失败，窗口会不停重试、持续刷新，电脑发热，极端情况下还可能重复发送；现在失败后
  只提示一次，排队暂停，点「重试」才会再发。
- **用键盘浏览时不会再被“弹走”。** 在设置的「归档」列表里按方向键、或在右侧文件列表里
  上下移动时，以前会突然打开会话或进入文件夹；现在方向键只负责选择，按 Enter 或双击
  才会打开。
- **小窗口下首页能看全了。** 窗口拉到最小（900×600）时，「最近会话」以前会被窗口底边
  裁掉而且滚不动；现在首页可以正常滚动，放大字号时也不会再被截断。

### 更不容易被界面“坑”到

- 团队和任务列表里被选中的那一行，文字和状态标签现在看得清了（暗色模式尤其明显）。
- 右侧「变更」面板点刷新后显示的是最新内容；切换工作区后也会自动跟着切，不会拿旧项目
  的改动给你看。
- 右侧「文件」面板刷新时会有转圈提示、按钮暂时变灰，不会让人以为没点到。
- 设置里填了一半的表单（MCP、LSP、预设、记忆等），按 Esc 或点关闭会先提醒「有未保存的
  修改」，不会悄悄清空。
- 打开“删除/清空”确认框时，窗口不再持续高刷新率重绘，更省电也不容易觉得卡。
- 命令面板（Cmd/Ctrl+K）每次打开都能看到当前选中哪一条；在设置界面里也能正常打开。
- 暗色主题下，选中会话行上的「运行中 / 置顶」小标签不再糊成一片。
- 插件页的工具搜索支持中文了（例如搜「记忆」能搜到），也不会再搜出看起来无关的工具。

### 更顺手、更一致

- 右侧「日志」面板在 AI 连续输出时不再卡顿（改成合并刷新，并且不占用界面线程）。
- 中文界面里的系统自带文案补齐：代码块按钮显示「复制」，读屏软件读分隔条时是「分隔条」。
- **Windows 用户**：右侧终端会自动使用 PowerShell 或 cmd，不再出现 “no shell available”。
- 首页和「新对话」用了不同图标；侧栏折叠后也能直接新建对话。
- 模型菜单不再把同一句说明显示两遍；英文界面的快捷键提示不再被截断。
- MCP 添加服务器的表单顺序改成「先填字段，再测试连接/添加」；数据与隐私页的主次按钮
  更清楚，不容易点错。
- 运行参数里的「使用默认」现在真的不会改你的设置；成功提示会在你下次进入页面时消失。
- 其它小修复：任务板输入标题后按 Enter 就能添加；团队活动时间线的时间不再折行；通知的
  关闭按钮更容易点；记忆整理失败时会写清楚原因并提示重试。

### 这次发布怎么验证的

- `./scripts/verify.sh --quick`：**9/9 PASS**；全量测试 42 个包全绿，回归测试
  **287 PASS / 0 FAIL**，界面快照 86 张。
- 上一轮检查用的复现工具重新跑了一遍：排队失败重试从「8 帧 7 次」降到「1 次」；
  中文工具搜索从「0 个结果」变成正常命中；归档页按方向键不会再跳出设置。
- 六个安装包覆盖 Windows / macOS / Linux（x86-64 与 ARM64），每个包都有 SHA256 校验。

## [0.1.3] - 2026-11-09

这个版本主要做两件事：把界面统一起来、减少误操作；同时给“AI 改界面”立规矩，避免以后越改越乱。

### 界面更统一、更好用

- 首页和会话页的输入框统一：宽度、边框、发送按钮和快捷键一致；回车或 Ctrl/Cmd+回车发送，Shift+回车换行。
- 工具调用卡片统一：普通工具用一行摘要，点开看输入和输出；计划、审批等关键卡片更醒目；失败时自动展开。
- 状态颜色统一：已取消不再显示成红色“失败”；团队成员的“空闲 / 运行中 / 完成 / 失败 / 中断”一眼能分清。
- 图标按钮统一：大小和点击范围一致，鼠标悬停有反馈；收起侧边栏后，顶部按钮也不会“看起来没反应”。
- 团队和任务更好找：会话页头、右侧检查器都能打开团队和任务板；任务按状态分组，显示负责人和依赖。
- 首页输入框宽度与会话页一致，窄窗口下发送按钮不再被挤出屏幕。

### 更安全、更少误操作

- 审批弹窗没有默认按钮，回车不会误点“允许”或“拒绝”。
- 删除会话、删除任务、关闭队友等危险操作，默认选中“取消”，按回车不会误删。
- 计划正在审阅时，按 Esc 不会误取消；只有明确点“取消计划”才会取消。
- 工具调用过程更透明：能看到工具名、状态、输入和输出（可复制），失败原因更清楚。

### 给 AI 改界面立规矩

- 新增一份“界面设计契约”，写清楚什么时候该用哪个组件、颜色 / 间距 / 字号只能用预设值、哪些写法禁止；AI 改界面前必须先查。
- 加入自动检查：AI 写了违规的颜色、尺寸或重复组件，检查会直接报错；以后界面不容易被改乱。

### 其他修复

- 消息时间、用量显示等小问题修复；切换会话不再残留错误提示。
- 修复了极端情况下计划审阅卡住的问题。
- 修复了 Windows 终端输出、窄窗口布局等一批细节问题。

本次发布前通过了完整回归测试：13 项检查全部通过、287 项回归测试通过、6 个平台构建校验通过。技术细节见仓库文档 `docs/DESIGN.md`、`docs/DESIGN-LINT.md`、`docs/AGENT-SURFACES.md`。

## [0.1.2] - 2026-11-09 · 交互修复轮

本轮针对真实使用反馈修复工作区 / 审批 / 输入区 / 用量显示 / Windows 编码，并加入
DSH 风格的按工具系统提示注入与更宽松的默认权限记忆。

### Added

- **按工作区新建会话（⊕）**：侧栏每个工作区分组行右侧新增「在“X”中新建会话」按钮
  （当前工作区常显，其余悬停/聚焦/键盘聚焦时出现；焦点在行上时 Space 触发）。点击会
  打开该工作区、在其中创建新会话、展开分组并进入会话页——新建工作区后可以立刻开始对话。
  全局「新对话」也会展开当前工作区的分组。
- **DSH 风格的系统提示注入**：每次运行都会重建系统提示，开头是身份 + 运行环境（真实
  OS/架构、应使用的 shell、工作目录与文件策略、当前审批策略），结尾按已注册工具逐条注入
  用法提示（read_file/list_dir/glob/grep/write_file/edit_file、pwsh/bash、terminal、jobs、
  web、subagent 等）——模型从一开始就知道这是 Windows（用 pwsh，不存在 bash），文件读写
  优先用专用文件工具而不是 shell 命令。子 agent / 团队成员共用同一份提示。
- **会话级「本会话内始终允许」**：审批弹窗的中键改为「本会话内始终允许」，选择后该工具在
  本会话后续运行中不再询问（持久化到 settings，删除会话或从会话菜单「清除自动允许的工具（N）」
  可清除）。弹窗在多个请求排队时显示「还有 N 个待审批请求」，工具名显示为本地化名称。
- **默认「工作区」权限放宽**：工作区模式现在自动放行工作区文件工具与只读/信息类工具
  （working_directory、read_image、memory、skill(s)、settings_get、todo*、team_task_list/get、
  terminal_list、job_* 读取、session 查询、lsp_* 等）；shell、网络、MCP、settings 写入与未知
  工具仍会询问。两处权限判断共用 `contracts.WorkspaceAutoAllowedTools` 单一清单。
- **会话底部状态条（DSH 风格）**：输入区下方新增一条安静的单行状态：`N 轮 · M 步 ·
  X tok/s · 上下文 U/窗口 · 缓存命中 P% · 剩余 Q%`（数据不准确/未知时省略对应项，Tooltip
  给出精确值）。上下文用量改用 `MessageUsage.ContextTokens`（最后一次模型调用的 prompt
  token 数，由新增的 rig.EventUsage 采集），不再拿累计计费 token 或压缩预算当上下文窗口。
- **Windows 命令行输出编码修复**：新增 `internal/lib/textenc`，在工具代理层与右侧终端
  统一把 GBK/cp936 / UTF-16LE 输出修复为 UTF-8（合法 UTF-8 原样通过）；中文 Windows 上
  `python …`、`cmd` 等输出不再乱码。
- **后台任务完成通知真正送达模型**：toolkit 的 root-scoped 工具代理实现 rig `NoticeSource`
  并在每个工具轮次后向 agent 排空 `jobs.Manager.PendingNotices()`，agent 侧工具包装器透传该
  接口；后台 job 结束会以 `Background notifications:` 形式进入下一轮上下文（此前文案承诺
  “会收到通知”但从未接线，真实会话里 6 个后台 job 全部滞留且模型一直在等通知）。
- **重复工具调用提醒（DSH loop hygiene 移植）**：agent 每 run 跟踪「工具名 + 规范化参数」
  连续相同调用，在第 3/5/8 次时在结果尾部附加 DSH 原文建议（只提示不阻断、todo 记账调用透传、
  新用户消息清空链）。
- **会话底部状态条（DSH 风格）**：新增 `p2-running-composer.png` / `p2-status-strip.png`
  截图记录「生成期间输入框常驻」与「轮/步/tok/s/上下文/缓存命中/剩余」状态条。

### Changed

- **新建工作区 = 直接选择目录**：点击「添加工作区」直接打开原生目录选择器，不再需要命名
  对话框；名称由后端按目录名推导，选中已注册的目录视为切换工作区；成功/失败用 toast 或
  标题行状态/错误行反馈。
- **去掉工作区「视图」菜单**：产品固定为「按工作区划分 + 会话最近活动排序（组内置顶优先）」，
  不再有排序/平铺开关与 `sidebar.sort`/`sidebar.group` 设置行；工作区分组按各自最新活动排序
  （空工作区按创建时间），新建工作区会出现在顶部；没有工作区后端时才回退到单一平铺列表。
  侧栏工作区树在零会话时也始终渲染分组行与 ⊕（此前空状态会隐藏整棵树）。
- **会话输入框在生成期间保持可见**：发送消息后输入框不再消失；排队发送默认开启
  （`ui.sendWhileRunning` 仅显式 `"false"` 关闭），运行中显示「排队 + 停止」，草稿保留；
  排队数据按会话保存，切走/切回不丢，由模块订阅在回合结束时发送给所属会话；「停止」会暂停
  排队发送而不是立刻把队列发出去。
- **用量数字改为紧凑单位**：`用时 358.8s · 173.6K tokens`（K/M/B，保留一位小数），不再显示
  `173,635` 这类长数字；展开明细同样使用紧凑单位，精确值在 Tooltip。
- **审批弹窗不再有默认操作**：此前「拒绝」是默认动作且打开即聚焦，用户刚按 Enter 发送消息、
  弹窗随即出现时，二次 Enter 会误拒工具调用（真实 Windows 会话导出里出现过一次 pwsh 被误拒）。
  现在不自动聚焦任何按钮、Enter 不做决定；Esc 仍显式拒绝，允许/拒绝都需要点击或 Tab+Enter。
- **工具指引增强**（来自真实会话复盘）：pwsh 行补充 Windows `Start-Process` 启动的是分离子进程
  （服务器型任务用 `run_in_background`、记 PID 并 `Stop-Process`）；jobs 行改为 DSH 原文并
  提醒服务器型 job 不会自己结束、别留多份。
- **全仓手搓 UI 审计后的官方组件化（第三轮）**：独立审计逐条核对 mygo v0.8.3
  文档后，把剩余 21 处真正手搓的 UI 迁移为官方组件（审计/证据：
  `.cache/ui-audit/report.md`、`.cache/migrate-shell/report.md`、
  `.cache/migrate-settings/report.md`）：
  - 设置页 7 个集合（记忆/模型目录/MCP/LSP/提示词预设/代理预设/诊断模块）由
    `for…range`+`Divider` 改为官方 **`ui.List`**（Key/Label/Selected/Rows，行内按钮保留，
    重排后 Key 跟随；行名/键盘/type-ahead 由控件提供）；
  - 右栏**产物历史**与 **composer 排队弹层**改为 `ui.List`（前者 Enter/双击展开，
    后者有界高度、逐条取消保留）；
  - Files **面包屑**改为官方 **`ui.Breadcrumbs`**（navigation landmark、Link 角色、
    CurrentPage、点击/Enter 导航、长路径 ellipsis；深路径保留 first→…→last two）；
  - **会话重命名对话框**补齐 `uikit.DialogFooter` + `AutoFocus` + `EnterTo`（全仓不再有
    手写 footer 的对话框）；
  - 消息评分与 /feedback 改为官方 **`ui.Toggle` / `ui.ToggleGroup`**（pressed 语义；
    `ui.Segmented` 无法表达“未评价”，偏差与备选方案已记录）；
  - 8 处文字忙碌态（更新检查/下载/安装、导入扫描/导入、模型连接检查、记忆提取、
    附件选择/压缩）改为官方 **`ui.Spinner`**（不伪造 Progress 百分比）；
  - 记忆 pane 的 Unicode `✎` 改为新增的 Lucide **`icons.Pencil()`**；设置「未保存」
    裸文本改为官方 `ui.Badge`；
  - 保留自绘项均有 `custom-widgets.md`/headless base 依据并列入审计报告：composer 卡片、
    slash/@ 富行建议菜单、命令面板按钮行、消息 hover 动作行、无边框图标按钮、tabs
    下划线、思考/工具行 trigger、toast parts、附件 chip 等；`ui.Rating`/`ui.Table`/
    `ui.Progress`/`ui.FindBar`/`ui.Accordion` 等经核对语义不适用，明确不“为用而用”。
- **会话过程展示改用 mygo 官方组件**（推理/思考、工具调用）：
  - 思考块由「`ui.Collapsible` + 另一行手搓的 `思考中…` + 原始 tail 预览 + 纯文本正文」改为
    **单个 `ui.CollapsibleBase`**：流式时 trigger 为官方 `ui.Spinner` + 「思考中…」，结束后为
    思考图标 + 「思考」+ 折叠行数；chevron 由 `parts.Progress()` 驱动；正文用官方
    `ui/markdown.Stream`/`View` 以更小更淡的 Markdown 主题渲染（不再有「思考中… ile.」这类
    原始 tail 文本，也不再重复渲染）。
  - 「生成中」指示器由 `ui.Badge` + 静态 Sparkles 图标改为官方 `ui.Spinner`（带无障碍 label）。
  - 工具调用行由手搓 chevron `ui.Button` + 原始 snake_case 工具名改为 **`ui.CollapsibleBase`
    整行 disclosure**：图标 + 本地化友好工具名 + 状态（运行中显示 `ui.Spinner`）+ 单行预览 +
    `parts.Progress()` chevron；原始工具 id 只出现在 Tooltip/无障碍描述里。H15 默认展开规则、
    每行折叠状态、`HoldPlace` 保持。
  - 决策卡与轨迹 Tab 的 `ui.Collapsible`、正文 `markdown`、会话 `ui.List` 本来就是官方用法，
    保持不变；`ui.Accordion`（设置类多分区）与 `DiffViewer`（读模型没有 diff 数据）经审计后
    **不使用**，原因记录在 `.cache/transcript-ui/report.md`。
- **删除当前工作区后的回退**：删除当前工作区时自动选择剩余工作区中最近创建的一个作为当前
  工作区（原子持久化，失败回滚），避免工具根变空后文件/shell 工具静默失败。

### Removed

- 侧栏工作区视图菜单、名称排序/平铺选项及其 `sidebar.sort` / `sidebar.group` 设置键
  （旧的遗留设置行会被忽略，不再写入）。
- 添加工作区命名对话框与「选择目录…/名称/目录」表单字段。

### Fixed

- 新建工作区后无法直接开始对话（重做 ⊕ 动作 + 空会话树渲染）。
- 中文 Windows 下命令输出乱码（见上）。
- Windows 上模型误用 bash（平台感知提示 + Windows 不注册 bash；非 Windows 仅在 PATH 中确实
  存在 pwsh 时才注册）。
- 会话切换会静默丢弃排队消息；生成结束时若不在会话页排队消息不会发出；停止后队列被立即发送。
- 审批弹窗工具名显示英文原始 id、多请求无计数提示。
- 空白标题会话在英文界面无法用 “Untitled” 搜索到。
- 切换工作区时旧工具构建（及其后台 job / terminal 进程）一直留到进程退出：现在在最后一个
  in-flight 调用结束后释放（不中断正在运行的调用）。
- 「停止」被归类成普通错误（`errorKind=other`，UI 显示原始 `cancelled`）；现在映射为本地化的
  「已取消」。
- 一个长且不含空格的工作区名会把该行的 ⋯/⊕ 挤出 280 DIP 侧栏：名称文本现在可收缩
  （`MinWidth(0)`），行内操作仍在栏内且可点击。
- Esc 关闭斜杠菜单后清空输入再输入同样的 `/`，菜单不会重新打开（提示词挂起状态残留）：
  输入为空时清除该状态，并提供回归测试。
- 极短回合（<250ms，如脚本化/取消回合）不再显示五位数 `tok/s` 这种无意义的平均速度。

### Verification (2026-11-09, v0.1.2)

- `./scripts/verify.sh` (full): **10/10 PASS** — gofmt, build, vet, full test
  suite, `-race` (384s), regression **287 PASS / 0 SKIP / 0 FAIL**, UI
  snapshot **83 steps / 81 PNGs**, desktop (Xvfb, tray) / headless / ACP
  smokes green; **535s** total.
- `go test ./...` 41 packages PASS; `go build ./...` and
  `GOOS=windows go build ./...` PASS; `go vet ./...` clean.
- `internal/devtools/ui-drive`: **84/84 PASS** (0 fail / 0 warn) on the final
  tree, including the new workspace ⊕, transcript reasoning/tool rows and
  approval no-default steps.
- Independent audits and evidence:
  `.cache/ui-audit/report.md` (hand-rolled UI audit),
  `.cache/migrate-shell/report.md` / `.cache/migrate-settings/report.md`
  (official-component migrations), `.cache/session-debug/report.md` (real
  Windows session export replay), `.cache/audit-round2/verification.md`.

---

## [0.1.1] - 2026-11-09

Second public ZeoKeel release: the UI/UX conformance pass against mygo v0.8.3,
the settings consolidation, denser and virtualized transcript/tool cards, dead
code and legacy archive removal, and the zeokeel-rig v0.13.2 SDK upgrade.

### Changed

- **Rig SDK v0.13.2**: `github.com/zeokeel/zeokeel-rig` v0.13.1 → v0.13.2, whose
  Windows command-allowlist comparison now ignores the `.exe` suffix and case. The
  host shell allowlist therefore lists bare `pwsh`/`powershell`; the temporary
  `.exe` entries added as a v0.13.1 workaround are gone. No other behavior change.
- **Settings consolidation**: the separate 模型目录 (Model catalog) page was
  merged into the 模型 (Model) page — one place now owns the provider
  connection, the model list, thinking-family/mode editing, custom modes and
  per-model generation parameters. The page title stays 模型; the standalone
  catalog page and its navigation entry are gone. The merged page keeps the
  connection form's footer Save and immediate model-list edits, with the
  editor's generation/custom-mode groups behind one 生成与自定义模式 disclosure.
- The provider quick-preset "自定义" chip was renamed to 手动配置 / Manual setup
  to distinguish it from the thinking-family "自定义" (custom) option.
- **Transcript order**: assistant text and tool cards now interleave in event
  order (`ToolCall.TextOffset`), so a reply that called tools keeps its
  pre-tool text above the calls and its final text below them instead of
  drawing the whole reply above the tool cards.
- **Quieter tool calls**: transcript tool cards are now compact one-line rows
  (muted icon/name, subtle status, one-line result preview and a small 详情
  disclosure) instead of full-width panels; decision tool cards (plan review,
  exit plan, ask user) stay prominent and the trajectory pane keeps its
  detailed cards.
- **Default permission is now workspace-level**: an unset `agent.permission`
  resolves to workspace everywhere (desktop assembly, approvals gate, session
  menu, behavior page). Workspace mode auto-allows the workspace file tools
  (read/write/edit/str_replace/list/glob/grep) without asking; shell, network,
  MCP and unknown tools still ask. The composer label stays quiet
  (工作区权限/Workspace) while full/never and non-default chat modes keep the
  warning label.
- **Unlimited iterations by default**: the agent assembly no longer sets
  `maxIterations: 20`; 0 means no practical limit (the run passes an effectively
  unreachable cap to Rig, which otherwise defaults to 20). The model page's
  runtime summary shows 不限制/unlimited when unset; a numeric value still caps
  the run.
- **mygo v0.8.3**: upgraded from v0.7.4 and adapted to the stricter Tester
  hit-testing, the richer menu APIs (model menu now uses `Menu.MaxWidth` +
  `MenuItem.Subtitle`/`Trailing` for model notes and context windows), the
  official `NewTesterWith`/`Tester.AccessByRole` test APIs, and GitHub
  alerts/mentions/references in Markdown. Hand-written import/skill input
  accessibility labels now follow the shared `inputLabel()` pattern.
- **Domestic Go module mirror**: the Makefile, `scripts/verify.sh` and CI
  workflows default to `GOPROXY=https://goproxy.cn,direct` with
  `GOSUMDB=sum.golang.org`; offline builds can override both to `off`.

### Removed

- **Legacy archive**: the 389 MB pre-rewrite `old/` module (836 tracked files)
  was removed from the repository, together with its `.gitignore` rules and
  the obsolete rebuild-plan/status docs that pointed at it. Historical design
  research remains in `docs/research/`.
- **Dead code**: unused handlers, helpers, test fakes and legacy constants
  found by `deadcode -test` / `staticcheck U1000` were removed (host tray
  helpers, old agent-preset picker remnants, toolcard color helpers, session
  goal-menu state, the unused diagnostic-export seam, run-command helper and
  stale test harnesses), while keeping the public contracts and behavior
  intact.

### Fixed (UI conformance, mygo v0.8.3)

A full UI/UX conformance pass against `mygo v0.8.3/docs/ui` and the
`design-guide` skill; the inventory and the rules live in
[docs/UI-CONFORMANCE.md](docs/UI-CONFORMANCE.md).

- **Shell**: the sidebar and the inspector are native `ui.Split` panes with
  persisted widths and `SplitReset`; the inspector draws as an accessible
  modal drawer (`RoleDialog`, Escape, focus trap, scrim token); the window menu
  presentation is unified with `Context.SetMenuPresentation(ui.PopoverMenus)`;
  the top band is a `ui.Toolbar` with visible command-palette and inspector
  entries; the palette list has a name and `ActiveDescendant`; screen changes
  and the inspector announce to assistive technology; chrome toggles ignore
  key auto-repeat; the rail is a keyboard `FocusGroup`; a back affordance and
  Alt+Left follow the navigation stack.
- **Brand theme**: `Theme.InactiveAccent` keeps the brand accent in inactive
  windows (matching the hand-drawn accent surfaces, locked by a test);
  `design.Of` wires the desktop text-size preference into `Tokens.TextScale`
  and the root theme scales its `FontSize` by the same factor, so native
  controls and brand-drawn text grow together (both covered by tests).
- **Shared kit**: `uikit.DialogFooter` supports a default (focused, Enter)
  action; `PageHeader`/`Section` titles are headings; `ErrorLine` is an alert
  live region with a real icon and selectable text; `KeyValueRows` values are
  selectable; one shared `uikit.EnterTo` and one shared `uikit.ToolIcon` mapping
  replace the per-module copies.
- **Right inspector**: the Changes pane renders the official
  `editor.DiffView` (word-level inline changes, line numbers, hunks) with a
  bounded text fallback for unparsable patches; Files/Log/Tools/Tasks/Agents
  are native `ui.List`s with keyboard selection and row a11y; the terminal
  shell picker is a `ui.Select` and its output follows the end; the directory
  listing and the session log are cached and refreshed explicitly instead of
  reading disk/SQLite every frame; empty states, file/image icons and
  selectable previews follow the guide.
- **Sidebar**: the workspace tree is a native `ui.Outline` (arrow keys open and
  close), session rows are a `ui.List` with `Key`/`Label`/`Selected`; right
  click and the row's ⋯ menu share one menu built with `ContextMenu` +
  `PopoverMenuButtonOptions`; destructive items use `MenuItem.Danger()`; the
  add-workspace dialog uses the shared footer with Enter submission.
- **Session**: the goal chip uses the official `PopoverMenuButton` menu
  (Danger items, stable IDs) instead of a hand-drawn popover; permission, chat
  mode, thinking, model and preset choices are `MenuItem.Radio` (single-choice
  semantics) with model `ID`/`Subtitle`/`Trailing`; the slash/@ suggestion
  popups are named list boxes with option roles, positions and an
  `ActiveDescendant`; dialogs accept Enter, and dialog widths use the design
  tokens; the English session menu is "Session settings" so it no longer
  collides with the global Settings.
- **Settings**: the memory page caches its read model (no per-frame SQLite),
  and add/edit/tidy/clear/extract invalidate it; dialogs submit on Enter
  (model catalog, presets, memory, policy); the data & privacy page makes
  exporting the primary action and opening the data directory secondary;
  diagnostics use localized unknown values; the stale English subtitle is
  fixed; settings dialog widths use tokens.
- **Other screens**: Home recent sessions are a `ui.List` with localized
  dates; Automation summaries are localized, truncated and hide internal
  session IDs, its enable switch and filters use Switch/Radio; Plugins show a
  localized tool summary with the original text in the tooltip and condense
  MCP errors; Approval uses `uikit.DialogFooter` with Deny as the focused
  default (Enter = deny); toasts use `ShadowOverlay` and grid tokens; icons for
  image/file reads are the correct glyphs, and an icon invariant test locks the
  SVG grid/stroke rules.
- **Official collections**: every remaining collection is a native `ui.List`;
  the model catalog editor's custom thinking-mode rows and the help dialog
  command list were the last two `Scroll`+row loops and now virtualize/scroll
  with the official list (keys follow the mode ID, falling back to the row
  while a new mode has no ID).
- **Bounded transcript**: a conversation builds only its newest 200 messages by
  default (a "load earlier messages (N)" row reveals the hidden range in
  steps), so streaming a reply no longer re-lays out thousands of rows in a
  long session; the test covers the tail window and the load-earlier step.
- **Number inputs where the official control fits**: the memory consolidation
  policy (max entries / max retention), the model runtime limits (max
  iterations / context budget, each with a "use the module default" switch that
  clears the settings row), the model context-window fields and the model
  generation parameters (temperature, Top P, max output tokens — each with a
  "use the provider default" switch) are native `ui.NumberInput`s: stepper
  buttons, arrow keys, range clamping and immediate application. The page tests
  were rewritten to the control's range semantics with one shared
  `typeNumberInput` helper, and the old free-text "must be a number / in range"
  error paths are gone.
- **One action-placement rule for settings**: `uikit.PageHeaderAction` and
  `uikit.SectionAction` put a page's immediate-effect actions (new skill,
  refresh, check updates) on the page header row and a section's utility
  actions (restore presets/defaults, export the diagnostic report) on the
  section heading row. The explicit-save pages keep their single primary action
  in the host footer, so the old mix of bottom-of-content and right-aligned
  action rows is gone; frame tests pin the placement and the migrated pages'
  tests pass.
- **Dead injection points removed**: the contract's `SlotTopBarBrand/Center/
  Actions` and `SlotOverlaySettings/Palette` constants, the welcome module's
  top-bar marker registration and the corresponding test assertions are gone.
  The shell has no top bar (screen headers plus the content-top toolbar own the
  global actions); the architecture/injection-point docs now say so instead of
  inviting modules to register slots the host never renders.
- **Native dialogs take their title as an argument**: the package-level dialog
  title variables (workspace folder, session export markdown/JSONL, data export
  and diagnostic report) are gone; every picker/saver seam now receives the
  frame's localized title, so two windows in different languages cannot race a
  shared global. Tests capture the title from the seam argument and one
  regression probe now checks both languages instead of skipping a known defect.
- **Transcript is a native List**: the conversation transcript now uses the
  official `ui.List` (`ListState.Key` by message ID, `Label`, `FollowEnd` for
  streaming, `HoldPlace` when a fold opens, `ScrollToEnd` behind the floating
  back-to-latest action) instead of a `Scroll` plus a bounded message loop, so
  only the visible rows build and long conversations stay fast. The help
  dialog's command list and the trajectory timeline use `ui.List` as well.
- **Brand new-chat button**: the sidebar's New chat action is the official
  `PrimaryButton` (theme accent + native hover/pressed/focus states) instead of
  a hand-drawn `ButtonBase`; its icon+label stay the button content and the
  label remains the accessible name.
- **Remaining collections are Lists**: the sidebar's message-search hits, the
  settings archive rows and skill cards, and the Plugins page's MCP servers,
  built-in modules, curated settings and tool catalogue all use official
  `ui.List` states (stable keys, named rows); the slash/@ suggestion popovers
  stay custom because their rich rows need the documented listbox/option
  accessibility model rather than a plain text autocomplete.
- **Read-only code preview**: Files' source previews use the official
  `editor.View` in read-only mode (syntax highlighting by extension, line
  numbers, soft wrap, virtualization) instead of a plain mono text block; the
  tests read the content from the editor's accessibility text value.
- **Agent preset tools are a TokenField**: the comma-separated tools text
  input is the official `ui.TokenField` (tokens as chips, Enter/comma to add,
  Backspace to remove), and the preset still persists `[]string`.
- **More native Lists**: the Memory tab's entries and the Automation screen's
  scheduled tasks are official `ui.List`s (keyed by ID, named rows, virtualized
  scrolling) with the search/tools/add rows fixed around them, so the last
  hand-drawn scrolling collections are gone.
- **Settings navigation follows the page**: the low-frequency 高级 group now
  unfolds for an advanced page and folds again when a daily page is selected,
  so the settings navigation no longer keeps the previous visit's longer
  layout (the snapshots showed both widths depending on the order pages were
  opened). A frame test covers the group folding after a daily page is chosen.
- **Bounded per-message UI state**: switching conversations drops the fold /
  usage / unfolded-actions / transcript-window entries of the conversation that
  no longer shows (one internal test covers the pruning), and sent attachments'
  decoded thumbnail caches are released, so a long-running app does not grow
  state for every session it ever opened.
- **Grouped subprocess fields**: the MCP add form's STDIO fields are a native
  `ui.Fieldset` with a legend, so assistive technology reads the group with
  every field inside it.

### Verification

- `./scripts/verify.sh` (2026-11-09): **10/10 PASS** — gofmt, build, vet,
  full test suite, `-race`, regression **284 PASS / 0 SKIP / 0 FAIL**, UI
  snapshot (81 steps / 79 PNGs), desktop (Xvfb) / headless / ACP smokes green.
- All six release archives cross-compile with `CGO_ENABLED=0`
  (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64,
  windows/arm64); SDK dependency is `github.com/zeokeel/zeokeel-rig v0.13.2`.

## [0.1.0] - 2026-11-08

The first public ZeoKeel release: a compile-time modular AI agent for the
desktop and the headless/ACP worlds, written in Go and drawn with native mygo
controls (no webview, no browser engine).

### Added

- **Compile-time modular architecture**: one binary, assembly products
  (`desktop` / `headless`), a kernel of contribution points, and `assembly.json`
  entries that enable or trim modules without runtime plugin shipping.
- **Desktop product**: native chat UI with streaming answers, reasoning
  preview, tool cards, session history, workspaces, right-side inspector
  (overview/tools/log/files/changes/terminal/team/tasks), command palette,
  keyboard shortcuts, tray/notification integration and global hotkey.
- **Headless + ACP**: the same binary runs `--headless` (with tools, memory,
  sessions, automations) or as an ACP stdio server for editor integration,
  including image prompt blocks.
- **Agent capabilities**: tools (shell/file/read/edit/todo), approvals and
  permission modes (ask/workspace/full/never), agent/chat modes
  (normal/plan/minimal/creator), subagents and team management, goals with
  continuation, plan review, memos/memory, skills, MCP servers, LSP tools,
  prompt/agent presets, session references and context compaction.
- **Model configuration**: providers and quick presets, per-session model
  switching, per-model thinking configuration for Qwen / DeepSeek / other
  families with custom modes, per-model generation parameters
  (temperature / top_p / max_tokens), runtime limits (max iterations, context
  budget, stream-completeness check) and a model catalog editor.
- **Settings surface**: 19 pages in five groups, with low-frequency advanced
  pages collapsed behind one group; progressive disclosure on the model,
  behavior and model-catalog pages; searchable navigation, fixed footer
  actions and unsaved-changes guards.
- **i18n**: Chinese and English UI, localized errors and date formatting.
- **Packaging**: cross-platform release builds with explicit OS + architecture
  asset names — `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`
  (`.tar.gz`) and `windows/amd64`, `windows/arm64` (`.zip` with `zeokeel.exe`);
  version/commit/build-time injected at link time, `CGO_ENABLED=0` pure-Go
  builds stripped with `-s -w`, gzip/zip-compressed archives, `SHA256SUMS.txt`
  and a download table in the release notes; CI on push/PR (including a
  cross-compile check for every release target) and a tag-driven release
  workflow.

### Fixed

- ACP failures are reported as JSON-RPC errors; interrupted turns persist
  partial assistant output and can be regenerated without losing reasoning.
- Oversized tool results spill to disk for both desktop and headless sessions.
- Truncated model streams are reported instead of silently appearing complete
  (zeokeel-rig `RequireCompleteStream`, v0.13.1).
- Attachment picker runs off the UI thread; manual path fallback for
  portal-less environments; per-conversation composer drafts and attachments.
- Composer action row simplified (attach menu, model, session menu, send);
  narrow-window overflow and session-menu accessibility fixes.
- Memory update/edit, model test-connection details, approval copy, and a
  large batch of UI interaction fixes found by independent audits.

### Verification

- `./scripts/verify.sh` (2026-11-08): **10/10 PASS** — gofmt, build, vet,
  full test suite, `-race`, regression **284 PASS / 0 SKIP / 0 FAIL**, UI
  snapshot (83 steps / 81 PNGs), desktop (Xvfb) / headless / ACP smokes.
- Desktop smoke: window + tray + single instance under Xvfb, no panics.
- Model thinking/generation mapping verified by captured request bodies and
  unit tests; SDK dependency is `github.com/zeokeel/zeokeel-rig v0.13.1`.

[0.1.4]: https://github.com/zeokeel/zeokeel/releases/tag/v0.1.4
[0.1.3]: https://github.com/zeokeel/zeokeel/releases/tag/v0.1.3
[0.1.2]: https://github.com/zeokeel/zeokeel/releases/tag/v0.1.2
[0.1.1]: https://github.com/zeokeel/zeokeel/releases/tag/v0.1.1
[0.1.0]: https://github.com/zeokeel/zeokeel/releases/tag/v0.1.0
