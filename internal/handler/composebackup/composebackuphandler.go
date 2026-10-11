package composebackup

import (
	"net/http"

	"github.com/libremk66/DockHamster/internal/logic/composebackup"
	"github.com/libremk66/DockHamster/internal/module"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
)

func write(w http.ResponseWriter, r *http.Request, resp *types.Resp, err error) {
	if err != nil {
		httpx.WriteJson(w, resp.Code, resp)
	} else {
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}

// GetSettingsHandler GET /api/composeBackup/settings
func GetSettingsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := composebackup.NewComposeBackupLogic(r.Context(), svcCtx)
		resp, err := l.GetSettings()
		write(w, r, resp, err)
	}
}

// SaveSettingsHandler POST /api/composeBackup/settings
func SaveSettingsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req module.ComposeBackupSettings
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := composebackup.NewComposeBackupLogic(r.Context(), svcCtx)
		resp, err := l.SaveSettings(&req)
		write(w, r, resp, err)
	}
}

// TestHandler POST /api/composeBackup/test —— 测试 WebDAV 连通性（用表单当前值，不落盘）
func TestHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req composebackup.TestReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := composebackup.NewComposeBackupLogic(r.Context(), svcCtx)
		resp, err := l.Test(&req)
		write(w, r, resp, err)
	}
}

// RunHandler POST /api/composeBackup/run —— 立即备份一次
func RunHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := composebackup.NewComposeBackupLogic(r.Context(), svcCtx)
		resp, err := l.Run()
		write(w, r, resp, err)
	}
}
