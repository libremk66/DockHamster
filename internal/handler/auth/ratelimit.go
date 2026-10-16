package auth

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 登录失败限速（进程内存实现，单实例语义）。
//
// 背景：/api/auth 此前无任何速率限制——secretKey 若为低熵人工口令（满足
// "8 位以上非纯数字"的最低要求），暴力/字典可在数小时内碾过；面板常被反向代理
// 或虚拟组网暴露到公网，端口扫描是常态。启动校验（空密钥拒绝）只保证"有密码"，
// 这里补上"猜不中"的这一半。
//
// 参数参考上游同类实现（onlyLTY/dockerCopilot PR#158）：
// 5 分钟窗口内 5 次失败 → 封 15 分钟；成功登录立即清零该 IP 的计数。
const (
	loginFailureWindow = 5 * time.Minute
	loginBlockDuration = 15 * time.Minute
	maxLoginFailures   = 5
	maxLoginClients    = 10_000 // 内存上限（防 map 无限增长）
	loginClientTTL     = 30 * time.Minute
)

type loginAttempt struct {
	failures     int
	windowStart  time.Time
	blockedUntil time.Time
	lastSeen     time.Time
}

type loginAttemptLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
}

var authLimiter = &loginAttemptLimiter{attempts: make(map[string]loginAttempt)}

// allow 是否放行；被封锁时返回剩余时长。
func (l *loginAttemptLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanup(now)
	a, ok := l.attempts[key]
	if !ok {
		return true, 0
	}
	a.lastSeen = now
	l.attempts[key] = a
	if now.Before(a.blockedUntil) {
		return false, a.blockedUntil.Sub(now)
	}
	return true, 0
}

// record 记录一次登录结果；成功即清空该 key 的失败记录。
func (l *loginAttemptLimiter) record(key string, success bool, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if success {
		delete(l.attempts, key)
		return
	}
	a, ok := l.attempts[key]
	if !ok || now.Sub(a.windowStart) > loginFailureWindow {
		a = loginAttempt{windowStart: now}
	}
	a.failures++
	a.lastSeen = now
	if a.failures >= maxLoginFailures {
		a.blockedUntil = now.Add(loginBlockDuration)
		a.failures = 0
		a.windowStart = now
	}
	l.attempts[key] = a
}

// cleanup 清理过期条目，并在超限时丢弃最旧的一半（时间复杂度低，够用）。
func (l *loginAttemptLimiter) cleanup(now time.Time) {
	if len(l.attempts) == 0 {
		return
	}
	for k, a := range l.attempts {
		if now.Sub(a.lastSeen) > loginClientTTL && now.After(a.blockedUntil) {
			delete(l.attempts, k)
		}
	}
	if len(l.attempts) > maxLoginClients {
		// 超限：清掉一半最旧的（简单粗糙，但保证内存可控）
		type kv struct {
			k string
			t time.Time
		}
		all := make([]kv, 0, len(l.attempts))
		for k, a := range l.attempts {
			all = append(all, kv{k, a.lastSeen})
		}
		for i := 0; i < len(all)/2; i++ {
			oldest := i
			for j := i + 1; j < len(all); j++ {
				if all[j].t.Before(all[oldest].t) {
					oldest = j
				}
			}
			all[i], all[oldest] = all[oldest], all[i]
			delete(l.attempts, all[i].k)
		}
	}
}

// loginClientKey 提取客户端标识：优先反代头（X-Real-IP → X-Forwarded-For 首项），
// 都不可用时回退 RemoteAddr。取值必须是合法 IP，否则忽略（防伪造垃圾 key）。
func loginClientKey(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); net.ParseIP(ip) != nil {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// 取第一段（最接近客户端），做 IP 校验
		first := xff
		if i := strings.IndexByte(xff, ','); i >= 0 {
			first = xff[:i]
		}
		first = strings.TrimSpace(first)
		if net.ParseIP(first) != nil {
			return first
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
