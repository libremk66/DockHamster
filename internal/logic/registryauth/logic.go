package registryauth

import (
	"context"
	"strings"

	"github.com/libremk66/DockHamster/internal/module"
	"github.com/libremk66/DockHamster/internal/svc"
	"github.com/libremk66/DockHamster/internal/types"
	"github.com/zeromicro/go-zero/core/logx"
)

// RegistryAuthLogic 私有镜像仓库凭据管理（ghcr.io / Docker Hub / 自建 registry）。
// 凭据只用于后端查询/拉取，**API 出口必须脱敏**（密码不回传，只给 hasPassword）。
type RegistryAuthLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewRegistryAuthLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RegistryAuthLogic {
	return &RegistryAuthLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// entryView 脱敏后的条目（密码只给"是否已设置"）
type entryView struct {
	Registry    string `json:"registry"`
	Username    string `json:"username"`
	HasPassword bool   `json:"hasPassword"`
}

func (l *RegistryAuthLogic) GetSettings() (*types.Resp, error) {
	resp := &types.Resp{Code: 200, Msg: "success"}
	views := []entryView{}
	if l.svcCtx.RegistryAuth != nil {
		for _, e := range l.svcCtx.RegistryAuth.Get().Entries {
			views = append(views, entryView{
				Registry:    e.Registry,
				Username:    e.Username,
				HasPassword: e.Password != "",
			})
		}
	}
	resp.Data = map[string]interface{}{"entries": views}
	return resp, nil
}

// SaveSettings 保存凭据：空密码沿用旧值（UI 不回显密码，留空 = 保持不变）。
func (l *RegistryAuthLogic) SaveSettings(req *module.RegistryAuthSettings) (*types.Resp, error) {
	if l.svcCtx.RegistryAuth == nil {
		return &types.Resp{Code: 500, Msg: "凭据存储未初始化", Data: map[string]interface{}{}}, nil
	}
	in := module.PreserveRegistryPasswords(l.svcCtx.RegistryAuth.Get(), *req)
	normalized := module.NormalizeRegistryAuthSettings(in)
	// 校验：registry 与用户名必填，密码不能为空（新条目必须给；老条目已在上面沿用）
	for _, e := range normalized.Entries {
		if strings.TrimSpace(e.Username) == "" {
			return &types.Resp{Code: 400, Msg: e.Registry + "：用户名不能为空", Data: map[string]interface{}{}}, nil
		}
		if e.Password == "" {
			return &types.Resp{Code: 400, Msg: e.Registry + "：密码不能为空（编辑时留空表示保持不变）", Data: map[string]interface{}{}}, nil
		}
	}
	if err := l.svcCtx.RegistryAuth.Save(normalized); err != nil {
		return &types.Resp{Code: 500, Msg: "保存失败：" + err.Error(), Data: map[string]interface{}{}}, nil
	}
	logx.Infof("私有仓库凭据已更新：%d 条", len(normalized.Entries))
	return l.GetSettings()
}
