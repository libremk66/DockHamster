package image

import (
	"net/http"

	"github.com/libremk66/DockHamster/internal/logic/image"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/rest/httpx"
)

func ImagesPruneHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := image.NewImagesPruneLogic(r.Context(), svcCtx)
		resp, err := l.ImagesPrune()
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		httpx.WriteJson(w, resp.Code, resp)
	}
}
