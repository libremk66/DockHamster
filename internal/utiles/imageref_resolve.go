package utiles

// 容器"可用镜像引用"的解析。
//
// 背景：moby 的 ContainerList 在"容器记录的引用已不指向当前镜像"时（例如同名 tag 被
// 重新 pull、旧镜像失去 RepoTags），会把 summary.Image 退化成镜像 ID
// （见 moby daemon/list.go 的 refreshImage）。面板若把这个 ID 当镜像名用，更新对话框
// 会把它原样回填 → 被 ValidateUpdateImageRef 拒绝 → 容器永远更新不了。
//
// 容器创建时的 Config.Image（ContainerInspect）保留了用户当时写的引用，是更可靠的来源。
// 本文件把"选哪个引用"的判定集中起来，列表展示、自动更新、更新检测三处共用。

import (
	"context"
	"strings"

	"github.com/libremk66/DockHamster/internal/svc"
	MyType "github.com/libremk66/DockHamster/internal/types"
)

// IsBareImageID 是否为裸镜像 ID（sha256:<hex> 或 12~64 位十六进制短 ID）。
func IsBareImageID(ref string) bool {
	return bareImageIDRe.MatchString(strings.ToLower(strings.TrimSpace(ref)))
}

// PullableImageRef 选出可拉取的镜像引用：
// 优先容器列表里的 Image（正常情况就是用户写的引用）；它是裸 ID/空时回退到
// 容器创建时的 Config.Image。返回值经 ValidateUpdateImageRef 规范化（补 :latest），
// 可直接用于拉取/查询；两者都不可用（或本身就是 ID）时返回空串。
func PullableImageRef(listImage, configImage string) string {
	for _, cand := range []string{listImage, configImage} {
		c := strings.TrimSpace(cand)
		if c == "" || IsBareImageID(c) {
			continue
		}
		if normalized, err := ValidateUpdateImageRef(c); err == nil {
			return normalized
		}
	}
	return ""
}

// ImageRefsForUntaggedImages 为"没有 RepoTags"的镜像找出可用引用（imageID → ref）。
//
// 这种镜像在镜像列表里没有名字可查，但容器创建时的引用可能仍然有效——
// 用它就能继续检测更新/自动更新，而不是一律标"无法检测"。
// 只有确实存在无标签镜像时才列容器，且只 inspect 命中的那几台（少见路径，
// 不给列表页增加 N 次 inspect）。
func ImageRefsForUntaggedImages(svcCtx *svc.ServiceContext, images []MyType.Image) map[string]string {
	untagged := map[string]bool{}
	for _, img := range images {
		if len(img.RepoTags) == 0 && img.ID != "" {
			untagged[img.ID] = true
		}
	}
	if len(untagged) == 0 {
		return nil
	}
	containers, err := GetContainerList(svcCtx)
	if err != nil {
		return nil
	}
	refs := map[string]string{}
	for _, c := range containers {
		if !untagged[c.ImageID] {
			continue
		}
		if _, done := refs[c.ImageID]; done {
			continue
		}
		ref := PullableImageRef(c.Image, "")
		if ref == "" {
			// 列表里的 Image 已是裸 ID：inspect 一次拿创建时的引用
			ins, ierr := svcCtx.DockerClient.ContainerInspect(context.Background(), c.ID)
			if ierr == nil && ins.Config != nil {
				ref = PullableImageRef("", ins.Config.Image)
			}
		}
		if ref != "" {
			refs[c.ImageID] = ref
		}
	}
	return refs
}
