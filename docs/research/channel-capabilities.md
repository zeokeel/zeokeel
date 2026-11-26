# 渠道能力核对（官方文档，2026-11-25）

> 用途：给 [开源实现规划](../plans/grokbot-open-source-plan.md) §6.4 与 [交互设计](../plans/grokbot-interaction-design.md) §8.2b 提供事实依据。
> 原则：**只写能指到来源的结论**；媒体实测/社区实测单独标注，不混进官方结论。

## 1. 微信 ClawBot（iLink / openclaw-weixin）

| 项 | 结论 | 来源 |
|---|---|---|
| 是什么 | 腾讯通过 OpenClaw 开放的**微信个人号 Bot 通道**，官方名「微信 ClawBot 插件」，底层 iLink 协议 | [OpenClaw: WeChat](https://docs.openclaw.ai/channels/wechat)、[新浪科技](https://finance.sina.cn/tech/2026-03-22/detail-inhrvnpc5210846.d.html) |
| 实现形态 | 外部插件 `@tencent-weixin/openclaw-weixin`（腾讯微信团队维护），OpenClaw 核心只提供 channel 契约 | [OpenClaw: WeChat](https://docs.openclaw.ai/channels/wechat) |
| 会话范围 | **只支持单聊**；插件能力声明不包含群聊 | 同上（"Group chats are not advertised"） |
| 登录 | 扫码；凭据存在本机 state 目录；可多账号 | 同上 |
| 收 | `getupdates` 长轮询（服务端 hold 约 35s） | 社区实现 [weixin-ClawBot-API](https://github.com/codeenxi/weixin-ClawBot-API) |
| 发 | `sendmessage`（iLink，`ilinkai.weixin.qq.com`），必须带当次消息的 `context_token`；缺字段 HTTP 200 但不投递 | 同上 |
| 24 小时窗口 | **超过 24h 未互动，下发消息被丢弃**（客服式会话） | 媒体实测：[蓝点网](https://www.landian.news/archives/112284.html)、[cnBeta](https://cnbeta.stateserver.com/wap/view/1555234.htm)（**官方文档未写**） |
| 限速 | **官方未公开**；社区实测约 7 条/5 分钟 | [weixin_clawbot (pub.dev)](https://pub.dev/packages/weixin_clawbot) |
| 媒体 | 插件声明支持媒体；但有实测称只能收文件、不能给用户发文件，**未定论** → 我们只做文本 | 同上 + 社区实测 |
| 群 | 不能进群 | [新浪科技](https://finance.sina.cn/tech/2026-03-22/detail-inhrvnpc5210846.d.html) |

**对我们的结论**：微信只做**单聊 + 纯文本**；审批用对话式回复；24h 窗口要做"保活检查 + 改道"。

## 2. 钉钉

| 项 | 结论 | 来源 |
|---|---|---|
| 发送形态 | 群聊酷应用机器人；普通消息 / 互动卡片 / 吊顶卡片 | [发消息概述](https://open.dingtalk.com/document/org/overview) |
| 限速 | 用开放平台 SDK 发机器人消息，**每个会话 200 次/分钟** | 同上 |
| 卡片回调 | 需先**注册互动卡片回调地址**；回调带签名头（`x-ddpaas-signature-timestamp` / `x-ddpaas-signature`，HmacSHA256） | [响应互动消息](https://open.dingtalk.com/document/robots/respond-to-interactive-messages) |
| 长连接 | **Stream 模式**支持：机器人收消息、事件订阅、互动卡片回调；零公网 IP、零内网穿透、TLS 反连 | [Stream 模式概述](https://open-dingtalk.github.io/developerpedia/docs/learn/stream/overview/) |
| 卡片走 Stream | 需要在创建卡片回调时设 `callbackType=STREAM`（开发者后台的 HTTP/Stream 选择不影响卡片回调） | [开发者百科：互动卡片](https://open-dingtalk.github.io/developerpedia/docs/learn/card/intro/) |

**对我们的结论**：**通知**可以用群自定义机器人 webhook（单向）；**接活与审批**必须用**企业内部应用机器人 + Stream**。

## 3. 飞书

| 项 | 结论 | 来源 |
|---|---|---|
| 群自定义机器人 | 只能**发**；**不能收消息、不能响应用户**；卡片按钮**只能 open_url，不支持回传交互（postback）** | [自定义机器人使用指南](https://open.feishu.cn/document/client-docs/bot-v3/add-custom-bot) |
| 自定义机器人限速 | **100 次/分钟、5 次/秒**（单租户单机器人）；body ≤ 20KB；建议避开整点/半点 | 同上 |
| 安全 | 自定义关键词 / IP 白名单 / 签名（`timestamp + "\\n" + secret` 的 HmacSHA256，时间戳 1 小时内有效） | 同上 |
| 长连接 | 飞书 SDK 建 WebSocket 全双工通道，服务器**需要能访问公网**（出站）：事件回调、卡片回调都可用 | [使用长连接接收回调](https://open.feishu.cn/document/event-subscription-guide/callback-subscription/step-1-choose-a-subscription-mode/configure-callback-request-address) |
| 旧版卡片回传 | **"消息卡片回传交互（旧）"不支持长连接**，只能把回调发到开发者服务器 | 同上 |

**对我们的结论**：飞书上"点按钮批准"要么用**应用机器人 + 长连接**，要么退化成"卡片按钮跳链接到本机页面"。

## 4. 邮件

| 项 | 结论 |
|---|---|
| 收 | IMAP；IDLE（RFC 2177）可做准实时到达；线程靠 `Message-ID` / `In-Reply-To` |
| 发 | SMTP 或服务商 API；附件大小/类型受服务商限制 |
| 交互 | 无卡片；审批只能靠"正文关键词"或"深链 + 网页" |
| 安全 | 平台层可配 SPF/DKIM/DMARC 与发件人白名单，但**本产品一个都没实现**：`internal/modules/email/imap.go` 只把 `From` 头原样记进 `Sender`，没有白名单、没有签名、没有头检查。所以正文一律当数据、不当指令；2026-11-26 起 email 也不再是 `replyrelay` 的人话来源（§6） |

## 5. 交叉结论（设计必须遵守）

1. **"通知"与"接活/审批"是两套接入方式**，同一平台可能都要接：webhook/自定义机器人只能发，应用机器人才能收。
2. **微信是单聊 + 24h 窗口**：不能进群、定时结果可能被丢。必须做"最近互动时间"检查与改道。
3. **限速差异极大**（钉钉 200/分钟/会话、飞书自定义 100/分钟、微信未公开且很低）：所有出站都要经队列 + 合并 + 配额。
4. **卡片不等于可交互**：飞书自定义机器人的卡片按钮只能跳链接；真正回调要应用机器人。
5. **未验证项**：微信限速/媒体能力、ClawBot 长期稳定性。上线前要各做一次真机验证（用 §12 的验收清单）。

## 6. 结论（2026-11-26）：钉钉/飞书不做入站

**产品定位改变了结论。** 本文件 §1–§5 是对"能不能接"的事实核对；这一节是**决定**：
ZeoKeel 是**个人助手**，不是公司助手。持有本机数据（工作区文件、记忆、会话、凭据）的 agent
只应该接受**本机拥有者**的指令。钉钉/飞书是公司群聊场景——任何同事 @ 一下机器人，消息就能进
收件箱，再被 Lead 分诊成任务、被 agent 执行。那等于把"谁能指挥我"交给了对方租户的群成员管理，
而我们对群成员没有任何控制权：入站渠道在**权限模型之外**，它绕过的不是某一项检查，而是
"这是一个人的助手"这个前提。

所以两者的入站能力**删除**（不是禁用、不是配置开关），只保留出站：

| 能力 | 钉钉 | 飞书 |
|---|---|---|
| 出站发送（通知、结果回执） | ✅ `Send` | ✅ `Send` |
| 状态查询（设置页显示是否已连接） | ✅ `Status` | ✅ `Status` |
| 入站消息（事件/回调 → 收件箱） | ❌ 已删 | ❌ 已删 |
| URL 握手 / 验签 / 重放窗口 | ❌ 已删 | ❌ 已删 |
| 卡片按钮解决审批 | ❌ 已删 | ❌ 已删 |

由此产生的两个**不是遗漏、而是选择**的结果：

1. **审批不能从卡片点。** 卡片按钮的回调就是一个公开 HTTP 入口，它必须自己鉴权（我们做过：
   验签 + 一次性 token），但"能鉴权"不等于"应该存在"——审批是这台机器上权限面的一部分，
   解决它的入口只留 Web（允许/始终允许/拒绝三值，无默认动作，Esc=拒绝）与微信对话式回复。
   微信这条的**已知风险**（`ApplyReply` 取全局待批队列、按序号、无发送者校验 → 任何人发一句
   「1 同意」就能解掉队首审批；用户已决定微信侧不加校验）写在 [../TRANSPORT.md](../TRANSPORT.md)
   §2.6，是**记录在案的已知风险**，不是"已加校验"。
2. **指令入口只剩 Web 与微信。** 微信是单聊（§1：进不了群），天然是一对一，符合"个人助手"。
   `replyrelay` 的 `channelSources` 里**只有 `wechat`**：
   - **邮件不在里面。** 邮件适配器不校验发件人（`From` 头只是原样记账，见 §4），而 `ApplyReply`
     按序号解全局待批队列——两者一接，就等于"任何人发封正文『1 同意』的邮件就能解掉队首审批"。
     移除成本几乎为零：产品从未注入 IMAP client，邮件入站本来就不可达。
   - **webhook 也不在里面。** 签名密钥认证的是**发送方系统**，不是人；它的文本是机器输入。

   两者都仍然可以进收件箱（`webhook` 有签名密钥，`email` 一旦接线也只是一个数据源），只是**不能回答审批**。

出站照旧走 `notifyroute` + `channeldispatch`（限速差异见 §5.3），钉钉/飞书仍然是通知面的一等渠道。
真机验收清单相应收窄：见 [../plans/channel-verification.md](../plans/channel-verification.md) §2/§3。

## 7. 变更记录

- 2026-11-25：建立；按官方文档核对钉钉/飞书/OpenClaw-WeChat，修正了"群里 @ 机器人"与"自定义机器人能收消息"两个错误假设。
- 2026-11-26：定位改为**个人助手**，钉钉/飞书入站能力下线（只留出站），见 §6。
- 2026-11-26（补）：`email` 从 `replyrelay` 的人话来源移除（`channelSources` 只剩 `wechat`），并更正 §4 里"邮件有发件人白名单"这条**代码里不存在的控制**；微信对话式回复的无发送者校验改为在 TRANSPORT §2.6 记为已知风险。