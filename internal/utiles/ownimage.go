package utiles

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/image"
	"github.com/libremk66/DockHamster/internal/module"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/zeromicro/go-zero/core/logx"
)

// ── 本项目镜像的"digest 双保险"检测 ──────────────────────────────────────
// 版本号文件靠人记得改；镜像 digest 由 CI 每次自动产生。
// 万一忘了 bump version，这里的 digest 比对仍能让用户看到"有新版本"。

const ownImageRepo = "libremk66/dockhamster"

var (
	ownImageCheckedAt time.Time
	ownImageResult    bool
	ownImageDigest    string
	ownImageMu        sync.Mutex
)

const ownImageCacheTTL = 10 * time.Minute

// OwnImageHasUpdate 检查 libremk66/dockhamster:latest 是否有更新（远端 digest ≠ 本地 digest）
func OwnImageHasUpdate(serviceContext *svc.ServiceContext) (bool, string) {
	ownImageMu.Lock()
	if !ownImageCheckedAt.IsZero() && time.Since(ownImageCheckedAt) < ownImageCacheTTL {
		r, d := ownImageResult, ownImageDigest
		ownImageMu.Unlock()
		return r, d
	}
	ownImageMu.Unlock()

	needUpdate, remoteDigest, ok := checkOwnImage(serviceContext)
	ownImageMu.Lock()
	if ok {
		ownImageResult = needUpdate
		ownImageDigest = remoteDigest
		ownImageCheckedAt = time.Now()
	}
	ownImageMu.Unlock()
	return needUpdate, remoteDigest
}

func checkOwnImage(serviceContext *svc.ServiceContext) (needUpdate bool, remoteDigest string, ok bool) {
	ctx := context.Background()
	serviceContext.DockerClient.NegotiateAPIVersion(ctx)

	// 本地镜像（找不到说明是自定义构建/别名，跳过检查）
	list, err := serviceContext.DockerClient.ImageList(ctx, image.ListOptions{All: false})
	if err != nil {
		logx.Infof("镜像更新双保险：列镜像失败: %v", err)
		return false, "", false
	}
	var localDigests []string
	for _, img := range list {
		for _, t := range img.RepoTags {
			if strings.HasPrefix(t, ownImageRepo+":") {
				localDigests = append(localDigests, img.RepoDigests...)
			}
		}
	}
	if len(localDigests) == 0 {
		return false, "", false // 没用官方镜像（或尚未拉取过 digest）→ 不判断
	}

	// 远端 digest：优先走守护进程通道（与拉取同一条路，国内可用），失败回退自建 HTTP
	img := types.Image{ImageName: ownImageRepo, ImageTag: "latest"}
	// 注意：remoteDigest 是具名返回值，这里不能再 :=
	dist, derr := serviceContext.DockerClient.DistributionInspect(ctx, ownImageRepo+":latest", "")
	if derr == nil && dist.Descriptor.Digest != "" {
		remoteDigest = dist.Descriptor.Digest.String()
		logx.Infof("镜像更新双保险：经守护进程取到远端 digest %s", remoteDigest)
	} else {
		logx.Infof("镜像更新双保险：守护进程通道失败(%v)，回退 HTTP", derr)
		token, terr := module.GetToken(img, "")
		if terr != nil {
			logx.Infof("镜像更新双保险：取 token 失败(继续): %v", terr)
		}
		digestURL, uerr := module.BuildManifestURL(img)
		if uerr != nil {
			logx.Infof("镜像更新双保险：构造 digest 地址失败: %v", uerr)
			return false, "", false
		}
		d2, derr2 := module.GetDigest(digestURL, token)
		if derr2 != nil || d2 == "" {
			logx.Infof("镜像更新双保险：取远端 digest 失败: %v", derr2)
			return false, "", false
		}
		remoteDigest = d2
	}
	for _, ld := range localDigests {
		if strings.Contains(ld, remoteDigest) {
			return false, remoteDigest, true
		}
	}
	return true, remoteDigest, true
}
