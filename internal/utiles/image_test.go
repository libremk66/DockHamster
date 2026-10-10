package utiles

import (
	"testing"

	"github.com/libremk66/DockHamster/internal/svc"
	MyType "github.com/libremk66/DockHamster/internal/types"
)

func TestImageParentSetCachesWithinTTL(t *testing.T) {
	orig := imageParentScan
	defer func() { imageParentScan = orig }()

	calls := 0
	imageParentScan = func(_ *svc.ServiceContext) map[string]bool {
		calls++
		return map[string]bool{"sha256:parent": true}
	}

	InvalidateImageParentCache()
	first := imageParentSet(nil)
	second := imageParentSet(nil)
	if calls != 1 {
		t.Fatalf("TTL 内应只扫描一次，实际 %d 次", calls)
	}
	if !first["sha256:parent"] || !second["sha256:parent"] {
		t.Fatalf("返回结果应包含父镜像 ID")
	}

	InvalidateImageParentCache()
	_ = imageParentSet(nil)
	if calls != 2 {
		t.Fatalf("缓存失效后应重新扫描，实际 %d 次", calls)
	}
}

func TestMarkChildImages(t *testing.T) {
	orig := imageParentScan
	defer func() { imageParentScan = orig }()

	imageParentScan = func(_ *svc.ServiceContext) map[string]bool {
		return map[string]bool{"b": true}
	}
	InvalidateImageParentCache()

	list := []MyType.Image{{}, {}}
	list[0].ID = "a"
	list[1].ID = "b"
	markChildImages(nil, list)

	if list[0].HasChildren {
		t.Fatalf("普通镜像不应被标记为父镜像")
	}
	if !list[1].HasChildren {
		t.Fatalf("被引用镜像应标记 HasChildren")
	}
}
