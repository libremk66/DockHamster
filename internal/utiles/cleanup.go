package utiles

import (
	"context"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
)

// CleanupOldImage 安全清理更新后遗留的旧镜像。
// 只有以下条件**全部满足**才删除（否则跳过，绝不冒险），返回是否真的清理了：
//  1. 没有任何容器（含已停止/已重命名的旧容器）引用它 —— 多容器共用镜像的保护
//  2. 它已没有任何 RepoTags（真正悬空；还挂着别的 tag 说明仍被使用）
//  3. 不等于更新后的新镜像
func CleanupOldImage(serviceContext *svc.ServiceContext, oldImageID string, newImageID string, enabled bool) (bool, error) {
	if !enabled {
		return false, nil
	}
	if oldImageID == "" || oldImageID == newImageID {
		return false, nil
	}
	ctx := context.Background()

	// 条件①：引用检查（所有容器，含已停止）
	list, err := serviceContext.DockerClient.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return false, fmt.Errorf("列出容器失败: %w", err)
	}
	for _, c := range list {
		if c.ImageID == oldImageID {
			logx.Infof("旧镜像 %s 仍被容器 %v 引用，跳过删除", oldImageID, c.Names)
			return false, nil
		}
	}

	// 条件②：tag 检查
	img, _, err := serviceContext.DockerClient.ImageInspectWithRaw(ctx, oldImageID)
	if err != nil {
		// 镜像已不存在（可能被别的机制清掉了）——视为无需清理
		logx.Infof("旧镜像 %s 查询失败（可能已不存在）: %v", oldImageID, err)
		return false, nil
	}
	if len(img.RepoTags) > 0 {
		logx.Infof("旧镜像 %s 仍有 tag %v，跳过删除", oldImageID, img.RepoTags)
		return false, nil
	}

	// 条件③：非强制删除；有子镜像等依赖时删除会失败，仅记录
	if _, err := serviceContext.DockerClient.ImageRemove(ctx, oldImageID, image.RemoveOptions{Force: false}); err != nil {
		return false, fmt.Errorf("删除旧镜像失败: %w", err)
	}
	logx.Infof("已清理旧镜像 %s", oldImageID)
	return true, nil
}
