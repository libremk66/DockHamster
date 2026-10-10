package utiles

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types/image"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
)

// RemoveImage 删除镜像。
// 行为：
//  1. 被其他镜像作为基础镜像引用（本地构建产物的父镜像）时直接拒绝——daemon 也无法强删；
//  2. 同一镜像 ID 存在多个标签引用时，逐个解除全部引用（否则 daemon 报
//     "image is referenced in multiple repositories"）；
//  3. 无标签的悬空镜像按 ID 删除。
func RemoveImage(ctx *svc.ServiceContext, imageRef string, force bool) error {
	ctx.DockerClient.NegotiateAPIVersion(context.Background())
	inspected, _, err := ctx.DockerClient.ImageInspectWithRaw(context.Background(), imageRef)
	if err != nil {
		return fmt.Errorf("获取镜像信息失败: %w", err)
	}
	if hasChildImage(ctx, inspected.ID) {
		return errors.New("该镜像被其他镜像依赖（作为基础镜像），无法删除")
	}

	refs := inspected.RepoTags
	if len(refs) == 0 {
		// 悬空镜像（无标签）：按 ID 删除
		if _, err := ctx.DockerClient.ImageRemove(context.Background(), inspected.ID, image.RemoveOptions{Force: force}); err != nil {
			return err
		}
		InvalidateImageParentCache()
		return nil
	}

	// 逐个解除标签引用；最后一个引用被解除时镜像本体随之删除
	var failures []string
	for _, ref := range refs {
		if _, err := ctx.DockerClient.ImageRemove(context.Background(), ref, image.RemoveOptions{Force: force}); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", ref, err))
			continue
		}
		logx.Infof("镜像引用已解除: %s", ref)
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	InvalidateImageParentCache()
	return nil
}

// hasChildImage 判断是否存在以该镜像为基础镜像的其他镜像
func hasChildImage(ctx *svc.ServiceContext, imageID string) bool {
	list, err := ctx.DockerClient.ImageList(context.Background(), image.ListOptions{})
	if err != nil {
		return false
	}
	for _, img := range list {
		if img.ID == imageID {
			continue
		}
		ins, _, err := ctx.DockerClient.ImageInspectWithRaw(context.Background(), img.ID)
		if err != nil {
			continue
		}
		if ins.Parent == imageID {
			return true
		}
	}
	return false
}
