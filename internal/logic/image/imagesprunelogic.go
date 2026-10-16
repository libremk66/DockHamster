package image

import (
	"context"
	"fmt"

	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/libremk66/DockHamster/internal/utiles"
	"github.com/zeromicro/go-zero/core/logx"
)

type ImagesPruneLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewImagesPruneLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ImagesPruneLogic {
	return &ImagesPruneLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ImagesPrune 反复清理无 Tag 镜像：删掉当前叶子后其父层降级成叶子，下一轮继续，直到无可删项。
func (l *ImagesPruneLogic) ImagesPrune() (resp *types.Resp, err error) {
	resp = &types.Resp{}
	result, perr := utiles.PruneDanglingImages(l.svcCtx)
	if perr != nil {
		resp.Code = 500
		resp.Msg = utiles.FriendlyDaemonError(perr).Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	resp.Code = 200
	resp.Msg = fmt.Sprintf("已清理 %d 个无Tag镜像", result.Removed)
	resp.Data = map[string]interface{}{
		"removed": result.Removed,
		"skipped": result.Skipped,
		"rounds":  result.Rounds,
	}
	return resp, nil
}
