package utiles

import (
	"testing"
	"time"
)

func TestFilterProjectVolumesByName(t *testing.T) {
	vols := []UnusedVolumeItem{
		{Name: "demoapp_cache-data", Project: "demoapp"},
		{Name: "legacy_dbdata", Project: "legacy"},
		{Name: "somevolume"},
	}

	// 正常命中 + 去重/去空白
	picked, missing := filterProjectVolumesByName(vols, []string{" legacy_dbdata ", "demoapp_cache-data", "legacy_dbdata"})
	if len(picked) != 2 || len(missing) != 0 {
		t.Fatalf("picked=%+v missing=%+v", picked, missing)
	}
	// 按扫描顺序返回（demoapp 在 legacy 之前）
	if picked[0].Name != "demoapp_cache-data" || picked[1].Name != "legacy_dbdata" {
		t.Fatalf("顺序应按扫描结果: %+v", picked)
	}

	// 名单里查不到的名字要单独返回（不能静默）
	_, missing = filterProjectVolumesByName(vols, []string{"nope", "demoapp_cache-data"})
	if len(missing) != 1 || missing[0] != "nope" {
		t.Fatalf("missing=%+v", missing)
	}

	// 空名单 → 不选中任何卷
	if picked, missing := filterProjectVolumesByName(vols, nil); len(picked) != 0 || len(missing) != 0 {
		t.Fatalf("空名单应无结果: %+v %+v", picked, missing)
	}

	// 安全属性：匿名卷/在用卷不会出现在 ProjectVolumes 列表里，名单自然也命中不了
	if picked, missing := filterProjectVolumesByName(nil, []string{"a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"}); len(picked) != 0 || len(missing) != 1 {
		t.Fatalf("匿名卷不应被命中: %+v %+v", picked, missing)
	}
}

func TestVolumeAgeSkipReason(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name    string
		created string
		hours   int
		skip    bool
	}{
		{"new volume", now.Add(-1 * time.Hour).Format(time.RFC3339), 24, true},
		{"old volume", now.Add(-25 * time.Hour).Format(time.RFC3339), 24, false},
		{"invalid time", "not-a-time", 24, true},
		{"zero window", now.Add(-48 * time.Hour).Format(time.RFC3339), 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := volumeAgeSkipReason(tc.created, tc.hours)
			if (reason != "") != tc.skip {
				t.Fatalf("volumeAgeSkipReason(%q, %d) = %q, skip=%v", tc.created, tc.hours, reason, tc.skip)
			}
		})
	}
}

func TestVolumeAgeOKBoundary(t *testing.T) {
	now := time.Now()
	if volumeAgeOK(now.Add(-23*time.Hour).Format(time.RFC3339), 24) {
		t.Fatal("23h old volume should not pass 24h window")
	}
	if !volumeAgeOK(now.Add(-25*time.Hour).Format(time.RFC3339), 24) {
		t.Fatal("25h old volume should pass 24h window")
	}
	if volumeAgeOK("bad-time", 24) {
		t.Fatal("unparsable time must be treated as unsafe")
	}
}
