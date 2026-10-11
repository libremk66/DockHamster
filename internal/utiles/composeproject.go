package utiles

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/libremk66/DockHamster/internal/svc"
)

// 「Compose」页（compose 项目视图）的数据构建。
//
// 设计：
//   - 按 com.docker.compose.project 聚合容器 → 项目 / 服务两层（不需要任何文件就能显示）；
//   - 每个项目判定 compose 文件可达性：原路径 → 备用根目录（设置 composeExtraRoots +
//     环境变量 COMPOSE_ALT_ROOTS）→ "创建者容器视角 → 宿主路径"换算（如 Portainer 的
//     /data/compose/<id>，通过扫描其它容器的挂载表换算）；
//   - 不可达的项目汇总成「挂载引导」：需要挂哪些宿主目录（同路径挂载）+ 可复制的覆盖片段；
//   - 可达的项目做「变量体检」：文件里 ${X} 在面板侧解析不了（.env / 进程环境都没有、
//     也没有默认值）→ 提示"变量不在文件里"（多半存在面板数据库，更新会有语义偏差）。
//
// 说明：本文件不 import selfupdate（selfupdate → utiles 已有依赖，反向会成环），
// 面板自身容器 ID 由调用方传入。

// ComposeServiceView 「Compose」页里的单个服务（一个容器）
type ComposeServiceView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Service     string `json:"service"`
	Image       string `json:"image"`
	State       string `json:"state"`
	HaveUpdate  bool   `json:"haveUpdate"`
	Uncheckable bool   `json:"uncheckable"`
}

// ComposeProjectView 一个 compose 项目。
// State：readable=面板能读到文件（可走 compose 更新通道）/ needsMount=需要挂载 /
// noFile=label 未记录文件路径（老版本 compose 或手工创建的容器）
type ComposeProjectView struct {
	Name          string               `json:"name"`
	Services      []ComposeServiceView `json:"services"`
	ConfigFiles   []string             `json:"configFiles"`
	WorkingDir    string               `json:"workingDir"`
	State         string               `json:"state"`
	ResolvedFiles []string             `json:"resolvedFiles,omitempty"`
	MountHint     string               `json:"mountHint,omitempty"`
	VarUnresolved []string             `json:"varUnresolved,omitempty"`
	HasEnvFile    bool                 `json:"hasEnvFile"`
}

// ComposeMountHint 建议挂载的宿主目录（同路径挂载；一个目录覆盖多个项目）
type ComposeMountHint struct {
	Path     string   `json:"path"`
	Projects []string `json:"projects"`
}

// ComposeMountGuide 挂载引导（hints 为空表示无需挂载）
type ComposeMountGuide struct {
	Hints       []ComposeMountHint `json:"hints"`
	SelfService string             `json:"selfService"`
	// Snippet 可复制的 compose 覆盖片段（仅在知道面板自身 service 名时生成）
	Snippet string `json:"snippet,omitempty"`
}

// ComposeProjectsView 「Compose」页响应体
type ComposeProjectsView struct {
	Projects []ComposeProjectView `json:"projects"`
	Guide    ComposeMountGuide    `json:"guide"`
	// ComposeAvailable 面板环境是否有 compose 命令（没有则更新只能走容器级 API 重建）
	ComposeAvailable   bool     `json:"composeAvailable"`
	ComposeUnavailable string   `json:"composeUnavailable,omitempty"`
	ExtraRoots         []string `json:"extraRoots"`
}

