package projects

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/utiles"
	"github.com/zeromicro/go-zero/core/logx"
)

type ExportLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewExportLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ExportLogic {
	return &ExportLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Export 打包 compose 配置（project 为空 = 全部可读项目），返回 zip 内容与建议文件名
func (l *ExportLogic) Export(project string) (*utiles.ComposeExportResult, string, error) {
	res, err := utiles.BuildComposeConfigZip(l.svcCtx, project)
	if err != nil {
		return nil, "", err
	}
	ts := time.Now().Format("20060102-150405")
	name := "dockhamster-compose-" + ts + ".zip"
	if p := strings.TrimSpace(project); p != "" {
		name = "dockhamster-compose-" + unsafeFileChars.ReplaceAllString(p, "_") + "-" + ts + ".zip"
	}
	return res, name, nil
}
