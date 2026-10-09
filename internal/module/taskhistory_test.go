package module

import (
	"path/filepath"
	"testing"
)

// 任务历史：写入 / 重启加载 / 按 ID 删除 / 顺序（最新在前）
func TestTaskHistoryStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task-history.json")
	t.Setenv("TaskHistoryFile", path)

	s := NewTaskHistoryStore()
	s.Add(TaskHistoryEntry{Time: "2026-10-09 04:00:01", Name: "nginx", Kind: "update", Source: "container", Message: "更新成功"})
	s.Add(TaskHistoryEntry{Time: "2026-10-09 04:00:02", Name: "qbittorrent", Kind: "pull", Source: "accelerator", Failed: true, DetailMsg: "拉取失败"})

	list := s.List()
	if len(list) != 2 {
		t.Fatalf("条数 = %d，想要 2", len(list))
	}
	if list[0].Name != "qbittorrent" {
		t.Fatalf("最新一条应排在最前: %+v", list[0])
	}
	if list[0].ID == "" || list[1].ID == "" {
		t.Fatalf("ID 未自动生成: %+v", list)
	}

	// 模拟面板重启
	s2 := NewTaskHistoryStore()
	list2 := s2.List()
	if len(list2) != 2 || list2[1].Name != "nginx" || !list2[0].Failed {
		t.Fatalf("重启后加载不对: %+v", list2)
	}

	// 删除：命中 1 条 + 不存在的 ID 不报错
	if n := s2.Delete([]string{list2[0].ID, "task-not-exist"}); n != 1 {
		t.Fatalf("删除条数 = %d，想要 1", n)
	}
	if list3 := s2.List(); len(list3) != 1 || list3[0].Name != "nginx" {
		t.Fatalf("删除后剩余不对: %+v", list3)
	}
}

// 任务历史上限：超过 500 条丢弃最旧的
func TestTaskHistoryCap(t *testing.T) {
	t.Setenv("TaskHistoryFile", filepath.Join(t.TempDir(), "task-history.json"))
	s := NewTaskHistoryStore()
	for i := 0; i < taskHistoryCap+10; i++ {
		s.Add(TaskHistoryEntry{Time: "t", Name: "x", Kind: "update", Source: "container"})
	}
	if n := len(s.List()); n != taskHistoryCap {
		t.Fatalf("条数 = %d，想要 %d", n, taskHistoryCap)
	}
}
