package utiles

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	dockerBackend "github.com/docker/docker/api/types/backend"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/libremk66/DockHamster/internal/config"
	"github.com/libremk66/DockHamster/internal/module"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
)

// ── 迁移包导出 ───────────────────────────────────────────────────────────
// 产物：/data/exports/<时间戳>.tar.gz，内含
//
//	manifest.json / images/*.tar.gz / compose.yaml / import.sh / README.txt / checksums.txt
//
// 包自带"无面板导入通道"（import.sh + README 场景指引），目标机没装面板也能还原。

const (
	exportsDir = "/data/exports"
	importsDir = "/data/imports"
)

// ExportOptions 导出参数
type ExportOptions struct {
	Containers    []string `json:"containers"`    // 容器名列表；空 = 全部容器
	IncludeImages bool     `json:"includeImages"` // 是否打包镜像文件（false = 只导配方）
	Compress      bool     `json:"compress"`      // 镜像文件是否 gzip
	RedactEnv     bool     `json:"redactEnv"`     // 环境变量脱敏（默认 true）
	LocalImages   []string `json:"localImages"`   // 额外指定要导出的本地镜像 ID（不依赖容器）
	Note          string   `json:"note,omitempty"`
}

func exportsDirPath() string { return exportsDir }

// ListExportPackages 列出已生成的迁移包
func ListExportPackages() ([]module.ExportTaskInfo, error) {
	entries, err := os.ReadDir(exportsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []module.ExportTaskInfo{}, nil
		}
		return nil, err
	}
	var out []module.ExportTaskInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.gz") {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		out = append(out, module.ExportTaskInfo{File: e.Name(), Size: info.Size(), CreatedAt: info.ModTime().Unix()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// ExportPackagePath 解析并校验导出包路径（防目录穿越）
func ExportPackagePath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) || !strings.HasSuffix(name, ".tar.gz") {
		return "", fmt.Errorf("非法文件名")
	}
	return filepath.Join(exportsDir, name), nil
}

// ImportPackagePath 解析并校验导入包路径
func ImportPackagePath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) || !strings.HasSuffix(name, ".tar.gz") {
		return "", fmt.Errorf("非法文件名")
	}
	return filepath.Join(importsDir, name), nil
}

func updateProgress(svcCtx *svc.ServiceContext, taskID string, pct int, msg, detail string, done bool) {
	if taskID == "" {
		return
	}
	p := svc.TaskProgress{TaskID: taskID, Percentage: pct, Name: "迁移导出", Message: msg, DetailMsg: detail, IsDone: done}
	svcCtx.UpdateProgress(taskID, p)
}

