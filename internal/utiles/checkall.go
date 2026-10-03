package utiles

import (
	"time"

	"github.com/google/uuid"

	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
)

// CheckAllImageUpdates 检查全部镜像的远端 digest，刷新"有新版本"状态。
// 供三处复用：启动时、定时任务（checkCron）、以及 UI 的「检查更新」按钮。
// 运行状态与进度记录在 svcCtx.AutoUpdateCheck（服务端持有，前端切页面不丢）。
// trigger: manual（UI 按钮）| cron（定时）| startup（启动时）
func CheckAllImageUpdates(svcCtx *svc.ServiceContext, trigger string) (int, int, error) {
	start := time.Now()
	list, err := GetImagesList(svcCtx)
	if err != nil {
		logx.Errorf("检查更新：获取镜像列表失败: %v", err)
		return 0, 0, err
	}
	state := svcCtx.AutoUpdateCheck
	// 统一在这里登记"运行中"——无论按钮/cron/启动触发，UI 都能看到进度
	if state != nil && !state.TryStart(uuid.New().String(), len(list), trigger) {
		logx.Infof("检查更新：已有任务在运行，跳过本轮（%s）", trigger)
		return 0, 0, nil
	}
	need := 0
	svcCtx.HubImageInfo.CheckUpdate(svcCtx.DockerClient, list, func(done, total, n int) {
		need = n
		if state != nil {
			state.Progress(done, n)
		}
	})
	if need == 0 { // 回调未触发时兜底统计
		for _, img := range list {
			if svcCtx.HubImageInfo.NeedUpdate(img.ID) {
				need++
			}
		}
	}
	if state != nil {
		state.Finish(len(list), need, time.Since(start))
	}
	logx.Infof("检查更新完成：共 %d 个镜像，%d 个有新版本，耗时 %.1fs", len(list), need, time.Since(start).Seconds())
	return len(list), need, nil
}
