package image

import (
	"context"

	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/libremk66/DockHamster/internal/utiles"

	"github.com/zeromicro/go-zero/core/logx"
)

// IntermediateStat 构建中间层统计（镜像页的透明提示：面板列表看不见它们，但能清理）。
type IntermediateStatLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewIntermediateStatLogic(ctx context.Context, svcCtx *svc.ServiceContext) *IntermediateStatLogic {
	return &IntermediateStatLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *IntermediateStatLogic) IntermediateStat() (resp *types.Resp, err error) {
	resp = &types.Resp{}
	stat, serr := utiles.CollectIntermediateImages(l.svcCtx)
	if serr != nil {
		resp.Code = 500
		resp.Msg = utiles.FriendlyDaemonError(serr).Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = map[string]interface{}{
		"count":      stat.Count,
		"totalSize":  stat.TotalSize,
		"sizeText":   utiles.FormatBytes(stat.TotalSize),
		"inUse":      stat.InUse,
		"untilHours": utiles.IntermediateUntilHours(),
	}
	return resp, nil
}
