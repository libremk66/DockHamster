package container

import (
	"net/http"

	"github.com/libremk66/DockHamster/internal/logic/container"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/rest/httpx"
)

func ListBackupsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := container.NewListBackupsLogic(r.Context(), svcCtx)
		resp, err := l.ListBackups()
		if err != nil {
			httpx.WriteJson(w, resp.Code, resp)
		} else {
			httpx.WriteJson(w, resp.Code, resp)
		}
	}
}
