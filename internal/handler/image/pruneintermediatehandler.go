package image

import (
	"net/http"

	"github.com/libremk66/DockHamster/internal/logic/image"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/rest/httpx"
)

func PruneIntermediateHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := image.NewPruneIntermediateLogic(r.Context(), svcCtx)
		resp, err := l.PruneIntermediate()
		if err != nil {
			httpx.WriteJson(w, resp.Code, resp)
		} else {
			httpx.WriteJson(w, resp.Code, resp)
		}
	}
}
