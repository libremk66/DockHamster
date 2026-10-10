package utiles

import (
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/network"
)

func demoInspect() types.ContainerJSON {
	return types.ContainerJSON{
		NetworkSettings: &types.NetworkSettings{
			Networks: map[string]*network.EndpointSettings{
				"bridge": {
					// 用户设置过的：
					IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: "172.18.0.10"},
					Aliases:    []string{"web", "web-alias"},
					Links:      []string{"db:db"},
					DriverOpts: map[string]string{"com.example.opt": "1"},
					// daemon 运行时回填的（不该被复制）：
					MacAddress: "02:42:ac:11:00:02",
					IPAddress:  "172.17.0.2",
					EndpointID: "abc123",
					Gateway:    "172.17.0.1",
				},
			},
		},
	}
}

// 老 daemon（API < 1.44）：必须丢掉每网络 MacAddress，其余用户配置保留
func TestSanitizeDropsMACForOldDaemon(t *testing.T) {
	nc := sanitizeNetworkingConfig(demoInspect(), false)
	ep, ok := nc.EndpointsConfig["bridge"]
	if !ok {
		t.Fatal("bridge 网络配置丢失")
	}
	if ep.MacAddress != "" {
		t.Fatalf("老 daemon 下不应带 MacAddress，实际 %q", ep.MacAddress)
	}
	if ep.IPAddress != "" || ep.EndpointID != "" || ep.Gateway != "" {
		t.Fatalf("运行时回填字段不应被复制: %+v", ep)
	}
	if ep.IPAMConfig == nil || ep.IPAMConfig.IPv4Address != "172.18.0.10" {
		t.Fatalf("静态 IP 配置应保留: %+v", ep.IPAMConfig)
	}
	if len(ep.Aliases) != 2 || len(ep.Links) != 1 || ep.DriverOpts["com.example.opt"] != "1" {
		t.Fatalf("别名/Links/DriverOpts 应保留: %+v", ep)
	}
}

// 新 daemon（API >= 1.44）：MacAddress 照旧保留（与修复前行为一致）
func TestSanitizeKeepsMACForNewDaemon(t *testing.T) {
	nc := sanitizeNetworkingConfig(demoInspect(), true)
	if ep := nc.EndpointsConfig["bridge"]; ep == nil || ep.MacAddress != "02:42:ac:11:00:02" {
		t.Fatalf("新 daemon 下 MacAddress 应保留: %+v", nc.EndpointsConfig["bridge"])
	}
}

// 空/缺数据不 panic，且返回非 nil 结构
func TestSanitizeNilSafety(t *testing.T) {
	cases := []types.ContainerJSON{
		{},
		{NetworkSettings: &types.NetworkSettings{}},
		{NetworkSettings: &types.NetworkSettings{Networks: map[string]*network.EndpointSettings{"bridge": nil}}},
	}
	for i, in := range cases {
		nc := sanitizeNetworkingConfig(in, false)
		if nc == nil || nc.EndpointsConfig == nil {
			t.Fatalf("case %d 应返回非 nil 配置", i)
		}
		if len(nc.EndpointsConfig) != 0 {
			t.Fatalf("case %d 应为空配置: %+v", i, nc.EndpointsConfig)
		}
	}
}

func TestAPIVersionAtLeast(t *testing.T) {
	cases := []struct {
		version, want string
		expect        bool
	}{
		{"1.43", "1.44", false},
		{"1.44", "1.44", true},
		{"1.45", "1.44", true},
		{"1.9", "1.44", false},
		{"2.0", "1.44", true},
		{"1.50", "1.44", true},
		{"1.44.1", "1.44", true},
		{"", "1.44", false},
		{"abc", "1.44", false},
		{"1", "1.44", false},
	}
	for _, c := range cases {
		if got := apiVersionAtLeast(c.version, c.want); got != c.expect {
			t.Fatalf("apiVersionAtLeast(%q, %q) = %v，期望 %v", c.version, c.want, got, c.expect)
		}
	}
}
