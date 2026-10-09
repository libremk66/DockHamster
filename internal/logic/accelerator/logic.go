package accelerator

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/libremk66/DockHamster/internal/module"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/libremk66/DockHamster/internal/utiles"
	"github.com/zeromicro/go-zero/core/logx"
)

type AcceleratorLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAcceleratorLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AcceleratorLogic {
	return &AcceleratorLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// GetSettings 读取加速源配置
func (l *AcceleratorLogic) GetSettings() (*types.Resp, error) {
	resp := &types.Resp{Code: 200, Msg: "success"}
	d := l.svcCtx.Accelerator.Get()
	resp.Data = map[string]interface{}{
		"sources":       d.Sources,
		"default":       d.Default,
		"useForUpdates": d.UseForUpdates,
		"builtin":       module.BuiltinAccelerators,
	}
	return resp, nil
}

// SaveSettings 保存加速源配置
func (l *AcceleratorLogic) SaveSettings(req *module.AcceleratorSettings) (*types.Resp, error) {
	if err := l.svcCtx.Accelerator.Save(*req); err != nil {
		return &types.Resp{Code: 500, Msg: "保存失败：" + err.Error(), Data: map[string]interface{}{}}, nil
	}
	return l.GetSettings()
}

// TestResult 单个加速源测速结果（对 https://host/v2/ 发探针）
type TestResult struct {
	Source    string `json:"source"`
	LatencyMs int64  `json:"latencyMs"` // 失败为 -1
	OK        bool   `json:"ok"`
	Status    string `json:"status"` // HTTP 状态码或错误摘要
}

// Test 并发测速（sources 为空则测当前配置的列表）
func (l *AcceleratorLogic) Test(reqSources []string) (*types.Resp, error) {
	sources := reqSources
	if len(sources) == 0 {
		sources = l.svcCtx.Accelerator.Get().Sources
	}
	var list []string
	seen := map[string]bool{}
	for _, raw := range sources {
		if h := module.NormalizeAcceleratorHost(raw); h != "" && !seen[h] {
			seen[h] = true
			list = append(list, h)
		}
	}
	results := make([]TestResult, len(list))
	var wg sync.WaitGroup
	for i, host := range list {
		wg.Add(1)
		go func(i int, host string) {
			defer wg.Done()
			results[i] = probeRegistry(host)
		}(i, host)
	}
	wg.Wait()
	// 可用的排前面，同组按延迟升序
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].OK != results[j].OK {
			return results[i].OK
		}
		return results[i].LatencyMs < results[j].LatencyMs
	})
	return &types.Resp{Code: 200, Msg: "success", Data: results}, nil
}

func probeRegistry(host string) TestResult {
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// 真 registry 对 /v2/ 直接回 200/401；跳到官网首页的域名不算可用
			return http.ErrUseLastResponse
		},
	}
	started := time.Now()
	resp, err := client.Get("https://" + host + "/v2/")
	latency := time.Since(started).Milliseconds()
	if err != nil {
		return TestResult{Source: host, LatencyMs: -1, OK: false, Status: trimProbeErr(err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized {
		return TestResult{Source: host, LatencyMs: latency, OK: true, Status: fmt.Sprintf("%d", resp.StatusCode)}
	}
	return TestResult{Source: host, LatencyMs: -1, OK: false, Status: fmt.Sprintf("HTTP %d", resp.StatusCode)}
}

func trimProbeErr(err error) string {
	s := err.Error()
	if strings.HasPrefix(s, "Get \"") {
		if i := strings.Index(s, "\": "); i != -1 {
			return s[i+3:]
		}
	}
	return s
}

// Pull 通过指定加速源拉取镜像（异步；返回 taskID，前端轮询 /api/progress/:taskid）
func (l *AcceleratorLogic) Pull(source, imageRef string) (*types.Resp, error) {
	source = module.NormalizeAcceleratorHost(source)
	if source == "" {
		return &types.Resp{Code: 400, Msg: "加速源无效", Data: map[string]interface{}{}}, nil
	}
	imageRef = strings.TrimSpace(imageRef)
	if imageRef == "" {
		return &types.Resp{Code: 400, Msg: "镜像为空", Data: map[string]interface{}{}}, nil
	}
	if _, err := utiles.MirrorRefFor(source, imageRef); err != nil {
		return &types.Resp{Code: 400, Msg: err.Error(), Data: map[string]interface{}{}}, nil
	}
	taskID := uuid.New().String()
	l.svcCtx.UpdateProgress(taskID, svc.TaskProgress{
		TaskID:     taskID,
		Name:       imageRef,
		Percentage: 0,
		Message:    "准备通过加速源拉取",
		DetailMsg:  "加速源: " + source + "\n镜像: " + imageRef,
		IsDone:     false,
	})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logx.Errorf("加速拉取 panic: %v", r)
			}
		}()
		l.svcCtx.InitTask(taskID, imageRef, "pull", "accelerator")
		if err := utiles.PullImageViaMirror(l.svcCtx, taskID, source, imageRef); err != nil {
			err = utiles.FriendlyPullError(imageRef, err)
			p, _ := l.svcCtx.GetProgress(taskID)
			p.TaskID = taskID
			p.Message = "加速拉取失败"
			p.DetailMsg = err.Error()
			p.IsDone = true
			p.Failed = true
			l.svcCtx.UpdateProgress(taskID, p)
			return
		}
		p, ok := l.svcCtx.GetProgress(taskID)
		if !ok {
			p = svc.TaskProgress{TaskID: taskID, Name: imageRef}
		}
		p.Message = "加速拉取完成"
		p.DetailMsg = "加速拉取完成"
		p.Percentage = 100
		p.IsDone = true
		l.svcCtx.UpdateProgress(taskID, p)
	}()
	return &types.Resp{Code: 200, Msg: "success", Data: map[string]string{"taskID": taskID}}, nil
}
