package autoupdate

import (
	"github.com/libremk66/DockHamster/internal/notify"
	"net/http"

	"github.com/libremk66/DockHamster/internal/logic/autoupdate"
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

func GetSettingsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := autoupdate.NewAutoUpdateLogic(r.Context(), svcCtx)
		resp, err := l.GetSettings()
		write(w, r, resp, err)
	}
}

func SaveSettingsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req module.AutoUpdateSettings
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := autoupdate.NewAutoUpdateLogic(r.Context(), svcCtx)
		resp, err := l.SaveSettings(&req)
		write(w, r, resp, err)
	}
}

func RunHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := autoupdate.NewAutoUpdateLogic(r.Context(), svcCtx)
		resp, err := l.Run()
		write(w, r, resp, err)
	}
}

func CheckNowHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := autoupdate.NewAutoUpdateLogic(r.Context(), svcCtx)
		resp, err := l.CheckNow()
		write(w, r, resp, err)
	}
}

func CheckStatusHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := autoupdate.NewAutoUpdateLogic(r.Context(), svcCtx)
		resp, err := l.CheckStatus()
		write(w, r, resp, err)
	}
}

func StatusHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := autoupdate.NewAutoUpdateLogic(r.Context(), svcCtx)
		resp, err := l.Status()
		write(w, r, resp, err)
	}
}

func TasksHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := autoupdate.NewAutoUpdateLogic(r.Context(), svcCtx)
		resp, err := l.Tasks()
		write(w, r, resp, err)
	}
}

func TaskHistoryHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := autoupdate.NewAutoUpdateLogic(r.Context(), svcCtx)
		resp, err := l.TaskHistory()
		write(w, r, resp, err)
	}
}

func DeleteTaskHistoryHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			IDs []string `json:"ids"`
		}
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := autoupdate.NewAutoUpdateLogic(r.Context(), svcCtx)
		resp, err := l.DeleteTaskHistory(req.IDs)
		write(w, r, resp, err)
	}
}

func TestNotifyHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Channel string          `json:"channel"`
			Config  *notify.Channel `json:"config,optional"`
		}
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := autoupdate.NewAutoUpdateLogic(r.Context(), svcCtx)
		resp, err := l.TestNotify(req.Channel, req.Config)
		write(w, r, resp, err)
	}
}

func GroupUpdateHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.GroupUpdateReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := autoupdate.NewAutoUpdateLogic(r.Context(), svcCtx)
		resp, err := l.UpdateGroup(&req)
		write(w, r, resp, err)
	}
}
