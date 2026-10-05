package selfupdate

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/libremk66/DockHamster/internal/config"
	su "github.com/libremk66/DockHamster/internal/selfupdate"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/zeromicro/go-zero/core/logx"
)

type SelfUpdateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewSelfUpdateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SelfUpdateLogic {
	return &SelfUpdateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// Status 自更新状态：当前版本 / 自身容器 / 上次更新结果
func (l *SelfUpdateLogic) Status() (*types.Resp, error) {
	data := map[string]interface{}{
		"version":     config.Version,
		"inContainer": false,
		"containerID": "",
	}
	selfID := su.CurrentContainerID()
	if ins, err := l.svcCtx.DockerClient.ContainerInspect(l.ctx, selfID); err == nil {
		data["inContainer"] = true
		data["containerID"] = ins.ID[:12]
		data["name"] = strings.TrimPrefix(ins.Name, "/")
		data["image"] = ins.Config.Image
		if res, ok := su.LastResult(); ok {
			data["lastResult"] = res
		}
	}
	return &types.Resp{Code: 200, Msg: "success", Data: data}, nil
}

// Run 一键自更新：定位自身容器 → 拉取同镜像新版本 → 启动接力容器
func (l *SelfUpdateLogic) Run() (*types.Resp, error) {
	selfID := su.CurrentContainerID()
	ins, err := l.svcCtx.DockerClient.ContainerInspect(l.ctx, selfID)
	if err != nil {
		return &types.Resp{
			Code: 400,
			Msg:  "未检测到自身容器（面板可能未运行在 Docker 中，无法自更新）",
			Data: map[string]interface{}{},
		}, nil
	}
	name := strings.TrimPrefix(ins.Name, "/")
	imageRef := strings.TrimSpace(ins.Config.Image)
	if imageRef == "" {
		return &types.Resp{Code: 400, Msg: "无法确定自身镜像名", Data: map[string]interface{}{}}, nil
	}

	taskID := uuid.New().String()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logx.Errorf("自更新 panic: %v", r)
			}
		}()
		if err := su.Launch(l.svcCtx, ins.ID, name, imageRef, taskID); err != nil {
			logx.Errorf("面板自更新失败: %v", err)
		}
	}()
	return &types.Resp{
		Code: 200,
		Msg:  "success",
		Data: map[string]string{"taskID": taskID, "image": imageRef, "name": name},
	}, nil
}
