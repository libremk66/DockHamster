package module

// 私有镜像仓库凭据（ghcr.io / Docker Hub / 自建 registry）。
//
// 背景：私有镜像（如 ghcr.io 的私有包）在匿名查询时会得到 unauthorized，
// 面板此前把它一律标成"无法检测"，更新也会因未授权失败。
// 这里按 registry host 存一份凭据，供三个出口使用：
//   ① 更新检测 DistributionInspect（X-Registry-Auth：base64url(JSON)）
//   ② 更新检测的 HTTP 回退 GetToken（Basic：base64("user:pass")）
//   ③ 拉取镜像 ImagePull（PullOptions.RegistryAuth：同 ①）
// 注意 ① 与 ② 的编码格式**不同**，不要混用。

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/distribution/reference"
	"github.com/docker/docker/api/types/registry"
	"github.com/zeromicro/go-zero/core/logx"
)

// RegistryAuthEntry 一条私有仓库凭据（按规范化后的 registry host 唯一）
type RegistryAuthEntry struct {
	Registry string `json:"registry"`
	Username string `json:"username,optional"`
	Password string `json:"password,optional"`
}

// RegistryAuthSettings 全部凭据（持久化到 /data/config/registry-auth.json）
type RegistryAuthSettings struct {
	Entries []RegistryAuthEntry `json:"entries"`
}

type RegistryAuthStore struct {
	mu   sync.RWMutex
	data RegistryAuthSettings
	path string
}

func registryAuthConfigPath() string {
	if p := strings.TrimSpace(os.Getenv("RegistryAuthConfigFile")); p != "" {
		return p
	}
	return "/data/config/registry-auth.json"
}

func NewRegistryAuthStore() *RegistryAuthStore {
	s := &RegistryAuthStore{path: registryAuthConfigPath()}
	s.load()
	return s
}

// Get 返回配置副本（密码字段保留——仅限后端内部使用，API 出口必须脱敏）
func (s *RegistryAuthStore) Get() RegistryAuthSettings {
	if s == nil {
		return RegistryAuthSettings{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := RegistryAuthSettings{Entries: append([]RegistryAuthEntry{}, s.data.Entries...)}
	return out
}

func (s *RegistryAuthStore) Save(in RegistryAuthSettings) error {
	if s == nil {
		return nil
	}
	normalized := NormalizeRegistryAuthSettings(in)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return err
	}
	// 0600：凭据明文落盘，只允许运行用户读写（与其它配置文件一致）
	if err := os.WriteFile(s.path, b, 0600); err != nil {
		return err
	}
	s.data = normalized
	return nil
}

func (s *RegistryAuthStore) load() {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return // 首次运行：没有配置
	}
	var loaded RegistryAuthSettings
	if err := json.Unmarshal(b, &loaded); err != nil {
		logx.Errorf("解析私有仓库凭据失败(忽略): %v", err)
		return
	}
	s.data = NormalizeRegistryAuthSettings(loaded)
}

// NormalizeRegistryHost 清洗 registry 为纯 host[:port]：去 scheme/路径、小写；
// Docker Hub 的几种写法统一成 docker.io。空/非法（含 @）返回空字符串。
func NormalizeRegistryHost(raw string) string {
	h := strings.TrimSpace(raw)
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	if i := strings.IndexAny(h, "/?# "); i != -1 {
		h = h[:i]
	}
	h = strings.ToLower(strings.Trim(h, "/"))
	switch h {
	case "index.docker.io", "registry-1.docker.io", "registry.docker.io":
		h = "docker.io"
	}
	if h == "" || strings.ContainsAny(h, "@\\") {
		return ""
	}
	return h
}

// RegistryHostOf 从镜像引用解析 registry host（无域名时即 docker.io）；无效引用返回 ""。
func RegistryHostOf(imageRef string) string {
	r := strings.TrimSpace(imageRef)
	if r == "" {
		return ""
	}
	named, err := reference.ParseNormalizedNamed(r)
	if err != nil {
		return ""
	}
	return NormalizeRegistryHost(reference.Domain(named))
}

