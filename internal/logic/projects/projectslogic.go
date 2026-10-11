package projects

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/libremk66/DockHamster/internal/selfupdate"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/libremk66/DockHamster/internal/utiles"

	"github.com/zeromicro/go-zero/core/logx"
)

// —— 「Compose」页（compose 项目视图）——

type ProjectsListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewProjectsListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ProjectsListLogic {
	return &ProjectsListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ProjectsList 返回项目/服务分组视图 + 可达性 + 挂载引导 + 变量体检
func (l *ProjectsListLogic) ProjectsList() (resp *types.Resp, err error) {
	resp = &types.Resp{}
	// utiles 不 import selfupdate（依赖环），面板自身容器 ID 从这里传入
	view, verr := utiles.BuildComposeProjectsView(l.svcCtx, selfupdate.CurrentContainerID())
	if verr != nil {
		resp.Code = 500
		resp.Msg = utiles.FriendlyDaemonError(verr).Error()
		resp.Data = map[string]interface{}{}
		return resp, verr
	}
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = view
	return resp, nil
}

// SaveRootsReq 额外根目录保存请求
type SaveRootsReq struct {
	Roots []string `json:"roots"`
}

type SaveRootsLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewSaveRootsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SaveRootsLogic {
	return &SaveRootsLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// SaveRoots 保存「Compose」页的额外 compose 根目录（绝对路径、须在面板容器内可达）。
// 只更新这一个字段，读改写都在同一进程内完成，不会覆盖其它设置。
func (l *SaveRootsLogic) SaveRoots(req *SaveRootsReq) (resp *types.Resp, err error) {
	resp = &types.Resp{}
	roots := make([]string, 0, len(req.Roots))
	seen := map[string]bool{}
	for _, r := range req.Roots {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if !filepath.IsAbs(r) {
			resp.Code = 400
			resp.Msg = fmt.Sprintf("必须是绝对路径：%s", r)
			resp.Data = map[string]interface{}{}
			return resp, nil
		}
		r = filepath.Clean(r)
		if seen[r] {
			continue
		}
		if st, serr := os.Stat(r); serr != nil || !st.IsDir() {
			resp.Code = 400
			resp.Msg = fmt.Sprintf("目录在面板容器内不可达（不存在）：%s；请先确认它已挂载进面板容器", r)
			resp.Data = map[string]interface{}{}
			return resp, nil
		}
		seen[r] = true
		roots = append(roots, r)
	}

	s := l.svcCtx.AutoUpdate.Get()
	s.ComposeExtraRoots = roots
	if serr := l.svcCtx.AutoUpdate.Save(s); serr != nil {
		resp.Code = 500
		resp.Msg = "保存失败：" + serr.Error()
		resp.Data = map[string]interface{}{}
		return resp, serr
	}
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = map[string]interface{}{"roots": roots}
	return resp, nil
}
