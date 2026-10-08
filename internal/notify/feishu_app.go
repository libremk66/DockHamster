package notify

// 飞书自建应用推送（App ID / App Secret 模式）。
// 流程：tenant_access_token（内存缓存，提前 10 分钟过期）→ POST /open-apis/im/v1/messages
// 消息优先发交互卡片（Markdown），失败自动回退纯文本——契约参考 MoviePilot v3 的飞书渠道。

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 飞书开放平台域名（Lark 国际版可覆盖为 https://open.larksuite.com）
const feishuDefaultDomain = "https://open.feishu.cn"

var (
	feishuTokenMu    sync.Mutex
	feishuTokenCache = map[string]feishuTokenEntry{}
)

type feishuTokenEntry struct {
	token  string
	expire time.Time
}

// feishuTenantToken 获取（并缓存）tenant_access_token；过期或被判定失效时自动刷新
func feishuTenantToken(appID, appSecret, domain string) (string, error) {
	feishuTokenMu.Lock()
	if e, ok := feishuTokenCache[appID]; ok && time.Now().Before(e.expire) {
		feishuTokenMu.Unlock()
		return e.token, nil
	}
	feishuTokenMu.Unlock()

	body, _ := json.Marshal(map[string]string{"app_id": appID, "app_secret": appSecret})
	resp, err := notifyJSONRequest(http.MethodPost, domain+"/open-apis/auth/v3/tenant_access_token/internal", "", string(body))
	if err != nil {
		return "", err
	}
	if code := intOf(resp["code"]); code != 0 {
		return "", fmt.Errorf("应用凭据无效或应用未启用（code=%d %s）",
			code, firstNonEmpty(strOf(resp["msg"]), strOf(resp["message"]), "未知原因"))
	}
	token := strOf(resp["tenant_access_token"])
	if token == "" {
		return "", fmt.Errorf("飞书未返回 tenant_access_token")
	}
	expire := intOf(resp["expire"])
	if expire <= 0 {
		expire = 7200
	}
	if expire > 600 {
		expire -= 600 // 提前 10 分钟视为过期
	}
	feishuTokenMu.Lock()
	feishuTokenCache[appID] = feishuTokenEntry{token: token, expire: time.Now().Add(time.Duration(expire) * time.Second)}
	feishuTokenMu.Unlock()
	return token, nil
}

// notifyJSONRequest 发送 JSON 请求并解析响应（飞书/企业微信等接口都是 {code|errcode,msg} 结构）
func notifyJSONRequest(method, target, token, body string) (map[string]interface{}, error) {
	var reader io.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, target, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s %s", resp.Status, clipBytes(string(rb), 160))
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rb, &out); err != nil {
		return nil, fmt.Errorf("响应不是合法 JSON：%s", clipBytes(string(rb), 120))
	}
	return out, nil
}

// feishuSendOnce 发送一条消息（msgType: interactive=卡片 / text=纯文本）
func feishuSendOnce(domain, token, receiveID, receiveIDType, msgType, content string) error {
	u := fmt.Sprintf("%s/open-apis/im/v1/messages?receive_id_type=%s", domain, url.QueryEscape(receiveIDType))
	body, _ := json.Marshal(map[string]string{
		"receive_id": receiveID,
		"msg_type":   msgType,
		"content":    content,
		"uuid":       randomHex(16), // 幂等：网络重试不会重复投递
	})
	resp, err := notifyJSONRequest(http.MethodPost, u, token, string(body))
	if err != nil {
		return err
	}
	if code := intOf(resp["code"]); code != 0 {
		return fmt.Errorf("code=%d %s", code, firstNonEmpty(strOf(resp["msg"]), strOf(resp["message"]), "未知原因"))
	}
	return nil
}

// feishuCardColor 按消息内容猜卡片主题色（失败类红、成功类绿、其余蓝）
func feishuCardColor(title, text string) string {
	joined := title + text
	switch {
	case strings.Contains(joined, "失败"), strings.Contains(joined, "异常"), strings.Contains(joined, "回滚"), strings.Contains(joined, "⚠️"), strings.Contains(joined, "🔴"):
		return "red"
	case strings.Contains(joined, "成功"), strings.Contains(joined, "✅"):
		return "green"
	}
	return "blue"
}

// sendFeishuApp 应用模式推送：交互卡片优先，失败自动回退纯文本
func sendFeishuApp(c Channel, title, text string) Result {
	label := ChannelLabels["feishu"]
	fail := func(err string) Result { return Result{Channel: label, OK: false, Error: err} }

	if strings.TrimSpace(c.AppID) == "" {
		return fail("未填 App ID")
	}
	if strings.TrimSpace(c.AppSecret) == "" {
		return fail("未填 App Secret")
	}
	if strings.TrimSpace(c.ReceiveID) == "" {
		return fail("未填接收者 ID（open_id / user_id / email / 群 chat_id）")
	}
	ridType := strings.TrimSpace(c.ReceiveIDType)
	if ridType == "" {
		ridType = "open_id"
	}
	domain := strings.TrimRight(strings.TrimSpace(c.Domain), "/")
	if domain == "" {
		domain = feishuDefaultDomain
	}

	token, err := feishuTenantToken(c.AppID, c.AppSecret, domain)
	if err != nil {
		return fail("获取租户令牌失败：" + err.Error())
	}

	// ① 交互卡片（Markdown 正文；标题带主题色）
	card := map[string]interface{}{
		"config": map[string]interface{}{"wide_screen_mode": true},
		"header": map[string]interface{}{
			"template": feishuCardColor(title, text),
			"title":    map[string]string{"tag": "plain_text", "content": clipBytes(title, 200)},
		},
		"elements": []map[string]interface{}{
			{"tag": "div", "text": map[string]string{"tag": "lark_md", "content": text}},
		},
	}
	cardBody, _ := json.Marshal(card)
	cardErr := feishuSendOnce(domain, token, c.ReceiveID, ridType, "interactive", string(cardBody))
	if cardErr == nil {
		return Result{Channel: label, OK: true}
	}

	// ② 回退纯文本（部分企业租户限制卡片消息）
	txtBody, _ := json.Marshal(map[string]string{"text": title + "\n" + text})
	if txtErr := feishuSendOnce(domain, token, c.ReceiveID, ridType, "text", string(txtBody)); txtErr != nil {
		return fail(fmt.Sprintf("发送失败：%s（卡片模式同样失败：%s）", txtErr.Error(), cardErr.Error()))
	}
	return Result{Channel: label, OK: true}
}

// ---- 小工具（JSON 值取值）----

func intOf(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}

func strOf(v interface{}) string {
	s, _ := v.(string)
	return s
}

// randomHex 本地随机十六进制串（免第三方依赖；用于飞书消息幂等 uuid）
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
