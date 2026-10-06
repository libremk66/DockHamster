package compose

import (
	"context"
	"os"

	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/libremk66/DockHamster/internal/utiles"

	"github.com/zeromicro/go-zero/core/logx"
)

type ComposeFileLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewComposeFileLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ComposeFileLogic {
	return &ComposeFileLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ComposeFile 返回容器所属 compose 项目的文件内容（只读展示用）
func (l *ComposeFileLogic) ComposeFile(req *types.ComposeFileReq) (resp *types.Resp, err error) {
	resp = &types.Resp{}
	meta, mErr := utiles.ComposeMetaOfContainer(l.svcCtx, req.ContainerId)
	if mErr != nil {
		resp.Code = 500
		resp.Msg = mErr.Error()
		return resp, mErr
	}
	if !meta.IsManaged {
		resp.Code = 404
		resp.Msg = "该容器不是 compose 管理的容器"
		return resp, nil
	}
	files, fErr := utiles.ResolveConfigFiles(meta.ConfigFiles)
	if fErr != nil {
		resp.Code = 500
		resp.Msg = fErr.Error()
		return resp, fErr
	}
	fileMap := make(map[string]string, len(files))
	readErrors := make(map[string]string)
	for _, f := range files {
		data, readErr := os.ReadFile(f)
		if readErr != nil {
			l.Errorf("读取 compose 文件失败 %s: %v", f, readErr)
			readErrors[f] = readErr.Error()
			continue
		}
		fileMap[f] = string(data)
	}
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = map[string]interface{}{
		"project":    meta.Project,
		"service":    meta.Service,
		"workingDir": meta.WorkingDir,
		"files":      fileMap,
		"errors":     readErrors,
	}
	return resp, nil
}
