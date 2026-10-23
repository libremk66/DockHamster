package utiles

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types/image"
	dockerMsgType "github.com/docker/docker/pkg/jsonmessage"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
)

// pullTimeout 单次拉取（含读进度流）的上限。TLS 握手/流中途挂住时必须有 deadline，
// 否则 defer 的失败终态永远执行不到，AutoUpdateState 会一直被占用。
const pullTimeout = 30 * time.Minute

// pullMessageSucceeded 判断 Docker 拉取流里的成功确认。
// EOF 本身不代表成功——连接中途断开也可能 EOF；必须有 aux 或最终状态。
func pullMessageSucceeded(msg dockerMsgType.JSONMessage) bool {
	if msg.Aux != nil && len(*msg.Aux) > 0 {
		return true
	}
	s := msg.Status
	return strings.Contains(s, "Downloaded newer image") || strings.Contains(s, "Image is up to date")
}

// setTaskMessage 覆写任务进度消息（taskIDs 为空/全空时跳过）。
// 组内多台共用一次拉取时传多个 taskID：每台的行都能看到同一进度。
func setTaskMessage(svcCtx *svc.ServiceContext, taskIDs []string, msg string) {
	for _, taskID := range taskIDs {
		if taskID == "" {
			continue
		}
		p, ok := svcCtx.GetProgress(taskID)
		if !ok {
			p = svc.TaskProgress{TaskID: taskID, Percentage: 0}
		}
		p.TaskID = taskID
		p.Message = msg
		p.DetailMsg = msg
		p.IsDone = false
		p.Failed = false
		svcCtx.UpdateProgress(taskID, p)
	}
}

// setTasksFailed 把一组任务落为"失败完成"终态。
// 「任务」页的进行中列表只按 IsDone 过滤，任何提前退出的路径都必须落终态，
// 否则排队中的行会永远停在「等待中，等待前序任务完成」。
// 已经处于失败态的任务保留原有（更具体的）错误信息，不覆盖。
func setTasksFailed(svcCtx *svc.ServiceContext, taskIDs []string, msg, detail string) {
	for _, taskID := range taskIDs {
		if taskID == "" {
			continue
		}
		p, ok := svcCtx.GetProgress(taskID)
		if !ok {
			p = svc.TaskProgress{TaskID: taskID}
		}
		if p.IsDone && p.Failed {
			continue
		}
		p.TaskID = taskID
		if msg != "" {
			p.Message = msg
		}
		p.DetailMsg = detail
		p.IsDone = true
		p.Failed = true
		svcCtx.UpdateProgress(taskID, p)
	}
}

// consumePullStream 消费拉取进度流但不写进度（无 taskID 的场景）
func consumePullStream(reader io.ReadCloser) error {
	defer func() { _ = reader.Close() }()
	decoder := json.NewDecoder(reader)
	sawSuccess := false
	for {
		var msg dockerMsgType.JSONMessage
		if err := decoder.Decode(&msg); err != nil {
			if err == io.EOF {
				if !sawSuccess {
					return fmt.Errorf("拉取流提前结束（未收到成功确认）")
				}
				return nil
			}
			return err
		}
		if msg.Error != nil {
			return msg.Error
		}
		if pullMessageSucceeded(msg) {
			sawSuccess = true
		}
	}
}

// PullImageViaMirror 通过加速源拉取 Docker Hub 镜像，成功后回打原始镜像名，
// 并移除加速源临时标签（避免镜像列表出现重复的一行）。
func PullImageViaMirror(svcCtx *svc.ServiceContext, taskID, source, imageRef string) error {
	var ids []string
	if taskID != "" {
		ids = []string{taskID}
	}
	return pullImageViaMirrorFor(svcCtx, ids, source, imageRef)
}

// pullImageViaMirrorFor 多任务版：进度同步写给 taskIDs 里的每个任务
func pullImageViaMirrorFor(svcCtx *svc.ServiceContext, taskIDs []string, source, imageRef string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
	defer cancel()
	mirrorRef, err := MirrorRefFor(source, imageRef)
	if err != nil {
		return err
	}
	setTaskMessage(svcCtx, taskIDs, "正在通过加速源 "+source+" 拉取镜像")
	reader, err := svcCtx.DockerClient.ImagePull(ctx, mirrorRef, image.PullOptions{})
	if err != nil {
		return err
	}
	if len(taskIDs) > 0 {
		err = decodePullResp(reader, svcCtx, taskIDs)
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

// PullImageForUpdate 单任务拉取入口（更新流程/自更新用）
func PullImageForUpdate(svcCtx *svc.ServiceContext, taskID, imageRef string) error {
	var ids []string
	if taskID != "" {
		ids = []string{taskID}
	}
	return PullImageForTasks(svcCtx, ids, imageRef)
}

// PullImageForTasks 拉取入口（多任务版）：配置了默认加速源且是 Docker Hub 镜像时
// 先走加速源，失败自动回退直连。taskIDs 为空时拉取但不写进度（纯预拉场景）。
// 批量更新把组内所有容器的 taskID 传进来 —— 同镜像只拉一次、进度对每台都可见。
// 任何失败出口都由 defer 统一落失败终态：ImagePull 在拿到进度流之前就失败
// （TLS 握手超时 / 限流 / 仓库不可达）时，此前不会写任何进度，排队任务会永远停在「等待中」。
func PullImageForTasks(svcCtx *svc.ServiceContext, taskIDs []string, imageRef string) (err error) {
	defer func() {
		if err != nil {
			setTasksFailed(svcCtx, taskIDs, "拉取镜像失败", FriendlyDaemonError(err).Error())
		}
	}()
	if source := svcCtx.Accelerator.UpdateSource(); source != "" {
		if _, err := MirrorRefFor(source, imageRef); err == nil {
			if perr := pullImageViaMirrorFor(svcCtx, taskIDs, source, imageRef); perr == nil {
				return nil
			} else {
				logx.Errorf("加速源拉取失败，回退直连 %s: %v", imageRef, perr)
				setTaskMessage(svcCtx, taskIDs, "加速源失败（"+perr.Error()+"），改用直连重试")
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
	defer cancel()
	// 私有仓库（ghcr 私有包等）：带上面板里配的凭据；没有配置时为空 = 匿名（行为不变）
	pullOpts := image.PullOptions{}
	if svcCtx.RegistryAuth != nil {
		pullOpts.RegistryAuth = svcCtx.RegistryAuth.DaemonAuth(imageRef)
	}
	reader, err := svcCtx.DockerClient.ImagePull(ctx, imageRef, pullOpts)
	if err != nil {
		return FriendlyPullError(imageRef, err)
	}
	if len(taskIDs) > 0 {
		return FriendlyPullError(imageRef, decodePullResp(reader, svcCtx, taskIDs))
	}
	return FriendlyPullError(imageRef, consumePullStream(reader))
}
