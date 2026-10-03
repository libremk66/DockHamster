package snapshot

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/libremk66/DockHamster/internal/utiles"
	"github.com/zeromicro/go-zero/core/logx"
)

type SnapshotLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewSnapshotLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SnapshotLogic {
	return &SnapshotLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *SnapshotLogic) options() utiles.SnapshotOptions {
	return utiles.SnapshotOptionsFromSettings(l.svcCtx.AutoUpdate.Get())
}

// List 快照列表 + 占用统计（一次请求给前端全部信息）
func (l *SnapshotLogic) List() (resp *types.Resp, err error) {
	resp = &types.Resp{}
	opts := l.options()
	list, err := utiles.ListSnapshots(l.svcCtx, opts.Prefix)
	if err != nil {
		resp.Code = 500
		resp.Msg = "获取快照列表失败: " + err.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	count, sizeBytes, _ := utiles.SnapshotStats(l.svcCtx, opts.Prefix)
	diskFree, _ := utiles.DiskFree()
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = map[string]interface{}{
		"snapshots":   list,
		"count":       count,
		"sizeBytes":   sizeBytes,
		"sizeLabel":   utiles.FormatBytes(sizeBytes),
		"diskFree":    diskFree,
		"diskFreeLbl": utiles.FormatBytes(diskFree),
		"prefix":      opts.Prefix,
		"keep":        opts.Keep,
	}
	return resp, nil
}

// Create 手动给容器当前镜像打一个快照
func (l *SnapshotLogic) Create(req *types.SnapshotCreateReq) (resp *types.Resp, err error) {
	resp = &types.Resp{}
	opts := l.options()
	imageID := req.ImageID
	containerName := req.ContainerName
	if imageID == "" {
		if containerName == "" {
			resp.Code = 400
			resp.Msg = "需要指定容器名或镜像 ID"
			resp.Data = map[string]interface{}{}
			return resp, nil
		}
		l.svcCtx.DockerClient.NegotiateAPIVersion(l.ctx)
		inspected, ierr := l.svcCtx.DockerClient.ContainerInspect(l.ctx, containerName)
		if ierr != nil {
			resp.Code = 404
			resp.Msg = "容器不存在: " + ierr.Error()
			resp.Data = map[string]interface{}{}
			return resp, nil
		}
		imageID = inspected.Image
	}
	ref, cerr := utiles.CreateSnapshot(l.svcCtx, imageID, containerName, opts)
	if cerr != nil {
		resp.Code = 500
		resp.Msg = "打快照失败: " + cerr.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = map[string]interface{}{"ref": ref}
	return resp, nil
}

// Rollback 用选中的快照重建容器（回滚前先给当前镜像也打一份快照，双向可回滚）
func (l *SnapshotLogic) Rollback(req *types.SnapshotRollbackReq) (resp *types.Resp, err error) {
	resp = &types.Resp{}
	if strings.TrimSpace(req.ContainerName) == "" || strings.TrimSpace(req.Ref) == "" {
		resp.Code = 400
		resp.Msg = "缺少容器名或快照引用"
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	opts := l.options()
	if !utiles.IsSnapshotRef(req.Ref, opts.Prefix) {
		resp.Code = 400
		resp.Msg = "只能回滚到快照（" + opts.Prefix + "/ 命名空间）"
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	l.svcCtx.DockerClient.NegotiateAPIVersion(l.ctx)
	inspected, ierr := l.svcCtx.DockerClient.ContainerInspect(l.ctx, req.ContainerName)
	if ierr != nil {
		resp.Code = 404
		resp.Msg = "容器不存在: " + ierr.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	// 快照镜像必须存在
	targetInspect, _, terr := l.svcCtx.DockerClient.ImageInspectWithRaw(l.ctx, req.Ref)
	if terr != nil {
		resp.Code = 404
		resp.Msg = "快照镜像不存在: " + terr.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}

	taskID := uuid.New().String()
	originalRef := inspected.Config.Image // 容器原本引用的镜像名（如 songhamster:latest）
	currentImageID := inspected.Image

	go func() {
		defer func() {
			if r := recover(); r != nil {
				l.Errorf("回滚过程 panic: %v", r)
			}
		}()
		// ① 回滚前给"当前版本"打一份快照 → 回滚可逆
		if _, serr := utiles.CreateSnapshot(l.svcCtx, currentImageID, req.ContainerName+"-before-rollback", opts); serr != nil {
			l.Errorf("回滚前快照失败(继续回滚): %v", serr)
		}
		// ② 把容器原本的镜像引用指回快照镜像（保持容器镜像名不变，后续更新不受影响）
		// 必须用独立 context：HTTP 响应返回后请求 ctx 被取消，用 l.ctx 会直接失败
		if terr := l.svcCtx.DockerClient.ImageTag(context.Background(), targetInspect.ID, originalRef); terr != nil {
			l.Errorf("回滚失败：重新标记 %s 出错: %v", originalRef, terr)
			l.svcCtx.UpdateProgress(taskID, svc.TaskProgress{
				TaskID: taskID, Name: req.ContainerName, Percentage: 100,
				Message: "回滚失败", DetailMsg: "重新标记镜像失败: " + terr.Error(), IsDone: true,
			})
			return
		}
		// ③ 走标准更新流程（跳过拉取，用本地已回指的镜像重建容器）
		uopts := utiles.UpdateOptions{
			SkipPull:        true,
			DelOldContainer: true,
			OldImagePolicy:  "keep", // 当前版本已打回滚前快照，这里显式不处理
			SnapshotOptions: opts,
		}
		if err := utiles.UpdateContainer(l.svcCtx, inspected.ID, req.ContainerName, originalRef, uopts, taskID); err != nil {
			l.Errorf("回滚重建容器失败: %v", err)
		}
	}()

	resp.Code = 200
	resp.Msg = "success"
	resp.Data = map[string]interface{}{"taskID": taskID, "ref": req.Ref}
	return resp, nil
}

// Prune 按保留数量清理超量快照
func (l *SnapshotLogic) Prune(req *types.SnapshotPruneReq) (resp *types.Resp, err error) {
	resp = &types.Resp{}
	opts := l.options()
	keep := req.Keep
	if keep <= 0 {
		keep = opts.Keep
	}
	removed, perr := utiles.PruneSnapshots(l.svcCtx, opts.Prefix, keep)
	if perr != nil {
		resp.Code = 500
		resp.Msg = "清理失败: " + perr.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	resp.Code = 200
	resp.Msg = fmt.Sprintf("已清理 %d 个旧快照", len(removed))
	resp.Data = map[string]interface{}{"removed": removed, "keep": keep}
	return resp, nil
}

// Delete 手动删除指定快照（被容器使用的会拒绝并说明）
func (l *SnapshotLogic) Delete(refs []string) (resp *types.Resp, err error) {
	resp = &types.Resp{}
	opts := l.options()
	if len(refs) == 0 {
		resp.Code = 400
		resp.Msg = "未指定要删除的快照"
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	removed, failed := utiles.RemoveSnapshots(l.svcCtx, refs, opts.Prefix)
	resp.Code = 200
	resp.Msg = fmt.Sprintf("已删除 %d 个，失败 %d 个", len(removed), len(failed))
	resp.Data = map[string]interface{}{"removed": removed, "failed": failed}
	return resp, nil
}
