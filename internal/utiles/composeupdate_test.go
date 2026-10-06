package utiles

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAltRootPrefixes(t *testing.T) {
	t.Run("未配置时不启用兜底", func(t *testing.T) {
		t.Setenv("COMPOSE_ALT_ROOTS", "")
		if got := altRootPrefixes(); len(got) != 0 {
			t.Fatalf("未配置时 = %v, want 空", got)
		}
	})
	t.Run("环境变量覆盖并去除空段", func(t *testing.T) {
		t.Setenv("COMPOSE_ALT_ROOTS", " /a , ,/b ")
		got := altRootPrefixes()
		want := []string{"/a", "/b"}
		if len(got) != len(want) {
			t.Fatalf("解析结果 = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("解析结果 = %v, want %v", got, want)
			}
		}
	})
}

func TestResolveConfigFiles(t *testing.T) {
	t.Run("原路径可达时原样返回", func(t *testing.T) {
		dir := t.TempDir()
		f := filepath.Join(dir, "docker-compose.yml")
		if err := os.WriteFile(f, []byte("services: {}"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := ResolveConfigFiles([]string{f})
		if err != nil {
			t.Fatalf("意外错误: %v", err)
		}
		if got[0] != f {
			t.Fatalf("解析结果 = %s, want %s", got[0], f)
		}
	})

	t.Run("原路径不可达时按备用根前缀命中", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("COMPOSE_ALT_ROOTS", dir)
		rel := "/no-such-root-xyz/docker-compose.yml"
		target := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("services: {}"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := ResolveConfigFiles([]string{rel})
		if err != nil {
			t.Fatalf("意外错误: %v", err)
		}
		if got[0] != target {
			t.Fatalf("解析结果 = %s, want %s", got[0], target)
		}
	})

	t.Run("全部不可达时返回错误", func(t *testing.T) {
		t.Setenv("COMPOSE_ALT_ROOTS", t.TempDir())
		_, err := ResolveConfigFiles([]string{"/no-such-root-xyz/missing.yml"})
		if err == nil {
			t.Fatal("期望错误，实际为 nil")
		}
	})

	t.Run("多根按顺序探测", func(t *testing.T) {
		first, second := t.TempDir(), t.TempDir()
		t.Setenv("COMPOSE_ALT_ROOTS", first+","+second)
		rel := "/multi-root/docker-compose.yml"
		target := filepath.Join(second, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("services: {}"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := ResolveConfigFiles([]string{rel})
		if err != nil {
			t.Fatalf("意外错误: %v", err)
		}
		if got[0] != target {
			t.Fatalf("解析结果 = %s, want %s", got[0], target)
		}
	})
}
