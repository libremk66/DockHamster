package utiles

import (
	"testing"

	"github.com/docker/docker/api/types/image"
)

func TestPruneCandidates(t *testing.T) {
	images := []image.Summary{
		{ID: "tagged", RepoTags: []string{"nginx:latest"}},
		{ID: "leaf"},
		{ID: "child", ParentID: "parent"},
		{ID: "parent"},
		{ID: "inuse"},
	}
	inUse := map[string]bool{"inuse": true}

	candidates, skipped := pruneCandidates(images, inUse)

	got := make(map[string]bool, len(candidates))
	for _, id := range candidates {
		got[id] = true
	}
	if len(candidates) != 2 || !got["leaf"] || !got["child"] {
		t.Fatalf("可删集合应为 {leaf,child}，实际 %v", candidates)
	}
	if skipped != 1 {
		t.Fatalf("被依赖的无 Tag 镜像应为 1 个，实际 %d", skipped)
	}
	for _, id := range []string{"tagged", "inuse", "parent"} {
		if got[id] {
			t.Fatalf("%s 不应进入可删集合: %v", id, candidates)
		}
	}
}

func TestPruneCandidatesEmpty(t *testing.T) {
	candidates, skipped := pruneCandidates(nil, nil)
	if len(candidates) != 0 || skipped != 0 {
		t.Fatalf("空列表应无可删项、无跳过项，实际 candidates=%v skipped=%d", candidates, skipped)
	}
}
