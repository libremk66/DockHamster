package module

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	ref "github.com/distribution/reference"
	"github.com/docker/docker/api/types/registry"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/zeromicro/go-zero/core/logx"
	"io"
	"net"
	"net/http"
	url2 "net/url"
	"strings"
	"sync"
	"time"
)

// 无法检测的原因（前端据此给不同的提示，避免一律显示"悬空/本地构建"误导用户）
const (
	CheckReasonNoTag    = "no-tag"    // 没有可用标签（悬空/本地构建/从未从仓库拉取过）
	CheckReasonAuth     = "auth"      // 认证被拒且**没有**配该仓库凭据（多半是私有镜像，也可能仓库不存在）
	CheckReasonAuthBad  = "auth-bad"  // 配了凭据仍被拒（凭据无效或无该镜像权限）
	CheckReasonNotFound = "not-found" // 仓库/标签不存在
)

// ImageCheckList 检查更新处理后的镜像列表
type ImageCheckList struct {
	NeedUpdate bool
	// Uncheckable 表示这张镜像**根本无法检测**（无 tag 的悬空镜像、本地构建无远端来源等）——
	// 与"本次查询失败"（保留旧状态）不同，它会显式清掉"有更新"标记，避免徽标永久卡死。
	Uncheckable bool
	// Reason 无法检测的原因（CheckReason*；可检测时为空）
	Reason string
}
type ImageUpdateData struct {
	mu   sync.RWMutex
	Data map[string]ImageCheckList
}

const ContentDigestHeader = "Docker-Content-Digest"

func NewImageCheck() *ImageUpdateData {
	return &ImageUpdateData{
		Data: map[string]ImageCheckList{},
	}
}

// NeedUpdate 并发安全地查询某镜像（按镜像 ID）是否需要更新
func (i *ImageUpdateData) NeedUpdate(imageID string) bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	v, ok := i.Data[imageID]
	return ok && v.NeedUpdate
}

// Uncheckable 并发安全地查询某镜像是否"无法检测"（无 tag / 无远端来源）
func (i *ImageUpdateData) Uncheckable(imageID string) bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	v, ok := i.Data[imageID]
	return ok && v.Uncheckable
}

// UncheckableReason 并发安全地取"无法检测"的原因（可检测时返回空串）
func (i *ImageUpdateData) UncheckableReason(imageID string) string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	v, ok := i.Data[imageID]
	if !ok || !v.Uncheckable {
		return ""
	}
	return v.Reason
}

// setAll 并发安全地整体替换数据
func (i *ImageUpdateData) setAll(data map[string]ImageCheckList) {
	i.mu.Lock()
	i.Data = data
	i.mu.Unlock()
}

// DockerInspector 走 Docker 守护进程的通道查询 registry。
// 为什么优先用它：守护进程运行在宿主机上，用的是**用户为拉镜像配置的那条通道**
// （代理 / registry-mirrors），与"点更新时实际拉取"完全一致；
// 而面板容器自己发 HTTP 在国内常常连不上官方 registry，只能退到公共加速站（缓存旧、易限流）。
type DockerInspector interface {
	DistributionInspect(ctx context.Context, image, encodedRegistryAuth string) (registry.DistributionInspect, error)
}

// CheckUpdateDeps 检测更新时用到的外部依赖（nil 时按匿名/无回退处理）。
type CheckUpdateDeps struct {
	// Auth 私有仓库凭据：有凭据时给 DistributionInspect / HTTP 回退带上认证
	Auth RegistryAuthLookup
	// RefFor 为"没有 RepoTag"的镜像提供可用引用（容器创建时的 Config.Image）。
	// 有了它，镜像丢失 tag 后仍能继续检测更新，而不是一律标"无法检测"。
	RefFor func(imageID string) string
}

func (i *ImageUpdateData) CheckUpdate(cli DockerInspector, imageList []types.Image, onProgress func(done, total, need int)) {
	i.CheckUpdateWithDeps(cli, imageList, CheckUpdateDeps{}, onProgress)
}

