package utiles

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	dockerMsgType "github.com/docker/docker/pkg/jsonmessage"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
	"io"
	"strings"
	"time"
)

// UpdateOptions 更新行为选项
type UpdateOptions struct {
	SkipPull        bool // 跳过拉取（批量更新中同一镜像已由调用方统一拉取）
	DelOldContainer bool // 更新完成后删除旧容器（false=保留改名后的旧容器）
	DeleteOldImage  bool // 兼容旧调用：true 等价于 OldImagePolicy="clean"
	// 旧镜像处置策略：clean=安全清理 / snapshot=打快照保留 / keep=不处理（空则回退 DeleteOldImage）
	OldImagePolicy string
	// 快照参数（仅策略为 snapshot 时使用）
	SnapshotOptions SnapshotOptions
}

// UpdateContainer 更新单个容器（UI「更新」按钮与回滚共用入口）
func UpdateContainer(serviceContext *svc.ServiceContext, id string, name string, imageNameAndTag string, opts UpdateOptions, taskID string) error {
	_, err := updateContainerCore(serviceContext, id, name, imageNameAndTag, opts, taskID)
	return err
}

// resolvePolicy 归一化策略：显式策略优先，否则回退旧布尔
func (o UpdateOptions) resolvePolicy() string {
	switch o.OldImagePolicy {
	case "clean", "snapshot", "keep":
		return o.OldImagePolicy
	}
	if o.DeleteOldImage {
		return "clean"
	}
	return "keep"
}

