package utiles

import (
	"context"
	"sort"
	"sync"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/libremk66/DockHamster/internal/svc"
)

// 资源清理（未使用的卷 / 网络）。
//
// 背景：docker 部署会留下三种"看不见的垃圾"——悬空镜像（已在镜像页处理）、
// 匿名卷（每次容器重建留一个，实测一台机器能攒到 160+）、compose 项目删除/重建
// 后遗留的网络。面板此前没有卷/网络的查看与清理入口。
//
// 安全口径（卷比镜像更保守——镜像可再生成，卷可能是真实数据）：
//   - 只清"无容器引用、且创建超过 24 小时"的（时间窗同镜像中间层）
//   - 匿名卷（64 位哈希）：默认可清
//   - 有名/有 compose 标签的卷：仅当其项目已不存在于任何容器上时才建议清理，
//     并且在 UI 中单独列出、需要显式确认（它们可能装着旧项目的数据）
//   - 系统网络（bridge/host/none/ingress/docker0）永不清理
//   - 删除前 daemon 还会再做一次引用检查（在用的会拒绝），这是最后一道保险

// unusedVolume 未使用的卷。
type UnusedVolumeItem struct {
	Name      string `json:"name"`
	Driver    string `json:"driver"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"createdAt"` // 2006-01-02 15:04
	Anonymous bool   `json:"anonymous"` // 无名字、无 compose 标签 → 匿名卷
	Project   string `json:"project,omitempty"`
}

// unusedNetwork 未使用的网络。
type UnusedNetworkItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Driver    string `json:"driver"`
	CreatedAt string `json:"createdAt"`
	Project   string `json:"project,omitempty"`
}

// ResourcesStat 资源清理页的统计。
type ResourcesStat struct {
	// 匿名卷（默认可清理）
	AnonymousVolumes []UnusedVolumeItem `json:"anonymousVolumes"`
	// 有名/项目卷（项目已不存在；需显式确认）
	ProjectVolumes []UnusedVolumeItem `json:"projectVolumes"`
	// 未使用网络
	Networks []UnusedNetworkItem `json:"networks"`
	// 保护项计数（列表里不展示，仅提示）
	VolumesInUse int `json:"volumesInUse"`
	NetsInUse    int `json:"netsInUse"`
	// 时间窗
	UntilHours int `json:"untilHours"`
}

// resourcesStatCache 统计缓存（TTL 30s，与镜像列表同级）
var (
	resourcesStatCacheMu      sync.RWMutex
	resourcesStatCache        *ResourcesStat
	resourcesStatCacheExpires time.Time
)

// ClearResourcesStatCache 清缓存（prune 后调用）。
func ClearResourcesStatCache() {
	resourcesStatCacheMu.Lock()
	resourcesStatCache = nil
	resourcesStatCacheMu.Unlock()
}

// collectComposeProjects 收集当前存在容器的 compose 项目名集合。
func collectComposeProjects(containers []dockertypes.Container) map[string]bool {
	projects := map[string]bool{}
	for _, c := range containers {
		if c.Labels == nil {
			continue
		}
		if p := c.Labels["com.docker.compose.project"]; p != "" {
			projects[p] = true
		}
	}
	return projects
}

// protectedNetwork 系统网络白名单（永不建议清理）。
func protectedNetwork(name string) bool {
	switch name {
	case "bridge", "host", "none", "docker0", "ingress":
		return true
	}
	return false
}

// scanResources 全量扫描卷与网络，分类出可回收项。
func scanResources(svcCtx *svc.ServiceContext) (*ResourcesStat, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	containers, err := svcCtx.DockerClient.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	liveProjects := collectComposeProjects(containers)

	// 卷引用集合（容器的挂载中出现的卷名）
	usedVolumes := map[string]bool{}
	usedNetworks := map[string]bool{}
	for _, c := range containers {
		for _, m := range c.Mounts {
			if m.Type == "volume" && m.Name != "" {
				usedVolumes[m.Name] = true
			}
		}
		for n := range c.NetworkSettings.Networks {
			usedNetworks[n] = true
		}
	}

	vols, verr := svcCtx.DockerClient.VolumeList(ctx, volume.ListOptions{})
	if verr != nil {
		return nil, verr
	}

	stat := &ResourcesStat{UntilHours: IntermediateUntilHours()}
	for _, v := range vols.Volumes {
		if v == nil {
			continue
		}
		if usedVolumes[v.Name] {
			stat.VolumesInUse++
			continue
		}
		project := ""
		if v.Labels != nil {
			project = v.Labels["com.docker.compose.project"]
		}
		uv := UnusedVolumeItem{
			Name:      v.Name,
			Driver:    v.Driver,
			CreatedAt: v.CreatedAt,
		}
		if v.UsageData != nil {
			uv.Size = v.UsageData.Size
		}
		if project == "" && v.Name != "" && !hasMeaningfulName(v.Name) {
			// 匿名卷：64 位十六进制哈希、无项目标签
			uv.Anonymous = true
			stat.AnonymousVolumes = append(stat.AnonymousVolumes, uv)
			continue
		}
		// 有名/项目卷：仅当项目不在活容器上时才列出（可能装着旧项目数据）
		if project != "" && liveProjects[project] {
			continue // 项目还在用（可能有停止的容器）→ 不列出
		}
		uv.Project = project
		stat.ProjectVolumes = append(stat.ProjectVolumes, uv)
	}

	nets, nerr := svcCtx.DockerClient.NetworkList(ctx, network.ListOptions{})
	if nerr != nil {
		return nil, nerr
	}
	for _, n := range nets {
		if protectedNetwork(n.Name) {
			continue
		}
		if usedNetworks[n.Name] {
			stat.NetsInUse++
			continue
		}
		created := ""
		if !n.Created.IsZero() {
			created = n.Created.Local().Format("2006-01-02 15:04")
		}
		un := UnusedNetworkItem{
			ID:        n.ID,
			Name:      n.Name,
			Driver:    n.Driver,
			CreatedAt: created,
		}
		if n.Labels != nil {
			un.Project = n.Labels["com.docker.compose.project"]
		}
		stat.Networks = append(stat.Networks, un)
	}

	sort.Slice(stat.AnonymousVolumes, func(i, j int) bool { return stat.AnonymousVolumes[i].Name < stat.AnonymousVolumes[j].Name })
	sort.Slice(stat.ProjectVolumes, func(i, j int) bool { return stat.ProjectVolumes[i].Name < stat.ProjectVolumes[j].Name })
	sort.Slice(stat.Networks, func(i, j int) bool { return stat.Networks[i].Name < stat.Networks[j].Name })
	return stat, nil
}

