package utiles

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/image"
	dockerMsgType "github.com/docker/docker/pkg/jsonmessage"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
)

// pullTimeout 单次拉取（含读进度流）的上限。TLS 握手/流中途挂住时必须有 deadline，
// 否则 defer 的失败终态永远执行不到，AutoUpdateState 会一直被占用。
const pullTimeout = 30 * time.Minute

// pullMessageSucceeded 判断 Docker 拉取流里的成功确认。
// EOF 本身不代表成功——连接中途断开也可能 EOF；必须有 aux 或最终状态。
func pullMessageSucceeded(msg dockerMsgType.JSONMessage) bool {
	if msg.Aux != nil && len(*msg.Aux) > 0 {
		return true
	}
	s := msg.Status
	return strings.Contains(s, "Downloaded newer image") || strings.Contains(s, "Image is up to date")
}

// setTaskMessage 覆写任务进度消息（taskIDs 为空/全空时跳过）。
// 组内多台共用一次拉取时传多个 taskID：每台的行都能看到同一进度。
func setTaskMessage(svcCtx *svc.ServiceContext, taskIDs []string, msg string) {
	for _, taskID := range taskIDs {
		if taskID == "" {
			continue
		}
		p, ok := svcCtx.GetProgress(taskID)
		if !ok {
			p = svc.TaskProgress{TaskID: taskID, Percentage: 0}
		}
		p.TaskID = taskID
		p.Message = msg
		p.DetailMsg = msg
		p.IsDone = false
		p.Failed = false
		svcCtx.UpdateProgress(taskID, p)
	}
}

// setTasksFailed 把一组任务落为"失败完成"终态。
// 「任务」页的进行中列表只按 IsDone 过滤，任何提前退出的路径都必须落终态，
// 否则排队中的行会永远停在「等待中，等待前序任务完成」。
// 已经处于失败态的任务保留原有（更具体的）错误信息，不覆盖。
func setTasksFailed(svcCtx *svc.ServiceContext, taskIDs []string, msg, detail string) {
	for _, taskID := range taskIDs {
		if taskID == "" {
			continue
		}
		p, ok := svcCtx.GetProgress(taskID)
		if !ok {
			p = svc.TaskProgress{TaskID: taskID}
		}
		if p.IsDone && p.Failed {
			continue
		}
		p.TaskID = taskID
		if msg != "" {
			p.Message = msg
		}
		p.DetailMsg = detail
		p.IsDone = true
		p.Failed = true
		svcCtx.UpdateProgress(taskID, p)
	}
}

// consumePullStream 消费拉取进度流但不写进度（无 taskID 的场景）
func consumePullStream(reader io.ReadCloser) error {
	defer func() { _ = reader.Close() }()
	decoder := json.NewDecoder(reader)
	sawSuccess := false
	for {
		var msg dockerMsgType.JSONMessage
		if err := decoder.Decode(&msg); err != nil {
			if err == io.EOF {
				if !sawSuccess {
					return fmt.Errorf("拉取流提前结束（未收到成功确认）")
				}
				return nil
			}
			return err
		}
		if msg.Error != nil {
			return msg.Error
		}
		if pullMessageSucceeded(msg) {
			sawSuccess = true
		}
	}
}

// PullImageViaMirror 通过加速源拉取 Docker Hub 镜像，成功后回打原始镜像名，
// 并移除加速源临时标签（避免镜像列表出现重复的一行）。
func PullImageViaMirror(svcCtx *svc.ServiceContext, taskID, source, imageRef string) error {
	var ids []string
	if taskID != "" {
		ids = []string{taskID}
	}
	return pullImageViaMirrorFor(svcCtx, ids, source, imageRef)
}

