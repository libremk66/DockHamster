package utiles

import (
	"context"
	"github.com/docker/docker/api/types/container"
	"github.com/google/uuid"
	"github.com/onlyLTY/dockerCopilot/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
	"os"
	"strings"
)

// parseNameList 解析逗号分隔的容器名列表（大小写不敏感）
func parseNameList(raw string) map[string]bool {
	out := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if name != "" {
			out[name] = true
		}
	}
	return out
}

// RunAutoUpdate 按白名单自动更新容器（由 AutoUpdateCron 定时触发）。
//
// 环境变量：
//
//	AutoUpdateContainers 白名单：逗号分隔的容器名（大小写不敏感）；
//	                     "*" 表示除自身与排除名单外的全部容器；
//	                     不配置则不启用自动更新（保持纯手动模式）
//	AutoUpdateExclude    排除名单：逗号分隔，优先级高于白名单
//	DeleteOldImage       更新完成后删除旧镜像（默认开启；=false 关闭）
//
// 同一镜像被多个容器共用时：只拉取一次，再逐个容器重建（各自完成后旧镜像由引用计数保护）。
func RunAutoUpdate(serviceContext *svc.ServiceContext) {
	whitelist := parseNameList(os.Getenv("AutoUpdateContainers"))
	if len(whitelist) == 0 {
		return // 未配置白名单，不自动更新
	}
	exclude := parseNameList(os.Getenv("AutoUpdateExclude"))
	updateAll := whitelist["*"]
	delOldContainer := os.Getenv("DelOldContainer") != "false"

	ctx := context.Background()
	list, err := serviceContext.DockerClient.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		logx.Errorf("自动更新：获取容器列表失败 %v", err)
		return
	}

	type target struct {
		id, name, pullRef string
	}
	var targets []target
	for _, c := range list {
		if len(c.Names) == 0 {
			continue
		}
		name := strings.TrimPrefix(c.Names[0], "/")
		lower := strings.ToLower(name)
		if strings.Contains(strings.ToLower(c.Image), "dockercopilot") {
			continue // 不自动更新 DockCopilot 自身
		}
		if exclude[lower] {
			continue
		}
		if !updateAll && !whitelist[lower] {
			continue
		}
		if !serviceContext.HubImageInfo.NeedUpdate(c.ImageID) {
			continue
		}
		targets = append(targets, target{id: c.ID, name: name, pullRef: resolvePullRef(serviceContext, c.ImageID, c.Image)})
	}
	if len(targets) == 0 {
		return
	}

	// 按镜像分组：同一镜像只拉取一次
	groups := make(map[string][]target)
	for _, t := range targets {
		groups[t.pullRef] = append(groups[t.pullRef], t)
	}
	for pullRef, group := range groups {
		logx.Infof("自动更新：拉取 %s（%d 个容器共用）", pullRef, len(group))
		if err := PullImageByRef(serviceContext, pullRef); err != nil {
			logx.Errorf("自动更新：拉取 %s 失败，跳过本组：%v", pullRef, err)
			continue
		}
		for _, t := range group {
			taskID := uuid.New().String()
			logx.Infof("自动更新：开始更新容器 %s", t.name)
			if err := updateContainerCore(serviceContext, t.id, t.name, pullRef, delOldContainer, taskID, true); err != nil {
				logx.Errorf("自动更新：容器 %s 更新失败：%v", t.name, err)
			} else {
				logx.Infof("自动更新：容器 %s 更新完成", t.name)
			}
		}
	}
}

// resolvePullRef 优先使用镜像的正式 RepoTag（避免用镜像 ID/摘要作拉取目标）
func resolvePullRef(serviceContext *svc.ServiceContext, imageID string, fallback string) string {
	images, err := GetImagesList(serviceContext)
	if err == nil {
		for _, img := range images {
			if img.ID == imageID && len(img.RepoTags) > 0 {
				return img.RepoTags[0]
			}
		}
	}
	return fallback
}
