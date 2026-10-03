package utiles

import (
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
)

// CheckAllImageUpdates 检查全部镜像的远端 digest，刷新"有新版本"状态。
// 供三处复用：启动时、定时任务（checkCron）、以及 UI 的「检查更新」按钮。
// 返回（检查总数, 需要更新数）。
func CheckAllImageUpdates(svcCtx *svc.ServiceContext) (int, int, error) {
	list, err := GetImagesList(svcCtx)
	if err != nil {
		logx.Errorf("检查更新：获取镜像列表失败: %v", err)
		return 0, 0, err
	}
	svcCtx.HubImageInfo.CheckUpdate(svcCtx.DockerClient, list)
	need := 0
	for _, img := range list {
		if svcCtx.HubImageInfo.NeedUpdate(img.ID) {
			need++
		}
	}
	logx.Infof("检查更新完成：共 %d 个镜像，%d 个有新版本", len(list), need)
	return len(list), need, nil
}
