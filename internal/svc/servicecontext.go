package svc

import (
	"github.com/docker/docker/client"
	"github.com/libremk66/DockHamster/internal/config"
	"github.com/libremk66/DockHamster/internal/module"
	"github.com/robfig/cron/v3"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/rest"
	"sync"
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
	// 自动更新（UI 配）
	AutoUpdate       *module.AutoUpdateStore
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
		AutoUpdateState: module.NewAutoUpdateState(),
		AutoUpdateCheck: module.NewCheckState(),
	}
}

func (ctx *ServiceContext) UpdateProgress(taskID string, progress TaskProgress) {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.ProgressStore[taskID] = progress
}

func (ctx *ServiceContext) GetProgress(taskID string) (TaskProgress, bool) {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	progress, ok := ctx.ProgressStore[taskID]
	return progress, ok
}
