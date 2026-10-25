package container

import (
	"context"
	"github.com/google/uuid"
	"github.com/libremk66/DockHamster/internal/selfupdate"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/libremk66/DockHamster/internal/utiles"
	"github.com/zeromicro/go-zero/core/logx"
	"os"
	"strings"
)

type UpdateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUpdateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateLogic {
	return &UpdateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *UpdateLogic) Update(req *types.ContainerUpdateReq) (resp *types.Resp, err error) {
	resp = &types.Resp{}
	// 镜像引用校验：拒绝裸镜像 ID（见 utiles.ValidateUpdateImageRef 注释），
	// 并顺手规范化（无标签补 :latest）
	normalized, verr := utiles.ValidateUpdateImageRef(req.ImageNameAndTag)
	if verr != nil {
		resp.Code = 400
		resp.Msg = verr.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	req.ImageNameAndTag = normalized
	taskID := uuid.New().String()
	isSelf := selfupdate.IsSelf(req.Id)
	go func() {
		// Catch any panic and log the error
		defer func() {
			if r := recover(); r != nil {
				l.Errorf("Recovered from panic in UpdateContainer: %v", r)
			}
		}()
		if isSelf {
			// 更新的是面板自身：走接力容器自更新（直接在自己的进程里重建自己会中断流程）
			l.svcCtx.UpdateProgress(taskID, svc.TaskProgress{
				TaskID:     taskID,
				Percentage: 5,
				Name:       req.ContainerName,
				Message:    "检测到更新面板自身，切换为接力自更新",
				DetailMsg:  "接力更新方式：拉取新镜像后用一次性容器完成替换，失败自动回滚",
				IsDone:     false,
			})
			if err := selfupdate.Launch(l.svcCtx, req.Id, req.ContainerName, req.ImageNameAndTag, taskID); err != nil {
				l.Errorf("Error in self image update: %v", err)
			}
			return
		}
		imageNameAndTag := req.ImageNameAndTag
		settings := l.svcCtx.AutoUpdate.Get()
		opts := utiles.UpdateOptions{
			SkipPull:        req.SkipPull,
			DelOldContainer: os.Getenv("DelOldContainer") != "false",
			DeleteOldImage:  settings.DeleteOldImage,
			OldImagePolicy:  settings.ResolveOldImagePolicy(req.ContainerName),
			SnapshotOptions: utiles.SnapshotOptionsFromSettings(settings),
			Trigger:         "container",
		}
		err := utiles.UpdateContainer(l.svcCtx, req.Id, req.ContainerName, imageNameAndTag, opts, taskID)
		// 手动更新也刷新「上次更新结果」：成功会覆盖之前的失败标记；
		// 失败则写入新的失败标记（用户可在容器列表点 × 清除）。
		msg := "更新成功"
		if err != nil {
			msg = "更新失败：" + strings.ReplaceAll(err.Error(), "\n", " ")
			l.Errorf("Error in UpdateContainer: %v", err)
		}
		if l.svcCtx.AutoUpdateState != nil {
			l.svcCtx.AutoUpdateState.SetContainer(req.ContainerName, err == nil, msg)
		}
	}()
	resp.Code = 200
	resp.Msg = "任务已提交，请在任务页查看结果"
	resp.Data = map[string]string{"taskID": taskID}
	return resp, nil
}
