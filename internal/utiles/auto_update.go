package utiles

import (
	"context"
	"fmt"
	"github.com/libremk66/DockHamster/internal/notify"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/google/uuid"
	"github.com/libremk66/DockHamster/internal/module"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
	"os"
)

// parseNameList 解析名称列表（大小写不敏感匹配用，统一转小写）
func parseNameList(names []string) map[string]bool {
	out := make(map[string]bool)
	for _, n := range names {
		name := strings.ToLower(strings.TrimSpace(n))
		if name != "" {
			out[name] = true
		}
	}
	return out
}

// AutoUpdateTarget 一次自动更新中"将被更新"的容器
type AutoUpdateTarget struct {
	ID      string
	Name    string
	PullRef string
	TaskID  string // 运行时分配；"更新前提醒"阶段为空
}

// CollectAutoUpdateTargets 按设置筛出"需要更新"的容器：
// 白名单（或全部）∩ 非排除名单 ∩ 镜像有新版本 ∩ 有可拉取的镜像名。
// 自动更新执行与"更新前提醒"共用这一段，保证预告清单与真正要更新的完全一致。
func CollectAutoUpdateTargets(serviceContext *svc.ServiceContext, settings module.AutoUpdateSettings) ([]AutoUpdateTarget, error) {
	whitelist := parseNameList(settings.Containers)
	exclude := parseNameList(settings.Exclude)
	updateAll := whitelist["*"]
	ctx := context.Background()
	list, err := serviceContext.DockerClient.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	var targets []AutoUpdateTarget
	for _, c := range list {
		if len(c.Names) == 0 {
			continue
		}
		name := strings.TrimPrefix(c.Names[0], "/")
		lower := strings.ToLower(name)
		if strings.Contains(strings.ToLower(c.Image), "dockhamster") {
			continue // 不自动更新 DockCopilot 自身
		}
		if exclude[lower] {
			continue
		}
		if !updateAll && !whitelist[lower] {
			continue
		}
		if !serviceContext.HubImageInfo.NeedUpdate(c.ImageID) {
			continue
		}
		targets = append(targets, AutoUpdateTarget{ID: c.ID, Name: name, PullRef: resolvePullRef(serviceContext, c.ImageID, c.Image)})
	}
	return targets, nil
}

