package utiles

import (
	"context"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

// perNetworkMACMinAPIVersion：「按网络指定 MAC 地址」所需的最低 Docker API 版本。
// 低于它时，创建请求里若带每网络 MacAddress，daemon 会直接拒绝：
//
//	"specify mac-address per network" requires API version 1.44, but the Docker daemon API version is X
//
// 群晖 DSM 自带的 Docker 24.0.x 就是 API 1.43 —— 这是"更新容器 / 面板自更新 / 迁移导出"
// 在群晖上全部失败的根因（Issue #4）。
const perNetworkMACMinAPIVersion = "1.44"

// NetworkingConfigForRecreate 由旧容器的 inspect 结果构造"重建用"的网络配置。
//
// 不能整块照抄 NetworkSettings.Networks：里面混着 daemon 运行时回填的字段
// （IPAddress / EndpointID / MacAddress …），既不是用户配置，其中每网络 MacAddress
// 还带 API 版本门槛（见上）。这里只保留用户真正设置过的部分：
//   - 保留：静态 IP（IPAMConfig）、别名（Aliases）、Links、DriverOpts
//   - MacAddress：仅在 daemon 支持（API ≥ 1.44）时带上；老 daemon 上它本来也无法被设置，
//     丢掉后由 Docker 重新分配（用户若用顶层 Config.MacAddress 固定过 MAC，那个字段
//     随 Config 原样传递，不受影响）
func NetworkingConfigForRecreate(cli *client.Client, inspect types.ContainerJSON) *network.NetworkingConfig {
	return sanitizeNetworkingConfig(inspect, perNetworkMACAllowed(cli))
}

// perNetworkMACAllowed 判断"这次请求能不能带每网络 MacAddress"。
//
// 关键：要比的是**客户端实际会使用的 API 版本**（协商结果，或被 DOCKER_API_VERSION 固定住的版本），
// 而不是 daemon 支持的最高版本 —— daemon 是按请求里的版本号做校验的。
// 客户端版本拿不到时（既没协商也没固定），保守地回退到 daemon 版本判断。
func perNetworkMACAllowed(cli *client.Client) bool {
	if cli == nil {
		return false
	}
	version := cli.ClientVersion()
	if version == "" {
		v, err := cli.ServerVersion(context.Background())
		if err != nil {
			return false
		}
		version = v.APIVersion
	}
	return apiVersionAtLeast(version, perNetworkMACMinAPIVersion)
}

// sanitizeNetworkingConfig 是 NetworkingConfigForRecreate 的纯函数内核（便于单测）。
func sanitizeNetworkingConfig(inspect types.ContainerJSON, allowPerNetworkMAC bool) *network.NetworkingConfig {
	endpoints := make(map[string]*network.EndpointSettings)
	if inspect.NetworkSettings != nil {
		for name, src := range inspect.NetworkSettings.Networks {
			if name == "" || src == nil {
				continue
			}
			dst := &network.EndpointSettings{
				IPAMConfig: src.IPAMConfig,
				Links:      src.Links,
				Aliases:    src.Aliases,
				DriverOpts: src.DriverOpts,
			}
			if allowPerNetworkMAC {
				dst.MacAddress = src.MacAddress
			}
			endpoints[name] = dst
		}
	}
	return &network.NetworkingConfig{EndpointsConfig: endpoints}
}

// apiVersionAtLeast 比较形如 "1.44" 的 API 版本号；解析失败按"不满足"处理（保守：宁可不带 MAC）。
func apiVersionAtLeast(version, want string) bool {
	major, minor, ok := parseAPIVersion(version)
	if !ok {
		return false
	}
	wantMajor, wantMinor, ok := parseAPIVersion(want)
	if !ok {
		return false
	}
	if major != wantMajor {
		return major > wantMajor
	}
	return minor >= wantMinor
}

// parseAPIVersion 解析 Docker 的 "major.minor" 版本号（如 "1.44"）；多余的段忽略。
func parseAPIVersion(s string) (int, int, bool) {
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return major, minor, true
}
