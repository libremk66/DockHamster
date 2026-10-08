package utiles

import (
	"context"
	"fmt"
	"strings"

	"github.com/libremk66/DockHamster/internal/svc"
)

// compose 管理 label 键（docker/compose v2 写入容器的事实来源）
const (
	labelComposeProject     = "com.docker.compose.project"
	labelComposeService     = "com.docker.compose.service"
	labelComposeConfigFiles = "com.docker.compose.project.config_files"
	labelComposeWorkDir     = "com.docker.compose.project.working_dir"
)

// ComposeMeta 从容器 labels 提取的 compose 项目信息；非 compose 容器 IsManaged 为 false
type ComposeMeta struct {
	IsManaged   bool     `json:"isManaged"`
	Project     string   `json:"project"`
	Service     string   `json:"service"`
	ConfigFiles []string `json:"configFiles"`
	WorkingDir  string   `json:"workingDir"`
}

// ComposeMetaFromLabels 从 labels map 解析 compose 元数据
func ComposeMetaFromLabels(labels map[string]string) ComposeMeta {
	meta := ComposeMeta{IsManaged: false}
	if labels == nil {
		return meta
	}
	project, hasProject := labels[labelComposeProject]
	service, hasService := labels[labelComposeService]
	if !hasProject || project == "" || !hasService || service == "" {
		return meta
	}
	meta.IsManaged = true
	meta.Project = project
	meta.Service = service
	// config_files 以宿主机路径分隔符分隔（linux 为 ":"，windows 为 ";"）
	for _, f := range strings.FieldsFunc(labels[labelComposeConfigFiles], func(r rune) bool {
		return r == ':' || r == ';'
	}) {
		if f != "" {
			meta.ConfigFiles = append(meta.ConfigFiles, f)
		}
	}
	meta.WorkingDir = labels[labelComposeWorkDir]
	return meta
}

// ComposeMetaOfContainer 查询指定容器的 compose 元数据
func ComposeMetaOfContainer(serviceContext *svc.ServiceContext, containerID string) (ComposeMeta, error) {
	inspected, err := serviceContext.DockerClient.ContainerInspect(context.Background(), containerID)
	if err != nil {
		return ComposeMeta{}, fmt.Errorf("inspect container: %w", err)
	}
	if inspected.Config == nil {
		return ComposeMeta{}, nil
	}
	return ComposeMetaFromLabels(inspected.Config.Labels), nil
}

// UpdateRef 返回该 service 的 compose 引用（"project/service"），用于日志与进度展示
func (m ComposeMeta) UpdateRef() string {
	if !m.IsManaged {
		return ""
	}
	return m.Project + "/" + m.Service
}