// ExportPackage 导出迁移包（阻塞执行，进度走 TaskProgress）
func ExportPackage(svcCtx *svc.ServiceContext, opts ExportOptions, taskID string) (string, error) {
	ctx := context.Background()
	svcCtx.DockerClient.NegotiateAPIVersion(ctx)

	if err := os.MkdirAll(exportsDir, 0755); err != nil {
		return "", err
	}

	// ① 收集容器
	updateProgress(svcCtx, taskID, 3, "正在收集容器信息", "", false)
	list, err := svcCtx.DockerClient.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return "", err
	}
	want := map[string]bool{}
	for _, n := range opts.Containers {
		want[n] = true
	}
	all := len(want) == 0

	type target struct{ id, name string }
	var targets []target
	for _, c := range list {
		name := strings.TrimPrefix(c.Names[0], "/")
		if all || want[name] {
			targets = append(targets, target{id: c.ID, name: name})
		}
	}
	if len(targets) == 0 {
		return "", fmt.Errorf("没有匹配的容器")
	}

	manifest := module.Manifest{
		SchemaVersion: module.ManifestSchemaVersion,
		Generator: module.ManifestGenerator{
			App: "DockHamster", Version: config.Version, Host: hostname(), CreatedAt: time.Now(),
		},
	}
	imageSet := map[string]bool{} // imageID → 需要导出

	for _, t := range targets {
		inspected, ierr := svcCtx.DockerClient.ContainerInspect(ctx, t.id)
		if ierr != nil {
			logx.Errorf("导出：inspect %s 失败: %v", t.name, ierr)
			continue
		}
		cfg := inspected.Config
		if cfg == nil {
			continue
		}
		// 环境变量脱敏（默认）：只保留 KEY，值换成 ****
		exportCfg := *cfg
		if opts.RedactEnv {
			env := make([]string, 0, len(cfg.Env))
			for _, kv := range cfg.Env {
				if i := strings.Index(kv, "="); i > 0 {
					k := kv[:i]
					if isSensitiveKey(k) {
						env = append(env, k+"=****（已脱敏）")
						continue
					}
				}
				env = append(env, kv)
			}
			exportCfg.Env = env
		}
		exportCfg.Hostname = ""
		hcfg := inspected.HostConfig
		if hcfg == nil {
			hcfg = &container.HostConfig{}
		}
		nc := &network.NetworkingConfig{EndpointsConfig: inspected.NetworkSettings.Networks}
		createCfg := dockerBackend.ContainerCreateConfig{Config: &exportCfg, HostConfig: hcfg, NetworkingConfig: nc, Name: t.name}
		raw, merr := json.Marshal(createCfg)
		if merr != nil {
			return "", merr
		}

		mc := module.ManifestContainer{
			Name:       t.name,
			ImageRef:   cfg.Image,
			ImageID:    inspected.Image,
			WasRunning: inspected.State != nil && inspected.State.Running,
			Create:     raw,
			Privileged: hcfg.Privileged,
		}
		for _, m := range inspected.Mounts {
			mc.Volumes = append(mc.Volumes, module.ManifestVolume{
				Type: string(m.Type), Source: m.Source, Target: m.Destination, ReadOnly: !m.RW,
			})
		}
		for netName := range inspected.NetworkSettings.Networks {
			mc.Networks = append(mc.Networks, netName)
		}
		for port, bindings := range hcfg.PortBindings {
			for _, b := range bindings {
				mc.Ports = append(mc.Ports, fmt.Sprintf("%s:%s->%s", b.HostIP, b.HostPort, port))
			}
		}
		sort.Strings(mc.Ports)
		manifest.Containers = append(manifest.Containers, mc)
		imageSet[inspected.Image] = true
	}
	for _, id := range opts.LocalImages {
		imageSet[id] = true
	}
	if len(manifest.Containers) == 0 && len(imageSet) == 0 {
		return "", fmt.Errorf("没有可导出的内容")
	}

	// ② 镜像清单 + 待导出镜像
	report, err := ClassifyImages(svcCtx, SnapshotPrefixDefault)
	if err != nil {
		return "", err
	}
	byID := map[string]ImageReportItem{}
	for _, it := range report.Images {
		byID[it.ID] = it
	}
	var toExport []ImageReportItem
	var totalBytes int64
	for id := range imageSet {
		it, ok := byID[id]
		if !ok {
			continue
		}
		toExport = append(toExport, it)
		totalBytes += it.Size
	}
	sort.Slice(toExport, func(i, j int) bool { return toExport[i].Size > toExport[j].Size })

	// 磁盘空间预检
	free, _ := DiskFree()
	if opts.IncludeImages && free < totalBytes*13/10+64*1024*1024 {
		return "", fmt.Errorf("磁盘空间不足：需要约 %s，可用 %s", humanBytes(totalBytes*13/10), humanBytes(free))
	}

	// ③ staging 目录
	pkgName := "export-" + time.Now().Format("20060102-1504")
	staging := filepath.Join(exportsDir, ".staging-"+pkgName)
	if err := os.MkdirAll(filepath.Join(staging, "images"), 0755); err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)

	// ④ 导出镜像（docker save → 可选 gzip，流式写盘 + 字节进度）
	var savedBytes int64
	for i, it := range toExport {
		ref, autoTagged, rerr := EnsureExportableRef(svcCtx, it.ID, it.Refs)
		if rerr != nil {
			logx.Errorf("导出：镜像 %s 无可用引用且自动打标签失败: %v", it.ShortID, rerr)
			continue
		}
		if autoTagged {
			it.Refs = append(it.Refs, ref)
		}
		fileName := sanitizeFileStem(ref) + ".tar"
		if opts.Compress {
			fileName += ".gz"
		}
		rec := module.ImageTransportRecord{Type: "archive", File: filepath.Join("images", fileName)}
		if opts.IncludeImages {
			pctBase := 8 + i*62/max(1, len(toExport))
			pctSpan := 62 / max(1, len(toExport))
			var written int64
			sum, werr := saveImageToFile(svcCtx, ref, filepath.Join(staging, "images", fileName), opts.Compress, func(n int64) {
				written += n
				overall := pctBase
				if it.Size > 0 {
					overall = pctBase + int(float64(pctSpan)*float64(written)/float64(it.Size))
				}
				updateProgress(svcCtx, taskID, min95(overall), fmt.Sprintf("正在导出镜像 %d/%d", i+1, len(toExport)),
					fmt.Sprintf("%s · %s / %s", ref, humanBytes(written), humanBytes(it.Size)), false)
			})
			if werr != nil {
				logx.Errorf("导出镜像 %s 失败: %v", ref, werr)
				continue
			}
			rec.SHA256 = sum
			if fi, serr := os.Stat(filepath.Join(staging, "images", fileName)); serr == nil {
				rec.Size = fi.Size()
			}
			savedBytes += written
		}
		manifest.Images = append(manifest.Images, module.ManifestImage{
			ID: it.ID, Refs: it.Refs, Source: it.Source, Size: it.Size, Transports: []module.ImageTransportRecord{rec},
		})
	}

	// ⑤ 清单 / compose / 脚本 / 校验和
	updateProgress(svcCtx, taskID, 75, "正在生成清单与导入脚本", "", false)
	mfBytes, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(staging, "manifest.json"), mfBytes, 0644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(staging, "compose.yaml"), []byte(buildComposeYaml(manifest)), 0644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(staging, "import.sh"), []byte(buildImportScript(pkgName)), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(staging, "README.txt"), []byte(buildReadmeTxt(pkgName, manifest)), 0644); err != nil {
		return "", err
	}
	if err := writeChecksums(staging); err != nil {
		logx.Errorf("校验和生成失败(不影响导出): %v", err)
	}

	// ⑥ 打包成单个 .tar.gz
	updateProgress(svcCtx, taskID, 88, "正在打包迁移包", "", false)
	outName := pkgName + ".tar.gz"
	outPath := filepath.Join(exportsDir, outName)
	if err := tarGzDir(staging, outPath, pkgName); err != nil {
		return "", err
	}
	fi, _ := os.Stat(outPath)
	updateProgress(svcCtx, taskID, 100, "导出完成",
		fmt.Sprintf("%s · %s（%d 个容器 / %d 个镜像）", outName, humanBytes(fi.Size()), len(manifest.Containers), len(manifest.Images)), true)
	logx.Infof("迁移包导出完成: %s (%s)", outPath, humanBytes(fi.Size()))
	_ = savedBytes
	return outName, nil
}

