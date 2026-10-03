package module

import (
	"encoding/json"
	"time"
)

// ── 容器迁移：数据模型 + 搬运抽象 ───────────────────────────────────────
// 设计目标：本期只落地"本地包"（docker save/load），但模型与接口为多种路线预留：
//   - ImageTransportRecord.Transports 是数组：同一镜像可同时有本地包 + registry 记录
//   - ImageTransport 接口可扩展 RegistryTransport，不动调用方与前端

// ManifestSchemaVersion 迁移包格式版本（导入端据此兼容未来格式）
const ManifestSchemaVersion = 1

// ImageTransportRecord 一个镜像的一种搬运记录
type ImageTransportRecord struct {
	Type   string `json:"type"`             // archive（本期） | registry（预留）
	File   string `json:"file,omitempty"`   // archive：包内相对路径 images/xxx.tar.gz
	Ref    string `json:"ref,omitempty"`    // registry：目标引用 nas:5000/snap/xxx:tag
	SHA256 string `json:"sha256,omitempty"` // archive：文件校验和
	Size   int64  `json:"size,omitempty"`   // 字节
}

// ManifestImage 包内镜像清单
type ManifestImage struct {
	ID         string                 `json:"id"`             // sha256:...
	Refs       []string               `json:"refs"`           // 导出时的本地 tag 列表
	Source     string                 `json:"source"`         // registry | local-build | dangling
	Size       int64                  `json:"size"`
	Transports []ImageTransportRecord `json:"transports,omitempty"`
}

// ManifestVolume 容器用到的宿主挂载
type ManifestVolume struct {
	Type     string `json:"type"` // bind | volume
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"readOnly"`
}

// ManifestContainer 包内容器配方（创建配置原样保留，保证精确重建）
type ManifestContainer struct {
	Name       string            `json:"name"`
	ImageRef   string            `json:"imageRef"`
	ImageID    string            `json:"imageId"`
	WasRunning bool              `json:"wasRunning"`
	Create     json.RawMessage   `json:"create"` // dockerBackend.ContainerCreateConfig（可含 env，按需脱敏）
	Volumes    []ManifestVolume  `json:"volumes"`
	Networks   []string          `json:"networks"`
	Ports      []string          `json:"ports"` // 形如 "0.0.0.0:8080->80/tcp"
	Privileged bool              `json:"privileged"`
}

// ManifestGenerator 生成信息
type ManifestGenerator struct {
	App       string    `json:"app"`
	Version   string    `json:"version"`
	Host      string    `json:"host"`
	CreatedAt time.Time `json:"createdAt"`
}

// Manifest 迁移包清单
type Manifest struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Generator     ManifestGenerator   `json:"generator"`
	Images        []ManifestImage     `json:"images"`
	Containers    []ManifestContainer `json:"containers"`
	Options       map[string]any      `json:"options,omitempty"`
}

// ExportTaskInfo 迁移包列表项
type ExportTaskInfo struct {
	File      string `json:"file"`      // /data/exports 下的文件名
	Size      int64  `json:"size"`      // 字节
	CreatedAt int64  `json:"createdAt"` // unix
}

// ImportPlanItem 导入预检的单个容器条目
type ImportPlanItem struct {
	Name          string            `json:"name"`
	ImageRef      string            `json:"imageRef"`
	ImageID       string            `json:"imageId"`
	ImageSource   string            `json:"imageSource"`   // local|package|registry|missing
	NameConflict  bool              `json:"nameConflict"`  // 已存在同名容器
	SuggestedName string            `json:"suggestedName"` // 冲突时的建议名
	PortConflicts []string          `json:"portConflicts"` // 被占用的宿主端口
	MissingMounts []ManifestVolume  `json:"missingMounts"` // 目标机不存在的宿主路径
	MountSuggest  map[string]string `json:"mountSuggest"`  // 源路径 → 建议映射（按同名目录）
	Privileged    bool              `json:"privileged"`
	WasRunning    bool              `json:"wasRunning"`
}

// ImportPlan 导入预检结果
type ImportPlan struct {
	File        string           `json:"file"`
	PackageName string           `json:"packageName"`
	Generator   ManifestGenerator `json:"generator"`
	Items       []ImportPlanItem `json:"items"`
	TotalSize   int64            `json:"totalSize"`
	DiskFree    int64            `json:"diskFree"`
	Warnings    []string         `json:"warnings,omitempty"`
}

// ImportItemOverride 导入执行时对单个容器的覆盖
type ImportItemOverride struct {
	Name      string            `json:"name"`      // 原始名（匹配用）
	Skip      bool              `json:"skip"`      // 跳过此项
	NewName   string            `json:"newName"`   // 改名（空=原名）
	PortMap   map[string]string `json:"portMap"`   // 宿主端口重映射 "8080"→"18080"
	MountMap  map[string]string `json:"mountMap"`  // 宿主路径重映射 /old→/new
	AutoCreateDirs bool         `json:"autoCreateDirs"` // 缺失宿主目录是否自动创建
}
