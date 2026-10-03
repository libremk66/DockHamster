package utiles

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	dockerBackend "github.com/docker/docker/api/types/backend"
	"github.com/libremk66/DockHamster/internal/module"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
)

// ── 迁移包导入：预检（dry-run）+ 执行 ────────────────────────────────────

// ReadManifestFromPackage 从 .tar.gz 包中流式读出 manifest.json（不解压整包）
func ReadManifestFromPackage(pkgPath string) (*module.Manifest, error) {
	f, err := os.Open(pkgPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("不是有效的 .tar.gz 迁移包: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, nerr := tr.Next()
		if nerr == io.EOF {
			break
		}
		if nerr != nil {
			return nil, nerr
		}
		// 防目录穿越：只接受包内的 manifest.json，且路径不含 ..
		if strings.Contains(hdr.Name, "..") {
			continue
		}
		if filepath.Base(hdr.Name) == "manifest.json" {
			var m module.Manifest
			if derr := json.NewDecoder(tr).Decode(&m); derr != nil {
				return nil, fmt.Errorf("manifest.json 解析失败: %w", derr)
			}
			if m.SchemaVersion > module.ManifestSchemaVersion {
				return nil, fmt.Errorf("迁移包格式版本 %d 高于当前支持（%d），请升级面板", m.SchemaVersion, module.ManifestSchemaVersion)
			}
			return &m, nil
		}
	}
	return nil, fmt.Errorf("包内没有找到 manifest.json")
}

// BuildImportPlan 生成导入预检（dry-run）：冲突、镜像可得性、缺失路径
func BuildImportPlan(svcCtx *svc.ServiceContext, pkgPath, pkgName string) (*module.ImportPlan, error) {
	m, err := ReadManifestFromPackage(pkgPath)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	svcCtx.DockerClient.NegotiateAPIVersion(ctx)

	// 现有容器：名字 & 端口占用
	containers, err := svcCtx.DockerClient.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	usedNames := map[string]bool{}
	usedPorts := map[string]string{} // "8080" → 容器名
	for _, c := range containers {
		name := strings.TrimPrefix(c.Names[0], "/")
		usedNames[name] = true
		for _, p := range c.Ports {
			if p.PublicPort > 0 {
				usedPorts[fmt.Sprint(p.PublicPort)] = name
			}
		}
		charName := func(n string) (string, bool) { return name, true }
		_ = charName
	}

	// 本地镜像
	images, err := svcCtx.DockerClient.ImageList(ctx, image.ListOptions{All: false})
	if err != nil {
		return nil, err
	}
	localByID := map[string]bool{}
	localByRef := map[string]bool{}
	for _, img := range images {
		localByID[img.ID] = true
		for _, t := range img.RepoTags {
			localByRef[t] = true
		}
	}
	// 包内镜像 ID 集合
	pkgImageByID := map[string]module.ManifestImage{}
	for _, mi := range m.Images {
		pkgImageByID[mi.ID] = mi
	}

	plan := &module.ImportPlan{
		File:        pkgName,
		PackageName: pkgName,
		Generator:   m.Generator,
		DiskFree:    diskFreeOrZero(),
	}
	var totalSize int64
	for _, mi := range m.Images {
		totalSize += mi.Size
	}
	plan.TotalSize = totalSize

	for _, c := range m.Containers {
		item := module.ImportPlanItem{
			Name:          c.Name,
			ImageRef:      c.ImageRef,
			ImageID:       c.ImageID,
			Privileged:    c.Privileged,
			WasRunning:    c.WasRunning,
			MountSuggest:  map[string]string{},
		}
		// 镜像可得性
		switch {
		case localByID[c.ImageID] || localByRef[c.ImageRef]:
			item.ImageSource = "local"
		case func() bool { mi, ok := pkgImageByID[c.ImageID]; if !ok { return false }; for _, tr := range mi.Transports { if tr.Type == "archive" { return true } }; return false }():
			item.ImageSource = "package"
		case func() bool { mi, ok := pkgImageByID[c.ImageID]; return ok && mi.Source == "registry" }():
			item.ImageSource = "registry"
		default:
			item.ImageSource = "missing"
		}
		// 名字冲突
		if usedNames[c.Name] {
			item.NameConflict = true
			item.SuggestedName = suggestFreeName(c.Name, usedNames)
		}
		// 端口冲突
		for _, p := range c.Ports {
			hostPort := extractHostPort(p)
			if hostPort == "" {
				continue
			}
			if owner, ok := usedPorts[hostPort]; ok {
				item.PortConflicts = append(item.PortConflicts, fmt.Sprintf("%s（被 %s 占用）", hostPort, owner))
			}
		}
		// 挂载路径缺失 + 同名目录建议
		for _, v := range c.Volumes {
			if v.Type != "bind" || !strings.HasPrefix(v.Source, "/") {
				continue
			}
			if _, serr := os.Stat(v.Source); serr != nil {
				item.MissingMounts = append(item.MissingMounts, v)
				if alt := suggestSimilarPath(v.Source); alt != "" {
					item.MountSuggest[v.Source] = alt
				}
			}
		}
		plan.Items = append(plan.Items, item)
	}
	if len(plan.Items) == 0 {
		plan.Warnings = append(plan.Warnings, "包内没有容器配方（只有镜像）")
	}
	return plan, nil
}

// ApplyImport 执行导入：按需 load 镜像 → 应用覆盖 → 建容器 → 可选启动
func ApplyImport(svcCtx *svc.ServiceContext, pkgPath, pkgName string, overrides []module.ImportItemOverride, autoCreateDirs, startAfter bool, taskID string) ([]string, error) {
	m, err := ReadManifestFromPackage(pkgPath)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	svcCtx.DockerClient.NegotiateAPIVersion(ctx)

	ovBy := map[string]module.ImportItemOverride{}
	for _, o := range overrides {
		ovBy[o.Name] = o
	}

	updateProgressFor(svcCtx, taskID, 2, "正在准备导入", pkgName, false)

	// ① 判断需要 load 哪些镜像（本地没有且包内提供的）
	images, ierr := svcCtx.DockerClient.ImageList(ctx, image.ListOptions{All: false})
	if ierr != nil {
		return nil, ierr
	}
	localByID := map[string]bool{}
	localByRef := map[string]bool{}
	for _, img := range images {
		localByID[img.ID] = true
		for _, t := range img.RepoTags {
			localByRef[t] = true
		}
	}
	needLoad := map[string]module.ManifestImage{} // imageID → manifest image（含 archive 记录）
	for _, c := range m.Containers {
		ov := ovBy[c.Name]
		if ov.Skip {
			continue
		}
		if localByID[c.ImageID] || localByRef[c.ImageRef] {
			continue
		}
		if mi, ok := findManifestImage(m, c.ImageID); ok {
			needLoad[c.ImageID] = mi
		}
	}

	// ② load 镜像（流式：只读包内 images/ 下的文件，逐个喂给 docker load）
	if len(needLoad) > 0 {
		loadFiles := map[string]string{} // 包内文件名 → imageID
		for id, mi := range needLoad {
			for _, tr := range mi.Transports {
				if tr.Type == "archive" && tr.File != "" {
					loadFiles[strings.TrimPrefix(tr.File, "images/")] = id
				}
			}
		}
		if len(loadFiles) > 0 {
			loaded, lerr := loadImagesFromPackage(svcCtx, pkgPath, loadFiles, taskID)
			if lerr != nil {
				return nil, lerr
			}
			for _, id := range loaded {
				localByID[id] = true
			}
		}
	}

	// ③ 逐个创建容器
	var results []string
	total := len(m.Containers)
	for i, c := range m.Containers {
		pct := 60 + i*38/max(1, total)
		ov := ovBy[c.Name]
		if ov.Skip {
			results = append(results, c.Name+"：已跳过")
			continue
		}
		name := c.Name
		if strings.TrimSpace(ov.NewName) != "" {
			name = strings.TrimSpace(ov.NewName)
		}
		updateProgressFor(svcCtx, taskID, pct, fmt.Sprintf("正在创建容器 %d/%d", i+1, total), name, false)

		var cc dockerBackend.ContainerCreateConfig
		if err := json.Unmarshal(c.Create, &cc); err != nil || cc.Config == nil {
			results = append(results, c.Name+"：配方解析失败，已跳过")
			continue
		}
		// 镜像可用性兜底：本地没有且包内没有 → 尝试从仓库拉
		if !localByID[c.ImageID] && !localByRef[c.ImageRef] {
			if mi, ok := findManifestImage(m, c.ImageID); ok && mi.Source == "registry" {
				if _, perr := svcCtx.DockerClient.ImagePull(ctx, c.ImageRef, image.PullOptions{}); perr != nil {
					results = append(results, fmt.Sprintf("%s：镜像 %s 本地缺失且拉取失败（%s）", c.Name, c.ImageRef, oneLine(perr.Error())))
					continue
				}
			} else {
				results = append(results, fmt.Sprintf("%s：镜像 %s 不可用（包内没有、本地没有、仓库未知）", c.Name, c.ImageRef))
				continue
			}
		}

		// 应用覆盖：名字 / 端口 / 挂载
		cc.Name = name
		if cc.HostConfig != nil {
			if len(ov.PortMap) > 0 {
				for port, binds := range cc.HostConfig.PortBindings {
					for bi := range binds {
						if np, ok := ov.PortMap[binds[bi].HostPort]; ok {
							binds[bi].HostPort = np
						}
					}
					cc.HostConfig.PortBindings[port] = binds
				}
			}
			if len(ov.MountMap) > 0 {
				for bi, bind := range cc.HostConfig.Binds {
					parts := strings.SplitN(bind, ":", 3)
					if len(parts) >= 2 {
						if np, ok := ov.MountMap[parts[0]]; ok {
							parts[0] = np
							cc.HostConfig.Binds[bi] = strings.Join(parts, ":")
						}
					}
				}
				for mi2 := range cc.HostConfig.Mounts {
					if cc.HostConfig.Mounts[mi2].Type == mount.TypeBind {
						if np, ok := ov.MountMap[cc.HostConfig.Mounts[mi2].Source]; ok {
							cc.HostConfig.Mounts[mi2].Source = np
						}
					}
				}
			}
		}
		// 缺失目录（可选自动创建）
		for _, v := range c.Volumes {
			if v.Type != "bind" || !strings.HasPrefix(v.Source, "/") {
				continue
			}
			src := v.Source
			if np, ok := ov.MountMap[src]; ok {
				src = np
			}
			if _, serr := os.Stat(src); serr != nil && (autoCreateDirs || ov.AutoCreateDirs) {
				if merr := os.MkdirAll(src, 0755); merr != nil {
					logx.Errorf("创建宿主目录 %s 失败: %v", src, merr)
				}
			}
		}

		if _, cerr := svcCtx.DockerClient.ContainerCreate(ctx, cc.Config, cc.HostConfig, cc.NetworkingConfig, nil, name); cerr != nil {
			results = append(results, fmt.Sprintf("%s：创建失败（%s）", name, oneLine(cerr.Error())))
			continue
		}
		if startAfter && c.WasRunning {
			if serr := svcCtx.DockerClient.ContainerStart(ctx, name, container.StartOptions{}); serr != nil {
				results = append(results, fmt.Sprintf("%s：已创建但启动失败（%s）", name, oneLine(serr.Error())))
				continue
			}
		}
		results = append(results, name+"：导入成功")
	}

	updateProgressFor(svcCtx, taskID, 100, "导入完成", strings.Join(results, "；"), true)
	logx.Infof("迁移包导入完成：%s", strings.Join(results, "；"))
	return results, nil
}

// loadImagesFromPackage 从包内按需流式读取镜像文件并 docker load
func loadImagesFromPackage(svcCtx *svc.ServiceContext, pkgPath string, want map[string]string, taskID string) ([]string, error) {
	f, err := os.Open(pkgPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)

	// 包内文件顺序不保证，先记录每个目标文件的偏移不可行 → 改为顺序扫描、命中即处理
	// （镜像文件通常是大头，逐个顺序读可接受）
	remaining := len(want)
	var loaded []string
	for {
		hdr, nerr := tr.Next()
		if nerr == io.EOF || remaining == 0 {
			break
		}
		if nerr != nil {
			return loaded, nerr
		}
		if strings.Contains(hdr.Name, "..") {
			continue
		}
		base := filepath.Base(hdr.Name)
		imageID, ok := want[base]
		if !ok || hdr.Typeflag != tar.TypeReg {
			continue
		}
		updateProgressFor(svcCtx, taskID, 20, "正在导入镜像", base, false)
		resp, lerr := svcCtx.DockerClient.ImageLoad(context.Background(), io.LimitReader(tr, hdr.Size), true)
		if lerr != nil {
			logx.Errorf("load 镜像 %s 失败: %v", base, lerr)
			remaining--
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		loaded = append(loaded, imageID)
		remaining--
		logx.Infof("已从迁移包导入镜像：%s", base)
	}
	return loaded, nil
}

// ── 小工具 ────────────────────────────────────────────────────────────────

func updateProgressFor(svcCtx *svc.ServiceContext, taskID string, pct int, msg, detail string, done bool) {
	if taskID == "" {
		return
	}
	svcCtx.UpdateProgress(taskID, svc.TaskProgress{TaskID: taskID, Percentage: pct, Name: "迁移导入", Message: msg, DetailMsg: detail, IsDone: done})
}

func findManifestImage(m *module.Manifest, imageID string) (module.ManifestImage, bool) {
	for _, mi := range m.Images {
		if mi.ID == imageID {
			return mi, true
		}
	}
	return module.ManifestImage{}, false
}

func diskFreeOrZero() int64 {
	v, _ := DiskFree()
	return v
}

// extractHostPort 从 "0.0.0.0:8080->80/tcp" 提取宿主端口
func extractHostPort(s string) string {
	i := strings.Index(s, "->")
	if i < 0 {
		return ""
	}
	left := s[:i]
	if j := strings.LastIndex(left, ":"); j >= 0 {
		return left[j+1:]
	}
	return ""
}

// suggestFreeName 名字冲突时给出可用建议名
func suggestFreeName(name string, used map[string]bool) string {
	for i := 2; i < 100; i++ {
		cand := fmt.Sprintf("%s-%d", name, i)
		if !used[cand] {
			return cand
		}
	}
	return name + "-imported"
}

// suggestSimilarPath 在常见挂载根目录下寻找同名目录，作为卷路径迁移建议
func suggestSimilarPath(src string) string {
	base := filepath.Base(src)
	roots := []string{"/media", "/mnt", "/home", "/data", "/volume1", "/vol1"}
	for _, root := range roots {
		if !strings.HasPrefix(src, root) {
			continue
		}
		// 在 root 下最多两层里找同名目录
		matches, _ := filepath.Glob(filepath.Join(root, "*", base))
		if len(matches) > 0 {
			return matches[0]
		}
		matches2, _ := filepath.Glob(filepath.Join(root, "*", "*", base))
		if len(matches2) > 0 {
			return matches2[0]
		}
	}
	return ""
}
