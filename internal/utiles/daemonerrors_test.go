package utiles

import (
	"errors"
	"strings"
	"testing"
)

// 翻译器：命中 → 中文 + 保留原文；未命中 → 原样返回
func TestFriendlyDaemonError(t *testing.T) {
	cases := []struct {
		raw        string
		wantSubstr []string
	}{
		{
			`Error response from daemon: conflict: unable to delete 215808b37a5f (cannot be forced) - image is being used by running container b0af3245ee21`,
			[]string{"镜像正在被容器", "b0af3245ee21", "请先停止该容器", "原始错误"},
		},
		{
			`Error response from daemon: conflict: unable to delete abc - image has dependent child images`,
			[]string{"被其他镜像作为基础镜像依赖", "无法直接删除"},
		},
		{
			`Error response from daemon: driver failed programming external connectivity: Bind for 0.0.0.0:8080 failed: port is already allocated`,
			[]string{"宿主端口", "8080", "已被占用", "端口映射"},
		},
		{
			`Error response from daemon: Conflict. The container name "/web" is already in use by container "abc".`,
			[]string{"已有同名容器", "web", "删除或改名"},
		},
		{
			`Error response from daemon: invalid mount config for type "bind": bind source path does not exist: /mnt/data`,
			[]string{"挂载的宿主路径", "/mnt/data", "不存在"},
		},
		{
			`创建新容器失败: "specify mac-address per network" requires API version 1.44, but the Docker daemon API version is 1.43`,
			[]string{"Docker 版本过老", "升级"},
		},
		{
			`toomanyrequests: You have reached your unauthenticated pull rate limit.`,
			[]string{"Docker Hub 匿名拉取已达上限", "加速源"},
		},
		{
			`Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?`,
			[]string{"连不上 Docker", "docker.sock"},
		},
		{
			`Error response from daemon: No such container: web`,
			[]string{"容器不存在"},
		},
	}
	for i, c := range cases {
		got := FriendlyDaemonError(errors.New(c.raw)).Error()
		for _, want := range c.wantSubstr {
			if !strings.Contains(got, want) {
				t.Fatalf("case %d 缺少 %q：\n%s", i, want, got)
			}
		}
	}
}

// 未命中任何模式：原样返回（不丢信息）
func TestFriendlyDaemonErrorPassthrough(t *testing.T) {
	raw := "some totally unknown daemon failure xyz"
	if got := FriendlyDaemonError(errors.New(raw)).Error(); got != raw {
		t.Fatalf("未命中应原样返回，实际 %q", got)
	}
	if FriendlyDaemonError(nil) != nil {
		t.Fatal("nil 应返回 nil")
	}
}
