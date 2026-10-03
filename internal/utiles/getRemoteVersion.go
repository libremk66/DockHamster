package utiles

import (
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/libremk66/DockHamster/internal/config"
	"github.com/zeromicro/go-zero/core/logx"
)

// 远端版本查询的缓存：网络抖动/GitHub 不可达时不至于让界面丢信息，也避免每次都打网络
var (
	remoteVersionCache   string
	remoteVersionCacheAt time.Time
	remoteVersionMu      sync.Mutex
)

const remoteVersionCacheTTL = 10 * time.Minute

// 版本检测使用多个源，按"直连优先 + 自动兜底"顺序尝试：
// 海外/可翻墙环境走 GitHub；国内直连不通时自动退到 jsDelivr CDN 或公共镜像。
// 不需要用户做任何配置。
var versionSources = []string{
	"https://raw.githubusercontent.com/libremk66/DockHamster/main/version",
	"https://cdn.jsdelivr.net/gh/libremk66/DockHamster@main/version",
	"https://ghfast.top/https://raw.githubusercontent.com/libremk66/DockHamster/main/version",
}

var preferredSource string

// orderedVersionSources 上次成功的源优先，其余按默认顺序；githubProxy 若配置则作为最优先前缀源
func orderedVersionSources(githubProxy string) []string {
	var out []string
	if githubProxy != "" {
		out = append(out, githubProxy+"https://raw.githubusercontent.com/libremk66/DockHamster/main/version")
	}
	remoteVersionMu.Lock()
	pref := preferredSource
	remoteVersionMu.Unlock()
	if pref != "" {
		out = append(out, pref)
	}
	for _, src := range versionSources {
		if src != pref {
			out = append(out, src)
		}
	}
	return out
}

func markSourceOK(src string) {
	remoteVersionMu.Lock()
	preferredSource = src
	remoteVersionMu.Unlock()
}

func GetRemoteVersion() (remoteVersion string, err error) {
	remoteVersionMu.Lock()
	if remoteVersionCache != "" && time.Since(remoteVersionCacheAt) < remoteVersionCacheTTL {
		cached := remoteVersionCache
		remoteVersionMu.Unlock()
		return cached, nil
	}
	remoteVersionMu.Unlock()

	githubProxy := os.Getenv("githubProxy")
	if githubProxy != "" {
		githubProxy = strings.TrimRight(githubProxy, "/") + "/"
	}

	var lastErr error
	for _, src := range orderedVersionSources(githubProxy) {
		v, ferr := fetchVersionFromURL(src)
		if ferr == nil && strings.TrimSpace(v) != "" {
			remoteVersion = v
			markSourceOK(src)
			err = nil
			goto fetched
		}
		lastErr = ferr
		logx.Infof("版本源不可用 %s: %v", src, ferr)
	}
	err = lastErr
fetched:
	if err != nil {
		// 失败时若有旧缓存，宁可返回旧值（附错误日志），也不让前端拿不到
		remoteVersionMu.Lock()
		cached := remoteVersionCache
		remoteVersionMu.Unlock()
		if cached != "" {
			logx.Infof("远端版本查询失败，返回缓存值 %s: %v", cached, err)
			return cached, nil
		}
		return "0.0.0", err
	}
	remoteVersionMu.Lock()
	remoteVersionCache = remoteVersion
	remoteVersionCacheAt = time.Now()
	remoteVersionMu.Unlock()

	localVersion := config.Version
	if strings.Contains(localVersion, "FNOS") {
		logx.Infof("飞牛版本，无需在线更新")
		return localVersion, nil
	}
	if localVersion == remoteVersion {
		logx.Info("版本一致:", localVersion)
		return remoteVersion, nil
	} else {
		logx.Infof("版本不一致! 本地: %s, 远程: %s\n", localVersion, remoteVersion)
		return remoteVersion, nil
	}

}

func fetchVersionFromURL(url string) (string, error) {
	// 超时保护：GitHub 不可达时不要让 API 一直挂着
	client := &http.Client{
		Timeout: 6 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
		},
	}

	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			logx.Error("关闭Body失败:", err)
		}
	}(resp.Body)

	versionData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(versionData)), nil
}
