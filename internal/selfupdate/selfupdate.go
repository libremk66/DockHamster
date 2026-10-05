// Package selfupdate 实现 DockHamster 的面板自更新（接力容器方案）。
//
// 容器没法"停掉自己再重建"（stop 自己的瞬间整个流程就断了），所以：
//
//	主容器：拉新镜像 → 用【新镜像】启动一次性接力容器（AutoRemove）→ 应答完前端后等着被替换
//	接力容器：停旧容器 → 改名备份 → 用旧容器完整配置 + 新镜像重建同名容器 →
//	          启动并校验稳定 → 成功删备份 / 失败自动回滚旧容器 → 结果写 /data
//	新容器：启动时读结果文件 → 写日志 + 发通知（结果只上报一次）
//
// 关键前提：新镜像里必须包含本包（接力模式靠新镜像里的二进制执行）。
package selfupdate

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	lastMu     sync.Mutex
	lastResult *Result
)

func setLastResult(r Result) {
	lastMu.Lock()
	defer lastMu.Unlock()
	lastResult = &r
}

// LastResult 返回本进程启动时上报过的自更新结果（若有）
func LastResult() (Result, bool) {
	lastMu.Lock()
	defer lastMu.Unlock()
	if lastResult == nil {
		return Result{}, false
	}
	return *lastResult, true
}

const (
	// EnvFlag 置为 "1" 时进程以接力模式运行（main 最先分支处理）
	EnvFlag = "DH_SELF_UPDATE"
	// EnvTarget 待更新的容器 ID（即面板自身容器）
	EnvTarget = "DH_SELF_UPDATE_TARGET"
	// EnvImage 更新目标镜像（主容器已拉取完成）
	EnvImage = "DH_SELF_UPDATE_IMAGE"

	// UpdaterName 接力容器固定命名，便于清理残留
	UpdaterName = "dockhamster-self-updater"

	resultFile = "/data/config/selfupdate-result.json"
	logFile    = "/data/config/selfupdate.log"
)

// Result 接力容器写入 /data 的执行结果，由新容器启动时上报
type Result struct {
	Status string `json:"status"` // success / failed
	Name   string `json:"name"`
	Image  string `json:"image"`
	Error  string `json:"error,omitempty"`
	At     string `json:"at"`
}

func relayLog(format string, args ...interface{}) {
	line := fmt.Sprintf("[self-updater] %s %s", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
	fmt.Println(line)
	if f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		_, _ = f.WriteString(line + "\n")
		_ = f.Close()
	}
}

func writeResult(res Result) {
	res.At = time.Now().Format("2006-01-02 15:04:05")
	if b, err := json.MarshalIndent(res, "", "  "); err == nil {
		_ = os.WriteFile(resultFile, b, 0644)
	}
}

// ConsumeResult 读取并删除结果文件（避免重复上报）
func ConsumeResult() (Result, bool) {
	b, err := os.ReadFile(resultFile)
	if err != nil {
		return Result{}, false
	}
	_ = os.Remove(resultFile)
	var res Result
	if err := json.Unmarshal(b, &res); err != nil {
		return Result{}, false
	}
	return res, true
}

// CurrentContainerID 返回本进程所在容器的 ID：
// 优先显式环境变量，其次从 /proc/self/mountinfo（Docker 会把 /etc/hostname 从容器目录挂进来），
// 最后退回 hostname（默认即容器短 ID）。
func CurrentContainerID() string {
	if id := strings.TrimSpace(os.Getenv("DH_CONTAINER_ID")); id != "" {
		return id
	}
	if mountInfo, err := os.ReadFile("/proc/self/mountinfo"); err == nil {
		if id := containerIDFromMountInfo(string(mountInfo)); id != "" {
			return id
		}
	}
	hostname, _ := os.Hostname()
	return strings.TrimSpace(hostname)
}

func containerIDFromMountInfo(content string) string {
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || !isDockerIdentityMount(fields[4]) {
			continue
		}
		parts := strings.Split(fields[3], "/")
		for i := 0; i+1 < len(parts); i++ {
			if parts[i] == "containers" && isFullContainerID(parts[i+1]) {
				return strings.ToLower(parts[i+1])
			}
		}
	}
	return ""
}

func isDockerIdentityMount(path string) bool {
	switch path {
	case "/etc/hostname", "/etc/hosts", "/etc/resolv.conf":
		return true
	default:
		return false
	}
}

func isFullContainerID(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// IsSelf 判断容器 ID 是否指向本进程所在容器（支持短 ID 前缀）
func IsSelf(containerID string) bool {
	self := CurrentContainerID()
	containerID = strings.TrimSpace(containerID)
	if containerID == "" || self == "" {
		return false
	}
	return containerID == self || strings.HasPrefix(containerID, self) || strings.HasPrefix(self, containerID)
}
