package utiles

import (
	"context"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/libremk66/DockHamster/internal/svc"
)

func RestartContainer(ctx *svc.ServiceContext, id string) error {
	timeout := 10
	signal := "SIGINT"
	stopOptions := container.StopOptions{
		Signal:  signal,
		Timeout: &timeout,
	}
	err := ctx.DockerClient.ContainerRestart(context.Background(), id, stopOptions)
	if err != nil {
		return err
	}
	// 面板主动重启，守护模块窗口内不告警
	NoteMaintenanceByID(ctx, id, 3*time.Minute)
	return nil
}
