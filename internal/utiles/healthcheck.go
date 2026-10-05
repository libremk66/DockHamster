package utiles

import (
	"context"
	"fmt"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/libremk66/DockHamster/internal/svc"
)

// WaitContainerHealthy 更新后校验新容器是否稳定运行。
// 通过条件：连续 3 次检查处于运行且未重启；
// 出现退出 / 重启循环 / OOM / 健康检查 unhealthy 立即判失败；
// 容器自带 HEALTHCHECK 时按其结果等待（上限约 90 秒）。
func WaitContainerHealthy(svcCtx *svc.ServiceContext, name string, onTick func(string)) (bool, string) {
	ctx := context.Background()
	streak := 0
	maxChecks := 45 // 2s × 45 ≈ 90s 上限
	for i := 0; i < maxChecks; i++ {
		time.Sleep(2 * time.Second)
		ins, err := svcCtx.DockerClient.ContainerInspect(ctx, name)
		if err != nil {
			return false, "查询容器状态失败: " + err.Error()
		}
		st := ins.State
		if st == nil {
			return false, "容器状态未知"
		}
		if st.OOMKilled {
			return false, "容器被 OOM 杀掉（内存不足）"
		}
		if st.Restarting {
			return false, fmt.Sprintf("容器启动后反复重启（退出码 %d）", st.ExitCode)
		}
		if !st.Running {
			return false, fmt.Sprintf("容器启动后退出（退出码 %d）", st.ExitCode)
		}
		if st.Health != nil {
			switch st.Health.Status {
			case "unhealthy":
				return false, "容器健康检查未通过（unhealthy）"
			case "starting":
				if onTick != nil {
					onTick("新容器启动中，等待健康检查…")
				}
				streak = 0
				continue
			}
		}
		streak++
		if onTick != nil {
			onTick(fmt.Sprintf("新容器运行正常（%d/3）", streak))
		}
		if streak >= 3 {
			return true, ""
		}
	}
	return false, "容器在 90 秒内未稳定运行"
}

// RollbackUpdate 回滚一次失败的更新：删除不健康的新容器，
// 把备份的旧容器（按 ID）改回原名并启动（原本在运行才启动）。
func RollbackUpdate(svcCtx *svc.ServiceContext, newContainerName string, oldContainerID string, originalName string, wasRunning bool) error {
	ctx := context.Background()
	timeout := 5
	_ = svcCtx.DockerClient.ContainerStop(ctx, newContainerName, container.StopOptions{Timeout: &timeout})
	_ = svcCtx.DockerClient.ContainerRemove(ctx, newContainerName, container.RemoveOptions{Force: true})
	if err := svcCtx.DockerClient.ContainerRename(ctx, oldContainerID, originalName); err != nil {
		return fmt.Errorf("旧容器恢复原名失败: %w", err)
	}
	if wasRunning {
		if err := svcCtx.DockerClient.ContainerStart(ctx, oldContainerID, container.StartOptions{}); err != nil {
			return fmt.Errorf("旧容器启动失败: %w", err)
		}
	}
	return nil
}

// NoteMaintenance 通知守护模块：这是面板主动发起的操作，窗口内不要告警
func NoteMaintenance(svcCtx *svc.ServiceContext, name string, d time.Duration) {
	if svcCtx == nil || svcCtx.Watchdog == nil || name == "" {
		return
	}
	svcCtx.Watchdog.NoteMaintenance(name, d)
}

// NoteMaintenanceByID 同上，按容器 ID 解析名字
func NoteMaintenanceByID(svcCtx *svc.ServiceContext, id string, d time.Duration) {
	if svcCtx == nil || svcCtx.Watchdog == nil {
		return
	}
	ins, err := svcCtx.DockerClient.ContainerInspect(context.Background(), id)
	if err != nil || ins.Name == "" {
		return
	}
	svcCtx.Watchdog.NoteMaintenance(trimLeadingSlash(ins.Name), d)
}

func trimLeadingSlash(s string) string {
	if len(s) > 0 && s[0] == '/' {
		return s[1:]
	}
	return s
}
