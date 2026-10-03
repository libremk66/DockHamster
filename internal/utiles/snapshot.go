package utiles

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/libremk66/DockHamster/internal/module"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
)

// ── 镜像快照 ─────────────────────────────────────────────────────────────
// 快照 = 给"更新前的旧镜像"打上独立命名空间的本地 tag（默认 dh-snap/<名字>:<日期>-<时分>）。
// 设计要点：
//  1. 打上 tag 后，旧镜像不再"悬空" → 自动清理的三重条件之②（无 RepoTag）不满足 → 天然受保护
//  2. 独立命名空间便于识别与批量管理，也不会干扰"更新检测"的本地 digest 比对
//  3. docker tag 到已存在的 tag 会**静默搬走**那个 tag（旧快照会丢）→ 冲突时自动加序号，绝不覆盖

const (
	SnapshotPrefixDefault   = "dh-snap"
	SnapshotTemplateDefault = "{name}:{date}-{time}"
	SnapshotKeepDefault     = 3
	// 单次为同一镜像打快照时的防覆盖尝试上限
	snapshotConflictLimit = 20
)

// SnapshotOptions 快照生成参数（来自自动更新设置）
type SnapshotOptions struct {
	Prefix   string
	Template string
	Keep     int
}

func DefaultSnapshotOptions() SnapshotOptions {
	return SnapshotOptions{Prefix: SnapshotPrefixDefault, Template: SnapshotTemplateDefault, Keep: SnapshotKeepDefault}
}

// Normalize 补全空值，保证任何来源的配置都能安全使用
func (o SnapshotOptions) Normalize() SnapshotOptions {
	if strings.TrimSpace(o.Prefix) == "" {
		o.Prefix = SnapshotPrefixDefault
	}
	o.Prefix = strings.ToLower(strings.Trim(o.Prefix, "/"))
	if strings.TrimSpace(o.Template) == "" {
		o.Template = SnapshotTemplateDefault
	}
	if o.Keep <= 0 {
		o.Keep = SnapshotKeepDefault
	}
	return o
}

// SnapshotInfo 一个快照（= 一条 dh-snap/ 标签）
type SnapshotInfo struct {
	Ref       string   `json:"ref"`       // dh-snap/songhamster:20261003-0400
	ImageID   string   `json:"imageId"`   // sha256:...
	ShortID   string   `json:"shortId"`   // 前 12 位
	BaseName  string   `json:"baseName"`  // songhamster（按名字分组的键）
	Tag       string   `json:"tag"`       // 20261003-0400
	Size      int64    `json:"size"`      // 字节
	Created   int64    `json:"created"`   // 镜像创建时间（unix）
	UsedBy    []string `json:"usedBy"`    // 引用该镜像的容器名
	InUse     bool     `json:"inUse"`     // 是否仍被容器使用
	TimeLabel string   `json:"timeLabel"` // 从 tag 里解析出的 2026-10-03 04:00
}

// SnapshotOptionsFromSettings 从自动更新设置构造快照参数（自动补默认值）
func SnapshotOptionsFromSettings(settings module.AutoUpdateSettings) SnapshotOptions {
	return SnapshotOptions{
		Prefix:   settings.SnapshotPrefix,
		Template: settings.SnapshotTemplate,
		Keep:     settings.SnapshotKeep,
	}.Normalize()
}

// IsSnapshotRef 判断某个 RepoTag 是否属于快照命名空间
func IsSnapshotRef(ref string, prefix string) bool {
	if prefix == "" {
		prefix = SnapshotPrefixDefault
	}
	return strings.HasPrefix(ref, strings.ToLower(prefix)+"/")
}

var invalidNameChars = regexp.MustCompile(`[^a-z0-9._-]+`)

// sanitizeSnapshotName 把镜像/容器名压成合法且可读的仓库名片段（小写，只保留 [a-z0-9._-]）
func sanitizeSnapshotName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = invalidNameChars.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-._")
	if name == "" {
		name = "image"
	}
	return name
}

