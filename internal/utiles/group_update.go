package utiles

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/google/uuid"
	"github.com/onlyLTY/dockerCopilot/internal/module"
	"github.com/onlyLTY/dockerCopilot/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
	"os"
)

// GroupUpdateTask 单个容器的更新任务（前端据此轮询进度）
type GroupUpdateTask struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	TaskID string `json:"taskID"`
}

// RunGroupUpdate 与指定容器共用同一镜像的所有容器**整组更新**（方案 A：UI 弹窗选择"全部更新"时调用）。
// 行为：同镜像只拉取一次 → 逐个容器重建（保持各自原有运行/停止状态）。
// 返回每个容器的任务 ID；实际执行在后台 goroutine 中进行。
func RunGroupUpdate(serviceContext *svc.ServiceContext, containerID string) ([]GroupUpdateTask, error) {
	ctx := context.Background()
	target, err := serviceContext.DockerClient.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("找不到容器: %w", err)
	}
	targetImageID := target.Image

	list, err := serviceContext.DockerClient.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("获取容器列表失败: %w", err)
	}

	type targetC struct {
		id, name string
	}
	var targets []targetC
	for _, c := range list {
		if c.ImageID != targetImageID || len(c.Names) == 0 {
			continue
		}
		targets = append(targets, targetC{id: c.ID, name: strings.TrimPrefix(c.Names[0], "/")})
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("未找到共用该镜像的容器")
	}

	if !serviceContext.AutoUpdateState.TryStart() {
		return nil, fmt.Errorf("已有更新任务在运行，请稍后再试")
	}

	settings := serviceContext.AutoUpdate.Get()
	opts := UpdateOptions{
		SkipPull:        true, // 统一拉取
		DelOldContainer: os.Getenv("DelOldContainer") != "false",
		DeleteOldImage:  settings.DeleteOldImage,
	}
	pullRef := resolvePullRef(serviceContext, targetImageID, target.Config.Image)

	tasks := make([]GroupUpdateTask, 0, len(targets))
	for _, t := range targets {
		tasks = append(tasks, GroupUpdateTask{ID: t.id, Name: t.name, TaskID: uuid.New().String()})
	}

	// 登记为"进行中"，供 status 接口实时展示
	active := make([]module.ActiveTask, 0, len(tasks))
	for _, t := range tasks {
		active = append(active, module.ActiveTask{Name: t.Name, TaskID: t.TaskID})
	}
	serviceContext.AutoUpdateState.SetActive(active)

	go func() {
		defer serviceContext.AutoUpdateState.Finish()
		defer serviceContext.AutoUpdateState.ClearActive()
		start := time.Now()
		result := module.AutoUpdateRunResult{
			Time:    time.Now().Format("2006-01-02 15:04:05"),
			Trigger: "group",
			Updated: []string{},
			Failed:  []module.AutoRunFailure{},
		}
		logx.Infof("整组更新：拉取 %s（%d 个容器共用）", pullRef, len(tasks))
		if err := PullImageByRef(serviceContext, pullRef); err != nil {
			logx.Errorf("整组更新：拉取 %s 失败：%v", pullRef, err)
			result.Failed = append(result.Failed, module.AutoRunFailure{Name: pullRef, Error: "拉取失败: " + oneLine(err.Error())})
			result.DurationSec = time.Since(start).Seconds()
			serviceContext.AutoUpdateState.AddRun(result)
			return
		}
		for _, t := range tasks {
			cleaned, err := updateContainerCore(serviceContext, t.ID, t.Name, pullRef, opts, t.TaskID)
			if err != nil {
				logx.Errorf("整组更新：容器 %s 更新失败：%v", t.Name, err)
				result.Failed = append(result.Failed, module.AutoRunFailure{Name: t.Name, Error: oneLine(err.Error())})
				serviceContext.AutoUpdateState.SetContainer(t.Name, false, oneLine(err.Error()))
			} else {
				logx.Infof("整组更新：容器 %s 更新完成", t.Name)
				result.Updated = append(result.Updated, t.Name)
				if cleaned {
					result.CleanedImages++
				}
				serviceContext.AutoUpdateState.SetContainer(t.Name, true, "更新成功")
			}
		}
		result.DurationSec = float64(int(time.Since(start).Seconds()*10)) / 10
		serviceContext.AutoUpdateState.AddRun(result)
		logx.Infof("整组更新完成：%d 成功 / %d 失败 / 清理旧镜像 %d", len(result.Updated), len(result.Failed), result.CleanedImages)
	}()

	return tasks, nil
}
