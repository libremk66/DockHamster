package auth

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoginLimiterBlocksAfterMaxFailures(t *testing.T) {
	l := &loginAttemptLimiter{attempts: make(map[string]loginAttempt)}
	now := time.Unix(1000000, 0)

	// 新客户端：放行
	if ok, _ := l.allow("1.2.3.4", now); !ok {
		t.Fatal("new client should be allowed")
	}

	// 失败 4 次：仍放行（未达阈值）
	for i := 0; i < maxLoginFailures-1; i++ {
		l.record("1.2.3.4", false, now)
		if ok, _ := l.allow("1.2.3.4", now); !ok {
			t.Fatalf("should still be allowed after %d failures", i+1)
		}
	}

	// 第 5 次失败：触发封锁
	l.record("1.2.3.4", false, now)
	if ok, retry := l.allow("1.2.3.4", now); ok {
		t.Fatal("should be blocked after max failures")
	} else if retry <= 0 || retry > loginBlockDuration {
		t.Fatalf("retry duration out of range: %v", retry)
	}
}

func TestLoginLimiterBlockExpires(t *testing.T) {
	l := &loginAttemptLimiter{attempts: make(map[string]loginAttempt)}
	now := time.Unix(1000000, 0)

	for i := 0; i < maxLoginFailures; i++ {
		l.record("1.2.3.4", false, now)
	}
	if ok, _ := l.allow("1.2.3.4", now); ok {
		t.Fatal("should be blocked")
	}

	// 封锁期过后恢复放行
	later := now.Add(loginBlockDuration + time.Second)
	if ok, _ := l.allow("1.2.3.4", later); !ok {
		t.Fatal("should be allowed after block expires")
	}
}

func TestLoginLimiterSuccessResets(t *testing.T) {
	l := &loginAttemptLimiter{attempts: make(map[string]loginAttempt)}
	now := time.Unix(1000000, 0)

	// 失败 4 次 → 成功 → 再失败 4 次，不应被封（计数已清零）
	for i := 0; i < maxLoginFailures-1; i++ {
		l.record("1.2.3.4", false, now)
	}
	l.record("1.2.3.4", true, now)
	for i := 0; i < maxLoginFailures-1; i++ {
		l.record("1.2.3.4", false, now)
		if ok, _ := l.allow("1.2.3.4", now); !ok {
			t.Fatalf("should be allowed after reset, failed at round %d", i+1)
		}
	}
}

func TestLoginLimiterWindowResets(t *testing.T) {
	l := &loginAttemptLimiter{attempts: make(map[string]loginAttempt)}
	now := time.Unix(1000000, 0)

	// 4 次失败 → 等过窗口 → 再 4 次失败：不应被封（窗口已滚动）
	for i := 0; i < maxLoginFailures-1; i++ {
		l.record("1.2.3.4", false, now)
	}
	later := now.Add(loginFailureWindow + time.Second)
	for i := 0; i < maxLoginFailures-1; i++ {
		l.record("1.2.3.4", false, later)
		if ok, _ := l.allow("1.2.3.4", later); !ok {
			t.Fatalf("should be allowed after window roll, failed at round %d", i+1)
		}
	}
}

func TestLoginLimiterKeysAreIndependent(t *testing.T) {
	l := &loginAttemptLimiter{attempts: make(map[string]loginAttempt)}
	now := time.Unix(1000000, 0)

	// A 被封不影响 B
	for i := 0; i < maxLoginFailures; i++ {
		l.record("10.0.0.1", false, now)
	}
	if ok, _ := l.allow("10.0.0.1", now); ok {
		t.Fatal("A should be blocked")
	}
	if ok, _ := l.allow("10.0.0.2", now); !ok {
		t.Fatal("B should not be affected")
	}
}

func TestLoginClientKey(t *testing.T) {
	cases := []struct {
		name       string
		realIP     string
		xff        string
		remoteAddr string
		want       string
	}{
		{"X-Real-IP 优先", "1.1.1.1", "2.2.2.2, 3.3.3.3", "4.4.4.4:1234", "1.1.1.1"},
		{"XFF 首段", "", "2.2.2.2, 3.3.3.3", "4.4.4.4:1234", "2.2.2.2"},
		{"XFF 非法则回退", "", "not-an-ip", "4.4.4.4:1234", "4.4.4.4"},
		{"无头走 RemoteAddr", "", "", "4.4.4.4:1234", "4.4.4.4"},
		{"RemoteAddr 无端口", "", "", "4.4.4.4", "4.4.4.4"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/api/auth", nil)
		if c.realIP != "" {
			r.Header.Set("X-Real-IP", c.realIP)
		}
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		r.RemoteAddr = c.remoteAddr
		if got := loginClientKey(r); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