// saveImageToFile docker save 流式写盘（可选 gzip），返回文件 sha256
func saveImageToFile(svcCtx *svc.ServiceContext, ref, dest string, compress bool, onBytes func(int64)) (string, error) {
	ctx := context.Background()
	rc, err := svcCtx.DockerClient.ImageSave(ctx, []string{ref})
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()

	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	hash := sha256.New()
	var sink io.Writer = io.MultiWriter(f, hash)
	var gz *gzip.Writer
	if compress {
		gz = gzip.NewWriter(sink)
		sink = gz
	}
	buf := make([]byte, 1<<20)
	var total int64
	for {
		n, rerr := rc.Read(buf)
		if n > 0 {
			if _, werr := sink.Write(buf[:n]); werr != nil {
				return "", werr
			}
			total += int64(n)
			if onBytes != nil {
				onBytes(int64(n))
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
	}
	if gz != nil {
		if err := gz.Close(); err != nil {
			return "", err
		}
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeChecksums(staging string) error {
	imgs, err := os.ReadDir(filepath.Join(staging, "images"))
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, e := range imgs {
		if e.IsDir() {
			continue
		}
		h, herr := sha256File(filepath.Join(staging, "images", e.Name()))
		if herr != nil {
			continue
		}
		b.WriteString(fmt.Sprintf("%s  %s\n", h, filepath.Join("images", e.Name())))
	}
	return os.WriteFile(filepath.Join(staging, "checksums.txt"), []byte(b.String()), 0644)
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// tarGzDir 把目录打成 tar.gz（内含一层顶层目录 rootName）
func tarGzDir(srcDir, dest, rootName string) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	err = filepath.Walk(srcDir, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		rel, rerr := filepath.Rel(srcDir, path)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		hdr, herr := tar.FileInfoHeader(info, "")
		if herr != nil {
			return herr
		}
		hdr.Name = filepath.Join(rootName, rel)
		if info.IsDir() {
			hdr.Name += "/"
		}
		if werr := tw.WriteHeader(hdr); werr != nil {
			return werr
		}
		if info.Mode().IsRegular() {
			src, oerr := os.Open(path)
			if oerr != nil {
				return oerr
			}
			defer func() { _ = src.Close() }()
			if _, cerr := io.Copy(tw, src); cerr != nil {
				return cerr
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// ── 小工具 ────────────────────────────────────────────────────────────────

func hostname() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "unknown"
}

func isSensitiveKey(k string) bool {
	uk := strings.ToUpper(k)
	for _, s := range []string{"PASS", "PWD", "SECRET", "TOKEN", "KEY", "AUTH", "CREDENTIAL", "COOKIE"} {
		if strings.Contains(uk, s) {
			return true
		}
	}
	return false
}

func sanitizeFileStem(ref string) string {
	stem := strings.NewReplacer("/", "_", ":", "_").Replace(ref)
	return sanitizeSnapshotName(stem)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min95(v int) int {
	if v > 95 {
		return 95
	}
	return v
}
