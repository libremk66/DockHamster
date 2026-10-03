package migrate

import (
	"net/http"
	"os"

	"github.com/libremk66/DockHamster/internal/logic/migrate"
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

func ImageReportHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := migrate.NewMigrateLogic(r.Context(), svcCtx)
		resp, err := l.ImageReport()
		write(w, r, resp, err)
	}
}

func TagImageHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.MigrateTagReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := migrate.NewMigrateLogic(r.Context(), svcCtx)
		resp, err := l.TagImage(&req)
		write(w, r, resp, err)
	}
}

func ExportHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.MigrateExportReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := migrate.NewMigrateLogic(r.Context(), svcCtx)
		resp, err := l.Export(&req)
		write(w, r, resp, err)
	}
}

func ListExportsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := migrate.NewMigrateLogic(r.Context(), svcCtx)
		resp, err := l.ListExports()
		write(w, r, resp, err)
	}
}

// DownloadExportHandler 流式下载迁移包（http.ServeFile 自带 Range 断点续传）
func DownloadExportHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		file := r.URL.Query().Get("file")
		path, perr := utiles.ExportPackagePath(file)
		if perr != nil {
			httpx.ErrorCtx(r.Context(), w, perr)
			return
		}
		if _, serr := os.Stat(path); serr != nil {
			httpx.ErrorCtx(r.Context(), w, serr)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+file+"\"")
		http.ServeFile(w, r, path)
	}
}

func DeleteExportHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		file := r.URL.Query().Get("file")
		l := migrate.NewMigrateLogic(r.Context(), svcCtx)
		resp, err := l.DeleteExport(file)
		write(w, r, resp, err)
	}
}

func UploadImportHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := migrate.NewMigrateLogic(r.Context(), svcCtx)
		resp, err := l.SaveUpload(r)
		write(w, r, resp, err)
	}
}

func PlanImportHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.MigratePlanReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := migrate.NewMigrateLogic(r.Context(), svcCtx)
		resp, err := l.Plan(&req)
		write(w, r, resp, err)
	}
}

func ApplyImportHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.MigrateApplyReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := migrate.NewMigrateLogic(r.Context(), svcCtx)
		resp, err := l.Apply(&req)
		write(w, r, resp, err)
	}
}
