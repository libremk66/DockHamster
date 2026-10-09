package migrate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/libremk66/DockHamster/internal/module"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/libremk66/DockHamster/internal/utiles"
	"github.com/zeromicro/go-zero/core/logx"
)

type MigrateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewMigrateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MigrateLogic {
	return &MigrateLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

// ImageReport 镜像体检：分类出"换机即丢"的镜像
func (l *MigrateLogic) ImageReport() (resp *types.Resp, err error) {
	resp = &types.Resp{}
	settings := l.svcCtx.AutoUpdate.Get()
	report, rerr := utiles.ClassifyImages(l.svcCtx, settings.SnapshotPrefix)
	if rerr != nil {
		resp.Code = 500
		resp.Msg = "扫描镜像失败: " + rerr.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = report
	return resp, nil
}

// TagImage 给镜像打一个可搬运的标签
func (l *MigrateLogic) TagImage(req *types.MigrateTagReq) (resp *types.Resp, err error) {
	resp = &types.Resp{}
	ref, terr := utiles.TagImageForMigration(l.svcCtx, req.ImageID, req.Ref)
	if terr != nil {
		resp.Code = 400
		resp.Msg = terr.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = map[string]interface{}{"ref": ref}
	return resp, nil
}

// Export 生成迁移包（异步，返回 taskID 供前端看进度）
func (l *MigrateLogic) Export(req *types.MigrateExportReq) (resp *types.Resp, err error) {
	resp = &types.Resp{}
	taskID := uuid.New().String()
	l.svcCtx.InitTask(taskID, "迁移导出", "migrate", "migrate")
	opts := utiles.ExportOptions{
		Containers:    req.Containers,
		IncludeImages: req.IncludeImages,
		Compress:      req.Compress,
		RedactEnv:     req.RedactEnv,
		LocalImages:   req.LocalImages,
		Note:          req.Note,
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				l.Errorf("导出过程 panic: %v", r)
			}
		}()
		name, eerr := utiles.ExportPackage(l.svcCtx, opts, taskID)
		if eerr != nil {
			l.Errorf("导出失败: %v", eerr)
			l.svcCtx.UpdateProgress(taskID, svc.TaskProgress{
				TaskID: taskID, Name: "迁移导出", Percentage: 100,
				Message: "导出失败", DetailMsg: eerr.Error(), IsDone: true, Failed: true,
			})
			return
		}
		l.Infof("导出完成: %s", name)
	}()
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = map[string]interface{}{"taskID": taskID}
	return resp, nil
}

// ListExports 已生成的迁移包
func (l *MigrateLogic) ListExports() (resp *types.Resp, err error) {
	resp = &types.Resp{}
	pkgs, lerr := utiles.ListExportPackages()
	if lerr != nil {
		resp.Code = 500
		resp.Msg = "读取导出目录失败: " + lerr.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = map[string]interface{}{"packages": pkgs}
	return resp, nil
}

// DeleteExport 删除一个迁移包
func (l *MigrateLogic) DeleteExport(file string) (resp *types.Resp, err error) {
	resp = &types.Resp{}
	path, perr := utiles.ExportPackagePath(file)
	if perr != nil {
		resp.Code = 400
		resp.Msg = perr.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	if err := os.Remove(path); err != nil {
		resp.Code = 500
		resp.Msg = "删除失败: " + err.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = map[string]interface{}{}
	return resp, nil
}

// SaveUpload 保存上传的迁移包到 /data/imports
func (l *MigrateLogic) SaveUpload(r *http.Request) (resp *types.Resp, err error) {
	resp = &types.Resp{}
	if err := os.MkdirAll("/data/imports", 0755); err != nil {
		resp.Code = 500
		resp.Msg = "创建导入目录失败: " + err.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	// 上限 8GB，防误传超大文件
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		resp.Code = 400
		resp.Msg = "解析上传失败: " + err.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	file, header, ferr := r.FormFile("file")
	if ferr != nil {
		resp.Code = 400
		resp.Msg = "缺少上传文件字段 file"
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	defer func() { _ = file.Close() }()
	name := filepath.Base(header.Filename)
	if !strings.HasSuffix(name, ".tar.gz") {
		resp.Code = 400
		resp.Msg = "只接受 .tar.gz 迁移包"
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	dest := filepath.Join("/data/imports", name)
	out, oerr := os.Create(dest)
	if oerr != nil {
		resp.Code = 500
		resp.Msg = "写入失败: " + oerr.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	defer func() { _ = out.Close() }()
	written, cerr := io.Copy(out, io.LimitReader(file, 8<<30))
	if cerr != nil {
		resp.Code = 500
		resp.Msg = "保存失败: " + cerr.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	// 上传完立即校验能否读出 manifest（避免用户拿到坏包白折腾）
	if _, merr := utiles.ReadManifestFromPackage(dest); merr != nil {
		_ = os.Remove(dest)
		resp.Code = 400
		resp.Msg = "迁移包校验失败: " + merr.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = map[string]interface{}{"file": name, "size": written}
	return resp, nil
}

// Plan 导入预检（dry-run）
func (l *MigrateLogic) Plan(req *types.MigratePlanReq) (resp *types.Resp, err error) {
	resp = &types.Resp{}
	path, perr := utiles.ImportPackagePath(req.File)
	if perr != nil {
		resp.Code = 400
		resp.Msg = perr.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	if _, serr := os.Stat(path); serr != nil {
		resp.Code = 404
		resp.Msg = "迁移包不存在（请先上传）"
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	plan, plerr := utiles.BuildImportPlan(l.svcCtx, path, req.File)
	if plerr != nil {
		resp.Code = 400
		resp.Msg = "预检失败: " + plerr.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = plan
	return resp, nil
}

// Apply 执行导入（异步）
func (l *MigrateLogic) Apply(req *types.MigrateApplyReq) (resp *types.Resp, err error) {
	resp = &types.Resp{}
	path, perr := utiles.ImportPackagePath(req.File)
	if perr != nil {
		resp.Code = 400
		resp.Msg = perr.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	if _, serr := os.Stat(path); serr != nil {
		resp.Code = 404
		resp.Msg = "迁移包不存在（请先上传）"
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	taskID := uuid.New().String()
	l.svcCtx.InitTask(taskID, "迁移导入", "migrate", "migrate")
	start := req.Start
	autoDirs := req.AutoCreateDirs
	items := make([]module.ImportItemOverride, 0, len(req.Items))
	for _, it := range req.Items {
		items = append(items, module.ImportItemOverride{
			Name:           it.Name,
			Skip:           it.Skip,
			NewName:        it.NewName,
			PortMap:        it.PortMap,
			MountMap:       it.MountMap,
			AutoCreateDirs: it.AutoCreateDirs,
		})
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				l.Errorf("导入过程 panic: %v", r)
			}
		}()
		results, aerr := utiles.ApplyImport(l.svcCtx, path, req.File, items, autoDirs, start, taskID)
		if aerr != nil {
			l.Errorf("导入失败: %v", aerr)
			l.svcCtx.UpdateProgress(taskID, svc.TaskProgress{
				TaskID: taskID, Name: "迁移导入", Percentage: 100,
				Message: "导入失败", DetailMsg: aerr.Error(), IsDone: true, Failed: true,
			})
			return
		}
		l.Infof("导入完成: %v", results)
	}()
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = map[string]interface{}{"taskID": taskID}
	return resp, nil
}

var _ = fmt.Sprintf
