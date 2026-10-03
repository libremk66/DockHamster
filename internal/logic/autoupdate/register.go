package autoupdate

import (
	"github.com/onlyLTY/dockerCopilot/internal/svc"
	"github.com/onlyLTY/dockerCopilot/internal/utiles"
	"github.com/robfig/cron/v3"
)

// RegisterAutoUpdateCron 按当前设置注册/重注册自动更新定时任务（UI 改 cron 后调用）
func RegisterAutoUpdateCron(svcCtx *svc.ServiceContext) error {
	svcCtx.CronMu.Lock()
	defer svcCtx.CronMu.Unlock()
	if svcCtx.CronEngine == nil {
		return nil
	}
	if svcCtx.AutoUpdateCronID != 0 {
		svcCtx.CronEngine.Remove(svcCtx.AutoUpdateCronID)
		svcCtx.AutoUpdateCronID = 0
	}
	expr := svcCtx.AutoUpdate.Get().Cron
	id, err := svcCtx.CronEngine.AddFunc(expr, func() {
		if !svcCtx.AutoUpdate.Get().Enabled {
			return
		}
		utiles.RunAutoUpdate(svcCtx, "auto")
	})
	if err != nil {
		return err
	}
	svcCtx.AutoUpdateCronID = id
	return nil
}

// ValidateCron 校验 cron 表达式（标准 5 段）
func ValidateCron(expr string) error {
	_, err := cron.ParseStandard(expr)
	return err
}
