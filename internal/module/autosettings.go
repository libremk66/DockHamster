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
	Enabled    bool     `json:"enabled"`
	Containers []string `json:"containers"`
	Exclude    []string `json:"exclude"`
	Cron       string   `json:"cron"`
	// 更新检查频率（只读探测 registry，不影响容器）；默认每小时 30 分
	CheckCron      string `json:"checkCron,optional"`
	DeleteOldImage bool   `json:"deleteOldImage"` // 兼容旧配置：true=清理；新字段 OldImagePolicy 优先
	// 旧镜像处置策略（全局默认）：clean=安全清理 / snapshot=打快照保留 / keep=不处理
	OldImagePolicy string `json:"oldImagePolicy,optional"`
	// 每容器覆盖：容器名 → inherit|clean|snapshot|keep（inherit/空 = 继承全局）
	ContainerPolicy map[string]string `json:"containerPolicy,optional"`
	// 快照保留数量（每个镜像最多保留几个快照）
	SnapshotKeep int `json:"snapshotKeep,optional"`
	// 快照命名空间与命名模板，如 dh-snap + {name}:{date}-{time}
	SnapshotPrefix   string         `json:"snapshotPrefix,optional"`
	SnapshotTemplate string         `json:"snapshotTemplate,optional"`
	Notify           NotifyChannels `json:"notify"`                 // 通知渠道（飞书/企业微信/钉钉/Bark/Server酱/Telegram/自定义）
	FeishuWebhook    string         `json:"feishuWebhook,optional"` // 已弃用：加载时自动迁移到 notify.feishu
	NotifyOnSuccess  bool           `json:"notifyOnSuccess"`
	NotifyOnFailure  bool           `json:"notifyOnFailure"`
	// WatchdogDisabled 关闭容器守护告警（异常退出/OOM/重启循环 → 通知）；默认开启
	WatchdogDisabled bool `json:"watchdogDisabled,optional"`
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
	if d.ContainerPolicy != nil {
		cp := make(map[string]string, len(d.ContainerPolicy))
		for k, v := range d.ContainerPolicy {
			cp[k] = v
		}
		d.ContainerPolicy = cp
	}
	return d
}

func (s *AutoUpdateStore) Save(newSet AutoUpdateSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	newSet.normalizePolicies()
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
	s.data.normalizePolicies()
}

// normalizePolicies 补全策略相关默认值，并兼容旧的 deleteOldImage 布尔字段
func (s *AutoUpdateSettings) normalizePolicies() {
	switch s.OldImagePolicy {
	case "clean", "snapshot", "keep":
	default:
		if s.DeleteOldImage {
			s.OldImagePolicy = "clean"
		} else {
			s.OldImagePolicy = "keep"
		}
	}
	// 保持旧字段与新策略一致，兼容旧前端/回滚
	s.DeleteOldImage = s.OldImagePolicy == "clean"
	if s.SnapshotKeep <= 0 {
		s.SnapshotKeep = 3
	}
	if strings.TrimSpace(s.SnapshotPrefix) == "" {
		s.SnapshotPrefix = "dh-snap"
	}
	if strings.TrimSpace(s.SnapshotTemplate) == "" {
		s.SnapshotTemplate = "{name}:{date}-{time}"
	}
	if strings.TrimSpace(s.CheckCron) == "" {
		s.CheckCron = "30 * * * *"
	}
	if s.ContainerPolicy == nil {
		s.ContainerPolicy = map[string]string{}
	}
}

