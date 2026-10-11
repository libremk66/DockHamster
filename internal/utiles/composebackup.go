package utiles

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/libremk66/DockHamster/internal/notify"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
)

// composeBackupPrefix 备份文件名前缀（保留策略按它识别自家备份）
const composeBackupPrefix = "dockhamster-compose-"

// backupTimeout 单次备份总超时（打包 + 上传 + 清理）
const backupTimeout = 3 * time.Minute

// RunComposeBackup 执行一次 compose 配置备份：
// 打包（可读项目）→ 上传 WebDAV（时间戳命名）→ 清理超出保留份数的旧备份 → 记录结果。
// 失败时记录 LastError 并通过已配置的通知渠道告警（定时任务无人盯屏，必须主动告知）。
func RunComposeBackup(svcCtx *svc.ServiceContext) (fileName string, size int, deleted []string, err error) {
	st := svcCtx.ComposeBackup.Get()
	if strings.TrimSpace(st.URL) == "" {
		return "", 0, nil, fmt.Errorf("未配置 WebDAV 地址")
	}

	finish := func(f string, n int, d []string, e error) (string, int, []string, error) {
		at := time.Now().Format("2006-01-02 15:04:05")
		emsg := ""
		if e != nil {
			emsg = e.Error()
			logx.Errorf("compose 配置备份失败: %v", e)
			sendBackupAlert(svcCtx, false, at, e)
		} else {
			logx.Infof("compose 配置备份完成: %s（%d 字节，清理 %d 份）", f, n, len(d))
		}
		svcCtx.ComposeBackup.UpdateLastRun(at, f, e == nil, emsg)
		return f, n, d, e
	}

	res, berr := BuildComposeConfigZip(svcCtx, "")
	if berr != nil {
		return finish("", 0, nil, berr)
	}
	client, cerr := NewWebDAVClient(st.URL, st.Username, st.Password, 90*time.Second)
	if cerr != nil {
		return finish("", 0, nil, cerr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), backupTimeout)
	defer cancel()

	if merr := client.MkdirAll(ctx, st.RemoteDir); merr != nil {
		return finish("", 0, nil, merr)
	}
	fileName = composeBackupPrefix + time.Now().Format("20060102-150405") + ".zip"
	if perr := client.Put(ctx, path.Join(strings.Trim(st.RemoteDir, "/"), fileName), res.Data); perr != nil {
		return finish("", 0, nil, perr)
	}
	// 清理旧份：失败不致命（备份已上传成功），降级为日志
	deleted, derr := client.PruneOldBackups(ctx, st.RemoteDir, composeBackupPrefix, st.Keep)
	if derr != nil {
		logx.Errorf("清理旧备份失败（备份已上传）: %v", derr)
	}
	return finish(fileName, len(res.Data), deleted, nil)
}

// sendBackupAlert 备份失败通知（走已配置的通知渠道；没配渠道则静默）
func sendBackupAlert(svcCtx *svc.ServiceContext, ok bool, at string, runErr error) {
	settings := svcCtx.AutoUpdate.Get()
	ch := settings.Notify
	title := "⚠️ " + HostTag(svcCtx) + "compose 配置备份失败 · " + at
	text := fmt.Sprintf("自动备份没有完成（%s）。\n\n原因：%s\n\n可在「Compose」页 →「自动备份到 WebDAV」检查设置后点「立即备份」重试。",
		at, runErr.Error())
	for _, r := range notify.Send(ch, title, text) {
		if !r.OK {
			logx.Errorf("备份告警通知发送失败(%s): %s", r.Channel, r.Error)
		}
	}
}
