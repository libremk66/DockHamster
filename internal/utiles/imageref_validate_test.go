package utiles

import (
	"strings"
	"testing"
)

// 镜像引用校验：拦裸 ID、放行正常引用、无标签补 :latest
func TestValidateUpdateImageRef(t *testing.T) {
	// 必须拒绝：裸镜像 ID（issue #4 里用户对话框里的那串）
	rejects := []string{
		"sha256:782fce54746021108c71ae57cb996ac3e8c0ed257f0d3575d6e1a1f0a1b2c3d4",
		"782fce547460",
		"782fce54746021108c71ae57cb996ac3e8c0ed257f0d3575",
	}
	for _, r := range rejects {
		if _, err := ValidateUpdateImageRef(r); err == nil {
			t.Fatalf("%s：应被拒绝", r)
		} else if !strings.Contains(err.Error(), "镜像 ID") {
			t.Fatalf("%s：错误信息应说明是镜像 ID，实际 %q", r, err.Error())
		}
	}

	// 放行并规范化
	cases := map[string]string{
		"nginx:latest":             "docker.io/library/nginx:latest",
		"nginx":                    "docker.io/library/nginx:latest", // 无标签补 latest
		"ghcr.io/owner/app:v1.2.3": "ghcr.io/owner/app:v1.2.3",
		"115lite:latest":           "docker.io/library/115lite:latest",
		"lscr.io/linuxserver/app":  "lscr.io/linuxserver/app:latest",
	}
	for in, want := range cases {
		got, err := ValidateUpdateImageRef(in)
		if err != nil {
			t.Fatalf("%s：不应报错：%v", in, err)
		}
		if got != want {
			t.Fatalf("%s：规范化结果 = %q，想要 %q", in, got, want)
		}
	}

	// 空与非法
	if _, err := ValidateUpdateImageRef("  "); err == nil {
		t.Fatal("空值应被拒绝")
	}
	if _, err := ValidateUpdateImageRef("不 是 镜 像"); err == nil {
		t.Fatal("非法字符应被拒绝")
	}
}
