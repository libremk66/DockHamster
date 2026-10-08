package notify

import (
	"strings"
	"testing"
)

// 字段缺失时应直接给出明确报错（不发起网络请求）
func TestSendFeishuAppValidation(t *testing.T) {
	cases := []struct {
		name string
		ch   Channel
		want string
	}{
		{"缺 App ID", Channel{AppSecret: "s", ReceiveID: "r"}, "未填 App ID"},
		{"缺 App Secret", Channel{AppID: "a", ReceiveID: "r"}, "未填 App Secret"},
		{"缺接收者", Channel{AppID: "a", AppSecret: "s"}, "未填接收者 ID"},
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

// QQ 渠道：字段缺失直接报错；常见错误码映射人话提示
func TestSendQQBotValidation(t *testing.T) {
	cases := []struct {
		name string
		ch   Channel
		want string
	}{
		{"缺 AppID", Channel{AppSecret: "s", ReceiveID: "r"}, "未填 AppID"},
		{"缺 ClientSecret", Channel{AppID: "a", ReceiveID: "r"}, "未填 ClientSecret"},
		{"缺目标", Channel{AppID: "a", AppSecret: "s"}, "未填接收目标"},
	}
	for _, tc := range cases {
		res := sendQQBot(tc.ch, "标题", "正文")
		if res.OK || !strings.Contains(res.Error, tc.want) {
			t.Fatalf("%s：res=%+v，想要包含 %q", tc.name, res, tc.want)
		}
	}
}

func TestQQHintFor(t *testing.T) {
	if !strings.Contains(qqHintFor(0, "主动消息条数已达上限"), "每月限 4 条") {
		t.Fatal("配额类错误未附提示")
	}
	if !strings.Contains(qqHintFor(11244, "x"), "Markdown") {
		t.Fatal("Markdown 类错误未附提示")
	}
	if qqHintFor(0, "别的错误") != "" {
		t.Fatal("普通错误不应附提示")
	}
}

// 企业微信应用模式：字段缺失直接报错；分块按行切且不超上限
func TestSendWecomAppValidation(t *testing.T) {
	cases := []struct {
		name string
		ch   Channel
		want string
	}{
		{"缺 CorpID", Channel{AppSecret: "s", AgentID: "1"}, "未填 CorpID"},
		{"缺 Secret", Channel{AppID: "a", AgentID: "1"}, "未填应用 Secret"},
		{"缺 AgentID", Channel{AppID: "a", AppSecret: "s"}, "未填 AgentID"},
	}
	for _, tc := range cases {
		res := sendWecomApp(tc.ch, "标题", "正文")
		if res.OK || !strings.Contains(res.Error, tc.want) {
			t.Fatalf("%s：res=%+v，想要包含 %q", tc.name, res, tc.want)
		}
	}
}

func TestWecomSplitContent(t *testing.T) {
	// 长内容按 2048 字节切成多块，每块不超限
	long := strings.Repeat("这是一行通知内容 abcdefg\n", 200) // ≈ 4000+ 字节
	chunks := wecomSplitContent(long, 2048)
	if len(chunks) < 2 {
		t.Fatalf("应切成多块，实际 %d 块", len(chunks))
	}
	for i, c := range chunks {
		if len([]byte(c)) > 2048 {
			t.Fatalf("第 %d 块超限：%d 字节", i+1, len([]byte(c)))
		}
	}
}
