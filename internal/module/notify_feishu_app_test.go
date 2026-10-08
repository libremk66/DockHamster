package module

import (
	"strings"
	"testing"
)

// 字段缺失时应直接给出明确报错（不发起网络请求）
func TestSendFeishuAppValidation(t *testing.T) {
	cases := []struct {
		name string
		ch   NotifyChannel
		want string
	}{
		{"缺 App ID", NotifyChannel{AppSecret: "s", ReceiveID: "r"}, "未填 App ID"},
		{"缺 App Secret", NotifyChannel{AppID: "a", ReceiveID: "r"}, "未填 App Secret"},
		{"缺接收者", NotifyChannel{AppID: "a", AppSecret: "s"}, "未填接收者 ID"},
	}
	for _, tc := range cases {
		res := sendFeishuApp(tc.ch, "标题", "正文")
		if res.OK {
			t.Fatalf("%s：预期失败，实际成功", tc.name)
		}
		if !strings.Contains(res.Error, tc.want) {
			t.Fatalf("%s：错误信息 = %q，想要包含 %q", tc.name, res.Error, tc.want)
		}
	}
}

// 卡片主题色：失败红 / 成功绿 / 其余蓝
func TestFeishuCardColor(t *testing.T) {
	cases := []struct {
		title, text, want string
	}{
		{"⚠️ 容器异常退出：nginx", "退出码 1", "red"},
		{"🔄 DockHamster 自动更新", "✅ 已更新 2 个：nginx", "green"},
		{"📋 日常提醒", "一切正常", "blue"},
	}
	for _, tc := range cases {
		if got := feishuCardColor(tc.title, tc.text); got != tc.want {
			t.Fatalf("feishuCardColor(%q,%q) = %s，想要 %s", tc.title, tc.text, got, tc.want)
		}
	}
}

// JSON 取值兼容 int/float64/string
func TestJSONValueHelpers(t *testing.T) {
	if intOf(float64(42)) != 42 || intOf("7") != 7 || intOf(nil) != 0 {
		t.Fatal("intOf 取值异常")
	}
	if strOf("abc") != "abc" || strOf(123) != "" {
		t.Fatal("strOf 取值异常")
	}
}
