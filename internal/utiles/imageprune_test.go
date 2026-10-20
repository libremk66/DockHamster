package utiles

import (
	"testing"
	"time"
)

func TestIntermediateAgeEligible(t *testing.T) {
	now := time.Now()
	if intermediateAgeEligible(now.Add(-1*time.Hour).Unix(), 24, now) {
		t.Fatal("1 小时前的层不应进入 24h 可清理统计")
	}
	if !intermediateAgeEligible(now.Add(-25*time.Hour).Unix(), 24, now) {
		t.Fatal("25 小时前的层应进入可清理统计")
	}
	if !intermediateAgeEligible(now.Unix(), 0, now) {
		t.Fatal("untilHours<=0 应视为不过滤")
	}
}
