package module

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ===== 通知渠道（移植自音乐仓鼠的通知模块：7 渠道 / 单向推送 / 失败不影响主流程） =====

// NotifyChannel 单个渠道配置（字段按渠道选用）
// 注意：tag 用 go-zero 的 ,optional（不能用 omitempty —— 输出省略后请求校验会当必填拒绝）
type NotifyChannel struct {
	Enabled      bool   `json:"enabled"`
	Webhook      string `json:"webhook,optional"`      // 飞书/企业微信/钉钉 群机器人地址
	Secret       string `json:"secret,optional"`       // 飞书/钉钉 加签密钥（可选）
	Server       string `json:"server,optional"`       // Bark 服务器（默认 https://api.day.app）
	Key          string `json:"key,optional"`          // Bark Key
	SendKey      string `json:"sendKey,optional"`      // Server酱 SendKey
	Token        string `json:"token,optional"`        // Telegram Bot Token
	ChatID       string `json:"chatId,optional"`       // Telegram chat_id
	APIBase      string `json:"apiBase,optional"`      // Telegram API 地址（可填反代）
	URL          string `json:"url,optional"`          // 自定义 Webhook URL
	Method       string `json:"method,optional"`       // 自定义 Webhook 方法 POST/GET
	Headers      string `json:"headers,optional"`      // 自定义请求头（JSON 文本）
	BodyTemplate string `json:"bodyTemplate,optional"` // 自定义 body 模板（{title} {text}）
	// 飞书「应用模式」（App ID/Secret；填了就走应用推送，与群机器人 Webhook 二选一）
	AppID         string `json:"appId,optional"`         // 自建应用 App ID
	AppSecret     string `json:"appSecret,optional"`     // 自建应用 App Secret
	ReceiveID     string `json:"receiveId,optional"`     // 接收者 ID（open_id/user_id/email/群 chat_id）
	ReceiveIDType string `json:"receiveIdType,optional"` // 接收者类型，默认 open_id
	Domain        string `json:"domain,optional"`        // 可选：开放平台域名（Lark 国际版填 https://open.larksuite.com）
}

// NotifyChannels 全部渠道
type NotifyChannels struct {
	Feishu     NotifyChannel `json:"feishu"`
	Wecom      NotifyChannel `json:"wecom"`
	Dingtalk   NotifyChannel `json:"dingtalk"`
	QQ         NotifyChannel `json:"qq"`
	Bark       NotifyChannel `json:"bark"`
	ServerChan NotifyChannel `json:"serverchan"`
	Telegram   NotifyChannel `json:"telegram"`
	Webhook    NotifyChannel `json:"webhook"`
}

