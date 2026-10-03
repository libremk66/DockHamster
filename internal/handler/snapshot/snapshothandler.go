package snapshot

import (
	"net/http"

	"github.com/libremk66/DockHamster/internal/logic/snapshot"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/libremk66/DockHamster/internal/utiles"
	"github.com/zeromicro/go-zero/rest/httpx"
)

func write(w http.ResponseWriter, r *http.Request, resp *types.Resp, err error) {
	if err != nil {
		httpx.WriteJson(w, resp.Code, resp)
	} else {
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}

func ListHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := snapshot.NewSnapshotLogic(r.Context(), svcCtx)
		resp, err := l.List()
		write(w, r, resp, err)
	}
}

func CreateHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.SnapshotCreateReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := snapshot.NewSnapshotLogic(r.Context(), svcCtx)
		resp, err := l.Create(&req)
		write(w, r, resp, err)
	}
}

func RollbackHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.SnapshotRollbackReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := snapshot.NewSnapshotLogic(r.Context(), svcCtx)
		resp, err := l.Rollback(&req)
		write(w, r, resp, err)
	}
}

func PruneHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.SnapshotPruneReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := snapshot.NewSnapshotLogic(r.Context(), svcCtx)
		resp, err := l.Prune(&req)
		write(w, r, resp, err)
	}
}

func DeleteHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		refs := utiles.ParseRefsParam(r.URL.Query().Get("refs"))
		l := snapshot.NewSnapshotLogic(r.Context(), svcCtx)
		resp, err := l.Delete(refs)
		write(w, r, resp, err)
	}
}
