package utiles

import (
	"testing"
	"time"
)

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