// RunAutoUpdate 按白名单自动更新容器（由定时任务或 UI「立即运行」触发）。
// 配置来自 svcCtx.AutoUpdate（UI 持久化；环境变量仅作初始默认值）。
// trigger: auto | manual；group 由 RunGroupUpdate 单独记录。
// 同一镜像被多个容器共用时：只拉取一次，再逐个容器重建；
// 旧镜像由 CleanupOldImage 的引用计数保护，最后一个使用者更新完成后才清理。
func RunAutoUpdate(serviceContext *svc.ServiceContext, trigger string) {
	settings := serviceContext.AutoUpdate.Get()
	whitelist := parseNameList(settings.Containers)
	if len(whitelist) == 0 {
		logx.Info("自动更新：白名单为空，跳过")
		return
	}
	delOldContainer := os.Getenv("DelOldContainer") != "false"

	if !serviceContext.AutoUpdateState.TryStart() {
		logx.Info("自动更新：已有任务在运行，跳过本轮")
		return
	}
	defer serviceContext.AutoUpdateState.Finish()

	start := time.Now()
	result := module.AutoUpdateRunResult{
		Time:    time.Now().Format("2006-01-02 15:04:05"),
		Trigger: trigger,
		Updated: []string{},
		Failed:  []module.AutoRunFailure{},
	}

	targets, terr := CollectAutoUpdateTargets(serviceContext, settings)
	if terr != nil {
		logx.Errorf("自动更新：获取容器列表失败 %v", terr)
		result.Note = "获取容器列表失败: " + terr.Error()
		result.DurationSec = time.Since(start).Seconds()
		serviceContext.AutoUpdateState.AddRun(result)
		notifyAutoUpdate(serviceContext, result)
		return
	}

	if len(targets) == 0 {
		logx.Info("自动更新：没有需要更新的容器")
		return
	}

	baseOpts := UpdateOptions{
		SkipPull:        true, // 拉取在下面按镜像统一处理
		DelOldContainer: delOldContainer,
		DeleteOldImage:  settings.DeleteOldImage,
		SnapshotOptions: SnapshotOptionsFromSettings(settings),
		Trigger:         "autoupdate",
	}

	// 预生成任务 ID 并登记为"进行中"，供 status 接口实时展示
	active := make([]module.ActiveTask, 0, len(targets))
	for i := range targets {
		targets[i].TaskID = uuid.New().String()
		active = append(active, module.ActiveTask{Name: targets[i].Name, TaskID: targets[i].TaskID})
	}
	serviceContext.AutoUpdateState.SetActive(active)
	defer serviceContext.AutoUpdateState.ClearActive()

	// 按镜像分组：同一镜像只拉取一次
	groups := make(map[string][]AutoUpdateTarget)
	for _, t := range targets {
		if t.PullRef == "" {
			// 镜像无标签（悬空/本地构建）：无法确定拉取目标，明确跳过并说明
			logx.Errorf("自动更新：容器 %s 的镜像无有效标签，跳过", t.Name)
			result.Failed = append(result.Failed, module.AutoRunFailure{Name: t.Name, Error: "镜像无有效标签（悬空/本地构建），无法自动更新"})
			serviceContext.AutoUpdateState.SetContainer(t.Name, false, "镜像无有效标签，跳过")
			continue
		}
		groups[t.PullRef] = append(groups[t.PullRef], t)
	}
	for pullRef, group := range groups {
		// 组内每台的 taskID 都带上：同镜像只拉一次，拉取进度对每一台的行都可见
		// （此前拉取阶段不写任何进度，是"整页停在等待中像卡死"的主因）
		groupTaskIDs := make([]string, 0, len(group))
		for _, t := range group {
			groupTaskIDs = append(groupTaskIDs, t.TaskID)
		}
		logx.Infof("自动更新：拉取 %s（%d 个容器共用）", pullRef, len(group))
		if err := PullImageByRefForTasks(serviceContext, pullRef, groupTaskIDs); err != nil {
			logx.Errorf("自动更新：拉取 %s 失败，跳过本组：%v", pullRef, err)
			// 失败进度已由拉取流程写进各 taskID（含 Failed 标记，UI 显示红色）
			for _, t := range group {
				result.Failed = append(result.Failed, module.AutoRunFailure{Name: t.Name, Error: "拉取失败: " + oneLine(FriendlyDaemonError(err).Error())})
				serviceContext.AutoUpdateState.SetContainer(t.Name, false, "拉取失败: "+oneLine(FriendlyDaemonError(err).Error()))
			}
			continue
		}
		for _, t := range group {
			logx.Infof("自动更新：开始更新容器 %s（旧镜像策略 %s）", t.Name, settings.ResolveOldImagePolicy(t.Name))
			opts := baseOpts
			opts.OldImagePolicy = settings.ResolveOldImagePolicy(t.Name)
			outcome, err := updateContainerCore(serviceContext, t.ID, t.Name, pullRef, opts, t.TaskID)
			if err != nil {
				logx.Errorf("自动更新：容器 %s 更新失败：%v", t.Name, err)
				result.Failed = append(result.Failed, module.AutoRunFailure{Name: t.Name, Error: oneLine(err.Error())})
				serviceContext.AutoUpdateState.SetContainer(t.Name, false, oneLine(err.Error()))
			} else {
				logx.Infof("自动更新：容器 %s 更新完成", t.Name)
				result.Updated = append(result.Updated, t.Name)
				if outcome.Cleaned {
					result.CleanedImages++
				}
				if outcome.SnapshotRef != "" {
					result.Snapshots = append(result.Snapshots, outcome.SnapshotRef)
				}
				serviceContext.AutoUpdateState.SetContainer(t.Name, true, "更新成功")
			}
		}
	}

	result.DurationSec = float64(int(time.Since(start).Seconds()*10)) / 10
	serviceContext.AutoUpdateState.AddRun(result)
	logx.Infof("自动更新完成：%d 成功 / %d 失败 / 清理旧镜像 %d / 快照 %d", len(result.Updated), len(result.Failed), result.CleanedImages, len(result.Snapshots))
	notifyAutoUpdate(serviceContext, result)
}

