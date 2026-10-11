package composebackup

import (
	"fmt"
	"strings"

	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/utiles"
)

// RegisterBackupCron 按当前设置注册/重注册 compose 配置备份定时任务（保存设置后与启动时调用）。
// 未开启或没填地址时不挂任务（等效于关闭）。
func RegisterBackupCron(svcCtx *svc.ServiceContext) error {
	svcCtx.CronMu.Lock()
	defer svcCtx.CronMu.Unlock()
	if svcCtx.CronEngine == nil {
		return nil
	}
	if svcCtx.ComposeBackupCronID != 0 {
		svcCtx.CronEngine.Remove(svcCtx.ComposeBackupCronID)
		svcCtx.ComposeBackupCronID = 0
	}
	st := svcCtx.ComposeBackup.Get()
	if !st.Enabled || strings.TrimSpace(st.URL) == "" {
		return nil
	}
	id, err := svcCtx.CronEngine.AddFunc(st.Cron, func() {
		cur := svcCtx.ComposeBackup.Get()
		if !cur.Enabled {
			return // 触发瞬间又关了
		}
		utiles.RunComposeBackup(svcCtx)
	})
	if err != nil {
		return fmt.Errorf("备份计划无效: %w", err)
	}
	svcCtx.ComposeBackupCronID = id
	return nil
}
