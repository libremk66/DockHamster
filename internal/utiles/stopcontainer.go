package utiles

import (
	"context"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/libremk66/DockHamster/internal/svc"
)

func StopContainer(ctx *svc.ServiceContext, id string) error {
	timeout := 10
	signal := "SIGINT"
	stopOptions := container.StopOptions{
		Signal:  signal,
		Timeout: &timeout,
	}
	err := ctx.DockerClient.ContainerStop(context.Background(), id, stopOptions)
	if err != nil {
		return err
	}
	// 面板主动停止，守护模块不要当成异常告警
	NoteMaintenanceByID(ctx, id, 10*time.Minute)
	return nil
}