// pullImageViaMirrorFor 多任务版：进度同步写给 taskIDs 里的每个任务
func pullImageViaMirrorFor(svcCtx *svc.ServiceContext, taskIDs []string, source, imageRef string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
	defer cancel()
	mirrorRef, err := MirrorRefFor(source, imageRef)
	if err != nil {
		return err
	}
	setTaskMessage(svcCtx, taskIDs, "正在通过加速源 "+source+" 拉取镜像")
	reader, err := svcCtx.DockerClient.ImagePull(ctx, mirrorRef, image.PullOptions{})
	if err != nil {
		return err
	}
	if len(taskIDs) > 0 {
		err = decodePullResp(reader, svcCtx, taskIDs)
	} else {
		err = consumePullStream(reader)
	}
	if err != nil {
		return err
	}
	if mirrorRef == imageRef {
		return nil
	}
	if err := svcCtx.DockerClient.ImageTag(ctx, mirrorRef, imageRef); err != nil {
		return err
	}
	if _, err := svcCtx.DockerClient.ImageRemove(ctx, mirrorRef, image.RemoveOptions{}); err != nil {
		// 回打成功即可用；清不掉临时标签只记日志
		logx.Errorf("移除加速源临时标签失败 %s: %v", mirrorRef, err)
	}
	return nil
}

// pullRetryDelays 直连拉取遇到瞬时限流/网络错误时的退避序列。
// 变量而非常量，便于单测把等待压缩到毫秒级；默认 4 次尝试（首次 + 3 次重试）。
// 限流窗口通常按分钟计，退避要跨过整分钟；retry-after 若更大则以其为准。
var pullRetryDelays = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute}

// pullRetryMinWait 单次退避下限；测试里可调小。
var pullRetryMinWait = time.Second

