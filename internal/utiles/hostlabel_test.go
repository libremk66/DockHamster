package utiles

import "testing"

func TestSanitizeHostLabel(t *testing.T) {
	cases := map[string]string{
		"  jknas  ":                       "jknas",
		"客厅\nNAS":                         "客厅 NAS", // 换行压成空格
		"a\t\tb":                          "a b",    // 连续空白压成一个
		"":                                "",
		"x123456789012345678901234567890": "x12345678901234567890123", // 截到 24 字符
	}
	for in, want := range cases {
		if got := sanitizeHostLabel(in); got != want {
			t.Fatalf("sanitizeHostLabel(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// 没有 svcCtx / 探测不到主机名时：返回空标记（标题保持原样，不出现空括号）
func TestHostTagNilSafety(t *testing.T) {
	if got := HostTag(nil); got != "" {
		t.Fatalf("nil 应返回空标记，实际 %q", got)
	}
	if got := ResolvedHostLabel(nil); got != "" {
		t.Fatalf("nil 应返回空标识，实际 %q", got)
	}
}
