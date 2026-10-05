package utiles

import (
	"fmt"
	"strings"
)

// FriendlyPullError 把"仓库里没有这个镜像"的原始报错翻译成人话。
// 常见于：本地 docker build 的镜像、私有/已删除的仓库、拼错的镜像名。
func FriendlyPullError(ref string, err error) error {
	if err == nil {
		return nil
	}
	low := strings.ToLower(err.Error())
	for _, sig := range []string{"pull access denied", "repository does not exist", "manifest unknown"} {
		if strings.Contains(low, sig) {
			return fmt.Errorf("镜像 %s 不在可访问的镜像仓库中（可能是本地构建、私有镜像或名字不存在），无法通过拉取更新", ref)
		}
	}
	return err
}

// hubRepoAndSuffix 解析镜像引用，返回 Docker Hub 仓库路径（官方镜像补 library/）与 tag/digest 后缀。
// 非 Docker Hub 镜像（ghcr.io、带域名/端口的私有仓库等）返回 ok=false。
func hubRepoAndSuffix(ref string) (repo, suffix string, ok bool) {
	name := strings.TrimSpace(ref)
	if name == "" {
		return "", "", false
	}
	if i := strings.Index(name, "@"); i != -1 {
		suffix = name[i:]
		name = name[:i]
	}
	lastColon := strings.LastIndex(name, ":")
	lastSlash := strings.LastIndex(name, "/")
	if lastColon > lastSlash { // 冒号在最后一个斜杠之后 = tag
		suffix = name[lastColon:] + suffix
		name = name[:lastColon]
	}
	parts := strings.Split(name, "/")
	switch {
	case len(parts) == 1:
		return "library/" + name, suffix, true
	case parts[0] == "docker.io" || parts[0] == "registry-1.docker.io" || parts[0] == "index.docker.io":
		rest := strings.Join(parts[1:], "/")
		if !strings.Contains(rest, "/") {
			rest = "library/" + rest
		}
		return rest, suffix, true
	case strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost":
		return "", "", false
	default:
		return name, suffix, true
	}
}

// MirrorRefFor 把 Docker Hub 镜像引用转换为走加速源的引用：
// nginx:latest → mirror/library/nginx:latest；user/app:1 → mirror/user/app:1
func MirrorRefFor(source, ref string) (string, error) {
	repo, suffix, ok := hubRepoAndSuffix(ref)
	if !ok {
		return "", fmt.Errorf("仅 Docker Hub 镜像支持走加速源（%s 来自其它仓库，请直连）", ref)
	}
	if strings.TrimSpace(source) == "" {
		return "", fmt.Errorf("加速源为空")
	}
	return strings.Trim(strings.TrimSpace(source), "/") + "/" + repo + suffix, nil
}