// snapshotBaseName 计算快照的"名字"部分：优先镜像自身的仓库短名，其次容器名
func snapshotBaseName(imageRepoTags []string, prefix string, containerName string) string {
	for _, tag := range imageRepoTags {
		if IsSnapshotRef(tag, prefix) {
			continue // 不拿快照 tag 当名字来源
		}
		repo := tag
		if i := strings.LastIndex(repo, ":"); i > strings.LastIndex(repo, "/") {
			repo = repo[:i] // 去掉 :tag
		}
		if i := strings.LastIndex(repo, "/"); i >= 0 {
			repo = repo[i+1:] // 去掉 registry/namespace，只留最后一段
		}
		if repo != "" && repo != "<none>" {
			return sanitizeSnapshotName(repo)
		}
	}
	return sanitizeSnapshotName(containerName)
}

// renderSnapshotRef 渲染快照全名；{name} {date} {time} {id}
func renderSnapshotRef(opts SnapshotOptions, baseName string, imageID string) string {
	opts = opts.Normalize()
	now := time.Now()
	shortID := strings.TrimPrefix(imageID, "sha256:")
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	tagPart := opts.Template
	tagPart = strings.ReplaceAll(tagPart, "{name}", baseName)
	tagPart = strings.ReplaceAll(tagPart, "{date}", now.Format("20060102"))
	tagPart = strings.ReplaceAll(tagPart, "{time}", now.Format("1504"))
	tagPart = strings.ReplaceAll(tagPart, "{id}", shortID)
	ref := opts.Prefix + "/" + strings.Trim(tagPart, "/")
	// tag 部分合法字符：字母数字与 _ . -
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		repo, tag := ref[:i], ref[i+1:]
		tag = regexp.MustCompile(`[^A-Za-z0-9_.-]+`).ReplaceAllString(tag, "-")
		ref = repo + ":" + tag
	}
	return ref
}

// CreateSnapshot 给指定镜像打一个快照 tag；返回实际使用的 ref（冲突时自动加序号）。
// 已存在的 tag 若指向同一镜像则直接复用，指向别的镜像则绝不覆盖（加 -2/-3…）。
func CreateSnapshot(serviceContext *svc.ServiceContext, imageID string, containerName string, opts SnapshotOptions) (string, error) {
	opts = opts.Normalize()
	ctx := context.Background()
	serviceContext.DockerClient.NegotiateAPIVersion(ctx)

	inspected, _, err := serviceContext.DockerClient.ImageInspectWithRaw(ctx, imageID)
	if err != nil {
		return "", fmt.Errorf("查询待快照镜像失败: %w", err)
	}
	baseName := snapshotBaseName(inspected.RepoTags, opts.Prefix, containerName)

	for attempt := 0; attempt < snapshotConflictLimit; attempt++ {
		ref := renderSnapshotRef(opts, baseName, imageID)
		if attempt > 0 {
			// 冲突时加序号：dh-snap/name:20261003-0400-2
			if i := strings.LastIndex(ref, ":"); i > 0 {
				ref = fmt.Sprintf("%s-%d", ref, attempt+1)
			}
		}
		// 该 ref 是否已被占用？
		if existing, _, ierr := serviceContext.DockerClient.ImageInspectWithRaw(ctx, ref); ierr == nil {
			if existing.ID == inspected.ID {
				return ref, nil // 已指向同一镜像，复用
			}
			logx.Infof("快照名 %s 已被其他镜像占用，尝试下一个序号", ref)
			continue
		}
		if err := serviceContext.DockerClient.ImageTag(ctx, imageID, ref); err != nil {
			return "", fmt.Errorf("打快照 tag 失败: %w", err)
		}
		logx.Infof("已为镜像 %s 创建快照 %s", imageID, ref)
		return ref, nil
	}
	return "", fmt.Errorf("快照名冲突过多，放弃创建")
}

