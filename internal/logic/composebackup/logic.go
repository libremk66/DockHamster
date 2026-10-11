package composebackup

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/libremk66/DockHamster/internal/logic/autoupdate"
	"github.com/libremk66/DockHamster/internal/module"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/libremk66/DockHamster/internal/utiles"
	"github.com/zeromicro/go-zero/core/logx"
)

// —— Compose 配置自动备份（WebDAV）——

type ComposeBackupLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewComposeBackupLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ComposeBackupLogic {
	return &ComposeBackupLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// GetSettings 读取备份设置（含最近一次执行结果）
func (l *ComposeBackupLogic) GetSettings() (resp *types.Resp, err error) {
	resp = &types.Resp{Code: 200, Msg: "success", Data: l.svcCtx.ComposeBackup.Get()}
	return resp, nil
}

// SaveSettings 保存备份设置（校验 cron / URL）并重排定时任务
func (l *ComposeBackupLogic) SaveSettings(req *module.ComposeBackupSettings) (resp *types.Resp, err error) {
	resp = &types.Resp{}
	req.Normalize()
	if req.Enabled {
		if strings.TrimSpace(req.URL) == "" {
			resp.Code, resp.Msg, resp.Data = 400, "开启自动备份前请先填写 WebDAV 地址", map[string]interface{}{}
			return resp, nil
		}
		if err := autoupdate.ValidateCron(req.Cron); err != nil {
			resp.Code, resp.Msg, resp.Data = 400, "备份计划（cron）无效："+err.Error(), map[string]interface{}{}
			return resp, nil
		}
	}
	if err := l.svcCtx.ComposeBackup.SaveSettings(*req); err != nil {
		resp.Code, resp.Msg, resp.Data = 500, "保存失败："+err.Error(), map[string]interface{}{}
		return resp, err
	}
	if rerr := RegisterBackupCron(l.svcCtx); rerr != nil {
		logx.Errorf("备份定时任务重注册失败: %v", rerr)
	}
	resp.Code, resp.Msg, resp.Data = 200, "success", l.svcCtx.ComposeBackup.Get()
	return resp, nil
}

// TestReq 测试连通性请求（表单当前值；不落盘）
type TestReq struct {
	URL       string `json:"url"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	RemoteDir string `json:"remoteDir"`
}

// Test 测试 WebDAV 连通性：PROPFIND 目标目录；目录不存在不算失败（首次备份会自动创建）
func (l *ComposeBackupLogic) Test(req *TestReq) (resp *types.Resp, err error) {
	resp = &types.Resp{}
	client, cerr := utiles.NewWebDAVClient(req.URL, req.Username, req.Password, 15*time.Second)
	if cerr != nil {
		resp.Code, resp.Msg, resp.Data = 400, cerr.Error(), map[string]interface{}{}
		return resp, nil
	}
	ctx, cancel := context.WithTimeout(l.ctx, 20*time.Second)
	defer cancel()
	exists, perr := client.Ping(ctx, req.RemoteDir)
	if perr != nil {
		resp.Code, resp.Msg, resp.Data = 200, perr.Error(), map[string]interface{}{"ok": false}
		return resp, nil
	}
	msg := "连接成功"
	if !exists && strings.Trim(req.RemoteDir, "/") != "" {
		msg = "连接成功（远端目录还不存在，首次备份会自动创建）"
	}
	resp.Code, resp.Msg, resp.Data = 200, msg, map[string]interface{}{"ok": true, "dirExists": exists}
	return resp, nil
}

// Run 立即备份（同步执行：打包→上传→清理旧份；数据量小、分钟级内完成）
func (l *ComposeBackupLogic) Run() (resp *types.Resp, err error) {
	resp = &types.Resp{}
	file, size, deleted, rerr := utiles.RunComposeBackup(l.svcCtx)
	if rerr != nil {
		resp.Code, resp.Msg = 500, "备份失败："+rerr.Error()
		resp.Data = map[string]interface{}{"ok": false}
		return resp, nil
	}
	resp.Code, resp.Msg = 200, "success"
	resp.Data = map[string]interface{}{
		"ok":      true,
		"file":    file,
		"size":    size,
		"deleted": deleted,
		"message": fmt.Sprintf("已上传 %s（%s）", file, humanSize(size)),
	}
	return resp, nil
}

func humanSize(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/1024/1024)
	case n >= 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
