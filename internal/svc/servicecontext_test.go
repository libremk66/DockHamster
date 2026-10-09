package svc

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/libremk66/DockHamster/internal/module"
)

// 任务元数据：InitTask 登记一次，后续 UpdateProgress 整体覆盖结构体时也要保留
func TestTaskMetaPreserved(t *testing.T) {
	ctx := &ServiceContext{ProgressStore: ProgressStoreType{}}
	ctx.InitTask("t1", "nginx", "update", "container")

	// 调用方常见写法：构造新结构体整体覆盖（不带元数据）
	ctx.UpdateProgress("t1", TaskProgress{TaskID: "t1", Percentage: 50, Message: "正在拉取新镜像"})

	p, ok := ctx.GetProgress("t1")
	if !ok {
		t.Fatal("任务不存在")
	}
	if p.Kind != "update" || p.Source != "container" || p.Name != "nginx" {
		t.Fatalf("元数据丢失: %+v", p)
	}
	if p.StartedAt.IsZero() {
		t.Fatal("StartedAt 未登记")
	}
	if p.Percentage != 50 {
		t.Fatalf("进度未更新: %+v", p)
	}
}

// 任务列表：进行中在前（按开始时间），已完成按更新时间倒序；耗时正确
func TestListTasksOrdering(t *testing.T) {
	ctx := &ServiceContext{ProgressStore: ProgressStoreType{}}

	ctx.InitTask("done1", "a", "update", "container")
	time.Sleep(5 * time.Millisecond)
	ctx.UpdateProgress("done1", TaskProgress{TaskID: "done1", IsDone: true, Percentage: 100})

	ctx.InitTask("run1", "b", "pull", "accelerator")

	ctx.InitTask("done2", "c", "update", "autoupdate")
	time.Sleep(5 * time.Millisecond)
	ctx.UpdateProgress("done2", TaskProgress{TaskID: "done2", IsDone: true, Failed: true, Percentage: 25})

	tasks := ctx.ListTasks()
	if len(tasks) != 3 {
		t.Fatalf("任务数 = %d，想要 3", len(tasks))
	}
	if tasks[0].TaskID != "run1" || tasks[0].IsDone {
		t.Fatalf("进行中的任务应排第一: %+v", tasks[0])
	}
	if tasks[1].TaskID != "done2" || !tasks[1].Failed {
		t.Fatalf("已完成应按更新时间倒序（done2 在前）: %+v", tasks[1])
	}
	if tasks[1].DurationSec <= 0 {
		t.Fatalf("耗时未计算: %+v", tasks[1])
	}
}

// 任务历史：完成跃迁写一条；重复更新不重写；批次型（autoupdate/group）由运行记录覆盖，跳过
func TestRecordTaskHistory(t *testing.T) {
	t.Setenv("TaskHistoryFile", filepath.Join(t.TempDir(), "task-history.json"))
	ctx := &ServiceContext{ProgressStore: ProgressStoreType{}, TaskHistory: module.NewTaskHistoryStore()}

	// ① 单容器更新 → 记一条
	ctx.InitTask("t1", "nginx", "update", "container")
	ctx.UpdateProgress("t1", TaskProgress{TaskID: "t1", Percentage: 100, IsDone: true, Message: "更新成功"})
	// 完成后再次更新（心跳/兜底刷新）不应重复记录
	ctx.UpdateProgress("t1", TaskProgress{TaskID: "t1", Percentage: 100, IsDone: true, Message: "更新成功"})

	// ② 自动更新批次内的单容器任务 → 跳过（批次记录在运行记录里）
	ctx.InitTask("t2", "qbittorrent", "update", "autoupdate")
	ctx.UpdateProgress("t2", TaskProgress{TaskID: "t2", Percentage: 100, IsDone: true})

	// ③ 失败任务也要记
	ctx.InitTask("t3", "sonarr", "pull", "accelerator")
	time.Sleep(5 * time.Millisecond)
	ctx.UpdateProgress("t3", TaskProgress{TaskID: "t3", Percentage: 30, IsDone: true, Failed: true, DetailMsg: "拉取失败"})

	list := ctx.TaskHistory.List()
	if len(list) != 2 {
		t.Fatalf("历史条数 = %d，想要 2: %+v", len(list), list)
	}
	if list[0].Name != "sonarr" || !list[0].Failed || list[0].Source != "accelerator" {
		t.Fatalf("失败任务记录不对: %+v", list[0])
	}
	if list[1].Name != "nginx" || list[1].Kind != "update" || list[1].DurationSec < 0 {
		t.Fatalf("成功任务记录不对: %+v", list[1])
	}
}
