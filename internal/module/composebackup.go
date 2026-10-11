package module

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zeromicro/go-zero/core/logx"
)

// ComposeBackupSettings 「Compose 配置自动备份」设置（WebDAV），持久化到 /data/config/compose-backup.json。
// Last* 字段是最近一次执行结果（只读展示）；保存设置时由服务端保留、不由前端回传覆盖。
type ComposeBackupSettings struct {
	Enabled   bool   `json:"enabled"`
	URL       string `json:"url"`                // WebDAV 根地址（如 https://dav.example.com/dav/）
	Username  string `json:"username,optional"`  //
	Password  string `json:"password,optional"`  //
	RemoteDir string `json:"remoteDir,optional"` // 相对根地址的目录（如 dockhamster/compose）
	Cron      string `json:"cron,optional"`      // 备份计划（标准 5 段 cron），默认每天 03:00
	Keep      int    `json:"keep,optional"`      // 远端保留份数（0=不清理）
	// 最近一次结果（服务端维护）
	LastRun   string `json:"lastRun,optional"`   // 时间
	LastOK    bool   `json:"lastOk,optional"`    // 是否成功
	LastFile  string `json:"lastFile,optional"`  // 上传的文件名
	LastError string `json:"lastError,optional"` // 失败原因（成功时清空）
}

func (s *ComposeBackupSettings) Normalize() {
	s.URL = strings.TrimSpace(s.URL)
	s.Username = strings.TrimSpace(s.Username)
	s.RemoteDir = strings.Trim(strings.TrimSpace(s.RemoteDir), "/")
	if strings.TrimSpace(s.Cron) == "" {
		s.Cron = "0 3 * * *"
	}
	if s.Keep < 0 {
		s.Keep = 0
	}
	if s.Keep == 0 {
		s.Keep = 7
	}
}

type ComposeBackupStore struct {
	mu   sync.RWMutex
	data ComposeBackupSettings
	path string
}

func composeBackupConfigPath() string {
	if p := os.Getenv("ComposeBackupConfigFile"); p != "" {
		return p
	}
	return "/data/config/compose-backup.json"
}

func NewComposeBackupStore() *ComposeBackupStore {
	s := &ComposeBackupStore{path: composeBackupConfigPath()}
	s.data.Normalize()
	s.load()
	return s
}

func (s *ComposeBackupStore) Get() ComposeBackupSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data
}

// SaveSettings 保存"设置字段"（保留服务端维护的 Last* 结果字段）
func (s *ComposeBackupStore) SaveSettings(in ComposeBackupSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	in.Normalize()
	in.LastRun = s.data.LastRun
	in.LastOK = s.data.LastOK
	in.LastFile = s.data.LastFile
	in.LastError = s.data.LastError
	return s.writeLocked(in)
}

// UpdateLastRun 记录最近一次执行结果
func (s *ComposeBackupStore) UpdateLastRun(at, file string, ok bool, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.LastRun = at
	s.data.LastFile = file
	s.data.LastOK = ok
	s.data.LastError = errMsg
	if err := s.writeLocked(s.data); err != nil {
		logx.Errorf("写入备份状态失败: %v", err)
	}
}

func (s *ComposeBackupStore) writeLocked(v ComposeBackupSettings) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.path, b, 0600); err != nil {
		return err
	}
	s.data = v
	return nil
}

func (s *ComposeBackupStore) load() {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return // 首次运行保持默认
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		logx.Errorf("解析 Compose 备份配置失败(用默认值): %v", err)
	}
	s.data.Normalize()
}
