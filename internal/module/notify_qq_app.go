package module

// QQ 官方机器人推送（AppID / ClientSecret 模式）。
// 流程：getAppAccessToken（内存缓存，提前 5 分钟刷新）→ POST /v2/groups|/v2/users/:id/messages
// 优先 Markdown（msg_type=2，需机器人开通能力），失败回退纯文本（msg_type=0）。
// 契约参考 MoviePilot v3 的 QQ 渠道（app/modules/qqbot）。
//
// ⚠️ QQ 官方限制：**主动消息每月限 4 条/群、4 条/用户**，且目标必须先与机器人交互过
// （没有 msg_id 的推送都属于"主动消息"）。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	qqTokenURL  = "https://bots.qq.com/app/getAppAccessToken"
	qqAPIBase   = "https://api.sgroup.qq.com"
	qqQuotaHint = "（注意：QQ 主动消息每月限 4 条/群、4 条/用户，且目标需先与机器人交互过）"
)

var (
	qqTokenMu    sync.Mutex
	qqTokenCache = map[string]qqTokenEntry{}
)

type qqTokenEntry struct {
	token  string
	expire time.Time
}

// qqAccessToken 获取（并缓存）机器人 AccessToken
func qqAccessToken(appID, clientSecret string) (string, error) {
	qqTokenMu.Lock()
	if e, ok := qqTokenCache[appID]; ok && time.Now().Before(e.expire) {
		qqTokenMu.Unlock()
		return e.token, nil
	}
	qqTokenMu.Unlock()

	body, _ := json.Marshal(map[string]string{"appId": appID, "clientSecret": clientSecret})
	rb, err := qqRequest(qqTokenURL, "", string(body))
	if err != nil {
		return "", err
	}
	var out struct {
		AccessToken string      `json:"access_token"`
		ExpiresIn   interface{} `json:"expires_in"`
		Code        int         `json:"code"`
		Message     string      `json:"message"`
	}
	if err := json.Unmarshal(rb, &out); err != nil {
		return "", fmt.Errorf("响应不是合法 JSON：%s", clipBytes(string(rb), 120))
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("AppID/ClientSecret 无效（code=%d %s）", out.Code, firstNonEmpty(out.Message, "未返回 access_token"))
	}
	expire := 7200
	switch v := out.ExpiresIn.(type) {
	case float64:
		expire = int(v)
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			expire = n
		}
	}
	if expire > 300 {
		expire -= 300 // 提前 5 分钟刷新
	}
	qqTokenMu.Lock()
	qqTokenCache[appID] = qqTokenEntry{token: out.AccessToken, expire: time.Now().Add(time.Duration(expire) * time.Second)}
	qqTokenMu.Unlock()
	return out.AccessToken, nil
}

// qqRequest 发送请求并返回原始响应体（错误时也带出 code/message）
func qqRequest(url, token, body string) ([]byte, error) {
	var reader io.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(http.MethodPost, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "QQBot "+token)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode >= 400 {
		var e struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(rb, &e)
		return nil, fmt.Errorf("code=%d %s%s", e.Code, firstNonEmpty(e.Message, clipBytes(string(rb), 120)), qqHintFor(e.Code, e.Message))
	}
	return rb, nil
}

// qqHintFor 对常见错误码补充人话提示
func qqHintFor(code int, message string) string {
	joined := fmt.Sprintf("%d %s", code, message)
	switch {
	case strings.Contains(joined, "主动") || strings.Contains(joined, "上限") || strings.Contains(joined, "频率"):
		return qqQuotaHint
	case code == 11244 || strings.Contains(strings.ToLower(joined), "markdown"):
		return "（机器人未开通 Markdown 能力，将自动改为纯文本重发）"
	}
	return ""
}

// sendQQBot QQ 机器人推送：Markdown 优先，失败回退纯文本
func sendQQBot(c NotifyChannel, title, text string) NotifyResult {
	label := ChannelLabels["qq"]
	fail := func(err string) NotifyResult { return NotifyResult{Channel: label, OK: false, Error: err} }

	if strings.TrimSpace(c.AppID) == "" {
		return fail("未填 AppID")
	}
	if strings.TrimSpace(c.AppSecret) == "" {
		return fail("未填 ClientSecret（机器人密钥）")
	}
	if strings.TrimSpace(c.ReceiveID) == "" {
		return fail("未填接收目标（群 group_openid 或用户 openid）")
	}
	targetType := strings.ToLower(strings.TrimSpace(c.ReceiveIDType))
	if targetType == "" {
		targetType = "group"
	}
	path := "/v2/groups/" + c.ReceiveID + "/messages"
	if targetType == "user" || targetType == "c2c" {
		path = "/v2/users/" + c.ReceiveID + "/messages"
	}

	token, err := qqAccessToken(c.AppID, c.AppSecret)
	if err != nil {
		return fail("获取 AccessToken 失败：" + err.Error())
	}

	// ① Markdown（msg_type=2）
	mdBody, _ := json.Marshal(map[string]interface{}{
		"markdown": map[string]string{"content": "**" + title + "**\n\n" + text},
		"msg_type": 2,
	})
	if _, err := qqRequest(qqAPIBase+path, token, string(mdBody)); err == nil {
		return NotifyResult{Channel: label, OK: true}
	} else {
		mdErr := err
		// ② 回退纯文本（msg_type=0；Markdown 未开通时必走这条）
		plainBody, _ := json.Marshal(map[string]interface{}{
			"content":  "【" + title + "】\n" + text,
			"msg_type": 0,
		})
		if _, err2 := qqRequest(qqAPIBase+path, token, string(plainBody)); err2 != nil {
			return fail(fmt.Sprintf("发送失败：%s（Markdown 模式：%s）", err2.Error(), mdErr.Error()))
		}
		return NotifyResult{Channel: label, OK: true}
	}
}
