package accelerator

import (
	"net/http"

	"github.com/libremk66/DockHamster/internal/logic/accelerator"
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
		l := accelerator.NewAcceleratorLogic(r.Context(), svcCtx)
		resp, err := l.GetSettings()
		write(w, r, resp, err)
	}
}

func SaveSettingsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req module.AcceleratorSettings
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := accelerator.NewAcceleratorLogic(r.Context(), svcCtx)
		resp, err := l.SaveSettings(&req)
		write(w, r, resp, err)
	}
}

func TestHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Sources []string `json:"sources,optional"`
		}
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := accelerator.NewAcceleratorLogic(r.Context(), svcCtx)
		resp, err := l.Test(req.Sources)
		write(w, r, resp, err)
	}
}

func PullHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Source string `json:"source"`
			Image  string `json:"image"`
		}
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := accelerator.NewAcceleratorLogic(r.Context(), svcCtx)
		resp, err := l.Pull(req.Source, req.Image)
		write(w, r, resp, err)
	}
}