// updateContainerCore 更新容器；返回旧镜像处置结果（是否清理 / 快照引用）。
func updateContainerCore(serviceContext *svc.ServiceContext, id string, name string, imageNameAndTag string, opts UpdateOptions, taskID string) (OldImageOutcome, error) {
	ctx := context.Background()
	serviceContext.UpdateProgress(taskID, svc.TaskProgress{
		TaskID:     taskID,
		Percentage: 0,
		Name:       name,
		Message:    "正在连接Docker",
		DetailMsg:  "正在连接Docker",
		IsDone:     false,
	})
	var oldTaskProgress, result = serviceContext.GetProgress(taskID)
	if !result {
		oldTaskProgress = svc.TaskProgress{
			Percentage: 0,
			Name:       "",
			Message:    "",
			DetailMsg:  "",
			IsDone:     false,
		}
	}
	timeout := 10
	signal := "SIGINT"

	serviceContext.UpdateProgress(taskID, oldTaskProgress)
	// 心跳：每 2 秒刷新一次耗时，长阶段不会"看起来卡死"
	hbStop := make(chan struct{})
	defer close(hbStop)
	go progressHeartbeat(serviceContext, taskID, hbStop)
	serviceContext.DockerClient.NegotiateAPIVersion(ctx)
	// 更新期间旧容器会被停止/改名，让守护模块暂时静默
	NoteMaintenance(serviceContext, name, 15*time.Minute)

	// compose 管理的容器走 compose 通道重建（保持 labels/配置漂移一致），失败不回退
	// API 重建——两通道行为差异大，静默混用会造成 compose 配置漂移，宁可让用户看到明确错误。
	if meta, mErr := ComposeMetaOfContainer(serviceContext, id); mErr == nil && meta.IsManaged {
		logx.Infof("容器 %s 由 compose 管理（%s），分流到 compose 更新通道", name, meta.UpdateRef())
		return OldImageOutcome{}, UpdateContainerViaCompose(serviceContext, meta, imageNameAndTag, opts.SkipPull, taskID)
	}

	if !opts.SkipPull {
		oldTaskProgress.Message = "正在拉取新镜像"
		oldTaskProgress.Percentage = 5
		oldTaskProgress.DetailMsg = "正在拉取新镜像"
		serviceContext.UpdateProgress(taskID, oldTaskProgress)
		// 配置了默认加速源时走加速源拉取（失败自动回退直连）
		if err := PullImageForUpdate(serviceContext, taskID, imageNameAndTag); err != nil {
			oldTaskProgress.Message = "拉取镜像失败"
			oldTaskProgress.DetailMsg = err.Error()
			oldTaskProgress.IsDone = true
			serviceContext.UpdateProgress(taskID, oldTaskProgress)
			logx.Errorf("Failed to pull image: %s", err)
			return OldImageOutcome{}, err
		}
	}
	oldTaskProgress, result = serviceContext.GetProgress(taskID)
	if !result {
		oldTaskProgress = svc.TaskProgress{
			Percentage: 0,
			Name:       "",
			Message:    "",
			DetailMsg:  "",
			IsDone:     false,
		}
	}
	if opts.SkipPull {
		oldTaskProgress.Message = "跳过拉取（同镜像已统一下载）"
		oldTaskProgress.DetailMsg = "跳过拉取（同镜像已统一下载）"
	} else {
		oldTaskProgress.Message = "拉取镜像成功"
		oldTaskProgress.DetailMsg = "拉取镜像成功"
	}
	oldTaskProgress.Percentage = 60
	serviceContext.UpdateProgress(taskID, oldTaskProgress)

	// 停止前先取一次容器信息：保留原始配置、镜像 ID 与运行状态
	inspectedContainer, err := serviceContext.DockerClient.ContainerInspect(ctx, id)
	if err != nil {
		oldTaskProgress.Message = "获取容器信息失败"
		oldTaskProgress.DetailMsg = err.Error()
		oldTaskProgress.IsDone = true
		serviceContext.UpdateProgress(taskID, oldTaskProgress)
		logx.Error("获取容器信息失败" + err.Error())
		return OldImageOutcome{}, err
	}
	oldImageID := inspectedContainer.Image
	wasRunning := inspectedContainer.State != nil && inspectedContainer.State.Running

	oldTaskProgress.Percentage = 62
	oldTaskProgress.Message = "正在停止容器"
	oldTaskProgress.DetailMsg = "正在停止容器"
	serviceContext.UpdateProgress(taskID, oldTaskProgress)
	stopOptions := container.StopOptions{
		Signal:  signal,
		Timeout: &timeout,
	}
	err = serviceContext.DockerClient.ContainerStop(context.Background(), id, stopOptions)
	if err != nil {
		oldTaskProgress.Message = "停止容器失败"
		oldTaskProgress.DetailMsg = err.Error()
		oldTaskProgress.IsDone = true
		serviceContext.UpdateProgress(taskID, oldTaskProgress)
		return OldImageOutcome{}, err
	}
	oldTaskProgress.Message = "容器停止成功"
	oldTaskProgress.DetailMsg = "容器停止成功"

	oldTaskProgress.Percentage = 66
	serviceContext.UpdateProgress(taskID, oldTaskProgress)
	oldTaskProgress.Message = "正在重命名旧容器"
	oldTaskProgress.DetailMsg = "正在重命名旧容器"
	serviceContext.UpdateProgress(taskID, oldTaskProgress)
	currentDate := time.Now().Format("2006-01-02-15-04-05")
	backupName := name + "-" + currentDate
	err = serviceContext.DockerClient.ContainerRename(context.Background(), id, backupName)
	if err != nil {
		oldTaskProgress.Message = "重命名旧容器失败"
		oldTaskProgress.DetailMsg = err.Error()
		oldTaskProgress.IsDone = true
		serviceContext.UpdateProgress(taskID, oldTaskProgress)
		return OldImageOutcome{}, err
	}
	oldTaskProgress.Message = "重命名旧容器成功"
	oldTaskProgress.DetailMsg = "重命名旧容器成功"
	oldTaskProgress.Percentage = 70
	serviceContext.UpdateProgress(taskID, oldTaskProgress)
	oldTaskProgress.Message = "正在创建新容器"
	oldTaskProgress.DetailMsg = "正在创建新容器"
	serviceContext.UpdateProgress(taskID, oldTaskProgress)
	// 回滚：新旧替换阶段任何失败都恢复旧容器（改名备份 → 原样启动）
	rollback := func(reason string) error {
		rbErr := RollbackUpdate(serviceContext, name, id, name, wasRunning)
		msg := "更新失败：" + reason + "，已回滚旧容器"
		if rbErr != nil {
			msg = fmt.Sprintf("更新失败：%s；回滚也失败(%v)，请手动处理（旧容器名 %s）", reason, rbErr, backupName)
		}
		oldTaskProgress.Message = msg
		oldTaskProgress.DetailMsg = msg
		oldTaskProgress.IsDone = true
		serviceContext.UpdateProgress(taskID, oldTaskProgress)
		logx.Errorf("更新 %s 失败并回滚: %s", name, reason)
		return fmt.Errorf("%s", msg)
	}
	inspectedContainer.Config.Hostname = ""
	inspectedContainer.Config.Image = imageNameAndTag
	inspectedContainer.Image = imageNameAndTag
	config := inspectedContainer.Config
	hostConfig := inspectedContainer.HostConfig
	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: inspectedContainer.NetworkSettings.Networks,
	}
	containerName := name
	_, err = serviceContext.DockerClient.ContainerCreate(ctx, config, hostConfig, networkingConfig, nil, containerName)
	if err != nil {
		return OldImageOutcome{}, rollback("创建新容器失败: " + err.Error())
	}
	oldTaskProgress.Message = "创建新容器成功"
	oldTaskProgress.DetailMsg = "创建新容器成功"
	oldTaskProgress.Percentage = 75
	serviceContext.UpdateProgress(taskID, oldTaskProgress)
	oldTaskProgress.Percentage = 82
	serviceContext.UpdateProgress(taskID, oldTaskProgress)
	if wasRunning {
		oldTaskProgress.Message = "正在启动新容器"
		oldTaskProgress.DetailMsg = "正在启动新容器"
		serviceContext.UpdateProgress(taskID, oldTaskProgress)
		err = serviceContext.DockerClient.ContainerStart(context.Background(), containerName, container.StartOptions{
			CheckpointID:  "",
			CheckpointDir: "",
		})
		if err != nil {
			return OldImageOutcome{}, rollback("启动新容器失败: " + err.Error())
		}
	} else {
		oldTaskProgress.Message = "容器原为停止状态，保持停止"
		oldTaskProgress.DetailMsg = "容器原为停止状态，保持停止"
		serviceContext.UpdateProgress(taskID, oldTaskProgress)
	}
	// 启动后健康校验：崩溃 / 重启循环 / OOM / unhealthy 则自动回滚旧容器
	if wasRunning {
		oldTaskProgress.Percentage = 85
		oldTaskProgress.Message = "正在校验新容器运行状态"
		oldTaskProgress.DetailMsg = "正在校验新容器运行状态"
		serviceContext.UpdateProgress(taskID, oldTaskProgress)
		healthy, reason := WaitContainerHealthy(serviceContext, containerName, func(msg string) {
			oldTaskProgress.Message = msg
			oldTaskProgress.DetailMsg = msg
			serviceContext.UpdateProgress(taskID, oldTaskProgress)
		})
		if !healthy {
			return OldImageOutcome{}, rollback(reason)
		}
	}
	if opts.DelOldContainer {
		err = serviceContext.DockerClient.ContainerRemove(context.Background(), id, container.RemoveOptions{})
		if err != nil {
			oldTaskProgress.Message = "删除旧容器失败"
			oldTaskProgress.DetailMsg = err.Error()
			oldTaskProgress.IsDone = true
			serviceContext.UpdateProgress(taskID, oldTaskProgress)
			return OldImageOutcome{}, err
		}
	}
	// 旧镜像处置：按策略（清理 / 打快照 / 不处理）；失败不影响更新结果
	policy := opts.resolvePolicy()
	outcome := OldImageOutcome{}
	if policy != "keep" {
		newImageID := ""
		if newInspected, ierr := serviceContext.DockerClient.ContainerInspect(ctx, containerName); ierr == nil {
			newImageID = newInspected.Image
		}
		if policy == "snapshot" {
			oldTaskProgress.Message = "正在为旧镜像打快照"
			oldTaskProgress.DetailMsg = "正在为旧镜像打快照"
		} else {
			oldTaskProgress.Message = "正在清理旧镜像"
			oldTaskProgress.DetailMsg = "正在清理旧镜像"
		}
		oldTaskProgress.Percentage = 90
		serviceContext.UpdateProgress(taskID, oldTaskProgress)
		outcome = HandleOldImage(serviceContext, policy, containerName, oldImageID, newImageID, opts.SnapshotOptions)
		if outcome.SnapshotRef != "" {
			logx.Infof("更新 %s：旧镜像已打快照 %s", name, outcome.SnapshotRef)
		}
	}
	oldTaskProgress.Message = "更新成功"
	detail := "更新成功"
	if outcome.SnapshotRef != "" {
		detail = "更新成功 · 已打快照 " + outcome.SnapshotRef
	}
	oldTaskProgress.DetailMsg = detail
	oldTaskProgress.Percentage = 100
	oldTaskProgress.IsDone = true
	serviceContext.UpdateProgress(taskID, oldTaskProgress)
	return outcome, nil
}

