package types

import (
	docker "github.com/docker/docker/api/types"
)

type Container struct {
	docker.Container
	Update bool `json:"Update"`
	// Uncheckable 镜像无有效标签（悬空/本地构建），更新检测无法进行（前端给"无法检测"提示）
	Uncheckable bool `json:"Uncheckable"`
}
