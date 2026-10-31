package utiles

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/image"
	dockerMsgType "github.com/docker/docker/pkg/jsonmessage"
	"github.com/libremk66/DockHamster/internal/svc"
)

func TestPullMessageSucceeded(t *testing.T) {
	if !pullMessageSucceeded(dockerMsgType.JSONMessage{Status: "Status: Downloaded newer image for nginx:latest"}) {
		t.Fatal("Downloaded newer image 应视为成功")
	}
	if !pullMessageSucceeded(dockerMsgType.JSONMessage{Status: "Image is up to date for nginx:latest"}) {
		t.Fatal("Image is up to date 应视为成功")
	}
	if pullMessageSucceeded(dockerMsgType.JSONMessage{Status: "Pulling from library/nginx"}) {
		t.Fatal("中间层状态不应视为成功")
	}
}

func TestConsumePullStreamRequiresSuccess(t *testing.T) {
	ok := `{"status":"Pulling from library/nginx"}` + "\n" +
		`{"status":"Status: Downloaded newer image for nginx:latest"}` + "\n"
	if err := consumePullStream(io.NopCloser(strings.NewReader(ok))); err != nil {
		t.Fatalf("完整流应成功: %v", err)
	}

	truncated := `{"status":"Pulling from library/nginx"}` + "\n"
	if err := consumePullStream(io.NopCloser(strings.NewReader(truncated))); err == nil {
		t.Fatal("没有成功确认的 EOF 应报错")
	}
}

func TestIsRetriablePullError(t *testing.T) {
	retriable := []string{
		`Error response from daemon: toomanyrequests: retry-after: 783.622µs, allowed: 44000/minute`,
		`toomanyrequests: You have reached your unauthenticated pull rate limit.`,
		`429 Too Many Requests`,
		`拉取镜像失败: unexpected EOF`,
		`拉取流提前结束（未收到成功确认）`,
	}
	for _, raw := range retriable {
		if !isRetriablePullError(errors.New(raw)) {
			t.Fatalf("应可重试：%s", raw)
		}
	}
	permanent := []string{
		`manifest unknown`,
		`denied: requested access to the resource is denied`,
		`no matching manifest for linux/arm64`,
	}
	for _, raw := range permanent {
		if isRetriablePullError(errors.New(raw)) {
			t.Fatalf("不应重试：%s", raw)
		}
	}
	if isRetriablePullError(nil) {
		t.Fatal("nil 不应重试")
	}
}

func TestRetryAfterFromError(t *testing.T) {
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{`toomanyrequests: retry-after: 783.622µs, allowed: 44000/minute`, 783622 * time.Nanosecond},
		{`toomanyrequests: retry-after: 2s, allowed: 1/minute`, 2 * time.Second},
		{`toomanyrequests: retry-after: 3, allowed: 1/minute`, 3 * time.Second},
		{`toomanyrequests: You have reached your pull rate limit.`, 0},
	}
	for _, c := range cases {
		if got := retryAfterFromError(errors.New(c.raw)); got != c.want {
			t.Fatalf("retryAfterFromError(%q) = %s，期望 %s", c.raw, got, c.want)
		}
	}
	if retryAfterFromError(nil) != 0 {
		t.Fatal("nil 应返回 0")
	}
}

func TestPullRetryWaitBounds(t *testing.T) {
	old := pullRetryDelays
	defer func() { pullRetryDelays = old }()

	// 用固定退避序列验证：retry-after 只是下限，指数退避跨过整分钟。
	pullRetryDelays = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute}
	ghcrErr := errors.New(`toomanyrequests: retry-after: 200µs, allowed: 44000/minute`)

	if got := pullRetryWait(ghcrErr, 1); got < 4500*time.Millisecond || got > 5500*time.Millisecond {
		t.Fatalf("第 1 次重试应约 5s（±10%%），实际 %s", got)
	}
	if got := pullRetryWait(ghcrErr, 2); got < 27*time.Second || got > 33*time.Second {
		t.Fatalf("第 2 次重试应约 30s（±10%%），实际 %s", got)
	}
	// 服务端要求更久时以其为准（封顶 2 分钟 + 抖动）
	longErr := errors.New(`toomanyrequests: retry-after: 90s, allowed: 1/minute`)
	if got := pullRetryWait(longErr, 1); got < 81*time.Second || got > 99*time.Second {
		t.Fatalf("应尊重 retry-after=90s（±10%%），实际 %s", got)
	}
	if got := pullRetryWait(errors.New(`toomanyrequests: retry-after: 600s, allowed: 1/minute`), 1); got > 132*time.Second {
		t.Fatalf("退避应封顶 2 分钟（含抖动），实际 %s", got)
	}
}

func TestPullDirectWithRetry(t *testing.T) {
	oldAttempt, oldDelays, oldMin := pullAttempt, pullRetryDelays, pullRetryMinWait
	defer func() {
		pullAttempt, pullRetryDelays, pullRetryMinWait = oldAttempt, oldDelays, oldMin
	}()
	pullRetryDelays = []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond}
	pullRetryMinWait = time.Millisecond

	const ref = "ghcr.io/example/app:latest"
	rateLimit := errors.New(`拉取镜像失败: toomanyrequests: retry-after: 1µs, allowed: 44000/minute`)

	t.Run("限流后重试成功", func(t *testing.T) {
		calls := 0
		pullAttempt = func(_ *svc.ServiceContext, _ context.Context, _ []string, _ string, _ image.PullOptions) error {
			calls++
			if calls < 3 {
				return rateLimit
			}
			return nil
		}
		if err := pullDirectWithRetry(nil, context.Background(), nil, ref, image.PullOptions{}); err != nil {
			t.Fatalf("第 3 次应成功: %v", err)
		}
		if calls != 3 {
			t.Fatalf("应调用 3 次，实际 %d", calls)
		}
	})

	t.Run("永久错误不重试", func(t *testing.T) {
		calls := 0
		pullAttempt = func(_ *svc.ServiceContext, _ context.Context, _ []string, _ string, _ image.PullOptions) error {
			calls++
			return errors.New("manifest unknown")
		}
		if err := pullDirectWithRetry(nil, context.Background(), nil, ref, image.PullOptions{}); err == nil {
			t.Fatal("应返回错误")
		}
		if calls != 1 {
			t.Fatalf("永久错误只应尝试 1 次，实际 %d", calls)
		}
	})

	t.Run("重试用尽返回错误", func(t *testing.T) {
		calls := 0
		pullAttempt = func(_ *svc.ServiceContext, _ context.Context, _ []string, _ string, _ image.PullOptions) error {
			calls++
			return rateLimit
		}
		err := pullDirectWithRetry(nil, context.Background(), nil, ref, image.PullOptions{})
		if err == nil || !strings.Contains(err.Error(), "toomanyrequests") {
			t.Fatalf("应保留限流错误，实际 %v", err)
		}
		if want := len(pullRetryDelays) + 1; calls != want {
			t.Fatalf("应尝试 %d 次，实际 %d", want, calls)
		}
	})
}