// NormalizeRegistryAuthSettings 归一化：registry 清洗后去重（后者覆盖前者、保留首次出现的位置），
// 丢弃 registry 为空、且用户名密码都空的条目。
func NormalizeRegistryAuthSettings(in RegistryAuthSettings) RegistryAuthSettings {
	out := RegistryAuthSettings{Entries: []RegistryAuthEntry{}}
	idx := map[string]int{}
	for _, e := range in.Entries {
		host := NormalizeRegistryHost(e.Registry)
		user := strings.TrimSpace(e.Username)
		pass := e.Password // 密码不 trim：前后空格可能是有效字符
		if host == "" || (user == "" && pass == "") {
			continue
		}
		entry := RegistryAuthEntry{Registry: host, Username: user, Password: pass}
		if i, ok := idx[host]; ok {
			out.Entries[i] = entry // 同 host 后写覆盖
			continue
		}
		idx[host] = len(out.Entries)
		out.Entries = append(out.Entries, entry)
	}
	return out
}

// PreserveRegistryPasswords 空密码沿用旧值（UI 不回显密码，编辑时留空即"保持不变"）。
func PreserveRegistryPasswords(old, in RegistryAuthSettings) RegistryAuthSettings {
	prev := map[string]string{}
	for _, e := range NormalizeRegistryAuthSettings(old).Entries {
		prev[e.Registry] = e.Password
	}
	for i, e := range in.Entries {
		if strings.TrimSpace(e.Password) == "" {
			if p, ok := prev[NormalizeRegistryHost(e.Registry)]; ok {
				in.Entries[i].Password = p
			}
		}
	}
	return in
}

// Lookup 按镜像引用里的 registry host 查凭据。
func (s *RegistryAuthStore) Lookup(imageRef string) (RegistryAuthEntry, bool) {
	if s == nil {
		return RegistryAuthEntry{}, false
	}
	host := RegistryHostOf(imageRef)
	if host == "" {
		return RegistryAuthEntry{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.data.Entries {
		if e.Registry == host {
			return e, true
		}
	}
	return RegistryAuthEntry{}, false
}

// DaemonAuth 返回 X-Registry-Auth / PullOptions.RegistryAuth 需要的编码
// （base64url(JSON AuthConfig)）。无凭据时返回空串（= 匿名）。
func (s *RegistryAuthStore) DaemonAuth(imageRef string) string {
	e, ok := s.Lookup(imageRef)
	if !ok {
		return ""
	}
	encoded, err := registry.EncodeAuthConfig(registry.AuthConfig{
		Username:      e.Username,
		Password:      e.Password,
		ServerAddress: e.Registry,
	})
	if err != nil {
		logx.Errorf("编码仓库凭据失败（按匿名处理）: %v", err)
		return ""
	}
	return encoded
}

// BasicAuth 返回 HTTP 回退（module.GetToken）需要的 base64("user:pass")。
// 与 DaemonAuth 的编码格式不同：那里是 JSON，这里是 user:pass。
func (s *RegistryAuthStore) BasicAuth(imageRef string) string {
	e, ok := s.Lookup(imageRef)
	if !ok {
		return ""
	}
	return base64.StdEncoding.EncodeToString([]byte(e.Username + ":" + e.Password))
}

// HasAuth 该引用所属的 registry 是否配置了凭据（用于区分"缺凭据"与"凭据被拒"）。
func (s *RegistryAuthStore) HasAuth(imageRef string) bool {
	_, ok := s.Lookup(imageRef)
	return ok
}

// RegistryAuthLookup 供 module.CheckUpdate 使用（避免把整个 store 传进检测层）。
type RegistryAuthLookup interface {
	DaemonAuth(imageRef string) string
	BasicAuth(imageRef string) string
	HasAuth(imageRef string) bool
}