// isRetriablePullError 判断错误是否值得重试。
// 只放瞬态错误：仓库限流、连接中断、握手超时；manifest unknown / 认证失败等永久错误不重试。
func isRetriablePullError(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	for _, kw := range []string{
		"toomanyrequests",
		"pull rate limit",
		"429 too many requests",
		"unexpected eof",
		"connection reset",
		"connection refused",
		"tls handshake timeout",
		"i/o timeout",
		"拉取流提前结束",
	} {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}

// retryAfterFromError 读取错误里的 retry-after（支持 µs/ms/s/纯数字秒）。
// 服务端给的值只作下限参考：GHCR 常回几百微秒，短到没有实际意义。
func retryAfterFromError(err error) time.Duration {
	if err == nil {
		return 0
	}
	low := strings.ToLower(err.Error())
	i := strings.Index(low, "retry-after:")
	if i < 0 {
		return 0
	}
	raw := strings.TrimSpace(low[i+len("retry-after:"):])
	if j := strings.IndexAny(raw, ",;)\n\t "); j >= 0 {
		raw = raw[:j]
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if d, perr := time.ParseDuration(raw); perr == nil && d > 0 {
		return d
	}
	if n, perr := strconv.Atoi(raw); perr == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return 0
}

// pullRetryWait 第 attempt 次失败后的等待时间（attempt 从 1 开始）：
// max(退避序列, retry-after)，夹在 1s..2min 之间，再加 ±10% 抖动避免多容器同时重试。
func pullRetryWait(err error, attempt int) time.Duration {
	base := pullRetryDelays[len(pullRetryDelays)-1]
	if attempt >= 1 && attempt <= len(pullRetryDelays) {
		base = pullRetryDelays[attempt-1]
	}
	if d := retryAfterFromError(err); d > base {
		base = d
	}
	if base < pullRetryMinWait {
		base = pullRetryMinWait
	}
	if base > 2*time.Minute {
		base = 2 * time.Minute
	}
	jitter := time.Duration(rand.Int63n(int64(base)/5+1)) - base/10
	return base + jitter
}

// pullAttempt 单次直连拉取（ImagePull + 消费进度流）。抽成变量便于单测
// 用假实现验证重试循环，不必真的起 registry / 连 Docker。
var pullAttempt = func(svcCtx *svc.ServiceContext, ctx context.Context, taskIDs []string, imageRef string, pullOpts image.PullOptions) error {
	reader, err := svcCtx.DockerClient.ImagePull(ctx, imageRef, pullOpts)
	if err == nil {
		if len(taskIDs) > 0 {
			err = decodePullResp(reader, svcCtx, taskIDs)
		} else {
			err = consumePullStream(reader)
		}
	}
	if err != nil {
		return FriendlyPullError(imageRef, err)
	}
	return nil
}

// pullDirectWithRetry 直连拉取并在瞬态错误上重试。
// 每次重试都重新发起 ImagePull（流内 429 与调用本身失败走同一条路），
// 总时长受外层 ctx（pullTimeout）约束，不会因重试无限挂住。
func pullDirectWithRetry(svcCtx *svc.ServiceContext, ctx context.Context, taskIDs []string, imageRef string, pullOpts image.PullOptions) error {
	for attempt := 1; ; attempt++ {
		err := pullAttempt(svcCtx, ctx, taskIDs, imageRef, pullOpts)
		if err == nil {
			return nil
		}
		if attempt > len(pullRetryDelays) || !isRetriablePullError(err) || ctx.Err() != nil {
			return err
		}
		wait := pullRetryWait(err, attempt)
		logx.Errorf("拉取 %s 失败（第 %d 次）：%v；%s 后重试", imageRef, attempt, err, wait)
		setTaskMessage(svcCtx, taskIDs, fmt.Sprintf(
			"拉取失败（%s），%s 后自动重试（%d/%d）",
			oneLine(err.Error()), wait.Round(time.Second), attempt+1, len(pullRetryDelays)+1))
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return err
		}
	}
}

// PullImageForUpdate 单任务拉取入口（更新流程/自更新用）
func PullImageForUpdate(svcCtx *svc.ServiceContext, taskID, imageRef string) error {
	var ids []string
	if taskID != "" {
		ids = []string{taskID}
	}
	return PullImageForTasks(svcCtx, ids, imageRef)
}

// PullImageForTasks 拉取入口（多任务版）：配置了默认加速源且是 Docker Hub 镜像时
// 先走加速源，失败自动回退直连。taskIDs 为空时拉取但不写进度（纯预拉场景）。
// 批量更新把组内所有容器的 taskID 传进来 —— 同镜像只拉一次、进度对每台都可见。
// 任何失败出口都由 defer 统一落失败终态：ImagePull 在拿到进度流之前就失败
// （TLS 握手超时 / 限流 / 仓库不可达）时，此前不会写任何进度，排队任务会永远停在「等待中」。
func PullImageForTasks(svcCtx *svc.ServiceContext, taskIDs []string, imageRef string) (err error) {
	defer func() {
		if err != nil {
			setTasksFailed(svcCtx, taskIDs, "拉取镜像失败", FriendlyDaemonError(err).Error())
		}
	}()
	if source := svcCtx.Accelerator.UpdateSource(); source != "" {
		if _, err := MirrorRefFor(source, imageRef); err == nil {
			if perr := pullImageViaMirrorFor(svcCtx, taskIDs, source, imageRef); perr == nil {
				return nil
			} else {
				logx.Errorf("加速源拉取失败，回退直连 %s: %v", imageRef, perr)
				setTaskMessage(svcCtx, taskIDs, "加速源失败（"+perr.Error()+"），改用直连重试")
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
	defer cancel()
	// 私有仓库（ghcr 私有包等）：带上面板里配的凭据；没有配置时为空 = 匿名（行为不变）
	pullOpts := image.PullOptions{}
	if svcCtx.RegistryAuth != nil {
		pullOpts.RegistryAuth = svcCtx.RegistryAuth.DaemonAuth(imageRef)
	}
	return pullDirectWithRetry(svcCtx, ctx, taskIDs, imageRef, pullOpts)
}
