# 渠道真机验证清单（上线前必做）

> 本文件是 **上线门槛**，不是介绍。适配器都只对假服务器（httptest）验证过；
> 真实平台的报文形状、签名实现与限速没有一处被真机确认。
> 每一项都要贴出命令与原始输出，没贴等于没做。

## 0. 通用准备

```bash
# 1) 起产品（临时数据目录），记下打印的 URL
go run ./cmd/zeokeel --product serve --data-dir /tmp/zk-verify
# 2) 在 Web 设置页「渠道」里录入凭据 / webhook 来源（或直接调 credentials.set）
# 3) 下面的脚本都以该 URL 为参数
scripts/channel-verify.sh feishu-challenge http://127.0.0.1:<port>
scripts/channel-verify.sh webhook     http://127.0.0.1:<port> <sourceID> <secret>
scripts/channel-verify.sh webhook     http://127.0.0.1:<port> <sourceID> <wrong-secret> --expect-fail
scripts/channel-verify.sh dingtalk    http://127.0.0.1:<port> <secret>
```

记录表（每格都要填）：

| 渠道 | 检查项 | 期望 | 实测 | 时间 |
|---|---|---|---|---|
| webhook | 正确签名 | 202 + `merged:false` | ✅ `HTTP 202 {"count":1,"id":"inbox-000001","merged":false}` | 2026-11-25 18:17 |
| webhook | 错误签名 | 401，且收件箱无新条目 | ✅ `HTTP 401 webhook: bad signature`，收件箱未新增 | 2026-11-25 18:17 |
| webhook | 同一 body 再投一次 | 202 + `merged:true`、count=2 | ✅ `HTTP 202 {"count":2,"id":"inbox-000001","merged":true}` | 2026-11-25 18:17 |
| dingtalk | 正确签名回调 | 202 | | |
| dingtalk | 超时时间戳 | 401（`stale`） | | |
| feishu | URL 握手 | 200 + 原样 challenge | ✅ `HTTP 200 {"challenge":"verify-challenge"}` | 2026-11-25 18:17 |
| feishu | 文本事件 | 202，收件箱出现条目 | | |
| feishu | 卡片按钮 | 点「同意」后审批被应用；改掉 token 后 403 | | |
| 微信 | 扫码登录 | 设置页显示已连接 | | |
| 微信 | 收一条文本 | 收件箱出现，`/inbox/incoming` 可见 | | |
| 微信 | 回一条文本 | 手机上收到，且 `context_token` 用的是当次消息 | | |
| 微信 | 24h 窗口外发送 | 本地拒绝（不显示已发送） | | |
| 微信 | 保活提示 | 距最后一条消息 20h 后状态显示需保活 | | |
| 邮件 | 发一封 | 主题中文不乱码（RFC 2047） | | |
| 邮件 | 收一封 | 正文 QP/base64 均正确解码 | | |
| 邮件 | 同一 Message-ID 再收 | 合并，count=2 | | |

## 1. 微信（ClawBot / iLink）

1. 设置页「渠道」→ kind=wechat，填 token（扫码流程拿到后）。
2. `心跳`（/settings/health）应显示 running，且 last pass 有 polls。
3. 手机发一条文本 → `/inbox/incoming` 出现 → 派成任务 → 任务板可见。
4. 通过 Web 回复一条 → 手机收到。
5. 隔 20h 再点发送：应该**本地拒绝**（`ErrWeChatWindowClosed`），而不是假装成功。

已知坑（做的时候会遇到）：
- `sendmessage` 少任一字段 → HTTP 200 但**不投递**；
- `context_token` 必须用当次消息的，不能复用旧的；
- 24h 窗口只能靠用户再发一条消息刷新，机器人无法主动续期。

## 2. 钉钉

1. 应用控制台把机器人回调地址设为 `https://<host>/api/inbound/dingtalk`（需公网或内网穿透）。
2. 用 `scripts/channel-verify.sh dingtalk` 先验签名，再在群里 @机器人 发一条。
3. 钉钉官方回调头是 `timestamp` + `sign`（时间戳**毫秒**），适配器两种拼写都接受；签名覆盖的是 `timestamp+"\n"+secret`，时间戳按原样参与计算。

## 3. 飞书

1. 事件订阅地址填 `https://<host>/api/inbound/feishu`，平台会先发 `url_verification`——必须回 **200 + challenge**（回 202 会判失败）。
2. 卡片回调地址填 `https://<host>/api/inbound/feishu/card`；卡片 action.value 必须带 `{approvalId,decision,token}`，否则会被 400 拒绝。
3. 若开启加密事件（encrypt），当前实现**不支持**，需先在设置里关闭。

## 4. 邮件

1. 设置页 kind=email：`username`/`password`（IMAP/SMTP 同一账号）。
2. 发一封中文主题邮件给自己 → 主题不乱码。
3. 收一封带 base64 正文的邮件 → 正文正确。

## 4b. 已验证的部分（本机真实进程，非 httptest）

方法：用 `.cache/zkverify` 起真实 `--product serve`（数据目录 `.cache/verify-run`，预置
`webhook/sources.json`），再用 `scripts/channel-verify.sh` 打真实 HTTP。

已通过：webhook 正确签名（202）/ 错误签名（401）/ 重投合并（count=2）、飞书握手（200 + challenge）。

未覆盖（仍需真机）：钉钉回调的真实 header 名、飞书事件与卡片的平台签名、微信/邮件全链路。

## 5. 结论记录

- [ ] 四个渠道全部通过
- [ ] 每条失败都已开 issue 并注明现象/时间
- [ ] 验证用的临时数据目录已删除（`rm -rf /tmp/zk-verify`）