// ListSnapshots 列出全部快照（按命名空间识别），并标注是否仍被容器引用
func ListSnapshots(serviceContext *svc.ServiceContext, prefix string) ([]SnapshotInfo, error) {
	if prefix == "" {
		prefix = SnapshotPrefixDefault
	}
	ctx := context.Background()
	serviceContext.DockerClient.NegotiateAPIVersion(ctx)

	containers, err := serviceContext.DockerClient.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	usedBy := map[string][]string{}
	for _, c := range containers {
		name := strings.TrimPrefix(c.Names[0], "/")
		usedBy[c.ImageID] = append(usedBy[c.ImageID], name)
	}

	images, err := serviceContext.DockerClient.ImageList(ctx, image.ListOptions{All: false})
	if err != nil {
		return nil, err
	}
	var result []SnapshotInfo
	for _, img := range images {
		for _, repoTag := range img.RepoTags {
			if !IsSnapshotRef(repoTag, prefix) {
				continue
			}
			rest := strings.TrimPrefix(repoTag, strings.ToLower(prefix)+"/")
			namePart, tagPart := rest, ""
			if i := strings.LastIndex(rest, ":"); i > 0 {
				namePart, tagPart = rest[:i], rest[i+1:]
			}
			info := SnapshotInfo{
				Ref:      repoTag,
				ImageID:  img.ID,
				Size:     img.Size,
				Created:  img.Created,
				BaseName: namePart,
				Tag:      tagPart,
				UsedBy:   usedBy[img.ID],
			}
			info.InUse = len(info.UsedBy) > 0
			info.ShortID = strings.TrimPrefix(img.ID, "sha256:")
			if len(info.ShortID) > 12 {
				info.ShortID = info.ShortID[:12]
			}
			info.TimeLabel = parseSnapshotTimeLabel(tagPart)
			result = append(result, info)
		}
	}
	// 新的在前
	sort.Slice(result, func(i, j int) bool { return result[i].Ref > result[j].Ref })
	return result, nil
}

// parseSnapshotTimeLabel 从 tag 后缀解析时间标签（20261003-0400 → 2026-10-03 04:00）
func parseSnapshotTimeLabel(tagPart string) string {
	re := regexp.MustCompile(`(\d{8})-(\d{4})`)
	m := re.FindStringSubmatch(tagPart)
	if len(m) != 3 {
		return ""
	}
	if t, err := time.Parse("200601021504", m[1]+m[2]); err == nil {
		return t.Format("2006-01-02 15:04")
	}
	return ""
}

// PruneSnapshots 按"每个镜像保留最近 keep 个"清理超量快照；仍被容器引用的不删。
func PruneSnapshots(serviceContext *svc.ServiceContext, prefix string, keep int) ([]string, error) {
	if keep <= 0 {
		keep = SnapshotKeepDefault
	}
	snaps, err := ListSnapshots(serviceContext, prefix)
	if err != nil {
		return nil, err
	}
	grouped := map[string][]SnapshotInfo{}
	for _, s := range snaps {
		grouped[s.BaseName] = append(grouped[s.BaseName], s)
	}
	var removed []string
	for _, list := range grouped {
		sort.Slice(list, func(i, j int) bool { return list[i].Ref > list[j].Ref }) // 新的在前
		if len(list) <= keep {
			continue
		}
		for _, s := range list[keep:] {
			if s.InUse {
				logx.Infof("快照 %s 仍被容器 %v 使用，跳过清理", s.Ref, s.UsedBy)
				continue
			}
			if _, err := serviceContext.DockerClient.ImageRemove(context.Background(), s.Ref, image.RemoveOptions{}); err != nil {
				logx.Errorf("清理快照 %s 失败: %v", s.Ref, err)
				continue
			}
			removed = append(removed, s.Ref)
		}
	}
	return removed, nil
}

// RemoveSnapshots 手动删除指定快照（拒绝删除被容器使用的）
func RemoveSnapshots(serviceContext *svc.ServiceContext, refs []string, prefix string) (removed []string, failed map[string]string) {
	failed = map[string]string{}
	snaps, err := ListSnapshots(serviceContext, prefix)
	if err != nil {
		for _, r := range refs {
			failed[r] = err.Error()
		}
		return nil, failed
	}
	index := map[string]SnapshotInfo{}
	for _, s := range snaps {
		index[s.Ref] = s
	}
	for _, ref := range refs {
		s, ok := index[ref]
		if !ok {
			failed[ref] = "不是有效的快照引用"
			continue
		}
		if s.InUse {
			failed[ref] = "仍被容器使用，拒绝删除"
			continue
		}
		if _, err := serviceContext.DockerClient.ImageRemove(context.Background(), ref, image.RemoveOptions{}); err != nil {
			failed[ref] = err.Error()
			continue
		}
		removed = append(removed, ref)
	}
	return removed, failed
}

