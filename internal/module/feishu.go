package module

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// SendFeishu 向飞书自定义机器人 webhook 发送文本消息
func SendFeishu(webhook string, text string) error {
	if webhook == "" {
		return fmt.Errorf("飞书 Webhook 未配置")
	}
	payload := map[string]interface{}{
		"msg_type": "text",
		"content":  map[string]string{"text": text},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(webhook, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("飞书返回 %s: %s", resp.Status, string(rb))
	}
	// 飞书成功响应：{"code":0,...} 或 {"StatusCode":0,...}
	var r struct {
		Code       int `json:"code"`
		StatusCode int `json:"StatusCode"`
	}
	if err := json.Unmarshal(rb, &r); err == nil {
		if r.Code != 0 || r.StatusCode != 0 {
			return fmt.Errorf("飞书返回异常: %s", string(rb))
		}
	}
	return nil
}
