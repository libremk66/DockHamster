package version

import (
	"context"

	"github.com/libremk66/DockHamster/internal/config"

	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/libremk66/DockHamster/internal/utiles"
	"github.com/zeromicro/go-zero/core/logx"
)

type VersionLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewVersionLogic(ctx context.Context, svcCtx *svc.ServiceContext) *VersionLogic {
	return &VersionLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *VersionLogic) Version(req *types.VersionReq) (resp *types.Resp, err error) {
	resp = &types.Resp{}
	if req.Type == "local" {
		resp.Code = 200
		resp.Msg = "success"
		resp.Data = map[string]string{
			"version":   config.Version,
			"buildDate": config.BuildDate,
		}
		return resp, nil
	} else if req.Type == "remote" {
		remoteVersion, err := utiles.GetRemoteVersion()
		if err != nil {
			resp.Code = 50001
			resp.Msg = "获取版本错误" + err.Error()
			resp.Data = map[string]string{
				"remoteVersion": config.Version,
			}
			return resp, nil
		}
		// 双保险：除版本号外，再看一眼"本项目镜像在仓库里的 digest 是否变了"
		// （防止某次提交忘了 bump version 文件 → 镜像更新了但用户收不到提示）
		imageUpdate, imageRemoteDigest := utiles.OwnImageHasUpdate(l.svcCtx)
		if remoteVersion != config.Version {
			resp.Code = 200
			resp.Msg = "程序有更新"
			resp.Data = map[string]interface{}{
				"remoteVersion":  remoteVersion,
				"imageUpdate":    imageUpdate,
				"remoteImageTag": "latest",
				"remoteDigest":   imageRemoteDigest,
			}
			return resp, nil
		}
		if imageUpdate {
			resp.Code = 200
			resp.Msg = "镜像有更新"
			resp.Data = map[string]interface{}{
				"remoteVersion": remoteVersion,
				"imageUpdate":   true,
				"remoteDigest":  imageRemoteDigest,
			}
			return resp, nil
		}
		resp.Code = 200
		resp.Msg = "程序无更新"
		resp.Data = map[string]interface{}{
			"remoteVersion": remoteVersion,
			"imageUpdate":   false,
		}
		return resp, nil
	} else {
		resp.Code = 400
		resp.Msg = "type 参数错误"
		resp.Data = map[string]string{}
		return resp, nil
	}
}
