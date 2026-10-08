package notify

// 企业微信「应用消息」推送（CorpID / Secret / AgentID 模式，可推送到个人微信）。
// 流程：GET /cgi-bin/gettoken（内存缓存，提前 5 分钟刷新）→ POST /cgi-bin/message/send
// 内容按 2048 字节按行分块、逐条发送（企业微信 text 上限）；契约参考 MoviePilot v3 的微信渠道。

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const wecomAPIBase = "https://qyapi.weixin.qq.com"

var (
	wecomTokenMu    sync.Mutex
	wecomTokenCache = map[string]wecomTokenEntry{}
)

type wecomTokenEntry struct {
	token  string
	expire time.Time
}

// wecomAccessToken 获取（并缓存）企业微信 access_token
func wecomAccessToken(corpID, corpSecret string) (string, error) {
	wecomTokenMu.Lock()
	if e, ok := wecomTokenCache[corpID]; ok && time.Now().Before(e.expire) {
		wecomTokenMu.Unlock()
		return e.token, nil
	}
	wecomTokenMu.Unlock()

	target := fmt.Sprintf("%s/cgi-bin/gettoken?corpid=%s&corpsecret=%s",
		wecomAPIBase, url.QueryEscape(corpID), url.QueryEscape(corpSecret))
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(target)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var out struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(rb, &out); err != nil {
		return "", fmt.Errorf("响应不是合法 JSON：%s", clipBytes(string(rb), 120))
	}
	if out.ErrCode != 0 || out.AccessToken == "" {
		return "", fmt.Errorf("CorpID 或应用 Secret 不正确（errcode=%d %s）%s",
			out.ErrCode, wecomCleanMsg(firstNonEmpty(out.ErrMsg, "未返回 access_token")), wecomHintFor(out.ErrCode))
	}
	expire := out.ExpiresIn
	if expire <= 0 {
		expire = 7200
	}
	if expire > 300 {
		expire -= 300
	}
	wecomTokenMu.Lock()
	wecomTokenCache[corpID] = wecomTokenEntry{token: out.AccessToken, expire: time.Now().Add(time.Duration(expire) * time.Second)}
	wecomTokenMu.Unlock()
	return out.AccessToken, nil
}

// wecomCleanMsg 去掉企业微信 errmsg 的调试尾巴（hint/ip/debug 链接），只留一句话
func wecomCleanMsg(msg string) string {
	for _, sep := range []string{", hint:", " hint:", ", from ip:", "more info at"} {
		if i := strings.Index(msg, sep); i > 0 {
			msg = msg[:i]
		}
	}
	msg = strings.Trim(strings.TrimSpace(msg), ",，")
	return clipBytes(msg, 80)
}

// wecomHintFor 常见错误码的人话提示
func wecomHintFor(errcode int) string {
	switch errcode {
	case 40001, 40013, 41001:
		return "（请核对 CorpID 与应用 Secret）"
	case 60011, 40056, 82001:
		return "（AgentID 不正确，或应用未开通相应权限）"
	case 45009:
		return "（接口调用超过频率限制，稍后再试）"
	}
	return ""
}

// wecomSplitContent 按 2048 字节按行分块（不切断多字节字符），与 MP 行为一致
func wecomSplitContent(content string, maxBytes int) []string {
	var chunks []string
	var cur strings.Builder
	curLen := 0
	for _, line := range strings.Split(content, "\n") {
		lineBytes := len([]byte(line)) + 1
		if curLen+lineBytes > maxBytes && curLen > 0 {
			chunks = append(chunks, strings.TrimRight(cur.String(), "\n"))
			cur.Reset()
			curLen = 0
		}
		cur.WriteString(line)
		cur.WriteString("\n")
		curLen += lineBytes
	}
	if curLen > 0 {
		chunks = append(chunks, strings.TrimRight(cur.String(), "\n"))
	}
	return chunks
}

// sendWecomApp 企业微信应用消息：内容分块逐条发送
func sendWecomApp(c Channel, title, text string) Result {
	label := ChannelLabels["wecom"]
	fail := func(err string) Result { return Result{Channel: label, OK: false, Error: err} }

	if strings.TrimSpace(c.AppID) == "" {
		return fail("未填 CorpID（企业 ID）")
	}
	if strings.TrimSpace(c.AppSecret) == "" {
		return fail("未填应用 Secret")
	}
	if strings.TrimSpace(c.AgentID) == "" {
		return fail("未填 AgentID（应用 ID）")
	}
	touser := strings.TrimSpace(c.ReceiveID)
	if touser == "" {
		touser = "@all"
	}

	token, err := wecomAccessToken(c.AppID, c.AppSecret)
	if err != nil {
		return fail("获取 access_token 失败：" + err.Error())
	}

	content := title
	if strings.TrimSpace(text) != "" {
		content = title + "\n" + strings.ReplaceAll(text, "\n\n", "\n")
	}
	chunks := wecomSplitContent(content, 2048)
	for i, chunk := range chunks {
		body, _ := json.Marshal(map[string]interface{}{
			"touser":  touser,
			"msgtype": "text",
			"agentid": c.AgentID,
			"text":    map[string]string{"content": chunk},
			"safe":    0,
		})
		resp, err := notifyJSONRequest(http.MethodPost, wecomAPIBase+"/cgi-bin/message/send?access_token="+url.QueryEscape(token), "", string(body))
		if err != nil {
			return fail(fmt.Sprintf("发送失败（第 %d/%d 段）：%s", i+1, len(chunks), err))
		}
		if code := intOf(resp["errcode"]); code != 0 {
			return fail(fmt.Sprintf("发送失败（第 %d 段）：errcode=%d %s%s",
				i+1, code, wecomCleanMsg(strOf(resp["errmsg"])), wecomHintFor(code)))
		}
	}
	return Result{Channel: label, OK: true}
}
