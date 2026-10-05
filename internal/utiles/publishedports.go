package utiles

import (
	"sort"
	"strconv"

	"github.com/docker/docker/api/types"
)

// PublishedTCPPorts 提取容器对外发布的 TCP 端口（供概览页快捷导航用）。
// 跳过仅绑定回环（127.0.0.1 / ::1）的发布，避免生成从外部点不开的链接；
// 结果去重并按端口号升序排列。
func PublishedTCPPorts(c types.ContainerJSON) []string {
	if c.NetworkSettings == nil {
		return nil
	}
	seen := make(map[string]bool)
	var nums []int
	for port, bindings := range c.NetworkSettings.Ports {
		if port.Proto() != "tcp" {
			continue
		}
		for _, b := range bindings {
			if b.HostPort == "" || b.HostIP == "127.0.0.1" || b.HostIP == "::1" {
				continue
			}
			n, err := strconv.Atoi(b.HostPort)
			if err != nil || seen[b.HostPort] {
				continue
			}
			seen[b.HostPort] = true
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)
	ports := make([]string, 0, len(nums))
	for _, n := range nums {
		ports = append(ports, strconv.Itoa(n))
	}
	return ports
}
