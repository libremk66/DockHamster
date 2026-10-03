package utiles

import (
	"context"
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
	return nil
}
