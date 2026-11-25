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

## 6. 本地自证 harness（含整条 M0 链路）（`scripts/verify-live/`）

上一节的 §4b 是手写的临时证据（一次性的 `/tmp` 脚本、手工贴输出）。本节把它变成**可重跑的
一条命令**：起真实产品进程，用产品自己的两条传输（WebSocket 控制面 + 签名 HTTP 入口）断言
自己的行为，并把原始回答逐条打印出来——**证据由被测进程产生，不是脚本的假设**。

它**不替代**本文档 §1–§4 的真机清单：整条链路都在本机 `127.0.0.1`、不接触任何平台。

### 6.1 怎么跑

```bash
scripts/verify-live/run.sh          # 零参数；退出码 0 = 全部 PASS
```

它顺序做这几件事（代码即注释，逐条对应 `run.sh`）：

1. `go build -o .cache/zkverify-live ./cmd/zeokeel` —— 被测二进制就是仓库当前代码；
2. 新建数据目录 `.cache/verify-live/<UTC 时间戳>/`，预置 `webhook/sources.json`
   （`id=verify`，secret 为 `verify-secret-0123456789` 的 base64，`channel=verify`）——
   入站路径在启动前就配好，不需要先走 WS 设置往返；
3. 后台起 `./.cache/zkverify-live --product serve --data-dir <dir>`，轮询
   `<dir>/gateway.json`（最多 30s）读**真实** port 与 token；
4. 跑 `node scripts/verify-live/checks.mjs --port N --token T --dir D`；
5. SIGTERM 停进程（10s 后仍未退则 SIGKILL），打印数据目录与 `product.log` 路径，
   以 checks 的退出码退出。

产物（每次运行一份，可留作证据）：`gateway.json`（运行期）、`product.log`（进程日志）、
`inbox/`、`credentials/`、`zeokeel.db`（被断言过的真实状态）。

### 6.2 覆盖了什么

`checks.mjs` 零依赖（只用 node 的 `net`/`crypto`）：先用 `POST /auth` 换 `zk` cookie，
再在裸 socket 上手写 RFC 6455 握手（带 `Origin`，网关 §3.2 C3 要求；Node 内置
`WebSocket` 无法设 cookie/Origin，所以不用它），`initialize` → `ready`（§4.2：`ready`
之前除 `initialize`/`ready`/`ping` 外一律 `not_ready`），然后逐条：

| 检查 | 断言 | 传输 |
|---|---|---|
| `initialize.capabilities` | 13 项能力齐全（inbox/triage/tasklease/approvalaction/notifyroute/webhook/lead/credentials/heartbeat/browser/teamnotes/wechatlogin/channeldispatch） | WS |
| `notify.plan.approval` | `category:"approval"` 的计划末位是 `inapp` | WS |
| `tasks.tools.get.unknown` | 不存在的任务返回 `{"tools":null}` 或带 code 的类型化错误——两种都算 PASS，记录的是"它怎么回答" | WS |
| `heartbeat.status` | `running=true` 且 `leadIntervalMs>0` | WS |
| `inbox.snapshot` | `items` 是数组（全新目录应为空） | WS |
| `credentials.list` | `credentials.set{kind:wechat,id:live,fields.token=live-secret}` 后列表含 `live`，且原始响应里**不含** `live-secret` | WS |
| `inbound.webhook.signed` | 正确 HMAC（`t=<unix>,v1=<hex sha256 of "<ts>.<body>">`）→ 202 | HTTP |
| `inbound.webhook.bad-signature` | 同一 body 换错 secret → 401 | HTTP |

每行输出 `PASS|FAIL <name> <原始回答>`，末尾一行 `SUMMARY n/m passed`；**只有全 PASS 才退出 0**。

### 6.3 实测（2026-11-25 20:46 北京时间，本机真实进程）

```
port: 9932
PASS initialize.capabilities missing=[] capabilities=["session.list","session.snapshot",…,"channeldispatch","context","tasks","regenerate","edi…"]
PASS notify.plan.approval channels=["inapp"] last="inapp" deferred=false reason=undefined
PASS tasks.tools.get.unknown result {"tools":null}
PASS heartbeat.status running=true leadIntervalMs=30000 channelIntervalMs=60000
PASS inbox.snapshot items=[] count=0
PASS credentials.list set={"ok":true} list={"credentials":[{"kind":"wechat","id":"live"}]} containsLive=true leakedSecret=false
PASS inbound.webhook.signed HTTP 202 (want 202) body={"count":1,"id":"inbox-000001","merged":false}
PASS inbound.webhook.bad-signature HTTP 401 (want 401) body=webhook: bad signature
SUMMARY 8/8 passed
```

### 6.4 它证明不了什么（仍需真机 / 仍需人）

- **真实平台报文**：钉钉 `timestamp`+`sign` 的真实形态与 header 名、飞书事件与卡片回调的
  平台签名、微信 `sendmessage` 的字段坑与 24h 窗口、邮件 IMAP/SMTP 编解码——本 harness
  不解析任何平台报文，这些仍按 §1–§4 在真机做。
- **出站投递**：`notify.deliver` 到真实渠道、`channeldispatch` 的重试/回退，需要真实凭据与
  真实网络；本机只验证了 `notify.plan` 的通道选择。
- **模型的真实运行**：`/api/chat/run` 的 SSE 全链路与审批停面需要模型 key，本 harness 不跑。
- **浏览器渲染**：这是协议级证据，不是 UI 证据；页面行为仍看 `npm run e2e:cold`。
- **合并语义**：webhook 同 body 重投应 `merged:true`，但 harness 每次用全新数据目录，
  因此只看到 `count:1,merged:false`；要验合并需在同一数据目录上重跑
  `scripts/channel-verify.sh webhook`（§4b 已记录）。

### 6.5 真实链路的实测输出（2026-11-25 20:47 北京时间）

同一条 harness 在真实进程上把 M0 整条链走通了（12/12 PASS）：

```
PASS inbound.webhook.signed HTTP 202 {"count":1,"id":"inbox-000001"}
PASS chain.dispatch taskIds=["task-1"]
PASS chain.claim-submit-review state=done
PASS chain.team-note-recorded notes=[{"id":"mem://user/self/memories/team-note/task-1",
     "title":"任务验收：live harness","author":"verify-live","reviewed":false, ...}]
PASS tasks.tools.roundtrip get={"taskId":"task-1","tools":["browser_open"]}
PASS inbound.webhook.bad-signature HTTP 401 webhook: bad signature
SUMMARY 12/12 passed
```

这条输出说明：签名投递 → 收件箱 → 派单 → 认领 → 交证据 → 验收 → **自动写入团队记忆** → 工具白名单往返，
全部发生在**装好的产品**上，而不是测试替身里。

### 6.6 追加的三项实测（2026-11-25 22:04 北京时间）

```
PASS approval.policy-roundtrip   get={"policy":"before_start","set":true}
PASS budget.stops-at-the-ceiling used240={"code":-32600,
     "message":"lease: task budget exhausted: task-1 used 240/100 tokens, 0/unlimited minutes"}
PASS tasks.tools.roundtrip       get={"taskId":"task-1","tools":["browser_open"]}
SUMMARY 14/14 passed
```

预算那次"爆表"是**真的被产品拒绝**，不是测试自己算出来的数：报文里带着 used/max。
