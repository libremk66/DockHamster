package selfupdate

import (
	"net/http"

	"github.com/libremk66/DockHamster/internal/logic/selfupdate"
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

func StatusHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := selfupdate.NewSelfUpdateLogic(r.Context(), svcCtx)
		resp, err := l.Status()
		write(w, r, resp, err)
	}
}

func RunHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := selfupdate.NewSelfUpdateLogic(r.Context(), svcCtx)
		resp, err := l.Run()
		write(w, r, resp, err)
	}
}
