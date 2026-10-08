# notify —— 零依赖多渠道通知模块

> **可直接整个目录拷贝到其它 Go 项目**：纯标准库实现（连 UUID 都是本地生成的），没有第三方依赖、没有框架耦合、没有全局状态以外的副作用。

用于把「一条标题 + 一段正文」推送到用户配置的多种渠道（群机器人 / 官方应用机器人 / 推送服务），每个渠道独立发送、独立失败，互不影响。

---

## 一、快速接入（3 步）

**1. 拷贝目录**：把 `internal/notify/` 整个拷进你的项目（包名即 `notify`）。

**2. 准备配置**：可以直接用 `notify.Channels`（内含 8 个渠道字段），或自己定义配置结构后转成 `notify.Channel`（字段一一对应即可，含 JSON tag）。

**3. 发送**：

```go
// 发往所有已启用渠道（失败逐渠道隔离，返回每个渠道的结果）
results := notify.Send(chs, "🔄 自动更新完成", "已更新 2 个：nginx、redis\n⏱ 耗时 12.3s")
for _, r := range results {
    if !r.OK {
        log.Printf("通知失败 %s: %s", r.Channel, r.Error)
    }
}

// 单渠道发送（"测试按钮"场景）
r := notify.SendOne("feishu", chs.Feishu, "🔔 通知测试", "如果你看到这条消息，说明配置成功 ✅")
```

---

## 二、数据模型

### `Channel`（单渠道配置，字段按渠道选用）

| 字段 | JSON | 用于 |
|---|---|---|
| `Enabled` | `enabled` | 全部 |
| `Webhook` | `webhook` | 飞书群机器人 / 企业微信群机器人 / 钉钉（含加签拼接） |
| `Secret` | `secret` | 飞书机器人加签、钉钉加签 |
| `Server` / `Key` | `server` / `key` | Bark |
| `SendKey` | `sendKey` | Server酱 |
| `Token` / `ChatID` / `APIBase` | `token` / `chatId` / `apiBase` | Telegram（Bot Token / chat_id / 可填反代） |
| `URL` / `Method` / `Headers` / `BodyTemplate` | `url` / `method` / `headers` / `bodyTemplate` | 自定义 Webhook（GET/POST、自定义头、body 模板 `{title}`/`{text}`） |
| `AppID` / `AppSecret` | `appId` / `appSecret` | **飞书应用 / 企业微信应用（CorpID）/ QQ 机器人** |
| `AgentID` | `agentId` | 企业微信应用 |
| `ReceiveID` / `ReceiveIDType` | `receiveId` / `receiveIdType` | 飞书应用（open_id/user_id/email/chat_id）、QQ（group/user）、企业微信（touser，默认 @all） |
| `Domain` | `domain` | 飞书（Lark 国际版填 `https://open.larksuite.com`） |

### `Channels`（8 渠道容器）

`Feishu / Wecom / Dingtalk / QQ / Bark / ServerChan / Telegram / Webhook`，配合：

- `notify.ChannelOrder` —— 发送顺序（也是前端展示顺序）
- `notify.ChannelLabels` —— 渠道中文名（发送结果里回传的就是它）
- `(Channels).ByName("feishu")` —— 按渠道键取配置

### `Result`

```go
type Result struct {
    Channel string // 中文渠道名（如"飞书"）
    OK      bool
    Error   string
}
```

---

## 三、渠道契约速查（移植 / 排障必备）