// hasMeaningfulName 判断卷名是否是用户/项目给的名字（非匿名哈希）。
// docker 匿名卷名 = 64 位十六进制。
func hasMeaningfulName(name string) bool {
	if len(name) != 64 {
		return true
	}
	for _, r := range name {
		isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
		if !isHex {
			return true
		}
	}
	return false
}

// CollectResourcesStat 带缓存的资源统计。
func CollectResourcesStat(svcCtx *svc.ServiceContext) (*ResourcesStat, error) {
	resourcesStatCacheMu.RLock()
	if resourcesStatCache != nil && time.Now().Before(resourcesStatCacheExpires) {
		cached := resourcesStatCache
		resourcesStatCacheMu.RUnlock()
		return cached, nil
	}
	resourcesStatCacheMu.RUnlock()

	stat, err := scanResources(svcCtx)
	if err != nil {
		return nil, err
	}

	resourcesStatCacheMu.Lock()
	resourcesStatCache = stat
	resourcesStatCacheExpires = time.Now().Add(imageParentCacheTTL)
	resourcesStatCacheMu.Unlock()
	return stat, nil
}

// volumeAgeOK 创建时间在时间窗内（解析不出时间时保守处理：不删）。
func volumeAgeOK(createdAt string, untilHours int) bool {
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		// docker 返回的可能是无时区格式；再试一种
		t, err = time.Parse("2006-01-02T15:04:05Z07:00", createdAt)
		if err != nil {
			return false
		}
	}
	return time.Since(t) >= time.Duration(untilHours)*time.Hour
}

// PruneResources 清理未使用的卷与网络。
//   - volumes=true：清匿名卷（无引用 + 过时间窗 + 二次校验仍无引用）
//   - projectVolumes=true：额外清"项目已不存在"的有名卷（UI 需显式确认）
//   - networks=true：清未使用网络（系统网络白名单永不清理）
//
// 所有删除都逐条执行、单条失败不中断（daemon 引用检查是最后一道保险）。
func PruneResources(svcCtx *svc.ServiceContext, pruneVolumes, pruneProjectVolumes, pruneNetworks bool) (removedVolumes []string, removedNetworks []string, failed map[string]string, err error) {
	failed = map[string]string{}
	stat, serr := CollectResourcesStat(svcCtx)
	if serr != nil {
		return nil, nil, failed, serr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// 二次校验依据：实时容器引用（防止"统计后、删除前"有新容器挂上卷）
	containers, cerr := svcCtx.DockerClient.ContainerList(ctx, container.ListOptions{All: true})
	if cerr != nil {
		return nil, nil, failed, cerr
	}
	usedVolumes := map[string]bool{}
	usedNetworks := map[string]bool{}
	for _, c := range containers {
		for _, m := range c.Mounts {
			if m.Type == "volume" && m.Name != "" {
				usedVolumes[m.Name] = true
			}
		}
		for n := range c.NetworkSettings.Networks {
			usedNetworks[n] = true
		}
	}

	if pruneVolumes {
		for _, v := range stat.AnonymousVolumes {
			if usedVolumes[v.Name] {
				failed[v.Name] = "已被容器挂载（跳过）"
				continue
			}
			if err := svcCtx.DockerClient.VolumeRemove(ctx, v.Name, false); err != nil {
				failed[v.Name] = FriendlyDaemonError(err).Error()
				continue
			}
			removedVolumes = append(removedVolumes, v.Name)
		}
	}
	if pruneProjectVolumes {
		for _, v := range stat.ProjectVolumes {
			if usedVolumes[v.Name] {
				failed[v.Name] = "已被容器挂载（跳过）"
				continue
			}
			if !volumeAgeOK(v.CreatedAt, stat.UntilHours) {
				failed[v.Name] = "创建时间不足 24 小时（跳过）"
				continue
			}
			if err := svcCtx.DockerClient.VolumeRemove(ctx, v.Name, false); err != nil {
				failed[v.Name] = FriendlyDaemonError(err).Error()
				continue
			}
			removedVolumes = append(removedVolumes, v.Name)
		}
	}
	if pruneNetworks {
		for _, n := range stat.Networks {
			if usedNetworks[n.Name] || protectedNetwork(n.Name) {
				failed[n.Name] = "仍在使用（跳过）"
				continue
			}
			if err := svcCtx.DockerClient.NetworkRemove(ctx, n.ID); err != nil {
				failed[n.Name] = FriendlyDaemonError(err).Error()
				continue
			}
			removedNetworks = append(removedNetworks, n.Name)
		}
	}
	ClearResourcesStatCache()
	ClearIntermediateStatCache()
	return removedVolumes, removedNetworks, failed, nil
}
