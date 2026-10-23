package module

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/registry"
)

func TestNormalizeRegistryHost(t *testing.T) {
	cases := map[string]string{
		"ghcr.io":                        "ghcr.io",
		"GHCR.IO":                        "ghcr.io",
		"https://ghcr.io/":               "ghcr.io",
		"https://index.docker.io/v1/":    "docker.io",
		"registry-1.docker.io":           "docker.io",
		"registry.docker.io":             "docker.io",
		"localhost:5000":                 "localhost:5000",
		"http://192.168.1.2:5000/path":   "192.168.1.2:5000",
		"  ghcr.io  ":                    "ghcr.io",
		"":                               "",
		"user@ghcr.io":                   "",
		"https://ghcr.io/v2/owner/pkg/":  "ghcr.io",
		"https://quay.io/repository/x/y": "quay.io",
	}
	for in, want := range cases {
		if got := NormalizeRegistryHost(in); got != want {
			t.Errorf("NormalizeRegistryHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRegistryHostOf(t *testing.T) {
	cases := map[string]string{
		"nginx:latest":                     "docker.io",
		"libremk66/dockhamster:latest":     "docker.io",
		"docker.io/library/nginx:1.25":     "docker.io",
		"ghcr.io/libremk66/hamsterdash":    "ghcr.io",
		"ghcr.io/libremk66/hamsterdash:v1": "ghcr.io",
		"localhost:5000/app:dev":           "localhost:5000",
		"registry.example.com/team/app":    "registry.example.com",
		"":                                 "",
	}
	for in, want := range cases {
		if got := RegistryHostOf(in); got != want {
			t.Errorf("RegistryHostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRegistryAuthSaveLoadAndEncoding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry-auth.json")
	t.Setenv("RegistryAuthConfigFile", path)

	s := NewRegistryAuthStore()
	if err := s.Save(RegistryAuthSettings{Entries: []RegistryAuthEntry{
		{Registry: "https://index.docker.io/v1/", Username: "hubuser", Password: "hubpass"},
		{Registry: "GHCR.IO", Username: "ghuser", Password: "ghpat"},
		{Registry: "ghcr.io", Username: "ghuser2", Password: "ghpat2"}, // 同 host 后写覆盖
		{Registry: "", Username: "x", Password: "y"},                   // 无 host：丢弃
	}}); err != nil {
		t.Fatalf("save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("凭据文件权限 = %o, want 0600", perm)
	}

	// 重新加载（模拟重启）
	s2 := NewRegistryAuthStore()
	got := s2.Get()
	if len(got.Entries) != 2 {
		t.Fatalf("entries = %+v, want 2", got.Entries)
	}
	if e, ok := s2.Lookup("ghcr.io/libremk66/hamsterdash:latest"); !ok || e.Username != "ghuser2" || e.Password != "ghpat2" {
		t.Fatalf("ghcr lookup = %+v ok=%v", e, ok)
	}
	if e, ok := s2.Lookup("nginx:latest"); !ok || e.Username != "hubuser" {
		t.Fatalf("docker hub lookup = %+v ok=%v", e, ok)
	}
	if _, ok := s2.Lookup("registry.example.com/team/app:latest"); ok {
		t.Fatal("未配置的 registry 不应命中")
	}

	// DaemonAuth = base64url(JSON AuthConfig)
	decoded, err := registry.DecodeAuthConfig(s2.DaemonAuth("ghcr.io/libremk66/hamsterdash:latest"))
	if err != nil {
		t.Fatalf("decode auth config: %v", err)
	}
	if decoded.Username != "ghuser2" || decoded.Password != "ghpat2" || decoded.ServerAddress != "ghcr.io" {
		t.Fatalf("daemon auth = %+v", decoded)
	}

	// BasicAuth = base64("user:pass")
	raw, err := base64.StdEncoding.DecodeString(s2.BasicAuth("ghcr.io/x/y"))
	if err != nil {
		t.Fatalf("decode basic: %v", err)
	}
	if string(raw) != "ghuser2:ghpat2" {
		t.Fatalf("basic auth = %q", raw)
	}

	// 无凭据的引用 → 空串（匿名）
	if s2.DaemonAuth("registry.example.com/team/app:latest") != "" {
		t.Fatal("未配置 registry 的 DaemonAuth 应为空")
	}
	if s2.BasicAuth("registry.example.com/team/app:latest") != "" {
		t.Fatal("未配置 registry 的 BasicAuth 应为空")
	}
}

func TestPreserveRegistryPasswords(t *testing.T) {
	old := RegistryAuthSettings{Entries: []RegistryAuthEntry{
		{Registry: "ghcr.io", Username: "u", Password: "oldpass"},
	}}
	in := RegistryAuthSettings{Entries: []RegistryAuthEntry{
		{Registry: "ghcr.io", Username: "u", Password: ""},     // 留空 → 沿用
		{Registry: "quay.io", Username: "q", Password: ""},     // 新条目 → 仍为空
		{Registry: "docker.io", Username: "d", Password: "np"}, // 显式新密码
	}}
	out := NormalizeRegistryAuthSettings(PreserveRegistryPasswords(old, in))
	byHost := map[string]string{}
	for _, e := range out.Entries {
		byHost[e.Registry] = e.Password
	}
	if byHost["ghcr.io"] != "oldpass" {
		t.Fatalf("ghcr 密码应沿用旧值，得到 %q", byHost["ghcr.io"])
	}
	if byHost["docker.io"] != "np" {
		t.Fatalf("docker.io 密码应使用新值，得到 %q", byHost["docker.io"])
	}
	if byHost["quay.io"] != "" {
		t.Fatalf("quay.io 无旧密码应为空，得到 %q", byHost["quay.io"])
	}
}

func TestRegistryAuthNilSafety(t *testing.T) {
	var s *RegistryAuthStore
	if s.DaemonAuth("ghcr.io/a/b") != "" || s.BasicAuth("ghcr.io/a/b") != "" {
		t.Fatal("nil store 应返回空凭据")
	}
	if _, ok := s.Lookup("ghcr.io/a/b"); ok {
		t.Fatal("nil store Lookup 应为 false")
	}
	if err := s.Save(RegistryAuthSettings{}); err != nil {
		t.Fatalf("nil store Save 应无害: %v", err)
	}
	if len(s.Get().Entries) != 0 {
		t.Fatal("nil store Get 应为空")
	}
}

func TestNormalizeRegistryAuthSettingsDedupe(t *testing.T) {
	out := NormalizeRegistryAuthSettings(RegistryAuthSettings{Entries: []RegistryAuthEntry{
		{Registry: "ghcr.io", Username: "a", Password: "1"},
		{Registry: "https://GHCR.io/", Username: "b", Password: "2"},
		{Registry: "ghcr.io", Username: "", Password: ""}, // 空条目：丢弃
	}})
	if len(out.Entries) != 1 || out.Entries[0].Username != "b" {
		t.Fatalf("去重结果 = %+v", out.Entries)
	}
	if !strings.EqualFold(out.Entries[0].Registry, "ghcr.io") {
		t.Fatalf("registry 未归一化: %q", out.Entries[0].Registry)
	}
}