// SnapshotStats 快照数量与占用（跨镜像去重，避免共享层重复计数）
func SnapshotStats(serviceContext *svc.ServiceContext, prefix string) (count int, sizeBytes int64, err error) {
	snaps, err := ListSnapshots(serviceContext, prefix)
	if err != nil {
		return 0, 0, err
	}
	seen := map[string]bool{}
	for _, s := range snaps {
		count++
		if !seen[s.ImageID] {
			seen[s.ImageID] = true
			sizeBytes += s.Size
		}
	}
	return count, sizeBytes, nil
}

// DiskFree 返回 "/"（容器根文件系统 = 宿主 /var/lib/docker 所在盘）的可用字节数
func DiskFree() (int64, error) {
	return diskFree("/")
}

// ── 旧镜像处置（更新流程调用）────────────────────────────────────────────

// OldImageOutcome 旧镜像处置结果
type OldImageOutcome struct {
	Cleaned     bool   `json:"cleaned"`
	SnapshotRef string `json:"snapshotRef,omitempty"`
	SnapshotErr string `json:"snapshotErr,omitempty"`
}

// HandleOldImage 按策略处置更新后的旧镜像：
//
//	clean    → 走三重条件安全清理（现有行为）
//	snapshot → 打快照 tag（天然免于清理）+ 按保留数量清理超量快照
//	keep     → 什么都不做
func HandleOldImage(serviceContext *svc.ServiceContext, policy string, containerName string, oldImageID string, newImageID string, opts SnapshotOptions) OldImageOutcome {
	outcome := OldImageOutcome{}
	if oldImageID == "" || oldImageID == newImageID {
		return outcome
	}
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "clean":
		cleaned, err := CleanupOldImage(serviceContext, oldImageID, newImageID, true)
		if err != nil {
			logx.Errorf("清理旧镜像失败(不影响更新): %v", err)
		}
		outcome.Cleaned = cleaned
	case "snapshot":
		ref, err := CreateSnapshot(serviceContext, oldImageID, containerName, opts)
		if err != nil {
			// 打快照失败不阻塞更新主流程；旧镜像此时一般已悬空，交给人工处理
			logx.Errorf("打快照失败(不影响更新): %v", err)
			outcome.SnapshotErr = err.Error()
			return outcome
		}
		outcome.SnapshotRef = ref
		if removed, perr := PruneSnapshots(serviceContext, opts.Normalize().Prefix, opts.Normalize().Keep); perr != nil {
			logx.Errorf("清理超量快照失败(不影响更新): %v", perr)
		} else if len(removed) > 0 {
			logx.Infof("按保留策略清理了 %d 个旧快照: %v", len(removed), removed)
		}
	default: // keep / 其它
	}
	return outcome
}

// MergePolicies 多容器共用镜像时的策略合并（保留优先：snapshot > keep > clean）
func MergePolicies(policies []string) string {
	rank := map[string]int{"clean": 0, "keep": 1, "snapshot": 2}
	best, bestRank := "clean", -1
	for _, p := range policies {
		p = strings.ToLower(strings.TrimSpace(p))
		r, ok := rank[p]
		if !ok {
			continue
		}
		if r > bestRank {
			best, bestRank = p, r
		}
	}
	return best
}

// FormatBytes 人类可读大小（导出给 API 层使用）
func FormatBytes(n int64) string { return humanBytes(n) }

// ParseRefsParam 解析逗号分隔的快照引用参数
func ParseRefsParam(raw string) []string {
	var refs []string
	for _, p := range strings.Split(raw, ",") {
		if s := strings.TrimSpace(p); s != "" {
			refs = append(refs, s)
		}
	}
	return refs
}

// snapshotSortKey 供外部排序使用（当前实现按 ref 字符串倒序即可，保留占位）
func snapshotSortKey(s SnapshotInfo) string {
	return strconv.Itoa(int(s.Created))
}
