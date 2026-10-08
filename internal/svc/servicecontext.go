package svc

import (
	"github.com/docker/docker/client"
	"github.com/libremk66/DockHamster/internal/config"
	"github.com/libremk66/DockHamster/internal/module"
	"github.com/libremk66/DockHamster/internal/watchdog"
	"github.com/robfig/cron/v3"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/rest"
	"sort"
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
	Failed bool
	// 任务元数据（InitTask 登记一次；UpdateProgress 合并时保留）：
	Kind      string    // update | pull | selfupdate | migrate
	Source    string    // container | autoupdate | group | accelerator | selfupdate | migrate | rollback
	StartedAt time.Time // 供任务页显示耗时/排序
	UpdatedAt time.Time // 已完成任务保留期依据
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

// InitTask 登记任务元数据（名称/类型/来源/开始时间）。
// 各触发入口在任务开始时调一次；后续 UpdateProgress 会保留这些字段。
func (ctx *ServiceContext) InitTask(taskID, name, kind, source string) {
	if taskID == "" {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	p := ctx.ProgressStore[taskID]
	p.TaskID = taskID
	if name != "" {
		p.Name = name
	}
	if kind != "" {
		p.Kind = kind
	}
	if source != "" {
		p.Source = source
	}
	now := time.Now()
	if p.StartedAt.IsZero() {
		p.StartedAt = now
	}
	p.UpdatedAt = now
	ctx.ProgressStore[taskID] = p
}

func (ctx *ServiceContext) UpdateProgress(taskID string, progress TaskProgress) {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	// 元数据只登记一次：进度更新里缺省的字段从已有记录补齐（调用方常整体覆盖结构体）
	if old, ok := ctx.ProgressStore[taskID]; ok {
		if progress.Kind == "" {
			progress.Kind = old.Kind
		}
		if progress.Source == "" {
			progress.Source = old.Source
		}
		if progress.Name == "" {
			progress.Name = old.Name
		}
		if progress.StartedAt.IsZero() {
			progress.StartedAt = old.StartedAt
		}
	}
	progress.UpdatedAt = time.Now()
	ctx.ProgressStore[taskID] = progress
}

// TaskView 任务列表项（「任务」页用）
type TaskView struct {
	TaskID      string  `json:"taskID"`
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`
	Source      string  `json:"source"`
	Percentage  int     `json:"percentage"`
	Message     string  `json:"message"`
	DetailMsg   string  `json:"detailMsg"`
	IsDone      bool    `json:"isDone"`
	Failed      bool    `json:"failed"`
	StartedAt   string  `json:"startedAt"`
	UpdatedAt   string  `json:"updatedAt"`
	DurationSec float64 `json:"durationSec"`
}

// ListTasks 列出全部任务（进行中在前按开始时间倒序；已完成按更新时间倒序）
func (ctx *ServiceContext) ListTasks() []TaskView {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	const layout = "2006-01-02 15:04:05"
	type entry struct {
		view    TaskView
		start   time.Time
		updated time.Time
	}
	running := []entry{}
	done := []entry{}
	for _, p := range ctx.ProgressStore {
		start := p.StartedAt
		if start.IsZero() {
			start = p.UpdatedAt
		}
		v := TaskView{
			TaskID: p.TaskID, Name: p.Name, Kind: p.Kind, Source: p.Source,
			Percentage: p.Percentage, Message: p.Message, DetailMsg: p.DetailMsg,
			IsDone: p.IsDone, Failed: p.Failed,
		}
		if !start.IsZero() {
			v.StartedAt = start.Format(layout)
		}
		if !p.UpdatedAt.IsZero() {
			v.UpdatedAt = p.UpdatedAt.Format(layout)
			v.DurationSec = p.UpdatedAt.Sub(start).Seconds()
			if v.DurationSec < 0 {
				v.DurationSec = 0
			}
		}
		e := entry{view: v, start: start, updated: p.UpdatedAt}
		if p.IsDone {
			done = append(done, e)
		} else {
			running = append(running, e)
		}
	}
	// 用真实时间排序（展示串只精确到秒，同秒会乱序）
	sort.Slice(running, func(i, j int) bool { return running[i].start.Before(running[j].start) })
	sort.Slice(done, func(i, j int) bool { return done[i].updated.After(done[j].updated) })
	out := make([]TaskView, 0, len(running)+len(done))
	for _, e := range running {
		out = append(out, e.view)
	}
	for _, e := range done {
		out = append(out, e.view)
	}
	return out
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