// ResolveOldImagePolicy 解析某容器实际生效的旧镜像策略（每容器覆盖 > 全局默认）
func (s *AutoUpdateSettings) ResolveOldImagePolicy(containerName string) string {
	if p, ok := s.ContainerPolicy[containerName]; ok {
		switch strings.ToLower(strings.TrimSpace(p)) {
		case "clean", "snapshot", "keep":
			return strings.ToLower(strings.TrimSpace(p))
		}
	}
	switch s.OldImagePolicy {
	case "clean", "snapshot", "keep":
		return s.OldImagePolicy
	}
	if s.DeleteOldImage {
		return "clean"
	}
	return "keep"
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
	if v := strings.TrimSpace(os.Getenv("AutoUpdateCheckCron")); v != "" {
		se.CheckCron = v
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
	Time          string           `json:"time"`
	Trigger       string           `json:"trigger"` // auto | manual | group
	Updated       []string         `json:"updated"`
	Failed        []AutoRunFailure `json:"failed"`
	CleanedImages int              `json:"cleanedImages"`
	Snapshots     []string         `json:"snapshots,omitempty"` // 本次打下的快照引用
	DurationSec   float64          `json:"durationSec"`
	Note          string           `json:"note,omitempty"`
}

// CheckState 更新检查的运行状态（服务端持有，前端切页面不丢）
type CheckState struct {
	mu           sync.Mutex
	Running      bool
	TaskID       string
	Checked      int
	Total        int
	Trigger      string // manual | cron | startup
	NeedUpdate   int
	LastCheckAt  time.Time
	LastDuration float64 // 秒
	LastChecked  int     // 上次检查的镜像数（用于估算耗时）
}

func NewCheckState() *CheckState { return &CheckState{} }

func (c *CheckState) TryStart(taskID string, total int, trigger string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Running {
		return false
	}
	c.Running = true
	c.TaskID = taskID
	c.Checked = 0
	c.Total = total
	c.Trigger = trigger
	c.NeedUpdate = 0
	return true
}

func (c *CheckState) Progress(checked, needUpdate int) {
	c.mu.Lock()
	c.Checked = checked
	c.NeedUpdate = needUpdate
	c.mu.Unlock()
}

func (c *CheckState) Finish(checked, need int, d time.Duration) {
	c.mu.Lock()
	c.Running = false
	c.Checked = checked
	c.NeedUpdate = need
	c.LastChecked = checked
	c.LastDuration = d.Seconds()
	c.LastCheckAt = time.Now()
	c.mu.Unlock()
}

// Snapshot 供 API 读取；EstimatedSeconds 为按上次实测推算的本轮预计耗时
func (c *CheckState) Snapshot() map[string]interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	est := 0.0
	if c.LastChecked > 0 && c.LastDuration > 0 {
		est = c.LastDuration // 上次实测值（同一台机器镜像数变化不大）
	} else if c.Total > 0 {
		est = float64(c.Total) * 0.4 // 冷启动经验值：并发 6 时每个镜像约 0.4s
	}
	lastAt := ""
	if !c.LastCheckAt.IsZero() {
		lastAt = c.LastCheckAt.Format("2006-01-02 15:04")
	}
	return map[string]interface{}{
		"running":          c.Running,
		"trigger":          c.Trigger,
		"checked":          c.Checked,
		"total":            c.Total,
		"needUpdate":       c.NeedUpdate,
		"lastCheckAt":      lastAt,
		"lastDurationSec":  c.LastDuration,
		"lastChecked":      c.LastChecked,
		"estimatedSeconds": int(est + 0.5),
	}
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

// AutoUpdateState 自动更新运行状态（最近 30 次记录 + 每容器最近状态）。
// 持久化到 /data/config/autoupdate-history.json：面板自更新/版本升级重建容器后记录不丢。
type AutoUpdateState struct {
	mu       sync.Mutex
	running  bool
	runs     []AutoUpdateRunResult
	last     map[string]ContainerAutoStatus
	active   []ActiveTask
	path     string
	lastSave time.Time
}

func autoUpdateHistoryPath() string {
	if p := os.Getenv("AutoUpdateHistoryFile"); p != "" {
		return p
	}
	return "/data/config/autoupdate-history.json"
}

func NewAutoUpdateState() *AutoUpdateState {
	s := &AutoUpdateState{
		runs: []AutoUpdateRunResult{},
		last: map[string]ContainerAutoStatus{},
		path: autoUpdateHistoryPath(),
	}
	s.load()
	return s
}

// historyFile 落盘结构（与内存字段一一对应）
type historyFile struct {
	Runs []AutoUpdateRunResult          `json:"runs"`
	Last map[string]ContainerAutoStatus `json:"last"`
}

func (s *AutoUpdateState) load() {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return // 首次运行
	}
	var h historyFile
	if err := json.Unmarshal(b, &h); err != nil {
		logx.Errorf("解析自动更新历史失败(忽略): %v", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if h.Runs != nil {
		s.runs = h.Runs
		if len(s.runs) > 30 {
			s.runs = s.runs[:30]
		}
	}
	if h.Last != nil {
		s.last = h.Last
	}
}

// persistLocked 原子写盘（临时文件 + rename，避免写一半被杀留下坏文件）；调用方持锁。
// force=false 时节流：距上次写入 <2s 先跳过（SetContainer 高频调用；最终状态有 AddRun 兜底）。
func (s *AutoUpdateState) persistLocked(force bool) {
	if s.path == "" {
		return
	}
	if !force && time.Since(s.lastSave) < 2*time.Second {
		return
	}
	b, err := json.Marshal(historyFile{Runs: s.runs, Last: s.last})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		logx.Errorf("创建历史目录失败: %v", err)
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		logx.Errorf("写自动更新历史失败: %v", err)
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		logx.Errorf("替换自动更新历史失败: %v", err)
	}
	s.lastSave = time.Now()
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
	s.persistLocked(true) // 运行记录是关键数据：每次都落盘，不节流
}

func (s *AutoUpdateState) SetContainer(name string, ok bool, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last[name] = ContainerAutoStatus{Time: time.Now().Format("2006-01-02 15:04:05"), OK: ok, Message: message}
	s.persistLocked(false) // 每台完成都会调：节流写，避免高频 IO
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