// notifyAutoUpdate 按设置向所有已启用渠道发送更新简报（有内容才发：有更新成功 或 有失败）。
// 渠道开关由通知设置里各渠道的 enabled 决定（notify.Send 内部过滤）。
func notifyAutoUpdate(serviceContext *svc.ServiceContext, r module.AutoUpdateRunResult) {
	settings := serviceContext.AutoUpdate.Get()
	hasResult := len(r.Updated) > 0 || len(r.Failed) > 0
	if !hasResult {
		return
	}
	if len(r.Failed) > 0 && !settings.NotifyOnFailure {
		return
	}
	if len(r.Failed) == 0 && !settings.NotifyOnSuccess {
		return
	}
	title, text := composeAutoUpdateMessage(r)
	results := notify.Send(settings.Notify, title, text)
	for _, res := range results {
		if res.OK {
			logx.Infof("通知已发送: %s", res.Channel)
		} else {
			logx.Errorf("通知发送失败 %s: %s", res.Channel, res.Error)
		}
	}
}

// composeAutoUpdateMessage 生成通知标题与正文（正文不含标题行，各渠道自行拼接）
// ---- 更新前提醒（提前量，与更新执行解耦）----
//
// 设计：更新本身仍由原 cron 可靠驱动；提醒只是一个"尽力而为"的定时器——
// 启动时 / 改设置后 / 每轮运行结束后重算并重挂。面板错过提醒时刻（重启等）不会影响更新。
var (
	beforeNoticeMu    sync.Mutex
	beforeNoticeTimer *time.Timer
	beforeNoticeFired time.Time // 已就本次计划时间发过提醒（防重算/重启导致重复发）
)

// ScheduleBeforeUpdateNotice 重算并挂上"更新前提醒"定时器（幂等，可反复调用）
func ScheduleBeforeUpdateNotice(serviceContext *svc.ServiceContext) {
	beforeNoticeMu.Lock()
	defer beforeNoticeMu.Unlock()
	if beforeNoticeTimer != nil {
		beforeNoticeTimer.Stop()
		beforeNoticeTimer = nil
	}
	settings := serviceContext.AutoUpdate.Get()
	if !settings.NotifyBeforeUpdate || !settings.Enabled || serviceContext.CronEngine == nil || serviceContext.AutoUpdateCronID == 0 {
		return
	}
	next := serviceContext.CronEngine.Entry(serviceContext.AutoUpdateCronID).Next
	if next.IsZero() {
		logx.Infof("更新前提醒：未挂（cron 引擎尚未给出下次时间）")
		return
	}
	lead := time.Duration(settings.NotifyBeforeUpdateLeadMin) * time.Minute
	delay := time.Until(next.Add(-lead))
	if delay < 0 {
		delay = 0 // 已进入提醒窗口（刚改完设置/刚启动）：立即提醒一次
	}
	plan := next
	logx.Infof("更新前提醒：已挂（计划 %s，提前 %s，%.0f 秒后发）", plan.Format("15:04"), lead, delay.Seconds())
	beforeNoticeTimer = time.AfterFunc(delay, func() {
		beforeNoticeMu.Lock()
		if beforeNoticeFired.Equal(plan) {
			beforeNoticeMu.Unlock()
			return
		}
		beforeNoticeFired = plan
		beforeNoticeMu.Unlock()
		sendBeforeUpdateNotice(serviceContext, plan)
	})
}

