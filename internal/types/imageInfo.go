package types

import (
	"github.com/docker/docker/api/types/image"
)

type Image struct {
	image.Summary
	ImageName  string `json:"imageName"`
	ImageTag   string `json:"imageTag"`
	InUsed     bool   `json:"inUsed"`
	SizeFormat string `json:"sizeFormat"`
	// Tags 该镜像 ID 的全部标签引用（多 tag 共享同一镜像时用于提示与一次清除）
	Tags []string `json:"tags"`
	// HasChildren 是否有其他镜像以本镜像为基础镜像（本地构建产物），此类镜像不可删除
	HasChildren bool `json:"hasChildren"`
}
