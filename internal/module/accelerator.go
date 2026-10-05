package module

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zeromicro/go-zero/core/logx"
)

// BuiltinAccelerators 内置候选加速源（首次生成配置用；之后以文件为准，可增删）
var BuiltinAccelerators = []string{
	"docker.1ms.run",
	"docker.m.daocloud.io",
	"docker.nju.edu.cn",
	"dockerproxy.net",
	"docker.xuanyuan.me",
	"docker.1panel.live",
	"hub.rat.dev",
}

// AcceleratorSettings 镜像加速源配置（持久化到 /data/config/accelerator.json）
type AcceleratorSettings struct {
	Sources []string `json:"sources"`
	Default string   `json:"default"` // 默认加速源（空 = 未选）
	// UseForUpdates 开启后，容器更新/恢复拉取镜像时自动走默认加速源（失败回退直连）
	UseForUpdates bool `json:"useForUpdates"`
}

type AcceleratorStore struct {
	mu   sync.RWMutex
	data AcceleratorSettings
	path string
}

func acceleratorConfigPath() string {
	if p := strings.TrimSpace(os.Getenv("AcceleratorConfigFile")); p != "" {
		return p
	}
	return "/data/config/accelerator.json"
}

func NewAcceleratorStore() *AcceleratorStore {
	s := &AcceleratorStore{path: acceleratorConfigPath()}
	s.data = normalizeAcceleratorSettings(AcceleratorSettings{Sources: BuiltinAccelerators})
	s.load()
	return s
}

func (s *AcceleratorStore) Get() AcceleratorSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d := s.data
	d.Sources = append([]string{}, d.Sources...)
	return d
}

// UpdateSource 返回"更新时自动加速"生效的默认源（未启用或未选源时返回空）
func (s *AcceleratorStore) UpdateSource() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.data.UseForUpdates {
		return ""
	}
	return s.data.Default
}

func (s *AcceleratorStore) Save(newSet AcceleratorSettings) error {
	newSet = normalizeAcceleratorSettings(newSet)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(newSet, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.path, b, 0600); err != nil {
		return err
	}
	s.data = newSet
	return nil
}

func (s *AcceleratorStore) load() {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return // 首次运行：保持内置默认值
	}
	var loaded AcceleratorSettings
	if err := json.Unmarshal(b, &loaded); err != nil {
		logx.Errorf("解析加速源配置失败(用默认值): %v", err)
		return
	}
	s.data = normalizeAcceleratorSettings(loaded)
}

// NormalizeAcceleratorHost 清洗加速源为纯 host[:port]（去 scheme/路径/空白）。
// 非法（空、含 @ 等）返回空字符串。
func NormalizeAcceleratorHost(raw string) string {
	h := strings.TrimSpace(raw)
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	h = strings.TrimSpace(h)
	if i := strings.IndexAny(h, "/?# "); i != -1 {
		h = h[:i]
	}
	h = strings.Trim(h, "/")
	if h == "" || strings.ContainsAny(h, "@\\") {
		return ""
	}
	return strings.ToLower(h)
}

func normalizeAcceleratorSettings(in AcceleratorSettings) AcceleratorSettings {
	out := AcceleratorSettings{UseForUpdates: in.UseForUpdates, Sources: []string{}}
	seen := map[string]bool{}
	for _, raw := range in.Sources {
		if h := NormalizeAcceleratorHost(raw); h != "" && !seen[h] {
			seen[h] = true
			out.Sources = append(out.Sources, h)
		}
	}
	if len(out.Sources) == 0 {
		out.Sources = append(out.Sources, BuiltinAccelerators...)
	}
	d := NormalizeAcceleratorHost(in.Default)
	if d != "" && seen[d] {
		out.Default = d
	}
	return out
}
