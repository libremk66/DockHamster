package utiles

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// 变量体检：.env 可解析 / 带默认值可解析 / 缺失变量报出 / $$ 转义不误报 / 注释行跳过
func TestComposeVarCheck(t *testing.T) {
	dir := t.TempDir()
	envFile := "DHV_FROM_ENV=1\n# 注释\nDHV_EMPTY=\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(envFile), 0o644); err != nil {
		t.Fatal(err)
	}
	compose := `services:
  app:
    environment:
      - A=${DHV_FROM_ENV}
      - B=${DHV_EMPTY}
      - C=${DHV_DEFAULT:-fallback}
      - D=${DHV_MISSING:?required}
      - E=$DHV_MISSING2
      - F=$$DHV_ESCAPED
    # - G=${DHV_COMMENTED}
`
	file := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(file, []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}

	hasEnv, unresolved := composeVarCheck([]string{file}, dir, nil)
	if !hasEnv {
		t.Fatalf("应识别到 .env 文件")
	}
	sort.Strings(unresolved)
	want := []string{"DHV_MISSING", "DHV_MISSING2"}
	if !reflect.DeepEqual(unresolved, want) {
		t.Fatalf("unresolved = %v, want %v", unresolved, want)
	}
}

// 无 .env、进程环境也没有 → 全部未解析
func TestComposeVarCheckNoSources(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(file, []byte("image: nginx:${DHV_TAG_X}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hasEnv, unresolved := composeVarCheck([]string{file}, dir, nil)
	if hasEnv {
		t.Fatalf("没有 .env 不应报 hasEnvFile")
	}
	if len(unresolved) != 1 || unresolved[0] != "DHV_TAG_X" {
		t.Fatalf("unresolved = %v, want [DHV_TAG_X]", unresolved)
	}
}

// 容器内视角路径 → 宿主路径换算（最长 destination 优先；同一路径多视角时产生多条候选）
func TestTranslatePathCandidates(t *testing.T) {
	mounts := []composeMountPair{
		{destination: "/data", source: "/host/portainer-data"},
		{destination: "/data", source: "/host/panel-data"},
		{destination: "/data/compose", source: "/host/compose-view"},
	}
	sort.Slice(mounts, func(i, j int) bool { return len(mounts[i].destination) > len(mounts[j].destination) })

	got, src, ok := translateOne("/data/compose/12/docker-compose.yml", mounts)
	if !ok || got != "/host/compose-view/12/docker-compose.yml" || src != "/host/compose-view" {
		t.Fatalf("translateOne = (%q, %q, %v)", got, src, ok)
	}
	if _, _, ok := translateOne("/var/lib/other/app.yml", mounts); ok {
		t.Fatalf("不命中的路径不应换算成功")
	}

	cands := translatePathCandidates([]string{"/data/compose/7/app.yml"}, mounts)
	if len(cands) != 3 {
		t.Fatalf("应有 3 条候选，got %d", len(cands))
	}
	if cands[0].source != "/host/compose-view" || cands[0].hostFiles[0] != "/host/compose-view/7/app.yml" {
		t.Fatalf("候选顺序应按 destination 具体程度降序: %+v", cands[0])
	}
	// 候选确实覆盖了两个 /data 视角
	sources := []string{cands[1].source, cands[2].source}
	sort.Strings(sources)
	if sources[0] != "/host/panel-data" || sources[1] != "/host/portainer-data" {
		t.Fatalf("候选覆盖不全: %v", sources)
	}

	// 挂载引导：优先非面板自身来源
	if s := pickHintSource(cands, map[string]bool{"/host/compose-view": true, "/host/panel-data": true}); s != "/host/portainer-data" {
		t.Fatalf("pickHintSource = %q, want /host/portainer-data", s)
	}
	if s := pickHintSource(cands, map[string]bool{"/host/compose-view": true, "/host/panel-data": true, "/host/portainer-data": true}); s != cands[0].source {
		t.Fatalf("全为自身来源时应退回第一个候选, got %q", s)
	}
	if s := pickHintSource(nil, nil); s != "" {
		t.Fatalf("无候选应返回空串, got %q", s)
	}
}

// 挂载引导片段：有 service 名才生成（只读挂载）；无 service（非 compose 部署）返回空串
func TestBuildMountSnippet(t *testing.T) {
	hints := []ComposeMountHint{
		{Path: "/opt/stacks", Projects: []string{"a"}},
		{Path: "/srv/portainer", Projects: []string{"b", "c"}},
	}
	got := buildMountSnippet("dockhamster", hints)
	want := "services:\n  dockhamster:\n    volumes:\n      - /opt/stacks:/opt/stacks:ro\n      - /srv/portainer:/srv/portainer:ro"
	if got != want {
		t.Fatalf("snippet = %q, want %q", got, want)
	}
	if s := buildMountSnippet("", hints); s != "" {
		t.Fatalf("无 service 名应返回空串, got %q", s)
	}
	if s := buildMountSnippet("dockhamster", nil); s != "" {
		t.Fatalf("无 hints 应返回空串, got %q", s)
	}
}

// 挂载对排序：destination 更长优先；同级时"面板类来源"排前面（引导不指错目录）
func TestSortMountPairs(t *testing.T) {
	mounts := []composeMountPair{
		{destination: "/data", source: "/host/panel-data", panelish: false},
		{destination: "/data", source: "/host/portainer-data", panelish: true},
		{destination: "/data/compose", source: "/host/other", panelish: false},
	}
	got := sortMountPairs(mounts)
	if got[0].destination != "/data/compose" {
		t.Fatalf("更具体的 destination 应排最前: %+v", got[0])
	}
	if got[1].source != "/host/portainer-data" || !got[1].panelish {
		t.Fatalf("同级应面板类来源优先: %+v", got[1])
	}
	if got[2].source != "/host/panel-data" {
		t.Fatalf("非面板类同级最后: %+v", got[2])
	}
}

// 备用根前缀解析（设置里的 composeExtraRoots 走同一条路径）
func TestResolveConfigFilesWithRoots(t *testing.T) {
	root := t.TempDir()
	rel := "/opt/stacks/app/docker-compose.yml"
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolved, err := resolveConfigFilesWith([]string{rel}, []string{root})
	if err != nil {
		t.Fatalf("应能经备用根解析: %v", err)
	}
	if len(resolved) != 1 || resolved[0] != full {
		t.Fatalf("resolved = %v, want [%s]", resolved, full)
	}

	if _, err := resolveConfigFilesWith([]string{rel}, nil); err == nil {
		t.Fatalf("无备用根时应报不可达")
	}
}
