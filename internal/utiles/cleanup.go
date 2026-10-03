package utiles

import (
	"context"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/onlyLTY/dockerCopilot/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
	"os"
)

// CleanupOldImage 安全清理更新后遗留的旧镜像。
// 只有以下条件**全部满足**才删除（否则跳过，绝不冒险）：
//  1. 没有任何容器（含已停止/已重命名的旧容器）引用它 —— 多容器共用镜像的保护
//  2. 它已没有任何 RepoTags（真正悬空；还挂着别的 tag 说明仍被使用）
//  3. 不等于更新后的新镜像
//
// 环境变量 DeleteOldImage=false 可整体关闭（默认开启）。
func CleanupOldImage(serviceContext *svc.ServiceContext, oldImageID string, newImageID string) error {
	if os.Getenv("DeleteOldImage") == "false" {
		return nil
	}
	if oldImageID == "" || oldImageID == newImageID {
		return nil
	}
	ctx := context.Background()

	// 条件①：引用检查（所有容器，含已停止）
	list, err := serviceContext.DockerClient.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return fmt.Errorf("列出容器失败: %w", err)
	}
	for _, c := range list {
		if c.ImageID == oldImageID {
			logx.Infof("旧镜像 %s 仍被容器 %v 引用，跳过删除", oldImageID, c.Names)
			return nil
		}
	}

	// 条件②：tag 检查
	img, _, err := serviceContext.DockerClient.ImageInspectWithRaw(ctx, oldImageID)
	if err != nil {
		// 镜像已不存在（可能被别的机制清掉了）——视为已清理
		logx.Infof("旧镜像 %s 查询失败（可能已不存在）: %v", oldImageID, err)
		return nil
	}
	if len(img.RepoTags) > 0 {
		logx.Infof("旧镜像 %s 仍有 tag %v，跳过删除", oldImageID, img.RepoTags)
		return nil
	}

	// 条件③：非强制删除；有子镜像等依赖时删除会失败，仅记录
	if _, err := serviceContext.DockerClient.ImageRemove(ctx, oldImageID, image.RemoveOptions{Force: false}); err != nil {
		return fmt.Errorf("删除旧镜像失败: %w", err)
	}
	logx.Infof("已清理旧镜像 %s", oldImageID)
	return nil
}