| 渠道 | 认证 | 发送端点 | 消息体 | 限制与降级 |
|---|---|---|---|---|
| **飞书·应用** | `POST open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal` `{app_id,app_secret}` → token（缓存，提前 10 分钟过期） | `POST /open-apis/im/v1/messages?receive_id_type=<类型>`，`Authorization: Bearer <token>` | `{receive_id, msg_type, content, uuid}`；卡片= `interactive`（header 主题色 + `div/lark_md`），纯文本= `text` | 卡片失败**自动回退纯文本**；标题按内容自动红/绿/蓝 |
| **飞书·群机器人** | 可选加签（HMAC-SHA256，key=`ts+"\n"+secret`） | Webhook | `{msg_type:"text", content:{text}}`（带签时加 `timestamp`+`sign`） | — |
| **企业微信·应用** | `GET /cgi-bin/gettoken?corpid&corpsecret` → token（缓存，提前 5 分钟） | `POST /cgi-bin/message/send?access_token=` | `{touser, msgtype:"text", agentid, text:{content}, safe:0}` | 正文 >2048 字节**按行分块逐条发**；errmsg 的调试尾巴会被清洗 |
| **企业微信·群机器人** | 无 | Webhook | `{msgtype:"text", text:{content}}` | 自动按 1900 字节截断 |
| **钉钉** | 可选加签（HMAC-SHA256，`&timestamp=&sign=`） | Webhook | `{msgtype:"text", text:{content}}` | — |
| **QQ·官方机器人** | `POST bots.qq.com/app/getAppAccessToken` `{appId,clientSecret}` → token（缓存，提前 5 分钟；`expires_in` 可能是字符串） | `POST api.sgroup.qq.com/v2/groups/{group_openid}/messages` 或 `/v2/users/{openid}/messages`，`Authorization: QQBot <token>` | Markdown=`{markdown:{content},msg_type:2}` → 失败回退 `{content,msg_type:0}` | ⚠️ **主动消息每月 4 条/目标**，且目标需先与机器人交互过 |
| **Bark** | 无 | `POST {server}/{key}` | `{title, body, group}` | — |
| **Server酱** | 无 | `POST sctapi.ftqq.com/{sendKey}.send` | 表单 `title`+`desp` | — |
| **Telegram** | 无（Token 在 URL） | `POST {apiBase}/bot{token}/sendMessage` | `{chat_id, text, disable_web_page_preview}` | 可填反代 `apiBase` |
| **自定义 Webhook** | 自定义头（JSON） | 用户 URL（GET/POST） | 默认 `{title,text}` JSON 或自定义 `bodyTemplate` | URL 支持 `{title}`/`{text}` 变量（URL 编码） |

> 所有 HTTP 请求超时 10 秒；响应里的 `code` / `errcode` 非 0 一律视为失败并带出可读原因。

---

## 四、扩展一个新渠道（约 5 步）

1. **加字段**（如需）：改 `notify.go` 的 `Channel` 结构体（保持 `,optional` tag 风格）；同时加进 `Channels`。
2. **注册**：`ChannelOrder`、`ChannelLabels`、`Channels` 字段、`ByName()` 的 switch 各加一行。
3. **实现**：在 `SendOne()` 的 switch 里加 `case "xxx": return sendXxx(c, title, text)`，新文件里写 `sendXxx`。
4. **照抄三个成熟模式**：
   - **token 缓存** → 参考 `feishuTenantToken`（`map[凭证]token` + 提前过期）
   - **错误码翻译** → 参考 `wecomHintFor` / `qqHintFor`（原始 errcode 用户看不懂，必须翻人话）
   - **能力降级** → 参考"卡片→纯文本""markdown→纯文本"（富格式失败自动退到最朴素的形式）
5. **测试**：`notify_test.go` 里加两条（① 字段缺失报错不联网 ② 任何新增纯函数），真机用 `SendOne` 当"测试按钮"。

---

## 五、已知坑（都是实测踩出来的）

1. **飞书卡片不是所有租户都能用** → 必须带纯文本回退，否则部分企业收不到。
2. **企业微信 text 上限 2048 字节**，且官方 errmsg 尾巴很长（`hint: [...] from ip ... more info at ...`）→ 分块发送 + 清洗文案。
3. **QQ 主动消息有硬配额**（4 条/月/目标 + 需先交互）→ 只能当"重要告警兜底"，错误信息里要主动提示配额。
4. **token 必须提前过期**（飞书 10 分钟、企微/QQ 5 分钟），否则边界时刻会偶发 401。
5. **各家的错误字段名不统一**：`code` / `errcode` / `code+message` 都有（`notifyPost` 已兼容三种）。
6. **渠道调用的失败绝不能影响主流程**：`Send` 逐渠道 recover 式隔离，调用方只处理 `[]Result`。
7. **JSON tag 用 `,optional` 而非 `omitempty`**（go-zero 校验要求；其它框架用标准 `omitempty` 即可）。

---

## 六、文件清单

| 文件 | 内容 |
|---|---|
| `notify.go` | 数据模型 + 分发（Send/SendOne）+ 群机器人渠道（飞书/企微/钉钉）+ Bark/Server酱/Telegram/自定义 Webhook + 工具函数 |
| `feishu_app.go` | 飞书自建应用（token 缓存 / 交互卡片 / 回退纯文本） |
| `wecom_app.go` | 企业微信应用消息（token 缓存 / 2048 分块 / errmsg 清洗） |
| `qq_app.go` | QQ 官方机器人（token 缓存 / Markdown 优先回退 / 配额提示） |
| `notify_test.go` | 单测（字段校验不联网 + 纯函数） |