func (i *ImageUpdateData) CheckUpdateWithDeps(cli DockerInspector, imageList []types.Image, deps CheckUpdateDeps, onProgress func(done, total, need int)) {
	// 保留本地仍存在的镜像的旧状态（单次查询失败不至于丢状态），
	// 同时清理已不存在镜像的过期条目（避免"阴魂不散"的更新提示）。
	i.mu.RLock()
	exists := make(map[string]bool, len(imageList))
	next := make(map[string]ImageCheckList, len(imageList))
	for _, image := range imageList {
		exists[image.ID] = true
	}
	for id, v := range i.Data {
		if exists[id] {
			next[id] = v
		}
	}
	i.mu.RUnlock()

	// 并发检查（限流 6）：经代理解析 registry 单次约 1~2 秒，
	// 串行检查 50+ 镜像要 1~2 分钟（UI 的「检查更新」按钮会等到超时），并发后约 10~20 秒。
	workers := max(len(imageList)/4, 6)
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for _, image := range imageList {
		if !strings.Contains(image.ImageName, "libremk66/dockhamster") {
			total++
		}
	}
	done, needCnt := 0, 0
	for _, image := range imageList {
		if strings.Contains(image.ImageName, "libremk66/dockhamster") {
			continue
		}
		wg.Add(1)
		go func(img types.Image) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			result := i.checkSingleImage(cli, img, deps)
			mu.Lock()
			if result != nil {
				next[img.ID] = *result
				if result.NeedUpdate {
					needCnt++
				}
			}
			done++
			if onProgress != nil {
				onProgress(done, total, needCnt)
			}
			mu.Unlock()
		}(image)
	}
	wg.Wait()
	i.setAll(next)
}

