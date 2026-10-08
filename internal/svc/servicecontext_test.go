package svc

import (
	"testing"
	"time"
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
