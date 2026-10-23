package utiles

import (
	"testing"

	"github.com/docker/docker/api/types/image"
	MyType "github.com/libremk66/DockHamster/internal/types"
)

func TestResolvePullRefFromImages(t *testing.T) {
	const id = "sha256:aaaa"
	withTag := MyType.Image{
		Summary: image.Summary{
			ID:       id,
			RepoTags: []string{"libremk66/dockhamster:latest"},
		},
	}
	noTag := MyType.Image{Summary: image.Summary{ID: id}}

	cases := []struct {
		name     string
		images   []MyType.Image
		imageID  string
		fallback string
		want     string
	}{
		{"有 RepoTag 用 tag", []MyType.Image{withTag}, id, "ignored:latest", "libremk66/dockhamster:latest"},
		{"无 RepoTag 用合法 fallback", []MyType.Image{noTag}, id, "acme/media-api:latest", "docker.io/acme/media-api:latest"},
		{"无 RepoTag 且 fallback 是裸 ID → 空", []MyType.Image{noTag}, id, "sha256:a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90", ""},
		{"镜像不在列表 → 用合法 fallback", nil, id, "nginx:1.25", "docker.io/library/nginx:1.25"},
		{"镜像不在列表且 fallback 为空 → 空", nil, id, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolvePullRefFromImages(c.images, c.imageID, c.fallback); got != c.want {
				t.Fatalf("resolvePullRefFromImages() = %q, want %q", got, c.want)
			}
		})
	}
}
