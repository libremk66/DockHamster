package utiles

import (
	"io"
	"strings"
	"testing"
	"time"

	dockerMsgType "github.com/docker/docker/pkg/jsonmessage"
	"github.com/libremk66/DockHamster/internal/svc"
)

// 分层聚合：按字节累计总进度，且层陆续登记（分母变大）时进度不回退
func TestPullProgressAggregateAndMonotonic(t *testing.T) {
	pp := newPullProgress()
	feed := func(id string, cur, total int64) float64 {
		pct, _ := pp.feed(dockerMsgType.JSONMessage{
			ID: id, Status: "Downloading",
			Progress: &dockerMsgType.JSONProgress{Current: cur, Total: total},
		})
		return pct
	}

	if pct := feed("l1", 50, 100); pct != 50 {
		t.Fatalf("第一层 50/100 → %v，想要 50", pct)
	}
	// 第二层登记（总量变大）：50/200 = 25%，但单调不回退 → 仍显示 50
	if pct := feed("l2", 0, 100); pct != 50 {
		t.Fatalf("新层登记后回退到 %v，想要保持 50", pct)
	}
	// 两层各推进一半：100/200 = 50%（与上一步持平，不回退）
	if pct := feed("l2", 50, 100); pct != 50 {
		t.Fatalf("100/200 → %v，想要 50", pct)
	}
	// 第一层完成 + 第二层一半：150/200 = 75%
	if pct := feed("l1", 100, 100); pct != 75 {
		t.Fatalf("150/200 → %v，想要 75", pct)
	}
	// 全部完成
	if pct := feed("l2", 100, 100); pct != 100 {
		t.Fatalf("全部完成 → %v，想要 100", pct)
	}
}

// "Pull complete" 状态把该层直接记为完成；未知总量时回退为状态文本
func TestPullProgressStatuses(t *testing.T) {
	pp := newPullProgress()
	if _, detail := pp.feed(dockerMsgType.JSONMessage{Status: "Pulling fs layer"}); detail != "Pulling fs layer" {
		t.Fatalf("无总量时细节 = %q，想要状态文本", detail)
	}
	pp.feed(dockerMsgType.JSONMessage{
		ID: "l1", Status: "Downloading",
		Progress: &dockerMsgType.JSONProgress{Current: 0, Total: 100},
	})
	pct, _ := pp.feed(dockerMsgType.JSONMessage{ID: "l1", Status: "Pull complete"})
	if pct != 100 {
		t.Fatalf("Pull complete 后 %v，想要 100", pct)
	}
}

// 速度：隔 1 秒采样一次，细节文案带 /s
func TestPullProgressSpeed(t *testing.T) {
	pp := newPullProgress()
	pp.feed(dockerMsgType.JSONMessage{
		ID: "l1", Status: "Downloading",
		Progress: &dockerMsgType.JSONProgress{Current: 0, Total: 10 << 20},
	})
	time.Sleep(1100 * time.Millisecond)
	_, detail := pp.feed(dockerMsgType.JSONMessage{
		ID: "l1", Status: "Downloading",
		Progress: &dockerMsgType.JSONProgress{Current: 5 << 20, Total: 10 << 20},
	})
	if !strings.Contains(detail, "/s") {
		t.Fatalf("细节文案 %q 未包含速度", detail)
	}
}

// decodePullResp 多任务分发：组内每台的任务都能拿到同一份拉取进度；
// 失败时每台都被标记 Failed（UI 显示红色，而不是绿色"完成"）
func TestDecodePullRespFanOut(t *testing.T) {
	newCtx := func() *svc.ServiceContext {
		return &svc.ServiceContext{ProgressStore: svc.ProgressStoreType{}}
	}
	ids := []string{"t1", "t2"}

	// 成功流：两条下载消息 + 完成
	stream := strings.Join([]string{
		`{"id":"l1","status":"Downloading","progressDetail":{"current":50,"total":100}}`,
		`{"id":"l1","status":"Downloading","progressDetail":{"current":100,"total":100}}`,
		`{"id":"l1","status":"Pull complete"}`,
	}, "\n")
	ctx := newCtx()
	if err := decodePullResp(io.NopCloser(strings.NewReader(stream)), ctx, ids); err != nil {
		t.Fatalf("成功流不应报错: %v", err)
	}
	for _, id := range ids {
		p, ok := ctx.GetProgress(id)
		if !ok {
			t.Fatalf("任务 %s 未收到进度", id)
		}
		if p.Percentage < 60 || p.Failed || p.IsDone {
			t.Fatalf("任务 %s 进度异常: %+v", id, p)
		}
		if !strings.Contains(p.DetailMsg, "/") {
			t.Fatalf("任务 %s 细节文案缺字节进度: %q", id, p.DetailMsg)
		}
	}

	// 失败流：流里带 error
	ctx2 := newCtx()
	bad := `{"errorDetail":{"message":"manifest unknown"}}`
	if err := decodePullResp(io.NopCloser(strings.NewReader(bad)), ctx2, ids); err == nil {
		t.Fatal("失败流应返回错误")
	}
	for _, id := range ids {
		p, _ := ctx2.GetProgress(id)
		if !p.Failed || !p.IsDone {
			t.Fatalf("任务 %s 未标记失败: %+v", id, p)
		}
	}
}
