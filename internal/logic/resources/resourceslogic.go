package resources

import (
	"context"
	"fmt"

	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/libremk66/DockHamster/internal/utiles"

	"github.com/zeromicro/go-zero/core/logx"
)

// ResourcesLogic 「资源清理」页：未使用卷/网络的查看与清理。
type ResourcesLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewResourcesLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ResourcesLogic {
	return &ResourcesLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// Stat 未使用卷/网络统计。
func (l *ResourcesLogic) Stat() (resp *types.Resp, err error) {
	resp = &types.Resp{}
	stat, serr := utiles.CollectResourcesStat(l.svcCtx)
	if serr != nil {
		resp.Code = 500
		resp.Msg = utiles.FriendlyDaemonError(serr).Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	// 汇总大小（卷）——注意：daemon 对 volume 的 usage 数据常为 0（未启用 du），
	// 前端在 sizeUnavailable 时显示"大小未知"而不是误导性的 0 B
	var volSize int64
	for _, v := range stat.AnonymousVolumes {
		volSize += v.Size
	}
	for _, v := range stat.ProjectVolumes {
		volSize += v.Size
	}
	// 空切片保证序列化为 []（而非 null），前端可直接 .map
	anonymous := stat.AnonymousVolumes
	if anonymous == nil {
		anonymous = []utiles.UnusedVolumeItem{}
	}
	project := stat.ProjectVolumes
	if project == nil {
		project = []utiles.UnusedVolumeItem{}
	}
	networks := stat.Networks
	if networks == nil {
		networks = []utiles.UnusedNetworkItem{}
	}
	sizeUnavailable := volSize == 0 && (len(stat.AnonymousVolumes)+len(stat.ProjectVolumes)) > 0
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = map[string]interface{}{
		"anonymousVolumes":     anonymous,
		"projectVolumes":       project,
		"networks":             networks,
		"volumesInUse":         stat.VolumesInUse,
		"netsInUse":            stat.NetsInUse,
		"unusedVolumeSize":     volSize,
		"unusedVolumeSizeText": utiles.FormatBytes(volSize),
		"sizeUnavailable":      sizeUnavailable,
		"untilHours":           stat.UntilHours,
	}
	return resp, nil
}

// Prune 执行清理：按请求里的三类开关逐项删除。
func (l *ResourcesLogic) Prune(req *types.PruneResourcesReq) (resp *types.Resp, err error) {
	resp = &types.Resp{}
	if !req.Volumes && !req.ProjectVolumes && !req.Networks {
		resp.Code = 400
		resp.Msg = "未选择要清理的资源类型"
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	removedVols, removedNets, failed, perr := utiles.PruneResources(l.svcCtx, req.Volumes, req.ProjectVolumes, req.Networks)
	if perr != nil {
		resp.Code = 500
		resp.Msg = utiles.FriendlyDaemonError(perr).Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	msg := fmt.Sprintf("已清理 %d 个卷、%d 个网络", len(removedVols), len(removedNets))
	if len(failed) > 0 {
		msg += fmt.Sprintf("；%d 项跳过/失败", len(failed))
	}
	resp.Code = 200
	resp.Msg = msg
	resp.Data = map[string]interface{}{
		"removedVolumes":  removedVols,
		"removedNetworks": removedNets,
		"failed":          failed,
	}
	return resp, nil
}