// checkSingleImage 返回 nil 表示本次检查**暂时**失败（保留旧状态，不作判断）；
// 返回 Uncheckable 表示这张镜像根本没法查（显式清掉更新标记，并带原因）。
func (i *ImageUpdateData) checkSingleImage(cli DockerInspector, image types.Image, deps CheckUpdateDeps) *ImageCheckList {
	repo, tag := image.ImageName, image.ImageTag
	// ① 无有效标签：悬空镜像（tag 被移走了）或本地构建。
	//    这种镜像没有"名字"可查（此前会拿假标签 "None" 去问 registry，必然失败 →
	//    又被"保留旧状态"策略兜住 → "有新版"徽标永久卡死，issue #4 的场景）。
	//    若容器创建时的引用还能拿到（deps.RefFor），就用它继续查——镜像丢了 tag
	//    不代表它没有远端来源。
	if len(image.RepoTags) == 0 || tag == "" || tag == "None" {
		refStr := ""
		if deps.RefFor != nil {
			refStr = strings.TrimSpace(deps.RefFor(image.ID))
		}
		named, perr := ref.ParseDockerRef(refStr)
		if refStr == "" || perr != nil {
			logx.Infof("镜像无有效标签（悬空/本地构建），标记为无法检测：%s", image.ImageName)
			return &ImageCheckList{NeedUpdate: false, Uncheckable: true, Reason: CheckReasonNoTag}
		}
		repo = named.Name()
		if tagged, ok := named.(ref.NamedTagged); ok {
			tag = tagged.Tag()
		} else {
			return &ImageCheckList{NeedUpdate: false, Uncheckable: true, Reason: CheckReasonNoTag}
		}
		logx.Infof("镜像无 tag，改用容器创建时的引用继续检测：%s:%s", repo, tag)
	}
	// ② 有 tag 但**没有任何 RepoDigest**：从没从仓库拉取过（本地构建/导入的镜像），
	//    本地没有可比较的基线，查远端也判不出"是否有新版"。
	//    这类直接标 no-tag（提示本地构建），别去查 registry——否则本地构建的
	//    同名镜像会被 registry 的 401 误判成"私有镜像缺凭据"。
	if len(image.RepoDigests) == 0 {
		logx.Infof("镜像无 RepoDigest（本地构建/导入），标记为无法检测：%s:%s", repo, tag)
		return &ImageCheckList{NeedUpdate: false, Uncheckable: true, Reason: CheckReasonNoTag}
	}
	refStr := repo + ":" + tag
	remoteDigest, source, err := i.resolveRemoteDigest(cli, repo, tag, deps.Auth)
	if err != nil || remoteDigest == "" {
		logx.Infof("获取远端 digest 失败（%s）: %v", source, err)
		// ② 区分"暂时失败"与"永久不可查"：仓库不存在/无权限这类错误不会再变好，
		//    显式标记"无法检测"清掉旧标记（带原因，前端给对应提示）；
		//    网络抖动等暂态错误仍保留旧状态。
		if err != nil {
			if reason := checkErrorReason(err); reason != "" {
				// 认证类再细分：已经给该 registry 配了凭据还被拒 = 凭据无效/无权限；
				// 没配 = 可能是私有镜像，也可能仓库本就不存在（匿名访问对两者都回 401/denied）。
				if reason == CheckReasonAuth && deps.Auth != nil && deps.Auth.HasAuth(refStr) {
					reason = CheckReasonAuthBad
				}
				logx.Infof("远端仓库不可达（非暂态，%s），标记为无法检测：%s", reason, refStr)
				return &ImageCheckList{NeedUpdate: false, Uncheckable: true, Reason: reason}
			}
		}
		return nil
	}
	logx.Infof("远端 digest 来源: %s（%s）", source, refStr)
	// 只与**同一仓库**的本地 digest 比较；任一匹配即视为已是最新。
	// （修复旧逻辑：循环内反复赋值，结果被 RepoDigests 末位元素覆盖导致的误报。）
	needUpdate := true
	compared := false
	for _, localRepoDigest := range image.RepoDigests {
		parts := strings.SplitN(localRepoDigest, "@", 2)
		if len(parts) != 2 || parts[1] == "" {
			continue
		}
		if normalizeRepoName(parts[0]) != normalizeRepoName(repo) {
			continue
		}
		compared = true
		if parts[1] == remoteDigest {
			needUpdate = false
			break
		}
	}
	if !compared {
		logx.Error("未找到同仓库的本地 digest，跳过检查 " + refStr)
		return nil
	}
	if needUpdate {
		logx.Info(refStr + " need update")
		logx.Infof("remoteDigest: %s", remoteDigest)
	} else {
		logx.Info(image.ImageName + ":" + image.ImageTag + " not need update")
	}
	return &ImageCheckList{NeedUpdate: needUpdate}
}

// checkErrorReason 判断"查不到"是否是永久性的（仓库/标签不存在、无权限），
// 而不是网络抖动；永久性的返回原因（CheckReason*），暂态返回空串。
// 永久性的不再保留旧状态，避免徽标卡死。
func checkErrorReason(err error) string {
	msg := strings.ToLower(err.Error())
	// 认证类优先判定：私有仓库未授权时，部分 registry 也会回 "not found"，
	// 先匹配认证关键词才能给"去配凭据"的正确提示。
	for _, kw := range []string{"unauthorized", "denied", "authentication required", "no basic auth", "no credentials"} {
		if strings.Contains(msg, kw) {
			return CheckReasonAuth
		}
	}
	for _, kw := range []string{
		"not found", "manifest unknown", "does not exist", "name unknown",
		"invalid reference", "no such image", "no such manifest",
	} {
		if strings.Contains(msg, kw) {
			return CheckReasonNotFound
		}
	}
	return ""
}

// isPermanentCheckError 兼容旧调用：错误是否永久（有原因即永久）
func isPermanentCheckError(err error) bool {
	return checkErrorReason(err) != ""
}

