package utiles

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/libremk66/DockHamster/internal/svc"
)

// 构建中间层（classic builder 产物：无标签、无引用、Parent 相连）的统计与清理。
//
// 背景：面板默认的镜像列表接口（不带 all=true）看不见中间层，用户没有 docker CLI
// 时无从下手，只能越攒越多（实测一台机器攒到 262 个 / 19.2 GB）。
//
// 清理通道用 daemon 的 ImagePrune（dangling 过滤）——这是唯一能级联处理父层依赖的方式，
// 逐个 ImageRemove 会因 "image has dependent child images" 失败。
// 两条安全底线（开发笔记里已认可的口径）：
//  1. 绝不加 dangling=false（即 docker prune -a）——只删无标签的，不碰"有标签但没人用"的；
//  2. 绝不 force——靠 daemon 自己的引用检查兜底：在用 / 被依赖的自动跳过，保证删不错。
//
// 默认只清 24 小时前的层（ImagePrune 的 until 过滤）：刚构建完还在调试的中间层不动，
// 避免把"下一轮构建的缓存"误删；对外解释口径 = 最坏只是下次构建慢一点。

// intermediateUntilHours 默认清理时间窗（小时）；只清该时间之前创建的中间层。
const intermediateUntilHours = 24

// IntermediateUntilHours 默认清理时间窗（供 UI 提示文案引用）。
func IntermediateUntilHours() int { return intermediateUntilHours }

var (
	intermediateStatCacheMu      sync.RWMutex
	intermediateStatCache        *ImageIntermediateStat
	intermediateStatCacheExpires time.Time
)

// ImageIntermediateStat 构建中间层的汇总信息（Compose 页/镜像页的透明提示用）。
type ImageIntermediateStat struct {
	Count     int   `json:"count"`     // 无标签、无引用的中间层数量
	TotalSize int64 `json:"totalSize"` // 合计字节数
	InUse     int   `json:"inUse"`     // 无标签但仍被容器使用的（不可清）
}

// ClearIntermediateStatCache 清缓存（prune 执行后调用，让统计立即反映结果）。
func ClearIntermediateStatCache() {
	intermediateStatCacheMu.Lock()
	intermediateStatCache = nil
	intermediateStatCacheMu.Unlock()
}

// CollectIntermediateImages 扫描无标签的构建中间层（带 30s TTL 缓存）。
// 用镜像列表的 all=true 全量 + 容器的引用计数判断，不依赖 ImageInspect（快）。
func CollectIntermediateImages(svcCtx *svc.ServiceContext) (*ImageIntermediateStat, error) {
	intermediateStatCacheMu.RLock()
	if intermediateStatCache != nil && time.Now().Before(intermediateStatCacheExpires) {
		cached := *intermediateStatCache
		intermediateStatCacheMu.RUnlock()
		return &cached, nil
	}
	intermediateStatCacheMu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	images, err := svcCtx.DockerClient.ImageList(ctx, image.ListOptions{All: true})
	if err != nil {
		return nil, err
	}

	inUse := map[string]bool{}
	containers, err := svcCtx.DockerClient.ContainerList(ctx, container.ListOptions{All: true})
	if err == nil {
		for _, c := range containers {
			inUse[c.ImageID] = true
		}
	}

	stat := &ImageIntermediateStat{}
	untilHours := IntermediateUntilHours()
	for _, img := range images {
		if len(img.RepoTags) > 0 {
			continue
		}
		if inUse[img.ID] {
			stat.InUse++
			continue
		}
		// 统计口径与 PruneIntermediateImages 的 24h 时间窗保持一致，
		// 否则界面会显示“可清理 N 个”，实际点下去一个都不删。
		if !intermediateAgeEligible(img.Created, untilHours, time.Now()) {
			continue
		}
		stat.Count++
		stat.TotalSize += img.Size
	}

	intermediateStatCacheMu.Lock()
	intermediateStatCache = stat
	intermediateStatCacheExpires = time.Now().Add(imageParentCacheTTL)
	intermediateStatCacheMu.Unlock()
	return stat, nil
}

// intermediateAgeEligible 判断无 Tag 镜像是否已超过清理时间窗。
// untilHours<=0 时视为不过滤（测试/立即清理）。
func intermediateAgeEligible(createdUnix int64, untilHours int, now time.Time) bool {
	if untilHours <= 0 {
		return true
	}
	return createdUnix <= now.Add(-time.Duration(untilHours)*time.Hour).Unix()
}

// PruneIntermediateImages 清理无标签的构建中间层。
// untilHours <= 0 时不带时间过滤（测试/立即清理用）；返回删除数、回收字节数与实际时间窗。
func PruneIntermediateImages(svcCtx *svc.ServiceContext, untilHours int) (deleted int, space int64, usedHours int, err error) {
	args := filters.NewArgs()
	args.Add("dangling", "true")
	usedHours = untilHours
	if untilHours > 0 {
		args.Add("until", fmt.Sprintf("%dh", untilHours))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	report, err := svcCtx.DockerClient.ImagesPrune(ctx, args)
	if err != nil {
		return 0, 0, usedHours, err
	}
	// 统计口径：ImagesDeleted 里 Deleted 条目=层被删，Untagged 条目=解除标签引用
	deleted = len(report.ImagesDeleted)
	space = int64(report.SpaceReclaimed)
	ClearIntermediateStatCache()
	return deleted, space, usedHours, nil
}
