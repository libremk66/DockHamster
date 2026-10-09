package utiles

import (
	"context"
	"encoding/json"
	"errors"
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
	// Trigger 触发来源（「任务」页展示用）：container | autoupdate | group | rollback
	Trigger string
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
	// 兜底校验：裸镜像 ID 不能作为拉取目标（自动/整组/回滚路径也走这里）
	if opts.Trigger != "rollback" { // 回滚用的是本地已有镜像的引用，跳过校验
		if normalized, verr := ValidateUpdateImageRef(imageNameAndTag); verr != nil {
			serviceContext.UpdateProgress(taskID, svc.TaskProgress{
				TaskID: taskID, Name: name, Percentage: 0,
				Message: "镜像名无效", DetailMsg: verr.Error(), IsDone: true, Failed: true,
			})
			return OldImageOutcome{}, verr
		} else {
			imageNameAndTag = normalized
		}
	}
	// 「任务」页元数据：类型=更新，来源=触发入口（容器页/自动更新/整组/回滚）
	serviceContext.InitTask(taskID, name, "update", opts.Trigger)
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

	// compose 管理的容器走 compose 通道重建（保持 labels/配置一致，避免配置漂移）。
	// 例外：面板侧 compose 环境不可用（未装 compose 命令 / compose 文件未挂载）→
	// 回退 API 重建通道，但**显式提示**（不静默：用户可据此补挂载以恢复 compose 通道）。
	// compose up 真正执行失败（拉取失败/重建失败）仍按错误返回，不回退。
	if meta, mErr := ComposeMetaOfContainer(serviceContext, id); mErr == nil && meta.IsManaged {
		logx.Infof("容器 %s 由 compose 管理（%s），分流到 compose 更新通道", name, meta.UpdateRef())
		cerr := UpdateContainerViaCompose(serviceContext, meta, imageNameAndTag, opts.SkipPull, taskID)
		if cerr == nil || !errors.Is(cerr, ErrComposeUnavailable) {
			return OldImageOutcome{}, cerr
		}
		logx.Infof("容器 %s compose 通道不可用（%v），回退 API 重建", name, cerr)
		oldTaskProgress.Message = "compose 环境不可用，已回退 API 重建"
		oldTaskProgress.DetailMsg = cerr.Error()
		oldTaskProgress.Percentage = 5
		oldTaskProgress.IsDone = false
		serviceContext.UpdateProgress(taskID, oldTaskProgress)
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
			oldTaskProgress.Failed = true
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
		oldTaskProgress.Failed = true
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
		oldTaskProgress.Failed = true
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
		oldTaskProgress.Failed = true
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
		oldTaskProgress.Failed = true
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
			oldTaskProgress.Failed = true
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
// PullImageByRefForTasks 批量拉取入口：进度同步写到 taskIDs（组内每台可见），
// taskIDs 为空时退化为"只拉取不写进度"。
func PullImageByRefForTasks(serviceContext *svc.ServiceContext, ref string, taskIDs []string) error {
	serviceContext.DockerClient.NegotiateAPIVersion(context.Background())
	return PullImageForTasks(serviceContext, taskIDs, ref)
}

// ---------- 拉取进度（分层聚合 + 速度） ----------

// 拉取阶段在总进度里的映射区间：5% ~ 60%（拉取通常是大头）
const (
	pullPctBase = 5
	pullPctSpan = 0.55
)

// pullProgress 聚合 docker 拉取流的分层进度：
//   - 百分比按各层"已下载/总字节"累计 —— 不再逐层各自 0→100（进度条来回跳的根源）；
//   - 层是陆续登记的（分母会长大），百分比做单调不回退处理；
//   - 速度按 ≥1 秒采样 + 指数平滑，供 UI 显示 MB/s（停滞时能一眼看出是网络问题）。
type pullProgress struct {
	cur       map[string]int64
	total     map[string]int64
	lastBytes int64
	lastAt    time.Time
	speed     float64
	maxPct    float64
}

func newPullProgress() *pullProgress {
	return &pullProgress{cur: map[string]int64{}, total: map[string]int64{}}
}

// feed 吸收一条拉取流消息 →（整体百分比 0-100，细节文案）；无总量信息时返回状态文本
func (p *pullProgress) feed(msg dockerMsgType.JSONMessage) (float64, string) {
	if id := msg.ID; id != "" {
		if msg.Progress != nil && msg.Progress.Total > 0 {
			p.total[id] = msg.Progress.Total
			if c := msg.Progress.Current; c > 0 {
				p.cur[id] = c
			}
		}
		switch msg.Status {
		case "Pull complete", "Download complete", "Already exists":
			if t, ok := p.total[id]; ok {
				p.cur[id] = t
			}
		}
	}
	var sumCur, sumTotal int64
	for id, t := range p.total {
		sumTotal += t
		if c := p.cur[id]; c > 0 {
			sumCur += c
		}
	}

	// 速度采样：≥1s 一次；分母变大导致 sumCur 回退时不计负速度
	now := time.Now()
	if p.lastAt.IsZero() {
		p.lastAt, p.lastBytes = now, sumCur
	} else if d := now.Sub(p.lastAt); d >= time.Second {
		delta := sumCur - p.lastBytes
		if delta < 0 {
			delta = 0
		}
		inst := float64(delta) / d.Seconds()
		if p.speed == 0 {
			p.speed = inst
		} else {
			p.speed = 0.4*inst + 0.6*p.speed
		}
		p.lastAt, p.lastBytes = now, sumCur
	}

	if sumTotal <= 0 {
		return 0, strings.TrimSpace(msg.Status)
	}
	pct := float64(sumCur) / float64(sumTotal) * 100
	if pct < p.maxPct {
		pct = p.maxPct // 新层登记会拉大分母：百分比只进不退
	} else {
		p.maxPct = pct
	}
	if pct > 100 {
		pct = 100
	}
	detail := fmt.Sprintf("%s / %s", humanBytes(sumCur), humanBytes(sumTotal))
	if p.speed > 0 {
		detail += " · " + humanBytes(int64(p.speed)) + "/s"
	}
	if s := strings.TrimSpace(msg.Status); s != "" && s != "Downloading" {
		detail = s + " · " + detail
	}
	return pct, detail
}

// decodePullResp 消费拉取进度流：聚合后的总进度 + 速度写到 taskIDs 里的每个任务
// （组内多台共用一次拉取时，每台的行都显示同一进度）。
func decodePullResp(reader io.ReadCloser, ctx *svc.ServiceContext, taskIDs []string) (err error) {
	defer func() { _ = reader.Close() }()
	decoder := json.NewDecoder(reader)
	pp := newPullProgress()
	// 承接外层已设置的阶段文案（如"正在拉取新镜像"）
	stageMsg := "正在拉取镜像"
	for _, id := range taskIDs {
		if p, ok := ctx.GetProgress(id); ok && p.Message != "" {
			stageMsg = p.Message
			break
		}
	}
	write := func(pct int, detail string, done, failed bool, message string) {
		for _, taskID := range taskIDs {
			if taskID == "" {
				continue
			}
			p, ok := ctx.GetProgress(taskID)
			if !ok {
				p = svc.TaskProgress{TaskID: taskID}
			}
			p.TaskID = taskID
			p.Percentage = pct
			if message != "" {
				p.Message = message
			}
			p.DetailMsg = detail
			p.IsDone = done
			p.Failed = failed
			ctx.UpdateProgress(taskID, p)
		}
	}
	fail := func(reason string) error {
		write(25, reason, true, true, "拉取镜像失败")
		return fmt.Errorf("拉取镜像失败: %s", reason)
	}
	for {
		var msg dockerMsgType.JSONMessage
		if err = decoder.Decode(&msg); err != nil {
			if err == io.EOF {
				return nil
			}
			logx.Errorf("Failed to decode pull image response: %s", err)
			return fail(err.Error())
		}
		if msg.Error != nil {
			logx.Errorf("Error: %s", msg.Error)
			return fail(msg.Error.Error())
		}
		pct, detail := pp.feed(msg)
		if detail == "" {
			continue
		}
		write(pullPctBase+int(pct*pullPctSpan), detail, false, false, stageMsg)
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