// ComposeAvailable 探测面板运行环境里的 compose 命令（复用更新通道同一探测）
func ComposeAvailable() (bool, string) {
	if _, _, err := composeBinary(); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// ComposeAltRoots 生效的备用根目录：环境变量 + 设置里的 composeExtraRoots（去重、保持顺序）
func ComposeAltRoots(svcCtx *svc.ServiceContext) []string {
	all := append([]string{}, altRootPrefixes()...)
	if svcCtx != nil {
		for _, r := range svcCtx.AutoUpdate.Get().ComposeExtraRoots {
			if r = strings.TrimSpace(r); r != "" {
				all = append(all, r)
			}
		}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(all))
	for _, r := range all {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// composeMountPair 一台容器的挂载（destination → source）
type composeMountPair struct {
	destination string
	source      string
	// panelish 来源容器镜像看着像"面板/编排工具"（Portainer/Dockge/1Panel…）。
	// 多个容器都把目录挂在 /data 是常态（Portainer 的数据卷、各面板自己的 /data），
	// 挂载引导要优先推"面板类"来源，否则可能让用户挂错目录。
	panelish bool
}

// composePanelImageHints 判定"面板类镜像"的关键词（小写子串匹配）
var composePanelImageHints = []string{
	"portainer", "dockge", "1panel", "casaos", "dockhand", "yacht", "caprover", "panel",
}

func isPanelLikeImage(image string) bool {
	low := strings.ToLower(image)
	for _, h := range composePanelImageHints {
		if strings.Contains(low, h) {
			return true
		}
	}
	return false
}

// sortMountPairs 挂载对排序：destination 越长越具体优先；同级时"面板类来源"优先（引导更准）
func sortMountPairs(mounts []composeMountPair) []composeMountPair {
	sorted := append([]composeMountPair{}, mounts...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if len(sorted[i].destination) != len(sorted[j].destination) {
			return len(sorted[i].destination) > len(sorted[j].destination)
		}
		if sorted[i].panelish != sorted[j].panelish {
			return sorted[i].panelish
		}
		return false
	})
	return sorted
}

// BuildComposeProjectsView 构建「Compose」页数据。
// selfContainerID：面板自身容器 ID（utiles 不 import selfupdate，由调用方传入；可为空）。
func BuildComposeProjectsView(svcCtx *svc.ServiceContext, selfContainerID string) (*ComposeProjectsView, error) {
	list, err := GetContainerList(svcCtx)
	if err != nil {
		return nil, err
	}
	list = CheckImageUpdate(svcCtx, list) // 复用检查层缓存给服务行带 haveUpdate/uncheckable

	roots := ComposeAltRoots(svcCtx)
	view := &ComposeProjectsView{
		Projects:   []ComposeProjectView{},
		Guide:      ComposeMountGuide{Hints: []ComposeMountHint{}},
		ExtraRoots: roots,
	}
	if ok, reason := ComposeAvailable(); ok {
		view.ComposeAvailable = true
	} else {
		view.ComposeUnavailable = reason
	}

	type projAcc struct {
		view ComposeProjectView
		meta ComposeMeta
	}
	accs := map[string]*projAcc{}
	var mounts []composeMountPair
	selfService := ""
	selfMountSources := map[string]bool{}

	for _, c := range list {
		ins, ierr := GetContainerInspect(svcCtx, c.ID)
		if ierr != nil {
			continue // 单个容器 inspect 失败（可能正被删除）不影响整体
		}
		if selfContainerID != "" && sameContainerID(c.ID, selfContainerID) {
			if ins.Config != nil {
				selfService = ins.Config.Labels[labelComposeService]
			}
			for _, m := range ins.Mounts {
				if m.Source != "" {
					selfMountSources[m.Source] = true
				}
			}
		}
		for _, m := range ins.Mounts {
			if m.Destination != "" && m.Source != "" {
				pair := composeMountPair{destination: m.Destination, source: m.Source}
				if ins.Config != nil {
					pair.panelish = isPanelLikeImage(ins.Config.Image)
				}
				mounts = append(mounts, pair)
			}
		}
		if ins.Config == nil {
			continue
		}
		meta := ComposeMetaFromLabels(ins.Config.Labels)
		if !meta.IsManaged {
			continue
		}
		acc := accs[meta.Project]
		if acc == nil {
			acc = &projAcc{view: ComposeProjectView{Name: meta.Project}}
			accs[meta.Project] = acc
		}
		if len(acc.view.ConfigFiles) == 0 && len(meta.ConfigFiles) > 0 {
			acc.view.ConfigFiles = meta.ConfigFiles
			acc.view.WorkingDir = meta.WorkingDir
			acc.meta = meta
		}
		name := c.ID
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		acc.view.Services = append(acc.view.Services, ComposeServiceView{
			ID: c.ID, Name: name, Service: meta.Service, Image: c.Image,
			State: c.State, HaveUpdate: c.Update, Uncheckable: c.Uncheckable,
		})
	}

	// 路径换算优先用最长的 destination 匹配（更具体的前缀优先），同级时面板类来源优先
	sortedMounts := sortMountPairs(mounts)

	names := make([]string, 0, len(accs))
	for n := range accs {
		names = append(names, n)
	}
	sort.Strings(names)

	hintProjects := map[string][]string{}
	for _, n := range names {
		acc := accs[n]
		pv := acc.view
		sort.Slice(pv.Services, func(i, j int) bool { return pv.Services[i].Service < pv.Services[j].Service })

		if len(pv.ConfigFiles) == 0 {
			pv.State = "noFile"
			view.Projects = append(view.Projects, pv)
			continue
		}

		resolved, rerr := resolveConfigFilesWith(pv.ConfigFiles, roots)
		wd := pv.WorkingDir
		if rerr != nil {
			// 原路径 + 备用根都不可达 → 尝试"创建者容器视角 → 宿主路径"换算。
			// 同一路径可能匹配多个挂载视角（多层容器都把某目录挂在 /data 是常态），
			// 逐个候选**实测能否读到**来挑选正确的那个；都读不到时给挂载引导
			// （优先非面板自身数据目录的候选）。
			cands := translatePathCandidates(pv.ConfigFiles, sortedMounts)
			readable := false
			for i := range cands {
				if resolved2, e2 := resolveConfigFilesWith(cands[i].hostFiles, roots); e2 == nil {
					resolved = resolved2
					if wd != "" {
						if hw, _, okW := translateOne(wd, []composeMountPair{cands[i].mount}); okW {
							wd = hw
						}
					}
					readable = true
					break
				}
			}
			if !readable {
				pv.State = "needsMount"
				pv.MountHint = pickHintSource(cands, selfMountSources)
				if pv.MountHint == "" {
					pv.MountHint = pv.WorkingDir
					if pv.MountHint == "" {
						pv.MountHint = filepath.Dir(pv.ConfigFiles[0])
					}
				}
			}
		}
		if pv.State == "" {
			pv.State = "readable"
			pv.ResolvedFiles = resolved
			pv.HasEnvFile, pv.VarUnresolved = composeVarCheck(resolved, wd, roots)
		}
		if pv.State == "needsMount" {
			if hp := filepath.Clean(pv.MountHint); filepath.IsAbs(hp) && hp != "/" {
				hintProjects[hp] = append(hintProjects[hp], pv.Name)
			}
		}
		view.Projects = append(view.Projects, pv)
	}

	// 前缀收敛：子目录被更短的父目录覆盖则并入父目录的引导（挂一次全覆盖）
	hintPaths := make([]string, 0, len(hintProjects))
	for p := range hintProjects {
		hintPaths = append(hintPaths, p)
	}
	sort.Strings(hintPaths)
	for _, p := range hintPaths {
		merged := false
		for i := range view.Guide.Hints {
			parent := view.Guide.Hints[i].Path
			if p == parent || strings.HasPrefix(p, parent+"/") {
				view.Guide.Hints[i].Projects = append(view.Guide.Hints[i].Projects, hintProjects[p]...)
				merged = true
				break
			}
		}
		if !merged {
			view.Guide.Hints = append(view.Guide.Hints, ComposeMountHint{Path: p, Projects: hintProjects[p]})
		}
	}
	for i := range view.Guide.Hints {
		sort.Strings(view.Guide.Hints[i].Projects)
		view.Guide.Hints[i].Projects = dedupStrings(view.Guide.Hints[i].Projects)
	}

	view.Guide.SelfService = selfService
	view.Guide.Snippet = buildMountSnippet(selfService, view.Guide.Hints)
	return view, nil
}

// buildMountSnippet 生成可复制的 compose 覆盖片段（同路径只读挂载）。
// 只读（:ro）即可：面板对 compose 文件只需要读；写权限没有需求，少给更安全。
// 不知道面板自身 service 名（非 compose 部署）时返回空串，由前端退化为通用挂载指引。
func buildMountSnippet(service string, hints []ComposeMountHint) string {
	if service == "" || len(hints) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("services:\n  " + service + ":\n    volumes:\n")
	for _, h := range hints {
		b.WriteString("      - " + h.Path + ":" + h.Path + ":ro\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// sameContainerID 容器 ID 前缀匹配（与 selfupdate.IsSelf 同语义的本地实现，避免依赖环）
func sameContainerID(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// composePathTranslation 一条换算候选：容器视角路径换算出的宿主路径 + 命中的挂载对
type composePathTranslation struct {
	hostFiles []string
	source    string // 挂载源（挂载引导取整块数据目录，如 Portainer 的数据卷）
	mount     composeMountPair
}

// translatePathCandidates 生成全部换算候选：对每个挂载对尝试把 paths 全部换算过去，
// 能全部命中的产出一条候选（沿用传入顺序 = destination 具体程度降序；按目标路径去重）。
// 调用方用"实际能否读到"在其中挑正确的视角（多个容器把目录挂在 /data 是常态）。
func translatePathCandidates(paths []string, sortedMounts []composeMountPair) []composePathTranslation {
	if len(paths) == 0 {
		return nil
	}
	out := make([]composePathTranslation, 0, len(sortedMounts))
	seen := map[string]bool{}
	for _, m := range sortedMounts {
		hosts := make([]string, len(paths))
		okAll := true
		for i, p := range paths {
			h, _, ok := translateOne(p, []composeMountPair{m})
			if !ok {
				okAll = false
				break
			}
			hosts[i] = h
		}
		if !okAll || seen[hosts[0]] {
			continue
		}
		seen[hosts[0]] = true
		out = append(out, composePathTranslation{hostFiles: hosts, source: m.source, mount: m})
	}
	return out
}

// pickHintSource 从候选里挑挂载引导的路径：优先非面板自身数据目录的来源；
// 全是自身来源时退回第一个候选（至少是真实存在的挂载视角）。
// 局限：候选都没被挂载时无法验证哪条是真路径，极端情况下引导可能需用户自行核对。
func pickHintSource(cands []composePathTranslation, selfSources map[string]bool) string {
	for _, c := range cands {
		if !selfSources[c.source] {
			return c.source
		}
	}
	if len(cands) > 0 {
		return cands[0].source
	}
	return ""
}

// translateOne 单个路径换算：找 destination 前缀命中的挂载，替换成 source 前缀。
func translateOne(p string, sortedMounts []composeMountPair) (string, string, bool) {
	if p == "" {
		return "", "", false
	}
	for _, m := range sortedMounts {
		if p == m.destination {
			return m.source, m.source, true
		}
		if strings.HasPrefix(p, m.destination+"/") {
			return filepath.Join(m.source, strings.TrimPrefix(p, m.destination+"/")), m.source, true
		}
	}
	return "", "", false
}

// resolveOneDir 目录可达性解析（原路径 → 备用根前缀）
func resolveOneDir(dir string, roots []string) (string, bool) {
	if dir == "" {
		return "", false
	}
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return dir, true
	}
	for _, root := range roots {
		candidate := filepath.Join(root, dir)
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

// composeVarPattern 匹配 ${VAR...} 与 $VAR 两种插值写法（compose 语义）
var composeVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)([^}]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// composeVarCheck 变量体检：找出文件里"面板侧解析不了"的变量。
// 插值来源按 docker compose 语义 = 进程环境 + 项目目录 .env（env_file 只传给容器、不参与插值）。
// 带默认值（${X:-d} / ${X-d}）算可解析；${X:?err} / ${X?err} 算必填。
// 局限：只跳过 "# 开头"的整行注释（行尾注释里的变量会误报，方向偏保守，可接受）。
func composeVarCheck(files []string, workingDir string, roots []string) (hasEnvFile bool, unresolved []string) {
	envValues := map[string]bool{}
	if wd, ok := resolveOneDir(workingDir, roots); ok {
		if b, err := os.ReadFile(filepath.Join(wd, ".env")); err == nil {
			hasEnvFile = true
			for _, line := range strings.Split(string(b), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				line = strings.TrimPrefix(line, "export ")
				k, _, found := strings.Cut(line, "=")
				if found {
					if k = strings.TrimSpace(k); k != "" {
						envValues[k] = true
					}
				}
			}
		}
	}

	required := map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			line = strings.ReplaceAll(line, "$$", "") // compose 里 $$ 是转义的字面 $
			for _, m := range composeVarPattern.FindAllStringSubmatch(line, -1) {
				name, mod := m[1], m[2]
				if name == "" {
					name, mod = m[3], ""
				}
				if name == "" {
					continue
				}
				if strings.HasPrefix(mod, ":-") || strings.HasPrefix(mod, "-") {
					continue // 有默认值
				}
				required[name] = true
			}
		}
	}

	for name := range required {
		if envValues[name] {
			continue
		}
		if _, ok := os.LookupEnv(name); ok {
			continue
		}
		unresolved = append(unresolved, name)
	}
	sort.Strings(unresolved)
	return hasEnvFile, unresolved
}

// dedupStrings 去重（保持第一次出现顺序）
func dedupStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
