package container

import (
	"context"
	"github.com/libremk66/DockHamster/internal/utiles"

	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type BackupLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewBackupLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BackupLogic {
	return &BackupLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *BackupLogic) Backup() (resp *types.Resp, err error) {
	resp = &types.Resp{}
	err = utiles.BackupContainer(l.svcCtx)
	if err != nil {
		resp.Code = 500
		resp.Msg = err.Error()
		resp.Data = map[string]interface{}{}
		return resp, err
	}
	resp.Msg = "success"
	resp.Code = 200
	resp.Data = map[string]interface{}{}
	return resp, nil
}
