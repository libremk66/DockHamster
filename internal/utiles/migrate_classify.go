package utiles

import (
	"context"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/libremk66/DockHamster/internal/module"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
)

// ── 镜像体检（迁移前扫描：哪些镜像换台机器会丢）──────────────────────────
// 分类规则（可解释、无需联网）：
//   registry   有 RepoDigests（是从仓库拉下来的）→ 目标机可直接 pull
//   local-build 有 tag 但没有 digest → 本地构建/从未推送，必须打包搬运
//   dangling   没有任何 tag → 连名字都没有，必须先打 tag 才能搬运

type ImageReportItem struct {
	ID         string   `json:"id"`
	ShortID    string   `json:"shortId"`
	Refs       []string `json:"refs"`
	Source     string   `json:"source"` // registry | local-build | dangling
	Size       int64    `json:"size"`
	UsedBy     []string `json:"usedBy"`
	InUse      bool     `json:"inUse"`
	Digests    []string `json:"digests,omitempty"`
	SnapshotOf string   `json:"snapshotOf,omitempty"` // 若该镜像带 dh-snap/ 标签（受保护）
}

type ImageReport struct {
	Total      int               `json:"total"`
	Registry   int               `json:"registry"`
	LocalBuild int               `json:"localBuild"`
	Dangling   int               `json:"dangling"`
	AtRisk     int               `json:"atRisk"` // local-build + dangling（换机即丢）
	Images     []ImageReportItem `json:"images"`
	DiskFree   int64             `json:"diskFree"`
	Prefix     string            `json:"snapshotPrefix"`
}

// ClassifyImages 扫描全部镜像并分类（供迁移页"镜像体检"使用）
func ClassifyImages(serviceContext *svc.ServiceContext, snapshotPrefix string) (*ImageReport, error) {
	ctx := context.Background()
	serviceContext.DockerClient.NegotiateAPIVersion(ctx)

	containers, err := serviceContext.DockerClient.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	usedBy := map[string][]string{}
	for _, c := range containers {
		usedBy[c.ImageID] = append(usedBy[c.ImageID], strings.TrimPrefix(c.Names[0], "/"))
	}

	images, err := serviceContext.DockerClient.ImageList(ctx, image.ListOptions{All: false})
	if err != nil {
		return nil, err
	}

	report := &ImageReport{Prefix: snapshotPrefix}
	for _, img := range images {
		refs := []string{}
		for _, t := range img.RepoTags {
			if t != "<none>:<none>" {
				refs = append(refs, t)
			}
		}
		item := ImageReportItem{
			ID:      img.ID,
			ShortID: shortImageID(img.ID),
			Refs:    refs,
			Size:    img.Size,
			UsedBy:  usedBy[img.ID],
			Digests: img.RepoDigests,
		}
		item.InUse = len(item.UsedBy) > 0

		hasSnapshotTag := false
		for _, t := range refs {
			if IsSnapshotRef(t, snapshotPrefix) {
				hasSnapshotTag = true
				item.SnapshotOf = strings.TrimPrefix(t, snapshotPrefix+"/")
				break
			}
		}

		switch {
		case len(refs) == 0:
			item.Source = "dangling"
			report.Dangling++
		case len(img.RepoDigests) > 0:
			item.Source = "registry"
			report.Registry++
		default:
			item.Source = "local-build"
			report.LocalBuild++
		}
		if item.Source == "dangling" || (item.Source == "local-build" && !hasSnapshotTag) {
			report.AtRisk++
		}
		report.Images = append(report.Images, item)
		report.Total++
	}
	// 有风险的排前面，其次按大小
	sort.SliceStable(report.Images, func(i, j int) bool {
		rank := func(s string) int {
			switch s {
			case "dangling":
				return 0
			case "local-build":
				return 1
			default:
				return 2
			}
		}
		if rank(report.Images[i].Source) != rank(report.Images[j].Source) {
			return rank(report.Images[i].Source) < rank(report.Images[j].Source)
		}
		return report.Images[i].Size > report.Images[j].Size
	})
	report.DiskFree, _ = DiskFree()
	return report, nil
}

// TagImageForMigration 给镜像打一个"可搬运"的标签（悬空/本地构建镜像导出前必须有名字）
// 返回实际使用的引用。
func TagImageForMigration(serviceContext *svc.ServiceContext, imageID string, ref string) (string, error) {
	ctx := context.Background()
	serviceContext.DockerClient.NegotiateAPIVersion(ctx)
	ref = sanitizeMigrateRef(ref)
	if ref == "" {
		return "", errInvalidRef()
	}
	// 已存在同名且指向其他镜像 → 拒绝覆盖（与快照同样的安全策略）
	if existing, _, ierr := serviceContext.DockerClient.ImageInspectWithRaw(ctx, ref); ierr == nil {
		if existing.ID != imageID {
			return "", errRefTaken(ref)
		}
		return ref, nil
	}
	if err := serviceContext.DockerClient.ImageTag(ctx, imageID, ref); err != nil {
		return "", err
	}
	logx.Infof("迁移打 tag：%s → %s", shortImageID(imageID), ref)
	return ref, nil
}

// EnsureExportableRef 导出前保证镜像有可用引用：悬空镜像自动打 dh-migrate/<短ID> 标签
func EnsureExportableRef(serviceContext *svc.ServiceContext, imageID string, refs []string) (ref string, autoTagged bool, err error) {
	for _, r := range refs {
		if r != "" && r != "<none>:<none>" && !IsSnapshotRef(r, SnapshotPrefixDefault) {
			return r, false, nil
		}
	}
	// 只有快照标签也算"有名字"（快照本身可搬运），否则自动打迁移标签
	for _, r := range refs {
		if IsSnapshotRef(r, SnapshotPrefixDefault) {
			return r, false, nil
		}
	}
	ref, err = TagImageForMigration(serviceContext, imageID, "dh-migrate/"+shortImageID(imageID)+":latest")
	if err != nil {
		return "", false, err
	}
	return ref, true, nil
}

// ── 小工具 ────────────────────────────────────────────────────────────────

func shortImageID(id string) string {
	s := strings.TrimPrefix(id, "sha256:")
	if len(s) > 12 {
		s = s[:12]
	}
	return s
}

// sanitizeMigrateRef 规范化用户输入的镜像引用（小写、去空格、合法字符）
func sanitizeMigrateRef(ref string) string {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if ref == "" {
		return ""
	}
	// 仓库名不允许大写与空格；tag 允许 [A-Za-z0-9_.-]
	parts := strings.SplitN(ref, ":", 2)
	repo := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '/', r == '.', r == '_', r == '-':
			return r
		default:
			return -1
		}
	}, parts[0])
	repo = strings.Trim(repo, "/")
	if repo == "" {
		return ""
	}
	if len(parts) == 2 {
		tag := strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
				return r
			default:
				return -1
			}
		}, parts[1])
		if tag == "" {
			tag = "latest"
		}
		return repo + ":" + tag
	}
	return repo + ":latest"
}

type refError struct{ msg string }

func (e refError) Error() string { return e.msg }

func errInvalidRef() error { return refError{msg: "镜像引用不合法"} }
func errRefTaken(ref string) error {
	return refError{msg: "标签 " + ref + " 已被其他镜像使用（拒绝覆盖）"}
}

var _ = module.ManifestSchemaVersion
