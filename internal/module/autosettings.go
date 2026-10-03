package module

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
)

// AutoUpdateSettings 自动更新配置（由 UI 读写并持久化到 /data/config/autoUpdate.json；
// 环境变量仅作为首次生成配置时的默认值，之后以文件为准）
type AutoUpdateSettings struct {
	Enabled         bool           `json:"enabled"`
	Containers      []string       `json:"containers"`
	Exclude         []string       `json:"exclude"`
	Cron            string         `json:"cron"`
	DeleteOldImage  bool           `json:"deleteOldImage"`
	Notify          NotifyChannels `json:"notify"`                  // 通知渠道（飞书/企业微信/钉钉/Bark/Server酱/Telegram/自定义）
	FeishuWebhook   string         `json:"feishuWebhook,optional"`  // 已弃用：加载时自动迁移到 notify.feishu
	NotifyOnSuccess bool           `json:"notifyOnSuccess"`
	NotifyOnFailure bool           `json:"notifyOnFailure"`
}

type AutoUpdateStore struct {
	mu   sync.RWMutex
	data AutoUpdateSettings
	path string
}

func autoUpdateConfigPath() string {
	if p := os.Getenv("AutoUpdateConfigFile"); p != "" {
		return p
	}
	return "/data/config/autoUpdate.json"
}

func NewAutoUpdateStore() *AutoUpdateStore {
	s := &AutoUpdateStore{path: autoUpdateConfigPath(), data: defaultAutoSettings()}
	s.load()
	return s
}

func (s *AutoUpdateStore) Get() AutoUpdateSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d := s.data
	d.Containers = append([]string{}, d.Containers...)
	d.Exclude = append([]string{}, d.Exclude...)
	return d
}

func (s *AutoUpdateStore) Save(newSet AutoUpdateSettings) error {
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

func (s *AutoUpdateStore) load() {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return // 首次运行：保持环境变量默认值
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		logx.Errorf("解析自动更新配置失败(用默认值): %v", err)
	}
	// 兼容旧版单渠道字段：feishuWebhook → notify.feishu
	if s.data.FeishuWebhook != "" && !s.data.Notify.Feishu.Enabled {
		f := s.data.Notify.Feishu
		f.Enabled = true
		if f.Webhook == "" {
			f.Webhook = s.data.FeishuWebhook
		}
		s.data.Notify.Feishu = f
	}
	s.data.FeishuWebhook = ""
}

func defaultAutoSettings() AutoUpdateSettings {
	se := AutoUpdateSettings{
		Enabled:         false,
		Containers:      []string{},
		Exclude:         []string{},
		Cron:            "0 4 * * *",
		DeleteOldImage:  true,
		NotifyOnSuccess: true,
		NotifyOnFailure: true,
	}
	if v := strings.TrimSpace(os.Getenv("AutoUpdateContainers")); v != "" {
		se.Containers = SplitNameList(v)
		se.Enabled = true
	}
	if v := strings.TrimSpace(os.Getenv("AutoUpdateExclude")); v != "" {
		se.Exclude = SplitNameList(v)
	}
	if v := strings.TrimSpace(os.Getenv("AutoUpdateCron")); v != "" {
		se.Cron = v
	}
	if os.Getenv("DeleteOldImage") == "false" {
		se.DeleteOldImage = false
	}
	if v := strings.TrimSpace(os.Getenv("FeishuWebhook")); v != "" {
		se.Notify.Feishu = NotifyChannel{Enabled: true, Webhook: v}
	}
	return se
}

// SplitNameList 解析逗号分隔的名称列表（去空白）
func SplitNameList(raw string) []string {
	out := []string{}
	for _, p := range strings.Split(raw, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ---- 运行记录与状态 ----

type AutoRunFailure struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

type AutoUpdateRunResult struct {
	Time          string          `json:"time"`
	Trigger       string          `json:"trigger"` // auto | manual | group
	Updated       []string        `json:"updated"`
	Failed        []AutoRunFailure `json:"failed"`
	CleanedImages int             `json:"cleanedImages"`
	DurationSec   float64         `json:"durationSec"`
	Note          string          `json:"note,omitempty"`
}

type ContainerAutoStatus struct {
	Time    string `json:"time"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// ActiveTask 进行中的更新任务（供 status 接口实时展示进度）
type ActiveTask struct {
	Name   string `json:"name"`
	TaskID string `json:"taskID"`
}

// AutoUpdateState 自动更新运行状态（内存，最近 30 次记录；重启即清空）
type AutoUpdateState struct {
	mu      sync.Mutex
	running bool
	runs    []AutoUpdateRunResult
	last    map[string]ContainerAutoStatus
	active  []ActiveTask
}

func NewAutoUpdateState() *AutoUpdateState {
	return &AutoUpdateState{
		runs: []AutoUpdateRunResult{},
		last: map[string]ContainerAutoStatus{},
	}
}

// SetActive 登记本轮进行中的任务（run 开始时调用）
func (s *AutoUpdateState) SetActive(tasks []ActiveTask) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = append([]ActiveTask{}, tasks...)
}

// ClearActive 清空进行中任务（run 结束）
func (s *AutoUpdateState) ClearActive() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = nil
}

// TryStart 防并发：已有任务运行中则返回 false
func (s *AutoUpdateState) TryStart() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return false
	}
	s.running = true
	return true
}

func (s *AutoUpdateState) Finish() {
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
}

func (s *AutoUpdateState) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *AutoUpdateState) AddRun(r AutoUpdateRunResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs = append([]AutoUpdateRunResult{r}, s.runs...)
	if len(s.runs) > 30 {
		s.runs = s.runs[:30]
	}
}

func (s *AutoUpdateState) SetContainer(name string, ok bool, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last[name] = ContainerAutoStatus{Time: time.Now().Format("2006-01-02 15:04:05"), OK: ok, Message: message}
}

// Snapshot 返回状态快照（并发安全）
func (s *AutoUpdateState) Snapshot() (bool, []AutoUpdateRunResult, map[string]ContainerAutoStatus, []ActiveTask) {
	s.mu.Lock()
	defer s.mu.Unlock()
	runs := append([]AutoUpdateRunResult{}, s.runs...)
	last := make(map[string]ContainerAutoStatus, len(s.last))
	for k, v := range s.last {
		last[k] = v
	}
	active := append([]ActiveTask{}, s.active...)
	return s.running, runs, last, active
}
