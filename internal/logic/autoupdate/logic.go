package autoupdate

import (
	"context"
	"github.com/libremk66/DockHamster/internal/notify"
	"sort"
	"strings"
	"time"

	"github.com/libremk66/DockHamster/internal/module"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/libremk66/DockHamster/internal/utiles"
	"github.com/zeromicro/go-zero/core/logx"
)

type AutoUpdateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAutoUpdateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AutoUpdateLogic {
	return &AutoUpdateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// GetSettings 读取自动更新设置
func (l *AutoUpdateLogic) GetSettings() (*types.Resp, error) {
	resp := &types.Resp{Code: 200, Msg: "success"}
	resp.Data = l.svcCtx.AutoUpdate.Get()
	return resp, nil
}

// SaveSettings 保存设置并重注册定时任务
func (l *AutoUpdateLogic) SaveSettings(req *module.AutoUpdateSettings) (*types.Resp, error) {
	resp := &types.Resp{}
	if err := ValidateCron(req.Cron); err != nil {
		resp.Code = 400
		resp.Msg = "自动更新计划无效：" + err.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	if strings.TrimSpace(req.CheckCron) != "" {
		if err := ValidateCron(req.CheckCron); err != nil {
			resp.Code = 400
			resp.Msg = "检查更新计划无效：" + err.Error()
			resp.Data = map[string]interface{}{}
			return resp, nil
		}
	}
	if err := l.svcCtx.AutoUpdate.Save(*req); err != nil {
		resp.Code = 500
		resp.Msg = "保存失败：" + err.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	if err := RegisterCrons(l.svcCtx); err != nil {
		resp.Code = 500
		resp.Msg = "定时任务注册失败：" + err.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = l.svcCtx.AutoUpdate.Get()
	return resp, nil
}

// CheckNow 立即检查一轮镜像更新（异步：立刻返回，前端轮询 CheckStatus 看进度）
func (l *AutoUpdateLogic) CheckNow() (resp *types.Resp, err error) {
	resp = &types.Resp{}
	if l.svcCtx.AutoUpdateCheck.Snapshot()["running"] == true {
		resp.Code = 409
		resp.Msg = "已有检查在进行中"
		resp.Data = l.svcCtx.AutoUpdateCheck.Snapshot()
		return resp, nil
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				l.Errorf("检查更新 panic: %v", r)
				l.svcCtx.AutoUpdateCheck.Finish(0, 0, 0)
			}
		}()
		// 状态登记在 CheckAllImageUpdates 内部完成（与 cron/startup 一致）
		if _, _, cerr := utiles.CheckAllImageUpdates(l.svcCtx, "manual"); cerr != nil {
			l.Errorf("检查更新失败: %v", cerr)
		}
	}()
	resp.Code = 200
	resp.Msg = "已开始检查"
	resp.Data = l.svcCtx.AutoUpdateCheck.Snapshot()
	return resp, nil
}

// CheckStatus 查询更新检查的运行状态与上次结果（前端按钮用它显示进度/上次时间）
func (l *AutoUpdateLogic) CheckStatus() (resp *types.Resp, err error) {
	resp = &types.Resp{}
	data := l.svcCtx.AutoUpdateCheck.Snapshot()
	_, nextCheck := l.nextRuns()
	data["nextCheckAt"] = nextCheck
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = data
	return resp, nil
}

// Run 立即运行一轮自动更新（异步）
func (l *AutoUpdateLogic) Run() (*types.Resp, error) {
	resp := &types.Resp{}
	settings := l.svcCtx.AutoUpdate.Get()
	if len(module.SplitNameList(strings.Join(settings.Containers, ","))) == 0 {
		resp.Code = 400
		resp.Msg = "白名单为空，请先在页面上勾选要自动更新的容器"
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	if l.svcCtx.AutoUpdateState.IsRunning() {
		resp.Code = 409
		resp.Msg = "已有更新任务正在运行，请稍后再试"
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	go utiles.RunAutoUpdate(l.svcCtx, "manual")
	resp.Code = 200
	resp.Msg = "已开始运行"
	resp.Data = map[string]interface{}{}
	return resp, nil
}

// Status 运行状态与最近记录（含进行中任务的实时进度）
// nextRuns 返回两条 cron 的下次运行时间（从调度引擎实际登记的条目读取，最准确）
func (l *AutoUpdateLogic) nextRuns() (nextAuto, nextCheck string) {
	if l.svcCtx.CronEngine == nil {
		return "", ""
	}
	now := time.Now()
	if id := l.svcCtx.AutoUpdateCronID; id != 0 {
		if e := l.svcCtx.CronEngine.Entry(id); !e.Next.IsZero() && e.Next.After(now) {
			nextAuto = e.Next.Format("2006-01-02 15:04")
		}
	}
	if id := l.svcCtx.CheckCronID; id != 0 {
		if e := l.svcCtx.CronEngine.Entry(id); !e.Next.IsZero() && e.Next.After(now) {
			nextCheck = e.Next.Format("2006-01-02 15:04")
		}
	}
	return
}

func (l *AutoUpdateLogic) Status() (*types.Resp, error) {
	running, runs, last, active := l.svcCtx.AutoUpdateState.Snapshot()
	nextAuto, nextCheck := l.nextRuns()
	// 进行中任务 + 实时进度快照
	activeTasks := make([]map[string]interface{}, 0, len(active))
	for _, t := range active {
		item := map[string]interface{}{
			"name": t.Name, "taskID": t.TaskID,
			"percentage": 0, "message": "等待中", "detailMsg": "", "isDone": false, "failed": false,
		}
		if p, ok := l.svcCtx.GetProgress(t.TaskID); ok {
			item["percentage"] = p.Percentage
			item["message"] = p.Message
			item["detailMsg"] = p.DetailMsg
			item["isDone"] = p.IsDone
			item["failed"] = p.Failed
		}
		activeTasks = append(activeTasks, item)
	}
	resp := &types.Resp{Code: 200, Msg: "success"}
	resp.Data = map[string]interface{}{
		"running":     running,
		"runs":        runs,
		"lastStatus":  last,
		"activeTasks": activeTasks,
		"nextAutoAt":  nextAuto,
		"nextCheckAt": nextCheck,
	}
	return resp, nil
}

// Tasks 「任务」页：列出全部任务（进行中 + 保留期内的已完成/失败）
func (l *AutoUpdateLogic) Tasks() (*types.Resp, error) {
	resp := &types.Resp{Code: 200, Msg: "success"}
	resp.Data = map[string]interface{}{
		"tasks": l.svcCtx.ListTasks(),
	}
	return resp, nil
}

// HistoryEntry 「任务」页历史记录的统一条目（批次运行 + 任务级历史合并后的展示模型）
type HistoryEntry struct {
	ID          string  `json:"id"`
	Type        string  `json:"type"` // run（自动/整组批次）| task（单任务）
	Time        string  `json:"time"`
	Trigger     string  `json:"trigger,omitempty"` // auto|manual|group|container|accelerator|selfupdate|migrate|rollback
	Kind        string  `json:"kind,omitempty"`    // update|pull|selfupdate|migrate（task 专属）
	Name        string  `json:"name,omitempty"`    // task 专属：容器/对象名
	Failed      bool    `json:"failed"`
	Message     string  `json:"message,omitempty"`
	DetailMsg   string  `json:"detailMsg,omitempty"`
	DurationSec float64 `json:"durationSec"`
	// run 专属字段
	Updated       []string                `json:"updated,omitempty"`
	Failures      []module.AutoRunFailure `json:"failures,omitempty"`
	CleanedImages int                     `json:"cleanedImages,omitempty"`
	Snapshots     []string                `json:"snapshots,omitempty"`
	Note          string                  `json:"note,omitempty"`
}

// TaskHistory 历史记录列表：自动/整组批次运行记录 + 任务级历史，合并按时间倒序
func (l *AutoUpdateLogic) TaskHistory() (*types.Resp, error) {
	resp := &types.Resp{Code: 200, Msg: "success"}
	_, runs, _, _ := l.svcCtx.AutoUpdateState.Snapshot()
	entries := make([]HistoryEntry, 0, len(runs)+8)
	for _, r := range runs {
		entries = append(entries, HistoryEntry{
			ID: r.ID, Type: "run", Time: r.Time, Trigger: r.Trigger,
			Failed: len(r.Failed) > 0, DurationSec: r.DurationSec,
			Updated: r.Updated, Failures: r.Failed, CleanedImages: r.CleanedImages,
			Snapshots: r.Snapshots, Note: r.Note,
		})
	}
	for _, e := range l.svcCtx.TaskHistory.List() {
		entries = append(entries, HistoryEntry{
			ID: e.ID, Type: "task", Time: e.Time, Trigger: e.Source, Kind: e.Kind, Name: e.Name,
			Failed: e.Failed, Message: e.Message, DetailMsg: e.DetailMsg, DurationSec: e.DurationSec,
		})
	}
	// 时间统一为 "2006-01-02 15:04:05"，字符串比较即时间比较
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Time > entries[j].Time })
	resp.Data = map[string]interface{}{"entries": entries}
	return resp, nil
}

// DeleteTaskHistory 按 ID 批量删除历史记录（run- 前缀走运行记录，其余走任务历史）
func (l *AutoUpdateLogic) DeleteTaskHistory(ids []string) (*types.Resp, error) {
	resp := &types.Resp{}
	runIDs := []string{}
	taskIDs := []string{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if strings.HasPrefix(id, "run-") {
			runIDs = append(runIDs, id)
		} else {
			taskIDs = append(taskIDs, id)
		}
	}
	if len(runIDs) == 0 && len(taskIDs) == 0 {
		resp.Code = 400
		resp.Msg = "未选择要删除的记录"
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	deleted := l.svcCtx.AutoUpdateState.DeleteRuns(runIDs) + l.svcCtx.TaskHistory.Delete(taskIDs)
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = map[string]interface{}{"deleted": deleted}
	return resp, nil
}

// TestNotify 发送一条渠道测试消息（draft 为前端当前表单值；为空则用已保存配置）
func (l *AutoUpdateLogic) TestNotify(channel string, draft *notify.Channel) (*types.Resp, error) {
	resp := &types.Resp{}
	if strings.TrimSpace(channel) == "" {
		resp.Code = 400
		resp.Msg = "未指定渠道"
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	cfg := l.svcCtx.AutoUpdate.Get().Notify.ByName(channel)
	if draft != nil {
		cfg = *draft
	}
	res := notify.SendOne(channel, cfg, "🔔 DockHamster 通知测试", "如果你看到这条消息，说明该渠道配置成功 ✅")
	if !res.OK {
		resp.Code = 500
		resp.Msg = "发送失败：" + res.Error
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	resp.Code = 200
	resp.Msg = "已发送，请查看「" + res.Channel + "」"
	resp.Data = map[string]interface{}{}
	return resp, nil
}

// UpdateGroup 整组更新：更新与该容器共用同一镜像的所有容器（方案 A「全部更新」）
func (l *AutoUpdateLogic) UpdateGroup(req *types.GroupUpdateReq) (*types.Resp, error) {
	resp := &types.Resp{}
	tasks, err := utiles.RunGroupUpdate(l.svcCtx, req.Id)
	if err != nil {
		resp.Code = 500
		resp.Msg = err.Error()
		resp.Data = map[string]interface{}{}
		return resp, nil
	}
	resp.Code = 200
	resp.Msg = "success"
	resp.Data = map[string]interface{}{"tasks": tasks}
	return resp, nil
}
