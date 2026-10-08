package module

import (
	"path/filepath"
	"testing"
)

// 运行记录/每容器状态持久化：面板容器重建（版本更新）后不丢
func TestAutoUpdateStatePersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autoupdate-history.json")
	t.Setenv("AutoUpdateHistoryFile", path)

	s := NewAutoUpdateState()
	s.SetContainer("alpha", true, "更新成功")
	s.AddRun(AutoUpdateRunResult{Time: "2026-10-08 22:00:00", Trigger: "manual", Updated: []string{"alpha"}})
	s.AddRun(AutoUpdateRunResult{Time: "2026-10-09 04:00:00", Trigger: "auto", Failed: []AutoRunFailure{{Name: "beta", Error: "拉取失败"}}})

	// 模拟面板重启：新实例从同一文件加载
	s2 := NewAutoUpdateState()
	_, runs, last, _ := s2.Snapshot()
	if len(runs) != 2 {
		t.Fatalf("加载到 %d 条运行记录，想要 2", len(runs))
	}
	if runs[0].Trigger != "auto" || runs[0].Failed[0].Name != "beta" {
		t.Fatalf("最新一条记录不对: %+v", runs[0])
	}
	if runs[1].Updated[0] != "alpha" {
		t.Fatalf("较早一条记录不对: %+v", runs[1])
	}
	st, ok := last["alpha"]
	if !ok || !st.OK {
		t.Fatalf("每容器状态未恢复: %+v", last)
	}
}

// 运行记录上限 30 条（超出滚动丢弃）
func TestAutoUpdateStateCapsRuns(t *testing.T) {
	t.Setenv("AutoUpdateHistoryFile", filepath.Join(t.TempDir(), "h.json"))
	s := NewAutoUpdateState()
	for i := 0; i < 35; i++ {
		s.AddRun(AutoUpdateRunResult{Time: "t", Trigger: "auto"})
	}
	_, runs, _, _ := s.Snapshot()
	if len(runs) != 30 {
		t.Fatalf("记录数 = %d，想要 30", len(runs))
	}
	s2 := NewAutoUpdateState()
	if _, runs2, _, _ := s2.Snapshot(); len(runs2) != 30 {
		t.Fatalf("重启后记录数 = %d，想要 30", len(runs2))
	}
}
