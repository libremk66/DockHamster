package autoupdate

import (
	"fmt"

	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/utiles"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/robfig/cron/v3"
)

// RegisterCrons 按当前设置注册/重注册两条定时任务（UI 改 cron 后调用）：
//  1. 自动更新（做）：到点执行更新，受总开关控制
//  2. 更新检查（看）：定期探测 registry 刷新"有新版本"角标，与总开关无关（只读、无害）
func RegisterCrons(svcCtx *svc.ServiceContext) error {
	svcCtx.CronMu.Lock()
	defer svcCtx.CronMu.Unlock()
	if svcCtx.CronEngine == nil {
		return nil
	}
	settings := svcCtx.AutoUpdate.Get()

	// ① 自动更新
	if svcCtx.AutoUpdateCronID != 0 {
		svcCtx.CronEngine.Remove(svcCtx.AutoUpdateCronID)
		svcCtx.AutoUpdateCronID = 0
	}
	id, err := svcCtx.CronEngine.AddFunc(settings.Cron, func() {
		if !svcCtx.AutoUpdate.Get().Enabled {
			return
		}
		utiles.RunAutoUpdate(svcCtx, "auto")
	})
	if err != nil {
		return fmt.Errorf("自动更新计划无效: %w", err)
	}
	svcCtx.AutoUpdateCronID = id

	// ② 更新检查
	if svcCtx.CheckCronID != 0 {
		svcCtx.CronEngine.Remove(svcCtx.CheckCronID)
		svcCtx.CheckCronID = 0
	}
	id2, err := svcCtx.CronEngine.AddFunc(settings.CheckCron, func() {
		if _, _, cerr := utiles.CheckAllImageUpdates(svcCtx); cerr != nil {
			logx.Errorf("定时检查更新失败: %v", cerr)
		}
	})
	if err != nil {
		return fmt.Errorf("检查更新计划无效: %w", err)
	}
	svcCtx.CheckCronID = id2
	return nil
}

// ValidateCron 校验 cron 表达式（标准 5 段）
func ValidateCron(expr string) error {
	_, err := cron.ParseStandard(expr)
	return err
}
