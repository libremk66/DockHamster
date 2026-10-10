package utiles

import (
	"strings"
	"testing"
)

// 更新前提醒的文案：标题带时间、逐个列出容器、包含"保存工作"与"另发简报"的说明
func TestComposeBeforeUpdateMessage(t *testing.T) {
	title, text := composeBeforeUpdateMessage("[jknas] ", []string{"jellyfin", "qbittorrent", "immich_server"}, "2026-10-10 04:00")
	if title != "⏳ [jknas] DockHamster 更新即将开始 · 2026-10-10 04:00" {
		t.Fatalf("标题不对: %q", title)
	}
	for _, want := range []string{
		"将更新 3 个容器",
		"· jellyfin", "· qbittorrent", "· immich_server",
		"请先保存工作",
		"另发结果简报",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("正文缺少 %q:\n%s", want, text)
		}
	}
}
