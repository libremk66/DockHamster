package utiles

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/google/uuid"
	"github.com/onlyLTY/dockerCopilot/internal/module"
	"github.com/onlyLTY/dockerCopilot/internal/svc"
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
		if strings.Contains(strings.ToLower(c.Image), "dockercopilot") {
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

	opts := UpdateOptions{
		SkipPull:        true, // 拉取在下面按镜像统一处理
		DelOldContainer: delOldContainer,
		DeleteOldImage:  settings.DeleteOldImage,
	}

	// 预生成任务 ID 并登记为"进行中"，供 status 接口实时展示
	active := make([]module.ActiveTask, 0, len(targets))
	for i := range targets {
		targets[i].taskID = uuid.New().String()
		active = append(active, module.ActiveTask{Name: targets[i].name, TaskID: targets[i].taskID})
	}
	serviceContext.AutoUpdateState.SetActive(active)
	defer serviceContext.AutoUpdateState.ClearActive()

	// 按镜像分组：同一镜像只拉取一次
	groups := make(map[string][]target)
	for _, t := range targets {
		groups[t.pullRef] = append(groups[t.pullRef], t)
	}
	for pullRef, group := range groups {
		logx.Infof("自动更新：拉取 %s（%d 个容器共用）", pullRef, len(group))
		if err := PullImageByRef(serviceContext, pullRef); err != nil {
			logx.Errorf("自动更新：拉取 %s 失败，跳过本组：%v", pullRef, err)
			for _, t := range group {
				result.Failed = append(result.Failed, module.AutoRunFailure{Name: t.name, Error: "拉取失败: " + oneLine(err.Error())})
				serviceContext.AutoUpdateState.SetContainer(t.name, false, "拉取失败: "+oneLine(err.Error()))
			}
			continue
		}
		for _, t := range group {
			logx.Infof("自动更新：开始更新容器 %s", t.name)
			cleaned, err := updateContainerCore(serviceContext, t.id, t.name, pullRef, opts, t.taskID)
			if err != nil {
				logx.Errorf("自动更新：容器 %s 更新失败：%v", t.name, err)
				result.Failed = append(result.Failed, module.AutoRunFailure{Name: t.name, Error: oneLine(err.Error())})
				serviceContext.AutoUpdateState.SetContainer(t.name, false, oneLine(err.Error()))
			} else {
				logx.Infof("自动更新：容器 %s 更新完成", t.name)
				result.Updated = append(result.Updated, t.name)
				if cleaned {
					result.CleanedImages++
				}
				serviceContext.AutoUpdateState.SetContainer(t.name, true, "更新成功")
			}
		}
	}

	result.DurationSec = float64(int(time.Since(start).Seconds()*10)) / 10
	serviceContext.AutoUpdateState.AddRun(result)
	logx.Infof("自动更新完成：%d 成功 / %d 失败 / 清理旧镜像 %d", len(result.Updated), len(result.Failed), result.CleanedImages)
	notifyAutoUpdate(serviceContext, result)
}

// notifyAutoUpdate 按设置发送飞书简报（有内容才发：有更新成功 或 有失败）
func notifyAutoUpdate(serviceContext *svc.ServiceContext, r module.AutoUpdateRunResult) {
	settings := serviceContext.AutoUpdate.Get()
	if settings.FeishuWebhook == "" {
		return
	}
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
	results := module.SendNotify(settings.Notify, title, text)
	for _, res := range results {
		if res.OK {
			logx.Infof("通知已发送: %s", res.Channel)
		} else {
			logx.Errorf("通知发送失败 %s: %s", res.Channel, res.Error)
		}
	}
}

// composeAutoUpdateMessage 生成通知标题与正文（正文不含标题行，各渠道自行拼接）
func composeAutoUpdateMessage(r module.AutoUpdateRunResult) (title, text string) {
	title = "🔄 DockerCopilot 自动更新 " + r.Time
	var b strings.Builder
	if len(r.Updated) > 0 {
		b.WriteString(fmt.Sprintf("✅ 已更新 %d 个：%s\n", len(r.Updated), strings.Join(r.Updated, "、")))
	}
	if r.CleanedImages > 0 {
		b.WriteString(fmt.Sprintf("🗑️ 清理旧镜像 %d 个\n", r.CleanedImages))
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

// resolvePullRef 优先使用镜像的正式 RepoTag（避免用镜像 ID/摘要作拉取目标）
func resolvePullRef(serviceContext *svc.ServiceContext, imageID string, fallback string) string {
	images, err := GetImagesList(serviceContext)
	if err == nil {
		for _, img := range images {
			if img.ID == imageID && len(img.RepoTags) > 0 {
				return img.RepoTags[0]
			}
		}
	}
	return fallback
}
