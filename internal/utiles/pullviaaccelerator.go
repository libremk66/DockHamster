package utiles

import (
	"context"
	"encoding/json"
	"io"

	"github.com/docker/docker/api/types/image"
	dockerMsgType "github.com/docker/docker/pkg/jsonmessage"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
)

// setTaskMessage 覆写任务进度消息（taskID 为空时跳过，批量更新等无进度场景）
func setTaskMessage(svcCtx *svc.ServiceContext, taskID, msg string) {
	if taskID == "" {
		return
	}
	p, ok := svcCtx.GetProgress(taskID)
	if !ok {
		p = svc.TaskProgress{TaskID: taskID, Percentage: 0}
	}
	p.TaskID = taskID
	p.Message = msg
	p.DetailMsg = msg
	p.IsDone = false
	svcCtx.UpdateProgress(taskID, p)
}

// consumePullStream 消费拉取进度流但不写进度（无 taskID 的场景）
func consumePullStream(reader io.ReadCloser) error {
	defer func() { _ = reader.Close() }()
	decoder := json.NewDecoder(reader)
	for {
		var msg dockerMsgType.JSONMessage
		if err := decoder.Decode(&msg); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if msg.Error != nil {
			return msg.Error
		}
	}
}

// PullImageViaMirror 通过加速源拉取 Docker Hub 镜像，成功后回打原始镜像名，
// 并移除加速源临时标签（避免镜像列表出现重复的一行）。
func PullImageViaMirror(svcCtx *svc.ServiceContext, taskID, source, imageRef string) error {
	ctx := context.Background()
	mirrorRef, err := MirrorRefFor(source, imageRef)
	if err != nil {
		return err
	}
	setTaskMessage(svcCtx, taskID, "正在通过加速源 "+source+" 拉取镜像")
	reader, err := svcCtx.DockerClient.ImagePull(ctx, mirrorRef, image.PullOptions{})
	if err != nil {
		return err
	}
	if taskID != "" {
		err = decodePullResp(reader, svcCtx, taskID)
	} else {
		err = consumePullStream(reader)
	}
	if err != nil {
		return err
	}
	if mirrorRef == imageRef {
		return nil
	}
	if err := svcCtx.DockerClient.ImageTag(ctx, mirrorRef, imageRef); err != nil {
		return err
	}
	if _, err := svcCtx.DockerClient.ImageRemove(ctx, mirrorRef, image.RemoveOptions{}); err != nil {
		// 回打成功即可用；清不掉临时标签只记日志
		logx.Errorf("移除加速源临时标签失败 %s: %v", mirrorRef, err)
	}
	return nil
}

// PullImageForUpdate 更新流程的拉取入口：配置了默认加速源且是 Docker Hub 镜像时
// 先走加速源，失败自动回退直连。taskID 为空（批量更新）时不写进度。
func PullImageForUpdate(svcCtx *svc.ServiceContext, taskID, imageRef string) error {
	if source := svcCtx.Accelerator.UpdateSource(); source != "" {
		if _, err := MirrorRefFor(source, imageRef); err == nil {
			if perr := PullImageViaMirror(svcCtx, taskID, source, imageRef); perr == nil {
				return nil
			} else {
				logx.Errorf("加速源拉取失败，回退直连 %s: %v", imageRef, perr)
				setTaskMessage(svcCtx, taskID, "加速源失败（"+perr.Error()+"），改用直连重试")
			}
		}
	}
	reader, err := svcCtx.DockerClient.ImagePull(context.Background(), imageRef, image.PullOptions{})
	if err != nil {
		return FriendlyPullError(imageRef, err)
	}
	if taskID != "" {
		return FriendlyPullError(imageRef, decodePullResp(reader, svcCtx, taskID))
	}
	return FriendlyPullError(imageRef, consumePullStream(reader))
}
