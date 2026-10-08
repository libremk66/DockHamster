package svc

import (
	"github.com/docker/docker/client"
	"github.com/libremk66/DockHamster/internal/config"
	"github.com/libremk66/DockHamster/internal/module"
	"github.com/libremk66/DockHamster/internal/watchdog"
	"github.com/robfig/cron/v3"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/rest"
	"sync"
	"time"
)

type ServiceContext struct {
	Config                     config.Config
	CookieCheckMiddleware      rest.Middleware
	Jwtuuid                    string
	BearerTokenCheckMiddleware rest.Middleware
	JwtSecret                  string
	PortainerJwt               string
	HubImageInfo               *module.ImageUpdateData
	IndexCheckMiddleware       rest.Middleware
	ProgressStore              ProgressStoreType
	DockerClient               *client.Client
	// 容器守护（异常告警）；在 main 里注入并启动
	Watchdog *watchdog.Watchdog
	// 自动更新（UI 配）
	AutoUpdate *module.AutoUpdateStore
	// 镜像加速源（UI 配）
	Accelerator      *module.AcceleratorStore
	AutoUpdateState  *module.AutoUpdateState
	AutoUpdateCheck  *module.CheckState
	CronEngine       *cron.Cron
	AutoUpdateCronID cron.EntryID
	CheckCronID      cron.EntryID
	CronMu           sync.Mutex
	mu               sync.Mutex
}

type TaskProgress struct {
	TaskID     string
	Percentage int
	Message    string
	Name       string
	DetailMsg  string
	IsDone     bool
	// Failed 标记失败态（前端红色展示；与 IsDone 组合区分"完成/失败"，不加 json tag 不直接序列化）
	Failed    bool
	UpdatedAt time.Time // 内部用：已完成任务过期清理依据
}

type ProgressStoreType map[string]TaskProgress

func NewServiceContext(c config.Config) *ServiceContext {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		logx.Errorf("Unable to create docker client: %s", err)
	}
	return &ServiceContext{
		Config:          c,
		HubImageInfo:    module.NewImageCheck(),
		ProgressStore:   make(ProgressStoreType),
		DockerClient:    cli,
		AutoUpdate:      module.NewAutoUpdateStore(),
		Accelerator:     module.NewAcceleratorStore(),
		AutoUpdateState: module.NewAutoUpdateState(),
		AutoUpdateCheck: module.NewCheckState(),
	}
}

func (ctx *ServiceContext) UpdateProgress(taskID string, progress TaskProgress) {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	progress.UpdatedAt = time.Now()
	ctx.ProgressStore[taskID] = progress
}

func (ctx *ServiceContext) GetProgress(taskID string) (TaskProgress, bool) {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	progress, ok := ctx.ProgressStore[taskID]
	return progress, ok
}

// PurgeFinishedProgress 清理"已完成且 N 时间未再更新"的进度记录，
// 避免长期运行下 ProgressStore 随任务数缓涨（活跃任务的进度每 2 秒被心跳刷新，不会被清）。
func (ctx *ServiceContext) PurgeFinishedProgress(olderThan time.Duration) int {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	n := 0
	for id, p := range ctx.ProgressStore {
		if p.IsDone && time.Since(p.UpdatedAt) > olderThan {
			delete(ctx.ProgressStore, id)
			n++
		}
	}
	return n
}
