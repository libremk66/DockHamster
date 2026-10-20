package utiles

import (
	"testing"

	"github.com/docker/docker/client"
	"github.com/libremk66/DockHamster/internal/module"
	"github.com/libremk66/DockHamster/internal/svc"
)

// 排队中的任务在任何提前退出的路径上都必须落"失败完成"终态，
// 否则「任务」页的进行中列表会永远停在「等待中」（回归测试）。
func TestSetTasksFailedMarksTerminalState(t *testing.T) {
	ctx := &svc.ServiceContext{ProgressStore: svc.ProgressStoreType{}}
	ctx.InitTask("t1", "demo-app", "update", "autoupdate")
	ctx.UpdateProgress("t1", svc.TaskProgress{
		TaskID: "t1", Percentage: 0, Message: "等待中", DetailMsg: "排队中，等待前序任务完成",
	})

	setTasksFailed(ctx, []string{"t1", ""}, "拉取镜像失败", "TLS 握手超时")

	p, ok := ctx.GetProgress("t1")
	if !ok {
		t.Fatal("任务不存在")
	}
	if !p.IsDone || !p.Failed {
		t.Fatalf("任务未落失败终态: %+v", p)
	}
	if p.Message != "拉取镜像失败" || p.DetailMsg != "TLS 握手超时" {
		t.Fatalf("失败信息不对: %+v", p)
	}
	if p.Kind != "update" || p.Source != "autoupdate" || p.Name != "demo-app" {
		t.Fatalf("元数据丢失: %+v", p)
	}
}

// 拉取在拿到进度流之前就失败（连不上 daemon/仓库）时，也必须把排队任务落失败终态。
// 用不可达的 Docker 地址真实走一遍 PullImageForTasks 的错误出口。
func TestPullFailureMarksQueuedTask(t *testing.T) {
	cli, err := client.NewClientWithOpts(client.WithHost("tcp://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := &svc.ServiceContext{
		ProgressStore: svc.ProgressStoreType{},
		DockerClient:  cli,
		Accelerator:   &module.AcceleratorStore{},
	}
	ctx.InitTask("t1", "demo-app", "update", "autoupdate")
	ctx.UpdateProgress("t1", svc.TaskProgress{
		TaskID: "t1", Percentage: 0, Message: "等待中", DetailMsg: "排队中，等待前序任务完成",
	})

	if err := PullImageForTasks(ctx, []string{"t1"}, "ghcr.io/example/nope:latest"); err == nil {
		t.Fatal("期望拉取失败，实际为 nil")
	}
	p, _ := ctx.GetProgress("t1")
	if !p.IsDone || !p.Failed {
		t.Fatalf("拉取失败后任务未落失败终态: %+v", p)
	}
	if p.Message != "拉取镜像失败" || p.DetailMsg == "" {
		t.Fatalf("失败信息不完整: %+v", p)
	}
	if p.Kind != "update" || p.Source != "autoupdate" {
		t.Fatalf("元数据丢失: %+v", p)
	}
}

// 拉取流内部已经写过更具体的失败原因时，兜底不能把它覆盖成泛化文案
func TestSetTasksFailedKeepsExistingError(t *testing.T) {
	ctx := &svc.ServiceContext{ProgressStore: svc.ProgressStoreType{}}
	ctx.InitTask("t1", "demo-app", "update", "autoupdate")
	ctx.UpdateProgress("t1", svc.TaskProgress{
		TaskID: "t1", Percentage: 25, Message: "拉取镜像失败",
		DetailMsg: "manifest unknown", IsDone: true, Failed: true,
	})

	setTasksFailed(ctx, []string{"t1"}, "拉取镜像失败", "TLS 握手超时")

	p, _ := ctx.GetProgress("t1")
	if p.DetailMsg != "manifest unknown" {
		t.Fatalf("已有具体错误被覆盖: %+v", p)
	}
}
