package autoupdate

import (
	"net/http"

	"github.com/onlyLTY/dockerCopilot/internal/logic/autoupdate"
	"github.com/onlyLTY/dockerCopilot/internal/module"
	"github.com/onlyLTY/dockerCopilot/internal/svc"
	"github.com/onlyLTY/dockerCopilot/internal/types"
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

func StatusHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := autoupdate.NewAutoUpdateLogic(r.Context(), svcCtx)
		resp, err := l.Status()
		write(w, r, resp, err)
	}
}

func TestNotifyHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.TestNotifyReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := autoupdate.NewAutoUpdateLogic(r.Context(), svcCtx)
		resp, err := l.TestNotify(&req)
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
