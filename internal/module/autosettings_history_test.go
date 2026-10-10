package module

import (
	"os"
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

// 运行记录上限（超出滚动丢弃）
func TestAutoUpdateStateCapsRuns(t *testing.T) {
	t.Setenv("AutoUpdateHistoryFile", filepath.Join(t.TempDir(), "h.json"))
	s := NewAutoUpdateState()
	for i := 0; i < autoUpdateRunCap+5; i++ {
		s.AddRun(AutoUpdateRunResult{Time: "t", Trigger: "auto"})
	}
	_, runs, _, _ := s.Snapshot()
	if len(runs) != autoUpdateRunCap {
		t.Fatalf("记录数 = %d，想要 %d", len(runs), autoUpdateRunCap)
	}
	s2 := NewAutoUpdateState()
	if _, runs2, _, _ := s2.Snapshot(); len(runs2) != autoUpdateRunCap {
		t.Fatalf("重启后记录数 = %d，想要 %d", len(runs2), autoUpdateRunCap)
	}
}

// 运行记录 ID：新记录自动生成；删除按 ID 生效；旧文件（无 ID）载入时回填
func TestAutoUpdateRunIDAndDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.json")
	t.Setenv("AutoUpdateHistoryFile", path)
	s := NewAutoUpdateState()
	s.AddRun(AutoUpdateRunResult{Time: "2026-10-09 04:00:00", Trigger: "auto"})
	s.AddRun(AutoUpdateRunResult{Time: "2026-10-09 05:00:00", Trigger: "manual"})
	_, runs, _, _ := s.Snapshot()
	if runs[0].ID == "" || runs[1].ID == "" {
		t.Fatalf("新记录应自动生成 ID: %+v", runs)
	}
	if n := s.DeleteRuns([]string{runs[0].ID}); n != 1 {
		t.Fatalf("删除条数 = %d，想要 1", n)
	}
	_, runs, _, _ = s.Snapshot()
	if len(runs) != 1 || runs[0].Time != "2026-10-09 04:00:00" {
		t.Fatalf("删除结果不对: %+v", runs)
	}

	// 模拟旧版本文件：手工写入无 ID 的记录
	legacy := `{"runs":[{"time":"2026-10-01 04:00:00","trigger":"auto","updated":[],"failed":[],"cleanedImages":0,"durationSec":1}],"last":{}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s2 := NewAutoUpdateState()
	_, runs2, _, _ := s2.Snapshot()
	if len(runs2) != 1 || runs2[0].ID != "run-2026-10-01 04:00:00" {
		t.Fatalf("旧记录未回填 ID: %+v", runs2)
	}
	if n := s2.DeleteRuns([]string{"run-2026-10-01 04:00:00"}); n != 1 {
		t.Fatalf("按回填 ID 删除失败: %d", n)
	}
}

// 提前量归一化：未设置/非法 → 默认 10；上限 120
func TestNotifyBeforeUpdateLeadNormalize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	t.Setenv("AutoUpdateConfigFile", path)
	// 未设置
	s := NewAutoUpdateStore()
	if got := s.Get().NotifyBeforeUpdateLeadMin; got != 10 {
		t.Fatalf("未设置时应默认 10，实际 %d", got)
	}
	// 超上限
	st := s.Get()
	st.NotifyBeforeUpdateLeadMin = 999
	if err := s.Save(st); err != nil {
		t.Fatal(err)
	}
	if got := s.Get().NotifyBeforeUpdateLeadMin; got != 120 {
		t.Fatalf("超上限应夹到 120，实际 %d", got)
	}
}