// sendBeforeUpdateNotice 发送"即将更新"提醒；清单按发信时刻的设置实时计算（与实际更新共用同一筛选）
func sendBeforeUpdateNotice(serviceContext *svc.ServiceContext, planAt time.Time) {
	settings := serviceContext.AutoUpdate.Get()
	if !settings.NotifyBeforeUpdate {
		logx.Infof("更新前提醒：跳过（期间被关掉）")
		return // 期间被关掉
	}
	targets, err := CollectAutoUpdateTargets(serviceContext, settings)
	if err != nil {
		logx.Errorf("更新前提醒：筛选目标失败: %v", err)
		return
	}
	if len(targets) == 0 {
		logx.Infof("更新前提醒：跳过（当前没有需要更新的容器）")
		return // 没有要更新的就不打扰
	}
	names := make([]string, 0, len(targets))
	for _, t := range targets {
		names = append(names, t.Name)
	}
	title, text := composeBeforeUpdateMessage(names, planAt.Format("2006-01-02 15:04"))
	for _, res := range notify.Send(settings.Notify, title, text) {
		if res.OK {
			logx.Infof("更新前提醒已发送: %s", res.Channel)
		} else {
			logx.Errorf("更新前提醒发送失败 %s: %s", res.Channel, res.Error)
		}
	}
}

// composeBeforeUpdateMessage 更新前提醒的文案（渠道通用；飞书应用模式会渲染成卡片）
func composeBeforeUpdateMessage(names []string, ts string) (title, text string) {
	title = "⏳ DockHamster 更新即将开始 · " + ts
	var b strings.Builder
	fmt.Fprintf(&b, "将更新 %d 个容器（会短暂重启）：\n", len(names))
	for _, n := range names {
		b.WriteString("· " + n + "\n")
	}
	b.WriteString("\n若正在使用以上服务，请先保存工作（编辑中的文档 / 未完成的下载 / 上传中的任务）。\n更新完成后会另发结果简报。")
	return title, b.String()
}

func composeAutoUpdateMessage(r module.AutoUpdateRunResult) (title, text string) {
	title = "🔄 DockHamster 自动更新 " + r.Time
	if r.Trigger == "group" {
		title = "🔄 DockHamster 整组更新 " + r.Time
	}
	var b strings.Builder
	if len(r.Updated) > 0 {
		b.WriteString(fmt.Sprintf("✅ 已更新 %d 个：%s\n", len(r.Updated), strings.Join(r.Updated, "、")))
	}
	if r.CleanedImages > 0 {
		b.WriteString(fmt.Sprintf("🗑️ 清理旧镜像 %d 个\n", r.CleanedImages))
	}
	if len(r.Snapshots) > 0 {
		b.WriteString(fmt.Sprintf("🏷️ 已打快照 %d 个：%s\n", len(r.Snapshots), strings.Join(r.Snapshots, "、")))
	}
	if len(r.Failed) > 0 {
		b.WriteString(fmt.Sprintf("⚠️ 失败 %d 个：\n", len(r.Failed)))
		for _, f := range r.Failed {
			b.WriteString("  - " + f.Name + "：" + f.Error + "\n")
		}
	}
	if r.Note != "" {
		b.WriteString("ℹ️ " + r.Note + "\n")
	}
	b.WriteString(fmt.Sprintf("⏱ 耗时 %.1fs", r.DurationSec))
	return title, b.String()
}

// oneLine 压成单行并截断（用于失败原因/通知文本）
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// resolvePullRef 取镜像的正式 RepoTag 作为拉取目标。
// 找不到 tag 时返回空串（**不再回退到镜像 ID/原始引用**）——ID 不是可拉取的名称，
// 回退只会把"更新/拉取"引向必然失败的路径（issue #4 的教训）；调用方见空串应明确跳过并说明原因。
func resolvePullRef(serviceContext *svc.ServiceContext, imageID string, fallback string) string {
	images, err := GetImagesList(serviceContext)
	if err == nil {
		for _, img := range images {
			if img.ID == imageID {
				if len(img.RepoTags) > 0 {
					return img.RepoTags[0]
				}
				return "" // 无 tag（悬空/本地构建）：不可作为拉取目标
			}
		}
	}
	// 列表里找不到该镜像（罕见）：仅当 fallback 是合法的带标签引用时才使用
	if fallback != "" {
		if normalized, verr := ValidateUpdateImageRef(fallback); verr == nil {
			return normalized
		}
	}
	return ""
}
