package autoupdate

import "testing"

// robfig/cron 默认 chain 不带 Recover：回调 panic 必须在这里被吃掉，
// 否则会从 goroutine 逃逸并退出整个面板进程。
func TestRunCronSafeRecoversPanic(t *testing.T) {
	reset := false
	runCronSafe("测试任务", func() { reset = true }, func() {
		panic("boom")
	})
	if !reset {
		t.Fatal("panic 后未执行状态复位")
	}
}

func TestRunCronSafeRunsNormally(t *testing.T) {
	ran := false
	runCronSafe("测试任务", nil, func() { ran = true })
	if !ran {
		t.Fatal("正常回调未执行")
	}
}