// PullImageByRef 仅负责拉取镜像并消费进度流（批量更新时同一镜像只拉一次）
// 配置了默认加速源时同样走加速（无进度展示）
func PullImageByRef(serviceContext *svc.ServiceContext, ref string) error {
	serviceContext.DockerClient.NegotiateAPIVersion(context.Background())
	return PullImageForUpdate(serviceContext, "", ref)
}

func decodePullResp(reader io.ReadCloser, ctx *svc.ServiceContext, taskID string) (err error) {
	defer func() { _ = reader.Close() }()
	decoder := json.NewDecoder(reader)
	var oldTaskProgress, result = ctx.GetProgress(taskID)
	if !result {
		oldTaskProgress = svc.TaskProgress{
			Percentage: 0,
			Name:       "",
			Message:    "",
			DetailMsg:  "",
			IsDone:     false,
		}
	}
	for {
		var msg dockerMsgType.JSONMessage
		if err = decoder.Decode(&msg); err != nil {
			if err == io.EOF {
				return nil
			}
			oldTaskProgress.Message = "拉取镜像失败"
			oldTaskProgress.DetailMsg = err.Error()
			oldTaskProgress.Percentage = 25
			oldTaskProgress.IsDone = true
			ctx.UpdateProgress(taskID, oldTaskProgress)
			logx.Errorf("Failed to decode pull image response: %s", err)
			return fmt.Errorf("拉取镜像失败: %w", err)
		}
		// Print the progress or error information from the response
		if msg.Error != nil {
			oldTaskProgress.Message = "拉取镜像失败"
			oldTaskProgress.DetailMsg = msg.Error.Error()
			oldTaskProgress.Percentage = 25
			oldTaskProgress.IsDone = true
			ctx.UpdateProgress(taskID, oldTaskProgress)
			logx.Errorf("Error: %s", msg.Error)
			return fmt.Errorf("拉取镜像失败: %w", msg.Error)
		} else {
			var formattedMsg string
			if msg.Progress != nil && msg.Progress.Total > 0 {
				pct := float64(msg.Progress.Current) / float64(msg.Progress.Total) * 100
				if pct > 100 {
					pct = 100
				}
				// 拉取阶段映射到 5%~60%（拉取通常是大头）
				oldTaskProgress.Percentage = 5 + int(pct*0.55)
				formattedMsg = fmt.Sprintf("%s %s / %s（%.0f%%）", msg.Status,
					humanBytes(msg.Progress.Current), humanBytes(msg.Progress.Total), pct)
			} else {
				formattedMsg = msg.Status
				if oldTaskProgress.Percentage < 5 {
					oldTaskProgress.Percentage = 5
				}
			}
			oldTaskProgress.DetailMsg = formattedMsg
			ctx.UpdateProgress(taskID, oldTaskProgress)
			logx.Infof("拉取镜像进度\t %s: %s\n", msg.Status, msg.Progress)
		}
	}
}

// progressHeartbeat 每 2 秒刷新 DetailMsg 的耗时后缀，避免长阶段"看起来卡死"
func progressHeartbeat(serviceContext *svc.ServiceContext, taskID string, stop <-chan struct{}) {
	start := time.Now()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			p, ok := serviceContext.GetProgress(taskID)
			if !ok || p.IsDone {
				continue
			}
			base := strings.SplitN(p.DetailMsg, " · ⏱", 2)[0]
			p.DetailMsg = fmt.Sprintf("%s · ⏱%ds", base, int(time.Since(start).Seconds()))
			serviceContext.UpdateProgress(taskID, p)
		}
	}
}

// humanBytes 人类可读的字节数
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%sB", float64(n)/float64(div), "KMGT"[exp:exp+1])
}
