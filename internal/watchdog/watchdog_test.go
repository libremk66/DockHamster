package watchdog

import "testing"

func TestBackupBaseName(t *testing.T) {
	tests := []struct {
		name     string
		wantBase string
		wantOK   bool
	}{
		{"nginx-2026-10-22-15-04-05", "nginx", true},
		{"myapp-old-2026-10-22-15-04-05", "myapp", true},
		{"foo-bar-2026-10-22-15-04-05", "foo-bar", true},
		{"myapp", "", false},
		{"myapp-2026-10-22-15-04", "", false},
		{"myapp-2026-10-22-15-04-05-extra", "", false},
		{"myapp-old", "", false},
	}
	for _, tt := range tests {
		base, ok := backupBaseName(tt.name)
		if base != tt.wantBase || ok != tt.wantOK {
			t.Fatalf("backupBaseName(%q) = (%q, %v), want (%q, %v)", tt.name, base, ok, tt.wantBase, tt.wantOK)
		}
	}
}

func TestShouldIgnoreBackup(t *testing.T) {
	running := map[string]bool{"myapp": true, "dockhamster": true}
	tests := []struct {
		name string
		want bool
	}{
		{"myapp-2026-10-22-15-04-05", true},
		{"dockhamster-old-2026-10-22-15-04-05", true},
		{"other-2026-10-22-15-04-05", false}, // 没有对应的运行中容器，不静默
		{"myapp", false},                     // 正常容器名不是备份后缀
	}
	for _, tt := range tests {
		if got := shouldIgnoreBackup(tt.name, running); got != tt.want {
			t.Fatalf("shouldIgnoreBackup(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
