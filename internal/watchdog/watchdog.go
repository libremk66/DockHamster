// Package watchdog 容器守护：巡检容器异常（意外退出 / OOM / 重启循环），通过通知渠道告警。
//
// 设计要点：
//   - 每 60 秒巡检一次（首次采样只建立基线，不告警，避免面板启动时误报）
//   - 只对"异常"退出告警：退出码非 0、OOM 被杀、反复重启；正常停止（退出码 0）不打扰
//   - 面板主动发起的停止/重启/更新会先登记静默窗口（NoteMaintenance），窗口内不告警
//   - 同类告警按容器做 30 分钟冷却；容器恢复运行后发一条恢复通知
package watchdog

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/zeromicro/go-zero/core/logx"
)

const (
	checkInterval     = 60 * time.Second
	restartWindow     = 10 * time.Minute // 重启计数窗口
	restartTrigger    = 3                // 窗口内重启次数达到该值告警
	notifyCooldown    = 30 * time.Minute // 同类告警冷却
	baselineDelay     = 25 * time.Second // 面板启动后多久建立基线
	recentCrashWindow = 10 * time.Minute // 首次见到时，多久内的异常退出也要告警
)

// Config 由调用方注入（避免与 svc 包循环依赖）
type Config struct {
	Disabled func() bool              // 是否停用守护
	Notify   func(title, text string) // 发通知（走已配置的通知渠道）
	HostTag  func() string            // 通知标题里的主机标记（如 "[jknas] "），可空
}

type containerState struct {
	running      bool
	restartCount int
	notifiedDown bool
	restartTimes []time.Time
	notifyAt     map[string]time.Time
}

type Watchdog struct {
	cli     *client.Client
	cfg     Config
	mu      sync.Mutex
	states  map[string]*containerState
	silence map[string]time.Time // 面板主动操作窗口
}

func New(cli *client.Client, cfg Config) *Watchdog {
	return &Watchdog{
		cli:     cli,
		cfg:     cfg,
		states:  map[string]*containerState{},
		silence: map[string]time.Time{},
	}
}

// hostTag 取通知标题的主机标记；未注入时返回空串
func (w *Watchdog) hostTag() string {
	if w == nil || w.cfg.HostTag == nil {
		return ""
	}
	return w.cfg.HostTag()
}

// Start 启动后台巡检
func (w *Watchdog) Start() {
	if w == nil {
		return
	}
	go func() {
		time.Sleep(baselineDelay)
		for {
			w.Tick()
			time.Sleep(checkInterval)
		}
	}()
}

// NoteMaintenance 登记面板主动操作窗口（停止/重启/更新），窗口内不告警
func (w *Watchdog) NoteMaintenance(name string, d time.Duration) {
	if w == nil || name == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.silence[name] = time.Now().Add(d)
}

