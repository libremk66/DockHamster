package utiles

import (
	"reflect"
	"testing"
)

func TestComposeMetaFromLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   ComposeMeta
	}{
		{
			name:   "nil labels",
			labels: nil,
			want:   ComposeMeta{IsManaged: false},
		},
		{
			name:   "非 compose 容器",
			labels: map[string]string{"foo": "bar"},
			want:   ComposeMeta{IsManaged: false},
		},
		{
			name: "缺少 project",
			labels: map[string]string{
				labelComposeService: "web",
			},
			want: ComposeMeta{IsManaged: false},
		},
		{
			name: "缺少 service",
			labels: map[string]string{
				labelComposeProject: "demo",
			},
			want: ComposeMeta{IsManaged: false},
		},
		{
			name: "完整元数据（单 compose 文件）",
			labels: map[string]string{
				labelComposeProject:     "llm-gateway",
				labelComposeService:     "new-api",
				labelComposeConfigFiles: "/vol1/docker/llm-gateway/docker-compose.yml",
				labelComposeWorkDir:     "/vol1/docker/llm-gateway",
			},
			want: ComposeMeta{
				IsManaged:   true,
				Project:     "llm-gateway",
				Service:     "new-api",
				ConfigFiles: []string{"/vol1/docker/llm-gateway/docker-compose.yml"},
				WorkingDir:  "/vol1/docker/llm-gateway",
			},
		},
		{
			name: "多 compose 文件（: 分隔）",
			labels: map[string]string{
				labelComposeProject:     "demo",
				labelComposeService:     "web",
				labelComposeConfigFiles: "/a/base.yml:/a/override.yml",
			},
			want: ComposeMeta{
				IsManaged:   true,
				Project:     "demo",
				Service:     "web",
				ConfigFiles: []string{"/a/base.yml", "/a/override.yml"},
			},
		},
		{
			name: "config_files 为空不产生空条目",
			labels: map[string]string{
				labelComposeProject:     "demo",
				labelComposeService:     "web",
				labelComposeConfigFiles: "",
			},
			want: ComposeMeta{
				IsManaged: true,
				Project:   "demo",
				Service:   "web",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ComposeMetaFromLabels(tt.labels); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ComposeMetaFromLabels() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestComposeMetaUpdateRef(t *testing.T) {
	if got := (ComposeMeta{}).UpdateRef(); got != "" {
		t.Errorf("非 compose 容器 UpdateRef() = %q, want empty", got)
	}
	meta := ComposeMeta{IsManaged: true, Project: "p", Service: "s"}
	if got, want := meta.UpdateRef(), "p/s"; got != want {
		t.Errorf("UpdateRef() = %q, want %q", got, want)
	}
}