type NotifyResult struct {
	Channel string `json:"channel"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

// ChannelLabels 渠道显示名（顺序即发送顺序）
var notifyOrder = []string{"feishu", "wecom", "dingtalk", "qq", "bark", "serverchan", "telegram", "webhook"}

var ChannelLabels = map[string]string{
	"feishu": "飞书", "wecom": "企业微信", "dingtalk": "钉钉", "qq": "QQ（机器人）",
	"bark": "Bark", "serverchan": "Server酱", "telegram": "Telegram", "webhook": "自定义 Webhook",
}

// ChannelByName 取某渠道的配置
func (n NotifyChannels) ChannelByName(t string) NotifyChannel {
	switch t {
	case "feishu":
		return n.Feishu
	case "wecom":
		return n.Wecom
	case "dingtalk":
		return n.Dingtalk
	case "qq":
		return n.QQ
	case "bark":
		return n.Bark
	case "serverchan":
		return n.ServerChan
	case "telegram":
		return n.Telegram
	case "webhook":
		return n.Webhook
	}
	return NotifyChannel{}
}

// SendNotify 向所有已启用渠道发送；逐渠道独立，失败不影响其他渠道
func SendNotify(channels NotifyChannels, title, text string) []NotifyResult {
	var out []NotifyResult
	for _, t := range notifyOrder {
		ch := channels.ChannelByName(t)
		if !ch.Enabled {
			continue
		}
		out = append(out, SendChannel(t, ch, title, text))
	}
	return out
}

// SendChannel 单渠道发送（测试接口也走这里）
func SendChannel(channelType string, c NotifyChannel, title, text string) NotifyResult {
	label := ChannelLabels[channelType]
	if label == "" {
		label = channelType
	}
	fail := func(err string) NotifyResult { return NotifyResult{Channel: label, OK: false, Error: err} }
	ok := func() NotifyResult { return NotifyResult{Channel: label, OK: true} }

	switch channelType {
	case "feishu":
		// 应用模式（App ID/Secret）：填了就优先走应用推送
		if strings.TrimSpace(c.AppID) != "" || strings.TrimSpace(c.AppSecret) != "" {
			return sendFeishuApp(c, title, text)
		}
		if c.Webhook == "" {
			return fail("未填 Webhook 地址（或改用 App ID/App Secret 应用模式）")
		}
		body := map[string]interface{}{
			"msg_type": "text",
			"content":  map[string]string{"text": title + "\n" + text},
		}
		if c.Secret != "" {
			ts := time.Now().Unix()
			body["timestamp"] = fmt.Sprintf("%d", ts)
			body["sign"] = feishuSign(c.Secret, ts)
		}
		b, _ := json.Marshal(body)
		if err := notifyPost(c.Webhook, map[string]string{"Content-Type": "application/json"}, string(b), http.MethodPost); err != nil {
			return fail(err.Error())
		}
		return ok()

	case "wecom":
		if c.Webhook == "" {
			return fail("未填 Webhook 地址")
		}
		// 企业微信 text 上限 2048 字节，先按字节截断
		body, _ := json.Marshal(map[string]interface{}{
			"msgtype": "text",
			"text":    map[string]string{"content": clipBytes(title+"\n"+text, 1900)},
		})
		if err := notifyPost(c.Webhook, map[string]string{"Content-Type": "application/json"}, string(body), http.MethodPost); err != nil {
			return fail(err.Error())
		}
		return ok()

	case "dingtalk":
		if c.Webhook == "" {
			return fail("未填 Webhook 地址")
		}
		target := c.Webhook
		if c.Secret != "" {
			ts := time.Now().UnixMilli()
			mac := hmac.New(sha256.New, []byte(c.Secret))
			mac.Write([]byte(fmt.Sprintf("%d\n%s", ts, c.Secret)))
			sign := url.QueryEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
			sep := "?"
			if strings.Contains(target, "?") {
				sep = "&"
			}
			target += fmt.Sprintf("%stimestamp=%d&sign=%s", sep, ts, sign)
		}
		body, _ := json.Marshal(map[string]interface{}{
			"msgtype": "text",
			"text":    map[string]string{"content": title + "\n" + text},
		})
		if err := notifyPost(target, map[string]string{"Content-Type": "application/json"}, string(body), http.MethodPost); err != nil {
			return fail(err.Error())
		}
		return ok()

	case "qq":
		return sendQQBot(c, title, text)

	case "bark":
		if c.Key == "" {
			return fail("未填 Key")
		}
		base := strings.TrimRight(c.Server, "/")
		if base == "" {
			base = "https://api.day.app"
		}
		body, _ := json.Marshal(map[string]string{"title": title, "body": text, "group": "DockHamster"})
		if err := notifyPost(base+"/"+url.PathEscape(c.Key), map[string]string{"Content-Type": "application/json"}, string(body), http.MethodPost); err != nil {
			return fail(err.Error())
		}
		return ok()

	case "serverchan":
		if c.SendKey == "" {
			return fail("未填 SendKey")
		}
		form := url.Values{"title": {title}, "desp": {text}}
		if err := notifyPost("https://sctapi.ftqq.com/"+url.PathEscape(c.SendKey)+".send",
			map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, form.Encode(), http.MethodPost); err != nil {
			return fail(err.Error())
		}
		return ok()

	case "telegram":
		if c.Token == "" {
			return fail("未填 Bot Token")
		}
		if c.ChatID == "" {
			return fail("未填 chat_id")
		}
		base := strings.TrimRight(c.APIBase, "/")
		if base == "" {
			base = "https://api.telegram.org"
		}
		body, _ := json.Marshal(map[string]interface{}{
			"chat_id": c.ChatID, "text": title + "\n" + text, "disable_web_page_preview": true,
		})
		if err := notifyPost(base+"/bot"+c.Token+"/sendMessage", map[string]string{"Content-Type": "application/json"}, string(body), http.MethodPost); err != nil {
			return fail(err.Error())
		}
		return ok()

	case "webhook":
		if c.URL == "" {
			return fail("未填 URL")
		}
		headers := map[string]string{"Content-Type": "application/json"}
		if strings.TrimSpace(c.Headers) != "" {
			var extra map[string]string
			if err := json.Unmarshal([]byte(c.Headers), &extra); err != nil {
				return fail("自定义请求头不是合法 JSON")
			}
			for k, v := range extra {
				headers[k] = v
			}
		}
		if strings.ToUpper(c.Method) == http.MethodGet {
			target := replaceNotifyVars(c.URL, url.QueryEscape(title), url.QueryEscape(text))
			if err := notifyPost(target, headers, "", http.MethodGet); err != nil {
				return fail(err.Error())
			}
			return ok()
		}
		body := ""
		if strings.TrimSpace(c.BodyTemplate) != "" {
			body = replaceNotifyVars(c.BodyTemplate, title, text)
		} else {
			b, _ := json.Marshal(map[string]string{"title": title, "text": text})
			body = string(b)
		}
		target := replaceNotifyVars(c.URL, url.QueryEscape(title), "")
		if err := notifyPost(target, headers, body, http.MethodPost); err != nil {
			return fail(err.Error())
		}
		return ok()
	}
	return fail("未知渠道")
}

// ===== 工具函数 =====

// notifyPost 带超时的请求；检查 HTTP 状态与响应体里的 code/errcode（部分服务用 200 表达失败）
func notifyPost(target string, headers map[string]string, body string, method string) error {
	var reader io.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, target, reader)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s %s", resp.Status, clipBytes(string(rb), 120))
	}
	var j struct {
		Code    int    `json:"code"`
		ErrCode *int   `json:"errcode"`
		Message string `json:"message"`
		Msg     string `json:"msg"`
		ErrMsg  string `json:"errmsg"`
	}
	if json.Unmarshal(rb, &j) == nil {
		if j.Code != 0 && j.Code != 200 {
			return fmt.Errorf("%s", firstNonEmpty(j.Message, j.Msg, fmt.Sprintf("code=%d", j.Code)))
		}
		if j.ErrCode != nil && *j.ErrCode != 0 {
			return fmt.Errorf("%s", firstNonEmpty(j.ErrMsg, fmt.Sprintf("errcode=%d", *j.ErrCode)))
		}
	}
	return nil
}

// feishuSign 飞书自定义机器人加签：HMAC-SHA256(key = timestamp+"\n"+secret, data = "") 再 base64
func feishuSign(secret string, timestamp int64) string {
	mac := hmac.New(sha256.New, []byte(fmt.Sprintf("%d\n%s", timestamp, secret)))
	mac.Write([]byte{})
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// clipBytes 按字节截断（不切断多字节字符）
func clipBytes(s string, maxBytes int) string {
	if len([]byte(s)) <= maxBytes {
		return s
	}
	rs := []rune(s)
	for len([]byte(string(rs))) > maxBytes-20 && len(rs) > 0 {
		rs = rs[:len(rs)-1]
	}
	return string(rs) + "\n…（内容过长已截断）"
}

func replaceNotifyVars(tpl, title, text string) string {
	tpl = strings.ReplaceAll(tpl, "{title}", title)
	tpl = strings.ReplaceAll(tpl, "{text}", text)
	return tpl
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