// Tick 执行一轮巡检（导出便于测试）
func (w *Watchdog) Tick() {
	if w == nil || w.cli == nil {
		return
	}
	if w.cfg.Disabled != nil && w.cfg.Disabled() {
		return
	}
	ctx := context.Background()
	list, err := w.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		logx.Errorf("守护巡检失败: %v", err)
		return
	}
	now := time.Now()

	w.mu.Lock()
	defer w.mu.Unlock()
	for _, c := range list {
		if len(c.Names) == 0 {
			continue
		}
		name := strings.TrimPrefix(c.Names[0], "/")
		ins, err := w.cli.ContainerInspect(ctx, c.ID)
		if err != nil || ins.State == nil {
			continue
		}
		st := ins.State
		prev := w.states[name]
		if prev == nil {
			// 首次见到的容器：记基线；但"最近几分钟内异常退出"的也要告警
			// （覆盖面板重启期间崩溃、开机后起不来这类没被观察到的崩溃）
			cur := &containerState{
				running:      st.Running,
				restartCount: ins.RestartCount,
				notifyAt:     map[string]time.Time{},
			}
			w.states[name] = cur
			inLoop := st.Restarting || ins.RestartCount >= restartTrigger
			if !st.Running && !inLoop && (st.OOMKilled || st.ExitCode != 0) && !now.Before(w.silence[name]) {
				if fin, perr := time.Parse(time.RFC3339Nano, st.FinishedAt); perr == nil && now.Sub(fin) < recentCrashWindow {
					if w.allowNotify(cur, "down", now) {
						cause := fmt.Sprintf("退出码 %d", st.ExitCode)
						if st.OOMKilled {
							cause = fmt.Sprintf("被 OOM 杀掉（内存不足，退出码 %d）", st.ExitCode)
						}
						w.notify(
							"🔴 "+w.hostTag()+"容器异常退出："+name,
							fmt.Sprintf("容器 %s %s\n时间：%s\n可在 DockHamster 容器页查看并重启", name, cause, fin.Local().Format("2006-01-02 15:04:05")),
						)
						cur.notifiedDown = true
					}
				}
			}
			continue
		}
		if prev.notifyAt == nil {
			prev.notifyAt = map[string]time.Time{}
		}
		silent := now.Before(w.silence[name])

		// ① 运行 → 停止：异常退出才告警（退出码 0 视为正常停止）
		if prev.running && !st.Running {
			abnormal := st.OOMKilled || st.ExitCode != 0
			if abnormal && !silent && w.allowNotify(prev, "down", now) {
				cause := fmt.Sprintf("退出码 %d", st.ExitCode)
				if st.OOMKilled {
					cause = fmt.Sprintf("被 OOM 杀掉（内存不足，退出码 %d）", st.ExitCode)
				}
				w.notify(
					"🔴 "+w.hostTag()+"容器异常退出："+name,
					fmt.Sprintf("容器 %s %s\n时间：%s\n可在 DockHamster 容器页查看并重启", name, cause, now.Format("2006-01-02 15:04:05")),
				)
				prev.notifiedDown = true
			}
		}

		// ② 停止 → 运行：之前告警过的容器恢复运行
		if !prev.running && st.Running && prev.notifiedDown {
			if w.allowNotify(prev, "up", now) {
				w.notify(
					"🟢 "+w.hostTag()+"容器已恢复运行："+name,
					fmt.Sprintf("容器 %s 已重新运行\n时间：%s", name, now.Format("2006-01-02 15:04:05")),
				)
			}
			prev.notifiedDown = false
		}

		// ③ 重启循环：统计窗口内重启次数
		if ins.RestartCount > prev.restartCount {
			for i := 0; i < ins.RestartCount-prev.restartCount; i++ {
				prev.restartTimes = append(prev.restartTimes, now)
			}
		}
		cut := now.Add(-restartWindow)
		kept := prev.restartTimes[:0]
		for _, t := range prev.restartTimes {
			if t.After(cut) {
				kept = append(kept, t)
			}
		}
		prev.restartTimes = kept
		if len(prev.restartTimes) >= restartTrigger && !silent && w.allowNotify(prev, "restart", now) {
			w.notify(
				"♻️ "+w.hostTag()+"容器反复重启："+name,
				fmt.Sprintf("容器 %s 最近 %d 分钟内重启 %d 次，可能存在配置错误或依赖异常\n建议到面板查看容器日志/状态",
					name, int(restartWindow.Minutes()), len(prev.restartTimes)),
			)
			prev.notifiedDown = true
		}

		prev.running = st.Running
		prev.restartCount = ins.RestartCount
	}
}

// allowNotify 同类告警冷却控制
func (w *Watchdog) allowNotify(s *containerState, key string, now time.Time) bool {
	if last, ok := s.notifyAt[key]; ok && now.Sub(last) < notifyCooldown {
		return false
	}
	s.notifyAt[key] = now
	return true
}

func (w *Watchdog) notify(title, text string) {
	logx.Info(title + " | " + strings.ReplaceAll(text, "\n", " "))
	if w.cfg.Notify != nil {
		w.cfg.Notify(title, text)
	}
}
