package projects

import (
	"net/http"
	"strconv"

	"github.com/libremk66/DockHamster/internal/logic/projects"
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

// ListHandler GET /api/projects —— 「Compose」页（compose 项目视图）
func ListHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := projects.NewProjectsListLogic(r.Context(), svcCtx)
		resp, err := l.ProjectsList()
		write(w, r, resp, err)
	}
}

// ExportReq 导出请求（POST：密码走 body，避免出现在 URL/访问日志里）
type ExportReq struct {
	Project  string `json:"project,optional"`
	Password string `json:"password,optional"`
}

// ExportHandler POST /api/projects/export —— 打包下载 compose 配置（zip 附件；password 非空 = AES-256）
func ExportHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ExportReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := projects.NewExportLogic(r.Context(), svcCtx)
		res, name, err := l.Export(req.Project, req.Password)
		if err != nil {
			httpx.WriteJson(w, 500, &types.Resp{Code: 500, Msg: err.Error(), Data: map[string]interface{}{}})
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(res.Data)))
		_, _ = w.Write(res.Data)
	}
}

// SaveRootsHandler POST /api/projects/roots —— 保存额外 compose 根目录
func SaveRootsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req projects.SaveRootsReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := projects.NewSaveRootsLogic(r.Context(), svcCtx)
		resp, err := l.SaveRoots(&req)
		write(w, r, resp, err)
	}
}