// resolveRemoteDigest 取远端 digest：**优先守护进程通道**，失败回退自建 HTTP（兼容加速站）。
// auth 提供私有仓库凭据（nil = 匿名）。
func (i *ImageUpdateData) resolveRemoteDigest(cli DockerInspector, repo, tag string, auth RegistryAuthLookup) (digest string, source string, err error) {
	refStr := repo
	if tag != "" {
		refStr = refStr + ":" + tag
	}
	// ① 守护进程通道（与拉取同一条路，拿到的就是实时 digest）
	if cli != nil {
		daemonAuth := ""
		if auth != nil {
			daemonAuth = auth.DaemonAuth(refStr)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		dist, derr := cli.DistributionInspect(ctx, refStr, daemonAuth)
		if derr == nil && dist.Descriptor.Digest != "" {
			return dist.Descriptor.Digest.String(), "daemon", nil
		}
		if derr != nil && isPermanentCheckError(derr) {
			return "", "daemon", derr
		}
		logx.Infof("守护进程通道查询 %s 失败，回退自建 HTTP：%v", refStr, derr)
	}
	// ② 回退：自己发 HTTP（token + HEAD；国内会被解析到加速站）
	eff := types.Image{ImageName: repo, ImageTag: tag}
	basicAuth := ""
	if auth != nil {
		basicAuth = auth.BasicAuth(refStr)
	}
	token, terr := GetToken(eff, basicAuth)
	if terr != nil {
		logx.Error("获取token失败或者无需获取token，继续尝试检查" + terr.Error())
	}
	digestURL, uerr := BuildManifestURL(eff)
	if uerr != nil {
		logx.Error("获取digestURL失败" + uerr.Error())
		return "", "http", uerr
	}
	d, gerr := GetDigest(digestURL, token)
	if gerr != nil {
		logx.Error("获取digest失败" + gerr.Error())
		return "", "http", gerr
	}
	return d, "http", nil
}

// normalizeRepoName 抹平仓库名常见写法差异（大小写 / docker.io / library 前缀）
func normalizeRepoName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimPrefix(name, "docker.io/")
	name = strings.TrimPrefix(name, "index.docker.io/")
	name = strings.TrimPrefix(name, "library/")
	return name
}

func BuildManifestURL(image types.Image) (string, error) {
	normalizedRef, err := ref.ParseDockerRef(image.ImageName + ":" + image.ImageTag)
	if err != nil {
		return "", err
	}
	normalizedTaggedRef, isTagged := normalizedRef.(ref.NamedTagged)
	if !isTagged {
		return "", errors.New("镜像无tag" + normalizedRef.String())
	}

	host, ErrGetRegistryAddress := GetRegistryAddress(normalizedTaggedRef.Name())
	img, tag := ref.Path(normalizedTaggedRef), normalizedTaggedRef.Tag()

	if ErrGetRegistryAddress != nil {
		return "", ErrGetRegistryAddress
	}

	url := url2.URL{
		Scheme: "https",
		Host:   host,
		Path:   fmt.Sprintf("/v2/%s/manifests/%s", img, tag),
	}
	return url.String(), nil
}

func GetDigest(url string, token string) (string, error) {
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr}

	req, _ := http.NewRequest("HEAD", url, nil)

	if token != "" {
		req.Header.Add("Authorization", token)
	}
	req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.list.v2+json")
	req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.v1+json")
	req.Header.Add("Accept", "application/vnd.oci.image.index.v1+json")

	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			logx.Error("GetDigest关闭body失败" + err.Error())
		}
	}(res.Body)

	if res.StatusCode != 200 {
		wwwAuthHeader := res.Header.Get("www-authenticate")
		if wwwAuthHeader == "" {
			wwwAuthHeader = "not present"
		}
		return "", fmt.Errorf("registry responded to head request with %q, auth: %q", res.Status, wwwAuthHeader)
	}
	return res.Header.Get(ContentDigestHeader), nil
}
