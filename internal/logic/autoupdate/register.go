package autoupdate

import (
	"fmt"

	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/utiles"
	"github.com/robfig/cron/v3"
	"github.com/zeromicro/go-zero/core/logx"
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
		runCronSafe("自动更新", func() {
			// 兜底复位：RunAutoUpdate 自身有 defer，但 panic 路径仍要保证状态不残留
			svcCtx.AutoUpdateState.Finish()
			svcCtx.AutoUpdateState.ClearActive()
		}, func() {
			if !svcCtx.AutoUpdate.Get().Enabled {
				return
			}
			utiles.RunAutoUpdate(svcCtx, "auto")
			// 本轮结束后重挂"更新前提醒"定时器（对准下一次计划时间）
			utiles.ScheduleBeforeUpdateNotice(svcCtx)
		})
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
		runCronSafe("更新检查", nil, func() {
			if _, _, cerr := utiles.CheckAllImageUpdates(svcCtx, "cron"); cerr != nil {
				logx.Errorf("定时检查更新失败: %v", cerr)
			}
		})
	})
	if err != nil {
		return fmt.Errorf("检查更新计划无效: %w", err)
	}
	svcCtx.CheckCronID = id2

	// ③ 更新前提醒（提前量）：重算并重挂（启动时、改设置后都会走到这里）
	utiles.ScheduleBeforeUpdateNotice(svcCtx)
	return nil
}

// runCronSafe 在 cron 回调外围捕获 panic。
// robfig/cron 的 New() 默认 chain 为空，不带 Recover；panic 会从 goroutine 逃逸并退出整个进程。
// onPanic 用于复位可能残留的运行状态（可重复调用）。
func runCronSafe(name string, onPanic func(), fn func()) {
	defer func() {
		if r := recover(); r != nil {
			logx.Errorf("定时任务 %s panic: %v", name, r)
			if onPanic != nil {
				onPanic()
			}
		}
	}()
	fn()
}

// ValidateCron 校验 cron 表达式（标准 5 段）
func ValidateCron(expr string) error {
	_, err := cron.ParseStandard(expr)
	return err
}
