package utiles

import (
	"context"
	"strings"
	"sync"

	"github.com/libremk66/DockHamster/internal/svc"
)

// 通知标题里的"主机标记"：一个通知渠道可能同时接多个 DockHamster（甚至同机多个面板），
// 标题带上主机名，用户一眼能分辨是谁发的。格式：🔄 [jknas] DockHamster 自动更新 · 04:00
//
// 取值优先级：设置里的自定义 HostLabel → daemon 视角的主机名（docker info 的 Name）。
// 注意：容器内的 os.Hostname() 是容器 ID 前缀（如 913875925d65），没有可读性，不能用。

var (
	daemonHostOnce sync.Once
	daemonHostVal  string
)

// daemonHostname 读 daemon 视角的主机名；失败给空串。只探测一次（主机名不会变）。
func daemonHostname(svcCtx *svc.ServiceContext) string {
	daemonHostOnce.Do(func() {
		if svcCtx == nil || svcCtx.DockerClient == nil {
			return
		}
		if info, err := svcCtx.DockerClient.Info(context.Background()); err == nil {
			daemonHostVal = strings.TrimSpace(info.Name)
		}
	})
	return daemonHostVal
}

// ResolvedHostLabel 解析当前生效的主机标识（自定义优先，其次 daemon 主机名）；拿不到返回空串。
func ResolvedHostLabel(svcCtx *svc.ServiceContext) string {
	if svcCtx != nil {
		if v := sanitizeHostLabel(svcCtx.AutoUpdate.Get().HostLabel); v != "" {
			return v
		}
	}
	return sanitizeHostLabel(daemonHostname(svcCtx))
}

// HostTag 通知标题用的标记，形如 "[jknas] "（含尾随空格）；拿不到主机标识时返回空串（标题不变）。
func HostTag(svcCtx *svc.ServiceContext) string {
	label := ResolvedHostLabel(svcCtx)
	if label == "" {
		return ""
	}
	return "[" + label + "] "
}

// sanitizeHostLabel 清洗用户/自动来源的标识：去首尾空白、换行压成空格、最多 24 个字符。
// （防换行把通知标题撑成多行，也防超长名字刷屏）
func sanitizeHostLabel(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.Join(strings.Fields(s), " ")
	rs := []rune(s)
	if len(rs) > 24 {
		s = string(rs[:24])
	}
	return s
}
