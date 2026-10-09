package module

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/logx"
)

// taskHistoryCap 任务历史保留条数上限（超出丢弃最旧的）
const taskHistoryCap = 500

// TaskHistoryEntry 一条任务历史（任务级：容器更新 / 拉取 / 面板自更新 / 迁移等）。
// 自动更新与整组更新是"批次"语义，走 AutoUpdateRunResult 记录，不在这里重复记（见 RecordTaskDone）。
type TaskHistoryEntry struct {
	ID          string  `json:"id"`
	Time        string  `json:"time"` // 完成时间（2006-01-02 15:04:05）
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`   // update | pull | selfupdate | migrate
	Source      string  `json:"source"` // container | accelerator | selfupdate | migrate | rollback
	Failed      bool    `json:"failed"`
	Message     string  `json:"message"`
	DetailMsg   string  `json:"detailMsg,omitempty"`
	StartedAt   string  `json:"startedAt,omitempty"`
	DurationSec float64 `json:"durationSec"`
}

// TaskHistoryStore 任务历史的持久化存储（内存切片 + 原子写盘）。
// 面板自更新/版本升级重建容器后记录不丢。
type TaskHistoryStore struct {
	mu      sync.Mutex
	entries []TaskHistoryEntry
	path    string
}

func taskHistoryPath() string {
	if p := os.Getenv("TaskHistoryFile"); p != "" {
		return p
	}
	return "/data/config/task-history.json"
}

func NewTaskHistoryStore() *TaskHistoryStore {
	s := &TaskHistoryStore{
		entries: []TaskHistoryEntry{},
		path:    taskHistoryPath(),
	}
	s.load()
	return s
}

// Add 追加一条历史（最新的在前）；超过上限丢弃最旧的。ID 为空时自动生成。
func (s *TaskHistoryStore) Add(e TaskHistoryEntry) {
	if e.ID == "" {
		e.ID = NewHistoryID("task")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append([]TaskHistoryEntry{e}, s.entries...)
	if len(s.entries) > taskHistoryCap {
		s.entries = s.entries[:taskHistoryCap]
	}
	s.persistLocked()
}

// List 返回全部历史（副本）
func (s *TaskHistoryStore) List() []TaskHistoryEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]TaskHistoryEntry{}, s.entries...)
}

// Delete 按 ID 删除，返回实际删除条数
func (s *TaskHistoryStore) Delete(ids []string) int {
	if len(ids) == 0 {
		return 0
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := make([]TaskHistoryEntry, 0, len(s.entries))
	for _, e := range s.entries {
		if want[e.ID] {
			continue
		}
		kept = append(kept, e)
	}
	n := len(s.entries) - len(kept)
	if n > 0 {
		s.entries = kept
		s.persistLocked()
	}
	return n
}

func (s *TaskHistoryStore) load() {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return // 首次运行
	}
	var entries []TaskHistoryEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		logx.Errorf("解析任务历史失败(忽略): %v", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = entries
	if len(s.entries) > taskHistoryCap {
		s.entries = s.entries[:taskHistoryCap]
	}
}

// persistLocked 原子写盘（临时文件 + rename）；调用方持锁
func (s *TaskHistoryStore) persistLocked() {
	if s.path == "" {
		return
	}
	b, err := json.Marshal(s.entries)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		logx.Errorf("创建任务历史目录失败: %v", err)
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		logx.Errorf("写任务历史失败: %v", err)
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		logx.Errorf("替换任务历史失败: %v", err)
	}
}

// NewHistoryID 生成历史记录 ID（运行记录与任务历史共用）
func NewHistoryID(prefix string) string {
	return prefix + "-" + uuid.New().String()
}
