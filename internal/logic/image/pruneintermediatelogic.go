package image

import (
	"context"

	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/libremk66/DockHamster/internal/utiles"

	"github.com/zeromicro/go-zero/core/logx"
)

// PruneIntermediate 清理构建中间层（无标签、无引用；默认只清 24 小时前的）。
// 与「删除无Tag镜像」的区别：这个走 daemon 的 prune 级联通道，能处理父层依赖，
// 而且覆盖面板镜像列表**看不见**的中间层（列表接口默认隐藏它们）。
type PruneIntermediateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewPruneIntermediateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PruneIntermediateLogic {
	return &PruneIntermediateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *PruneIntermediateLogic) PruneIntermediate() (resp *types.Resp, err error) {
	resp = &types.Resp{}
	deleted, space, untilHours, perr := utiles.PruneIntermediateImages(l.svcCtx, utiles.IntermediateUntilHours())
	if perr != nil {
		resp.Code = 409
		resp.Msg = utiles.FriendlyDaemonError(perr).Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = map[string]interface{}{
		"deleted":    deleted,
		"space":      space,
		"spaceText":  utiles.FormatBytes(space),
		"untilHours": untilHours,
	}
	return resp, nil
}
