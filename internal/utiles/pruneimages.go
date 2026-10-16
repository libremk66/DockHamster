package utiles

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
)

// pruneMaxRounds 单次「清理无Tag」的最大轮数。
// 每轮只删当前没有子镜像的无 Tag 镜像；删掉叶子后其父层才降级成叶子，需要下一轮才看得到，
// 一轮清不完，因此循环删到没有可删项为止；上限仅作兜底。
const pruneMaxRounds = 20

// PruneDanglingResult 一次清理的结果。
type PruneDanglingResult struct {
	Removed int `json:"removed"` // 实际删除的镜像数
	Skipped int `json:"skipped"` // 剩余无 Tag 但有子镜像的数量（构建中间层，即本地构建缓存）
	Rounds  int `json:"rounds"`  // 实际执行的轮数
}

// pruneCandidates 挑出本轮可删的无 Tag 镜像 ID，并统计仍被依赖的数量。
// 可删条件（全部满足）：无 RepoTags、无容器引用、没有子镜像。
// 无 Tag 但有子镜像的（本地构建中间层）daemon 会拒绝删除，且它们是构建缓存，统计为 skipped。
func pruneCandidates(images []image.Summary, inUse map[string]bool) (candidates []string, skipped int) {
	hasChild := make(map[string]bool, len(images))
	for _, img := range images {
		if img.ParentID != "" {
			hasChild[img.ParentID] = true
		}
	}
	for _, img := range images {
		if len(img.RepoTags) != 0 {
			continue
		}
		if inUse[img.ID] {
			continue
		}
		if hasChild[img.ID] {
			skipped++
			continue
		}
		candidates = append(candidates, img.ID)
	}
	return candidates, skipped
}

// PruneDanglingImages 反复清理无 Tag 镜像，直到一轮下来没有可删项或达到轮数上限。
// 返回累计删除数、剩余被依赖数、实际轮数。
func PruneDanglingImages(serviceContext *svc.ServiceContext) (PruneDanglingResult, error) {
	result := PruneDanglingResult{}
	ctx := context.Background()
	serviceContext.DockerClient.NegotiateAPIVersion(ctx)

	for round := 0; round < pruneMaxRounds; round++ {
		result.Rounds = round + 1

		containers, err := serviceContext.DockerClient.ContainerList(ctx, container.ListOptions{All: true})
		if err != nil {
			return result, fmt.Errorf("列出容器失败: %w", err)
		}
		inUse := make(map[string]bool, len(containers))
		for _, c := range containers {
			inUse[c.ImageID] = true
		}

		list, err := serviceContext.DockerClient.ImageList(ctx, image.ListOptions{All: true})
		if err != nil {
			return result, fmt.Errorf("列出镜像失败: %w", err)
		}

		candidates, skipped := pruneCandidates(list, inUse)
		result.Skipped = skipped
		if len(candidates) == 0 {
			break
		}

		removedThisRound := 0
		for _, id := range candidates {
			if _, err := serviceContext.DockerClient.ImageRemove(ctx, id, image.RemoveOptions{Force: false}); err != nil {
				logx.Infof("清理无Tag镜像 %s 失败，跳过: %v", id, err)
				continue
			}
			removedThisRound++
		}
		result.Removed += removedThisRound
		InvalidateImageParentCache()
		if removedThisRound == 0 {
			break
		}
	}
	return result, nil
}
