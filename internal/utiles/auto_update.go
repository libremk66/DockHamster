package utiles

import (
	"context"
	"fmt"
	"github.com/libremk66/DockHamster/internal/notify"
	"strings"
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
	exclude := parseNameList(settings.Exclude)
	updateAll := whitelist["*"]
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

	ctx := context.Background()
	list, err := serviceContext.DockerClient.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		logx.Errorf("自动更新：获取容器列表失败 %v", err)
		result.Note = "获取容器列表失败: " + err.Error()
		result.DurationSec = time.Since(start).Seconds()
		serviceContext.AutoUpdateState.AddRun(result)
		notifyAutoUpdate(serviceContext, result)
		return
	}

	type target struct {
		id, name, pullRef, taskID string
	}
	var targets []target
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
		targets = append(targets, target{id: c.ID, name: name, pullRef: resolvePullRef(serviceContext, c.ImageID, c.Image)})
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
		targets[i].taskID = uuid.New().String()
		active = append(active, module.ActiveTask{Name: targets[i].name, TaskID: targets[i].taskID})
	}
	serviceContext.AutoUpdateState.SetActive(active)
	defer serviceContext.AutoUpdateState.ClearActive()

	// 定时更新：正式开始前发一条预告（列出即将更新的容器，提醒保存工作）。
	// 只对定时触发——手动「立即运行」时用户就在面板前，不需要提醒。
	if trigger == "auto" {
		names := make([]string, 0, len(targets))
		for _, t := range targets {
			names = append(names, t.name)
		}
		notifyBeforeUpdate(serviceContext, names)
	}

	// 按镜像分组：同一镜像只拉取一次
	groups := make(map[string][]target)
	for _, t := range targets {
		if t.pullRef == "" {
			// 镜像无标签（悬空/本地构建）：无法确定拉取目标，明确跳过并说明
			logx.Errorf("自动更新：容器 %s 的镜像无有效标签，跳过", t.name)
			result.Failed = append(result.Failed, module.AutoRunFailure{Name: t.name, Error: "镜像无有效标签（悬空/本地构建），无法自动更新"})
			serviceContext.AutoUpdateState.SetContainer(t.name, false, "镜像无有效标签，跳过")
			continue
		}
		groups[t.pullRef] = append(groups[t.pullRef], t)
	}
	for pullRef, group := range groups {
		// 组内每台的 taskID 都带上：同镜像只拉一次，拉取进度对每一台的行都可见
		// （此前拉取阶段不写任何进度，是"整页停在等待中像卡死"的主因）
		groupTaskIDs := make([]string, 0, len(group))
		for _, t := range group {
			groupTaskIDs = append(groupTaskIDs, t.taskID)
		}
		logx.Infof("自动更新：拉取 %s（%d 个容器共用）", pullRef, len(group))
		if err := PullImageByRefForTasks(serviceContext, pullRef, groupTaskIDs); err != nil {
			logx.Errorf("自动更新：拉取 %s 失败，跳过本组：%v", pullRef, err)
			// 失败进度已由拉取流程写进各 taskID（含 Failed 标记，UI 显示红色）
			for _, t := range group {
				result.Failed = append(result.Failed, module.AutoRunFailure{Name: t.name, Error: "拉取失败: " + oneLine(err.Error())})
				serviceContext.AutoUpdateState.SetContainer(t.name, false, "拉取失败: "+oneLine(err.Error()))
			}
			continue
		}
		for _, t := range group {
			logx.Infof("自动更新：开始更新容器 %s（旧镜像策略 %s）", t.name, settings.ResolveOldImagePolicy(t.name))
			opts := baseOpts
			opts.OldImagePolicy = settings.ResolveOldImagePolicy(t.name)
			outcome, err := updateContainerCore(serviceContext, t.id, t.name, pullRef, opts, t.taskID)
			if err != nil {
				logx.Errorf("自动更新：容器 %s 更新失败：%v", t.name, err)
				result.Failed = append(result.Failed, module.AutoRunFailure{Name: t.name, Error: oneLine(err.Error())})
				serviceContext.AutoUpdateState.SetContainer(t.name, false, oneLine(err.Error()))
			} else {
				logx.Infof("自动更新：容器 %s 更新完成", t.name)
				result.Updated = append(result.Updated, t.name)
				if outcome.Cleaned {
					result.CleanedImages++
				}
				if outcome.SnapshotRef != "" {
					result.Snapshots = append(result.Snapshots, outcome.SnapshotRef)
				}
				serviceContext.AutoUpdateState.SetContainer(t.name, true, "更新成功")
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
// notifyBeforeUpdate 定时更新开始前发送预告（开关：设置里的 NotifyBeforeUpdate；默认关闭）。
// 同步发送：确保消息先于任何容器重启送出（代价是开跑前多等一两秒）。
func notifyBeforeUpdate(serviceContext *svc.ServiceContext, names []string) {
	settings := serviceContext.AutoUpdate.Get()
	if !settings.NotifyBeforeUpdate || len(names) == 0 {
		return
	}
	title, text := composeBeforeUpdateMessage(names, time.Now().Format("2006-01-02 15:04"))
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
	if len(s) > 120 {
		return s[:120] + "…"
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
